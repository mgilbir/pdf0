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
| `corpus.py`, `corpus_arabic.py`, `corpus_khmer.py`, `corpus_javanese.py`, `corpus_balinese.py`, `corpus_tibetan.py` | generate the six corpora |
| `corpus.txt`, `arabic.txt`, `khmer.txt`, `javanese.txt`, `balinese.txt`, `tibetan.txt` | the strings, one per line |
| `shape.py` | shapes one corpus with one font and writes its expectations |
| `*.expected.txt` | glyph, advance and offset for each, in font units |
| `difffuzz.py` | generates text instead of listing it — see below |

Each corpus is weighted towards the places shaping decides something rather than
towards realistic prose. Prose exercises one path many times; a grid exercises
many paths once.

- **Latin** — the letter pairs that kern, every base with every common mark,
  three marks on one letter over the whole combining-diacritical block, Greek
  and Cyrillic pairwise over the *whole* alphabet in both cases, the Devanagari
  conjunct and vowel grids, ligatures formed across a skipped mark, and every
  category of character that nothing is drawn for.
- **Arabic** — every letter in each of its four positions, every ordered pair,
  the letters that join only to the right, lam-alef in all four alef forms, the
  vowels and the tanween and the shadda on every letter of the block rather than
  the twenty-eight of the alphabet — the added letters are the ones `ccmp` splits
  into a skeleton and marks of several kinds — and hamza written over and under a
  carrier beside a vowel, which is where the mark ordering of UTR #53 bites.
- **Khmer** — every consonant under every other, two levels of subscript,
  subscript Ro, the pre-base and split vowels, a syllable with nothing to hang
  its marks off, and the join controls.
- **Javanese** — every aksara with every sandhangan, the pangkon grid of every
  consonant stacked under every other, stacked pairs carrying a vowel too, and
  every *pair* of sandhangan on one letter. The last is there because this font
  carries a placeholder mark through its rules and takes it off again with a
  substitution of no glyphs at all, and the rule that puts the placeholder there
  needs a second sign to fire.
- **Balinese** — the same shape of grid, and the split vowel signs U+1B40 and
  U+1B41, each written as one character and drawn as two marks on opposite sides.
- **Tibetan** — every consonant with every vowel, and the first sixteen with
  every subjoined form. It is
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
their reasons in `deliberateDifferences` in `fonts/harfbuzz_test.go`. Tibetan
had two more and no longer does: they were recorded as a difference of opinion
about how to decorate a letter that is not there, and were really a reserved
code point being given a category that broke the cluster. All
thirteen are the same thing: a character nothing is drawn for, written between a
consonant and its virama. This package removes it before shaping, so the conjunct forms; HarfBuzz
keeps it until after, so the syllable breaks and the orphaned virama gets a
dotted circle. The list is checked in both directions — an entry that starts
agreeing fails, and so does one that is not in the corpus — so it cannot go
stale.


## Differential fuzzing

`difffuzz.py` generates text instead of listing it, shapes it with both, and
reports what disagrees. The corpora above are fixed lists chosen by hand; they
are good at what they were written for and blind to everything nobody thought
of, which is most of the state space.

```sh
PYTHON=.hbenv/bin/python make hbfuzz          # a minute
python3 testdata/harfbuzz/difffuzz.py 600     # ten
```

It does not mutate the fonts. Random bytes produce a font neither side can read,
and structured mutation produces one whose *correct* shaping nobody knows —
HarfBuzz's answer would be as arbitrary as this package's. Malformed fonts are
the Go fuzzer's job (`fonts/panic_test.go`), which asks a different question: not
"is this right" but "does this survive".

### What it took to make the output mean anything

Five, each added because without it the report was mostly one false positive:

- **A letter at the front.** A string of nothing but marks has no strong
  direction, so the two resolve one differently — this package by running
  UAX #9, HarfBuzz by guessing. 12,000 strings gave 241 reversals and no defect.
- **One bidirectional class.** The Arabic-Indic digits are class AN, so a number
  inside right-to-left text reads left to right; this package orders it that way
  and HarfBuzz reverses it with everything else. Another 1,600 reversals.
- **A minimiser that keeps the first character.** Shrinking a real difference
  past its anchoring letter turns it into one of the above, so the tool would
  "minimise" a genuine defect into a false one and report that.
- **One script.** The bundled face was listed with all four of its scripts'
  ranges at once, so it drew strings mixing Latin with Devanagari. HarfBuzz
  performs no script itemization either — its caller must hand it a run of one
  script, exactly as it must hand it one direction — so such a string is not a
  comparison of shaping. 853 of 854 differences it reported for that font were
  this. It is now listed once per script.
- **Asking again after minimising.** The tool decided whether a difference was
  already understood *before* shrinking it. Dropping a character can turn
  something nobody has seen into something written down, so hundreds of
  "new" differences were a recorded gap all along.

And the list of characters nothing is drawn for is now derived from Unicode's
definition rather than written out by hand. The hand-written one was missing the
two Khmer inherent-vowel signs, so 175 differences that are the deliberate
decision above were reported as though they were new.

### What it has found

- The Arabic mark reordering of UTR #53 was applied to every Arabic run rather
  than to the runs the report is about. The corpus had only ever written hamza,
  which is one of the fourteen characters it names, so every case passed. Two
  minutes of fuzzing found it.
- The same reordering, once narrowed, still had both of the report's two
  conditions the other way about: it moved a modifier mark that did not *begin*
  its class's run, and it put the marks written above the letter inside the ones
  written below. No corpus writes two such marks on one letter. What settled it
  was the report's own text, checked by enumerating base + two marks over the
  classes it names and reading the order back out of HarfBuzz's glyphs.
- That the universal engine inserts no dotted circle for a cluster it cannot
  parse, which the Indic and Khmer shapers here do — and, with it, that a
  character the engine calls Other is not a gap between clusters but the start of
  one. Both needed the engine's cluster *grammar* rather than a scan, and both
  went when it arrived.
- That a reserved code point was being given a category that broke the cluster
  after it, which cost 34 Tibetan strings a spurious dotted circle.
- That a multiple substitution of *no glyphs at all* — a deletion, which the
  format's own text forbids and Noto Sans Javanese states anyway — was being
  ignored, leaving a placeholder mark on the page beside every letter that went
  through the rule.

Then, once those were fixed and the tool stopped mis-reporting (below), four
more in positioning:

- A lookup's pair-kerning subtables are alternatives in which the *first* match
  wins. They were merged into one table keyed by glyph pair, so the last one
  won: Noto Sans states be+TE as -20 in an explicit pair list and -40 in a class
  table of the same lookup, and this package applied -40.
- Kerning lookups were merged into one pass under one merged set of flags, so a
  lookup that ignores marks silenced every other lookup for marks, and lookups
  could not accumulate.
- A mark's advance was cancelled while it was being attached — after the
  positioning rules had run, so a rule that gave a mark an advance on purpose
  had it taken away again. When this happens is a decision each script's model
  takes for itself: never for Indic and Khmer, before the rules for the
  universal engine and Myanmar, after them for everything else.
- Mark attachment ran one walk backwards, trying mark-to-mark at every mark it
  passed and mark-to-base at the first thing that was not one. They are
  different questions. Mark-to-base looks past any marks in the way; mark-to-mark
  looks past exactly the glyphs *its own lookup* skips and attaches to what it
  lands on, never past it — and the lookups' flags were being dropped at load,
  `mergedFlags` going as far as to strip the bit that says a mark glyph set is
  in use. So a Latin letter with three marks had the third climb over a middle
  one nothing stacks on, and an Arabic sukun did not stack on the dots above its
  letter, because the ring below was in the way and its lookup's mark glyph set
  says to step over it.

  Stopping at the glyph immediately before is *not* the correction, which is
  worth recording because it was tried: it fixes Latin, Greek and Cyrillic and
  breaks Arabic. Both directions are the same missing thing, and the flags are
  it.

The mark-stacking class is what is left, and it is larger than it looked. With
nothing masked the fuzzer reports 334 differences over 644,400 strings: khmer
217, tibetan 85, devanagari 29, and one each in arabic, balinese and javanese.
The earlier figure of thirteen was an artifact — the tool skipped any string with
an ignorable inside it, which hid differences that had nothing to do with
ignorables.

They are one shape: several marks on one base, where this package stacks a mark
on the one before it and HarfBuzz leaves it on the base. The smallest case is

	U+0905 U+0945 U+0955   this package  3917 at y 247
	                       HarfBuzz      3917 at y 0, beside 3916

## What is left

Over 421,200 generated strings the fuzzer reports 40 differences, in three
classes. The figure of one quoted earlier was a 2,000-string sample per script
and not the whole of it.

**Five units of x, Tibetan.** The one this section used to be about, and it is
not a defect: CoreText was asked and places the mark where this package places
it. Absolute positions are 427 for this package, 427 for CoreText and 422 for
HarfBuzz — see testdata/coretext. Pinned in the corpus and listed in
`deliberateDifferences`; the fuzzer names only the exact string, so any other
Tibetan case it reports is a different question wearing the same shape.

**An invisible character the font gave a width, Arabic.** `U+063D U+061C U+0655`
puts the mark at 250 against HarfBuzz's 850, and is not a defect either:
CoreText was asked and closes the gap as this package does. Noto Sans Arabic
gives U+061C a glyph 600 units wide; HarfBuzz carries it through positioning and
deletes it at the end, keeping the hole. Pinned in the corpus and listed in
`deliberateDifferences`.

**Two pre-base vowels, Devanagari.** `U+091B U+094E U+093F` comes out as
different glyphs in a different order — 3975, 4032 against HarfBuzz's 3935,
3975. Both U+094E and U+093F are written after the consonant and drawn before
it, and this is substitution as well as reordering, so it is not the same
question as the two above.
## How to look at either

The per-lookup trace is the tool, and it works from Python — the buffer is not
passed to the callback, but the buffer object is in scope and can be read from
inside it:

	log = []
	def cb(msg):
	    log.append((msg, [(i.codepoint, p.x_offset, p.y_offset)
	                      for i, p in zip(b.glyph_infos, b.glyph_positions)]))
	    return True
	b.set_message_func(cb)

Print only the lines where the positions changed and it says which lookup moved
what. That is how the last one was found, after four readings of the tables had
each produced a plausible wrong answer.

## What the trace says, and where it points

uharfbuzz's message callback reports every lookup, which is more than it first
appeared to — the earlier note here said it reported only the table boundaries,
and that was a filter reading `"GPOS" in message` throwing away lines that read
`start lookup 19 feature 'blwm'`.

	python3 - <<'EOF'
	import uharfbuzz as hb
	...
	b.set_message_func(lambda *a: (msgs.append(a), True)[1])
	EOF

For the Tibetan case the GPOS lookups that run, in order, are 14-17 (abvm),
18-21 (blwm), 22-23 (dist) and 24-25 (mkmk). Lookups 18 and 19 both apply. So do
**21, 23 and 24**, and *none of them has been examined*. Lookup 21 is a second
MarkMarkPos with its own filtering set; 23 is a pair adjustment; 24 is the mkmk
one already known not to cover this mark.

The arithmetic says HarfBuzz's answer is the mark-to-base placement — mark anchor
(-235, 0) against a base anchor, not either of the two mark-to-mark targets,
whose anchors give -439 and +33 against the answer of -102. Something after
lookup 19 puts it back on the base, and lookup 21 is the candidate nobody has
looked at.

## What to try next

Dump the buffer between lookups rather than reasoning from the final positions.
The message callback's first argument is not the buffer — that was tried and
raised — so the shape of the callback arguments has to be established first, or
`hb-shape --verbose` used instead, which prints the same trace with the glyph
positions beside it.

## What to try next

Not another reading of the tables — three have failed. Get HarfBuzz to say which
lookup it applied:

- `hb-shape --trace` from a HarfBuzz build with `HB_DEBUG_APPLY`, which names
  every lookup as it is tried. uharfbuzz's message callback is not enough: it
  reports only `start table GPOS` and `end table GPOS`.
- Failing that, bisect the font: strip lookup 11 from a copy and see whether
  HarfBuzz's answer changes. If it does not, the lookup was never reached and
  the question becomes what reaches it.
