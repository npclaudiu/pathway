package pathway

import (
	"errors"
	"math"
	"reflect"

	"github.com/google/uuid"
)

// Load populates the destination struct with properties from the node/edge with the given ID.
// dest must be a pointer to a struct.
func (tx *Tx) Load(id uuid.UUID, dest interface{}) error {
	// 1. Get properties
	props, err := tx.GetProperties(id)
	if err != nil {
		return err
	}
	if props == nil {
		return ErrNodeNotFound
	}

	// 2. Reflect on dest
	v := reflect.ValueOf(dest)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return errors.New("dest must be a non-nil pointer")
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return errors.New("dest must be a pointer to a struct")
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("graph")
		if tag == "-" || tag == "" {
			continue
		}

		// Check if property exists
		if val, ok := props[tag]; ok {
			// Set field
			// Use simple type assertion/conversion
			// Ideally we need a robust coercion library (like mapstructure)
			// primarily for float64 -> int issues with JSON/Protobuf.
			// For now, we do basic set.
			fVal := v.Field(i)
			if !fVal.CanSet() {
				continue
			}

			valRef := reflect.ValueOf(val)
			if valRef.Type().AssignableTo(field.Type) {
				fVal.Set(valRef)
			} else if converted, ok := convertNumericValue(valRef, field.Type); ok {
				fVal.Set(converted)
			}
		}
	}

	return nil
}

func convertNumericValue(value reflect.Value, target reflect.Type) (reflect.Value, bool) {
	result := reflect.New(target).Elem()
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := value.Int()
		switch target.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if result.OverflowInt(n) {
				return reflect.Value{}, false
			}
			result.SetInt(n)
			return result, true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if n < 0 || result.OverflowUint(uint64(n)) {
				return reflect.Value{}, false
			}
			result.SetUint(uint64(n))
			return result, true
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := value.Uint()
		switch target.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if result.OverflowUint(n) {
				return reflect.Value{}, false
			}
			result.SetUint(n)
			return result, true
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if n > math.MaxInt64 || result.OverflowInt(int64(n)) {
				return reflect.Value{}, false
			}
			result.SetInt(int64(n))
			return result, true
		}
	case reflect.Float32, reflect.Float64:
		n := value.Float()
		switch target.Kind() {
		case reflect.Float32, reflect.Float64:
			if result.OverflowFloat(n) {
				return reflect.Value{}, false
			}
			result.SetFloat(n)
			return result, true
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if math.IsNaN(n) || math.IsInf(n, 0) || n < math.MinInt64 || n > math.MaxInt64 || result.OverflowInt(int64(n)) {
				return reflect.Value{}, false
			}
			result.SetInt(int64(n))
			return result, true
		}
	}
	return reflect.Value{}, false
}
