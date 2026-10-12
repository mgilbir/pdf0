# 0010 — Cancellation is a `…Context` variant, not an option

**Status:** accepted, in force.

## Context

pdf0 had no way to stop work in progress. Its own `internal/cmd/corpusprobe`
gave up on a slow file with `select` and `time.After`, which bounds the wait and
not the work: the goroutine kept a core and its memory until it finished by
itself. Adding cancellation (fbbe94d, 2026-07-28) came after the functional
options of [0009](0009-functional-options.md), so `WithContext(ctx)` was
available and would have been the smaller change.

## Decision

A context is an explicit first parameter of a variant of the entry point —
`ReadContext`, `WriteContext`, `ValidatePDFAContext`, `ExtractTextContext` and
the rest — never an `Option`. The line for which entry points get one is cost
proportional to the document's content; and an entry point gets a variant only
if it has somewhere honest to report the cancellation: a finding under `limit`
for a validator, a returned error for `Read` and `Write`, a partial result and
an error for the extractors.

## Consequences

- An option is stored: it resolves onto the `Document` and governs every later
  call. A context captured at `Read` would still govern a validation minutes
  later — the case Go's documentation means by "do not store a Context in a
  struct". A variant scopes the context to the one call.
- Whether a call can be cancelled is visible where the call is written, not in
  an argument to a `Read` twenty lines earlier.
- Adding the variants changed no existing signature. The cost is a second name
  per entry point; [untrusted-input.md](../untrusted-input.md#bounding-time-the-context-variants) lists
  which have one, and why the rest do not.
