// Command social_network demonstrates Pathway's storage and traversal APIs.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	db, err := pathway.Open(":memory:")
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	var followsID uuid.UUID
	if err := db.Update(ctx, func(tx *pathway.Tx) error {
		for id, name := range map[uuid.UUID]string{alice: "Alice", bob: "Bob"} {
			if err := tx.PutNode(id, "User"); err != nil {
				return err
			}
			if err := tx.SetProperties(id, map[string]any{
				"name":   name,
				"active": true,
			}); err != nil {
				return err
			}
		}

		followsID, err = tx.PutEdge(alice, bob, "FOLLOWS")
		if err != nil {
			return err
		}
		return tx.SetProperties(followsID, map[string]any{"since": 2024})
	}); err != nil {
		return err
	}

	g := pathway.NewTraversalSource(db)
	names, err := g.V(alice.String()).Out("FOLLOWS").Values("name").ToList()
	if err != nil {
		return err
	}
	fmt.Printf("Alice follows: %v\n", names)

	results, err := g.V(alice.String()).Out("FOLLOWS").Path().ToList()
	if err != nil {
		return err
	}
	for _, element := range results[0].(pathway.Path) {
		fmt.Printf("%s %-7s %s\n", element.Kind, element.Label, element.ID)
	}

	return db.Update(ctx, func(tx *pathway.Tx) error {
		return tx.DeleteEdge(followsID)
	})
}
