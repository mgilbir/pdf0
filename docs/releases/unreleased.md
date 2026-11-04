# Unreleased

The running draft of the next release's notes. The pull request that changes
the API or a behaviour a caller could notice adds its entry here, in the
section it belongs to, as it should read to a user. When a tag is cut, this
file is emptied into the GitHub release notes (summary and what changed are
written then, from these entries) and starts again empty.

A removed or renamed identifier is written in **bold**, not in backticks: the
docs lint resolves every backticked name against the tree as it is, and a
removed one is no longer in it. Before tagging, check the Breaking section
against what gorelease reports, run from a clean checkout of the commit to be
tagged (it refuses a tree with untracked files):

```sh
go run golang.org/x/exp/cmd/gorelease@latest -base=v0.7.0
```

## Breaking changes

## Behaviour changes

### fonts

- **forme v0.14.0** (#PR). A subset decides each glyph the face used before
  it carries it: a glyph whose record is malformed — an instruction length past
  its record, a cut-off or self-referencing composite, a component past the
  end of the font, a CFF glyph whose charstring does not run — is refused,
  where it used to be copied as it stood, and `Embed` fails with a
  `*shape.GlyphError` naming every such glyph used. A face embedded whole (its
  licence forbids subsetting) is unaffected. Each subset also runs under
  forme's default limits on work and bytes (`fonts.ErrSubsetLimit`).

### fonts

- **A document declaring a version before PDF 1.6 carries a CFF program
  bare** (#418), as `/FontFile3 /CIDFontType0C`, not in the OpenType wrapper
  PDF 1.6 added; one declaring a version before 1.4 writes bitmap and colour
  glyphs without transparency. `AddPage`, `AddForm` and `AddTilingPattern` set
  both from the version the document declares, as they did from a PDF/A-1
  claim.

## Added

### fonts

- **`Face.CheckGlyph`** (#PR) says, before a glyph is drawn, whether `Embed`
  can carry it — forme's decision for a subsetted face, taking the number
  drawing hands out — so a run needing a glyph that cannot be kept can be drawn
  another way. **`Face.EmbedFormsContext`** embeds under a context and
  **`fonts.SubsetLimits`**, failing with the context's error or one wrapping
  **`fonts.ErrSubsetLimit`**.

### fonts

- **Drawing a face into a content stream of one's own** (#417):
  `Face.Record` does to a face what `Draw` does — marks the glyphs used and
  records what each was drawn for, which the ToUnicode CMap says — and returns
  the run's stretches (`fonts.Segment`) and which need an `/ActualText`.
  `Face.AppendCode` writes a glyph's code and `Face.NominalAdvance` gives the
  width the font dictionary states for it. A face written as Type 3 fonts is
  refused (`fonts.ErrDrawnAsType3`).
- **`fonts.Forms.BareCFF`** (#418) writes a CFF program bare without the rest
  of what `Forms.PDFA1` changes, and **`fonts.Embedded.Version`** says the
  earliest PDF version whose features cover every object an embedding wrote,
  for a caller that writes its own header.
- **`fonts.Forms.Named`** (#419) writes a composite face without its program,
  under its PostScript name, for a pipeline that embeds each font once at a
  later assembly step: the font is complete once the program the face draws
  is added to its descriptor. Not standalone, and not PDF/A on its own.

## Fixed

### Validation

- **A CIDSet is held to the CIDs shown as veraPDF holds it** (#416). At
  PDF/A-1 a CID shown has to be in the `/CIDSet` only when the embedded
  program has it, and only for a subset; at PDF/A-2 and -3 the set is held to
  the program alone. A `CIDFontType0` whose CFF is not CID-keyed, whose program
  has no CIDs, used to fail every set at PDF/A-2 and -3, and a set of CID 0
  alone, which veraPDF passes at every part, at all of them.
