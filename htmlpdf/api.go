package htmlpdf

import (
	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/shape"
)

// The names a caller needs to render HTML, gathered here so that the common
// case takes one import.
//
// Everything below is declared in github.com/mgilbir/forme/layout, which is
// where the engine lives and where these belong: a box tree, a stylesheet and a
// sheet of paper are not PDF's ideas. But a caller whose whole business is
// "turn this page into a PDF" should not have to know that, or import two
// modules to write one call, so the handful of names that call needs are
// aliased into the package that owns the call.
//
// Aliases and not wrappers. These are the same types, so a value made here can
// be passed to layout and back with no conversion, and a caller that outgrows
// this list — a resource resolver, a font set, a severity policy, the display
// list itself — imports layout and finds everything already fits. This is a
// shortcut through the door, not a second door.
//
// Options is the exception, and is this package's own type. It used to be
// layout.Options too, which put forme's description of a backend — what it can
// draw — in the hands of a caller who is not the backend, and left nowhere to
// say what the caller wants of the PDF. See Options.
type (
	// Input is the document: HTML, stylesheets, and where to find what they
	// refer to. See layout.Input for the fields this does not mention.
	Input = layout.Input
	// Stylesheet is one author stylesheet.
	Stylesheet = layout.Stylesheet
	// PageSize is a sheet and its margins.
	PageSize = layout.PageSize
	// Finding is one thing the engine has to say about the document.
	Finding = layout.Finding
	// Size is a width and a height in the engine's own units.
	Size = layout.Size
	// Severity is how much a Finding matters. Error is the one that refuses a
	// document; see RefusedError.
	Severity = layout.Severity
	// RunLimits bounds the shaping RenderContext lets a document cost.
	RunLimits = shape.RunLimits
)

// ErrRunLimit is what RenderContext fails with, wrapped, when a document's
// shaping goes past its RunLimits.
var ErrRunLimit = shape.ErrRunLimit

// The severities, so that a caller reading Finding.Severity or writing a
// Policy does not have to reach past this package for the constants.
const (
	Ignore = layout.Ignore
	Warn   = layout.Warn
	Error  = layout.Error
)

// PageSizePt builds a sheet from a width and height in points, which is the
// unit page sizes are published in and the one a PDF records.
func PageSizePt(w, h float64) PageSize { return layout.PageSizePt(w, h) }

// The sheets with names. Each carries a margin already — a page size with no
// margin sets text to the paper's edge, which nobody wants and everybody would
// then have to write out.
var (
	A4     = layout.A4
	A5     = layout.A5
	Letter = layout.Letter
)

// Options is the sheet, the thresholds under which a document is refused rather
// than produced badly, and what the caller asks of the PDF.
//
// The first four are forme's and mean what they mean in layout.Options, to
// which they are passed unchanged. What is not here is the rest of
// layout.Options, which describes the backend rather than the document — that
// this one draws a TransformGroup, for one — and is this package's to state,
// not a caller's to get wrong (see layoutOptions).
//
// EmbedAllFonts is the caller's. It is a fact about the file: forme lays the
// page out in faces and knows nothing of which of them a PDF can name without
// carrying, which is the only thing that separates the two kinds of face.
type Options struct {
	// Page is the sheet. The zero value is A4 with a 20mm margin.
	Page PageSize
	// MinScale is the floor under scale-to-fit. A document that had to be
	// shrunk past it is refused rather than produced illegibly. Zero uses
	// forme's default of 0.5.
	MinScale float64
	// MinFontSizePt is the floor under an effective font size, in points. Zero
	// uses forme's default of 6.
	MinFontSizePt float64
	// AllowScaleUp lets an underfull page be enlarged to fill the sheet. It is
	// off by default because it is surprising and it degrades images.
	AllowScaleUp bool

	// EmbedAllFonts makes every font the page shows one embedded in the file:
	// none of the fourteen standard fonts, which a PDF names and a reader
	// supplies. A document that must render the same everywhere, or that a
	// standard forbids them in (PDF/A, ISO 19005-2 6.2.11.4.1), wants it.
	//
	// The page is laid out in the faces Input.Fonts holds and the ones the
	// document brings with @font-face, and in nothing else. Left nil,
	// Input.Fonts would be forme's standard set, so with this on a nil set is
	// an empty one instead: a document that brings its own faces renders, and
	// one that asks for a family nothing supplies is refused, by forme's
	// no-face rule. A family a non-empty set lacks is set in the set's own
	// default and reported under font-fallback, as it always is; the font
	// shown is embedded either way. github.com/mgilbir/forme/fonts/notosans
	// is a face that needs no file.
	//
	// A standard face a caller's own Input.Fonts returns is refused when the
	// page is written, under RuleStandardFont, which Input.Policy does not
	// lower: a caller who asked for every font to be embedded has said what
	// a page that names one is worth.
	EmbedAllFonts bool
}

// layoutOptions is the options forme composes under: the caller's sheet and
// thresholds, and what this backend draws. TransformGroups is set because this
// backend draws a TransformGroup, so a transform at any angle is drawn through
// its matrix rather than reported and drawn untransformed; it is a fact about
// the backend, not a choice about the document.
//
// Every field of layout.Options is either set here from Options or stated here
// for the backend; TestEveryLayoutOptionIsDecided holds the list, so a field a
// newer forme adds is a test failure until it is decided rather than a zero
// value nobody chose.
func (o Options) layoutOptions() layout.Options {
	return layout.Options{
		Page:            o.Page,
		MinScale:        o.MinScale,
		MinFontSizePt:   o.MinFontSizePt,
		AllowScaleUp:    o.AllowScaleUp,
		TransformGroups: true,
	}
}

// layoutInput is the input forme composes: the caller's, with an empty font
// set in place of a nil one when every font is to be embedded, since forme
// reads a nil set as its standard fonts.
func (o Options) layoutInput(in Input) Input {
	if o.EmbedAllFonts && in.Fonts == nil {
		in.Fonts = noFaces{}
	}
	return in
}

// noFaces is a font set with no face in it, which is not the same as no font
// set: forme lays a document out in its standard fonts when given none, and in
// only the document's own @font-face faces when given this.
type noFaces struct{}

func (noFaces) Face(string, bool, bool) (*shape.Face, bool) { return nil, false }
