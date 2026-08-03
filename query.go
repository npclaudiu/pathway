package pathway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Traversal represents a fluent query builder.
// Currently empty but kept for type safety and potential future state.
type Traversal struct {
}

// Node is a materialized node returned by an unprojected traversal. Its fields
// own their data and remain valid after the traversal transaction closes.
type Node struct {
	ID    uuid.UUID
	Label string
}

// Edge is a materialized adjacency edge returned by an unprojected edge
// traversal. Other is the endpoint relative to the traversal direction.
type Edge struct {
	ID    uuid.UUID
	Other uuid.UUID
	Label string
}

// PathElementKind identifies the graph element represented by a path entry.
type PathElementKind string

const (
	PathNode PathElementKind = "node"
	PathEdge PathElementKind = "edge"
)

// PathElement is one typed node or edge in a traversal path. Other is set for
// edges and identifies the endpoint reached by that traversal step.
type PathElement struct {
	Kind  PathElementKind
	ID    uuid.UUID
	Label string
	Other uuid.UUID
}

// Path is an ordered snapshot of the elements visited by a traversal result.
type Path []PathElement

// TraversalSource is the starting point for graph traversals.
// It holds a reference to the database and spawns TraversalPipelines.
type TraversalSource struct {
	db *Database
}

// NewTraversalSource creates a new traversal source from a database instance.
//
// Usage:
//
//	g := pathway.NewTraversalSource(db)
func NewTraversalSource(db *Database) *TraversalSource {
	return &TraversalSource{db: db}
}

// V starts a traversal.
// If ids are provided, it starts at the specified nodes.
// If no ids are provided, it starts a scan of all nodes in the graph.
//
// Usage:
//
//	// Start at specific node
//	g.V(id).Out()...
//
//	// Start at all nodes (scan)
//	g.V().HasLabel("Person")...
func (ts *TraversalSource) V(ids ...uuid.UUID) *TraversalPipeline {
	ownedIDs := append([]uuid.UUID(nil), ids...)
	return &TraversalPipeline{
		db: ts.db,
		steps: []Step{
			func(tx *Tx, _ Iterator) Iterator {
				if len(ownedIDs) == 0 {
					return tx.ScanNodes()
				}
				return newFixedNodeIterator(tx, ownedIDs)
			},
		},
	}
}

// VStrings parses textual UUIDs and starts a traversal at those nodes. It
// returns ErrInvalidNodeID if any value is malformed; no partial traversal is
// constructed. Prefer V when UUIDs are already available.
func (ts *TraversalSource) VStrings(ids ...string) (*TraversalPipeline, error) {
	parsed := make([]uuid.UUID, len(ids))
	for i, text := range ids {
		id, err := uuid.Parse(text)
		if err != nil {
			return nil, fmt.Errorf("%w at position %d: %q", ErrInvalidNodeID, i, text)
		}
		parsed[i] = id
	}
	return ts.V(parsed...), nil
}

// VExternal resolves external identities and starts a traversal at their
// internal nodes, preserving caller order and skipping unbound identities. An
// empty argument list scans all nodes, matching V. Each external start adds a
// forward lookup and reverse-consistency check before normal node loading.
func (ts *TraversalSource) VExternal(ids ...ExternalID) (*TraversalPipeline, error) {
	ownedIDs := make([]ExternalID, len(ids))
	for i, id := range ids {
		if err := id.validate(); err != nil {
			return nil, fmt.Errorf("external ID at position %d: %w", i, err)
		}
		ownedIDs[i] = id.clone()
	}
	return &TraversalPipeline{
		db: ts.db,
		steps: []Step{func(tx *Tx, _ Iterator) Iterator {
			if len(ownedIDs) == 0 {
				return tx.ScanNodes()
			}
			resolved := make([]uuid.UUID, 0, len(ownedIDs))
			for _, externalID := range ownedIDs {
				nodeID, exists, err := tx.ResolveExternalID(externalID)
				if err != nil {
					return &fixedNodeIterator{tx: tx, idx: -1, err: err}
				}
				if exists {
					resolved = append(resolved, nodeID)
				}
			}
			return newVerifiedFixedNodeIterator(tx, resolved)
		}},
	}, nil
}

// Step defines a processing step in the traversal pipeline.
// It takes a transaction context and a previous iterator, and returns a new iterator.
type Step func(tx *Tx, prev Iterator) Iterator

// RepeatConfig holds configuration for repeat steps (loops).
type RepeatConfig struct {
	sub            func(*TraversalPipeline) *TraversalPipeline
	until          NodePredicate
	untilSet       bool
	times          int
	timesSet       bool
	emit           bool
	emitSet        bool
	allowUnbounded bool
}

// TraversalPipeline represents a chain of query steps.
type TraversalPipeline struct {
	db           *Database
	steps        []Step
	activeRepeat *RepeatConfig
	repeats      []*RepeatConfig
	buildErr     error
}

func (tp *TraversalPipeline) closeActiveRepeat() {
	tp.activeRepeat = nil
}

func (tp *TraversalPipeline) setRepeatError(format string, args ...any) {
	if tp.buildErr == nil {
		tp.buildErr = fmt.Errorf("%w: %s", ErrInvalidRepeat, fmt.Sprintf(format, args...))
	}
}

func (tp *TraversalPipeline) repeatForModifier(name string) *RepeatConfig {
	if tp.activeRepeat == nil {
		tp.setRepeatError("%s must immediately follow Repeat or another repeat modifier", name)
		return nil
	}
	return tp.activeRepeat
}

func (tp *TraversalPipeline) validateRepeatConfiguration() error {
	if tp.buildErr != nil {
		return tp.buildErr
	}
	for _, repeat := range tp.repeats {
		if repeat.sub == nil {
			return fmt.Errorf("%w: Repeat requires a non-nil body", ErrInvalidRepeat)
		}
		if !repeat.untilSet && !repeat.timesSet && !repeat.allowUnbounded {
			return fmt.Errorf(
				"%w: Repeat requires Until, Times, or AllowUnboundedRepeat",
				ErrInvalidRepeat,
			)
		}
	}
	return nil
}

// Navigation Steps

// Out moves to outgoing neighbor nodes, optionally filtering by edge label.
// Label filters use exact storage ranges. Multiple labels are deduplicated and
// returned in adjacency-key order, independently of argument order.
//
// Usage:
//
//	g.V().Out("KNOWS")...
func (tp *TraversalPipeline) Out(labels ...string) *TraversalPipeline {
	tp.closeActiveRepeat()
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		// Out() moves to neighbor nodes.
		// We flatten the stream of EdgeIterators and wrap them in neighborIterator.

		// Step 1: Flatten Node -> EdgeIterator
		flat := newFlatMapEdgeIterator(tx, prev, func(id uuid.UUID) Iterator {
			return tx.OutEdges(id, labels...)
		})

		// Step 2: Wrap EdgeIterator -> NodeIterator (Neighbor)
		return newNeighborIterator(tx, flat, "out")
	})
	return tp
}

// In moves to incoming neighbor nodes, optionally filtering by edge label.
// Label filters use exact storage ranges. Multiple labels are deduplicated and
// returned in adjacency-key order, independently of argument order.
//
// Usage:
//
//	g.V().In("EMPLOYED_BY")...
func (tp *TraversalPipeline) In(labels ...string) *TraversalPipeline {
	tp.closeActiveRepeat()
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		flat := newFlatMapEdgeIterator(tx, prev, func(id uuid.UUID) Iterator {
			return tx.InEdges(id, labels...)
		})
		return newNeighborIterator(tx, flat, "in")
	})
	return tp
}

// HasLabel filters the current node stream, keeping only nodes with one of the
// specified labels.
func (tp *TraversalPipeline) HasLabel(labels ...string) *TraversalPipeline {
	tp.closeActiveRepeat()
	labelSet := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		labelSet[label] = struct{}{}
	}
	tp.steps = append(tp.steps, func(_ *Tx, prev Iterator) Iterator {
		return newNodeFilterIterator(prev, func(node Node) bool {
			_, matches := labelSet[node.Label]
			return matches
		})
	})
	return tp
}

// Until terminates the active Repeat when pred returns true for the current
// materialized Node. It must immediately follow Repeat or another repeat
// modifier. A nil predicate or invalid ordering returns ErrInvalidRepeat when
// the traversal executes.
func (tp *TraversalPipeline) Until(pred NodePredicate) *TraversalPipeline {
	repeat := tp.repeatForModifier("Until")
	if repeat == nil {
		return tp
	}
	if repeat.untilSet {
		tp.setRepeatError("Until may be specified only once")
		return tp
	}
	if pred == nil {
		tp.setRepeatError("Until requires a non-nil predicate")
		return tp
	}
	if repeat.allowUnbounded {
		tp.setRepeatError("Until cannot be combined with AllowUnboundedRepeat")
		return tp
	}
	repeat.until = pred
	repeat.untilSet = true
	return tp
}

// Recursion Steps

// Repeat repeats the provided sub-traversal. Every Repeat must be terminated
// with Until or Times unless AllowUnboundedRepeat is explicitly selected.
//
// Usage:
//
//	// 2-hop friends
//	g.V().Repeat(func(t *TraversalPipeline) *TraversalPipeline {
//		return t.Out("KNOWS")
//	}).Times(2)
func (tp *TraversalPipeline) Repeat(sub func(*TraversalPipeline) *TraversalPipeline) *TraversalPipeline {
	tp.closeActiveRepeat()
	conf := &RepeatConfig{sub: sub}
	tp.activeRepeat = conf
	tp.repeats = append(tp.repeats, conf)
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		return newRepeatIterator(tx, prev, conf)
	})
	return tp
}

// Times terminates the active Repeat after n iterations. It must immediately
// follow Repeat or another repeat modifier, and n must be positive. Invalid
// configuration returns ErrInvalidRepeat when the traversal executes.
func (tp *TraversalPipeline) Times(n int) *TraversalPipeline {
	repeat := tp.repeatForModifier("Times")
	if repeat == nil {
		return tp
	}
	if repeat.timesSet {
		tp.setRepeatError("Times may be specified only once")
		return tp
	}
	if n <= 0 {
		tp.setRepeatError("Times requires a positive iteration count")
		return tp
	}
	if repeat.allowUnbounded {
		tp.setRepeatError("Times cannot be combined with AllowUnboundedRepeat")
		return tp
	}
	repeat.times = n
	repeat.timesSet = true
	return tp
}

// Emit causes the active Repeat to emit the current element at each iteration,
// effectively returning intermediate results as well as final results. Emit is
// not a termination policy and must immediately follow Repeat or another repeat
// modifier.
func (tp *TraversalPipeline) Emit() *TraversalPipeline {
	repeat := tp.repeatForModifier("Emit")
	if repeat == nil {
		return tp
	}
	if repeat.emitSet {
		tp.setRepeatError("Emit may be specified only once")
		return tp
	}
	repeat.emit = true
	repeat.emitSet = true
	return tp
}

// AllowUnboundedRepeat explicitly permits the active Repeat to run without an
// Until predicate or Times limit. It must immediately follow Repeat or another
// repeat modifier and cannot be combined with a termination policy. Callers
// should execute unbounded repeats with a cancellable context or stopping
// callback.
func (tp *TraversalPipeline) AllowUnboundedRepeat() *TraversalPipeline {
	repeat := tp.repeatForModifier("AllowUnboundedRepeat")
	if repeat == nil {
		return tp
	}
	if repeat.allowUnbounded {
		tp.setRepeatError("AllowUnboundedRepeat may be specified only once")
		return tp
	}
	if repeat.untilSet || repeat.timesSet {
		tp.setRepeatError("AllowUnboundedRepeat cannot be combined with Until or Times")
		return tp
	}
	repeat.allowUnbounded = true
	return tp
}

// Path transforms the current stream into immutable Path values containing the
// ordered nodes and edges visited for each result. Collect them with ToPaths or
// stream them with EachPath.
// Usage:
//
//	g.V().Out().Path()
func (tp *TraversalPipeline) Path() *TraversalPipeline {
	tp.closeActiveRepeat()
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		// Path step just materializes the path into the value stream?
		// Or strictly exposes path history?
		// Usually Path() maps the current object to its Path.
		// So validation: return an iterator where Value() IS the Path.
		return newPathIterator(prev)
	})
	return tp
}

// Projection Steps

// IDs projects UUIDs from the current node stream. Unlike materializing nodes
// with ToNodes, this projection does not load node labels. Collect the result
// with ToIDs or stream it with EachID.
func (tp *TraversalPipeline) IDs() *TraversalPipeline {
	tp.closeActiveRepeat()
	tp.steps = append(tp.steps, func(_ *Tx, prev Iterator) Iterator {
		return newIDIterator(prev)
	})
	return tp
}

// Values projects properties from the current elements. It emits one typed
// scalar per requested key, in key order. Missing properties are omitted, and
// calling Values without keys produces an empty result stream.
func (tp *TraversalPipeline) Values(keys ...string) *TraversalPipeline {
	tp.closeActiveRepeat()
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		return newValueIterator(tx, prev, keys)
	})
	return tp
}

// Terminal Steps

// Each executes the traversal pipeline and calls yield once for every result.
// It streams results without retaining them, so memory use does not grow with
// the traversal size. Returning an error from yield stops iteration; the error
// is returned after all traversal resources have been closed. Cancellation is
// checked while building and advancing the traversal.
//
// Unprojected nodes and edges are passed as Node and Edge values. IDs emits
// uuid.UUID, Path emits Path, and Values emits the dynamic property types
// documented by Predicate. The values passed to yield own their data and
// remain valid after yield returns. Prefer a typed EachNode, EachEdge,
// EachPath, or EachID terminal for statically typed results.
func (tp *TraversalPipeline) Each(ctx context.Context, yield func(any) error) (err error) {
	return tp.each(ctx, yield, nil)
}

// EachNode executes a node traversal and calls yield for every Node. It
// returns ErrTraversalResult when the final pipeline step emits another type.
func (tp *TraversalPipeline) EachNode(ctx context.Context, yield func(Node) error) error {
	return eachTyped(tp, ctx, resultShapeNode, "pathway.Node", yield)
}

// EachEdge executes an edge traversal and calls yield for every Edge. It
// returns ErrTraversalResult when the final pipeline step emits another type.
func (tp *TraversalPipeline) EachEdge(ctx context.Context, yield func(Edge) error) error {
	return eachTyped(tp, ctx, resultShapeEdge, "pathway.Edge", yield)
}

// EachPath executes a traversal ending in Path and calls yield for every Path.
// It returns ErrTraversalResult when the final pipeline step emits another
// type.
func (tp *TraversalPipeline) EachPath(ctx context.Context, yield func(Path) error) error {
	return eachTyped(tp, ctx, resultShapePath, "pathway.Path", yield)
}

// EachID executes a traversal ending in IDs and calls yield for every UUID. It
// returns ErrTraversalResult when the final pipeline step emits another type.
func (tp *TraversalPipeline) EachID(ctx context.Context, yield func(uuid.UUID) error) error {
	return eachTyped(tp, ctx, resultShapeID, "uuid.UUID", yield)
}

func eachTyped[T any](
	tp *TraversalPipeline,
	ctx context.Context,
	shape traversalResultShape,
	expected string,
	yield func(T) error,
) error {
	if yield == nil {
		return errors.New("pathway: nil traversal callback")
	}
	return tp.each(ctx, func(value any) error {
		typed, ok := value.(T)
		if !ok {
			return fmt.Errorf("%w: got %T, want %s", ErrTraversalResult, value, expected)
		}
		return yield(typed)
	}, requireResultShape(shape, expected))
}

func (tp *TraversalPipeline) each(
	ctx context.Context,
	yield func(any) error,
	iteratorReady func(Iterator) error,
) (err error) {
	if tp.db == nil {
		return ErrInvalidDatabase
	}
	if ctx == nil {
		return errors.New("pathway: nil traversal context")
	}
	if yield == nil {
		return errors.New("pathway: nil traversal callback")
	}
	if err := tp.validateRepeatConfiguration(); err != nil {
		return err
	}

	queryDesc := "Traversal Query"
	if tp.db.options.OnQueryStart != nil {
		tp.db.options.OnQueryStart(ctx, queryDesc)
	}
	start := time.Now()
	defer func() {
		if tp.db.options.OnQueryEnd != nil {
			tp.db.options.OnQueryEnd(ctx, queryDesc, time.Since(start), err)
		}
	}()

	if err = ctx.Err(); err != nil {
		return err
	}

	tx, err := tp.db.NewReadTx(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()

	var iter Iterator
	defer func() {
		if iter != nil {
			err = errors.Join(err, iter.Close())
		}
	}()

	for _, step := range tp.steps {
		if err = ctx.Err(); err != nil {
			return err
		}
		next := step(tx, iter)
		if next == nil {
			return errors.New("pipeline step returned nil iterator")
		}
		iter = next
	}
	if iter == nil {
		return errors.New("pipeline contains no traversal steps")
	}
	if iteratorReady != nil {
		if err = iteratorReady(iter); err != nil {
			return err
		}
	}

	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		if !iter.Next() {
			break
		}
		if err = ctx.Err(); err != nil {
			return err
		}

		var result any
		switch current := iter.(type) {
		case resultIterator:
			result, err = current.Result()
		case NodeIterator:
			var id uuid.UUID
			var label string
			id, label, err = current.Node()
			result = Node{ID: id, Label: label}
		case EdgeIterator:
			var id, other uuid.UUID
			var label string
			id, other, label, err = current.Edge()
			result = Edge{ID: id, Other: other, Label: label}
		default:
			result = append([]byte(nil), iter.Key()...)
		}
		if err != nil {
			return err
		}
		if err = yield(result); err != nil {
			return err
		}
	}

	if err = iter.Error(); err != nil {
		return err
	}
	return ctx.Err()
}

// ToList executes the traversal pipeline and returns dynamic materialized
// results. Nodes use Node, edges use Edge, IDs uses uuid.UUID, Path uses Path,
// and Values uses the property types documented by Predicate. Prefer ToNodes,
// ToEdges, ToIDs, or ToPaths for statically typed results. To consume dynamic
// results with bounded memory, use Each.
func (tp *TraversalPipeline) ToList() (results []any, err error) {
	return tp.ToListContext(context.Background())
}

// ToListContext executes the traversal pipeline with ctx and returns
// materialized results. Cancellation stops planning, reads, scans, repeats,
// and result collection, and is propagated to observability hooks.
func (tp *TraversalPipeline) ToListContext(ctx context.Context) (results []any, err error) {
	resultCapacity := 16
	err = tp.each(ctx, func(value any) error {
		if results == nil {
			results = make([]any, 0, resultCapacity)
		}
		results = append(results, value)
		return nil
	}, func(iter Iterator) error {
		if hinted, ok := iter.(resultSizeHinter); ok {
			if hint := hinted.ResultSizeHint(); hint > 0 {
				resultCapacity = hint
			}
		}
		return nil
	})
	return results, err
}

// ToNodes executes a node traversal and returns its materialized Node values.
// It returns ErrTraversalResult when the final pipeline step emits another
// type. Use EachNode to consume large node result sets with bounded memory.
func (tp *TraversalPipeline) ToNodes() ([]Node, error) {
	return tp.ToNodesContext(context.Background())
}

// ToNodesContext is ToNodes with cancellation and observability context.
func (tp *TraversalPipeline) ToNodesContext(ctx context.Context) ([]Node, error) {
	return collectTyped[Node](tp, ctx, resultShapeNode, "pathway.Node")
}

// ToEdges executes an edge traversal and returns its materialized Edge values.
// It returns ErrTraversalResult when the final pipeline step emits another
// type. Use EachEdge to consume large edge result sets with bounded memory.
func (tp *TraversalPipeline) ToEdges() ([]Edge, error) {
	return tp.ToEdgesContext(context.Background())
}

// ToEdgesContext is ToEdges with cancellation and observability context.
func (tp *TraversalPipeline) ToEdgesContext(ctx context.Context) ([]Edge, error) {
	return collectTyped[Edge](tp, ctx, resultShapeEdge, "pathway.Edge")
}

// ToPaths executes a traversal ending in Path and returns typed path snapshots.
// It returns ErrTraversalResult when the final pipeline step emits another
// type. Use EachPath to consume large path result sets with bounded memory.
func (tp *TraversalPipeline) ToPaths() ([]Path, error) {
	return tp.ToPathsContext(context.Background())
}

// ToPathsContext is ToPaths with cancellation and observability context.
func (tp *TraversalPipeline) ToPathsContext(ctx context.Context) ([]Path, error) {
	return collectTyped[Path](tp, ctx, resultShapePath, "pathway.Path")
}

// ToIDs executes a traversal ending in IDs and returns typed UUID values. It
// returns ErrTraversalResult when the final pipeline step emits another type.
// Use EachID to consume large ID result sets with bounded memory.
func (tp *TraversalPipeline) ToIDs() ([]uuid.UUID, error) {
	return tp.ToIDsContext(context.Background())
}

// ToIDsContext is ToIDs with cancellation and observability context.
func (tp *TraversalPipeline) ToIDsContext(ctx context.Context) ([]uuid.UUID, error) {
	return collectTyped[uuid.UUID](tp, ctx, resultShapeID, "uuid.UUID")
}

func collectTyped[T any](
	tp *TraversalPipeline,
	ctx context.Context,
	shape traversalResultShape,
	expected string,
) (results []T, err error) {
	resultCapacity := 16
	err = tp.each(ctx, func(value any) error {
		typed, ok := value.(T)
		if !ok {
			return fmt.Errorf("%w: got %T, want %s", ErrTraversalResult, value, expected)
		}
		if results == nil {
			results = make([]T, 0, resultCapacity)
		}
		results = append(results, typed)
		return nil
	}, func(iter Iterator) error {
		if err := validateResultShape(iter, shape, expected); err != nil {
			return err
		}
		if hinted, ok := iter.(resultSizeHinter); ok {
			if hint := hinted.ResultSizeHint(); hint > 0 {
				resultCapacity = hint
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func requireResultShape(shape traversalResultShape, expected string) func(Iterator) error {
	return func(iter Iterator) error {
		return validateResultShape(iter, shape, expected)
	}
}

func validateResultShape(iter Iterator, expected traversalResultShape, name string) error {
	actual := resultShapeDynamic
	switch current := iter.(type) {
	case resultIterator:
		actual = current.ResultShape()
	case NodeIterator:
		actual = resultShapeNode
	case EdgeIterator:
		actual = resultShapeEdge
	}
	if actual != expected {
		return fmt.Errorf(
			"%w: got %s, want %s",
			ErrTraversalResult,
			resultShapeName(actual),
			name,
		)
	}
	return nil
}

func resultShapeName(shape traversalResultShape) string {
	switch shape {
	case resultShapeNode:
		return "pathway.Node"
	case resultShapeEdge:
		return "pathway.Edge"
	case resultShapeID:
		return "uuid.UUID"
	case resultShapePath:
		return "pathway.Path"
	default:
		return "dynamic value"
	}
}
