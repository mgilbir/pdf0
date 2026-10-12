# 0009 — Resource limits are functional options

**Status:** accepted, in force.

## Context

Every guard against a hostile file — decompressed bytes, object-stream bytes,
content work, image pixels — has a default that is safe for untrusted input and
wrong for someone. Making them configurable (545ae27, 2026-07-28) needed a shape
for the knob, and three were on the table: an exported `Limits` struct, the
package-level variables four of the limits already were (unexported, so tests
could lower them), and values carried in a `context.Context`.

For a limit, zero is a meaningful value. `Limits{MaxDecodedStreamBytes: 0}`
reads equally well as "no cap" and as "the default", and the type cannot say
which; escaping that takes `*int` fields or sentinels, re-explained on every
field. A package variable is one value per process: two documents with
different limits cannot coexist, assigning one while another goroutine
validates is a data race, and a library setting it changes every other user in
the process. A context value degrades silently: a dropped or unpropagated
context reverts to the default with no compile error and no runtime signal,
and these limits are a security control.

## Decision

Limits are variadic functional options on the entry points that read a file:
`Read(r, size, opts ...Option)`, with one `With*` function per limit
(`WithMaxWork`, `WithMaxDecodedStreamBytes`, …). "Unset" is "the option was
never passed"; a value that is not positive is refused with `ErrInvalidOption`
before anything is read. The resolved limits are stored on the `Document`, so
every validator and extractor run on it inherits them. `context.Context` is not
used to carry configuration.

The full argument, with the transports weighed and the defaults measured, is
the design record [configurable-limits.md](../proposals/configurable-limits.md);
the options and their defaults are listed in
[untrusted-input.md](../untrusted-input.md#capping-what-one-document-costs).

## Consequences

- Adding a knob is additive: a new `With*` function, and no existing call site
  changes. Every caller who passed none compiled unchanged when they arrived.
- The limit travels with the document, which is the right lifetime for "what
  this document may cost" and the wrong one for "how long this call may take"
  — which is why cancellation is a separate mechanism ([0010](0010-context-variants.md)).
- A limit enforced where no `Document` exists has to be threaded by hand; the
  design record lists the few that were left internal on that cost.
