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

Pebble format upgrades are permanent, so back up the database first. On its
first open, Pathway then atomically migrates the original unversioned Pathway
keys to schema version 2. New and in-memory databases do not need the Pebble
upgrade. A database written by a newer, unsupported Pathway schema is rejected
with `ErrUnsupportedSchema`.

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
		V(alice.String()).
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
* **Edges**: Directed, labeled connections with UUIDs and optional properties.
  Pathway is a multigraph, so parallel edges are preserved as distinct records.
* **Properties**: JSON-compatible key-value pairs attached to existing nodes or
  edges. Node properties have exact, type-aware indexes.
* **Constraints**:
  * **Labels and indexed property names**: Maximum 65,535 UTF-8 bytes.
  * **IDs**: UUIDs only.
  * **Properties**: Standard JSON-compatible values; indexed strings, numbers,
    and booleans remain type-distinct.

### Fluid Query Capabilities

Pathway currently supports these Gremlin-inspired traversal steps:

* **Traversal**: `V`, `Out`, `In`
* **Filtering**: `HasLabel`
* **Projection**: `Values`, `Path`
* **Recursion**: `Repeat` with `Times`

`Values` emits one scalar for each requested property that exists. `Path`
returns a typed `pathway.Path` containing ordered node and edge elements.

## Documentation

For a practical guide on data modeling and graph queries, refer to the [Social
Network Tutorial](docs/tutorial.md). Otherwise, consult the [API
Reference](docs/api.md), [storage format](docs/storage-format.md), and
[architecture notes](docs/design.md), plus the
[runnable example](examples/social_network/main.go). Benchmark methodology and
reproducible commands are documented in [docs/benchmarks.md](docs/benchmarks.md).
