package pathway

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type UserStruct struct {
	Name string `graph:"name"`
	Age  int    `graph:"age"`
	Skip string `graph:"-"`
	None string
}

func TestTx_Load(t *testing.T) {
	db, _ := Open(":memory:")
	defer db.Close()
	ctx := context.Background()

	id := uuid.New()

	// Setup data
	db.Update(ctx, func(tx *Tx) error {
		tx.PutNode(id, "User")
		tx.SetProperties(id, map[string]interface{}{
			"name": "Alice",
			"age":  30, // Passed as int, stored as int (or float via protobuf if internally converted, usually float64)
		})
		return nil
	})

	// Test Success
	db.View(ctx, func(tx *Tx) error {
		var u UserStruct
		err := tx.Load(id, &u)
		if err != nil {
			t.Fatalf("Load failed: %v", err)
		}

		if u.Name != "Alice" {
			t.Errorf("Name mismatch: %s", u.Name)
		}
		if u.Age != 30 {
			t.Errorf("Age mismatch: %d", u.Age)
		}
		return nil
	})

	// Test Errors
	db.View(ctx, func(tx *Tx) error {
		// Nil
		if err := tx.Load(id, nil); err == nil {
			t.Error("expected error for nil")
		}
		// Non-pointer
		var u UserStruct
		if err := tx.Load(id, u); err == nil {
			t.Error("expected error for non-pointer")
		}
		// Non-struct pointer
		i := 10
		if err := tx.Load(id, &i); err == nil {
			t.Error("expected error for int pointer")
		}

		return nil
	})

	// Test Node Not Found
	db.View(ctx, func(tx *Tx) error {
		var u UserStruct
		// Random ID, not in DB. But Wait, GetProperties returns nil, nil if no props?
		// Tx.Load calls GetProperties -> returns nil, nil -> returns ErrNodeNotFound?
		// Let's check Load impl: "if props == nil { return ErrNodeNotFound }"
		// GetProperties returns nil if not found.
		// So checking random ID:
		if err := tx.Load(uuid.New(), &u); err != ErrNodeNotFound {
			t.Errorf("expected ErrNodeNotFound, got %v", err)
		}
		return nil
	})
}
