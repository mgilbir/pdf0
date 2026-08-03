package fonts

import (
	"sort"
	"unicode"
)

// Cursive joining: choosing which of a letter's four shapes to draw.
//
// A cursive script — Arabic above all, and Syriac, Mongolian, Adlam and others
// — writes a letter differently depending on what it joins to. The font
// supplies the shapes and Unicode says which letters can join in which
// direction; putting the two together is what turns a row of disconnected
// letterforms into writing.
//
// This is the one piece of script-specific shaping here, and it is worth being
// clear about why it is the one. It is decidable from the characters alone, it
// needs no reordering, and its absence is not subtle: Arabic set without it is
// not merely ugly but hard to read, in a way a reader will notice immediately
// and a developer who does not read Arabic will not.
//
// # What this does not do
//
// It chooses forms. It does not reorder, which Indic scripts require — a vowel
// written after a consonant may belong before it — and it does not join the
// strokes themselves, which is cursive attachment (GPOS 3). Text in an Indic
// script is not correctly set by this package.

// joiningType is what a character can join to.
type joiningType uint8

const (
	joinU joiningType = iota // non-joining
	joinL                    // joins to the left only
	joinR                    // joins to the right only
	joinD                    // dual-joining: both sides
	joinC                    // join-causing: joins neighbours without a shape of its own
	joinT                    // transparent: skipped, and does not break a join
)

// joiningTypeOf reports a character's joining type.
//
// A character the table does not name is non-joining, except a non-spacing
// mark, which is transparent — a vowel sign written between two letters must
// not break their join, and treating it as an ordinary character would.
func joiningTypeOf(r rune) joiningType {
	i := sort.Search(len(joiningRanges), func(i int) bool { return joiningRanges[i].hi >= r })
	if i < len(joiningRanges) && r >= joiningRanges[i].lo {
		return joiningRanges[i].t
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return joinT
	}
	return joinU
}

// The features that name each form. A font declares a lookup under each,
// mapping the isolated letter to the shape for that position.
const (
	featIsolated = "isol"
	featInitial  = "init"
	featMedial   = "medi"
	featFinal    = "fina"
)

// joinForms decides, for each character of a run, which form feature applies.
//
// The rule is symmetric and worth stating in one place. A character joins to
// what precedes it when that neighbour can join *forwards* — it is dual-joining
// or left-joining or join-causing — and joins to what follows when that
// neighbour can join *backwards*. Transparent characters are skipped in both
// directions, which is the whole reason they have a type of their own.
//
// What a character does with those two facts depends on what it can do at all:
// a dual-joining letter takes any of the four forms, a right-joining one only
// isolated or final, a left-joining one only isolated or initial.
func joinForms(runes []rune) []string {
	types := make([]joiningType, len(runes))
	for i, r := range runes {
		types[i] = joiningTypeOf(r)
	}
	forms := make([]string, len(runes))
	for i := range runes {
		if types[i] == joinT {
			continue // a transparent character takes no form of its own
		}
		joinsPrev := false
		for j := i - 1; j >= 0; j-- {
			if types[j] == joinT {
				continue
			}
			joinsPrev = types[j] == joinD || types[j] == joinL || types[j] == joinC
			break
		}
		joinsNext := false
		for j := i + 1; j < len(runes); j++ {
			if types[j] == joinT {
				continue
			}
			joinsNext = types[j] == joinD || types[j] == joinR || types[j] == joinC
			break
		}
		switch types[i] {
		case joinD:
			switch {
			case joinsPrev && joinsNext:
				forms[i] = featMedial
			case joinsPrev:
				forms[i] = featFinal
			case joinsNext:
				forms[i] = featInitial
			default:
				forms[i] = featIsolated
			}
		case joinR:
			if joinsPrev {
				forms[i] = featFinal
			} else {
				forms[i] = featIsolated
			}
		case joinL:
			if joinsNext {
				forms[i] = featInitial
			} else {
				forms[i] = featIsolated
			}
		default:
			// A character that cannot join has no positional form to select.
			// A space, a digit, a full stop: each has one shape, and naming a
			// form for it would say the font might have another. Join-causing
			// characters are here too — a tatweel joins its neighbours and is
			// itself only ever drawn one way.
		}
	}
	return forms
}

// applyJoining substitutes each glyph for the shape its position calls for.
//
// It runs before every other substitution, because the joined forms are what
// the ligatures and contextual rules of a cursive script are written against —
// a font's lam-alef ligature is between the *final* lam and the alef, not
// between their isolated shapes.
//
// A font that declares none of the four features is left alone, which is every
// font for a script that does not join.
func (sh shaper) applyJoining(buf []Glyph, runes []rune) []Glyph {
	l := sh.l
	if len(l.single[featInitial]) == 0 && len(l.single[featMedial]) == 0 &&
		len(l.single[featFinal]) == 0 && len(l.single[featIsolated]) == 0 {
		return buf
	}
	if len(runes) != len(buf) {
		// Substitution has already changed the run, so the characters no longer
		// line up with the glyphs. Joining must come first; this is a guard
		// rather than a case to handle.
		return buf
	}
	forms := joinForms(runes)
	for i := range buf {
		if forms[i] == "" {
			continue
		}
		if to, ok := l.single[forms[i]][buf[i].GID]; ok {
			buf[i].GID = to
			buf[i].XAdvance = sh.f.advanceGID(to)
		}
	}
	return buf
}

// HasJoiningForms reports whether the font carries the positional forms a
// cursive script needs. A caller can use it to tell a face that can set Arabic
// from one that merely has the letters.
func (f *Face) HasJoiningForms() bool {
	l := f.layout
	return len(l.single[featInitial]) > 0 || len(l.single[featMedial]) > 0 ||
		len(l.single[featFinal]) > 0 || len(l.single[featIsolated]) > 0
}
