# Pathway Storage Format

Pathway stores graph records in Pebble using byte-ordered key prefixes. This
document describes Pathway schema version 4. Pathway has not yet made a stable
format compatibility promise; this document records the current development
format and is not a replacement for the public Go API.

All integer length fields are unsigned big-endian values. UUID fields contain
the UUID's 16 raw bytes. Labels and property names used in node indexes have a
maximum encoded length of 65,535 bytes. Labels are intended to be UTF-8, but
schema v4 does not validate their encoding.

## Schema marker and compatibility

The key `0x00 || "pathway/schema-version"` stores a four-byte, big-endian
Pathway schema version. The current value is `4`.

Schema v4 replaces the development property and index value formats directly.
Unversioned stores and stores marked with earlier or later schema versions are
rejected with `ErrUnsupportedSchema` and must be recreated. An empty database
receives the version marker with a synced write before index definitions are
reconciled.

## Record prefixes

| Prefix | Record | Key suffix | Value |
|---:|---|---|---|
| `0x01` | Node | `node-ID` | `label-length:u16 \| label` |
| `0x02` | Outgoing adjacency | `source-ID \| label-length:u16 \| label \| target-ID \| edge-ID` | `edge-ID` |
| `0x03` | Incoming adjacency | `target-ID \| label-length:u16 \| label \| source-ID \| edge-ID` | `edge-ID` |
| `0x04` | Properties | `entity-ID` | Typed Protobuf property map; see below |
| `0x05` | Node property index | See below | Empty |
| `0x06` | Edge-ID reverse index | `edge-ID` | `source-ID \| target-ID \| label-length:u16 \| label` |
| `0x07` | Property-index definition | `label-length:u16 \| label \| property-length:u16 \| property` | Empty |

The edge ID is part of both adjacency keys, giving Pathway multigraph
semantics: edges with identical endpoints and labels remain distinct. The
reverse record lets `DeleteEdge` find and remove both adjacency records and the
edge's properties with bounded point operations.

Adjacency keys are ordered by anchor UUID, encoded label, other endpoint UUID,
then edge UUID. Because the encoded label begins with its `u16` byte length,
label order is byte length first and label bytes second. An exact label prefix
therefore forms one contiguous Pebble range; multi-label reads consume those
disjoint ranges in encoded-prefix order.

## Property values

A property record is a Protobuf `Properties` message containing
`map<string, PropertyValue>`. Each `PropertyValue` has exactly one `oneof`
variant:

| Field | Protobuf representation | Go value returned by `GetProperties` |
|---:|---|---|
| `1` | Null enum | `nil` |
| `2` | `bool` | `bool` |
| `3` | `sint64` | `int64` |
| `4` | `uint64` | `uint64` |
| `5` | `double` | `float64` |
| `6` | `string` | `string` |
| `7` | `bytes` | owned `[]byte` |
| `8` | repeated typed values | `[]any` |
| `9` | map of typed values | `map[string]any` |

All signed Go integer inputs are canonicalized to `int64`, all unsigned inputs
to `uint64`, and `float32` to `float64`. Integer conversion never passes through
`float64`, so the full signed and unsigned 64-bit ranges remain exact. Bytes
remain bytes rather than becoming base64 strings. Lists and maps recursively
use the same variants, with nesting limited to 100 levels. A missing `oneof`
variant or malformed Protobuf data is an error.

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
| `2` | Signed integer, exact 8-byte representation |
| `3` | Unsigned integer, exact 8-byte representation |
| `4` | Float, canonicalized to an IEEE-754 `float64` bit pattern |
| `5` | String |
| `6` | Bytes |
| `7` | JSON list |
| `8` | JSON object |

Type and length fields prevent collisions such as string `"1"` versus integer
`1`, signed `1` versus unsigned `1` or floating-point `1`, and exact value
`"a"` versus `"ab"`. The current index supports equality lookups only; its
numeric representations do not define range-query ordering.

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
