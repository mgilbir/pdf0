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

## The second question: five units of x

`offsets.txt` asks a different thing of the same harness, now that it prints
displacements as well as advances.

One Tibetan string is the last difference between this package and HarfBuzz, and
it is five units of x on one glyph. The y agrees, the target agrees, the anchors
agree and the lookup agrees — HarfBuzz's x is the attachment delta against the
target's *final* offset, and this package's is the same delta against the
target's offset *at the moment the attachment was made*. Neither model produces
both of HarfBuzz's numbers: attaching against final positions gets the x right
and the y wrong by 887, and attaching against intermediate positions gets the y
right and the x wrong by 5.

So before writing more code, it is worth knowing whether HarfBuzz is even the
one to match here.

```sh
swiftc -O -o shape shape.swift
./shape ../harfbuzz/fonts/NotoSerifTibetan.ttf < offsets.txt
```

Three lines. The first two are controls that this package and HarfBuzz already
agree on, so if CoreText does not match them the harness is measuring something
else and the third line says nothing:

| line | expected of both |
| --- | --- |
| 1 | `6,704  1328,0,-614,0` |
| 2 | `96,641  1778,0,-591,-30  1530,0,-557,-367  1322,0,-62,-102` |

The third line is the question. Both agree up to the last glyph:

	92,579  1442,0,-706,-294  1738,0,-375,-316  1460,0,-649,-835
	1422,0,-455,-1181  1328,0,-596,0  1323,0,?,-154

- last glyph `1323,0,-157,-154` — CoreText agrees with HarfBuzz, and this
  package has a defect worth chasing into HarfBuzz's propagation.
- last glyph `1323,0,-152,-154` — CoreText agrees with *this package*, HarfBuzz
  is the outlier, and the right answer is to leave it alone and record why.

Anything else is a third answer and more interesting than either.
