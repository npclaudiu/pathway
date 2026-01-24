package benchmarks

import (
	"context"
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

func BenchmarkGetNode(b *testing.B) {
	nodeCount := 10000
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		// seeding
		ids := GenerateRandomGraph(b, db, nodeCount, 0)
		ctx := context.Background()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Random access
			targetIDStr := ids[rand.Intn(len(ids))]
			targetID, _ := uuid.Parse(targetIDStr)
			err := db.View(ctx, func(tx *pathway.Tx) error {
				_, exists, err := tx.GetNode(targetID)
				if err != nil {
					return err
				}
				if !exists {
					b.Fatalf("node not found: %s", targetID)
				}
				return nil
			})
			if err != nil {
				b.Fatalf("view error: %v", err)
			}
		}
	})
}

func BenchmarkFindNodes(b *testing.B) {
	nodeCount := 5000
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		GenerateRandomGraph(b, db, nodeCount, 0)
		ctx := context.Background()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			count := 0
			// Full scan using ScanNodes
			err := db.View(ctx, func(tx *pathway.Tx) error {
				iter := tx.ScanNodes()
				defer iter.Close()

				for iter.Next() {
					count++
				}
				return iter.Error()
			})
			if err != nil {
				b.Fatalf("find nodes error: %v", err)
			}
			if count != nodeCount {
				b.Fatalf("expected %d nodes, got %d", nodeCount, count)
			}
		}
	})
}
