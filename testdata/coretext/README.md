# Asking CoreText

One question, which this repository cannot answer on Linux and which decides
whether a deliberate difference stays deliberate.

A character nothing is drawn for, written inside a syllable — a soft hyphen
between a Devanagari consonant and its virama, say. This package removes every
default-ignorable before shaping, so the syllable is whole and the conjunct
forms. HarfBuzz keeps them until after, so the syllable breaks and the orphaned
virama gets a dotted circle.

Unicode permits both. Section 5.21 enumerates six processes such a character is
to be ignored in — text segmentation, line breaking, cursive joining,
identifiers, searching and sorting, and display — and shaping is not one of
them; the display rule says only that the character has no glyph of its own
"although they may have an effect on the display of other characters". So the
standard neither asks for the removal nor forbids it, and the reasons for the
choice are argued in `deliberateDifferences` in `fonts/harfbuzz_test.go`.

What is not known is whether anything other than HarfBuzz agrees with HarfBuzz.
If CoreText breaks the syllable too, this package stands alone among the engines
that can be measured, which is an argument for changing it. If CoreText keeps
the conjunct, the choice is a real disagreement between engines and ours is as
defensible as either.

## Running it

```sh
swiftc -O -o shape shape.swift
./shape ../../fonts/notosans/NotoSans-Variable.ttf              < ignorables.txt
./shape ../harfbuzz/fonts/NotoSansKhmer.ttf                     < ignorables-khmer.txt
```

## Reading it

The first two lines of each file are the control and say whether the harness
works at all, before anything it reports about the rest is worth believing:

- **line 1** is the syllable with nothing written inside it. It must come out as
  the conjunct — one glyph for Devanagari — and must not say `DOTTED`.
- **line 2** is the same syllable with its consonant taken away, which is
  genuinely malformed. It must say `DOTTED`.

If either control is wrong the harness is wrong, and nothing below it means
anything. `SUBSTITUTED-FONT` on any line means CoreText went looking in another
font and the glyph ids are not ours — also a harness problem.

Then, for the remaining lines: `DOTTED` on them is CoreText agreeing with
HarfBuzz, and a single glyph with no `DOTTED` is CoreText agreeing with this
package.

## What the two engines that can be measured here say

Both controls agree, which is what makes them controls:

| line | | pdf0 | HarfBuzz |
| --- | --- | --- | --- |
| 1 | the syllable, nothing inside it | 1 glyph | 1 glyph |
| 2 | the same, consonant removed | 3 glyphs, dotted | 3 glyphs, dotted |
| 3+ | the syllable with an ignorable inside | **1 glyph** | **4 glyphs, dotted** |

Khmer is the same shape: controls 1 and 2 glyphs, and then 1 against 3.

So a `DOTTED` on line 3 and after is CoreText siding with HarfBuzz, and a single
glyph is CoreText siding with this package.

## What it is not

It is not a full comparison of CoreText against this package. CoreText is a
layout engine rather than a shaper — it does its own itemization, bidi and font
fallback — so it is the wrong instrument for measuring positions, and this asks
it only a question it cannot be confused about: how many glyphs, and is one of
them a dotted circle.
