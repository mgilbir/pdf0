# 0007 — The bidirectional algorithm moves to forme, and is exported

**Status:** accepted, in force. Supersedes [0006](0006-bidi-in-two-places.md).

## Context

ADR 0006 decided that UAX #9 should be implemented twice: once inside forme, to
decide which way a run of glyphs is drawn, and once here, to lay a paragraph out
across boxes with a base direction and the CSS `unicode-bidi` controls. It
weighed exporting forme's copy and rejected it, in one sentence:

> the API it would have to grow is a base direction, per-character levels,
> per-line L1 and L2, and mirroring — which is the whole of UAX #9 as a public
> surface, in a module whose subject is fonts. A shaping library would then
> carry a layout engine's API for one consumer.

Both halves of that have since stopped being true.

**The premise is gone.** forme's subject is no longer fonts. The HTML and CSS
engine is moving into it — `css`, `html`, `style` and the layout engine itself —
so the layout caller is not a foreign consumer reaching into a font library. It
is the module's other half.

**The cost was smaller than the estimate.** The API it actually had to grow is
`Resolve`, `Paragraph.Level`, `Levels` and `LineLevels`: about a hundred lines
over the algorithm that was already there, plus exporting names that already
existed. "The whole of UAX #9 as a public surface" describes the *shape* of the
API correctly and its *size* not at all. Rule L4's mirroring did not have to be
exported at all; it stays where 0006 put it, on the shaper's side, and for the
reason 0006 gave.

The cost that was named as real also came due. Two implementations meant two
generated `Bidi_Class` tables, two `cmd/genbidi` reading the same four files
from the Unicode Character Database, two conformance harnesses over the same
770,241 and 91,707 cases, and two sets of Makefile targets to fetch the same
data. Every Unicode release would have been two migrations.

## Decision

The algorithm lives in `github.com/mgilbir/forme/bidi`, exported. `internal/bidi`
and `cmd/genbidi` are deleted here, along with the `bidi-tests` and `bidi-tables`
Makefile targets; forme has its own.

The package serves both callers rather than one:

- `LogicalRuns`, `VisualRuns`, `VisualOrder`, `RunCharacters` — what the shaper
  asks, unchanged, so `shape` keeps every call site it had.
- `Resolve`, `Paragraph.Level`, `Levels`, `LineLevels` — what a layout engine
  asks. `Resolve` keeps the paragraph instead of answering about a whole string,
  because rule L1 depends on where a *line* ends and the lines do not exist yet
  when the paragraph is resolved.

`shape` refers to the class constants through a block of aliases rather than
several hundred rewritten call sites. Shaping reads a character's bidi class in
the joining rules, the mark ordering, and the Indic and Universal engines; that
code's correctness rests on a comparison against HarfBuzz, and a large mechanical
diff through it buys nothing.

## Consequences

- One implementation, one generated table, one generator, one conformance
  harness. Unicode's suites pass unchanged against the moved package: 770,241
  and 91,707 cases, zero failures.
- §5.2 — a character rule X9 removed takes the level of the one before it — is
  applied for the two callers that must *place* such a character and not in the
  shared core. Unicode's conformance files state no level for a removed
  character and the harness checks that none is offered, so doing it in the core
  fails 547,281 cases. That is not a subtlety this would have found by reading;
  the suite found it.
- ADR 0006's seam still holds and is still the right sentence: the layout engine
  decides where each run goes on the line, the shaper decides the order of the
  glyphs inside one. Only the packaging changed.
- The lesson is about `internal/`, and it is worth stating plainly. 0006's
  arguments were all about forme's *API surface* rather than about the algorithm,
  and each was answerable by exporting something. Code that another module would
  plausibly want should be exported; leaving it unexported is what makes the
  second implementation look like the cheaper option.
