package render

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// Inline layout: text into lines.
//
// §1 of the rendering proposal calls this the deceptive one, and it is right —
// line boxes, breaking, baseline alignment and whitespace at line edges are
// individually modest and collectively larger than flexbox. What is here is the
// part that puts words on a page: measuring runs against a real face, finding
// where a line may break, and stacking the lines.
//
// # Where a line may break, and what is refused
//
// Doing this properly is UAX #14, a table-driven Unicode algorithm keyed on a
// line-breaking class per code point. What is implemented is a subset, and the
// boundary of the subset is stated here rather than discovered:
//
//   - after a space or a preserved tab;
//   - after a run of preserved spaces, and — under break-spaces — after each
//     one of them;
//   - at a zero-width space, which is how an author marks an opportunity
//     inside a word;
//   - after a hyphen that is not the last character of its run;
//   - on both sides of an ideograph.
//
// That covers Latin, Greek, Cyrillic and the CJK scripts, which is most of what
// a document generator sets. Everything else UAX #14 says is *not* done, and
// the two families that matter are not approximated:
//
//   - the scripts that need a dictionary to find a word boundary — Thai, Lao,
//     Khmer, Burmese;
//   - the bidirectional reordering right-to-left text needs once a line is
//     broken.
//
// Both are refused through the finding vocabulary rather than guessed at,
// because §6.3 is exactly right about them: unshaped or unbroken text still
// looks like text, so the failure mode looks like success. A paragraph of Thai
// run together as one unbreakable word overflows silently and reads as a
// rendering bug rather than as an unimplemented feature.
//
// word-break and overflow-wrap are not implemented either, and are left to be
// reported as unsupported properties rather than approximated. Both ask for a
// break *between two characters*, which is only correct at a grapheme cluster
// boundary — breaking inside one splits a letter from its accent — and nothing
// in this module's dependencies knows where those are. A "break-all" that split
// combining sequences would corrupt exactly the text it was asked to fit.
//
// # White space at a line edge
//
// The rest of CSS Text §4 lives here, and whitespace.go explains why it is
// split across three stages rather than done once. What this file owns is
// §4.1.2: the collapsible space removed at each end of a line, the tab stops a
// preserved tab advances to, and the preserved space that hangs past the line's
// end instead of pushing the next word down.

// # What relative positioning does to an inline box, and what it does not
//
// §9.4.3 applies to an inline box like any other, and the offset is carried on
// each run rather than on a fragment because an inline box has no fragment: a
// <span> broken across a line contributes to two line boxes and to neither
// exclusively. Offsetting the runs is the whole of the visible effect here,
// since this engine draws no background, border or padding on an inline box.
//
// What is given up is the *stacking level*. §9.9 makes a positioned inline a
// positioned box, painted at Appendix E step 7 with everything else positioned;
// its runs are painted at step 6 with the rest of the block's text. The two
// differ only where a relatively positioned inline's text overlaps a positioned
// box that comes earlier in the document — every other pair is ordered the same
// way by both rules, because step 6 already comes after every block background
// and every float and before every positioned box. Closing it would mean
// splitting a line box's runs into stacking levels, which is a change to the
// shape of a line rather than an addition to it.

// LineFragment is a line box: one row of text within a block.
type LineFragment struct {
	// Rect is the line box in the same coordinates as the fragment holding it.
	Rect Rect
	// Baseline is the distance from the top of the line box to the baseline the
	// text sits on. Painting needs it, and it is not derivable afterwards —
	// half-leading is split above and below the text.
	Baseline style.Unit
	// Runs are the pieces of text on the line, in reading order.
	Runs []TextRun
}

// TextRun is a piece of text on a line, set in one face at one size.
type TextRun struct {
	Text string
	// Face is what it is set in, and Size the font size.
	Face *fonts.Face
	Size style.Unit
	// X is the offset from the left of the line box, and Width the advance.
	X, Width style.Unit
	// Box is the inline box the text came from, which carries the colour and
	// the decoration painting will need.
	Box *Box
	// Offset is §9.4.3's relative displacement, accumulated over the inline
	// boxes this run sits inside.
	//
	// It is on the run rather than on a fragment because an inline box has no
	// fragment: a line box holds runs, and a <span> that spans a line break
	// contributes to two of them. Carrying the offset per run is what lets a
	// relatively positioned inline move its text and nothing else — which is the
	// whole of what relative positioning does to an inline box, since this
	// engine draws no background, border or padding on one.
	Offset Point
}

// inlineFrame is what the walk over an inline subtree carries down: enough to
// resolve a relatively positioned inline box's own offset, and the offset
// already accumulated from the inline boxes around it.
//
// The accumulation is what makes nesting work. "<span style=...><em style=...>"
// offsets the em by the sum of the two, because §9.4.3 moves a box together with
// everything inside it, and the inside of an inline box is a run of text that
// has been flattened out of the tree by the time anything could walk it again.
type inlineFrame struct {
	containing style.Unit
	cbHeight   style.Unit
	cbDefinite bool
	offset     Point
	// measuring says the walk is being made to find an intrinsic width rather
	// than to lay a line out, so nothing that has a layout of its own is given
	// one.
	//
	// It is not an optimisation. Laying an inline-block out during a
	// measurement produces a fragment that is then discarded — and the
	// absolutely positioned boxes found inside it would have been recorded
	// against it, so every one of them would be placed twice, once against a
	// rectangle that no longer exists. That is the same fault settle() has to
	// undo when a subtree is laid out again, and here it is cheaper not to
	// create it.
	measuring bool
	// strut is the block container's own line metrics, which an atomic inline
	// needs to resolve vertical-align. It is the containing block's rather than
	// the box's own, because that is what the property is measured against: a
	// "text-top" is the top of the *parent's* content area.
	strut strut
}

// inlineItem is one piece of inline content before it has been put on a line.
type inlineItem struct {
	text  string
	box   *Box
	face  *fonts.Face
	size  style.Unit
	width style.Unit
	// breakBefore marks an item that may begin a line, which is what a break
	// opportunity is once the text has been cut into pieces.
	breakBefore bool
	// space marks white space of any kind: it ends the word before it and does
	// not join the word after it.
	space bool
	// collapsible marks white space that §4.1.2 removes when it lands at
	// either end of a line.
	//
	// It is not the same question as space, and conflating the two is what made
	// "<pre>   x</pre>" lose its indentation: the leading run is white space,
	// so it was dropped at the start of the line, but it is *preserved* white
	// space and dropping it removes something the author wrote.
	collapsible bool
	// hangs marks preserved white space that sits past the end of the line
	// rather than moving to the next one.
	//
	// §4.1.2 makes a trailing run of preserved spaces hang under pre and
	// pre-wrap, so it is not counted when the line is measured for alignment
	// and never causes a break of its own. break-spaces is the value that opts
	// out of this, which is the whole difference between it and pre-wrap.
	hangs bool
	// tab marks one preserved tab. Its advance is not a property of the text —
	// it is the distance to the next tab stop, so it is resolved when the tab
	// has a place on a line and not before.
	tab bool
	// tabStop is the distance between two tab stops, from tab-size.
	tabStop style.Unit
	// forced marks a break the author asked for — a <br>, or a newline in
	// preserved white space. It ends the line wherever it falls, which is the
	// difference between a break opportunity and an instruction.
	forced bool
	// noWrap marks text that may not break at its spaces, so a line takes it
	// whole or overflows.
	noWrap bool
	// float is the box of a float met in this run of inline content. It carries
	// no text of its own: it is a marker saying "a float belongs here", because
	// where a float appears among the words decides which line box it is placed
	// against, and that position is lost once the items are on lines.
	float *Box
	// offset is the relative displacement of the inline boxes this item is
	// inside, which travels with the item because the flattening loses the boxes
	// themselves.
	offset Point
	// atomicBox marks an item that is a box on the line rather than a run of
	// text: a replaced element or an inline-block. It is set whether or not the
	// box was laid out, because an intrinsic-width measurement needs to know
	// there is one without producing a fragment for it.
	atomicBox *Box
	// atomic is that box's fragment, already laid out. It is nil while
	// measuring.
	//
	// Being laid out already is what makes the item atomic: its size comes from
	// its own content and its own declarations, so nothing about the line can
	// change it. All the line decides is where it goes.
	atomic *Fragment
	// ascent and descent are how far the item reaches above and below the
	// baseline, measured over its *margin* box.
	//
	// The two differ by which of §10.8.1's rules gave them. A replaced element's
	// baseline is its bottom margin edge, so it is all ascent — which is why a
	// picture sits on the line of type rather than in the middle of it, and why
	// a line holding one is as tall as the image plus whatever descender space
	// the surrounding text still wants. An inline-block's baseline is the
	// baseline of its *last line box*, so a box of two paragraphs hangs below
	// the line by the depth of its second one — unless it has no line boxes at
	// all or clips its overflow, when it too is all ascent.
	ascent, descent style.Unit
	// align is what vertical-align asked for, and raise is the displacement a
	// "sub", a "super", a length or a percentage adds on top of it.
	align vAlign
	raise style.Unit
	// abs is the box of an absolutely positioned box met in this run, and it is
	// a marker for the same reason and a different consequence. A float met
	// among the words changes where the words go; an absolutely positioned one
	// does not change anything at all, but its *static position* — where it
	// would have been — is what §10.3.7 falls back on, and that is exactly the
	// information the flattening destroys.
	abs *Box
}

// inlineContent lays a box's inline children into lines and returns the height
// they need.
//
// # Why this drives the line breaking rather than calling it
//
// Before floats, every line in a block was the same width and the whole
// paragraph could be broken in one go. It cannot be now. The width available to
// a line depends on which floats overlap the y the line starts at, and that y
// depends on how tall the lines above it were — so the available width is not
// known until the previous line has been placed. Each line is therefore measured
// against its own band, one at a time.
//
// The floats themselves are met *while* the lines are being built, which is the
// second reason the loop is here: a float's position depends on the line it
// appears on, and the lines after it depend on the float.
//
// # text-align is not applied here, and that is a gap rather than a decision
//
// Every run is placed from the line box's start edge. text-align is in the
// property registry — so the cascade accepts it, and nothing reports it — and
// no stage acts on it, which is precisely the shape §6.3 exists to prevent: a
// centred heading comes out left-aligned and the author is told nothing.
//
// It is named here because it is what makes the *other* half of §4.1.2's
// trailing-space rule invisible. A hanging space is excluded from the width a
// line is measured at "for fit, alignment, or justification"; the fit and the
// intrinsic sizing are done, the alignment cannot be until there is an
// alignment to do.
func (l *layouter) inlineContent(b *Box, parent *Fragment, width style.Unit, origin flow) style.Unit {
	st := l.strutFor(b)
	items, _ := l.collectInline(b, nil, startOfContext(), inlineFrame{
		containing: width, cbHeight: origin.cbHeight, cbDefinite: origin.cbDefinite,
		strut: st,
	})
	if len(items) == 0 {
		return 0
	}

	lo, hi := origin.x, origin.x.Add(width)

	var y style.Unit
	for i := 0; i < len(items); {
		// A float that begins a line is placed before the line is measured,
		// because it is one of the floats the line has to avoid. §9.5.1 rule 4
		// puts its top at the top of the line box it belongs to.
		for i < len(items) && items[i].float != nil {
			parent.Children = append(parent.Children,
				l.floatChild(items[i].float, width, origin, y, style.MaxUnit, 0, 0))
			i++
		}
		if i >= len(items) {
			break
		}

		left, right := origin.ctx.bandAt(origin.y.Add(y), lo, hi)
		y, left, right = l.roomForLine(items[i], origin, y, left, right, lo, hi)
		lineWidth := right.Sub(left)

		runs, next, mid, forced := l.breakOneLine(items, i, lineWidth, left.Sub(lo))
		lh, bl := stackLine(runs, st)
		if len(runs) > 0 || forced {
			line := LineFragment{
				Rect:     Rect{X: left.Sub(lo), Y: y, W: right.Sub(left), H: lh},
				Baseline: bl,
			}
			var x style.Unit
			// Atomic inlines are placed as children of the block rather than as
			// runs, so aligning the line has to move them too. The range is
			// noted here because floats placed before the line are already in
			// this slice and must not move: a float is out of flow and
			// text-align says nothing about it.
			atomicStart := len(parent.Children)
			for _, item := range runs {
				if item.atomic != nil {
					// Placed as a child of the block rather than as a run,
					// because it is a box: it has a background, a border, a
					// padding and possibly a subtree of its own, and every one
					// of those is painted by machinery that works on fragments.
					// Its margin box hangs from the line's baseline by its own
					// ascent, which is what puts a picture on the line of type
					// and an inline-block's last line of text on it.
					f := item.atomic
					f.BorderRect.X = line.Rect.X.Add(x).Add(f.Margin.Left)
					f.BorderRect.Y = y.Add(atomicTop(item, st, lh, bl)).Add(f.Margin.Top)
					parent.Children = append(parent.Children, f)
					x = x.Add(item.width)
					continue
				}
				line.Runs = append(line.Runs, TextRun{
					Text: item.text, Face: item.face, Size: item.size,
					X: x, Width: item.width, Box: item.box, Offset: item.offset,
				})
				x = x.Add(item.width)
			}
			if shift := l.alignLine(b, line.Rect.W, alignedWidth(runs, x)); shift != 0 {
				for k := range line.Runs {
					line.Runs[k].X = line.Runs[k].X.Add(shift)
				}
				for k := atomicStart; k < len(parent.Children); k++ {
					parent.Children[k].BorderRect.X =
						parent.Children[k].BorderRect.X.Add(shift)
				}
			}
			parent.Lines = append(parent.Lines, line)
		}

		// The out-of-flow boxes met along the line are dealt with once the line
		// is settled, because until it is neither its top nor its left edge is
		// known — and both are what the two kinds need.
		for _, f := range mid {
			if f.abs {
				// §10.6.4's static position for a box written among the words:
				// the top of the line box it appeared on, and the pen position
				// it appeared at. The x is taken from the line's own left edge
				// rather than the block's, so a box written beside a float
				// records the position it would really have had.
				l.deferAbsolute(f.box, parent, left.Sub(lo).Add(f.used), y, 0)
				continue
			}
			parent.Children = append(parent.Children,
				l.floatChild(f.box, width, origin, y, lineWidth.Sub(f.used), lh, 0))
		}

		i = next
		if len(runs) > 0 || forced {
			// Only a line that exists occupies a line's height. A run of inline
			// content that is nothing but the collapsible space between two
			// block children produces no line box at all, and giving it one
			// would put a blank line into every document whose markup is
			// indented.
			y = y.Add(lh)
		}
	}
	return y
}

// vAlign is what vertical-align asks of an atomic inline.
//
// The set is CSS 2.1 §10.8.1's, less the two that are not a choice of position:
// "inherit" is the cascade's business and a length or percentage is carried as
// a displacement from the baseline rather than as a mode of its own, because
// that is exactly what it is — "vertical-align: 4px" is baseline alignment with
// the baseline moved.
//
// It is read for atomic inlines only. An ordinary inline box's runs are not
// aligned by it, which is a real gap and a pre-existing one: <sup> and <sub>
// are set at the smaller size the user-agent stylesheet gives them and on the
// same baseline as their surroundings. Closing that means giving a text run a
// vertical displacement of its own, which the run already has a field for, and
// it is a separate change from this one.
type vAlign uint8

const (
	vAlignBaseline vAlign = iota
	vAlignTop
	vAlignBottom
	vAlignMiddle
	vAlignTextTop
	vAlignTextBottom
)

// strut is the block's own contribution to every line box it makes.
//
// CSS 2.1 §10.8 gives each line box an imaginary zero-width inline box of the
// block's font and line-height, and that box takes part in the alignment
// whether or not there is any text on the line. It is why a line holding
// nothing but an image is still as tall as the image *plus* the descender space
// the type would have wanted, and why an empty <p> occupies a line.
type strut struct {
	// height and baseline are the line-height and where the baseline sits in it.
	height, baseline style.Unit
	// ascent and descent are the font's own extents at the block's size, which
	// are what "text-top" and "text-bottom" name. They are not the same as the
	// two above: those include the half-leading.
	ascent, descent style.Unit
	// xHeight is what "middle" is measured against.
	xHeight style.Unit
}

// strutFor measures the block's own font at its own size.
func (l *layouter) strutFor(b *Box) strut {
	s := strut{height: l.lineHeight(b), baseline: l.baselineOf(b, l.lineHeight(b))}
	face, ok := l.fontFor(b)
	if !ok {
		return s
	}
	d := face.Descriptor()
	upem := float64(face.UnitsPerEm())
	if upem == 0 {
		return s
	}
	s.ascent = b.FontSize.Mul(float64(d.Ascent) / upem)
	s.descent = b.FontSize.Mul(-float64(d.Descent) / upem)
	// The x-height, which no face this engine reads reports directly. Half an em
	// is the figure every implementation falls back to and is within a few per
	// cent for the Latin faces; a wrong x-height moves a "vertical-align:
	// middle" box by a pixel or two, where having no answer at all would move it
	// by half its own height.
	s.xHeight = b.FontSize.Mul(0.5)
	if d.CapHeight > 0 {
		// Cap height is reported, and x-height is about seven tenths of it for
		// the faces that report either.
		s.xHeight = b.FontSize.Mul(float64(d.CapHeight) / upem * 0.7)
	}
	return s
}

// verticalAlignOf reads the vertical-align property of an atomic inline.
func (l *layouter) verticalAlignOf(b *Box, s strut) (vAlign, style.Unit) {
	raw := strings.ToLower(strings.TrimSpace(b.Style["vertical-align"]))
	switch raw {
	case "", "baseline":
		return vAlignBaseline, 0
	case "top":
		return vAlignTop, 0
	case "bottom":
		return vAlignBottom, 0
	case "middle":
		return vAlignMiddle, 0
	case "text-top":
		return vAlignTextTop, 0
	case "text-bottom":
		return vAlignTextBottom, 0
	case "sub":
		// The specification leaves the distance to the engine. A fifth of the
		// font size is what browsers use.
		return vAlignBaseline, style.Unit(0).Sub(b.FontSize.Mul(0.2))
	case "super":
		return vAlignBaseline, b.FontSize.Mul(0.33)
	}
	// A length raises the box by that much; a percentage is of the line-height,
	// which is the one property whose percentages are of line-height rather than
	// of the containing block.
	if length, ok := l.parseLength(b, "vertical-align"); ok {
		if v, ok := length.Resolve(s.height, true); ok {
			return vAlignBaseline, v
		}
	}
	return vAlignBaseline, 0
}

// stackLine gives a line its height and its baseline, once what is on it is
// known.
//
// CSS 2.1 §10.8 builds a line box by aligning everything on it against the
// baseline and then taking the distance from the highest top to the lowest
// bottom. The block's own "strut" — an imaginary zero-width piece of its font
// at its line-height — always takes part, which is what gives a line of text its
// height whether or not there is text on it, and what leaves the familiar gap
// under an image that sits alone on a line: the strut still wants its
// descender.
//
// What is implemented is the baseline alignment of §10.8.1 and nothing else.
// vertical-align is not read, so a "vertical-align: top" or "middle" on an image
// is laid out as though it were "baseline". That is a real difference and it is
// left rather than approximated: getting the other keywords right needs the line
// box's extent, which is what is being computed here, so they are a second pass
// rather than another case.
func stackLine(runs []inlineItem, s strut) (height, baseline style.Unit) {
	baseline = s.baseline
	// What the strut wants below the baseline. It can be *negative*, which is
	// the case that makes this a maximum rather than a floor: "line-height: 0"
	// gives the strut a half-leading of minus half the font's own height, so
	// its descent is below the baseline by a negative amount and an image on
	// the line has to be able to overrule it. Taking the strut's descent
	// unconditionally would make such a line shorter than the picture on it.
	descent := s.height.Sub(s.baseline)

	// First pass: everything aligned against the baseline, which is what
	// decides where the baseline is.
	for _, item := range runs {
		if item.atomic == nil {
			continue
		}
		switch item.align {
		case vAlignTop, vAlignBottom:
			// Aligned against the line box, which does not exist yet.
			continue
		}
		a, d := alignedExtents(item, s)
		if a > baseline {
			baseline = a
		}
		if d > descent {
			descent = d
		}
	}

	// Second pass: the two that align against the line box itself. §10.8.1
	// defines them in terms of a line box whose height they can change, which
	// reads as circular and is not: a box taller than the line grows it on the
	// side away from its own edge, and a box that fits changes nothing.
	height = baseline.Add(descent)
	for _, item := range runs {
		if item.atomic == nil {
			continue
		}
		h := item.ascent.Add(item.descent)
		switch item.align {
		case vAlignTop:
			// Its top is the line's top, so anything it needs it takes from
			// below the baseline.
			if h > height {
				descent = descent.Add(h.Sub(height))
				height = h
			}
		case vAlignBottom:
			// Its bottom is the line's bottom, so it takes from above.
			if h > height {
				baseline = baseline.Add(h.Sub(height))
				height = h
			}
		}
	}
	return baseline.Add(descent), baseline
}

// alignedExtents is how far an item reaches above and below the baseline once
// its vertical-align has been applied.
func alignedExtents(item inlineItem, s strut) (ascent, descent style.Unit) {
	h := item.ascent.Add(item.descent)
	switch item.align {
	case vAlignTextTop:
		// The top of the box against the top of the parent's content area,
		// which is the font's own ascent above the baseline rather than the
		// line box's top: the half-leading is not part of it.
		return s.ascent, h.Sub(s.ascent)
	case vAlignTextBottom:
		return h.Sub(s.descent), s.descent
	case vAlignMiddle:
		// The box's own midpoint against the baseline raised by half the
		// parent's x-height.
		half := h.Div(2)
		return half.Add(s.xHeight.Div(2)), half.Sub(s.xHeight.Div(2))
	}
	// Baseline, with whatever "sub", "super" or a length displaced it by.
	return item.ascent.Add(item.raise), item.descent.Sub(item.raise)
}

// atomicTop is where an item's margin box goes within its line box.
func atomicTop(item inlineItem, s strut, height, baseline style.Unit) style.Unit {
	h := item.ascent.Add(item.descent)
	switch item.align {
	case vAlignTop:
		return 0
	case vAlignBottom:
		return height.Sub(h)
	}
	ascent, _ := alignedExtents(item, s)
	return baseline.Sub(ascent)
}

// isAtomicInline reports whether an inline-level box takes part in a line as a
// box rather than as a run of words.
//
// An inline-block is one. That is the whole of what "inline-block" means and it
// is easy to under-implement: an engine that walked into it the way it walks
// into a <span> would flatten its content onto the surrounding line, and the
// box's own width, height, background, border and padding would all quietly do
// nothing — a shape of failure that looks like the declarations were ignored
// rather than like the box was.
//
// An inline table and an inline flex container are atomic too, and are
// deliberately not here: neither has a layout to be atomic *with* yet, and
// giving them a box before they have contents to put in it would produce an
// empty rectangle where the author expected a table.
func isAtomicInline(b *Box) bool {
	return b.Outer == OuterInline && b.Inner == InnerFlowRoot
}

// atomicItem lays out an atomic inline and makes the line item for it.
func (l *layouter) atomicItem(b *Box, frame inlineFrame, pending bool) inlineItem {
	item := inlineItem{
		box: b, atomicBox: b, size: b.FontSize,
		breakBefore: pending, offset: frame.offset,
	}
	if frame.measuring {
		// No fragment: the caller wants a width, and the widths of an atomic
		// inline are what intrinsic.go computes from the box tree.
		return item
	}

	var frag *Fragment
	if b.Replaced != nil {
		frag = l.replacedFragment(b, frame)
	} else {
		frag = l.inlineBlockFragment(b, frame)
	}
	box := frag.MarginRect()
	item.atomic = frag
	item.width = box.W
	item.ascent, item.descent = box.H, 0
	item.align, item.raise = l.verticalAlignOf(b, frame.strut)

	// §10.8.1: an inline-block's baseline is the baseline of its last in-flow
	// line box. With no line box at all it is the bottom margin edge — which is
	// also a replaced element's, so the value set above already says so.
	//
	// An overflow that is not visible does not simply fall back to the bottom
	// margin edge, which is what CSS 2.1 said and what CSS 2.2 corrected: it is
	// the *higher* of the two candidates. The correction matters because the
	// 2.1 rule made "overflow: auto" on a one-line box drop the whole box below
	// its neighbours' baseline, which is a visible jump from a declaration that
	// was only ever about clipping.
	if b.Replaced == nil {
		if bl, ok := lastLineBaseline(frag); ok {
			ascent := frag.Margin.Top.Add(bl)
			if overflowIsScrollable(b.Style) {
				// A smaller ascent is a baseline further up the page, which is
				// what "higher" means here.
				ascent = style.Min(ascent, box.H)
			}
			item.ascent = ascent
			item.descent = box.H.Sub(ascent)
		}
	}
	return item
}

// replacedFragment lays out an inline-level replaced box.
//
// Its margins are its own — an "auto" margin on an inline-level box is zero
// rather than a share of anything, which l.edges already produces — and its
// size comes from §10.3.2. What it does not get here is a position: that is the
// line's to decide.
func (l *layouter) replacedFragment(b *Box, frame inlineFrame) *Fragment {
	margin := l.edges(b, "margin", frame.containing)
	border := l.borderWidths(b)
	padding := l.edges(b, "padding", frame.containing)
	size := l.replacedSize(b, frame.containing, frame.cbHeight, frame.cbDefinite)

	frag := &Fragment{
		Box: b, Margin: margin, Border: border, Padding: padding,
		BorderRect: Rect{
			W: size.W.Add(padding.Horizontal()).Add(border.Horizontal()),
			H: size.H.Add(padding.Vertical()).Add(border.Vertical()),
		},
		// §9.4.3's offset, accumulated over the inline boxes around it, plus
		// its own when it is itself relatively positioned. It travels on the
		// fragment rather than being folded into the position for the reason
		// layout.go gives: the box still occupies where the flow put it.
		Offset: frame.offset,
	}
	if b.Position == PositionRelative {
		d := l.relativeOffset(b, frame.containing, frame.cbHeight, frame.cbDefinite)
		frag.Offset = Point{X: frame.offset.X.Add(d.X), Y: frame.offset.Y.Add(d.Y)}
	}
	if b.Position.positioned() {
		// §10.1 makes any positioned box a containing block, and an image with
		// "position: relative" is the everyday way to hang a caption on one.
		l.positioned[b] = frag
	}
	return frag
}

// inlineBlockFragment lays out an inline-block.
//
// Its width is CSS 2.1 §10.3.9's: shrink-to-fit, the same formula a float uses,
// against whatever the containing block leaves after its own margins, border
// and padding. Everything else is ordinary block layout, run through blockIn
// with the width handed to it — margin collapsing inside, floats contained,
// line breaking, list markers and the height rules are all the same, and a
// second implementation of them would agree with the first on the day it was
// written and on no day after.
func (l *layouter) inlineBlockFragment(b *Box, frame inlineFrame) *Fragment {
	margin := l.edges(b, "margin", frame.containing)
	border := l.borderWidths(b)
	padding := l.edges(b, "padding", frame.containing)

	width, ok := l.lengthOf(b, "width", frame.containing)
	if !ok {
		room := frame.containing.
			Sub(margin.Horizontal()).
			Sub(border.Horizontal()).
			Sub(padding.Horizontal())
		width = l.shrinkToFit(b, maxZero(room))
	}
	width = l.clampWidth(b, width, frame.containing)

	// A fresh formatting context, because an inline-block establishes one:
	// no float inside it escapes and none outside reaches in. That is not a
	// choice made here — it is what "flow-root" means, and blockIn would make
	// one anyway for a box that seals its margins.
	frag, _ := l.blockIn(b, frame.containing,
		flow{ctx: &floatContext{}, cbHeight: frame.cbHeight, cbDefinite: frame.cbDefinite},
		&forcedGeometry{margin: margin, width: width})
	if b.Position == PositionRelative {
		d := l.relativeOffset(b, frame.containing, frame.cbHeight, frame.cbDefinite)
		frag.Offset = Point{X: frame.offset.X.Add(d.X), Y: frame.offset.Y.Add(d.Y)}
	} else {
		frag.Offset = frame.offset
	}
	return frag
}

// lastLineBaseline finds the baseline of the last line box in a subtree, as a
// distance from the top of that subtree's border box.
//
// "Last in the normal flow" is what §10.8.1 asks for, so the walk goes
// backwards through the children and skips everything out of flow: a float at
// the end of an inline-block does not give the box its baseline, and neither
// does an absolutely positioned caption hanging off it.
func lastLineBaseline(f *Fragment) (style.Unit, bool) {
	inset := f.Border.Top.Add(f.Padding.Top)
	for i := len(f.Children) - 1; i >= 0; i-- {
		c := f.Children[i]
		if c.Box == nil || c.Box.outOfFlow() {
			continue
		}
		if bl, ok := lastLineBaseline(c); ok {
			return inset.Add(c.BorderRect.Y).Add(bl), true
		}
	}
	if n := len(f.Lines); n > 0 {
		line := f.Lines[n-1]
		return inset.Add(line.Rect.Y).Add(line.Baseline), true
	}
	return 0, false
}

// roomForLine moves a line down past floats that leave it no usable width.
//
// CSS 2.1 §9.5 says a line box that is too small to hold any content is shifted
// downwards until it either fits or there are no floats left. Without this a
// paragraph beside two facing floats would have every line clipped to nothing
// and the text would vanish — a failure that produces a page with a hole in it
// and no other symptom, which is the shape §6 exists to prevent.
//
// It moves only for the *first* item of the line. An item that does not fit on a
// full-width line is genuinely too wide and is reported as an overflow rather
// than chased down the page for ever.
func (l *layouter) roomForLine(first inlineItem, origin flow, y, left, right, lo, hi style.Unit) (
	style.Unit, style.Unit, style.Unit) {

	for left != lo || right != hi {
		if right.Sub(left) > 0 && (first.space || first.width <= right.Sub(left)) {
			break
		}
		next, ok := origin.ctx.nextBottomBelow(origin.y.Add(y))
		if !ok {
			break
		}
		y = next.Sub(origin.y)
		left, right = origin.ctx.bandAt(origin.y.Add(y), lo, hi)
	}
	return y, left, right
}

// midLineBox is an out-of-flow box met after a line had already begun, together
// with how much of that line had been filled when it was reached.
//
// The same record serves floats and absolutely positioned boxes because both
// need the one number and neither needs anything else — but they need it for
// opposite reasons, which is why the two are told apart rather than merged. For
// a float it says whether there is still room beside the line; for an absolutely
// positioned box it *is* the static position, and there is no question of room
// because the box takes none.
type midLineBox struct {
	box *Box
	// used is how much of the line's width had been filled when the box was
	// reached, measured from the line's own left edge.
	used style.Unit
	// abs distinguishes the two kinds.
	abs bool
}

// inlineState is what the flattening carries from one inline box to the next.
//
// Both fields are about a rule that spans a box boundary, which is why they
// travel rather than being recomputed per box: neither can be answered by
// looking at one text node.
type inlineState struct {
	// breakOpportunity says the content before this point ended at one. In
	// "foo <em>bar</em>" the space and the word are in different text boxes, so
	// an engine that started each box afresh would find no opportunity between
	// them and set the whole phrase as one unbreakable word.
	breakOpportunity bool
	// afterCollapsibleSpace says the last thing emitted was a collapsible
	// space, so §4.1.1's fourth rule collapses the next one into it —
	// "provided both spaces are within the same inline formatting context",
	// which is exactly the span this state covers.
	//
	// It starts true, because the beginning of the context is the beginning of
	// its first line and §4.1.2 removes the collapsible space there.
	afterCollapsibleSpace bool
}

// startOfContext is the state an inline formatting context begins in.
func startOfContext() inlineState { return inlineState{afterCollapsibleSpace: true} }

// collectInline flattens an inline subtree into measurable items.
//
// The tree is flattened because a line break can fall anywhere, including inside
// an <em> — so what goes on a line is a sequence of runs, not a sequence of
// boxes. Each item keeps the box it came from, which is what painting needs to
// know its colour.
func (l *layouter) collectInline(b *Box, out []inlineItem, state inlineState, frame inlineFrame) ([]inlineItem, inlineState) {
	for _, child := range b.Children {
		if child.Position.outOfFlow() {
			// Out of flow and, unlike a float, out of the way: it takes no width
			// on the line, breaks nothing and shortens nothing. All that is kept
			// is where it was written, which is its static position.
			out = append(out, inlineItem{abs: child})
			continue
		}
		if child.Float != FloatNone {
			// Out of flow, so it neither takes width on the line nor breaks it:
			// it is recorded where it was written and placed when the line it
			// belongs to is known. The state passes straight through, because
			// "a <span class=float></span>b" is still one word followed by
			// another with a space between them.
			out = append(out, inlineItem{float: child})
			continue
		}
		if child.Replaced != nil || isAtomicInline(child) {
			// An atomic inline: a replaced element, or an inline-block. It is
			// one unbreakable thing with a size of its own, and it is laid out
			// here — before the line it will sit on has even been chosen —
			// because nothing about that line can change its size. That is the
			// whole difference between an atomic inline and an ordinary inline
			// box, whose extent is whatever its words turn out to need and
			// which therefore has to be flattened into the run.
			out = append(out, l.atomicItem(child, frame, state.breakOpportunity))
			// An atomic inline is content, not space: it ends any break
			// opportunity that was carried into it, and a collapsible space
			// after it survives rather than collapsing into whatever came
			// before the picture.
			state.breakOpportunity = false
			state.afterCollapsibleSpace = false
			continue
		}
		if child.IsText() {
			var items []inlineItem
			items, state = l.itemsFor(child, state, frame.offset)
			out = append(out, items...)
			continue
		}
		if child.Element != nil && strings.EqualFold(child.Element.Name, "br") {
			// A line break the author wrote. It is not a break *opportunity* —
			// it ends the line wherever it falls, even mid-word and even on a
			// line with room to spare.
			out = append(out, inlineItem{box: child, forced: true})
			// What follows is at the start of a line, so a collapsible space
			// there is removed rather than indenting it.
			state = startOfContext()
			continue
		}
		if child.Outer == OuterInline {
			inner := frame
			if child.Position == PositionRelative {
				d := l.relativeOffset(child, frame.containing, frame.cbHeight, frame.cbDefinite)
				inner.offset = Point{
					X: frame.offset.X.Add(d.X),
					Y: frame.offset.Y.Add(d.Y),
				}
			}
			out, state = l.collectInline(child, out, state, inner)
		}
	}
	return out, state
}

// itemsFor cuts one text box into items at its break opportunities and measures
// each, applying the half of §4.1.1 that could not be done per node.
func (l *layouter) itemsFor(b *Box, in inlineState, offset Point) ([]inlineItem, inlineState) {
	face, ok := l.fontFor(b)
	if !ok {
		return nil, in
	}
	l.checkScript(b)
	l.checkGlyphs(b, face)

	size := b.FontSize
	ws := whiteSpaceOf(b.Style["white-space"])
	pieces, endedAtBreak := splitAtBreaks(b.Text, ws)
	if len(pieces) == 0 {
		// A box that produced nothing passes an opportunity through rather than
		// swallowing it — and it may have created one of its own, which is what
		// a <span> holding a single zero-width space is. Either source counts.
		in.breakOpportunity = in.breakOpportunity || endedAtBreak
		return nil, in
	}

	var tabStop style.Unit
	for _, p := range pieces {
		if p.tab {
			tabStop = l.tabStop(b, face)
			break
		}
	}

	out := make([]inlineItem, 0, len(pieces))
	state := in
	for _, p := range pieces {
		if p.segment {
			// A segment break that survived Phase I is a break the author
			// wrote, and it ends the line as firmly as a <br> does.
			out = append(out, inlineItem{box: b, face: face, size: size, forced: true, offset: offset})
			state = startOfContext()
			continue
		}
		if p.collapsible && state.afterCollapsibleSpace {
			// §4.1.1's fourth rule: a collapsible space following another
			// collapses to zero advance width, across an inline boundary as
			// readily as within one — so "a <span> </span> b" sets one space
			// and not three. It keeps its break opportunity, which is what the
			// rule's parenthesis is for.
			state.breakOpportunity = true
			continue
		}

		item := inlineItem{
			text: p.text, box: b, face: face, size: size,
			breakBefore: p.breakBefore || state.breakOpportunity,
			space:       p.space, collapsible: p.collapsible,
			tab: p.tab, tabStop: tabStop,
			// Preserved white space hangs unless the value is break-spaces,
			// which exists precisely so that it does not.
			hangs:  p.space && !p.collapsible && !ws.breakSpaces,
			noWrap: !ws.wrap, offset: offset,
		}
		if !p.tab {
			// A tab is measured against a tab stop when it lands, so there is
			// nothing to measure here and the face's own advance for U+0009 —
			// whatever a face happens to give a character it has no glyph for —
			// would be the wrong number to carry.
			item.width = l.measure(face, p.text, size)
		}
		out = append(out, item)
		state = inlineState{afterCollapsibleSpace: p.collapsible}
	}
	return out, inlineState{
		breakOpportunity:      endedAtBreak,
		afterCollapsibleSpace: state.afterCollapsibleSpace,
	}
}

// tabStop is the distance between two tab stops, which is what tab-size sets.
//
// A number is a count of space advances in the box's own font, which is why
// this needs the face; a length is itself. The initial value is 8, the width
// every terminal and every editor has used for a tab since they had one.
func (l *layouter) tabStop(b *Box, face *fonts.Face) style.Unit {
	raw := strings.TrimSpace(b.Style["tab-size"])
	if n, ok := parseNumber(raw); ok {
		return l.measure(face, " ", b.FontSize).Mul(n)
	}
	if v, ok := l.lengthOf(b, "tab-size", 0); ok && v >= 0 {
		return v
	}
	return l.measure(face, " ", b.FontSize).Mul(8)
}

// tabAdvance is the distance from x to the next tab stop.
//
// Tab stops are at multiples of the tab size from the block's content edge, so
// a tab's advance is a property of where it lands rather than of the text it
// sits in — which is why it cannot be measured with the rest of a run.
//
// The arithmetic is exact rather than floating point, because a layout unit is
// a fixed-point integer and a tab stop computed in floats would drift along a
// line of them until two columns that should align did not.
//
// A tab size of zero renders no tab at all, which is what §4.1.2 says and is
// the only way to ask for a tab that takes no room.
func tabAdvance(x, stop style.Unit) style.Unit {
	if stop <= 0 {
		return 0
	}
	if x < 0 {
		x = 0
	}
	return stop.Sub(x % stop)
}

// measure returns the advance width of a string, memoized.
//
// Measuring is the inner loop of line breaking, and the same words recur
// constantly in a document — every "the" in a page measures the same. The key
// includes the face and the size because both scale the answer.
func (l *layouter) measure(face *fonts.Face, text string, size style.Unit) style.Unit {
	if text == "" {
		return 0
	}
	key := measureKey{face: face, text: text, size: size}
	if got, ok := l.measured[key]; ok {
		return got
	}
	// Measure returns the advance in the units the size was given in, so a size
	// in CSS pixels gives an advance in CSS pixels.
	w, _ := style.FromPx(face.Measure(text, size.Px()))
	l.measured[key] = w
	return w
}

type measureKey struct {
	face *fonts.Face
	text string
	size style.Unit
}

// piece is a run of text between two break opportunities, together with what
// §4.1.2 has to know about it once it lands on a line.
type piece struct {
	text        string
	breakBefore bool
	// space marks white space of any kind, collapsible marks the subset of it
	// that a line edge removes, and tab and segment mark the two preserved
	// characters that are not simply text of their own width.
	space       bool
	collapsible bool
	tab         bool
	segment     bool
}

// splitAtBreaks cuts text at the break opportunities this engine implements.
//
// The subset is stated in the file comment. Each rule below is one of UAX #14's,
// named by what it does rather than by its class letters, and the ones left out
// are left out loudly — checkScript reports text that needs them.
//
// It takes the white-space value because two of the rules depend on it: a
// preserved space is a piece of its own rather than a collapsed one, and
// break-spaces wants each space separately because a line may end after any one
// of them.
//
// The text is walked rune by rune rather than through a []rune, which is not a
// micro-optimisation: a text node is untrusted and arbitrarily large, and a
// decoded copy of one is four bytes per character of buffering nobody asked for.
func splitAtBreaks(text string, ws whiteSpace) ([]piece, bool) {
	var out []piece
	var cur strings.Builder
	breakNext := false

	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, piece{text: cur.String(), breakBefore: breakNext})
		cur.Reset()
		breakNext = false
	}
	// A white-space piece takes the pending opportunity but does not consume
	// it: what follows a space may begin a line whatever came before it, and an
	// earlier version that cleared the flag here lost the opportunity after
	// "a- b" entirely.
	emit := func(p piece) {
		p.breakBefore = breakNext
		out = append(out, p)
	}

	for i := 0; i < len(text); {
		r, size := rune(text[i]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRuneInString(text[i:])
		}
		i += size

		switch {
		case r == '\n' || r == '\r':
			// Only a *preserved* break reaches here: Phase I turned a
			// collapsible one into a space. A CR is folded with the LF that may
			// follow it, so that text which reached this stage without going
			// through Phase I — a caller measuring raw content — still counts
			// one break rather than two.
			if r == '\r' && i < len(text) && text[i] == '\n' {
				i++
			}
			flush()
			emit(piece{text: "\n", space: true, segment: true})
			breakNext = true

		case r == '\t' && !ws.collapse:
			// A preserved tab is its own piece because each one advances to its
			// own tab stop, so two of them are not one run of a doubled width.
			flush()
			emit(piece{text: "\t", space: true, tab: true})
			breakNext = true

		case r == ' ' || r == '\t':
			flush()
			if ws.collapse {
				// Phase I already reduced the run to a single space and turned
				// any tab into one, so there is nothing left to gather.
				emit(piece{text: " ", space: true, collapsible: true})
				breakNext = true
				break
			}
			// Preserved. Under pre and pre-wrap the run hangs or wraps as a
			// unit, so it is one piece; under break-spaces a line may end after
			// any single space, so each is its own.
			start := i - size
			if !ws.breakSpaces {
				for i < len(text) && text[i] == ' ' {
					i++
				}
			}
			emit(piece{text: text[start:i], space: true})
			breakNext = true

		case r == '​':
			// A zero-width space is a break opportunity and nothing else: it is
			// how an author marks one inside a word.
			flush()
			breakNext = true

		case isIdeographic(r):
			// CJK breaks between ideographs, which is why it needs no spaces.
			flush()
			cur.WriteRune(r)
			flush()
			breakNext = true

		case r == '-' && !endsRunOrSpace(text, i):
			// A hyphen ends a run and the next may begin a line — which is what
			// lets a hyphenated compound break where it is written.
			cur.WriteRune(r)
			flush()
			breakNext = true

		default:
			cur.WriteRune(r)
		}
	}
	flush()
	// breakNext survives the last piece: it says the text ended at an
	// opportunity, which matters when what follows is in another box.
	return out, breakNext
}

// endsRunOrSpace reports whether the text at i is the end of the run or white
// space, which is what stops a trailing hyphen being a break opportunity: there
// would be nothing after it to move to the next line.
func endsRunOrSpace(text string, i int) bool {
	if i >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[i:])
	return unicode.IsSpace(r)
}

// isIdeographic reports whether a rune breaks on both sides, which is what makes
// CJK line breaking possible without word boundaries.
func isIdeographic(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // Extension A
		return true
	case r >= 0xF900 && r <= 0xFAFF: // Compatibility Ideographs
		return true
	case r >= 0x3040 && r <= 0x30FF: // Hiragana and Katakana
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul syllables
		return true
	case r >= 0x20000 && r <= 0x2FA1F: // Extensions B and beyond
		return true
	}
	return false
}

// breakOneLine fills a single line, greedily, and says where the next one
// starts.
//
// Greedy is what browsers do: a line takes what fits and the next one starts
// after it. The alternative — minimising raggedness across a paragraph, which is
// what TeX does — produces better-looking text and needs the whole paragraph
// before any line is settled, which is a different shape of engine.
//
// One line at a time rather than the whole paragraph, because width is no longer
// a property of the paragraph: with a float beside it, each line has its own.
//
// forced reports that the line ended at a break the author wrote, which is what
// makes an empty line real — "a<br><br>b" leaves a blank line, and an engine
// that dropped empty lines would close the gap up.
//
// lineX is where the line box starts within the block's content box, which a
// float beside it makes something other than zero. It is needed because a tab
// stop is measured from the block's edge and not from the line's.
//
// The returned items carry their resolved widths: a tab's is not known until it
// has a place, so an item on a line is not always the item that came in.
func (l *layouter) breakOneLine(items []inlineItem, from int, width, lineX style.Unit) (
	line []inlineItem, next int, outOfFlow []midLineBox, forced bool) {

	var used style.Unit
	i := from
	for ; i < len(items); i++ {
		item := items[i]

		if item.float != nil {
			// Recorded with how far along the line it was reached, which is what
			// decides whether it goes beside this line or below it.
			outOfFlow = append(outOfFlow, midLineBox{box: item.float, used: used})
			continue
		}

		if item.abs != nil {
			// Recorded and otherwise ignored. It consumes no width, so the words
			// on the line are placed exactly as they would have been had the box
			// not been written at all — which is what "out of flow" means and is
			// the assertion a test can make that a float cannot.
			outOfFlow = append(outOfFlow, midLineBox{box: item.abs, used: used, abs: true})
			continue
		}

		if item.forced {
			// An instruction rather than an opportunity: the line ends here
			// whatever room is left, and an empty one still occupies its height.
			return trimLineEdge(line), i + 1, outOfFlow, true
		}

		// §4.1.2's first rule: a sequence of collapsible spaces at the
		// beginning of a line is removed. It is the space the break happened
		// at, or the one the author left after a tag, and keeping it would
		// indent every line after the first.
		//
		// Only a *collapsible* one. Preserved white space at the start of a
		// line is content — it is what makes "<pre>    indented</pre>" indent,
		// and it is the whole of the pre-wrap leading-space rule.
		if item.collapsible && len(line) == 0 {
			continue
		}

		if item.tab {
			item.width = tabAdvance(lineX.Add(used), item.tabStop)
		}

		// A hanging space never causes a break: it sits past the line's end
		// rather than moving to the next one. Without this, "XX    XX" under
		// pre-wrap would push the second word down a line for spaces that take
		// no room on the page at all.
		if !item.noWrap && !item.hangs && item.breakBefore &&
			len(line) > 0 && used.Add(item.width) > width {
			return trimLineEdge(line), i, outOfFlow, false
		}

		// A single item wider than the line has nowhere to go. It is placed and
		// overflows — breaking inside a word would be worse, since a word split
		// at an arbitrary point reads as a different word — and it is reported,
		// because the part past the edge is simply not drawn and nothing else
		// about the page says so.
		if item.width > width && len(line) == 0 && !item.space && !item.noWrap {
			l.reportOverflow(item, width)
		}
		line = append(line, item)
		used = used.Add(item.width)
	}
	return trimLineEdge(line), i, outOfFlow, false
}

// reportOverflow names content too wide for the box holding it.
//
// It is reported once per piece of text rather than once per line, because a
// paragraph containing one impossible word would otherwise complain on every
// line it wraps to.
func (l *layouter) reportOverflow(item inlineItem, width style.Unit) {
	what := "the text " + quoteValue(item.text)
	key := item.text
	if item.atomic != nil {
		// A replaced element has no text to name it by, and two different
		// images of the same width are two findings rather than one — so the
		// key is where it is in the document rather than what it says.
		what = "the image"
		key = "\x00replaced\x00" + PathOf(item.box.Element)
	}
	if l.reportedOverflow[key] {
		return
	}
	l.reportedOverflow[key] = true
	l.rec.ReportDetail(Finding{
		Rule: RuleUnbreakableOverflow,
		Message: what + " is " +
			fmtPx(item.width) + " wide and cannot be broken, in a space " +
			fmtPx(width) + " wide; the part past the edge will not be drawn",
		Path: PathOf(item.box.Element),
	})
}

func fmtPx(u style.Unit) string {
	return strconvFormat(u.Px()) + "px"
}

// trimLineEdge is §4.1.2's third rule: a sequence of collapsible spaces at the
// end of a line is removed.
//
// CSS Text removes it because it is the space the break happened at: leaving it
// would make a right-aligned line hang, and a centred one sit off-centre by half
// a space.
//
// Preserved white space is *not* removed, and the difference is deliberate: it
// hangs instead, so it stays in the runs, which is what a reader copying text
// out of the page gets. Removing it would silently drop characters the author
// wrote from the document's text, which is the same class of fault as the
// missing spaces that once made "A heading" extract as "Aheading".
//
// What the hanging currently affects is the break decision — hangs is what
// stops a trailing space pushing the next word down a line — and nothing else,
// because §4.1.2's other consumers of it are alignment and justification and
// this engine does neither. See the note on text-align in inlineContent.
func trimLineEdge(line []inlineItem) []inlineItem {
	for len(line) > 0 && line[len(line)-1].collapsible {
		line = line[:len(line)-1]
	}
	return line
}

// lineHeight resolves the line-height property.
//
// "normal" is the face's own recommendation, which for the metrics this engine
// has means about 1.2 times the size — the figure every renderer uses when a
// face does not say otherwise. A bare number is a multiplier rather than a
// length, which is the one place in CSS where that is true and the reason
// line-height is usually written that way: a multiplier inherits as a ratio and
// a length inherits as a fixed distance.
func (l *layouter) lineHeight(b *Box) style.Unit {
	value := strings.ToLower(strings.TrimSpace(b.Style["line-height"]))
	if value == "" || value == "normal" {
		return b.FontSize.Mul(1.2)
	}
	if n, ok := parseNumber(value); ok {
		return b.FontSize.Mul(n)
	}
	if length, ok := l.parseLength(b, "line-height"); ok {
		if v, ok := length.Resolve(b.FontSize, true); ok {
			return v
		}
	}
	return b.FontSize.Mul(1.2)
}

// baselineOf is where the text sits within a line box.
//
// The line box is usually taller than the text, and the difference — the leading
// — is split equally above and below it. That is what makes a paragraph's lines
// evenly spaced rather than crowded against their tops.
func (l *layouter) baselineOf(b *Box, lineHeight style.Unit) style.Unit {
	face, ok := l.fontFor(b)
	if !ok {
		return lineHeight.Mul(0.8)
	}
	d := face.Descriptor()
	unitsPerEm := float64(face.UnitsPerEm())
	if unitsPerEm == 0 {
		return lineHeight.Mul(0.8)
	}
	ascent := b.FontSize.Mul(float64(d.Ascent) / unitsPerEm)
	descent := b.FontSize.Mul(-float64(d.Descent) / unitsPerEm)
	halfLeading := lineHeight.Sub(ascent).Sub(descent).Div(2)
	return halfLeading.Add(ascent)
}

// parseNumber reads a bare number, which line-height accepts as a multiplier.
func parseNumber(s string) (float64, bool) {
	var v float64
	var seenDigit, seenDot bool
	frac := 0.1
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
			if seenDot {
				v += float64(c-'0') * frac
				frac /= 10
			} else {
				v = v*10 + float64(c-'0')
			}
		case c == '.' && !seenDot:
			seenDot = true
		default:
			return 0, false
		}
	}
	return v, seenDigit
}

// checkScript reports text this engine cannot break or order correctly.
//
// It is the unsupported-script guardrail of §6.3, and it is an error by default
// for the reason given there: unbroken or unordered text still looks like text,
// so the failure mode looks like success. A paragraph of Thai run together as
// one word overflows silently; a line of Arabic laid out left to right reads as
// a rendering bug rather than as something this engine declined to do.
func (l *layouter) checkScript(b *Box) {
	for _, r := range b.Text {
		if script, bad := unsupportedScript(r); bad {
			key := script + "\x00" + b.Style["font-family"]
			if l.reportedScripts[key] {
				return
			}
			l.reportedScripts[key] = true
			l.rec.ReportDetail(Finding{
				Rule:    RuleUnsupportedScript,
				Message: script,
				Path:    PathOf(b.Element),
			})
			return
		}
	}
}

// checkGlyphs reports characters the chosen face has no glyph for.
//
// This is the glyph-missing guardrail of §6.3, an error by default because tofu
// is the purest form of silent garbage: a reader who sees a row of boxes where
// letters should be blames their PDF viewer, not the document, and the author
// never hears about it at all.
//
// It is reported once per character rather than once per occurrence, because
// what an author needs to know is *which* characters their font cannot set —
// hearing it four hundred times about the same one is not four hundred times as
// useful.
func (l *layouter) checkGlyphs(b *Box, face *fonts.Face) {
	for _, r := range b.Text {
		if r == '\n' || r == '\t' {
			continue
		}
		if _, ok := face.GlyphID(r); ok {
			continue
		}
		key := string(r) + "\x00" + face.Name()
		if l.reportedGlyphs[key] {
			continue
		}
		l.reportedGlyphs[key] = true
		l.rec.ReportDetail(Finding{
			Rule: RuleGlyphMissing,
			Message: "the face " + quoteValue(face.Name()) + " has no glyph for " +
				describeRune(r) + ", which would be drawn as a blank box",
			Path: PathOf(b.Element),
		})
	}
}

// describeRune names a character for a diagnostic, by code point as well as by
// its shape — the shape is what the author recognises and the code point is what
// they can search for, and a character with no glyph often cannot be shown at
// all in whatever is reading the report.
func describeRune(r rune) string {
	out := "U+" + strings.ToUpper(hex(uint32(r)))
	if unicode.IsPrint(r) {
		out += " (" + string(r) + ")"
	}
	return out
}

func hex(v uint32) string {
	const digits = "0123456789abcdef"
	if v == 0 {
		return "0000"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{digits[v&0xf]}, b...)
		v >>= 4
	}
	for len(b) < 4 {
		b = append([]byte{'0'}, b...)
	}
	return string(b)
}

// unsupportedScript names why a rune cannot be laid out, or reports false.
func unsupportedScript(r rune) (string, bool) {
	switch {
	// Right-to-left. Shaping these is done in forme; ordering them within a
	// broken line is not done here, and text laid out in the wrong order is
	// text that reads as nonsense while looking like a font problem.
	case r >= 0x0590 && r <= 0x05FF, // Hebrew
		r >= 0x0600 && r <= 0x06FF, // Arabic
		r >= 0x0700 && r <= 0x074F, // Syriac
		r >= 0x0780 && r <= 0x07BF, // Thaana
		r >= 0x07C0 && r <= 0x08FF, // NKo, Samaritan, Arabic Extended
		r >= 0xFB1D && r <= 0xFDFF, // Hebrew and Arabic presentation forms
		r >= 0xFE70 && r <= 0xFEFF:
		return "right-to-left text needs the bidirectional algorithm applied to " +
			"each line, which is not implemented; it would be laid out in the wrong order", true

	// Scripts with no spaces between words, which need a dictionary to know
	// where a line may break.
	case r >= 0x0E00 && r <= 0x0E7F, // Thai
		r >= 0x0E80 && r <= 0x0EFF, // Lao
		r >= 0x1780 && r <= 0x17FF, // Khmer
		r >= 0x1000 && r <= 0x109F: // Myanmar
		return "this script writes no spaces between words, so finding a line " +
			"break needs a dictionary, which is not implemented; the text would " +
			"run on as one unbreakable word", true
	}
	return "", false
}

// strconvFormat renders a length for a diagnostic, to a tenth of a pixel — more
// precision than that is noise in a message a person reads.
func strconvFormat(v float64) string {
	return strconv.FormatFloat(float64(int(v*10+0.5))/10, 'f', -1, 64)
}
