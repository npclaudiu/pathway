package pathway

import (
	"context"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
)

// BulkWriter stages graph insertions in one atomic Database.BulkUpdate. It
// caches node-existence checks so edges that share endpoints do not repeatedly
// read and decode the same node records. A BulkWriter is valid only during its
// callback and must not be used concurrently.
type BulkWriter struct {
	tx            *Tx
	nodeExistence map[uuid.UUID]bool
	err           error
	closed        bool
}

// BulkUpdate stages nodes, edges, and properties in one transaction and
// commits them once with the database's configured durability. Any writer
// operation or callback error rolls back the complete batch, including errors
// the callback did not explicitly return.
func (d *Database) BulkUpdate(ctx context.Context, fn func(writer *BulkWriter) error) error {
	return d.Update(ctx, func(tx *Tx) error {
		writer := &BulkWriter{
			tx:            tx,
			nodeExistence: make(map[uuid.UUID]bool),
		}
		defer func() { writer.closed = true }()

		if err := fn(writer); err != nil {
			return err
		}
		return writer.err
	})
}

// PutNode stages a node upsert and makes the node immediately available to
// later PutEdge calls in the same bulk callback.
func (w *BulkWriter) PutNode(id uuid.UUID, label string) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.tx.PutNode(id, label); err != nil {
		return w.fail(err)
	}
	w.nodeExistence[id] = true
	return nil
}

// PutEdge stages a directed edge after validating its endpoints. Each distinct
// endpoint is read at most once during the bulk callback; nodes inserted through
// this writer require no additional endpoint read.
func (w *BulkWriter) PutEdge(srcID, dstID uuid.UUID, label string) (uuid.UUID, error) {
	if err := w.ready(); err != nil {
		return uuid.Nil, err
	}
	for _, id := range [...]uuid.UUID{srcID, dstID} {
		exists, err := w.nodeExists(id)
		if err != nil {
			return uuid.Nil, err
		}
		if !exists {
			return uuid.Nil, w.fail(ErrDanglingEdge)
		}
	}
	edgeID, err := w.tx.putEdge(srcID, dstID, label)
	if err != nil {
		return uuid.Nil, w.fail(err)
	}
	return edgeID, nil
}

// SetProperties completely replaces an existing node or edge's properties in
// the current bulk transaction.
func (w *BulkWriter) SetProperties(id uuid.UUID, props map[string]any) error {
	if err := w.ready(); err != nil {
		return err
	}
	if err := w.tx.SetProperties(id, props); err != nil {
		return w.fail(err)
	}
	return nil
}

func (w *BulkWriter) nodeExists(id uuid.UUID) (bool, error) {
	if exists, cached := w.nodeExistence[id]; cached {
		return exists, nil
	}
	_, exists, err := w.tx.GetNode(id)
	if err != nil {
		return false, w.fail(err)
	}
	w.nodeExistence[id] = exists
	return exists, nil
}

func (w *BulkWriter) ready() error {
	if w.closed || w.tx == nil {
		return pebble.ErrClosed
	}
	return w.err
}

func (w *BulkWriter) fail(err error) error {
	if w.err == nil {
		w.err = err
	}
	return w.err
}
