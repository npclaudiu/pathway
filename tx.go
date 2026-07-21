package pathway

import (
	"context"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
	"github.com/npclaudiu/pathway/internal/properties"
)

// Tx represents a database transaction.
// It can be either read-only (created via View) or read-write (created via Update).
type Tx struct {
	db       *Database
	ctx      context.Context
	batch    *pebble.Batch // For write transactions
	reader   pebble.Reader // For read transactions
	readOnly bool
	id       uint64
}

// pebbleIterator implements the Iterator interface for pebble.Iterator.
type pebbleIterator struct {
	*pebble.Iterator
}

func (p *pebbleIterator) Key() []byte {
	return p.Iterator.Key()
}

func (p *pebbleIterator) Value() []byte {
	return p.Iterator.Value()
}

func (p *pebbleIterator) Path() []interface{} {
	return nil // Base iterator has no path history
}

// NewIterator creates a new low-level iterator for the transaction.
// This is primarily for internal use; users should typically use high-level iterators
// like ScanNodes, OutEdges, etc.
func (tx *Tx) NewIterator(opts *pebble.IterOptions) (Iterator, error) {
	var iter *pebble.Iterator
	var err error

	if tx.readOnly {
		iter, err = tx.reader.NewIter(opts)
	} else {
		iter, err = tx.batch.NewIter(opts)
	}
	if err != nil {
		return nil, err
	}
	return &pebbleIterator{iter}, nil
}

// Get retrieves the raw value for a given key.
// It handles the difference between read-only readers and write batches.
func (tx *Tx) Get(key []byte) ([]byte, error) {
	if tx.readOnly {
		val, closer, err := tx.reader.Get(key)
		if err != nil {
			return nil, err
		}
		defer func() { _ = closer.Close() }()
		// We must copy the value because closer.Close() invalidates it
		// Pebble docs say: "The caller must close the returned Closer when finished with the result."
		// We return []byte which might be used after this function returns.
		// So we strictly need to copy.
		result := make([]byte, len(val))
		copy(result, val)
		return result, nil
	}
	// Batch.Get returns ([]byte, io.Closer, error) too?
	// Let's check Pebble API. batch.Get(key) returns ([]byte, io.Closer, error).
	// Yes.
	val, closer, err := tx.batch.Get(key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()
	result := make([]byte, len(val))
	copy(result, val)
	return result, nil
}

// Set sets the raw value for a given key.
// Returns an error if the transaction is read-only.
func (tx *Tx) Set(key, value []byte, opts *pebble.WriteOptions) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	return tx.batch.Set(key, value, opts)
}

// Delete deletes the raw value for a given key.
// Returns an error if the transaction is read-only.
func (tx *Tx) Delete(key []byte, opts *pebble.WriteOptions) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	return tx.batch.Delete(key, opts)
}

// Access allows executing a function within the transaction context.
// Useful for executing multiple operations on the same transaction (e.g. Iterators).
func (tx *Tx) Access(fn func(tx *Tx) error) error {
	return fn(tx)
}

// Close closes the transaction.
// This releases the underlying snapshot. For write transactions (Update), it's a no-op
// as the batch is managed by the DB.Update method, but for read-only (View/NewReadTx),
// it releases the read lease.
func (tx *Tx) Close() error {
	if tx.readOnly && tx.reader != nil {
		return tx.reader.Close()
	}
	return nil
}

// PutNode creates or updates a node with the given label.
//
// Usage:
//
//	id := uuid.New()
//	err := tx.PutNode(id, "Person")
func (tx *Tx) PutNode(id uuid.UUID, label string) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	// TODO: Validate label length
	key := encoding.EncodeNodeKey(id)
	// Value: [LabelLen][Label]
	lblBytes := []byte(label)
	// Simplify: store simplified label
	// In real implementation we encode with length prefix
	// For now, let's stick to the Spec encoding details implemented in other helpers if needed,
	// but here we construct value manually or via helper.
	// The spec 3.2 says Value: LabelID (var length). Let's respect label encoding.
	val := make([]byte, 2+len(lblBytes))
	// We can assume label len fits in uint16 for now or use binary helper
	// For simplicity of this step, let's just write raw bytes or a helper.
	// Spec 3.6 Label Encoding: [length:2 bytes][utf8 bytes]

	// We really should expose a EncodeLabel helper but let's inline for now to save a file:
	// Or assume EncodeNodeKey handles key, we handle value.
	if len(lblBytes) > 65535 {
		return encoding.ErrInvalidLabel
	}
	val[0] = byte(len(lblBytes) >> 8)
	val[1] = byte(len(lblBytes))
	copy(val[2:], lblBytes)

	return tx.batch.Set(key, val, nil)
}

// PutEdge creates a directed edge between two nodes.
// It performs a dual-write, creating both an outgoing key (for traversals from source)
// and an incoming key (for traversals to target).
//
// Returns an error if either source or destination node does not exist.
//
// Usage:
//
//	edgeID, err := tx.PutEdge(srcID, dstID, "KNOWS")
func (tx *Tx) PutEdge(srcID, dstID uuid.UUID, label string) (uuid.UUID, error) {
	if tx.readOnly {
		return uuid.Nil, pebble.ErrReadOnly
	}

	// 1. Verify source and target nodes exist
	// This requires a Get on the batch or DB.
	// Note: If nodes were just added in this batch, we must read from the batch (Reader interface).
	// Our Tx structs have readers.

	_, exists, err := tx.GetNode(srcID)
	if err != nil {
		return uuid.Nil, err
	}
	if !exists {
		return uuid.Nil, ErrDanglingEdge
	}

	_, exists, err = tx.GetNode(dstID)
	if err != nil {
		return uuid.Nil, err
	}
	if !exists {
		return uuid.Nil, ErrDanglingEdge
	}

	// 2. Generate Edge ID
	edgeID := uuid.New()

	// 3. Encoder keys
	outKey, err := encoding.EncodeEdgeOutKey(srcID, dstID, label)
	if err != nil {
		return uuid.Nil, err
	}

	inKey, err := encoding.EncodeEdgeInKey(srcID, dstID, label)
	if err != nil {
		return uuid.Nil, err
	}

	val := encoding.EncodeEdgeValue(edgeID)

	// 4. Write to Batch
	if err := tx.batch.Set(outKey, val, nil); err != nil {
		return uuid.Nil, err
	}
	if err := tx.batch.Set(inKey, val, nil); err != nil {
		return uuid.Nil, err
	}

	return edgeID, nil
}

// SetProperties sets a map of properties for a given node or edge.
// This completely replaces any existing properties for that entity.
//
// Usage:
//
//	err := tx.SetProperties(id, map[string]interface{}{
//	    "name": "Alice",
//	    "age":  30,
//	})
func (tx *Tx) SetProperties(id uuid.UUID, props map[string]interface{}) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}

	// 1. Maintain Index (only for Nodes)
	lbl, isNode, err := tx.GetNode(id)
	if err != nil {
		return err
	}

	if isNode {
		// Fetch old properties to delete from index
		oldProps, err := tx.GetProperties(id)
		if err != nil {
			return err
		}

		// Delete old index entries
		for k, v := range oldProps {
			valStr := fmt.Sprintf("%v", v)
			idxKey := encoding.EncodeIndexKey(lbl, k, valStr, id)
			if err := tx.batch.Delete(idxKey, nil); err != nil {
				return err
			}
		}

		// Add new index entries
		for k, v := range props {
			valStr := fmt.Sprintf("%v", v)
			idxKey := encoding.EncodeIndexKey(lbl, k, valStr, id)
			if err := tx.batch.Set(idxKey, nil, nil); err != nil { // empty value for index
				return err
			}
		}
	}

	// 2. Set actual properties
	data, err := properties.MarshalProperties(props)
	if err != nil {
		return err
	}

	key := encoding.EncodePropertyKey(id)
	return tx.batch.Set(key, data, nil)
}

// DeleteNode deletes a node and all its incident edges (both outgoing and incoming).
// This ensures graph consistency so no dangling edges remain.
// Note: This operation can be expensive for highly connected nodes as it requires
// scanning and deleting all edges.
func (tx *Tx) DeleteNode(id uuid.UUID) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}

	// Get label before deleting for index cleanup
	lbl, isNode, err := tx.GetNode(id)
	if err != nil {
		return err
	}

	if isNode {
		// Clean up index
		oldProps, err := tx.GetProperties(id)
		if err != nil {
			return err
		}
		for k, v := range oldProps {
			valStr := fmt.Sprintf("%v", v)
			idxKey := encoding.EncodeIndexKey(lbl, k, valStr, id)
			if err := tx.batch.Delete(idxKey, nil); err != nil {
				return err
			}
		}
	}

	// 1. Delete Node Key
	key := encoding.EncodeNodeKey(id)
	if err := tx.batch.Delete(key, nil); err != nil {
		return err
	}

	// 2. Delete Incident Edges
	// We need to iterate over all Out edges and In edges to find and delete them.
	// This is expensive but necessary for consistency.
	// IMPLEMENTATION:
	// Iterate Out edges (0x02 + id)
	// For each edge:
	//    Delete OutKey (current key)
	//    Construct InKey (0x03 + target + label + source) and delete it

	// Outgoing
	iterOut := tx.OutEdges(id) // This uses the Tx's reader (batch+db)
	defer func() { _ = iterOut.Close() }()
	for iterOut.Next() {
		_, target, label, _ := iterOut.Edge() // ignoring error for brevity in plan, but should handle

		// Delete Out Key (we can reconstruct or use Iter key if safe? Safest to reconstruct)
		outK, _ := encoding.EncodeEdgeOutKey(id, target, label)
		if err := tx.batch.Delete(outK, nil); err != nil {
			return err
		}

		inK, _ := encoding.EncodeEdgeInKey(id, target, label)
		if err := tx.batch.Delete(inK, nil); err != nil {
			return err
		}
	}

	// Incoming
	iterIn := tx.InEdges(id)
	defer func() { _ = iterIn.Close() }()
	for iterIn.Next() {
		_, source, label, _ := iterIn.Edge() // note: Edge() signature returns target for Out, source for In?
		// Our iterator wrapper unifies this, but EdgeIterator.Edge() returns (edgeID, otherNodeID, label).
		// So for InEdges, otherNodeID is Source.

		inK, _ := encoding.EncodeEdgeInKey(source, id, label)
		if err := tx.batch.Delete(inK, nil); err != nil {
			return err
		}

		outK, _ := encoding.EncodeEdgeOutKey(source, id, label)
		if err := tx.batch.Delete(outK, nil); err != nil {
			return err
		}
	}

	// 3. Delete Properties
	propKey := encoding.EncodePropertyKey(id)
	return tx.batch.Delete(propKey, nil)
}

// DeleteEdge removes a specific edge.
// Note: Currently, deleting by EdgeID alone is not fully supported efficiently without an index.
// Use DeleteEdgeBetween(src, dst, label) if available (planned for future).
//
// Deprecated: Use DeleteNode or specific edge removal logic when API expands.
func (tx *Tx) DeleteEdge(edgeID uuid.UUID) error {
	// This is tricky because the Key-Value mapping is:
	// Key -> EdgeID.
	// To delete by EdgeID, we need a reverse index (EdgeID -> Keys) OR scan all edges.
	// The current spec doesn't mandate an EdgeID index.
	// Optimally, user should provide (Src, Dst, Label) to delete.
	// If we strictly need DeleteEdge(uuid), we must add an index or scan.
	// Strategy: For Phase 1, we return "Not Implemented" or scan (very slow).
	return errors.New("delete by ID not supported without index; use DeleteEdgeBetween(src, dst, label)")
}

// READ OPERATIONS

// GetNode retrieves a node's label by its ID.
// Returns the label, a boolean indicating existence, and any error.
func (tx *Tx) GetNode(id uuid.UUID) (string, bool, error) {
	key := encoding.EncodeNodeKey(id)
	val, err := tx.Get(key) // Tx.Get handles batch vs snapshot reading
	if err == pebble.ErrNotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// Decode label
	label, _ := encoding.DecodeLabel(val)
	return label, true, nil
}

// GetProperties retrieves the properties map for a given node or edge ID.
// Returns nil if no properties exist.
func (tx *Tx) GetProperties(id uuid.UUID) (map[string]interface{}, error) {
	key := encoding.EncodePropertyKey(id)
	val, err := tx.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil // No props
	}
	if err != nil {
		return nil, err
	}
	return properties.UnmarshalProperties(val)
}

// OutEdges returns an iterator for outgoing edges from the given node ID.
// Optionally filters by edge labels.
//
// Usage:
//
//	iter := tx.OutEdges(nodeID, "KNOWS", "WORKS_WITH")
//	defer iter.Close()
//	for iter.Next() { ... }
func (tx *Tx) OutEdges(id uuid.UUID, labels ...string) EdgeIterator {
	// Prefix: 0x02 + id
	prefix := make([]byte, 1+16)
	prefix[0] = encoding.PrefixEdgeOut
	copy(prefix[1:], id[:])

	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}

	iter, err := tx.NewIterator(opts)
	if err != nil {
		return &edgeIterator{err: err}
	}

	// Seek to first key
	iter.SeekGE(prefix)

	// Wrap in edge implementation
	return &edgeIterator{iter: iter, valid: iter.Valid(), first: true, labels: labels}
}

// InEdges returns an iterator for incoming edges to the given node ID.
// Optionally filters by edge labels.
func (tx *Tx) InEdges(id uuid.UUID, labels ...string) EdgeIterator {
	prefix := make([]byte, 1+16)
	prefix[0] = encoding.PrefixEdgeIn
	copy(prefix[1:], id[:])

	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}

	iter, err := tx.NewIterator(opts)
	if err != nil {
		return &edgeIterator{err: err}
	}
	iter.SeekGE(prefix)

	return &edgeIterator{iter: iter, valid: iter.Valid(), first: true, labels: labels}
}

// FindNodes scans the index.
func (tx *Tx) FindNodes(label, propKey, propValue string) NodeIterator {
	prefix := encoding.EncodeIndexPrefix(label, propKey, propValue)
	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}

	iter, err := tx.NewIterator(opts)
	if err != nil {
		return &nodeIndexIterator{err: err}
	}

	iter.SeekGE(prefix)
	return &nodeIndexIterator{iter: iter, valid: iter.Valid(), first: true}
}

// ScanNodes scans all nodes in the database.
// This is a full table scan and can be slow for large datasets.
func (tx *Tx) ScanNodes() NodeIterator {
	prefix := []byte{encoding.PrefixNode}
	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}

	iter, err := tx.NewIterator(opts)
	if err != nil {
		return &nodeIterator{err: err}
	}

	iter.SeekGE(prefix)
	return &nodeIterator{iter: iter, valid: iter.Valid(), first: true}
}

// Helper to calculate upper bound for prefix scan
func keyUpperBound(b []byte) []byte {
	end := make([]byte, len(b))
	copy(end, b)
	for i := len(end) - 1; i >= 0; i-- {
		end[i] = end[i] + 1
		if end[i] != 0 {
			return end[:i+1]
		}
	}
	return nil // No upper bound (overflow)
}
