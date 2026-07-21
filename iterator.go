package pathway

import (
	"errors"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

// Iterator is the generic interface for iterating over key-value pairs.
// It abstracts the underlying storage iterator.
// Users typically interact with specific interfaces like NodeIterator or EdgeIterator.
type Iterator interface {
	// Next advances the iterator to the next element. Returns false if exhausted/error.
	Next() bool
	// SeekGE moves to the first key greater than or equal to the given key.
	SeekGE(key []byte) bool
	// Key returns the current key.
	Key() []byte
	// Value returns the current value.
	Value() []byte
	// Valid returns true if the iterator is positioned at a valid element.
	Valid() bool
	// Close releases resources.
	Close() error
	// Error returns any accumulated error.
	Error() error

	// Path returns the current path history for the element.
	Path() Path
}

// EdgeIterator iterates over edges returning typed data.
type EdgeIterator interface {
	Iterator // Embed generic iterator
	// Edge returns the edge ID, the node at the other end of the adjacency,
	// its label, and any decoding error. The other node is the target for an
	// outgoing iterator and the source for an incoming iterator.
	Edge() (uuid.UUID, uuid.UUID, string, error)
}

// NodeIterator iterates over nodes returning typed data.
type NodeIterator interface {
	Iterator // Embed generic iterator
	// Node returns the ID and Label of the current node.
	Node() (uuid.UUID, string, error)
}

// resultIterator is implemented by query steps that project a materialized
// value rather than a graph element.
type resultIterator interface {
	Iterator
	Result() (interface{}, error)
}

// errorIterator preserves an iterator-construction error while providing a
// safe, inert implementation of every Iterator method.
type errorIterator struct {
	err error
}

func newErrorIterator(err error) *errorIterator { return &errorIterator{err: err} }
func (it *errorIterator) Next() bool            { return false }
func (it *errorIterator) SeekGE([]byte) bool    { return false }
func (it *errorIterator) Key() []byte           { return nil }
func (it *errorIterator) Value() []byte         { return nil }
func (it *errorIterator) Valid() bool           { return false }
func (it *errorIterator) Close() error          { return nil }
func (it *errorIterator) Error() error          { return it.err }
func (it *errorIterator) Path() Path            { return nil }

// edgeIterator implements EdgeIterator using the generic Iterator.
type edgeIterator struct {
	iter   Iterator
	valid  bool
	err    error
	first  bool
	labels []string // Filter: if empty, match all
}

func (it *edgeIterator) Next() bool {
	if it.err != nil {
		return false
	}

	// Loop to find next matching edge
	for {
		if it.first {
			it.first = false
			// Check current valid
		} else {
			it.valid = it.iter.Next()
		}

		if !it.valid {
			return false
		}

		// If no filter, we are good
		if len(it.labels) == 0 {
			return true
		}

		// Check Label
		// Key: ... [LabelLen] [Label] [TargetID]
		// We need to decode label to check it.
		// Optimization: We could check bytes if we encoded labels?
		// But decoding is robust.
		// We reuse Edge() logic or partial decode?
		// Let's decode label from Key.
		key := it.iter.Key()
		// Format: [Prefix(1)] + [ID(16)] + [Len(2)] + [Label(N)] + [ID(16)]
		if len(key) < 19 {
			// Invalid key, skip or error?
			// If invalid, Edge() will error. Let's return true and let Edge() handle error.
			return true
		}
		offset := 17
		label, n := encoding.DecodeLabel(key[offset:])
		if n == 0 {
			return true // Let Edge() error
		}

		match := false
		for _, l := range it.labels {
			if l == label {
				match = true
				break
			}
		}
		if match {
			return true
		}
		// Loop again
	}
}

func (it *edgeIterator) Edge() (uuid.UUID, uuid.UUID, string, error) {
	if !it.valid {
		return uuid.Nil, uuid.Nil, "", it.Error()
	}
	// Key format: [Prefix] + [SourceID] + [LabelLen] + [Label] +
	// [TargetID] + [EdgeID]. Incoming keys use target/source in place of
	// source/target.
	key := it.iter.Key()
	if len(key) < 51 {
		return uuid.Nil, uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}

	val := it.iter.Value()
	if len(val) < 16 {
		return uuid.Nil, uuid.Nil, "", encoding.ErrInvalidValueFormat
	}
	var edgeID uuid.UUID
	copy(edgeID[:], val[:16])

	offset := 17
	label, n := encoding.DecodeLabel(key[offset:])
	if n == 0 {
		return uuid.Nil, uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}
	offset += n

	var otherID uuid.UUID
	copy(otherID[:], key[offset:offset+16])

	return edgeID, otherID, label, nil
}

func (it *edgeIterator) Close() error {
	return it.iter.Close()
}

func (it *edgeIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	return it.iter.Error()
}

func (it *edgeIterator) Key() []byte            { return it.iter.Key() }
func (it *edgeIterator) Value() []byte          { return it.iter.Value() }
func (it *edgeIterator) Valid() bool            { return it.valid }
func (it *edgeIterator) SeekGE(key []byte) bool { return it.iter.SeekGE(key) }
func (it *edgeIterator) Path() Path             { return it.iter.Path() }

// nodeIterator implements NodeIterator using the generic Iterator.
type nodeIterator struct {
	iter  Iterator
	valid bool
	err   error
	first bool
}

func (it *nodeIterator) Next() bool {
	if it.err != nil {
		return false
	}
	if it.first {
		it.first = false
		return it.valid
	}
	it.valid = it.iter.Next()
	return it.valid
}

func (it *nodeIterator) Node() (uuid.UUID, string, error) {
	if !it.valid {
		return uuid.Nil, "", it.Error()
	}
	// Key format: [Prefix] + [NodeID]
	key := it.iter.Key()
	if len(key) < 17 {
		return uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}
	var id uuid.UUID
	copy(id[:], key[1:])

	val := it.iter.Value()
	label, _ := encoding.DecodeLabel(val)

	return id, label, nil
}

func (it *nodeIterator) Close() error {
	return it.iter.Close()
}

func (it *nodeIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	return it.iter.Error()
}

func (it *nodeIterator) Key() []byte            { return it.iter.Key() }
func (it *nodeIterator) Value() []byte          { return it.iter.Value() }
func (it *nodeIterator) Valid() bool            { return it.valid }
func (it *nodeIterator) SeekGE(key []byte) bool { return it.iter.SeekGE(key) }
func (it *nodeIterator) Path() Path             { return it.iter.Path() }

// nodeIndexIterator implements NodeIterator using the index keys.
type nodeIndexIterator struct {
	iter  Iterator
	valid bool
	err   error
	first bool
}

func (it *nodeIndexIterator) Next() bool {
	if it.err != nil {
		return false
	}
	if it.first {
		it.first = false
		return it.valid
	}
	it.valid = it.iter.Next()
	return it.valid
}

func (it *nodeIndexIterator) Node() (uuid.UUID, string, error) {
	if !it.valid {
		return uuid.Nil, "", it.Error()
	}
	// Key format: [EncodeIndexPrefix...] + [NodeID: 16 bytes]
	key := it.iter.Key()
	if len(key) < 17 { // Minimum is prefix (1) + NodeID (16)
		return uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}

	// Extract NodeID from the end
	var id uuid.UUID
	copy(id[:], key[len(key)-16:])

	// Reconstruct Label from prefix
	// format: [Prefix:1] [LabelLen:2] [Label]
	offset := 1
	label, n := encoding.DecodeLabel(key[offset:])
	if n == 0 {
		return uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}

	return id, label, nil
}

func (it *nodeIndexIterator) Close() error {
	if it.iter != nil {
		return it.iter.Close()
	}
	return nil
}

func (it *nodeIndexIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	if it.iter != nil {
		return it.iter.Error()
	}
	return nil
}

func (it *nodeIndexIterator) Key() []byte {
	// Reconstruct Node key for standard Iterator interface
	if !it.valid {
		return nil
	}
	id, _, err := it.Node()
	if err != nil {
		return nil
	}
	return encoding.EncodeNodeKey(id)
}

func (it *nodeIndexIterator) Value() []byte {
	// Reconstruct Value for standard Iterator interface
	if !it.valid {
		return nil
	}
	_, label, err := it.Node()
	if err != nil {
		return nil
	}
	return []byte(label)
}

func (it *nodeIndexIterator) Valid() bool            { return it.valid }
func (it *nodeIndexIterator) SeekGE(key []byte) bool { return false } // Seek on index iterator implies index seek, which means prefix needs to change.
func (it *nodeIndexIterator) Path() Path {
	if !it.valid {
		return nil
	}
	id, label, err := it.Node()
	if err != nil {
		return nil
	}
	return Path{{Kind: PathNode, ID: id, Label: label}}
}

// fixedNodeIterator iterates over a fixed slice of UUIDs
type fixedNodeIterator struct {
	tx     *Tx
	ids    []uuid.UUID
	idx    int
	curID  uuid.UUID
	curLbl string
	err    error
}

func newFixedNodeIterator(tx *Tx, ids []uuid.UUID) *fixedNodeIterator {
	return &fixedNodeIterator{tx: tx, ids: ids, idx: -1}
}

func (it *fixedNodeIterator) Next() bool {
	it.idx++
	if it.idx >= len(it.ids) {
		return false
	}
	// Check existence
	lbl, exists, err := it.tx.GetNode(it.ids[it.idx])
	if err != nil {
		it.err = err
		return false
	}
	if !exists {
		return it.Next() // Recurse to skip
	}
	it.curID = it.ids[it.idx]
	it.curLbl = lbl
	return true
}

func (it *fixedNodeIterator) Node() (uuid.UUID, string, error) {
	return it.curID, it.curLbl, it.err
}

func (it *fixedNodeIterator) Close() error { return nil }
func (it *fixedNodeIterator) Error() error { return it.err }

func (it *fixedNodeIterator) Key() []byte            { return encoding.EncodeNodeKey(it.curID) }
func (it *fixedNodeIterator) Value() []byte          { return []byte(it.curLbl) }
func (it *fixedNodeIterator) Valid() bool            { return it.idx >= 0 && it.idx < len(it.ids) }
func (it *fixedNodeIterator) SeekGE(key []byte) bool { return false }

func (it *fixedNodeIterator) Path() Path {
	return Path{{Kind: PathNode, ID: it.curID, Label: it.curLbl}}
}

// flatMapEdgeIterator flattens streams of EdgeIterators
type flatMapEdgeIterator struct {
	tx      *Tx
	prev    Iterator
	mapper  func(uuid.UUID) Iterator // Returns generic Iterator which must be EdgeIterator
	curIter Iterator                 // Current inner iterator (EdgeIterator)
	err     error
}

func newFlatMapEdgeIterator(tx *Tx, prev Iterator, mapper func(uuid.UUID) Iterator) *flatMapEdgeIterator {
	return &flatMapEdgeIterator{tx: tx, prev: prev, mapper: mapper}
}

func (it *flatMapEdgeIterator) Next() bool {
	if it.curIter != nil {
		if it.curIter.Next() {
			return true
		}
		if err := it.curIter.Close(); err != nil {
			it.err = err
			return false
		}
		it.curIter = nil
	}

	if !it.prev.Next() {
		return false
	}

	// Extract Node ID from prev
	var nodeID uuid.UUID
	// Try typed
	if ni, ok := it.prev.(NodeIterator); ok {
		id, _, err := ni.Node()
		if err != nil {
			it.err = err
			return false
		}
		nodeID = id
	} else {
		// Fallback: try key
		key := it.prev.Key()
		if len(key) > 17 && key[0] == encoding.PrefixNode {
			copy(nodeID[:], key[1:])
		} else {
			it.err = errors.New("pipeline type mismatch: expected Node")
			return false
		}
	}

	it.curIter = it.mapper(nodeID)
	return it.Next()
}

func (it *flatMapEdgeIterator) Edge() (uuid.UUID, uuid.UUID, string, error) {
	if it.curIter == nil {
		return uuid.Nil, uuid.Nil, "", nil
	}
	if ei, ok := it.curIter.(EdgeIterator); ok {
		return ei.Edge()
	}
	return uuid.Nil, uuid.Nil, "", errors.New("inner iterator is not EdgeIterator")
}

func (it *flatMapEdgeIterator) Close() error {
	var err error
	if it.curIter != nil {
		err = it.curIter.Close()
	}
	return errors.Join(err, it.prev.Close())
}
func (it *flatMapEdgeIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	if it.curIter != nil && it.curIter.Error() != nil {
		return it.curIter.Error()
	}
	return it.prev.Error()
}

func (it *flatMapEdgeIterator) Key() []byte {
	if it.curIter != nil {
		return it.curIter.Key()
	}
	return nil
}
func (it *flatMapEdgeIterator) Value() []byte {
	if it.curIter != nil {
		return it.curIter.Value()
	}
	return nil
}
func (it *flatMapEdgeIterator) Valid() bool          { return it.curIter != nil && it.curIter.Valid() }
func (it *flatMapEdgeIterator) SeekGE(k []byte) bool { return false }

func (it *flatMapEdgeIterator) Path() Path {
	p := it.prev.Path()
	if p == nil {
		p = Path{}
	}

	if it.curIter == nil {
		return p
	}

	if ei, ok := it.curIter.(EdgeIterator); ok {
		id, other, label, _ := ei.Edge()
		edge := PathElement{Kind: PathEdge, ID: id, Other: other, Label: label}

		newPath := make(Path, len(p)+1)
		copy(newPath, p)
		newPath[len(p)] = edge
		return newPath
	}
	return p
}

// filterIterator
type filterIterator struct {
	prev Iterator
	pred func(interface{}) bool
}

func newFilterIterator(prev Iterator, pred func(interface{}) bool) *filterIterator {
	return &filterIterator{prev: prev, pred: pred}
}

func (it *filterIterator) Next() bool {
	for it.prev.Next() {
		var val interface{}
		if ni, ok := it.prev.(NodeIterator); ok {
			id, lbl, _ := ni.Node()
			val = struct {
				ID    uuid.UUID
				Label string
			}{id, lbl}
		} // TODO: Add Edge case logic for values

		if it.pred(val) {
			return true
		}
	}
	return false
}

func (it *filterIterator) Close() error         { return it.prev.Close() }
func (it *filterIterator) Error() error         { return it.prev.Error() }
func (it *filterIterator) Key() []byte          { return it.prev.Key() }
func (it *filterIterator) Value() []byte        { return it.prev.Value() }
func (it *filterIterator) Valid() bool          { return it.prev.Valid() }
func (it *filterIterator) SeekGE(k []byte) bool { return it.prev.SeekGE(k) }
func (it *filterIterator) Path() Path           { return it.prev.Path() }

func (it *filterIterator) Node() (uuid.UUID, string, error) {
	if ni, ok := it.prev.(NodeIterator); ok {
		return ni.Node()
	}
	return uuid.Nil, "", errors.New("not a node iterator")
}
func (it *filterIterator) Edge() (uuid.UUID, uuid.UUID, string, error) {
	if ei, ok := it.prev.(EdgeIterator); ok {
		return ei.Edge()
	}
	return uuid.Nil, uuid.Nil, "", errors.New("not an edge iterator")
}

// valueIterator projects requested property values as a scalar stream.
type valueIterator struct {
	tx      *Tx
	prev    Iterator
	keys    []string
	pending []interface{}
	current interface{}
	valid   bool
	err     error
}

func newValueIterator(tx *Tx, prev Iterator, keys []string) *valueIterator {
	return &valueIterator{tx: tx, prev: prev, keys: append([]string(nil), keys...)}
}

func (it *valueIterator) Next() bool {
	it.valid = false
	for {
		if len(it.pending) > 0 {
			it.current = it.pending[0]
			it.pending = it.pending[1:]
			it.valid = true
			return true
		}
		if it.err != nil || !it.prev.Next() {
			return false
		}

		var entityID uuid.UUID
		switch prev := it.prev.(type) {
		case NodeIterator:
			id, _, err := prev.Node()
			if err != nil {
				it.err = err
				return false
			}
			entityID = id
		case EdgeIterator:
			id, _, _, err := prev.Edge()
			if err != nil {
				it.err = err
				return false
			}
			entityID = id
		default:
			it.err = errors.New("pipeline type mismatch: Values requires nodes or edges")
			return false
		}

		props, err := it.tx.GetProperties(entityID)
		if err != nil {
			it.err = err
			return false
		}
		for _, key := range it.keys {
			if value, exists := props[key]; exists {
				it.pending = append(it.pending, value)
			}
		}
	}
}

func (it *valueIterator) Result() (interface{}, error) {
	if !it.valid {
		if err := it.Error(); err != nil {
			return nil, err
		}
		return nil, errors.New("invalid iterator state")
	}
	return it.current, nil
}

func (it *valueIterator) Close() error       { return it.prev.Close() }
func (it *valueIterator) Key() []byte        { return nil }
func (it *valueIterator) Value() []byte      { return nil }
func (it *valueIterator) Valid() bool        { return it.valid }
func (it *valueIterator) SeekGE([]byte) bool { return false }
func (it *valueIterator) Path() Path         { return it.prev.Path() }
func (it *valueIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	return it.prev.Error()
}

// pathIterator exposes the path history as the value
type pathIterator struct {
	prev    Iterator
	curPath Path
}

func newPathIterator(prev Iterator) *pathIterator {
	return &pathIterator{prev: prev}
}

func (it *pathIterator) Next() bool {
	if it.prev.Next() {
		it.curPath = append(Path(nil), it.prev.Path()...)
		return true
	}
	return false
}

func (it *pathIterator) Key() []byte {
	return nil
}

func (it *pathIterator) Value() []byte {
	return nil
}

func (it *pathIterator) Result() (interface{}, error) {
	return append(Path(nil), it.curPath...), nil
}

func (it *pathIterator) Close() error         { return it.prev.Close() }
func (it *pathIterator) Error() error         { return it.prev.Error() }
func (it *pathIterator) Valid() bool          { return it.prev.Valid() }
func (it *pathIterator) SeekGE(k []byte) bool { return it.prev.SeekGE(k) }
func (it *pathIterator) Path() Path           { return append(Path(nil), it.curPath...) }

// repeatIterator implements BFS traversal
type traverser struct {
	id    uuid.UUID
	label string
	path  Path
	depth int
}

type repeatIterator struct {
	tx      *Tx
	prev    Iterator
	conf    *RepeatConfig
	queue   []traverser
	inited  bool
	curItem traverser
	err     error
}

func newRepeatIterator(tx *Tx, prev Iterator, conf *RepeatConfig) *repeatIterator {
	return &repeatIterator{tx: tx, prev: prev, conf: conf}
}

func (it *repeatIterator) Next() bool {
	if !it.inited {
		// Drain prev into queue
		for it.prev.Next() {
			var id uuid.UUID
			var lbl string
			if ni, ok := it.prev.(NodeIterator); ok {
				id, lbl, _ = ni.Node()
			}
			it.queue = append(it.queue, traverser{
				id: id, label: lbl, path: it.prev.Path(), depth: 0,
			})
		}
		it.inited = true
	}

	// BFS Loop
	for len(it.queue) > 0 {
		cur := it.queue[0]
		it.queue = it.queue[1:]

		// Check termination
		val := struct {
			ID    uuid.UUID
			Label string
		}{cur.id, cur.label}
		stop := false
		if it.conf.until != nil {
			if it.conf.until(val) {
				stop = true
			}
		}
		if it.conf.times > 0 && cur.depth >= it.conf.times {
			stop = true
		}

		if stop {
			it.curItem = cur
			return true
		}

		// Recurse
		startIter := &fixedNodeIterator{
			tx:  it.tx,
			ids: []uuid.UUID{cur.id},
			idx: -1,
		}

		tempTP := &TraversalPipeline{db: it.tx.db, steps: []Step{
			func(_ *Tx, _ Iterator) Iterator { return startIter },
		}}
		outTP := it.conf.sub(tempTP)

		var iter Iterator = nil
		for _, step := range outTP.steps {
			iter = step(it.tx, iter)
			if iter == nil {
				break
			}
		}

		if iter != nil {
			for iter.Next() {
				if ni, ok := iter.(NodeIterator); ok {
					nid, nlbl, _ := ni.Node()
					it.queue = append(it.queue, traverser{
						id: nid, label: nlbl, path: iter.Path(), depth: cur.depth + 1,
					})
				}
			}
			if err := iter.Close(); err != nil {
				it.err = err
				return false
			}
		}

		if it.conf.emit {
			it.curItem = cur
			return true
		}
	}

	return false
}

func (it *repeatIterator) Node() (uuid.UUID, string, error) {
	return it.curItem.id, it.curItem.label, nil
}
func (it *repeatIterator) Close() error         { return it.prev.Close() }
func (it *repeatIterator) Error() error         { return it.err }
func (it *repeatIterator) Key() []byte          { return encoding.EncodeNodeKey(it.curItem.id) }
func (it *repeatIterator) Value() []byte        { return []byte(it.curItem.label) }
func (it *repeatIterator) Valid() bool          { return true }
func (it *repeatIterator) SeekGE(k []byte) bool { return false }
func (it *repeatIterator) Path() Path           { return it.curItem.path }

// neighborIterator wraps an EdgeIterator and yields the neighbor Node.
type neighborIterator struct {
	tx   *Tx
	iter EdgeIterator
	dir  string // "out" or "in"
	// Current Node state
	curID  uuid.UUID
	curLbl string
	err    error
}

func newNeighborIterator(tx *Tx, iter EdgeIterator, dir string) *neighborIterator {
	return &neighborIterator{tx: tx, iter: iter, dir: dir}
}

func (it *neighborIterator) Next() bool {
	if it.err != nil {
		return false
	}
	// Loop until we find a valid node or iterator exhausts
	for it.iter.Next() {
		_, otherID, _, err := it.iter.Edge() // OutEdges returns: edgeID, targetID, label
		if err != nil {
			it.err = err
			return false
		}
		// Fetch Node Label
		lbl, exists, err := it.tx.GetNode(otherID)
		if err != nil {
			it.err = err
			return false
		}
		if !exists {
			continue // Dangling edge? skip
		}
		it.curID = otherID
		it.curLbl = lbl
		return true
	}
	return false
}

func (it *neighborIterator) Node() (uuid.UUID, string, error) {
	if it.curID == uuid.Nil {
		return uuid.Nil, "", errors.New("invalid iterator state")
	}
	return it.curID, it.curLbl, nil
}

func (it *neighborIterator) Close() error { return it.iter.Close() }
func (it *neighborIterator) Error() error { return it.err }

// Key/Value reflect the Node
func (it *neighborIterator) Key() []byte          { return encoding.EncodeNodeKey(it.curID) }
func (it *neighborIterator) Value() []byte        { return []byte(it.curLbl) }
func (it *neighborIterator) Valid() bool          { return it.iter.Valid() } // approx
func (it *neighborIterator) SeekGE(k []byte) bool { return false }
func (it *neighborIterator) Path() Path {
	// Extend path from edge
	p := it.iter.Path()
	path := make(Path, len(p)+1)
	copy(path, p)
	path[len(p)] = PathElement{Kind: PathNode, ID: it.curID, Label: it.curLbl}
	return path
}
