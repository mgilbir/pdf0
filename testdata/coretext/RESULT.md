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
