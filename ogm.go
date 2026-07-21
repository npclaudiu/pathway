package pathway

import (
	"errors"
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
			} else {
				// Try basic conversions
				// e.g. float64 to int
				if valRef.Kind() == reflect.Float64 && field.Type.Kind() == reflect.Int {
					fVal.SetInt(int64(val.(float64)))
				}
			}
		}
	}

	return nil
}
