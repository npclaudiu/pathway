package pathway

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBulkUpdateWritesGraphAtomically(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Indexes: []IndexDefinition{
		{Label: "User", Property: "name"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	var edgeIDs []uuid.UUID
	err = db.BulkUpdate(ctx, func(writer *BulkWriter) error {
		for id, name := range map[uuid.UUID]string{alice: "Alice", bob: "Bob"} {
			if err := writer.PutNode(id, "User"); err != nil {
				return err
			}
			if err := writer.SetProperties(id, map[string]interface{}{"name": name}); err != nil {
				return err
			}
		}
		for i := 0; i < 2; i++ {
			edgeID, err := writer.PutEdge(alice, bob, "FOLLOWS")
			if err != nil {
				return err
			}
			if err := writer.SetProperties(edgeID, map[string]interface{}{"rank": i}); err != nil {
				return err
			}
			edgeIDs = append(edgeIDs, edgeID)
		}
		if got := len(writer.nodeExistence); got != 2 {
			t.Fatalf("cached endpoint count = %d, want 2", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertIndexedNode(t, db, "User", "name", "Alice", alice)
	if err := db.View(ctx, func(tx *Tx) error {
		iter := tx.OutEdges(alice, "FOLLOWS")
		defer closeTestResource(t, iter)
		seen := make(map[uuid.UUID]struct{})
		for iter.Next() {
			edgeID, otherID, _, err := iter.Edge()
			if err != nil {
				return err
			}
			if otherID != bob {
				t.Fatalf("edge target = %s, want %s", otherID, bob)
			}
			seen[edgeID] = struct{}{}
		}
		if err := iter.Error(); err != nil {
			return err
		}
		if len(seen) != len(edgeIDs) {
			t.Fatalf("persisted edges = %d, want %d", len(seen), len(edgeIDs))
		}
		for i, edgeID := range edgeIDs {
			props, err := tx.GetProperties(edgeID)
			if err != nil {
				return err
			}
			if props["rank"] != float64(i) {
				t.Fatalf("edge %d rank = %v, want %d", i, props["rank"], i)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBulkUpdateCachesExistingEndpoints(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	srcID, dstID := uuid.New(), uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(srcID, "User"); err != nil {
			return err
		}
		return tx.PutNode(dstID, "User")
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.BulkUpdate(ctx, func(writer *BulkWriter) error {
		if _, err := writer.PutEdge(srcID, dstID, "FOLLOWS"); err != nil {
			return err
		}
		if got := len(writer.nodeExistence); got != 2 {
			t.Fatalf("cache size after first edge = %d, want 2", got)
		}
		if _, err := writer.PutEdge(srcID, dstID, "FOLLOWS"); err != nil {
			return err
		}
		if got := len(writer.nodeExistence); got != 2 {
			t.Fatalf("cache size after shared endpoints = %d, want 2", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBulkUpdateRollsBackIgnoredWriterError(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	id := uuid.New()

	err = db.BulkUpdate(ctx, func(writer *BulkWriter) error {
		if err := writer.PutNode(id, "User"); err != nil {
			return err
		}
		// BulkWriter remembers operation failures, so even an accidentally
		// ignored error prevents a partial commit.
		_, _ = writer.PutEdge(id, uuid.New(), "FOLLOWS")
		return nil
	})
	if !errors.Is(err, ErrDanglingEdge) {
		t.Fatalf("BulkUpdate error = %v, want ErrDanglingEdge", err)
	}
	if err := db.View(ctx, func(tx *Tx) error {
		_, exists, err := tx.GetNode(id)
		if err != nil {
			return err
		}
		if exists {
			t.Fatal("node from failed bulk update was committed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
