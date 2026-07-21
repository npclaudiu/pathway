package encoding

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
)

var (
	ErrInvalidLabel       = errors.New("invalid label: exceeds max length or contains illegal characters")
	ErrInvalidIndexKey    = errors.New("invalid index key: label or property key exceeds maximum length")
	ErrInvalidIndexValue  = errors.New("invalid index value")
	ErrInvalidKeyFormat   = errors.New("invalid key format encountered during iteration")
	ErrInvalidValueFormat = errors.New("invalid value format encountered during iteration")
)

// Key Prefixes
const (
	PrefixNode       byte = 0x01
	PrefixEdgeOut    byte = 0x02
	PrefixEdgeIn     byte = 0x03
	PrefixProperties byte = 0x04
	PrefixIndex      byte = 0x05
	PrefixEdgeByID   byte = 0x06
)

// EncodeNodeKey returns the key for a node: [PrefixNode] + [NodeID]
func EncodeNodeKey(id uuid.UUID) []byte {
	k := make([]byte, 1+16)
	k[0] = PrefixNode
	copy(k[1:], id[:])
	return k
}

// EncodeEdgeOutKey returns the key for an outgoing edge:
// [PrefixEdgeOut] + [SourceID] + [LabelLen] + [Label] + [TargetID] + [EdgeID]
func EncodeEdgeOutKey(srcID, dstID, edgeID uuid.UUID, label string) ([]byte, error) {
	lblBytes := []byte(label)
	if len(lblBytes) > 65535 {
		return nil, ErrInvalidLabel
	}
	k := make([]byte, 1+16+2+len(lblBytes)+16+16)
	k[0] = PrefixEdgeOut
	copy(k[1:], srcID[:])
	binary.BigEndian.PutUint16(k[17:], uint16(len(lblBytes)))
	copy(k[19:], lblBytes)
	copy(k[19+len(lblBytes):], dstID[:])
	copy(k[19+len(lblBytes)+16:], edgeID[:])
	return k, nil
}

// EncodeEdgeInKey returns the key for an incoming edge:
// [PrefixEdgeIn] + [TargetID] + [LabelLen] + [Label] + [SourceID] + [EdgeID]
func EncodeEdgeInKey(srcID, dstID, edgeID uuid.UUID, label string) ([]byte, error) {
	lblBytes := []byte(label)
	if len(lblBytes) > 65535 {
		return nil, ErrInvalidLabel
	}
	k := make([]byte, 1+16+2+len(lblBytes)+16+16)
	k[0] = PrefixEdgeIn
	copy(k[1:], dstID[:])
	binary.BigEndian.PutUint16(k[17:], uint16(len(lblBytes)))
	copy(k[19:], lblBytes)
	copy(k[19+len(lblBytes):], srcID[:])
	copy(k[19+len(lblBytes)+16:], edgeID[:])
	return k, nil
}

// EncodeDiff returns the bytes for EdgeID
func EncodeEdgeValue(edgeID uuid.UUID) []byte {
	v := make([]byte, 16)
	copy(v, edgeID[:])
	return v
}

// EncodeEdgeIDKey returns the reverse-index key for an edge ID:
// [PrefixEdgeByID] + [EdgeID].
func EncodeEdgeIDKey(edgeID uuid.UUID) []byte {
	k := make([]byte, 1+16)
	k[0] = PrefixEdgeByID
	copy(k[1:], edgeID[:])
	return k
}

// EncodeEdgeRecord encodes the endpoints and label stored in the edge-ID
// reverse index: [SourceID] + [TargetID] + [LabelLen] + [Label].
func EncodeEdgeRecord(srcID, dstID uuid.UUID, label string) ([]byte, error) {
	lblBytes := []byte(label)
	if len(lblBytes) > 65535 {
		return nil, ErrInvalidLabel
	}

	v := make([]byte, 16+16+2+len(lblBytes))
	copy(v, srcID[:])
	copy(v[16:], dstID[:])
	binary.BigEndian.PutUint16(v[32:], uint16(len(lblBytes)))
	copy(v[34:], lblBytes)
	return v, nil
}

// DecodeEdgeRecord decodes an edge-ID reverse-index value.
func DecodeEdgeRecord(value []byte) (uuid.UUID, uuid.UUID, string, error) {
	if len(value) < 34 {
		return uuid.Nil, uuid.Nil, "", ErrInvalidValueFormat
	}

	labelLen := int(binary.BigEndian.Uint16(value[32:]))
	if len(value) != 34+labelLen {
		return uuid.Nil, uuid.Nil, "", ErrInvalidValueFormat
	}

	var srcID, dstID uuid.UUID
	copy(srcID[:], value[:16])
	copy(dstID[:], value[16:32])
	return srcID, dstID, string(value[34:]), nil
}

// EncodePropertyKey returns the key for properties: [PrefixProperties] + [EntityID]
func EncodePropertyKey(entityID uuid.UUID) []byte {
	k := make([]byte, 1+16)
	k[0] = PrefixProperties
	copy(k[1:], entityID[:])
	return k
}

// DecodeLabel extracts the label from a length-prefixed byte slice.
// It assumes the slice starts with the 2-byte length.
func DecodeLabel(b []byte) (string, int) {
	if len(b) < 2 {
		return "", 0
	}
	l := binary.BigEndian.Uint16(b)
	if len(b) < 2+int(l) {
		return "", 0
	}
	return string(b[2 : 2+l]), 2 + int(l)
}

// numericToBytes converts generic numeric types to big-endian bytes for index sorting.
func numericToBytes(val interface{}) []byte {
	switch v := val.(type) {
	case int:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(v)) // Note: strictly implies non-negative or needs offset binary for signed
		return b
	case float64:
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, math.Float64bits(v))
		return b
	default:
		return nil
	}
}

const (
	indexValueNil byte = iota
	indexValueBool
	indexValueNumber
	indexValueString
	indexValueBytes
	indexValueList
	indexValueObject
)

// EncodeIndexPrefix creates a prefix for an exact index scan. Property values
// are typed and length-delimited so neither textual type collisions nor value
// prefix matches are possible.
//
// Format: [PrefixIndex] + [LabelLen:2] + [Label] + [KeyLen:2] + [Key] +
// [ValueType:1] + [ValueLen:4] + [Value]
func EncodeIndexPrefix(label, propKey string, propValue interface{}) ([]byte, error) {
	lblBytes := []byte(label)
	keyBytes := []byte(propKey)
	if len(lblBytes) > math.MaxUint16 || len(keyBytes) > math.MaxUint16 {
		return nil, ErrInvalidIndexKey
	}

	valueType, valBytes, err := encodeIndexValue(propValue)
	if err != nil {
		return nil, err
	}
	if uint64(len(valBytes)) > math.MaxUint32 {
		return nil, ErrInvalidIndexValue
	}

	k := make([]byte, 1+2+len(lblBytes)+2+len(keyBytes)+1+4+len(valBytes))
	k[0] = PrefixIndex

	offset := 1
	binary.BigEndian.PutUint16(k[offset:], uint16(len(lblBytes)))
	offset += 2
	copy(k[offset:], lblBytes)
	offset += len(lblBytes)

	binary.BigEndian.PutUint16(k[offset:], uint16(len(keyBytes)))
	offset += 2
	copy(k[offset:], keyBytes)
	offset += len(keyBytes)

	k[offset] = valueType
	offset++
	binary.BigEndian.PutUint32(k[offset:], uint32(len(valBytes)))
	offset += 4
	copy(k[offset:], valBytes)

	return k, nil
}

// EncodeIndexKey creates a full index key for a specific node property.
// Format: [EncodeIndexPrefix] + [NodeID: 16 bytes]
func EncodeIndexKey(label, propKey string, propValue interface{}, id uuid.UUID) ([]byte, error) {
	prefix, err := EncodeIndexPrefix(label, propKey, propValue)
	if err != nil {
		return nil, err
	}
	k := make([]byte, len(prefix)+16)
	copy(k, prefix)
	copy(k[len(prefix):], id[:])
	return k, nil
}

func encodeIndexValue(value interface{}) (byte, []byte, error) {
	switch v := value.(type) {
	case nil:
		return indexValueNil, nil, nil
	case bool:
		if v {
			return indexValueBool, []byte{1}, nil
		}
		return indexValueBool, []byte{0}, nil
	case string:
		return indexValueString, []byte(v), nil
	case []byte:
		return indexValueBytes, append([]byte(nil), v...), nil
	case int:
		return encodeIndexNumber(float64(v))
	case int8:
		return encodeIndexNumber(float64(v))
	case int16:
		return encodeIndexNumber(float64(v))
	case int32:
		return encodeIndexNumber(float64(v))
	case int64:
		return encodeIndexNumber(float64(v))
	case uint:
		return encodeIndexNumber(float64(v))
	case uint8:
		return encodeIndexNumber(float64(v))
	case uint16:
		return encodeIndexNumber(float64(v))
	case uint32:
		return encodeIndexNumber(float64(v))
	case uint64:
		return encodeIndexNumber(float64(v))
	case float32:
		return encodeIndexNumber(float64(v))
	case float64:
		return encodeIndexNumber(v)
	case []interface{}:
		return encodeIndexJSON(indexValueList, v)
	case map[string]interface{}:
		return encodeIndexJSON(indexValueObject, v)
	default:
		return 0, nil, fmt.Errorf("%w: unsupported type %T", ErrInvalidIndexValue, value)
	}
}

func encodeIndexNumber(value float64) (byte, []byte, error) {
	if value == 0 {
		value = 0 // Canonicalize negative zero.
	}
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, math.Float64bits(value))
	return indexValueNumber, b, nil
}

func encodeIndexJSON(valueType byte, value interface{}) (byte, []byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrInvalidIndexValue, err)
	}
	return valueType, b, nil
}
