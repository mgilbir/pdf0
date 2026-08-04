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
// Unicode's Default_Ignorable_Code_Point property, which covers far more than
// the two join controls: the bidirectional controls, the variation selectors,
// the word joiner, the musical beam marks, the soft hyphen. Every one of them
// is an instruction rather than a letter, and a font that maps one to a visible
// glyph is not asking for it to be drawn — it is saying what it would look like
// if it were, which is a question nobody asked.
//
// Getting this wrong is not subtle. A soft hyphen is written to mark where a
// word *may* break, and is the ordinary way HTML says so; before this was
// handled, a run carrying one had a hyphen drawn in the middle of the word,
// whether it broke there or not.
//
// The table is Unicode's (ignorabletable.go, generated). The two decisions
// below are this package's, and are the reason the two are kept apart.

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

// isDefaultIgnorable reports whether Unicode says nothing should be drawn for a
// character.
//
// The ranges are few and sorted, and the first of them is above almost every
// character of ordinary text, so the common answer costs one comparison.
func isDefaultIgnorable(r rune) bool {
	if r < defaultIgnorableRanges[0].lo {
		return false
	}
	lo, hi := 0, len(defaultIgnorableRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < defaultIgnorableRanges[mid].lo:
			hi = mid - 1
		case r > defaultIgnorableRanges[mid].hi:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// hiddenBeforeShaping reports whether a character is taken out before the font
// is asked about it at all.
//
// Every default-ignorable is, with two sets of exceptions, and both are about
// characters that would be *wrong* to remove rather than merely cheap to keep:
//
//   - The join controls. They are the two that change what the font is asked
//     for, so they have to survive until the rules about them have run; they
//     are taken out afterwards by hideJoiners and by the syllabic shapers. That
//     is the whole subject of this file.
//   - The Hangul fillers, U+115F, U+1160, U+3164 and U+FFA0. Unicode marks them
//     default-ignorable, and they are the one part of the property a text
//     renderer should not act on: they are letters (category Lo), used to write
//     an incomplete syllable — a jamo with a deliberately empty slot — and they
//     occupy width on the page. Hiding them collapses the syllable. HarfBuzz
//     excludes them for the same reason.
//
// Everything else goes before the buffer is built, which is after normalisation
// and so after U+034F COMBINING GRAPHEME JOINER has done the one thing it is
// for: standing between two characters to stop them composing.
//
// # Where this differs from HarfBuzz, and why
//
// HarfBuzz keeps these characters through shaping and hides them at the end.
// The two agree everywhere except one case: a character nothing is drawn for,
// written *inside* a cluster of a syllabic script — between a consonant and its
// virama, say. Removing it first leaves the syllable whole and the conjunct
// forms; keeping it breaks the syllable, and the orphaned virama then gets a
// dotted circle, the placeholder that says the text is malformed.
//
// Both are defensible and they differ only on malformed text. Unicode defines
// the property as characters that "should be ignored in rendering", which is
// what this does; HarfBuzz gives the syllable model the last word. The choice
// here puts a well-formed conjunct on the page rather than a dotted circle,
// because a document is written once and read many times and a reader cannot
// fix the text. The thirteen cases where it shows are listed, with this reason,
// in fonts/harfbuzz_test.go.
func hiddenBeforeShaping(r rune) bool {
	if !isDefaultIgnorable(r) {
		return false
	}
	switch r {
	case zeroWidthJoiner, zeroWidthNonJoiner:
		return false
	case hangulChoseongFiller, hangulJungseongFiller, hangulFiller, halfwidthHangulFiller:
		return false
	}
	return true
}

// hiddenAfterShaping reports whether a character is one nothing is drawn for
// that a syllabic shaper is nevertheless handed, and so has to take back out
// once it has done its work.
//
// The Hangul fillers are not among them for the reason they are excluded above:
// they are letters and they occupy width.
func hiddenAfterShaping(r rune) bool {
	if !isDefaultIgnorable(r) {
		return false
	}
	switch r {
	case hangulChoseongFiller, hangulJungseongFiller, hangulFiller, halfwidthHangulFiller:
		return false
	}
	return true
}

// dropHiddenCharacters removes the characters nothing is drawn for, keeping the
// rest of a run and the offsets that map it back to the text.
//
// It returns the input unchanged when there is nothing to drop, which is every
// ordinary string: the scan is one comparison per character against the lowest
// code point the property covers, and allocating a copy of every run to remove
// nothing would cost more than the property is worth.
func dropHiddenCharacters(runes []rune, offsets []int) ([]rune, []int) {
	first := -1
	for i, r := range runes {
		if hiddenBeforeShaping(r) {
			first = i
			break
		}
	}
	if first < 0 {
		return runes, offsets
	}
	outR := append(make([]rune, 0, len(runes)-1), runes[:first]...)
	outO := append(make([]int, 0, len(offsets)-1), offsets[:first]...)
	for i := first + 1; i < len(runes); i++ {
		if hiddenBeforeShaping(runes[i]) {
			continue
		}
		outR = append(outR, runes[i])
		outO = append(outO, offsets[i])
	}
	return outR, outO
}

// The Hangul fillers, named for the reason hiddenBeforeShaping gives.
const (
	hangulChoseongFiller  = 0x115F
	hangulJungseongFiller = 0x1160
	hangulFiller          = 0x3164
	halfwidthHangulFiller = 0xFFA0
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
