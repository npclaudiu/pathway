package benchmarks

import (
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

// RunBenchmark executes a benchmark function against both Memory and Disk storage backends.
func RunBenchmark(b *testing.B, fn func(b *testing.B, db *pathway.Database)) {
	b.Helper()

	// 1. In-Memory Benchmark
	b.Run("Memory", func(b *testing.B) {
		db, err := pathway.Open(":memory:")
		if err != nil {
			b.Fatalf("failed to open memory db: %v", err)
		}
		defer db.Close()
		fn(b, db)
	})

	// 2. Disk-Based Benchmark
	b.Run("Disk", func(b *testing.B) {
		dir, err := os.MkdirTemp("", "pathway-bench-*")
		if err != nil {
			b.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir)

		db, err := pathway.Open(dir)
		if err != nil {
			b.Fatalf("failed to open disk db: %v", err)
		}
		defer db.Close()
		fn(b, db)
	})
}

// GenerateRandomGraph creates a graph with `nodeCount` nodes and `edgeCount` edges.
// It returns the list of node IDs created.
func GenerateRandomGraph(b *testing.B, db *pathway.Database, nodeCount, edgeCount int) []string {
	var nodeIDs []string
	ctx := b.Context()

	// batch size for inserts
	batchSize := 1000

	// Create Nodes
	for i := 0; i < nodeCount; i += batchSize {
		end := i + batchSize
		if end > nodeCount {
			end = nodeCount
		}

		err := db.Update(ctx, func(tx *pathway.Tx) error {
			for j := i; j < end; j++ {
				id := uuid.New()
				nodeIDs = append(nodeIDs, id.String())
				if err := tx.PutNode(id, "Person"); err != nil {
					return err
				}
				if err := tx.SetProperties(id, map[string]interface{}{
					"age":  rand.Intn(100),
					"name": fmt.Sprintf("User-%d", j),
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatalf("failed to insert nodes: %v", err)
		}
	}

	// Create Edges
	if edgeCount > 0 && len(nodeIDs) > 1 {
		for i := 0; i < edgeCount; i += batchSize {
			end := i + batchSize
			if end > edgeCount {
				end = edgeCount
			}

			err := db.Update(ctx, func(tx *pathway.Tx) error {
				for j := i; j < end; j++ {
					fromStr := nodeIDs[rand.Intn(len(nodeIDs))]
					toStr := nodeIDs[rand.Intn(len(nodeIDs))]
					if fromStr == toStr {
						toStr = nodeIDs[(rand.Intn(len(nodeIDs))+1)%len(nodeIDs)]
					}

					from, _ := uuid.Parse(fromStr)
					to, _ := uuid.Parse(toStr)

					edgeID, err := tx.PutEdge(from, to, "KNOWS")
					if err != nil {
						return err
					}
					if err := tx.SetProperties(edgeID, map[string]interface{}{
						"weight": rand.Float64(),
					}); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				b.Fatalf("failed to insert edges: %v", err)
			}
		}
	}

	return nodeIDs
}
