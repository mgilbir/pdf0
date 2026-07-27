# Architecture decision records

Short records of decisions that are non-obvious from the code, keep coming up,
and would otherwise be re-litigated. Each states the context, the decision, and
what it costs — not just what was done, but why the obvious alternative was
rejected.

These are retrospective: they document decisions already made and in force.
A record is superseded, never edited to say something different.

| ADR | Decision |
|-----|----------|
| [0001](0001-corpus-as-oracle.md) | The veraPDF corpus outranks a spec reading |
| [0002](0002-formalis-extraction.md) | EN 16931 invoice rules live in a separate module |
| [0003](0003-arlington-as-parser-oracle.md) | Arlington is a parser-faithfulness oracle, not a second validator |
| [0004](0004-executed-content-model.md) | PDF/A rules apply to executed content, not present content |
| [0005](0005-parallel-slice-dictionary.md) | `Dictionary` is parallel slices with a lazy index, not a map |
