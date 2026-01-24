package pathway

import "strings"

// Predicate is a function that tests a value.
type Predicate func(val interface{}) bool

// Eq returns a predicate that checks for strict equality.
func Eq(expected interface{}) Predicate {
	return func(val interface{}) bool {
		return val == expected
	}
}

// Gt returns a predicate that checks if value > expected.
// Only supports int/float64 for now directly.
func Gt(expected interface{}) Predicate {
	return func(val interface{}) bool {
		switch v := val.(type) {
		case int:
			if e, ok := expected.(int); ok {
				return v > e
			}
		case float64:
			if e, ok := expected.(float64); ok {
				return v > e
			}
		}
		return false // Type mismatch or unsupported
	}
}

// Lt returns a predicate that checks if value < expected.
func Lt(expected interface{}) Predicate {
	return func(val interface{}) bool {
		switch v := val.(type) {
		case int:
			if e, ok := expected.(int); ok {
				return v < e
			}
		case float64:
			if e, ok := expected.(float64); ok {
				return v < e
			}
		}
		return false
	}
}

// Prefix returns a predicate that checks if a string starts with prefix.
func Prefix(prefix string) Predicate {
	return func(val interface{}) bool {
		if s, ok := val.(string); ok {
			return strings.HasPrefix(s, prefix)
		}
		return false
	}
}

// Contains returns a predicate that checks if a string contains substr.
func Contains(substr string) Predicate {
	return func(val interface{}) bool {
		if s, ok := val.(string); ok {
			return strings.Contains(s, substr)
		}
		return false
	}
}
