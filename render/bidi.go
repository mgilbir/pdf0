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
// the range of runes within it.
// The paragraph number counts from one; see inlineItem.bidiPara.
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
	return out
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

	levels := make([]uint8, len(runs))
	plain := true
	for i, item := range runs {
		switch {
		case item.para != para:
			// An item with no text of its own — an out-of-flow marker that
			// reached the line — takes the level of what precedes it, so it
			// never splits a run.
			if i > 0 {
				levels[i] = levels[i-1]
			}
		case item.bidiStart-start < len(lineLevels):
			levels[i] = lineLevels[item.bidiStart-start]
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
