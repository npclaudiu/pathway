package benchmarks

import (
	"testing"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

func BenchmarkInsertNode(b *testing.B) {
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		ctx := b.Context()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			id := uuid.New()
			err := db.Update(ctx, func(tx *pathway.Tx) error {
				if err := tx.PutNode(id, "Benchmark"); err != nil {
					return err
				}
				return tx.SetProperties(id, map[string]interface{}{
					"index": i,
				})
			})
			if err != nil {
				b.Fatalf("failed to insert node: %v", err)
			}
		}
	})
}

func BenchmarkBatchInsertNode_100(b *testing.B) {
	const batchSize = 100
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		ctx := b.Context()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := db.Update(ctx, func(tx *pathway.Tx) error {
				for j := 0; j < batchSize; j++ {
					id := uuid.New()
					err := tx.PutNode(id, "Benchmark")
					if err != nil {
						return err
					}
					err = tx.SetProperties(id, map[string]interface{}{
						"batch": i,
					})
					if err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				b.Fatalf("failed to batch insert: %v", err)
			}
		}
	})
}

func BenchmarkInsertEdge(b *testing.B) {
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		// Edge endpoints must exist. Reusing them intentionally measures parallel
		// edge insertion under Pathway's multigraph semantics.
		nodes := GenerateRandomGraph(b, db, 2, 0)
		if len(nodes) < 2 {
			b.Fatal("not enough nodes generated")
		}
		fromUUID := nodes[0]
		toUUID := nodes[1]
		ctx := b.Context()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := db.Update(ctx, func(tx *pathway.Tx) error {
				edgeID, err := tx.PutEdge(fromUUID, toUUID, "BENCH_EDGE")
				if err != nil {
					return err
				}
				return tx.SetProperties(edgeID, map[string]interface{}{
					"iteration": i,
				})
			})
			if err != nil {
				b.Fatalf("failed to insert edge: %v", err)
			}
		}
	})
}
