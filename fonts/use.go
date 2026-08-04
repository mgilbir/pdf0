package fonts

// The Universal Shaping Engine.
//
// Several dozen scripts are written the way the Indic ones are — a syllable
// stored in one order and drawn in another — without each having a shaper
// written for it. Javanese, Balinese, Buginese, Chakma, Tai Tham, Tibetan and
// the rest are covered by one model instead, and this is it.
//
// It is the same shape of work as indic.go, khmer.go and myanmar.go, and shares
// their machinery (syllabic.go). What it does not share is their per-script
// knowledge: those three encode what a Devanagari or Khmer or Myanmar syllable
// is, character by character. This one is told, by a table derived from
// Unicode's own properties, and so covers a script it has never heard of.

// useCategory is what a character is, as far as the syllable model is
// concerned.
//
// The names are the specification's. They are terse because they appear in the
// syllable grammar, where a longer name would obscure the shape of the rule
// rather than explain it.
type useCategory uint8

const (
	useO    useCategory = iota // other: not part of a syllable
	useB                       // base: a letter a syllable is built on
	useN                       // a Brahmi joining number
	useGB                      // a placeholder a mark can hang off
	useCGJ                     // transparent: the grapheme joiner, ZWJ, variation selectors
	useF                       // a final consonant
	useFM                      // a final modifier
	useM                       // a medial consonant
	useCM                      // a modifier: nukta, gemination, consonant killer
	useSUB                     // a subjoined consonant
	useCS                      // a consonant that stacks what follows
	useH                       // halant: kills the vowel and joins what follows
	useHVM                     // halant or vowel modifier, which Sinhala's is both of
	useHN                      // a number joiner
	useIS                      // an invisible stacker
	useZWNJ                    // the zero width non-joiner
	useRK                      // a reordering killer
	useR                       // a repha, drawn at the end of the syllable
	useSk                      // the sakot, which joins across a syllable break
	useSM                      // a symbol modifier
	useV                       // a dependent vowel
	useVM                      // a vowel modifier
	useWJ                      // a word joiner, and the unassigned code points
)

// usePosition is which side of the letter a mark is drawn, for the categories
// that have a side. It is what decides where a glyph is moved to.
type usePosition uint8

const (
	usePosNone usePosition = iota
	usePosPre              // before the letter
	usePosAbv              // above it
	usePosBlw              // below it
	usePosPst              // after it
)

// useRange is one run of characters the table gives the same answer for.
type useRange struct {
	lo, hi rune
	cat    useCategory
	pos    usePosition
}

// useCategoryOf reports what a character is and where its mark sits.
//
// The ranges are sorted and searched, and the first is above almost every
// character of ordinary text — so a Latin string is answered by one comparison
// per character.
func useCategoryOf(r rune) (useCategory, usePosition) {
	if r < useRanges[0].lo {
		return useO, usePosNone
	}
	lo, hi := 0, len(useRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < useRanges[mid].lo:
			hi = mid - 1
		case r > useRanges[mid].hi:
			lo = mid + 1
		default:
			return useRanges[mid].cat, useRanges[mid].pos
		}
	}
	return useO, usePosNone
}
