package fonts

// The zero-width joiner and non-joiner: characters that say something about
// their neighbours and are themselves never drawn.
//
// U+200D ZERO WIDTH JOINER asks for the letters either side of it to be joined
// — in Devanagari, for an explicit half form where the font would otherwise
// have made a conjunct. U+200C ZERO WIDTH NON-JOINER asks for the opposite.
// Neither is a letter, neither has a shape, and both have to be obeyed.
//
// Those two facts pull in opposite directions, and getting either wrong is
// visible:
//
//   - The instruction cannot be obeyed unless the character is *there*. Cursive
//     joining reads them as join-causing and non-joining (arabic.go); the Indic
//     reordering reads a joiner after a virama as a demand for a half form and
//     a non-joiner as a refusal of one (indic.go). Dropping them early would
//     silently ignore what the writer asked for.
//   - Nothing may be drawn for them, and no rule of the font may be broken by
//     one standing in the way. A font's rule that measures what follows a vowel
//     sign is written about letters; a joiner between the sign and the letter
//     must not hide the letter from it, or the rule picks a plainer form than
//     its author meant.
//
// So a joiner is present while the rules that are about it run, invisible to
// the rules that are not, and gone before anything is positioned or drawn.
//
// # What is covered
//
// These two characters, and no others.
//
// Unicode's Default_Ignorable_Code_Point property covers far more — the
// bidirectional controls, the variation selectors, the soft hyphen — and every
// one of them should be treated exactly this way. They are not: text carrying
// one still gets whatever glyph the font happens to map it to, drawn. The two
// join controls are singled out because they are the ones that change what the
// font is *asked for*, which is the part that cannot be fixed by a caller
// stripping characters before shaping.

// The two join controls. They are named rather than derived because what they
// mean is particular to them: every other format character this package sees is
// simply passed through.
const (
	zeroWidthNonJoiner = 0x200C
	zeroWidthJoiner    = 0x200D
)

// joinerKind is what stands at a buffer position, as far as a lookup's rules
// for stepping over things are concerned.
type joinerKind uint8

const (
	notJoiner joinerKind = iota
	joinerZWJ
	joinerZWNJ
)

func joinerKindOf(r rune) joinerKind {
	switch r {
	case zeroWidthJoiner:
		return joinerZWJ
	case zeroWidthNonJoiner:
		return joinerZWNJ
	}
	return notJoiner
}

// stepsOverJoiner reports whether a lookup steps over the glyph at a buffer
// position because a join control stands there.
//
// The rule is asymmetric, and the asymmetry is the specification's rather than
// a simplification of it:
//
//   - Matching *context* — what a rule requires to precede or follow the glyphs
//     it replaces — always steps over a zero-width joiner, and steps over a
//     non-joiner unless the feature asked to see it. Context is a claim about
//     the letters around a rule, and a joiner is not a letter.
//   - Matching *input* — the glyphs a rule actually replaces — steps over a
//     joiner only where the feature allows it, and never steps over a
//     non-joiner. A non-joiner's whole purpose is to stop the letters either
//     side of it from being joined, and a rule that joined them by stepping
//     over it would do exactly what it was written to prevent.
//
// The Indic features are the ones that ask for the joiners to stay visible to
// their input: half forms and conjuncts are precisely what a joiner is written
// to force or forbid, so their lookups must see it. Everything else — the
// ligatures, the contextual alternates, all of positioning — treats them as
// though they were not there.
func (sh shaper) stepsOverJoiner(at int, context bool) bool {
	if sh.joinerAt == nil {
		return false
	}
	switch sh.joinerAt(at) {
	case joinerZWJ:
		return context || !sh.manualJoiners
	case joinerZWNJ:
		return context && !sh.manualJoiners
	}
	return false
}

// dropGlyphs removes the glyphs a predicate names, keeping the rest in order.
//
// The predicate is by *position* rather than by glyph index, which is the whole
// point: a face commonly maps a join control to the same glyph as the space, or
// to no glyph at all, and a pass that deleted every glyph with that index would
// delete the spaces of the text along with the joiners.
func dropGlyphs(buf []Glyph, drop func(i int) bool) []Glyph {
	n := 0
	for i := range buf {
		if drop(i) {
			continue
		}
		buf[n] = buf[i]
		n++
	}
	return buf[:n]
}

// hideJoiners removes the join controls of a run whose glyphs still correspond
// one for one to its characters.
//
// It is the general path's end of the story above: by the time it runs the
// joiners have chosen the letters' forms (applyJoining) and nothing after it is
// written about them, so they are taken out before any substitution can see
// them or any positioning can leave a gap for them.
//
// A run whose glyphs no longer line up with its characters is left alone. That
// cannot happen where this is called — nothing before it changes the buffer's
// length — and the guard is here so that moving the call would fail visibly
// rather than delete the wrong glyphs.
func hideJoiners(buf []Glyph, runes []rune) []Glyph {
	if len(runes) != len(buf) {
		return buf
	}
	return dropGlyphs(buf, func(i int) bool { return joinerKindOf(runes[i]) != notJoiner })
}
