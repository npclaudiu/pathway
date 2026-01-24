package pathway

import "errors"

// Core library errors
var (
	ErrInvalidDatabase = errors.New("invalid database instance")
	ErrInvalidSnapshot = errors.New("invalid snapshot instance")
	ErrKeyNotFound     = errors.New("key not found")
	ErrNodeNotFound    = errors.New("node not found")
	ErrEdgeNotFound    = errors.New("edge not found")
	ErrDanglingEdge    = errors.New("cannot create edge: source or target node does not exist")
)
