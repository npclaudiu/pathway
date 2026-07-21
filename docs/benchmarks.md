# Benchmarking Pathway

The benchmark suite runs each workload against an in-memory Pebble filesystem
and a temporary on-disk database. Setup and workload validation happen before
the timed region. Read and traversal benchmarks use an already populated
database, so the disk results represent warm-cache operation plus transaction
overhead; they are not cold-start or cold-I/O measurements.

Run the complete suite with allocations:

```bash
go test ./benchmarks -run '^$' -bench . -benchmem -count 5
```

Run focused suites:

```bash
go test ./benchmarks -run '^$' -bench 'Benchmark(GetNode|ScanNodes|FindNodes)$' -benchmem -count 5
go test ./benchmarks -run '^$' -bench 'Benchmark(TraverseOut|BFS_2Hop)$' -benchmem -count 5
go test ./benchmarks -run '^$' -bench 'Benchmark(InsertNode|BatchInsertNode_100|InsertEdge|BulkInsertEdge_100|InsertEdgeLargeEndpointLabels)$' -benchmem -count 5
```

`BenchmarkScanNodes` measures a full node scan. `BenchmarkFindNodes` separately
measures an exact unique hit, a miss, an adversarial shared-prefix miss, and a
lower-selectivity numeric hit. Traversal setup creates deterministic adjacency
layers and validates the expected one-hop and two-hop cardinalities before
starting the timer.

The shared benchmark fixture explicitly configures `Person/name` and
`Person/age` indexes. This keeps `BenchmarkFindNodes` an indexed lookup while
write benchmarks include the maintenance cost only for those selected
properties.

Every write benchmark has `Sync` and `NoSync` sub-benchmarks, each containing
the usual `Memory` and `Disk` backends. For example,
`BenchmarkInsertNode/Sync/Disk` measures the durable default, while
`BenchmarkInsertNode/NoSync/Disk` measures an explicitly relaxed WAL commit.
The in-memory variants expose CPU and allocation differences but cannot measure
storage durability. Never interpret the `NoSync` throughput as free: recent
successful updates may be lost after a process or machine crash.

`BenchmarkBatchInsertNode_100` uses `BulkUpdate` for node and property loading.
`BenchmarkBulkInsertEdge_100` inserts 100 parallel edges between the same two
nodes in one bulk callback, exercising the endpoint cache: each distinct
endpoint is validated once per batch. Compare it with `BenchmarkInsertEdge` to
separate batching and validation reuse from single-transaction latency.
`BenchmarkInsertEdgeLargeEndpointLabels` uses maximum-length node labels to
make accidental endpoint-value copies show up clearly in allocation and byte
metrics; correct existence-only validation does not scale with label size.

For before/after comparisons, capture both revisions and use `benchstat`:

```bash
go test ./benchmarks -run '^$' -bench . -benchmem -count 10 > before.txt
go test ./benchmarks -run '^$' -bench . -benchmem -count 10 > after.txt
benchstat before.txt after.txt
```

CPU and memory profiles can be collected for a focused benchmark:

```bash
go test ./benchmarks -run '^$' -bench BenchmarkTraverseOut/Memory -cpuprofile cpu.out
go test ./benchmarks -run '^$' -bench BenchmarkFindNodes/Memory -memprofile mem.out
go tool pprof cpu.out
```

Avoid treating results from different machines or noisy shared CI runners as
regressions. Record the Go version, operating system, CPU, benchmark count, and
whether the filesystem cache was warm when publishing numbers.
