package render

import "strings"

// visibility: a box that is laid out and not painted.
//
// CSS 2.1 §11.2. The property was registered and unread, so "visibility: hidden"
// showed the box — which is the one failure mode in this whole cluster that can
// leak information rather than merely look wrong.
//
// # Why this cannot prune the box tree
//
// The obvious implementation is to drop the box, and it is wrong twice over.
//
// A hidden box still takes part in layout: it occupies its space, its margins
// collapse, it makes room for itself among its siblings, and a float inside it
// still shortens the lines beside it. That is the whole difference between
// "visibility: hidden" and "display: none", and it is why authors reach for the
// first when they want to reserve room for something that is not there yet.
//
// And visibility *inherits*, so a descendant may set "visibility: visible" and
// reappear inside a hidden ancestor. That is not a corner case — it is how a
// hidden container with one visible child is written — and it means the decision
// cannot be made once for a subtree at all. Every box answers for itself, from
// the value the cascade already gave it, and the answer for a parent says nothing
// about its children.
//
// So the property is read at painting time and nowhere else, per box and per
// run: a run of text belongs to the inline box it came from, which may be
// visible inside a hidden block.
//
// # collapse
//
// "visibility: collapse" on a table row or column removes the row and lets the
// rest of the table close up, which is a layout effect rather than a painting
// one. Everywhere else it means "hidden", and that is what this does with it —
// so a collapsed row is invisible and still occupies its height. The difference
// is reported by checkVisibility rather than left to look deliberate.

// isHidden reports whether a box's own marks are suppressed.
func isHidden(b *Box) bool {
	if b == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(b.Style["visibility"])) {
	case "hidden", "collapse":
		return true
	}
	return false
}

// checkVisibility reports a "collapse" this engine treats as "hidden".
//
// Only on the table boxes where the two differ. On every other box the
// specification says collapse computes to hidden, so there is nothing to report:
// saying so would be complaining about a value that was applied exactly as
// written.
func (l *layouter) checkVisibility(b *Box) {
	if !strings.EqualFold(strings.TrimSpace(b.Style["visibility"]), "collapse") {
		return
	}
	switch b.Inner {
	case InnerTableRow, InnerTableRowGroup, InnerTableColumn, InnerTableColumnGroup:
	default:
		return
	}
	// Per element, and with no suppression of its own: *which* row is still
	// taking space is the actionable part, and the Recorder already drops a
	// repeat of an identical finding about the same place.
	l.rec.ReportDetail(Finding{
		Rule:   RuleUnsupportedValue,
		Source: AtHTML(offsetOf(b)),
		Message: "\"visibility: collapse\" removes a table row or column and closes the " +
			"table up; it was drawn as \"hidden\" instead, so the space it occupies is " +
			"still there",
		Path:     PathOf(b.Element),
		Property: "visibility",
	})
}
