package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/style"
)

// Block layout: the fifth of §3's stages, turning boxes into fragments with a
// resolved position and size in absolute page coordinates.
//
// What is here is the block formatting context of CSS 2.1 §9.4.1 and §10 — the
// widths, the box model, and margin collapsing. Inline layout is not, and its
// absence is deliberate rather than partial: measuring a line needs a font and a
// line-breaking algorithm, and a guess at either would produce geometry that is
// wrong in a way nothing about the output would reveal. A block whose content is
// inline therefore has no content height yet, and the render says so once.
//
// # Why margin collapsing is here and not later
//
// It is the part of block layout that cannot be added afterwards. Collapsing
// changes where every subsequent box goes, so an engine that positioned boxes
// first and collapsed margins second would have to move everything it had
// placed — and the rules about *which* margins collapse depend on borders and
// padding that are themselves being resolved. Doing it in the same pass is what
// keeps it a single traversal.

// Fragment is a box with a resolved geometry.
//
// A box produces one fragment here. It will produce more than one when content
// can break — a paragraph across two columns, an inline across two lines — which
// is why this is a fragment rather than simply a laid-out box, and why the type
// is named for the thing that can be plural.
type Fragment struct {
	// Box is what generated this fragment.
	Box *Box

	// BorderRect is the border box in absolute page coordinates: the area a
	// background paints and a border draws on.
	BorderRect Rect

	// Margin, Border and Padding are the resolved edges. They are kept rather
	// than folded into rectangles because painting needs them separately — a
	// border is drawn on its own width — and because the guardrails ask about
	// them by name.
	Margin, Border, Padding Edges

	Children []*Fragment
}

// ContentRect is the area the children were laid out in.
func (f *Fragment) ContentRect() Rect {
	return f.BorderRect.Inset(f.Border.Add(f.Padding))
}

// PaddingRect is the border box minus its border.
func (f *Fragment) PaddingRect() Rect { return f.BorderRect.Inset(f.Border) }

// MarginRect is the border box plus its margin, which is the space the box
// actually occupies in its parent's flow.
func (f *Fragment) MarginRect() Rect { return f.BorderRect.Outset(f.Margin) }

// Layout positions a box tree inside an available width, returning the root
// fragment.
//
// avail is the content box of the page: what §11 calls the page box minus its
// margins. Only the width constrains layout — the height is what comes out, and
// whether it fits is §5's scale-to-fit question, asked afterwards.
func Layout(root *Box, avail Size, rec *Recorder) *Fragment {
	if root == nil {
		return nil
	}
	l := &layouter{rec: rec, avail: avail, lengths: map[lengthKey]style.Length{}}
	frag := l.block(root, Point{}, avail.W)
	l.reportInlineGap()
	l.reportCollapseGap()
	return frag
}

type layouter struct {
	rec   *Recorder
	avail Size

	// lengths memoizes parsing a computed value into a Length.
	//
	// The cascade stores computed values as text, so laying out a document
	// re-parses "1em" once per box per property — a thousand elements with ten
	// box properties each is ten thousand tokenizer runs of a string that is
	// almost always one of a handful. The key includes the font size because
	// that is what an em resolves against.
	lengths map[lengthKey]style.Length

	// sawInline records that some block had inline content, so the gap is
	// reported once rather than per box.
	sawInline bool
	// sawParentChildCollapse records that a document met the margin-collapsing
	// case this engine does not implement. See reportCollapseGap.
	sawParentChildCollapse bool
}

type lengthKey struct {
	value    string
	fontSize style.Unit
}

// reportInlineGap says once that inline content was not measured.
//
// Once, rather than per box: every document with text would otherwise produce a
// finding per paragraph, and a report that long is one nobody reads. It is a
// limit rather than an unsupported feature — the engine stopped short of doing
// something it is going to do — which is the distinction RuleLimit exists for.
func (l *layouter) reportInlineGap() {
	if !l.sawInline {
		return
	}
	l.rec.Report(RuleLimit, NoSource,
		"inline layout is not implemented yet, so text contributes no height "+
			"and every block containing only text was laid out as empty")
}

// reportCollapseGap says that a margin which should have collapsed through a
// parent did not.
//
// Sibling margins collapse; a parent's margin collapsing with its first or last
// child's does not, and that half is not implemented. Reporting it is not a
// formality — the difference is visible geometry, so a document where it applies
// is laid out differently here than in a browser, and a page that is quietly
// different is exactly what §6 exists to prevent.
//
// It is reported only when it would actually change something: a box whose first
// or last in-flow child has a non-zero adjoining margin, with no border or
// padding between them to stop the collapse. A document where the case never
// arises hears nothing, which is what keeps the report worth reading.
func (l *layouter) reportCollapseGap() {
	if !l.sawParentChildCollapse {
		return
	}
	l.rec.Report(RuleLimit, NoSource,
		"a margin that should collapse through a parent did not: margins between "+
			"siblings collapse, but a parent's own margin collapsing with its "+
			"first or last child's is not implemented, so those boxes sit further "+
			"apart here than in a browser")
}

// block lays out a block-level box at a position, inside a containing width,
// and returns its fragment.
//
// The position given is the top left of the *border box*: the caller has
// already decided where the margin puts it, because deciding that is what
// margin collapsing does and only the caller can see both sides of a collapse.
func (l *layouter) block(b *Box, at Point, containing style.Unit) *Fragment {
	margin := l.edges(b, "margin", containing)
	border := l.borderWidths(b)
	padding := l.edges(b, "padding", containing)

	width := l.resolveWidth(b, margin, border, padding, containing, &margin)

	frag := &Fragment{
		Box:     b,
		Margin:  margin,
		Border:  border,
		Padding: padding,
		BorderRect: Rect{
			X: at.X, Y: at.Y,
			W: width.Add(padding.Horizontal()).Add(border.Horizontal()),
		},
	}

	contentX := frag.BorderRect.X.Add(border.Left).Add(padding.Left)
	contentY := frag.BorderRect.Y.Add(border.Top).Add(padding.Top)
	contentHeight := l.children(b, frag, Point{X: contentX, Y: contentY}, width)

	// An explicit height replaces the content's own, which is what makes
	// overflow possible at all.
	if h, ok := l.explicitHeight(b, containing); ok {
		contentHeight = h
	}
	contentHeight = l.clampHeight(b, contentHeight, containing)

	frag.BorderRect.H = contentHeight.Add(padding.Vertical()).Add(border.Vertical())
	return frag
}

// children lays out a box's children and returns the content height they need.
func (l *layouter) children(b *Box, parent *Fragment, at Point, width style.Unit) style.Unit {
	if len(b.Children) == 0 {
		return 0
	}

	// A block container's children are either all block-level or all
	// inline-level — the anonymous box rule guarantees it — so this is a
	// two-way choice rather than a mixture.
	if b.Children[0].Outer == OuterInline {
		// Inline content. Measuring it needs a font and a line-breaking
		// algorithm; until those exist it occupies no height, and the render
		// says so once.
		l.sawInline = true
		return 0
	}

	y := at.Y
	// prevBottom is the bottom margin left uncollapsed by the previous sibling,
	// waiting to be collapsed with the next one's top margin.
	var prevBottom style.Unit
	first := true

	for _, child := range b.Children {
		if child.Outer != OuterBlock {
			continue
		}
		childMargin := l.edges(child, "margin", width)

		// Adjoining vertical margins collapse into one, and the one is not
		// their sum: positives take the largest, negatives the most negative,
		// and a mixture adds those two. That rule is why two paragraphs with
		// 1em margins are 1em apart and not 2em.
		var gap style.Unit
		if first {
			// The first child's top margin should collapse with the parent's
			// own when nothing separates them. It does not here, so the case is
			// noticed and reported rather than being silently different.
			if parent.Border.Top == 0 && parent.Padding.Top == 0 && childMargin.Top != 0 {
				l.sawParentChildCollapse = true
			}
			gap = childMargin.Top
			first = false
		} else {
			gap = collapse(prevBottom, childMargin.Top)
		}
		y = y.Add(gap)

		frag := l.block(child, Point{X: at.X, Y: y}, width)
		// The child's own margin box positions it horizontally; the vertical
		// margins were dealt with above and must not be applied twice.
		frag.BorderRect.X = at.X.Add(frag.Margin.Left)
		parent.Children = append(parent.Children, frag)

		y = frag.BorderRect.Bottom()
		prevBottom = frag.Margin.Bottom
	}
	// The last child's bottom margin is inside the content height here. It
	// should collapse with the parent's own bottom margin when nothing
	// separates them; that half is not implemented, so the case is reported.
	if parent.Border.Bottom == 0 && parent.Padding.Bottom == 0 && prevBottom != 0 {
		l.sawParentChildCollapse = true
	}
	return y.Sub(at.Y).Add(prevBottom)
}

// collapse combines two adjoining margins.
//
// The largest positive and the most negative are taken separately and then
// added, which is the rule of CSS 2.1 §8.3.1. It is not max() and it is not a
// sum: 20px against -30px is -10px, and 20px against 10px is 20px.
func collapse(a, b style.Unit) style.Unit {
	pos, neg := style.Unit(0), style.Unit(0)
	for _, m := range [2]style.Unit{a, b} {
		if m > pos {
			pos = m
		}
		if m < neg {
			neg = m
		}
	}
	return pos.Add(neg)
}

// resolveWidth computes a block-level box's content width by the constraint of
// CSS 2.1 §10.3.3: the margins, borders, padding and width across the box add up
// to the containing block's width.
//
// It may adjust the margins, which is why they are passed by pointer: "auto" on
// both sides is what centres a box, and it can only be resolved once the width
// is known.
func (l *layouter) resolveWidth(b *Box, margin, border, padding Edges,
	containing style.Unit, out *Edges) style.Unit {

	fixed := border.Horizontal().Add(padding.Horizontal())
	available := containing.Sub(fixed)

	declared, hasWidth := l.explicitWidth(b, containing)
	marginLeftAuto := l.isAuto(b, "margin-left")
	marginRightAuto := l.isAuto(b, "margin-right")

	if !hasWidth {
		// An auto width fills whatever the margins leave, which is why a plain
		// <div> is as wide as its parent. An auto margin against an auto width
		// is zero — there is nothing left over to distribute.
		if marginLeftAuto {
			out.Left = 0
		}
		if marginRightAuto {
			out.Right = 0
		}
		width := available.Sub(out.Horizontal())
		return l.clampWidth(b, maxZero(width), containing)
	}

	width := l.clampWidth(b, declared, containing)
	slack := available.Sub(width).Sub(margin.Horizontal())

	switch {
	case marginLeftAuto && marginRightAuto:
		// Centred. An odd number of units puts the extra on the right, which is
		// arbitrary but has to be decided somewhere, and deciding it here keeps
		// it reproducible.
		half := slack.Div(2)
		out.Left, out.Right = half, slack.Sub(half)
	case marginLeftAuto:
		out.Left = slack
	case marginRightAuto:
		out.Right = slack
	default:
		// Over-constrained: the specification resolves it by ignoring the right
		// margin, which keeps the box where its left margin put it.
		out.Right = margin.Right.Add(slack)
	}
	return width
}

func maxZero(u style.Unit) style.Unit {
	if u < 0 {
		return 0
	}
	return u
}

// clampWidth and clampHeight apply the min and max properties, with the CSS
// rule that a minimum wins over a maximum.
func (l *layouter) clampWidth(b *Box, v, containing style.Unit) style.Unit {
	lo := style.Unit(0)
	if min, ok := l.lengthOf(b, "min-width", containing); ok {
		lo = min
	}
	hi := style.MaxUnit
	if max, ok := l.lengthOf(b, "max-width", containing); ok {
		hi = max
	}
	return style.Clamp(v, lo, hi)
}

func (l *layouter) clampHeight(b *Box, v, containing style.Unit) style.Unit {
	lo := style.Unit(0)
	if min, ok := l.lengthOf(b, "min-height", containing); ok {
		lo = min
	}
	hi := style.MaxUnit
	if max, ok := l.lengthOf(b, "max-height", containing); ok {
		hi = max
	}
	return style.Clamp(v, lo, hi)
}

func (l *layouter) explicitWidth(b *Box, containing style.Unit) (style.Unit, bool) {
	return l.lengthOf(b, "width", containing)
}

// explicitHeight resolves a declared height.
//
// A percentage height resolves against the containing block's *height*, which is
// indefinite while that block is itself being sized by its content — so a
// percentage here is refused rather than resolved against the width, which is a
// mistake that produces a plausible number.
func (l *layouter) explicitHeight(b *Box, containing style.Unit) (style.Unit, bool) {
	l.ensureFontSize(b)
	length, ok := l.parseLength(b, "height")
	if !ok || length.Kind != style.LengthAbsolute {
		return 0, false
	}
	return length.Value, true
}

// lengthOf resolves a property to a length, with percentages against a basis.
func (l *layouter) lengthOf(b *Box, property string, basis style.Unit) (style.Unit, bool) {
	length, ok := l.parseLength(b, property)
	if !ok {
		return 0, false
	}
	return length.Resolve(basis, true)
}

// isAuto reports whether a property is the auto keyword.
func (l *layouter) isAuto(b *Box, property string) bool {
	length, ok := l.parseLength(b, property)
	return ok && length.Kind == style.LengthAuto
}

// parseLength reads one of a box's computed values, memoized.
func (l *layouter) parseLength(b *Box, property string) (style.Length, bool) {
	raw := strings.TrimSpace(b.Style[property])
	if raw == "" {
		return style.Length{}, false
	}
	key := lengthKey{value: raw, fontSize: b.FontSize}
	if got, ok := l.lengths[key]; ok {
		return got, true
	}

	vals, _ := css.ParseComponentValues(raw)
	length, _, ok := style.ParseLength(vals, style.LengthContext{
		FontSize:       b.FontSize,
		RootFontSize:   b.FontSize,
		ViewportWidth:  l.avail.W,
		ViewportHeight: l.avail.H,
		ViewportKnown:  true,
	})
	if !ok {
		return style.Length{}, false
	}
	l.lengths[key] = length
	return length, true
}

func (l *layouter) ensureFontSize(b *Box) {
	if b.FontSize == 0 {
		b.FontSize = defaultFontSize
	}
}

// edges resolves the four sides of a margin or padding.
//
// Percentages on *both* axes resolve against the containing block's width, which
// looks wrong and is not: it is what makes a percentage padding produce a box
// with a constant aspect ratio, and resolving the vertical ones against the
// height would need a height that is usually not known yet.
func (l *layouter) edges(b *Box, prefix string, containing style.Unit) Edges {
	side := func(name string) style.Unit {
		v, ok := l.lengthOf(b, prefix+"-"+name, containing)
		if !ok {
			return 0
		}
		if prefix == "padding" && v < 0 {
			// A negative padding is not a thing; the declaration is invalid and
			// the initial value stands.
			return 0
		}
		return v
	}
	return Edges{
		Top:    side("top"),
		Right:  side("right"),
		Bottom: side("bottom"),
		Left:   side("left"),
	}
}

// borderWidths resolves the four border widths, which are zero unless a style is
// set — "border-width: 5px" with no "border-style" draws nothing and occupies
// nothing, which is a rule that surprises people and is in the specification for
// a reason: it lets a width be declared once and switched on per side.
func (l *layouter) borderWidths(b *Box) Edges {
	side := func(name string) style.Unit {
		if noBorder(b.Style["border-"+name+"-style"]) {
			return 0
		}
		v, ok := l.lengthOf(b, "border-"+name+"-width", 0)
		if !ok {
			// The keyword widths. They are the only place a border width is not
			// a length, and "medium" is the initial value.
			return keywordBorderWidth(b.Style["border-"+name+"-width"])
		}
		return maxZero(v)
	}
	return Edges{
		Top:    side("top"),
		Right:  side("right"),
		Bottom: side("bottom"),
		Left:   side("left"),
	}
}

func noBorder(styleValue string) bool {
	switch strings.ToLower(strings.TrimSpace(styleValue)) {
	case "", "none", "hidden":
		return true
	}
	return false
}

// keywordBorderWidth is the thin/medium/thick scale, which the specification
// leaves to the engine beyond requiring thin <= medium <= thick. These are the
// values every browser uses.
func keywordBorderWidth(value string) style.Unit {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "thin":
		return mustPx(1)
	case "thick":
		return mustPx(5)
	case "medium":
		return mustPx(3)
	}
	return 0
}
