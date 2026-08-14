# 0006 — The bidirectional algorithm is implemented twice, on purpose

**Status:** superseded by [0007](0007-bidi-in-forme.md). The reasoning below is
kept as it was written, because what changed is its premise and not its logic.

## Context

Laying out right-to-left text needs UAX #9, the Unicode Bidirectional Algorithm.
It was already in the dependency tree: `github.com/mgilbir/forme` implements it,
in full, checked against Unicode's own conformance suites, because *shaping* a
string needs to know which way each run of it is drawn. The Makefile said so.

So implementing `direction` and `unicode-bidi` in the layout engine looked like
plumbing to an existing implementation, and it is not. The algorithm forme has is
the right one for the job forme does and cannot do the job the layout engine
needs:

- **It is unexported.** `shape.ShapeRuns` and `Face.ShapeGlyphs` apply it
  internally and hand back glyphs; nothing in forme's API returns an embedding
  level, and a level per character is the whole output the layout engine needs.
- **It runs per string, with no base direction.** Rules P2 and P3 pick the
  direction from the first strong character. `direction: ltr` and
  `direction: rtl` set the paragraph embedding level *instead* of that, and
  `unicode-bidi: plaintext` asks for it back — three answers where forme's API
  offers one.
- **Its paragraph is a string; CSS's is a subtree.** A bidi paragraph in a
  document is a block container's whole inline content, spanning every `<span>`,
  `<em>` and `<img>` inside it, split at forced line breaks. Handing forme one
  text node at a time would resolve each in isolation, which is precisely the
  bug: the direction of a word depends on its neighbours, and its neighbours are
  in other boxes.
- **Reordering happens per line, after breaking.** Rules L1 and L2 need the line,
  and forme has never seen one — a shaper is asked for a run, not for a page.
- **`unicode-bidi` has no equivalent.** The property is defined in terms of the
  explicit formatting codes, which have to be *inserted* at inline box
  boundaries as the tree is flattened. That is a layout operation on a box tree.

Two alternatives were weighed. Adding `golang.org/x/text` was rejected: it is not
a dependency today, its `bidi` package exposes runs for a whole paragraph and not
levels per character, and there is no per-line API — so the same two things
missing above would still be missing, at the cost of a new module. Exporting the
algorithm out of forme was rejected too: the API it would have to grow is a base
direction, per-character levels, per-line L1 and L2, and mirroring — which is
the whole of UAX #9 as a public surface, in a module whose subject is fonts. A
shaping library would then carry a layout engine's API for one consumer.

## Decision

`internal/bidi` implements UAX #9 for layout: `Resolve` over a paragraph with a
base direction, `LineLevels` for rule L1 over one line, and `Reorder` for rule
L2. `cmd/genbidi` generates its `Bidi_Class` and bracket tables from the Unicode
Character Database, and the generated table is committed while the input is not —
the arrangement `cmd/genhtmlentities` and `cmd/gencolors` already use.

Rule L4, the mirrored bracket, stays on forme's side of the seam and its table is
not generated here: the glyph can only be chosen after the run has been reversed,
which is the shaper's work.

forme keeps its own copy and keeps applying it to the strings it shapes. The
seam between them is one sentence: **the layout engine decides where each run
goes on the line; the shaper decides the order of the glyphs inside one.** A
right-to-left run is handed to the shaper with an explicit override in front of
it, so that the two agree about a run — a lone bracket between two Hebrew words —
that has nothing in it to say which way it goes.

## Consequences

- Two implementations of one algorithm, which is a real cost and the reason this
  record exists. It is bounded by both being checked against the same external
  oracle: Unicode's `BidiTest.txt` and `BidiCharacterTest.txt`, run in full on
  each side by that side's own tests. Neither is checked against the other, and
  neither should be — that would make one a restatement of the other, which is
  the failure ADR 0003 records this repository learning twice.
- `internal/`, so it is not API. If a caller ever wants the algorithm, that is a
  decision to take then, on its merits.
- pdf0 now fetches part of the Unicode Character Database (`make bidi-tables`)
  and Unicode's conformance suite (`make bidi-tests`). Both are gitignored and
  both are optional: the tests skip when the suite is absent, and the generated
  table is committed so a checkout builds with no network.
- The dependency count did not change. That was a consideration and not the
  deciding one — a dependency that fitted would have been taken.
