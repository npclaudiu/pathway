package encoding

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrInvalidLabel       = errors.New("invalid label: exceeds max length or contains illegal characters")
	ErrInvalidIndexKey    = errors.New("invalid index key: label or property key exceeds maximum length")
	ErrInvalidIndexValue  = errors.New("invalid index value")
	ErrInvalidExternalID  = errors.New("invalid external ID")
	ErrInvalidKeyFormat   = errors.New("invalid key format encountered during iteration")
	ErrInvalidValueFormat = errors.New("invalid value format encountered during iteration")
)

// Key Prefixes
const (
	PrefixNode           byte = 0x01
	PrefixEdgeOut        byte = 0x02
	PrefixEdgeIn         byte = 0x03
	PrefixProperties     byte = 0x04
	PrefixIndex          byte = 0x05
	PrefixEdgeByID       byte = 0x06
	PrefixIndexDef       byte = 0x07
	PrefixExternalID     byte = 0x08
	PrefixExternalByNode byte = 0x09
)

const MaxExternalIDPartBytes = math.MaxUint16

// EncodeNodeKey returns the key for a node: [PrefixNode] + [NodeID]
func EncodeNodeKey(id uuid.UUID) []byte {
	k := make([]byte, 1+16)
	k[0] = PrefixNode
	copy(k[1:], id[:])
	return k
}

// EncodeExternalIDKey returns the forward lookup key for a namespaced external
// ID. Every component is length-delimited, so scopes and values cannot collide
// through concatenation.
func EncodeExternalIDKey(namespace string, scope, value []byte) ([]byte, error) {
	suffix, err := encodeExternalIDSuffix(namespace, scope, value)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 1+len(suffix))
	key[0] = PrefixExternalID
	copy(key[1:], suffix)
	return key, nil
}

// EncodeExternalIDReverseKey returns a node-owned cleanup record key.
func EncodeExternalIDReverseKey(nodeID uuid.UUID, namespace string, scope, value []byte) ([]byte, error) {
	suffix, err := encodeExternalIDSuffix(namespace, scope, value)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 1+len(nodeID)+len(suffix))
	key[0] = PrefixExternalByNode
	copy(key[1:], nodeID[:])
	copy(key[17:], suffix)
	return key, nil
}

// EncodeExternalIDReversePrefix returns the range prefix for all external IDs
// bound to nodeID.
func EncodeExternalIDReversePrefix(nodeID uuid.UUID) []byte {
	key := make([]byte, 1+len(nodeID))
	key[0] = PrefixExternalByNode
	copy(key[1:], nodeID[:])
	return key
}

// DecodeExternalIDKey decodes a forward external-ID key.
func DecodeExternalIDKey(key []byte) (string, []byte, []byte, error) {
	if len(key) < 1 || key[0] != PrefixExternalID {
		return "", nil, nil, ErrInvalidKeyFormat
	}
	return decodeExternalIDSuffix(key[1:])
}

// DecodeExternalIDReverseKey decodes a node-owned cleanup record key.
func DecodeExternalIDReverseKey(key []byte) (uuid.UUID, string, []byte, []byte, error) {
	if len(key) < 17 || key[0] != PrefixExternalByNode {
		return uuid.Nil, "", nil, nil, ErrInvalidKeyFormat
	}
	var nodeID uuid.UUID
	copy(nodeID[:], key[1:17])
	namespace, scope, value, err := decodeExternalIDSuffix(key[17:])
	return nodeID, namespace, scope, value, err
}

// EncodeExternalIDValue stores the internal UUID in a forward lookup record.
func EncodeExternalIDValue(nodeID uuid.UUID) []byte {
	return append([]byte(nil), nodeID[:]...)
}

// DecodeExternalIDValue decodes and validates a forward lookup value.
func DecodeExternalIDValue(value []byte) (uuid.UUID, error) {
	if len(value) != len(uuid.UUID{}) {
		return uuid.Nil, ErrInvalidValueFormat
	}
	var nodeID uuid.UUID
	copy(nodeID[:], value)
	return nodeID, nil
}

func encodeExternalIDSuffix(namespace string, scope, value []byte) ([]byte, error) {
	if len(namespace) == 0 || len(namespace) > MaxExternalIDPartBytes || !utf8.ValidString(namespace) ||
		len(scope) > MaxExternalIDPartBytes || len(value) == 0 || len(value) > MaxExternalIDPartBytes {
		return nil, ErrInvalidExternalID
	}

	suffix := make([]byte, 2+len(namespace)+2+len(scope)+2+len(value))
	offset := 0
	binary.BigEndian.PutUint16(suffix[offset:], uint16(len(namespace)))
	offset += 2
	copy(suffix[offset:], namespace)
	offset += len(namespace)
	binary.BigEndian.PutUint16(suffix[offset:], uint16(len(scope)))
	offset += 2
	copy(suffix[offset:], scope)
	offset += len(scope)
	binary.BigEndian.PutUint16(suffix[offset:], uint16(len(value)))
	offset += 2
	copy(suffix[offset:], value)
	return suffix, nil
}

func decodeExternalIDSuffix(suffix []byte) (string, []byte, []byte, error) {
	readPart := func() ([]byte, bool) {
		if len(suffix) < 2 {
			return nil, false
		}
		length := int(binary.BigEndian.Uint16(suffix))
		suffix = suffix[2:]
		if len(suffix) < length {
			return nil, false
		}
		part := append([]byte(nil), suffix[:length]...)
		suffix = suffix[length:]
		return part, true
	}

	namespace, ok := readPart()
	if !ok || len(namespace) == 0 || !utf8.Valid(namespace) {
		return "", nil, nil, ErrInvalidKeyFormat
	}
	scope, ok := readPart()
	if !ok {
		return "", nil, nil, ErrInvalidKeyFormat
	}
	value, ok := readPart()
	if !ok || len(value) == 0 || len(suffix) != 0 {
		return "", nil, nil, ErrInvalidKeyFormat
	}
	return string(namespace), scope, value, nil
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

// EncodeEdgeOutPrefix returns the exact key prefix shared by outgoing edges
// from srcID with label.
func EncodeEdgeOutPrefix(srcID uuid.UUID, label string) ([]byte, error) {
	return encodeEdgeLabelPrefix(PrefixEdgeOut, srcID, label)
}

// EncodeEdgeInPrefix returns the exact key prefix shared by incoming edges to
// dstID with label.
func EncodeEdgeInPrefix(dstID uuid.UUID, label string) ([]byte, error) {
	return encodeEdgeLabelPrefix(PrefixEdgeIn, dstID, label)
}

func encodeEdgeLabelPrefix(prefix byte, anchorID uuid.UUID, label string) ([]byte, error) {
	labelBytes := []byte(label)
	if len(labelBytes) > math.MaxUint16 {
		return nil, ErrInvalidLabel
	}

	key := make([]byte, 1+16+2+len(labelBytes))
	key[0] = prefix
	copy(key[1:], anchorID[:])
	binary.BigEndian.PutUint16(key[17:], uint16(len(labelBytes)))
	copy(key[19:], labelBytes)
	return key, nil
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
	indexValueSignedInteger
	indexValueUnsignedInteger
	indexValueFloat
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
	propertyPrefix, err := EncodeIndexPropertyPrefix(label, propKey)
	if err != nil {
		return nil, err
	}

	valueType, valBytes, err := encodeIndexValue(propValue)
	if err != nil {
		return nil, err
	}
	if uint64(len(valBytes)) > math.MaxUint32 {
		return nil, ErrInvalidIndexValue
	}

	k := make([]byte, len(propertyPrefix)+1+4+len(valBytes))
	copy(k, propertyPrefix)
	offset := len(propertyPrefix)
	k[offset] = valueType
	offset++
	binary.BigEndian.PutUint32(k[offset:], uint32(len(valBytes)))
	offset += 4
	copy(k[offset:], valBytes)

	return k, nil
}

// EncodeIndexPropertyPrefix creates the prefix shared by every indexed value
// for one label/property pair.
func EncodeIndexPropertyPrefix(label, propKey string) ([]byte, error) {
	lblBytes := []byte(label)
	keyBytes := []byte(propKey)
	if len(lblBytes) > math.MaxUint16 || len(keyBytes) > math.MaxUint16 {
		return nil, ErrInvalidIndexKey
	}

	k := make([]byte, 1+2+len(lblBytes)+2+len(keyBytes))
	k[0] = PrefixIndex

	offset := 1
	binary.BigEndian.PutUint16(k[offset:], uint16(len(lblBytes)))
	offset += 2
	copy(k[offset:], lblBytes)
	offset += len(lblBytes)

	binary.BigEndian.PutUint16(k[offset:], uint16(len(keyBytes)))
	offset += 2
	copy(k[offset:], keyBytes)

	return k, nil
}

// EncodeIndexDefinitionKey returns the persisted definition key for a
// label/property pair.
func EncodeIndexDefinitionKey(label, propKey string) ([]byte, error) {
	lblBytes := []byte(label)
	keyBytes := []byte(propKey)
	if len(lblBytes) > math.MaxUint16 || len(keyBytes) > math.MaxUint16 {
		return nil, ErrInvalidIndexKey
	}

	k := make([]byte, 1+2+len(lblBytes)+2+len(keyBytes))
	k[0] = PrefixIndexDef
	offset := 1
	binary.BigEndian.PutUint16(k[offset:], uint16(len(lblBytes)))
	offset += 2
	copy(k[offset:], lblBytes)
	offset += len(lblBytes)
	binary.BigEndian.PutUint16(k[offset:], uint16(len(keyBytes)))
	offset += 2
	copy(k[offset:], keyBytes)
	return k, nil
}

// DecodeIndexDefinitionKey decodes a persisted label/property definition.
func DecodeIndexDefinitionKey(key []byte) (string, string, error) {
	if len(key) < 5 || key[0] != PrefixIndexDef {
		return "", "", ErrInvalidKeyFormat
	}
	label, consumed := DecodeLabel(key[1:])
	if consumed == 0 {
		return "", "", ErrInvalidKeyFormat
	}
	offset := 1 + consumed
	propKey, consumed := DecodeLabel(key[offset:])
	if consumed == 0 || offset+consumed != len(key) {
		return "", "", ErrInvalidKeyFormat
	}
	return label, propKey, nil
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
		return encodeIndexSignedInteger(int64(v))
	case int8:
		return encodeIndexSignedInteger(int64(v))
	case int16:
		return encodeIndexSignedInteger(int64(v))
	case int32:
		return encodeIndexSignedInteger(int64(v))
	case int64:
		return encodeIndexSignedInteger(v)
	case uint:
		return encodeIndexUnsignedInteger(uint64(v))
	case uint8:
		return encodeIndexUnsignedInteger(uint64(v))
	case uint16:
		return encodeIndexUnsignedInteger(uint64(v))
	case uint32:
		return encodeIndexUnsignedInteger(uint64(v))
	case uint64:
		return encodeIndexUnsignedInteger(v)
	case float32:
		return encodeIndexFloat(float64(v))
	case float64:
		return encodeIndexFloat(v)
	case []interface{}:
		return encodeIndexJSON(indexValueList, v)
	case map[string]interface{}:
		return encodeIndexJSON(indexValueObject, v)
	default:
		return 0, nil, fmt.Errorf("%w: unsupported type %T", ErrInvalidIndexValue, value)
	}
}

func encodeIndexSignedInteger(value int64) (byte, []byte, error) {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(value))
	return indexValueSignedInteger, b, nil
}

func encodeIndexUnsignedInteger(value uint64) (byte, []byte, error) {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, value)
	return indexValueUnsignedInteger, b, nil
}

func encodeIndexFloat(value float64) (byte, []byte, error) {
	if value == 0 {
		value = 0 // Canonicalize negative zero.
	}
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, math.Float64bits(value))
	return indexValueFloat, b, nil
}

func encodeIndexJSON(valueType byte, value interface{}) (byte, []byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrInvalidIndexValue, err)
	}
	return valueType, b, nil
}
