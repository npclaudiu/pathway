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
	defer db.Close()

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
	defer db.Close()
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
	db.Update(ctx, func(tx *Tx) error {
		return tx.PutNode(u1, "User")
	})

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
	defer db.Close()
	ctx := context.Background()

	err := db.Update(ctx, func(tx *Tx) error {
		return tx.DeleteEdge(uuid.New())
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected not supported error, got %v", err)
	}
}

func TestTx_FindNodes_NotImplemented(t *testing.T) {
	db, _ := Open(":memory:")
	defer db.Close()
	ctx := context.Background()

	err := db.View(ctx, func(tx *Tx) error {
		it := tx.FindNodes("User", "name", "val")
		if it.Error() == nil {
			t.Error("expected error from FindNodes")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTx_ScanNodes(t *testing.T) {
	db, _ := Open(":memory:")
	defer db.Close()
	ctx := context.Background()

	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	db.Update(ctx, func(tx *Tx) error {
		for _, id := range ids {
			tx.PutNode(id, "User")
		}
		return nil
	})

	err := db.View(ctx, func(tx *Tx) error {
		it := tx.ScanNodes()
		defer it.Close()

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
	defer db.Close()
	ctx := context.Background()

	id := uuid.New()
	db.Update(ctx, func(tx *Tx) error {
		tx.PutNode(id, "A")
		return nil
	})

	db.View(ctx, func(tx *Tx) error {
		it := tx.ScanNodes()
		defer it.Close()

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
			p := it.Path()
			if p == nil { // scanNodes returns nil path? No, implementation wrapper?
				// nodeIterator doesn't implement Path, iterator wrapper does return nil?
				// Let's check iterator.go
			}
		}
		if it.Error() != nil {
			t.Error(it.Error())
		}
		return nil
	})
}
