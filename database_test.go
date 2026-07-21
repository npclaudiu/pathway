package pathway

import (
	"os"
	"testing"
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
