package pathway

import (
	"context"
	"errors"
	"reflect"
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

func TestPublicIteratorInterfacesExposeOnlySupportedOperations(t *testing.T) {
	tests := []struct {
		name    string
		typeOf  reflect.Type
		methods []string
	}{
		{"Iterator", reflect.TypeOf((*Iterator)(nil)).Elem(), []string{"Close", "Error", "Next"}},
		{"NodeIterator", reflect.TypeOf((*NodeIterator)(nil)).Elem(), []string{"Close", "Error", "Next", "Node"}},
		{"EdgeIterator", reflect.TypeOf((*EdgeIterator)(nil)).Elem(), []string{"Close", "Edge", "Error", "Next"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.typeOf.NumMethod() != len(test.methods) {
				t.Fatalf("method count = %d, want %d", test.typeOf.NumMethod(), len(test.methods))
			}
			for i, want := range test.methods {
				if got := test.typeOf.Method(i).Name; got != want {
					t.Fatalf("method %d = %s, want %s", i, got, want)
				}
			}
		})
	}
}

func TestTraverserQueuePreservesFIFOAndReleasesPoppedState(t *testing.T) {
	var queue traverserQueue
	for i := range 20 {
		queue.push(traverser{
			depth:    i,
			path:     Path{{Kind: PathNode, ID: uuid.New()}},
			ancestry: &repeatAncestry{node: uuid.New()},
		})
	}
	for want := range 10 {
		item, ok := queue.pop()
		if !ok || item.depth != want {
			t.Fatalf("pop = (%d, %v), want (%d, true)", item.depth, ok, want)
		}
	}
	for i := 20; i < 30; i++ {
		queue.push(traverser{
			depth:    i,
			path:     Path{{Kind: PathNode, ID: uuid.New()}},
			ancestry: &repeatAncestry{node: uuid.New()},
		})
	}
	for want := 10; want < 30; want++ {
		item, ok := queue.pop()
		if !ok || item.depth != want {
			t.Fatalf("pop = (%d, %v), want (%d, true)", item.depth, ok, want)
		}
	}
	if _, ok := queue.pop(); ok || queue.size != 0 {
		t.Fatal("empty queue returned a traverser")
	}
	for i, item := range queue.items {
		if item.path != nil || item.ancestry != nil || item.node.ID != uuid.Nil {
			t.Fatalf("queue slot %d retains popped traversal state", i)
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
	if id, err := fixed.nodeID(); err != nil || id != source {
		t.Fatalf("fixed nodeID() = %s, %v; want %s, nil", id, err, source)
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
	if id, err := neighbor.nodeID(); err != nil || id != target {
		t.Fatalf("neighbor nodeID() = %s, %v; want %s, nil", id, err, target)
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

func TestTypedIteratorsShareCurrentResultContract(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Indexes: []IndexDefinition{
		{Label: "Node", Property: "name"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx := context.Background()
	source, target := uuid.New(), uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(source, "Node"); err != nil {
			return err
		}
		if err := tx.SetProperties(source, map[string]interface{}{"name": "source"}); err != nil {
			return err
		}
		if err := tx.PutNode(target, "Node"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(source, target, "A"); err != nil {
			return err
		}
		_, err := tx.PutEdge(source, target, "B")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	tx, err := db.NewReadTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, tx)

	nodeIterators := []struct {
		name string
		iter NodeIterator
	}{
		{"scan", tx.ScanNodes()},
		{"index", tx.FindNodes("Node", "name", "source")},
		{"fixed", newFixedNodeIterator(tx, []uuid.UUID{source})},
		{"filter", newNodeFilterIterator(newFixedNodeIterator(tx, []uuid.UUID{source}), func(Node) bool { return true })},
		{"neighbor", newNeighborIterator(tx, tx.OutEdges(source, "A"), "out")},
		{"repeat", newRepeatIterator(tx, newFixedNodeIterator(tx, []uuid.UUID{source}), &RepeatConfig{
			sub:      func(p *TraversalPipeline) *TraversalPipeline { return p.Out("A") },
			times:    1,
			timesSet: true,
		})},
	}
	for _, test := range nodeIterators {
		t.Run("node/"+test.name, func(t *testing.T) {
			assertNodeIteratorContract(t, test.iter)
		})
	}

	edgeIterators := []struct {
		name string
		iter EdgeIterator
	}{
		{"range", tx.OutEdges(source)},
		{"multiple ranges", tx.OutEdges(source, "A", "B")},
		{"flat map", newFlatMapEdgeIterator(tx, newFixedNodeIterator(tx, []uuid.UUID{source}), func(id uuid.UUID) Iterator {
			return tx.OutEdges(id)
		})},
	}
	for _, test := range edgeIterators {
		t.Run("edge/"+test.name, func(t *testing.T) {
			assertEdgeIteratorContract(t, test.iter)
		})
	}

	t.Run("empty node", func(t *testing.T) {
		assertEmptyNodeIteratorContract(t, tx.FindNodes("Node", "name", "missing"))
	})
	t.Run("empty edge", func(t *testing.T) {
		assertEmptyEdgeIteratorContract(t, tx.OutEdges(target))
	})
}

func assertNodeIteratorContract(t *testing.T, iter NodeIterator) {
	t.Helper()
	defer closeTestResource(t, iter)
	if _, _, err := iter.Node(); !errors.Is(err, ErrInvalidIteratorState) {
		t.Fatalf("Node before Next error = %v, want ErrInvalidIteratorState", err)
	}
	if !iter.Next() {
		t.Fatalf("Next returned false before a result: %v", iter.Error())
	}
	id, label, err := iter.Node()
	if err != nil {
		t.Fatal(err)
	}
	ownedID, ownedLabel := id, label
	for iter.Next() {
		if _, _, err := iter.Node(); err != nil {
			t.Fatal(err)
		}
	}
	if err := iter.Error(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := iter.Node(); !errors.Is(err, ErrInvalidIteratorState) {
		t.Fatalf("Node after exhaustion error = %v, want ErrInvalidIteratorState", err)
	}
	if ownedID != id || ownedLabel != label {
		t.Fatal("previously returned node values changed after advancement")
	}
}

func assertEdgeIteratorContract(t *testing.T, iter EdgeIterator) {
	t.Helper()
	defer closeTestResource(t, iter)
	if _, _, _, err := iter.Edge(); !errors.Is(err, ErrInvalidIteratorState) {
		t.Fatalf("Edge before Next error = %v, want ErrInvalidIteratorState", err)
	}
	if !iter.Next() {
		t.Fatalf("Next returned false before a result: %v", iter.Error())
	}
	id, other, label, err := iter.Edge()
	if err != nil {
		t.Fatal(err)
	}
	ownedID, ownedOther, ownedLabel := id, other, label
	for iter.Next() {
		if _, _, _, err := iter.Edge(); err != nil {
			t.Fatal(err)
		}
	}
	if err := iter.Error(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := iter.Edge(); !errors.Is(err, ErrInvalidIteratorState) {
		t.Fatalf("Edge after exhaustion error = %v, want ErrInvalidIteratorState", err)
	}
	if ownedID != id || ownedOther != other || ownedLabel != label {
		t.Fatal("previously returned edge values changed after advancement")
	}
}

func assertEmptyNodeIteratorContract(t *testing.T, iter NodeIterator) {
	t.Helper()
	defer closeTestResource(t, iter)
	if iter.Next() {
		t.Fatal("empty node iterator advanced")
	}
	if err := iter.Error(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := iter.Node(); !errors.Is(err, ErrInvalidIteratorState) {
		t.Fatalf("empty Node error = %v, want ErrInvalidIteratorState", err)
	}
}

func assertEmptyEdgeIteratorContract(t *testing.T, iter EdgeIterator) {
	t.Helper()
	defer closeTestResource(t, iter)
	if iter.Next() {
		t.Fatal("empty edge iterator advanced")
	}
	if err := iter.Error(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := iter.Edge(); !errors.Is(err, ErrInvalidIteratorState) {
		t.Fatalf("empty Edge error = %v, want ErrInvalidIteratorState", err)
	}
}

func assertSafeFailedIterator(t *testing.T, iter Iterator) {
	t.Helper()
	if iter.Next() {
		t.Error("failed iterator advanced")
	}
	if err := iter.Error(); err == nil {
		t.Error("failed iterator did not expose its error")
	}
	if err := iter.Close(); err != nil {
		t.Errorf("failed iterator close = %v", err)
	}
}
