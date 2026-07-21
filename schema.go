package pathway

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
	"github.com/npclaudiu/pathway/internal/properties"
)

const currentSchemaVersion uint32 = 2

var schemaVersionKey = []byte("\x00pathway/schema-version")

// ensureSchema initializes new databases and upgrades the original unversioned
// format. The migration and version marker are committed in one atomic batch:
// an interruption therefore exposes either the complete old schema or the
// complete new schema, making a retry safe.
func ensureSchema(db *pebble.DB) error {
	value, closer, err := db.Get(schemaVersionKey)
	if err == nil {
		versionValue := append([]byte(nil), value...)
		if closeErr := closer.Close(); closeErr != nil {
			return closeErr
		}
		if len(versionValue) != 4 {
			return fmt.Errorf("%w: malformed version marker", ErrUnsupportedSchema)
		}
		version := binary.BigEndian.Uint32(versionValue)
		if version != currentSchemaVersion {
			return fmt.Errorf("%w: found version %d, supported version is %d", ErrUnsupportedSchema, version, currentSchemaVersion)
		}
		return nil
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return err
	}

	empty, err := databaseIsEmpty(db)
	if err != nil {
		return err
	}
	if empty {
		return writeSchemaVersion(db)
	}
	return migrateSchemaV1ToV2(db)
}

func databaseIsEmpty(db *pebble.DB) (bool, error) {
	iter, err := db.NewIter(nil)
	if err != nil {
		return false, err
	}
	empty := !iter.First()
	return empty, errors.Join(iter.Error(), iter.Close())
}

func writeSchemaVersion(db *pebble.DB) error {
	value := make([]byte, 4)
	binary.BigEndian.PutUint32(value, currentSchemaVersion)
	return db.Set(schemaVersionKey, value, pebble.Sync)
}

func migrateSchemaV1ToV2(db *pebble.DB) error {
	batch := db.NewBatch()
	defer func() { _ = batch.Close() }()

	// Replace both legacy adjacency ranges. New keys include the edge ID, and a
	// reverse record makes deletion by ID a bounded operation.
	if err := batch.DeleteRange([]byte{encoding.PrefixEdgeOut}, []byte{encoding.PrefixEdgeIn}, nil); err != nil {
		return err
	}
	if err := batch.DeleteRange([]byte{encoding.PrefixEdgeIn}, []byte{encoding.PrefixProperties}, nil); err != nil {
		return err
	}

	edgeIter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{encoding.PrefixEdgeOut},
		UpperBound: []byte{encoding.PrefixEdgeIn},
	})
	if err != nil {
		return err
	}
	for edgeIter.First(); edgeIter.Valid(); edgeIter.Next() {
		srcID, dstID, label, err := decodeLegacyOutgoingEdgeKey(edgeIter.Key())
		if err != nil {
			_ = edgeIter.Close()
			return err
		}
		if len(edgeIter.Value()) != 16 {
			_ = edgeIter.Close()
			return encoding.ErrInvalidValueFormat
		}
		var edgeID uuid.UUID
		copy(edgeID[:], edgeIter.Value())

		outKey, err := encoding.EncodeEdgeOutKey(srcID, dstID, edgeID, label)
		if err != nil {
			_ = edgeIter.Close()
			return err
		}
		inKey, err := encoding.EncodeEdgeInKey(srcID, dstID, edgeID, label)
		if err != nil {
			_ = edgeIter.Close()
			return err
		}
		record, err := encoding.EncodeEdgeRecord(srcID, dstID, label)
		if err != nil {
			_ = edgeIter.Close()
			return err
		}
		for _, entry := range []struct {
			key   []byte
			value []byte
		}{
			{key: outKey, value: encoding.EncodeEdgeValue(edgeID)},
			{key: inKey, value: encoding.EncodeEdgeValue(edgeID)},
			{key: encoding.EncodeEdgeIDKey(edgeID), value: record},
		} {
			if err := batch.Set(entry.key, entry.value, nil); err != nil {
				_ = edgeIter.Close()
				return err
			}
		}
	}
	if err := errors.Join(edgeIter.Error(), edgeIter.Close()); err != nil {
		return err
	}

	// Old indexes are prefix-ambiguous, so rebuild them from canonical property
	// values instead of attempting an in-place key transformation.
	if err := batch.DeleteRange([]byte{encoding.PrefixIndex}, []byte{encoding.PrefixEdgeByID}, nil); err != nil {
		return err
	}
	nodeIter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{encoding.PrefixNode},
		UpperBound: []byte{encoding.PrefixEdgeOut},
	})
	if err != nil {
		return err
	}
	for nodeIter.First(); nodeIter.Valid(); nodeIter.Next() {
		if len(nodeIter.Key()) != 17 {
			_ = nodeIter.Close()
			return encoding.ErrInvalidKeyFormat
		}
		var nodeID uuid.UUID
		copy(nodeID[:], nodeIter.Key()[1:])
		label, consumed := encoding.DecodeLabel(nodeIter.Value())
		if consumed == 0 || consumed != len(nodeIter.Value()) {
			_ = nodeIter.Close()
			return encoding.ErrInvalidValueFormat
		}

		propertyValue, closer, err := db.Get(encoding.EncodePropertyKey(nodeID))
		if errors.Is(err, pebble.ErrNotFound) {
			continue
		}
		if err != nil {
			_ = nodeIter.Close()
			return err
		}
		propertyData := append([]byte(nil), propertyValue...)
		if err := closer.Close(); err != nil {
			_ = nodeIter.Close()
			return err
		}
		props, err := properties.UnmarshalProperties(propertyData)
		if err != nil {
			_ = nodeIter.Close()
			return err
		}
		for propKey, propValue := range props {
			indexKey, err := encoding.EncodeIndexKey(label, propKey, propValue, nodeID)
			if err != nil {
				_ = nodeIter.Close()
				return err
			}
			if err := batch.Set(indexKey, nil, nil); err != nil {
				_ = nodeIter.Close()
				return err
			}
		}
	}
	if err := errors.Join(nodeIter.Error(), nodeIter.Close()); err != nil {
		return err
	}

	versionValue := make([]byte, 4)
	binary.BigEndian.PutUint32(versionValue, currentSchemaVersion)
	if err := batch.Set(schemaVersionKey, versionValue, nil); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func decodeLegacyOutgoingEdgeKey(key []byte) (uuid.UUID, uuid.UUID, string, error) {
	if len(key) < 35 || key[0] != encoding.PrefixEdgeOut {
		return uuid.Nil, uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}
	label, consumed := encoding.DecodeLabel(key[17:])
	if consumed == 0 {
		return uuid.Nil, uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}
	otherOffset := 17 + consumed
	if len(key) != otherOffset+16 {
		return uuid.Nil, uuid.Nil, "", encoding.ErrInvalidKeyFormat
	}
	var srcID, dstID uuid.UUID
	copy(srcID[:], key[1:17])
	copy(dstID[:], key[otherOffset:])
	return srcID, dstID, label, nil
}
