# 0005 — `Dictionary` is parallel slices with a lazy index, not a map

**Status:** accepted, in force.

## Context

The obvious representation for a PDF dictionary is `map[Name]Object`. Go maps
have no defined iteration order, and pdf0's central promise is faithful
round-tripping: `Read → Write → Read → Write` must be byte-identical
(`TestWriteIsIdempotent`). A map would reorder keys on every write, so the output
would differ from the input for no reason and differ from itself between runs.

Key order also carries meaning for a reader diffing two files, and for the
byte-level PDF/A rules that inspect a serialized object.

## Decision

`Dictionary` holds parallel `Keys []Name` and `Values []Object` slices,
preserving insertion order exactly as parsed.

Because a linear scan is O(n) per lookup, `Get`/`Set` maintain a lazy
name→slot index once a dictionary reaches `dictLookupThreshold` (64) keys, and
drop it on operations that shift slots. Below the threshold the linear scan beats
allocating a map.

## Consequences

- Round-tripping is exact, and key order survives every read/write cycle.
- The index is not a micro-optimisation: a large dictionary walked in a loop — a
  `/RoleMap`, a `/Names` tree, an attacker-sized resource dictionary — is
  O(n) per lookup without it, which turns a small crafted file into a
  super-linear CPU denial of service through the validators.
- The index is owned by the `Dictionary`. Copying a `Dictionary` by value and
  then mutating both copies is unsupported, as it already was for the shared
  `Values` backing array. Use `Clone()` to copy before mutating.
- Callers get `Get`/`Set`/`Delete` rather than map syntax, and duplicate keys are
  representable where a map would silently collapse them — which is the correct
  behaviour for a parser that must not lose what a file actually contains.
