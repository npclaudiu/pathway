package pathway

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

// Logger defines a simple logging interface.
type Logger interface {
	Infof(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

// Options configuration for the database.
type Options struct {
	// OnQueryStart is called before a query executes.
	OnQueryStart func(ctx context.Context, query string)

	// OnQueryEnd is called after execution, providing duration and error/success status.
	OnQueryEnd func(ctx context.Context, query string, duration time.Duration, err error)

	// Logger interface for internal debug logs.
	Logger Logger

	// PebbleOptions allows customizing the underlying storage engine.
	PebbleOptions *pebble.Options
}

// Database represents a connection to the embedded graph database.
type Database struct {
	db       *pebble.DB
	nextTxID atomic.Uint64
	options  Options
}

// Open creates or opens a graph database at the given path with default options.
// Use OpenWithOptions for custom configuration.
func Open(path string) (*Database, error) {
	return OpenWithOptions(path, Options{})
}

// OpenWithOptions opens the database with specific options.
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

// Close closes the database connection.
func (d *Database) Close() error {
	return d.db.Close()
}

// Update executes a function within a read-write transaction.
// If the function returns an error, the transaction is rolled back.
func (d *Database) Update(ctx context.Context, fn func(tx *Tx) error) error {
	batch := d.db.NewIndexedBatch()
	defer batch.Close()

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
// The transaction is automatically rolled back after the function returns.
func (d *Database) View(ctx context.Context, fn func(tx *Tx) error) error {
	snapshot := d.db.NewSnapshot()
	defer snapshot.Close()

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
// It can be used to reduce space usage after large deletions.
func (g *Database) Compact(ctx context.Context) error {
	if g == nil || g.db == nil {
		return ErrInvalidDatabase
	}
	// Pebble's Compact compacts the specified range.
	// Start and end are nil to indicate the full range.
	return g.db.Compact(nil, nil, true)
}
