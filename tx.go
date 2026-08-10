package pathway

import (
	"bytes"
	"context"
	"errors"
	"slices"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
	"github.com/npclaudiu/pathway/internal/properties"
)

// Tx represents a database transaction.
// It can be either read-only (created via View) or read-write (created via Update).
type Tx struct {
	db                *Database
	ctx               context.Context
	batch             *pebble.Batch // For write transactions
	reader            pebble.Reader // For read transactions
	readOnly          bool
	closed            bool
	id                uint64
	externalIDsLocked bool
}

// pebbleIterator adapts Pebble to the package-private rawIterator cursor.
type pebbleIterator struct {
	*pebble.Iterator
	ctx context.Context
	err error
}

func (p *pebbleIterator) Next() bool {
	if p.contextCanceled() {
		return false
	}
	return p.Iterator.Next()
}

func (p *pebbleIterator) SeekGE(key []byte) bool {
	if p.contextCanceled() {
		return false
	}
	return p.Iterator.SeekGE(key)
}

func (p *pebbleIterator) Valid() bool {
	return !p.contextCanceled() && p.Iterator.Valid()
}

func (p *pebbleIterator) Error() error {
	p.contextCanceled()
	return errors.Join(p.err, p.Iterator.Error())
}

func (p *pebbleIterator) contextCanceled() bool {
	if p.err == nil && p.ctx != nil {
		p.err = p.ctx.Err()
	}
	return p.err != nil
}

func (p *pebbleIterator) Key() []byte {
	return p.Iterator.Key()
}

func (p *pebbleIterator) Value() []byte {
	return p.Iterator.Value()
}

// newIterator creates a low-level storage cursor. Raw keys, values, seeking,
// and positioning remain private to storage adapters; callers use ScanNodes,
// FindNodes, OutEdges, or InEdges instead.
func (tx *Tx) newIterator(opts *pebble.IterOptions) (rawIterator, error) {
	if tx.closed {
		return nil, pebble.ErrClosed
	}
	if err := tx.contextErr(); err != nil {
		return nil, err
	}
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
	if err := tx.contextErr(); err != nil {
		return nil, errors.Join(err, iter.Close())
	}
	return &pebbleIterator{Iterator: iter, ctx: tx.ctx}, nil
}

// Get retrieves the raw value for a given key.
// It handles the difference between read-only readers and write batches.
func (tx *Tx) Get(key []byte) ([]byte, error) {
	if err := tx.contextErr(); err != nil {
		return nil, err
	}
	reader := tx.reader
	if !tx.readOnly {
		reader = tx.batch
	}
	val, closer, err := reader.Get(key)
	if err != nil {
		if contextErr := tx.contextErr(); contextErr != nil {
			return nil, contextErr
		}
		return nil, err
	}
	defer func() { _ = closer.Close() }()
	if err := tx.contextErr(); err != nil {
		return nil, err
	}
	// Pebble invalidates val when closer closes, so callers receive an owned
	// copy that remains valid after this read finishes.
	result := append([]byte(nil), val...)
	if err := tx.contextErr(); err != nil {
		return nil, err
	}
	return result, nil
}

func (tx *Tx) contextErr() error {
	if tx.ctx == nil {
		return nil
	}
	return tx.ctx.Err()
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
		if tx.closed {
			return nil
		}
		tx.closed = true
		return tx.reader.Close()
	}
	return nil
}

// PutNode creates or updates a node with the given label. Changing an existing
// node's label migrates its configured property-index entries.
//
// Usage:
//
//	id := uuid.New()
//	err := tx.PutNode(id, "Person")
func (tx *Tx) PutNode(id uuid.UUID, label string) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	lblBytes := []byte(label)
	if len(lblBytes) > 65535 {
		return encoding.ErrInvalidLabel
	}

	oldLabel, exists, err := tx.GetNode(id)
	if err != nil {
		return err
	}
	if exists && oldLabel != label {
		oldIndexes := tx.indexedProperties(oldLabel)
		newIndexes := tx.indexedProperties(label)
		if len(oldIndexes) != 0 || len(newIndexes) != 0 {
			props, err := tx.GetProperties(id)
			if err != nil {
				return err
			}
			for propKey, propValue := range props {
				if _, indexed := oldIndexes[propKey]; indexed {
					oldIndexKey, err := encoding.EncodeIndexKey(oldLabel, propKey, propValue, id)
					if err != nil {
						return err
					}
					if err := tx.batch.Delete(oldIndexKey, nil); err != nil {
						return err
					}
				}
				if _, indexed := newIndexes[propKey]; indexed {
					newIndexKey, err := encoding.EncodeIndexKey(label, propKey, propValue, id)
					if err != nil {
						return err
					}
					if err := tx.batch.Set(newIndexKey, nil, nil); err != nil {
						return err
					}
				}
			}
		}
	}

	key := encoding.EncodeNodeKey(id)
	val := make([]byte, 2+len(lblBytes))
	val[0] = byte(len(lblBytes) >> 8)
	val[1] = byte(len(lblBytes))
	copy(val[2:], lblBytes)

	return tx.batch.Set(key, val, nil)
}

// PutEdge creates a directed edge between two existing nodes. Endpoint checks
// test key existence without copying or decoding node labels. Pathway uses
// multigraph semantics: every call creates a distinct edge, even when the
// endpoints and label match an existing edge.
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

	exists, err := tx.nodeExists(srcID)
	if err != nil {
		return uuid.Nil, err
	}
	if !exists {
		return uuid.Nil, ErrDanglingEdge
	}

	exists, err = tx.nodeExists(dstID)
	if err != nil {
		return uuid.Nil, err
	}
	if !exists {
		return uuid.Nil, ErrDanglingEdge
	}
	return tx.putEdge(srcID, dstID, label)
}

// putEdge stages an edge after its callers have validated both endpoints. Keep
// this package-private so the public API cannot bypass dangling-edge checks.
func (tx *Tx) putEdge(srcID, dstID uuid.UUID, label string) (uuid.UUID, error) {
	edgeID := uuid.New()

	outKey, err := encoding.EncodeEdgeOutKey(srcID, dstID, edgeID, label)
	if err != nil {
		return uuid.Nil, err
	}

	inKey, err := encoding.EncodeEdgeInKey(srcID, dstID, edgeID, label)
	if err != nil {
		return uuid.Nil, err
	}

	val := encoding.EncodeEdgeValue(edgeID)
	edgeRecord, err := encoding.EncodeEdgeRecord(srcID, dstID, label)
	if err != nil {
		return uuid.Nil, err
	}

	if err := tx.batch.Set(outKey, val, nil); err != nil {
		return uuid.Nil, err
	}
	if err := tx.batch.Set(inKey, val, nil); err != nil {
		return uuid.Nil, err
	}
	if err := tx.batch.Set(encoding.EncodeEdgeIDKey(edgeID), edgeRecord, nil); err != nil {
		return uuid.Nil, err
	}

	return edgeID, nil
}

// SetProperties sets a map of properties for an existing node or edge. This
// completely replaces any existing properties for that entity. It returns
// ErrEntityNotFound if id identifies neither kind of entity.
// Signed integers are stored and returned as int64, unsigned integers as
// uint64, floats as float64, and byte slices remain byte slices. Lists and
// nested maps recursively support the same property value types.
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

	// Marshal first, then index the canonical representation returned by the
	// property codec. This keeps numeric index entries stable across updates.
	data, err := properties.MarshalProperties(props)
	if err != nil {
		return err
	}
	canonicalProps, err := properties.UnmarshalProperties(data)
	if err != nil {
		return err
	}

	// 1. Maintain Index (only for Nodes)
	lbl, isNode, err := tx.GetNode(id)
	if err != nil {
		return err
	}
	if !isNode {
		if _, err := tx.Get(encoding.EncodeEdgeIDKey(id)); errors.Is(err, pebble.ErrNotFound) {
			return ErrEntityNotFound
		} else if err != nil {
			return err
		}
	}

	indexedProperties := tx.indexedProperties(lbl)
	if isNode && len(indexedProperties) != 0 {
		oldProps, err := tx.GetProperties(id)
		if err != nil {
			return err
		}

		for property := range indexedProperties {
			oldValue, hadOldValue := oldProps[property]
			newValue, hasNewValue := canonicalProps[property]

			var oldIndexKey, newIndexKey []byte
			if hadOldValue {
				oldIndexKey, err = encoding.EncodeIndexKey(lbl, property, oldValue, id)
				if err != nil {
					return err
				}
			}
			if hasNewValue {
				newIndexKey, err = encoding.EncodeIndexKey(lbl, property, newValue, id)
				if err != nil {
					return err
				}
			}
			if hadOldValue && hasNewValue && bytes.Equal(oldIndexKey, newIndexKey) {
				continue
			}
			if hadOldValue {
				if err := tx.batch.Delete(oldIndexKey, nil); err != nil {
					return err
				}
			}
			if hasNewValue {
				if err := tx.batch.Set(newIndexKey, nil, nil); err != nil {
					return err
				}
			}
		}
	}

	// 2. Set actual properties
	key := encoding.EncodePropertyKey(id)
	return tx.batch.Set(key, data, nil)
}

// DeleteNode deletes a node and all its incident edges (both outgoing and
// incoming), including each edge's reverse-index entry and properties. Its cost
// is linear in the node's degree.
func (tx *Tx) DeleteNode(id uuid.UUID) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	tx.lockExternalIDs()

	// Get label before deleting for index cleanup
	lbl, isNode, err := tx.GetNode(id)
	if err != nil {
		return err
	}

	if isNode {
		indexedProperties := tx.indexedProperties(lbl)
		if len(indexedProperties) != 0 {
			oldProps, err := tx.GetProperties(id)
			if err != nil {
				return err
			}
			for k := range indexedProperties {
				v, exists := oldProps[k]
				if !exists {
					continue
				}
				idxKey, err := encoding.EncodeIndexKey(lbl, k, v, id)
				if err != nil {
					return err
				}
				if err := tx.batch.Delete(idxKey, nil); err != nil {
					return err
				}
			}
		}
		if err := tx.deleteExternalIDs(id); err != nil {
			return err
		}
	}

	// Collect before mutating the adjacency ranges. Self-loops appear in both
	// scans, so the map also prevents a duplicate DeleteEdge call.
	edgeIDs := make(map[uuid.UUID]struct{})
	collect := func(iter EdgeIterator) (resultErr error) {
		defer func() {
			resultErr = errors.Join(resultErr, iter.Close())
		}()
		for iter.Next() {
			edgeID, _, _, err := iter.Edge()
			if err != nil {
				return err
			}
			edgeIDs[edgeID] = struct{}{}
		}
		return iter.Error()
	}
	if err := collect(tx.OutEdges(id)); err != nil {
		return err
	}
	if err := collect(tx.InEdges(id)); err != nil {
		return err
	}

	for edgeID := range edgeIDs {
		if err := tx.DeleteEdge(edgeID); err != nil {
			return err
		}
	}

	if err := tx.batch.Delete(encoding.EncodeNodeKey(id), nil); err != nil {
		return err
	}
	return tx.batch.Delete(encoding.EncodePropertyKey(id), nil)
}

// DeleteEdge removes a specific edge, including both adjacency records, its
// reverse-index record, and its properties.
func (tx *Tx) DeleteEdge(edgeID uuid.UUID) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}

	reverseKey := encoding.EncodeEdgeIDKey(edgeID)
	record, err := tx.Get(reverseKey)
	if err == pebble.ErrNotFound {
		return ErrEdgeNotFound
	}
	if err != nil {
		return err
	}

	srcID, dstID, label, err := encoding.DecodeEdgeRecord(record)
	if err != nil {
		return corruptRecord("edge reverse-index value", err)
	}
	outKey, err := encoding.EncodeEdgeOutKey(srcID, dstID, edgeID, label)
	if err != nil {
		return err
	}
	inKey, err := encoding.EncodeEdgeInKey(srcID, dstID, edgeID, label)
	if err != nil {
		return err
	}

	for _, key := range [][]byte{
		outKey,
		inKey,
		reverseKey,
		encoding.EncodePropertyKey(edgeID),
	} {
		if err := tx.batch.Delete(key, nil); err != nil {
			return err
		}
	}
	return nil
}

// READ OPERATIONS

// nodeExists checks only for a node key. It deliberately avoids Tx.Get and
// GetNode because endpoint validation does not need to copy or decode labels.
// Reading through the indexed batch preserves read-your-writes for nodes staged
// earlier in the same update.
func (tx *Tx) nodeExists(id uuid.UUID) (bool, error) {
	if err := tx.contextErr(); err != nil {
		return false, err
	}
	reader := tx.reader
	if !tx.readOnly {
		reader = tx.batch
	}
	_, closer, err := reader.Get(encoding.EncodeNodeKey(id))
	if contextErr := tx.contextErr(); contextErr != nil {
		if closer != nil {
			contextErr = errors.Join(contextErr, closer.Close())
		}
		return false, contextErr
	}
	if errors.Is(err, pebble.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := tx.contextErr(); err != nil {
		return false, errors.Join(err, closer.Close())
	}
	if err := closer.Close(); err != nil {
		return false, err
	}
	return true, nil
}

// GetNode retrieves a node's label by its ID.
// Returns the label, a boolean indicating existence, and any error.
func (tx *Tx) GetNode(id uuid.UUID) (string, bool, error) {
	key := encoding.EncodeNodeKey(id)
	val, err := tx.Get(key) // Tx.Get handles batch vs snapshot reading
	if errors.Is(err, pebble.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	label, err := encoding.DecodeNodeValue(val)
	if err != nil {
		return "", false, corruptRecord("node value", err)
	}
	return label, true, nil
}

// GetProperties retrieves the properties map for a given node or edge ID.
// Signed integers are returned as int64, unsigned integers as uint64, and
// floats as float64. Returns nil if no properties exist.
func (tx *Tx) GetProperties(id uuid.UUID) (map[string]interface{}, error) {
	key := encoding.EncodePropertyKey(id)
	val, err := tx.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil // No props
	}
	if err != nil {
		return nil, err
	}
	props, err := properties.UnmarshalProperties(val)
	if err != nil {
		return nil, corruptRecord("property value", err)
	}
	if err := tx.contextErr(); err != nil {
		return nil, err
	}
	return props, nil
}

// OutEdges returns outgoing edges from id. When labels are provided, each
// unique label is read through an exact Pebble range. Results use adjacency-key
// order, independently of the order in which labels are supplied.
//
// Usage:
//
//	iter := tx.OutEdges(nodeID, "KNOWS", "WORKS_WITH")
//	defer iter.Close()
//	for iter.Next() { ... }
func (tx *Tx) OutEdges(id uuid.UUID, labels ...string) EdgeIterator {
	basePrefix := make([]byte, 1+16)
	basePrefix[0] = encoding.PrefixEdgeOut
	copy(basePrefix[1:], id[:])
	return tx.edgesByLabel(basePrefix, labels, func(label string) ([]byte, error) {
		return encoding.EncodeEdgeOutPrefix(id, label)
	})
}

// InEdges returns incoming edges to id. When labels are provided, each unique
// label is read through an exact Pebble range. Results use adjacency-key order,
// independently of the order in which labels are supplied.
func (tx *Tx) InEdges(id uuid.UUID, labels ...string) EdgeIterator {
	basePrefix := make([]byte, 1+16)
	basePrefix[0] = encoding.PrefixEdgeIn
	copy(basePrefix[1:], id[:])
	return tx.edgesByLabel(basePrefix, labels, func(label string) ([]byte, error) {
		return encoding.EncodeEdgeInPrefix(id, label)
	})
}

func (tx *Tx) edgesByLabel(basePrefix []byte, labels []string, encodePrefix func(string) ([]byte, error)) EdgeIterator {
	if len(labels) == 0 {
		return tx.edgeRange(basePrefix)
	}

	prefixes := make([][]byte, 0, len(labels))
	for _, label := range labels {
		prefix, err := encodePrefix(label)
		if err != nil {
			return &edgeIterator{iter: newErrorIterator(err), err: err}
		}
		prefixes = append(prefixes, prefix)
	}
	slices.SortFunc(prefixes, bytes.Compare)
	unique := prefixes[:0]
	for _, prefix := range prefixes {
		if len(unique) == 0 || !bytes.Equal(unique[len(unique)-1], prefix) {
			unique = append(unique, prefix)
		}
	}
	if len(unique) == 1 {
		return tx.edgeRange(unique[0])
	}
	return newMultiEdgeIterator(tx, unique)
}

func (tx *Tx) edgeRange(prefix []byte) EdgeIterator {
	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}
	iter, err := tx.newIterator(opts)
	if err != nil {
		return &edgeIterator{iter: newErrorIterator(err), err: err}
	}
	iter.SeekGE(prefix)
	return &edgeIterator{iter: iter, valid: iter.Valid(), first: true}
}

// FindNodes performs an exact typed lookup in a node-property index configured
// through Options.Indexes. An unindexed label/property pair returns no results.
// String, numeric, and boolean values are distinct; value prefixes do not
// match. The iterator reports an encoding error if label or propKey exceeds
// 65,535 bytes.
func (tx *Tx) FindNodes(label, propKey string, propValue interface{}) NodeIterator {
	prefix, err := encoding.EncodeIndexPrefix(label, propKey, propValue)
	if err != nil {
		return &nodeIndexIterator{iter: newErrorIterator(err), err: err}
	}
	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}

	iter, err := tx.newIterator(opts)
	if err != nil {
		return &nodeIndexIterator{iter: newErrorIterator(err), err: err}
	}

	iter.SeekGE(prefix)
	return &nodeIndexIterator{
		iter:   iter,
		prefix: prefix,
		label:  label,
		valid:  iter.Valid(),
		first:  true,
	}
}

// ScanNodes scans all nodes in the database.
// This is a full table scan and can be slow for large datasets.
func (tx *Tx) ScanNodes() NodeIterator {
	prefix := []byte{encoding.PrefixNode}
	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	}

	iter, err := tx.newIterator(opts)
	if err != nil {
		return &nodeIterator{iter: newErrorIterator(err), err: err}
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
