package pathway

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"
)

const currentSchemaVersion uint32 = 5

var schemaVersionKey = []byte("\x00pathway/schema-version")

// ensureSchema initializes new databases and rejects obsolete development
// formats. Pathway has not made a persistent-format compatibility promise, so
// incompatible pre-release stores must be recreated rather than migrated.
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
			return fmt.Errorf(
				"%w: found version %d, supported version is %d; recreate this pre-release store",
				ErrUnsupportedSchema,
				version,
				currentSchemaVersion,
			)
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
	if !empty {
		return fmt.Errorf(
			"%w: unversioned development store; recreate it with schema version %d",
			ErrUnsupportedSchema,
			currentSchemaVersion,
		)
	}
	return writeSchemaVersion(db)
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
