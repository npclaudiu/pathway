package pathway

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble/v2"
)

func TestOpenWritesAndAcceptsCurrentSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	rawDB, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	value, closer, err := rawDB.Get(schemaVersionKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != 4 || binary.BigEndian.Uint32(value) != currentSchemaVersion {
		t.Fatalf("schema marker = %x, want version %d", value, currentSchemaVersion)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
}

func TestOpenRejectsUnsupportedSchemaVersions(t *testing.T) {
	for _, version := range []uint32{2, 3, currentSchemaVersion - 1, currentSchemaVersion + 1} {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			dir := t.TempDir()
			rawDB, err := pebble.Open(dir, &pebble.Options{})
			if err != nil {
				t.Fatal(err)
			}
			value := make([]byte, 4)
			binary.BigEndian.PutUint32(value, version)
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
			if !strings.Contains(err.Error(), "recreate") {
				t.Fatalf("error does not explain recovery: %v", err)
			}
		})
	}
}

func TestOpenRejectsUnversionedDevelopmentStore(t *testing.T) {
	dir := t.TempDir()
	rawDB, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Set([]byte("legacy"), []byte("value"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dir)
	if db != nil {
		_ = db.Close()
		t.Fatal("Open returned a database for an unversioned store")
	}
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("expected ErrUnsupportedSchema, got %v", err)
	}
	if !strings.Contains(err.Error(), "recreate") {
		t.Fatalf("error does not explain recovery: %v", err)
	}
}
