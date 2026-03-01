package encoding

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/google/uuid"
)

var (
	ErrInvalidLabel       = errors.New("invalid label: exceeds max length or contains illegal characters")
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
)

// EncodeNodeKey returns the key for a node: [PrefixNode] + [NodeID]
func EncodeNodeKey(id uuid.UUID) []byte {
	k := make([]byte, 1+16)
	k[0] = PrefixNode
	copy(k[1:], id[:])
	return k
}

// EncodeEdgeOutKey returns the key for an outgoing edge:
// [PrefixEdgeOut] + [SourceID] + [LabelLen] + [Label] + [TargetID]
func EncodeEdgeOutKey(srcID, dstID uuid.UUID, label string) ([]byte, error) {
	lblBytes := []byte(label)
	if len(lblBytes) > 65535 {
		return nil, ErrInvalidLabel
	}
	k := make([]byte, 1+16+2+len(lblBytes)+16)
	k[0] = PrefixEdgeOut
	copy(k[1:], srcID[:])
	binary.BigEndian.PutUint16(k[17:], uint16(len(lblBytes)))
	copy(k[19:], lblBytes)
	copy(k[19+len(lblBytes):], dstID[:])
	return k, nil
}

// EncodeEdgeInKey returns the key for an incoming edge:
// [PrefixEdgeIn] + [TargetID] + [LabelLen] + [Label] + [SourceID]
func EncodeEdgeInKey(srcID, dstID uuid.UUID, label string) ([]byte, error) {
	lblBytes := []byte(label)
	if len(lblBytes) > 65535 {
		return nil, ErrInvalidLabel
	}
	k := make([]byte, 1+16+2+len(lblBytes)+16)
	k[0] = PrefixEdgeIn
	copy(k[1:], dstID[:])
	binary.BigEndian.PutUint16(k[17:], uint16(len(lblBytes)))
	copy(k[19:], lblBytes)
	copy(k[19+len(lblBytes):], srcID[:])
	return k, nil
}

// EncodeDiff returns the bytes for EdgeID
func EncodeEdgeValue(edgeID uuid.UUID) []byte {
	v := make([]byte, 16)
	copy(v, edgeID[:])
	return v
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

// EncodeIndexPrefix creates a prefix for scanning an index.
// Format: [PrefixIndex] + [Label length: 2 bytes] + [Label] + [Key length: 2 bytes] + [Key] + [Value]
func EncodeIndexPrefix(label, propKey, propValue string) []byte {
	lblBytes := []byte(label)
	keyBytes := []byte(propKey)
	valBytes := []byte(propValue)

	k := make([]byte, 1+2+len(lblBytes)+2+len(keyBytes)+len(valBytes))
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

	copy(k[offset:], valBytes)

	return k
}

// EncodeIndexKey creates a full index key for a specific node property.
// Format: [EncodeIndexPrefix] + [NodeID: 16 bytes]
func EncodeIndexKey(label, propKey, propValue string, id uuid.UUID) []byte {
	prefix := EncodeIndexPrefix(label, propKey, propValue)
	k := make([]byte, len(prefix)+16)
	copy(k, prefix)
	copy(k[len(prefix):], id[:])
	return k
}
