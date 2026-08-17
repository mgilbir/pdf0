package htmlpdf

import "github.com/mgilbir/forme/layout"

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
type (
	// Input is the document: HTML, stylesheets, and where to find what they
	// refer to. See layout.Input for the fields this does not mention.
	Input = layout.Input
	// Stylesheet is one author stylesheet.
	Stylesheet = layout.Stylesheet
	// Options is the sheet and the thresholds under which a document is
	// refused rather than produced badly.
	Options = layout.Options
	// PageSize is a sheet and its margins.
	PageSize = layout.PageSize
	// Finding is one thing the engine has to say about the document.
	Finding = layout.Finding
	// Size is a width and a height in the engine's own units.
	Size = layout.Size
	// Severity is how much a Finding matters. Error is the one that refuses a
	// document; see RefusedError.
	Severity = layout.Severity
)

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
