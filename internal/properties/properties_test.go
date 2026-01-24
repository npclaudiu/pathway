package properties

import (
	"testing"
)

func TestMarshalUnmarshal(t *testing.T) {
	input := map[string]interface{}{
		"string": "value",
		"int":    42, // structpb converts numbers to float64
		"bool":   true,
		"float":  3.14,
		"nil":    nil,
	}

	// Marshal
	data, err := MarshalProperties(input)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("encoded data empty")
	}

	// Unmarshal
	output, err := UnmarshalProperties(data)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Check fields
	if output["string"] != "value" {
		t.Error("string mismatch")
	}
	if output["bool"] != true {
		t.Error("bool mismatch")
	}
	if output["nil"] != nil {
		t.Error("nil mismatch")
	}
	// Note: structpb converts int -> float64
	if v, ok := output["int"].(float64); !ok || v != 42 {
		t.Errorf("int mismatch, got %v (%T)", output["int"], output["int"])
	}
}

func TestMarshalNil(t *testing.T) {
	data, err := MarshalProperties(nil)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Error("expected nil data for nil map")
	}
}

func TestUnmarshalEmpty(t *testing.T) {
	res, err := UnmarshalProperties(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Error("expected nil map")
	}

	res, err = UnmarshalProperties([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Error("expected nil map")
	}
}

func TestMarshalInvalid(t *testing.T) {
	input := map[string]interface{}{
		"bad": func() {}, // Functions are not json/structpb serializable
	}
	_, err := MarshalProperties(input)
	if err == nil {
		t.Error("expected error for unspported type")
	}
}

func TestUnmarshalInvalidData(t *testing.T) {
	data := []byte{0xFF, 0xFF} // Garbage
	_, err := UnmarshalProperties(data)
	if err == nil {
		t.Error("expected error for garbage data")
	}
}
