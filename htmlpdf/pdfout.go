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
	"sort"

	"github.com/mgilbir/forme/html"
	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/paragraph"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/core"
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

// RefusedError is the engine, or this backend, declining to produce a document.
//
// It means a rule fired at Error severity: the content had to be shrunk past
// legibility to fit, or a face had no glyph for a character the page needs, or
// the page asks for something this backend cannot draw (see RuleVerticalText
// and its siblings), or something else that would have produced a document
// nobody should ship. The page was laid out — that is how the rule fired — so
// the Result returned beside this says how far it got.
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
//		// Building the document failed: an image that could not be
//		// embedded, a font whose licence forbids embedding it.
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
//
// The document is one page: forme composes a document onto a single sheet,
// scaled to fit (see Result.Scale), and there is no pagination to write. The
// sheet is the one the document laid out on — the caller's, with the
// document's own @page rules applied.
func Render(in layout.Input, opts layout.Options) (Result, error) {
	composed := layout.Compose(in, opts)

	out := Result{
		Scale:       composed.Scale,
		NaturalSize: composed.NaturalSize,
		Findings:    composed.Findings,
		Truncated:   composed.Truncated,
	}
	// What the display list says that this backend cannot write, checked
	// before anything is written: a page that would say something else is
	// refused the way the engine refuses one, unless the caller's policy says
	// the loss is acceptable.
	backend, refused := checkDrawable(composed, in.Policy)
	out.Findings = append(out.Findings, backend...)
	if composed.Refused || refused {
		return out, &RefusedError{
			Findings:  out.Findings,
			Truncated: composed.Truncated,
		}
	}

	doc, err := writePage(composed.Ops, composed.Page, composed.Scale)
	if err != nil {
		return out, err
	}
	out.Document = doc
	return out, nil
}

// checkDrawable reads the display list and the box tree for what this backend
// cannot draw, and reports each at the severity the caller's policy gives it,
// Error by default. It returns the findings and whether any of them refuses
// the document.
//
// One finding per rule, however many operations raised it: a vertical page is
// one fact about the document, and a line per run would bury it.
func checkDrawable(c layout.Composed, policy layout.Policy) ([]layout.Finding, bool) {
	counts := map[layout.Rule]int{}
	for _, op := range c.Ops {
		switch v := op.(type) {
		case layout.DrawText:
			if v.Sideways || v.Anticlockwise || v.Upright {
				counts[RuleVerticalText]++
			}
		case layout.FillRect, layout.DrawImage, layout.TileImage:
		default:
			counts[RuleUnknownOp]++
		}
	}
	if c.Root != nil {
		counts[RuleLinkDropped] = countLinks(c.Root.Box)
	}

	messages := map[layout.Rule]string{
		RuleVerticalText: "%d run(s) of text are set down the page (a vertical writing-mode " +
			"or text-orientation: upright), which this PDF backend cannot draw; they would " +
			"be drawn across the page",
		RuleLinkDropped: "the document has %d hyperlink(s), and the display list carries no " +
			"links, so the PDF would show their text with nothing to follow",
		RuleUnknownOp: "the display list has %d operation(s) of a kind this backend does not " +
			"know, which a newer layout engine added; the page would be missing them",
	}
	var (
		out     []layout.Finding
		refused bool
	)
	for _, rule := range []layout.Rule{RuleVerticalText, RuleLinkDropped, RuleUnknownOp} {
		n := counts[rule]
		if n == 0 {
			continue
		}
		sev := layout.Error
		if s, ok := policy[rule]; ok {
			sev = s
		}
		if sev == layout.Ignore {
			continue
		}
		if sev == layout.Error {
			refused = true
		}
		out = append(out, layout.Finding{
			Rule: rule, Severity: sev,
			Message: fmt.Sprintf(messages[rule], n),
			Source:  layout.Source{HTMLOffset: -1, CSSOffset: -1},
		})
	}
	return out, refused
}

// countLinks counts the <a href> elements that generated a box, which are the
// links the page would have had: an element with display: none generates none
// and is not one.
//
// The box tree is walked with a stack rather than by recursion, because its
// depth is the document's nesting depth and a document is untrusted input.
func countLinks(root *layout.Box) int {
	if root == nil {
		return 0
	}
	n := 0
	seen := map[*html.Node]bool{}
	stack := []*layout.Box{root}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if el := b.Element; el != nil && !seen[el] && el.Type == html.ElementNode && el.Name == "a" {
			// One element can generate several boxes — an inline split
			// around a block, a continuation — and is still one link.
			seen[el] = true
			if el.HasAttr("href") {
				n++
			}
		}
		stack = append(stack, b.Children...)
	}
	return n
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
//
// Every field of every operation is either drawn here or refused by
// checkDrawable before this runs; drawnFields in this file lists which, and a
// test holds that list against the operations layout declares.
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
	alphas := newAlphaStates()
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
			// Overhang is about the page-overflow guard in layout and says
			// nothing about how the rectangle is painted.
			if v.Rect.Empty() || !(v.Color.A > 0) {
				continue // no area, or no ink: nothing is painted
			}
			b.Save()
			alphas.use(b, v.Color.A)
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
			alphas.use(b, v.Color.A)
			b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
			b.BeginText()
			b.SetFont(name, v.Size.Px())
			if !(v.Color.A > 0) {
				// Transparent text is not painted and is still text: CSS
				// "color: transparent" leaves it selectable, and so does this.
				b.SetTextRenderMode(content.InvisibleText)
			}
			// The y axis is inverted by the transform, so text drawn through it
			// would be mirrored. The text matrix undoes that inversion locally,
			// which leaves the glyphs upright while the position still comes
			// from the flipped system.
			b.SetTextMatrix(1, 0, 0, -1, v.At.X.Px(), v.At.Y.Px())
			// The glyphs layout measured, shaped with the run's direction, its
			// context either side and the features the document turned off —
			// layout.ShapedGlyphs is the pairing of all of them, and a backend
			// that reshapes the text alone draws a different run from the one
			// placed (a ligature measured as two letters, a kern turned back on,
			// an Arabic word in isolated forms).
			text := layout.ShapedText(v)
			glyphs, _ := layout.ShapedGlyphs(v)
			glyphs = withLetterSpacing(glyphs, text, v)
			face.Draw(b, text, glyphs, v.Size.Px())
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

		default:
			// checkDrawable refused this document or the caller's policy let
			// it through knowing the operation is not drawn.
		}
	}
	b.Restore()

	if _, err := doc.AddPage(pdf0.Page{
		Width:      page.Width.Pt(),
		Height:     page.Height.Pt(),
		Content:    b,
		Faces:      faces,
		XObjects:   xobjects,
		Patterns:   patterns,
		ExtGStates: alphas.states,
		// A translucent mark composites against whatever is behind it, and
		// without a page group what that is is left to the reader.
		Group: len(alphas.states) > 0,
	}); err != nil {
		return nil, err
	}
	return doc, nil
}

// drawnFields says, for every display-list operation, what this backend does
// with each of its fields: draws it, or refuses the document over it.
//
// It is here so that a field cannot be ignored by omission. forme's DrawText
// has grown a field for every thing a run of text can be — a direction, three
// kinds of vertical, a context either side, features turned off — and a
// backend that reads the ones it knew about draws the rest wrong without a
// word. TestEveryDrawOpFieldIsAccountedFor holds this list to the types
// themselves, so a field or an operation a newer forme adds fails the build's
// tests until this says what happens to it.
var drawnFields = map[string]map[string]string{
	"FillRect": {
		"Rect":     "the rectangle filled",
		"Color":    "the fill colour; alpha through an ExtGState, and nothing painted at zero",
		"Overhang": "read by layout's page-overflow guard; it says nothing about painting",
	},
	"DrawText": {
		"At":            "the origin of the text matrix",
		"Text":          "what the glyphs were shaped from and what the page extracts as",
		"RTL":           "through layout.ShapedText and layout.ShapedGlyphs",
		"Sideways":      "refused: RuleVerticalText",
		"Anticlockwise": "refused: RuleVerticalText",
		"Upright":       "refused: RuleVerticalText",
		"Face":          "the font, adopted and embedded",
		"Size":          "the font size",
		"Color":         "the fill colour; alpha through an ExtGState, and invisible text at zero",
		"PreContext":    "through layout.ShapedGlyphs",
		"PostContext":   "through layout.ShapedGlyphs",
		"MergePre":      "through layout.ShapedGlyphs",
		"MergePost":     "through layout.ShapedGlyphs",
		"ContextKerns":  "through layout.ShapedGlyphs",
		"Features":      "through layout.ShapedGlyphs",
		"CharSpacing":   "added after each typographic character unit: withLetterSpacing",
		"Clip":          "clipTo",
	},
	"DrawImage": {
		"Rect":  "the placement matrix",
		"Image": "embedded through images.Embed",
		"Key":   "one image XObject per key",
		"Clip":  "clipTo",
	},
	"TileImage": {
		"Clip":  "the area painted",
		"Tile":  "the first cell",
		"StepX": "the pattern's /XStep",
		"StepY": "the pattern's /YStep",
		"Image": "embedded through images.Embed",
		"Key":   "one image XObject per key",
	},
}

// alphaStates is the ExtGStates a page's translucent marks select, one per
// distinct alpha.
type alphaStates struct {
	states map[object.Name]object.Object
	byA    map[float64]object.Name
}

func newAlphaStates() *alphaStates {
	return &alphaStates{states: map[object.Name]object.Object{}, byA: map[float64]object.Name{}}
}

// use selects the state for an alpha, inside the Save the mark already opened,
// so the Restore that ends the mark takes it away again.
//
// The same alpha for filling and stroking (/ca and /CA): the display list's
// colour is the mark's, and every mark here is painted by filling, a glyph
// included — the stroke alpha is set so that a later stroked mark cannot
// inherit an opaque one by accident. An opaque colour selects nothing, which
// keeps a page with no transparency free of it, and so free of the PDF/A
// rules about it.
func (a *alphaStates) use(b *content.Builder, alpha float64) {
	if !(alpha > 0) || alpha >= 1 {
		return
	}
	name, ok := a.byA[alpha]
	if !ok {
		name = object.Name(fmt.Sprintf("GS%d", len(a.byA)+1))
		gs := &object.Dictionary{}
		gs.Set("Type", object.Name("ExtGState"))
		gs.Set("ca", object.Real(alpha))
		gs.Set("CA", object.Real(alpha))
		a.byA[alpha] = name
		a.states[name] = gs
	}
	b.SetExtGState(name)
}

// withLetterSpacing adds a run's letter-spacing to the glyphs it falls after.
//
// CSS Text §8.2 adds it after each typographic character unit, not after each
// glyph: a letter with a mark on it is one unit and two glyphs, and a ligature
// is one glyph and as many units as letters. So it goes on the last glyph of
// each shaping cluster, once for every unit that ends in the cluster's text —
// which is how layout measured the run, and how forme's own reference drawing
// places it. The Tc operator this used to set adds it after every glyph,
// which moved a mark off its letter by the spacing and made a run with
// ligatures narrower on the page than layout said it was.
//
// The spacing is in the run's units and a glyph's advance in thousandths of
// its em, so it is scaled by the size the run is set at.
func withLetterSpacing(glyphs []shape.Glyph, text string, v layout.DrawText) []shape.Glyph {
	if v.CharSpacing == 0 || len(glyphs) == 0 || !(v.Size.Px() > 0) {
		return glyphs
	}
	ends := paragraph.SpacingAfterOffsets(text)
	if len(ends) == 0 {
		return glyphs
	}
	// Each cluster's extent in the text, from its offset to the next one's.
	starts := make([]int, 0, len(glyphs))
	seen := map[int]bool{}
	for _, g := range glyphs {
		if !seen[g.Cluster] {
			seen[g.Cluster] = true
			starts = append(starts, g.Cluster)
		}
	}
	sort.Ints(starts)
	end := make(map[int]int, len(starts))
	for i, c := range starts {
		end[c] = len(text)
		if i+1 < len(starts) {
			end[c] = starts[i+1]
		}
	}
	perUnit := v.CharSpacing.Px() * 1000 / v.Size.Px()
	out := append([]shape.Glyph(nil), glyphs...)
	for i := range out {
		if i+1 < len(out) && out[i+1].Cluster == out[i].Cluster {
			continue // not the last glyph of its cluster
		}
		units := 0
		for at := range ends {
			if at >= out[i].Cluster && at < end[out[i].Cluster] {
				units++
			}
		}
		out[i].XAdvance += perUnit * float64(units)
	}
	return out
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

	// Flate-compressed like every other stream this module writes.
	compressed := core.FlateEncode(drawn)
	stream := object.NewStream(nil, compressed)
	stream.Dict.Set("Filter", object.Name("FlateDecode"))
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
	stream.Dict.Set("Length", object.Integer(len(compressed)))
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
