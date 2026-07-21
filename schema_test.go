package pathway

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	pathencoding "github.com/npclaudiu/pathway/internal/encoding"
	"github.com/npclaudiu/pathway/internal/properties"
)

func TestOpen_MigratesUnversionedSchema(t *testing.T) {
	dir := t.TempDir()
	srcID, dstID, edgeID := uuid.New(), uuid.New(), uuid.New()

	rawDB, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	batch := rawDB.NewBatch()
	for _, node := range []struct {
		id    uuid.UUID
		label string
	}{{srcID, "Person"}, {dstID, "Person"}} {
		if err := batch.Set(pathencoding.EncodeNodeKey(node.id), legacyNodeValue(node.label), nil); err != nil {
			t.Fatal(err)
		}
	}
	propertyData, err := properties.MarshalProperties(map[string]interface{}{"name": "alice", "rank": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Set(pathencoding.EncodePropertyKey(srcID), propertyData, nil); err != nil {
		t.Fatal(err)
	}
	if err := batch.Set(legacyEdgeKey(pathencoding.PrefixEdgeOut, srcID, dstID, "KNOWS"), edgeID[:], nil); err != nil {
		t.Fatal(err)
	}
	if err := batch.Set(legacyEdgeKey(pathencoding.PrefixEdgeIn, dstID, srcID, "KNOWS"), edgeID[:], nil); err != nil {
		t.Fatal(err)
	}
	// A representative v1 index key. Migration must discard and rebuild it.
	if err := batch.Set([]byte{pathencoding.PrefixIndex, 0, 1, 'x'}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	assertMigrated := func(db *Database) {
		t.Helper()
		if err := db.View(context.Background(), func(tx *Tx) error {
			iter := tx.OutEdges(srcID, "KNOWS")
			defer closeTestResource(t, iter)
			if !iter.Next() {
				t.Fatalf("migrated edge missing: %v", iter.Error())
			}
			gotEdgeID, gotDstID, gotLabel, err := iter.Edge()
			if err != nil {
				return err
			}
			if gotEdgeID != edgeID || gotDstID != dstID || gotLabel != "KNOWS" {
				t.Fatalf("unexpected migrated edge: %s %s %q", gotEdgeID, gotDstID, gotLabel)
			}
			if iter.Next() {
				t.Fatal("migration duplicated edge")
			}

			indexIter := tx.FindNodes("Person", "name", "alice")
			defer closeTestResource(t, indexIter)
			if !indexIter.Next() {
				t.Fatalf("rebuilt property index missing: %v", indexIter.Error())
			}
			gotID, _, err := indexIter.Node()
			if err != nil {
				return err
			}
			if gotID != srcID {
				t.Fatalf("rebuilt index returned %s, want %s", gotID, srcID)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrated(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// A second open is a no-op, demonstrating that the atomic migration is
	// safely repeatable after any interruption before commit.
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	assertMigrated(db)
}

func TestOpen_RejectsUnsupportedSchema(t *testing.T) {
	dir := t.TempDir()
	rawDB, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	value := make([]byte, 4)
	binary.BigEndian.PutUint32(value, currentSchemaVersion+1)
	if err := rawDB.Set(schemaVersionKey, value, pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if db != nil {
		_ = db.Close()
		t.Fatal("Open returned a database for an unsupported schema")
	}
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("expected ErrUnsupportedSchema, got %v", err)
	}
}

func TestOpen_MigratesV2ImplicitIndexesToDefinitions(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()
	rawDB, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	propertyData, err := properties.MarshalProperties(map[string]interface{}{"name": "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	indexKey, err := pathencoding.EncodeIndexKey("Person", "name", "legacy", id)
	if err != nil {
		t.Fatal(err)
	}
	versionValue := make([]byte, 4)
	binary.BigEndian.PutUint32(versionValue, 2)
	batch := rawDB.NewBatch()
	for _, entry := range []struct {
		key, value []byte
	}{
		{schemaVersionKey, versionValue},
		{pathencoding.EncodeNodeKey(id), legacyNodeValue("Person")},
		{pathencoding.EncodePropertyKey(id), propertyData},
		{indexKey, nil},
	} {
		if err := batch.Set(entry.key, entry.value, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	assertIndexedNode(t, db, "Person", "name", "legacy", id)

	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.SetProperties(id, map[string]interface{}{"name": "updated"})
	}); err != nil {
		t.Fatal(err)
	}
	assertIndexedNode(t, db, "Person", "name", "updated", id)
	if err := db.View(context.Background(), func(tx *Tx) error {
		iter := tx.FindNodes("Person", "name", "legacy")
		defer closeTestResource(t, iter)
		if iter.Next() {
			t.Fatal("v2 index definition did not maintain an updated property")
		}
		return iter.Error()
	}); err != nil {
		t.Fatal(err)
	}
}

func legacyNodeValue(label string) []byte {
	value := make([]byte, 2+len(label))
	binary.BigEndian.PutUint16(value, uint16(len(label)))
	copy(value[2:], label)
	return value
}

func legacyEdgeKey(prefix byte, anchorID, otherID uuid.UUID, label string) []byte {
	key := make([]byte, 1+16+2+len(label)+16)
	key[0] = prefix
	copy(key[1:], anchorID[:])
	binary.BigEndian.PutUint16(key[17:], uint16(len(label)))
	copy(key[19:], label)
	copy(key[19+len(label):], otherID[:])
	return key
}
