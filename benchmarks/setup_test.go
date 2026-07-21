package benchmarks

import (
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

func closeBenchmarkResource(b testing.TB, closer interface{ Close() error }) {
	b.Helper()
	if err := closer.Close(); err != nil {
		b.Errorf("close benchmark resource: %v", err)
	}
}

func removeBenchmarkDirectory(b testing.TB, path string) {
	b.Helper()
	if err := os.RemoveAll(path); err != nil {
		b.Errorf("remove benchmark directory: %v", err)
	}
}

// RunBenchmark executes a benchmark function against both Memory and Disk storage backends.
func RunBenchmark(b *testing.B, fn func(b *testing.B, db *pathway.Database)) {
	b.Helper()

	// 1. In-Memory Benchmark
	b.Run("Memory", func(b *testing.B) {
		b.ReportAllocs()
		db, err := pathway.Open(":memory:")
		if err != nil {
			b.Fatalf("failed to open memory db: %v", err)
		}
		defer closeBenchmarkResource(b, db)
		fn(b, db)
	})

	// 2. Disk-Based Benchmark
	b.Run("Disk", func(b *testing.B) {
		b.ReportAllocs()
		dir, err := os.MkdirTemp("", "pathway-bench-*")
		if err != nil {
			b.Fatalf("failed to create temp dir: %v", err)
		}
		defer removeBenchmarkDirectory(b, dir)

		db, err := pathway.Open(dir)
		if err != nil {
			b.Fatalf("failed to open disk db: %v", err)
		}
		defer closeBenchmarkResource(b, db)
		fn(b, db)
	})
}

// GenerateRandomGraph creates a graph with `nodeCount` nodes and `edgeCount` edges.
// It returns the list of node IDs created.
func GenerateRandomGraph(b *testing.B, db *pathway.Database, nodeCount, edgeCount int) []uuid.UUID {
	nodeIDs := make([]uuid.UUID, 0, nodeCount)
	ctx := b.Context()
	rng := rand.New(rand.NewSource(1))

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
				nodeIDs = append(nodeIDs, id)
				if err := tx.PutNode(id, "Person"); err != nil {
					return err
				}
				if err := tx.SetProperties(id, map[string]interface{}{
					"age":  rng.Intn(100),
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
					// Build deterministic adjacency layers. When edgeCount is a
					// multiple of nodeCount, every node has the same out-degree.
					fromIndex := j % len(nodeIDs)
					layer := j / len(nodeIDs)
					toIndex := (fromIndex + 1 + layer%(len(nodeIDs)-1)) % len(nodeIDs)
					from := nodeIDs[fromIndex]
					to := nodeIDs[toIndex]

					edgeID, err := tx.PutEdge(from, to, "KNOWS")
					if err != nil {
						return err
					}
					if err := tx.SetProperties(edgeID, map[string]interface{}{
						"weight": float64(j+1) / float64(edgeCount),
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
