package pathway

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
	"github.com/cockroachdb/pebble/v2/vfs"
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

func TestOpenWithOptions_ClonesCallerOwnedConfiguration(t *testing.T) {
	listener := &pebble.EventListener{}
	filters := map[string]pebble.FilterPolicy{}
	pebbleOptions := &pebble.Options{
		FS:            vfs.Default,
		BytesPerSync:  4096,
		EventListener: listener,
		Filters:       filters,
	}
	pebbleOptions.Levels[0].FilterPolicy = bloom.FilterPolicy(10)
	indexes := []IndexDefinition{{Label: "Person", Property: "name"}}
	opts := Options{PebbleOptions: pebbleOptions, Indexes: indexes}

	db, err := OpenWithOptions(":memory:", opts)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	if pebbleOptions.FS != vfs.Default {
		t.Fatal("OpenWithOptions replaced the caller's Pebble filesystem")
	}
	if listener.BackgroundError != nil {
		t.Fatal("OpenWithOptions populated the caller's event listener")
	}
	if len(filters) != 0 {
		t.Fatal("OpenWithOptions populated the caller's filter map")
	}
	if db.options.PebbleOptions == pebbleOptions {
		t.Fatal("database retained the caller's Pebble options pointer")
	}
	if db.options.PebbleOptions.FS == vfs.Default {
		t.Fatal("in-memory database did not install a private memory filesystem")
	}
	if len(db.options.Indexes) != 1 || &db.options.Indexes[0] == &indexes[0] {
		t.Fatal("database retained the caller's index slice")
	}

	pebbleOptions.BytesPerSync = 8192
	indexes[0].Label = "Changed"
	if db.options.PebbleOptions.BytesPerSync != 4096 {
		t.Fatal("caller mutation changed the database's Pebble options")
	}
	if db.options.Indexes[0].Label != "Person" {
		t.Fatal("caller mutation changed the database's index options")
	}
}

func TestOpenWithOptions_ReusesConfigurationConcurrently(t *testing.T) {
	const workers = 16
	opts := Options{
		PebbleOptions: &pebble.Options{
			FS:            vfs.Default,
			BytesPerSync:  4096,
			EventListener: &pebble.EventListener{},
			Filters:       map[string]pebble.FilterPolicy{},
		},
		Indexes: []IndexDefinition{{Label: "Person", Property: "name"}},
	}
	opts.PebbleOptions.Levels[0].FilterPolicy = bloom.FilterPolicy(10)

	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			db, err := OpenWithOptions(":memory:", opts)
			if err != nil {
				errs <- err
				return
			}
			if err := db.Close(); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent OpenWithOptions: %v", err)
	}
	if opts.PebbleOptions.FS != vfs.Default {
		t.Fatal("concurrent opens replaced the shared Pebble filesystem")
	}
	if opts.PebbleOptions.EventListener.BackgroundError != nil {
		t.Fatal("concurrent opens populated the shared event listener")
	}
	if len(opts.PebbleOptions.Filters) != 0 {
		t.Fatal("concurrent opens populated the shared filter map")
	}
}
