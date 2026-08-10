// Package grapheme finds grapheme cluster boundaries, which is UAX #29's
// definition of where one user-perceived character ends and the next begins.
//
// It exists because CSS needs to know where text may be cut. CSS Text §2 defines
// a soft wrap opportunity as falling between "typographic character units", and
// says a typographic character unit is the grapheme cluster — so "break-all",
// which lets a line end between two characters rather than only between two
// words, is a question about grapheme clusters and about nothing else. Cutting
// anywhere else corrupts the text it was asked to fit: it separates a letter
// from the accent that belongs to it, splits a Hangul syllable into the jamo it
// is spelled with, halves a flag, or breaks a Devanagari conjunct in the middle.
//
// # Why not the shaper's clusters
//
// The obvious candidate is the cluster index a shaper returns, and it is not
// this. It was measured rather than assumed: forme's clusters are *finer* than
// grapheme clusters — a base with two combining marks breaks between the marks,
// a keycap breaks off its digit, conjoining Hangul breaks into three, a flag
// breaks into two letters — and in a right-to-left run they are not even in
// text order, because the glyphs come back in the order they are drawn. A
// position in the glyph stream is not a position in the string.
//
// So this works on the characters, which is also where it is needed: the line
// breaker splits a string and never sees a glyph.
//
// # What is implemented
//
// All of UAX #29's grapheme rules, GB1 through GB999, including the two that an
// implementation usually skips and that are exactly the ones that corrupt Indic
// and emoji text when they are missing: GB9c, which holds a consonant conjunct
// together across its virama, and GB11, which holds an emoji ZWJ sequence
// together. The tables are generated from the Unicode Character Database by
// cmd/gengrapheme and the whole of it is checked against Unicode's own
// GraphemeBreakTest.txt — see conformance_test.go.
package grapheme

import (
	"sort"
	"unicode/utf8"
)

// Break is a character's Grapheme_Cluster_Break property.
type Break uint8

// The Grapheme_Cluster_Break values. Other is the default.
const (
	Other Break = iota
	CR
	LF
	Control
	Extend
	ZWJ
	RegionalIndicator
	Prepend
	SpacingMark
	HangulL
	HangulV
	HangulT
	HangulLV
	HangulLVT
)

// conjunct is a character's Indic_Conjunct_Break property, which rule GB9c
// tests. None is the default.
type conjunct uint8

const (
	conjunctNone conjunct = iota
	conjunctConsonant
	conjunctExtend
	conjunctLinker
)

// asciiLimit is the code point below which the three properties are known
// without a table: everything under it is Other, except the two line
// terminators and the C0 controls.
//
// It is 0x300 rather than 0x80 because the first table entry that is not a
// control is U+00AD SOFT HYPHEN, and the run from there to the combining marks
// at U+0300 is entirely Other as well. Latin text therefore never bisects.
const asciiLimit = 0x300

// BreakOf returns a character's Grapheme_Cluster_Break.
func BreakOf(r rune) Break {
	if r < asciiLimit {
		switch {
		case r == '\r':
			return CR
		case r == '\n':
			return LF
		case r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) || r == 0xAD:
			return Control
		}
		return Other
	}
	i := sort.Search(len(breakRanges), func(i int) bool { return breakRanges[i].hi >= r })
	if i < len(breakRanges) && r >= breakRanges[i].lo {
		return breakRanges[i].val
	}
	return Other
}

// pictographic reports Extended_Pictographic, which rule GB11 tests.
func pictographic(r rune) bool {
	if r < 0xA9 { // the lowest character in the property
		return false
	}
	i := sort.Search(len(pictRanges), func(i int) bool { return pictRanges[i].hi >= r })
	return i < len(pictRanges) && r >= pictRanges[i].lo
}

// conjunctOf returns a character's Indic_Conjunct_Break.
func conjunctOf(r rune) conjunct {
	if r < asciiLimit {
		return conjunctNone
	}
	i := sort.Search(len(conjunctRanges), func(i int) bool { return conjunctRanges[i].hi >= r })
	if i < len(conjunctRanges) && r >= conjunctRanges[i].lo {
		return conjunctRanges[i].val
	}
	return conjunctNone
}

// props is everything the rules ask about one character, looked up once.
type props struct {
	br   Break
	cj   conjunct
	pict bool
}

func propsOf(r rune) props {
	// A character below the limit is Other, InCB=None and not pictographic, and
	// the great majority of the text this runs over is below it.
	if r < asciiLimit {
		return props{br: BreakOf(r)}
	}
	return props{br: BreakOf(r), cj: conjunctOf(r), pict: pictographic(r)}
}

// A scanner walks a string and answers, at each character, whether a cluster
// boundary falls before it.
//
// The state is what the rules need to see behind them, and each field is one
// rule's memory:
//
//   - prev is the character before, which most of the rules are stated over;
//   - ri counts the regional indicators immediately behind, because GB12 and
//     GB13 pair flags off from the start of the run and so turn on the parity
//     of that count rather than on the previous character alone;
//   - pict tracks how much of GB11's "pictographic, then any number of
//     extending marks, then a joiner" has been seen;
//   - conj tracks the same for GB9c's "consonant, then a linker, with only
//     extending marks and further linkers between".
//
// Two of those are why this cannot be a table of previous-class against
// next-class: the answer depends on a run behind the previous character, not
// just on the previous character.
type scanner struct {
	prev props
	ri   int
	pict pictState
	conj conjState
	set  bool // whether prev holds a character at all
}

type pictState uint8

const (
	pictNone   pictState = iota
	pictSeen             // Extended_Pictographic Extend*
	pictJoined           // Extended_Pictographic Extend* ZWJ
)

type conjState uint8

const (
	conjNone      conjState = iota
	conjConsonant           // Consonant [Extend Linker]*
	conjLinked              // Consonant [Extend Linker]* Linker [Extend Linker]*
)

// boundaryBefore reports whether a cluster boundary falls between the previous
// character and this one, and folds this one into the state.
//
// The rules are applied in the order UAX #29 states them, and the order is not
// cosmetic: GB4 and GB5 isolate the controls before GB9 could attach a mark to
// one, and GB9c and GB11 are tried before GB999 gives up.
func (s *scanner) boundaryBefore(r rune) bool {
	cur := propsOf(r)
	brk := s.decide(cur)
	s.advance(cur)
	return brk
}

func (s *scanner) decide(cur props) bool {
	if !s.set {
		return true // GB1: sot ÷ Any
	}
	p, c := s.prev, cur
	switch {
	case p.br == CR && c.br == LF:
		return false // GB3
	case p.br == Control || p.br == CR || p.br == LF:
		return true // GB4
	case c.br == Control || c.br == CR || c.br == LF:
		return true // GB5
	case p.br == HangulL && (c.br == HangulL || c.br == HangulV || c.br == HangulLV || c.br == HangulLVT):
		return false // GB6
	case (p.br == HangulLV || p.br == HangulV) && (c.br == HangulV || c.br == HangulT):
		return false // GB7
	case (p.br == HangulLVT || p.br == HangulT) && c.br == HangulT:
		return false // GB8
	case c.br == Extend || c.br == ZWJ:
		return false // GB9
	case c.br == SpacingMark:
		return false // GB9a
	case p.br == Prepend:
		return false // GB9b
	case s.conj == conjLinked && c.cj == conjunctConsonant:
		return false // GB9c
	case s.pict == pictJoined && c.pict:
		return false // GB11
	case p.br == RegionalIndicator && c.br == RegionalIndicator && s.ri%2 == 1:
		return false // GB12, GB13
	}
	return true // GB999
}

// advance folds a character into the state.
func (s *scanner) advance(cur props) {
	if cur.br == RegionalIndicator {
		s.ri++
	} else {
		s.ri = 0
	}

	// GB11's prefix. A pictographic starts one; extending marks continue it; a
	// joiner completes it; anything else ends it. Testing pictographic first
	// matters because a pictographic *after* a completed prefix starts a fresh
	// one rather than leaving the old one standing.
	switch {
	case cur.pict:
		s.pict = pictSeen
	case s.pict == pictSeen && cur.br == Extend:
		// unchanged
	case s.pict == pictSeen && cur.br == ZWJ:
		s.pict = pictJoined
	default:
		s.pict = pictNone
	}

	// GB9c's prefix, in the same shape. A linker is what promotes a consonant's
	// run to one that may attach to the next consonant.
	switch {
	case cur.cj == conjunctConsonant:
		s.conj = conjConsonant
	case s.conj != conjNone && cur.cj == conjunctLinker:
		s.conj = conjLinked
	case s.conj != conjNone && cur.cj == conjunctExtend:
		// unchanged
	default:
		s.conj = conjNone
	}

	s.prev = cur
	s.set = true
}

// A Scanner reports cluster boundaries one character at a time.
//
// It exists because the caller that needs this most — the line breaker — is
// already walking the text rune by rune, and a boundary *list* would cost an
// allocation proportional to the text for the common case where every character
// is its own cluster. A Scanner costs nothing and answers in constant time.
//
// The zero Scanner is ready to use and is positioned before the first character.
// One Scanner reads one string: reset it by assigning Scanner{}.
type Scanner struct {
	sc scanner
}

// Boundary reports whether a grapheme cluster boundary falls immediately before
// r, and advances past r.
//
// It is true for the first character of a string, which is a boundary in UAX
// #29's terms (rule GB1). A caller looking for the positions it may cut at
// should ignore that first answer, as Boundaries does.
func (s *Scanner) Boundary(r rune) bool { return s.sc.boundaryBefore(r) }

// Boundaries appends to dst the byte offsets *inside* s at which a grapheme
// cluster begins, in increasing order.
//
// The two ends are left out because they are not choices: every string starts
// and ends a cluster, so a caller looking for the places it may cut wants
// neither. The result is therefore empty for a string of one cluster, and dst is
// returned unchanged for the empty string.
//
// dst is appended to so a caller in a loop can reuse one buffer; pass nil for a
// fresh slice.
func Boundaries(dst []int, s string) []int {
	var sc scanner
	for i, r := range s {
		// An invalid byte is its own cluster on both sides. range yields
		// U+FFFD for one, which is Other and would let a following mark attach
		// to it; that would be a cluster spanning a byte that is not a
		// character, so it is cut instead.
		if r == utf8.RuneError {
			if _, n := utf8.DecodeRuneInString(s[i:]); n == 1 {
				if i > 0 {
					dst = append(dst, i)
				}
				sc = scanner{}
				continue
			}
		}
		if sc.boundaryBefore(r) && i > 0 {
			dst = append(dst, i)
		}
	}
	return dst
}

// Count returns the number of grapheme clusters in s.
func Count(s string) int {
	var sc scanner
	n := 0
	for _, r := range s {
		if sc.boundaryBefore(r) {
			n++
		}
	}
	return n
}
