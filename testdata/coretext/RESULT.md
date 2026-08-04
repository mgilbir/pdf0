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
