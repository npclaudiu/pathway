package pathway

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
)

func closeTestResource(t testing.TB, closer interface{ Close() error }) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Errorf("close resource: %v", err)
	}
}

func removeTestDirectory(t testing.TB, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Errorf("remove test directory: %v", err)
	}
}

func TestOpenClose(t *testing.T) {
	dir, err := os.MkdirTemp("", "pathway-test")
	if err != nil {
		t.Fatal(err)
	}
	defer removeTestDirectory(t, dir)

	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestOpenInMemory(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	defer closeTestResource(t, db)
}

func TestOpenWithOptions_Durability(t *testing.T) {
	for _, test := range []struct {
		name     string
		options  Options
		wantSync bool
	}{
		{name: "zero value default", options: Options{}, wantSync: true},
		{name: "explicit sync", options: Options{Durability: DurabilitySync}, wantSync: true},
		{name: "no sync", options: Options{Durability: DurabilityNoSync}, wantSync: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := OpenWithOptions(":memory:", test.options)
			if err != nil {
				t.Fatal(err)
			}
			defer closeTestResource(t, db)

			if db.writeOpt.Sync != test.wantSync {
				t.Fatalf("commit sync = %t, want %t", db.writeOpt.Sync, test.wantSync)
			}
			id := uuid.New()
			if err := db.Update(context.Background(), func(tx *Tx) error {
				return tx.PutNode(id, "Person")
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.View(context.Background(), func(tx *Tx) error {
				_, exists, err := tx.GetNode(id)
				if err != nil {
					return err
				}
				if !exists {
					t.Fatal("successful update was not visible")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOpenWithOptions_RejectsInvalidDurability(t *testing.T) {
	db, err := OpenWithOptions(":memory:", Options{Durability: DurabilityMode(255)})
	if db != nil {
		_ = db.Close()
		t.Fatal("OpenWithOptions returned a database for an invalid durability mode")
	}
	if !errors.Is(err, ErrInvalidDurability) {
		t.Fatalf("error = %v, want ErrInvalidDurability", err)
	}
}
