# Pathway

Pathway is an experimental Go library for an embedded, persistent graph database
based on the [Pebble](https://github.com/cockroachdb/pebble) key-value database.
It provides a fluid, Gremlin-like query interface for natural graph traversals.

> **NOTE**: This is an experimental library that was not validated for
> production use.

Vibe coded using [Google Antigravity](https://antigravityide.com).

## Key Features

* **Embedded & ACID**: Built on Pebble (LSM-Tree) with full Snapshot Isolation
  transaction support.
* **Fluid Query Interface**: Expressive, chainable traversals (e.g.,
  `V().Out().HasLabel()`).
* **Dual-Edge Indexing**: Edges are stored bidirectionally, enabling **O(1)**
  lookups for both outgoing (`Alice -> ?`) and incoming (`? -> Alice`)
  traversals.
* **Schema-less**: Supports dynamic properties on both Nodes and Edges (Polyglot
  Persistence).

## Installation

```bash
go get github.com/npclaudiu/pathway
```

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
 // Open an in‑memory database for the example.
 db, err := pathway.Open(":memory:")
 if err != nil {
  log.Fatalf("failed to open db: %v", err)
 }
 defer db.Close()

 ctx := context.Background()

 // Create a node.
 nodeID := uuid.New()
 if err := db.Update(ctx, func(tx *pathway.Tx) error {
  if err := tx.PutNode(nodeID, "User"); err != nil {
   return err
  }
  // Set a property.
  return tx.SetProperties(nodeID, map[string]interface{}{"name": "alice"})
 }); err != nil {
  log.Fatalf("failed to create node: %v", err)
 }

 // Query the node back.
 if err := db.View(ctx, func(tx *pathway.Tx) error {
  label, exists, err := tx.GetNode(nodeID)
  if err != nil {
   return err
  }
  if exists {
   log.Printf("node %s has label %s", nodeID, label)
  }
  return nil
 }); err != nil {
  log.Fatalf("read transaction failed: %v", err)
 }
}
```

## Architecture & Performance

### Data Model

* **Nodes**: Atomic entities identified by **16-byte UUIDs**.
* **Edges**: Directed connections with a **Label** and properties.
* **Properties**: Key-Value pairs attached to nodes/edges.
* **Constraints**:
  * **Labels**: Recommended max 255 bytes.
  * **IDs**: UUIDs only.

### Performance Characteristics

* **Node Lookup**: **O(1)** (Direct Key Access).
* **Edge Traversal**: **O(E)** (Linear to the number of edges on the node).
* **Writes**: Atomic batch commits.

## Concurrency & Thread Safety

* **Graph Handle**: The `*Database` instance is **safe** for concurrent use.
* **Transactions**: Individual `Tx` (Read-Write) and `ReadTx` (Read-Only)
  objects are **NOT thread-safe**. They must be confined to a single goroutine.
* **Isolation**: Read transactions see a consistent snapshot of the database at
  the time of creation, isolated from concurrent writes.

## Fluid Query Capabilities

Pathway supports a rich set of traversal steps inspired by Gremlin:

* **Traversal**: `Out`, `In`, `Both`, `OutE`, `InV`
* **Filtering**: `Has`, `HasLabel`, `Where`
* **Projection**: `Values`, `Limit`, `Count`, `Path`
* **Recursion**: `Repeat`, `Until`, `Times`

## Documentation

For a comprehensive guide on data modeling and complex graph queries, see the
[Social Network Tutorial](docs/tutorial.md).
