package pathway

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestIteratorConstructionErrorsAreSafe(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	tx, err := db.NewReadTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}

	edges := []EdgeIterator{tx.OutEdges(uuid.New()), tx.InEdges(uuid.New())}
	for _, iter := range edges {
		assertSafeFailedIterator(t, iter)
		if _, _, _, err := iter.Edge(); err == nil {
			t.Error("Edge did not expose the construction error")
		}
	}

	nodes := []NodeIterator{tx.ScanNodes(), tx.FindNodes("Node", "name", "value")}
	for _, iter := range nodes {
		assertSafeFailedIterator(t, iter)
		if _, _, err := iter.Node(); err == nil {
			t.Error("Node did not expose the construction error")
		}
	}
}

func assertSafeFailedIterator(t *testing.T, iter Iterator) {
	t.Helper()
	if iter.Next() {
		t.Error("failed iterator advanced")
	}
	if iter.SeekGE([]byte("anything")) {
		t.Error("failed iterator seek succeeded")
	}
	if iter.Key() != nil {
		t.Errorf("failed iterator key = %x", iter.Key())
	}
	if iter.Value() != nil {
		t.Errorf("failed iterator value = %x", iter.Value())
	}
	if iter.Valid() {
		t.Error("failed iterator is valid")
	}
	if iter.Path() != nil {
		t.Errorf("failed iterator path = %#v", iter.Path())
	}
	if err := iter.Error(); err == nil {
		t.Error("failed iterator did not expose its error")
	}
	if err := iter.Close(); err != nil {
		t.Errorf("failed iterator close = %v", err)
	}
}
