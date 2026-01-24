package pathway

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

func TestSnapshot(t *testing.T) {
	db, _ := Open(":memory:")
	defer db.Close()
	ctx := context.Background()

	// 1. Create Data
	id := uuid.New()
	db.Update(ctx, func(tx *Tx) error {
		return tx.PutNode(id, "SnapNode")
	})

	// 2. Take Snapshot
	snap, err := db.NewSnapshot(ctx)
	if err != nil {
		t.Fatalf("NewSnapshot failed: %v", err)
	}
	defer snap.Close()

	// 3. Verify Get
	key := encoding.EncodeNodeKey(id)
	val, err := snap.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(val) == 0 {
		t.Error("expected non-empty val")
	}

	// 4. Update DB (should not affect snapshot)
	db.Update(ctx, func(tx *Tx) error {
		return tx.DeleteNode(id)
	})

	val2, err := snap.Get(key)
	if err != nil {
		t.Errorf("Snapshot view should persist: %v", err)
	}
	if len(val2) == 0 {
		t.Error("expected data in snapshot")
	}

	// 5. Get Missing
	missingKey := encoding.EncodeNodeKey(uuid.New())
	_, err = snap.Get(missingKey)
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}

	// 6. Nil Safety
	var nilSnap *Snapshot
	if err := nilSnap.Close(); err != nil {
		t.Error("Close on nil should be safe")
	}
	if _, err := nilSnap.Get(key); err != ErrInvalidSnapshot {
		t.Error("Get on nil should return error")
	}
}

func TestSnapshot_InvalidDB(t *testing.T) {
	var db *Database
	_, err := db.NewSnapshot(context.Background())
	if err != ErrInvalidDatabase {
		t.Errorf("expected ErrInvalidDatabase, got %v", err)
	}
}
