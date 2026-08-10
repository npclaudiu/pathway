package pathway

import (
	"errors"
	"fmt"
)

// Core library errors
var (
	ErrInvalidDatabase      = errors.New("invalid database instance")
	ErrInvalidSnapshot      = errors.New("invalid snapshot instance")
	ErrKeyNotFound          = errors.New("key not found")
	ErrNodeNotFound         = errors.New("node not found")
	ErrEdgeNotFound         = errors.New("edge not found")
	ErrEntityNotFound       = errors.New("node or edge not found")
	ErrInvalidNodeID        = errors.New("invalid textual node ID")
	ErrTraversalResult      = errors.New("unexpected traversal result type")
	ErrInvalidRepeat        = errors.New("invalid repeat configuration")
	ErrInvalidIteratorState = errors.New("iterator is not positioned on a result")
	ErrInvalidExternalID    = errors.New("invalid external ID")
	ErrExternalIDConflict   = errors.New("external ID is already bound to another node")
	ErrExternalIDCorrupt    = errors.New("corrupt external ID records")
	ErrDanglingEdge         = errors.New("cannot create edge: source or target node does not exist")
	ErrUnsupportedSchema    = errors.New("unsupported pathway storage schema")
	ErrInvalidDurability    = errors.New("invalid durability mode")
	ErrCorruptData          = errors.New("corrupt stored data")
)

func corruptRecord(recordType string, cause error) error {
	return fmt.Errorf("%w: malformed %s record: %w", ErrCorruptData, recordType, cause)
}
