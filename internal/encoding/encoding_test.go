package encoding

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestEncodeNodeKey(t *testing.T) {
	id := uuid.New()
	key := EncodeNodeKey(id)

	if len(key) != 17 {
		t.Errorf("expected length 17, got %d", len(key))
	}
	if key[0] != PrefixNode {
		t.Errorf("expected prefix %x, got %x", PrefixNode, key[0])
	}
	if !bytes.Equal(key[1:], id[:]) {
		t.Error("uuid mismatch")
	}
}

func TestEncodeEdgeOutKey(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	label := "TEST"

	key, err := EncodeEdgeOutKey(src, dst, label)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Layout: [Prefix(1)] [Src(16)] [Len(2)] [Label(4)] [Dst(16)]
	// Total: 1+16+2+4+16 = 39
	expectedLen := 39
	if len(key) != expectedLen {
		t.Errorf("expected length %d, got %d", expectedLen, len(key))
	}
	if key[0] != PrefixEdgeOut {
		t.Errorf("expected prefix %x, got %x", PrefixEdgeOut, key[0])
	}
}

func TestEncodeEdgeOutKey_TooLong(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	label := strings.Repeat("A", 65536) // Too long

	_, err := EncodeEdgeOutKey(src, dst, label)
	if err != ErrInvalidLabel {
		t.Errorf("expected ErrInvalidLabel, got %v", err)
	}
}

func TestEncodeEdgeInKey(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	label := "TEST"

	key, err := EncodeEdgeInKey(src, dst, label)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Layout: [Prefix(1)] [Dst(16)] [Len(2)] [Label(4)] [Src(16)]
	// Total: 39
	expectedLen := 39
	if len(key) != expectedLen {
		t.Errorf("expected length %d, got %d", expectedLen, len(key))
	}
	if key[0] != PrefixEdgeIn {
		t.Errorf("expected prefix %x, got %x", PrefixEdgeIn, key[0])
	}
}

func TestEncodeEdgeInKey_TooLong(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	label := strings.Repeat("A", 65536)

	_, err := EncodeEdgeInKey(src, dst, label)
	if err != ErrInvalidLabel {
		t.Errorf("expected ErrInvalidLabel, got %v", err)
	}
}

func TestEncodeEdgeValue(t *testing.T) {
	id := uuid.New()
	val := EncodeEdgeValue(id)
	if len(val) != 16 {
		t.Errorf("expected length 16, got %d", len(val))
	}
	if !bytes.Equal(val, id[:]) {
		t.Error("uuid mismatch")
	}
}

func TestEncodePropertyKey(t *testing.T) {
	id := uuid.New()
	key := EncodePropertyKey(id)
	if len(key) != 17 {
		t.Errorf("expected length 17, got %d", len(key))
	}
	if key[0] != PrefixProperties {
		t.Errorf("expected prefix %x, got %x", PrefixProperties, key[0])
	}
}

func TestDecodeLabel(t *testing.T) {
	// 1. Valid
	lbl := "HELLO"
	b := make([]byte, 2+len(lbl))
	binary.BigEndian.PutUint16(b, uint16(len(lbl)))
	copy(b[2:], []byte(lbl))

	res, n := DecodeLabel(b)
	if res != lbl {
		t.Errorf("expected %s, got %s", lbl, res)
	}
	if n != len(b) {
		t.Errorf("expected consumed %d, got %d", len(b), n)
	}

	// 2. Too Short (No length)
	_, n = DecodeLabel([]byte{0x00})
	if n != 0 {
		t.Errorf("expected error/0, got %d", n)
	}

	// 3. Too Short (Payload missing)
	b2 := make([]byte, 2)
	binary.BigEndian.PutUint16(b2, 100) // Expect 100 bytes
	_, n = DecodeLabel(b2)
	if n != 0 {
		t.Errorf("expected error/0, got %d", n)
	}
}

func TestNumericToBytes(t *testing.T) {
	// Int
	v := 42
	b := numericToBytes(v)
	if len(b) != 8 {
		t.Errorf("int should be 8 bytes")
	}

	// Float
	f := 3.14
	b2 := numericToBytes(f)
	if len(b2) != 8 {
		t.Errorf("float should be 8 bytes")
	}
	decoded := math.Float64frombits(binary.BigEndian.Uint64(b2))
	if decoded != f {
		t.Errorf("float mismatch")
	}

	// Invalid
	b3 := numericToBytes("string")
	if b3 != nil {
		t.Errorf("string should return nil")
	}
}
