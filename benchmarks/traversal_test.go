package benchmarks

import (
	"context"
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

func BenchmarkTraverseOut(b *testing.B) {
	nodeCount := 2000
	// Create a dense graph for traversal test
	edgeCount := nodeCount * 5
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		ids := GenerateRandomGraph(b, db, nodeCount, edgeCount)
		rng := rand.New(rand.NewSource(3))
		wantDegree := edgeCount / nodeCount
		validation, err := pathway.NewTraversalSource(db).V(ids[0].String()).Out().ToList()
		if err != nil {
			b.Fatalf("validate traversal workload: %v", err)
		}
		if len(validation) != wantDegree {
			b.Fatalf("expected out-degree %d, got %d", wantDegree, len(validation))
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := ids[rng.Intn(len(ids))]
			// ToList owns the read transaction, so this measures the complete query
			// lifecycle rather than nesting another snapshot around it.
			list, err := pathway.NewTraversalSource(db).V(startID.String()).Out().ToList()
			if err != nil {
				b.Fatalf("traversal error: %v", err)
			}
			// Just verify count
			count := len(list)
			if count < 0 {
				b.Fatal("negative count")
			}
		}
		b.StopTimer()
	})
}

func BenchmarkBFS_2Hop(b *testing.B) {
	nodeCount := 1000
	edgeCount := nodeCount * 3
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		ids := GenerateRandomGraph(b, db, nodeCount, edgeCount)
		rng := rand.New(rand.NewSource(4))
		wantPaths := (edgeCount / nodeCount) * (edgeCount / nodeCount)
		validation, err := pathway.NewTraversalSource(db).V(ids[0].String()).Out().Out().ToList()
		if err != nil {
			b.Fatalf("validate two-hop workload: %v", err)
		}
		if len(validation) != wantPaths {
			b.Fatalf("expected %d two-hop paths, got %d", wantPaths, len(validation))
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := ids[rng.Intn(len(ids))]
			_, err := pathway.NewTraversalSource(db).V(startID.String()).Out().Out().ToList()
			if err != nil {
				b.Fatalf("2-hop traversal error: %v", err)
			}
		}
		b.StopTimer()
	})
}

func BenchmarkTraverseOutHighDegree(b *testing.B) {
	const degree = 1000
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		source := uuid.New()
		neighbors := make([]uuid.UUID, degree)
		if err := db.BulkUpdate(context.Background(), func(writer *pathway.BulkWriter) error {
			if err := writer.PutNode(source, "Hub"); err != nil {
				return err
			}
			for i := range neighbors {
				neighbors[i] = uuid.New()
				if err := writer.PutNode(neighbors[i], "Neighbor"); err != nil {
					return err
				}
				if _, err := writer.PutEdge(source, neighbors[i], "LINK"); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("set up high-degree graph: %v", err)
		}

		g := pathway.NewTraversalSource(db)
		ids, err := g.V(source.String()).Out("LINK").IDs().ToList()
		if err != nil || len(ids) != degree {
			b.Fatalf("validate ID projection: len=%d, err=%v", len(ids), err)
		}
		nodes, err := g.V(source.String()).Out("LINK").ToList()
		if err != nil || len(nodes) != degree {
			b.Fatalf("validate node projection: len=%d, err=%v", len(nodes), err)
		}

		b.Run("IDs", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				results, err := g.V(source.String()).Out("LINK").IDs().ToList()
				if err != nil || len(results) != degree {
					b.Fatalf("ID traversal: len=%d, err=%v", len(results), err)
				}
			}
		})

		b.Run("Labels", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				results, err := g.V(source.String()).Out("LINK").ToList()
				if err != nil || len(results) != degree {
					b.Fatalf("label traversal: len=%d, err=%v", len(results), err)
				}
			}
		})
	})
}
