# Font embedding

What the font writer puts in a file, and why: which PDF font each kind of face
becomes, what `/W`, `/CIDSet`, `/CIDSystemInfo` and `/ToUnicode` hold, how
PDF/A-1 changes it, and how bitmap and colour glyphs become Type 3 fonts. Open
it when a reader or a validator disagrees with a font pdf0 wrote, or before
changing `fonts/`. To set text, start from [text.md](text.md), which has the
calls, the licence rule and a diagram of the dispatch below. Reading fonts in
other people's files, to validate them, is [fonts.md](fonts.md).

The writer's specification is executable: pdf0's own PDF/A validator checks
everything below against the program embedded — `/W` against the program's
metrics, `/CIDSet` against its CIDs — and the tests aim it back at the output,
at every PDF/A level. veraPDF, the reference validator, checks the Type 3
output too (`TestType3DocumentsAreValidForVeraPDF`).

## What each face is written as

| face | font dictionary | descendant | program | `/CIDSet` |
|---|---|---|---|---|
| composite, `glyf` outlines | `Type0`, `Identity-H` (and `Identity-V` for the vertical form) | `CIDFontType2`, `/CIDToGIDMap /Identity` | `/FontFile2` | every glyph slot of the program |
| composite, CID-keyed CFF (CFF2 is one) | `Type0` | `CIDFontType0` | `/FontFile3 /OpenType`; under PDF/A-1 `/FontFile3 /CIDFontType0C` | the CIDs of the glyphs kept |
| composite, CFF that is not CID-keyed | `Type0` | `CIDFontType0` | as above | none; under PDF/A-1, every glyph slot |
| simple | `TrueType`, `WinAnsiEncoding`, `/Widths` for codes 32–255 | — | `/FontFile2` | — |
| standard | `Type1`, `WinAnsiEncoding` except for Symbol and ZapfDingbats | — | none: no descriptor, no program | — |
| composite, bitmap strikes or COLR glyphs | `Type3` fonts: one per strike (or one for the COLR paintings), per text colour where a glyph bakes it in, and per 256 codes | — | glyph procedures in `/CharProcs` | — |

The [diagram in text.md](text.md#which-pdf-font-a-face-becomes) shows which
row a face lands in. Every stream — program, `/CIDSet`, `/ToUnicode`, glyph
procedures — is Flate-compressed. A subset's `/BaseFont` carries the six-letter
tag ISO 32000-2 9.6.4 requires, derived from the glyphs kept, so the same
document writes the same file twice; a font embedded whole because its
licence forbids subsetting carries no tag, because it is not a subset.

## One path from glyph to code

`fonts.Face` has eight ways to put a string on a page — `Encode`, `Shape`,
`ShapeWith`, `Draw`, `DrawShaped`, `DrawUpright`, `DrawReplaced` and
`DrawUprightReplaced` — and all of them go through one path in `fonts/draw.go`:

- **One function writes a code** (`appendCode`): `GlyphCode`'s two bytes for a
  composite face — the glyph index, or the CID for a CID-keyed CFF — and the
  WinAnsi byte for a simple or standard face. `/W`, `/CIDSet` and `/ToUnicode`
  are keyed by the same call, so they agree with the page by construction. A
  face written as Type 3 fonts asks which of its fonts a glyph is in instead.
- **One plan decides what the text is** (`plan`): the ToUnicode entry of each
  glyph and where an `/ActualText` is needed, as
  [text.md](text.md#getting-the-text-back) describes. The two emitters — spans
  for `Shape`, operators for `Draw` — only write what the plan decided.
- **The ToUnicode CMap covers the glyphs drawn** and nothing else.

`fonts_drawpaths_test.go` draws every path in a CID-keyed CFF, a TrueType face,
an Arabic face, two bitmap-only faces, a simple face and a standard face, and
checks both that the text extracts as written and that PDF/A-1b, -2b, -3b and
-4 find nothing wrong with the font.

## /CIDSet

`/CIDSet` is a bitmap in the CIDFont's descriptor: bit *i*, high bit first,
set when the embedded program has CID *i*. ISO 19005-2 6.2.11.4.2 (and -1
6.3.5 before it) says it "shall identify all CIDs which are present in the
font program, regardless of whether a CID in the font is referenced or used by
the PDF or not". What a program *has* depends on how it is keyed, and the
writer reads that as veraPDF does — its GFPDCIDFont rule over its font
parser's list of a program's CIDs, checked both ways at PDF/A-2 and -3:

- **TrueType** (`CIDFontType2`, Identity `/CIDToGIDMap`): a CID for every glyph
  slot the program declares, empty or not. forme's subset keeps the face's
  numbering and empties the slots it drops, so the set is every slot of the
  program — 4,515 bits for Noto Sans, however few glyphs a page draws — and not
  the glyphs kept. Listing only those is what veraPDF reported on every page
  htmlpdf wrote.
- **CID-keyed CFF** (CFF2 included): the CIDs its charset names. A subset
  holds only the kept glyphs, renumbered, each keeping its CID, so the set is
  the kept glyphs' CIDs.
- **CFF that is not CID-keyed**: no CIDs of its own — ISO 32000-1 9.7.4.2
  reads a CID as its glyph index — and veraPDF counts none, so any CID in a set
  is one the program lacks. No `/CIDSet` is written: PDF/A-2 and later do not
  require one and PDF 2.0 deprecates it. Under PDF/A-1, where a subset must
  have one and only the CIDs shown are checked, it lists every glyph slot, as a
  TrueType's does.

`TestTheCIDSetIsWhatTheProgramHas` holds the writer to this for each kind at
every level, and `TestTheValidatorHoldsTheCIDSetToTheProgram` holds pdf0's
validator to veraPDF's reading. The validator's side is
[fonts.md](fonts.md#cidset-and-charset).

## PDF/A-1

A document claiming PDF/A-1 is based on PDF 1.4, which predates the OpenType
font file and transparency. Two fields of `fonts.Forms` say so, and
`Document.AddPage` (and `AddForm`, `AddTilingPattern`) sets both from the
document's claim:

- **`Forms.PDFA1`**: a CFF program is written bare, as the program's `CFF `
  table in a `/FontFile3 /CIDFontType0C` stream, rather than in the OpenType
  wrapper PDF 1.6 added (ISO 19005-1 6.3.2, which the validator checks). A CFF2
  face's program is the CFF forme converts it to, whole or subset, so it is
  written bare like any other. A CFF face whose program had no `CFF ` table
  would be refused, but forme produces no such program. And a CFF that is not
  CID-keyed carries the `/CIDSet` a PDF/A-1 subset must have (see
  [/CIDSet](#cidset)).
- **`Forms.Opaque`**: a face written as Type 3 fonts uses no transparency
  (ISO 19005-1 6.4). A greyscale glyph is a stencil of its coverage, a colour
  bitmap is painted through a 1-bit `/Mask` cut from its alpha, and a COLR
  colour glyph is its outline in the text colour — refused when it has no
  outline of its own, as Noto Color Emoji's have not. Colours are kept;
  anti-aliased edges are not.

`Face.Embed` cannot see a document's claim and sets neither.

## CID-keyed CFF

A CID-keyed CFF numbers its glyphs by CID and reaches them through its charset,
so the CID and the glyph index are two different numberings. Every static Noto
CJK face is one. Four things in the written document are keyed by CID rather
than by glyph, and all of them come from the program rather than being
assumed:

| written | keyed by | read from |
|---|---|---|
| the code in the content stream | CID | `Encode`, which asks the charset |
| `/W` | CID | `shape.Face.GlyphCode` |
| `/CIDSet` | CID | the same |
| `/ToUnicode` | CID | the same |
| `/CIDSystemInfo` | — | the CFF's ROS operator |

Getting any of them wrong is invisible on a font whose charset happens to be the
identity, and most are. In Noto Sans JP `ｱ` is glyph 15435 and CID 59158, so a
`/W` written by glyph index describes something fifteen thousand places away —
and the reader shows a plausible page with the wrong metrics.

`/CIDSystemInfo` is the font's own registry, ordering and supplement, from
`shape.Face.CharacterCollection`, because §9.7.4.2 requires it to be compatible
with the character collection of the glyph source. Noto's is
`Adobe-Identity-0`; the veraPDF corpus carries an `Adobe-Japan1-6` font, which
is what the test uses, since a font already in the Identity collection cannot
tell a read value from an assumed one.

**A CID-keyed font that cannot name its collection is refused, not defaulted.**
`CharacterCollection` returns `ok == false` for a ROS naming strings the font
does not carry, or a supplement below zero — both malformed in ways that parse,
which is what makes them dangerous. Writing `Adobe-Identity-0` there looks like
caution and is not: it is a specific claim that the CIDs are the font's own
arbitrary numbering, and a reader believing it over an Adobe-Japan1 font looks
every glyph up in the wrong collection. The check runs *before* subsetting,
since the collection is a fact about the face; a font that cannot be embedded
then says so for the reason that matters rather than reporting whatever the
subsetter met first.

Widths come from `hmtx` through `Face.GlyphAdvances`, not from the CFF
charstrings — the two disagree in Noto Sans JP (glyph 34 is 608 in `hmtx` and
742 in the CFF) and `hmtx` is what an OpenType wrapper makes authoritative. It
is also what layout measured with, so the document's `/W` and its line breaks
agree.

A face from `fonts.Adopt` is embedded exactly as a loaded one is. `GlyphCode`
answers the keying from the face, and the collection is the face's own, read
when forme loaded the program; the subset carries the ROS and the charset
through untouched.

## Variable fonts and CFF2

A variable face is embedded as the instance it draws. forme hands a backend
the instance itself (`shape.LoadInstance`, which htmlpdf receives from layout
for bold text in a variable face): a static face with its own program, name,
advances and metrics at that point of the design space. `fonts.Adopt` embeds
it like any other face — its subset, `/W` from its advances and `/CIDSet` as
[/CIDSet](#cidset) says for its kind of program — so nothing of the default
master reaches the document. A variable face that was loaded rather than
instanced (`fonts.Load`, `fonts.NotoSans`) is its default master, and that is
what it draws and what is embedded; forme's subsetter drops the variation
tables either way.

A CFF2 font is read as the CFF it draws (forme 6f4fa65) and embedded as that:
a `CIDFontType0` whose `/FontFile3` is `/OpenType`, an sfnt with a CID-keyed
`CFF ` table in `Adobe-Identity-0` and no `CFF2`, which PDF has no font file
type for — or, under PDF/A-1, that `CFF ` table bare. Its instances are cut the
same way.

`TestAnInstanceIsEmbeddedAsItIsDrawn` holds both to an oracle outside the two
modules: `testdata/fontinstance/oracle.txt`, what HarfBuzz and fontTools'
instancer say each glyph advances and inks at two locations of Noto Sans and
of forme's CFF2 fixture. The document is written at every PDF/A level and read
back: `/W`, the embedded program's own advance and outline box, `/CIDSet` and
the validator; at PDF/A-1b the bare program is checked to be the `CFF ` table
of the wrapped one. The drawing-path matrix has a variable instance and both
CFF2 faces as rows.

One reader does not draw the wrapped programs: Poppler (24.02) shows nothing,
or the wrong glyph, for an OpenType-wrapped CID-keyed CFF whose CIDs are not
its glyph indices, which is every CID-keyed subset this package writes outside
PDF/A-1, and so every CFF2 font. It passes the CID to FreeType, which addresses
an sfnt-wrapped font by glyph index. Ghostscript draws them, and Poppler draws
the same program embedded bare as `/CIDFontType0C`, which is what PDF/A-1
gets. Elsewhere the wrapper is kept because `hmtx` in it is what makes the
widths authoritative (see [CID-keyed CFF](#cid-keyed-cff)), and the fonts it
applies to predate CFF2.

## Bitmap-only faces

A face whose glyphs are only bitmaps — EBDT, Apple's bdat, CBDT or sbix, with
no glyf, CFF or CFF2 outlines (`shape.Face.BitmapOnly`) — cannot be embedded
as a font program: a reader draws a program from its outlines, and would draw
these blank. It is written as Type 3 fonts instead (ISO 32000-2 9.6.4), whose
glyphs are content streams painting forme's image of each glyph
(`shape.Face.PaintGlyph`). So is a face with outlines whose licence permits
embedding only its bitmaps, from its strikes; the rule is in
[text.md](text.md#licences). The design record is
[bitmap-fonts-type3.md](proposals/bitmap-fonts-type3.md).

- **A 1-bit glyph** is a `d1` stencil: an image mask, painted in whatever
  colour the text is shown in.
- **A greyscale glyph** (a 2-, 4- or 8-bit strike) is a `d0` image of the fill
  colour with the coverage as its `/SMask`, so it keeps its anti-aliasing. The
  colour is baked into the glyph, so each text colour is its own font.
- **A colour bitmap** (CBDT, sbix) is a `d0` image of its PNG, with an `/SMask`
  for its alpha.

Under PDF/A-1 the last two are painted without transparency (see
[PDF/A-1](#pdfa-1)).

The strike is the one the text size picks in CSS pixels — points × 4/3 by
default, or the size itself for a stream drawn in CSS pixels, as htmlpdf's is
(`fonts.Face.SetPixelsPerUnit`). A glyph missing from that strike is drawn from
the nearest strike that has it, rather than blank.

A Type 3 font has one-byte codes, so a face is a set of sub-fonts keyed by
strike and colour and split into planes of 256 codes. The caller names the face
once, as it names any face; that name is the home font, and the others are
`fonts.SubfontName` of it (`F1.1`, `F1.2`, …). `Draw` and the other `Draw`
methods pick each glyph's font, select it with `Tf` inside the run, and select
the caller's name again after it, which is why the content builder reports the
font and fill colour a stream has set (`content.Builder.Font`,
`content.Builder.FillColor`). `fonts.Embedded.Subfonts` are the other fonts'
dictionaries, and `Page.Faces` defines every one under its name. `Encode` and
`Shape` have no stream to switch in, and write codes in the home font.

`fonts_bitmap_test.go` renders the result with Ghostscript and poppler and
compares every pixel of every glyph with forme's image of it, over forme's EBDT
and bdat fixtures and Noto Color Emoji (`make notoemoji`), and holds it to the
PDF/A validator and to the text a reader extracts.

## Colour glyphs (COLR)

A face with COLR colour glyphs (COLRv0 layers, COLRv1 paint graphs) has
outlines, and used to be embedded as them, so its glyphs came out in the text
colour alone. It is now drawn in colour, as Type 3 fonts like a bitmap face's,
by default; `fonts.Face.SetColourGlyphs(false)` keeps the old embedding. A
face whose licence permits embedding only bitmaps is never drawn in colour,
since the paintings are outline data ([text.md](text.md#licences)). Each
glyph's procedure is the PDF translation of forme's painting
(`shape.Face.PaintGlyph`), in `fonts/colr.go`:

- transforms and clips are `cm` and `W n` inside `q`/`Q`;
- solid fills set their colour and, through an ExtGState `ca`, their alpha;
- linear and radial gradients are axial and radial shadings whose function
  is the colour line stitched from linear pieces, a period at a time for
  repeat and reflect, mirrored for reflect;
- sweep gradients are function-based shadings computing the angle with a
  PostScript calculator function;
- a stop with alpha draws its shading through a luminosity soft mask;
- the fifteen blend modes are isolated groups drawn with `/BM`, and Clear,
  Src, Dest, DestOver, SrcIn, DestIn, SrcOut and DestOut are the groups
  reordered or one drawn through an alpha soft mask of the other. A group
  that blends or masks is itself drawn as an isolated group, so it blends
  with its own backdrop and nothing on the page;
- SrcAtop, DestAtop, Xor and Plus have no exact PDF equivalent, and a glyph
  that uses one is refused rather than approximated.

A paint in the foreground colour sets no colour: a `d0` procedure starts in
the colour the text is shown in. So one font serves every text colour, and
only a gradient with a stop in the foreground colour, whose shading has to
state it, is keyed by the text colour. A glyph that sets no colour of its own
is a `d1` procedure. Under PDF/A-1 a colour glyph is its outline in the text
colour (see [PDF/A-1](#pdfa-1)).

`fonts_colr_test.go` holds every glyph of Google's COLRv1 conformance font
(`testdata/colrv1`) to Skia, the renderer COLRv1 was specified against,
through blackrenderer, and Noto Color Emoji's COLRv1 build to HarfBuzz
(`hb-view`), in Ghostscript's and poppler's rendering: edges to within
anti-aliasing, and the inside of every shape to within 12 levels a channel.
Skia, not HarfBuzz, is the oracle for the conformance font: HarfBuzz's cairo
renderer draws the sweeps that pass a full turn differently from Skia, and
pdf0 draws them as Skia does. Two renderer defects were found on the way and
are worked around in what pdf0 writes: Ghostscript refuses a calculator
function with a real of seventeen digits and a function-based shading without
an explicit `/Matrix`. One is not: Ghostscript draws a shading inside two
nested clips as one flat colour (glyph 208 of the conformance font), which
poppler draws correctly.

## Cost

A face drawn as Type 3 fonts costs what its glyphs cost, once each: a page
that draws glyphs already drawn adds nothing, and the embedding a document
rewrites as pages add glyphs reuses each glyph's images, paintings and
compressed procedure. Measured on 200 A4 pages of 50 glyphs each, cycling
through every emoji the face has (1,212 distinct):

| face | build | file | per glyph | peak RSS |
|---|---|---|---|---|
| Noto Sans (text, for scale) | 0.05 s | 0.2 MB | 0.3 KB | 34 MB |
| Noto Color Emoji, CBDT | 0.62 s | 5.6 MB | 4.6 KB | 92 MB |
| Noto Color Emoji, COLRv1 | 0.93 s | 8.6 MB | 7.1 KB | 276 MB |

The time and the file grow with the distinct glyphs, not with the pages.
Before the procedures were compressed once, a COLR face's build was
quadratic in its glyphs: 3.4 s for the same document, each page slower than
the last.

Every PDF/A document these fonts produce, at every level, is also validated
by veraPDF (`TestType3DocumentsAreValidForVeraPDF`, testdata/verapdf), as
well as by pdf0's own validator.

## Confirmed limitations

- **Colour glyphs:** a COLR glyph that composites in SrcAtop, DestAtop, Xor
  or Plus is refused. A gradient stop in the text colour needs the text in
  DeviceRGB or DeviceGray. In PDF/A-1 a colour glyph with no outline of its
  own is refused. A linear or radial colour line whose stops all share one
  offset is drawn as a step even when it repeats; Skia draws such a sweep as
  nothing, and pdf0 does the same for a sweep, with nothing to say what a
  linear or radial one should be.
- **Bitmap faces:** a greyscale glyph in a colour that is not DeviceGray,
  DeviceRGB or DeviceCMYK (a named ICC space, a Separation, a pattern), or in
  a colour the stream has not set, is drawn as a stencil of its coverage,
  which keeps the colour and loses the anti-aliasing. SVG glyphs are refused.
  A font with outlines *and* bitmaps is embedded as its outlines unless its
  licence permits embedding only its bitmaps (see
  [text.md](text.md#licences)).
- **No vertical Type 3:** a face written as Type 3 fonts has no vertical form;
  `DrawUpright` places its glyphs one by one in the horizontal fonts.
- **Poppler and wrapped CID-keyed CFF:** see [Variable fonts and
  CFF2](#variable-fonts-and-cff2).

## Fonts and font data from elsewhere

- **The bundled face** (`fonts.NotoSans`) is forme's, in its
  `fonts/notosans` package, under the SIL Open Font License with its licence
  file beside it. `fonts.NotoSansLicense` returns the text, for a program that
  ships the font inside something and has to reproduce the licence.
- **The standard fourteen faces' metrics** are generated from the Adobe Core
  14 AFM files, and live with the rest of the shaping code in forme
  (`shape/standard14.go`), which carries the Adobe copyright notices and the
  licence paragraph verbatim as that licence requires. Only advance widths and
  font-wide metrics are taken; no outline, no font program and no character
  shape is reproduced, and no AFM file is redistributed. The fourteen names —
  Helvetica, Times, Courier, Symbol, ZapfDingbats and the variants — are the
  identifiers ISO 32000-2 9.6.2.2 gives these faces, and are what a `/BaseFont`
  entry holds. Several are trademarks of their owners. Naming one in a PDF is
  how the format says "the face the reader already has".
- **One typeface is committed to this repository**, as a test fixture:
  `testdata/colrv1/test_glyphs-glyf_colr_1.ttf`, Google Fonts' COLRv1
  conformance font, under the Apache License 2.0 in `testdata/colrv1/LICENSE`
  ([its README](../testdata/colrv1/README.md) gives the source and pin). It is
  part of the module's source, as all of `testdata` is, and only tests read it;
  no package embeds it in a document.
- **The other fonts the tests use are fetched, not committed**: Noto Sans JP
  (`make notocjk`) and Noto Color Emoji (`make notoemoji`); see
  [testing.md](testing.md). forme's own fixtures are read from the module cache.

forme's notices for everything it carries are its
[THIRD_PARTY_NOTICES](https://github.com/mgilbir/forme/blob/main/THIRD_PARTY_NOTICES).

## File map

| File | Owns |
|------|------|
| `fonts/face.go` | `Face` and its constructors: `Load`, `LoadSimple`, `Standard`, `NotoSans`, `Adopt`, `Clone` |
| `fonts/draw.go`, `fonts/shape.go`, `fonts/bounded.go` | the one drawing path: codes, the text plan, spans and operators; the bounded `…Context` calls |
| `fonts/vertical.go` | the vertical form and `DrawUpright` |
| `fonts/embed.go`, `fonts/embedding.go` | `Embed`, `EmbedForms`, `Forms`; the composite, simple and standard writers, `/W`, `/CIDSet`, `/ToUnicode`; the licence |
| `fonts/type3.go`, `fonts/type3embed.go`, `fonts/colr.go` | Type 3 faces: sub-fonts, strikes, glyph images, and the COLR translator |
| `simplefont/` | the standard Latin encodings and character sets, shared with the validator and the text extractor |
| `faceembed.go` | one embedding per face per document, rewritten in place as pages add glyphs; reads the PDF/A-1 claim into `Forms` |
| `page_add.go`, `form.go`, `pattern.go` | `Faces` on a page, a form and a tiling pattern |
