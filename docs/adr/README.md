# Architecture decision records

Short records of decisions that are non-obvious from the code, keep coming up,
and would otherwise be re-litigated. Each states the context, the decision, and
what it costs — not just what was done, but why the obvious alternative was
rejected.

These are retrospective: they document decisions already made and in force.

A record's Context and Decision are never rewritten. When a fact behind a
decision changes but the decision stands, the record gains a dated note at the
end of its Consequences — "**Amended YYYY-MM-DD:**" and what changed, with the
commit or version — and the original text stays as it was. When the decision
itself changes, a new record supersedes the old one, which keeps its text and
says "superseded" in its status line. (0002 was rewritten in place once, before
this rule was written down; its amendment note says what changed.)

| ADR | Decision |
|-----|----------|
| [0001](0001-corpus-as-oracle.md) | The veraPDF corpus outranks a spec reading |
| [0002](0002-formalis-extraction.md) | EN 16931 invoice rules live in a separate module |
| [0003](0003-arlington-as-parser-oracle.md) | Arlington is a parser-faithfulness oracle, not a second validator |
| [0004](0004-executed-content-model.md) | PDF/A rules apply to executed content, not present content |
| [0005](0005-parallel-slice-dictionary.md) | `Dictionary` is parallel slices with a lazy index, not a map — *superseded by 0008* |
| [0006](0006-bidi-in-two-places.md) | The bidirectional algorithm is implemented twice, on purpose — *superseded by 0007* |
| [0007](0007-bidi-in-forme.md) | The bidirectional algorithm moves to forme, and is exported |
| [0008](0008-opaque-dictionary.md) | `Dictionary` is opaque: ordered, one entry per key, index kept by the mutators |
| [0009](0009-functional-options.md) | Resource limits are functional options, not a `Limits` struct, a package variable or a context value |
| [0010](0010-context-variants.md) | Cancellation is a `…Context` variant of an entry point, not a `WithContext` option |
| [0011](0011-view-and-bridge.md) | Subsystems read a `core.View`, reached through `internal/bridge`; `Document` stays on top |
| [0012](0012-root-reexports.md) | The root package re-exports the object model and nothing else |
| [0013](0013-reserved-rule-identifiers.md) | `limit` and `internal` name the checker, not the document, and `limit` is spelled the same in formalis |
| [0014](0014-read-never-fails-on-encrypt.md) | `Read` never fails on `/Encrypt`; a Locked document is carried through `Write` |
| [0015](0015-objstm-index-mismatch-is-fatal.md) | An object stream that disagrees with the xref about an object's number fails the read |
| [0016](0016-readcontext-returns-nil.md) | A cancelled `ReadContext` returns nil, never a partial `Document` |
| [0017](0017-shaping-and-layout-in-forme.md) | Shaping, font programs and the HTML/CSS layout engine live in forme |
| [0018](0018-retract-v0.2.0-v0.3.1.md) | v0.2.0 through v0.3.1 are retracted, not deleted |
| [0019](0019-signature-consent-on-the-document.md) | Consent to invalidate signatures is held by the `Document`, not given per call |
