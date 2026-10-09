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
	"context"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/paragraph"
	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
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

	// ShapingWork is the lookup work RenderContext charged the document's
	// shaping, for a caller keeping a budget across documents. Render
	// charges none and reports 0.
	ShapingWork int64
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
	return finish(layout.Compose(in, opts), in)
}

// RenderContext is Render for a document a caller does not control: the
// document is composed under ctx, and all the shaping it costs — every run of
// text, in every face, measured to break lines or shaped to be drawn — under
// one budget (forme's layout.ComposeContext). A zero field of limits is a
// default that grows with the text: no bound on a paragraph's length, glyphs
// in proportion to it, and 64 million units of work plus 1,024 a byte. A field
// set is a fixed bound.
//
// When the context is done or the budget runs out, it returns the context's
// error, or one wrapping ErrRunLimit, and a zero Result: no document, and no
// findings from a composition that stopped part way. Otherwise it returns
// what Render returns for the document, the same bytes, and the work charged
// in Result.ShapingWork.
func RenderContext(ctx context.Context, in layout.Input, opts layout.Options, limits RunLimits) (Result, error) {
	composed, err := layout.ComposeContext(ctx, in, opts, limits)
	if err != nil {
		return Result{}, err
	}
	out, err := finish(composed, in)
	out.ShapingWork = composed.ShapingWork
	return out, err
}

// finish checks a composition and writes it: the part of Render that follows
// laying the document out.
func finish(composed layout.Composed, in layout.Input) (Result, error) {
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
	// The first instance of each rule, and why: the links a document cannot
	// carry are usually all one kind — every relative reference in it — and
	// the first says which, as the first undrawable operation does.
	first := map[layout.Rule]string{}
	note := func(rule layout.Rule, why string) {
		if counts[rule] == 0 {
			first[rule] = why
		}
		counts[rule]++
	}
	// curved is whether the operations are inside a ClipPath.
	var visit func(ops []layout.Op, curved bool)
	visit = func(ops []layout.Op, curved bool) {
		for _, op := range ops {
			if why := undrawable(op); why != "" {
				note(RuleUndrawable, why)
			}
			switch v := op.(type) {
			case layout.DrawText:
				if !drawableTurn(v) {
					note(RuleVerticalText, "")
				}
			case layout.DrawTextShadow:
				if !drawableTurn(v.Run) {
					note(RuleVerticalText, "")
				}
			case layout.DrawEmphasisMark:
				if !drawableTurn(v.Mark) {
					note(RuleVerticalText, "")
				}
			case layout.DrawGlyphs:
			case layout.Link:
				if _, err := linkTarget(v.Href); err != nil {
					note(RuleLinkDropped, err.Error())
				} else if curved {
					// A link annotation's area is its /Rect, which cannot be
					// cut to a curve, so the link would reach past the
					// rounded clip it is inside. forme puts no link in one.
					note(RuleUndrawable, "a link inside a clip to a rounded shape, which a link "+
						"annotation's rectangle cannot follow")
				}
			case layout.ClipPath:
				visit(v.Ops, true)
			case layout.FilterGroup:
				visit(v.Ops, curved)
			case layout.TransformGroup:
				// The display list holds one only when the caller sets
				// Options.TransformGroups, which asks for what this backend
				// does not draw yet; with it off, layout draws what a page's
				// own rectangles can say and reports the rest.
				note(RuleUndrawable, "a transform drawn through a matrix (Options.TransformGroups), "+
					"which this backend does not draw yet")
			case layout.FillRect, layout.DrawImage, layout.TileImage, layout.FillPath, layout.FillGradient:
			default:
				note(RuleUnknownOp, "")
			}
		}
	}
	visit(c.Ops, false)
	firstLink, firstUndrawable := first[RuleLinkDropped], first[RuleUndrawable]

	messages := map[layout.Rule]string{
		RuleVerticalText: "%d run(s) of text are turned in a way layout does not turn " +
			"text (Anticlockwise or Upright without Sideways, or Upright with " +
			"Anticlockwise), which this backend does not guess the meaning of",
		RuleLinkDropped: "%d hyperlink(s) cannot be written as PDF links, so the page would " +
			"show their text with nothing to follow; the first: " + firstLink,
		RuleUnknownOp: "the display list has %d operation(s) of a kind this backend does not " +
			"know, which a newer layout engine added; the page would be missing them",
		RuleUndrawable: "%d operation(s) ask for what a PDF page cannot say as the display list " +
			"states it, so the page would be missing them; the first: " + firstUndrawable,
	}
	var (
		out     []layout.Finding
		refused bool
	)
	for _, rule := range []layout.Rule{RuleVerticalText, RuleLinkDropped, RuleUnknownOp, RuleUndrawable} {
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

// undrawable is why this backend cannot write an operation it knows as the
// display list states it, or "" when it can. checkDrawable reports what it
// says under RuleUndrawable, and the writer leaves out what it refused, so the
// two cannot disagree about which operations are drawn.
func undrawable(op layout.Op) string {
	switch v := op.(type) {
	case layout.DrawText:
		return squeezeUndrawable(v)
	case layout.FillPath:
		return pathUndrawable(v.Path)
	case layout.ClipPath:
		return pathUndrawable(v.Path)
	case layout.FillGradient:
		_, why, _ := planGradient(v)
		return why
	case layout.FilterGroup:
		return filterUndrawable(v)
	case layout.DrawTextShadow:
		if v.StdDev > 0 {
			return fmt.Sprintf("a text shadow blurred by a standard deviation of %gpx, which PDF has no "+
				"operation for", v.StdDev.Px())
		}
		return squeezeUndrawable(v.Run)
	case layout.DrawEmphasisMark:
		return squeezeUndrawable(v.Mark)
	case layout.DrawGlyphs:
		if v.Face == nil {
			return ""
		}
		if v.Face.IsSimple() || v.Face.IsStandard() {
			return fmt.Sprintf("glyphs drawn by index in %s, whose codes are characters and not glyph indices",
				v.Face.Name())
		}
		for _, g := range v.Glyphs {
			if g.GID < 0 || g.GID >= v.Face.NumGlyphs() {
				return fmt.Sprintf("glyph %d of %s, which has %d", g.GID, v.Face.Name(), v.Face.NumGlyphs())
			}
		}
	}
	return ""
}

// squeezeUndrawable is why a run's DrawText.WidthScale cannot be written, or
// "".
//
// The squeeze is PDF's horizontal scaling, Tz (ISO 32000-2 9.3.4), which
// scales text space's x axis: the glyphs' widths, and every displacement along
// the run with them. That is the direction a run across the page, or one
// turned sideways, advances in, and the one forme squeezes. It is not the
// direction an upright run advances in — the pen goes down the page by the
// vertical advances, which Tz leaves alone, and the glyphs would be narrowed
// across the line instead — so a squeezed upright run is refused. forme makes
// none (only a text-combine-upright composition is squeezed, and it is a
// horizontal run), and a scale that is not a positive number squeezes nothing
// a page can show.
func squeezeUndrawable(v layout.DrawText) string {
	switch {
	case v.WidthScale == 0:
		return ""
	case !(v.WidthScale > 0) || math.IsInf(v.WidthScale, 0):
		return fmt.Sprintf("a run of text squeezed by %g (DrawText.WidthScale), which is no width", v.WidthScale)
	case v.Upright:
		return "a run of text set upright and squeezed across its advance (DrawText.WidthScale), " +
			"which PDF's horizontal scaling (Tz) cannot say: it scales across the page, and an " +
			"upright run advances down it"
	}
	return ""
}

// textMatrix is the linear part of a run's text matrix: where the text
// space's x axis (the advance) and y axis (up the glyph) point in the
// layout's coordinates, in which y grows down the page.
//
// A run across the page advances along +x with its glyphs' up along -y; the
// matrix [1 0 0 -1] undoes the page transform's inversion locally, which
// leaves the glyphs upright while the position still comes from the flipped
// system.
//
// A sideways run is the same run turned a quarter, which is all "sideways"
// means (DrawText.Sideways, CSS Writing Modes 5.1): each glyph is its
// horizontal self, the advance and the marks' offsets are the horizontal
// ones, and the only change is the direction the text space points. Turned
// clockwise, as vertical-rl, vertical-lr and sideways-rl set it, the advance
// goes down the page (+y) and a glyph's up points right (+x): [0 1 1 0].
// Turned anticlockwise, as sideways-lr sets it, the advance goes up the page
// (-y) and up points left (-x): [0 -1 -1 0]. Both are forme's placeRun, which
// is how layout placed the run's ink and decorations. Everything the drawing
// writes in text space — the TJ displacements, a mark's rise — turns with it.
//
// An upright run is not a turned horizontal run: its glyphs stand as they do
// in the font, and uprightGlyphs says where each goes.
func textMatrix(v layout.DrawText) (a, b, c, d float64) {
	switch {
	case v.Upright:
		// The glyphs stand as they do in the font, whichever way the line
		// runs: DrawUpright moves the pen down the page itself.
		return 1, 0, 0, -1
	case v.Sideways && v.Anticlockwise:
		return 0, -1, -1, 0
	case v.Sideways:
		return 0, 1, 1, 0
	}
	return 1, 0, 0, -1
}

// drawableTurn reports whether a run's turn is one this backend draws: across
// the page, turned a quarter either way with its glyphs turned too, or set
// upright down a line turned clockwise. A combination forme does not make —
// Anticlockwise or Upright without Sideways, or Upright with Anticlockwise,
// which CSS Writing Modes 5.1 rules out, since sideways-lr turns every
// character — is not.
func drawableTurn(v layout.DrawText) bool {
	switch {
	case v.Anticlockwise && !v.Sideways:
		return false
	case v.Upright && (!v.Sideways || v.Anticlockwise):
		return false
	}
	return true
}

// uprightGlyphs is a run set upright as layout measured it, with its
// letter-spacing down the run.
//
// layout.ShapedGlyphs shapes an upright run as layout measured it (forme
// 5a6c5b6): the run's own text, with the vertical rules and metrics
// (shape.Features.Vertical) and its context either side, each glyph with its
// vertical advance and the point it is hung from. Where the face states
// vertical metrics (its vmtx, see shape.Face.StatesVerticalMetrics) those are
// the advances. Where it states none, the advances are the em a character CSS
// Writing Modes 4.4 synthesizes, which is how layout measured the run, given to
// the first glyph of each character's cluster; each glyph is still hung where
// shaping hangs it. DrawText.Upright states that a backend stepping its pen
// down by -YAdvance draws the glyphs where layout placed them, so that is what
// is drawn.
//
// This used to move the glyphs onto the em boxes itself, centring the cell
// shaping gave a character (the height of the face's line) on its em box,
// because layout.ShapedGlyphs shaped an upright run as a horizontal one. It
// no longer does, and a second placement here would draw the run somewhere
// layout did not put it.
//
// The text is the run's own and not layout.ShapedText's: an upright run is set
// in the order it is written (CSS Writing Modes 5.1 treats its characters as
// strong left-to-right), and its glyphs' clusters are offsets into v.Text.
func uprightGlyphs(v layout.DrawText) []shape.Glyph {
	glyphs, _ := layout.ShapedGlyphs(v)
	return withLetterSpacing(glyphs, v.Text, v)
}

// linkTarget is the URI a display-list link is written with, or why it
// cannot be one.
//
// forme makes a Link only of an http, https or mailto URL or of a reference
// with no scheme, and reports every other href itself (layout.RuleLinkRefused).
// A relative href is resolved by forme where the document has a <base href>
// with an http or https URL (HTML 4.2.3, RFC 3986 5.2), fragments included, so
// it arrives as a URL. The URLs go through pdf0.LinkURI, the rule every link
// annotation goes through: an allowlist of schemes, the URL standard's
// normalisation, and 7-bit percent-encoding. A reference that arrives with no
// scheme cannot be written correctly. It is relative to the HTML document,
// whose address this backend is never given (forme's Input has none, and a
// base that is a path leaves it relative), and a PDF reader resolves a
// relative /URI against the PDF's own location (ISO 32000-2 12.6.4.8), which
// is another place. A fragment names an element of the HTML document, and the
// display list does not say where on the page that element is, so there is no
// destination to write.
func linkTarget(href string) (string, error) {
	if u, err := url.Parse(href); err == nil && u.Scheme == "" {
		if strings.HasPrefix(href, "#") {
			return "", fmt.Errorf("%q is a fragment of the HTML document, and the display "+
				"list does not say where on the page its target is", href)
		}
		return "", fmt.Errorf("%q is relative to the HTML document, whose address this "+
			"backend is not given (a <base href> with an http or https URL resolves it); "+
			"a PDF reader would resolve it against the PDF's own", href)
	}
	return pdf0.LinkURI(href)
}

// pageTransform is the one transform of writePage, as a function: layout
// units, y down, to page space in points, y up. A link annotation's /Rect is
// in the page's default coordinates and is not drawn through the content
// stream's "cm", so it is put through the same numbers here.
type pageTransform struct{ k, tx, ty float64 }

// rect is a layout rectangle in page space, as [xMin yMin xMax yMax].
func (m pageTransform) rect(r layout.Rect) [4]float64 {
	return [4]float64{
		m.tx + m.k*r.X.Px(), m.ty - m.k*r.Bottom().Px(),
		m.tx + m.k*r.Right().Px(), m.ty - m.k*r.Y.Px(),
	}
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
		"Sideways":      "the text matrix turned a quarter clockwise: textMatrix",
		"Anticlockwise": "the text matrix turned a quarter anticlockwise: textMatrix",
		"Upright":       "shaped by layout.ShapedGlyphs with the vertical metrics, an em a character where the face states none, and drawn in the face's vertical form, each glyph hung from its vertical origin: uprightGlyphs, fonts.Face.DrawUpright; refused without Sideways or with Anticlockwise: RuleVerticalText",
		"Face":          "the font, adopted and embedded",
		"Size":          "the font size",
		"Color":         "the fill colour; alpha through an ExtGState, and invisible text at zero",
		"PreContext":    "through layout.ShapedGlyphs",
		"PostContext":   "through layout.ShapedGlyphs",
		"MergePre":      "through layout.ShapedGlyphs",
		"MergePost":     "through layout.ShapedGlyphs",
		"ContextKerns":  "through layout.ShapedGlyphs",
		"Features":      "through layout.ShapedGlyphs, with Vertical set for an upright run",
		"CharSpacing":   "added after each typographic character unit: withLetterSpacing",
		"WidthScale":    "horizontal scaling, Tz at a hundred times it, with the letter-spacing unsqueezed for it: withLetterSpacing; refused on an upright run: squeezeUndrawable",
		"Clip":          "clipTo",
	},
	"DrawImage": {
		"Rect":  "the placement matrix",
		"Image": "embedded through images.Embed",
		"Key":   "one image XObject per key",
		"Clip":  "clipTo",
	},
	"Link": {
		"Rects": "one link annotation per area, through the page transform",
		"Href": "the annotation's URI action, through pdf0.LinkURI; a relative reference, " +
			"a fragment or a URI the builder refuses is refused: RuleLinkDropped",
	},
	"TileImage": {
		"Clip":  "the area painted",
		"Tile":  "the first cell",
		"StepX": "the pattern's /XStep",
		"StepY": "the pattern's /YStep",
		"Image": "embedded through images.Embed",
		"Key":   "one image XObject per key",
	},
	"FillGradient": {
		"Clip":     "the area painted, a clip",
		"Tile":     "the first tile: the gradient clipped to it, or a tiling pattern's cell",
		"StepX":    "the pattern's /XStep",
		"StepY":    "the pattern's /YStep",
		"Gradient": "an axial (linear), radial (radial, under a matrix for the ellipse) or function-based (conic) shading; its colour a stitching function of type 2 pieces, type 4 where premultiplied alpha needs it, and its alpha a constant /ca or a luminosity soft mask: gradient.go; refused past maxGradientPieces or maxConicPieces, or when not a number: planGradient",
		"Overhang": "read by layout's page-overflow guard; it says nothing about painting",
	},
	"FillPath": {
		"Path":     "the path, arcs as cubic Béziers of at most 45°, filled by the even-odd rule (f*): pathTo; refused for an arc that is no number or sweeps past a turn: pathUndrawable",
		"Color":    "the fill colour; alpha through an ExtGState, and nothing painted at zero",
		"Clip":     "clipTo",
		"Overhang": "read by layout's page-overflow guard; it says nothing about painting",
	},
	"ClipPath": {
		"Path": "the clip, by the even-odd rule (W* n), around a Save and Restore: pathTo; refused as FillPath's is",
		"Ops":  "drawn inside the clip; a link among them is refused, since its rectangle cannot follow the curve",
	},
	"FilterGroup": {
		"Filters": "each a transparency group over the step before: opacity at /ca, a sharp drop shadow as a fill through an alpha soft mask of the group at the offset, under it; a blur, a blurred drop shadow and a colour matrix are refused: filterUndrawable",
		"Ops":     "drawn into a form XObject, an isolated transparency group",
		"Clip":    "clipTo, around the filtered result",
	},
	"DrawTextShadow": {
		"Run":    "drawn as a DrawText is, every field of it as DrawText's list says, as an artifact inside an empty /ActualText: canvas.notText",
		"StdDev": "a sharp shadow at zero; a blurred one is refused: undrawable",
	},
	"DrawEmphasisMark": {
		"Mark": "drawn as a DrawText is, every field of it as DrawText's list says, as an artifact inside an empty /ActualText: canvas.notText",
	},
	"TransformGroup": {
		"Matrix": "refused, the group and all inside it: checkDrawable; layout makes none unless Options.TransformGroups is set",
		"Ops":    "refused with the group",
		"Clip":   "refused with the group",
	},
	"DrawGlyphs": {
		"At":     "the origin of the text matrix",
		"Text":   "the one /ActualText the glyphs stand for, and what their ToUnicode entries are written from; an artifact when empty",
		"Glyphs": "drawn through fonts.Face.DrawReplaced, each at its offsets and advance; refused past the face's glyphs",
		"Face":   "the font, adopted and embedded; refused for a simple or standard face, whose codes are characters",
		"Size":   "the font size",
		"Color":  "the fill colour; alpha through an ExtGState, and invisible at zero",
		"Clip":   "clipTo",
	},
}

// alphaStates is the ExtGStates a page's translucent marks select, one per
// distinct alpha.
type alphaStates struct {
	states map[object.Name]object.Object
	byA    map[float64]object.Name
	// onUse is told each time a translucent state is selected, so that the
	// page knows it needs a transparency group.
	onUse func()
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
	if a.onUse != nil {
		a.onUse()
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
// its em, so it is scaled by the size the run is set at. It goes along the
// run's advance: across for a run across the page or turned sideways, down
// for an upright one.
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
	if v.WidthScale > 0 && !v.Upright {
		// Horizontal scaling (Tz) scales every displacement along the run,
		// and the spacing is written as one. forme squeezes the glyphs and
		// their advances and offsets, and not the spacing (DrawText.WidthScale;
		// its reference drawing adds CharSpacing unscaled), so it is written
		// unsqueezed here for Tz to squeeze back to what layout measured.
		perUnit /= v.WidthScale
	}
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
		if v.Upright {
			// Down the page, which a vertical advance states as negative.
			out[i].YAdvance -= perUnit * float64(units)
		} else {
			out[i].XAdvance += perUnit * float64(units)
		}
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

// tilingPattern builds the PDF pattern that draws one background tiling of a
// picture.
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
func tilingPattern(doc *pdf0.Document, name object.Name, ref object.Object, v layout.TileImage, base [6]float64) (object.Object, error) {
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

	// A pattern carries its own resources: the cell is a content stream of its
	// own, so the page's /XObject is not in scope for it.
	xobjects := &object.Dictionary{}
	xobjects.Set(name, ref)
	res := &object.Dictionary{}
	res.Set("XObject", xobjects)
	return tiling(doc, cell, res, v.Tile, v.StepX.Px(), v.StepY.Px(), base)
}

// tiling writes a coloured tiling pattern whose cell is the tile, drawn by
// cell with the resources res, repeated every stepX and stepY.
//
// # The matrix, which is the part that is easy to get wrong
//
// A pattern's /Matrix maps pattern space to the *default* coordinate space of
// the content stream that paints with it — the page's, or a form's — not to the
// space in force where the pattern is painted (ISO 32000-2 8.7.3.1). So the page
// transform the page's content stream set up with a "cm" does not apply to it,
// and has to be repeated: base is that transform, so that pattern space is
// layout's own coordinate system, y downwards, and the cell can be written in
// exactly the units every rectangle in the display list is in. Inside a form,
// whose own space is already layout's (see canvas), base is the identity.
//
// Getting this wrong does not produce a blank page. It produces a background
// tiled at three quarters of the right size, in the wrong place, which looks like
// a layout bug anywhere except here.
func tiling(doc *pdf0.Document, cell *content.Builder, res *object.Dictionary, tile layout.Rect, stepX, stepY float64, base [6]float64) (object.Object, error) {
	drawn, err := cell.Bytes()
	if err != nil {
		return nil, fmt.Errorf("building a background tile: %w", err)
	}
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
		numberOf(tile.X.Px()), numberOf(tile.Y.Px()),
		numberOf(tile.Right().Px()), numberOf(tile.Bottom().Px()),
	})
	stream.Dict.Set("XStep", numberOf(stepX))
	stream.Dict.Set("YStep", numberOf(stepY))
	stream.Dict.Set("Resources", res)
	m := make(object.Array, 6)
	for i, v := range base {
		m[i] = numberOf(v)
	}
	stream.Dict.Set("Matrix", m)
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
