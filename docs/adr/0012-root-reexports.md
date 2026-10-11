# 0012 — The root package re-exports the object model and nothing else

**Status:** accepted, in force.

## Context

After the package split the root package gave second names to types and
functions of other packages: the subpackage findings and levels, then a
handful of builder options, the syntax constructors, `object.Equal` and three
forwarding functions. Each type had two spellings, the docs used both, and
`object_api.go` and the README each claimed a re-export policy the code did not
follow (audit 2026-09-22 C160).

The object model cannot simply move up instead. It sits at the bottom of the
module — `syntax`, `internal/core`, every validator and the root all read it —
so it can depend on none of them. But a caller cannot build or inspect a PDF
without writing `Dictionary`, `Name` and `Integer`: `Document.Trailer` is an
`object.Dictionary`.

## Decision

The root package declares the entry points over a `Document` and what they
own: their options, results and errors. It gives a second name to exactly one
group, the object model's types, aliased in `object_api.go` (`pdf0.Dictionary`
is `object.Dictionary`). Every other name lives only in the package that
declares it — `pdfa.Violation`, `pdfa.PDFA2b`, `syntax.NewParser`,
`object.Equal`. An error declared in an internal package and given its only
public name in the root (`ErrWrongPassword`) is ownership, not a second name.
4bce899 (2026-08-02) dropped the subpackage aliases; 2dfb820 (2026-09-23)
removed the remaining second names and added the lint.

## Consequences

- One spelling per name, and godoc links each alias to the real declaration,
  which is why `object` is a regular package rather than internal.
- `TestRootReexportsOnlyTheObjectModel` (`internal/lint`) fails on a new alias
  of another public package's type, a variable initialised to one, or a
  function that only forwards its parameters to one.
- A caller of a validator imports its package for the finding and level types.
  That is a second import for most programs; it was judged cheaper than a
  second name for every type.
