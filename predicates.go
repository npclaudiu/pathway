package pathway

import (
	"math"
	"math/big"
	"reflect"
	"strings"
)

// NodePredicate tests a materialized node. Repeat passes the current node to
// Until predicates; the value owns its fields and is safe to retain.
type NodePredicate func(Node) bool

// Predicate tests a dynamic property value. Values can be nil, bool, int64,
// uint64, float64, string, []byte, []any, or map[string]any, matching the
// documented Values and GetProperties result contract.
type Predicate func(any) bool

// Eq returns a predicate that checks for deep, type-sensitive equality. It is
// safe for byte slices, lists, and maps.
//
// Usage:
//
//	pathway.Eq("Person")
func Eq(expected any) Predicate {
	return func(value any) bool {
		return reflect.DeepEqual(value, expected)
	}
}

// Gt returns a predicate that checks whether a numeric value is greater than
// expected. All signed integer, unsigned integer, and floating-point Go types
// are compared by exact numeric value. Unsupported types and NaN do not match.
//
// Usage:
//
//	pathway.Gt(18)
func Gt(expected any) Predicate {
	return func(value any) bool {
		comparison, ok := compareNumbers(value, expected)
		return ok && comparison > 0
	}
}

// Lt returns a predicate that checks whether a numeric value is less than
// expected. It has the same numeric conversion contract as Gt.
//
// Usage:
//
//	pathway.Lt(100.0)
func Lt(expected any) Predicate {
	return func(value any) bool {
		comparison, ok := compareNumbers(value, expected)
		return ok && comparison < 0
	}
}

func compareNumbers(left, right any) (int, bool) {
	if numericNaN(left) || numericNaN(right) {
		return 0, false
	}
	leftInfinity, leftIsInfinite := infinitySign(left)
	rightInfinity, rightIsInfinite := infinitySign(right)
	if leftIsInfinite || rightIsInfinite {
		switch {
		case leftIsInfinite && rightIsInfinite:
			return leftInfinity - rightInfinity, true
		case leftIsInfinite:
			return leftInfinity, true
		default:
			return -rightInfinity, true
		}
	}

	leftNumber, ok := numberAsRat(left)
	if !ok {
		return 0, false
	}
	rightNumber, ok := numberAsRat(right)
	if !ok {
		return 0, false
	}
	return leftNumber.Cmp(rightNumber), true
}

func numericNaN(value any) bool {
	switch value := value.(type) {
	case float32:
		return math.IsNaN(float64(value))
	case float64:
		return math.IsNaN(value)
	default:
		return false
	}
}

func infinitySign(value any) (int, bool) {
	var number float64
	switch value := value.(type) {
	case float32:
		number = float64(value)
	case float64:
		number = value
	default:
		return 0, false
	}
	if math.IsInf(number, 1) {
		return 1, true
	}
	if math.IsInf(number, -1) {
		return -1, true
	}
	return 0, false
}

func numberAsRat(value any) (*big.Rat, bool) {
	result := new(big.Rat)
	switch value := value.(type) {
	case int:
		return result.SetInt64(int64(value)), true
	case int8:
		return result.SetInt64(int64(value)), true
	case int16:
		return result.SetInt64(int64(value)), true
	case int32:
		return result.SetInt64(int64(value)), true
	case int64:
		return result.SetInt64(value), true
	case uint:
		return result.SetInt(new(big.Int).SetUint64(uint64(value))), true
	case uint8:
		return result.SetInt(new(big.Int).SetUint64(uint64(value))), true
	case uint16:
		return result.SetInt(new(big.Int).SetUint64(uint64(value))), true
	case uint32:
		return result.SetInt(new(big.Int).SetUint64(uint64(value))), true
	case uint64:
		return result.SetInt(new(big.Int).SetUint64(value)), true
	case float32:
		converted := result.SetFloat64(float64(value))
		return converted, converted != nil
	case float64:
		converted := result.SetFloat64(value)
		return converted, converted != nil
	default:
		return nil, false
	}
}

// Prefix returns a predicate that checks if a string starts with prefix.
//
// Usage:
//
//	pathway.Prefix("User-")
func Prefix(prefix string) Predicate {
	return func(val any) bool {
		if s, ok := val.(string); ok {
			return strings.HasPrefix(s, prefix)
		}
		return false
	}
}

// Contains returns a predicate that checks if a string contains substr.
func Contains(substr string) Predicate {
	return func(val any) bool {
		if s, ok := val.(string); ok {
			return strings.Contains(s, substr)
		}
		return false
	}
}
