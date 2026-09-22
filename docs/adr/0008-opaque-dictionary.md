# 0008 — `Dictionary` is opaque: ordered, one entry per key, index kept by the mutators

**Status:** accepted, in force. Supersedes [0005](0005-parallel-slice-dictionary.md).

## Context

ADR 0005 kept key order by storing a dictionary as exported parallel `Keys` and
`Values` slices, and kept lookups fast with a name→slot index that `Get` built
lazily once a dictionary reached 64 keys. That put three contracts in one type,
and the 2026-09-22 audit found each of them broken:

- **A read that writes.** `Get` built and stored the index on first use. The
  validators promise that one `*Document` may be validated from several
  goroutines at once, so two goroutines reading the same large dictionary raced
  on the index — a data race `-race` reports on any parser-produced dictionary
  of 64 keys or more (C45). The concurrency test had never seen it because its
  reference file has no dictionary that large.
- **An index that could go stale.** It was invalidated only when `len(Keys)`
  changed, so reordering or renaming keys in place — the fields were exported —
  left lookups answering from old slots, and only above 64 keys (C98).
- **Duplicate keys with no owner.** The parser collapsed a duplicated key, `Set`
  updated the first, `Delete` removed only the first (so the second came back,
  C121), and `DictionaryEqual` compared entries as a multiset. Each part of the
  package had its own answer.

## Decision

`Dictionary`'s storage is unexported and reached only through methods:
`Len`, `Get`, `Lookup`, `Has`, `Set`, `Delete`, `All`/`Keys`/`Values`
(Go iterators, in insertion order), `Clone`, and the constructor
`NewDictionary(entries ...Entry)`.

- **Order** is insertion order, as before, so round-tripping is unchanged.
- **One entry per key.** `Set` on an existing key replaces its value in place;
  `NewDictionary` applies the same rule to its arguments, and so does the
  parser: a duplicated key in a file keeps its first position and its last
  value. ISO 32000-2 7.3.7 leaves duplicates undefined, and this is what common
  readers do. `Delete` therefore removes the key, and `DictionaryEqual` is a
  lookup per entry.
- **The mutators maintain the index.** A dictionary of 64 or more entries keeps
  a key→slot map that `Set` extends and `Delete` repairs. Reads only consult
  it, so reads are pure and any number of goroutines may read one dictionary
  concurrently. `Set` is O(1) amortised at every size, which is also what lets
  the parser build a crafted 100,000-key dictionary in linear time without the
  private index it used to keep and throw away.
- **Copies share, like a map.** The storage sits behind a pointer, so copying a
  `Dictionary` struct — `Stream.Dict` or `Document.Trailer` taken by value, as
  the validators' per-run shallow `Document` copy does — copies a reference to
  the same entries. A mutation through either copy is seen through both, and
  neither can corrupt the other. `Clone` gives an independent copy.

## Consequences

- The audit's three defects cannot be expressed through the API: there is no
  way to write a stale slot, no read path that stores anything, and no way to
  build a dictionary holding a key twice.
- Callers iterate with `for k, v := range d.All()` instead of indexing two
  slices, and build literals with `NewDictionary`. Positional access was not
  added: no caller needed it once iteration existed.
- Reads of a nil `*Dictionary` are defined and empty, so `ResolveDict(x).Get(k)`
  on a missing dictionary no longer panics.
- A dictionary costs one more small allocation (the storage behind the
  pointer). Parsing a large dictionary is cheaper than before, because the
  parser's own throwaway index is gone.
- A file that really does contain a duplicated key is still read as one entry,
  as it was; the second occurrence's value wins. Nothing in the object model
  records that the file had a duplicate. A validator that needs to report
  duplicates has to look at the bytes.
