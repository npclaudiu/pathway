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
		validation, err := pathway.NewTraversalSource(db).V(ids[0]).Out().ToNodes()
		if err != nil {
			b.Fatalf("validate traversal workload: %v", err)
		}
		if len(validation) != wantDegree {
			b.Fatalf("expected out-degree %d, got %d", wantDegree, len(validation))
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := ids[rng.Intn(len(ids))]
			// ToNodes owns the read transaction, so this measures the complete query
			// lifecycle rather than nesting another snapshot around it.
			list, err := pathway.NewTraversalSource(db).V(startID).Out().ToNodes()
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
		validation, err := pathway.NewTraversalSource(db).V(ids[0]).Out().Out().ToNodes()
		if err != nil {
			b.Fatalf("validate two-hop workload: %v", err)
		}
		if len(validation) != wantPaths {
			b.Fatalf("expected %d two-hop paths, got %d", wantPaths, len(validation))
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			startID := ids[rng.Intn(len(ids))]
			_, err := pathway.NewTraversalSource(db).V(startID).Out().Out().ToNodes()
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
		ids, err := g.V(source).Out("LINK").IDs().ToIDs()
		if err != nil || len(ids) != degree {
			b.Fatalf("validate ID projection: len=%d, err=%v", len(ids), err)
		}
		nodes, err := g.V(source).Out("LINK").ToNodes()
		if err != nil || len(nodes) != degree {
			b.Fatalf("validate node projection: len=%d, err=%v", len(nodes), err)
		}

		b.Run("IDs", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				results, err := g.V(source).Out("LINK").IDs().ToIDs()
				if err != nil || len(results) != degree {
					b.Fatalf("ID traversal: len=%d, err=%v", len(results), err)
				}
			}
		})

		b.Run("Labels", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				results, err := g.V(source).Out("LINK").ToNodes()
				if err != nil || len(results) != degree {
					b.Fatalf("label traversal: len=%d, err=%v", len(results), err)
				}
			}
		})
	})
}

func BenchmarkTraverseOutByLabelHighDegree(b *testing.B) {
	const (
		commonEdges    = 5000
		rareEdges      = 5
		secondaryEdges = 7
	)
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		source := uuid.New()
		if err := db.BulkUpdate(context.Background(), func(writer *pathway.BulkWriter) error {
			if err := writer.PutNode(source, "Hub"); err != nil {
				return err
			}
			for label, count := range map[string]int{
				"COMMON":    commonEdges,
				"RARE":      rareEdges,
				"SECONDARY": secondaryEdges,
			} {
				for i := 0; i < count; i++ {
					target := uuid.New()
					if err := writer.PutNode(target, "Neighbor"); err != nil {
						return err
					}
					if _, err := writer.PutEdge(source, target, label); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			b.Fatalf("set up labeled high-degree graph: %v", err)
		}

		g := pathway.NewTraversalSource(db)
		cases := []struct {
			name   string
			labels []string
			want   int
		}{
			{name: "All", want: commonEdges + rareEdges + secondaryEdges},
			{name: "SingleRare", labels: []string{"RARE"}, want: rareEdges},
			{name: "MultipleRare", labels: []string{"SECONDARY", "RARE"}, want: rareEdges + secondaryEdges},
		}
		for _, bench := range cases {
			results, err := g.V(source).Out(bench.labels...).IDs().ToIDs()
			if err != nil || len(results) != bench.want {
				b.Fatalf("validate %s: len=%d, want=%d, err=%v", bench.name, len(results), bench.want, err)
			}
			b.Run(bench.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					results, err := g.V(source).Out(bench.labels...).IDs().ToIDs()
					if err != nil || len(results) != bench.want {
						b.Fatalf("labeled traversal: len=%d, want=%d, err=%v", len(results), bench.want, err)
					}
				}
			})
		}
	})
}

func BenchmarkRepeatBFS(b *testing.B) {
	const (
		cycleNodes    = 128
		branchDepth   = 6
		highDepthHops = 255
	)
	RunBenchmark(b, func(b *testing.B, db *pathway.Database) {
		cycleStart, cycleCount := seedCycle(b, db, cycleNodes)
		branchStart, branchCount := seedBinaryTree(b, db, branchDepth)
		deepStart := seedChain(b, db, highDepthHops)
		g := pathway.NewTraversalSource(db)

		benchmarks := []struct {
			name     string
			pipeline *pathway.TraversalPipeline
			want     int
		}{
			{
				name: "Cycle",
				pipeline: g.V(cycleStart).
					Repeat(func(p *pathway.TraversalPipeline) *pathway.TraversalPipeline { return p.Out("CYCLE") }).
					Emit().
					AllowUnboundedRepeat(),
				want: cycleCount,
			},
			{
				name: "Branching",
				pipeline: g.V(branchStart).
					Repeat(func(p *pathway.TraversalPipeline) *pathway.TraversalPipeline { return p.Out("BRANCH") }).
					Times(branchDepth).
					Emit(),
				want: branchCount,
			},
			{
				name: "HighDepth",
				pipeline: g.V(deepStart).
					Repeat(func(p *pathway.TraversalPipeline) *pathway.TraversalPipeline { return p.Out("DEEP") }).
					Times(highDepthHops),
				want: 1,
			},
		}

		for _, benchmark := range benchmarks {
			b.Run(benchmark.name, func(b *testing.B) {
				validation, err := benchmark.pipeline.ToNodesContext(b.Context())
				if err != nil || len(validation) != benchmark.want {
					b.Fatalf("validate repeat: len=%d, want=%d, err=%v", len(validation), benchmark.want, err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					results, err := benchmark.pipeline.ToNodesContext(b.Context())
					if err != nil || len(results) != benchmark.want {
						b.Fatalf("repeat traversal: len=%d, want=%d, err=%v", len(results), benchmark.want, err)
					}
				}
			})
		}
	})
}

func seedCycle(b testing.TB, db *pathway.Database, count int) (uuid.UUID, int) {
	b.Helper()
	nodes := make([]uuid.UUID, count)
	if err := db.BulkUpdate(context.Background(), func(writer *pathway.BulkWriter) error {
		for i := range nodes {
			nodes[i] = uuid.New()
			if err := writer.PutNode(nodes[i], "Cycle"); err != nil {
				return err
			}
		}
		for i := range nodes {
			if _, err := writer.PutEdge(nodes[i], nodes[(i+1)%len(nodes)], "CYCLE"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatalf("seed cycle: %v", err)
	}
	return nodes[0], len(nodes)
}

func seedBinaryTree(b testing.TB, db *pathway.Database, depth int) (uuid.UUID, int) {
	b.Helper()
	count := (1 << (depth + 1)) - 1
	nodes := make([]uuid.UUID, count)
	if err := db.BulkUpdate(context.Background(), func(writer *pathway.BulkWriter) error {
		for i := range nodes {
			nodes[i] = uuid.New()
			if err := writer.PutNode(nodes[i], "Branch"); err != nil {
				return err
			}
		}
		for parent := 0; 2*parent+1 < len(nodes); parent++ {
			if _, err := writer.PutEdge(nodes[parent], nodes[2*parent+1], "BRANCH"); err != nil {
				return err
			}
			if _, err := writer.PutEdge(nodes[parent], nodes[2*parent+2], "BRANCH"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatalf("seed binary tree: %v", err)
	}
	return nodes[0], len(nodes)
}

func seedChain(b testing.TB, db *pathway.Database, hops int) uuid.UUID {
	b.Helper()
	nodes := make([]uuid.UUID, hops+1)
	if err := db.BulkUpdate(context.Background(), func(writer *pathway.BulkWriter) error {
		for i := range nodes {
			nodes[i] = uuid.New()
			if err := writer.PutNode(nodes[i], "Depth"); err != nil {
				return err
			}
		}
		for i := 0; i < len(nodes)-1; i++ {
			if _, err := writer.PutEdge(nodes[i], nodes[i+1], "DEEP"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatalf("seed chain: %v", err)
	}
	return nodes[0]
}
