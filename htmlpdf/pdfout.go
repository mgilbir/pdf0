// Package htmlpdf turns HTML and CSS into a PDF.
//
// Three inputs — a document, a stylesheet, a sheet of paper — and Render gives
// back a document, or says why it would not. See docs/htmlpdf.md and
// examples/html_to_pdf.
//
// Very little of that work happens here. The HTML parser, the CSS cascade, the
// box model, floats, tables, line breaking and the bidirectional algorithm are
// github.com/mgilbir/forme, which has no idea what a PDF is and is not the
// poorer for it. What arrives here is a display list — rectangles, runs of
// text, images, clips — and what leaves is a document.
//
// So Render is layout.Compose plus writePage, and the seam is exact: the step
// that turns a display list into a document is the only one that knows what a
// document is. A caller who wants the display list instead — to draw onto a
// canvas, to test, to write some other format — calls Compose and never imports
// this package.
//
// The package is named for the pair it joins rather than for the joining. It
// was called "render" while it held the engine as well, and that stopped being
// true and stopped being accurate on the same day: what is left renders
// nothing, it writes.
package htmlpdf

import (
	"fmt"
	"image"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
)

// From a display list to a PDF page.
//
// This is where the two conversions happen that everything upstream was written
// to defer, and they happen exactly once each:
//
//   - The origin flips. CSS puts it at the top left with y increasing
//     downwards; PDF puts it at the bottom left with y increasing upwards. A
//     coordinate system that changed halfway through an engine is one where
//     every sign error is plausible, so nothing above this file has ever seen
//     PDF's.
//   - layout.Layout units become points. CSS px is 1/96 inch and a PDF point is 1/72,
//     so the factor is exactly 0.75.
//
// Both fold into the single transform §5 asks for, together with the
// scale-to-fit factor. One "cm" at the top of the content stream, and the
// output stays vector: text remains selectable and searchable, and no image is
// resampled.

// Result is what a render produces, whether or not it produced a document.
//
// A struct rather than a bare document, because there is more to report than
// the bytes: what the engine had to say, how far it had to shrink the content,
// and how big the content wanted to be. Those are worth having when the render
// succeeded and worth having *most* when it did not, so Result is returned
// alongside an error rather than instead of one.
type Result struct {
	// Document is the finished PDF.
	//
	// Non-nil exactly when Render returned a nil error — see Render for why
	// that is worth stating. A caller that has checked the error does not need
	// to check this.
	Document *pdf0.Document

	// Scale is the factor of §5: 1 when the content fitted, less when it had to
	// be shrunk. It is reported because a caller may want to refuse a document
	// that only fitted by being made small.
	Scale float64

	// Findings is everything the guardrails raised, in a deterministic order —
	// or as much of it as the engine's reporting limit allowed, which Truncated
	// says.
	Findings []layout.Finding

	// Truncated is the report having been cut at that limit. A caller showing
	// findings to anyone has to say so: a cut list presented as a complete one
	// is how four hundred problems become three.
	Truncated bool

	// NaturalSize is what the content needed at its natural size, before any
	// scaling. It is what a caller adjusting a template needs to know.
	NaturalSize layout.Size
}

// Render lays a document out and writes it onto one PDF page.
// RefusedError is the engine declining to produce a document.
//
// It means a rule fired at Error severity: the content had to be shrunk past
// legibility to fit, or a face had no glyph for a character the page needs, or
// something else that would have produced a document nobody should ship. The
// page was laid out — that is how the rule fired — so the Result returned
// beside this says how far it got.
//
// It is an error and not a quiet nil because the caller asked for a document
// and has not got one, and Go has one place a caller looks for that.
// Distinguish it from a failure to write with errors.As:
//
//	out, err := htmlpdf.Render(in, opts)
//	var refused *htmlpdf.RefusedError
//	switch {
//	case errors.As(err, &refused):
//		// The document is wrong. refused.Findings says how.
//	case err != nil:
//		// Writing failed: out of disk, a broken io.Writer.
//	}
//
// A caller who does not care which just checks err, which is the point.
type RefusedError struct {
	// Findings is everything raised, in a deterministic order — the rules that
	// caused the refusal and any warning alongside them.
	//
	// It may not contain the finding that caused the refusal. The engine counts
	// a rule the moment it fires and only then tries to record it, so a
	// document that tripped enough rules to fill the report can be refused by
	// one the limit dropped. Truncated says when the list is partial.
	Findings []layout.Finding

	// Truncated is the report having been cut at the engine's limit, so
	// Findings is some of what was raised rather than all of it.
	Truncated bool
}

func (e *RefusedError) Error() string {
	var why []string
	for _, f := range e.Findings {
		if f.Severity == layout.Error {
			why = append(why, string(f.Rule)+": "+f.Message)
		}
	}
	switch len(why) {
	case 0:
		// Reachable, and not the "cannot happen" this first claimed. A rule
		// counts the moment it fires, before the report deduplicates and before
		// its bound cuts it — so the finding that refused the document may be
		// one the bound dropped, and then there is a refusal with nothing in
		// the list to explain it. Saying that is better than a bare refusal or
		// a guess.
		if e.Truncated {
			return "htmlpdf: refused to produce a document — the reason is not in " +
				"the findings, which were cut at the reporting limit"
		}
		return "htmlpdf: refused to produce a document"
	case 1:
		if e.Truncated {
			// One reason survived the bound, and there is no way to know how
			// many did not. Saying only the one presents a cut list as a
			// complete one, which is what the flag exists to prevent.
			return "htmlpdf: refused to produce a document — " + why[0] +
				", and more that were cut at the reporting limit"
		}
		return "htmlpdf: refused to produce a document — " + why[0]
	}
	if e.Truncated {
		// "and 499 more" is a count, and a count of a list that was cut is a
		// floor rather than a total: a document with two thousand problems
		// reports five hundred and reads as though it had five hundred.
		return fmt.Sprintf("htmlpdf: refused to produce a document — %s (and at "+
			"least %d more; the findings were cut at the reporting limit)",
			why[0], len(why)-1)
	}
	return fmt.Sprintf("htmlpdf: refused to produce a document — %s (and %d more)",
		why[0], len(why)-1)
}

// Render turns a document, its stylesheets and a sheet of paper into a PDF.
//
// The error is the only thing a caller has to check. It is non-nil exactly when
// Result.Document is nil, and there is no state where one says yes and the
// other no — so the ordinary shape works and cannot go wrong:
//
//	out, err := htmlpdf.Render(in, opts)
//	if err != nil {
//		return err
//	}
//	return out.Document.Write(w)
//
// It used to be otherwise. A document the engine refused came back as a nil
// Document with a nil error, on the reasoning that "this needs a three-point
// font to fit, so I have not made one" is not an I/O failure. That reasoning is
// sound and the shape it produced was not: the second check is not where anyone
// looks, and a caller who wrote the five lines above shipped a nil dereference.
// A refusal is now a RefusedError.
//
// Findings are on the Result either way. A document can be produced and still
// be worth complaining about — a property the engine does not implement, an
// image it was not allowed to load — and those are not refusals.
func Render(in layout.Input, opts layout.Options) (Result, error) {
	composed := layout.Compose(in, opts)

	out := Result{
		Scale:       composed.Scale,
		NaturalSize: composed.NaturalSize,
		Findings:    composed.Findings,
		Truncated:   composed.Truncated,
	}
	if composed.Refused {
		return out, &RefusedError{
			Findings:  composed.Findings,
			Truncated: composed.Truncated,
		}
	}

	doc, err := writePage(composed.Ops, pageOf(opts), composed.Scale)
	if err != nil {
		return out, err
	}
	out.Document = doc
	return out, nil
}

// pageOf is the sheet Compose used, which is the one to write.
//
// Compose fills in the default when the caller left it zero and does not hand
// the filled-in value back, so this repeats that one line rather than have two
// places disagree about what layout.A4 means.
func pageOf(opts layout.Options) layout.PageSize {
	if opts.Page.Width == 0 || opts.Page.Height == 0 {
		return layout.A4
	}
	return opts.Page
}

// writePage turns a display list into a one-page document.
//
// The document is made before the content stream rather than after, which is
// the one ordering constraint here: an image has to be written into the file as
// an object before the drawing can name it, and an object cannot be added to a
// document that does not exist yet. Fonts are the other way round — a face is
// subsetted to the glyphs it was asked to set, so it can only be embedded once
// the drawing is finished — which is why AddPage takes the faces and this
// passes the images.
func writePage(ops []layout.Op, page layout.PageSize, scale float64) (*pdf0.Document, error) {
	doc := pdf0.NewDocument()
	b := &content.Builder{}

	// The one transform. Reading it right to left: layout.layout units become points,
	// the y axis is inverted, the whole thing is scaled to fit, and the result
	// is placed inside the page's margin.
	//
	// A matrix [a b c d e f] maps (x, y) to (ax + cy + e, bx + dy + f). With
	// a = k and d = -k the x axis keeps its direction and the y axis reverses,
	// which is exactly the difference between the two coordinate systems.
	const pxToPt = 72.0 / 96.0
	k := pxToPt * scale
	tx := page.Margin.Left.Pt()
	ty := page.Height.Pt() - page.Margin.Top.Pt()
	b.Save()
	b.Concat(k, 0, 0, -k, tx, ty)

	// Keyed by the shaping face, which is what the display list carries, and
	// held as the embedding wrapper, which is what writing the document needs.
	// Adopt does not copy: the two are the same font, and each records the
	// glyphs the other used, which is what makes the subset come out right.
	faces := map[object.Name]*fonts.Face{}
	names := map[*shape.Face]object.Name{}
	xobjects := map[object.Name]object.Object{}
	patterns := map[object.Name]object.Object{}
	// Keyed by the source bytes rather than by the decoded image, so a logo
	// drawn on every row of a table is one image XObject in the file.
	imageNames := map[string]object.Name{}
	// embed puts a picture in the file once and returns the name the drawing
	// refers to it by.
	embed := func(img image.Image, key string) (object.Name, error) {
		if name, ok := imageNames[key]; ok {
			return name, nil
		}
		// images.Embed is the module's own encoder, and using it rather than
		// writing a second one is what keeps the two directions checking each
		// other: whatever it writes, the extraction side of that package reads
		// back, and the pixels have to survive the trip. It also brings its own
		// pixel cap.
		ref, err := images.Embed(doc, img)
		if err != nil {
			return "", fmt.Errorf("embedding an image: %w", err)
		}
		name := object.Name(fmt.Sprintf("Im%d", len(imageNames)+1))
		imageNames[key] = name
		xobjects[name] = ref
		return name, nil
	}

	for _, op := range ops {
		switch v := op.(type) {
		case layout.FillRect:
			if v.Rect.Empty() {
				continue
			}
			b.Save()
			b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
			// The rectangle is given in layout.layout units and the transform above
			// converts them, so the numbers written here are the layout.layout's own.
			b.Rect(v.Rect.X.Px(), v.Rect.Y.Px(), v.Rect.W.Px(), v.Rect.H.Px())
			b.Fill()
			b.Restore()

		case layout.DrawText:
			if v.Face == nil || v.Text == "" {
				continue
			}
			name, ok := names[v.Face]
			if !ok {
				name = object.Name(fmt.Sprintf("F%d", len(names)+1))
				names[v.Face] = name
				faces[name] = fonts.Adopt(v.Face)
			}
			face := faces[name]
			b.Save()
			clipTo(b, v.Clip)
			b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
			b.BeginText()
			b.SetFont(name, v.Size.Px())
			if v.CharSpacing != 0 {
				// Tc, in unscaled text space units — the same units the size above
				// is given in, since the text matrix below has no scale of its own.
				//
				// word-spacing needs no operator to go with it, and that is worth
				// stating rather than leaving as an omission. Tw applies only to
				// the single-byte code 32, so it would silently do nothing for a
				// composite face, and it is not needed anyway: line breaking
				// already makes every run of spaces an item of its own with a
				// position of its own, so the extra advance is spent between runs
				// rather than inside one, and no run of spaces shows ink for the
				// spread to be visible in.
				b.SetCharSpacing(v.CharSpacing.Px())
			}
			// The y axis is inverted by the transform, so text drawn through it
			// would be mirrored. The text matrix undoes that inversion locally,
			// which leaves the glyphs upright while the position still comes
			// from the flipped system.
			b.SetTextMatrix(1, 0, 0, -1, v.At.X.Px(), v.At.Y.Px())
			face.DrawShaped(b, layout.ShapedText(v), v.Size.Px())
			b.EndText()
			b.Restore()

		case layout.DrawImage:
			if v.Image == nil || v.Rect.Empty() {
				continue
			}
			name, err := embed(v.Image, v.Key)
			if err != nil {
				return nil, err
			}
			b.Save()
			clipTo(b, v.Clip)
			// An image XObject is painted into the unit square, so the matrix
			// *is* the placement. The negative vertical scale is not a flip: in
			// these coordinates y increases downwards, so the image's own
			// bottom edge — the one at v=0 — belongs at the rectangle's largest
			// y. Getting the sign wrong here draws the picture upside down
			// above the box rather than the right way up inside it.
			b.Concat(v.Rect.W.Px(), 0, 0, -v.Rect.H.Px(),
				v.Rect.X.Px(), v.Rect.Bottom().Px())
			b.Draw(name)
			b.Restore()

		case layout.TileImage:
			if v.Image == nil || v.Clip.Empty() || v.Tile.Empty() {
				continue
			}
			name, err := embed(v.Image, v.Key)
			if err != nil {
				return nil, err
			}
			cols, rows := v.Tiles()
			if cols <= 0 || rows <= 0 {
				continue
			}
			b.Save()
			b.Rect(v.Clip.X.Px(), v.Clip.Y.Px(), v.Clip.W.Px(), v.Clip.H.Px())
			b.Clip()
			b.EndPath()
			if cols == 1 && rows == 1 {
				// One tile, which is what "no-repeat" produces and what most
				// backgrounds are. A pattern for it would be a dictionary, a
				// stream and a resource to say what two operators already say.
				b.Concat(v.Tile.W.Px(), 0, 0, -v.Tile.H.Px(),
					v.Tile.X.Px(), v.Tile.Bottom().Px())
				b.Draw(name)
			} else {
				// A real tiling, drawn as PDF's own: one pattern object with a
				// step, painted over the clip in a single fill. The alternative
				// — a Do per tile — would put the tile count into the file, and
				// the tile count is the number this engine refuses to let a
				// stylesheet choose.
				pname := object.Name(fmt.Sprintf("Pt%d", len(patterns)+1))
				pattern, err := tilingPattern(doc, name, xobjects[name], v, k, tx, ty)
				if err != nil {
					return nil, err
				}
				patterns[pname] = pattern
				b.SetColorSpace("Pattern")
				b.SetPattern(pname)
				b.Rect(v.Clip.X.Px(), v.Clip.Y.Px(), v.Clip.W.Px(), v.Clip.H.Px())
				b.Fill()
			}
			b.Restore()
		}
	}
	b.Restore()

	if _, err := doc.AddPage(pdf0.Page{
		Width:    page.Width.Pt(),
		Height:   page.Height.Pt(),
		Content:  b,
		Faces:    faces,
		XObjects: xobjects,
		Patterns: patterns,
	}); err != nil {
		return nil, err
	}
	return doc, nil
}

// clipTo narrows the graphics state to a clip, if the operation carries one.
//
// It is called immediately after the Save that begins an operation and never
// anywhere else, which is what makes an unbalanced clip impossible rather than
// merely avoided: the clipping path is part of the graphics state, so the
// Restore that ends the operation takes it away, and there is no path through
// this file where one happens without the other. A display list cannot express
// a clip that outlives the mark it belongs to, so a hostile document cannot
// leave one open and blank the rest of the page.
//
// "W n" rather than "W f": the path sets the clip and is not painted. Emitting
// "W" without a path-painting operator afterwards is a malformed content
// stream, and "n" is the operator that means "no paint" — which is why the
// pair is written together here and not split across a helper.
func clipTo(b *content.Builder, c layout.Clip) {
	if !c.Active {
		return
	}
	b.Rect(c.Rect.X.Px(), c.Rect.Y.Px(), c.Rect.W.Px(), c.Rect.H.Px())
	b.Clip()
	b.EndPath()
}

// tilingPattern builds the PDF pattern that draws one background tiling.
//
// # Why a pattern rather than a Do per tile
//
// The tile count is (area / tile size) and a stylesheet chooses both ends of it.
// Writing one drawing operator per tile would put that number into the content
// stream, so a document could ask for a file of any size — and the check that
// stopped it would have to be a cap on the *output*, which is the wrong place: by
// then the work has been done. A pattern has the count nowhere in it. PDF's
// XStep and YStep say how far apart the cells are and the reader repeats them
// across whatever is filled, which is precisely the value the display list
// carries.
//
// # The matrix, which is the part that is easy to get wrong
//
// A pattern's /Matrix maps pattern space to the *default* coordinate space of
// the page — not to the space in force where the pattern is painted (ISO 32000-2
// 8.7.3.1). So the page transform this content stream set up with a "cm" does
// not apply to it, and has to be repeated here. That is what k, tx and ty are:
// the same numbers, so that pattern space is the layout.layout's own coordinate system,
// y downwards, and the cell below can be written in exactly the units every
// rectangle in the display list is in.
//
// Getting this wrong does not produce a blank page. It produces a background
// tiled at three quarters of the right size, in the wrong place, which looks like
// a layout.layout bug anywhere except here.
func tilingPattern(doc *pdf0.Document, name object.Name, ref object.Object, v layout.TileImage, k, tx, ty float64) (object.Object, error) {
	cell := &content.Builder{}
	cell.Save()
	// The same placement layout.DrawImage uses, in the same coordinates: an image
	// XObject fills the unit square, so the matrix is the position and the
	// negative vertical scale is what puts the picture the right way up in a
	// coordinate system whose y grows downwards.
	cell.Concat(v.Tile.W.Px(), 0, 0, -v.Tile.H.Px(),
		v.Tile.X.Px(), v.Tile.Bottom().Px())
	cell.Draw(name)
	cell.Restore()
	drawn, err := cell.Bytes()
	if err != nil {
		return nil, fmt.Errorf("building a background tile: %w", err)
	}

	// A pattern carries its own resources: the cell is a content stream of its
	// own, so the page's /XObject is not in scope for it.
	xobjects := &object.Dictionary{}
	xobjects.Set(name, ref)
	res := &object.Dictionary{}
	res.Set("XObject", xobjects)

	stream := &object.Stream{Dict: object.Dictionary{}, Data: drawn}
	stream.Dict.Set("Type", object.Name("Pattern"))
	stream.Dict.Set("PatternType", object.Integer(1))
	// PaintType 1 is a coloured pattern: the cell brings its own colour, which
	// an image does. PaintType 2 would take the colour from where it is painted
	// and leave an image undefined.
	stream.Dict.Set("PaintType", object.Integer(1))
	// TilingType 2 lets a reader distort the spacing by no more than a pixel to
	// keep the cells on the device grid, which is what stops a tiled background
	// showing seams at some zoom levels.
	stream.Dict.Set("TilingType", object.Integer(2))
	stream.Dict.Set("BBox", object.Array{
		numberOf(v.Tile.X.Px()), numberOf(v.Tile.Y.Px()),
		numberOf(v.Tile.Right().Px()), numberOf(v.Tile.Bottom().Px()),
	})
	stream.Dict.Set("XStep", numberOf(v.StepX.Px()))
	stream.Dict.Set("YStep", numberOf(v.StepY.Px()))
	stream.Dict.Set("Resources", res)
	stream.Dict.Set("Matrix", object.Array{
		numberOf(k), object.Integer(0), object.Integer(0), numberOf(-k),
		numberOf(tx), numberOf(ty),
	})
	stream.Dict.Set("Length", object.Integer(len(drawn)))
	// Indirect, because a stream cannot be a direct object in a dictionary.
	return doc.Add(stream), nil
}

// numberOf writes a value as an integer when it is one, which keeps the file
// readable and matches what every other producer emits.
func numberOf(v float64) object.Object {
	if v == float64(int64(v)) {
		return object.Integer(int(v))
	}
	return object.Real(v)
}
