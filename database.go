package pathway

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/vfs"
)

// Logger defines a simple logging interface for internal database logs.
// It allows users to plug in their own logging implementation (e.g., standard log, zap, logrus).
type Logger interface {
	// Infof logs a formatted information message.
	Infof(format string, args ...interface{})
	// Errorf logs a formatted error message.
	Errorf(format string, args ...interface{})
}

// Options configuration for the database.
//
// Example:
//
//	opts := pathway.Options{
//	    OnQueryStart: func(ctx context.Context, query string) {
//	        fmt.Printf("Starting query: %s\n", query)
//	    },
//	}
type Options struct {
	// OnQueryStart is called before a query executes.
	// This is useful for auditing or tracing query execution.
	OnQueryStart func(ctx context.Context, query string)

	// OnQueryEnd is called after execution, providing duration and error/success status.
	// This is useful for monitoring performance and logging slow queries.
	OnQueryEnd func(ctx context.Context, query string, duration time.Duration, err error)

	// Logger interface for internal debug logs.
	// If nil, no internal logging will be performed.
	Logger Logger

	// PebbleOptions allows customizing the underlying storage engine (cockroachdb/pebble).
	// Use this to tune cache sizes, compaction settings, or file system options.
	PebbleOptions *pebble.Options
}

// Database represents a connection to the embedded graph database.
// It is safe for concurrent use by multiple goroutines.
type Database struct {
	db       *pebble.DB
	nextTxID atomic.Uint64
	options  Options
}

// Open creates or opens a graph database at the given path with default options.
//
// Usage:
//
//	// Open a database on disk
//	db, err := pathway.Open("data/graph.db")
//
//	// Open an in-memory database (useful for testing)
//	db, err := pathway.Open(":memory:")
func Open(path string) (*Database, error) {
	return OpenWithOptions(path, Options{})
}

// OpenWithOptions opens the database with specific options.
// This allows configuration of logging, monitoring hooks, and underlying storage engine settings.
func OpenWithOptions(path string, opts Options) (*Database, error) {
	pOpts := opts.PebbleOptions
	if pOpts == nil {
		pOpts = &pebble.Options{}
	}
	if path == ":memory:" {
		pOpts.FS = vfs.NewMem()
		path = ""
	}
	db, err := pebble.Open(path, pOpts)
	if err != nil {
		return nil, err
	}
	return &Database{db: db, options: opts}, nil
}

// Close closes the database connection and releases all resources.
// It is important to call Close() to ensure all data is flushed to disk (if persistent)
// and locks are released.
func (d *Database) Close() error {
	return d.db.Close()
}

// Update executes a function within a read-write transaction.
// The transaction is committed if the function returns nil, or rolled back if it returns an error.
//
// Usage:
//
//	err := db.Update(ctx, func(tx *pathway.Tx) error {
//	    return tx.PutNode(uuid.New(), "Person")
//	})
func (d *Database) Update(ctx context.Context, fn func(tx *Tx) error) error {
	batch := d.db.NewIndexedBatch()
	defer func() { _ = batch.Close() }()

	tx := &Tx{
		db:       d,
		ctx:      ctx,
		batch:    batch,
		readOnly: false,
		id:       d.nextTxID.Add(1),
	}

	if err := fn(tx); err != nil {
		return err // Rollback is implicit as batch is not applied
	}

	return batch.Commit(pebble.Sync)
}

// View executes a function within a read-only transaction.
// The transaction provides a consistent snapshot of the database at the start of the call.
// It is automatically rolled back after the function returns.
//
// Usage:
//
//	err := db.View(ctx, func(tx *pathway.Tx) error {
//	    node, exists, err := tx.GetNode(id)
//	    // ...
//	    return nil
//	})
func (d *Database) View(ctx context.Context, fn func(tx *Tx) error) error {
	snapshot := d.db.NewSnapshot()
	defer func() { _ = snapshot.Close() }()

	tx := &Tx{
		db:       d,
		ctx:      ctx,
		reader:   snapshot,
		readOnly: true,
		id:       d.nextTxID.Add(1),
	}

	return fn(tx)
}

// NewReadTx creates a new read-only transaction.
// The caller is responsible for calling Tx.Close() on the returned transaction.
// Consider using View() instead which manages the transaction lifecycle automatically.
func (d *Database) NewReadTx(ctx context.Context) (*Tx, error) {
	snapshot := d.db.NewSnapshot()
	tx := &Tx{
		db:       d,
		ctx:      ctx,
		reader:   snapshot,
		readOnly: true,
		id:       d.nextTxID.Add(1),
	}
	return tx, nil
}

// Compact triggers Pebble's manual compaction for the entire key range.
// It can be used to reclaim disk space after deleting a large number of nodes or edges.
// Note: This operation can be expensive and should typically not be run during high load.
func (g *Database) Compact(ctx context.Context) error {
	if g == nil || g.db == nil {
		return ErrInvalidDatabase
	}
	// Pebble's Compact compacts the specified range.
	// Start and end are nil to indicate the full range.
	return g.db.Compact(ctx, nil, nil, true)
}
