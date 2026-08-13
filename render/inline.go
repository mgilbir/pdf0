package render

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/grapheme"
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
//   - after an "other space separator" that UAX #14 puts in class BA or ID —
//     everything in the Zs category except U+0020 and U+00A0, less the two the
//     algorithm calls GL, which are U+2007 FIGURE SPACE and U+202F NARROW
//     NO-BREAK SPACE. break-spaces overrides the two exceptions, because that
//     value puts an opportunity after every separator without qualification;
//   - at a zero-width space, which is how an author marks an opportunity
//     inside a word;
//   - after a hyphen that is not the last character of its run;
//   - on both sides of an ideograph.
//
// Two of the omissions are worth naming because they are the ones that show. A
// break is not taken *before* an ideograph or an ideographic space where the
// text before it is Latin, though LB31 allows one; and the class table is a
// handful of ranges rather than the whole of it. Both err towards fewer
// opportunities, which overflows a line rather than breaking it in the wrong
// place — the direction that fails visibly.
//
// That covers Latin, Greek, Cyrillic and the CJK scripts, which is most of what
// a document generator sets. Everything else UAX #14 says is *not* done, and the
// family that matters is not approximated: the scripts that need a dictionary to
// find a word boundary — Thai, Lao, Khmer, Burmese.
//
// It is refused through the finding vocabulary rather than guessed at, because
// §6.3 is exactly right about it: unbroken text still looks like text, so the
// failure mode looks like success. A paragraph of Thai run together as one
// unbreakable word overflows silently and reads as a rendering bug rather than
// as an unimplemented feature.
//
// The other refusal that used to be here — the bidirectional reordering a
// right-to-left line needs — is not a refusal any more. bidi.go resolves the
// levels over each paragraph and this file reorders the runs of each line once
// it has been broken; see visualRuns below.
//
// word-break: break-all *is* implemented, and the machinery it needed is worth
// naming because it turned out to be the answer to a question this file had
// wrong for everything, not only for break-all.
//
// It asks for a break *between two characters*, and CSS Text §2 says which
// positions those are: a soft wrap opportunity falls between typographic
// character units, which is the grapheme cluster. Breaking inside one splits a
// letter from its accent.
//
// The shaper's clusters are the obvious candidate for those positions and are
// not them, which was measured rather than assumed — graphemecluster_test.go
// shapes five strings whose UAX #29 segmentation is not in doubt and finds
// forme's clusters finer than the grapheme clusters in every one: a base with
// two combining marks breaks between the marks, a keycap breaks off its digit,
// conjoining Hangul breaks into three, a flag breaks into two letters, and a
// Thai spacing mark leaves its consonant. In a right-to-left run the clusters
// are not even ordered — the glyphs come back as they are drawn — so "the
// cluster changed" does not name a position in the text.
//
// So internal/grapheme is a UAX #29 segmenter over the *characters*, which is
// where this belongs anyway since splitAtBreaks never sees a glyph. Every cut
// this file makes now goes through it, not only break-all's, and that caught a
// break the ideograph rule had been offering since it was written: a Hangul
// syllable spelled as a precomposed LV plus its own trailing jamo was cut in
// two, so a line could end in the middle of one letter.
//
// keep-all and auto-phrase are still not implemented, and both *remove* or
// *move* an opportunity rather than adding one — so ignoring either breaks a
// line where the author said not to, and both are reported.
//
// overflow-wrap is not implemented and is reported. It is a different shape of
// problem from break-all rather than a smaller one: its opportunities exist
// only when the line cannot otherwise fit, so it is not a question about the
// text at all but about what the breaker should do having failed once.
//
// # White space at a line edge
//
// The rest of CSS Text §4 lives here, and whitespace.go explains why it is
// split across three stages rather than done once. What this file owns is
// §4.1.2: the collapsible space removed at each end of a line, the tab stops a
// preserved tab advances to, and the preserved space that hangs past the line's
// end instead of pushing the next word down.
//
// The hang has two strengths and the difference is not cosmetic. A line that
// ended at a soft wrap hangs its trailing white space *unconditionally* — it is
// never counted for fit or alignment. A line that ended at a forced break, which
// includes the end of the content, hangs it *conditionally*: it hangs only if it
// does not otherwise fit, so a space that fits is content and the line is
// aligned around it. inlineContent applies the second and alignedWidth the
// first.

// # What relative positioning does to an inline box, and what it does not
//
// §9.4.3 applies to an inline box like any other, and the offset is carried on
// each run rather than on one fragment because an inline box does not have one:
// a <span> broken across a line contributes to two line boxes and to neither
// exclusively. It has a fragment *per line* — see LineFragment.Boxes and
// inlinepaint.go — and the offset reaches those the same way, recorded per box
// as the walk accumulates it and folded into the position by absolutise.
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
	// Runs are the pieces of text on the line, in *reading* order — the order
	// they were written, which on a line that mixes directions is not the order
	// they are drawn in. Where each one goes is its own X.
	//
	// The two are kept apart on purpose. The order here is the order the runs
	// reach the content stream, and so the order a reader extracting text from
	// the finished page gets them in; a right-to-left paragraph has to be drawn
	// the way it reads and copied out the way it was written, and only the X
	// decides the first.
	Runs []TextRun
	// Boxes are the fragments of the inline boxes that have a background or a
	// border on this line, in tree order — so an inner box's decoration is
	// painted over the box it is inside.
	//
	// They are fragments rather than a shape of their own because everything that
	// paints a background already works on one, and they hang from the line
	// rather than from the block's children for two reasons: Appendix E paints
	// them with the line's own content and not with the block backgrounds, and
	// one inline box produces one of them *per line* — see inlinepaint.go for
	// §8.6's slice model, which is the whole of why this is plural.
	//
	// A box with nothing to draw has none. The overwhelming majority of the
	// inline boxes in a document are an <em> or an <a> with no background and no
	// border, and a rectangle for each of them on each line would be work in
	// proportion to the document that nothing would ever read.
	Boxes []*Fragment
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
	// Decorations are the lines ruled across this run: CSS 2.1 §16.3.1's
	// underline, overline and line-through. They are on the run rather than
	// derived from Box at paint time because a decoration belongs to whichever
	// *ancestor* declared it, and that box's colour is the line's colour — see
	// textdecoration.go, where the difference between propagating and inheriting
	// is worked through.
	Decorations []textDecoration
	// RTL says the run reads right to left, so its glyphs are drawn from the
	// right edge of its box towards the left and its brackets are mirrored.
	//
	// It is on the run rather than derived from the text because it is not a
	// property of the text: a run of punctuation between two Hebrew words is
	// right-to-left and has nothing in it that says so. The algorithm decided it
	// from the neighbours, which are other runs by the time anything paints this
	// one.
	RTL bool
	// LetterSpacing is what letter-spacing added after each character of this
	// run. It is carried into painting as well as into the width because the two
	// have to agree: the width decided where the next run starts, and glyphs
	// drawn without the same spacing would leave a gap the size of the whole
	// run's spacing before it.
	LetterSpacing style.Unit
	// Offset is §9.4.3's relative displacement, accumulated over the inline
	// boxes this run sits inside.
	//
	// It is on the run rather than on a single fragment because a <span> that
	// spans a line break has one fragment per line: a line box holds runs, and
	// the box's own background and border are a Boxes entry on each of the lines
	// it reaches. Both are moved by the same displacement and are given it
	// separately — this one at paint time, the fragment's in absolutise, because
	// a background image is placed against the rectangle its box is drawn at.
	Offset Point
	// Shift is how far this run's own baseline sits below the line box's,
	// which is §10.8.1's vertical-align applied to the inline box the run came
	// from. It is negative for a run that is raised.
	//
	// It is a length on the run rather than a line of its own because a line
	// box has exactly one baseline — §10.8's alignment is *against* that
	// baseline — and every run on the line is placed relative to it. Folding it
	// into Offset would have been shorter and is wrong: Offset is §9.4.3's
	// relative positioning, which moves a box after layout without changing
	// anything about the line, whereas this displacement is part of what
	// decided the line's height.
	Shift style.Unit
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
	// valign is §10.8.1's vertical-align, accumulated over the inline boxes the
	// walk is currently inside.
	//
	// It travels on the frame for the same reason offset does: the flattening
	// destroys the boxes, and a run of text has to carry out of it whatever its
	// ancestors declared. A text box cannot be asked — vertical-align is not
	// inherited, so the anonymous box holding a <span>'s words has the initial
	// value whatever the span said.
	valign vAlignState
	// bidi collects the context's text in logical order as the walk flattens it,
	// so that the bidirectional algorithm has a paragraph to run over. It is nil
	// while measuring: an intrinsic width is a sum over the items and does not
	// depend on the order they are set in.
	bidi *bidiBuilder
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
	//
	// "Of any kind" is §4.1.2's sense — white space, other space separators and
	// preserved tabs — rather than Phase I's, which is only U+0020, U+0009 and
	// the segment breaks. The two differ over the ideographic space and its
	// relatives, which hang at the end of a line and are never collapsed.
	space bool
	// collapsible marks white space that §4.1.2 removes when it lands at
	// either end of a line.
	//
	// It is not the same question as space, and conflating the two is what made
	// "<pre>   x</pre>" lose its indentation: the leading run is white space,
	// so it was dropped at the start of the line, but it is *preserved* white
	// space and dropping it removes something the author wrote.
	collapsible bool
	// trimAtEnd marks white space that §4.1.2's third rule removes outright when
	// it lands at the end of a line, rather than hanging past it.
	//
	// It is the collapsible spaces, and one character more: "any trailing U+1680
	// OGHAM SPACE MARK whose white-space property is normal, nowrap, or
	// pre-line". The ogham space mark is not collapsible — it is a *visible*
	// space, a stemline in an ogham face, and collapsing a run of them would
	// shorten a line of ogham the way collapsing a run of hyphens would shorten a
	// rule — so it needs the removal without the collapsing, and that is why this
	// is a second flag rather than a use of the first.
	trimAtEnd bool
	// hangs marks preserved white space that sits past the end of the line
	// rather than moving to the next one.
	//
	// §4.1.2 hangs whatever white space its third rule left at the end of a
	// line, so it is not counted when the line is measured for alignment and
	// never causes a break of its own. Two values are named as not doing it:
	// break-spaces, which is the whole difference between it and pre-wrap, and
	// pre, which the rule does not list — a line under pre ends only where the
	// author ended it, and the rule is about what happens at a wrap.
	hangs bool
	// hangsHard says the hang is unconditional: the sequence never takes
	// room, whether or not there is room for it. §4.1.2 gives that answer
	// for normal, nowrap and pre-line, and the conditional one for pre-wrap
	// before a forced break — where the sequence does take room, and gives
	// it up only when it would overflow. The two differ nowhere on the page
	// and differ in both intrinsic widths.
	hangsHard bool
	// breakWord is overflow-wrap's last-resort break, carried per item because
	// the property is the box's and a line holds items from several boxes.
	breakWord bool
	// anywhere is the value of overflow-wrap that also lowers the min-content
	// width, so a shrink-to-fit box narrows to its widest character rather than
	// to its widest word. §5.5 says break-word's opportunities "are not
	// considered when calculating min-content intrinsic sizes" and anywhere's
	// are, which is the whole difference between the two values.
	anywhere bool
	// tab marks one preserved tab. Its advance is not a property of the text —
	// it is the distance to the next tab stop, so it is resolved when the tab
	// has a place on a line and not before.
	tab bool
	// tabStop is the distance between two tab stops, from tab-size.
	tabStop style.Unit
	// tabFloor is §4.1.2's 0.5ch threshold: a tab whose shift would be shorter
	// than this advances to the tab stop after the nearest one instead.
	tabFloor style.Unit
	// forced marks a break the author asked for — a <br>, or a newline in
	// preserved white space. It ends the line wherever it falls, which is the
	// difference between a break opportunity and an instruction.
	forced bool
	// noWrap marks text that may not break at its spaces, so a line takes it
	// whole or overflows.
	noWrap bool
	// inset marks an item that is an inline box's own horizontal margin, border
	// and padding rather than anything of its content: §8.3, §8.4 and §8.5 make
	// all three apply to a non-replaced inline box on the horizontal axis, and
	// what they do there is push the content along. See insetItems.
	inset bool
	// insetLead distinguishes the two: the item before the box's content from
	// the item after it, in *logical* order.
	//
	// Which of them carries the box's left inset and which its right is not the
	// same question, and on a right-to-left line it is the other answer — see
	// insetSides for §8.6. The flag says where the item sits in the content, and
	// the width says what it holds.
	insetLead bool
	// insetLevel is the embedding level the box's own edges sit at, and
	// insetLevelKnown says insetSides worked one out.
	//
	// An inset carries no characters, so the algorithm gives it no level of its
	// own, and the two obvious guesses are both wrong somewhere: the level of the
	// neighbouring item glues the box's edge to whatever run happens to abut it,
	// and the paragraph's base level detaches it from its own content. What the
	// edge of an inline box sits at is the *lowest* level anything inside it
	// reached — an embedding inside the box only raises the level of what is
	// inside, and the box's own boundary is outside all of them.
	//
	// The flag is separate because zero is a real level, the left-to-right one,
	// and a box with no content on the line at all has to stay distinguishable
	// from a box whose content is left-to-right.
	insetLevel      uint8
	insetLevelKnown bool
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
	// leads reports that this item is a run of text whose own inline box takes
	// part in §10.8.1's stacking, and above and below are how far it reaches from
	// the baseline.
	//
	// The flag is separate from the two lengths rather than derived from them
	// because zero is a legitimate answer: "line-height: 0" on a <span> gives a
	// run that reaches nowhere at all, and reading that as "this item has no
	// metrics" would let a taller strut win a line the span was supposed to
	// collapse. Every other kind of item — a float marker, an absolutely
	// positioned one, an inline box's own inset — leaves it clear.
	leads        bool
	above, below style.Unit
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
	// valign is §10.8.1's vertical-align, as the walk accumulated it over the
	// inline boxes this item sits inside.
	valign vAlignState
	// decorations are the lines ruled across this item, and spacing is what
	// letter-spacing and word-spacing added to its width. Both travel with the
	// item because the flattening loses the boxes they were read from.
	decorations []textDecoration
	spacing     textSpacing
	// abs is the box of an absolutely positioned box met in this run, and it is
	// a marker for the same reason and a different consequence. A float met
	// among the words changes where the words go; an absolutely positioned one
	// does not change anything at all, but its *static position* — where it
	// would have been — is what §10.3.7 falls back on, and that is exactly the
	// information the flattening destroys.
	abs *Box
	// bidiPara, bidiStart and bidiEnd say where this item's text sits in the
	// inline formatting context's bidi paragraphs, which is what the algorithm
	// resolves levels over.
	//
	// bidiPara counts from one so that zero means "contributes no characters",
	// which is what a float or an absolutely positioned box is: out of flow, and
	// taking no part in the ordering. Numbering from zero would have made a
	// forgotten field on any of the several places that build an item read as a
	// claim to be the first character of the first paragraph.
	bidiPara           int
	bidiStart, bidiEnd int
	// para is the resolved paragraph, filled in once the algorithm has run, and
	// level is this item's embedding level. Both are zero-valued in a document
	// that needs no reordering, which is what tells the line builder there is
	// nothing to do.
	para  *bidiParagraph
	level uint8
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
// # What decides where a finished line sits
//
// Two things move a line's content within the band it was measured against, and
// they are added rather than chosen between: §16.1's text-indent, which applies
// to the first line box only and is taken off the room that line has for text,
// and §16.2's text-align, which distributes whatever is left over. Both are
// applied after the line has been broken, because the width a line is *aligned*
// at is not the width it was *broken* at — §4.1.2 excludes a hanging space "for
// fit, alignment, or justification", and the fit has already happened. textalign.go
// has the rest of that argument.
func (l *layouter) inlineContent(b *Box, parent *Fragment, width style.Unit, origin flow) style.Unit {
	st := l.strutFor(b)
	// The block container is one bidi paragraph — or several, where it holds a
	// forced break — and its own unicode-bidi may wrap the whole of it in an
	// override. Both are decided before the walk, because the walk is what fills
	// the paragraph in.
	_, open, closing := paragraphDirection(b)
	para := newBidiBuilder(open)
	items, _ := l.collectInline(b, l.markerItems(b), startOfContext(), inlineFrame{
		containing: width, cbHeight: origin.cbHeight, cbDefinite: origin.cbDefinite,
		strut: st, bidi: para,
	})
	para.leave(open, closing)
	if len(items) == 0 {
		return 0
	}
	items = l.resolveBidi(b, items, para)

	lo, hi := origin.x, origin.x.Add(width)

	// §16.1's indent, which applies to the first line box this container
	// generates and to no other. It is resolved once: a percentage is of this
	// box's own content width, which is the width being laid out in.
	indent := l.textIndent(b, width)
	firstLine := true
	_ = firstLine

	// CSS Overflow 4's line-clamp: how many lines this block shows, and whether
	// it has to say that it cut something off.
	maxLines := l.lineClamp(b)
	clampEllipsis := style.Unit(0)
	if maxLines > 0 && l.countLines(items, width, indent, maxLines+1) > maxLines {
		// Only when content is actually discarded. A block clamped to three
		// lines that has two says nothing, which is the difference between the
		// property and a truncation.
		// The block's own font, because §"block-ellipsis" puts the mark in the
		// block container rather than in whatever inline box the last line
		// happened to end inside — the suite's line-clamp-002 sets the text in a
		// span a quarter the block's size and expects the ellipsis at the
		// block's.
		if face, ok := l.fontFor(b); ok {
			clampEllipsis = l.measure(face, blockEllipsis, b.FontSize)
		}
	}

	// §5.1's balancing, as a cap on how wide a line may be broken.
	//
	// It is a cap and not a width: the line boxes still span the band, and only
	// the *breaking* is done in the narrower measure. Narrowing the box itself
	// would move a centred line and shorten a right-aligned one, which is a
	// different rendering from the one balancing asks for.
	balanceCaps := l.balanceCaps(b, items, width, indent)
	if balanceCaps != nil && maxLines > 0 {
		// Balancing a clamped block is a different question, because the clamp
		// has already decided how many lines there are: any width at all
		// produces that many, so "the narrowest width with the same line count"
		// asks nothing. What must not change is how much of the content is
		// *shown* — §5.1 evens out the lines, it does not throw more away — so
		// the search is over the reach instead. See balanceClampedWidth.
		w := l.balanceClampedWidth(items, width, indent, clampEllipsis, maxLines)
		for i := range balanceCaps {
			balanceCaps[i] = w
		}
	}
	// Whether any line of a balanced box turned out to be shortened by a float,
	// which is what says the balancing has to be done again in the widths those
	// lines really had.
	balanceMetFloat := false

	// The inline boxes with a background or a border, gathered as the lines are
	// built and turned into fragments once the last line is known: §8.6 puts a
	// box's trailing margin, border and padding on the piece it ends with, and
	// which piece that is cannot be answered until there are no more.
	var decor inlineDecor

	// Where this box's lines begin, so that a first attempt at them can be taken
	// back. Balancing beside a float needs the widths the lines actually had,
	// and those are not known until the lines exist: the floats inside the box
	// are placed as the loop meets them, and what shortens a line is decided by
	// the lines above it. So the box is laid out once to find out, thrown away,
	// and laid out again in the measure that answer gives.
	//
	// It is thrown away with the same three handles the per-line retry above
	// uses — the float context, the out-of-flow queue and the fragment's own
	// children — plus its lines. Anything else the pass touched is a memo keyed
	// by box, and recomputing it gives the same answer.
	linesAt, kidsAt := len(parent.Lines), len(parent.Children)
	ctxAt, absAt := origin.ctx.mark(), len(l.deferred)

	var y style.Unit
	// The width each line turned out to have, which is the band a float left it.
	var bands []style.Unit
	// iByte is how far into items[i] the next line begins, which is other than
	// zero only where overflow-wrap ended the line inside a word.
	var iByte int
	for pass := 0; ; pass++ {
		y, iByte = 0, 0
		bands = bands[:0]
		firstLine = true
		balanceMetFloat = false
		decor = inlineDecor{l: l, containing: width, strut: st}
		for i := 0; i < len(items); {
			// Where this pass started, so that the foot of the loop can tell whether
			// it moved. Nothing in the body increments the cursor on its own: it is
			// carried entirely by what breakOneLine hands back.
			wasI, wasByte := i, iByte
			// A float that begins a line is placed before the line is measured,
			// because it is one of the floats the line has to avoid. §9.5.1 rule 4
			// puts its top at the top of the line box it belongs to.
			for iByte == 0 && i < len(items) && items[i].float != nil {
				parent.Children = append(parent.Children,
					l.floatChild(items[i].float, width, origin, y, style.MaxUnit, 0, 0))
				i++
			}
			if i >= len(items) {
				break
			}

			left, right := origin.ctx.bandAt(origin.y.Add(y), lo, hi)
			// What has to fit beside a float is what this line will actually start
			// with, which is the *remainder* of a word the line before broke — not
			// the whole word, whose width would push the line down past a float it
			// has room beside.
			firstItem := items[i]
			if iByte > 0 {
				_, firstItem = l.splitItem(firstItem, iByte)
			}
			y, left, right = l.roomForLine(firstItem, origin, y, left, right, lo, hi)

			// The indent is taken off the room the first line has for text, not off
			// the line box: the line box still spans the band, and its content starts
			// further in. Taking it off the box instead would make a "text-align:
			// right" first line end short of the right edge by the indent, which is
			// the opposite of what §16.1 asks for.
			lineIndent := style.Unit(0)
			if firstLine {
				lineIndent = indent
			}
			// The room the ellipsis needs on this line, which is the last one the
			// clamp allows and nothing before it.
			lineEllipsis := style.Unit(0)
			if maxLines > 0 && len(parent.Lines) == maxLines-1 {
				lineEllipsis = clampEllipsis
			}

			// A line is shortened by every float its *box* meets, not only by the
			// ones its top edge meets — §9.5's "line boxes created next to the float
			// are shortened to make room for the margin box of the float", where
			// "next to" is a relation between two rectangles. So the band has to be
			// asked over the line's height, and the line's height is not known until
			// the line has been broken against a band.
			//
			// The circle is broken the way browsers break it: break against the band
			// at the top, measure, ask again over the height that produced, and
			// break again if the answer moved. A float whose top is below the top of
			// the line is exactly the case a single-y query cannot see, and the suite
			// tests it by name in floats-wrap-top-below-inline-*.
			var (
				runs     []inlineItem
				next     int
				nextByte int
				mid      []midLineBox
				forced   bool
				lh, bl   style.Unit
				stack    lineStack
				midKids  []*Fragment
				after    []midLineBox
				baseRoom = right.Sub(left)
			)
			// Where this line's own floats begin, so that an attempt that has to be
			// made again can put the context back as it found it. The floats that
			// *started* the line are before the mark and stay.
			midMark, midAbs := origin.ctx.mark(), len(l.deferred)
			for attempt := 0; ; attempt++ {
				origin.ctx.truncate(midMark)
				l.deferred = l.deferred[:midAbs]
				midKids = midKids[:0]

				runs, next, nextByte, mid, forced = l.breakOneLine(items, i, iByte,
					// The cap is a *line* width, so the indent comes off it and not
					// off the band before it: the search counted the first line's
					// room as the balanced width less the indent, and taking the
					// indent off the band instead makes the two disagree by exactly
					// the indent on the one line it applies to.
					style.Min(right.Sub(left), capAt(balanceCaps, i)).
						Sub(lineIndent).Sub(lineEllipsis),
					left.Sub(lo).Add(lineIndent))
				stack = stackLine(runs, st)
				lh, bl = stack.height, stack.baseline

				// A float met part-way along the line is placed *now*, before the
				// band is asked again, because §9.5 shortens "the current and
				// subsequent line boxes" — the line it appears on included. Placing
				// it after the line was settled, which is what this used to do, left
				// the words it should have pushed aside sitting underneath it: the
				// suite's floats-001, -006, -029, -030 and -031 each put a float
				// after the content of a line and check that the content moved.
				//
				// The room it is offered is measured in the band the line started
				// from rather than in the one it has been narrowed to. That is what
				// makes the loop settle: narrowing the band and then asking again
				// whether the float fits in what is left of it counts the float
				// twice, and a float that fits on one attempt stops fitting on the
				// next and starts fitting again on the one after. Against the
				// original band the advance can only shrink as the band narrows, so
				// a float that fits goes on fitting.
				//
				// A float that ends up *below* the line — because it did not fit
				// beside what is already there, or because it cleared something —
				// is taken back out and placed once the line is settled. It is not
				// beside the line, so it does not shorten it, and leaving it in
				// would: a line box is a little taller than the box on it, so a
				// float sitting under the line reaches a few pixels into the range
				// the band is asked over and narrows it to nothing. floats-
				// placement-006 is that exactly, an inline-block and a "clear: both"
				// float that between them drove the line two hundred pixels down
				// the page.
				//
				// From the first such float onwards the rest are placed after the
				// line too, in order. Rolling one back is a truncation, so it can
				// only be the last thing in the context; and a float placed while an
				// earlier one is missing is placed against the wrong obstacles.
				after = after[:0]
				for _, f := range mid {
					if f.abs {
						continue
					}
					if len(after) > 0 {
						after = append(after, f)
						continue
					}
					held, heldAbs := origin.ctx.mark(), len(l.deferred)
					kid := l.floatChild(f.box, width, origin, y, baseRoom.Sub(f.used), lh, 0)
					if kid.MarginRect().Y > y {
						origin.ctx.truncate(held)
						// The out-of-flow boxes the discarded layout found go with
						// it, or the float would defer each of them twice when it is
						// laid out again after the line.
						l.deferred = l.deferred[:heldAbs]
						after = append(after, f)
						continue
					}
					midKids = append(midKids, kid)
				}

				if lh <= 0 || attempt >= maxLineFits {
					break
				}
				top := origin.y.Add(y)
				nl, nr := origin.ctx.bandOver(top, top.Add(lh), lo, hi)
				if nl == left && nr == right {
					break
				}
				// The narrower band may leave no room at all, in which case the line
				// drops past the float exactly as it would have at its top edge.
				ny, nl, nr := l.roomForLine(items[i], origin, y, nl, nr, lo, hi)
				if ny == y && nl == left && nr == right {
					break
				}
				y, left, right = ny, nl, nr
			}
			// On the line the clamp ends at, a unit too wide to sit beside the
			// ellipsis is not shown at all. Everywhere else this engine sets an
			// over-long word anyway, because the alternative is losing it; here
			// losing it is exactly what the clamp asks for, and the mark is what
			// stands in its place. "123456789012" against nine characters less an
			// ellipsis leaves the line holding nothing but the mark, which is what
			// line-clamp-004 draws.
			if lineEllipsis > 0 {
				var used style.Unit
				for _, r := range runs {
					used = used.Add(r.width)
				}
				if used > right.Sub(left).Sub(lineIndent).Sub(lineEllipsis) {
					runs, next, nextByte = nil, len(items), 0
					stack = stackLine(runs, st)
					lh, bl = stack.height, stack.baseline
				}
			}

			lineWidth := right.Sub(left)
			if balanceCaps != nil && lineWidth < width {
				balanceMetFloat = true
			}
			bands = append(bands, lineWidth)
			textWidth := lineWidth.Sub(lineIndent)
			if len(runs) > 0 || forced || lineEllipsis > 0 {
				line := LineFragment{
					Rect:     Rect{X: left.Sub(lo), Y: y, W: right.Sub(left), H: lh},
					Baseline: bl,
				}
				// Rules L1 and L2 over the runs of this line: where each of them
				// sits, which on a line mixing directions is not the order they
				// were collected in.
				xs, total := l.lineOffsets(runs)
				// Atomic inlines are placed as children of the block rather than as
				// runs, so aligning the line has to move them too. The range is
				// noted here because floats placed before the line are already in
				// this slice and must not move: a float is out of flow and
				// text-align says nothing about it.
				atomicStart := len(parent.Children)
				for k, item := range runs {
					x := xs[k]
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
						f.BorderRect.Y = y.Add(stack.atomicTop(item)).Add(f.Margin.Top)
						parent.Children = append(parent.Children, f)
						continue
					}
					if item.inset {
						// An inline box's own margin, border and padding. It has
						// taken its room on the line already — lineOffsets counted
						// its width, so everything after it is where it belongs —
						// and it draws nothing, so it becomes no run. A run with no
						// text would reach the content stream as an empty
						// text-showing operator and reach a reader extracting the
						// page as nothing at all.
						continue
					}
					shift := style.Unit(0)
					decorations := item.decorations
					if item.valign.aligned() {
						shift = stack.shift(item.valign, item.above, item.below)
						// §16.3.1: a decoration declared by an ancestor is drawn at
						// *that* box's position and is not moved by the alignment of
						// what it crosses — "text decorations on inline boxes are
						// drawn across the entire element, going across any descendant
						// elements without paying any attention to their presence". So
						// three spans at three different vertical-aligns under one
						// overlining div are crossed by one straight line, not three
						// stepped ones.
						//
						// The copy is made only for a run something moved, which is
						// almost none of them.
						decorations = l.decorationsAt(decorations, &stack)
					}
					line.Runs = append(line.Runs, TextRun{
						Text: item.text, Face: item.face, Size: item.size,
						X: x, Width: item.width, Box: item.box, Offset: item.offset,
						Decorations: decorations, LetterSpacing: item.spacing.letter,
						RTL:   item.level&1 == 1,
						Shift: shift,
					})
				}
				// §4.1.2's hang comes in two strengths and the difference shows
				// exactly here. Where the line ended at a soft wrap, its trailing
				// white space hangs *unconditionally* and is never counted, which is
				// what alignedWidth does. Where it ended at a forced break — a <br>,
				// a preserved newline, or simply the end of the content — it hangs
				// *conditionally*: "it hangs only if it does not otherwise fit in the
				// line", so a space that fits is content and the line is aligned
				// around it. The specification's own example is a centred pre-wrap
				// paragraph reading " 0 " in five characters, which centres as three
				// and not as two.
				//
				// The two are not a matter of degree. Counting the space on a
				// soft-wrapped line pushes a right-aligned line a space clear of the
				// edge; not counting it on the last line centres " 0 " off-centre.
				//
				// "Only if it does not otherwise fit" is a rule about each character
				// and not about the sequence, which is the second half of the same
				// paragraph: the UA "may also visually collapse the character advance
				// widths of any that would otherwise overflow". So a sequence that
				// fits counts entirely, one that does not counts up to the line's
				// edge, and the part past the edge hangs. Taking it as all-or-nothing
				// is the reading that was here, and it centres a line of five
				// characters and thirty-two spaces as though it were five: the page
				// shows the letter two characters right of where every browser puts
				// it, and the same document without the spaces is correct, so the
				// fault reads as an alignment bug rather than a white-space one.
				used := alignedWidth(runs, total)
				if forced || next >= len(items) {
					used = style.Max(used, style.Min(total, textWidth))
				}
				// Which *side* it hangs off, which is not a second way of saying how
				// much. §4.1.2 hangs the white space past the line's end, and the
				// end of a right-to-left line is its left edge: rule L1 has already
				// given the trailing spaces the paragraph's own level, so
				// lineOffsets draws them leftmost — at the positions before the
				// first word rather than after the last.
				//
				// So on such a line the content does not begin where alignLine put
				// it, it begins a hang further in. Aligning without this leaves
				// every right-to-left pre-wrap line pushed right by the width of the
				// space it was meant to hang, which is what the ten dir=rtl
				// pre-wrap-align tests measure. It is invisible in a left-to-right
				// document, where the hang follows the content and moves nothing.
				rtl := lineBaseIsRTL(b, runs)
				shift := lineIndent.Add(l.alignLine(b, rtl, textWidth, used))
				if rtl {
					shift = shift.Sub(total.Sub(used))
				}
				if shift != 0 {
					for k := range line.Runs {
						line.Runs[k].X = line.Runs[k].X.Add(shift)
					}
					for k := atomicStart; k < len(parent.Children); k++ {
						parent.Children[k].BorderRect.X =
							parent.Children[k].BorderRect.X.Add(shift)
					}
				}
				// The inline boxes on this line, recorded now because the alignment
				// has moved everything on it for the last time. The index is where
				// the line is about to go, since a fragment cannot be hung on it
				// until §8.6 knows which piece of its box it is.
				decor.addLine(len(parent.Lines), runs, xs,
					line.Rect.X.Add(shift), line.Rect.Y.Add(line.Baseline), &stack)
				if lineEllipsis > 0 {
					// The mark goes where the line's own content ends, which is not
					// where the line box does: an aligned line may have been moved,
					// and a right-to-left one ends at its left. alignedWidth is the
					// same measure the alignment used.
					at := shift.Add(used)
					if lineBaseIsRTL(b, runs) {
						at = shift.Sub(lineEllipsis)
					}
					if face, ok := l.fontFor(b); ok {
						line.Runs = append(line.Runs, TextRun{
							Text: blockEllipsis, Face: face, Size: b.FontSize,
							X: at, Width: lineEllipsis, Box: b,
						})
					}
				}
				parent.Lines = append(parent.Lines, line)
				// The indent belongs to the first line box that actually exists. A run
				// of inline content that produced none — the collapsible space between
				// two block children — has not used it up.
				firstLine = false
			}

			// The floats met along the line were placed while it was being fitted —
			// the line had to be broken against them — but their fragments join the
			// tree here, after the line's own boxes, so that paint order is the
			// order the display list has always had.
			parent.Children = append(parent.Children, midKids...)
			for _, f := range after {
				parent.Children = append(parent.Children,
					l.floatChild(f.box, width, origin, y, lineWidth.Sub(f.used), lh, 0))
			}

			// An absolutely positioned box met along the line is dealt with once the
			// line is settled, because until it is neither its top nor its left edge
			// is known, and both are what it needs.
			for _, f := range mid {
				if f.abs {
					// §10.6.4's static position for a box written among the words:
					// the top of the line box it appeared on, and the pen position
					// it appeared at. The x is taken from the line's own left edge
					// rather than the block's, so a box written beside a float
					// records the position it would really have had.
					//
					// §10.3.7 asks for the same position from the other side, because
					// a right-to-left containing block anchors "right" rather than
					// "left". f.used is the advance in *logical* order, which is from
					// the left on a left-to-right line and from the right on a
					// right-to-left one — so the two are mirror images and neither is
					// the negation of the other: one counts from the line's left edge
					// and the other from the block's content right edge, and the line
					// need not reach either.
					if !f.box.staticInline {
						// §10.3.7's hypothetical box for a box that would have been
						// block-level had it been static: a block box, whose margin
						// edges are the containing block's content edges. Only the
						// line it was written on is taken from here — which is what
						// makes this different from the block walk's answer and the
						// reason the box is collected among the words at all.
						//
						// Reading the pen position instead is invisible until
						// something moves the line's own left edge, and then it is
						// not: float-in-inline-001 and position-absolute-007 in the
						// suite each write such a box beside a float and check that
						// the float did not carry it along.
						l.deferAbsolute(f.box, parent, 0, y, 0, 0)
						continue
					}
					l.deferAbsolute(f.box, parent, left.Sub(lo).Add(f.used), y,
						width.Sub(right.Sub(lo).Sub(f.used)), 0)
				}
			}

			i, iByte = next, nextByte
			// The cursor has to move, and only forwards. A break that hands back the
			// position it was given is a bug in breakOneLine rather than anything a
			// document can ask for — but the loop has no increment of its own, so
			// such a break is not a wrong line, it is a render that never finishes:
			// the same line is measured, appended and measured again until the
			// process is killed. Two of the mutation plants over this function reach
			// exactly that state, and unguarded each one takes 25GB before the OOM
			// killer arrives.
			//
			// So the cursor is pushed past the item it stalled on. What that costs is
			// the rest of one item, and what it buys is the same bargain maxLineFits
			// strikes: output that is slightly wrong beats output that never comes.
			//
			// The comparison is a function of its own because it is the part that can
			// be got wrong quietly: the recovery below cannot be reached by any
			// document, so a test can only reach the decision.
			if !cursorAdvanced(wasI, wasByte, i, iByte) {
				i, iByte = wasI+1, 0
			}
			if len(runs) > 0 || forced || lineEllipsis > 0 {
				// Only a line that exists occupies a line's height. A run of inline
				// content that is nothing but the collapsible space between two
				// block children produces no line box at all, and giving it one
				// would put a blank line into every document whose markup is
				// indented.
				//
				// The clamp's own line exists even when nothing of the content fits
				// on it, because the ellipsis is on it: a block clamped to three
				// lines whose third holds only the mark is three lines tall.
				y = y.Add(lh)
			}
			if maxLines > 0 && len(parent.Lines) >= maxLines {
				// The clamp: everything after this line is discarded, which is what
				// "continue: discard" means and what the ellipsis just said.
				break
			}
		}
		if pass > 0 {
			// Laid out twice already: the second pass balanced in the bands the
			// first one measured, which is the best this can do.
			break
		}
		if !balanceMetFloat || maxLines > 0 {
			// Nothing to do again: the box does not balance, or no float reached
			// into it. A clamped box is left to the first answer as well — the
			// clamp's own search is over how far the content reaches rather than
			// over a line count, and the two have not been put together.
			break
		}
		parent.Lines = parent.Lines[:linesAt]
		parent.Children = parent.Children[:kidsAt]
		origin.ctx.truncate(ctxAt)
		l.deferred = l.deferred[:absAt]
		w := l.balanceWidthInBands(items, bands, width, indent)
		for i := range balanceCaps {
			balanceCaps[i] = w
		}
	}
	decor.finish(parent)
	return y
}

// vAlign is what vertical-align asks of an inline-level box.
//
// The set is CSS 2.1 §10.8.1's, less the two that are not a choice of position:
// "inherit" is the cascade's business and a length or percentage is carried as
// a displacement from the baseline rather than as a mode of its own, because
// that is exactly what it is — "vertical-align: 4px" is baseline alignment with
// the baseline moved.
//
// It is read for an ordinary inline box as well as for an atomic one, which it
// was not: a <sup> used to be set at the smaller size the user-agent stylesheet
// gives it and on the same baseline as its surroundings. The two share every
// line of the arithmetic — §10.8.1 aligns "inline-level boxes" and says nothing
// about which kind — and the only difference left is where the two extents come
// from. See itemExtents.
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
	if ascent, descent, ok := l.lineExtents(b, face); ok {
		s.ascent, s.descent = ascent, descent
	}
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

// leading is how far a run of text in an inline box reaches above and below the
// baseline it sits on: CSS 2.1 §10.8.1's leading, half above the font's ascent
// and half below its descent.
//
// It is the same arithmetic the strut is measured by, and deliberately so — the
// strut *is* an inline box, the block's own, and the only thing that makes it
// special is that it is on every line whether or not anything else is. Sharing
// the formula is what keeps a line of plain text exactly as tall as it was: the
// text box inherits the block's font and line-height, so its two numbers are the
// strut's two numbers and the maximum below changes nothing.
//
// The half-leading may be negative — "line-height: 0" asks for a box shorter
// than its own type — and it is passed on rather than clamped, because that is
// precisely how a stylesheet packs lines closer than the font wants.
func (l *layouter) leading(b *Box) (above, below style.Unit) {
	h := l.lineHeight(b)
	above = l.baselineOf(b, h)
	return above, h.Sub(above)
}

// verticalAlignOf reads the vertical-align property of an inline-level box.
func (l *layouter) verticalAlignOf(b *Box) (vAlign, style.Unit) {
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
	// of the containing block. It is the box's *own* line-height and not the
	// block's: §10.8.1 says "a percentage of the 'line-height' value" with no
	// qualification, and an unqualified percentage in CSS is of the element's own
	// value of the property named.
	if length, ok := l.parseLength(b, "vertical-align"); ok {
		if v, ok := length.Resolve(l.lineHeight(b), true); ok {
			return vAlignBaseline, v
		}
	}
	return vAlignBaseline, 0
}

// vAlignState is where §10.8.1's alignment has got to at one point of the walk
// over an inline subtree.
//
// The two halves answer different questions and cannot be one field, which is
// the whole reason this is a struct. align and raise say where the box sits
// against the baseline of *what it is inside*; subtree and lineAlign say which
// aligned subtree it belongs to and where that subtree sits against the *line
// box*. Only "top" and "bottom" ask the second question, and §10.8.1 defines
// them alone in terms of a subtree:
//
//	The aligned subtree of an inline element contains that element and the
//	aligned subtrees of all children inline elements whose computed
//	vertical-align value is not top or bottom.
//
// So a "middle" inside a "top" is still part of the top's subtree, placed within
// it by its own rule, and the whole of it then moves to the top of the line
// together. Getting that wrong is visible rather than academic: a
// "vertical-align: top" span holding text in two sizes had each run's own top
// put at the top of the line, which pulls the smaller one up out of the words it
// belongs with.
type vAlignState struct {
	// align and raise place the box against its parent's baseline. A keyword
	// other than "baseline" is a position rather than a displacement, so it
	// replaces what it is inside instead of adding to it; a length, a
	// percentage, "sub" and "super" accumulate, which is what makes nested
	// superscripts rise twice.
	align vAlign
	raise style.Unit
	// lineAlign is vAlignTop or vAlignBottom when the box is inside an aligned
	// subtree placed against the line box, and vAlignBaseline when it is not.
	// subtree is the box that asked for it, which is what groups the items of
	// one subtree together.
	lineAlign vAlign
	subtree   *Box
}

// vAlignFor combines an inline box's own vertical-align with what the boxes
// around it already asked for.
func (l *layouter) vAlignFor(b *Box, in vAlignState) vAlignState {
	own, lift := l.verticalAlignOf(b)
	switch own {
	case vAlignTop, vAlignBottom:
		// A new aligned subtree, placed against the line box. Nothing outside it
		// carries in: whatever raised the boxes around this one, this one's top
		// or bottom is the line's.
		return vAlignState{lineAlign: own, subtree: b}
	case vAlignBaseline:
		in.raise = in.raise.Add(lift)
		return in
	}
	// "middle", "text-top" or "text-bottom": a position against the parent, so
	// it replaces the accumulated displacement and stays in whatever subtree it
	// was already in.
	//
	// The reset is written twice and that was established rather than assumed.
	// alignedExtents reads raise only in its baseline case, so clearing it here
	// changes no answer today: planted on its own, this line decides nothing and
	// no test in the package moves. Planted *together* with a raise added to one
	// of the keyword cases, the pair is caught — so the rule is guarded, by
	// whichever of the two a later change leaves standing. It is kept because it
	// is what makes the field's documented meaning true, and a stale displacement
	// travelling in a struct that calls it the displacement is the sort of thing
	// the next reader spends an afternoon on.
	in.align, in.raise = own, 0
	return in
}

// aligned reports whether vertical-align moved anything at all, which is false
// for the great majority of the inline content of the great majority of
// documents.
func (v vAlignState) aligned() bool {
	return v.align != vAlignBaseline || v.raise != 0 || v.lineAlign != vAlignBaseline
}

// itemExtents is how far an inline-level item's own box reaches above and below
// its own baseline, before vertical-align has moved it.
//
// The two kinds of item answer it differently and that is the whole of what
// distinguishes them here. An atomic inline's box is its margin box, measured
// when it was laid out. A run of text is §10.8's inline box: "the height of the
// inline box encloses all glyphs and their half-leading on each side and is thus
// exactly 'line-height'", which is the pair the leading gave it.
//
// Anything else on a line — an inset, a float marker, the record of an
// absolutely positioned box — is not an inline-level box at all and takes no
// part in §10.8.1's stacking.
func itemExtents(item inlineItem) (ascent, descent style.Unit, ok bool) {
	if item.atomic != nil {
		return item.ascent, item.descent, true
	}
	if item.leads {
		return item.above, item.below, true
	}
	return 0, 0, false
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
// What is implemented is §10.8.1's alignment of every inline-level box on the
// line — a run of text as much as an atomic inline, since the section names
// neither and the arithmetic is the same for both once each has said how far it
// reaches above and below its own baseline. See itemExtents.
//
// Where it is not exact is which box a keyword alignment is measured against.
// §10.8.1 names the *parent*; this engine has flattened the tree by now and uses
// the block's own strut, so a "text-top" inside an intervening box whose font
// metrics differ from the block's names the block's content area rather than
// that box's.
func stackLine(runs []inlineItem, s strut) lineStack {
	ls := lineStack{strut: s, baseline: s.baseline}
	// What the strut wants below the baseline. It can be *negative*, which is
	// the case that makes this a maximum rather than a floor: "line-height: 0"
	// gives the strut a half-leading of minus half the font's own height, so
	// its descent is below the baseline by a negative amount and an image on
	// the line has to be able to overrule it. Taking the strut's descent
	// unconditionally would make such a line shorter than the picture on it.
	descent := s.height.Sub(s.baseline)

	// First pass: everything aligned against the baseline, which is what
	// decides where the baseline is. What belongs to an aligned subtree is
	// gathered instead, because it is placed against a line box that does not
	// exist yet.
	//
	// Stacking the text runs and not only the atomic inlines was a fault fixed
	// rather than a simplification kept: a <span> set larger than the paragraph
	// around it grew nothing, so its line box stayed the strut's height and its
	// baseline sat where the smaller type wanted it.
	for _, item := range runs {
		a, d, ok := itemExtents(item)
		if !ok {
			continue
		}
		a, d = alignedExtents(item.valign, a, d, s)
		if item.valign.lineAlign != vAlignBaseline {
			ls.gather(item.valign, a, d)
			continue
		}
		if a > ls.baseline {
			ls.baseline = a
		}
		if d > descent {
			descent = d
		}
	}

	// Second pass: the subtrees that align against the line box itself. §10.8.1
	// defines them in terms of a line box whose height they can change, which
	// reads as circular and is not: a subtree taller than the line grows it on
	// the side away from its own edge, and one that fits changes nothing.
	height := ls.baseline.Add(descent)
	for i := range ls.groups {
		g := &ls.groups[i]
		h := g.ascent.Add(g.descent)
		switch g.lineAlign {
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
				ls.baseline = ls.baseline.Add(h.Sub(height))
				height = h
			}
		}
	}
	ls.height = ls.baseline.Add(descent)

	// Where each subtree's own baseline ended up, which is what places the boxes
	// in it. It is a third pass because the height above is not settled until
	// every subtree has had its say, and a "top" subtree that grew the line
	// moves a "bottom" one.
	for i := range ls.groups {
		g := &ls.groups[i]
		if g.lineAlign == vAlignBottom {
			g.baseline = ls.height.Sub(g.descent)
			continue
		}
		g.baseline = g.ascent
	}
	return ls
}

// lineStack is a finished line box: its height and baseline, and where each
// aligned subtree on it ended up.
type lineStack struct {
	strut            strut
	height, baseline style.Unit
	// groups is one entry per aligned subtree placed against the line box. There
	// is one for each "vertical-align: top" or "bottom" box with content on the
	// line, which is none at all in almost every document — so the lookups below
	// scan a slice rather than consult a map, and an ordinary line never
	// allocates.
	groups []alignGroup
}

// alignGroup is one of §10.8.1's aligned subtrees, as it appears on one line.
type alignGroup struct {
	box       *Box
	lineAlign vAlign
	// ascent and descent are the subtree's extents: the highest top and the
	// lowest bottom of the boxes in it, measured from the subtree's own
	// baseline.
	ascent, descent style.Unit
	// baseline is where that baseline sits, from the top of the line box.
	baseline style.Unit
}

// gather adds one box's extents to its subtree's.
func (ls *lineStack) gather(v vAlignState, ascent, descent style.Unit) {
	for i := range ls.groups {
		if ls.groups[i].box != v.subtree {
			continue
		}
		if ascent > ls.groups[i].ascent {
			ls.groups[i].ascent = ascent
		}
		if descent > ls.groups[i].descent {
			ls.groups[i].descent = descent
		}
		return
	}
	ls.groups = append(ls.groups, alignGroup{
		box: v.subtree, lineAlign: v.lineAlign, ascent: ascent, descent: descent,
	})
}

// baselineFor is where the baseline a box is placed against sits, from the top
// of the line box: the line's own, or its aligned subtree's.
func (ls *lineStack) baselineFor(v vAlignState) style.Unit {
	if v.lineAlign == vAlignBaseline {
		return ls.baseline
	}
	for i := range ls.groups {
		if ls.groups[i].box == v.subtree {
			return ls.groups[i].baseline
		}
	}
	return ls.baseline
}

// shift is how far a box's own baseline sits below the line's, once
// vertical-align has placed it.
//
// It is the one number painting needs: a run's glyphs sit on its own baseline,
// and the line has only one of its own.
func (ls *lineStack) shift(v vAlignState, ascent, descent style.Unit) style.Unit {
	a, _ := alignedExtents(v, ascent, descent, ls.strut)
	// a is how far the box reaches above the baseline it is aligned against, so
	// the box's own baseline is that much below that box's top — and ascent is
	// how far its own baseline is below its own top.
	return ls.baselineFor(v).Sub(a).Add(ascent).Sub(ls.baseline)
}

// alignedExtents is how far a box reaching ascent above and descent below its
// own baseline reaches above and below the baseline it is aligned against, once
// its vertical-align has been applied.
func alignedExtents(v vAlignState, ascent, descent style.Unit, s strut) (style.Unit, style.Unit) {
	h := ascent.Add(descent)
	switch v.align {
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
	return ascent.Add(v.raise), descent.Sub(v.raise)
}

// atomicTop is where an atomic inline's margin box goes within its line box.
func (ls *lineStack) atomicTop(item inlineItem) style.Unit {
	return ls.baseline.
		Add(ls.shift(item.valign, item.ascent, item.descent)).
		Sub(item.ascent)
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
//
// The item may always begin a line. CSS Text §5.1 says to treat an atomic inline
// as U+FFFC OBJECT REPLACEMENT CHARACTER for the purpose of line breaking, and
// UAX #14 puts that character in class CB, whose rule LB20 is "break before and
// after unresolved CB" — so a picture is never welded to the word beside it, and
// two pictures side by side are two units rather than one. What comes *after*
// the item is the caller's business, because the rule there has an exception;
// see where the state is set.
func (l *layouter) atomicItem(b *Box, frame inlineFrame) inlineItem {
	item := inlineItem{
		box: b, atomicBox: b, size: b.FontSize,
		breakBefore: true, offset: frame.offset,
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
	item.valign = l.vAlignFor(b, frame.valign)

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
		baseline, ok := lastLineBaseline(frag)
		if b.TableWrapper {
			// §10.8.1 again, and a different sentence of it: "the baseline of an
			// 'inline-table' is the baseline of the first row of the table".
			//
			// The first row and not the last line box, which is the rule for an
			// inline-block and the one an inline-table was sharing. A table of
			// two rows therefore sat on its *second* one: the word beside it
			// lined up with the bottom row and the rest of the table hung above
			// the text, which is inline-table-002a and its neighbours.
			//
			// The wrapper is what arrives here — §17.4 puts one around every
			// table, and for an inline-table it is the atomic inline — so the
			// search starts outside the table and finds the first line box in
			// it, which is in the first cell of the first row.
			baseline, ok = firstBaseline(frag)
		}
		if ok {
			bl := baseline
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

	width, ok := l.explicitWidth(b, frame.containing)
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

// cursorAdvanced reports whether a line took at least one item, or at least one
// byte of one, from where it began.
//
// The cursor is the pair (item, byte into that item), so "forwards" is the
// lexicographic order on the two and not a comparison of either alone: a line
// that ends inside the item it started in has the same index and a greater
// offset, and a line that ends in a later item may have any offset at all,
// including a smaller one — which is why the offset is only consulted when the
// index has not moved.
func cursorAdvanced(wasI, wasByte, i, iByte int) bool {
	return i > wasI || (i == wasI && iByte > wasByte)
}

// maxLineFits bounds how many times one line box may be broken again because
// the band it was broken against turned out to be the wrong one.
//
// Each round is a strictly narrower band or a strictly lower line, so the
// sequence cannot return to a state it has left — but a narrower band can make
// a line taller (a word wraps onto it) as easily as shorter, and a taller line
// meets a different set of floats, so there is no argument that it settles at
// all. Two extra attempts is what the suite's deepest case needs; past that the
// line keeps the break it has, which is a line that is slightly wrong rather
// than a render that does not finish.
//
// A variable so that a test can lower it and watch the bound decide, on the
// model of maxRelayouts.
var maxLineFits = 2

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
			// One neutral character in the paragraph, before the item is built,
			// so that the ordering sees a picture between two words as something
			// that is there rather than as a gap between them.
			para, start, end := frame.bidi.object()
			// An atomic inline: a replaced element, or an inline-block. It is
			// one unbreakable thing with a size of its own, and it is laid out
			// here — before the line it will sit on has even been chosen —
			// because nothing about that line can change its size. That is the
			// whole difference between an atomic inline and an ordinary inline
			// box, whose extent is whatever its words turn out to need and
			// which therefore has to be flattened into the run.
			item := l.atomicItem(child, frame)
			item.bidiPara, item.bidiStart, item.bidiEnd = para, start, end
			out = append(out, item)
			// LB20's other half: a line may also begin after the picture, so
			// "<img/><img/>" is two units and the second wraps when it does not
			// fit. What this is not is an opportunity for a *space* to take —
			// LB7 says not to break before one and is the earlier rule, so the
			// space stays with the picture and the break falls after the space
			// where LB18 puts it. itemsFor is where that exception lives, since
			// it is the only place that knows a piece is a space.
			//
			// The distinction is visible rather than theoretical: a float after
			// "<img/> " goes on the line after the picture, and offering the
			// opportunity to the space put it one line further down again.
			state.breakOpportunity = true
			state.afterCollapsibleSpace = false
			continue
		}
		if child.IsText() {
			var items []inlineItem
			items, state = l.itemsFor(child, state, frame)
			out = append(out, items...)
			continue
		}
		if child.Element != nil && strings.EqualFold(child.Element.Name, "br") {
			// A line break the author wrote. It is not a break *opportunity* —
			// it ends the line wherever it falls, even mid-word and even on a
			// line with room to spare.
			out = append(out, inlineItem{box: child, forced: true})
			// It ends a bidi paragraph too: CSS makes a forced break a paragraph
			// separator, so the direction of what follows is decided afresh
			// rather than by the first strong character of the block.
			frame.bidi.breakParagraph()
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
			// §10.8.1's vertical-align, composed with what the boxes outside
			// this one already asked for. It is recorded against the box as well
			// as carried down, because the box's own background and border are
			// moved by it and they are made from the box rather than from the
			// items — see inlineDecor.finish.
			inner.valign = l.vAlignFor(child, frame.valign)
			if inner.valign.aligned() && !frame.measuring {
				l.inlineAligns[child] = inner.valign
			}
			if inner.offset != (Point{}) && !frame.measuring {
				// The box's own displacement, which its background and border are
				// drawn at. It is recorded here because this is the only walk that
				// has it: the items carry the offset of whatever box they came
				// from, which for a nested inline is not this one's.
				l.inlineOffsets[child] = inner.offset
			}
			// The formatting codes unicode-bidi stands for, around the box's
			// contents. This is the one walk that sees where an inline box begins
			// and ends, and an embedding or an isolate is exactly a pair of
			// characters at those two points.
			open, closing := bidiControls(child)
			frame.bidi.enter(open)
			lead, trail, any := l.insetItems(child, frame.containing)
			if any {
				// A break opportunity carried in belongs to the box's leading
				// edge and not to its first word: a line may end before
				// "<span style='margin-left: 99px'>word</span>", and it may not
				// end between that margin and the word it pushes along.
				//
				// Whether a collapsible space may still collapse into the one
				// before it is a different question and is left alone. §4.1.1's
				// fourth rule collapses across an inline boundary, and a margin
				// on the boundary does not make two spaces into one space each.
				lead.breakBefore = state.breakOpportunity
				state.breakOpportunity = false
				out = append(out, lead)
			}
			out, state = l.collectInline(child, out, state, inner)
			if any {
				// The trailing edge takes no opportunity of its own: a line
				// cannot end between a box's last word and its own closing
				// margin, so whatever was carried in passes through to whatever
				// comes after the box.
				out = append(out, trail)
			}
			frame.bidi.leave(open, closing)
		}
	}
	return out, state
}

// insetItems is the room a non-replaced inline box's own margin, border and
// padding take on the line.
//
// # Why an inline box needs this at all
//
// §8.3, §8.4 and §8.5 each say the same thing about the horizontal axis and the
// opposite about the vertical: margin-left, border-left-width and padding-left
// apply to a non-replaced inline box and push its content along, while
// margin-top and margin-bottom do not apply at all and a vertical padding or
// border bleeds over the lines above and below without changing the height of
// any of them.
//
// This engine had none of it. A "<span style='margin-left: 96px'>" set its text
// where the span before it ended, and the measurement that found it is worth
// naming because the tests were not looking for this: css/CSS2/text's
// letter-spacing and word-spacing families check their property by drawing the
// same picture twice, once with the property and once with an equivalent margin
// on an inline box. The engine got letter-spacing and word-spacing right and the
// margin wrong, so 34 tests failed and read as spacing failures.
//
// # What is emitted, and what a line does with it
//
// Two items with no text: one before the box's content and one after, carrying
// the summed inset of that side. They are ordinary items in every other respect,
// which is what makes the rest of §9.4.2 come out on its own:
//
//   - The inset is added once at the box's real start and once at its real end,
//     not once per line, which is what §8.6's "box-decoration-break: slice"
//     asks for on a box broken across lines.
//   - A line box holding an inline box with a non-zero horizontal margin,
//     border or padding is not one of §9.4.2's zero-height line boxes, and it
//     is not one here either — there is an item on it, so the line exists and
//     takes the strut's height. A box whose insets are all zero emits nothing
//     and so still produces no line, which is what keeps an empty "<span></span>"
//     from putting a blank line into every document.
//   - The intrinsic widths pick it up without being told: widthsOf's default
//     case adds an unbreakable item to both the running word and the line.
//
// The pair is emitted together or not at all, and that is what the third result
// says. A box with a margin on one side only used to emit the one item that had
// a width in it; both are needed now, because §8.6 can move the width from one
// to the other and there has to be something at the far end to move it to. The
// item that ends up empty draws nothing and takes no room, so nothing but
// insetSides can tell the difference.
//
// # §8.6's bidi box model
//
// The left inset is emitted before the box's content and the right after it, in
// *logical* order. On a line the reordering does not touch — every line of a
// left-to-right document — logical order is physical order and there is nothing
// more to do. On a line it does touch, the item emitted first is drawn last.
//
// §8.6 is physical on both sides of its rule: with either direction, "the
// leftmost generated box" carries the left margin, border and padding and "the
// rightmost" carries the right ones, and what the direction decides is only
// *which line box* of a box broken across several. So the fix is not to swap on
// the direction property. Swapping on the declared direction was tried and
// measured nine clean passes worse, because "direction: rtl" with the initial
// "unicode-bidi: normal" changes the box model and does not reorder anything —
// the swap assumed an order that had not changed.
//
// insetSides does it on the *resolved embedding level* of the box's content
// instead, which is the thing that actually says whether the content was
// reversed, and which is known only after resolveBidi has run.
//
// What is painted in that room is inlinepaint.go's, and the two have to agree
// about §8.6 or the ink and the space it sits in would come apart: this decides
// which side's inset is *reserved* on which piece, and that decides which side's
// border is *drawn* on which fragment. Both read the same two flags, and a test
// that plants a defect in either of them fails on the other's assertions.
//
// An outline is still not drawn, on an inline box or on any other — nothing in
// this engine paints one.
// splitInsetSides turns "does the box begin here" into "which physical side",
// which is the same question only in a left-to-right containing block.
//
// §8.6's slice model gives a box broken by a block inside it its own inset on
// the pieces at its two ends and nothing on the joins, and which *physical*
// side that is depends on which way the containing block runs. In a
// right-to-left one the piece the box begins on is the rightmost, so what
// belongs to it is the right inset: "<span style='padding-right: 10px'>" holding
// nothing but a block reserves its ten pixels *before* the block under rtl and
// after it under ltr. block-in-inline-empty-002 and -004 are that pair, written
// once each way with their references in the two orders.
//
// The containing block's direction and not the box's own, for the reason
// resolveWidth gives where it settles the over-constrained case: a box that
// declares "direction: rtl" is saying which way its contents run, not which
// side of its parent it hangs from.
//
// That choice is a reading rather than a measurement, and it is worth saying
// which. Every document in the suite sets the direction on an ancestor and lets
// the span inherit it, so the two answers agree on all of them and no test can
// tell them apart — planting the box's own direction here changes nothing. It
// follows resolveWidth because being wrong the same way twice is at least
// consistent, and because the alternative reads the property as saying
// something about the box's own outside, which it does not.
func splitInsetSides(b *Box) (noLeft, noRight bool) {
	noLeft, noRight = b.noLeadInset, b.noTrailInset
	if beginsAtRight(b) {
		noLeft, noRight = noRight, noLeft
	}
	return noLeft, noRight
}

// beginsAtRight reports whether the physical side an inline box *begins* on is
// its right, which is what its own "direction: rtl" makes it.
//
// §8.6 states the rule twice, once per direction, and the two halves differ only
// in which physical side goes with which end of the box:
//
//	When the element's 'direction' property is 'ltr', the leftmost generated box
//	of the first line box in which the element appears has the left margin, left
//	border and left padding [...] When the element's 'direction' property is
//	'rtl', the rightmost generated box of the first line box [...] has the right
//	padding, right border and right margin.
//
// "The element's" — its own, not its containing block's, and the suite settles
// it rather than leaving it a reading. CSS2/box has the four combinations as
// four documents against four references: rtl-span-only is a "direction: rtl"
// span inside a left-to-right block and its reference gives the first line the
// *right* inset, while ltr-span-only is the mirror and gives the first line the
// left one. The containing block decides where the content sits; the element
// decides which of its own edges begins it.
//
// An earlier note here said no test could tell the two apart and followed
// resolveWidth on that basis. That was wrong about the suite: those two
// documents exist for exactly this question.
func beginsAtRight(b *Box) bool {
	return isRTL(b)
}

func (l *layouter) insetItems(b *Box, containing style.Unit) (lead, trail inlineItem, any bool) {
	edges := l.edges(b, "margin", containing).
		Add(l.borderWidths(b)).
		Add(l.paddingOf(b, containing))

	left, right := edges.Left, edges.Right
	noLeft, noRight := splitInsetSides(b)
	if noLeft {
		left = 0
	}
	if noRight {
		right = 0
	}
	if left == 0 && right == 0 {
		return inlineItem{}, inlineItem{}, false
	}
	item := inlineItem{box: b, inset: true}
	lead, trail = item, item
	lead.insetLead = true
	lead.width, trail.width = left, right
	return lead, trail, true
}

// itemsFor cuts one text box into items at its break opportunities and measures
// each, applying the half of §4.1.1 that could not be done per node.
func (l *layouter) itemsFor(b *Box, in inlineState, frame inlineFrame) ([]inlineItem, inlineState) {
	offset := frame.offset
	face, ok := l.faceForText(b)
	if !ok {
		return nil, in
	}
	l.checkScript(b)
	l.checkGlyphs(b, face)

	size := b.FontSize
	ws := whiteSpaceFor(b.Style)
	// Both are read once per text box rather than once per piece: they are
	// inherited properties, so every piece of one box has the same answer, and
	// the decorations are memoized across the whole tree besides.
	decorations := l.decorationsFor(b)
	spacing := l.spacingFor(b)
	// §10.8.1's leading, for the same reason and read the same number of times.
	// It is the inline box's own line-height and font rather than the block's,
	// which is the whole of what makes a <span> set larger than the paragraph
	// around it grow the line it is on.
	above, below := l.leading(b)
	ow := overflowWrapOf(b.Style)
	wb, unhandled := wordBreakOf(b.Style["word-break"])
	if unhandled != "" {
		l.reportWordBreak(b, unhandled)
	}
	lb, unhandledLine := lineBreakOf(b.Style["line-break"])
	if unhandledLine != "" {
		l.reportLineBreak(b, unhandledLine)
	}
	pieces, endedAtBreak := splitAtBreaks(b.Text, ws, wb, lb)
	if len(pieces) == 0 {
		// A box that produced nothing passes an opportunity through rather than
		// swallowing it — and it may have created one of its own, which is what
		// a <span> holding a single zero-width space is. Either source counts.
		in.breakOpportunity = in.breakOpportunity || endedAtBreak
		return nil, in
	}

	var tabStop, tabFloor style.Unit
	for _, p := range pieces {
		if p.tab {
			tabStop = l.tabStop(b, face)
			// §4.1.2's threshold is half of "ch", which is the advance of "0" in
			// the box's own font — the same measurement the "ch" unit is, taken
			// here rather than through lengthOf because there is no declaration
			// to parse. A face with no digit gives nothing to halve, and then the
			// threshold is absent rather than zero: absent means every shift is
			// long enough, which is the behaviour of every engine that does not
			// implement the rule and is the one that cannot move a tab stop by
			// mistake.
			tabFloor = l.measure(face, "0", size).Div(2)
			break
		}
	}

	out := make([]inlineItem, 0, len(pieces))
	state := in
	for _, p := range pieces {
		if p.segment {
			// A segment break that survived Phase I is a break the author
			// wrote, and it ends the line as firmly as a <br> does — and ends a
			// bidi paragraph with it, for the same reason.
			out = append(out, inlineItem{box: b, face: face, size: size, forced: true,
				offset: offset, leads: true, above: above, below: below,
				valign: frame.valign})
			frame.bidi.breakParagraph()
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

		para, start, end := frame.bidi.add(p.text)
		item := inlineItem{
			bidiPara: para, bidiStart: start, bidiEnd: end,
			text: p.text, box: b, face: face, size: size,
			leads: true, above: above, below: below,
			// §10.8.1's vertical-align, which a text box cannot be asked for
			// itself: the property is not inherited, so the anonymous box holding
			// a <span>'s words carries the initial value however the span was
			// aligned. The frame brought the answer down from the boxes the walk
			// is inside.
			valign: frame.valign,
			// An opportunity carried in from the piece before is offered to
			// anything but a space. UAX #14's LB7 — "do not break before spaces"
			// — is an earlier rule than every rule that creates one, so a space
			// belongs to the unit in front of it and the break falls after it.
			// The piece's own opportunity still stands, which is what puts the
			// break after a preserved space rather than losing it.
			breakBefore: p.breakBefore || (state.breakOpportunity && !p.space),
			space:       p.space, collapsible: p.collapsible,
			trimAtEnd: p.trimAtEnd,
			tab:       p.tab, tabStop: tabStop, tabFloor: tabFloor,
			// §4.1.2's fourth rule, which is three answers and not one.
			//
			// What reaches it is whatever rule 3 left: under a collapsing value
			// that is the other space separators and the preserved tabs, and
			// under a preserving one it is the spaces as well. The rule then
			// names the values one at a time.
			//
			//   - normal, nowrap and pre-line — which is exactly ws.collapse —
			//     hang the sequence *unconditionally*. It never takes room.
			//   - pre-wrap hangs it unconditionally too, "unless the sequence is
			//     followed by a forced line break, in which case it must
			//     conditionally hang the sequence instead". A conditional hang
			//     takes room and gives it up only where the room is not there.
			//   - break-spaces is named to say the sequence does *not* hang: the
			//     spaces are data and take room even when they overflow.
			//   - pre is not in the list at all, so nothing hangs under it. That
			//     is not an omission to be read past: a line under pre ends only
			//     where the author ended it, so its trailing spaces are before a
			//     forced break or the end of the block, and the rule's whole
			//     subject is what to do at a wrap.
			//
			// The distinction is invisible on the page and decides two intrinsic
			// widths, which is where hangsHard is read. See widthsOf.
			hangs:     p.space && !p.collapsible && !ws.breakSpaces && (ws.collapse || ws.wrap),
			hangsHard: p.space && !p.collapsible && ws.collapse,
			noWrap:    !ws.wrap, offset: offset,
			breakWord:   ow.breakWord,
			anywhere:    ow.anywhere,
			decorations: decorations, spacing: spacing,
		}
		if !p.tab {
			// A tab is measured against a tab stop when it lands, so there is
			// nothing to measure here and the face's own advance for U+0009 —
			// whatever a face happens to give a character it has no glyph for —
			// would be the wrong number to carry.
			item.width = l.measureSpaced(face, p.text, size, spacing)
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
//
// floor is §4.1.2's threshold: "if this distance is less than 0.5ch, then the
// subsequent tab stop is used instead". It is a rule about the *shift* and not
// about where the tab lands, which is why it is applied to the remainder rather
// than to the position: a tab already sitting a hair before a stop is a tab that
// would otherwise be invisible, and the paragraph it is in would lose the column
// it was written to make. Without it a tab at 7.9ch of an 8ch stop advances a
// tenth of a character and the text after it is a tenth of a character from the
// text before it — which looks like no tab at all rather than like a wrong one,
// and is the shape of silent difference §6 is about.
//
// A floor of zero is *absent* rather than "no distance is short enough": the
// comparison is strict, so a zero floor can never fire, and a caller that could
// not measure a "0" passes zero to say exactly that. The two readings agree
// here, which is why there is one parameter and not two.
func tabAdvance(x, stop, floor style.Unit) style.Unit {
	if stop <= 0 {
		return 0
	}
	if x < 0 {
		x = 0
	}
	d := stop.Sub(x % stop)
	if d < floor {
		d = d.Add(stop)
	}
	return d
}

// measure returns the advance width of a string in a face, memoized.
//
// It is the face's own advance and nothing else, which is what the three callers
// that use it want: a tab stop is a multiple of the space advance, "ch" is the
// advance of a zero, and a list marker is set without the text's spacing. Text
// that is laid out on a line goes through measureSpaced instead.
func (l *layouter) measure(face *fonts.Face, text string, size style.Unit) style.Unit {
	return l.measureSpaced(face, text, size, textSpacing{})
}

// measureSpaced is the advance of a run as it will be set, with letter-spacing
// and word-spacing in it.
//
// Measuring is the inner loop of line breaking, and the same words recur
// constantly in a document — every "the" in a page measures the same. The key
// includes the face and the size because both scale the answer, and the spacing
// because it changes it: two boxes at the same size in the same face with
// different letter-spacing must not share an entry. Leaving it out of the key is
// the same memoization bug lengthKey.zeroAdvance records for the "ch" unit, and
// it produces a wrong page only in a document that uses two values.
func (l *layouter) measureSpaced(face *fonts.Face, text string, size style.Unit,
	sp textSpacing) style.Unit {

	if text == "" {
		return 0
	}
	key := measureKey{face: face, text: text, size: size, spacing: sp}
	if got, ok := l.measured[key]; ok {
		return got
	}
	// Measure returns the advance in the units the size was given in, so a size
	// in CSS pixels gives an advance in CSS pixels.
	w, _ := style.FromPx(face.Measure(text, size.Px()))
	w = w.Add(spacingAdvance(text, sp))
	l.measured[key] = w
	return w
}

type measureKey struct {
	face    *fonts.Face
	text    string
	size    style.Unit
	spacing textSpacing
}

// piece is a run of text between two break opportunities, together with what
// §4.1.2 has to know about it once it lands on a line.
type piece struct {
	text        string
	breakBefore bool
	// space marks white space of any kind, collapsible marks the subset of it
	// Phase I folds together, trimAtEnd the subset a line edge removes, and tab
	// and segment the two preserved characters that are not simply text of their
	// own width.
	space       bool
	collapsible bool
	trimAtEnd   bool
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
func splitAtBreaks(text string, ws whiteSpace, wb wordBreak, lb lineBreak) ([]piece, bool) {
	var out []piece
	var cur strings.Builder
	breakNext := false

	// Grapheme cluster boundaries, walked in lockstep with the scan.
	//
	// It runs for every value of word-break and not only for break-all, because
	// the rule it enforces is not break-all's: CSS Text §2 puts a soft wrap
	// opportunity *between* typographic character units, so no opportunity this
	// function produces may fall inside a cluster. The ideograph rule below used
	// to produce one — a Hangul syllable followed by its own trailing jamo was
	// cut in two, which put half a syllable at the end of a line.
	//
	// A Scanner rather than a list of offsets: the scan is already linear, and a
	// list would allocate one int per character for Latin text, where every
	// character is its own cluster and nothing is learned.
	var clusters grapheme.Scanner
	// deferBreak says the previous character allows a line to end after it, and
	// the opportunity has not been taken yet.
	//
	// It is deferred because whether the cut is legal depends on the character
	// that *follows*: only that one says whether the cluster ended. Taking the
	// opportunity where it is offered is what cut the syllable open.
	deferBreak := false

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
		start := i
		i += size

		atBoundary := clusters.Boundary(r)

		// The opportunity that may fall before this character: one deferred from
		// the character before, or — under break-all, CSS Text §5.2 — one at
		// every typographic character unit boundary inside a word.
		//
		// White space is excluded from break-all's half, and that exclusion is
		// UAX #14's LB7 rather than a simplification: a line may not end between
		// a word and the space after it, so the space stays on its word's line.
		// Without it, "X XX X" in four characters of room breaks after the
		// fourth — which fits more text and is the wrong answer. The other
		// separators are excluded with it, which errs towards fewer
		// opportunities and so overflows a line rather than breaking it in a
		// place the algorithm did not sanction.
		// line-break: anywhere is the third source and the widest: §5.3 puts an
		// opportunity around *every* typographic character unit, so it needs
		// neither break-all's exclusion of white space nor anything deferred. It
		// is what makes "X XX X" in four characters of room break after the
		// fourth — the answer break-all must not give, and the one the suite's
		// break-spaces-before-first-char-007 asks for by name.
		if (deferBreak || (wb.breakAll && !startsSpacePiece(r, ws)) || lb.anywhere) &&
			atBoundary && cur.Len() > 0 {
			flush()
			breakNext = true
		}
		deferBreak = false

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

		case isOtherSpaceSeparator(r):
			// §4.1's "other space separators". Phase I never saw them — it is
			// defined over U+0020, U+0009 and the segment breaks and nothing else
			// — so what arrives here is exactly what the author wrote, and it is
			// §4.1.2's fourth rule that has something to say about it: a run of
			// them at the end of a line hangs just as a run of preserved spaces
			// does, whatever the white-space value, because the rule is written
			// over "white space, other space separators, and/or preserved tabs".
			//
			// One character each rather than a run, because a run of them is not
			// one thing: U+3000 offers an opportunity after it and U+202F does
			// not, so two adjacent separators can differ in the only property
			// that would justify gathering them.
			//
			// The ogham space mark is the exception §4.1.2's *third* rule carves
			// out: where white space collapses it is removed at the end of a line
			// rather than hung, which is trimAtEnd. It is still not collapsible —
			// a run of ogham space marks is a run of stemlines and folding them
			// into one would shorten the line.
			flush()
			emit(piece{
				text: text[start:i], space: true,
				trimAtEnd: r == 0x1680 && ws.collapse,
			})
			breakNext = ws.breakSpaces || separatorBreaksAfter(r)

		case r == ' ' || r == '\t':
			flush()
			if ws.collapse {
				// Phase I already reduced the run to a single space and turned
				// any tab into one, so there is nothing left to gather.
				emit(piece{text: " ", space: true, collapsible: true, trimAtEnd: true})
				breakNext = true
				break
			}
			// Preserved. Under pre and pre-wrap the run hangs or wraps as a
			// unit, so it is one piece; under break-spaces a line may end after
			// any single space, so each is its own.
			// Under pre and pre-wrap the run hangs or wraps as a unit, so it is
			// gathered — unless line-break: anywhere says a line may end between
			// any two of them, which is a run that is no longer one thing.
			if !ws.breakSpaces && !lb.anywhere {
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
			//
			// The opportunity after it is deferred rather than taken, because a
			// Hangul syllable can be followed by a trailing jamo that belongs to
			// it and by a combining mark that belongs to it, and neither is a
			// place a line may end. The next character's boundary decides.
			flush()
			cur.WriteRune(r)
			deferBreak = true

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
	// opportunity, which matters when what follows is in another box. A deferred
	// one counts, and has to: text ending in an ideograph offers a break to
	// whatever box comes next, and the character that would have confirmed it is
	// in that box rather than this one.
	return out, breakNext || deferBreak
}

// startsSpacePiece reports whether a character is one splitAtBreaks gives a
// white-space piece of its own.
//
// It is the set break-all's opportunities are withheld before — see the call
// site — and it is written as a predicate rather than inlined so the two places
// cannot drift apart: a character that grew a branch below without being added
// here would silently gain a break opportunity before it.
func startsSpacePiece(r rune, ws whiteSpace) bool {
	switch {
	case r == '\n' || r == '\r':
		return true
	case r == '\t':
		return true
	case r == ' ':
		return true
	case r == '​':
		return true
	}
	return isOtherSpaceSeparator(r)
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

// blockEllipsis is what a clamped block puts at the end of its last line.
//
// CSS Overflow 4's "block-ellipsis: auto" is "a UA-defined value", and the
// horizontal ellipsis is the one every engine uses and the one the suite's
// references write.
const blockEllipsis = "\u2026"

// lineClamp is how many lines CSS Overflow 4 lets this block show, or zero for
// no limit.
//
// Two spellings, and the second is not a synonym for the first. The unprefixed
// property is read on its own; the prefixed one is only the clamp when the two
// declarations that made it work in the engine it came from are there as well —
// §"Legacy" gives the trio as "display: -webkit-box", "-webkit-box-orient:
// vertical" and "-webkit-line-clamp". A document that writes only
// "-webkit-line-clamp" on an ordinary block is not asking for anything, and
// browsers give it nothing.
//
// A count of zero or less clamps nothing rather than clamping everything away:
// the value is an integer with no stated floor, and a block with no lines at all
// is not something a stylesheet can plausibly be asking for.
func (l *layouter) lineClamp(b *Box) int {
	if n, ok := positiveInteger(b.Style["line-clamp"]); ok {
		return n
	}
	if !strings.EqualFold(strings.TrimSpace(b.Style["display"]), "-webkit-box") ||
		!strings.EqualFold(strings.TrimSpace(b.Style["-webkit-box-orient"]), "vertical") {
		return 0
	}
	if n, ok := positiveInteger(b.Style["-webkit-line-clamp"]); ok {
		return n
	}
	return 0
}

// positiveInteger reads a whole number above zero, which is the only form of
// either clamp property this engine acts on.
func positiveInteger(value string) (int, bool) {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
		if n > maxClampLines {
			return maxClampLines, true
		}
	}
	return n, n > 0
}

// maxClampLines bounds the count an untrusted stylesheet can state.
//
// The number is only ever compared against a line count, so a large one clamps
// nothing — but it is parsed from a document and multiplied by nothing, and a
// bound costs one line. It is far above any block a reader would call clamped.
const maxClampLines = 1 << 20

// maxBalanceLines bounds how many lines this engine will balance.
//
// Balancing costs a binary search over the width, and each probe breaks the
// whole paragraph again — so a page of running prose set to "text-wrap: balance"
// would be laid out sixteen times over. CSS Text §5.1 allows the bound in as
// many words ("UAs may disable balancing when the number of lines exceeds some
// threshold"), and balancing is a display effect: it is what a headline of two
// or three lines is for, and nobody can see it in a paragraph of thirty.
//
// It is a variable so that a test can lower it far enough to watch it decide
// something. A bound that has only ever been observed not to trip is one nobody
// knows works.
var maxBalanceLines = 6

// balanceWidth is CSS Text §5.1's "text-wrap-style: balance", computed as the
// narrowest width that still fits the text in the same number of lines.
//
//	balance: Line breaks are chosen to balance the remaining (empty) space in
//	each line box, if a better balance than block-progression-first filling is
//	possible.
//
// The specification gives no algorithm, and the one below is the one every
// implementation uses, because its two statements turn out to be the same: a
// greedy break at the narrowest width that still makes N lines is the greedy
// break whose longest line is as short as it can be, which is exactly "the
// remaining space is as even as it can be made". "The quickest brown fox jumped
// over the lazy dog" in thirty-five characters greedily fills the first line to
// thirty-three and leaves twelve on the second; the narrowest width that still
// takes two lines is twenty-four, and there it reads "The quickest brown fox /
// jumped over the lazy dog", which is what the suite's text-wrap-balance-003
// draws with an explicit <br>.
//
// The search needs the count to fall as the width grows, and it does: a wider
// line takes at least what a narrower one took.
//
// Returns MaxUnit — no cap at all — when the box does not balance, when it is
// one line already, or when it is longer than this engine will balance.
func (l *layouter) balanceWidth(items []inlineItem, width, indent style.Unit) style.Unit {
	full := l.countLines(items, width, indent, maxBalanceLines+1)
	if full < 2 || full > maxBalanceLines {
		return style.MaxUnit
	}
	// One unit is the finest distinction the geometry can hold, so the search
	// stops when the bracket is that wide and there is nothing left to choose
	// between.
	lo, hi := style.Unit(1), width
	for hi.Sub(lo) > 1 {
		mid := lo.Add(hi.Sub(lo).Div(2))
		if l.countLines(items, mid, indent, full+1) <= full {
			hi = mid
			continue
		}
		lo = mid
	}
	return hi
}

// balanceCaps is the balanced width for each line, by the item it starts at.
//
// One number would not do, because §5.1 balances "each group of lines separated
// by a forced line break" on its own: a headline of two lines with a <br> in the
// middle is two groups of one, and balancing the pair together would move the
// break the author wrote. text-wrap-balance-004 is that exactly — a <section>
// with a <br> in it, checked against two <div>s balanced separately.
//
// The text indent belongs to the first group alone, for the same reason it
// belongs to the first line: §16.1 gives it to the first formatted line of the
// element, and the line after a <br> is not one.
//
// A nil result means no cap anywhere, which is what a box that does not balance
// gets and is what capAt reads as MaxUnit.
func (l *layouter) balanceCaps(b *Box, items []inlineItem, width, indent style.Unit) []style.Unit {
	if !strings.EqualFold(strings.TrimSpace(b.Style["text-wrap-style"]), "balance") {
		return nil
	}
	caps := make([]style.Unit, len(items))
	for i := range caps {
		caps[i] = style.MaxUnit
	}
	start := 0
	for i := 0; i <= len(items); i++ {
		if i < len(items) && !items[i].forced {
			continue
		}
		ind := style.Unit(0)
		if start == 0 {
			ind = indent
		}
		w := l.balanceWidth(items[start:i], width, ind)
		for j := start; j < i; j++ {
			caps[j] = w
		}
		start = i + 1
	}
	return caps
}

// capAt is the balanced width for a line beginning at an item.
func capAt(caps []style.Unit, i int) style.Unit {
	if i < 0 || i >= len(caps) {
		return style.MaxUnit
	}
	return caps[i]
}

// balanceClampedWidth is §5.1's balancing where CSS Overflow 4's clamp has
// already cut the block off: the narrowest width that still shows everything the
// full width showed.
//
// The suite states the rule as a picture rather than as prose, and both of its
// halves are in the diagrams. line-clamp-002 balances "1 2 3 4 5 6 7 8 9 0 1 2"
// into two lines of thirteen characters where the second carries a four-
// character ellipsis — so the ellipsis is part of what is being evened out, not
// something added afterwards. And line-clamp-003 shows *more* text balanced than
// unbalanced: three lines of "1 2 3", "4 5 6", "7 8 9…" against an unbalanced
// "1 2 3 4 5", "6 7 8 9", "…", because the narrower measure lets the last line
// hold something beside the mark.
//
// So the search is over how far into the content the clamped layout reaches,
// and the answer is the narrowest width that reaches as far as the full width
// did. Reaching *further* is fine and is what the third line above does.
func (l *layouter) balanceClampedWidth(items []inlineItem,
	width, indent, ellipsis style.Unit, maxLines int) style.Unit {

	wantI, wantByte := l.clampedReach(items, width, indent, ellipsis, maxLines)
	lo, hi := style.Unit(1), width
	for hi.Sub(lo) > 1 {
		mid := lo.Add(hi.Sub(lo).Div(2))
		i, iByte := l.clampedReach(items, mid, indent, ellipsis, maxLines)
		if i > wantI || (i == wantI && iByte >= wantByte) {
			hi = mid
			continue
		}
		lo = mid
	}
	return hi
}

// clampedReach is how far into the items a clamped block gets: the cursor after
// the last line it shows.
//
// The last line is the one the ellipsis sits on, so it is broken in a narrower
// measure than the rest — and it is the one line that does not overflow. A word
// too long for its line is set anyway everywhere else in this engine, because
// the alternative is losing it; here the alternative is exactly what the clamp
// asks for, since what does not fit beside the mark is what the mark stands for.
// "unbreakable" against nine characters less an ellipsis shows nothing at all,
// which is what the suite's line-clamp-003 draws.
func (l *layouter) clampedReach(items []inlineItem,
	width, indent, ellipsis style.Unit, maxLines int) (int, int) {

	i, iByte := 0, 0
	for n := 0; n < maxLines; n++ {
		for iByte == 0 && i < len(items) && items[i].float != nil {
			i++
		}
		if i >= len(items) {
			break
		}
		room := width
		if n == 0 {
			room = room.Sub(indent)
		}
		last := n == maxLines-1
		if last {
			room = room.Sub(ellipsis)
		}
		wasI, wasByte := i, iByte
		runs, next, nextByte, _, _ := l.breakOneLine(items, i, iByte, room, 0)
		if last {
			var used style.Unit
			for _, r := range runs {
				used = used.Add(r.width)
			}
			if used > room {
				// The breaker only overflows when a single unit left it no
				// choice, so a line wider than its room is one unit that did not
				// fit — and on the clamped line that unit is not shown.
				break
			}
		}
		i, iByte = next, nextByte
		if !cursorAdvanced(wasI, wasByte, i, iByte) {
			break
		}
	}
	return i, iByte
}

// balanceWidthInBands is §5.1's balancing where a float has shortened some of
// the lines: the same search, over the widths the lines actually had.
//
// The bands come from laying the box out once, which is the only way to know
// them — a float inside the box is placed as the lines are built, and what
// shortens a line is decided by the lines above it. They are the *greedy*
// layout's bands and the balanced one may differ slightly, since a line that
// changes height meets a different set of floats; the difference is a line's
// worth of a float's edge, and browsers make the same approximation.
func (l *layouter) balanceWidthInBands(items []inlineItem, bands []style.Unit,
	width, indent style.Unit) style.Unit {

	full := l.countLinesInBands(items, bands, width, indent, maxBalanceLines+1)
	if full < 2 || full > maxBalanceLines {
		return style.MaxUnit
	}
	lo, hi := style.Unit(1), width
	for hi.Sub(lo) > 1 {
		mid := lo.Add(hi.Sub(lo).Div(2))
		if l.countLinesInBands(items, bands, mid, indent, full+1) <= full {
			hi = mid
			continue
		}
		lo = mid
	}
	return hi
}

// countLinesInBands is countLines with a width per line rather than one for all
// of them.
//
// A line's room is the narrower of the band it is in and the width being
// probed — the cap chooses break points inside the room the floats leave, it
// does not widen a line past them.
func (l *layouter) countLinesInBands(items []inlineItem, bands []style.Unit,
	cap, indent style.Unit, limit int) int {

	n := 0
	iByte := 0
	for i := 0; i < len(items); {
		for iByte == 0 && i < len(items) && items[i].float != nil {
			i++
		}
		if i >= len(items) {
			break
		}
		room := style.Min(bandAt(bands, n), cap)
		if n == 0 {
			room = room.Sub(indent)
		}
		wasI, wasByte := i, iByte
		runs, next, nextByte, _, forced := l.breakOneLine(items, i, iByte, room, 0)
		if len(runs) > 0 || forced {
			n++
		}
		if n >= limit {
			return n
		}
		i, iByte = next, nextByte
		if !cursorAdvanced(wasI, wasByte, i, iByte) {
			break
		}
	}
	return n
}

// bandAt is the width of the nth line, or of the last one recorded once the
// probe runs past what was measured.
//
// A probe that makes more lines than the layout did is asking about lines that
// were never laid out, and the band below the last float is the best answer
// there is: it is what every line after it had.
func bandAt(bands []style.Unit, n int) style.Unit {
	if len(bands) == 0 {
		return style.MaxUnit
	}
	if n >= len(bands) {
		return bands[len(bands)-1]
	}
	return bands[n]
}

// countLines is how many lines the greedy breaker makes of these items in a
// given width.
//
// It stops counting at limit, because the caller only ever needs to know whether
// the count is above a number: a two-character probe width over a page of text
// would otherwise break every word in it to answer a question already settled.
//
// Floats are not consulted. Balancing chooses break points within the room the
// content has, and what that room is on each line is decided by the real loop
// against the real bands; a count that placed floats would have to place them
// once per probe and roll them back once per probe.
func (l *layouter) countLines(items []inlineItem, width, indent style.Unit, limit int) int {
	n := 0
	iByte := 0
	for i := 0; i < len(items); {
		for iByte == 0 && i < len(items) && items[i].float != nil {
			i++
		}
		if i >= len(items) {
			break
		}
		room := width
		if n == 0 {
			room = width.Sub(indent)
		}
		wasI, wasByte := i, iByte
		runs, next, nextByte, _, forced := l.breakOneLine(items, i, iByte, room, 0)
		if len(runs) > 0 || forced {
			n++
		}
		if n >= limit {
			return n
		}
		i, iByte = next, nextByte
		if !cursorAdvanced(wasI, wasByte, i, iByte) {
			// The same forward-progress guard the real loop carries. A probe
			// width of one unit is narrower than any glyph, and a breaker that
			// cannot fit even one would otherwise be asked for ever.
			break
		}
	}
	return n
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
func (l *layouter) breakOneLine(items []inlineItem, from, fromByte int, width, lineX style.Unit) (
	line []inlineItem, next, nextByte int, outOfFlow []midLineBox, forced bool) {

	var used style.Unit
	// content says the line holds something a reader would see. It is not the
	// same as the line being non-empty: an inline box's own margin, border and
	// padding take room without being content, and §4.1.2's rule about the
	// *beginning of a line* is about the text. "<span style='margin-left: 5px'>
	// x</span>" sets one space in from five pixels, not from nine.
	content := false
	// A break opportunity that fell on an inline box's leading edge is a break
	// before the box, and the box's margin travels to the next line with the
	// word it pushes along. That decision cannot be taken when the margin is
	// reached: the margin is an item of its own, the word after it is not an
	// opportunity of its own, and it is the *pair* that has to fit. So where the
	// opportunity was is remembered and the line rewinds to it if the word turns
	// out not to fit.
	//
	// Rewinding rather than looking ahead is what keeps this linear: each item is
	// still visited once, and the position is a single index rather than a scan.
	insetAt, insetLine, insetFlow := -1, 0, 0
	// The most recent point at which this line could have ended, kept for
	// break-spaces. §3's break-spaces value puts the soft wrap opportunity
	// *after* every preserved space and nowhere else, so a space belongs to the
	// unit that precedes it: "X XX X" in four characters is "X " and "XX X",
	// because the run "XX " — word and the space that follows it, with no
	// opportunity between them — is three characters wide and does not fit after
	// the first two. Greedy filling has to measure that whole run and not just
	// its first item, and this marker is how: the space is placed if it fits, and
	// sends the line back to the last opportunity if it does not.
	//
	// It is a marker rather than a lookahead for the reason insetAt is: each item
	// is visited once on the way forward and the rewind is a single index, so a
	// paragraph costs one pass however many times a line has to be given back.
	// Progress is guaranteed because the marker is only set once the line holds
	// content, so it is always past the item the line started at.
	oppAt, oppLine, oppFlow := -1, 0, 0
	// Where the white space that ends this line begins.
	//
	// §4.1.2's third and fourth rules are both about white space "at the end of a
	// line": the collapsible part of it is removed and what remains hangs. Neither
	// can happen to white space the line breaks *inside*, and breaking inside it
	// is what a greedy fill does on its own — the run is wider than the room left,
	// so the first opportunity in it ends the line and the rest goes to the next
	// one. What that produces is a line of nothing but spaces, which is precisely
	// the thing the two rules exist to prevent.
	//
	// So the run is found before the fill starts and the fill is told not to break
	// in it. It reaches from the last item that is not white space to the end of
	// the line's material — the next forced break, or the last item there is — and
	// an inline box's own inset is passed over rather than ending it, since a
	// margin is not content and a span wrapped around the spaces must not make
	// them breakable again.
	end := len(items)
	for k := from; k < len(items); k++ {
		if items[k].forced {
			end = k
			break
		}
	}
	tailFrom := end
	for tailFrom > from && isLineTailSpace(items[tailFrom-1]) {
		tailFrom--
	}
	i := from
	for ; i < len(items); i++ {
		item := items[i]
		if i == from && fromByte > 0 {
			// The line begins part-way through an item, because the line before
			// it ended inside this word. The cursor is an index *and* an offset
			// rather than a rewritten items slice: the caller re-runs this over
			// several band widths, so anything written back would be seen by the
			// next attempt and the split would compound.
			_, item = l.splitItem(item, fromByte)
		}

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
			return trimLineEdge(line), i + 1, 0, outOfFlow, true
		}

		// §4.1.2's first rule: a sequence of collapsible spaces at the
		// beginning of a line is removed. It is the space the break happened
		// at, or the one the author left after a tag, and keeping it would
		// indent every line after the first.
		//
		// Only a *collapsible* one. Preserved white space at the start of a
		// line is content — it is what makes "<pre>    indented</pre>" indent,
		// and it is the whole of the pre-wrap leading-space rule.
		//
		// The test is on the line's *content* rather than on the line being
		// empty, because an inline box's margin is not content — but no
		// document in the suite can tell the two apart, and that is worth
		// recording rather than leaving as an implied claim. Reaching the
		// difference needs a collapsible space that survives collection sitting
		// immediately after an inline box's leading margin, on a line that
		// begins there. The two requirements contradict each other: a line
		// begins at a margin only when a space preceded the box, and a space
		// before the box is exactly what makes §4.1.1 collapse away the one
		// after its opening tag. Planting "len(line) == 0" here moves nothing —
		// 2973 clean passes either way — so this branch is the correct reading
		// of the rule and has no test, which is a different thing from being
		// covered.
		if item.collapsible && !content {
			continue
		}

		if item.tab {
			// The distance to the next tab stop, plus whatever letter-spacing adds
			// after the character — a tab is a character like any other for that
			// purpose, and leaving it out would put the run after a tab a spacing
			// to the left of where it is drawn.
			item.width = tabAdvance(lineX.Add(used), item.tabStop, item.tabFloor).
				Add(item.spacing.letter)
		}

		// A hanging space never causes a break: it sits past the line's end
		// rather than moving to the next one. Without this, "XX    XX" under
		// pre-wrap would push the second word down a line for spaces that take
		// no room on the page at all.
		if !item.noWrap && !item.hangs && i < tailFrom && item.breakBefore &&
			len(line) > 0 && used.Add(item.width) > width {
			return trimLineEdge(line), i, 0, outOfFlow, false
		}

		// The rewind. The item does not begin a break opportunity of its own,
		// but an inline box opened just before it did, and the pair is what does
		// not fit — so the line ends where the box began and the box's leading
		// margin goes with it.
		if !item.noWrap && !item.hangs && i < tailFrom && !item.breakBefore && !item.inset &&
			insetAt >= 0 && used.Add(item.width) > width {
			return trimLineEdge(line[:insetLine]), insetAt, 0, outOfFlow[:insetFlow], false
		}

		// The rewind to the last opportunity. Something that does not fit and
		// cannot begin a line of its own is the tail of the unit before it, so a
		// line that cannot hold it cannot hold that unit either and ends at the
		// last opportunity instead. Where there is no such opportunity it stays
		// and the line overflows.
		//
		// It was written for break-spaces, whose preserved space is exactly that
		// — data, never dropped to make a line fit — and it was restricted to
		// spaces, which was too narrow. "xy <span>ab</span>cdefgh" in seventy-two
		// pixels put everything on one line and let it overflow: "cdefgh" begins
		// no opportunity, because there is none between a span and the text after
		// it, so nothing sent the line back to the space it had.
		//
		// Atomic inlines are still excluded, and that is measured rather than
		// reasoned. Letting an inline-block or an image rewind costs thirty-two
		// reftests, so the suite says the behaviour they have is the right one
		// and this is not the change that should alter it; extending the rule to
		// text alone moves nothing on the suite either way and fixes the case
		// above, which makes it a strict improvement rather than a trade.
		if (item.space || item.atomicBox == nil) && !item.collapsible &&
			!item.hangs && i < tailFrom && !item.noWrap && !item.inset &&
			!item.breakBefore && oppAt >= 0 && used.Add(item.width) > width {
			return trimLineEdge(line[:oppLine]), oppAt, 0, outOfFlow[:oppFlow], false
		}

		// A single item wider than the line has nowhere to go. It is placed and
		// overflows — breaking inside a word would be worse, since a word split
		// at an arbitrary point reads as a different word — and it is reported,
		// because the part past the edge is simply not drawn and nothing else
		// about the page says so.
		// overflow-wrap, CSS Text §5.5: the last resort.
		//
		// Its opportunities exist only "if there are no otherwise-acceptable
		// break points in the line", and this is the place that knows: every
		// rewind above has been tried and none applied, so ending the line here
		// is the only way not to overflow.
		//
		// The condition is about the *line* and not about one over-wide item,
		// which is the correction that made this do anything at all. A first
		// draft fired only where a single item was wider than the whole line,
		// and the suite's own fixture is not that shape: "XXXX XX" in four
		// characters cuts into pieces that each fit, and it is the run of them
		// that does not. Requiring no rewind target is what keeps it a last
		// resort — a line with a space in it breaks at the space.
		//
		// That last requirement is the correct reading of the rule and has no
		// test, which is a different thing from being covered. It cannot have
		// one: the two rewinds above return before this is reached, and the
		// branch before them takes any item that begins an opportunity of its
		// own, so what is left to arrive here holding a rewind target is an
		// atomic inline or a collapsible space — and breakInsideWord can cut
		// neither, so the branch declines and the line goes on to the same place
		// it would have reached anyway. Instrumented to count the case, no
		// document in the suite reaches it: zero hits over all 5177 reftests.
		// Dropping the conjunct therefore moves nothing, which is why it is
		// recorded here rather than left as an implied claim.
		if item.breakWord && !item.noWrap && !item.hangs && i < tailFrom && !item.inset && !item.tab &&
			insetAt < 0 && oppAt < 0 && used.Add(item.width) > width {
			// The offset is into items[i]. It is only the cursor's offset away
			// from that when this *is* the item the cursor pointed at: a line
			// that began at a float and reached its first text later is at
			// i > from, where the item is whole.
			base := 0
			if i == from {
				base = fromByte
			}
			// As much of the item as the room left will hold. This is what
			// keeps the fill greedy: a line with "ab" on it and "cdefgh" next
			// takes "cd" as well rather than stopping at the two characters it
			// already has.
			if head, at, ok := l.breakInsideWord(item, width.Sub(used)); ok {
				line = append(line, head)
				return trimLineEdge(line), i, base + at, outOfFlow, false
			}
			// Nothing of it fits — a single character too wide for the room
			// left, or a space, which has one cluster and cannot be cut. The
			// line ends in front of it and it begins the next one, which is
			// where a preserved space under break-spaces has to go: the value
			// exists so that spaces are data, and dropping one to tidy the line
			// would lose it.
			//
			// Only where the line holds something already. Otherwise there is
			// nothing to end and the item would begin this line again for ever;
			// it is placed, it overflows, and it is reported below.
			if content {
				return trimLineEdge(line), i, base, outOfFlow, false
			}
		}

		if item.width > width && !content && !item.space && !item.noWrap && !item.inset {
			// An inset is not text and has no text to name in the report. A
			// margin wider than the line is also not the fault the report is
			// about — nothing is clipped, the content is simply pushed past the
			// edge, and the box the author wrote is the box that was drawn.
			l.reportOverflow(item, width)
		}
		// Recorded before the switch below, because that is where content becomes
		// true: an opportunity at the very start of a line is not one the line can
		// be sent back to.
		if item.breakBefore && content {
			oppAt, oppLine, oppFlow = i, len(line), len(outOfFlow)
		}

		switch {
		case item.inset && item.breakBefore && content && insetAt < 0:
			// The line could have ended here. Remember enough to come back.
			insetAt, insetLine, insetFlow = i, len(line), len(outOfFlow)
		case !item.inset:
			// Something that is not a margin has been placed, so the break
			// before the last box is no longer the one to rewind to: there is a
			// nearer opportunity, or none, and either way this one is spent.
			insetAt = -1
			content = true
		}
		line = append(line, item)
		used = used.Add(item.width)
	}
	return trimLineEdge(line), i, 0, outOfFlow, false
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
// end of a line is removed, "as well as any trailing U+1680 OGHAM SPACE MARK
// whose white-space property is normal, nowrap, or pre-line". Both are
// trimAtEnd, which is why the scan reads that rather than collapsible.
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
// What the hanging affects is the break decision — hangs is what stops a
// trailing space pushing the next word down a line — and the alignment, which
// alignedWidth discounts it from so that a centred line does not sit half a
// space off centre. Justification is the one consumer §4.1.2 names that this
// engine still does not have, and textalign.go reports it rather than setting
// justified text ragged in silence.
// An inline box's own margin, border and padding is not text and does not stop
// the rule reaching the space before it: "<span>word </span>" ends the line with
// a space whether or not the span has a margin, and the span's margin is still
// its margin once the space has gone. So the scan looks past an inset and the
// inset is kept.
func trimLineEdge(line []inlineItem) []inlineItem {
	end := len(line)
	for end > 0 && (line[end-1].trimAtEnd || line[end-1].inset) {
		end--
	}
	if end == len(line) {
		return line
	}
	// Cutting the capacity keeps the append below from writing over the items
	// after end, which are still the caller's.
	out := line[:end:end]
	for _, item := range line[end:] {
		if item.inset {
			out = append(out, item)
		}
	}
	return out
}

// isLineTailSpace reports whether an item can be part of the white space that
// ends a line: the space itself, an inline box's own inset, and a box that is
// out of flow.
//
// The last two are there so that a span wrapped around the spaces, or an
// absolutely positioned box written among them, does not break the run in two
// and make the half before it breakable again. Neither is content — §4.1.2's
// rules are about the text — and neither takes the line anywhere.
func isLineTailSpace(item inlineItem) bool {
	if item.inset || item.abs != nil {
		return true
	}
	// White space that the end of a line does something to: the third rule
	// removes it, or the fourth hangs it. break-spaces is the value where
	// neither happens — its spaces are data, they take room, and §3 puts an
	// opportunity after every one of them — so a line may end inside a run of
	// them and this must not say otherwise.
	return item.space && (item.hangs || item.trimAtEnd)
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
		return l.normalLineHeight(b)
	}
	if n, ok := parseNumber(value); ok {
		return b.FontSize.Mul(n)
	}
	if length, ok := l.parseLength(b, "line-height"); ok {
		if v, ok := length.Resolve(b.FontSize, true); ok {
			return v
		}
	}
	return l.normalLineHeight(b)
}

// normalLineHeightFallbackFactor is what "normal" means for a face that does
// not say.
//
// The font's own answer is ascent + descent + line gap, and forme reports all
// three now — but only for a face that has the tables to state them. The
// fourteen standard PDF faces have no hhea and no OS/2 at all: their metrics
// come from AFM data, which carries no line gap, so Descriptor reports zero with
// the bit clear. Zero and silence are different answers and this is the one
// place in the engine where reading them as the same would be invisible: a page
// spaced by a number the font never gave.
//
// 1.2 for those, because CSS 2.1 §10.8.1 recommends between 1.0 and 1.2 and a
// value inside the range beats one derived from a term that is missing.
const normalLineHeightFallbackFactor = 1.2

// normalLineHeight is "line-height: normal".
//
// Worth recording what changed when the font finally got asked. Ahem states
// ascent 800, descent -200 and a line gap of zero, which comes to exactly one
// em — right for a face whose every glyph is an em square, and a figure no
// constant would have produced. Noto Sans comes to more than 1.2. Neither is
// something an engine can guess, which is the whole argument for asking.
func (l *layouter) normalLineHeight(b *Box) style.Unit {
	face, ok := l.fontFor(b)
	if !ok {
		return b.FontSize.Mul(normalLineHeightFallbackFactor)
	}
	top, bottom, upem, ok := lineMetrics(face)
	if !ok {
		return b.FontSize.Mul(normalLineHeightFallbackFactor)
	}
	var gap float64
	if d := face.Descriptor(); d.Has(fonts.MetricLineGap) {
		gap = float64(d.LineGap)
	}
	// One multiplication over the summed ratio rather than three over the terms.
	// A layout unit is a 64th of a pixel and each product is rounded to one, so
	// adding three rounded products is not the same number as rounding the sum —
	// it is out by up to a unit and a half, which is enough to move a line.
	h := b.FontSize.Mul((top - bottom + gap) / upem)
	if h <= 0 {
		// A face stating metrics that sum to nothing would collapse every line
		// on the page. It is not a value to pass on.
		return b.FontSize.Mul(normalLineHeightFallbackFactor)
	}
	return h
}

// lineExtents is how far a face's type reaches above and below its baseline,
// for the purpose of laying lines out.
//
// It is one function because the two places that ask have to agree. "line-height:
// normal" is these two plus the line gap, and an inline box's *content area* —
// what its background paints and what "text-top" and "text-bottom" name — is
// these two exactly. When they come from different formulas the two disagree,
// and the suite catches it directly: inline-formatting-context-002 draws a black
// inline box beside a float of the same text and asks for the two to be the same
// height. Ours were 14.4 and 19.2.
//
// # Where the numbers come from, and the case that forced this
//
// hhea's ascender and descender, for any face that has an hhea to state them.
// The standard fourteen PDF faces have neither hhea nor OS/2: their metrics come
// from AFM data, whose Ascender and Descender are *typographic* — about the top
// of a "d" and the bottom of a "p" — rather than the box the face wants a line
// laid out in. Times comes to 0.900em that way and Courier to 0.786em, which is
// tighter than any browser sets the same text: a browser reads usWinAscent and
// usWinDescent from a real Times, which come to about 1.15em.
//
// So for a face that states no line gap — the one thing that says there is no
// hhea and no OS/2 behind these numbers — the glyph bounding box is used
// instead. It is the AFM's own answer to the same question, it is the nearest
// thing the file has to usWin*, and it puts Times at 1.116em and Courier at
// 1.055em: inside §10.8.1's recommended range, and close to what a browser
// produces for the same document.
//
// The previous shape of this was a 1.2 factor for "normal" alone, which put the
// line height in range and left the content area at 0.900em. That is the pair
// the suite disagreed with — and simply dropping the factor, so both came to
// 0.900em, made them agree at a line height no browser would produce and lost
// inline-formatting-context-015, whose reference is a 30px cell that two lines
// of text have to fill.
func lineMetrics(face *fonts.Face) (top, bottom, upem float64, ok bool) {
	upem = float64(face.UnitsPerEm())
	if upem <= 0 {
		return 0, 0, 0, false
	}
	d := face.Descriptor()
	top, bottom = float64(d.Ascent), float64(d.Descent)
	if !d.Has(fonts.MetricLineGap) {
		top, bottom = float64(d.BBox[3]), float64(d.BBox[1])
	}
	if top-bottom <= 0 {
		return 0, 0, 0, false
	}
	return top, bottom, upem, true
}

// lineExtents is lineMetrics at a box's font size.
func (l *layouter) lineExtents(b *Box, face *fonts.Face) (ascent, descent style.Unit, ok bool) {
	top, bottom, upem, ok := lineMetrics(face)
	if !ok {
		return 0, 0, false
	}
	return b.FontSize.Mul(top / upem), b.FontSize.Mul(-bottom / upem), true
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
	ascent, descent, ok := l.lineExtents(b, face)
	if !ok {
		return lineHeight.Mul(0.8)
	}
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

// reportWordBreak reports a word-break value this engine reads as normal.
//
// "break-all" is implemented; "keep-all" and "auto-phrase" are not, and both
// *remove* or *move* opportunities rather than adding them — keep-all stops CJK
// text breaking between two ideographs, and auto-phrase moves a Korean break to
// a phrase boundary. Ignoring either breaks a line somewhere the author said not
// to, which no amount of looking at the page reveals as a missing feature.
//
// Once per value per box, for the same reason checkScript is once per script.
func (l *layouter) reportWordBreak(b *Box, value string) {
	if l.reportedWordBreak == nil {
		l.reportedWordBreak = map[string]bool{}
	}
	if l.reportedWordBreak[value] {
		return
	}
	l.reportedWordBreak[value] = true
	l.rec.ReportDetail(Finding{
		Rule:     RuleUnsupportedValue,
		Property: "word-break",
		Message: value + " was read as normal, so a line may break where the " +
			"value asked it not to",
		Path: PathOf(b.Element),
	})
}

// reportLineBreak reports a line-break value this engine reads as auto.
//
// Unlike its word-break counterpart it is conditional on the text, and the
// condition is what keeps it honest. loose, normal and strict differ from auto
// only in how strictly CJK text may break — around small kana, around iteration
// marks, before centred punctuation — and this engine's whole CJK rule is
// "between two ideographs", which all three leave alone. Over Latin text the
// three values provably change nothing, and the suite says so: pre-wrap-004,
// -005 and -006 exist to assert that "XX    XX" wraps the same under loose,
// normal and strict as under auto. Warning there would be crying wolf on a page
// that is correct.
//
// So the report is made where the difference could show, which is text with an
// ideograph in it — the only text this engine breaks by a rule the three values
// have anything to say about.
func (l *layouter) reportLineBreak(b *Box, value string) {
	if !strings.ContainsFunc(b.Text, isIdeographic) {
		return
	}
	if l.reportedLineBreak == nil {
		l.reportedLineBreak = map[string]bool{}
	}
	if l.reportedLineBreak[value] {
		return
	}
	l.reportedLineBreak[value] = true
	l.rec.ReportDetail(Finding{
		Rule:     RuleUnsupportedValue,
		Property: "line-break",
		Message: value + " was read as auto, so CJK text may break where the " +
			"value asked it not to",
		Path: PathOf(b.Element),
	})
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
	// The question has to be the one *drawing* answers, and it was not.
	//
	// This asked face.GlyphID, which is whether the face has a glyph mapped to
	// a code point. Shaping asks something different and gets a different
	// answer: a no-break space has no glyph of its own and is set as a space, a
	// bidi override has none and takes no room at all, and the same goes for
	// every fixed-width Unicode space and every zero-width format control. All
	// of them draw correctly, and all of them were being reported — at Error
	// severity, the one that stops a document being produced.
	//
	// Measured over the reftest suite, that was the single most common finding
	// in the whole engine: 154 documents reported a missing glyph for the
	// no-break space alone, and 260 documents were kept out of the clean-pass
	// count by nothing else. A guardrail wrong that often is worse than no
	// guardrail, because the reports it is right about are buried.
	//
	// Shaping the whole run first is also what makes this cheap: the answer is
	// almost always that nothing is missing, and only then is it worth walking
	// the characters to find out which.
	if !missesVisible(face, b.Text) {
		return
	}
	for _, r := range b.Text {
		if r == '\n' || r == '\t' || marksNoPaper(r) {
			continue
		}
		if _, missing := face.Shape(string(r)); missing == 0 {
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
				describeRune(r) + ", which is set as a space, so the character is " +
				"missing from the page and from the text extracted out of it",
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
//
// The right-to-left scripts used to be here, and are not any more: the
// bidirectional algorithm is applied per paragraph and its reordering per line —
// see bidi.go — so Hebrew, Arabic, Syriac and Thaana are set in the order they
// are read. Shaping them was already forme's, and still is.
func unsupportedScript(r rune) (string, bool) {
	switch {
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

// missesVisible reports whether a face cannot set some character of text that
// would have put ink on the page.
//
// It is one predicate rather than two because it has two callers that must not
// disagree: the guardrail that reports a missing glyph, and the fallback that
// goes looking for a face which has it. They did disagree, briefly, and the
// result was the fallback substituting a whole different font for a paragraph
// whose only "missing" character was a no-break space — changing every metric on
// the page to fix nothing. Asking the same question twice in two ways is how
// that happens, so it is asked once.
//
// Shaping the whole run first is what keeps it cheap: the answer is almost
// always no, and only then is it worth walking the characters.
func missesVisible(face *fonts.Face, text string) bool {
	if _, missing := face.Shape(text); missing == 0 {
		return false
	}
	for _, r := range text {
		if r == '\n' || r == '\t' || marksNoPaper(r) {
			continue
		}
		if _, missing := face.Shape(string(r)); missing > 0 {
			return true
		}
	}
	return false
}

// marksNoPaper reports whether a character is a space by definition.
//
// A face that cannot encode one of these is not a problem to report. The encoder
// substitutes a space for anything it cannot represent, and for a character that
// was never going to put ink down that substitution is either exactly right — a
// no-break space *is* a space, differing only in whether a line may break at it,
// which is settled long before the face is asked — or wrong by a fraction of an
// em, as for the fixed-width spaces whose whole purpose is to be a particular
// width.
//
// The distinction matters because the substitution is not harmless in general. A
// Hebrew letter the face cannot encode also becomes a space, so the word does
// not appear as a row of boxes — it is simply absent, from the page and from the
// text extracted out of it. That is worth an error. A no-break space becoming a
// space is not, and reporting it was the most common finding this engine
// produced: 154 documents in the reftest suite raised it for U+00A0 alone.
//
// # Why the format characters are not listed here
//
// They were, and the list could not be observed. A planted defect that deleted
// the whole format-character branch — soft hyphen, the zero-width spaces, the
// bidi embeddings and isolates, the byte order mark — broke nothing, and the
// reason is that shaping already answers "not missing" for every one of them,
// on both kinds of face. A simple face encodes through WinAnsi and drops them;
// a composite face shapes them to no glyph and no advance, which is what they
// are for. Measured on Ahem: every one reports missing=0, and so does the
// no-break space, because a composite face has a real glyph for it.
//
// So only the space separators are here, and only the simple faces need them —
// which is to say the fourteen standard PDF faces, which is what a document gets
// unless a caller supplies something else.
func marksNoPaper(r rune) bool {
	switch {
	case r == 0x00A0, // no-break space
		r == 0x1680,                // ogham space mark
		r >= 0x2000 && r <= 0x200A, // en quad through hair space
		r == 0x202F,                // narrow no-break space
		r == 0x205F,                // medium mathematical space
		r == 0x3000:                // ideographic space
		return true
	}
	return false
}

// faceForText is fontFor, with a substitution when the family the document asked
// for cannot set the text.
//
// The standard fourteen faces cover Latin and nothing else, so a document with a
// Hebrew word in it gets a face that cannot encode the letters — and the encoder
// substitutes a space for each, which means the word is absent from the page and
// from the text extracted out of it rather than showing as boxes a reader would
// notice. That is what checkGlyphs reports, and reporting it is not the same as
// fixing it: a caller that has a face with the letters should be able to say so.
//
// FallbackFontSet is how it says so, and this is the only place that asks. The
// substitution is reported, because it changes the metrics and therefore where
// every line breaks — the same reason a missing family is reported.
//
// Two limits, both deliberate and both visible in what they leave behind:
//
// It is per box. A box whose text mixes scripts that no single face covers keeps
// the family's face and reports the missing glyphs, because choosing one face
// for the box cannot help it. Cutting a run into per-face pieces is what
// fonts.Stack does and it reaches into measurement, line breaking and the
// content stream; until that exists, this handles the common shape — a run of
// text that is all one script.
//
// It is cached per box rather than per family, because the answer depends on the
// text. Shaping a run to find out whether it is covered is not free, and
// itemsFor is on the hot path.
func (l *layouter) faceForText(b *Box) (*fonts.Face, bool) {
	face, ok := l.fontFor(b)
	if !ok || b.Text == "" {
		return face, ok
	}
	if got, cached := l.textFaces[b]; cached {
		return got, got != nil
	}
	chosen := face
	if missesVisible(face, b.Text) {
		if set, canFall := l.fontSet.(FallbackFontSet); canFall {
			bold := isBold(b.Style["font-weight"])
			italic := isItalic(b.Style["font-style"])
			if alt, found := set.FaceFor(b.Text, bold, italic); found {
				chosen = alt
				l.rec.ReportDetail(Finding{
					Rule: RuleFontFallback,
					Message: "no face for " + quoteValue(b.Style["font-family"]) +
						" could set this text, so " + quoteValue(alt.Name()) +
						" was used for it; the metrics and the line breaks will differ",
					Path:     PathOf(b.Element),
					Property: "font-family",
				})
			}
		}
	}
	l.textFaces[b] = chosen
	return chosen, true
}

// splitItem cuts one text item in two at a byte offset, re-measuring both.
//
// The bidi range goes with the text. An item carries the span of the paragraph
// buffer its characters occupy, and the two halves occupy the two halves of it —
// which is what keeps the resolved levels attached to the right characters after
// a word has been broken across a line.
//
// Re-measuring rather than apportioning the original width is the point. A face
// may kern or ligate across the cut, so the two pieces do not in general add up
// to the whole, and the number that has to be right is the one used to place the
// text that is actually drawn.
func (l *layouter) splitItem(item inlineItem, at int) (head, tail inlineItem) {
	head, tail = item, item
	head.text, tail.text = item.text[:at], item.text[at:]
	// at is an offset into the string, and the bidi range counts runes: the
	// paragraph the levels were resolved over is a []rune, and bidiStart is a
	// position in it. Adding the byte offset to it is right for Latin and wrong
	// for everything that needs the algorithm at all — a Hebrew letter is two
	// bytes, so a word cut in half moved the range twice as far as the text, and
	// the tail then read its level from a position two characters past its own.
	//
	// What that looks like is "אבגדהו12" in an RTL block narrow enough to cut the
	// word: the "12" belongs to the left of the letters and was drawn to the
	// right of them, on the line the tail begins, while the same text unbroken
	// orders correctly.
	runesBefore := utf8.RuneCountInString(item.text[:at])
	head.bidiEnd = item.bidiStart + runesBefore
	tail.bidiStart = item.bidiStart + runesBefore
	head.width = l.measureSpaced(item.face, head.text, item.size, item.spacing)
	tail.width = l.measureSpaced(item.face, tail.text, item.size, item.spacing)
	// The tail begins a line, so it takes no opportunity from what was in front
	// of the head — there is nothing in front of it any more.
	//
	// Nothing reads it: a tail is always the first item of the line it starts, and
	// there an opportunity is neither recorded (that wants content before it) nor
	// acted on (the break in front of one wants a line with something on it). It
	// is cleared because leaving it would make the field state something untrue
	// about where the item now sits, not because a document can tell.
	tail.breakBefore = false
	return head, tail
}

// breakInsideWord is overflow-wrap's last resort: the largest prefix of an item
// that fits, cut where a grapheme cluster ends.
//
// The cut is at a cluster boundary and not at a character, for the reason
// break-all's is — CSS Text §2 puts a soft wrap opportunity between typographic
// character units, and a cut inside one separates a letter from its accent. It
// is the same rule and the same table; only when it applies differs.
//
// The prefix is found by bisection over the boundaries rather than by measuring
// the text a cluster at a time. Widths do not add up: a face may kern or ligate
// across the join, so a running total is not the width of the prefix it claims
// to be. Bisection measures each candidate whole, which is exact for the one
// that is chosen, and costs a logarithmic number of measurements rather than
// one per character — which matters, because the input is untrusted and this is
// reached precisely for the longest words in a document.
//
// It reports false when there is nothing to gain: an item with one cluster, or
// one whose first cluster already overflows. Both leave the word to overflow and
// be reported, which is right — a line cannot hold less than one character, and
// breaking one off to leave the rest overflowing anyway would only lose a
// character off the end.
func (l *layouter) breakInsideWord(item inlineItem, width style.Unit) (head inlineItem, at int, ok bool) {
	if !item.breakWord || item.face == nil || width <= 0 || item.text == "" {
		return inlineItem{}, 0, false
	}
	bounds := grapheme.Boundaries(nil, item.text)
	if len(bounds) == 0 {
		return inlineItem{}, 0, false // one cluster: nothing to cut
	}

	// The largest boundary whose prefix fits. Bisection needs the predicate to
	// be monotone, and it is for any face whose advances are non-negative: a
	// longer prefix is never narrower. A face with a negative advance would make
	// this pick a cut that is merely *a* fitting one rather than the longest,
	// which is a worse line and not a wrong page.
	lo, hi := 0, len(bounds) // lo is known to fit (the empty prefix), hi is not known
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if mid > len(bounds) {
			break
		}
		w := l.measureSpaced(item.face, item.text[:bounds[mid-1]], item.size, item.spacing)
		if w <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return inlineItem{}, 0, false // not even one cluster fits
	}
	at = bounds[lo-1]
	head, _ = l.splitItem(item, at)
	return head, at, true
}
