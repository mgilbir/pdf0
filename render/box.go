package render

import (
	"strings"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
	"github.com/mgilbir/pdf0/style"
)

// The box tree: the fourth of §3's stages, turning a styled document into the
// boxes layout will position.
//
// It is a separate stage because the document tree and the box tree are not the
// same shape, and the places they differ are where layout goes wrong if the
// distinction is skipped. An element can produce no box, or several. A run of
// text between two block children needs a box of its own that no element
// generated. And whether a box is block-level or inline-level is a property of
// the *box*, decided by the cascade, not of the tag that produced it.
//
// # Why this is in package render rather than its own
//
// §3 sketches each stage as a package. What it actually requires is that each
// stage have "a data structure at its boundary" so §7's oracle can attach, and
// Box is that. Splitting the stages into packages would mean moving the finding
// vocabulary below them all — the box stage reports, so it cannot sit above the
// package that defines a finding — and that is a rearrangement to make on
// evidence rather than in advance.

// Outer is how a box participates in its parent's formatting context: as a
// block, as something in a line, or not at all.
type Outer uint8

const (
	// OuterNone is "display: none". The element and everything inside it
	// produce no boxes, which is different from producing an invisible one:
	// nothing inside is laid out, measured or painted.
	OuterNone Outer = iota
	// OuterBlock takes a line of its own.
	OuterBlock
	// OuterInline sits in a line with its siblings.
	OuterInline
)

func (o Outer) String() string {
	switch o {
	case OuterBlock:
		return "block"
	case OuterInline:
		return "inline"
	}
	return "none"
}

// Inner is what formatting context a box establishes for its children.
type Inner uint8

const (
	// InnerFlow is ordinary block-and-inline layout.
	InnerFlow Inner = iota
	// InnerFlowRoot is the same, but as an independent formatting context —
	// what an inline-block establishes, and what stops margins collapsing
	// through it.
	InnerFlowRoot
	// InnerFlex, InnerTable and the table-internal contexts are named here and
	// laid out later; naming them now is what lets the box tree be built once.
	InnerFlex
	InnerTable
	InnerTableRowGroup
	InnerTableRow
	InnerTableCell
	InnerTableCaption
	InnerTableColumnGroup
	InnerTableColumn
	// InnerText is a run of text, which has no children and no context.
	InnerText
)

func (i Inner) String() string {
	switch i {
	case InnerFlowRoot:
		return "flow-root"
	case InnerFlex:
		return "flex"
	case InnerTable:
		return "table"
	case InnerTableRowGroup:
		return "table-row-group"
	case InnerTableRow:
		return "table-row"
	case InnerTableCell:
		return "table-cell"
	case InnerTableCaption:
		return "table-caption"
	case InnerTableColumnGroup:
		return "table-column-group"
	case InnerTableColumn:
		return "table-column"
	case InnerText:
		return "text"
	}
	return "flow"
}

// Box is a node of the box tree.
type Box struct {
	Outer Outer
	Inner Inner

	// Element is the element that generated this box, or nil when nothing did.
	// An anonymous box has no element and no style of its own — it inherits
	// everything, which is what the specification means by anonymous.
	Element *html.Node

	// Style is the computed style of the generating element. An anonymous box
	// carries the style it inherits from, so that a consumer never has to walk
	// up looking for one.
	Style style.ComputedStyle

	// Text is the content of a text box, after white-space processing. It is
	// empty for every other kind.
	Text string

	// FontSize is the computed font-size, resolved here rather than in the
	// cascade because it is the one property whose value depends on its own
	// parent's computed value: "font-size: 2em" means twice the parent's size,
	// so it can only be resolved walking downwards.
	//
	// Only an element that *declared* one resolves; an element that inherited
	// its font-size takes its parent's number unchanged. See fontSizeOf for why
	// that distinction is the whole of the property's correctness.
	//
	// Every font-relative length in the box — its margins, its padding, its
	// line height — is measured against this, so it has to exist before layout
	// asks anything about geometry.
	FontSize style.Unit

	// ListItem marks a box that generates a marker — a bullet or a number.
	ListItem bool

	// ListValue is what a numbered marker counts to, taken from the "list-item"
	// counter rather than from the item's position among its siblings.
	//
	// ListNumbered says whether it means anything, because zero is a value a
	// list can legitimately be at — <ol start="0"> — and not a way of saying
	// there is no counter. Reading the zero as "unset" numbered that list from
	// one, which is the same shape of fault as a sentinel colliding with a real
	// value elsewhere in this engine.
	//
	// The two differ whenever a document says so, and documents do: <ol start>,
	// <li value>, a "counter-reset: list-item" on the list, an item that is not
	// the first child, or a list whose items are not siblings at all. Counting
	// siblings gets every one of those wrong, and gets them wrong quietly — the
	// list is numbered, just not with the numbers the author asked for.
	ListValue    int
	ListNumbered bool

	// Replaced is the content of a replaced element — the decoded image an
	// <img> names — or nil for every other box.
	//
	// It is nil rather than empty when the content could not be loaded, and
	// that is the whole of what CSS means by an element being replaced: an
	// image that did not arrive makes the element an ordinary inline box
	// holding its alt text, not a replaced one holding nothing. Layout
	// therefore asks whether this is nil rather than asking what the element's
	// tag is, and an <img> with a broken src goes down exactly the same path as
	// a <span>.
	Replaced *ReplacedContent

	// BackgroundImages is the pictures this box's background-image named, by the
	// reference the stylesheet wrote.
	//
	// It is a map rather than a slice because the layers are read later, by
	// layout, and matching a slice to them by index would depend on the loading
	// pass and the reading pass agreeing about a value they parse separately —
	// which is the sort of coupling that survives every test and breaks on the
	// document that repeats one file in two layers.
	//
	// A reference that failed to load is absent rather than present and nil, so
	// a layer naming it paints nothing. That is the same answer a broken <img>
	// gets and for the same reason: the picture is missing, not the box.
	BackgroundImages map[string]*ReplacedContent

	// TableWrapper marks the anonymous box §17.4 puts around a table to hold it
	// and its captions.
	//
	// It is a flag rather than an Inner of its own because the wrapper really is
	// an ordinary flow root — block layout, margin collapsing, floats and
	// positioning all treat it as one, and that is the point of it. The one thing
	// that is not ordinary is its width, which is the table's rather than its
	// containing block's, and that is the single question this answers.
	TableWrapper bool

	// Float and Clear are CSS 2.1 §9.5. They live on the box rather than being
	// read out of Style at layout time for the same reason Outer and Inner do:
	// whether a box is in the normal flow changes what the box tree itself is
	// allowed to do to it — an anonymous block box is generated around a run of
	// *in-flow* inline content, and a float in that run is not part of the run.
	Float FloatSide
	Clear ClearSide

	// Position is CSS 2.1 §9.3's scheme, and it lives here for the same reason
	// Float does and one more. An absolutely positioned box is out of flow, so
	// the anonymous-box rules have to know about it before layout runs; and
	// §9.7 blockifies it exactly as it blockifies a float, which is a
	// computed-value rule and so belongs to the stage that computes the box.
	Position PositionScheme

	// ZIndex and ZAuto are §9.9's stacking level. They are read here rather than
	// out of Style at paint time because painting order is decided by a
	// traversal that has to sort boxes against each other, and a sort key that
	// has to be re-parsed at every comparison is a sort that gets written once
	// and then quietly avoided.
	ZIndex int
	ZAuto  bool

	// Order is the box's index in document order, which is the tie-break
	// Appendix E uses between two positioned boxes at the same stacking level.
	//
	// It is recorded rather than derived because painting does not walk the tree
	// in document order any more: an absolutely positioned box is placed after
	// the flow has been laid out and hangs from whichever fragment could hold
	// it, so its position among its siblings has been lost by the time anything
	// needs to sort it. Two overlapping cards written one after the other would
	// otherwise stack in whichever order the placement pass happened to reach
	// them, which is stable, invisible and wrong.
	Order int

	// noLeadInset and noTrailInset mark a piece of an inline box that was split
	// by a block inside it as one whose own margin, border and padding does not
	// begin or does not end here.
	//
	// §8.6's slice model: an inline box broken across a block — or across a line
	// — carries its left inset on its first piece and its right on its last, and
	// nothing on the joins. They are set on the pieces splitInline makes rather
	// than derived at layout time because only the split knows which piece is
	// which; by the time inline layout sees them they are ordinary siblings.
	//
	// Both false is a box that was never split, which is every box in a document
	// without a block inside an inline.
	noLeadInset, noTrailInset bool

	Children []*Box
	Parent   *Box
}

// outOfFlow reports whether a box takes no space among its siblings, which is
// true of a float and of an absolutely positioned box.
//
// The two are different mechanisms and the box tree treats them identically,
// because everything the box tree does with either is a consequence of the one
// property they share: a box that is not in the flow does not end a run of
// inline content, does not force an anonymous block around its neighbours, and
// does not split the inline box it was written inside.
func (b *Box) outOfFlow() bool {
	return b.Float != FloatNone || b.Position.outOfFlow()
}

// Anonymous reports whether a box was generated by the engine rather than by an
// element.
func (b *Box) Anonymous() bool { return b.Element == nil && b.Inner != InnerText }

// IsText reports whether a box is a run of text.
func (b *Box) IsText() bool { return b.Inner == InnerText }

// maxBoxes bounds the tree. A document is untrusted, and anonymous box
// generation can produce more boxes than there were elements, so the html
// package's node cap does not bound this on its own.
//
// It is a variable rather than a constant only so that a test can lower it.
// Building a million boxes to watch a cap fire would take longer than the rest
// of the suite put together, and a bound that is never seen to fire is one
// nobody knows works — which was found the hard way: the first test for this
// built five thousand boxes, nowhere near the cap, and passed just as happily
// with the cap removed.
var maxBoxes = 1 << 20

// BuildBoxes turns a styled document into a box tree.
//
// The root box is the one the document element generated. It is nil when the
// document produces no boxes at all, which is what "html { display: none }"
// means and is not an error.
func BuildBoxes(doc *html.Node, styled style.Styled, rec *Recorder) *Box {
	b := &boxBuilder{
		styles: styled.Styles, pseudo: styled.Pseudo, rec: rec,
		ownFontSize: styled.OwnFontSize, ownPseudoFontSize: styled.OwnPseudoFontSize,
		// Counters are settled before any box exists, because a counter's value
		// depends on what came *before* an element in the document and the box
		// walk cannot answer that while descending.
		counters: computeCounters(doc, styled.Styles, styled.Pseudo),
	}
	root := documentElementOf(doc)
	if root == nil {
		return nil
	}
	// The root's font-size is resolved against the initial value, since there
	// is no parent to take one from, and it then becomes what every "rem" in
	// the document means.
	b.rootFontSize = defaultFontSize
	b.rootFontSize = b.fontSizeOf(root, defaultFontSize)

	box := b.build(root, nil, b.rootFontSize)
	if box == nil {
		return nil
	}
	// Anonymous boxes are added after the whole tree exists, because the rule
	// that generates them is about a box's *set* of children — a block child
	// anywhere makes every inline run a candidate — and that set is not known
	// while the children are still being made.
	b.fixup(box)
	// §17.4's wrapper is a second pass because it has to see tables that the
	// first pass generated as well as the ones the author wrote, and it may
	// replace the root itself: "html { display: table }" is legal.
	return b.wrapTables(box)
}

func documentElementOf(doc *html.Node) *html.Node {
	if doc == nil {
		return nil
	}
	if doc.Type == html.ElementNode {
		return doc
	}
	for _, c := range doc.Children {
		if c.Type == html.ElementNode {
			return c
		}
	}
	return nil
}

// defaultFontSize is the initial value of font-size, "medium", in layout units.
// It is what the root resolves a relative size against and what a document that
// sets no size gets.
var defaultFontSize = mustPx(16)

func mustPx(px float64) style.Unit {
	u, _ := style.FromPx(px)
	return u
}

type boxBuilder struct {
	styles map[*html.Node]style.ComputedStyle
	pseudo map[style.PseudoKey]style.ComputedStyle
	// ownFontSize and ownPseudoFontSize say which elements declared a font-size
	// of their own. See fontSizeOf.
	ownFontSize       map[*html.Node]bool
	ownPseudoFontSize map[style.PseudoKey]bool
	rec               *Recorder
	// counters holds what each element's generated content sees. See counter.go
	// for why it is a walk of its own rather than something this builder can
	// answer on the way down.
	counters     map[*html.Node]counterValues
	rootFontSize style.Unit
	count        int
	// afterWord says the last character emitted was part of a word, which is what
	// "text-transform: capitalize" needs to know and what a text node cannot
	// answer on its own: in "<b>e</b>xample" the "x" does not begin a word. It is
	// carried on the builder because the walk visits text in document order, and
	// it is reset around a block-level box because a block starts a new line of
	// text whatever preceded it.
	afterWord bool
	// stopped records that the box cap was reached, so it is reported once
	// rather than per box.
	stopped bool
}

// build makes the box or boxes for one node, or nil for none.
func (b *boxBuilder) build(n *html.Node, inherited style.ComputedStyle, fontSize style.Unit) *Box {
	switch n.Type {
	case html.TextNode:
		return b.textBox(n, inherited, fontSize)
	case html.ElementNode:
		return b.elementBox(n, fontSize)
	}
	return nil
}

// fontSizeOf resolves an element's computed font-size against its parent's.
//
// A value this engine cannot resolve leaves the size at the parent's, which is
// what inheriting would have given — the safe answer, since a size of zero would
// make every em-based margin in the subtree vanish.
//
// # Why an inherited size is not resolved again
//
// Only an element that *declared* a font-size resolves one. CSS makes the
// computed value of font-size an absolute length, so inheritance passes a
// number; what the cascade stores is what the author wrote, so a descendant of
// "font-size: 2em" inherits the string "2em" and re-resolving it against the
// parent would double the size at every level. A paragraph four elements inside
// such a wrapper came out at 256px.
//
// That was a real bug, found by the table work: the two halves of a reftest
// nested their content to different depths, so the compounding moved one and not
// the other and the difference finally showed. It had been invisible until then
// because it moves every part of a document equally.
func (b *boxBuilder) fontSizeOf(n *html.Node, parent style.Unit) style.Unit {
	if !b.ownFontSize[n] {
		return parent
	}
	cs, ok := b.styles[n]
	if !ok {
		return parent
	}
	vals, _ := css.ParseComponentValues(cs["font-size"])
	size, unsupported, ok := style.ResolveFontSize(vals, parent, b.rootFontSize)
	if !ok {
		if unsupported {
			b.rec.ReportDetail(Finding{
				Rule:     RuleUnsupportedValue,
				Source:   AtHTML(n.Offset),
				Message:  "the font-size " + quoteValue(cs["font-size"]) + " could not be resolved; the inherited size was kept",
				Path:     PathOf(n),
				Property: "font-size",
			})
		}
		return parent
	}
	return size
}

// quoteValue renders a value for a diagnostic without letting a hostile
// stylesheet put control characters into a caller's log.
func quoteValue(s string) string {
	const max = 40
	var out strings.Builder
	out.WriteByte('"')
	for i, r := range s {
		if i >= max {
			out.WriteString("...")
			break
		}
		if r < 0x20 || r == 0x7F {
			out.WriteByte('?')
			continue
		}
		out.WriteRune(r)
	}
	out.WriteByte('"')
	return out.String()
}

func (b *boxBuilder) elementBox(n *html.Node, parentFontSize style.Unit) *Box {
	cs := b.styles[n]
	outer, inner, listItem := displayOf(cs)
	if outer == OuterNone {
		// Nothing inside is laid out either, which is the difference between
		// display:none and visibility:hidden.
		return nil
	}
	if !b.room(n) {
		return nil
	}
	order := b.count

	fontSize := b.fontSizeOf(n, parentFontSize)
	float := floatOf(cs)
	position := positionOf(cs)
	if position.outOfFlow() {
		// §9.7's first row: a box that is absolutely positioned does not float.
		// The two mechanisms both remove a box from the flow and they cannot
		// both do it — an implementation that honoured the float as well would
		// shift the box to an edge it was never asked to go to, and then resolve
		// its offsets against a containing block chosen for the float.
		float = FloatNone
	}
	outer, inner = outOfFlowDisplay(outer, inner, float, position)
	if outer == OuterBlock && inner == InnerFlow && overflowIsScrollable(cs) {
		// CSS 2.1 §9.4.1: a block box whose overflow is anything but visible
		// establishes a block formatting context. That is not a painting
		// detail — it is what makes "overflow: hidden" the idiom for containing
		// a float, because §10.6.7 gives a formatting-context root a height
		// that includes the floats inside it. An engine that treated overflow
		// as purely visual would leave the float sticking out of the box the
		// author wrapped around it precisely to stop that.
		inner = InnerFlowRoot
	}
	z, zAuto := zIndexOf(cs)
	box := &Box{
		Outer: outer, Inner: inner, Element: n, Style: cs,
		ListItem: listItem, FontSize: fontSize,
		Float: float, Clear: clearOf(cs),
		Position: position, ZIndex: z, ZAuto: zAuto, Order: order,
	}
	box.ListValue, box.ListNumbered = b.listValueOf(n, listItem)
	if outer != OuterInline {
		// A block-level box begins its text afresh, so a word cannot run into it
		// from the paragraph before. Without this, "<p>hi</p><p>there</p>" under
		// "capitalize" would leave the second paragraph's "t" lower-case, since
		// the "i" before it is a word character.
		b.afterWord = false
	}

	// ::before and ::after bracket the element's own children rather than
	// replacing them, which is why they are added here and not by the caller.
	if before := b.generated(n, "before", fontSize); before != nil {
		before.Parent = box
		box.Children = append(box.Children, before)
	}
	for _, child := range n.Children {
		if c := b.build(child, cs, fontSize); c != nil {
			c.Parent = box
			box.Children = append(box.Children, c)
		}
	}
	if after := b.generated(n, "after", fontSize); after != nil {
		after.Parent = box
		box.Children = append(box.Children, after)
	}
	if outer != OuterInline {
		// And a block-level box ends its text: the word does not continue into
		// whatever comes after it.
		b.afterWord = false
	}
	return box
}

// fontSizeOfStyle resolves a font-size from a computed style that belongs to no
// element of its own, which is what a pseudo-element has.
//
// own says whether a rule set the pseudo-element's font-size, for the reason
// fontSizeOf gives: a pseudo-element inherits from its originating element, and
// re-resolving a size it inherited would compound it.
func (b *boxBuilder) fontSizeOfStyle(cs style.ComputedStyle, parent style.Unit, own bool) style.Unit {
	if !own {
		return parent
	}
	vals, _ := css.ParseComponentValues(cs["font-size"])
	size, _, ok := style.ResolveFontSize(vals, parent, b.rootFontSize)
	if !ok {
		return parent
	}
	return size
}

// room reports whether another box may be made.
func (b *boxBuilder) room(n *html.Node) bool { return b.roomAt(n.Offset) }

// roomAt is room for a box no element generated, which has no node to be
// reported against — every box §17.2.1 inserts is one.
func (b *boxBuilder) roomAt(offset int) bool {
	if b.count >= maxBoxes {
		if !b.stopped {
			b.stopped = true
			b.rec.Report(RuleLimit, AtHTML(offset),
				"the document produces more boxes than this engine will build; "+
					"the rest of it was not laid out")
		}
		return false
	}
	b.count++
	return true
}

// textBox makes a text box, applying Phase I of white-space processing.
//
// Only Phase I: the rules that span a box boundary and the rules that need a
// line are applied later, and whitespace.go says where and why.
//
// Text whose content collapses to nothing produces no box. That is not an
// optimisation: an empty text box between two blocks would make the parent a
// block container with inline content, and so generate an anonymous block that
// occupies a line.
func (b *boxBuilder) textBox(n *html.Node, inherited style.ComputedStyle, fontSize style.Unit) *Box {
	text := collapseWhitespace(n.Text, inherited["white-space"])
	// text-transform, applied here so that the text every later stage measures,
	// breaks, draws and writes into the PDF is the text that will appear.
	// texttransform.go works through why it cannot wait until paint time.
	//
	// It runs after the white-space processing rather than before it, which
	// changes no answer — a case mapping neither creates nor destroys white space
	// — and means "capitalize" sees the word boundaries the reader will.
	text, b.afterWord = transformText(text,
		transformOf(inherited["text-transform"]), b.afterWord)
	if text == "" {
		return nil
	}
	if !b.room(n) {
		return nil
	}
	return &Box{
		Outer: OuterInline, Inner: InnerText,
		Style: inherited, Text: text, FontSize: fontSize,
	}
}

// displayOf reads the display property into the outer/inner pair.
//
// CSS now defines display as two values — how a box behaves in its parent, and
// what context it makes for its children — and the single keywords are
// shorthands for pairs. Modelling it as the pair is what makes "inline-block"
// stop being a special case: it is simply inline outside and flow-root inside.
func displayOf(cs style.ComputedStyle) (Outer, Inner, bool) {
	value := strings.ToLower(strings.TrimSpace(cs["display"]))

	// The two-value syntax, "inline flow-root" and friends.
	if outer, inner, ok := twoValueDisplay(value); ok {
		return outer, inner, false
	}

	switch value {
	case "none":
		return OuterNone, InnerFlow, false
	case "block", "flow-root":
		inner := InnerFlow
		if value == "flow-root" {
			inner = InnerFlowRoot
		}
		return OuterBlock, inner, false
	case "inline":
		return OuterInline, InnerFlow, false
	case "inline-block":
		return OuterInline, InnerFlowRoot, false
	case "list-item":
		return OuterBlock, InnerFlow, true
	case "flex":
		return OuterBlock, InnerFlex, false
	case "inline-flex":
		return OuterInline, InnerFlex, false
	case "table":
		return OuterBlock, InnerTable, false
	case "inline-table":
		return OuterInline, InnerTable, false
	case "table-row-group":
		return OuterBlock, InnerTableRowGroup, false
	case "table-header-group":
		return OuterBlock, InnerTableRowGroup, false
	case "table-footer-group":
		return OuterBlock, InnerTableRowGroup, false
	case "table-row":
		return OuterBlock, InnerTableRow, false
	case "table-cell":
		return OuterBlock, InnerTableCell, false
	case "table-caption":
		return OuterBlock, InnerTableCaption, false
	case "table-column-group":
		return OuterBlock, InnerTableColumnGroup, false
	case "table-column":
		return OuterBlock, InnerTableColumn, false
	case "contents":
		// "display: contents" replaces the element with its children. It is not
		// implemented, and the closest available answer — treating it as inline
		// — is wrong in a way that shows: the element's own box would take part
		// in layout when the author asked for it not to. Treated as inline and
		// reported by the caller.
		return OuterInline, InnerFlow, false
	}
	// An unrecognised value: the initial one, which is what the cascade would
	// have used had the declaration been invalid.
	return OuterInline, InnerFlow, false
}

// floatOf and clearOf read the two out-of-flow properties.
//
// An unrecognised value gives the initial one, which is what the cascade would
// have produced had the declaration been thrown out. "inline-start" and
// "inline-end" are CSS Logical rather than CSS 2.1 and are not read here: they
// need the writing mode, and answering them as "left" would be right for a
// left-to-right document and silently wrong for the documents they exist for.
func floatOf(cs style.ComputedStyle) FloatSide {
	switch strings.ToLower(strings.TrimSpace(cs["float"])) {
	case "left":
		return FloatLeft
	case "right":
		return FloatRight
	}
	return FloatNone
}

func clearOf(cs style.ComputedStyle) ClearSide {
	switch strings.ToLower(strings.TrimSpace(cs["clear"])) {
	case "left":
		return ClearLeft
	case "right":
		return ClearRight
	case "both":
		return ClearBoth
	}
	return ClearNone
}

// outOfFlowDisplay applies CSS 2.1 §9.7: a floated or absolutely positioned
// box's display is blockified.
//
// The table in §9.7 is what makes "float: left" work on a <span> without the
// author also writing "display: block", and it makes "position: absolute" do the
// same — which matters more than it sounds, because the everyday abspos idiom is
// on a <span> or an <a>. It is a computed-value rule rather than a layout one,
// and doing it here rather than in layout is what keeps the rest of the engine
// from having to ask "is this inline box actually inline" at every step — the
// box tree's invariant that an inline box takes part in a line is only true if
// an out-of-flow box has already stopped being inline.
//
// Both also establish a block formatting context of their own, which is the half
// of §9.7 that is easy to forget and expensive to omit: without it the margins
// of a float's first child would collapse out through its top edge, so a floated
// <div> holding a <p> would be positioned an em above where the author put it,
// and the floats inside it would escape into the surrounding context.
func outOfFlowDisplay(outer Outer, inner Inner, float FloatSide, position PositionScheme) (Outer, Inner) {
	if outer == OuterNone || (float == FloatNone && !position.outOfFlow()) {
		return outer, inner
	}
	switch inner {
	case InnerFlow, InnerFlowRoot:
		return OuterBlock, InnerFlowRoot
	case InnerTable, InnerFlex:
		// The two-value forms whose inner half survives blockification: an
		// inline-table floats as a table, an inline-flex as a flex container.
		return OuterBlock, inner
	}
	// The table-internal displays. §9.7 turns each into its block-level
	// equivalent, and the honest one for a lone floated cell or row is a block.
	return OuterBlock, InnerFlowRoot
}

// overflowIsScrollable reports whether either axis of overflow is something
// other than visible.
func overflowIsScrollable(cs style.ComputedStyle) bool {
	for _, axis := range [2]string{"overflow-x", "overflow-y"} {
		switch strings.ToLower(strings.TrimSpace(cs[axis])) {
		case "", "visible":
		default:
			return true
		}
	}
	return false
}

// twoValueDisplay reads the "outer inner" form.
func twoValueDisplay(value string) (Outer, Inner, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return 0, 0, false
	}
	var outer Outer
	switch parts[0] {
	case "block":
		outer = OuterBlock
	case "inline":
		outer = OuterInline
	default:
		return 0, 0, false
	}
	var inner Inner
	switch parts[1] {
	case "flow":
		inner = InnerFlow
	case "flow-root":
		inner = InnerFlowRoot
	case "flex":
		inner = InnerFlex
	case "table":
		inner = InnerTable
	default:
		return 0, 0, false
	}
	return outer, inner, true
}

// fixup adds the anonymous boxes the specification requires, depth first so a
// parent sees children that are already settled.
// The table repair runs first, and before the two flow rules rather than after
// them, because it is the rule that decides *which* boxes are in a flow at all.
// A stray cell inside a <span> becomes an inline-table there, and an
// inline-table is an atomic inline that does not split the span it is in; run
// the split first and the cell is lifted out as a block, taking the table it
// should have grown with it.
func (b *boxBuilder) fixup(box *Box) {
	for _, c := range box.Children {
		b.fixup(c)
	}
	box.Children = b.fixupTables(box)
	box.Children = b.splitBlockInInline(box)
	box.Children = b.wrapInlines(box)
}

// splitBlockInInline breaks an inline box apart around any block-level box
// inside it, per CSS 2.1 §9.2.1.1.
//
// "<span>before<div>block</div>after</span>" does not put a block inside an
// inline. The span is *split*: an inline piece holding "before", then the block,
// then a second inline piece holding "after", all three siblings of whatever the
// span was a child of. Both pieces keep the span's style, so a border on it is
// drawn twice — which is exactly what a browser does and looks odd until you
// know why.
//
// Without this the block would sit inside an inline formatting context, where
// nothing in block layout knows how to place it. It is common markup — an <a>
// wrapping a card of block content is the everyday case — so leaving it
// unhandled is not a corner.
func (b *boxBuilder) splitBlockInInline(parent *Box) []*Box {
	// There is deliberately no early return for an inline parent. One was
	// written here first, on the reasoning that inside an inline formatting
	// context there is nothing to promote a block *to* — and removing it changes
	// no output, because fixup runs depth first: a block promoted to be a direct
	// child of an inline is then lifted again by that inline's own parent, and
	// the sequence comes out the same either way.
	var out []*Box
	for _, child := range parent.Children {
		// Only an inline box that is *not* itself a block container splits. An
		// inline-block is inline outside and a block container inside, so a
		// block within it is where the author put it and lifting it out would
		// empty the very box that was meant to hold it.
		if child.Outer != OuterInline || laysOutOwnChildren(child) || !containsBlock(child) {
			out = append(out, child)
			continue
		}
		for _, piece := range splitInline(child) {
			piece.Parent = parent
			out = append(out, piece)
		}
	}
	return out
}

// isBlockContainer reports whether a box lays its children out as blocks and
// lines rather than taking part in someone else's line.
//
// It is the distinction the two rules above both turn on. A <div> is one. An
// inline-block is one despite being inline on the outside — that is the whole of
// what flow-root means. An ordinary <span> is not, and neither is a flex
// container or a table, which have rules of their own.
func isBlockContainer(b *Box) bool {
	switch b.Inner {
	case InnerFlowRoot, InnerTableCell, InnerTableCaption:
		// A cell and a caption are block containers that happen to live in a
		// table: their children are blocks and lines like any other, which is
		// why the anonymous block rule has to reach inside them.
		return true
	case InnerFlow:
		return b.Outer == OuterBlock
	}
	return false
}

// laysOutOwnChildren reports whether a box is opaque to the two flow rules
// above: whatever is inside it belongs to it, and neither splitting nor
// wrapping may reach past it.
//
// It differs from isBlockContainer at exactly the boxes that are not block
// containers and are not part of anyone else's inline formatting context
// either — an inline-table, an inline-flex. Those hold block-level boxes
// legitimately, and §9.2.1.1's split must not fire on one: an inline-table in
// the middle of a sentence would otherwise break the sentence in two and be
// lifted out as a block, which is the opposite of what "inline" asked for.
func laysOutOwnChildren(b *Box) bool {
	switch b.Inner {
	case InnerTable, InnerFlex:
		return true
	}
	return isBlockContainer(b)
}

// containsBlock reports whether an inline box has a block-level box anywhere
// inside it.
func containsBlock(b *Box) bool {
	for _, c := range b.Children {
		if c.outOfFlow() {
			// An out-of-flow box inside an inline box does not split it.
			// §9.2.1.1 is about a *block-level box in the normal flow* appearing
			// inside an inline formatting context, and neither a float nor an
			// absolutely positioned box is in the flow: they are placed beside
			// the line boxes rather than interrupting them. Splitting the inline
			// around one would draw the inline's border twice and put a line
			// break in the middle of a sentence — which is what the everyday
			// "position: absolute" tooltip inside a sentence would do.
			continue
		}
		if c.Outer == OuterBlock {
			return true
		}
		// The search stops at a nested block container. A block inside an
		// inline-block belongs to that box and is not something to be lifted
		// past it.
		if c.Outer == OuterInline && !laysOutOwnChildren(c) && containsBlock(c) {
			return true
		}
	}
	return false
}

// splitInline returns the sequence an inline box becomes once the blocks inside
// it have been lifted out: inline pieces and blocks, alternating.
//
// An inline piece with nothing in it is dropped. "<span><div>x</div></span>" is
// one block and no inline pieces at all, and keeping two empty spans either side
// would give the line boxes they generate a height the author never asked for.
//
// That last is a simplification and it has a cost worth stating. §9.4.2 makes an
// empty inline box with a non-zero horizontal margin, border or padding into a
// line box that is *not* zero-height, so a span whose only child is a block does
// keep its own padding-right somewhere — on an empty trailing piece. Dropping
// the piece drops the padding with it. The tests that show it are
// visuren/emptyspan-1 and -4 and normal-flow/block-in-inline-empty-001 and -004;
// keeping such a piece means deciding whether it is empty *after* its lengths
// are resolved, which is a layouter's question and not a box builder's.
//
// The insets that do survive go on the right pieces, which is §8.6's slice
// model: the left inset on the first piece and the right on the last.
func splitInline(b *Box) []*Box {
	var out []*Box

	piece := clonePiece(b)
	flush := func() {
		if len(piece.Children) == 0 {
			return
		}
		// Every piece after the first begins in the middle of the box, and
		// every piece is assumed to end in the middle until the split turns out
		// to be over. The last one is corrected below.
		piece.noLeadInset = len(out) > 0
		piece.noTrailInset = true
		out = append(out, piece)
		piece = clonePiece(b)
	}

	for _, child := range b.Children {
		switch {
		case child.outOfFlow():
			// Out of flow, so it does not interrupt the inline it sits in. It
			// stays with the piece so that the inline formatting context which
			// has to flow around it is the one it was written in — and, for an
			// absolutely positioned box, so that the place it was written is
			// still where its static position is taken from.
			child.Parent = piece
			piece.Children = append(piece.Children, child)

		case child.Outer == OuterBlock:
			flush()
			out = append(out, child)

		case child.Outer == OuterInline && !laysOutOwnChildren(child) && containsBlock(child):
			// A nested inline that itself holds a block: split it too, and its
			// inline pieces belong to this one's current piece.
			for _, inner := range splitInline(child) {
				if inner.Outer == OuterBlock {
					flush()
					out = append(out, inner)
					continue
				}
				inner.Parent = piece
				piece.Children = append(piece.Children, inner)
			}

		default:
			child.Parent = piece
			piece.Children = append(piece.Children, child)
		}
	}
	flush()
	// The split is over, so whichever piece came last really does end the box.
	if n := len(out); n > 0 && out[n-1].Outer != OuterBlock {
		out[n-1].noTrailInset = false
	}
	// A box that begins or ends with a block still has a fragment there, and its
	// own left or right inset belongs to that fragment. Keeping it is what makes
	// "<span style='padding-right: 10px'><div>x</div></span>" reserve the ten
	// pixels the specification says it does. It is two boxes at most, whatever
	// the box contains.
	//
	// A box with no inset to carry keeps neither, and that is not an
	// optimisation. §9.4.2 says a line box holding nothing but an inline box
	// with *zero* margins, borders and padding "must be treated as not
	// existing", and a piece that exists is a piece an anonymous block gets
	// wrapped around — which would stand between two blocks and stop their
	// margins collapsing. Three tests in box-display say so, and they are the
	// reason this asks the question rather than always keeping the piece.
	if len(out) > 0 && mayInsetHorizontally(b.Style) {
		if out[0].Outer == OuterBlock {
			lead := clonePiece(b)
			lead.noTrailInset = true
			out = append([]*Box{lead}, out...)
		}
		if out[len(out)-1].Outer == OuterBlock {
			trail := clonePiece(b)
			trail.noLeadInset = true
			out = append(out, trail)
		}
	}
	return out
}

// mayInsetHorizontally reports whether a style could give an inline box a
// non-zero margin, border or padding on the horizontal axis.
//
// It is a syntactic question asked without a layouter, so it cannot resolve a
// length and does not try: anything that is not written as a zero counts as
// possibly non-zero. Being wrong that way keeps a fragment that turns out to
// need nothing, which costs a margin collapse; being wrong the other way would
// drop room the author asked for. The first is the direction to be wrong in,
// and the common cases — the property absent, or a reset stylesheet's
// "margin: 0" — are both answered exactly.
func mayInsetHorizontally(cs style.ComputedStyle) bool {
	for _, side := range [2]string{"left", "right"} {
		if !isZeroLength(cs["margin-"+side]) || !isZeroLength(cs["padding-"+side]) {
			return true
		}
		if !noBorder(cs["border-"+side+"-style"]) && !isZeroLength(cs["border-"+side+"-width"]) {
			return true
		}
	}
	return false
}

// isZeroLength reports whether a declaration is absent or written as a zero.
//
// "auto" is a zero here because that is what it computes to for the horizontal
// margin of an inline box: §10.3.1 gives an inline box's auto margins the value
// zero rather than sharing out the space, which is what makes an inline box
// uncentreable.
func isZeroLength(v string) bool {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "", "0", "auto":
		return true
	default:
		n, ok := parseNumber(strings.TrimRight(s, "%abcdefghijklmnopqrstuvwxyz"))
		return ok && n == 0
	}
}

// clonePiece makes an empty copy of an inline box, keeping everything that
// decides how it is styled and measured.
func clonePiece(b *Box) *Box {
	return &Box{
		Outer: b.Outer, Inner: b.Inner,
		Element: b.Element, Style: b.Style,
		FontSize: b.FontSize, ListItem: b.ListItem, Replaced: b.Replaced,
		Float: b.Float, Clear: b.Clear,
		Position: b.Position, ZIndex: b.ZIndex, ZAuto: b.ZAuto,
		Order: b.Order,
	}
}

// wrapInlines is the anonymous block box rule of CSS Display §2.1: when a block
// container has any block-level child, every run of inline-level children is
// wrapped in an anonymous block box.
//
// Without it a block container would have two kinds of child and layout would
// have to handle a mixture at every step. With it, a block container's children
// are either all block-level or all inline-level, which is the invariant the
// whole of block layout is written against.
func (b *boxBuilder) wrapInlines(parent *Box) []*Box {
	if len(parent.Children) == 0 {
		return parent.Children
	}
	// The rule applies to *block containers*, and an inline box is not one —
	// which matters more than it sounds. Wrapping an inline box's children in
	// anonymous blocks would leave a block inside an inline formatting context
	// by another name, and it would do it *before* splitBlockInInline could see
	// the inline content to split, so the pieces would come out empty and the
	// styling of the split element would vanish.
	//
	// An inline-block is a block container despite being inline outside, which
	// is the whole of what flow-root means here.
	if !isBlockContainer(parent) {
		return parent.Children
	}

	// An out-of-flow box does not count as a block-level child here, and does
	// not end a run of inline content either. It neither needs an anonymous
	// block around it nor forces one around the text beside it — and the text
	// beside it is exactly what has to stay in one inline formatting context,
	// because that context is what shortens its line boxes to make room for a
	// float. Wrapping the text on each side of a float in its own anonymous
	// block would give the float two separate contexts to flow around and
	// neither would be the one it was placed in.
	//
	// For an absolutely positioned box the consequence is different and just as
	// necessary: its static position is where it was written among the words,
	// and an anonymous block boundary drawn through that run would put it at the
	// start of a box that the author's sentence does not contain.
	hasBlock := false
	for _, c := range parent.Children {
		if c.Outer == OuterBlock && !c.outOfFlow() {
			hasBlock = true
			break
		}
	}
	if !hasBlock {
		// All inline: the parent is an inline formatting context and no
		// anonymous box is needed.
		return parent.Children
	}

	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		// A run that is nothing but collapsible white space generates no
		// anonymous box. Otherwise "<div>\n<p>a</p>\n</div>" — whose newlines
		// are text nodes — would gain two anonymous blocks and two blank lines
		// that the author did not write and cannot see in the markup.
		if allWhitespace(run) {
			run = nil
			return
		}
		// An anonymous box inherits and has nothing else. Handing it the
		// parent's whole computed style would hand it the parent's margins,
		// padding, borders and background too — so the anonymous block around a
		// paragraph of text inside <body> would be indented by body's own
		// margin, and the gap it left would look like a deliberate one.
		anon := &Box{
			Outer: OuterBlock, Inner: InnerFlow,
			Style: style.Inherited(parent.Style), Parent: parent,
			FontSize: parent.FontSize,
			Children: run,
		}
		for _, c := range run {
			c.Parent = anon
		}
		out = append(out, anon)
		run = nil
	}

	for _, c := range parent.Children {
		if c.Outer == OuterBlock && !c.outOfFlow() {
			flush()
			out = append(out, c)
			continue
		}
		run = append(run, c)
	}
	flush()
	return out
}

// allWhitespace reports whether a run of inline boxes is nothing but white
// space that would be collapsed away.
//
// The qualification is the whole of it. CSS Display generates no anonymous
// block around white space that *collapses*, which is why an indented
// "<div>\n<p>a</p>\n</div>" gains no blank lines. White space that is preserved
// is content — a blank line inside a <pre> is a line the author wrote — so a
// run of it generates its box like any other text.
func allWhitespace(run []*Box) bool {
	for _, c := range run {
		if c.outOfFlow() {
			// An out-of-flow box in the run is content, even when every
			// character around it is a space. Dropping the run would drop it.
			return false
		}
		if !c.IsText() {
			return false
		}
		if !whiteSpaceOf(c.Style["white-space"]).collapse {
			return false
		}
		if strings.TrimSpace(c.Text) != "" {
			return false
		}
	}
	return true
}

// listValueOf reads the "list-item" counter for a list item.
//
// It is resolved at build time because that is when the counters are known: they
// depend on what came *before* an element in the document, which the layout walk
// cannot answer while descending. See counter.go.
func (b *boxBuilder) listValueOf(n *html.Node, listItem bool) (int, bool) {
	if !listItem {
		return 0, false
	}
	if vals := b.counters[n]["list-item"]; len(vals) > 0 {
		return vals[len(vals)-1], true
	}
	return 0, false
}
