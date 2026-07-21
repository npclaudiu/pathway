package pathway

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Traversal represents a fluent query builder.
// Currently empty but kept for type safety and potential future state.
type Traversal struct {
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
//	g.V("uuid-string").Out()...
//
//	// Start at all nodes (scan)
//	g.V().HasLabel("Person")...
func (ts *TraversalSource) V(ids ...string) *TraversalPipeline {
	return &TraversalPipeline{
		db: ts.db,
		steps: []Step{
			func(tx *Tx, _ Iterator) Iterator {
				if len(ids) == 0 {
					return tx.ScanNodes()
				} else {
					uids := make([]uuid.UUID, 0, len(ids))
					for _, s := range ids {
						if id, err := uuid.Parse(s); err == nil {
							uids = append(uids, id)
						}
					}
					return newFixedNodeIterator(tx, uids)
				}
			},
		},
	}
}

// Step defines a processing step in the traversal pipeline.
// It takes a transaction context and a previous iterator, and returns a new iterator.
type Step func(tx *Tx, prev Iterator) Iterator

// RepeatConfig holds configuration for repeat steps (loops).
type RepeatConfig struct {
	sub   func(*TraversalPipeline) *TraversalPipeline
	until Predicate
	times int
	emit  bool
}

// TraversalPipeline represents a chain of query steps.
type TraversalPipeline struct {
	db           *Database
	steps        []Step
	activeRepeat *RepeatConfig
}

// Navigation Steps

// Out moves to outgoing neighbor nodes, optionally filtering by edge label.
//
// Usage:
//
//	g.V().Out("KNOWS")...
func (tp *TraversalPipeline) Out(labels ...string) *TraversalPipeline {
	// If we have an active repeat config effectively "open", calling another step "closes" it?
	// Gremlin logic: .repeat(...).out(). "out" follows the repeat block.
	// So yes, ensure activeRepeat is closed implicitly if user forgot Until?
	// Standard Gremlin: repeat() loops forever if no termination.
	// We'll leave it open for Until/Times, but subsequent non-modifier calling implies end.
	// But honestly, it's safer to just let activeRepeat hang?
	// No, if I call .Out(), it appends a step. Repeat step was ALREADY appended.
	// Modifiers like Until modify the *last* step if it is Repeat.
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
//
// Usage:
//
//	g.V().In("EMPLOYED_BY")...
func (tp *TraversalPipeline) In(labels ...string) *TraversalPipeline {
	tp.activeRepeat = nil
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		flat := newFlatMapEdgeIterator(tx, prev, func(id uuid.UUID) Iterator {
			return tx.InEdges(id, labels...)
		})
		return newNeighborIterator(tx, flat, "in")
	})
	return tp
}

// HasLabel filters the current stream of elements, keeping only those with the specified label(s).
func (tp *TraversalPipeline) HasLabel(labels ...string) *TraversalPipeline {
	tp.activeRepeat = nil
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		// Filter logic
		return newFilterIterator(prev, func(val interface{}) bool {
			// We need to know if val is Node or Edge and check label
			// iterators return typed data via interface methods?
			// Generic Iterator returns Key/Value access?
			// Let's rely on extracting objects for now.
			// This requires identifying if current stream is Nodes or Edges.
			// Complex. For V1 simplification: assume NodeIterator for HasLabel after V()?
			// But after Out(), stream is Edges? Spec says Out() moves to Neighbor Nodes.
			// Ah: Out() -> Neighbor Nodes. OutE() -> Incident Edges.
			// So Out() stream is Nodes.

			// Simplest: Check if item has Label() method or struct field?
			// Our iterators (NodeIterator/EdgeIterator) have distinct methods.
			// We might need a unified "Element" interface for items in stream?
			// Or cast.
			if s, ok := val.(struct {
				ID    uuid.UUID
				Label string
			}); ok {
				for _, l := range labels {
					if s.Label == l {
						return true
					}
				}
			}
			return false
		})
	})
	return tp
}

// Recursion Steps

// Repeat repeats the provided sub-traversal.
// It is used in conjunction with Until(), Times(), or Emit() to control the loop.
//
// Usage:
//
//	// 2-hop friends
//	g.V().Repeat(func(t *TraversalPipeline) { return t.Out("KNOWS") }).Times(2)
func (tp *TraversalPipeline) Repeat(sub func(*TraversalPipeline) *TraversalPipeline) *TraversalPipeline {
	conf := &RepeatConfig{sub: sub}
	tp.activeRepeat = conf
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		return newRepeatIterator(tx, prev, conf)
	})
	return tp
}

// Until terminates a Repeat loop when the predicate is true for the current element.
func (tp *TraversalPipeline) Until(pred Predicate) *TraversalPipeline {
	if tp.activeRepeat != nil {
		tp.activeRepeat.until = pred
	}
	// Don't close activeRepeat yet?
	// Usually modifiers can be chained: .Repeat().Times().Until().
	// We close implicitly at next step or explicitly?
	// Let's keep it open until a non-modifier step is added.
	return tp
}

// Times terminates a Repeat loop after a fixed number of iterations.
func (tp *TraversalPipeline) Times(n int) *TraversalPipeline {
	if tp.activeRepeat != nil {
		tp.activeRepeat.times = n
	}
	return tp
}

// Emit causes the Repeat loop to emit the current element at each iteration,
// effectively returning intermediate results as well as the final results.
func (tp *TraversalPipeline) Emit() *TraversalPipeline {
	if tp.activeRepeat != nil {
		tp.activeRepeat.emit = true
	}
	return tp
}

// Path transforms the current stream into immutable Path values containing the
// ordered nodes and edges visited for each result.
// Usage:
//
//	g.V().Out().Path()
func (tp *TraversalPipeline) Path() *TraversalPipeline {
	tp.activeRepeat = nil
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

// Values projects properties from the current elements. It emits one typed
// scalar per requested key, in key order. Missing properties are omitted, and
// calling Values without keys produces an empty result stream.
func (tp *TraversalPipeline) Values(keys ...string) *TraversalPipeline {
	tp.activeRepeat = nil
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		return newValueIterator(tx, prev, keys)
	})
	return tp
}

// Terminal Steps

// ToList executes the traversal pipeline and returns the results as a list.
// This triggers the actual database transaction.
func (tp *TraversalPipeline) ToList() (results []interface{}, err error) {
	if tp.db == nil {
		return nil, ErrInvalidDatabase
	}

	// Observability Hooks
	// We construct a query descriptor (placeholder for now)
	queryDesc := "Traversal Query"
	ctx := context.Background() // TODO: Accept context in ToList? Spec suggests Context support (Sec 3). The method signature currently is ToList() without args.
	// For now we use Background.

	if tp.db.options.OnQueryStart != nil {
		tp.db.options.OnQueryStart(ctx, queryDesc)
	}
	start := time.Now()

	defer func() {
		if tp.db.options.OnQueryEnd != nil {
			tp.db.options.OnQueryEnd(ctx, queryDesc, time.Since(start), err)
		}
	}()

	// 1. Start Read Tx
	// ToList executions are typically read-only.
	tx, txErr := tp.db.NewReadTx(ctx)
	if txErr != nil {
		err = txErr
		return nil, err
	}
	defer func() { err = errors.Join(err, tx.Close()) }()

	// 2. Execute Pipeline
	var iter Iterator = nil // Start null
	for _, step := range tp.steps {
		iter = step(tx, iter)
		if iter == nil {
			err = errors.New("pipeline step returned nil iterator")
			return nil, err
		}
	}
	defer func() { err = errors.Join(err, iter.Close()) }()

	// 3. Drain Iterator
	for iter.Next() {
		// Extract value
		if ri, ok := iter.(resultIterator); ok {
			value, e := ri.Result()
			if e != nil {
				err = e
				return nil, err
			}
			results = append(results, value)
		} else if ni, ok := iter.(NodeIterator); ok {
			id, lbl, e := ni.Node()
			if e != nil {
				err = e
				return nil, err
			}
			results = append(results, map[string]interface{}{"id": id, "label": lbl, "type": "node"})
		} else if ei, ok := iter.(EdgeIterator); ok {
			id, other, lbl, e := ei.Edge()
			if e != nil {
				err = e
				return nil, err
			}
			results = append(results, map[string]interface{}{"id": id, "other": other, "label": lbl, "type": "edge"})
		} else {
			results = append(results, iter.Key())
		}
	}

	if iter.Error() != nil {
		err = iter.Error()
	}

	return results, err
}
