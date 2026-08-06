package render

import (
	"strings"

	"github.com/mgilbir/pdf0/style"
)

// text-align: where a line box sits in the width it was given.
//
// CSS 2.1 §16.2. This is the last step of laying out a line and it is deliberately
// separate from breaking it: the width a line is *aligned* at is not the width it
// was *broken* at. §4.1.2 says a trailing space is excluded "for fit, alignment,
// or justification", and the fit has already happened by the time anything here
// runs — so the space that let the break happen must not be counted again when
// deciding where the line goes, or a centred line sits half a space off centre.
//
// The property was registered as understood long before anything read it, which
// meant a centred heading came out flush left and the engine said nothing. That
// is the exact failure the finding vocabulary exists to prevent, and it is worth
// recording that a property table claiming support is not support.

// textAlign is a resolved alignment.
type textAlign uint8

const (
	alignLeft textAlign = iota
	alignRight
	alignCenter
	// alignJustify is recognised and not performed; see alignmentOf.
	alignJustify
)

// alignmentOf resolves the text-align of a block container.
//
// "start" and "end" are resolved against the inline base direction, which is what
// the direction property sets. That is why the initial value matters more than it
// looks: it is "start", so a block with "direction: rtl" and no text-align at all
// is right-aligned, and getting this wrong would leave every right-to-left
// paragraph flush against the edge its text runs away from.
//
// "left" and "right" are physical and are *not* affected by direction. The pair
// exists precisely so that an author can say "that edge" rather than "the edge
// the text starts at".
// rtl is the inline base direction the line was laid out in, which is the
// block's own direction except under "unicode-bidi: plaintext" — there each
// paragraph decides its own, and each has to be aligned against the one it was
// set in or a paragraph of Hebrew would be flush against the left edge of a
// block the algorithm just set right to left.
func alignmentOf(b *Box, rtl bool) textAlign {
	switch strings.ToLower(strings.TrimSpace(b.Style["text-align"])) {
	case "right":
		return alignRight
	case "center":
		return alignCenter
	case "justify":
		return alignJustify
	case "end":
		if rtl {
			return alignLeft
		}
		return alignRight
	case "left":
		return alignLeft
	}
	// start, match-parent, and anything unrecognised. §16.2 makes start the
	// initial value, so this is also the answer for a block that says nothing.
	if rtl {
		return alignRight
	}
	return alignLeft
}

// alignLine returns how far a line's content moves within the width it was given.
//
// used is the width the content actually occupies with its hanging white space
// already discounted. A line at least as wide as the space it has does not move:
// an overfull line overflows to the right whatever the alignment says, because
// moving it would push it off the other edge as well.
func (l *layouter) alignLine(b *Box, rtl bool, lineWidth, used style.Unit) style.Unit {
	slack := lineWidth.Sub(used)
	if slack <= 0 {
		return 0
	}
	switch alignmentOf(b, rtl) {
	case alignRight:
		return slack
	case alignCenter:
		// Half the slack, in layout units rather than pixels, so a line with an
		// odd number of units left over is not rounded twice.
		return slack.Div(2)
	case alignJustify:
		// Justification stretches the spaces of every line but the last, which
		// needs the break opportunities inside the line and a decision about
		// what to do with a line that has none. Neither is here, so the line is
		// left where "start" would put it and the difference is reported —
		// silently setting justified text ragged is the kind of wrong page that
		// looks deliberate.
		l.reportJustify(b)
		return 0
	}
	return 0
}

// reportJustify names the gap once per box rather than once per line.
func (l *layouter) reportJustify(b *Box) {
	key := PathOf(b.Element)
	if l.reportedJustify[key] {
		return
	}
	l.reportedJustify[key] = true
	l.rec.ReportDetail(Finding{
		Rule:   RuleUnsupportedValue,
		Source: AtHTML(offsetOf(b)),
		Message: "\"text-align: justify\" is not implemented; the lines were set " +
			"ragged from the start edge, so the right edge will not line up",
		Path:     key,
		Property: "text-align",
	})
}

// alignedWidth is the width a line occupies for the purpose of aligning it.
//
// It is the pen position at the end of the line, less any run of white space
// hanging past the break. §4.1.2 removes a *collapsible* trailing space outright
// — trimLineEdge does that, and it happens before this — but a *preserved* one
// stays in the runs so that the document's text is what the author wrote. It
// still must not be counted here, or "pre-wrap" text would centre around
// characters that mark no paper.
func alignedWidth(runs []inlineItem, total style.Unit) style.Unit {
	for i := len(runs) - 1; i >= 0; i-- {
		item := runs[i]
		if item.atomicBox != nil || item.atomic != nil {
			break
		}
		if strings.TrimSpace(item.text) != "" {
			break
		}
		total = total.Sub(item.width)
	}
	return style.Max(total, 0)
}
