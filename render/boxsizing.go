package render

import (
	"strings"

	"github.com/mgilbir/pdf0/style"
)

// box-sizing: what a declared width and height are the width and height *of*.
//
// The initial value, "content-box", is CSS's original box model: "width: 200px;
// padding: 10px; border: 1px solid" occupies 222px. "border-box" makes the
// declared number the border box, so the same box occupies 200px and its content
// is 178px wide. The property was in the registry and unread, which meant every
// "box-sizing: border-box" — the first line of most stylesheets written in the
// last fifteen years — produced a layout wider than the author's by twice the
// padding, with no finding.
//
// # Why this is a conversion and not a branch
//
// Every declared value on an axis refers to the same box: width, min-width and
// max-width are all border-box values under "border-box", and all content-box
// values under "content-box". So the whole property is one subtraction applied
// at the boundary — turn a declared value into a content value by taking off the
// padding and the border — and nothing downstream has to know.
//
// The clamping is where a naive version goes wrong, and it goes wrong quietly.
// "box-sizing: border-box; width: 100px; min-width: 150px; padding: 20px" has a
// used border-box width of 150 and a content width of 110. An implementation
// that converted the width to a content value first and then clamped it against
// min-width would compare 60 against 150 and produce a content width of 150 — a
// border box of 190, forty pixels too wide, from an arithmetic slip that looks
// like a deliberate minimum. So clampWidth converts the *other* way: it puts the
// content value back into the declared space, clamps it there against the
// declared limits, and converts the result back.
//
// # Where it is not applied
//
// A table's column widths are resolved by §17.5.2's own algorithm, which reads
// the declared widths of its cells and columns directly rather than through the
// helpers here. box-sizing on a table box is therefore reported rather than
// applied — see checkTableBoxSizing.

// borderBoxSizing reports whether a box's declared sizes include its padding and
// border.
func borderBoxSizing(b *Box) bool {
	return strings.EqualFold(strings.TrimSpace(b.Style["box-sizing"]), "border-box")
}

// sizingInset is what a declared width or height covers besides the content: the
// padding and the border on each axis, or nothing at all under "content-box".
//
// The edges are resolved here rather than passed in because the callers that
// need this are spread across four files and three of them have already
// computed the same numbers — threading them through every signature would be a
// change to a dozen call sites to save a handful of map lookups on the boxes that
// use the property at all. Under "content-box", which is nearly every box in
// nearly every document, this returns before resolving anything.
func (l *layouter) sizingInset(b *Box, containing style.Unit) (horizontal, vertical style.Unit) {
	if !borderBoxSizing(b) {
		return 0, 0
	}
	border := l.borderWidths(b)
	padding := l.paddingOf(b, containing)
	return border.Horizontal().Add(padding.Horizontal()),
		border.Vertical().Add(padding.Vertical())
}

// checkTableBoxSizing reports a border-box declaration on a table box, which the
// table algorithm does not read.
//
// It is a value this engine understood and did not apply, which is precisely
// what RuleUnsupportedValue is for: a table laid out to the content-box model
// when the author asked for the other one is a plausible table with every column
// wider than it was told to be.
func (l *layouter) checkTableBoxSizing(b *Box) {
	if !borderBoxSizing(b) {
		return
	}
	switch b.Inner {
	case InnerTable, InnerTableCell, InnerTableRow, InnerTableRowGroup,
		InnerTableColumn, InnerTableColumnGroup:
	default:
		return
	}
	// No suppression of its own: this one is per *element*, since which table was
	// laid out to the wrong model is exactly what the author needs, and the
	// Recorder already drops a repeat of an identical finding about the same
	// place — which is what a box laid out twice to settle its floats produces.
	l.rec.ReportDetail(Finding{
		Rule:   RuleUnsupportedValue,
		Source: AtHTML(offsetOf(b)),
		Message: "\"box-sizing: border-box\" is not applied to a table box; its width " +
			"was measured to the content-box model, so the padding and border are " +
			"added to it rather than taken out of it",
		Path:     PathOf(b.Element),
		Property: "box-sizing",
	})
}

// The intrinsic sizing keywords, and why they are a finding rather than nothing.
//
// "width: min-content" is correct CSS Sizing: it asks for the narrowest width
// the content can take without overflowing. This engine computes that number —
// intrinsic.go does nothing else — but it does not accept the keyword as a
// declared width, so the declaration falls through parseLength as "not a length
// at all" and the box is laid out at its automatic width.
//
// That is the silent failure §6.3 is about, and it is worse here than most. The
// automatic width of a block is the whole of its containing block, so a box
// asked to shrink to its content is set to the full width of the page instead —
// about the largest wrong answer available — and nothing about the page says so.
// It was found in the white-space intrinsic-size tests, where a box that should
// have been 50px wide came out 626px wide with no finding of any kind.
//
// It is a report and not an implementation because accepting the keyword means
// resolving it at every place a used width is decided, which is a change to the
// sizing model rather than an addition to it. What is cheap and honest meanwhile
// is saying that the declaration was dropped.
//
// The set is the keywords of CSS Sizing 3 that name an intrinsic size. "auto" is
// not among them: it is the initial value and it is applied. The fit-content()
// *function* is a different value and is caught by the same check, since its
// name is the first thing in it.
var intrinsicSizeKeywords = map[string]bool{
	"min-content": true,
	"max-content": true,
	"fit-content": true,
	"stretch":     true,
}

// sizingProperties are the ones an intrinsic keyword is valid on, and so the
// ones where dropping it is this engine's limitation rather than the author's
// mistake. "min-content" written as a margin is not correct CSS, and reporting
// it as unsupported would send an author looking for a feature instead of for a
// typo — which is the distinction RuleInvalidCSS and RuleUnsupportedValue are
// kept apart for.
var sizingProperties = [...]string{
	"width", "min-width", "max-width",
	"height", "min-height", "max-height",
}

// checkIntrinsicSizing reports a sizing property that named an intrinsic size.
//
// Once per element rather than once per property, because a box that says
// "width: min-content" and "height: max-content" has one thing wrong with it and
// what the author needs is the box. The message still names the declaration it
// found, so which one was dropped is in it.
func (l *layouter) checkIntrinsicSizing(b *Box) {
	for _, prop := range sizingProperties {
		raw := strings.TrimSpace(b.Style[prop])
		if raw == "" {
			continue
		}
		name := strings.ToLower(raw)
		if i := strings.IndexByte(name, '('); i > 0 {
			name = strings.TrimSpace(name[:i])
		}
		if !intrinsicSizeKeywords[name] {
			continue
		}
		l.rec.ReportDetail(Finding{
			Rule:   RuleUnsupportedValue,
			Source: AtHTML(offsetOf(b)),
			Message: "\"" + prop + ": " + raw + "\" is not applied; this engine does " +
				"not accept an intrinsic size as a declared one, so the box was laid " +
				"out at its automatic size instead",
			Path:     PathOf(b.Element),
			Property: prop,
		})
		return
	}
}
