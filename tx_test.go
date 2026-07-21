package pathway

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

func TestTx_PutNode_InvalidLabel(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx := context.Background()
	longLabel := strings.Repeat("A", 65536)

	err = db.Update(ctx, func(tx *Tx) error {
		return tx.PutNode(uuid.New(), longLabel)
	})

	if err != encoding.ErrInvalidLabel {
		t.Errorf("expected ErrInvalidLabel, got %v", err)
	}
}

func TestTx_PutEdge_Dangling(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	u1 := uuid.New() // Not added
	u2 := uuid.New() // Not added

	err := db.Update(ctx, func(tx *Tx) error {
		_, err := tx.PutEdge(u1, u2, "REL")
		return err
	})
	if err != ErrDanglingEdge {
		t.Errorf("expected ErrDanglingEdge, got %v", err)
	}

	// Add U1 only
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.PutNode(u1, "User")
	}); err != nil {
		t.Fatal(err)
	}

	err = db.Update(ctx, func(tx *Tx) error {
		_, err := tx.PutEdge(u1, u2, "REL")
		return err
	})
	if err != ErrDanglingEdge {
		t.Errorf("expected ErrDanglingEdge (target missing), got %v", err)
	}
}

func TestTx_DeleteEdge_NotImplemented(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	err := db.Update(ctx, func(tx *Tx) error {
		return tx.DeleteEdge(uuid.New())
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestTx_FindNodes(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	id1 := uuid.New()
	id2 := uuid.New()

	// Seed properties
	err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(id1, "User"); err != nil {
			return err
		}
		if err := tx.SetProperties(id1, map[string]interface{}{"name": "alice", "age": 30}); err != nil {
			return err
		}

		if err := tx.PutNode(id2, "User"); err != nil {
			return err
		}
		return tx.SetProperties(id2, map[string]interface{}{"name": "bob", "age": 40})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test FindNodes positive case
	err = db.View(ctx, func(tx *Tx) error {
		it := tx.FindNodes("User", "name", "alice")
		defer closeTestResource(t, it)

		if !it.Next() {
			t.Error("expected to find node")
		}

		id, lbl, err := it.Node()
		if err != nil {
			t.Error(err)
		}
		if id != id1 {
			t.Errorf("expected id %v, got %v", id1, id)
		}
		if lbl != "User" {
			t.Errorf("expected label User, got %s", lbl)
		}

		if it.Next() {
			t.Error("expected exactly one node")
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	// Test FindNodes negative case
	err = db.View(ctx, func(tx *Tx) error {
		it := tx.FindNodes("User", "name", "charlie")
		defer closeTestResource(t, it)

		if it.Next() {
			t.Error("expected no nodes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_ScanNodes(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	if err := db.Update(ctx, func(tx *Tx) error {
		for _, id := range ids {
			if err := tx.PutNode(id, "User"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := db.View(ctx, func(tx *Tx) error {
		it := tx.ScanNodes()
		defer closeTestResource(t, it)

		count := 0
		for it.Next() {
			count++
			id, lbl, err := it.Node()
			if err != nil {
				return err
			}
			if lbl != "User" {
				t.Errorf("unexpected label %s", lbl)
			}
			found := false
			for _, x := range ids {
				if x == id {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("unexpected node id %v", id)
			}
		}
		if count != 3 {
			t.Errorf("expected 3 nodes, got %d", count)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKeyUpperBound(t *testing.T) {
	// Normal case
	in := []byte{0x01, 0x02}
	out := keyUpperBound(in)
	expected := []byte{0x01, 0x03}
	if !bytes.Equal(out, expected) {
		t.Errorf("expected %x, got %x", expected, out)
	}

	// Overflow last byte
	in = []byte{0x01, 0xFF}
	out = keyUpperBound(in)
	expected = []byte{0x02} // 0x02 is prefix of 0x01FF + 1 = 0x0200
	// Wait, algorithm:
	// iterate from end. 0xFF + 1 = 0. carry.
	// 0x01 + 1 = 0x02. return [:1] (0 to i=0 inclusive).
	if !bytes.Equal(out, expected) {
		t.Errorf("expected %x, got %x", expected, out)
	}

	// Full overflow
	in = []byte{0xFF, 0xFF}
	out = keyUpperBound(in)
	if out != nil {
		t.Errorf("expected nil for full overflow, got %x", out)
	}
}

func TestTx_Iterator_GenericMethods(t *testing.T) {
	db, _ := Open(":memory:")
	defer closeTestResource(t, db)
	ctx := context.Background()

	id := uuid.New()
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.PutNode(id, "A")
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(ctx, func(tx *Tx) error {
		it := tx.ScanNodes()
		defer closeTestResource(t, it)

		if it.Next() {
			k := it.Key()
			v := it.Value()
			if len(k) == 0 || len(v) == 0 {
				t.Error("Key/Value empty")
			}
			if !it.Valid() {
				t.Error("expected Valid")
			}
			// SeekGE (scan usually supports it?)
			// NodeIterator wraps Pebble iterator.
			if !it.SeekGE(k) {
				t.Error("SeekGE failed")
			}
			// Path
			_ = it.Path()
		}
		if it.Error() != nil {
			t.Error(it.Error())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
