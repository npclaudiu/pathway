package tests

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

// Helper: Replicate encoding logic to verify storage keys directly
// We recreate this here to ensure our tests are independent of internal changes logic (blackbox-ish)
// asking "Did it write the bytes we expect?"
// But better: use the internal package if allowed to strictly test integration.
// Go allows importing internal if parent is the same.
// Importing internal/encoding is cleaner and less error prone.

// NOTE: Since we are in package `tests`, we cannot import `internal` from parent unless `tests` is internal?
// Go restriction: `internal` packages are only visible to the tree rooted at the parent of `internal`.
// verifyModule/pathway/internal -> visible to verifyModule/pathway and verifyModule/pathway/foo
// So `tests` package under `pathway` CAN import `pathway/internal`.
// Wait: `github.com/npclaudiu/pathway/internal/encoding`.

// Let's rely on public API but for storage verification we might need to craft keys manually
// if import fails. Let's assume manual construction for robustness and strict "spec" adherence verification.

const (
	PrefixNode       byte = 0x01
	PrefixEdgeOut    byte = 0x02
	PrefixEdgeIn     byte = 0x03
	PrefixProperties byte = 0x04
)

func encodeNodeKey(id uuid.UUID) []byte {
	k := make([]byte, 1+16)
	k[0] = PrefixNode
	copy(k[1:], id[:])
	return k
}

func encodeEdgeOutKey(srcID, dstID uuid.UUID, label string) []byte {
	lblBytes := []byte(label)
	k := make([]byte, 1+16+2+len(lblBytes)+16)
	k[0] = PrefixEdgeOut
	copy(k[1:], srcID[:])
	binary.BigEndian.PutUint16(k[17:], uint16(len(lblBytes)))
	copy(k[19:], lblBytes)
	copy(k[19+len(lblBytes):], dstID[:])
	return k
}

func encodeEdgeInKey(srcID, dstID uuid.UUID, label string) []byte {
	lblBytes := []byte(label)
	k := make([]byte, 1+16+2+len(lblBytes)+16)
	k[0] = PrefixEdgeIn
	copy(k[1:], dstID[:]) // Target first for In
	binary.BigEndian.PutUint16(k[17:], uint16(len(lblBytes)))
	copy(k[19:], lblBytes)
	copy(k[19+len(lblBytes):], srcID[:])
	return k
}

func TestAPICoverage(t *testing.T) {
	// 1. Database Lifecycle
	t.Run("DatabaseLifecycle", func(t *testing.T) {
		db, err := pathway.Open(":memory:")
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}

		err = db.Close()
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	})

	// 2. Node Operations
	t.Run("NodeOperations", func(t *testing.T) {
		db, _ := pathway.Open(":memory:")
		defer db.Close()
		ctx := context.Background()

		id := uuid.New()
		label := "Person"

		// A. PutNode
		err := db.Update(ctx, func(tx *pathway.Tx) error {
			return tx.PutNode(id, label)
		})
		if err != nil {
			t.Fatalf("PutNode failed: %v", err)
		}

		// B. GetNode & Verification
		err = db.View(ctx, func(tx *pathway.Tx) error {
			// Public API check
			l, exists, err := tx.GetNode(id)
			if err != nil {
				return err
			}
			if !exists {
				t.Error("GetNode: Node not found")
			}
			if l != label {
				t.Errorf("GetNode: Expected label %s, got %s", label, l)
			}

			// Storage Verification (Manual Key Get)
			err = tx.Access(func(tx *pathway.Tx) error {
				k := encodeNodeKey(id)
				val, err := tx.Get(k)
				if err != nil {
					t.Errorf("Storage: Node key not found: %v", err)
				}
				// Verify value (LabelLen + Label)
				if len(val) != 2+len(label) {
					t.Errorf("Storage: Value length mismatch. Want %d, Got %d", 2+len(label), len(val))
				}
				return nil
			})
			return err
		})
		if err != nil {
			t.Fatalf("View failed: %v", err)
		}

		// C. DeleteNode
		err = db.Update(ctx, func(tx *pathway.Tx) error {
			return tx.DeleteNode(id)
		})
		if err != nil {
			t.Fatalf("DeleteNode failed: %v", err)
		}

		// Verify Deletion
		err = db.View(ctx, func(tx *pathway.Tx) error {
			_, exists, _ := tx.GetNode(id)
			if exists {
				t.Error("GetNode: Node should be deleted")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Verification failed: %v", err)
		}
	})

	// 3. Edge Operations
	t.Run("EdgeOperations", func(t *testing.T) {
		db, _ := pathway.Open(":memory:")
		defer db.Close()
		ctx := context.Background()

		u1 := uuid.New()
		u2 := uuid.New()

		// Setup nodes
		if err := db.Update(ctx, func(tx *pathway.Tx) error {
			if err := tx.PutNode(u1, "User"); err != nil {
				return err
			}
			return tx.PutNode(u2, "User")
		}); err != nil {
			t.Fatal(err)
		}

		// A. PutEdge & Constraints
		// Constraint: Missing node
		err := db.Update(ctx, func(tx *pathway.Tx) error {
			_, err := tx.PutEdge(u1, uuid.New(), "KNOWS") // Missing target
			if !errors.Is(err, pathway.ErrDanglingEdge) {
				t.Errorf("Expected dangling edge error, got %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Constraint check failed to return (expected handled error inside): %v", err)
		}

		// Successful Put
		var edgeID uuid.UUID
		err = db.Update(ctx, func(tx *pathway.Tx) error {
			var err error
			edgeID, err = tx.PutEdge(u1, u2, "KNOWS")
			return err
		})
		if err != nil {
			t.Fatalf("PutEdge failed: %v", err)
		}

		// B. Storage Verification
		err = db.View(ctx, func(tx *pathway.Tx) error {
			return tx.Access(func(tx *pathway.Tx) error {
				outK := encodeEdgeOutKey(u1, u2, "KNOWS")
				inK := encodeEdgeInKey(u1, u2, "KNOWS")

				if _, err := tx.Get(outK); err != nil {
					t.Errorf("Storage: OutEdge key missing")
				}
				if _, err := tx.Get(inK); err != nil {
					t.Errorf("Storage: InEdge key missing")
				}
				return nil
			})
		})
		if err != nil {
			t.Errorf("Storage verification failed: %v", err)
		}

		// C. Traversal (Iterator)
		if err := db.View(ctx, func(tx *pathway.Tx) error {
			iter := tx.OutEdges(u1) // Should find 1
			defer iter.Close()
			found := false
			for iter.Next() {
				eid, other, lbl, _ := iter.Edge()
				if eid == edgeID && other == u2 && lbl == "KNOWS" {
					found = true
				}
			}
			if !found {
				t.Error("Iterator: OutEdges did not find expected edge")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		// D. Delete Cascade
		err = db.Update(ctx, func(tx *pathway.Tx) error {
			return tx.DeleteNode(u1) // Should remove edge u1->u2
		})
		if err != nil {
			t.Fatalf("DeleteNode failed: %v", err)
		}

		// Verify Edge Removed
		err = db.View(ctx, func(tx *pathway.Tx) error {
			return tx.Access(func(tx *pathway.Tx) error {
				outK := encodeEdgeOutKey(u1, u2, "KNOWS")
				if _, err := tx.Get(outK); err == nil {
					t.Error("Storage: OutEdge key should be deleted")
				}
				return nil
			})
		})
		if err != nil {
			t.Errorf("Deletion verification failed: %v", err)
		}
	})

	// 4. Property Operations
	t.Run("PropertyOperations", func(t *testing.T) {
		db, _ := pathway.Open(":memory:")
		defer db.Close()
		ctx := context.Background()
		id := uuid.New()

		// A. Set Properties
		props := map[string]interface{}{
			"name":   "Alice",
			"age":    30,
			"active": true,
		}

		if err := db.Update(ctx, func(tx *pathway.Tx) error {
			// Need node first? Properties are separate keys, but logical model usually implies node exists.
			// System doesn't strictly enforce Property->Node dependency in `SetProperties` implementation?
			// Let's create node for correctness.
			if err := tx.PutNode(id, "User"); err != nil {
				return err
			}
			return tx.SetProperties(id, props)
		}); err != nil {
			t.Fatal(err)
		}

		// B. Get Properties
		if err := db.View(ctx, func(tx *pathway.Tx) error {
			p, err := tx.GetProperties(id)
			if err != nil {
				t.Fatalf("GetProperties failed: %v", err)
			}
			if p["name"] != "Alice" {
				t.Errorf("Prop mismatch usage")
			}
			// JSON unmarshal for numbers makes them float64 usually
			if age, ok := p["age"].(float64); !ok || age != 30 {
				t.Errorf("Prop mismatch age: got %v (%T)", p["age"], p["age"])
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	// 5. Query API
	t.Run("QueryAPI", func(t *testing.T) {
		db, _ := pathway.Open(":memory:")
		defer db.Close()
		ctx := context.Background()

		// Seeding: A -> B
		a := uuid.New()
		b := uuid.New()
		if err := db.Update(ctx, func(tx *pathway.Tx) error {
			if err := tx.PutNode(a, "A"); err != nil {
				return err
			}
			if err := tx.PutNode(b, "B"); err != nil {
				return err
			}
			if _, err := tx.PutEdge(a, b, "LINK"); err != nil {
				return err
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		// A. Traversal
		g := pathway.NewTraversalSource(db)
		results, err := g.V(a.String()).Out("LINK").ToList()
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("Expected 1 result, got %d", len(results))
		}

		// Check result content
		if m, ok := results[0].(map[string]interface{}); ok {
			if id, ok := m["id"].(uuid.UUID); !ok || id != b {
				t.Errorf("Expected node B, got %v", m)
			}
		} else {
			t.Errorf("Expected map result, got %T", results[0])
		}

		// B. Filtering
		// Add another edge A->C (Label C)
		c := uuid.New()
		if err := db.Update(ctx, func(tx *pathway.Tx) error {
			if err := tx.PutNode(c, "C"); err != nil {
				return err
			}
			if _, err := tx.PutEdge(a, c, "OTHER"); err != nil {
				return err
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		// Query: A.Out(LINK) should only return B
		results, err = g.V(a.String()).Out("LINK").ToList()
		if err != nil {
			t.Errorf("Query failed: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("Filter: Expected 1 result, got %d", len(results))
		}

		// Query: A.Out() should return B and C
		results, err = g.V(a.String()).Out().ToList()
		if err != nil {
			t.Errorf("Query failed: %v", err)
		}
		if len(results) != 2 {
			t.Errorf("NoFilter: Expected 2 results, got %d", len(results))
		}
	})
}
