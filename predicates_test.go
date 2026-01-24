package pathway

import "testing"

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
