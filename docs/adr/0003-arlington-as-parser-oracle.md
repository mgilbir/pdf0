# 0003 — Arlington is a parser-faithfulness oracle, not a second validator

**Status:** accepted, in force.

## Context

pdf0's round-trip tests compare pdf0's object model against itself: `Read →
Write → Read` must agree. That proves self-*consistency*, not correctness. A
parser that read a page's `/MediaBox` array as a string would round-trip
perfectly and still be wrong.

The Arlington PDF Model (`github.com/pdf-association/arlington-pdf-model`,
Apache-2.0) is a machine-readable grammar of the ISO 32000 object model — one TSV
per object type per version, giving each key's type, whether it is required,
whether it is inheritable, and what it may link to. It is ground truth from
outside pdf0.

Two earlier attempts at using it failed and are worth recording so they are not
retried: an output-guard that validated pdf0's own generated files (which are
trivial, so it tested nothing), and a "consistency guard" whose contradiction
check set each key to its own current value and therefore always passed —
tautological.

## Decision

Arlington is used as an **external oracle for parser and serializer
faithfulness**: does pdf0's object model represent what is actually in the file —
right types, keys present, structure intact? Traversal is link-driven from the
catalog so each object is checked against the right variant spec, and only
unconditional constraints are enforced (required=TRUE, literal types and
possible values). Predicate-gated rules and version-appropriateness are skipped —
version conformance is a validator's concern, not a parser's.

It is deliberately **not** a second conformance validator. pdf0's PDF/A rules and
Arlington's base grammar barely overlap; treating it as a rival validator would
guard almost nothing.

## Consequences

- The finding it produces is meaningful: a structural discrepancy on a real-world
  file, which no self-referential test can surface. Over the veraPDF conformant
  set it found zero parser bugs — that null result is the value.
- The model is not vendored. `make arlington` clones it; `ARLINGTON_MODEL` points
  the tests at it; they skip when it is absent, like every other external oracle.
- Conservatism is the price: rules gated on predicates are not enforced, so a
  clean run is evidence of faithfulness, not proof of it.
- The reach was narrower than the null result made it look. Until 2026-10-28 the
  walker read every grammar keyed `*` as an array's, so it never entered a
  resource map (fonts, images, patterns, graphics states). It walked a name or
  number tree's root as if it were one of the tree's values, read an inherited
  page attribute only when the ancestor's own grammar listed it, and judged a
  1.x file by the PDF 2.0 grammar. Three of the five findings it then accepted
  as facts about the input were its own. With all four fixed, each file is held
  to the grammar of the version it declares. One row where the model contradicts
  ISO 32000 (a form XObject's `/Resources`, which the model requires and both
  editions call optional) is excused by `arlModelDeviations`. The ratchet is now
  a list, `testdata/arlington-corpus-expected.tsv`: 40 findings in 31 files, each
  checked against the file's raw bytes, and still no parser bug among them.
