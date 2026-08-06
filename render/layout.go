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
// widths, the box model, margin collapsing, and where the out-of-flow boxes of
// §9.5 go. Inline layout is next door in inline.go, which is a division by
// formatting context rather than by convenience: a block container's children
// are all block-level or all inline-level, never a mixture, so the two walks
// never interleave. Floats are the one thing that crosses the line, because a
// float can be written among either and its geometry is read by both.
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

	// Lines is the inline content of a block container, in the same coordinates
	// as its children.
	//
	// A fragment has children or lines, with one exception: a float is out of
	// flow, so a block whose in-flow content is entirely inline can still have
	// floated children beside those lines. The anonymous box rule deliberately
	// does not wrap a float, because wrapping it would put it in a different
	// formatting context from the text meant to run around it.
	Lines []LineFragment

	// Marker is the bullet or number a list item generates, nil otherwise. It
	// is on the fragment rather than in the box tree because its text depends
	// on the item's position among its siblings, which is not a property of the
	// box.
	Marker *Marker

	// collapsed is the grid lines of a table using §17.6.2's collapsing border
	// model, in coordinates relative to this fragment's border box.
	//
	// They hang off the table rather than off the cells because that is whose
	// borders they are: a collapsed border is centred on a grid line and belongs
	// half to the cell on each side, so neither cell can draw it.
	collapsed []collapsedBand
	// inCollapsedGrid marks a fragment whose own border is part of that grid —
	// the table and every cell in it — and so must not be painted here.
	//
	// It is separate from the list because a table with no borders anywhere still
	// must not fall back to painting the one it declared: that border took part
	// in the conflict resolution like every other candidate, and losing is not
	// the same as being absent.
	inCollapsedGrid bool

	// Offset is CSS 2.1 §9.4.3's relative displacement: how far the box is drawn
	// from where the flow put it.
	//
	// It is carried rather than folded into BorderRect because folding it in
	// during layout would be the classic way to get relative positioning wrong.
	// A relatively positioned box still *occupies* its original space, so every
	// question layout asks afterwards — where the next sibling goes, how tall
	// the parent is, which band a line box has — must be answered against the
	// unoffset position. Applying the offset in absolutise, which visits each
	// fragment once and already translates each subtree by its parent's origin,
	// moves the box and everything inside it and moves nothing else.
	Offset Point
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
//
// set supplies the faces; a nil one uses the fourteen standard faces, which need
// no embedding and cover Latin.
func Layout(root *Box, avail Size, set FontSet, rec *Recorder) *Fragment {
	if root == nil {
		return nil
	}
	l := &layouter{
		rec: rec, avail: avail,
		lengths:          map[lengthKey]style.Length{},
		fonts:            map[fontKey]resolvedFont{},
		measured:         map[measureKey]style.Unit{},
		reportedScripts:  map[string]bool{},
		reportedGlyphs:   map[string]bool{},
		reportedOverflow: map[string]bool{},
		reportedJustify:  map[string]bool{},
		decorations:      map[*Box][]textDecoration{},
		intrinsic:        map[*Box]intrinsicWidths{},
		grids:            map[*Box]*tableGrid{},
		tableDemands:     map[*Box][]tableColumnDemand{},
		collapsed:        map[*Box]*collapsedGrid{},
		positioned:       map[*Box]*Fragment{},
		fontSet:          set,
		rootFontSize:     root.FontSize,
	}
	if l.rootFontSize == 0 {
		l.rootFontSize = defaultFontSize
	}
	if l.fontSet == nil {
		l.fontSet = StandardFonts()
	}
	// The root box establishes the outermost block formatting context, so no
	// float in the document can escape the page. The context handed in here is
	// therefore a placeholder that nothing will ever be put in — block() makes a
	// fresh one for any box that establishes a context, and the root is one.
	// The initial containing block is the page's content box: this engine has
	// one page whose size is settled before layout runs, so unlike a viewport it
	// has a definite height, and a percentage resolved against it is a number
	// rather than an indefinite value that computes to auto.
	page := Rect{W: avail.W, H: avail.H}
	frag, m := l.block(root, avail.W,
		flow{ctx: &floatContext{}, cbHeight: avail.H, cbDefinite: true})

	// Layout runs in coordinates relative to each parent's content box, because
	// a box's position is not known until its margins have finished collapsing
	// with its descendants' — and that is only settled after its subtree is
	// laid out. Absolute coordinates are then one pass over the finished tree.
	//
	// The alternative, translating each subtree as it is placed, costs a walk
	// per level; this costs one walk in total.
	// A floated root element has no containing block to be shifted within
	// except the page itself, and no siblings to make room for — so all that is
	// left of §9.5 for it is which edge it goes against. Its width already
	// shrank to fit, and leaving the position alone would honour half of the
	// declaration and quietly drop the visible half.
	if root.Float == FloatRight {
		frag.BorderRect.X = avail.W.Sub(frag.Margin.Right).Sub(frag.BorderRect.W)
	}

	absolutise(frag, 0, m.top)

	// Everything out of flow is placed now, against a tree that is already in
	// page coordinates. That ordering is the whole design of position.go: an
	// absolutely positioned box resolves against an *ancestor's* box, which does
	// not exist as a rectangle until this point, and it may do so because
	// nothing in the flow depends on it in return.
	l.placeAbsolutes(page)

	// The root element is the one box the walk above cannot take out of the
	// flow, because nothing walked *to* it: it is where layout starts, it has no
	// parent to record a static position against, and its fragment is the return
	// value rather than a child of anything. "html { position: absolute }" is
	// rare enough that giving Layout a second entry point for it would be
	// machinery for a corner — but laying it out in the flow and saying nothing
	// is the silent approximation this engine exists not to make.
	if root.Position.outOfFlow() {
		l.rec.ReportDetail(Finding{
			Rule:   RulePositionApproximated,
			Source: AtHTML(offsetOf(root)),
			Message: "the root element is taken out of the flow, but it is what the " +
				"flow starts from; it was laid out in place and its offsets were not applied",
			Path:     PathOf(root.Element),
			Property: "position",
		})
	}
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
	// fonts and measured memoize the two things inline layout asks for most: a
	// face for a style, and the width of a string in one.
	fonts    map[fontKey]resolvedFont
	measured map[measureKey]style.Unit
	// intrinsic memoizes the two content-based widths of a box, which are what
	// a float with an auto width is sized by.
	intrinsic map[*Box]intrinsicWidths
	// grids and tableDemands memoize the two expensive answers about a table:
	// where its cells sit in the grid, and what each column asks for. Both are
	// wanted once while the table's width is being resolved and again while it
	// is laid out, so a table nested n deep would cost 2^n without them.
	grids        map[*Box]*tableGrid
	tableDemands map[*Box][]tableColumnDemand
	// collapsed memoizes §17.6.2's resolved grid lines, for the same reason and
	// with one more: every cell asks for it while its own border is resolved, so
	// without the memo a table would re-run the conflict resolution once per
	// cell.
	collapsed map[*Box]*collapsedGrid
	// deferred holds the out-of-flow boxes met during the walk, to be placed
	// once the tree is in absolute coordinates. See position.go for why they
	// can wait and floats cannot.
	deferred []absCandidate
	// positioned maps each positioned box to its fragment, which is how an
	// absolutely positioned box finds the containing block §10.1 gives it. It is
	// a map rather than a walk up the fragment tree because a fragment does not
	// know its parent — layout builds downwards — and giving it one would add a
	// pointer to every fragment to answer a question a handful of boxes ask.
	positioned map[*Box]*Fragment
	// fontSet is where faces come from.
	fontSet FontSet
	// rootFontSize is the font-size of the root element, which is what "rem"
	// resolves against. It is settled once, before the walk: the point of rem is
	// that it does not compound as elements nest, so reading it from the box in
	// hand — as this did — makes it a synonym for em.
	rootFontSize style.Unit
	// relayouts counts the subtrees that had to be laid out a second time
	// because the position predicted for them turned out to be wrong. It is
	// bounded: see maxRelayouts.
	relayouts int
	// reportedScripts and reportedGlyphs suppress repeating a complaint that is
	// about a script or a character rather than about a place.
	reportedScripts map[string]bool
	reportedGlyphs  map[string]bool
	// reportedOverflow suppresses repeating one run's overflow per line.
	reportedOverflow map[string]bool
	// reportedJustify suppresses repeating the justification gap per line.
	reportedJustify map[string]bool
	// decorations memoizes the text decorations drawn across each box, which are
	// built from the box's parent's — see decorationsFor.
	decorations map[*Box][]textDecoration
}

type lengthKey struct {
	value    string
	fontSize style.Unit
	// zeroAdvance is part of the key because "ch" resolves against the face, so
	// two boxes at the same size in different fonts do not share an answer.
	// Leaving it out would have made the first font to parse "40ch" decide it
	// for every other — a memoization bug, which is the kind that produces a
	// wrong page only when a document uses two fonts.
	zeroAdvance style.Unit
}

// collapsed is what a box contributes to its parent's flow once its own margins
// have collapsed with its descendants'.
//
// through marks a box with nothing to separate its own two margins — no border,
// no padding, no content, no height — so they collapse into one another and the
// box's neighbours end up adjoining each other through it. An empty <div>
// between two paragraphs is the everyday case, and an engine that misses it puts
// a gap where the author sees none.
type collapsed struct {
	top, bottom style.Unit
	through     bool
}

// absolutise turns positions relative to each parent's content box into absolute
// page coordinates, in one pass over the finished tree.
//
// It is also where §9.4.3's relative offset is applied, and the two belong
// together: the offset is a translation of a whole subtree, which is precisely
// what this walk already performs at every level. Adding it here means a
// relatively positioned box carries its descendants with it for free and moves
// nothing else at all, because by the time this runs every position in the flow
// has already been decided against the unoffset geometry.
func absolutise(f *Fragment, x, y style.Unit) {
	if f == nil {
		return
	}
	f.BorderRect.X = f.BorderRect.X.Add(x).Add(f.Offset.X)
	f.BorderRect.Y = f.BorderRect.Y.Add(y).Add(f.Offset.Y)

	content := f.ContentRect()
	for _, c := range f.Children {
		absolutise(c, content.X, content.Y)
	}
}

// block lays out a block-level box at a position, inside a containing width,
// and returns its fragment.
//
// The position given is the top left of the *border box*: the caller has
// already decided where the margin puts it, because deciding that is what
// margin collapsing does and only the caller can see both sides of a collapse.
// at is where the box's own border box sits inside the formatting context: the
// containing block's content left edge, and the flow position the caller has
// reached. The caller cannot supply the box's own left edge because that depends
// on margins which may be auto, and auto margins are resolved here.
func (l *layouter) block(b *Box, containing style.Unit, at flow) (*Fragment, collapsed) {
	return l.blockIn(b, containing, at, nil)
}

// blockIn is block layout with the option of having the box's width, margins and
// height decided by the caller instead of by its own declarations.
//
// The only caller that supplies them is the absolute placement of position.go,
// whose §10.3.7 constraint resolves a width against a containing block this walk
// cannot see. Routing it through the same function rather than giving it a
// layout of its own is deliberate: margin collapsing, floats, line breaking,
// list markers and the height rules are identical for an absolutely positioned
// box, and a second implementation of them would agree with this one on the day
// it was written and on no day after.
func (l *layouter) blockIn(b *Box, containing style.Unit, at flow,
	forced *forcedGeometry) (*Fragment, collapsed) {

	// The two values this engine understands and does not act on for a box of
	// this shape. Both are asked once per box here rather than by a pass of their
	// own, because this is the one function every block-level box goes through.
	l.checkTableBoxSizing(b)
	l.checkVisibility(b)

	margin := l.edges(b, "margin", containing)
	if b.Inner == InnerTable {
		// §17.4 moved the margins to the wrapper. Reading them here as well
		// would apply them twice: the wrapper would be indented and the table
		// would be indented again inside it.
		margin = Edges{}
	}
	border := l.borderWidths(b)
	padding := l.paddingOf(b, containing)

	// A replaced box is sized from its content rather than from its containing
	// block, on both axes at once, and the answer is then handed to the
	// ordinary block arithmetic as though the author had declared it. That is
	// exactly what CSS 2.1 §10.3.4 and §10.6.5 say to do — the margin rules for
	// a block-level replaced element are the same ones as for any other block,
	// applied to a width that came from somewhere else.
	var replaced *Size
	if b.Replaced != nil {
		s := l.replacedSize(b, containing, at.cbHeight, at.cbDefinite)
		replaced = &s
	}

	width := l.resolveWidth(b, margin, border, padding, containing, &margin, replaced)
	declaredHeight, hasHeight := l.explicitHeight(b, containing)
	if replaced != nil {
		declaredHeight, hasHeight = replaced.H, true
	}
	if forced != nil {
		margin, width = forced.margin, forced.width
		if forced.hasHeight {
			declaredHeight, hasHeight = forced.height, true
		}
	}

	// A box that establishes a block formatting context seals both its edges.
	// CSS 2.1 §8.3.1 puts it the other way round — the margins of such a box do
	// not collapse with its in-flow children — but the effect is the same as a
	// border of zero width that a margin cannot cross, and expressing it here
	// keeps the collapsing rules in one place. Leaving it out is what would make
	// a floated <div> holding a <p> sit an em above where the author put it.
	sealed := establishesBFC(b)

	// A margin collapses through an edge only when nothing sits on that edge to
	// stop it. A border or a padding of even one unit is something.
	topOpen := border.Top == 0 && padding.Top == 0 && !sealed
	// The bottom edge also needs the height to be the content's own: a declared
	// height is a floor the margin cannot reach across.
	bottomOpen := border.Bottom == 0 && padding.Bottom == 0 && !hasHeight && !sealed

	frag := &Fragment{
		Box:     b,
		Margin:  margin,
		Border:  border,
		Padding: padding,
		BorderRect: Rect{
			// Relative to the parent's content box; absolutise fixes it later.
			X: margin.Left,
			W: width.Add(padding.Horizontal()).Add(border.Horizontal()),
		},
	}
	if b.Position == PositionRelative {
		frag.Offset = l.relativeOffset(b, containing, at.cbHeight, at.cbDefinite)
	}
	if b.Position.positioned() {
		// Recorded even for a box that is only relatively positioned, because
		// §10.1 makes any positioned ancestor a containing block — that is the
		// entire reason the "position: relative with no offsets" wrapper is an
		// idiom rather than a no-op.
		l.positioned[b] = frag
	}

	// Where this box's children are laid out, in the coordinates of the
	// formatting context they belong to.
	//
	// The root box gets a context of its own even though it does not seal its
	// margins: a float has to be contained by the page whatever else is true,
	// and the root's relationship to the canvas — which is what its margin
	// behaviour is about — is a separate question this does not touch.
	//
	// The containing block a child resolves a percentage offset against is this
	// box's content box, and whether its height is *definite* is a separate
	// question from what the height turns out to be: a box sized by its content
	// has no height to take a percentage of while its children are still being
	// laid out, and CSS 2.1 makes such a percentage compute to auto rather than
	// to the number the content later happens to produce.
	inner := flow{
		ctx:        at.ctx,
		x:          at.x.Add(margin.Left).Add(border.Left).Add(padding.Left),
		y:          at.y.Add(border.Top).Add(padding.Top),
		cbHeight:   declaredHeight,
		cbDefinite: hasHeight,
	}
	own := at.ctx
	if sealed || b.Parent == nil {
		own = &floatContext{}
		inner = flow{ctx: own, cbHeight: declaredHeight, cbDefinite: hasHeight}
	}

	contentHeight, hoistTop, hoistBottom, placedAnything :=
		l.children(b, frag, width, topOpen, bottomOpen, inner)

	if hasHeight {
		if b.Inner == InnerTable || b.Inner == InnerTableCell {
			// §17.5.3: a declared height on a table or a cell is a *minimum*.
			// Content is never cut off to honour it, which is the opposite of
			// what a declared height does to an ordinary block — and is why a
			// table with "height: 1px" is as tall as its rows rather than a
			// hairline with its rows spilling out.
			contentHeight = style.Max(contentHeight, declaredHeight)
		} else {
			contentHeight = declaredHeight
		}
	} else if own != at.ctx {
		// CSS 2.1 §10.6.7: the auto height of a box that establishes a block
		// formatting context reaches the bottom of the floats inside it. This is
		// the entire reason "overflow: hidden" is the idiom for containing a
		// float, and an ordinary block deliberately does not do it — a float in
		// a plain <div> hangs out of the bottom, which looks like a bug and is
		// the specified behaviour.
		contentHeight = style.Max(contentHeight, own.bottom())
	}
	minHeight, hasMinHeight := l.lengthOf(b, "min-height", containing)
	if b.Replaced == nil {
		// A replaced box's height has already been through §10.4's constraint
		// table, which resolves the two axes together to keep the picture's
		// shape. Clamping it again here would undo that on one axis and leave
		// the other, which is how an image comes out squashed rather than
		// merely small.
		contentHeight = l.clampHeight(b, contentHeight, containing)
	}

	frag.BorderRect.H = contentHeight.Add(padding.Vertical()).Add(border.Vertical())

	out := collapsed{top: margin.Top, bottom: margin.Bottom}
	if topOpen {
		out.top = collapse(margin.Top, hoistTop)
	}
	if bottomOpen {
		out.bottom = collapse(margin.Bottom, hoistBottom)
	}

	// A box collapses through when there is nothing at all between its two
	// margins: no border, no padding, no height it was given, no height its
	// content needed, and no minimum keeping it open.
	if topOpen && bottomOpen && !placedAnything && frag.BorderRect.H == 0 &&
		!(hasMinHeight && minHeight > 0) {
		both := collapse(out.top, out.bottom)
		return frag, collapsed{top: both, bottom: both, through: true}
	}
	return frag, out
}

// children lays out a box's children and reports what came of them: the content
// height, the margins hoisted out through each open edge, and whether anything
// with a size was actually placed.
//
// The walk keeps a *pending* margin rather than adding gaps as it goes. Every
// margin it meets collapses into that one value, and the value is only committed
// to the flow when something with a size arrives — which is what lets an empty
// box between two paragraphs disappear entirely instead of separating them.
func (l *layouter) children(b *Box, parent *Fragment, width style.Unit,
	topOpen, bottomOpen bool, origin flow) (height, hoistTop, hoistBottom style.Unit, placed bool) {

	if b.Inner == InnerTable {
		// A table's children are not a flow at all: they are the grid, and §17.5
		// places them from the columns and rows rather than by stacking them.
		// The two hoisted margins are zero because a table seals both its edges,
		// and something was placed because a table occupies its own height
		// whether or not any cell has content in it.
		return l.tableContent(b, parent, width, origin), 0, 0, true
	}
	if len(b.Children) == 0 {
		return 0, 0, 0, false
	}
	// A block container's in-flow children are either all block-level or all
	// inline-level — the anonymous box rule guarantees it — so this is a two-way
	// choice rather than a mixture. Floats are the reason this is a scan rather
	// than a look at the first child: a float is block-level and out of flow, so
	// "<div><span class=float></span>text</div>" has a block-level child *and*
	// an inline formatting context, and deciding from the first child would lose
	// the text entirely.
	if hasInlineChild(b) {
		// Inline content: lines of text, which have a height of their own.
		return l.inlineContent(b, parent, width, origin), 0, 0, true
	}

	var y, pending style.Unit
	hoisted := false
	// listIndex counts the list items among the children, which is what a
	// numbered marker is numbered by. It counts only list items, so a heading
	// between two items does not advance the numbering.
	listIndex := 0

	for _, child := range b.Children {
		if child.Outer != OuterBlock {
			continue
		}
		if child.ListItem {
			listIndex++
		}

		if child.outOfFlow() {
			// A float's margin box goes at the flow position, and its margins
			// collapse with nothing — so the pending margin is committed for it
			// without being consumed, and the box after it still collapses with
			// the box before it as though the float were not there. That is what
			// makes a float between two paragraphs leave their spacing alone.
			//
			// The exception is a parent whose top edge is still open and has
			// hoisted nothing: the pending margin is on its way out of the
			// parent altogether, so the float sits at the parent's content top.
			offset := pending
			if topOpen && !hoisted {
				offset = 0
			}
			if child.Position.outOfFlow() {
				// The same position, kept rather than used: this is the *static*
				// position of §10.6.4, where the box's margin box would have
				// started had it been in the flow, and it is the answer §10.3.7
				// falls back on when neither offset on an axis was given. It can
				// only be recorded here, because nothing after the walk knows
				// where in the flow the box was written.
				l.deferAbsolute(child, parent, 0, y.Add(offset), listIndex)
				continue
			}
			parent.Children = append(parent.Children,
				l.floatChild(child, width, origin, y.Add(offset), style.MaxUnit, 0, listIndex))
			continue
		}

		// Where the child's border box is predicted to land, so that the floats
		// inside it can be placed while it is being laid out.
		//
		// It has to be a prediction: the child's own collapsed top margin is not
		// known until its subtree has been walked, because a descendant's margin
		// can escape through its top edge. The prediction uses the child's own
		// margin and is therefore exact unless a descendant's is larger — and
		// what happens when it is not exact is settled below rather than left to
		// be approximately right.
		est := y
		if !topOpen || hoisted {
			est = y.Add(collapse(pending, l.ownTopMargin(child, width)))
		}
		est = est.Add(l.clearanceAt(child, origin, est))

		mark, consulted := origin.ctx.mark(), origin.ctx.consulted
		absMark := len(l.deferred)
		cf, cm := l.block(child, width, origin.at(est))
		// Whether the *subtree* read the float geometry, captured before the
		// clearance query below adds a read of its own.
		//
		// Over-attributing that read would not move anything — the second
		// layout happens at the corrected position and produces what the
		// translation would have — so this is about cost rather than about
		// geometry, and a planted defect that sets it to true is invisible in
		// the output by design. What it would cost is a redundant layout of
		// every cleared subtree in the document, and a spurious "stopped short"
		// once those exhausted the budget.
		subtreeRead := origin.ctx.consulted != consulted

		pending = collapse(pending, cm.top)

		// The position §9.5.2 measures clearance against: where the box would
		// have gone had it not cleared anything.
		hypothetical := y.Add(pending)
		if topOpen && !hoisted && !cm.through {
			hypothetical = y
		}
		clearance := l.clearanceAt(child, origin, hypothetical)

		if cm.through && clearance == 0 {
			// Nothing separates this box's own margins, so it contributes no
			// height and its two margins join the run. It still gets a
			// position, because it still exists.
			pending = collapse(pending, cm.bottom)
			at := y.Add(pending)
			cf = l.settle(child, width, origin, cf, est, at, mark, absMark, subtreeRead)
			cf.BorderRect.Y = at
			if child.ListItem {
				cf.Marker = l.markerFor(child, cf, listIndex)
			}
			parent.Children = append(parent.Children, cf)
			continue
		}

		if topOpen && !hoisted {
			// The parent's top edge is open, so everything collected so far
			// belongs outside the parent rather than inside it.
			hoistTop = pending
			pending = 0
			hoisted = true
		}

		at := y.Add(pending).Add(clearance)
		cf = l.settle(child, width, origin, cf, est, at, mark, absMark, subtreeRead)
		if child.ListItem {
			cf.Marker = l.markerFor(child, cf, listIndex)
		}
		parent.Children = append(parent.Children, cf)

		y = at
		cf.BorderRect.Y = y
		y = y.Add(cf.BorderRect.H)
		pending = cm.bottom
		placed = true
	}

	if bottomOpen {
		// The bottom edge is open too, so the trailing margin belongs outside.
		hoistBottom = pending
		pending = 0
	}
	if topOpen && !hoisted {
		// Every child collapsed through, so nothing was ever committed and the
		// whole run belongs outside.
		hoistTop = collapse(hoistTop, pending)
		if bottomOpen {
			hoistBottom = collapse(hoistBottom, pending)
		}
		pending = 0
	}
	return y.Add(pending), hoistTop, hoistBottom, placed
}

// hasInlineChild reports whether a box has any in-flow inline-level child, which
// is what makes it an inline formatting context.
func hasInlineChild(b *Box) bool {
	for _, c := range b.Children {
		if c.Outer == OuterInline {
			return true
		}
	}
	return false
}

// at moves the flow position without changing the containing block.
func (f flow) at(y style.Unit) flow {
	return flow{
		ctx: f.ctx, x: f.x, y: f.y.Add(y),
		cbHeight: f.cbHeight, cbDefinite: f.cbDefinite,
	}
}

// ownTopMargin is a box's own declared top margin, before anything collapses
// into it.
func (l *layouter) ownTopMargin(b *Box, containing style.Unit) style.Unit {
	v, _ := l.lengthOf(b, "margin-top", containing)
	return v
}

// clearanceAt returns how far down a box has to move to clear the floats it
// asked to clear, given where it would otherwise have sat.
//
// CSS 2.1 §9.5.2 computes clearance as a difference rather than as a position,
// and the distinction is the whole rule: a box that was already below the floats
// gets no clearance at all, so "clear: left" on a paragraph well down the page
// changes nothing. An implementation that simply moved the box to the float
// bottom would push it *up* in that case.
func (l *layouter) clearanceAt(b *Box, origin flow, at style.Unit) style.Unit {
	if b.Clear == ClearNone {
		return 0
	}
	want := origin.ctx.clearance(b.Clear)
	have := origin.y.Add(at)
	if want > have {
		return want.Sub(have)
	}
	return 0
}

// floatChild lays a float out and places it in the formatting context.
//
// room is how much of the current line is still free and drop is how far down
// the next line begins. The two only mean anything for a float met part-way
// along a line; a float met between blocks passes MaxUnit and zero, because
// there is no line for it to be squeezed off the end of.
//
// The float is laid out *before* it is placed, and that order is not an
// accident: a float establishes its own formatting context, so nothing outside
// it can reach inside, and its size therefore does not depend on where it ends
// up. Placing first would need a size that is not known yet.
func (l *layouter) floatChild(b *Box, width style.Unit, origin flow,
	top, room, drop style.Unit, index int) *Fragment {

	cf, _ := l.block(b, width, origin.at(top))
	if b.ListItem {
		cf.Marker = l.markerFor(b, cf, index)
	}

	box := cf.MarginRect()
	size := Size{W: box.W, H: box.H}

	// §9.5.1 rules 5 and 6 in one line: no higher than the flow has reached, and
	// no higher than the floats this box was told to clear.
	at := origin.y.Add(top)
	if size.W > room {
		// Rule 9, in the form browsers apply it: a float met after a line has
		// begun goes beside the rest of that line when it fits, and below the
		// line when it does not. This engine does not re-break the text already
		// placed on the line, so what it gives up is the case where a wide float
		// should have pushed earlier words of the same line down with it.
		at = at.Add(drop)
	}
	if c := origin.ctx.clearance(b.Clear); c > at {
		at = c
	}

	rect := origin.ctx.place(size, b.Float, at, origin.x, origin.x.Add(width))

	// Back out of the formatting context's coordinates into the parent's content
	// box, which is what every fragment position in this engine is relative to.
	cf.BorderRect.X = rect.X.Sub(origin.x).Add(cf.Margin.Left)
	cf.BorderRect.Y = rect.Y.Sub(origin.y).Add(cf.Margin.Top)
	return cf
}

// maxRelayouts bounds how many subtrees one render may lay out twice.
//
// It is a variable rather than a constant so that a test can lower it far enough
// to watch the bound fire. A cap that has only ever been observed not to trip is
// one nobody knows works, which this repository has learned before: the first
// test for the box cap built five thousand boxes, nowhere near the limit, and
// passed just as happily with the cap removed.
var maxRelayouts = 4096

// settle corrects a child that was laid out at a predicted position.
//
// The prediction is described where it is made. When it was right, which is the
// overwhelmingly common case, this does nothing at all. When it was wrong there
// are two repairs, and which one applies is decided by whether the subtree ever
// *read* the float geometry:
//
//   - If it only added floats, every one of them is wrong by the same constant,
//     and moving them is exactly equivalent to having laid the subtree out in
//     the right place. Nothing else in the subtree depends on the offset.
//   - If it read the geometry — a line shortened, a float placed beside another,
//     a clearance computed — then the answer it got was against the wrong band,
//     and no translation repairs that. The subtree is laid out again from the
//     corrected position, which is now exact because the collapsed top margin
//     that the prediction was missing is known.
//
// The second repair is bounded, because a corrected parent re-lays its children
// and each of those may correct again. Past the bound the cheap repair is used
// and the render says it stopped short, which is a page that is slightly wrong
// and says so rather than one that took unbounded time to be right.
func (l *layouter) settle(child *Box, width style.Unit, origin flow, cf *Fragment,
	predicted, actual style.Unit, mark, absMark int, read bool) *Fragment {

	delta := actual.Sub(predicted)
	if delta == 0 || origin.ctx.mark() == mark && !read {
		return cf
	}
	if !read || l.relayouts >= maxRelayouts {
		if l.relayouts >= maxRelayouts {
			l.rec.Report(RuleLimit, AtHTML(offsetOf(child)),
				"too many boxes had to be laid out twice to settle where the floats "+
					"around them are; the rest were placed against the position they "+
					"were predicted to have")
		}
		origin.ctx.shift(mark, delta)
		return cf
	}

	l.relayouts++
	origin.ctx.boxes = origin.ctx.boxes[:mark]
	// The out-of-flow boxes the discarded layout found are discarded with it.
	// Without this they would be placed twice — once against a fragment that is
	// about to be thrown away — and the page would carry a ghost of every
	// absolutely positioned box inside a subtree that had to be laid out again.
	l.deferred = l.deferred[:absMark]
	again, _ := l.block(child, width, origin.at(actual))
	return again
}

func offsetOf(b *Box) int {
	if b == nil || b.Element == nil {
		return -1
	}
	return b.Element.Offset
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
// replaced, when not nil, is the width and height CSS 2.1 §10.3.2 gave a
// replaced box. It stands in for a declared width, and it is *not* clamped
// again: §10.4's constraint table has already applied the minimum and maximum
// on both axes together, and a second independent clamp would break the ratio
// it was careful to keep.
func (l *layouter) resolveWidth(b *Box, margin, border, padding Edges,
	containing style.Unit, out *Edges, replaced *Size) style.Unit {

	fixed := border.Horizontal().Add(padding.Horizontal())
	available := containing.Sub(fixed)

	clamp := func(v style.Unit) style.Unit {
		if replaced != nil {
			return v
		}
		return l.clampWidth(b, v, containing)
	}

	declared, hasWidth := l.explicitWidth(b, containing)
	if replaced != nil {
		declared, hasWidth = replaced.W, true
	}
	marginLeftAuto := l.isAuto(b, "margin-left")
	marginRightAuto := l.isAuto(b, "margin-right")

	switch {
	case b.Inner == InnerTable:
		// §17.5.2 decides a table's width by its own algorithm, and then §10.3.3
		// resolves the margins against it exactly as though it had been
		// declared. Routing it through the declared-width branch below is what
		// gives a table "margin: 0 auto" — the margins are on the wrapper, so
		// this arrives here only for a table whose author put a width on it.
		declared, hasWidth = l.tableUsedWidth(b, available), true
	case b.TableWrapper && !hasWidth:
		// §17.4: the wrapper is as wide as the table's border box and no wider,
		// which is what makes "margin: 0 auto" centre a table rather than
		// centring nothing inside a full-width box.
		declared, hasWidth = l.shrinkToFit(b, maxZero(available.Sub(margin.Horizontal()))), true
	}

	if b.Float != FloatNone {
		// CSS 2.1 §10.3.5, the rules for a floated box, and both halves matter.
		// An auto margin is zero rather than a share of the slack — a float is
		// not centred by "margin: auto", which surprises people — and an auto
		// width shrinks to fit rather than filling the line, which is the whole
		// visible difference between a float and a block. A float that filled
		// its containing block would leave no room beside it, so nothing would
		// ever flow around one, and the feature would look implemented and do
		// nothing.
		if marginLeftAuto {
			out.Left = 0
		}
		if marginRightAuto {
			out.Right = 0
		}
		if hasWidth {
			return clamp(declared)
		}
		return clamp(l.shrinkToFit(b, maxZero(available.Sub(out.Horizontal()))))
	}

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
		return clamp(maxZero(width))
	}

	width := clamp(declared)
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
		// Over-constrained. The specification resolves it by ignoring one of the
		// two margins, and which one is decided by the *containing block's*
		// direction rather than the box's own: the margin at the end of the
		// inline direction is the one that gives way, so the box stays where its
		// starting margin put it.
		//
		// The containing block's and not this box's, because a box that declares
		// "direction: rtl" is stating which way its own contents run, not which
		// side of its parent it hangs from.
		if b.Parent != nil && isRTL(b.Parent) {
			out.Left = margin.Left.Add(slack)
			break
		}
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
//
// v is a *content* size and so is the answer, but the limits are declared values
// and mean whatever box-sizing says they mean. So under "border-box" the clamp
// happens in the declared space — the inset is added, the limits applied, the
// inset taken off again — rather than comparing a content width against a
// border-box minimum. boxsizing.go works through what the naive order produces.
func (l *layouter) clampWidth(b *Box, v, containing style.Unit) style.Unit {
	inset, _ := l.sizingInset(b, containing)
	lo := style.Unit(0)
	if min, ok := l.lengthOf(b, "min-width", containing); ok {
		lo = min
	}
	hi := style.MaxUnit
	if max, ok := l.lengthOf(b, "max-width", containing); ok {
		hi = max
	}
	return maxZero(style.Clamp(v.Add(inset), lo, hi).Sub(inset))
}

func (l *layouter) clampHeight(b *Box, v, containing style.Unit) style.Unit {
	_, inset := l.sizingInset(b, containing)
	lo := style.Unit(0)
	if min, ok := l.lengthOf(b, "min-height", containing); ok {
		lo = min
	}
	hi := style.MaxUnit
	if max, ok := l.lengthOf(b, "max-height", containing); ok {
		hi = max
	}
	return maxZero(style.Clamp(v.Add(inset), lo, hi).Sub(inset))
}

// explicitWidth resolves a declared width to a *content* width.
//
// Under "box-sizing: border-box" the declaration named the border box, so the
// padding and the border come out of it. A declared width smaller than its own
// padding gives a content width of zero rather than a negative one — the box
// still exists and is still as wide as its padding, which is what every browser
// produces and what the specification's "the content box cannot be negative"
// amounts to.
func (l *layouter) explicitWidth(b *Box, containing style.Unit) (style.Unit, bool) {
	v, ok := l.lengthOf(b, "width", containing)
	if !ok {
		return 0, false
	}
	inset, _ := l.sizingInset(b, containing)
	return maxZero(v.Sub(inset)), true
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
	_, inset := l.sizingInset(b, containing)
	return maxZero(length.Value.Sub(inset)), true
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
	// The face is resolved only for a value that needs it. Asking for one has
	// side effects — fontFor reports a fallback when the requested family is
	// missing — so resolving it for every length would make a box that merely
	// declares an unavailable font report it without ever setting any text. It
	// is also a measurement per property, on the hot path of layout.
	var zero style.Unit
	var haveMetrics bool
	if usesCh(raw) {
		zero, haveMetrics = l.zeroAdvance(b)
	}
	key := lengthKey{value: raw, fontSize: b.FontSize, zeroAdvance: zero}
	if got, ok := l.lengths[key]; ok {
		return got, true
	}

	vals, _ := css.ParseComponentValues(raw)
	length, _, ok := style.ParseLength(vals, style.LengthContext{
		FontSize:         b.FontSize,
		RootFontSize:     l.rootFontSize,
		ViewportWidth:    l.avail.W,
		ViewportHeight:   l.avail.H,
		ViewportKnown:    true,
		ZeroAdvance:      zero,
		FontMetricsKnown: haveMetrics,
	})
	if !ok {
		return style.Length{}, false
	}
	l.lengths[key] = length
	return length, true
}

// usesCh reports whether a value might carry a "ch" length.
//
// It is a cheap over-approximation: a false positive costs one face lookup that
// the parse then does not use, and a false negative would silently resolve "ch"
// against no font at all. The unit is always preceded by a digit, which is what
// keeps "inherit" and a family name containing the letters from matching.
func usesCh(raw string) bool {
	for i := 0; i+1 < len(raw); i++ {
		if (raw[i] == 'c' || raw[i] == 'C') && (raw[i+1] == 'h' || raw[i+1] == 'H') &&
			i > 0 && raw[i-1] >= '0' && raw[i-1] <= '9' {
			return true
		}
	}
	return false
}

// zeroAdvance is the width of "0" in a box's own font, which is what "ch" means.
//
// It reports false when no face could be found, so that a "ch" length is
// unresolvable — and therefore reported — rather than silently zero, which would
// collapse the box the author was trying to size.
func (l *layouter) zeroAdvance(b *Box) (style.Unit, bool) {
	face, ok := l.fontFor(b)
	if !ok {
		return 0, false
	}
	return l.measure(face, "0", b.FontSize), true
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
	if e, ok := l.collapsedBorderWidths(b); ok {
		// A table or a cell in §17.6.2's model does not have the border it
		// declared: it has half of whichever border won each of the grid lines
		// it touches, which may have come from any of six boxes.
		return e
	}
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

// collapsedBorderWidths is the used border of a table or of one of its cells
// under §17.6.2, or false for every other box.
//
// It is asked here rather than by the table code alone because a cell goes
// through ordinary block layout — blockIn resolves its own border from its own
// style like any other box — and a second answer given only to the table
// algorithm would leave the cell's fragment carrying a border box that disagreed
// with where the table put it.
func (l *layouter) collapsedBorderWidths(b *Box) (Edges, bool) {
	switch b.Inner {
	case InnerTable:
		if !borderCollapses(b) {
			return Edges{}, false
		}
		return l.collapsedGridFor(b).table, true
	case InnerTableCell:
		table := tableAncestorOf(b)
		if table == nil || !borderCollapses(table) {
			return Edges{}, false
		}
		e, ok := l.collapsedGridFor(table).cells[b]
		return e, ok
	}
	return Edges{}, false
}

// tableAncestorOf finds the table a cell belongs to.
//
// §17.2.1 guarantees the chain — a cell is in a row, a row is in a row group or
// in a table — so this is two or three steps. The bound is there because the box
// tree is built from an untrusted document and a walk with no end is the one
// mistake a cycle in it could turn into a hang.
func tableAncestorOf(cell *Box) *Box {
	b := cell.Parent
	for i := 0; b != nil && i < 4; i++ {
		if b.Inner == InnerTable {
			return b
		}
		b = b.Parent
	}
	return nil
}

// paddingOf is a box's padding, which a table using §17.6.2 does not have.
//
// "in this model, a table does not have padding (but does have borders and
// margins)". It is not a rounding: the grid lines start at the table's border,
// so a padding would put a gap between the table's own border and the first
// line's other half — two borders that are meant to be one.
func (l *layouter) paddingOf(b *Box, containing style.Unit) Edges {
	if b.Inner == InnerTable && borderCollapses(b) {
		return Edges{}
	}
	return l.edges(b, "padding", containing)
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
