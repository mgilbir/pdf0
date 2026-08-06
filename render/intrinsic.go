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
//   - A table is measured by §17.5.2.2's own two passes rather than by the walk
//     here, because a table's width is a property of its columns and not of any
//     one of its children. That lives in tablelayout.go and is reached from
//     measureWidths.

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
	if b.Replaced != nil {
		// A replaced element has no preference between a narrow box and a wide
		// one: it is exactly one width and cannot be broken, so both numbers
		// are that width. This is what stops a floated image shrinking to
		// nothing — without it, contentWidths would walk a box with no children
		// and answer zero, and the float would be a sliver with the picture
		// hanging out of it.
		w := l.replacedIntrinsicWidth(b)
		return intrinsicWidths{min: w, max: w}
	}
	if b.IsText() {
		return l.textWidths(b)
	}
	// A table's two widths come from its grid rather than from stacking its
	// children, and §17.4's wrapper is as wide as the table inside it. Both are
	// next door in tablelayout.go, which is where the grid is.
	if b.Inner == InnerTable {
		return l.tableContentWidths(b)
	}
	if b.TableWrapper {
		return l.tableWrapperWidths(b)
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
func (l *layouter) inlineWidths(b *Box) intrinsicWidths {
	// The frame is empty because §9.4.3's offset is applied after layout and so
	// changes no width: a relatively positioned inline demands exactly the room
	// it would have demanded without the declaration. It is marked as a
	// measurement so that nothing on the way down is laid out — see
	// inlineFrame.measuring for why that is a correctness rule and not a saving.
	items, _ := l.collectInline(b, nil, startOfContext(), inlineFrame{measuring: true})
	return l.widthsOf(items)
}

// textWidths measures one text box.
//
// It goes through the same items line breaking would, rather than reading the
// pieces itself, so that the two cannot disagree about what a tab is worth or
// about which space survives — a disagreement that shows up as a float sized to
// a width the text it holds does not need.
func (l *layouter) textWidths(b *Box) intrinsicWidths {
	items, _ := l.itemsFor(b, startOfContext(), Point{})
	return l.widthsOf(items)
}

// widthsOf is the pair over a flattened run of inline items.
//
// The two numbers differ in exactly one way: the maximum lets everything sit on
// one line and so adds the pieces up, while the minimum breaks at every
// opportunity and so takes the widest unbreakable run. A forced break — a <br>,
// or a newline in preserved white space — ends a line in both.
func (l *layouter) widthsOf(items []inlineItem) intrinsicWidths {
	var out intrinsicWidths
	// edge is the trailing run of collapsible space, which §4.1.2 removes at the
	// end of a line: a box sized to include it would be wider than the text it
	// holds by however many spaces happened to end each line.
	//
	// A *preserved* trailing space is deliberately not subtracted here even
	// though it hangs on a wrapped line, because no line here is wrapped. Every
	// line these widths are measured over ends at a forced break or at the end
	// of the content, and §4.1.2 makes the hang at those two *conditional*: the
	// space takes room unless taking it would overflow. A box being sized to
	// its own preferred width cannot overflow, so the space takes room.
	var line, run, edge style.Unit
	endRun := func() {
		out.min = style.Max(out.min, run)
		run = 0
	}
	endLine := func() {
		endRun()
		out.max = style.Max(out.max, line.Sub(edge))
		line, edge = 0, 0
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

		case item.abs != nil:
			// Out of flow: it takes no width on the line, so it contributes to
			// neither number.

		case item.atomicBox != nil:
			// An atomic inline cannot be broken, so it joins the unbreakable
			// run rather than ending it — "an <img> in a sentence" is one word,
			// a picture and another word, with the picture as unsplittable as
			// either. It contributes its own two widths, which for a replaced
			// element are the same number and for an inline-block are its
			// content's.
			if item.breakBefore {
				endRun()
			}
			got := l.outerWidths(item.atomicBox, 0)
			run = run.Add(got.min)
			line = line.Add(got.max)

		case item.forced:
			endLine()

		case item.space:
			w := item.width
			if item.tab {
				// Tab stops are measured from the block's content edge, and on
				// an unbroken line that edge is where this measurement started.
				w = tabAdvance(line, item.tabStop)
			}
			if item.noWrap {
				// Text that may not break has one width, not two. A space in it
				// is a space and not an opportunity, and an engine that ended
				// the unbreakable run here would give a nowrap paragraph a
				// minimum width of its longest word — so a float holding one
				// would be sized to a fraction of the text it then overflows.
				run = run.Add(w)
			} else {
				endRun()
			}
			line = line.Add(w)
			if item.collapsible {
				edge = edge.Add(w)
			} else {
				edge = 0
			}

		default:
			if item.breakBefore && !item.noWrap {
				endRun()
			}
			run = run.Add(item.width)
			line = line.Add(item.width)
			edge = 0
		}
	}
	endLine()
	return out
}
