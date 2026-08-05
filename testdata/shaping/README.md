# Shaping corpus

`corpus.txt` is 12,475 strings, and it is [forme][]'s: the corpus that module
shapes and compares against HarfBuzz, glyph for glyph. It is weighted towards
the places shaping decides something — every ordered pair of Latin letters, the
combining marks that stack, the Devanagari conjunct grid, the characters
nothing is drawn for — rather than towards realistic prose, because prose
exercises one path many times and a grid exercises many paths once.

It is here for a different question. forme asks whether the shaping is right;
this module takes the shaping as given and asks whether its several ways of
putting a shaped run on a page agree with each other. `Shape` writes spans and
`Draw` places glyphs; `MeasureShaped` says what either will occupy. If they
disagree, a layout engine fills a line to one width and paints it at another,
and nothing in either call's own output shows it. Latin will not find that —
a mark with a zero advance, a reordered matra and a ligature across a nukta
will, and the corpus has thousands of each.

The generator lives with the oracle, in forme. To refresh this file, run
forme's `testdata/harfbuzz/corpus.py` and copy the result over.

[forme]: https://github.com/mgilbir/forme
