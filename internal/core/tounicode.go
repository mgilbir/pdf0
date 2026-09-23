package core

import (
	"unicode/utf16"

	"github.com/mgilbir/pdf0/object"
)

// ToUnicode CMaps (ISO 32000-2 9.10.3): what text a character code stands for.
// They are read by the same token-level CMap reader as the CID CMaps
// (cmapparse.go), so every consumer — the whitespace probe, the Private Use
// Area rule, the forbidden-target rule and text extraction — sees the same
// mapping.

// maxToUnicodeRange is the widest bfrange that is read. A range wider than
// this is skipped, as it always was; no real ToUnicode CMap comes near it.
const maxToUnicodeRange = 65536

// ToUnicode is a ToUnicode CMap, answered per code without being expanded.
//
// Its entries are kept as they were written — a bfchar is a range of one code
// — and resolved once into disjoint segments that each name the entry that
// answers for them, the last one written winning where entries overlap, as
// they did when every entry was written into a map in order (ResolveSpans).
// Expanding them was what a hostile file asked for: 2,000 copies of
// <0000> <FFFF> <0041> are 914 bytes asking for 131 million map writes, and
// text extraction spent four seconds and 32 GB of allocation on them (audit
// 2026-09-22 C53). Building this costs O(n log n) in the number of entries,
// charged to the run's work meter, and a lookup is a binary search.
type ToUnicode struct {
	entries []toUniEntry
	spans   Spans
}

// toUniEntry is one bfchar or bfrange entry: its first code, and either base
// (the string form: the destination of the first code, whose last UTF-16 unit
// is incremented across the range) or array (one destination per code).
type toUniEntry struct {
	lo, hi uint32
	base   []uint16
	array  [][]byte
}

// units is what entry e maps code (which it covers) to.
func (e *toUniEntry) units(code uint32) []uint16 {
	if e.array != nil {
		return utf16Units(e.array[code-e.lo])
	}
	u := make([]uint16, len(e.base))
	copy(u, e.base)
	u[len(u)-1] += uint16(code - e.lo)
	return u
}

// Runes is the full sequence of Unicode scalar values code maps to, and
// whether it maps to any. A mapping to no text is no mapping.
func (t *ToUnicode) Runes(code int) ([]rune, bool) {
	if t == nil || code < 0 {
		return nil, false
	}
	sp, ok := t.spans.Find(uint32(code))
	if !ok {
		return nil, false
	}
	rs := decodeUnits(t.entries[sp.Idx].units(uint32(code)))
	return rs, len(rs) > 0
}

// First is the first Unicode scalar value code maps to. A code mapped to
// U+0000 is reported as unmapped.
func (t *ToUnicode) First(code int) (rune, bool) {
	rs, ok := t.Runes(code)
	if !ok || rs[0] == 0 {
		return 0, false
	}
	return rs[0], true
}

// readToUnicode reads a ToUnicode stream's entries. The work is charged to the
// run's meter: the scan by the lexer, and each entry here.
func readToUnicode(doc View, s *object.Stream) (*ToUnicode, Reason) {
	data, r := doc.Content(s)
	if r != ReasonOK {
		return nil, r
	}
	t := &ToUnicode{}
	var ranges []Span
	add := func(e toUniEntry) {
		doc.Charge(1)
		t.entries = append(t.entries, e)
		ranges = append(ranges, Span{Lo: e.lo, Hi: e.hi})
	}
	scanCMap(doc.Cancel, data, cmapVisitor{
		bfChar: func(src cmapCode, dst bfDest) bool {
			if dst.valid && !dst.isArray {
				if u := utf16Units(dst.utf16); len(u) > 0 {
					add(toUniEntry{lo: src.v, hi: src.v, base: u})
				}
			}
			return true
		},
		bfRange: func(lo, hi cmapCode, dst bfDest) bool {
			if !dst.valid || hi.v < lo.v || hi.v-lo.v >= maxToUnicodeRange {
				return true
			}
			if dst.isArray {
				// One destination per code, as far as the array goes.
				if n := len(dst.array); n > 0 {
					last := lo.v + uint32(min(uint64(n-1), uint64(hi.v-lo.v)))
					add(toUniEntry{lo: lo.v, hi: last, array: dst.array})
				}
				return true
			}
			if base := utf16Units(dst.utf16); len(base) > 0 {
				add(toUniEntry{lo: lo.v, hi: hi.v, base: base})
			}
			return true
		},
	})
	t.spans = ResolveSpans(ranges, true)
	return t, ReasonOK
}

type toUnicodeSlot struct{}

// ParseToUnicode reads a font's ToUnicode CMap, memoised per stream for the
// run.
//
// The Reason is ReasonAbsent when the font has no ToUnicode stream, and the
// stream's decode outcome otherwise: a nil map with a declined Reason means
// pdf0 did not read the mappings, not that there are none.
func ParseToUnicode(doc View, fontDict *object.Dictionary) (*ToUnicode, Reason) {
	s, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*object.Stream)
	if !ok {
		return nil, ReasonAbsent
	}
	type parsed struct {
		t *ToUnicode
		r Reason
	}
	memo := Slot[map[*object.Stream]parsed](doc.Run, toUnicodeSlot{})
	if p, ok := (*memo)[s]; ok {
		doc.Charge(1)
		return p.t, p.r
	}
	t, r := readToUnicode(doc, s)
	if *memo == nil {
		*memo = map[*object.Stream]parsed{}
	}
	(*memo)[s] = parsed{t, r}
	return t, r
}

// HasForbiddenUnicodeTargets reports whether a ToUnicode CMap maps any code to
// U+0000, U+FEFF or U+FFFE (ISO 19005-4 6.2.10.7) — in a bfchar destination,
// any destination of an array-form bfrange, or any value a string-form
// bfrange reaches by incrementing.
//
// Every entry is asked, including one a later entry overrides: the rule is
// about what the CMap writes. A string-form range is asked arithmetically —
// which of its incremented last units is a forbidden value — rather than code
// by code.
//
// Every destination is read as the CMap reader reads it. The scan this
// replaces took every third hex string of a bfrange section as a destination,
// so one array-form entry threw the count out of step and a source code was
// read as a target (audit 2026-09-22 C76).
//
// A stream that is not read finds nothing; its producer recorded any declined
// outcome.
func HasForbiddenUnicodeTargets(doc View, stream *object.Stream) bool {
	t, r := readToUnicode(doc, stream)
	if r != ReasonOK {
		return false
	}
	forbidden := func(u uint16) bool { return u == 0x0000 || u == 0xFEFF || u == 0xFFFE }
	for i := range t.entries {
		e := &t.entries[i]
		if e.array != nil {
			for _, d := range e.array[:e.hi-e.lo+1] {
				doc.Charge(1)
				for _, u := range utf16Units(d) {
					if forbidden(u) {
						return true
					}
				}
			}
			continue
		}
		for _, u := range e.base[:len(e.base)-1] {
			if forbidden(u) {
				return true
			}
		}
		// The last unit takes base+0 … base+(hi-lo), modulo 2^16.
		last, span := e.base[len(e.base)-1], e.hi-e.lo
		for _, f := range []uint16{0x0000, 0xFEFF, 0xFFFE} {
			if uint32(f-last) <= span {
				return true
			}
		}
	}
	return false
}

// decodeUnits decodes UTF-16 code units, pairing surrogates.
func decodeUnits(units []uint16) []rune {
	if len(units) == 0 {
		return nil
	}
	return utf16.Decode(units)
}
