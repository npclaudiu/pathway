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
	db, err := pathway.OpenWithOptions(":memory:", pathway.Options{
		// This is the default. Keep it explicit when adapting the example to an
		// on-disk database so successful updates survive crashes.
		Durability: pathway.DurabilitySync,
	})
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
	if err := db.BulkUpdate(ctx, func(writer *pathway.BulkWriter) error {
		for id, name := range map[uuid.UUID]string{alice: "Alice", bob: "Bob"} {
			if err := writer.PutNode(id, "User"); err != nil {
				return err
			}
			if err := writer.SetProperties(id, map[string]any{
				"name":   name,
				"active": true,
			}); err != nil {
				return err
			}
		}

		// Both nodes were staged above, so endpoint validation is served by the
		// bulk writer's cache without additional node reads.
		followsID, err = writer.PutEdge(alice, bob, "FOLLOWS")
		if err != nil {
			return err
		}
		return writer.SetProperties(followsID, map[string]any{"since": 2024})
	}); err != nil {
		return err
	}

	g := pathway.NewTraversalSource(db)
	// Each requested edge label is an exact adjacency range. Multi-label results
	// retain deterministic storage-key order rather than argument order.
	ids, err := g.V(alice.String()).Out("MENTORS", "FOLLOWS").IDs().ToList()
	if err != nil {
		return err
	}
	fmt.Printf("Alice follows IDs: %v\n", ids)
	nodes, err := g.V(alice.String()).Out("FOLLOWS").ToList()
	if err != nil {
		return err
	}
	for _, result := range nodes {
		node := result.(pathway.Node)
		fmt.Printf("Alice follows node: %s (%s)\n", node.ID, node.Label)
	}

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
