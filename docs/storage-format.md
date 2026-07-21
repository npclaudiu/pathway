# Pathway Storage Format

Pathway stores graph records in Pebble using byte-ordered key prefixes. This
document describes Pathway schema version 2. It is a compatibility contract for
opening existing databases, not a replacement for the public Go API.

All integer length fields are unsigned big-endian values. UUID fields contain
the UUID's 16 raw bytes. Labels and property names used in node indexes have a
maximum encoded length of 65,535 bytes. Labels are intended to be UTF-8, but
schema v2 does not validate their encoding.

## Schema marker and migration

The key `0x00 || "pathway/schema-version"` stores a four-byte, big-endian
Pathway schema version. The current value is `2`.

When an unversioned database is opened, Pathway treats it as schema version 1
and atomically migrates it to version 2. The migration rewrites adjacency keys,
creates edge-ID reverse records, rebuilds property indexes, and writes the
version marker in one synced Pebble batch. An interruption therefore leaves
either the original database or the complete migrated database, so reopening is
safe. A malformed or newer schema marker returns `ErrUnsupportedSchema`.

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

The edge ID is part of both adjacency keys, giving Pathway multigraph
semantics: edges with identical endpoints and labels remain distinct. The
reverse record lets `DeleteEdge` find and remove both adjacency records and the
edge's properties with bounded point operations.

## Property indexes

Every node property currently receives an exact-match index entry:

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

Changing a node label migrates all of that node's index entries in the same
transaction. Deleting a node removes its index entries and all incoming,
outgoing, and self-loop edge records, including edge properties.
