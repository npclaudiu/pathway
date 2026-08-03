package pathway

import (
	"math"
	"testing"
)

func TestPredicates(t *testing.T) {
	// Eq
	if !Eq(5)(5) {
		t.Error("Eq(5)(5) should be true")
	}
	if Eq(5)(6) {
		t.Error("Eq(5)(6) should be false")
	}
	if Eq("a")("b") {
		t.Error("Eq(a)(b) should be false")
	}
	if !Eq(map[string]any{"items": []any{int64(1)}})(map[string]any{"items": []any{int64(1)}}) {
		t.Error("Eq should deeply compare dynamic maps and lists")
	}
	if Eq(int64(5))(5) {
		t.Error("Eq should remain type-sensitive")
	}

	// Gt
	if !Gt(5)(6) {
		t.Error("Gt(5)(6) should be true (6 > 5)")
	}
	if Gt(5)(5) {
		t.Error("Gt(5)(5) should be false")
	}
	if Gt(5)(4) {
		t.Error("Gt(5)(4) should be false")
	}

	if !Gt(3.14)(3.15) {
		t.Error("Gt float should work")
	}
	if Gt(3.14)(3.14) {
		t.Error("Gt float equal should be false")
	}
	if !Gt(5)(int64(6)) {
		t.Error("Gt should compare canonical int64 properties with integer literals")
	}
	if !Gt(int64(-1))(uint64(0)) {
		t.Error("Gt should compare signed and unsigned integers safely")
	}
	if Gt(0)(math.NaN()) {
		t.Error("Gt should reject NaN")
	}
	if Gt(math.NaN())(math.Inf(1)) {
		t.Error("Gt should reject a NaN bound")
	}
	if !Gt(0)(math.Inf(1)) || Gt(0)(math.Inf(-1)) {
		t.Error("Gt should compare infinities")
	}

	// Type Mismatches
	if Gt(5)("s") {
		t.Error("Gt mixed types should be false")
	}
	if Gt("s")(5) {
		t.Error("Gt string should be false")
	}

	// Lt
	if !Lt(5)(4) {
		t.Error("Lt(5)(4) should be true")
	}
	if Lt(5)(5) {
		t.Error("Lt(5)(5) should be false")
	}
	if Lt(5)(6) {
		t.Error("Lt(5)(6) should be false")
	}

	if !Lt(3.14)(1.0) {
		t.Error("Lt float should work")
	}
	if !Lt(uint64(6))(int64(5)) {
		t.Error("Lt should compare signed and unsigned integers safely")
	}
	if Lt(5)("s") {
		t.Error("Lt mixed types should be false")
	}

	// Prefix
	if !Prefix("foo")("foobar") {
		t.Error("Prefix match failed")
	}
	if Prefix("foo")("bar") {
		t.Error("Prefix mismatch should be false")
	}
	if Prefix("foo")(123) {
		t.Error("Prefix non-string should be false")
	}

	// Contains
	if !Contains("oob")("foobar") {
		t.Error("Contains match failed")
	}
	if Contains("baz")("foobar") {
		t.Error("Contains mismatch should be false")
	}
	if Contains("f")(123) {
		t.Error("Contains non-string should be false")
	}
}
