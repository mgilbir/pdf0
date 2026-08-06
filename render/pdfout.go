package render

import (
	"fmt"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/style"
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
//   - Layout units become points. CSS px is 1/96 inch and a PDF point is 1/72,
//     so the factor is exactly 0.75.
//
// Both fold into the single transform §5 asks for, together with the
// scale-to-fit factor. One "cm" at the top of the content stream, and the
// output stays vector: text remains selectable and searchable, and no image is
// resampled.

// PageSize is the sheet a document is laid out onto.
type PageSize struct {
	// Width and Height are the whole sheet.
	Width, Height style.Unit
	// Margin is the space left around the content.
	Margin Edges
}

// PageSizePt builds a page size from a width and height in points, which is how
// paper is conventionally measured.
func PageSizePt(w, h float64) PageSize {
	return PageSize{Width: ptToUnit(w), Height: ptToUnit(h)}
}

// WithMarginPt returns the page with a uniform margin in points.
func (p PageSize) WithMarginPt(m float64) PageSize {
	u := ptToUnit(m)
	p.Margin = Edges{Top: u, Right: u, Bottom: u, Left: u}
	return p
}

// The paper sizes a document generator is actually asked for.
var (
	A4     = PageSizePt(595.276, 841.89).WithMarginPt(56.7) // 20mm
	A5     = PageSizePt(419.528, 595.276).WithMarginPt(42.5)
	Letter = PageSizePt(612, 792).WithMarginPt(54) // 0.75in
	Legal  = PageSizePt(612, 1008).WithMarginPt(54)
)

// ptToUnit converts points to layout units: a point is 1/72 inch and a CSS
// pixel is 1/96, so a point is 4/3 of a pixel.
func ptToUnit(pt float64) style.Unit {
	u, _ := style.FromPx(pt * 96 / 72)
	return u
}

// Content is the area a document is laid out in: the sheet minus its margins.
func (p PageSize) Content() Size {
	return Size{
		W: p.Width.Sub(p.Margin.Horizontal()),
		H: p.Height.Sub(p.Margin.Vertical()),
	}
}

// Result is what a render produces.
//
// The shape follows the Factur-X precedent §6 names — a struct, because there is
// more to report than findings.
type Result struct {
	// Document is the finished PDF, and is nil when a rule fired at Error
	// severity. A caller that got findings and no document was told not to
	// render, rather than left to decide.
	Document *pdf0.Document

	// Scale is the factor of §5: 1 when the content fitted, less when it had to
	// be shrunk. It is reported because a caller may want to refuse a document
	// that only fitted by being made small.
	Scale float64

	// Findings is everything the guardrails raised, in a deterministic order.
	Findings []Finding

	// NaturalSize is what the content needed at its natural size, before any
	// scaling. It is what a caller adjusting a template needs to know.
	NaturalSize Size
}

// Options configure a render beyond the input itself.
type Options struct {
	// Page is the sheet. The zero value is A4 with a 20mm margin.
	Page PageSize
	// Fonts supplies the faces; nil uses the standard fourteen.
	Fonts FontSet
	// MinScale is the floor §6.1 puts under scale-to-fit. A document that had
	// to be shrunk past it is refused rather than produced illegibly. Zero uses
	// the default of 0.5.
	MinScale float64
	// MinFontSizePt is the floor under an effective font size, in points. Zero
	// uses the default of 6.
	MinFontSizePt float64
	// AllowScaleUp lets an underfull page be enlarged to fill the sheet. It is
	// off by default because it is surprising and it degrades images.
	AllowScaleUp bool
}

// Render lays a document out and writes it onto one PDF page.
func Render(in Input, opts Options) (Result, error) {
	if opts.Page.Width == 0 || opts.Page.Height == 0 {
		opts.Page = A4
	}
	if opts.MinScale == 0 {
		opts.MinScale = 0.5
	}
	if opts.MinFontSizePt == 0 {
		opts.MinFontSizePt = 6
	}

	built := Build(in)
	rec := NewRecorder(in.Policy)
	for _, f := range built.Findings {
		rec.ReportDetail(f)
	}

	avail := opts.Page.Content()
	root := Layout(built.Root, avail, opts.Fonts, rec)

	// The natural size is the far edge of the root's border box, not its margin
	// box, and the difference is not cosmetic. A block-level box resolves an
	// over-constrained width by widening its right margin, so the root's margin
	// box is *always* exactly the page width — measuring that would report every
	// document as needing precisely the space it was given, and scale-to-fit
	// would never fire. The border box's far edges include the root's own left
	// and top margins, since those move it, and exclude the one that was
	// invented to make the arithmetic add up.
	natural := Size{}
	if root != nil {
		natural = Size{W: root.BorderRect.Right(), H: root.BorderRect.Bottom()}
	}

	scale := fitScale(natural, avail, opts.AllowScaleUp)
	checkScale(rec, scale, opts.MinScale)
	checkFontSizes(rec, root, scale, opts.MinFontSizePt)

	ops := Paint(root)
	checkPageOverflow(rec, ops, avail, scale)

	out := Result{Scale: scale, NaturalSize: natural}
	if rec.Failed() {
		out.Findings = rec.Findings()
		return out, nil
	}

	doc, err := writePage(ops, opts.Page, scale)
	if err != nil {
		return out, err
	}
	out.Document = doc
	out.Findings = rec.Findings()
	return out, nil
}

// fitScale is §5's factor: one number, applied to everything.
//
// The proposal argues this at length and the argument decides the whole shape of
// the engine. Laying out again at a smaller size would reflow the text, which
// moves the line breaks, which changes the height — non-monotonically, since a
// smaller font can produce a *taller* block by breaking differently. Scaling the
// finished layout geometrically leaves every proportion as the author designed
// it, needs one pass, and makes the size of every element exactly its natural
// size times this number, so a threshold check is a multiplication rather than
// an iteration.
func fitScale(natural, avail Size, allowUp bool) float64 {
	s := 1.0
	if natural.W > 0 && natural.W > avail.W {
		s = min(s, avail.W.Px()/natural.W.Px())
	}
	if natural.H > 0 && natural.H > avail.H {
		s = min(s, avail.H.Px()/natural.H.Px())
	}
	if allowUp && natural.W > 0 && natural.H > 0 {
		up := min(avail.W.Px()/natural.W.Px(), avail.H.Px()/natural.H.Px())
		if up > s {
			s = up
		}
	}
	return s
}

// checkScale is the min-scale guardrail of §6.1.
//
// It is the blunt one and probably the most useful: if the content had to be
// shrunk past half to fit, the document is wrong, and no per-element threshold
// is needed to say so.
func checkScale(rec *Recorder, scale, floor float64) {
	if scale >= floor {
		return
	}
	rec.ReportDetail(Finding{
		Rule: RuleMinScale,
		Message: fmt.Sprintf(
			"the content had to be scaled to %.0f%% to fit the page, past the floor of %.0f%%",
			scale*100, floor*100),
	})
}

// checkFontSizes is the min-font-size guardrail of §6.1.
//
// Because the scale is geometric, the effective size of every element is exactly
// its natural size times the factor — so this is one multiplication per box,
// computed before anything is emitted, with no iteration and no possibility of a
// later pass invalidating it. That exactness is the whole reason §5 chose
// geometric scaling.
func checkFontSizes(rec *Recorder, root *Fragment, scale, floorPt float64) {
	if root == nil {
		return
	}
	seen := map[style.Unit]bool{}
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f.Box != nil && len(f.Lines) > 0 {
			size := f.Box.FontSize
			if !seen[size] {
				seen[size] = true
				effective := size.Mul(scale).Pt()
				if effective < floorPt {
					rec.ReportDetail(Finding{
						Rule: RuleMinFontSize,
						Message: fmt.Sprintf(
							"text would be set at %.2fpt, below the floor of %.2fpt"+
								" (%.2fpt before the page scaling of %.0f%%)",
							effective, floorPt, size.Pt(), scale*100),
						Path: PathOf(f.Box.Element),
					})
				}
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
}

// checkPageOverflow is the overflow-page guardrail of §6.2.
//
// It should never fire. The scale of §5 is computed so that everything fits, so
// this is a self-check on that computation as much as a guardrail on the
// document — which is exactly why it is worth having. A threshold that verifies
// an earlier calculation catches the case where the calculation was wrong, and
// that is a class of fault no amount of checking the document can reach.
//
// Content that overflows its own *box* is the other guardrail's business; this
// is only about leaving the page.
func checkPageOverflow(rec *Recorder, ops []Op, avail Size, scale float64) {
	page := Rect{W: avail.W.Div(scale), H: avail.H.Div(scale)}
	var worst Rect
	var found bool

	for _, op := range ops {
		r, ok := op.(FillRect)
		if !ok || r.Rect.Empty() {
			continue
		}
		if page.Contains(r.Rect) {
			continue
		}
		if !found || r.Rect.Right() > worst.Right() || r.Rect.Bottom() > worst.Bottom() {
			worst, found = r.Rect, true
		}
	}
	if !found {
		return
	}
	rec.ReportDetail(Finding{
		Rule: RuleOverflowPage,
		Message: fmt.Sprintf(
			"content reaches %.1f x %.1f px after scaling, outside the page's %.1f x %.1f; "+
				"the scale-to-fit calculation did not account for it",
			worst.Right().Px(), worst.Bottom().Px(), page.W.Px(), page.H.Px()),
	})
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
func writePage(ops []Op, page PageSize, scale float64) (*pdf0.Document, error) {
	doc := pdf0.NewDocument()
	b := &content.Builder{}

	// The one transform. Reading it right to left: layout units become points,
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

	faces := map[object.Name]*fonts.Face{}
	names := map[*fonts.Face]object.Name{}
	xobjects := map[object.Name]object.Object{}
	// Keyed by the source bytes rather than by the decoded image, so a logo
	// drawn on every row of a table is one image XObject in the file.
	imageNames := map[string]object.Name{}

	for _, op := range ops {
		switch v := op.(type) {
		case FillRect:
			if v.Rect.Empty() {
				continue
			}
			b.Save()
			b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
			// The rectangle is given in layout units and the transform above
			// converts them, so the numbers written here are the layout's own.
			b.Rect(v.Rect.X.Px(), v.Rect.Y.Px(), v.Rect.W.Px(), v.Rect.H.Px())
			b.Fill()
			b.Restore()

		case DrawText:
			if v.Face == nil || v.Text == "" {
				continue
			}
			name, ok := names[v.Face]
			if !ok {
				name = object.Name(fmt.Sprintf("F%d", len(names)+1))
				names[v.Face] = name
				faces[name] = v.Face
			}
			b.Save()
			b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
			b.BeginText()
			b.SetFont(name, v.Size.Px())
			// The y axis is inverted by the transform, so text drawn through it
			// would be mirrored. The text matrix undoes that inversion locally,
			// which leaves the glyphs upright while the position still comes
			// from the flipped system.
			b.SetTextMatrix(1, 0, 0, -1, v.At.X.Px(), v.At.Y.Px())
			v.Face.DrawShaped(b, v.Text, v.Size.Px())
			b.EndText()
			b.Restore()

		case DrawImage:
			if v.Image == nil || v.Rect.Empty() {
				continue
			}
			name, ok := imageNames[v.Key]
			if !ok {
				// images.Embed is the module's own encoder, and using it rather
				// than writing a second one is what keeps the two directions
				// checking each other: whatever it writes, the extraction side
				// of that package reads back, and the pixels have to survive the
				// trip. It also brings its own pixel cap.
				ref, err := images.Embed(doc, v.Image)
				if err != nil {
					return nil, fmt.Errorf("embedding an image: %w", err)
				}
				name = object.Name(fmt.Sprintf("Im%d", len(imageNames)+1))
				imageNames[v.Key] = name
				xobjects[name] = ref
			}
			b.Save()
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
		}
	}
	b.Restore()

	if _, err := doc.AddPage(pdf0.Page{
		Width:    page.Width.Pt(),
		Height:   page.Height.Pt(),
		Content:  b,
		Faces:    faces,
		XObjects: xobjects,
	}); err != nil {
		return nil, err
	}
	return doc, nil
}
