package pathway

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
)

func TestExternalIDOwnsExactComponents(t *testing.T) {
	scope := []byte("https://example.test/repository.git")
	digest := bytes.Repeat([]byte{0xab}, 20)
	id, err := NewScopedExternalID("git-object/sha1", scope, digest)
	if err != nil {
		t.Fatal(err)
	}
	scope[0], digest[0] = 'X', 0
	if id.Namespace() != "git-object/sha1" {
		t.Fatalf("namespace = %q", id.Namespace())
	}
	if id.Scope()[0] != 'h' || id.Value()[0] != 0xab {
		t.Fatal("constructor retained caller-owned bytes")
	}
	returnedScope, returnedValue := id.Scope(), id.Value()
	returnedScope[0], returnedValue[0] = 'Y', 1
	if id.Scope()[0] != 'h' || id.Value()[0] != 0xab {
		t.Fatal("accessor exposed mutable internal bytes")
	}

	for name, makeID := range map[string]func() (ExternalID, error){
		"empty namespace": func() (ExternalID, error) { return NewExternalID("", []byte{1}) },
		"invalid namespace": func() (ExternalID, error) {
			return NewExternalID(string([]byte{0xff}), []byte{1})
		},
		"empty value": func() (ExternalID, error) { return NewExternalID("namespace", nil) },
		"long namespace": func() (ExternalID, error) {
			return NewExternalID(strings.Repeat("n", MaxExternalIDPartBytes+1), []byte{1})
		},
		"long scope": func() (ExternalID, error) {
			return NewScopedExternalID("namespace", make([]byte, MaxExternalIDPartBytes+1), []byte{1})
		},
		"long value": func() (ExternalID, error) {
			return NewExternalID("namespace", make([]byte, MaxExternalIDPartBytes+1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := makeID(); !errors.Is(err, ErrInvalidExternalID) {
				t.Fatalf("error = %v, want ErrInvalidExternalID", err)
			}
		})
	}
}

func TestExternalIDBindingLifecycleAndGitForms(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)

	sha1, err := NewExternalID("git-object/sha1", bytes.Repeat([]byte{0x11}, 20))
	if err != nil {
		t.Fatal(err)
	}
	sha256, err := NewExternalID("git-object/sha256", bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repoA, err := NewScopedExternalID("git-object/sha1", []byte("repo-a"), bytes.Repeat([]byte{0x33}, 20))
	if err != nil {
		t.Fatal(err)
	}
	repoB, err := NewScopedExternalID("git-object/sha1", []byte("repo-b"), bytes.Repeat([]byte{0x33}, 20))
	if err != nil {
		t.Fatal(err)
	}
	nodeA, nodeB := uuid.New(), uuid.New()

	if err := db.Update(context.Background(), func(tx *Tx) error {
		if err := tx.PutNode(nodeA, "Commit"); err != nil {
			return err
		}
		if err := tx.PutNode(nodeB, "Commit"); err != nil {
			return err
		}
		for _, externalID := range []ExternalID{sha1, sha256, repoA, repoB} {
			if err := tx.BindExternalID(externalID, nodeA); err != nil {
				return err
			}
		}
		if err := tx.BindExternalID(sha1, nodeA); err != nil {
			return err
		}
		if err := tx.BindExternalID(sha1, nodeB); !errors.Is(err, ErrExternalIDConflict) {
			t.Fatalf("conflicting bind error = %v", err)
		}
		if err := tx.UnbindExternalID(repoB); err != nil {
			return err
		}
		return tx.BindExternalID(repoB, nodeB)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(context.Background(), func(tx *Tx) error {
		for externalID, want := range map[string]struct {
			id   ExternalID
			node uuid.UUID
		}{
			"sha1":   {sha1, nodeA},
			"sha256": {sha256, nodeA},
			"repo-a": {repoA, nodeA},
			"repo-b": {repoB, nodeB},
		} {
			got, exists, err := tx.ResolveExternalID(want.id)
			if err != nil {
				return err
			}
			if !exists || got != want.node {
				t.Fatalf("%s resolved to %s/%v, want %s", externalID, got, exists, want.node)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Update(context.Background(), func(tx *Tx) error { return tx.DeleteNode(nodeA) }); err != nil {
		t.Fatal(err)
	}
	if err := db.View(context.Background(), func(tx *Tx) error {
		for _, removed := range []ExternalID{sha1, sha256, repoA} {
			if _, exists, err := tx.ResolveExternalID(removed); err != nil || exists {
				t.Fatalf("deleted binding resolved: exists=%v err=%v", exists, err)
			}
			reverseKey, err := removed.reverseKey(nodeA)
			if err != nil {
				return err
			}
			if _, err := tx.Get(reverseKey); !errors.Is(err, pebble.ErrNotFound) {
				t.Fatalf("deleted reverse binding error = %v, want pebble.ErrNotFound", err)
			}
		}
		got, exists, err := tx.ResolveExternalID(repoB)
		if err != nil || !exists || got != nodeB {
			t.Fatalf("unrelated scoped binding = %s/%v/%v", got, exists, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPutNodeByExternalIDIsAtomicAndIdempotent(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	externalID, err := NewExternalID("import/user", []byte("alice"))
	if err != nil {
		t.Fatal(err)
	}

	var first, second uuid.UUID
	if err := db.Update(context.Background(), func(tx *Tx) error {
		var err error
		first, err = tx.PutNodeByExternalID(externalID, "User")
		if err != nil {
			return err
		}
		second, err = tx.PutNodeByExternalID(externalID, "Person")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("replayed import IDs = %s and %s", first, second)
	}
	if err := db.View(context.Background(), func(tx *Tx) error {
		label, exists, err := tx.GetNode(first)
		if err != nil || !exists || label != "Person" {
			t.Fatalf("upserted node = %q/%v/%v", label, exists, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rollbackID, err := NewExternalID("import/user", []byte("rollback"))
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("abort import")
	var staged uuid.UUID
	err = db.Update(context.Background(), func(tx *Tx) error {
		staged, err = tx.PutNodeByExternalID(rollbackID, "User")
		if err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("rollback error = %v", err)
	}
	if err := db.View(context.Background(), func(tx *Tx) error {
		if _, exists, err := tx.ResolveExternalID(rollbackID); err != nil || exists {
			t.Fatalf("rolled-back binding = %v/%v", exists, err)
		}
		if _, exists, err := tx.GetNode(staged); err != nil || exists {
			t.Fatalf("rolled-back node = %v/%v", exists, err)
		}
		reverseKey, err := rollbackID.reverseKey(staged)
		if err != nil {
			return err
		}
		if _, err := tx.Get(reverseKey); !errors.Is(err, pebble.ErrNotFound) {
			t.Fatalf("rolled-back reverse binding error = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentExternalIDImportsConverge(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	externalID, err := NewExternalID("git-object/sha1", bytes.Repeat([]byte{0x44}, 20))
	if err != nil {
		t.Fatal(err)
	}

	const workers = 24
	start := make(chan struct{})
	results := make(chan uuid.UUID, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			var nodeID uuid.UUID
			err := db.Update(context.Background(), func(tx *Tx) error {
				var err error
				nodeID, err = tx.PutNodeByExternalID(externalID, "Commit")
				return err
			})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- nodeID
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var canonical uuid.UUID
	for nodeID := range results {
		if canonical == uuid.Nil {
			canonical = nodeID
		}
		if nodeID != canonical {
			t.Fatalf("concurrent import returned %s and %s", canonical, nodeID)
		}
	}

	if err := db.View(context.Background(), func(tx *Tx) error {
		iter := tx.ScanNodes()
		defer closeTestResource(t, iter)
		count := 0
		for iter.Next() {
			count++
		}
		if err := iter.Error(); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("concurrent import created %d nodes, want 1", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestExternalIDCorruptionAbortsReadsAndDeletion(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	externalID, err := NewExternalID("source/item", []byte("42"))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	if err := db.Update(context.Background(), func(tx *Tx) error {
		if err := tx.PutNode(nodeID, "Item"); err != nil {
			return err
		}
		if err := tx.BindExternalID(externalID, nodeID); err != nil {
			return err
		}
		reverseKey, err := externalID.reverseKey(nodeID)
		if err != nil {
			return err
		}
		return tx.Set(reverseKey, []byte{1}, nil)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(context.Background(), func(tx *Tx) error {
		_, _, err := tx.ResolveExternalID(externalID)
		if !errors.Is(err, ErrExternalIDCorrupt) {
			t.Fatalf("resolve error = %v, want ErrExternalIDCorrupt", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = db.Update(context.Background(), func(tx *Tx) error { return tx.DeleteNode(nodeID) })
	if !errors.Is(err, ErrExternalIDCorrupt) {
		t.Fatalf("DeleteNode error = %v, want ErrExternalIDCorrupt", err)
	}
	if err := db.View(context.Background(), func(tx *Tx) error {
		_, exists, err := tx.GetNode(nodeID)
		if err != nil || !exists {
			t.Fatalf("failed deletion committed: exists=%v err=%v", exists, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	forwardKey, err := externalID.forwardKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(context.Background(), func(tx *Tx) error {
		return tx.Set(forwardKey, []byte("short"), nil)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.View(context.Background(), func(tx *Tx) error {
		_, _, err := tx.ResolveExternalID(externalID)
		if !errors.Is(err, ErrExternalIDCorrupt) || !strings.Contains(err.Error(), "forward") {
			t.Fatalf("malformed forward error = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

}

func TestTraversalStartsFromExternalIDs(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, db)
	firstID, _ := NewExternalID("source/node", []byte("first"))
	secondID, _ := NewExternalID("source/node", []byte("second"))
	missingID, _ := NewExternalID("source/node", []byte("missing"))
	var firstNode, secondNode uuid.UUID
	if err := db.Update(context.Background(), func(tx *Tx) error {
		var err error
		firstNode, err = tx.PutNodeByExternalID(firstID, "First")
		if err != nil {
			return err
		}
		secondNode, err = tx.PutNodeByExternalID(secondID, "Second")
		return err
	}); err != nil {
		t.Fatal(err)

	}

	g := NewTraversalSource(db)
	pipeline, err := g.VExternal(secondID, missingID, firstID)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := pipeline.IDs().ToIDs()
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{secondNode, firstNode}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("external traversal IDs = %v, want %v", ids, want)
	}
	if pipeline, err := g.VExternal(ExternalID{}); pipeline != nil || !errors.Is(err, ErrInvalidExternalID) {
		t.Fatalf("zero external ID = %#v/%v", pipeline, err)
	}
	all, err := g.VExternal()
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := all.ToNodes()
	if err != nil || len(nodes) != 2 {
		t.Fatalf("empty VExternal scan = %#v/%v", nodes, err)
	}
}
