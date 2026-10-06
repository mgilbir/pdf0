# Bitmap-only fonts as Type 3 fonts — design record

**Status:** proposed; being built in stages (see [Stages](#stages)).

A font whose glyphs are only bitmaps (EBDT/EBLC, Apple's bdat/bloc, CBDT/CBLC,
sbix) and that has no glyf, CFF or CFF2 outlines cannot be embedded as a
TrueType or OpenType font program. PDF readers draw an embedded program from its
outlines and draw nothing from its bitmap tables, so a program without outlines
would come out as blank glyphs. forme's subsetter refuses such a face, and pdf0
has passed that refusal on: `Embed` fails with "the font's glyphs are only
bitmaps", and `htmlpdf.Render` fails with it.

PDF's mechanism for bitmap glyphs is the Type 3 font (ISO 32000-2 9.6.4), whose
glyphs are content streams. ISO 32000-2 8.9.6.2 names painting glyph bitmaps
as one of the main uses of stencil masking. forme 0.8.0 paints every bitmap
glyph through `shape.Face.PaintGlyph`, which is what makes this buildable:

- An EBDT or bdat glyph arrives as an `ImageMask`: one byte of coverage per
  pixel, to be painted in the foreground.
- A CBDT or sbix glyph arrives as a PNG.

In both cases `Image.Box` is where the image goes, in font units.

## Decisions

Two were the maintainer's:

1. **No glyph limit.** A Type 3 font is a simple font: one-byte codes, at most
   256 glyphs. A face that uses more is split across several Type 3 fonts, and
   drawing switches between them with `Tf` inside a run.
2. **Greyscale strikes keep their anti-aliasing.** A glyph that takes the text's
   colour (`d1`) can only paint a 1-bit stencil. A 2-, 4- or 8-bit strike is
   therefore painted as a `d0` glyph in the text's colour, with the coverage as
   its soft mask. That colour is baked into the glyph, so there is one Type 3
   font per face per text colour.

These follow from the probes and are this design's own:

3. **The strike is chosen from the text size.** A bitmap font's strikes are
   designed per size and need not agree. In forme's `Strikes.ttf` fixture the
   12 ppem strike is 1-bit, the 16 ppem strike is 2-bit, and glyph 5 is missing
   from the largest strike. The ppem asked for is the text size in CSS pixels
   (points × 4/3), which is the strike a browser at 1× would draw. A glyph
   missing from that strike is drawn from the nearest strike that has it,
   rather than blank.
4. **Colour bitmaps are colour images.** A CBDT or sbix glyph is a `d0` glyph
   that paints the PNG decoded to RGB, with an alpha `/SMask` when it is not
   opaque. It ignores the text colour, as it does in a browser.
5. **1-bit strikes stay colour-independent.** Coverage that is all 0 or 255 is
   a `d1` glyph painting an image mask, so one font serves every text colour.
   Only a strike with partial coverage needs a font per colour.

So a bitmap face maps to a set of **sub-fonts**, each a Type 3 font, keyed by:

| key | varies when |
|---|---|
| strike (ppem of the strike drawn) | the text size picks another strike, or a glyph falls back to one |
| colour | the strike has partial coverage and the text colour differs |
| plane | the face has more than 256 glyphs in use under the same strike and colour |

Codes are assigned in order of first use within a sub-font, and never change
once assigned. That is what lets the document rewrite an embedding in place as
pages add glyphs (see `faceembed.go`).

## Naming and drawing

A caller selects a face as it does now: `Page.Faces["F1"] = face` and
`b.SetFont("F1", size)`. When a bitmap face draws a glyph whose sub-font is not
the current one, it writes `Tf` with a derived name (`F1.2`, `F1.3`, …). After the
run it writes `Tf` for the caller's name again, so the font the caller selected
is the current one, as before. To do that, the Builder now tracks the current
font and size, and the fill colour, across `q`/`Q`.

`Page.Faces` (and Form's and Pattern's) keep naming one face per name. The
document embeds every sub-font the face has used, and adds each derived name to
the resources with its own font dictionary. The Builder's resource check sees
the derived names, because `SetFont` records them.

`Shape`, `ShapeWith` and `Encode` return bare codes with no Builder, so they
cannot switch fonts. For a bitmap face they return codes in the sub-font of the
first glyph, and count each glyph that would need another sub-font as missing.
`Draw`, `DrawShaped` and the other `Draw*` methods are the full path.

## What each glyph is

- **1-bit strike:** `wx 0 llx lly urx ury d1`, then the image mask placed by its
  box: `q w 0 0 h x y cm BI … ID … EI Q`, or a `Do` of a mask XObject in the
  font's `/Resources`.
- **Greyscale strike:** `wx 0 d0`, then an image XObject in the text colour
  (DeviceGray, DeviceRGB or DeviceCMYK, as the colour was set) whose `/SMask` is
  the coverage.
- **Colour strike:** `wx 0 d0`, then the decoded PNG as an image XObject, with
  an `/SMask` for its alpha.

`/FontMatrix` maps font units to text space (`1/upem`). `/Widths` comes from the
face's advances, so pdf0's PDF/A validator (`checkType3Widths`) can check them
against each glyph's `wx`. `/ToUnicode` comes from the same text record as a
composite face's. `/FontBBox` is the union of the image boxes.

A text colour in a colour space other than DeviceGray, DeviceRGB or DeviceCMYK
(a named ICC space, Separation, a pattern) cannot be baked into a greyscale
glyph from inside the fonts package, because the glyph would need that space's
object. Such text is drawn from the 1-bit threshold of the coverage as a `d1`
glyph, so it keeps its colour and loses its anti-aliasing. This is a known limit.

## Oracles

- **PDF/A:** pdf0's own validator (Type 3 widths, resources, transparency) over
  every output, at the levels the output can claim. PDF/A-1 forbids soft masks,
  so greyscale and colour glyphs are not PDF/A-1. That is reported, not hidden.
- **Rendering:** each test page is rasterised with Ghostscript and pdftoppm and
  compared pixel by pixel against forme's `PaintGlyph` output at the same
  scale.
- **Text:** `ExtractText` and `pdftotext` return the text that was drawn.
- **Fixtures:** forme's `Strikes.ttf` (EBDT, mixed 1-bit and 2-bit strikes) and
  `StrikesApple.ttf` (bdat). A real CBDT font is added as a fetched dataset
  (Noto Color Emoji), because the CBDT and sbix fixtures in forme's testdata
  hold placeholder PNG data.

## Stages

1. **Builder state:** `content.Builder` tracks the current font, size and fill
   colour across `q`/`Q`, and exposes them.
2. **Type 3 embedding and drawing:** the fonts package handles sub-fonts, the
   three glyph kinds, `Tf` switching and per-sub-font ToUnicode and widths.
3. **Document and htmlpdf:** `embedFaces` embeds and rewrites every sub-font,
   the resource sets get the derived names, htmlpdf stops refusing bitmap
   faces, and the end-to-end oracles above run.
