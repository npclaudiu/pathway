package pathway

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Traversal represents a fluent query builder.
type Traversal struct {
	tx   *Tx
	iter Iterator // The current iterator in the pipeline
	err  error
}

// NewTraversalSource creates a new traversal starting point from a transaction.
// Note: Spec says g := graph.NewTraversalSource(db), which implies it manages its own Tx?
// Or we pass an existing Tx?
// The spec example shows: db.Open(); g := NewTraversalSource(db); query := g.V() ... results, err := query.ToList()
// This implies the query execution manages the transaction lifecycle OR we start one implicitly.
// BUT, to prevent long running open transactions during query build, we might want `ToList` to start the Tx?
// However, the `Graph` struct is the entry point.
// Let's attach NewTraversalSource to *Database for now as per spec 13.6 scenario.

type TraversalSource struct {
	db *Database
}

func NewTraversalSource(db *Database) *TraversalSource {
	return &TraversalSource{db: db}
}

// V starts a traversal at specific Nodes or all Nodes.
// This initiates a Read Transaction implicitly if one isn't active?
// Problem: If we chain, we need a Tx.
// Spec 13.2.2 says Traversal holds a reference to Storage Engine.
// Let's make Traversal hold *Database and start Tx on execution?
// OR Source starts Tx immediately?
// Better: ToList() executes. But Steps need access to Tx for schema info?
// Actually, strict iterators only need Tx at execution time.
// But some optimizations (Index Injection) need schema.
// For V1, simplest is: T holds *Database, and Start steps are lazy.
// Wait, Step signature: func(prev Iterator) Iterator.
// This means we build a chain of functions.
// When ToList is called:
// tx = db.BeginRead()
// rootIter = ...
// for each step: iter = step(iter)
// drain(iter)
// tx.Commit/Close()

// But our iterator implementations (edgeIterator) need *Tx (or at least *pebble.Iterator from Tx).
// So steps are closures that take a *Tx (or context) and return Iterator?
// No, Spec says `Step func(prev Iterator) Iterator`.
// This implies prev Iterator is already bound to a Tx.
// So the Chain must operate within an active Tx context.

// Revised Plan:
// Traversal holds a pipeline of `func(Tx, Iterator) Iterator`.
// Execution starts Tx, creates generic "Start" iterator, feeds through pipeline.

type Step func(tx *Tx, prev Iterator) Iterator

type RepeatConfig struct {
	sub   func(*TraversalPipeline) *TraversalPipeline
	until Predicate
	times int
	emit  bool
}

type TraversalPipeline struct {
	db           *Database
	steps        []Step
	activeRepeat *RepeatConfig
}

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

// Navigation Steps

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

func (tp *TraversalPipeline) Repeat(sub func(*TraversalPipeline) *TraversalPipeline) *TraversalPipeline {
	conf := &RepeatConfig{sub: sub}
	tp.activeRepeat = conf
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		return newRepeatIterator(tx, prev, conf)
	})
	return tp
}

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

func (tp *TraversalPipeline) Times(n int) *TraversalPipeline {
	if tp.activeRepeat != nil {
		tp.activeRepeat.times = n
	}
	return tp
}

func (tp *TraversalPipeline) Emit() *TraversalPipeline {
	if tp.activeRepeat != nil {
		tp.activeRepeat.emit = true
	}
	return tp
}

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

func (tp *TraversalPipeline) Values(keys ...string) *TraversalPipeline {
	tp.activeRepeat = nil
	tp.steps = append(tp.steps, func(tx *Tx, prev Iterator) Iterator {
		// Map step: Extract properties
		// Used via flatMap or custom map iterator.
		// For V1, newMapIterator?
		// Let's implement inline or helper later.
		// Placeholder returning prev for now to allow compilation
		return prev
	})
	return tp
}

// Terminal Steps

func (tp *TraversalPipeline) ToList() ([]interface{}, error) {
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

	var err error
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
	defer tx.Close()

	// 2. Execute Pipeline
	var iter Iterator = nil // Start null
	for _, step := range tp.steps {
		iter = step(tx, iter)
		if iter == nil {
			err = errors.New("pipeline step returned nil iterator")
			return nil, err
		}
	}
	defer iter.Close()

	// 3. Drain Iterator
	var results []interface{}
	for iter.Next() {
		// Extract value
		if ni, ok := iter.(NodeIterator); ok {
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

// Helpers for Iterators (Need to be defined)
// - ScanNodes: Tx method
// - fixedNodeIterator: Struct
// - flatMapEdgeIterator: Struct
// - filterIterator: Struct

// To make this file compile, we need placeholders or implement them.
// I will implement placeholders for the helper structs here or in iterators_impl.go and update Tx.
