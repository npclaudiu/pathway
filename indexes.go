package pathway

import (
	"errors"

	"github.com/cockroachdb/pebble/v2"
	"github.com/google/uuid"
	"github.com/npclaudiu/pathway/internal/encoding"
	"github.com/npclaudiu/pathway/internal/properties"
)

func reconcileIndexDefinitions(db *pebble.DB, desired []IndexDefinition) ([]IndexDefinition, error) {
	stored, err := loadIndexDefinitions(db)
	if err != nil {
		return nil, err
	}
	if desired == nil {
		return stored, nil
	}

	storedSet := indexDefinitionSet(stored)
	desiredSet := make(map[IndexDefinition]struct{}, len(desired))
	for _, definition := range desired {
		if _, err := encoding.EncodeIndexDefinitionKey(definition.Label, definition.Property); err != nil {
			return nil, err
		}
		desiredSet[definition] = struct{}{}
	}

	batch := db.NewBatch()
	defer func() { _ = batch.Close() }()
	changed := false

	for definition := range storedSet {
		if _, keep := desiredSet[definition]; keep {
			continue
		}
		propertyPrefix, err := encoding.EncodeIndexPropertyPrefix(definition.Label, definition.Property)
		if err != nil {
			return nil, err
		}
		if err := batch.DeleteRange(propertyPrefix, keyUpperBound(propertyPrefix), nil); err != nil {
			return nil, err
		}
		definitionKey, err := encoding.EncodeIndexDefinitionKey(definition.Label, definition.Property)
		if err != nil {
			return nil, err
		}
		if err := batch.Delete(definitionKey, nil); err != nil {
			return nil, err
		}
		changed = true
	}

	for definition := range desiredSet {
		if _, exists := storedSet[definition]; exists {
			continue
		}
		propertyPrefix, err := encoding.EncodeIndexPropertyPrefix(definition.Label, definition.Property)
		if err != nil {
			return nil, err
		}
		// Rebuilding starts from a clean range. This normally has no entries for
		// a new definition, but also repairs remnants left by older or manually
		// modified databases.
		if err := batch.DeleteRange(propertyPrefix, keyUpperBound(propertyPrefix), nil); err != nil {
			return nil, err
		}
		if err := buildIndexDefinition(db, batch, definition); err != nil {
			return nil, err
		}
		definitionKey, err := encoding.EncodeIndexDefinitionKey(definition.Label, definition.Property)
		if err != nil {
			return nil, err
		}
		if err := batch.Set(definitionKey, nil, nil); err != nil {
			return nil, err
		}
		changed = true
	}

	if changed {
		if err := batch.Commit(pebble.Sync); err != nil {
			return nil, err
		}
	}
	return definitionsFromSet(desiredSet), nil
}

func loadIndexDefinitions(db *pebble.DB) ([]IndexDefinition, error) {
	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{encoding.PrefixIndexDef},
		UpperBound: keyUpperBound([]byte{encoding.PrefixIndexDef}),
	})
	if err != nil {
		return nil, err
	}

	var definitions []IndexDefinition
	for iter.First(); iter.Valid(); iter.Next() {
		label, property, err := encoding.DecodeIndexDefinitionKey(iter.Key())
		if err != nil {
			_ = iter.Close()
			return nil, err
		}
		definitions = append(definitions, IndexDefinition{Label: label, Property: property})
	}
	if err := errors.Join(iter.Error(), iter.Close()); err != nil {
		return nil, err
	}
	return definitions, nil
}

func buildIndexDefinition(db *pebble.DB, batch *pebble.Batch, definition IndexDefinition) error {
	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: []byte{encoding.PrefixNode},
		UpperBound: []byte{encoding.PrefixEdgeOut},
	})
	if err != nil {
		return err
	}

	for iter.First(); iter.Valid(); iter.Next() {
		if len(iter.Key()) != 17 {
			_ = iter.Close()
			return encoding.ErrInvalidKeyFormat
		}
		label, consumed := encoding.DecodeLabel(iter.Value())
		if consumed == 0 || consumed != len(iter.Value()) {
			_ = iter.Close()
			return encoding.ErrInvalidValueFormat
		}
		if label != definition.Label {
			continue
		}

		var nodeID uuid.UUID
		copy(nodeID[:], iter.Key()[1:])
		propertyValue, closer, err := db.Get(encoding.EncodePropertyKey(nodeID))
		if errors.Is(err, pebble.ErrNotFound) {
			continue
		}
		if err != nil {
			_ = iter.Close()
			return err
		}
		propertyData := append([]byte(nil), propertyValue...)
		if err := closer.Close(); err != nil {
			_ = iter.Close()
			return err
		}
		props, err := properties.UnmarshalProperties(propertyData)
		if err != nil {
			_ = iter.Close()
			return err
		}
		value, exists := props[definition.Property]
		if !exists {
			continue
		}
		indexKey, err := encoding.EncodeIndexKey(definition.Label, definition.Property, value, nodeID)
		if err != nil {
			_ = iter.Close()
			return err
		}
		if err := batch.Set(indexKey, nil, nil); err != nil {
			_ = iter.Close()
			return err
		}
	}
	return errors.Join(iter.Error(), iter.Close())
}

func indexDefinitionMap(definitions []IndexDefinition) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{})
	for _, definition := range definitions {
		propertiesForLabel := result[definition.Label]
		if propertiesForLabel == nil {
			propertiesForLabel = make(map[string]struct{})
			result[definition.Label] = propertiesForLabel
		}
		propertiesForLabel[definition.Property] = struct{}{}
	}
	return result
}

func indexDefinitionSet(definitions []IndexDefinition) map[IndexDefinition]struct{} {
	result := make(map[IndexDefinition]struct{}, len(definitions))
	for _, definition := range definitions {
		result[definition] = struct{}{}
	}
	return result
}

func definitionsFromSet(set map[IndexDefinition]struct{}) []IndexDefinition {
	result := make([]IndexDefinition, 0, len(set))
	for definition := range set {
		result = append(result, definition)
	}
	return result
}

func (tx *Tx) indexedProperties(label string) map[string]struct{} {
	if tx.db == nil {
		return nil
	}
	return tx.db.indexes[label]
}
