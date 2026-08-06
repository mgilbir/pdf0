package render

import "github.com/mgilbir/pdf0/style"

// Intrinsic widths: how wide a box wants to be when nothing tells it.
//
// Everything else in this engine sizes a box from the outside in — a width is
// declared, or it fills what the containing block leaves. A float cannot be
// sized that way. CSS 2.1 §10.3.5 gives it the *shrink-to-fit* width, and
// shrink-to-fit is defined in terms of two numbers that come from the content:
//
//   - the **preferred width**, which is how wide the content would be if it were
//     never broken — every line of a paragraph run together as one;
//   - the **preferred minimum width**, which is how narrow the box can get
//     before the content overflows it — the widest thing that cannot be broken,
//     usually the longest word.
//
// The result is min(max(minimum, available), preferred): as wide as the content
// wants, capped by the space there is, and never squeezed below what would
// overflow.
//
// This is not an optimisation and it is not a nicety. A float whose width filled
// its containing block would leave nothing beside it, so no line box would ever
// be shortened and the whole feature would appear to work while doing nothing.
//
// # What is approximated, and where that shows
//
// The two widths are computed over the box tree rather than by a trial layout,
// which is what makes them cheap and is where they are inexact:
//
//   - A percentage width contributes as though it were auto. CSS Sizing says a
//     percentage that cannot be resolved behaves as auto for intrinsic sizing,
//     which is the same answer for the common case and not for a percentage
//     larger than the content.
//   - Break opportunities are found within each text box rather than across the
//     boundary between two, so "<em>super</em>market" is measured as two words
//     rather than one. That makes the minimum a little small; it never makes it
//     too large, so a float sized by it fits where it should.
//   - Nothing here knows about tables, whose own automatic layout is two passes
//     of exactly this kind and is not implemented.

// intrinsicWidths is the pair, measured across a box's *margin* box so that the
// caller can compare them against a containing block directly.
type intrinsicWidths struct {
	min, max style.Unit
}

// shrinkToFit is CSS 2.1 §10.3.5's formula.
//
// available is what the containing block leaves after this box's own margins,
// border and padding, so the widths compared against it are content widths.
func (l *layouter) shrinkToFit(b *Box, available style.Unit) style.Unit {
	got := l.contentWidths(b)
	return style.Min(style.Max(got.min, available), got.max)
}

// outerWidths returns a box's intrinsic widths including its own margins,
// border and padding — what it takes up in a parent.
func (l *layouter) outerWidths(b *Box, containing style.Unit) intrinsicWidths {
	inner := l.contentWidths(b)
	if declared, ok := l.explicitWidth(b, containing); ok {
		inner = intrinsicWidths{min: declared, max: declared}
	}
	edges := l.edges(b, "margin", containing).Horizontal().
		Add(l.borderWidths(b).Horizontal()).
		Add(l.edges(b, "padding", containing).Horizontal())
	return intrinsicWidths{
		min: maxZero(inner.min.Add(edges)),
		max: maxZero(inner.max.Add(edges)),
	}
}

// contentWidths returns the two widths of a box's content box, memoized.
func (l *layouter) contentWidths(b *Box) intrinsicWidths {
	if got, ok := l.intrinsic[b]; ok {
		return got
	}
	// Recorded before the recursion rather than after. The box tree is a tree
	// and cannot cycle, but a memo written only on the way out would recompute
	// a shared subtree once per path to it, and the whole point of measuring
	// this way rather than by trial layout is that it is cheap.
	l.intrinsic[b] = intrinsicWidths{}
	got := l.measureWidths(b)
	l.intrinsic[b] = got
	return got
}

func (l *layouter) measureWidths(b *Box) intrinsicWidths {
	if b.IsText() {
		return l.textWidths(b)
	}
	if hasInlineChild(b) {
		return l.inlineWidths(b)
	}

	// Block children stack, so each one's demand is independent of the others'
	// and the box needs the widest.
	var out intrinsicWidths
	for _, c := range b.Children {
		if c.Outer != OuterBlock {
			continue
		}
		// A float among block children is measured too. It is out of flow, but
		// it is still inside this box, and a box narrower than the float it
		// contains would push the float out of itself.
		got := l.outerWidths(c, 0)
		out.min = style.Max(out.min, got.min)
		out.max = style.Max(out.max, got.max)
	}
	return out
}

// inlineWidths measures a run of inline content.
//
// The two numbers differ in exactly one way: the maximum lets everything sit on
// one line and so adds the pieces up, while the minimum breaks at every
// opportunity and so takes the widest unbreakable run. A forced break — a <br>,
// or a newline in preserved white space — ends a line in both.
func (l *layouter) inlineWidths(b *Box) intrinsicWidths {
	items, _ := l.collectInline(b, nil, false)

	var out intrinsicWidths
	var line, run style.Unit
	endRun := func() {
		out.min = style.Max(out.min, run)
		run = 0
	}
	endLine := func() {
		endRun()
		out.max = style.Max(out.max, line)
		line = 0
	}

	for _, item := range items {
		switch {
		case item.float != nil:
			// A float beside text is as wide as it is whether or not the text
			// wraps, so it raises both numbers on its own rather than joining
			// the run of words.
			got := l.outerWidths(item.float, 0)
			out.min = style.Max(out.min, got.min)
			out.max = style.Max(out.max, got.max)

		case item.forced:
			endLine()

		case item.space:
			// A space is a break opportunity, so it ends the unbreakable run;
			// it still occupies width on a line that is never broken.
			endRun()
			line = line.Add(item.width)

		default:
			if item.breakBefore {
				endRun()
			}
			run = run.Add(item.width)
			line = line.Add(item.width)
		}
	}
	endLine()
	return out
}

// textWidths measures one text box.
func (l *layouter) textWidths(b *Box) intrinsicWidths {
	face, ok := l.fontFor(b)
	if !ok {
		return intrinsicWidths{}
	}
	pieces, _ := splitAtBreaks(b.Text)

	var out intrinsicWidths
	var line, run style.Unit
	for _, p := range pieces {
		w := l.measure(face, p.text, b.FontSize)
		if p.space || p.breakBefore {
			out.min = style.Max(out.min, run)
			run = 0
		}
		if !p.space {
			run = run.Add(w)
		}
		line = line.Add(w)
	}
	out.min = style.Max(out.min, run)
	out.max = line
	return out
}
