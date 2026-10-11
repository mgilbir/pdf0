# 0017 — Shaping, font programs and the layout engine live in forme

**Status:** accepted, in force.

## Context

Turning characters into positioned glyphs is not a PDF problem, and neither is
laying HTML and CSS out into boxes. Both grew here: the OpenType layout engine,
the script shapers, the font program reader and the subsetter; then the HTML
parser, the CSS syntax and cascade and the layout engine. None of it mentioned
PDF. Of the layout side's files, one depended on anything of PDF's: the one
that wrote the display list into a document, `htmlpdf/pdfout.go` today.

## Decision

They live in `github.com/mgilbir/forme`, and pdf0 depends on it:

- shaping, the font program reader and the subsetter moved on 2026-08-05
  (f0a997c), with their oracles (HarfBuzz's answers, CoreText, the
  Unicode-derived table generators);
- the bidirectional algorithm and grapheme clusters followed on 2026-08-14
  ([0007](0007-bidi-in-forme.md); c527699);
- the HTML parser, CSS and the layout engine moved the same day (e2d4d9c), with
  the Web Platform Tests and the reftest ratchet.

What stays is what PDF decides. `fonts` turns a shaped run into text-showing
operators and writes the font into the document; `htmlpdf` writes forme's laid-out
display list into pages. forme is an ordinary module requirement in `go.mod`
(since 5794212; before it, a checkout beside this one).

## Consequences

- A change to shaping or layout is made in forme, with forme's oracles, and
  arrives here as a version bump. forme has its own backlog; a pdf0 bug that is
  a shaping bug is filed there.
- pdf0's tests over shaping and layout are the ones that read a finished PDF
  back: geometry, content streams, embedded fonts, extracted text.
  `testdata/shaping/corpus.txt` stays, because `Shape`, `Draw` and
  `MeasureShaped` have to agree with each other over real text.
- forme exports what both modules need (its bidi package, its font test
  fixtures) rather than each keeping a copy; the lesson of
  [0006](0006-bidi-in-two-places.md) and 0007.
