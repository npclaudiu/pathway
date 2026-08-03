package benchmarks

import (
	"bytes"
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
		rng := rand.New(rand.NewSource(2))

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Random access
			targetID := ids[rng.Intn(len(ids))]
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

func BenchmarkResolveExternalID(b *testing.B) {
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		externalID, err := pathway.NewExternalID("git-object/sha1", bytes.Repeat([]byte{0x42}, 20))
		if err != nil {
			b.Fatal(err)
		}
		var expected uuid.UUID
		if err := db.Update(b.Context(), func(tx *pathway.Tx) error {
			var err error
			expected, err = tx.PutNodeByExternalID(externalID, "Commit")
			return err
		}); err != nil {
			b.Fatal(err)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := db.View(b.Context(), func(tx *pathway.Tx) error {
				nodeID, exists, err := tx.ResolveExternalID(externalID)
				if err != nil {
					return err
				}
				if !exists || nodeID != expected {
					b.Fatalf("resolved node = %s/%v, want %s", nodeID, exists, expected)
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkScanNodes(b *testing.B) {
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
				defer closeBenchmarkResource(b, iter)

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

func BenchmarkFindNodes(b *testing.B) {
	const nodeCount = 5000
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		GenerateRandomGraph(b, db, nodeCount, 0)
		ctx := context.Background()
		age42Count := 0
		rng := rand.New(rand.NewSource(1))
		for range nodeCount {
			if rng.Intn(100) == 42 {
				age42Count++
			}
		}

		for _, benchmark := range []struct {
			name    string
			propKey string
			value   interface{}
			want    int
		}{
			{name: "UniqueHit", propKey: "name", value: "User-2500", want: 1},
			{name: "SharedPrefixMiss", propKey: "name", value: "User-", want: 0},
			{name: "Miss", propKey: "name", value: "not-present", want: 0},
			{name: "SelectiveHit", propKey: "age", value: 42, want: age42Count},
		} {
			b.Run(benchmark.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					count := 0
					err := db.View(ctx, func(tx *pathway.Tx) error {
						iter := tx.FindNodes("Person", benchmark.propKey, benchmark.value)
						defer closeBenchmarkResource(b, iter)
						for iter.Next() {
							count++
						}
						return iter.Error()
					})
					if err != nil {
						b.Fatalf("indexed lookup error: %v", err)
					}
					if count != benchmark.want {
						b.Fatalf("expected %d nodes, got %d", benchmark.want, count)
					}
				}
			})
		}
	})
}
