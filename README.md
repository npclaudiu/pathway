# Pathway

Pathway is a vibe coding experiment aiming to produce a Go library for an
embedded, persistent graph database
based on the [Pebble](https://github.com/cockroachdb/pebble) key-value database.
It provides a fluid, Gremlin-like query interface for natural graph traversals.

> **NOTE**: This library is in an early stage of development.
> Use with caution.

## Installation

```bash
go get github.com/npclaudiu/pathway
```

### Development storage compatibility

Pathway currently uses schema version 5 and has not made a persistent-format
compatibility promise. Unversioned stores and stores using earlier development
schemas are rejected with `ErrUnsupportedSchema`; recreate them with the
current release. Establish migrations and compatibility fixtures before using
Pathway where stored data cannot be rebuilt.

## Quickstart

Initialize the database, perform transactions, and run queries.

```go
package main

import (
	"context"
	"log"

	"github.com/google/uuid"
	"github.com/npclaudiu/pathway"
)

func main() {
	db, err := pathway.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	if err := db.Update(ctx, func(tx *pathway.Tx) error {
		for id, name := range map[uuid.UUID]string{alice: "Alice", bob: "Bob"} {
			if err := tx.PutNode(id, "User"); err != nil {
				return err
			}
			if err := tx.SetProperties(id, map[string]any{"name": name}); err != nil {
				return err
			}
		}
		_, err := tx.PutEdge(alice, bob, "FOLLOWS")
		return err
	}); err != nil {
		log.Fatal(err)
	}

	names, err := pathway.NewTraversalSource(db).
		V(alice).
		Out("FOLLOWS").
		Values("name").
		ToList()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Alice follows %v", names)
}
```

## Features

### Data Model

* **Nodes**: Atomic entities identified by **16-byte UUIDs**.
  Existing domain identities can be bound through immutable, namespaced
  `ExternalID` values while adjacency records keep compact internal UUIDs.
* **Edges**: Directed, labeled connections with UUIDs and optional properties.
  Pathway is a multigraph, so parallel edges are preserved as distinct records.
* **Properties**: Typed key-value maps attached to existing nodes or edges,
  including signed and unsigned integers, floats, strings, bytes, booleans,
  nulls, lists, and nested maps. Selected node properties can have exact,
  type-aware indexes.
* **Constraints**:
  * **Labels and indexed property names**: Maximum 65,535 UTF-8 bytes.
  * **IDs**: UUIDs only.
  * **Properties**: Signed integers decode as `int64`, unsigned integers as
    `uint64`, and floating-point values as `float64`. Indexed signed, unsigned,
    and floating-point values remain type-distinct.

### Property indexes

Configure indexes by node label and property when opening the database:

```go
db, err := pathway.OpenWithOptions("graph.db", pathway.Options{
	Indexes: []pathway.IndexDefinition{
		{Label: "User", Property: "name"},
	},
})
```

The configured slice is authoritative: Pathway atomically builds newly added
indexes from existing nodes and drops removed indexes while opening. A `nil`
slice, including the one used by `Open`, preserves definitions stored in an
existing database and creates no indexes for a new database. Use a non-nil
empty slice to remove all indexes. `FindNodes` only returns results for a
configured label/property pair.

`OpenWithOptions` takes an owned snapshot of its configuration. It clones
`PebbleOptions`, mutable Pebble listener/map/slice containers, and `Indexes`
before applying defaults or replacing the filesystem for `:memory:`. Callers
may therefore reuse one options value for concurrent opens, and later changes
to the original containers do not alter an open database.

### Write durability

Updates are synchronously durable by default. For replayable imports where
throughput matters more than retaining the most recent acknowledged writes
after a crash, opt into relaxed commits explicitly:

```go
db, err := pathway.OpenWithOptions("graph.db", pathway.Options{
	Durability: pathway.DurabilityNoSync,
})
```

Both modes commit each `Update` as one atomic Pebble batch and make it visible
before returning. `DurabilitySync` (the zero value) synchronizes the WAL before
success is reported. `DurabilityNoSync` permits Pebble to buffer recent WAL
writes in process memory, so a process or machine crash can lose successful
updates. Schema marker and index-definition changes always use synced commits.

### Bulk ingestion

Use `BulkUpdate` to make high-throughput graph loading explicit:

```go
err := db.BulkUpdate(ctx, func(writer *pathway.BulkWriter) error {
	if err := writer.PutNode(alice, "User"); err != nil {
		return err
	}
	if err := writer.PutNode(bob, "User"); err != nil {
		return err
	}
	_, err := writer.PutEdge(alice, bob, "FOLLOWS")
	return err
})
```

All nodes, edges, and properties staged by the callback commit once and
atomically using the database's configured durability. Any writer-operation or
callback error rolls back the entire batch—even if a writer error is
accidentally ignored. Within one callback, Pathway caches node-existence checks,
so many edges sharing endpoints do not repeatedly read the same node records.
The writer is valid only during its callback and is not safe for concurrent use.
Ordinary `Tx.PutEdge` also validates endpoints with existence-only key probes;
it does not copy or decode node labels.

### Fluid Query Capabilities

Pathway currently supports these Gremlin-inspired traversal steps:

* **Traversal**: `V`, `Out`, `In`
* **Filtering**: `HasLabel`
* **Projection**: `IDs`, `Values`, `Path`
* **Recursion**: `Repeat` with `Until`, `Times`, and `Emit`

`IDs` emits node UUIDs without loading labels, which is useful for high-degree
traversals. `Values` emits one scalar for each requested property that exists.
`Path` returns a typed `pathway.Path` containing ordered node and edge elements.
Use `ToNodes`, `ToEdges`, `ToPaths`, or `ToIDs` to collect common result shapes
without type assertions. `Values` remains dynamic because a property can be any
supported scalar, list, or map; collect it with `ToList`.
Labels passed to `Out` or `In` use exact Pebble ranges instead of scanning
unrelated adjacency entries; multi-label results have deterministic storage-key
order regardless of argument order.

Direct `NodeIterator` and `EdgeIterator` values are forward-only. Call `Next`
before reading each typed `Node` or `Edge`, check `Error` after `Next` returns
false, and always call `Close`. Typed UUIDs and labels are owned values that
remain valid after the iterator advances or closes; raw storage buffers and
seek operations are not exposed. Reading a typed value outside its current
result window returns `ErrInvalidIteratorState`. Transaction iterators must be
closed before their owning transaction.

Every `Repeat` must be followed immediately by a positive `Times`, a non-nil
`Until`, or the explicit `AllowUnboundedRepeat` opt-in. Repeat modifiers may be
combined and can appear in any order, but they cannot be added after another
traversal or projection step. Invalid configurations return `ErrInvalidRepeat`
when the traversal executes. `Emit` controls intermediate results and does not
terminate a repeat.

Repeat bodies are compiled once and run breadth-first. The default
`RepeatDeduplicateNodes` mode visits each UUID once, so converging paths produce
one result. Use `WithRepeatVisitMode(RepeatPathSensitive)` to preserve distinct
simple paths to the same node. Both modes suppress cycles: node mode uses a
repeat-wide visited set, while path mode rejects a node already in that
traverser's ancestry. `AllowUnboundedRepeat` therefore means no explicit depth
or predicate limit, not that cycles are revisited forever. Use a cancellable
context for large traversals.

`V` accepts `uuid.UUID` values directly and does not parse them. When IDs arrive
as text, use `VStrings`; it returns `ErrInvalidNodeID` for any malformed value
instead of silently constructing a partial traversal:

```go
query, err := g.VStrings(textID)
if err != nil {
	return err
}
results, err := query.Out("FOLLOWS").ToNodes()
```

Large traversals can be consumed with bounded memory by using the typed
streaming terminal for their result shape:

```go
err := g.V().HasLabel("Person").EachNode(ctx, func(person pathway.Node) error {
	fmt.Println(person.ID)
	return nil
})
```

`EachNode`, `EachEdge`, `EachPath`, and `EachID` propagate their context to query
hooks and stop on cancellation or when the callback returns an error. In every
case, they close the traversal iterators and read transaction before returning.
They return `ErrTraversalResult` if the pipeline's final result shape does not
match the selected terminal.

Each typed collection terminal has a context variant, such as
`ToNodesContext(ctx)`, for cancelling planning, reads, scans, repeats, and
collection. `Each` and `ToList` remain the dynamic terminals for `Values` and
custom pipelines.

### External Identities

Imports can preserve stable source identifiers without deriving UUIDs. An
external identity has an exact, case-sensitive UTF-8 namespace, an optional
opaque scope, and opaque value bytes. Pathway owns all component bytes and does
not normalize them. Keep the identifier algorithm in the namespace and store
digests as raw bytes:

```go
digest, err := hex.DecodeString(commitChecksum)
if err != nil {
	return err
}
commitID, err := pathway.NewScopedExternalID(
	"git-object/sha1",
	[]byte(canonicalRepositoryURL),
	digest,
)
if err != nil {
	return err
}
err = db.Update(ctx, func(tx *pathway.Tx) error {
	_, err := tx.PutNodeByExternalID(commitID, "Commit")
	return err
})
```

`BindExternalID` attaches additional identities to an existing node;
`ResolveExternalID` performs the reverse lookup. Reimporting the same identity
is idempotent, while a binding to another node returns
`ErrExternalIDConflict`. `VExternal` resolves identities and starts a normal
UUID-backed traversal:

```go
query, err := pathway.NewTraversalSource(db).VExternal(commitID)
if err != nil {
	return err
}
commits, err := query.ToNodes()
```

Use different namespaces for SHA-1 and SHA-256. Repository-local identities
belong in `Scope`; length-delimited storage keeps scope/value boundaries
unambiguous. Resolving an external start adds forward, reverse-consistency, and
node-existence reads before traversal; adjacency keys and traversal hops remain
UUID-based.

## Documentation

For a practical guide on data modeling and graph queries, refer to the [Social
Network Tutorial](docs/tutorial.md). Otherwise, consult the [API
Reference](docs/api.md), [storage format](docs/storage-format.md), and
[architecture notes](docs/design.md), plus the
[runnable example](examples/social_network/main.go). Benchmark methodology and
reproducible commands are documented in [docs/benchmarks.md](docs/benchmarks.md).
