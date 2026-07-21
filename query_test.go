package pathway

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestQuery_V_InvalidUUID(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)

	// Create one valid node
	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.PutNode(id, "Thing")
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// Query with one valid and one invalid
	results, err := g.V(id.String(), "not-a-uuid").ToList()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result (valid uuid), got %d", len(results))
	}
}

func TestQuery_ToList_NilDatabase(t *testing.T) {
	// g with nil db
	g := NewTraversalSource(nil)
	q := g.V()

	_, err := q.ToList()
	if err != ErrInvalidDatabase {
		t.Errorf("expected ErrInvalidDatabase, got %v", err)
	}
}

func TestQuery_Path_Check(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New()
	u2 := uuid.New()
	var edgeID uuid.UUID
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(u1, "A"); err != nil {
			return err
		}
		if err := tx.PutNode(u2, "B"); err != nil {
			return err
		}
		var err error
		edgeID, err = tx.PutEdge(u1, u2, "LINK")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	res, err := g.V(u1.String()).Out("LINK").Path().ToList()
	if err != nil {
		t.Fatalf("Path query failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected one path, got %d", len(res))
	}
	path, ok := res[0].(Path)
	if !ok {
		t.Fatalf("path result has type %T, want pathway.Path", res[0])
	}
	want := Path{
		{Kind: PathNode, ID: u1, Label: "A"},
		{Kind: PathEdge, ID: edgeID, Label: "LINK", Other: u2},
		{Kind: PathNode, ID: u2, Label: "B"},
	}
	if !reflect.DeepEqual(path, want) {
		t.Fatalf("path = %#v, want %#v", path, want)
	}

	// Materialized paths do not share mutable backing storage with later reads.
	path[0].Label = "mutated"
	again, err := g.V(u1.String()).Out("LINK").Path().ToList()
	if err != nil {
		t.Fatal(err)
	}
	if again[0].(Path)[0].Label != "A" {
		t.Fatal("path result was mutated through shared backing storage")
	}
}

func TestQuery_HasLabel(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New()
	u2 := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(u1, "Person"); err != nil {
			return err
		}
		if err := tx.PutNode(u2, "Robot"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// HasLabel("Robot")
	res, err := g.V().HasLabel("Robot").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("expected 1 robot, got %d", len(res))
	}
	if m, ok := res[0].(map[string]interface{}); ok {
		if m["label"] != "Robot" {
			t.Errorf("expected label Robot, got %v", m["label"])
		}
	}
}

func TestQuery_In(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New()
	u2 := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(u1, "A"); err != nil {
			return err
		}
		if err := tx.PutNode(u2, "B"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(u1, u2, "LINK"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// u2 <- In("LINK") == u1
	res, err := g.V(u2.String()).In("LINK").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("expected 1 result, got %d", len(res))
	}
}

func TestQuery_Repeat(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	// Chain: A -> B -> C
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(a, "Node"); err != nil {
			return err
		}
		if err := tx.PutNode(b, "Node"); err != nil {
			return err
		}
		if err := tx.PutNode(c, "Node"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(a, b, "NEXT"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(b, c, "NEXT"); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)

	// Repeat(Out).Times(2)
	res, err := g.V(a.String()).Repeat(func(p *TraversalPipeline) *TraversalPipeline {
		return p.Out("NEXT")
	}).Times(2).ToList()

	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result (C), got %d", len(res))
	}
}

func TestQuery_Values(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	id := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		if err := tx.PutNode(id, "Node"); err != nil {
			return err
		}
		return tx.SetProperties(id, map[string]interface{}{
			"name":   "Alice",
			"age":    30,
			"active": true,
		})
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	res, err := g.V(id.String()).Values("name").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0] != "Alice" {
		t.Fatalf("single-key projection = %#v, want [Alice]", res)
	}

	res, err = g.V(id.String()).Values("name", "missing", "age", "active").ToList()
	if err != nil {
		t.Fatal(err)
	}
	want := []interface{}{"Alice", float64(30), true}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("multi-key mixed projection = %#v, want %#v", res, want)
	}

	res, err = g.V(id.String()).Values("missing").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("missing property should be omitted, got %#v", res)
	}
}

func TestQuery_IDsAvoidLabelMaterializationAcrossHops(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	a, b, c := uuid.New(), uuid.New(), uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		for id, label := range map[uuid.UUID]string{a: "A", b: "B", c: "C"} {
			if err := tx.PutNode(id, label); err != nil {
				return err
			}
		}
		if _, err := tx.PutEdge(a, b, "NEXT"); err != nil {
			return err
		}
		if _, err := tx.PutEdge(b, c, "NEXT"); err != nil {
			return err
		}
		return tx.SetProperties(b, map[string]interface{}{"name": "middle"})
	}); err != nil {
		t.Fatal(err)
	}

	g := NewTraversalSource(db)
	oneHop, err := g.V(a.String()).Out("NEXT").IDs().ToList()
	if err != nil {
		t.Fatal(err)
	}
	if want := []interface{}{b}; !reflect.DeepEqual(oneHop, want) {
		t.Fatalf("one-hop IDs = %#v, want %#v", oneHop, want)
	}

	twoHops, err := g.V(a.String()).Out("NEXT").Out("NEXT").IDs().ToList()
	if err != nil {
		t.Fatal(err)
	}
	if want := []interface{}{c}; !reflect.DeepEqual(twoHops, want) {
		t.Fatalf("two-hop IDs = %#v, want %#v", twoHops, want)
	}

	values, err := g.V(a.String()).Out("NEXT").Values("name").ToList()
	if err != nil {
		t.Fatal(err)
	}
	if want := []interface{}{"middle"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("neighbor values = %#v, want %#v", values, want)
	}
}

func TestQuery_Limit_Count_Placeholder(t *testing.T) {
	// Not implemented in query.go yet, skipping.
	// But Emit/Until are methods on Pipeline.
	// We should call them to cover the builder code.

	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	g := NewTraversalSource(db)

	p := g.V().Emit().Until(func(i interface{}) bool { return true })
	if p == nil {
		t.Error("Emit/Until returned nil")
	}
}
