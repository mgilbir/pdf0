# 0016 — A cancelled `ReadContext` returns nil, never a partial `Document`

**Status:** accepted, in force.

## Context

A read cancelled part-way ([0010](0010-context-variants.md)) has loaded some of
a file's objects and not others. The extractors return their partial result with
the error, because discarding work is as dishonest as presenting it as complete;
the question was whether `Read` should do the same.

## Decision

A cancelled `ReadContext` or `ReadWithPasswordContext` returns a nil `Document`
and an error wrapping `ctx.Err()`, so `errors.Is` tells a cancellation from a
deadline (fbbe94d, 2026-07-28; `TestReadContextCancelled`).

## Consequences

- A document missing an arbitrary subset of its objects is indistinguishable
  from one whose file genuinely lacks them. Every validator would report the
  absence as a conformance failure, and `Write` would emit a smaller file as if
  it were the original. A nil result cannot be mistaken for either.
- A caller that cancels a read has nothing to inspect, and must read again to
  get the document. That is the price of the guarantee.
- The rule is the same as for a read that panics and is recovered: the
  `Document` is discarded and the error returned.
