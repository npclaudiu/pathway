# Pathway Architecture and Maintainer Design Notes

This document describes the architecture implemented by the current Pathway
codebase. It focuses on invariants, ownership, execution flow, persistence, and
maintenance hazards. Public API usage belongs in the tutorial and API
reference; exact schema bytes are specified in
[storage-format.md](storage-format.md).

Pathway is experimental. Some public types expose Pebble directly, and several
query APIs are intentionally narrower than their Gremlin inspiration. Treat
the implementation and tests as authoritative when this document and the code
disagree.

## Design goals and boundaries

Pathway is an embedded, persistent, directed property-graph library for Go. It
is designed around these choices:

- Pebble provides ordered key-value storage, snapshots, batches, WALs,
  compaction, and crash recovery.
- Nodes and edges use UUIDs. Edges are directed, labeled, and first-class
  entities that may own properties.
- The graph is a multigraph. Parallel edges with identical endpoints and labels
  remain distinct because every edge has its own UUID.
- Graph mutations update all denormalized records in one Pebble batch.
- Reads are exposed both as typed transaction operations and as lazy iterators.
- The fluent traversal API builds a pipeline of iterator transformations and
  materializes it in a read snapshot at `ToList`.
- Node-property indexes are explicitly configured by label/property pair,
  persisted in the store, and support exact typed equality only.

Pathway is not a server, distributed database, query optimizer, schema/type
system, or serializable transaction manager. It has no network protocol,
locking API, uniqueness constraints, range property queries, or conflict
detection between concurrent writers.

## Component map

```text
Application
   |
   +-- Database / Tx / Snapshot ---------------- public storage API
   |       |
   |       +-- graph mutations and typed reads
   |       +-- Pebble indexed batches and snapshots
   |
   +-- TraversalSource / TraversalPipeline ------ public query API
           |
           +-- iterator adapters ---------------- execution engine
                   |
                   +-- Tx point reads and prefix scans
                           |
                           +-- internal/encoding -- ordered keys and records
                           +-- internal/properties -- property codec
                                   |
                                   +-- internal/proto
                           |
                           +-- Pebble
```

The main source files have intentionally coarse responsibilities:

| File or package | Responsibility |
|---|---|
| `database.go` | Database lifecycle, options, managed read/write callbacks, compaction |
| `bulk.go` | Atomic ingestion writer, sticky errors, and endpoint-validation cache |
| `indexes.go` | Persisted index-definition reconciliation, rebuilds, and runtime lookup cache |
| `tx.go` | Transaction abstraction, graph mutation algorithms, point reads, scan construction |
| `schema.go` | Pathway schema marker, initialization, and supported migrations to v3 |
| `internal/encoding` | Key prefixes, adjacency/reverse records, typed index encoding |
| `internal/properties` | Protobuf conversion for dynamic property maps |
| `internal/proto` | Generated property message; never edit generated Go manually |
| `iterator.go` | Typed iterators and traversal iterator adapters |
| `query.go` | Fluent pipeline construction, terminal execution, public path representation |
| `predicates.go` | Small dynamically typed predicate helpers |
| `ogm.go` | Reflection-based property-to-struct loading |
| `snapshot.go` | Low-level raw Pebble snapshot wrapper |
| `errors.go` | Public sentinel errors |

## Data model and invariants

### Nodes

A node consists of a caller-supplied UUID and a string label. `PutNode` is an
upsert: the same UUID updates the existing node rather than returning a
duplicate error. Labels may be changed. A label change removes and creates the
configured property-index entries needed by the old and new labels in the same
batch.

Labels are byte-length-prefixed with a `uint16`, so their encoded byte form may
not exceed 65,535 bytes. Labels are intended to be UTF-8, but the encoder
currently enforces only length and does not validate UTF-8 or reject any
particular character, despite the wording of `ErrInvalidLabel`.

### Edges

`PutEdge` generates a fresh UUID and requires both endpoint node keys to be
visible through the current transaction. Validation probes existence without
copying or decoding labels. It creates three records:

1. an outgoing adjacency record anchored at the source;
2. an incoming adjacency record anchored at the target;
3. an edge-ID reverse record containing source, target, and label.

The edge UUID appears in both adjacency keys, so repeated insertion of the same
`(source, target, label)` creates parallel edges. Traversing neighbors therefore
may return the same neighboring node more than once, once for each edge.

The three records are one logical edge. Code that adds another edge-derived
record must update `PutEdge`, `DeleteEdge`, `DeleteNode`, migrations, and
corruption tests together.

### Entity UUID namespace

Node and edge properties use the same key family, keyed only by entity UUID.
Maintainers should therefore treat node and edge UUIDs as one global namespace.
The public API does not explicitly prevent a caller from creating a node whose
UUID equals an existing edge UUID. If that happens, the property record is
shared and `SetProperties` identifies the UUID as a node first. Random UUIDs
make accidental collisions negligible, but imported or deterministic IDs need
external validation until the library enforces this invariant.

### Properties

Properties are a complete map associated with an existing node or edge.
`SetProperties` replaces the whole map; it is not a patch operation. Unknown
UUIDs return `ErrEntityNotFound` and do not create orphan records.

Configured node properties are indexed; unconfigured node properties and all
edge properties are not. Clearing properties removes configured node index
entries and writes an empty property value. A missing property key and an empty
decoded property map both currently surface as `nil` from `GetProperties`.

## Persistence architecture

### Ordered key families

Every Pathway key begins with a one-byte record prefix. Nodes, outgoing edges,
incoming edges, properties, indexes, edge reverse records, and persisted index
definitions occupy separate contiguous ranges. Prefix scans use a lower bound
equal to the desired prefix and `keyUpperBound(prefix)` as an exclusive upper
bound.

The exact schema-v3 layouts and type tags are documented in
[storage-format.md](storage-format.md). Important architectural consequences
are:

- Node scans are ordered lexicographically by UUID bytes.
- Adjacency scans are ordered by anchor UUID, encoded label, other endpoint,
  then edge UUID—not insertion time. Encoded labels sort by byte length first
  and label bytes second.
- Exact index results are ordered by node UUID after the complete typed value.
- `OutEdges` and `InEdges` define this adjacency order for their results.
  Multi-label filters preserve the same order restricted to the unique labels,
  independently of caller argument order. Other query ordering remains
  observable but is not a stable public contract.
- Adjacency values repeat the edge UUID even though it is also in the key. The
  iterators read it from the value; changing this duplication is a schema
  change.

### Property serialization and canonicalization

`internal/properties` stores a protobuf message whose map values are
`google.protobuf.Value`. This deliberately limits values to JSON-like dynamic
types and has several consequences:

- All Go integer and floating-point inputs become protobuf numbers and decode
  as `float64`.
- Integers beyond the exact `float64` range may lose precision.
- `[]byte` is converted by `structpb` to a base64 string and decodes as a
  string. Although the index encoder defines a bytes tag, normal persisted
  properties do not retain a distinct byte-slice type.
- Lists decode as `[]any`; objects decode as `map[string]any`.
- Unsupported Go values fail before the batch commits.
- Non-finite floating-point values should be avoided. `structpb` represents
  them specially when converted back to Go and can change their apparent type.

`SetProperties` intentionally marshals and immediately unmarshals the new map
before building indexes. Indexes are therefore based on the same canonical
values that later reads and index deletion will observe. Removing this round
trip without replacing it with an equivalent canonicalization step can leave
undeletable or unqueryable index records.

### Exact typed indexes

Every configured node label/property pair creates index keys containing:

```text
label | property name | value type | value length | value | node UUID
```

Length delimiters prevent a lookup for `"a"` from matching `"ab"`; a type tag
prevents string `"1"` from colliding with number `1`. Numeric scalar inputs are
canonicalized to `float64`, and negative zero is normalized to positive zero.
Lists and objects are JSON-encoded after property canonicalization.

`FindNodes` constructs the complete prefix preceding the node UUID and scans
only that range. It is an equality API, not a range API: IEEE-754 bits are not
encoded for numeric sort order. Encoding failures are returned through the
`NodeIterator.Error` channel because `FindNodes` itself does not return an
error.

Definitions use their own key family and are loaded into an immutable, nested
map on `Database` after schema migration and option reconciliation. Transactions
use that cache instead of reading definition records on every mutation.

Indexes are denormalized state. Their correctness depends on all node mutation
paths:

- `SetProperties` considers configured properties only and skips entries whose
  canonical encoded key is unchanged.
- `PutNode` removes configured entries for the old label and adds configured
  entries for the new label when a label changes.
- `DeleteNode` removes configured entries before deleting the node.
- Index reconciliation clears and rebuilds each newly configured range from
  canonical node properties, or deletes the range for a removed definition.
- Schema migration rebuilds legacy indexes or materializes definitions for
  existing typed indexes, depending on the source version.

### Schema versioning and migration

`OpenWithOptions` opens Pebble, calls `ensureSchema`, reconciles persisted index
definitions with `Options.Indexes`, and only then returns a `Database`.

- An empty store receives the current four-byte schema marker with a synced
  write.
- A store with the current marker opens normally.
- Schema v2 is migrated to v3; a malformed or otherwise unsupported marker
  returns `ErrUnsupportedSchema`.
- Any non-empty store with no marker is treated as the original unversioned
  schema (v1) and migrated directly to schema v3.

The v1-to-v3 migration treats legacy outgoing adjacency records as the source
of truth. It deletes both legacy adjacency ranges, recreates outgoing and
incoming records with edge IDs in their keys, creates reverse records, deletes
all legacy property indexes, and rebuilds indexes from nodes and their property
records. It also records a definition for every rebuilt label/property pair.
The data changes and new marker are committed in one synced Pebble batch, so
interruption before commit leaves v1 and interruption after commit leaves v3.

The v2-to-v3 migration does not rewrite already typed index entries. It scans
current nodes and persists a definition for each label/property pair it finds,
preserving every index that can be inferred from current data. A caller can
provide an authoritative desired `Options.Indexes` set on the same open; the
subsequent reconciliation then adds or removes definitions atomically.

This atomic approach is restart-safe but consumes batch memory proportional to
the migrated graph. Large-database migration will eventually need a staged,
checkpointed design. Orphan incoming records are intentionally not preserved;
incoming adjacency is reconstructed from outgoing records.

Pathway schema versioning is independent of Pebble's format version. A database
written by Pebble v1 must first undergo the Pebble upgrade described in the
README.

## Database and transaction lifecycle

### Opening and options

`Open(":memory:")` installs Pebble's in-memory filesystem and passes an empty
path to Pebble. `OpenWithOptions` uses the caller's `*pebble.Options` directly;
for the in-memory special case it assigns the filesystem on that object. This
means caller-owned options are currently mutable shared configuration rather
than a cloned value.

`Options.Indexes` has deliberately tri-state slice semantics. `nil` preserves
the definitions already stored and creates none in a new database. A non-nil
slice is the complete desired set; an empty one drops every index. Reconciliation
uses one synced Pebble batch for all definition records and index data, so a
failed or interrupted open cannot expose a partially changed configuration.
Building each added definition scans nodes and reads matching-label property
records. Removing one issues a range deletion over its exact label/property
prefix. Index changes therefore require exclusive database open and may make
startup expensive, but normal transactions never coordinate a mutable catalog.

`Options.Durability` controls ordinary `Database.Update` commits. Its zero value
is `DurabilitySync`, preserving the historical guarantee that a successful
update has synchronized its WAL record to stable storage. `DurabilityNoSync`
uses an unsynchronized WAL commit: the batch is still atomic and immediately
visible, but Pebble may retain recent WAL bytes in process memory, so a process
or machine crash can lose updates whose callbacks already returned success.
The resolved Pebble write option is validated once at open and cached on
`Database`; invalid enum values return `ErrInvalidDurability`. Schema marker
writes, migrations, and index-definition reconciliation deliberately continue
to use `pebble.Sync` in both modes.

`Database` is safe for concurrent use to the extent provided by Pebble. The
atomic transaction counter assigns internal IDs, but those IDs and the optional
`Logger` are not currently used. A `TraversalPipeline` or `Tx` should not be
shared concurrently unless future code explicitly adds such a guarantee.

### Write callbacks

`Database.Update` creates a Pebble indexed batch. The indexed form is necessary
because mutation algorithms read their own earlier writes—for example, nodes
can be created and then connected in one callback.

If the callback succeeds, the entire batch commits with the database's resolved
durability option; if it returns an error, closing the uncommitted batch rolls
it back. Both modes preserve batch atomicity and read-your-writes. The default
synced mode makes the update durable before returning; the relaxed mode trades
that crash guarantee for lower commit latency. Neither mode provides
application-level conflict detection or serializable isolation between
concurrent `Update` callbacks. The write path does not create a separate
snapshot, so maintainers must not infer a stronger transaction model than
Pebble's batch behavior.

Single-record durable workloads pay a WAL sync per callback; grouping writes in
one transaction remains the primary bulk-ingestion optimization.

`Database.BulkUpdate` makes that optimization explicit. It constructs one
`BulkWriter` inside a normal `Update`, so commit, rollback, durability,
read-your-writes, indexes, and graph invariants remain shared with the ordinary
transaction path. The writer exposes only `PutNode`, `PutEdge`, and
`SetProperties`; it does not expose the unchecked edge insertion helper or raw
Pebble operations.

The writer stores the first operation error. After a failure, later calls return
that error without staging more data, and `BulkUpdate` returns it even when the
callback mistakenly returns `nil`. This prevents a partially successful import
from committing. A callback error also aborts the batch. The writer is marked
closed when the callback ends and is not safe for concurrent use.

Its UUID-to-existence map avoids repeated node probes during edge-heavy imports.
`PutNode` records the new node immediately, so a later edge to that node needs
no endpoint read. For pre-existing nodes, the first `PutEdge` performs an
existence-only probe for each distinct endpoint; later edges reuse the cached
result. The package-private `Tx.putEdge` performs the three edge writes after
either normal `Tx.PutEdge` validation or cached bulk validation. It must remain
unexported.

The context passed to `Update` is stored on `Tx` but is not checked by point
reads, scans, or commit. Cancellation therefore does not currently interrupt a
write callback automatically.

### Managed and reusable reads

`Database.View` creates a Pebble snapshot, invokes the callback, and closes the
snapshot. All reads in the callback observe a consistent point-in-time view.
The deferred snapshot-close error is currently ignored.

`NewReadTx` exposes the same snapshot-backed behavior with explicit lifetime;
the caller must call `Close`. `Tx.Close` is idempotent for read transactions and
sets an internal closed flag so later iterator construction returns
`pebble.ErrClosed` rather than entering Pebble's closed-snapshot panic path.

`Snapshot` is a separate low-level raw-key wrapper. Its `Get` copies Pebble's
borrowed value and translates Pebble not-found into `ErrKeyNotFound`. Its
context argument is unused, and its error conventions differ from `Tx.Get`.

`Tx.Get` always copies values before closing Pebble's value closer. Iterator
`Key` and `Value` methods, in contrast, expose Pebble's current borrowed slices;
callers must copy them before advancing the iterator if they need to retain
them.

### Raw escape hatches

`Tx.Get`, `Set`, `Delete`, `NewIterator`, and `Access`, plus `Snapshot.Get`,
expose low-level storage behavior. Several signatures include Pebble types.
These are useful for advanced access and tests but couple the public API to the
storage engine. Raw writes can violate every graph invariant and should not be
used for ordinary application data.

## Mutation algorithms

### `PutNode`

1. Validate the label byte length.
2. Read the existing node through the transaction.
3. If its label changes and either label has configured indexes, load its
   properties, remove matching entries for the old label, and add matching
   entries for the new label.
4. Write the node record.

The cost of creating a node is one existence read plus one write. Relabeling is
one node write when neither label has indexes. Otherwise it is linear in
property count and adds writes only for properties configured on the relevant
old or new label.

### `PutEdge`

1. Probe both endpoint node keys without copying or decoding their values;
   missing endpoints return `ErrDanglingEdge`.
2. Generate an edge UUID.
3. Encode outgoing, incoming, and reverse records.
4. Write all three to the batch.

The existence probes read through the indexed batch, preserving read-your-writes
for nodes created earlier in the same update. Edge properties, if any, require a
separate `SetProperties` call in the same update. `BulkWriter.PutEdge` validates
each distinct endpoint once per bulk callback and then uses the same
package-private edge write path.

### `SetProperties`

1. Serialize and canonicalize the complete new map.
2. Resolve the UUID as a node first, then as an edge through the reverse index.
3. For a node with configured indexes, load its old properties and encode the
   old and new key for each configured property.
4. Delete and insert only keys whose canonical encoded value changed; handle a
   removed or newly present configured property with one delete or insert.
5. Replace the property record.

Property serialization remains linear in the complete replacement map. Index
maintenance is linear in the configured-property count, unchanged values cause
no index writes, and a label with no indexes avoids the old-property read.

### `DeleteEdge`

1. Read the reverse record by edge UUID; absence returns `ErrEdgeNotFound`.
2. Decode endpoints and label.
3. Reconstruct and delete outgoing and incoming adjacency keys.
4. Delete the reverse record and property record.

All four deletes remain in the caller's batch and commit atomically. Corrupt
reverse data aborts the update before any partial state is committed.

### `DeleteNode`

1. Load the label and, only when it has configured indexes, the properties;
   remove matching node index entries.
2. Scan both outgoing and incoming adjacency ranges.
3. Decode and collect edge UUIDs before mutating either range.
4. Deduplicate UUIDs so a self-loop, present in both scans, is deleted once.
5. Call `DeleteEdge` for every incident edge.
6. Delete the node and node-property record.

The algorithm is linear in degree plus property count and stores all incident
edge UUIDs in memory. Iterator, decoding, and close errors abort the update.
Deleting a missing node currently succeeds rather than returning
`ErrNodeNotFound`; `DeleteEdge` has different missing-entity semantics.

## Iterator architecture

### Public contract

`Iterator` exposes cursor operations plus `Path`. `NodeIterator` and
`EdgeIterator` add typed decoders. The intended consumption pattern is:

```go
iter := tx.OutEdges(id)
defer iter.Close()
for iter.Next() {
    edgeID, otherID, label, err := iter.Edge()
    if err != nil {
        return err
    }
    // consume the current entry
}
return iter.Error()
```

High-level scan constructors seek the underlying Pebble iterator immediately.
Their wrapper's `first` flag makes the first call to `Next` return that already
positioned record; later calls advance Pebble.

Construction errors are represented by an inert `errorIterator`. Every method
is safe, `Next` and `SeekGE` return false, and `Error` preserves the cause. This
avoids nil-wrapper panics while retaining the existing API shape.

### Concrete adapters

- `nodeIterator` decodes the node UUID from the key and label from the value.
- `edgeIterator` decodes the edge UUID from the value and the relative other
  endpoint from the adjacency key.
- `multiEdgeIterator` lazily opens one exact label range at a time. Prefixes are
  sorted and deduplicated before iteration, so at most one Pebble iterator is
  open and output matches adjacency-key order.
- `nodeIndexIterator` takes the node UUID from the key suffix and the label from
  the index key; it does not point-read the node.
- `fixedNodeIterator` resolves explicit IDs supplied to `V` with an existence
  probe and loads their labels only when requested.
- `flatMapEdgeIterator` converts each input node into an adjacency iterator and
  flattens those iterators. It consumes the package-private ID-only iterator
  capability when available, so another hop does not force label loading.
- `neighborIterator` converts each adjacency entry into a lightweight node
  reference. Its UUID comes directly from the adjacency key; `Node`, `Value`,
  and `Path` load and cache the other endpoint's label on demand.
- `idIterator` materializes UUID results without requesting labels.
- `filterIterator`, `valueIterator`, `pathIterator`, and `repeatIterator`
  implement the remaining query steps.

`NodeIterator` remains source-compatible for external implementations. The
execution engine detects an optional package-private `NodeID` method and falls
back to `Node` for third-party iterators. Labels are not duplicated into
adjacency records, so this optimization does not change the persistent schema
or add a relabel synchronization invariant.

### Iterator limitations

The interface is broader than several adapters can implement meaningfully.
`SeekGE` returns false for fixed, index, projection, repeat, and neighbor
iterators. `Valid` is approximate for some wrappers. `filterIterator`
structurally implements both typed iterator interfaces even though its actual
input determines which one is meaningful. New code should not assume uniform
seek semantics across all iterator types.

Malformed node labels are not consistently rejected: `GetNode` and
`nodeIterator.Node` currently ignore a failed `DecodeLabel` result. Edge and
reverse-record decoders are stricter. Corruption handling should become
consistent before treating low-level raw writes as supported.

`flatMapEdgeIterator.Next` uses recursion to skip empty inner iterators. A long
sequence of input nodes with no matching edges can grow the stack.

## Traversal and query execution

### Pipeline construction

`TraversalSource.V` creates a mutable `TraversalPipeline` containing a slice of
`Step` closures. A step accepts the read transaction and previous iterator and
returns a replacement iterator. Builder methods append steps and return the
same pipeline pointer; pipelines are not immutable values and should not be
modified concurrently.

With no IDs, `V` starts `ScanNodes`. With textual IDs, it parses UUIDs and uses
`fixedNodeIterator`. Malformed UUID strings are silently dropped, and valid but
missing nodes are skipped. Explicit-ID order is preserved; scan order is UUID
byte order.

`Out` and `In` use this composition:

```text
NodeIterator
  -> flatMapEdgeIterator(OutEdges or InEdges)
  -> neighborIterator(lazy node reference)
  -> NodeIterator
```

UUIDs flow between navigation steps through an internal ID-only capability.
`IDs` consumes that capability and therefore scans adjacency without
point-reading each neighbor's node record. Normal node materialization,
`HasLabel`, and `Path` request labels and perform one lazy point read per
neighbor. `Values` uses the UUID directly and reads only the property record.
`OutEdges` and `InEdges` encode each requested label into the prefix already
present in adjacency keys. A single label becomes one exact Pebble lower/upper
bound. Multiple labels become sorted, deduplicated, disjoint bounded iterators
consumed one at a time, so unrelated labels are never advanced through and only
one Pebble iterator is open at once.

`HasLabel` currently assumes a node stream and evaluates an anonymous
`{ID, Label}` value. There is no general typed element algebra or query planner;
pipeline type compatibility is enforced dynamically at iteration time.

### Terminal execution

`ToList` performs all execution:

1. Validate the database.
2. Invoke the optional query-start hook with `context.Background` and the
   constant descriptor `"Traversal Query"`.
3. Create one snapshot-backed read transaction.
4. Apply step closures in order to form the final iterator stack.
5. Drain it into `[]interface{}`, allocating the backing slice lazily.
6. Close the iterator stack and transaction, joining close errors.
7. Invoke the query-end hook with duration and the final error.

Terminal results depend on the final iterator:

- projected `resultIterator` values, including UUIDs from `IDs`, are appended
  directly;
- nodes become exported `Node` values containing UUID and label;
- edges become exported `Edge` values containing edge UUID, relative endpoint,
  and label;
- an unknown generic iterator falls back to an owned copy of its raw key.

The terminal uses an iterator cardinality hint when one is available. Otherwise
it starts with capacity 16, which covers common shallow traversals without the
repeated slice growth previously visible in allocation profiles. Empty results
remain nil and do not allocate a result backing slice. Struct values and copied
fallback keys remain valid after the snapshot and Pebble iterators close.

There is no streaming terminal API; every result is held in memory. `ToList`
does not accept or observe a caller context, and the contexts accepted by
transaction constructors are not checked during iteration. Observability hooks
therefore always receive a background context for traversal queries. Hook
panics are not recovered. The configured `Logger` is currently unused.

### ID and property projection

`IDs()` accepts a node stream and emits one `uuid.UUID` per input node. It does
not retain path state or load node labels. Explicit `V(id)` still performs one
existence-only probe for the starting node; each traversed neighbor UUID comes
from its adjacency entry. On stores corrupted through raw writes, an ID-only
traversal can therefore expose a dangling neighbor UUID, while a label-bearing
projection detects its missing node record as `ErrNodeNotFound`. Public edge
mutation APIs prevent dangling edges.

`Values(keys...)` accepts node or edge streams. For each entity, it point-loads
the complete property map, then emits one scalar for each requested key that is
present, preserving requested-key order. Missing properties are omitted. With
multiple input entities, values are flattened into one stream; the result does
not retain the source entity or property name. Calling `Values` with no keys
emits nothing.

This design is simple but introduces one property read and full map decode per
input entity.

### Paths

`Path` materializes a typed `pathway.Path`, an ordered slice of
`PathElement`. Node entries contain kind, UUID, and label. Edge entries contain
the edge UUID and label plus `Other`, the relative endpoint reached by that
adjacency step. A path edge does not currently retain both endpoints or an
explicit direction.

Path slices are copied by `pathIterator`, so advancing an iterator or mutating
one materialized result does not mutate another result. The `dir` field on
`neighborIterator` is currently unused.

### Repeat execution

`Repeat` stores a mutable `RepeatConfig` captured by the pipeline step.
`Until`, `Times`, and `Emit` mutate the active configuration and silently do
nothing when no repeat is active. Some later steps clear `activeRepeat`, while
`Out` currently does not, so modifier ordering is not uniformly enforced.

`repeatIterator` performs breadth-first expansion:

- it first drains the upstream node iterator into an in-memory queue;
- it removes the queue head by reslicing;
- it builds and executes the repeat sub-pipeline separately for each traverser;
- it enqueues returned nodes with incremented depth;
- termination is checked when a traverser is dequeued.

There is no visited set or cycle detection. A repeat with neither a reachable
`Until` condition nor a positive `Times` limit can run forever on a cyclic
graph. Queue reslicing retains backing memory, and per-traverser pipeline
construction is expensive. Repeat path propagation is also incomplete because
each sub-traversal starts from a fresh fixed-node path rather than explicitly
carrying the entire parent path.

## OGM and predicates

`Tx.Load` is a small reflection helper, not a full object-graph mapper. It loads
properties into fields tagged `graph:"property"` on a non-nil struct pointer.
It supports assignable values and one special conversion from `float64` to
`int`; unsupported fields are silently left unchanged. Missing properties
return `ErrNodeNotFound` even if the requested UUID was intended as an edge.
Nil property values and more complex conversions need additional defensive
handling before this API can be considered robust.

Predicate helpers are dynamically typed. `Gt` and `Lt` require matching `int`
or `float64` types; they do not coerce between them. `Prefix` and `Contains`
accept strings only. `Eq` uses interface equality and can panic when handed
incomparable dynamic values such as maps or slices. Predicates are currently
used most visibly by repeat termination; they are not a general indexed-filter
language.

## Errors and corruption behavior

Public sentinel errors cover invalid database/snapshot handles, missing raw
snapshot keys, missing nodes/edges/entities, dangling edge creation, and
unsupported schema versions. Pebble errors such as `pebble.ErrNotFound`,
`pebble.ErrReadOnly`, and `pebble.ErrClosed` can also escape from public APIs.
Encoding errors live in `internal/encoding` but are observable through iterator
and mutation returns.

Error conventions are not yet completely uniform:

- `GetNode` represents absence with `exists == false`; `GetProperties` returns
  `nil`; `DeleteEdge` returns `ErrEdgeNotFound`; `DeleteNode` succeeds.
- `Snapshot.Get` translates absence to `ErrKeyNotFound`, while `Tx.Get` returns
  Pebble's error.
- Iterator construction errors are deferred to `Iterator.Error`.
- `View` ignores snapshot-close errors, while `ToList` and `DeleteNode` join
  relevant close errors.

Raw storage corruption normally aborts mutations that decode edge or migration
records. Node decoding is less strict as noted above. Tests deliberately inject
malformed adjacency and reverse records to verify rollback. New decoders should
validate lengths before slicing or narrowing integer fields and should return
stable errors instead of panicking.

## Performance model

The principal costs and write amplification are architectural, not incidental:

| Operation | Dominant work |
|---|---|
| Create node | existence read, node write |
| Relabel node | node write; with relevant indexes, property read plus deletes/inserts for configured properties |
| Set node properties | property encode/write; with indexes, old-property read/decode plus writes for changed configured values |
| Create edge | two existence-only endpoint probes, three record writes |
| Bulk-create edges | at most one existence-only probe per distinct node in the callback, three record writes per edge |
| Set edge properties | reverse-record existence read, property encode/write |
| Delete edge | reverse point read, four deletes |
| Delete node | property/index work, two adjacency scans, incident-edge UUID set, four deletes per edge |
| Exact indexed lookup | snapshot creation plus bounded index scan |
| One-hop traversal with `IDs` | starting-node existence probe plus adjacency scan; no neighbor-label reads |
| Label-bearing one-hop traversal | adjacency scan plus one lazy node point read per edge |
| Label-filtered adjacency | one exact bounded scan per unique requested label |
| `Values` | one property point read and full decode per entity |
| `ToList` | snapshot lifecycle, one result slice, and typed value boxing |

Write benchmarks exercise both synchronous and relaxed durability, with memory
and disk backends under each mode. The benchmark harness seeds deterministic
adjacency layers, validates cardinality outside timed regions, and separately
measures scans and exact indexes. See [benchmarks.md](benchmarks.md) before
interpreting results.

Likely optimization directions must preserve invariants:

- batching label reads when label-bearing nodes must be materialized;
- streaming terminal methods and caller contexts;
- a typed property codec that avoids protobuf/`structpb` hot-path conversions;
- queue-head indexes and visited policies for repeat traversal.

## Safe extension playbooks

### Changing persistent storage

Pathway currently has no compatibility clients. Until a compatibility promise
is made, persistent layout changes may replace the development format directly
without data or schema migrations. Define the new byte layout and invariants,
update mutation/deletion/decoding together, adjust the current schema marker so
old development stores are rejected rather than misread, and update golden
encoding tests plus [storage-format.md](storage-format.md). Developers may
recreate old stores.

Before the first compatibility release, replace this policy with a versioned
migration and fixture strategy.

Never change a key prefix or field order as a local refactor. Pebble ordering is
part of traversal bounds and therefore part of the persisted schema.

### Adding a derived record or index

List every source mutation that can make the record stale. At minimum consider
create, update, relabel, delete, node cascade, migration, and raw corruption.
Write source and derived records in the same `Update` batch. Add tests that
assert both positive logical results and the physical absence of stale keys.

### Adding a traversal step

1. Specify its accepted upstream element type and output type.
2. Decide whether it is a graph-element iterator or a `resultIterator`.
3. Propagate upstream errors and close ownership exactly once.
4. Define `Next`, `Valid`, `SeekGE`, `Key`, `Value`, and `Path` behavior—even if
   some operations deliberately return false or nil.
5. Preserve path immutability if the step contributes path state.
6. Define ordering, duplicates, missing-property behavior, and materialized
   result shape.
7. Test empty streams, construction failures, malformed data, and cleanup.
8. Update `ToList` if a new terminal result category is introduced.

The pipeline is dynamically typed today. Prefer an explicit error over a panic
or silent type mismatch.

### Changing property representation

Property bytes and index bytes are coupled through canonicalization. A codec
change must either preserve decoded scalar identity or rebuild every property
index. Pay special attention to number precision, byte slices, nulls, nested
values, non-finite floats, and deterministic map encoding. Update `Tx.Load` and
query projections alongside the codec.

### Changing transaction or durability behavior

Make consistency and crash guarantees explicit. Read-your-writes is required
by graph construction. All denormalized edge and index records must commit
atomically. Keep `DurabilitySync` as the zero-value default, treat
`DurabilityNoSync` as an explicit risk choice, and benchmark both modes. New
relaxed modes must not silently apply to schema or index-catalog changes.

## Tests, generated files, and maintenance workflow

The repository separates root-package unit tests, `internal` codec/encoding
tests, black-box integration tests under `tests`, schema migration tests, and
benchmarks.

Useful commands are:

```bash
go test ./...
go test -race ./...
go vet ./...
make lint
make docs
make docs-check
make bench
```

`make generate` regenerates `internal/proto/properties.pb.go` from the `.proto`
source. `make docs` regenerates `docs/api.md`; do not hand-edit that generated
file. The tools have a separate module under `tools` so generator and linter
dependencies do not enter the library's runtime dependency graph.

For persistent changes, tests should cover the logical API, exact key format,
absence of stale records, rejection of obsolete development formats, and safe
failure on malformed bytes. For performance changes, validate the intended
workload before the timed region and compare multiple runs with `benchstat`.

## Known architectural debt

`IMPROVEMENTS.md` is the tracked roadmap. The most consequential remaining
design issues are:

- label-bearing and property projections still perform per-result point reads;
- mutable, dynamically typed pipelines and dynamically typed projections;
- no context-aware or streaming terminal execution;
- incomplete repeat validation, cycle policy, queue efficiency, and paths;
- inconsistent iterator seek/valid and corruption semantics;
- public exposure of Pebble types and mutable caller-owned Pebble options;
- limited reflection loading and dynamically typed predicates;

These limitations are documented so maintainers do not accidentally treat them
as intended long-term contracts. Changes should remain compatible where useful,
but correctness and explicit semantics take precedence over preserving
placeholder behavior.
