package benchmarks

import (
	"math/rand"
	"testing"

	"github.com/npclaudiu/pathway"
)

func BenchmarkTraverseOut(b *testing.B) {
	nodeCount := 2000
	// Create a dense graph for traversal test
	edgeCount := nodeCount * 5
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		ids := GenerateRandomGraph(b, db, nodeCount, edgeCount)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := ids[rand.Intn(len(ids))]
			// Query builder essentially starts its own read context if we follow standard,
			// but here NewTraversalSource takes *Database.
			// To benchmark "just" traversal inside a view (fair comparison?),
			// checking query.go: ToList() starts a NewReadTx.
			// So we should NOT wrap it in db.View which starts ANOTHER tx.
			// NewTraversalSource(db) is correct.

			// Benchmarking full query lifecycle (including tx start/end)
			list, err := pathway.NewTraversalSource(db).V(startID).Out().ToList()
			if err != nil {
				b.Fatalf("traversal error: %v", err)
			}
			// Just verify count
			count := len(list)
			if count < 0 {
				b.Fatal("negative count")
			}
		}
	})
}

func BenchmarkBFS_2Hop(b *testing.B) {
	nodeCount := 1000
	edgeCount := nodeCount * 3
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		ids := GenerateRandomGraph(b, db, nodeCount, edgeCount)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := ids[rand.Intn(len(ids))]
			_, err := pathway.NewTraversalSource(db).V(startID).Out().Out().ToList()
			if err != nil {
				b.Fatalf("2-hop traversal error: %v", err)
			}
		}
	})
}
