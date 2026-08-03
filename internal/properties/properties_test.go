package properties

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"

	propertypb "github.com/npclaudiu/pathway/internal/proto"
	"google.golang.org/protobuf/proto"
)

func TestMarshalUnmarshalPreservesTypedValues(t *testing.T) {
	input := map[string]interface{}{
		"string":    "value",
		"bool":      true,
		"float32":   float32(3.25),
		"float64":   3.5,
		"int":       int(-42),
		"int8":      int8(-8),
		"int16":     int16(-16),
		"int32":     int32(-32),
		"int64":     int64(math.MinInt64),
		"uint":      uint(42),
		"uint8":     uint8(8),
		"uint16":    uint16(16),
		"uint32":    uint32(32),
		"uint64":    uint64(math.MaxUint64),
		"bytes":     []byte{0, 1, 2, 255},
		"nil":       nil,
		"list":      []interface{}{int64(math.MaxInt64), uint64(math.MaxUint64), "item", nil},
		"map":       map[string]interface{}{"signed": int64(-1), "data": []byte("nested")},
		"emptyList": []interface{}{},
		"emptyMap":  map[string]interface{}{},
	}
	want := map[string]interface{}{
		"string":    "value",
		"bool":      true,
		"float32":   float64(3.25),
		"float64":   3.5,
		"int":       int64(-42),
		"int8":      int64(-8),
		"int16":     int64(-16),
		"int32":     int64(-32),
		"int64":     int64(math.MinInt64),
		"uint":      uint64(42),
		"uint8":     uint64(8),
		"uint16":    uint64(16),
		"uint32":    uint64(32),
		"uint64":    uint64(math.MaxUint64),
		"bytes":     []byte{0, 1, 2, 255},
		"nil":       nil,
		"list":      []interface{}{int64(math.MaxInt64), uint64(math.MaxUint64), "item", nil},
		"map":       map[string]interface{}{"signed": int64(-1), "data": []byte("nested")},
		"emptyList": []interface{}{},
		"emptyMap":  map[string]interface{}{},
	}

	data, err := MarshalProperties(input)
	if err != nil {
		t.Fatalf("MarshalProperties failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("encoded data is empty")
	}

	output, err := UnmarshalProperties(data)
	if err != nil {
		t.Fatalf("UnmarshalProperties failed: %v", err)
	}
	if !reflect.DeepEqual(output, want) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", output, want)
	}
}

func TestIntegerRoundTripHasNoPrecisionLoss(t *testing.T) {
	input := map[string]interface{}{
		"aboveFloatPrecision": int64(1<<53 + 1),
		"maxSigned":           int64(math.MaxInt64),
		"minSigned":           int64(math.MinInt64),
		"maxUnsigned":         uint64(math.MaxUint64),
	}

	data, err := MarshalProperties(input)
	if err != nil {
		t.Fatal(err)
	}
	output, err := UnmarshalProperties(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output, input) {
		t.Fatalf("integer round trip mismatch:\n got: %#v\nwant: %#v", output, input)
	}
}

func TestBytesRoundTripAsOwnedBytes(t *testing.T) {
	data, err := MarshalProperties(map[string]interface{}{"data": []byte("raw")})
	if err != nil {
		t.Fatal(err)
	}
	output, err := UnmarshalProperties(data)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := output["data"].([]byte)
	if !ok || !bytes.Equal(got, []byte("raw")) {
		t.Fatalf("decoded bytes = %v (%T)", output["data"], output["data"])
	}
	got[0] = 'X'
	again, err := UnmarshalProperties(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(again["data"].([]byte)) != "raw" {
		t.Fatal("decoded bytes alias encoded or previously decoded data")
	}
}

func TestNonFiniteFloatsRoundTrip(t *testing.T) {
	data, err := MarshalProperties(map[string]interface{}{
		"nan":    math.NaN(),
		"posInf": math.Inf(1),
		"negInf": math.Inf(-1),
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := UnmarshalProperties(data)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsNaN(output["nan"].(float64)) || !math.IsInf(output["posInf"].(float64), 1) || !math.IsInf(output["negInf"].(float64), -1) {
		t.Fatalf("non-finite values changed: %#v", output)
	}
}

func TestMarshalNil(t *testing.T) {
	data, err := MarshalProperties(nil)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Fatal("expected nil data for nil map")
	}
}

func TestUnmarshalEmpty(t *testing.T) {
	for _, data := range [][]byte{nil, {}} {
		result, err := UnmarshalProperties(data)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			t.Fatalf("expected nil map, got %#v", result)
		}
	}
}

func TestMarshalRejectsUnsupportedAndExcessivelyNestedValues(t *testing.T) {
	_, err := MarshalProperties(map[string]interface{}{"bad": func() {}})
	if !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("unsupported value error = %v", err)
	}

	var nested interface{} = nil
	for range maxNestingDepth + 2 {
		nested = []interface{}{nested}
	}
	_, err = MarshalProperties(map[string]interface{}{"nested": nested})
	if !errors.Is(err, ErrUnsupportedValue) {
		t.Fatalf("nested value error = %v", err)
	}
}

func TestUnmarshalRejectsInvalidDataAndMissingKinds(t *testing.T) {
	if _, err := UnmarshalProperties([]byte{0xff, 0xff}); err == nil {
		t.Fatal("expected malformed protobuf error")
	}

	data, err := proto.Marshal(&propertypb.Properties{Data: map[string]*propertypb.PropertyValue{
		"missing": {},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = UnmarshalProperties(data)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("missing kind error = %v", err)
	}
}

var (
	benchmarkEncodedProperties []byte
	benchmarkDecodedProperties map[string]interface{}
)

func benchmarkPropertyMap() map[string]interface{} {
	return map[string]interface{}{
		"name":     "Alice",
		"active":   true,
		"age":      int64(42),
		"sequence": uint64(1<<63 + 9),
		"score":    98.5,
		"avatar":   []byte{0, 1, 2, 3, 4, 5, 6, 7},
		"roles":    []interface{}{"admin", "author", int64(3)},
		"metadata": map[string]interface{}{"region": "eu", "attempts": uint64(7)},
	}
}

func BenchmarkMarshalProperties(b *testing.B) {
	props := benchmarkPropertyMap()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var err error
		benchmarkEncodedProperties, err = MarshalProperties(props)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalProperties(b *testing.B) {
	data, err := MarshalProperties(benchmarkPropertyMap())
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkDecodedProperties, err = UnmarshalProperties(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}
