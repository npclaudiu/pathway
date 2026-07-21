# Pathway Storage Format

Pathway stores graph records in Pebble using byte-ordered key prefixes. This
document describes Pathway schema version 3. It is a compatibility contract for
opening existing databases, not a replacement for the public Go API.

All integer length fields are unsigned big-endian values. UUID fields contain
the UUID's 16 raw bytes. Labels and property names used in node indexes have a
maximum encoded length of 65,535 bytes. Labels are intended to be UTF-8, but
schema v3 does not validate their encoding.

## Schema marker and migration

The key `0x00 || "pathway/schema-version"` stores a four-byte, big-endian
Pathway schema version. The current value is `3`.

When an unversioned database is opened, Pathway treats it as schema version 1
and atomically migrates it to version 3. The migration rewrites adjacency keys,
creates edge-ID reverse records, rebuilds property indexes, persists their
definitions, and writes the version marker in one synced Pebble batch.

Opening schema version 2 atomically creates a persisted definition for every
label/property pair present on an existing node, retaining the corresponding
typed index entries. This preserves indexes that can be inferred from existing
data; applications should pass an explicit desired definition set when they
need indexes for properties not yet present. An interruption leaves either the
complete old schema or the complete new schema, so reopening is safe. A
malformed or unsupported schema marker returns `ErrUnsupportedSchema`.

Databases originally written with Pebble v1 must first have their Pebble format
upgraded as described in the project README. Back up an on-disk database before
either upgrade.

## Record prefixes

| Prefix | Record | Key suffix | Value |
|---:|---|---|---|
| `0x01` | Node | `node-ID` | `label-length:u16 \| label` |
| `0x02` | Outgoing adjacency | `source-ID \| label-length:u16 \| label \| target-ID \| edge-ID` | `edge-ID` |
| `0x03` | Incoming adjacency | `target-ID \| label-length:u16 \| label \| source-ID \| edge-ID` | `edge-ID` |
| `0x04` | Properties | `entity-ID` | Protobuf-encoded property map |
| `0x05` | Node property index | See below | Empty |
| `0x06` | Edge-ID reverse index | `edge-ID` | `source-ID \| target-ID \| label-length:u16 \| label` |
| `0x07` | Property-index definition | `label-length:u16 \| label \| property-length:u16 \| property` | Empty |

The edge ID is part of both adjacency keys, giving Pathway multigraph
semantics: edges with identical endpoints and labels remain distinct. The
reverse record lets `DeleteEdge` find and remove both adjacency records and the
edge's properties with bounded point operations.

## Property indexes

Each configured node label/property pair receives exact-match index entries:

```text
0x05 | label-length:u16 | label |
       property-length:u16 | property |
       value-type:u8 | value-length:u32 | value |
       node-ID
```

The value type tags are:

| Tag | Value |
|---:|---|
| `0` | Null |
| `1` | Boolean |
| `2` | Number, canonicalized to an IEEE-754 `float64` bit pattern |
| `3` | String |
| `4` | Bytes (supported by the encoder; normal properties canonicalize bytes to base64 strings) |
| `5` | JSON list |
| `6` | JSON object |

Type and length fields prevent collisions such as string `"1"` versus number
`1`, or exact value `"a"` versus `"ab"`. The current index supports equality
lookups only; its numeric representation does not define range-query ordering.

Changing a node label removes and creates that node's entries according to the
old and new labels' configured definitions in the same transaction. Deleting a
node removes its configured index entries and all incoming, outgoing, and
self-loop edge records, including edge properties.

## Index definition lifecycle

`Options.Indexes` reconciles the desired definitions while the database opens.
For every new definition, Pathway deletes any stale keys in that
label/property range, scans existing nodes, builds entries from their canonical
property records, and persists the definition. Removing a definition deletes
its complete index range and definition record. All changes commit together in
one synced batch.

A `nil` configuration preserves the definition records already in the store;
for a new store that means no indexes. A non-nil configuration is
authoritative, and a non-nil empty slice removes all indexes. During normal
updates, unconfigured properties produce no index writes and configured entries
are changed only when their canonical value changes.
