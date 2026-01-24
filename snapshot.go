package pathway

import (
	"context"

	"github.com/cockroachdb/pebble"
)

// Snapshot represents a read‑only view of the database at a point in time.
// It holds a Pebble snapshot which guarantees a consistent view even while
// writes are occurring.
type Snapshot struct {
	snap *pebble.Snapshot
}

// NewSnapshot creates a snapshot of the current database state. The snapshot
// is tied to the lifetime of the underlying Graph (Database) instance.
func (g *Database) NewSnapshot(ctx context.Context) (*Snapshot, error) {
	if g == nil || g.db == nil {
		return nil, ErrInvalidDatabase
	}
	snap := g.db.NewSnapshot()
	return &Snapshot{snap: snap}, nil
}

// Close releases the snapshot resources.
func (s *Snapshot) Close() error {
	if s == nil || s.snap == nil {
		return nil
	}
	s.snap.Close()
	return nil
}

// Get returns a Pebble reader that can be used to read keys from the snapshot.
func (s *Snapshot) Get(key []byte) ([]byte, error) {
	if s == nil || s.snap == nil {
		return nil, ErrInvalidSnapshot
	}
	val, closer, err := s.snap.Get(key)
	if err == pebble.ErrNotFound {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	// Must copy
	ret := make([]byte, len(val))
	copy(ret, val)
	return ret, nil
}
