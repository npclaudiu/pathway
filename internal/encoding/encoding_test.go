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

func TestDecodeNodeKeyRequiresCompleteRecord(t *testing.T) {
	id := uuid.New()
	decoded, err := DecodeNodeKey(EncodeNodeKey(id))
	if err != nil || decoded != id {
		t.Fatalf("DecodeNodeKey() = %s, %v; want %s, nil", decoded, err, id)
	}
	for _, key := range [][]byte{
		nil,
		append([]byte{PrefixNode}, id[:15]...),
		append(EncodeNodeKey(id), 0),
		append([]byte{PrefixProperties}, id[:]...),
	} {
		if _, err := DecodeNodeKey(key); err != ErrInvalidKeyFormat {
			t.Fatalf("DecodeNodeKey(%x) error = %v", key, err)
		}
	}
}

func TestExternalIDEncodingRoundTripAndFraming(t *testing.T) {
	nodeID := uuid.New()
	namespace := "git-object/sha256"
	scope := []byte("repo\x00scope")
	value := []byte{0, 1, 2, 0, 3}

	forward, err := EncodeExternalIDKey(namespace, scope, value)
	if err != nil {
		t.Fatal(err)
	}
	gotNamespace, gotScope, gotValue, err := DecodeExternalIDKey(forward)
	if err != nil {
		t.Fatal(err)
	}
	if gotNamespace != namespace || !bytes.Equal(gotScope, scope) || !bytes.Equal(gotValue, value) {
		t.Fatalf("decoded external ID = %q/%x/%x", gotNamespace, gotScope, gotValue)
	}

	reverse, err := EncodeExternalIDReverseKey(nodeID, namespace, scope, value)
	if err != nil {
		t.Fatal(err)
	}
	gotNodeID, gotNamespace, gotScope, gotValue, err := DecodeExternalIDReverseKey(reverse)
	if err != nil {
		t.Fatal(err)
	}
	if gotNodeID != nodeID || gotNamespace != namespace ||
		!bytes.Equal(gotScope, scope) || !bytes.Equal(gotValue, value) {
		t.Fatalf("decoded reverse external ID = %s/%q/%x/%x", gotNodeID, gotNamespace, gotScope, gotValue)
	}
	if !bytes.HasPrefix(reverse, EncodeExternalIDReversePrefix(nodeID)) {
		t.Fatal("reverse key does not use the node cleanup prefix")
	}

	// Length framing prevents component-boundary collisions.
	first, err := EncodeExternalIDKey("a", []byte("bc"), []byte("d"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeExternalIDKey("ab", []byte("c"), []byte("d"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("distinct external ID components encoded to the same key")
	}
}

func TestExternalIDEncodingRejectsMalformedData(t *testing.T) {
	if _, err := EncodeExternalIDKey("", nil, []byte("value")); err != ErrInvalidExternalID {
		t.Fatalf("empty namespace error = %v", err)
	}
	if _, err := EncodeExternalIDKey("namespace", nil, nil); err != ErrInvalidExternalID {
		t.Fatalf("empty value error = %v", err)
	}
	if _, _, _, err := DecodeExternalIDKey([]byte{PrefixExternalID, 0, 1}); err != ErrInvalidKeyFormat {
		t.Fatalf("truncated key error = %v", err)
	}
	if _, _, _, _, err := DecodeExternalIDReverseKey(make([]byte, 16)); err != ErrInvalidKeyFormat {
		t.Fatalf("truncated reverse key error = %v", err)
	}
	if _, err := DecodeExternalIDValue(make([]byte, 15)); err != ErrInvalidValueFormat {
		t.Fatalf("short forward value error = %v", err)
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

func TestEncodeEdgeLabelPrefixesMatchFullKeys(t *testing.T) {
	src, dst, edgeID := uuid.New(), uuid.New(), uuid.New()
	for name, test := range map[string]struct {
		prefix func() ([]byte, error)
		key    func() ([]byte, error)
	}{
		"outgoing": {
			prefix: func() ([]byte, error) { return EncodeEdgeOutPrefix(src, "LINK") },
			key:    func() ([]byte, error) { return EncodeEdgeOutKey(src, dst, edgeID, "LINK") },
		},
		"incoming": {
			prefix: func() ([]byte, error) { return EncodeEdgeInPrefix(dst, "LINK") },
			key:    func() ([]byte, error) { return EncodeEdgeInKey(src, dst, edgeID, "LINK") },
		},
	} {
		t.Run(name, func(t *testing.T) {
			prefix, err := test.prefix()
			if err != nil {
				t.Fatal(err)
			}
			key, err := test.key()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(key, prefix) {
				t.Fatalf("full key %x does not start with %x", key, prefix)
			}
			if len(prefix) != 1+16+2+len("LINK") {
				t.Fatalf("prefix length = %d", len(prefix))
			}
		})
	}

	tooLong := strings.Repeat("x", math.MaxUint16+1)
	if _, err := EncodeEdgeOutPrefix(src, tooLong); err != ErrInvalidLabel {
		t.Fatalf("outgoing long-label error = %v", err)
	}
	if _, err := EncodeEdgeInPrefix(dst, tooLong); err != ErrInvalidLabel {
		t.Fatalf("incoming long-label error = %v", err)
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

func TestDecodeEdgeKeyAndValueRequireCompleteRecords(t *testing.T) {
	anchor, other, edgeID := uuid.New(), uuid.New(), uuid.New()
	key, err := EncodeEdgeOutKey(anchor, other, edgeID, "LINK")
	if err != nil {
		t.Fatal(err)
	}
	gotAnchor, gotOther, gotEdgeID, gotLabel, err := DecodeEdgeKey(key)
	if err != nil || gotAnchor != anchor || gotOther != other ||
		gotEdgeID != edgeID || gotLabel != "LINK" {
		t.Fatalf(
			"DecodeEdgeKey() = %s, %s, %s, %q, %v",
			gotAnchor,
			gotOther,
			gotEdgeID,
			gotLabel,
			err,
		)
	}

	malformedLength := append([]byte(nil), key...)
	binary.BigEndian.PutUint16(malformedLength[17:], uint16(len("LINK")+1))
	for _, invalid := range [][]byte{
		key[:len(key)-1],
		append(append([]byte(nil), key...), 0),
		malformedLength,
		append([]byte{PrefixNode}, key[1:]...),
	} {
		if _, _, _, _, err := DecodeEdgeKey(invalid); err != ErrInvalidKeyFormat {
			t.Fatalf("DecodeEdgeKey(%x) error = %v", invalid, err)
		}
	}

	decodedValue, err := DecodeEdgeValue(EncodeEdgeValue(edgeID))
	if err != nil || decodedValue != edgeID {
		t.Fatalf("DecodeEdgeValue() = %s, %v; want %s, nil", decodedValue, err, edgeID)
	}
	for _, invalid := range [][]byte{make([]byte, 15), make([]byte, 17)} {
		if _, err := DecodeEdgeValue(invalid); err != ErrInvalidValueFormat {
			t.Fatalf("DecodeEdgeValue(%x) error = %v", invalid, err)
		}
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

func TestDecodeNodeValueRequiresCompleteRecord(t *testing.T) {
	value := []byte{0, 4, 'N', 'o', 'd', 'e'}
	if label, err := DecodeNodeValue(value); err != nil || label != "Node" {
		t.Fatalf("DecodeNodeValue() = %q, %v; want Node, nil", label, err)
	}
	for _, invalid := range [][]byte{
		nil,
		{0},
		{0, 2, 'A'},
		append(append([]byte(nil), value...), 0),
	} {
		if _, err := DecodeNodeValue(invalid); err != ErrInvalidValueFormat {
			t.Fatalf("DecodeNodeValue(%x) error = %v", invalid, err)
		}
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
	unsignedOne, err := EncodeIndexPrefix("Node", "value", uint64(1))
	if err != nil {
		t.Fatal(err)
	}
	floatOne, err := EncodeIndexPrefix("Node", "value", float64(1))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(numberOne, unsignedOne) || bytes.Equal(numberOne, floatOne) || bytes.Equal(unsignedOne, floatOne) {
		t.Fatal("signed, unsigned, and floating-point values collided")
	}

	largeInteger, err := EncodeIndexPrefix("Node", "value", int64(1<<53))
	if err != nil {
		t.Fatal(err)
	}
	adjacentInteger, err := EncodeIndexPrefix("Node", "value", int64(1<<53+1))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(largeInteger, adjacentInteger) {
		t.Fatal("adjacent integers above float64 precision collided")
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

func TestIndexDefinitionKey_Golden(t *testing.T) {
	key, err := EncodeIndexDefinitionKey("Person", "name")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		PrefixIndexDef,
		0, 6, 'P', 'e', 'r', 's', 'o', 'n',
		0, 4, 'n', 'a', 'm', 'e',
	}
	if !bytes.Equal(key, want) {
		t.Fatalf("definition key mismatch:\n got %x\nwant %x", key, want)
	}
	label, property, err := DecodeIndexDefinitionKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if label != "Person" || property != "name" {
		t.Fatalf("decoded definition = (%q, %q)", label, property)
	}
}

func TestDecodeIndexDefinitionKey_Invalid(t *testing.T) {
	for _, key := range [][]byte{
		nil,
		{PrefixIndexDef},
		{PrefixIndexDef, 0, 1, 'A', 0, 2, 'x'},
		{PrefixNode, 0, 0, 0, 0},
	} {
		if _, _, err := DecodeIndexDefinitionKey(key); err != ErrInvalidKeyFormat {
			t.Fatalf("DecodeIndexDefinitionKey(%x) error = %v", key, err)
		}
	}
}
