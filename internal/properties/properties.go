package properties

import (
	"errors"
	"fmt"

	propertypb "github.com/npclaudiu/pathway/internal/proto"
	"google.golang.org/protobuf/proto"
)

const maxNestingDepth = 100

var (
	// ErrUnsupportedValue indicates that a caller supplied a value outside the
	// property type contract.
	ErrUnsupportedValue = errors.New("unsupported property value")

	// ErrInvalidValue indicates that encoded property data contains a missing
	// or malformed typed value.
	ErrInvalidValue = errors.New("invalid encoded property value")
)

// MarshalProperties converts a Go property map to its compact typed protobuf
// representation. Signed integers are canonicalized to int64, unsigned
// integers to uint64, and floating-point values to float64.
func MarshalProperties(props map[string]interface{}) ([]byte, error) {
	if props == nil {
		return nil, nil
	}

	data, err := marshalMap(props, 0)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(&propertypb.Properties{Data: data})
}

func marshalMap(props map[string]interface{}, depth int) (map[string]*propertypb.PropertyValue, error) {
	if depth > maxNestingDepth {
		return nil, fmt.Errorf("%w: nesting exceeds %d levels", ErrUnsupportedValue, maxNestingDepth)
	}

	result := make(map[string]*propertypb.PropertyValue, len(props))
	for key, value := range props {
		encoded, err := marshalValue(value, depth)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", key, err)
		}
		result[key] = encoded
	}
	return result, nil
}

func marshalValue(value interface{}, depth int) (*propertypb.PropertyValue, error) {
	if depth > maxNestingDepth {
		return nil, fmt.Errorf("%w: nesting exceeds %d levels", ErrUnsupportedValue, maxNestingDepth)
	}

	result := &propertypb.PropertyValue{}
	switch value := value.(type) {
	case nil:
		result.Kind = &propertypb.PropertyValue_NullValue{NullValue: propertypb.NullValue_NULL_VALUE}
	case bool:
		result.Kind = &propertypb.PropertyValue_BoolValue{BoolValue: value}
	case int:
		result.Kind = &propertypb.PropertyValue_IntValue{IntValue: int64(value)}
	case int8:
		result.Kind = &propertypb.PropertyValue_IntValue{IntValue: int64(value)}
	case int16:
		result.Kind = &propertypb.PropertyValue_IntValue{IntValue: int64(value)}
	case int32:
		result.Kind = &propertypb.PropertyValue_IntValue{IntValue: int64(value)}
	case int64:
		result.Kind = &propertypb.PropertyValue_IntValue{IntValue: value}
	case uint:
		result.Kind = &propertypb.PropertyValue_UintValue{UintValue: uint64(value)}
	case uint8:
		result.Kind = &propertypb.PropertyValue_UintValue{UintValue: uint64(value)}
	case uint16:
		result.Kind = &propertypb.PropertyValue_UintValue{UintValue: uint64(value)}
	case uint32:
		result.Kind = &propertypb.PropertyValue_UintValue{UintValue: uint64(value)}
	case uint64:
		result.Kind = &propertypb.PropertyValue_UintValue{UintValue: value}
	case float32:
		result.Kind = &propertypb.PropertyValue_DoubleValue{DoubleValue: float64(value)}
	case float64:
		result.Kind = &propertypb.PropertyValue_DoubleValue{DoubleValue: value}
	case string:
		result.Kind = &propertypb.PropertyValue_StringValue{StringValue: value}
	case []byte:
		result.Kind = &propertypb.PropertyValue_BytesValue{BytesValue: value}
	case []interface{}:
		values := make([]*propertypb.PropertyValue, len(value))
		for i, item := range value {
			encoded, err := marshalValue(item, depth+1)
			if err != nil {
				return nil, fmt.Errorf("list index %d: %w", i, err)
			}
			values[i] = encoded
		}
		result.Kind = &propertypb.PropertyValue_ListValue{
			ListValue: &propertypb.PropertyList{Values: values},
		}
	case map[string]interface{}:
		values, err := marshalMap(value, depth+1)
		if err != nil {
			return nil, err
		}
		result.Kind = &propertypb.PropertyValue_MapValue{
			MapValue: &propertypb.PropertyMap{Values: values},
		}
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedValue, value)
	}
	return result, nil
}

// UnmarshalProperties decodes typed protobuf property data. The returned map
// owns all strings, bytes, lists, and nested maps in it.
func UnmarshalProperties(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return nil, nil
	}

	message := &propertypb.Properties{}
	if err := proto.Unmarshal(data, message); err != nil {
		return nil, err
	}

	return unmarshalMap(message.Data, 0)
}

func unmarshalMap(data map[string]*propertypb.PropertyValue, depth int) (map[string]interface{}, error) {
	if depth > maxNestingDepth {
		return nil, fmt.Errorf("%w: nesting exceeds %d levels", ErrInvalidValue, maxNestingDepth)
	}

	result := make(map[string]interface{}, len(data))
	for key, value := range data {
		decoded, err := unmarshalValue(value, depth)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", key, err)
		}
		result[key] = decoded
	}
	return result, nil
}

func unmarshalValue(value *propertypb.PropertyValue, depth int) (interface{}, error) {
	if value == nil {
		return nil, fmt.Errorf("%w: missing value", ErrInvalidValue)
	}
	if depth > maxNestingDepth {
		return nil, fmt.Errorf("%w: nesting exceeds %d levels", ErrInvalidValue, maxNestingDepth)
	}

	switch kind := value.Kind.(type) {
	case *propertypb.PropertyValue_NullValue:
		if kind.NullValue != propertypb.NullValue_NULL_VALUE {
			return nil, fmt.Errorf("%w: unknown null value %d", ErrInvalidValue, kind.NullValue)
		}
		return nil, nil
	case *propertypb.PropertyValue_BoolValue:
		return kind.BoolValue, nil
	case *propertypb.PropertyValue_IntValue:
		return kind.IntValue, nil
	case *propertypb.PropertyValue_UintValue:
		return kind.UintValue, nil
	case *propertypb.PropertyValue_DoubleValue:
		return kind.DoubleValue, nil
	case *propertypb.PropertyValue_StringValue:
		return kind.StringValue, nil
	case *propertypb.PropertyValue_BytesValue:
		return append([]byte(nil), kind.BytesValue...), nil
	case *propertypb.PropertyValue_ListValue:
		if kind.ListValue == nil {
			return nil, fmt.Errorf("%w: missing list", ErrInvalidValue)
		}
		result := make([]interface{}, len(kind.ListValue.Values))
		for i, item := range kind.ListValue.Values {
			decoded, err := unmarshalValue(item, depth+1)
			if err != nil {
				return nil, fmt.Errorf("list index %d: %w", i, err)
			}
			result[i] = decoded
		}
		return result, nil
	case *propertypb.PropertyValue_MapValue:
		if kind.MapValue == nil {
			return nil, fmt.Errorf("%w: missing map", ErrInvalidValue)
		}
		return unmarshalMap(kind.MapValue.Values, depth+1)
	case nil:
		return nil, fmt.Errorf("%w: missing kind", ErrInvalidValue)
	default:
		return nil, fmt.Errorf("%w: unknown kind %T", ErrInvalidValue, kind)
	}
}
