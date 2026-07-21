# Pathway

Pathway is an experimental Go library for an embedded, persistent graph database
based on the [Pebble](https://github.com/cockroachdb/pebble) key-value database.
It provides a fluid, Gremlin-like query interface for natural graph traversals.

> **NOTE**: This is an experimental library in an early stage of development.
> Use with caution.

Developed using [Google Antigravity](https://antigravity.google/).

## Installation

```bash
go get github.com/npclaudiu/pathway
```

### Upgrading existing databases

Pathway now uses Pebble v2. Before opening an on-disk database created by an
earlier Pathway version, upgrade its Pebble format with the final v1 release:

```bash
go run github.com/cockroachdb/pebble/cmd/pebble@v1.1.5 db upgrade <db-dir>
```

Pebble format upgrades are permanent, so back up the database first. New and
in-memory databases do not need this step.

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

## Features

### Data Model

* **Nodes**: Atomic entities identified by **16-byte UUIDs**.
* **Edges**: Directed connections with a **Label** and properties.
* **Properties**: Key-Value pairs attached to nodes/edges.
* **Constraints**:
  * **Labels**: Recommended max 255 bytes.
  * **IDs**: UUIDs only.
  * **Properties**: Supports standard JSON types. Encoded with type prefix for
    type safety.

### Fluid Query Capabilities

Pathway supports a rich set of traversal steps inspired by Gremlin:

* **Traversal**: `Out`, `In`, `Both`, `OutE`, `InV`
* **Filtering**: `Has`, `HasLabel`, `Where`
* **Projection**: `Values`, `Limit`, `Count`, `Path`
* **Recursion**: `Repeat`, `Until`, `Times`

## Documentation

For a practical guide on data modeling and graph queries, refer to the [Social
Network Tutorial](docs/tutorial.md). Otherwise, consult the [API
Reference](docs/api.md).
