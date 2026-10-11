# 0015 — An object stream that contradicts the xref fails the read

**Status:** accepted, in force.

## Context

An object stored in an object stream is located twice: the cross-reference
entry names the container and an index into it, and the container's own header
lists, for each index, the object number stored there. The two can disagree.

pdf0 recovers from almost every kind of damage in this machinery. A container
that is missing, is not a stream, does not decode, lists fewer objects than an
entry's index, or holds an object that does not parse is recorded as broken;
the read continues without its objects, validation reports why, and `Write`
refuses rather than emit a document missing them (audit 2026-09-22 C102 made
these recoverable; they used to fail the whole read).

## Decision

One case stays fatal: an entry whose index names a different object number in
the container's index (`loadCompressedObjects`, since 3c5934c, 2026-07-07;
`TestReadObjStmXrefIndexMismatch`). `Read` returns an error rather than choose.

## Consequences

- This is not missing data but two parts of the file disagreeing about which
  object is object N. Either choice is a guess a crafted file can steer: it
  shows one object to pdf0 and another to a reader that trusts the other part —
  a validator certifying content a viewer never displays. Refusing is the only
  answer an attacker cannot select.
- A damaged real-world file with this inconsistency is unreadable by pdf0 even
  where another reader opens it. That is accepted; the other recoveries keep
  the cost of it narrow.
- The mismatch reported is deterministic: the objects of a container are
  checked in number order, so the same file fails with the same message every
  run.
