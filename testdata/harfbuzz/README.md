# Shaping checked against HarfBuzz

`fonts/harfbuzz_test.go` shapes every line of `corpus.txt` with the bundled face
and compares the result against `expected.txt`, which is what HarfBuzz answered.

## Why

Every other test of the shaper tests it against itself: a fixture built by
`internal/fonttest`, read by `fonts`, asserted by `fonts`. That catches a reader
that contradicts itself. It cannot catch a reader that is *consistently* wrong,
which is what a misread font table looks like — the same misreading writes the
fixture and the expectation.

HarfBuzz is outside. It is what browsers, terminals and typesetters shape with,
so where the two differ the page a reader sees is HarfBuzz's answer and not this
package's.

It was added after three defects had already reached the tree, each a plausible
reading of the specification that no self-consistent test would have
contradicted:

- mark attachment took anchors from whichever subtable was read last, putting
  the accents of a third of the sample in the wrong place;
- a ligature deleted the marks it stepped over;
- pair adjustments were read from `kern` alone, so every Devanagari conjunct was
  set at its nominal width — this font states Devanagari spacing under `dist`
  and declares no `kern` for it at all.

Widening the corpus afterwards immediately found a fourth: marks inside a
ligature were not attached to it at all, because GPOS type 5 was unimplemented.

## Why the answers are checked in

Regenerating them needs Python, `uharfbuzz` and the font. Running the comparison
needs a Go toolchain. This has to run on every change to the shaper, and an
oracle that needs the right Python on the machine is one that quietly stops
running.

`expected.txt` is about 110 KB, which is half of `testdata/spec_examples.json`.

## Files

| file | what it is |
| --- | --- |
| `corpus.py` | generates `corpus.txt` |
| `corpus.txt` | the strings, one per line |
| `shape.py` | shapes them with HarfBuzz and writes `expected.txt` |
| `expected.txt` | glyph, advance and offset for each, in font units |

The corpus is weighted towards the places shaping decides something — the letter
pairs that kern, the marks that attach, the Devanagari conjunct grid, ligatures
formed across a skipped mark, the characters nothing is drawn for — rather than
towards realistic prose. Prose exercises one path many times; a grid exercises
many paths once.

## Regenerating

```sh
python3 -m venv .hbenv && .hbenv/bin/pip install uharfbuzz
PYTHON=.hbenv/bin/python make hbshaping
```

Review the diff to `expected.txt` before committing. A change there is HarfBuzz
changing its mind, and is worth understanding rather than accepting.

`expected.txt` records the SHA-256 of the font it was generated against, and the
test refuses to run if the bundled font is a different one — otherwise a font
upgrade would leave it asserting yesterday's answers about today's glyph
indices, and every assertion would be about the wrong glyph.

## What is deliberately not compared

**Anything whose direction HarfBuzz would guess.** HarfBuzz performs no
bidirectional algorithm: its caller is required to run UAX #9 and hand it runs of
a single direction, and `hb_buffer_guess_segment_properties` only picks a
direction from the first character that has a script. So for `RLO a b c` HarfBuzz
answers `abc` and this package answers `cba` — and this package is right, because
an override means what it says. That is a difference in what the two are *for*.
The right oracle for it is Unicode's own `BidiTest.txt` and
`BidiCharacterTest.txt`, which `fonts/bidi_conformance_test.go` runs in full;
`corpus.py` therefore leaves the right-to-left forcing controls out.

**Thirteen cases that differ on purpose**, listed with their reasons in
`deliberateDifferences` in `fonts/harfbuzz_test.go`. All thirteen are the same
thing: a character nothing is drawn for, written between a consonant and its
virama. This package removes it before shaping, so the conjunct forms; HarfBuzz
keeps it until after, so the syllable breaks and the orphaned virama gets a
dotted circle. The list is checked in both directions — an entry that starts
agreeing fails, and so does one that is not in the corpus — so it cannot go
stale.
