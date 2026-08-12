package render

import (
	"strings"

	"github.com/mgilbir/pdf0/internal/bidi"
	"github.com/mgilbir/pdf0/style"
)

// Bidirectional text: the direction and unicode-bidi properties, and the
// reordering they ask for.
//
// # Where the algorithm runs, and where the reordering does
//
// UAX #9 resolves an embedding level per character over a *paragraph*, and CSS
// makes a block container's inline content one — split at every forced line
// break, because a <br> ends a paragraph as surely as a newline in plain text
// does. The reordering is then applied per *line*, after the breaking, and that
// separation is the whole reason this is not a pass over a string: which spaces
// are reset by rule L1, and which stretch is reversed by rule L2, both depend on
// where the line ended, which nothing knows until the line has been filled.
//
// So the work is in three places and each is the only one that could do its
// part:
//
//   - collectInline builds the paragraph as it flattens the tree, because that
//     is the one walk that sees the inline boxes in document order — and the
//     boxes are where unicode-bidi's formatting codes come from.
//   - resolveBidi runs the algorithm and splits the items at level boundaries,
//     so that every item on a line has one direction. An item is the atom the
//     line is built from; one spanning a level boundary could not be placed.
//   - inlineContent reorders the items of each line as it positions them.
//
// # Why the runs and not the text
//
// The reordering is over *runs*, not over the characters inside one. Each run is
// set by forme, which applies the algorithm to the string it is given and hands
// back glyphs in the order they are drawn — so a Hebrew word comes back already
// reversed and with its brackets mirrored, and the levels resolved here decide
// only where each run goes on the line. Reversing the text as well would undo it.
//
// The one thing the shaper cannot know is what a run's direction is when the run
// has no strong character in it: a lone bracket between two Hebrew words is
// right-to-left because of its neighbours, and the neighbours are in other runs
// by then. That is why a right-to-left run is drawn with an explicit override in
// front of it — see paint.go.

// The explicit formatting codes, by name rather than by number: this is the one
// file where a wrong code point would produce text in the wrong order with
// nothing else to say so.
const (
	runeLRE = '‪'
	runeRLE = '‫'
	runePDF = '‬'
	runeLRO = '‭'
	runeRLO = '‮'
	runeLRI = '⁦'
	runeRLI = '⁧'
	runeFSI = '⁨'
	runePDI = '⁩'
	// runeObject is U+FFFC OBJECT REPLACEMENT CHARACTER, which is what an
	// atomic inline counts as in the paragraph: CSS Writing Modes says an
	// element that is not text takes part in the algorithm as one neutral
	// character, so an image between two Hebrew words does not split them.
	runeObject = '￼'
)

// bidiParagraph is one paragraph with its levels resolved. The alias keeps the
// algorithm's package out of inline.go, which carries enough already.
type bidiParagraph = bidi.Paragraph

// bidiMode is what unicode-bidi asks for.
type bidiMode uint8

const (
	// bidiNormal is the initial value: the box is transparent to the algorithm,
	// and a direction declared on it does nothing at all. That last part
	// surprises everyone once — "direction: rtl" on a <span> with no
	// unicode-bidi is inert by design, because an inline box that changed the
	// direction without opening an embedding would reorder text outside itself.
	bidiNormal bidiMode = iota
	bidiEmbed
	bidiIsolate
	bidiOverride
	bidiIsolateOverride
	bidiPlaintext
)

func bidiModeOf(b *Box) bidiMode {
	switch strings.ToLower(strings.TrimSpace(b.Style["unicode-bidi"])) {
	case "embed":
		return bidiEmbed
	case "isolate":
		return bidiIsolate
	case "bidi-override":
		return bidiOverride
	case "isolate-override":
		return bidiIsolateOverride
	case "plaintext":
		return bidiPlaintext
	}
	return bidiNormal
}

// isRTL reads the direction property. It is inherited, so every box has an
// answer and the initial one is left to right.
func isRTL(b *Box) bool {
	return strings.EqualFold(strings.TrimSpace(b.Style["direction"]), "rtl")
}

// bidiControls is the pair of formatting codes an inline box stands for.
//
// CSS Writing Modes defines unicode-bidi by naming the codes, which is why this
// is a table rather than a mechanism: "embed" *is* an LRE or an RLE with a
// matching PDF, and "isolate" *is* an LRI or an RLI with a matching PDI. Writing
// it any other way would be inventing a second definition of a property that
// already has one.
func bidiControls(b *Box) (open, close []rune) {
	rtl := isRTL(b)
	switch bidiModeOf(b) {
	case bidiEmbed:
		if rtl {
			return []rune{runeRLE}, []rune{runePDF}
		}
		return []rune{runeLRE}, []rune{runePDF}
	case bidiIsolate:
		if rtl {
			return []rune{runeRLI}, []rune{runePDI}
		}
		return []rune{runeLRI}, []rune{runePDI}
	case bidiOverride:
		if rtl {
			return []rune{runeRLO}, []rune{runePDF}
		}
		return []rune{runeLRO}, []rune{runePDF}
	case bidiIsolateOverride:
		// Both, in that order: the isolate keeps the box's contents from
		// affecting the text around it, and the override forces a direction on
		// everything inside. <bdo dir=rtl> is the element this exists for.
		if rtl {
			return []rune{runeRLI, runeRLO}, []rune{runePDF, runePDI}
		}
		return []rune{runeLRI, runeLRO}, []rune{runePDF, runePDI}
	case bidiPlaintext:
		// A first-strong isolate: the contents decide their own direction, which
		// is what "plaintext" means and what <bdi> and dir=auto ask for.
		return []rune{runeFSI}, []rune{runePDI}
	}
	return nil, nil
}

// paragraphDirection is the base direction of a block container's inline
// content, and the controls its own unicode-bidi wraps that content in.
//
// The two are separate because they answer different halves of the property.
// "direction" sets the paragraph embedding level; "unicode-bidi" says what
// happens to what is inside it, and for a block container only two of its values
// have any effect — an override applies to the whole paragraph, and plaintext
// hands the direction back to the text.
func paragraphDirection(b *Box) (bidi.Direction, []rune, []rune) {
	dir := bidi.LeftToRight
	if isRTL(b) {
		dir = bidi.RightToLeft
	}
	switch bidiModeOf(b) {
	case bidiOverride, bidiIsolateOverride:
		if dir == bidi.RightToLeft {
			return dir, []rune{runeRLO}, []rune{runePDF}
		}
		return dir, []rune{runeLRO}, []rune{runePDF}
	case bidiPlaintext:
		// Rules P2 and P3 over each paragraph of the content. The direction
		// property is ignored, which is what makes this the right value for
		// text whose language the author does not know.
		return bidi.Auto, nil, nil
	}
	return dir, nil, nil
}

// bidiBuilder collects the text of an inline formatting context in logical
// order, as the flattening walks it.
//
// It is one rune slice per bidi paragraph rather than one for the context,
// because rule P1 resolves each paragraph on its own: a <br> in the middle of a
// block starts a new one, and running the algorithm across the break would let
// the first word of the document decide the direction of the last.
type bidiBuilder struct {
	paras [][]rune
	// stack is the formatting codes currently open, so that a paragraph started
	// in the middle of them begins with them again. An isolate that spans a
	// <br> is still open on the line after it.
	stack [][]rune
	// needed records that something in the context requires the algorithm: a
	// right-to-left character, an Arabic number, or a formatting code. A
	// left-to-right paragraph with none of them resolves to all-zero levels, so
	// the whole of the algorithm can be skipped for it — which is nearly every
	// paragraph nearly every document sets.
	needed bool
}

func newBidiBuilder(open []rune) *bidiBuilder {
	p := &bidiBuilder{paras: [][]rune{nil}}
	if len(open) > 0 {
		p.enter(open)
	}
	return p
}

// cur is the paragraph being built.
func (p *bidiBuilder) cur() *[]rune { return &p.paras[len(p.paras)-1] }

// add appends a run of text and returns where it landed: which paragraph, and
// the range of runes within it. The paragraph number counts from one; see
// inlineItem.bidiPara.
//
// This runs over every character of every document, including the great majority
// that will turn out not to need the algorithm at all: the paragraph has to be
// built before anything can know whether it was worth building. It costs a
// little under three per cent of the layout of a page of Latin prose, measured,
// and two things that look like improvements are not:
//
//   - Sizing the slice for the run rather than letting append double it makes it
//     three times worse. Every call then reallocates and copies the paragraph so
//     far, because a slice grown to exactly the length it needs has no room for
//     the next word.
//   - Testing each rune as it is copied, instead of scanning the run again for a
//     character that needs the algorithm, is slower too. The second scan stops
//     at the first byte below U+0590 without a call, and the great majority of
//     text is nothing else.
func (p *bidiBuilder) add(text string) (para, start, end int) {
	if p == nil {
		return 0, 0, 0
	}
	cur := p.cur()
	start = len(*cur)
	for _, r := range text {
		*cur = append(*cur, r)
	}
	if !p.needed {
		p.needed = bidi.Needed(text)
	}
	return len(p.paras), start, len(*cur)
}

// object appends the one character an atomic inline counts as.
func (p *bidiBuilder) object() (para, start, end int) {
	if p == nil {
		return 0, 0, 0
	}
	cur := p.cur()
	start = len(*cur)
	*cur = append(*cur, runeObject)
	return len(p.paras), start, len(*cur)
}

// enter opens an inline box's formatting codes, and leave closes them.
func (p *bidiBuilder) enter(open []rune) {
	if p == nil || len(open) == 0 {
		return
	}
	p.stack = append(p.stack, open)
	cur := p.cur()
	*cur = append(*cur, open...)
	p.needed = true
}

func (p *bidiBuilder) leave(open, close []rune) {
	if p == nil || len(open) == 0 {
		return
	}
	p.stack = p.stack[:len(p.stack)-1]
	cur := p.cur()
	*cur = append(*cur, close...)
}

// breakParagraph ends the current bidi paragraph at a forced line break and
// starts the next one, reopening whatever formatting codes were still open.
func (p *bidiBuilder) breakParagraph() {
	if p == nil {
		return
	}
	p.paras = append(p.paras, nil)
	cur := p.cur()
	for _, open := range p.stack {
		*cur = append(*cur, open...)
	}
}

// resolveBidi runs the algorithm over the context's paragraphs and splits the
// items so that each has a single embedding level.
//
// Splitting is what makes the rest of inline layout able to ignore all of this.
// A line is built from items and positioned item by item, so an item that
// straddled a level boundary — "abcHEBREW" with no space in it — could not be
// placed at all: half of it belongs at one end of the line and half at the
// other.
func (l *layouter) resolveBidi(b *Box, items []inlineItem, p *bidiBuilder) []inlineItem {
	if p == nil || len(items) == 0 {
		return items
	}
	dir, _, _ := paragraphDirection(b)
	if dir == bidi.LeftToRight && !p.needed {
		// Every level is zero, the visual order is the logical one, and there is
		// nothing for the reordering to do. This is the common case by a wide
		// margin, and skipping it here is what keeps a page of Latin text from
		// paying for a feature it does not use.
		return items
	}

	resolved := make([]*bidi.Paragraph, len(p.paras))
	for i, text := range p.paras {
		if len(text) > 0 {
			resolved[i] = bidi.Resolve(text, dir)
		}
	}

	out := make([]inlineItem, 0, len(items))
	for _, item := range items {
		if item.bidiPara < 1 || item.bidiPara > len(resolved) {
			out = append(out, item)
			continue
		}
		para := resolved[item.bidiPara-1]
		if para == nil {
			out = append(out, item)
			continue
		}
		out = append(out, l.splitByLevel(item, para)...)
	}
	insetSides(out)
	return out
}

// insetSides is CSS 2.1 §8.6: an inline box's margin, border and padding go on
// the physical side they are named for, whichever way its content reads.
//
// # The rule, and why it is not the direction property
//
// §8.6 says it twice, once per direction, and both halves put the left inset on
// "the leftmost generated box" of the element and the right inset on "the
// rightmost". What the direction property changes is only which *line box* those
// two are looked for on when the element is broken across several — the first or
// the last. On a single line it changes nothing at all.
//
// So the question this has to answer is not "what did direction say" but "was
// this box's content reversed", and only the algorithm knows that. A
// "direction: rtl" span with the initial "unicode-bidi: normal" is not reversed —
// the property is inert on an inline box without an embedding, by design, since a
// box that changed the direction without opening one would reorder text outside
// itself. Swapping on the property was tried and cost nine clean passes.
//
// # How it is done
//
// Two things are decided per box, and both are about the box's content rather
// than about anything declared on it.
//
// The first is whether the two items should swap what they hold. They are
// emitted in logical order with the left inset first; a box whose content
// resolves to an odd level *throughout* will have that content reversed by the
// reordering and the two items reversed with it, so their widths are exchanged
// here. The item that will be drawn last is given the inset that belongs on the
// right, and the one that will be drawn first the inset that belongs on the
// left. Nothing else about either item moves, so line breaking still sees the
// box's leading edge where the box begins.
//
// "Throughout" is the strict reading and it is deliberate. A box whose content
// resolves to more than one level has ends that need not be at its visual edges
// at all — its leftmost generated box may be neither of them — and §8.6 then
// asks for something two items cannot express. Guessing from the first item's
// level is measurably worse: css-text/white-space's tab-bidi-001 has an outer
// span holding a right-to-left <bdo> followed by left-to-right text, and
// swapping on the <bdo>'s level put the span's left border three pixels inside
// where the reference draws it.
//
// The second is the level the two items are *reordered* at, which they need
// because they carry no characters and so the algorithm gave them none. It is
// the lowest level anything inside the box reached. An embedding raises the
// level of what is inside it and leaves the box's own boundary outside, so the
// lowest is the box's own — and both other candidates were tried and are wrong:
//
//   - the neighbouring item's level glues the box's edge to whatever run abuts
//     it, which is the tab-bidi-001 fault again, one border out of place;
//   - the paragraph's base level detaches the edge from its own content, which
//     costs bidi-span-003: a purple-bordered <span> of Latin in a
//     "direction: rtl" div had its opening border thrown to the far end of the
//     line, so the border drawn round one word enclosed two.
//
// # Cost
//
// One pass and no allocation beyond the stack of inline boxes currently open.
// Every question is answered in constant time per box: the counts are subtracted
// from the running totals at the close, and the minimum is folded into the
// enclosing box's as each one pops, so no box ever rescans its own content. The
// stack's depth is the inline nesting, which the HTML parser caps at 256.
func insetSides(items []inlineItem) {
	// noLevel is above every level UAX #9 can produce: MaxDepth is 125 and rule
	// X8 admits one more.
	const noLevel uint8 = 255

	// open is one inline box whose lead inset has been seen and whose trail
	// inset has not.
	type open struct {
		box  *Box
		lead int
		// content and odd are the running counts at the moment the box opened.
		// Subtracting them at the close gives the box's own.
		//
		// They are counts rather than a level and a "have we got one yet" flag
		// because zero is a real embedding level — the left-to-right one — so a
		// box that has seen no content and a box whose content is left-to-right
		// have to stay distinguishable. Counting keeps them apart without a
		// sentinel: no content is a count of zero, which swaps nothing.
		content, odd int
		// min is the lowest level seen inside this box so far, or noLevel.
		min uint8
	}
	var stack []open
	content, odd := 0, 0

	for i := range items {
		if items[i].inset {
			if items[i].insetLead {
				stack = append(stack, open{
					box: items[i].box, lead: i,
					content: content, odd: odd, min: noLevel,
				})
				continue
			}
			if len(stack) == 0 || stack[len(stack)-1].box != items[i].box {
				// The pair is emitted together by insetItems and nested by the
				// recursion that emits it, so this cannot happen. It is checked
				// because the alternative to skipping is swapping two unrelated
				// boxes' insets on a document nobody wrote deliberately.
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			// A box's content counts as its parent's too, so the minimum folds
			// outwards as the stack unwinds rather than being recomputed.
			if len(stack) > 0 && top.min < stack[len(stack)-1].min {
				stack[len(stack)-1].min = top.min
			}
			if n := content - top.content; n > 0 && odd-top.odd == n {
				items[top.lead].width, items[i].width =
					items[i].width, items[top.lead].width
			}
			if top.min != noLevel {
				items[top.lead].insetLevel, items[top.lead].insetLevelKnown = top.min, true
				items[i].insetLevel, items[i].insetLevelKnown = top.min, true
			}
			continue
		}
		if items[i].para == nil {
			// No characters of its own: a float marker, or a run in a paragraph
			// the algorithm did not resolve. It says nothing about direction.
			continue
		}
		content++
		if items[i].level&1 == 1 {
			odd++
		}
		if len(stack) > 0 && items[i].level < stack[len(stack)-1].min {
			stack[len(stack)-1].min = items[i].level
		}
	}
}

// splitByLevel cuts one item where its characters change embedding level.
func (l *layouter) splitByLevel(item inlineItem, para *bidi.Paragraph) []inlineItem {
	levels := para.Levels()
	if item.bidiEnd > len(levels) || item.bidiStart >= item.bidiEnd {
		item.para = para
		return []inlineItem{item}
	}
	item.para = para
	item.level = levels[item.bidiStart]

	// Almost every item is one level throughout — a word is one direction — so
	// the scan for a boundary is the whole cost in the usual case.
	uniform := true
	for i := item.bidiStart + 1; i < item.bidiEnd; i++ {
		if levels[i] != levels[item.bidiStart] {
			uniform = false
			break
		}
	}
	if uniform || item.text == "" {
		return []inlineItem{item}
	}

	runes := []rune(item.text)
	if len(runes) != item.bidiEnd-item.bidiStart {
		// The item's text and its place in the paragraph disagree, which can only
		// mean the two were built from different text. Splitting on the levels
		// would cut the string at the wrong place, so the item is left whole at
		// the level of its first character — wrong order rather than wrong text.
		//
		// Instrumented over the suite it never fires: every range is built by
		// bidiBuilder.add from the very text it describes, and splitItem moves
		// text and range together. So this is defence against a range that has
		// drifted rather than a case the layout produces — and the danger it
		// guards is that such drift is otherwise silent, since giving up on the
		// reordering looks like text that simply had one direction.
		return []inlineItem{item}
	}

	var out []inlineItem
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && levels[item.bidiStart+j] == levels[item.bidiStart+i] {
			j++
		}
		piece := item
		piece.text = string(runes[i:j])
		piece.bidiStart = item.bidiStart + i
		piece.bidiEnd = item.bidiStart + j
		piece.level = levels[item.bidiStart+i]
		piece.width = l.measureSpaced(item.face, piece.text, item.size, item.spacing)
		if i > 0 {
			// A level boundary is not a break opportunity. "abcHEBREW" is one
			// word however many directions it is written in, and a line must not
			// be allowed to end inside it.
			piece.breakBefore = false
		}
		out = append(out, piece)
		i = j
	}
	return out
}

// lineOffsets is where each of a line's items sits from the line's left edge,
// together with the pen position at the end of it.
//
// The reordering is applied to the *positions* and not to the slice, and that is
// deliberate. Painting reads a run's own X, so the order of the slice makes no
// difference to the page — but it is also the order the runs are written into
// the content stream, which is the order a reader extracts the text in. Keeping
// the slice in logical order is what lets a right-to-left paragraph be drawn the
// way it reads and copied out the way it was written; reordering the slice would
// have traded the second for nothing.
func lineOffsets(runs []inlineItem) ([]style.Unit, style.Unit) {
	xs := make([]style.Unit, len(runs))
	var x style.Unit
	order := lineVisualOrder(runs)
	if order == nil {
		for i, item := range runs {
			xs[i] = x
			x = x.Add(item.width)
		}
		return xs, x
	}
	for _, k := range order {
		xs[k] = x
		x = x.Add(runs[k].width)
	}
	return xs, x
}

// lineBaseIsRTL is the inline base direction a line was set in.
//
// It is the block's own direction almost always, and the exception is the reason
// this is not simply isRTL: under "unicode-bidi: plaintext" each bidi paragraph
// decides its own direction from its first strong character, so two lines of one
// block can differ — and "text-align: start", which is the initial value,
// resolves against the line's and not the block's.
func lineBaseIsRTL(b *Box, runs []inlineItem) bool {
	for _, item := range runs {
		if item.para != nil {
			return item.para.Level&1 == 1
		}
	}
	return isRTL(b)
}

// lineVisualOrder is rules L1 and L2 over one finished line: the order its items
// are drawn in, left to right.
//
// It returns nil where the order is the logical one, which is what every line of
// a left-to-right document gets and what the caller treats as "no reordering".
func lineVisualOrder(runs []inlineItem) []int {
	para := (*bidi.Paragraph)(nil)
	for _, item := range runs {
		if item.para != nil {
			para = item.para
			break
		}
	}
	if para == nil {
		return nil
	}

	// The line's extent in the paragraph. Every item on a line belongs to the
	// same bidi paragraph: a forced break ends both the line and the paragraph,
	// so the two can never be crossed by one line.
	start, end := -1, -1
	for _, item := range runs {
		if item.para != para {
			continue
		}
		if start < 0 || item.bidiStart < start {
			start = item.bidiStart
		}
		if item.bidiEnd > end {
			end = item.bidiEnd
		}
	}
	if start < 0 || end <= start {
		return nil
	}

	// Rule L1, which is why this is done per line and not once per paragraph: a
	// trailing space takes the paragraph's own direction so that it hangs on the
	// side the next word would have been on.
	lineLevels := para.LineLevels(start, end)

	// An item's level, or levelUnset where it has no characters to get one from:
	// an inline box's own inset, a float marker, the record of an absolutely
	// positioned box.
	//
	// The sentinel is the whole reason this is two passes. Zero is a real
	// embedding level — the left-to-right one — so an item left at zero because
	// nothing had filled it in is indistinguishable from one the algorithm put
	// there, and it lands in the middle of a right-to-left run as an
	// left-to-right island that the reordering then moves to the wrong end. That
	// is exactly what happened to the first item on a line: it had no previous
	// item to copy, so it kept the zero, and a margin at the start of a
	// right-to-left line was placed at the left of the line instead of beside the
	// box it belongs to.
	//
	// 255 cannot collide: UAX #9 caps an embedding level at MaxDepth + 1.
	const levelUnset uint8 = 255

	levels := make([]uint8, len(runs))
	for i, item := range runs {
		switch {
		case item.para == para && item.bidiStart-start < len(lineLevels):
			levels[i] = lineLevels[item.bidiStart-start]
		case item.insetLevelKnown:
			// An inline box's own margin, border and padding: no characters, and
			// so no level from the algorithm. insetSides worked out the level the
			// box's edges sit at from what is inside it.
			levels[i] = item.insetLevel
		default:
			levels[i] = levelUnset
		}
	}

	// Whatever is left has nothing to say about direction — a float marker, the
	// record of an absolutely positioned box, an inset whose box put no content
	// on this line — and takes the level of what precedes it, so that it never
	// splits a run in two.
	for i := range runs {
		if levels[i] != levelUnset {
			continue
		}
		if i > 0 {
			levels[i] = levels[i-1]
			continue
		}
		// The first item on the line, with nothing before it to copy. Rule L1
		// gives the paragraph's own embedding level to a line's leading and
		// trailing separators, and this is that position — so the base level is
		// what an item with no characters gets, rather than the zero that a
		// left-to-right paragraph happens to share with it.
		levels[i] = para.Level
	}

	plain := true
	for i := range levels {
		if levels[i] == levelUnset {
			// Nothing on the line has a level at all, which can only mean the
			// line holds no text. There is nothing to reorder.
			return nil
		}
		if levels[i] != levels[0] {
			plain = false
		}
	}
	if plain && levels[0]&1 == 0 {
		// One even level throughout: the line reads left to right and the
		// reordering is the identity.
		return nil
	}
	return bidi.Reorder(levels)
}
