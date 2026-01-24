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
