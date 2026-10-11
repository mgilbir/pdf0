# Building a PDF

How to make a PDF from nothing with pdf0: start a document, draw pages with
`content.Builder`, and write the file. Reach for this when your program
produces the drawing itself — a report, a label, a chart, an invoice. If what
you have is HTML and CSS, [htmlpdf.md](htmlpdf.md) is the shorter road; it
draws through this same API.

The tutorial builds one page. The sections after it are how-tos, one per thing
you may want to add; where a program in [`examples/`](../examples/) does it
for real, the section names it. Outlines, links, the structure tree, patterns
and transparency have no example yet, but every snippet here is type-checked
against the code. The godoc is the reference for every call named here.

## Tutorial: one page, from nothing

```go
doc := pdf0.NewDocument()

face, err := fonts.NotoSans() // bundled with the module: no font file needed
if err != nil {
	return err
}

var page content.Builder

// A filled band across the top of the page.
page.Save().
	SetRGB(0.15, 0.35, 0.75).
	Rect(0, 762, 595, 80).Fill().
	Restore()

// A heading in white on the band, and a line of body text below it.
page.BeginText().SetFont("F1", 28).SetGray(1).MoveText(72, 790)
face.DrawShaped(&page, "Hello, pdf0", 28)
page.EndText()

page.BeginText().SetFont("F1", 12).SetGray(0).MoveText(72, 720)
face.DrawShaped(&page, "A page built from nothing, with an embedded font.", 12)
page.EndText()

// A rule under the text.
page.Save().
	SetStrokeGray(0.6).SetLineWidth(0.5).
	MoveTo(72, 710).LineTo(523, 710).Stroke().
	Restore()

if _, err := doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, // A4, in points
	Content: &page,
	Faces:   map[object.Name]*fonts.Face{"F1": face},
}); err != nil {
	return err
}
return doc.Save(w)
```

What each step did:

1. **`pdf0.NewDocument`** started an empty PDF 2.0 document: a catalog, an
   empty page tree, and the file identifier (`/ID`) PDF 2.0 requires. A
   `Document` you assemble by hand has no `/ID`, and `Write` does not add one.
2. **`content.Builder`** recorded the drawing. Coordinates are in points
   (1/72 inch) with the origin at the bottom left of the page and y increasing
   upwards, so the band at y = 762 is at the top of an 842-point page. Every
   call returns the builder, so calls chain. The builder enforces the shape of
   a content stream — `Save`/`Restore` balanced, text operators only between
   `BeginText` and `EndText`, a path painted before anything else is drawn —
   and keeps the first mistake rather than returning an error from every call.
3. **`Face.DrawShaped`** shaped the string with the font and drew its glyphs
   inside the text object, whose font the builder selected as `F1` at the same
   size. The face records each glyph it draws, which is what the embedded
   subset and the text a reader extracts are made from. Setting text is
   [text.md](text.md); this guide does not repeat it.
4. **`Document.AddPage`** checked the drawing, reported the builder's first
   mistake if it had one, and wrote the page. Every name the drawing used
   (`F1` here) must be defined by one of the page's maps, or the page is
   refused. Naming the face in `Page.Faces` embedded it, subset to the glyphs
   the page drew.
5. **`Document.Save`** wrote the file. A document that claims no conformance
   level is written exactly as `Write` would write it; one that claims PDF/A
   is checked first ([below](#a-pdfa-document)).

Run it as a program with `go run ./examples/simple_pdfa > out.pdf`, which draws
the same kind of page into a PDF/A-4 document. `examples/simple_pdf` and
`examples/simple_pdf17` draw with a standard font instead, which a reader
supplies and nothing embeds.

## Drawing

`content.Builder` has one method per content-stream operator, named for what it
does rather than for the operator: `MoveTo`, `LineTo`, `CurveTo`, `Rect` and
`ClosePath` build a path; `Fill`, `Stroke`, `FillStroke`, `Clip` and `EndPath`
end it. `SetRGB`, `SetGray` and `SetCMYK` set the fill colour, and their
`SetStroke…` twins the stroke colour; `SetLineWidth`, `SetLineCap`, `SetDash`
and their siblings set the rest of the stroke. `Translate`, `Scale` and
`Concat` change the coordinate system until the matching `Restore`.

Two rules catch most first attempts:

- **Set the colour before the path.** Between `MoveTo` (or `Rect`) and the
  painting call only path operators are allowed, so a colour set in between is
  a mistake the builder records.
- **Wrap each change of state in `Save` … `Restore`.** A colour or a
  transformation set without one stays in force for everything drawn after it.

A name the drawing uses — a font for `SetFont`, an image or form for `Draw`, a
graphics state for `SetExtGState`, a shading for `Shading`, a pattern for
`SetPattern` — is any name you choose, defined by the matching map on the page:
`Page.Faces` or `Page.Fonts`, `Page.XObjects`, `Page.ExtGStates`,
`Page.Shadings`, `Page.Patterns`, `Page.ColorSpaces`. `Builder.Resources` lists
the names a drawing used.

Text: [text.md](text.md), and `examples/text`, which measures words and breaks
a paragraph into lines.

## Images

`images.Embed` writes a Go `image.Image` into the document and returns a
reference; the page names it in `Page.XObjects` and the drawing places it with
`Draw`. An image fills the unit square, so the matrix it is drawn under is its
size and position:

<!-- snippet
var img image.Image
-->
```go
ref, err := images.Embed(doc, img)
if err != nil {
	return err
}
var b content.Builder
// An image fills the unit square, so the matrix is its size and place.
b.Save().Concat(200, 0, 0, 150, 72, 600).Draw("Im0").Restore()
_, err = doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	XObjects: map[object.Name]object.Object{"Im0": ref},
})
```

`images.EmbedJPEG` keeps a JPEG as it is and `images.EmbedStencil` writes a
one-bit mask drawn in the fill colour; [images.md](images.md#writing-images)
says which to use when. `examples/extract_images` embeds two images and reads
them back; `examples/flavored` places one beside text and a chart.

## A drawing used more than once

`Document.AddForm` writes a drawing once as a form XObject, and every `Draw`
of its name paints it. A form has its own bounding box, which clips it, and
its own resource maps, as a page has:

```go
var logo content.Builder
logo.SetRGB(0.15, 0.35, 0.75).Rect(0, 0, 40, 40).Fill()
logoRef, err := doc.AddForm(pdf0.Form{
	BBox:    [4]float64{0, 0, 40, 40},
	Content: &logo,
})
if err != nil {
	return err
}

var b content.Builder
b.Save().Translate(72, 750).Draw("Logo").Restore()
b.Save().Translate(483, 750).Draw("Logo").Restore()
_, err = doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	XObjects: map[object.Name]object.Object{"Logo": logoRef},
})
```

## Title, author and dates

`Document.SetDocumentInfo` describes the document. It writes both the
information dictionary and the XMP metadata, from one `pdf0.DocumentInfo`, so
the two cannot disagree; a field left empty is left as the document has it. A
document claiming PDF/A-4 is described in XMP alone, as that level requires.

```go
err := doc.SetDocumentInfo(pdf0.DocumentInfo{
	Title:   "Quarterly results",
	Author:  "Finance",
	Subject: "Revenue by quarter",
})
```

`examples/flavored` does this at three conformance levels.

## An outline (bookmarks)

`Document.SetOutline` replaces the document's outline with a tree of
`pdf0.OutlineItem`. Each entry points at a page by the reference `AddPage`
returned, and `To` says where on the page to arrive — the zero value shows the
whole page, `pdf0.AtTop` brings a y coordinate to the top of the window:

<!-- snippet
var p1, p2 object.IndirectRef // returned by AddPage
-->
```go
err := doc.SetOutline([]pdf0.OutlineItem{
	{Title: "Introduction", Page: p1},
	{Title: "Results", Page: p2, Open: true, Children: []pdf0.OutlineItem{
		{Title: "By quarter", Page: p2, To: pdf0.Destination{Kind: pdf0.AtTop, Top: 600}},
	}},
})
```

## Links

A `pdf0.Link` in `Page.Links` makes a rectangle of the page follow a URI or go
to another page. It has exactly one of the two. The page it goes to must
already be in the document, so a link can only point at an earlier page:

<!-- snippet
var b content.Builder
var firstRef object.IndirectRef // returned by AddPage for an earlier page
-->
```go
_, err := doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	Links: []pdf0.Link{
		{Rect: [4]float64{72, 716, 160, 732}, URI: "https://github.com/mgilbir/pdf0"},
		{Rect: [4]float64{72, 696, 210, 712}, Page: &firstRef},
	},
})
```

A link draws nothing: the rectangle is only where a click lands, so draw the
text or the box it covers yourself. A URI's scheme must be http, https, ftp,
mailto or tel. `pdf0.LinkURI` applies that rule on its own, so a caller
converting links from untrusted input can drop a bad one instead of failing
the page.

## Tagged PDF and the structure tree

A tagged PDF says what each part of a page *is* — a heading, a paragraph, a
figure with its alternate text — which is what a screen reader reads and what
PDF/UA requires. Mark each piece of content with `BeginTagged` and an
identifier unique on the page, then describe the elements with
`Document.SetStructureTree`:

<!-- snippet
var face *fonts.Face
-->
```go
var b content.Builder
b.BeginTagged("H1", 0).
	BeginText().SetFont("F1", 20).MoveText(72, 760)
face.DrawShaped(&b, "Annual report", 20)
b.EndText().EndMarked()

b.BeginTagged("P", 1).
	BeginText().SetFont("F1", 11).MoveText(72, 730)
face.DrawShaped(&b, "Revenue grew in every quarter.", 11)
b.EndText().EndMarked()

pageRef, err := doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	Faces: map[object.Name]*fonts.Face{"F1": face},
})
if err != nil {
	return err
}
return doc.SetStructureTree([]pdf0.StructElem{{
	Tag:  "Document",
	Page: &pageRef, // inherited by the children
	Children: []pdf0.StructElem{
		{Tag: "H1", Content: []int{0}},
		{Tag: "Summary", Content: []int{1}}, // mapped onto P below
	},
}}, map[string]string{"Summary": "P"})
```

`Content` lists the identifiers given to `BeginTagged`. A tag of your own is
mapped onto the standard type it behaves like by the role map, the second
argument; nil is fine when every tag is standard. Give a `Figure` its `Alt`
text. `SetStructureTree` marks the document as tagged and builds the index from
each page back into the tree.

A structure tree is most of PDF/UA, not all of it. pdf0 has no call for the
rest — the catalog's `/Lang`, `/ViewerPreferences` with `/DisplayDocTitle`, and
the `pdfuaid` identification in XMP — so a document built this way still
fails `pdf0.ValidatePDFUA` on those document-level rules until you add them.
Check with the validator; [pdfua.md](pdfua.md) says what it reads.

## Gradients and patterns

`pdf0.LinearGradient` and `pdf0.RadialGradient` build a shading from colour
stops in DeviceRGB. There are two ways to paint one:

- **`Builder.Shading`** paints it over the whole current clip, ignoring any
  path — so clip to a shape first. Its coordinates are those of the current
  transformation.
- **As a pattern**, wrapped by `pdf0.ShadingPattern`: a colour that any fill
  can use. A pattern's coordinates are anchored to the page, not to the
  current transformation; pass a matrix to move it.

```go
ramp, err := pdf0.LinearGradient(72, 0, 272, 0, []pdf0.Stop{
	{Offset: 0, Color: [3]float64{0.15, 0.35, 0.75}},
	{Offset: 1, Color: [3]float64{0.95, 0.55, 0.15}},
})
if err != nil {
	return err
}
fill, err := pdf0.ShadingPattern(ramp, nil)
if err != nil {
	return err
}

var b content.Builder
// sh paints the shading over the whole clip, so clip to the shape first.
b.Save().Rect(72, 600, 200, 100).Clip().EndPath().Shading("Sh0").Restore()
// A pattern is a colour: any fill can use it.
b.Save().SetColorSpace("Pattern").SetPattern("P0").
	MoveTo(72, 450).LineTo(272, 450).LineTo(172, 560).ClosePath().Fill().
	Restore()

_, err = doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	Shadings: map[object.Name]object.Object{"Sh0": ramp},
	Patterns: map[object.Name]object.Object{"P0": fill},
})
```

`Document.AddTilingPattern` writes a drawing that repeats: `BBox` is one cell,
and `XStep` and `YStep` the distance between cells, which default to the cell's
size. Like a shading pattern, the tiling is anchored to the page, so two shapes
filled with it show one continuous tiling.

```go
var cell content.Builder
cell.SetRGB(0.8, 0.1, 0.1).Rect(0, 0, 5, 5).Fill()
dots, err := doc.AddTilingPattern(pdf0.TilingPattern{
	BBox:    [4]float64{0, 0, 10, 10}, // a 5pt square in a 10pt cell
	Content: &cell,
})
if err != nil {
	return err
}

var b content.Builder
b.SetColorSpace("Pattern").SetPattern("Dots").Rect(72, 600, 200, 100).Fill()
_, err = doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	Patterns: map[object.Name]object.Object{"Dots": dots},
})
```

A pattern with `Uncolored` set carries shape without colour, and
`pdf0.UncoloredPatternSpace` is the colour space a page selects it through. The
builder cannot yet paint one: that takes the colour components and the pattern
name in one `scn`, and `SetPattern` writes the name alone. Use a coloured
pattern.

## Transparency and blending

Translucency and blend modes are graphics states: build one, name it in
`Page.ExtGStates`, and select it with `SetExtGState` inside `Save` …
`Restore`. `pdf0.Opacity` takes the fill and stroke alpha (1 is opaque),
`pdf0.Blend` a `pdf0.BlendMode`, and `pdf0.BlendWithOpacity` both at once. Set
`Page.Group` on a page with translucent marks, so the page says what they are
composited against instead of leaving it to the reader:

```go
half, err := pdf0.Opacity(0.5, 0.5)
if err != nil {
	return err
}
multiply, err := pdf0.Blend(pdf0.BlendMultiply)
if err != nil {
	return err
}

var b content.Builder
b.Save().SetRGB(0.15, 0.35, 0.75).Rect(72, 600, 200, 100).Fill().Restore()
b.Save().SetExtGState("Half").SetRGB(0.95, 0.55, 0.15).Rect(172, 550, 200, 100).Fill().Restore()
b.Save().SetExtGState("Mul").SetRGB(0.9, 0.9, 0.2).Rect(122, 520, 200, 100).Fill().Restore()

_, err = doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b,
	Group:      true, // define what translucent marks composite against
	ExtGStates: map[object.Name]object.Object{"Half": half, "Mul": multiply},
})
```

A soft mask takes the transparency from another drawing. Draw the mask as a
form with `Group` set, then build the graphics state from it:
`Document.LuminositySoftMask` lets through what the form paints white and
hides what it paints black; `Document.AlphaSoftMask` lets through wherever the
form painted at all. `pdf0.NoSoftMask` removes a mask without a `Restore`.

```go
// The mask: a horizontal ramp from black to white, drawn in a group form.
ramp, err := pdf0.LinearGradient(72, 0, 272, 0, []pdf0.Stop{
	{Offset: 0, Color: [3]float64{0, 0, 0}},
	{Offset: 1, Color: [3]float64{1, 1, 1}},
})
if err != nil {
	return err
}
var m content.Builder
m.Shading("Ramp")
maskForm, err := doc.AddForm(pdf0.Form{
	BBox:     [4]float64{72, 600, 272, 700},
	Content:  &m,
	Group:    true, // a soft mask's form must be a transparency group
	Shadings: map[object.Name]object.Object{"Ramp": ramp},
})
if err != nil {
	return err
}
fade, err := doc.LuminositySoftMask(maskForm, nil)
if err != nil {
	return err
}

var b content.Builder
b.Save().SetExtGState("Fade").
	SetRGB(0.15, 0.35, 0.75).Rect(72, 600, 200, 100).Fill().
	Restore()
_, err = doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &b, Group: true,
	ExtGStates: map[object.Name]object.Object{"Fade": fade},
})
```

## A PDF/A document

Start from `pdf0.NewPDFADocument` with the level, instead of `NewDocument`, and
write with `Save`. The document starts with what the level requires that a
drawing does not supply: the XMP identification of the level and an sRGB output
intent. `pdf0.NewPDFADocumentWithInfo` adds a title and author;
`pdf0.NewPDFADocumentWith` takes your own output intent profile.

`Save` writes the document to memory, reads it back, validates it at the level
it claims — the file-structure rules included, which judge bytes and so cannot
be checked on a document that has not been written — and copies it to `w` only
if it passed. A failed `Save` writes nothing, and its error says why:

```go
doc, err := pdf0.NewPDFADocument(pdfa.PDFA4)
if err != nil {
	return err
}
// ... AddPage, with every font embedded ...

err = doc.Save(w)
var nonconforming *pdf0.ConformanceError
var incomplete *pdf0.IncompleteCheckError
switch {
case errors.As(err, &nonconforming):
	for _, v := range nonconforming.Violations {
		fmt.Println(v) // what to fix; nothing was written
	}
case errors.As(err, &incomplete):
	// No violation found, but a check could not finish; nothing was written.
}
return err
```

What a drawing must do differently for PDF/A:

- **Embed every font.** A standard face (`fonts.Standard`) is never embedded,
  so `Save` refuses a PDF/A document that uses one. Use `fonts.NotoSans` or a
  font you load, through `Page.Faces`; [text.md](text.md#pdfa) has the
  PDF/A-1 details.
- **Draw in the output intent's colour.** The intent `NewPDFADocument` adds is
  sRGB, so `SetRGB` and `SetGray` are fine and `SetCMYK` is refused (rule
  6.2.4.3).
- **Describe it with `SetDocumentInfo`**, which keeps the information
  dictionary and the XMP in agreement, as PDF/A requires.

An empty violation list means no implemented rule fired, not that the whole of
ISO 19005 was checked ([validators.md](validators.md#what-an-empty-result-means)).
`examples/simple_pdfa` saves a page with text at PDF/A-4, and
`examples/flavored` saves one page as plain PDF, PDF/A-2b and PDF/A-4 and
prints what each `Save` did.

## Factur-X and Order-X invoices

A Factur-X invoice is a PDF/A-3 document carrying the invoice XML as an
attachment. Build the visible invoice at `pdfa.PDFA3b` as above, then call
`pdf0.EmbedFacturX` with the CII XML and its profile (or `pdf0.EmbedOrderX` for
an order), and `Save`. `Save` checks the PDF/A-3 container; the invoice's own
rules are checked by `pdf0.ValidateFacturX` on the document read back.
[validators.md](validators.md#factur-x-and-order-x) describes what that
validates.

## When AddPage says no

`AddPage`, `AddForm` and `AddTilingPattern` check before they change the
document, so a refused call leaves it as it was. They refuse:

- a page with no `Content`;
- a drawing the builder recorded a mistake in — an unbalanced `Save`, a text
  operator outside `BeginText`, a colour set inside a path, a number that is
  not finite — with that first mistake;
- a name the drawing used that no map on the page defines;
- a page size that is not a positive number, or a `Rotate` that is not a
  multiple of 90;
- a link with both or neither of a URI and a page, a URI whose scheme is not
  one of those allowed, or a page that is not yet in the document;
- any page added to a document that was read encrypted and could not be
  decrypted (`Document.Locked`).
