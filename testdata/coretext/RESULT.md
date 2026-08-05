# What CoreText answered

Run on macOS against the same two font files, 2026-08-04.

Both controls came out right, which is what makes the rest worth reading: the
syllable with nothing written inside it shaped to the single conjunct glyph and
said nothing about a dotted circle, and the same syllable with its consonant
taken away shaped to three glyphs and said `DOTTED`.

Then every remaining line said `DOTTED`.

| | pdf0 | HarfBuzz | CoreText |
| --- | --- | --- | --- |
| Devanagari, ignorable inside the syllable | 1 glyph | 4 glyphs, dotted | **4 glyphs, dotted** |
| Khmer, ignorable inside the syllable | 1 glyph | 3 glyphs, dotted | **3 glyphs, dotted** |

Twelve of the thirteen Devanagari strings are usable — U+FEFF reported
`SUBSTITUTED-FONT`, so CoreText answered out of some other font and that line
says nothing — and all twenty-four Khmer strings are.

## What it settles

CoreText keeps a default-ignorable through cluster analysis and draws nothing
for it. For several of them it emits a glyph of its own with a zero advance —
glyph 3 for the grapheme joiner, 2844 for variation selector 16, 338 and 339 for
the Khmer inherent vowels — and breaks the syllable anyway. That is HarfBuzz's
model exactly: the character is invisible and is not removed.

So both engines that can be measured agree with each other and neither agrees
with this package. Taken with Unicode's own section 5.21, which enumerates the
six processes such a character is to be ignored in and does not name shaping
among them, the reading this package has been using is the odd one out and is
not the one the standard supports.

## What it does not settle

Whether the resulting page is better. The argument for the present behaviour was
never that other engines agree — it was that a soft hyphen inside a conjunct is
a legitimate hyphenation point, and that a document is written once and read
many times. Nothing here touches that. What it removes is the *other* half of
the argument, that Unicode asks for the character to be ignored in rendering:
Unicode asks for it not to be drawn, which every engine here does.

## What changing it would take

Not a flag. HarfBuzz's model is *keep, then hide*, and this package's is *drop
first*, so the work is to move the removal from before shaping to after it.

1. Stop `dropHiddenCharacters` at its two call sites in `fonts/glyphbuf.go`,
   so the characters reach the shaper and can break a cluster.
2. Give each of them a glyph the font can carry. A face that has no glyph for
   U+00AD would otherwise shape it as `.notdef`, and a `.notdef` is a real glyph
   id that a font's rules can match on — HarfBuzz substitutes an invisible glyph
   precisely so that cannot happen. This is the step most likely to go wrong.
3. Drop them at the end, in every path that shapes: the general one through
   `hideJoiners`, and `indic.go`, `khmer.go`, `myanmar.go` — each of which
   already drops the join controls this way and would widen to all ignorables —
   and `use.go`, which has no end-of-run drop at all and needs one.

The corpora do not need regenerating: `expected.txt` is HarfBuzz's answer and
does not move. What moves is that all thirty-seven entries in
`deliberateDifferences` should disappear, which is the test for whether the
change is complete and correct. `deliberateDifferences` becoming empty is also
the point of doing it — every remaining difference then means a defect.

Afterwards: re-run the differential fuzzer, because ignorables reaching the
shaper is a change every script sees and the corpora are a fixed list.

# The batch of forty-eight

Controls passed in all three files.

| | devanagari | tibetan | balinese |
| --- | --- | --- | --- |
| CoreText agrees with pdf0 | 4 | 19 | 0 |
| CoreText agrees with HarfBuzz | 1 | 6 | 1 |
| CoreText agrees with neither | 0 | 17 | 0 |

## What it settles

**Twenty-three are not defects.** The "a mark a few units to one side" family —
which is what most of these were — goes to this package, as the two questions
before it did. Three for three.

**Eight are.** Where CoreText and HarfBuzz agree against this package there is
nothing to argue about. Six of them are one shape: a deprecated Tibetan vowel
sign, U+0F77 or U+0F79, followed by U+0F74. The other two are
`U+0908 U+094E U+093C U+093C` and `U+1B1F U+1B44 U+1B46 U+1B38`.

## And seventeen that are a different question

CoreText inserts dotted circles where neither of the other two does — glyph 1282
is U+25CC in this font, and it appears two or three times in each of those
lines. That is not the mark-placement question these were gathered to ask; it is
CoreText judging the text more malformed than the other two do, and on it
CoreText is the one out of step. Worth its own look, not worth confusing with
this.

## Where the six begin

`U+0F45 U+0F77 U+0F74` is five glyphs here and four in the other two, and the
*base* differs — glyph 11 against 68 — so this is substitution, not placement.

Each of the two deprecated signs on its own is identical in both, dotted circle
and all, so nothing is wrong with how either is taken apart. What differs is
only what happens when U+0F74 follows one, and it changes the letter's own
glyph. That points at a rule the font states over the decomposed sequence which
one of the two is not reaching, rather than at anything in the mark machinery.

### And where they end

Narrowed to one sentence: **this package shapes the decomposed sequence
correctly and the composed one wrongly.**

	U+0F45 U+0FB2 U+0F71 U+0F80 U+0F74   both: 68 1766 1424 1347
	U+0F45 U+0FB2 U+0F71 U+0F74 U+0F80   both: 68 1766 1424 1347
	U+0F45 U+0F77                        both: 291 1421 1347
	U+0F45 U+0F77 U+0F74                 pdf0: 11 1765 1421 1347 1432
	                                     hb:   68 1766 1424 1347

So nothing is wrong with the shaping, and nothing is wrong with taking the sign
apart on its own. What is wrong is the order the pieces end up in when a mark
follows: U+0F74 belongs *between* U+0F71 and U+0F80 by combining class, and this
package leaves it after both.

The reason it is not simply a sorting bug: U+0F77's decomposition is
`<compat> 0FB2 0F81`, a *compatibility* mapping, so NFD does not apply it and
neither engine decomposes it during normalisation — the split comes from the
font's 'ccmp', which runs long after the canonical sort. HarfBuzz reaches the
right answer anyway; this package does not.

The fix worth trying first is to decompose these signs before shaping, the way
split vowels already are, so their pieces are sorted with what follows them
rather than after it. The evidence above says that is sufficient: given the
pieces in either order, this package already answers exactly as the other two do.

### What the six actually are

Not a normalisation problem. HarfBuzz's own trace, on the composed input:

	start table GSUB              [11, 1334, 1331]
	end lookup 16 feature 'ccmp'  [11, 1391, 1327, 1347, 1331]
	end lookup 22 feature 'ccmp'  [11, 1391, 1327, 1331, 1347]   <- swapped
	end lookup 51 feature 'blws'  [11, 1765, 1423, 1347]
	end lookup 1137 feature 'blws'[68, 1766, 1424, 1347]

The *font* puts the pieces in order, in 'ccmp' lookup 22, and the 'blws' chain
then merges them. Nothing here depends on the canonical sort, which is why
decomposing before shaping was the wrong idea however it was arranged.

This package stops at `[11, 1765, 1421, 1347, 1432]` — it applies lookup 16 and
51 and not 22, so the pieces are never reordered and the merges that depend on
that never match.

Lookup 22 is GSUB type 5, ContextSubst format 1, flags 0, and this package
implements that format. So it is a lookup that should apply and does not, which
is a bug to find rather than a question to decide. The next step is to put the
same trace against this package — apply 'ccmp' a lookup at a time over
`U+0F45 U+0F77 U+0F74` and see whether 22 is reached at all, whether its
coverage matches, and whether its rule matches — rather than to reason about it.

### The fix that was tried first, and why it was taken out again

Decomposing the four signs before shaping does fix all six. It was written,
measured, and reverted, because it costs more than it buys.

Given `U+0FB2 U+0F71 U+0F80 U+0F74` in either order this package already answers
as the other two do, so putting those runes in front of the shaper makes the six
agree exactly. The corpora stayed green. The differential fuzzer did not: over
half a million strings it went from 42 differences to 78, and the new ones are
this package inserting a dotted circle where neither of the others does —

	U+0F43 U+0F79 U+0F77   pdf0 has 1282 (U+25CC) and HarfBuzz has none

because two signs taken apart make a run of marks the cluster grammar reads as
broken. Six fixed against thirty introduced.

Two smaller things were learned on the way and are worth not rediscovering:

  - The decomposition walk deliberately never takes the *second* part apart, on
    the grounds that no canonical decomposition needs it. That is true, and
    U+0F77 is not canonical: it is U+0FB2 followed by U+0F81, and U+0F81 is what
    has to come apart. Making the walk general enough to do it changed answers
    elsewhere that were already right, so if this is tried again the four signs
    want a flattened decomposition of their own rather than a more capable walk.
  - The face has a glyph for U+0F81, so "take it apart only if it cannot be
    drawn" is not the test. Whether a piece *can* be drawn says nothing about
    whether it should be left whole.

What the six actually need is for the pieces to sort correctly *without* the
cluster grammar then judging the result malformed — which is a question about
the grammar, not about normalisation.

# The last batch: forty-six, none of them defects

Controls passed. Not one line went to HarfBuzz.

| | devanagari | tibetan |
| --- | --- | --- |
| CoreText agrees with pdf0 | 6 | 21 |
| CoreText agrees with HarfBuzz | 0 | 0 |
| CoreText agrees with neither | 0 | 19 |

The nineteen are the dotted-circle class again — CoreText judging the text more
malformed than either of the others and inserting U+25CC two or three times a
line, where pdf0 and HarfBuzz agree exactly. That is a disagreement about
cluster validity, not about where a mark sits, and on it CoreText is alone.

So the "mark a few units to one side" family is decided, four questions running.
It is now recognised in the fuzzer by its *shape* — same glyphs, same order,
same advances, only the offsets differing — rather than by listing the strings,
so it keeps holding for text nobody has generated yet. A difference in which
glyphs, or how many, or how wide, is not this and is still reported.

That takes the fuzzer from 46 differences to 3.

## What those three are

`U+0975 U+0930 U+094D U+094B`, and two like it: the same five glyphs in a
different order, with the dotted circle in a different place.

	pdf0     3898,1023  3979,409  3932,0   1586,594  3964,259
	harfbuzz 3898,1023  1586,594  3979,409 3932,0    3964,259

The circle marks where a broken cluster begins, so the two disagree about where
the syllable breaks rather than about anything in positioning. It has not been
looked at, and it is the only shaping question left open.
