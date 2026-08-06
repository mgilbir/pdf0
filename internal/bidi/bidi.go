// Package bidi implements the Unicode Bidirectional Algorithm, UAX #9.
//
// # What it is for, and why an approximation is not an option
//
// Text in Hebrew, Arabic, Syriac or Thaana is stored in the order it is read and
// drawn in the opposite one, and a document that mixes it with Latin, with
// digits, or with punctuation is not two runs laid end to end: the boundaries
// between the directions are decided by rules with real content in them. A digit
// after an Arabic word runs left to right; a digit after a Hebrew word runs left
// to right *and* takes a different level from one after a Latin word; a comma
// between two numbers joins them and a comma between a number and a letter does
// not; a parenthesis takes its direction from what is inside it rather than from
// what precedes it.
//
// None of that is recoverable from "reverse the right-to-left parts". A
// reversal-based renderer sets "‏רחוב 42‏" as "24 בוחר", which is wrong in a way
// that no reftest in this repository would catch and that any reader of the
// script sees immediately. So this is the algorithm, and it is checked against
// Unicode's own conformance data rather than against a reading of it — see
// conformance_test.go.
//
// # The shape of it
//
// The algorithm runs over one *paragraph*, and produces an embedding level per
// character: even levels run left to right, odd ones right to left. Reordering is
// then applied per *line*, after the line breaking, because where a line ends
// decides which trailing spaces are reset (L1) and what range is reversed (L2).
// That split is why this package hands back levels rather than reordered text:
// the caller breaks lines, and only the caller knows where they broke.
//
//	Resolve      P2, P3, X1-X10, W1-W7, N0-N2, I1, I2 — one paragraph
//	LineLevels   L1 — one line of that paragraph
//	Reorder      L2 — the visual order of one line
//	Mirror       L4 — the character drawn in place of a bracket in an RTL run
//
// # Bounds
//
// The explicit formatting codes nest, and a document can nest them to any depth
// it likes. UAX #9 caps the embedding level at MaxDepth, and everything past it
// is counted as overflow and dropped rather than pushed — so the state this keeps
// is a fixed 127 entries however deep the input goes. Without the cap a
// megabyte of U+202B is a stack the size of the document. BD16's bracket stack is
// bounded the same way, at 63 pairs, which is the number the specification
// states.
//
// Everything else is linear in the length of the paragraph. That is not
// automatic: the obvious reading of N0 scans backwards from each bracket pair for
// the strong type before it, which is quadratic on "(a)(a)(a)..." — so the strong
// context is precomputed in one pass instead. The one place work is superlinear
// is inspecting the inside of nested bracket pairs, which the depth bound caps at
// 64 passes over the text.
package bidi

import "sort"

// Class is a Unicode Bidi_Class value: what the algorithm knows about a
// character.
//
// The order matches cmd/genbidi's classNames, and the generated table names these
// constants directly.
type Class uint8

const (
	// The strong types. AL is Arabic letters, which differ from R in what they
	// do to a following digit — W2 makes it an Arabic number rather than a
	// European one, and the two take different levels.
	L Class = iota
	R
	AL

	// The weak types: numbers and the punctuation that joins them.
	EN
	ES
	ET
	AN
	CS
	NSM
	BN

	// The neutrals.
	B
	S
	WS
	ON

	// The explicit formatting codes. The first five are the embeddings and
	// overrides, which X9 removes; the last four are the isolates, which are
	// kept because they take part in the neutral rules.
	LRE
	RLE
	LRO
	RLO
	PDF
	LRI
	RLI
	FSI
	PDI
)

// MaxDepth is UAX #9's max_depth: the deepest embedding level the algorithm
// will produce. An explicit code that would exceed it is counted as overflow and
// otherwise ignored.
//
// It is a security bound as much as a conformance one. The explicit codes nest,
// the input is untrusted, and a directional-status stack that grew with the
// nesting would be a memory-exhaustion primitive costing three bytes of input per
// entry.
const MaxDepth = 125

// maxBracketPairs is BD16's bound on the bracket stack. An opening bracket found
// with the stack full stops BD16 for the rest of the isolating run sequence,
// which is what the specification says to do — the alternative, growing the
// stack, is the same unbounded primitive as the depth cap prevents.
const maxBracketPairs = 63

// Direction is the base direction a paragraph is laid out in.
type Direction int8

const (
	// Auto is rules P2 and P3: the direction of the first strong character,
	// left to right if there is none. It is what "unicode-bidi: plaintext"
	// asks for, and what a paragraph whose direction nobody stated gets.
	Auto Direction = iota
	// LeftToRight and RightToLeft are what the CSS direction property sets:
	// paragraph embedding level 0 and 1 respectively.
	LeftToRight
	RightToLeft
)

// Paragraph is one resolved bidi paragraph.
//
// It holds a level per character and the character's original class, because L1
// is stated in terms of the classes as they were *before* the algorithm ran: a
// space that resolved to a right-to-left level still resets at the end of a line.
type Paragraph struct {
	// Level is the paragraph embedding level, 0 or 1.
	Level uint8

	levels []uint8
	orig   []Class
}

// Levels returns the resolved embedding level of each character, in logical
// order. The slice is the paragraph's own and must not be modified.
func (p *Paragraph) Levels() []uint8 { return p.levels }

// Len is the number of characters resolved.
func (p *Paragraph) Len() int { return len(p.levels) }

// Resolve runs the algorithm over one paragraph.
//
// The text is one paragraph and not a document: rule P1 splits text into
// paragraphs at a paragraph separator, and that split belongs to the caller,
// which in a layout engine has already made it — a forced line break ends a bidi
// paragraph, and the engine knows where those are long before this is called.
func Resolve(text []rune, dir Direction) *Paragraph {
	n := len(text)
	p := &Paragraph{orig: make([]Class, n), levels: make([]uint8, n)}
	for i, r := range text {
		p.orig[i] = ClassOf(r)
	}

	// BD9, once: which PDI closes which isolate initiator. P2, X5c and BD13 all
	// need it, and all three would otherwise rescan.
	pdi, initiator := matchIsolates(p.orig)

	switch dir {
	case LeftToRight:
		p.Level = 0
	case RightToLeft:
		p.Level = 1
	default:
		p.Level = firstStrongLevel(p.orig, pdi, 0, n)
	}

	// The types the rules work on, which the overrides and the weak and neutral
	// rules rewrite. The originals are kept for L1 and for BD13, both of which
	// are stated against the text as it was written.
	types := make([]Class, n)
	copy(types, p.orig)

	explicitLevels(p.orig, types, p.levels, p.Level, pdi)

	// X9. The embeddings, overrides and pops are removed from the rules'
	// view — but not from the text, which still has to be drawn and measured, so
	// they are filtered out by index rather than deleted.
	retained := make([]int32, 0, n)
	for i := 0; i < n; i++ {
		if !removedByX9(p.orig[i]) {
			retained = append(retained, int32(i))
		}
	}

	for _, seq := range isolatingRunSequences(retained, p.levels, p.orig, pdi, initiator, p.Level) {
		seq.resolveWeak(types)
		seq.resolveBrackets(types, p.orig, text)
		seq.resolveNeutral(types)
		seq.resolveImplicit(types, p.levels)
	}

	// UAX #9 §5.2: a character X9 removed is given the level of the character
	// before it, so that it never splits a run in two. It marks no paper, so any
	// other answer would put a visible seam in the middle of a word for the sake
	// of a control code.
	level := p.Level
	for i := 0; i < n; i++ {
		if removedByX9(p.orig[i]) {
			p.levels[i] = level
			continue
		}
		level = p.levels[i]
	}
	return p
}

// LineLevels is rule L1 over one line of the paragraph: the levels to reorder
// that line by.
//
// L1 is separate from Resolve because it depends on where the line ends, which
// is decided by line breaking and not by the text. A trailing space takes the
// paragraph's own direction so that it hangs on the correct side, and so does a
// tab and everything before it — otherwise a line of Hebrew ending in a space
// would put that space at the left of the line, where nothing follows it.
//
// The range is in characters of the paragraph. The returned slice is fresh and
// the caller may keep it.
func (p *Paragraph) LineLevels(start, end int) []uint8 {
	if start < 0 {
		start = 0
	}
	if end > len(p.levels) {
		end = len(p.levels)
	}
	if start >= end {
		return nil
	}
	out := make([]uint8, end-start)
	copy(out, p.levels[start:end])

	// Backwards, because clauses 3 and 4 are both "a run of white space ending
	// at" something — the end of the line, or a separator. One reverse scan
	// answers both: the flag says a run met now would reach one of them.
	trailing := true
	for i := end - 1; i >= start; i-- {
		switch t := p.orig[i]; {
		case t == B || t == S:
			// Clauses 1 and 2. A segment or paragraph separator always resets,
			// and white space before it resets too, which is what clause 3 says.
			out[i-start] = p.Level
			trailing = true
		case t == WS || isIsolateFormatting(t) || removedByX9(t):
			// §5.2 puts the removed formatting characters in with the white
			// space here: they mark no paper, so a run of spaces with a PDF in
			// the middle of it is still a run of spaces.
			if trailing {
				out[i-start] = p.Level
			}
		default:
			trailing = false
		}
	}
	return out
}

// Reorder is rule L2: the visual order of a line, given its levels.
//
// The result maps a visual position to the logical position of the character
// that goes there, so drawing result[0], result[1] ... left to right sets the
// line. Levels are the line's own — LineLevels produces them — and the slice is
// indexed by position within the line rather than within the paragraph.
func Reorder(levels []uint8) []int {
	order := make([]int, len(levels))
	for i := range order {
		order[i] = i
	}
	if len(levels) == 0 {
		return order
	}

	highest, lowestOdd := 0, MaxDepth+2
	for _, l := range levels {
		if int(l) > highest {
			highest = int(l)
		}
		if l&1 == 1 && int(l) < lowestOdd {
			lowestOdd = int(l)
		}
	}
	// From the highest level down to the lowest odd one, reverse any contiguous
	// run at that level or above. The scan is over the *levels*, which stay in
	// logical order throughout; it is the order array that is reversed under
	// them, which is what makes the nesting come out right.
	for level := highest; level >= lowestOdd; level-- {
		for i := 0; i < len(levels); i++ {
			if int(levels[i]) < level {
				continue
			}
			j := i
			for j < len(levels) && int(levels[j]) >= level {
				j++
			}
			reverse(order[i:j])
			i = j - 1
		}
	}
	return order
}

func reverse(s []int) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// Needed reports whether a string needs the algorithm at all under a
// left-to-right base direction.
//
// It is the fast path, and it is worth having: almost every paragraph a document
// generator sets is Latin text in a left-to-right block, where every level is
// zero and the visual order is the logical one. The test beside this pins the
// bound the scan relies on — that no character below U+0590 is right-to-left,
// Arabic-numeric, or an explicit formatting code — against the generated table,
// so a future Unicode that broke it would fail rather than silently skip the
// algorithm on text that needed it.
func Needed(s string) bool {
	for _, r := range s {
		if r < 0x0590 {
			continue
		}
		switch ClassOf(r) {
		case R, AL, AN, LRE, RLE, LRO, RLO, PDF, LRI, RLI, FSI, PDI:
			return true
		}
	}
	return false
}

// ClassOf returns a character's Bidi_Class.
func ClassOf(r rune) Class {
	// Bisection over the ranges that are not left-to-right. Absence is the
	// answer for most of the code space, including the whole of Latin.
	i := sort.Search(len(classRanges), func(i int) bool { return classRanges[i].hi >= r })
	if i < len(classRanges) && classRanges[i].lo <= r {
		return classRanges[i].class
	}
	return L
}

// Mirror is rule L4: the character drawn in place of this one in a right-to-left
// run, if there is one.
//
// It is applied at drawing time and not to the text, because the text is what a
// reader copies out of the page: a parenthesis written as "(" must extract as
// "(" however it was drawn.
func Mirror(r rune) (rune, bool) {
	i := sort.Search(len(mirrors), func(i int) bool { return mirrors[i].from >= r })
	if i < len(mirrors) && mirrors[i].from == r {
		return mirrors[i].to, true
	}
	return 0, false
}

// removedByX9 reports whether rule X9 takes a character out of the rules' view.
// The isolates are deliberately not here: X9 keeps them, and the neutral rules
// treat them as neutrals.
func removedByX9(c Class) bool {
	switch c {
	case RLE, LRE, RLO, LRO, PDF, BN:
		return true
	}
	return false
}

func isIsolateInitiator(c Class) bool { return c == LRI || c == RLI || c == FSI }

func isIsolateFormatting(c Class) bool { return isIsolateInitiator(c) || c == PDI }

// isNeutralOrIsolate is the "NI" of rules N0 to N2: the neutrals together with
// the isolate formatting characters, which resolve the same way.
func isNeutralOrIsolate(c Class) bool {
	switch c {
	case B, S, WS, ON, FSI, LRI, RLI, PDI:
		return true
	}
	return false
}

// noDirection is "counts as neither direction", for the several places that ask
// a character which way it leans and have to be able to hear "it does not".
//
// It is emphatically not zero. L is zero, and a zero sentinel here reads as
// "left to right" everywhere it is compared — which switched off every
// left-to-right override in rule X6 and made rule N1 treat a space as strong
// left-to-right text. Both were silent: the levels came out plausible and wrong,
// and it took Unicode's own conformance data to say so.
const noDirection Class = 0xFF

// strongOf maps a type to the direction it counts as in the neutral and bracket
// rules. Numbers count as right-to-left there, which is what keeps a quantity
// and its unit together in Hebrew.
func strongOf(c Class) Class {
	switch c {
	case L:
		return L
	case R, EN, AN:
		return R
	}
	return noDirection
}

// dirOf is the direction of a level: even runs left to right.
func dirOf(level uint8) Class {
	if level&1 == 1 {
		return R
	}
	return L
}

// matchIsolates is BD9: for each isolate initiator, the PDI that matches it, and
// for each PDI, the initiator it matches. Unmatched entries are -1.
//
// The scan is one pass with a stack of initiators. The stack can be as deep as
// the text is long — an isolate initiator is three bytes and pushes one entry —
// which is linear rather than bounded, and deliberately so: BD9 has no depth
// limit and matching is what X5c and BD13 are stated in terms of. The *levels*
// are what MaxDepth bounds, and that bound is in explicitLevels.
func matchIsolates(classes []Class) (pdi, initiator []int32) {
	n := len(classes)
	pdi = make([]int32, n)
	initiator = make([]int32, n)
	for i := range pdi {
		pdi[i], initiator[i] = -1, -1
	}
	var stack []int32
	for i := 0; i < n; i++ {
		switch {
		case isIsolateInitiator(classes[i]):
			stack = append(stack, int32(i))
		case classes[i] == PDI && len(stack) > 0:
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			pdi[open] = int32(i)
			initiator[i] = open
		}
	}
	return pdi, initiator
}

// firstStrongLevel is rules P2 and P3 over a range: the level implied by the
// first strong character, skipping anything inside an isolate.
//
// It serves X5c as well as P2, which is why it takes a range: a first-strong
// isolate takes its direction from the text it encloses, by the same rule.
func firstStrongLevel(classes []Class, pdi []int32, start, end int) uint8 {
	for i := start; i < end; i++ {
		switch c := classes[i]; {
		case c == L:
			return 0
		case c == R || c == AL:
			return 1
		case isIsolateInitiator(c):
			// P2 looks past an isolate entirely: what is inside it is a
			// paragraph of its own as far as direction is concerned. An isolate
			// that is never closed swallows the rest of the range, which is what
			// makes an unterminated FSI default rather than take the direction of
			// text it was never meant to describe.
			if p := pdi[i]; p >= 0 && int(p) < end {
				i = int(p)
				continue
			}
			return 0
		}
	}
	return 0
}

// status is one entry of the directional status stack of X1.
type status struct {
	level uint8
	// override is L or R where the stack entry overrides the characters under
	// it, and noDirection where it does not.
	override Class
	isolate  bool
}

// explicitLevels is rules X1 to X8: the level of every character, before the
// weak, neutral and implicit rules refine it.
//
// The stack is bounded by MaxDepth, which is the whole of the defence against a
// document that nests explicit codes a million deep: past the cap the counters
// take over, nothing is pushed, and the state stays 127 entries wide.
func explicitLevels(orig []Class, types []Class, levels []uint8, para uint8, pdi []int32) {
	stack := make([]status, 1, MaxDepth+2)
	stack[0] = status{level: para, override: noDirection}
	overflowIsolate, overflowEmbedding, validIsolate := 0, 0, 0

	// apply is X6's assignment, shared with the isolates: the character takes the
	// current level, and the current override if there is one.
	apply := func(i int) {
		top := stack[len(stack)-1]
		levels[i] = top.level
		if top.override != noDirection {
			types[i] = top.override
		}
	}

	for i := 0; i < len(orig); i++ {
		switch t := orig[i]; t {
		case RLE, LRE, RLO, LRO:
			// X2 to X5. The code itself takes the level in force *before* it, so
			// that a run it opens does not appear to start one character early.
			levels[i] = stack[len(stack)-1].level
			next := nextLevel(stack[len(stack)-1].level, t == RLE || t == RLO)
			override := noDirection
			switch t {
			case RLO:
				override = R
			case LRO:
				override = L
			}
			if next <= MaxDepth && overflowIsolate == 0 && overflowEmbedding == 0 {
				stack = append(stack, status{level: next, override: override})
			} else if overflowIsolate == 0 {
				overflowEmbedding++
			}

		case RLI, LRI, FSI:
			// X5a to X5c. An isolate is a character in its own right — unlike an
			// embedding, it takes part in the neutral rules — so it is assigned a
			// level and an override before the stack moves.
			apply(i)
			rtl := t == RLI
			if t == FSI {
				end := len(orig)
				if p := pdi[i]; p >= 0 {
					end = int(p)
				}
				rtl = firstStrongLevel(orig, pdi, i+1, end) == 1
			}
			next := nextLevel(stack[len(stack)-1].level, rtl)
			if next <= MaxDepth && overflowIsolate == 0 && overflowEmbedding == 0 {
				validIsolate++
				stack = append(stack, status{level: next, override: noDirection, isolate: true})
			} else {
				overflowIsolate++
			}

		case PDI:
			// X6a. An overflowing isolate is closed first, and only a valid one
			// pops — which is what stops a stray PDI unwinding an embedding it
			// has nothing to do with.
			switch {
			case overflowIsolate > 0:
				overflowIsolate--
			case validIsolate > 0:
				overflowEmbedding = 0
				for !stack[len(stack)-1].isolate {
					stack = stack[:len(stack)-1]
				}
				stack = stack[:len(stack)-1]
				validIsolate--
			}
			apply(i)

		case PDF:
			// X7. It pops at most one embedding, never an isolate: an isolate is
			// closed by its PDI and by nothing else, which is the whole point of
			// having isolates.
			levels[i] = stack[len(stack)-1].level
			switch {
			case overflowIsolate > 0:
			case overflowEmbedding > 0:
				overflowEmbedding--
			case !stack[len(stack)-1].isolate && len(stack) >= 2:
				stack = stack[:len(stack)-1]
				levels[i] = stack[len(stack)-1].level
			}

		case B:
			// X8. A paragraph separator can only be the last character of a
			// paragraph, and it takes the paragraph's own level.
			levels[i] = para

		case BN:
			// Removed by X9. Its level is filled in afterwards, from the
			// character before it, so that it never splits a run.
			levels[i] = stack[len(stack)-1].level

		default:
			apply(i)
		}
	}
}

// nextLevel is the least odd or least even level greater than the current one.
func nextLevel(current uint8, odd bool) uint8 {
	if odd {
		return (current + 1) | 1
	}
	return (current + 2) &^ 1
}

// sequence is one isolating run sequence, BD13: the level runs that an isolate
// initiator and its matching PDI join into a single stretch of text for the
// purposes of the weak and neutral rules.
//
// It is those rules' unit rather than the level run because an isolate is meant
// to be transparent to what surrounds it *structurally* while being opaque to it
// directionally: "a ⁧b⁩ c" is one sequence for the text outside the isolate,
// with the isolated part removed from it, so a number after the isolate still
// sees the letter before it.
type sequence struct {
	// idx are the paragraph indices of the characters in the sequence, in
	// logical order.
	idx []int32
	// level is the embedding level shared by every character in it.
	level uint8
	// sos and eos are the directions the text on either side counts as, which is
	// what the rules use where they would otherwise look past the sequence.
	sos, eos Class
}

// isolatingRunSequences builds every sequence of the paragraph, BD13 and X10.
func isolatingRunSequences(retained []int32, levels []uint8, orig []Class,
	pdi, initiator []int32, para uint8) []sequence {

	if len(retained) == 0 {
		return nil
	}
	// The level runs, as ranges of the retained slice.
	type levelRun struct{ start, end int }
	var runs []levelRun
	for i := 0; i < len(retained); {
		j := i + 1
		for j < len(retained) && levels[retained[j]] == levels[retained[i]] {
			j++
		}
		runs = append(runs, levelRun{i, j})
		i = j
	}
	// Which run a paragraph index belongs to, so that following an isolate
	// initiator to its PDI is a lookup rather than a search. One int32 per
	// character; the alternative is a scan per initiator, which is quadratic on
	// text that is mostly isolates.
	runOf := make([]int32, len(levels))
	for i := range runOf {
		runOf[i] = -1
	}
	for r, run := range runs {
		for k := run.start; k < run.end; k++ {
			runOf[retained[k]] = int32(r)
		}
	}

	out := make([]sequence, 0, len(runs))
	for r, run := range runs {
		first := retained[run.start]
		if orig[first] == PDI && initiator[first] >= 0 {
			// It continues the sequence its initiator started, so it is not the
			// beginning of one.
			continue
		}
		seq := sequence{level: levels[first]}
		lastRun := r
		for cur := r; ; {
			c := runs[cur]
			seq.idx = append(seq.idx, retained[c.start:c.end]...)
			lastRun = cur
			last := retained[c.end-1]
			if !isIsolateInitiator(orig[last]) || pdi[last] < 0 {
				break
			}
			next := runOf[pdi[last]]
			if next < 0 {
				break
			}
			// The matching PDI is always later in the text than its initiator,
			// so this walks forwards and terminates. It cannot land back on the
			// current run either: that would put the PDI after a run's last
			// character and inside the same run.
			cur = int(next)
		}

		// X10. Each end takes the direction of the higher of two levels: the
		// sequence's own, and the level of the text just outside it. An isolate
		// initiator with no matching PDI is compared against the paragraph level
		// instead, because there is no "just outside" for it — everything after
		// it is inside.
		before := para
		if run.start > 0 {
			before = levels[retained[run.start-1]]
		}
		seq.sos = dirOf(max8(seq.level, before))

		after := para
		lastIdx := seq.idx[len(seq.idx)-1]
		if end := runs[lastRun].end; end < len(retained) &&
			!(isIsolateInitiator(orig[lastIdx]) && pdi[lastIdx] < 0) {
			after = levels[retained[end]]
		}
		seq.eos = dirOf(max8(seq.level, after))

		out = append(out, seq)
	}
	return out
}

func max8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

// resolveWeak is rules W1 to W7, in order and in place.
func (s sequence) resolveWeak(types []Class) {
	// W1: a combining mark takes the type of what it is written on. A mark after
	// an isolate boundary has nothing to take, so it is other-neutral.
	prev := s.sos
	for _, i := range s.idx {
		if types[i] == NSM {
			if isIsolateFormatting(prev) {
				types[i] = ON
			} else {
				types[i] = prev
			}
		}
		prev = types[i]
	}

	// W2: a European number after an Arabic letter is an Arabic number. This is
	// the rule a reversal-based renderer has no way to express, and it is why
	// digits after Arabic and digits after Hebrew are laid out differently.
	strong := s.sos
	for _, i := range s.idx {
		switch types[i] {
		case L, R, AL:
			strong = types[i]
		case EN:
			if strong == AL {
				types[i] = AN
			}
		}
	}

	// W3: Arabic letters are right-to-left from here on; the distinction has
	// done its work in W2.
	for _, i := range s.idx {
		if types[i] == AL {
			types[i] = R
		}
	}

	// W4: a single separator between two numbers of the same kind joins them.
	// "1,234" is one number and "a,1" is not.
	for k := 1; k+1 < len(s.idx); k++ {
		t := types[s.idx[k]]
		if t != ES && t != CS {
			continue
		}
		before, after := types[s.idx[k-1]], types[s.idx[k+1]]
		switch {
		case before == EN && after == EN:
			types[s.idx[k]] = EN
		case t == CS && before == AN && after == AN:
			types[s.idx[k]] = AN
		}
	}

	// W5: a run of terminators touching a European number joins it — the "%" of
	// "50%" and the "$" of "$50".
	for k := 0; k < len(s.idx); {
		if types[s.idx[k]] != ET {
			k++
			continue
		}
		j := k
		for j < len(s.idx) && types[s.idx[j]] == ET {
			j++
		}
		beforeEN := k > 0 && types[s.idx[k-1]] == EN
		afterEN := j < len(s.idx) && types[s.idx[j]] == EN
		if beforeEN || afterEN {
			for m := k; m < j; m++ {
				types[s.idx[m]] = EN
			}
		}
		k = j
	}

	// W6: every separator and terminator that did not join a number is a
	// neutral.
	for _, i := range s.idx {
		switch types[i] {
		case ES, ET, CS:
			types[i] = ON
		}
	}

	// W7: a European number in left-to-right context is left-to-right, so
	// "abc 123" is one run and not two.
	strong = s.sos
	for _, i := range s.idx {
		switch types[i] {
		case L, R:
			strong = types[i]
		case EN:
			if strong == L {
				types[i] = L
			}
		}
	}
}

// bracketPair is one matched pair, as positions within the sequence.
type bracketPair struct{ open, close int }

// resolveBrackets is rule N0: a bracket pair takes one direction as a unit.
//
// Without it "he said “‏שלום‏”" comes out with its quotation marks the wrong way
// round, because each mark on its own is a neutral between text of two
// directions and N1 has no reason to prefer either.
func (s sequence) resolveBrackets(types, orig []Class, text []rune) {
	pairs := s.bracketPairs(types, text)
	if len(pairs) == 0 {
		return
	}
	e := dirOf(s.level)
	o := L
	if e == L {
		o = R
	}

	// The strong context before the current pair, kept by a pointer that only
	// moves forwards.
	//
	// N0 says to check backwards from each opening bracket for the first strong
	// type, and the obvious implementation does exactly that — which is quadratic
	// on "(a)(a)(a)…", a line an untrusted document can be made entirely of. It
	// cannot be precomputed either, because the rule is sequential: a pair this
	// rule has already resolved *is* strong context for the next one, which is why
	// the second pair of "(a)(b)" in a right-to-left paragraph follows the first
	// rather than the letter inside it.
	//
	// The sweep gets both. Pairs are processed in order of their opening bracket,
	// so the pointer never has to go back; and any assignment at a position the
	// pointer has yet to reach was made before it gets there, because a closing
	// bracket comes after the opening one that caused the assignment.
	sweep, lastStrong := 0, s.sos
	for _, p := range pairs {
		for ; sweep < p.open; sweep++ {
			if d := strongOf(types[s.idx[sweep]]); d != noDirection {
				lastStrong = d
			}
		}

		// The inside of the pair. Nesting is bounded by the bracket stack, so
		// the total work over all pairs is bounded by that depth times the
		// length — not by the square of it.
		foundE, foundO := false, false
		for k := p.open + 1; k < p.close; k++ {
			switch strongOf(types[s.idx[k]]) {
			case e:
				foundE = true
			case o:
				foundO = true
			}
			if foundE {
				break
			}
		}
		dir := noDirection
		switch {
		case foundE:
			dir = e
		case foundO:
			// Opposite-direction text inside. It keeps that direction only if
			// the context before the pair agrees; otherwise the pair follows its
			// embedding, so a parenthesis inside a Latin sentence stays with the
			// sentence.
			if lastStrong == o {
				dir = o
			} else {
				dir = e
			}
		default:
			// Nothing strong inside: N1 and N2 decide, as for any other
			// neutral.
			continue
		}
		types[s.idx[p.open]] = dir
		types[s.idx[p.close]] = dir
		// A combining mark on a bracket goes with it. W1 has already given it
		// the bracket's old type, so the original class is what identifies it.
		for _, at := range [2]int{p.open, p.close} {
			for k := at + 1; k < len(s.idx) && orig[s.idx[k]] == NSM; k++ {
				types[s.idx[k]] = dir
			}
		}
	}
}

// bracketPairs is BD16.
//
// The stack is bounded at maxBracketPairs and an opening bracket met with it
// full abandons the rule for the rest of the sequence, which is what the
// specification says to do. A document nesting parentheses ten thousand deep
// therefore costs a fixed 63 entries rather than one per character.
func (s sequence) bracketPairs(types []Class, text []rune) []bracketPair {
	type entry struct {
		ch  rune
		pos int
	}
	var stack []entry
	var pairs []bracketPair
	for k, i := range s.idx {
		if types[i] != ON {
			// BD16 looks only at characters that are still other-neutral: a
			// bracket that an override turned into a letter is not a bracket any
			// more.
			continue
		}
		paired, open, ok := bracketOf(text[i])
		if !ok {
			continue
		}
		if open {
			if len(stack) == maxBracketPairs {
				// Not an error and not a truncation of the text — the pairs
				// found so far stand, and the rest of the sequence's brackets
				// resolve as ordinary neutrals.
				break
			}
			stack = append(stack, entry{ch: paired, pos: k})
			continue
		}
		// A closing bracket matches the nearest opening one that wants it, and
		// discards anything opened since — "[(])" has one pair, not two.
		for d := len(stack) - 1; d >= 0; d-- {
			if !sameBracket(stack[d].ch, text[i]) {
				continue
			}
			pairs = append(pairs, bracketPair{open: stack[d].pos, close: k})
			stack = stack[:d]
			break
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].open < pairs[j].open })
	return pairs
}

// bracketOf returns the character that pairs with this one and whether this is
// the opening half.
func bracketOf(r rune) (paired rune, open, ok bool) {
	i := sort.Search(len(brackets), func(i int) bool { return brackets[i].ch >= r })
	if i < len(brackets) && brackets[i].ch == r {
		return brackets[i].paired, brackets[i].open, true
	}
	return 0, false, false
}

// sameBracket compares two brackets under canonical equivalence.
//
// BidiBrackets.txt states the one case this matters for: U+2329 and U+3008 are
// canonically equivalent, as are U+232A and U+3009, so an angle bracket opened
// with one and closed with the other is still a pair. Normalising the whole text
// to compare two characters would be a great deal of machinery for two pairs.
func sameBracket(a, b rune) bool {
	return canonicalBracket(a) == canonicalBracket(b)
}

func canonicalBracket(r rune) rune {
	switch r {
	case 0x3008:
		return 0x2329
	case 0x3009:
		return 0x232A
	}
	return r
}

// resolveNeutral is rules N1 and N2.
func (s sequence) resolveNeutral(types []Class) {
	e := dirOf(s.level)
	for k := 0; k < len(s.idx); {
		if !isNeutralOrIsolate(types[s.idx[k]]) {
			k++
			continue
		}
		j := k
		for j < len(s.idx) && isNeutralOrIsolate(types[s.idx[j]]) {
			j++
		}
		before := s.sos
		if k > 0 {
			before = strongOf(types[s.idx[k-1]])
		}
		after := s.eos
		if j < len(s.idx) {
			after = strongOf(types[s.idx[j]])
		}
		// N1 where the two sides agree, N2 where they do not. A neighbour that
		// counts as neither direction cannot happen — the run stops at the first
		// character that is not a neutral — but the check is here anyway, because
		// a noDirection that reached this comparison would read as "both sides say
		// left to right" and quietly apply N1 where N2 was meant.
		dir := e
		if before == after && before != noDirection {
			dir = before
		}
		for m := k; m < j; m++ {
			types[s.idx[m]] = dir
		}
		k = j
	}
}

// resolveImplicit is rules I1 and I2: the levels the reordering works on.
func (s sequence) resolveImplicit(types []Class, levels []uint8) {
	for _, i := range s.idx {
		if s.level&1 == 0 {
			switch types[i] {
			case R:
				levels[i] = s.level + 1
			case AN, EN:
				levels[i] = s.level + 2
			default:
				levels[i] = s.level
			}
			continue
		}
		switch types[i] {
		case L, EN, AN:
			levels[i] = s.level + 1
		default:
			levels[i] = s.level
		}
	}
}
