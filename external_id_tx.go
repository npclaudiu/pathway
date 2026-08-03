package pathway

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
)

// ResolveExternalID returns the internal UUID bound to externalID. The boolean
// is false when no binding exists. Dangling or inconsistent forward/reverse
// records return ErrExternalIDCorrupt.
func (tx *Tx) ResolveExternalID(externalID ExternalID) (uuid.UUID, bool, error) {
	return tx.resolveExternalID(externalID, true)
}

// BindExternalID atomically binds externalID to an existing node. Repeating
// the same binding is idempotent. Binding an existing external ID to a
// different node returns ErrExternalIDConflict.
func (tx *Tx) BindExternalID(externalID ExternalID, nodeID uuid.UUID) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	tx.lockExternalIDs()

	exists, err := tx.nodeExists(nodeID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNodeNotFound
	}

	boundID, bound, err := tx.resolveExternalID(externalID, true)
	if err != nil {
		return err
	}
	if bound {
		if boundID == nodeID {
			return nil
		}
		return fmt.Errorf("%w: existing node %s", ErrExternalIDConflict, boundID)
	}
	return tx.setExternalIDBinding(externalID, nodeID)
}

// UnbindExternalID atomically removes an external-ID binding. Removing a
// binding that does not exist succeeds.
func (tx *Tx) UnbindExternalID(externalID ExternalID) error {
	if tx.readOnly {
		return pebble.ErrReadOnly
	}
	tx.lockExternalIDs()

	nodeID, bound, err := tx.resolveExternalID(externalID, true)
	if err != nil || !bound {
		return err
	}
	forwardKey, err := externalID.forwardKey()
	if err != nil {
		return err
	}
	reverseKey, err := externalID.reverseKey(nodeID)
	if err != nil {
		return err
	}
	if err := tx.batch.Delete(forwardKey, nil); err != nil {
		return err
	}
	return tx.batch.Delete(reverseKey, nil)
}

// PutNodeByExternalID atomically resolves or creates the node identified by
// externalID, updates its label, and returns its stable internal UUID. Repeated
// imports of the same external ID are idempotent.
func (tx *Tx) PutNodeByExternalID(externalID ExternalID, label string) (uuid.UUID, error) {
	if tx.readOnly {
		return uuid.Nil, pebble.ErrReadOnly
	}
	tx.lockExternalIDs()

	nodeID, bound, err := tx.resolveExternalID(externalID, true)
	if err != nil {
		return uuid.Nil, err
	}
	if bound {
		return nodeID, tx.PutNode(nodeID, label)
	}

	nodeID = uuid.New()
	if err := tx.PutNode(nodeID, label); err != nil {
		return uuid.Nil, err
	}
	if err := tx.setExternalIDBinding(externalID, nodeID); err != nil {
		return uuid.Nil, err
	}
	return nodeID, nil
}

func (tx *Tx) resolveExternalID(
	externalID ExternalID,
	verifyNode bool,
) (uuid.UUID, bool, error) {
	forwardKey, err := externalID.forwardKey()
	if err != nil {
		return uuid.Nil, false, err
	}
	value, err := tx.Get(forwardKey)
	if errors.Is(err, pebble.ErrNotFound) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	nodeID, err := encoding.DecodeExternalIDValue(value)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("%w: malformed forward value", ErrExternalIDCorrupt)
	}

	reverseKey, err := externalID.reverseKey(nodeID)
	if err != nil {
		return uuid.Nil, false, err
	}
	reverseValue, err := tx.Get(reverseKey)
	if errors.Is(err, pebble.ErrNotFound) {
		return uuid.Nil, false, fmt.Errorf("%w: missing reverse record", ErrExternalIDCorrupt)
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	if len(reverseValue) != 0 {
		return uuid.Nil, false, fmt.Errorf("%w: non-empty reverse record", ErrExternalIDCorrupt)
	}
	if verifyNode {
		exists, err := tx.nodeExists(nodeID)
		if err != nil {
			return uuid.Nil, false, err
		}
		if !exists {
			return uuid.Nil, false, fmt.Errorf("%w: binding targets a missing node", ErrExternalIDCorrupt)
		}
	}
	return nodeID, true, nil
}

func (tx *Tx) setExternalIDBinding(externalID ExternalID, nodeID uuid.UUID) error {
	forwardKey, err := externalID.forwardKey()
	if err != nil {
		return err
	}
	reverseKey, err := externalID.reverseKey(nodeID)
	if err != nil {
		return err
	}
	if err := tx.batch.Set(forwardKey, encoding.EncodeExternalIDValue(nodeID), nil); err != nil {
		return err
	}
	return tx.batch.Set(reverseKey, nil, nil)
}

func (tx *Tx) deleteExternalIDs(nodeID uuid.UUID) (resultErr error) {
	prefix := encoding.EncodeExternalIDReversePrefix(nodeID)
	iter, err := tx.newIterator(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: keyUpperBound(prefix),
	})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, iter.Close()) }()

	type bindingKeys struct {
		forward []byte
		reverse []byte
	}
	var bindings []bindingKeys
	for valid := iter.SeekGE(prefix); valid; valid = iter.Next() {
		key := append([]byte(nil), iter.Key()...)
		recordedNodeID, namespace, scope, value, err := encoding.DecodeExternalIDReverseKey(key)
		if err != nil || recordedNodeID != nodeID || len(iter.Value()) != 0 {
			return fmt.Errorf("%w: malformed reverse record", ErrExternalIDCorrupt)
		}
		externalID, err := NewScopedExternalID(namespace, scope, value)
		if err != nil {
			return fmt.Errorf("%w: invalid reverse identity", ErrExternalIDCorrupt)
		}
		forwardKey, err := externalID.forwardKey()
		if err != nil {
			return err
		}
		forwardValue, err := tx.Get(forwardKey)
		if errors.Is(err, pebble.ErrNotFound) {
			return fmt.Errorf("%w: reverse record has no forward record", ErrExternalIDCorrupt)
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(forwardValue, encoding.EncodeExternalIDValue(nodeID)) {
			return fmt.Errorf("%w: reverse record points to another node", ErrExternalIDCorrupt)
		}
		bindings = append(bindings, bindingKeys{forward: forwardKey, reverse: key})
	}
	if err := iter.Error(); err != nil {
		return err
	}

	for _, binding := range bindings {
		if err := tx.batch.Delete(binding.forward, nil); err != nil {
			return err
		}
		if err := tx.batch.Delete(binding.reverse, nil); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Tx) lockExternalIDs() {
	if tx.externalIDsLocked {
		return
	}
	tx.db.identityMu.Lock()
	tx.externalIDsLocked = true
}

func (tx *Tx) unlockExternalIDs() {
	if tx.externalIDsLocked {
		tx.externalIDsLocked = false
		tx.db.identityMu.Unlock()
	}
}
