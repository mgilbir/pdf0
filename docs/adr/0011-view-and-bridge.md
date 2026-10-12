# 0011 — Subsystems read a `core.View`; `Document` stays on top

**Status:** accepted, in force.

## Context

When pdf0 split from one flat package into a core and a package per subsystem,
the validators, the signature code and the image codecs all needed the object
graph, the trailer, the version, the resolved limits and the cancellation
signal. Handing them `*Document` would have put the public facade at the bottom
of the import graph: every subsystem would import the root package that imports
every subsystem.

The first answer (6d88625, 2026-08-01) was `core.View`, a value carrying exactly
what the subsystems read. It left a second problem: a function that takes a
`core.View` cannot usefully be exported from a public package, because no
caller outside the module can name the type. About thirty were, and pkg.go.dev
listed them as API nobody could call (audit 2026-09-22 C163).

## Decision

`Document` is the top-level facade and is declared in the root package.
Everything below the API boundary reads a `core.View`, and `Document.view()` is
where the boundary is. The entry points of `pdfa`, `pdfua`, `pdfx`, `pdfvt`,
`pdfr`, `dpart`, `facturx`, `sign` and `images` that take a view are
unexported; each package installs them in `internal/bridge` from an `init`
function, and the root package resolves them into typed variables when it is
initialised (784e861, 2026-09-23).

## Consequences

- No public package imports the root, and no exported name mentions an internal
  type. `TestPublicAPIMentionsNoInternalType` fails on one that does, and
  `TestBridgeEntriesInstalledAndResolved` on an entry left unresolved.
- The bridge entries are typed `any` inside the bridge, because their
  signatures carry the public packages' types and the bridge cannot import
  them. A missing install or a mismatched signature therefore panics when the
  consumer is initialised — at the start of every binary and test that links
  it — rather than at a later call.
- A view carries a per-operation run, so the memo tables one validation builds
  are not seen by another running concurrently on the same document.
- What a rule needs from the root package (the reader, for embedded files) is
  installed on the run by the caller, never shared in a package variable;
  [architecture.md](../architecture.md#handing-work-back-across-the-boundary)
  has the detail.
