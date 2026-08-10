package pathway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

func TestStoredCorruptionReturnsContextualSentinel(t *testing.T) {
	t.Run("node value", func(t *testing.T) {
		db := openCorruptionTestDatabase(t)
		id := uuid.New()
		secret := "private-label"
		value := append([]byte{0, byte(len(secret))}, secret...)
		value = append(value, 0)
		setCorruptRecord(t, db, encoding.EncodeNodeKey(id), value)

		err := db.View(context.Background(), func(tx *Tx) error {
			label, exists, err := tx.GetNode(id)
			if label != "" || exists {
				t.Fatalf("corrupt node appeared valid as %q, %v", label, exists)
			}
			return err
		})
		assertCorruptRecordError(t, err, "node value", secret)

		paths, err := NewTraversalSource(db).V(id).Path().ToPaths()
		if len(paths) != 0 {
			t.Fatalf("corrupt node produced paths: %v", paths)
		}
		assertCorruptRecordError(t, err, "node value", secret)
	})

	t.Run("node key", func(t *testing.T) {
		db := openCorruptionTestDatabase(t)
		key := append(encoding.EncodeNodeKey(uuid.New()), 0)
		setCorruptRecord(t, db, key, []byte{0, 1, 'N'})

		err := db.View(context.Background(), func(tx *Tx) error {
			iter := tx.ScanNodes()
			defer closeTestResource(t, iter)
			if !iter.Next() {
				t.Fatalf("ScanNodes did not reach corrupt key: %v", iter.Error())
			}
			_, _, err := iter.Node()
			if !errors.Is(iter.Error(), ErrCorruptData) {
				t.Fatalf("iterator error = %v, want ErrCorruptData", iter.Error())
			}
			return err
		})
		assertCorruptRecordError(t, err, "node key", "")
	})

	t.Run("edge key", func(t *testing.T) {
		db := openCorruptionTestDatabase(t)
		source := uuid.New()
		key := append([]byte{encoding.PrefixEdgeOut}, source[:]...)
		key = append(key, 0, 31)
		key = append(key, make([]byte, 32)...)
		setCorruptRecord(t, db, key, encoding.EncodeEdgeValue(uuid.New()))

		err := db.View(context.Background(), func(tx *Tx) error {
			iter := tx.OutEdges(source)
			defer closeTestResource(t, iter)
			if !iter.Next() {
				t.Fatalf("OutEdges did not reach corrupt key: %v", iter.Error())
			}
			_, _, _, err := iter.Edge()
			if !errors.Is(iter.Error(), ErrCorruptData) {
				t.Fatalf("iterator error = %v, want ErrCorruptData", iter.Error())
			}
			return err
		})
		assertCorruptRecordError(t, err, "edge adjacency key", "")
	})

	t.Run("edge value", func(t *testing.T) {
		db := openCorruptionTestDatabase(t)
		source, target, edgeID := uuid.New(), uuid.New(), uuid.New()
		key, err := encoding.EncodeEdgeOutKey(source, target, edgeID, "LINK")
		if err != nil {
			t.Fatal(err)
		}
		value := append(encoding.EncodeEdgeValue(edgeID), 0)
		setCorruptRecord(t, db, key, value)

		err = db.View(context.Background(), func(tx *Tx) error {
			iter := tx.OutEdges(source)
			defer closeTestResource(t, iter)
			if !iter.Next() {
				t.Fatalf("OutEdges did not reach corrupt value: %v", iter.Error())
			}
			_, _, _, err := iter.Edge()
			return err
		})
		assertCorruptRecordError(t, err, "edge adjacency value", "")
	})

	t.Run("property value", func(t *testing.T) {
		db := openCorruptionTestDatabase(t)
		id := uuid.New()
		setCorruptRecord(t, db, encoding.EncodePropertyKey(id), []byte{0xff, 0xff})

		err := db.View(context.Background(), func(tx *Tx) error {
			properties, err := tx.GetProperties(id)
			if properties != nil {
				t.Fatalf("corrupt properties appeared valid: %v", properties)
			}
			return err
		})
		assertCorruptRecordError(t, err, "property value", "")
	})

	t.Run("node index key", func(t *testing.T) {
		db := openCorruptionTestDatabase(t)
		prefix, err := encoding.EncodeIndexPrefix("Person", "name", "alice")
		if err != nil {
			t.Fatal(err)
		}
		setCorruptRecord(t, db, append(prefix, make([]byte, 15)...), nil)

		err = db.View(context.Background(), func(tx *Tx) error {
			iter := tx.FindNodes("Person", "name", "alice")
			defer closeTestResource(t, iter)
			if !iter.Next() {
				t.Fatalf("FindNodes did not reach corrupt key: %v", iter.Error())
			}
			_, _, err := iter.Node()
			return err
		})
		assertCorruptRecordError(t, err, "node index key", "")
	})
}

func openCorruptionTestDatabase(t *testing.T) *Database {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestResource(t, db) })
	return db
}

func setCorruptRecord(t *testing.T, db *Database, key, value []byte) {
	t.Helper()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.Set(key, value, nil)
	}); err != nil {
		t.Fatal(err)
	}
}

func assertCorruptRecordError(t *testing.T, err error, recordType, sensitive string) {
	t.Helper()
	if !errors.Is(err, ErrCorruptData) {
		t.Fatalf("error = %v, want ErrCorruptData", err)
	}
	if !strings.Contains(err.Error(), recordType) {
		t.Fatalf("error %q does not identify %q", err, recordType)
	}
	if sensitive != "" && strings.Contains(err.Error(), sensitive) {
		t.Fatalf("error exposes stored value %q: %v", sensitive, err)
	}
}
