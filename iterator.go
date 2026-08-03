package pathway

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

// Iterator is the common lifecycle contract for graph and traversal results.
//
// Call Next before reading each result. A true return positions the iterator on
// one result until the next call to Next. False means iteration has ended;
// callers must then check Error. Close releases iterator resources and must be
// called once on every iterator, including empty and failed iterators. Close
// ends the iterator lifetime; no methods may be called afterward. Iterators are
// not safe for concurrent use. An iterator returned by Tx must be closed before
// its transaction closes.
type Iterator interface {
	// Next advances to the next result. It returns false on exhaustion or error.
	Next() bool
	// Close releases all resources owned by the iterator.
	Close() error
	// Error reports construction or advancement errors. It is checked after
	// Next returns false and does not report exhaustion as an error.
	Error() error
}

// EdgeIterator iterates over typed edges. Edge is valid only after Next returns
// true and before Next is called again or the iterator is closed. Its UUIDs and
// string are caller-owned values and remain valid after advancement or Close.
type EdgeIterator interface {
	Iterator
	// Edge returns the edge ID, the node at the other end of the adjacency,
	// its label, and any decoding error. The other node is the target for an
	// outgoing iterator and the source for an incoming iterator.
	Edge() (uuid.UUID, uuid.UUID, string, error)
}

// NodeIterator iterates over typed nodes. Node is valid only after Next returns
// true and before Next is called again or the iterator is closed. Its UUID and
// string are caller-owned values and remain valid after advancement or Close.
type NodeIterator interface {
	Iterator
	// Node returns the ID and Label of the current node.
	Node() (uuid.UUID, string, error)
}

type traversalResultShape uint8

const (
	resultShapeDynamic traversalResultShape = iota
	resultShapeNode
	resultShapeEdge
	resultShapeID
	resultShapePath
)

// resultIterator is implemented by query steps that project a materialized
// value rather than a graph element.
type resultIterator interface {
	Iterator
	Result() (any, error)
	ResultShape() traversalResultShape
}

// resultSizeHinter provides an upper bound for terminal preallocation. Most
// traversal steps cannot know their output cardinality without executing.
type resultSizeHinter interface {
	ResultSizeHint() int
}

// nodeIDIterator is an internal optional capability for consuming a node ID
// without forcing lazy iterators to load the node label.
type nodeIDIterator interface {
	nodeID() (uuid.UUID, error)
}

func currentNodeID(iter Iterator) (uuid.UUID, error) {
	if ids, ok := iter.(nodeIDIterator); ok {
		return ids.nodeID()
	}
	if nodes, ok := iter.(NodeIterator); ok {
		id, _, err := nodes.Node()
		return id, err
	}
	return uuid.Nil, errors.New("pipeline type mismatch: expected node")
}

// rawIterator is the package-private storage cursor used by typed adapters.
// Key and Value follow Pebble's lifetime rule: their buffers are valid only
// until the next positioning call or Close.
type rawIterator interface {
	Next() bool
	SeekGE([]byte) bool
	Key() []byte
	Value() []byte
	Valid() bool
	Close() error
	Error() error
}

type pathSource interface {
	path() Path
}

func currentPath(iter Iterator) Path {
	if source, ok := iter.(pathSource); ok {
		return source.path()
	}
	return nil
}

func iteratorStateError(err error) error {
	if err != nil {
		return err
	}
	return ErrInvalidIteratorState
}

// errorIterator preserves an iterator-construction error while providing a
// safe, inert implementation of every rawIterator method.
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

// edgeIterator implements EdgeIterator using the generic Iterator.
type edgeIterator struct {
	iter    rawIterator
	valid   bool
	current bool
	err     error
	first   bool
}

func (it *edgeIterator) Next() bool {
	it.current = false
	if it.err != nil {
		return false
	}
	if err := it.iter.Error(); err != nil {
		it.err = err
		it.valid = false
		return false
	}

	if it.first {
		it.first = false
		it.current = it.valid
		return it.valid
	}
	it.valid = it.iter.Next()
	it.current = it.valid
	return it.valid
}

func (it *edgeIterator) Edge() (uuid.UUID, uuid.UUID, string, error) {
	if !it.current {
		return uuid.Nil, uuid.Nil, "", iteratorStateError(it.Error())
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

// multiEdgeIterator consumes exact, disjoint label ranges one at a time. Only
// one Pebble iterator is open at once, even when many labels are requested.
type multiEdgeIterator struct {
	tx       *Tx
	prefixes [][]byte
	next     int
	current  EdgeIterator
	valid    bool
	err      error
}

func newMultiEdgeIterator(tx *Tx, prefixes [][]byte) *multiEdgeIterator {
	return &multiEdgeIterator{tx: tx, prefixes: prefixes}
}

func (it *multiEdgeIterator) Next() bool {
	it.valid = false
	for it.err == nil {
		if err := it.tx.contextErr(); err != nil {
			it.err = err
			return false
		}
		if it.current == nil {
			if it.next >= len(it.prefixes) {
				return false
			}
			it.current = it.tx.edgeRange(it.prefixes[it.next])
			it.next++
		}
		if it.current.Next() {
			it.valid = true
			return true
		}
		it.err = it.current.Error()
		it.err = errors.Join(it.err, it.current.Close())
		it.current = nil
	}
	return false
}

func (it *multiEdgeIterator) Edge() (uuid.UUID, uuid.UUID, string, error) {
	if !it.valid || it.current == nil {
		return uuid.Nil, uuid.Nil, "", iteratorStateError(it.Error())
	}
	return it.current.Edge()
}

func (it *multiEdgeIterator) Close() error {
	it.valid = false
	if it.current == nil {
		return nil
	}
	err := it.current.Close()
	it.current = nil
	return err
}

func (it *multiEdgeIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	if it.current != nil {
		return it.current.Error()
	}
	return nil
}

// nodeIterator implements NodeIterator using the generic Iterator.
type nodeIterator struct {
	iter    rawIterator
	valid   bool
	current bool
	err     error
	first   bool
}

func (it *nodeIterator) Next() bool {
	it.current = false
	if it.err != nil {
		return false
	}
	if err := it.iter.Error(); err != nil {
		it.err = err
		it.valid = false
		return false
	}
	if it.first {
		it.first = false
		it.current = it.valid
		return it.valid
	}
	it.valid = it.iter.Next()
	it.current = it.valid
	return it.valid
}

func (it *nodeIterator) Node() (uuid.UUID, string, error) {
	if !it.current {
		return uuid.Nil, "", iteratorStateError(it.Error())
	}
	id, err := it.nodeID()
	if err != nil {
		return uuid.Nil, "", err
	}

	val := it.iter.Value()
	label, _ := encoding.DecodeLabel(val)

	return id, label, nil
}

func (it *nodeIterator) nodeID() (uuid.UUID, error) {
	if !it.current {
		return uuid.Nil, iteratorStateError(it.Error())
	}
	key := it.iter.Key()
	if len(key) < 17 {
		return uuid.Nil, encoding.ErrInvalidKeyFormat
	}
	var id uuid.UUID
	copy(id[:], key[1:])
	return id, nil
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

func (it *nodeIterator) path() Path {
	id, label, err := it.Node()
	if err != nil {
		return nil
	}
	return Path{{Kind: PathNode, ID: id, Label: label}}
}

// nodeIndexIterator implements NodeIterator using the index keys.
type nodeIndexIterator struct {
	iter    rawIterator
	valid   bool
	current bool
	err     error
	first   bool
}

func (it *nodeIndexIterator) Next() bool {
	it.current = false
	if it.err != nil {
		return false
	}
	if err := it.iter.Error(); err != nil {
		it.err = err
		it.valid = false
		return false
	}
	if it.first {
		it.first = false
		it.current = it.valid
		return it.valid
	}
	it.valid = it.iter.Next()
	it.current = it.valid
	return it.valid
}

func (it *nodeIndexIterator) Node() (uuid.UUID, string, error) {
	if !it.current {
		return uuid.Nil, "", iteratorStateError(it.Error())
	}
	id, err := it.nodeID()
	if err != nil {
		return uuid.Nil, "", err
	}

	// Reconstruct Label from prefix
	// format: [Prefix:1] [LabelLen:2] [Label]
	key := it.iter.Key()
	offset := 1
	label, n := encoding.DecodeLabel(key[offset:])
	if n == 0 {
		return uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}

	return id, label, nil
}

func (it *nodeIndexIterator) nodeID() (uuid.UUID, error) {
	if !it.current {
		return uuid.Nil, iteratorStateError(it.Error())
	}
	key := it.iter.Key()
	if len(key) < 17 {
		return uuid.Nil, encoding.ErrInvalidKeyFormat
	}
	var id uuid.UUID
	copy(id[:], key[len(key)-16:])
	return id, nil
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

func (it *nodeIndexIterator) path() Path {
	if !it.current {
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
	tx          *Tx
	ids         []uuid.UUID
	verified    bool
	idx         int
	curID       uuid.UUID
	curLbl      string
	labelLoaded bool
	err         error
}

func newFixedNodeIterator(tx *Tx, ids []uuid.UUID) *fixedNodeIterator {
	return &fixedNodeIterator{tx: tx, ids: ids, idx: -1}
}

func newVerifiedFixedNodeIterator(tx *Tx, ids []uuid.UUID) *fixedNodeIterator {
	return &fixedNodeIterator{tx: tx, ids: ids, idx: -1, verified: true}
}

func (it *fixedNodeIterator) Next() bool {
	if it.err != nil {
		return false
	}
	for {
		it.idx++
		if it.idx >= len(it.ids) {
			return false
		}
		if !it.verified {
			exists, err := it.tx.nodeExists(it.ids[it.idx])
			if err != nil {
				it.err = err
				return false
			}
			if !exists {
				continue
			}
		}
		it.curID = it.ids[it.idx]
		it.curLbl = ""
		it.labelLoaded = false
		return true
	}
}

func (it *fixedNodeIterator) Node() (uuid.UUID, string, error) {
	if !it.positioned() {
		return uuid.Nil, "", iteratorStateError(it.Error())
	}
	if err := it.loadLabel(); err != nil {
		return uuid.Nil, "", err
	}
	return it.curID, it.curLbl, it.err
}

func (it *fixedNodeIterator) nodeID() (uuid.UUID, error) {
	if !it.positioned() {
		return uuid.Nil, iteratorStateError(it.Error())
	}
	return it.curID, nil
}

func (it *fixedNodeIterator) loadLabel() error {
	if it.labelLoaded || it.err != nil {
		return it.err
	}
	label, exists, err := it.tx.GetNode(it.curID)
	if err != nil {
		it.err = err
		return err
	}
	if !exists {
		it.err = ErrNodeNotFound
		return it.err
	}
	it.curLbl = label
	it.labelLoaded = true
	return nil
}

func (it *fixedNodeIterator) Close() error { return nil }
func (it *fixedNodeIterator) Error() error { return it.err }

func (it *fixedNodeIterator) positioned() bool {
	return it.idx >= 0 && it.idx < len(it.ids) && it.err == nil
}

func (it *fixedNodeIterator) path() Path {
	if err := it.loadLabel(); err != nil {
		return nil
	}
	return Path{{Kind: PathNode, ID: it.curID, Label: it.curLbl}}
}

func (it *fixedNodeIterator) ResultSizeHint() int { return len(it.ids) }

// flatMapEdgeIterator flattens streams of EdgeIterators
type flatMapEdgeIterator struct {
	tx      *Tx
	prev    Iterator
	mapper  func(uuid.UUID) Iterator // Returns generic Iterator which must be EdgeIterator
	curIter Iterator                 // Current inner iterator (EdgeIterator)
	valid   bool
	err     error
}

func newFlatMapEdgeIterator(tx *Tx, prev Iterator, mapper func(uuid.UUID) Iterator) *flatMapEdgeIterator {
	return &flatMapEdgeIterator{tx: tx, prev: prev, mapper: mapper}
}

func (it *flatMapEdgeIterator) Next() bool {
	it.valid = false
	if it.curIter != nil {
		if it.curIter.Next() {
			it.valid = true
			return true
		}
		if err := errors.Join(it.curIter.Error(), it.curIter.Close()); err != nil {
			it.err = err
			return false
		}
		it.curIter = nil
	}

	if !it.prev.Next() {
		return false
	}

	nodeID, err := currentNodeID(it.prev)
	if err != nil {
		it.err = err
		return false
	}

	it.curIter = it.mapper(nodeID)
	return it.Next()
}

func (it *flatMapEdgeIterator) Edge() (uuid.UUID, uuid.UUID, string, error) {
	if !it.valid || it.curIter == nil {
		return uuid.Nil, uuid.Nil, "", iteratorStateError(it.Error())
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

func (it *flatMapEdgeIterator) path() Path {
	p := currentPath(it.prev)
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

// nodeFilterIterator filters a node stream with a typed predicate.
type nodeFilterIterator struct {
	prev  Iterator
	pred  NodePredicate
	valid bool
	err   error
}

func newNodeFilterIterator(prev Iterator, pred NodePredicate) *nodeFilterIterator {
	return &nodeFilterIterator{prev: prev, pred: pred}
}

func (it *nodeFilterIterator) Next() bool {
	it.valid = false
	nodes, ok := it.prev.(NodeIterator)
	if !ok {
		it.err = errors.New("pipeline type mismatch: node filter requires nodes")
		return false
	}
	for it.prev.Next() {
		id, label, err := nodes.Node()
		if err != nil {
			it.err = err
			return false
		}
		if it.pred(Node{ID: id, Label: label}) {
			it.valid = true
			return true
		}
	}
	return false
}

func (it *nodeFilterIterator) Close() error { return it.prev.Close() }
func (it *nodeFilterIterator) Error() error {
	return errors.Join(it.err, it.prev.Error())
}
func (it *nodeFilterIterator) path() Path { return currentPath(it.prev) }

func (it *nodeFilterIterator) Node() (uuid.UUID, string, error) {
	if !it.valid {
		return uuid.Nil, "", iteratorStateError(it.Error())
	}
	if ni, ok := it.prev.(NodeIterator); ok {
		return ni.Node()
	}
	return uuid.Nil, "", errors.New("pipeline type mismatch: node filter requires nodes")
}
func (it *nodeFilterIterator) nodeID() (uuid.UUID, error) {
	if !it.valid {
		return uuid.Nil, iteratorStateError(it.Error())
	}
	return currentNodeID(it.prev)
}
func (it *nodeFilterIterator) ResultSizeHint() int {
	if hinted, ok := it.prev.(resultSizeHinter); ok {
		return hinted.ResultSizeHint()
	}
	return 0
}

// valueIterator projects requested property values as a scalar stream.
type valueIterator struct {
	tx      *Tx
	prev    Iterator
	keys    []string
	pending []any
	current any
	valid   bool
	err     error
}

func newValueIterator(tx *Tx, prev Iterator, keys []string) *valueIterator {
	return &valueIterator{tx: tx, prev: prev, keys: append([]string(nil), keys...)}
}

func (it *valueIterator) Next() bool {
	it.valid = false
	for {
		if err := it.tx.contextErr(); err != nil {
			it.err = err
			return false
		}
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
			id, err := currentNodeID(prev)
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

func (it *valueIterator) Result() (any, error) {
	if !it.valid {
		if err := it.Error(); err != nil {
			return nil, err
		}
		return nil, errors.New("invalid iterator state")
	}
	return it.current, nil
}

func (it *valueIterator) ResultShape() traversalResultShape { return resultShapeDynamic }

func (it *valueIterator) Close() error { return it.prev.Close() }
func (it *valueIterator) path() Path   { return currentPath(it.prev) }
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
		it.curPath = append(Path(nil), currentPath(it.prev)...)
		return true
	}
	return false
}

func (it *pathIterator) Result() (any, error) {
	return append(Path(nil), it.curPath...), nil
}

func (it *pathIterator) ResultShape() traversalResultShape { return resultShapePath }

func (it *pathIterator) Close() error { return it.prev.Close() }
func (it *pathIterator) Error() error { return it.prev.Error() }
func (it *pathIterator) path() Path   { return append(Path(nil), it.curPath...) }
func (it *pathIterator) ResultSizeHint() int {
	if hinted, ok := it.prev.(resultSizeHinter); ok {
		return hinted.ResultSizeHint()
	}
	return 0
}

// repeatIterator implements BFS traversal
type traverser struct {
	node  Node
	path  Path
	depth int
}

type repeatIterator struct {
	tx      *Tx
	prev    Iterator
	conf    *RepeatConfig
	queue   []traverser
	inited  bool
	valid   bool
	curItem traverser
	err     error
}

func newRepeatIterator(tx *Tx, prev Iterator, conf *RepeatConfig) *repeatIterator {
	return &repeatIterator{tx: tx, prev: prev, conf: conf}
}

func (it *repeatIterator) Next() bool {
	it.valid = false
	if it.contextCanceled() {
		return false
	}
	if !it.inited {
		// Drain prev into queue
		nodes, ok := it.prev.(NodeIterator)
		if !ok {
			it.err = errors.New("pipeline type mismatch: repeat requires nodes")
			return false
		}
		for it.prev.Next() {
			if it.contextCanceled() {
				return false
			}
			id, lbl, err := nodes.Node()
			if err != nil {
				it.err = err
				return false
			}
			it.queue = append(it.queue, traverser{
				node: Node{ID: id, Label: lbl}, path: currentPath(it.prev), depth: 0,
			})
		}
		if err := it.prev.Error(); err != nil {
			it.err = err
			return false
		}
		it.inited = true
	}

	// BFS Loop
	for len(it.queue) > 0 {
		if it.contextCanceled() {
			return false
		}
		cur := it.queue[0]
		it.queue = it.queue[1:]

		// Check termination
		stop := false
		if it.conf.until != nil {
			if it.conf.until(cur.node) {
				stop = true
			}
		}
		if it.conf.times > 0 && cur.depth >= it.conf.times {
			stop = true
		}

		if stop {
			it.curItem = cur
			it.valid = true
			return true
		}

		// Recurse
		startIter := &fixedNodeIterator{
			tx:  it.tx,
			ids: []uuid.UUID{cur.node.ID},
			idx: -1,
		}

		tempTP := &TraversalPipeline{db: it.tx.db, steps: []Step{
			func(_ *Tx, _ Iterator) Iterator { return startIter },
		}}
		outTP := it.conf.sub(tempTP)
		if outTP == nil {
			it.err = fmt.Errorf("%w: Repeat body returned a nil pipeline", ErrInvalidRepeat)
			return false
		}
		if err := outTP.validateRepeatConfiguration(); err != nil {
			it.err = err
			return false
		}

		var iter Iterator
		for _, step := range outTP.steps {
			if it.contextCanceled() {
				if iter != nil {
					it.err = errors.Join(it.err, iter.Close())
				}
				return false
			}
			next := step(it.tx, iter)
			if next == nil {
				it.err = errors.New("repeat pipeline step returned nil iterator")
				if iter != nil {
					it.err = errors.Join(it.err, iter.Close())
				}
				return false
			}
			iter = next
		}

		if iter != nil {
			for iter.Next() {
				if it.contextCanceled() {
					it.err = errors.Join(it.err, iter.Close())
					return false
				}
				nodes, ok := iter.(NodeIterator)
				if !ok {
					it.err = errors.Join(
						errors.New("pipeline type mismatch: repeat body must return nodes"),
						iter.Close(),
					)
					return false
				}
				nid, nlbl, err := nodes.Node()
				if err != nil {
					it.err = errors.Join(err, iter.Close())
					return false
				}
				it.queue = append(it.queue, traverser{
					node: Node{ID: nid, Label: nlbl}, path: currentPath(iter), depth: cur.depth + 1,
				})
			}
			if err := errors.Join(iter.Error(), iter.Close()); err != nil {
				it.err = err
				return false
			}
		}

		if it.conf.emit {
			it.curItem = cur
			it.valid = true
			return true
		}
	}

	return false
}

func (it *repeatIterator) contextCanceled() bool {
	if it.err != nil {
		return true
	}
	if err := it.tx.contextErr(); err != nil {
		it.err = err
		return true
	}
	return false
}

func (it *repeatIterator) Node() (uuid.UUID, string, error) {
	if !it.valid {
		return uuid.Nil, "", iteratorStateError(it.Error())
	}
	return it.curItem.node.ID, it.curItem.node.Label, nil
}
func (it *repeatIterator) nodeID() (uuid.UUID, error) {
	if !it.valid {
		return uuid.Nil, iteratorStateError(it.Error())
	}
	return it.curItem.node.ID, nil
}
func (it *repeatIterator) Close() error { return it.prev.Close() }
func (it *repeatIterator) Error() error { return it.err }
func (it *repeatIterator) path() Path   { return it.curItem.path }

// neighborIterator wraps an EdgeIterator and yields the neighbor Node.
type neighborIterator struct {
	tx   *Tx
	iter EdgeIterator
	dir  string // "out" or "in"
	// Current Node state
	curID       uuid.UUID
	curLbl      string
	labelLoaded bool
	valid       bool
	err         error
}

func newNeighborIterator(tx *Tx, iter EdgeIterator, dir string) *neighborIterator {
	return &neighborIterator{tx: tx, iter: iter, dir: dir}
}

func (it *neighborIterator) Next() bool {
	it.valid = false
	if it.err != nil {
		return false
	}
	if !it.iter.Next() {
		return false
	}
	_, otherID, _, err := it.iter.Edge()
	if err != nil {
		it.err = err
		return false
	}
	it.curID = otherID
	it.curLbl = ""
	it.labelLoaded = false
	it.valid = true
	return true
}

func (it *neighborIterator) Node() (uuid.UUID, string, error) {
	if !it.valid {
		return uuid.Nil, "", iteratorStateError(it.Error())
	}
	if err := it.loadLabel(); err != nil {
		return uuid.Nil, "", err
	}
	return it.curID, it.curLbl, nil
}

func (it *neighborIterator) nodeID() (uuid.UUID, error) {
	if !it.valid {
		return uuid.Nil, iteratorStateError(it.Error())
	}
	return it.curID, nil
}

func (it *neighborIterator) loadLabel() error {
	if it.labelLoaded || it.err != nil {
		return it.err
	}
	if !it.valid {
		return errors.New("invalid iterator state")
	}
	label, exists, err := it.tx.GetNode(it.curID)
	if err != nil {
		it.err = err
		return err
	}
	if !exists {
		it.err = ErrNodeNotFound
		return it.err
	}
	it.curLbl = label
	it.labelLoaded = true
	return nil
}

func (it *neighborIterator) Close() error { return it.iter.Close() }
func (it *neighborIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	return it.iter.Error()
}

func (it *neighborIterator) path() Path {
	if err := it.loadLabel(); err != nil {
		return nil
	}
	// Extend path from edge
	p := currentPath(it.iter)
	path := make(Path, len(p)+1)
	copy(path, p)
	path[len(p)] = PathElement{Kind: PathNode, ID: it.curID, Label: it.curLbl}
	return path
}

// idIterator projects node IDs without materializing node labels.
type idIterator struct {
	prev    Iterator
	current uuid.UUID
	valid   bool
	err     error
}

func newIDIterator(prev Iterator) *idIterator { return &idIterator{prev: prev} }

func (it *idIterator) Next() bool {
	it.valid = false
	if it.err != nil || !it.prev.Next() {
		return false
	}
	id, err := currentNodeID(it.prev)
	if err != nil {
		it.err = err
		return false
	}
	it.current = id
	it.valid = true
	return true
}

func (it *idIterator) Result() (any, error) {
	if !it.valid {
		if err := it.Error(); err != nil {
			return nil, err
		}
		return nil, errors.New("invalid iterator state")
	}
	return it.current, nil
}

func (it *idIterator) ResultShape() traversalResultShape { return resultShapeID }

func (it *idIterator) Close() error { return it.prev.Close() }
func (it *idIterator) Error() error {
	if it.err != nil {
		return it.err
	}
	return it.prev.Error()
}

func (it *idIterator) ResultSizeHint() int {
	if hinted, ok := it.prev.(resultSizeHinter); ok {
		return hinted.ResultSizeHint()
	}
	return 0
}
