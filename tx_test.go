package pathway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble/v2"
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

func TestTx_NodeExistsReadsOwnWrites(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	id := uuid.New()

	if err := db.Update(ctx, func(tx *Tx) error {
		exists, err := tx.nodeExists(id)
		if err != nil {
			return err
		}
		if exists {
			t.Fatal("missing node reported as existing")
		}
		if err := tx.PutNode(id, "endpoint"); err != nil {
			return err
		}
		exists, err = tx.nodeExists(id)
		if err != nil {
			return err
		}
		if !exists {
			t.Fatal("node staged in the batch was not found")
		}
		_, err = tx.PutEdge(id, id, "SELF")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(ctx, func(tx *Tx) error {
		exists, err := tx.nodeExists(id)
		if err != nil {
			return err
		}
		if !exists {
			t.Fatal("committed node was not found through snapshot")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTx_PutEdge_AllowsParallelEdges(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	ctx := context.Background()
	src, dst := uuid.New(), uuid.New()
	var firstID, secondID uuid.UUID

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(src, "Node"); err != nil {
			return err
		}
		if err := tx.PutNode(dst, "Node"); err != nil {
			return err
		}
		if firstID, err = tx.PutEdge(src, dst, "LINK"); err != nil {
			return err
		}
		secondID, err = tx.PutEdge(src, dst, "LINK")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatal("parallel edges received the same ID")
	}

	if err := db.View(ctx, func(tx *Tx) error {
		iter := tx.OutEdges(src, "LINK")
		defer closeTestResource(t, iter)

		got := make(map[uuid.UUID]bool)
		for iter.Next() {
			edgeID, other, label, err := iter.Edge()
			if err != nil {
				return err
			}
			if other != dst || label != "LINK" {
				t.Fatalf("unexpected parallel edge: other=%s label=%q", other, label)
			}
			got[edgeID] = true
		}
		if err := iter.Error(); err != nil {
			return err
		}
		if !got[firstID] || !got[secondID] || len(got) != 2 {
			t.Fatalf("expected both parallel edges, got %v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTx_DeleteEdge(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	src, dst := uuid.New(), uuid.New()
	var edgeID uuid.UUID

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(src, "Node"); err != nil {
			return err
		}
		if err := tx.PutNode(dst, "Node"); err != nil {
			return err
		}
		edgeID, err = tx.PutEdge(src, dst, "LINK")
		if err != nil {
			return err
		}
		return tx.SetProperties(edgeID, map[string]interface{}{"weight": 3})
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.DeleteEdge(edgeID)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(ctx, func(tx *Tx) error {
		for name, iter := range map[string]EdgeIterator{
			"outgoing": tx.OutEdges(src),
			"incoming": tx.InEdges(dst),
		} {
			if iter.Next() {
				t.Errorf("%s adjacency record still exists", name)
			}
			if err := iter.Error(); err != nil {
				return err
			}
			if err := iter.Close(); err != nil {
				return err
			}
		}
		if props, err := tx.GetProperties(edgeID); err != nil {
			return err
		} else if props != nil {
			t.Errorf("edge properties still exist: %v", props)
		}
		if _, err := tx.Get(encoding.EncodeEdgeIDKey(edgeID)); err == nil {
			t.Error("edge reverse-index record still exists")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err = db.Update(ctx, func(tx *Tx) error {
		return tx.DeleteEdge(edgeID)
	})
	if !errors.Is(err, ErrEdgeNotFound) {
		t.Errorf("expected ErrEdgeNotFound, got %v", err)
	}
}

func TestTx_DeleteNode_RemovesAllIncidentEdgeRecords(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	victim, incomingSource, outgoingTarget := uuid.New(), uuid.New(), uuid.New()
	edgeIDs := make([]uuid.UUID, 0, 68)
	otherNodes := []uuid.UUID{incomingSource, outgoingTarget}

	if err := db.Update(ctx, func(tx *Tx) error {
		for _, id := range append([]uuid.UUID{victim}, otherNodes...) {
			if err := tx.PutNode(id, "Node"); err != nil {
				return err
			}
		}
		for i := 0; i < 64; i++ {
			id := uuid.New()
			otherNodes = append(otherNodes, id)
			if err := tx.PutNode(id, "Node"); err != nil {
				return err
			}
		}

		pairs := [][2]uuid.UUID{
			{incomingSource, victim},
			{victim, outgoingTarget},
			{victim, victim},
		}
		for _, id := range otherNodes[2:] {
			pairs = append(pairs, [2]uuid.UUID{victim, id})
		}
		for _, pair := range pairs {
			edgeID, err := tx.PutEdge(pair[0], pair[1], "LINK")
			if err != nil {
				return err
			}
			edgeIDs = append(edgeIDs, edgeID)
			if err := tx.SetProperties(edgeID, map[string]interface{}{"owned": true}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.DeleteNode(victim)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(ctx, func(tx *Tx) error {
		if _, exists, err := tx.GetNode(victim); err != nil {
			return err
		} else if exists {
			t.Error("deleted node still exists")
		}
		for _, edgeID := range edgeIDs {
			if _, err := tx.Get(encoding.EncodeEdgeIDKey(edgeID)); !errors.Is(err, pebble.ErrNotFound) {
				t.Errorf("reverse record %s still exists: %v", edgeID, err)
			}
			props, err := tx.GetProperties(edgeID)
			if err != nil {
				return err
			}
			if props != nil {
				t.Errorf("properties for edge %s still exist", edgeID)
			}
		}
		for _, id := range otherNodes {
			if _, exists, err := tx.GetNode(id); err != nil {
				return err
			} else if !exists {
				t.Errorf("non-deleted endpoint %s is missing", id)
			}
			for _, iter := range []EdgeIterator{tx.OutEdges(id), tx.InEdges(id)} {
				if iter.Next() {
					t.Errorf("incident adjacency remains for %s", id)
				}
				if err := iter.Error(); err != nil {
					return err
				}
				if err := iter.Close(); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTx_SetProperties_RejectsUnknownEntity(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	unknownID := uuid.New()

	err = db.Update(ctx, func(tx *Tx) error {
		return tx.SetProperties(unknownID, map[string]interface{}{"orphaned": true})
	})
	if !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("expected ErrEntityNotFound, got %v", err)
	}
	if err := db.View(ctx, func(tx *Tx) error {
		props, err := tx.GetProperties(unknownID)
		if err != nil {
			return err
		}
		if props != nil {
			t.Fatalf("unknown entity received properties: %v", props)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTx_DeleteNode_CorruptAdjacencyAborts(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	id := uuid.New()
	malformedKey := append([]byte{encoding.PrefixEdgeOut}, id[:]...)
	malformedKey = append(malformedKey, 0, 0)

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(id, "Node"); err != nil {
			return err
		}
		return tx.Set(malformedKey, make([]byte, 16), nil)
	}); err != nil {
		t.Fatal(err)
	}
	err = db.Update(ctx, func(tx *Tx) error { return tx.DeleteNode(id) })
	if !errors.Is(err, encoding.ErrInvalidKeyFormat) {
		t.Fatalf("expected ErrInvalidKeyFormat, got %v", err)
	}
	if err := db.View(ctx, func(tx *Tx) error {
		_, exists, err := tx.GetNode(id)
		if err != nil {
			return err
		}
		if !exists {
			t.Fatal("failed deletion was not rolled back")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTx_DeleteEdge_CorruptReverseRecordAborts(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	edgeID := uuid.New()

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.Set(encoding.EncodeEdgeIDKey(edgeID), []byte("invalid"), nil); err != nil {
			return err
		}
		return tx.SetProperties(edgeID, map[string]interface{}{"preserved": true})
	}); err != nil {
		t.Fatal(err)
	}
	err = db.Update(ctx, func(tx *Tx) error { return tx.DeleteEdge(edgeID) })
	if !errors.Is(err, encoding.ErrInvalidValueFormat) {
		t.Fatalf("expected ErrInvalidValueFormat, got %v", err)
	}
	if err := db.View(ctx, func(tx *Tx) error {
		props, err := tx.GetProperties(edgeID)
		if err != nil {
			return err
		}
		if props["preserved"] != true {
			t.Fatalf("failed deletion removed edge properties: %v", props)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTx_FindNodes(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Indexes: []IndexDefinition{
		{Label: "User", Property: "name"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()

	id1 := uuid.New()
	id2 := uuid.New()

	// Seed properties
	err = db.Update(ctx, func(tx *Tx) error {
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

func TestTx_FindNodes_UsesExactTypedValues(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Indexes: []IndexDefinition{
		{Label: "Item", Property: "value"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()

	values := []interface{}{"a", "ab", "1", 1, true}
	ids := make([]uuid.UUID, len(values))
	if err := db.Update(ctx, func(tx *Tx) error {
		for i, value := range values {
			ids[i] = uuid.New()
			if err := tx.PutNode(ids[i], "Item"); err != nil {
				return err
			}
			if err := tx.SetProperties(ids[i], map[string]interface{}{"value": value}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for i, value := range values {
		if err := db.View(ctx, func(tx *Tx) error {
			iter := tx.FindNodes("Item", "value", value)
			defer closeTestResource(t, iter)
			if !iter.Next() {
				t.Fatalf("no result for %v (%T): %v", value, value, iter.Error())
			}
			id, _, err := iter.Node()
			if err != nil {
				return err
			}
			if id != ids[i] {
				t.Fatalf("lookup for %v (%T) returned %s, want %s", value, value, id, ids[i])
			}
			if iter.Next() {
				t.Fatalf("lookup for %v (%T) returned a prefix/type collision", value, value)
			}
			return iter.Error()
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTx_PutNode_LabelChangeMigratesIndexes(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Indexes: []IndexDefinition{
		{Label: "OldLabel", Property: "name"},
		{Label: "OldLabel", Property: "rank"},
		{Label: "NewLabel", Property: "name"},
		{Label: "NewLabel", Property: "rank"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	id := uuid.New()

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(id, "OldLabel"); err != nil {
			return err
		}
		return tx.SetProperties(id, map[string]interface{}{
			"name": "indexed",
			"rank": 7,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.PutNode(id, "NewLabel")
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(ctx, func(tx *Tx) error {
		for propKey, value := range map[string]interface{}{"name": "indexed", "rank": 7} {
			oldIter := tx.FindNodes("OldLabel", propKey, value)
			if oldIter.Next() {
				t.Errorf("stale %q index remains under old label", propKey)
			}
			if err := oldIter.Error(); err != nil {
				return err
			}
			if err := oldIter.Close(); err != nil {
				return err
			}

			newIter := tx.FindNodes("NewLabel", propKey, value)
			if !newIter.Next() {
				return fmt.Errorf("missing %q index under new label: %v", propKey, newIter.Error())
			}
			gotID, gotLabel, err := newIter.Node()
			if err != nil {
				return err
			}
			if gotID != id || gotLabel != "NewLabel" {
				t.Errorf("unexpected migrated index result: %s %q", gotID, gotLabel)
			}
			if newIter.Next() {
				t.Errorf("duplicate migrated index for %q", propKey)
			}
			if err := newIter.Error(); err != nil {
				return err
			}
			if err := newIter.Close(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
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
