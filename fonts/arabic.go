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
// It is decidable from the characters alone, it needs no reordering, and its
// absence is not subtle: Arabic set without it is not merely ugly but hard to
// read, in a way a reader will notice immediately and a developer who does not
// read Arabic will not.
//
// # What this does not do
//
// It chooses forms. It does not reorder, which Indic scripts require — a vowel
// written after a consonant may belong before it — and it does not join the
// strokes themselves, which is cursive attachment (GPOS 3). Reordering is
// indic.go's, and the two are alternatives rather than stages: no script both
// joins cursively and reorders.

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
//
// A join-causing character takes forms too, and takes them the same way a
// dual-joining letter does. U+0640 TATWEEL is the one anybody writes: it is the
// stroke that stretches a word, it connects on both sides, and a font draws it
// differently at the start of a join than in the middle — Noto Sans Arabic
// carries uni0640.init and uni0640.medi for exactly that. Treating it as
// formless leaves it drawn as the isolated stroke, floating clear of the letters
// it is supposed to be joining.
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
		case joinD, joinC:
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
			// A character that cannot join has no positional form to select. A
			// space, a digit, a full stop: each has one shape, and naming a form
			// for it would say the font might have another.
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
// markJoiningForms records, on each glyph, which positional form its character
// takes from its neighbours.
//
// It only decides; nothing is substituted here. The two have to be separate
// because they happen at different moments and the run is a different shape at
// each. Deciding needs the characters, so it has to happen while the glyphs
// still correspond to them one for one — before any substitution. Substituting
// needs the glyphs the font's rules are written against, which in a real Arabic
// font are not the letters at all: Noto Sans Arabic composes 'ccmp' rules that
// split every letter into a skeleton and its dots, and states the four forms
// over the skeletons. A shaper that substitutes the forms first finds nothing to
// substitute, and every letter comes out in its isolated shape.
func markJoiningForms(buf []Glyph, runes []rune) {
	if len(runes) != len(buf) {
		// Nothing has been substituted yet where this is called, so this cannot
		// happen; the guard is here so that moving the call fails visibly rather
		// than assigning forms to the wrong glyphs.
		return
	}
	forms := joinForms(runes)
	for i := range buf {
		switch forms[i] {
		case featIsolated:
			buf[i].join = joinIsolated
		case featFinal:
			buf[i].join = joinFinal
		case featMedial:
			buf[i].join = joinMedial
		case featInitial:
			buf[i].join = joinInitial
		}
	}
}

// joinFormOrder is the order the four form features are applied in, which is
// the order every shaper applies them and the order the OpenType Arabic
// specification lists them.
//
// A glyph takes one form, so the order between them decides nothing on its own.
// It matters because a font may state a form as a contextual rule that looks at
// what its neighbours have already become.
var joinFormOrder = [...]joinForm{joinIsolated, joinFinal, joinMedial, joinInitial}

// applyJoiningForms substitutes each letter for the form its position calls for.
//
// The lookups are applied through the lookup list rather than read out of a
// flattened table of single substitutions, because a font may state a form as
// anything a lookup can be — a contextual rule, or a ligature that joins a
// letter to the one before it. Reading only the single substitutions gets the
// common case and silently drops the rest.
func (sh shaper) applyJoiningForms(buf []Glyph) []Glyph {
	for _, form := range joinFormOrder {
		lookups := sh.l.featureLookups[form.tag()]
		if len(lookups) == 0 {
			continue
		}
		for _, idx := range lookups {
			for i := 0; i < len(buf); {
				if buf[i].join != form {
					i++
					continue
				}
				consumed, out := sh.applyGSUBAt(idx, buf, i, 0)
				if consumed > 0 {
					buf = out
					i += consumed
					continue
				}
				i++
			}
		}
	}
	return buf
}

// HasJoiningForms reports whether the font carries the positional forms a
// cursive script needs. A caller can use it to tell a face that can set Arabic
// from one that merely has the letters.
func (f *Face) HasJoiningForms() bool {
	l := f.layout
	for _, form := range joinFormOrder {
		if len(l.featureLookups[form.tag()]) > 0 || len(l.single[form.tag()]) > 0 {
			return true
		}
	}
	return false
}
