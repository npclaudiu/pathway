package pathway

import (
	"context"
	"errors"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

func TestIndexDefinitionsBuildPreserveAndDrop(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	id := uuid.New()

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(id, "Item"); err != nil {
			return err
		}
		if err := tx.SetProperties(id, map[string]interface{}{
			"code":  "A-1",
			"value": "first",
		}); err != nil {
			return err
		}
		if got := tx.batch.Count(); got != 2 {
			t.Fatalf("unindexed node/property write count = %d, want 2", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.View(ctx, func(tx *Tx) error {
		iter := tx.FindNodes("Item", "value", "first")
		defer closeTestResource(t, iter)
		if iter.Next() {
			t.Fatal("unconfigured property unexpectedly had an index")
		}
		return iter.Error()
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(ctx, func(tx *Tx) error {
		// A rebuild must clear the complete range before adding canonical
		// entries, including remnants not backed by a node.
		staleKey, err := encoding.EncodeIndexKey("Item", "value", "first", uuid.New())
		if err != nil {
			return err
		}
		return tx.Set(staleKey, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	definitions := []IndexDefinition{{Label: "Item", Property: "value"}}
	db, err = OpenWithOptions(dir, Options{Indexes: definitions})
	if err != nil {
		t.Fatal(err)
	}
	assertIndexedNode(t, db, "Item", "value", "first", id)
	if err := db.View(ctx, func(tx *Tx) error {
		iter := tx.FindNodes("Item", "code", "A-1")
		defer closeTestResource(t, iter)
		if iter.Next() {
			t.Fatal("unconfigured property was indexed during rebuild")
		}
		return iter.Error()
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Nil preserves persisted definitions when configuration is not being
	// changed by this open.
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertIndexedNode(t, db, "Item", "value", "first", id)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// A non-nil empty slice is authoritative and drops all definitions and
	// their index entries.
	db, err = OpenWithOptions(dir, Options{Indexes: []IndexDefinition{}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	if err := db.View(ctx, func(tx *Tx) error {
		iter := tx.FindNodes("Item", "value", "first")
		defer closeTestResource(t, iter)
		if iter.Next() {
			t.Fatal("dropped index still returned a node")
		}
		if err := iter.Error(); err != nil {
			return err
		}
		definitionKey, err := encoding.EncodeIndexDefinitionKey("Item", "value")
		if err != nil {
			return err
		}
		if _, err := tx.Get(definitionKey); !errors.Is(err, pebble.ErrNotFound) {
			t.Fatalf("dropped definition record still exists: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSetPropertiesUpdatesOnlyChangedIndexedValues(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Indexes: []IndexDefinition{
		{Label: "Item", Property: "indexed"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	ctx := context.Background()
	id := uuid.New()

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.PutNode(id, "Item"); err != nil {
			return err
		}
		return tx.SetProperties(id, map[string]interface{}{
			"indexed":   "same",
			"unindexed": "one",
		})
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.SetProperties(id, map[string]interface{}{
			"indexed":   "same",
			"unindexed": "two",
		}); err != nil {
			return err
		}
		if got := tx.batch.Count(); got != 1 {
			t.Fatalf("unchanged indexed value produced %d writes, want property write only", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(ctx, func(tx *Tx) error {
		if err := tx.SetProperties(id, map[string]interface{}{
			"indexed":   "changed",
			"unindexed": "two",
		}); err != nil {
			return err
		}
		if got := tx.batch.Count(); got != 3 {
			t.Fatalf("changed indexed value produced %d writes, want delete, insert, property write", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(ctx, func(tx *Tx) error {
		oldIter := tx.FindNodes("Item", "indexed", "same")
		defer closeTestResource(t, oldIter)
		if oldIter.Next() {
			t.Fatal("old indexed value remains after change")
		}
		return oldIter.Error()
	}); err != nil {
		t.Fatal(err)
	}
	assertIndexedNode(t, db, "Item", "indexed", "changed", id)
}

func assertIndexedNode(t *testing.T, db *Database, label, property string, value interface{}, wantID uuid.UUID) {
	t.Helper()
	if err := db.View(context.Background(), func(tx *Tx) error {
		iter := tx.FindNodes(label, property, value)
		defer closeTestResource(t, iter)
		if !iter.Next() {
			t.Fatalf("indexed node missing: %v", iter.Error())
		}
		id, gotLabel, err := iter.Node()
		if err != nil {
			return err
		}
		if id != wantID || gotLabel != label {
			t.Fatalf("indexed node = (%s, %q), want (%s, %q)", id, gotLabel, wantID, label)
		}
		if iter.Next() {
			t.Fatal("index returned duplicate nodes")
		}
		return iter.Error()
	}); err != nil {
		t.Fatal(err)
	}
}
