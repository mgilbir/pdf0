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

// maxToUnicodeRange is the widest bfrange that is expanded. A range wider than
// this is skipped, as it always was; no real ToUnicode CMap comes near it.
const maxToUnicodeRange = 65536

// eachToUnicode calls fn with every code a ToUnicode stream maps and the UTF-16
// code units it maps to. A bfrange with a string destination increments the
// last code unit across the range (9.10.3); one with an array gives each code
// its own destination. fn must not keep units: the slice is reused.
func eachToUnicode(doc View, s *object.Stream, fn func(code int, units []uint16)) Reason {
	data, r := doc.Content(s)
	if r != ReasonOK {
		return r
	}
	scanCMap(doc.Cancel, data, cmapVisitor{
		bfChar: func(src cmapCode, dst bfDest) bool {
			if dst.valid && !dst.isArray {
				fn(int(src.v), utf16Units(dst.utf16))
			}
			return true
		},
		bfRange: func(lo, hi cmapCode, dst bfDest) bool {
			if !dst.valid || hi.v-lo.v >= maxToUnicodeRange {
				return true
			}
			if dst.isArray {
				for i, d := range dst.array {
					if uint64(i) > uint64(hi.v-lo.v) {
						break
					}
					fn(int(lo.v)+i, utf16Units(d))
				}
				return true
			}
			base := utf16Units(dst.utf16)
			if len(base) == 0 {
				return true
			}
			u := make([]uint16, len(base))
			for off := uint32(0); off <= hi.v-lo.v; off++ {
				copy(u, base)
				u[len(u)-1] += uint16(off)
				fn(int(lo.v+off), u)
			}
			return true
		},
	})
	return ReasonOK
}

// ParseToUnicodeMap parses a font's ToUnicode CMap into a map from character
// code to the first Unicode scalar value it produces. Used to tell whether a
// rendered glyph represents whitespace (ISO 32000-1 9.10.3). A code mapped to
// U+0000 is left out.
//
// The Reason is ReasonAbsent when the font has no ToUnicode stream, and the
// stream's decode outcome otherwise: a nil map with a declined Reason means
// pdf0 did not read the mappings, not that there are none.
func (doc View) ParseToUnicodeMap(fontDict *object.Dictionary) (map[int]rune, Reason) {
	s, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*object.Stream)
	if !ok {
		return nil, ReasonAbsent
	}
	m := map[int]rune{}
	if r := eachToUnicode(doc, s, func(code int, units []uint16) {
		if rs := decodeUnits(units); len(rs) > 0 && rs[0] != 0 {
			m[code] = rs[0]
		}
	}); r != ReasonOK {
		return nil, r
	}
	return m, ReasonOK
}

// ParseToUnicodeRunes parses a font's ToUnicode CMap into a map from character
// code to the full sequence of Unicode scalar values it produces.
//
// The Private Use Area rule needs the whole sequence: a destination is written
// as UTF-16BE, so a code point above the BMP arrives as a surrogate pair, and
// reading only the first unit reports the high surrogate D840 instead of
// U+10016D — a value that is in no Private Use Area, on a file that is in one.
//
// The Reason is ParseToUnicodeMap's.
func ParseToUnicodeRunes(doc View, fontDict *object.Dictionary) (map[int][]rune, Reason) {
	s, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*object.Stream)
	if !ok {
		return nil, ReasonAbsent
	}
	m := map[int][]rune{}
	if r := eachToUnicode(doc, s, func(code int, units []uint16) {
		if rs := decodeUnits(units); len(rs) > 0 {
			m[code] = rs
		}
	}); r != ReasonOK {
		return nil, r
	}
	return m, ReasonOK
}

// HasForbiddenUnicodeTargets reports whether a ToUnicode CMap maps any code to
// U+0000, U+FEFF or U+FFFE (ISO 19005-4 6.2.10.7) — in a bfchar destination,
// any destination of an array-form bfrange, or any value a string-form
// bfrange reaches by incrementing.
//
// Every destination is read as the CMap reader reads it. The scan this
// replaces took every third hex string of a bfrange section as a destination,
// so one array-form entry threw the count out of step and a source code was
// read as a target (audit 2026-09-22 C76).
//
// A stream that is not read finds nothing; its producer recorded any declined
// outcome.
func HasForbiddenUnicodeTargets(doc View, stream *object.Stream) bool {
	found := false
	_ = eachToUnicode(doc, stream, func(_ int, units []uint16) { // reason: presence-only; the producer recorded any declined trip
		for _, u := range units {
			if u == 0x0000 || u == 0xFEFF || u == 0xFFFE {
				found = true
			}
		}
	})
	return found
}

// decodeUnits decodes UTF-16 code units, pairing surrogates.
func decodeUnits(units []uint16) []rune {
	if len(units) == 0 {
		return nil
	}
	return utf16.Decode(units)
}
