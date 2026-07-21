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
	edgeID := uuid.New()
	label := "TEST"

	key, err := EncodeEdgeOutKey(src, dst, edgeID, label)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Layout: [Prefix(1)] [Src(16)] [Len(2)] [Label(4)] [Dst(16)] [Edge(16)]
	// Total: 1+16+2+4+16+16 = 55
	expectedLen := 55
	if len(key) != expectedLen {
		t.Errorf("expected length %d, got %d", expectedLen, len(key))
	}
	if key[0] != PrefixEdgeOut {
		t.Errorf("expected prefix %x, got %x", PrefixEdgeOut, key[0])
	}
	if !bytes.Equal(key[len(key)-16:], edgeID[:]) {
		t.Error("edge uuid mismatch")
	}
}

func TestEncodeEdgeOutKey_TooLong(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	edgeID := uuid.New()
	label := strings.Repeat("A", 65536) // Too long

	_, err := EncodeEdgeOutKey(src, dst, edgeID, label)
	if err != ErrInvalidLabel {
		t.Errorf("expected ErrInvalidLabel, got %v", err)
	}
}

func TestEncodeEdgeInKey(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	edgeID := uuid.New()
	label := "TEST"

	key, err := EncodeEdgeInKey(src, dst, edgeID, label)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Layout: [Prefix(1)] [Dst(16)] [Len(2)] [Label(4)] [Src(16)] [Edge(16)]
	// Total: 55
	expectedLen := 55
	if len(key) != expectedLen {
		t.Errorf("expected length %d, got %d", expectedLen, len(key))
	}
	if key[0] != PrefixEdgeIn {
		t.Errorf("expected prefix %x, got %x", PrefixEdgeIn, key[0])
	}
	if !bytes.Equal(key[len(key)-16:], edgeID[:]) {
		t.Error("edge uuid mismatch")
	}
}

func TestEncodeEdgeInKey_TooLong(t *testing.T) {
	src := uuid.New()
	dst := uuid.New()
	edgeID := uuid.New()
	label := strings.Repeat("A", 65536)

	_, err := EncodeEdgeInKey(src, dst, edgeID, label)
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

func TestEdgeIDReverseIndexEncoding(t *testing.T) {
	src, dst, edgeID := uuid.New(), uuid.New(), uuid.New()
	label := "KNOWS"

	key := EncodeEdgeIDKey(edgeID)
	if len(key) != 17 || key[0] != PrefixEdgeByID || !bytes.Equal(key[1:], edgeID[:]) {
		t.Fatalf("unexpected reverse-index key: %x", key)
	}

	record, err := EncodeEdgeRecord(src, dst, label)
	if err != nil {
		t.Fatal(err)
	}
	if len(record) != 16+16+2+len(label) {
		t.Fatalf("unexpected record length: %d", len(record))
	}
	gotSrc, gotDst, gotLabel, err := DecodeEdgeRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if gotSrc != src || gotDst != dst || gotLabel != label {
		t.Fatalf("unexpected decoded record: %s %s %q", gotSrc, gotDst, gotLabel)
	}
}

func TestDecodeEdgeRecord_Invalid(t *testing.T) {
	if _, _, _, err := DecodeEdgeRecord(make([]byte, 33)); err != ErrInvalidValueFormat {
		t.Fatalf("expected ErrInvalidValueFormat, got %v", err)
	}

	record, err := EncodeEdgeRecord(uuid.New(), uuid.New(), "edge")
	if err != nil {
		t.Fatal(err)
	}
	record = append(record, 0)
	if _, _, _, err := DecodeEdgeRecord(record); err != ErrInvalidValueFormat {
		t.Fatalf("expected ErrInvalidValueFormat, got %v", err)
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

func TestEncodeIndexKey_Golden(t *testing.T) {
	id := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	key, err := EncodeIndexKey("A", "b", "x", id)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		PrefixIndex,
		0, 1, 'A',
		0, 1, 'b',
		indexValueString,
		0, 0, 0, 1, 'x',
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
	}
	if !bytes.Equal(key, want) {
		t.Fatalf("index key mismatch:\n got %x\nwant %x", key, want)
	}
}

func TestEncodeIndexPrefix_TypedAndDelimited(t *testing.T) {
	stringOne, err := EncodeIndexPrefix("Node", "value", "1")
	if err != nil {
		t.Fatal(err)
	}
	numberOne, err := EncodeIndexPrefix("Node", "value", 1)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stringOne, numberOne) {
		t.Fatal("string and numeric values collided")
	}

	short, err := EncodeIndexPrefix("Node", "value", "a")
	if err != nil {
		t.Fatal(err)
	}
	long, err := EncodeIndexPrefix("Node", "value", "ab")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(long, short) {
		t.Fatal("length-delimited value still permits prefix matches")
	}
}

func TestEncodeIndexPrefix_ValidatesLengths(t *testing.T) {
	tooLong := strings.Repeat("x", math.MaxUint16+1)
	if _, err := EncodeIndexPrefix(tooLong, "key", "value"); err != ErrInvalidIndexKey {
		t.Fatalf("expected ErrInvalidIndexKey for label, got %v", err)
	}
	if _, err := EncodeIndexPrefix("label", tooLong, "value"); err != ErrInvalidIndexKey {
		t.Fatalf("expected ErrInvalidIndexKey for property key, got %v", err)
	}
}
