# Shaping checked against HarfBuzz

`fonts/harfbuzz_test.go` shapes every line of three corpora, each with its own
font, and compares the result against what HarfBuzz answered for it.

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

Adding the Arabic font found four more in the first run, with only 347 of 1188
cases agreeing:

- the positional forms were substituted *before* `ccmp`, and a real Arabic font
  states them over the skeletons `ccmp` produces rather than over the letters —
  so every letter was set in its isolated shape;
- and the form a letter had been given was not carried onto the pieces `ccmp`
  split it into, so it was lost even once the order was right;
- pair positioning read only the advance from a ValueRecord, and a right-to-left
  font states a kern as a placement *and* an advance;
- U+0640 TATWEEL, the stroke that stretches a word, was treated as having no
  positional form of its own.

The Khmer font agreed on all 2441 cases at the first run.

## Fonts

| font | what it covers | why |
| --- | --- | --- |
| `fonts/notosans/NotoSans-Variable.ttf` | Latin, Greek, Cyrillic, Devanagari | the bundled face, embedded in this module |
| `fonts/NotoSansArabic.ttf` | Arabic | cursive joining, which nothing else here exercises |
| `fonts/NotoSansKhmer.ttf` | Khmer | a syllable model that draws characters out of order |
| `fonts/NotoSansJavanese.ttf` | Javanese | the Universal Shaping Engine |
| `fonts/NotoSansBalinese.ttf` | Balinese | a second script for that engine, so one cannot be overfitted to |
| `fonts/NotoSerifTibetan.ttf` | Tibetan | a *large* font: 1190 lookups, one of them 738 subtables, and twenty mark glyph sets |

The five extra fonts are Google's Noto builds under the SIL Open Font License
1.1, the same licence and publisher as the bundled face, with their copyright
notices beside them as that licence requires. Neither declares a Reserved Font
Name. They are test data: nothing this module ships embeds them.

## Why the answers are checked in

Regenerating them needs Python, `uharfbuzz` and the font. Running the comparison
needs a Go toolchain. This has to run on every change to the shaper, and an
oracle that needs the right Python on the machine is one that quietly stops
running.

The three expectation files come to about 210 KB together, and the two extra
fonts to 1.2 MB — against the 2 MB the bundled face already costs.

## Files

| file | what it is |
| --- | --- |
| `corpus.py`, `corpus_arabic.py`, `corpus_khmer.py` | generate the three corpora |
| `corpus.txt`, `arabic.txt`, `khmer.txt` | the strings, one per line |
| `shape.py` | shapes one corpus with one font and writes its expectations |
| `expected.txt`, `arabic.expected.txt`, `khmer.expected.txt` | glyph, advance and offset for each, in font units |

Each corpus is weighted towards the places shaping decides something rather than
towards realistic prose. Prose exercises one path many times; a grid exercises
many paths once.

- **Latin** — the letter pairs that kern, every base with every common mark,
  Greek and Cyrillic pairwise, the Devanagari conjunct and vowel grids,
  ligatures formed across a skipped mark, and every category of character that
  nothing is drawn for.
- **Arabic** — every letter in each of its four positions, every ordered pair,
  the letters that join only to the right, lam-alef in all four alef forms, the
  vowels and the tanween and the shadda, and hamza written over and under a
  carrier beside a vowel, which is where the mark ordering of UTR #53 bites.
- **Khmer** — every consonant under every other, two levels of subscript,
  subscript Ro, the pre-base and split vowels, a syllable with nothing to hang
  its marks off, and the join controls.
- **Javanese** — every aksara with every sandhangan, the pangkon grid of every
  consonant stacked under every other, and stacked pairs carrying a vowel too.
- **Balinese** — the same shape of grid, and the split vowel signs U+1B40 and
  U+1B41, each written as one character and drawn as two marks on opposite sides.
- **Tibetan** — every consonant with every vowel and every subjoined form. It is
  here for its size rather than its script: it found a lookup list truncated at
  512 against its 1190, one lookup's subtables truncated at 256 against its 738,
  and the mark glyph sets that were not read at all. The first two were silent,
  because a lookup is named by index and cutting the list breaks every reference
  past the cut.

## Why two scripts for one engine

The Universal Shaping Engine claims some seventy scripts. It was written against
Javanese and reached all 894 of its cases — at which point Balinese, its close
relative, was wrong in 66 of 764. One script cannot tell a general model from one
overfitted to it, and the defect that found was in code five years older than the
engine: a vowel sign written as one character and drawn as two marks on opposite
sides of the letter was being taken apart and then put back together.

All five corpora must now agree exactly. The ratchet the test still supports was
used while the engine was being written and is documented there for the next time
something lands in pieces.

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

**Thirteen cases that differ on purpose**, all in the Latin corpus, listed with
their reasons in `deliberateDifferences` in `fonts/harfbuzz_test.go`. All
thirteen are the same thing: a character nothing is drawn for, written between a
consonant and its virama. This package removes it before shaping, so the conjunct forms; HarfBuzz
keeps it until after, so the syllable breaks and the orphaned virama gets a
dotted circle. The list is checked in both directions — an entry that starts
agreeing fails, and so does one that is not in the corpus — so it cannot go
stale.
