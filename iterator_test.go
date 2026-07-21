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

func TestNodeIteratorsLoadLabelsLazily(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx := context.Background()
	source, target := uuid.New(), uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(source, "Source"); err != nil {
			return err
		}
		if err := tx.PutNode(target, "Target"); err != nil {
			return err
		}
		_, err := tx.PutEdge(source, target, "LINK")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := db.NewReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, tx)

	fixed := newFixedNodeIterator(tx, []uuid.UUID{source})
	if !fixed.Next() {
		t.Fatalf("fixed iterator did not advance: %v", fixed.Error())
	}
	if fixed.labelLoaded {
		t.Fatal("fixed iterator eagerly loaded its label")
	}
	if id, err := fixed.NodeID(); err != nil || id != source {
		t.Fatalf("fixed NodeID() = %s, %v; want %s, nil", id, err, source)
	}
	if fixed.labelLoaded {
		t.Fatal("fixed NodeID loaded its label")
	}
	if _, label, err := fixed.Node(); err != nil || label != "Source" {
		t.Fatalf("fixed Node() label = %q, %v; want Source, nil", label, err)
	}
	if !fixed.labelLoaded {
		t.Fatal("fixed Node did not cache its label")
	}

	neighbor := newNeighborIterator(tx, tx.OutEdges(source), "out")
	if !neighbor.Next() {
		t.Fatalf("neighbor iterator did not advance: %v", neighbor.Error())
	}
	if neighbor.labelLoaded {
		t.Fatal("neighbor iterator eagerly loaded its label")
	}
	if id, err := neighbor.NodeID(); err != nil || id != target {
		t.Fatalf("neighbor NodeID() = %s, %v; want %s, nil", id, err, target)
	}
	if neighbor.labelLoaded {
		t.Fatal("neighbor NodeID loaded its label")
	}
	if _, label, err := neighbor.Node(); err != nil || label != "Target" {
		t.Fatalf("neighbor Node() label = %q, %v; want Target, nil", label, err)
	}
	if !neighbor.labelLoaded {
		t.Fatal("neighbor Node did not cache its label")
	}
	closeTestResource(t, neighbor)
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
