package pathway

import (
	"fmt"
	"unicode/utf8"

	"github.com/npclaudiu/pathway/internal/encoding"
)

// MaxExternalIDPartBytes is the maximum encoded byte length of an ExternalID
// namespace, scope, or value. Namespaces and values must also be non-empty.
const MaxExternalIDPartBytes = 65_535

// ExternalID is an immutable, namespaced identifier owned by an external data
// source. Namespace is exact, case-sensitive UTF-8; Pathway performs no Unicode
// normalization. Scope and Value are opaque bytes compared exactly. Scope may
// be empty, while Namespace and Value must contain 1..MaxExternalIDPartBytes
// bytes.
//
// Use distinct namespaces for identifiers with different canonical forms. For
// example, Git SHA-1 and SHA-256 object IDs should use different namespaces and
// store the raw digest in Value. Repository-local identities should put the
// canonical repository identity in Scope instead of concatenating it with the
// digest.
type ExternalID struct {
	namespace string
	scope     []byte
	value     []byte
}

// NewExternalID constructs a global external ID and takes an owned copy of
// value.
func NewExternalID(namespace string, value []byte) (ExternalID, error) {
	return NewScopedExternalID(namespace, nil, value)
}

// NewScopedExternalID constructs an external ID and takes owned copies of
// scope and value. Exact bytes are canonical: Pathway does not trim, fold,
// decode, hash, or otherwise normalize any component.
func NewScopedExternalID(namespace string, scope, value []byte) (ExternalID, error) {
	if err := validateExternalIDParts(namespace, scope, value); err != nil {
		return ExternalID{}, err
	}
	return ExternalID{
		namespace: namespace,
		scope:     append([]byte(nil), scope...),
		value:     append([]byte(nil), value...),
	}, nil
}

// Namespace returns the exact, case-sensitive namespace.
func (id ExternalID) Namespace() string { return id.namespace }

// Scope returns an owned copy of the optional opaque scope.
func (id ExternalID) Scope() []byte { return append([]byte(nil), id.scope...) }

// Value returns an owned copy of the opaque identifier value.
func (id ExternalID) Value() []byte { return append([]byte(nil), id.value...) }

func (id ExternalID) validate() error {
	return validateExternalIDParts(id.namespace, id.scope, id.value)
}

func validateExternalIDParts(namespace string, scope, value []byte) error {
	switch {
	case len(namespace) == 0:
		return fmt.Errorf("%w: namespace is empty", ErrInvalidExternalID)
	case len(namespace) > MaxExternalIDPartBytes:
		return fmt.Errorf("%w: namespace exceeds %d bytes", ErrInvalidExternalID, MaxExternalIDPartBytes)
	case !utf8.ValidString(namespace):
		return fmt.Errorf("%w: namespace is not valid UTF-8", ErrInvalidExternalID)
	case len(scope) > MaxExternalIDPartBytes:
		return fmt.Errorf("%w: scope exceeds %d bytes", ErrInvalidExternalID, MaxExternalIDPartBytes)
	case len(value) == 0:
		return fmt.Errorf("%w: value is empty", ErrInvalidExternalID)
	case len(value) > MaxExternalIDPartBytes:
		return fmt.Errorf("%w: value exceeds %d bytes", ErrInvalidExternalID, MaxExternalIDPartBytes)
	default:
		return nil
	}
}

func (id ExternalID) clone() ExternalID {
	return ExternalID{
		namespace: id.namespace,
		scope:     append([]byte(nil), id.scope...),
		value:     append([]byte(nil), id.value...),
	}
}

func (id ExternalID) forwardKey() ([]byte, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	key, err := encoding.EncodeExternalIDKey(id.namespace, id.scope, id.value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidExternalID, err)
	}
	return key, nil
}

func (id ExternalID) reverseKey(nodeID [16]byte) ([]byte, error) {
	if err := id.validate(); err != nil {
		return nil, err
	}
	key, err := encoding.EncodeExternalIDReverseKey(nodeID, id.namespace, id.scope, id.value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidExternalID, err)
	}
	return key, nil
}
