package fonts

import "sort"

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

// useInfo is what the model knows about one glyph, kept in step with the buffer
// as substitution reshapes it.
type useInfo struct {
	cat useCategory
	pos usePosition
}

// useSyllable is one cluster: the stretch of glyphs that are drawn together and
// that a font's rules are applied to as a unit.
type useSyllable struct{ start, end int }

// continuesSyllable reports whether a category can follow the base of a cluster.
//
// The engine's grammar states the tail in order — modifiers, then a stacked
// consonant, then medials, then vowels, then vowel modifiers, then finals — and
// a shaper that enforced that order would reject text it should merely set
// oddly. What matters for segmentation is where the cluster *ends*, which is at
// the first character that could begin one of its own.
func continuesSyllable(c useCategory) bool {
	switch c {
	case useCM, useSUB, useN, useHN, useM, useV, useVM, useF, useFM,
		useCGJ, useZWNJ, useRK, useSM:
		return true
	}
	return false
}

// stacksNext reports whether a category joins the letter after it into the same
// cluster: a halant, an invisible stacker, or the sakot.
func stacksNext(c useCategory) bool {
	return c == useH || c == useIS || c == useSk || c == useHVM
}

// useSyllables cuts a run into clusters.
//
// A cluster is a base and what hangs off it, optionally introduced by a repha or
// a stacking consonant. Anything that is part of no cluster — a space, a full
// stop, a Latin letter — is passed over, and the glyphs of it are left exactly
// as they are.
func useSyllables(info []useInfo) []useSyllable {
	var out []useSyllable
	for i := 0; i < len(info); {
		start := i
		// A repha or a consonant-with-stacker may come first, but only if a
		// base follows: on its own each is just a mark.
		if c := info[i].cat; c == useR || c == useCS {
			if i+1 < len(info) && isUseBase(info[i+1].cat) {
				i++
			}
		}
		if !isUseBase(info[i].cat) {
			i = start + 1
			continue
		}
		i++
		for i < len(info) {
			switch {
			case continuesSyllable(info[i].cat):
				i++
			case stacksNext(info[i].cat) && i+1 < len(info) && isUseBase(info[i+1].cat):
				i += 2
			case stacksNext(info[i].cat):
				// A stacker with nothing to stack still belongs to the letter
				// before it — it is what says that letter has no vowel.
				i++
			default:
				out = append(out, useSyllable{start, i})
				start = -1
			}
			if start < 0 {
				break
			}
		}
		if start >= 0 {
			out = append(out, useSyllable{start, i})
		}
	}
	return out
}

func isUseBase(c useCategory) bool { return c == useB || c == useGB || c == useN }

// The features the engine applies, in the order it applies them.
//
// They are in three groups because a pass runs between them. The first two are
// what a font uses to build the shapes of a cluster — the half forms, the
// subjoined consonants, the conjuncts — and they have to have run before the
// reordering, because the reordering moves what they produced. The last group is
// the typographic polish, applied to a cluster already in the order it is drawn.
var (
	useBasicFeatures = []string{"locl", "ccmp", "nukt", "akhn"}
	// 'pref' is applied on its own, because what it did has to be looked at
	// before anything else runs — see shapeUseSyllable.
	usePreBaseFeature = []string{"pref"}
	useShapeFeatures  = []string{
		"rphf", "rkrf", "abvf", "blwf", "half", "pstf", "vatu", "cjct",
	}
	useFinalFeatures = []string{
		"isol", "init", "medi", "fina", "abvs", "blws", "haln", "pres", "psts",
	}
	// And the substitutions that are not about this model at all: the required
	// ligatures and the contextual alternates every script gets, whatever
	// shaper set it. They are the same list the Khmer pass applies for the same
	// reason, and they go with the presentation features because they are
	// likewise written about what a reader sees rather than about syllables.
	//
	// The Javanese corpus does not exercise them: removing them changes none of
	// its 894 answers. They are here because a font that states a required
	// ligature under 'rlig' — which is what the feature is for — would otherwise
	// have it ignored, not because anything here has been seen to need it.
	useRunFeatures = []string{"rlig", "clig", "calt", "rclt", "liga"}
)

// shapeUniversal is the whole substitution pass for a run the engine handles.
func (sh shaper) shapeUniversal(buf []Glyph, runes []rune) []Glyph {
	info := make([]useInfo, len(runes))
	for i, r := range runes {
		info[i].cat, info[i].pos = useCategoryOf(r)
	}

	// Each cluster is shaped where it lies, and what it does to the buffer's
	// length shifts every cluster after it — so they are walked in order and the
	// shift carried along.
	shift := 0
	for _, syl := range useSyllables(info) {
		start, end := syl.start+shift, syl.end+shift
		if start < 0 || end > len(buf) || start >= end {
			continue
		}
		var delta int
		buf, delta = sh.shapeUseSyllable(buf, &info, start, end)
		shift += delta
	}
	// The presentation features, applied in lookup order rather than feature
	// order.
	//
	// Which comes first is the font's decision, not this list's: a font states
	// its rules in one lookup list, and their indices are the order it means
	// them in. Noto Sans Javanese relies on that. Its jha-keret ligature is
	// lookup 18 and the rule that turns that jha into a variant is lookup 26,
	// reached from 'blws'; taking the features in the order this list happens to
	// name them applies the variant first and the ligature can no longer match,
	// which costs exactly the two cases the corpus has of it.
	//
	// This is the one place the engine needs it. The rest of this package still
	// applies features in the order it names them, which is right wherever a
	// font's lookups do not overlap — and every other corpus here says they do
	// not.
	var lookups []int
	seen := map[int]bool{}
	for _, tag := range append(append([]string{}, useFinalFeatures...), useRunFeatures...) {
		for _, idx := range sh.l.featureLookups[tag] {
			if !seen[idx] {
				seen[idx] = true
				lookups = append(lookups, idx)
			}
		}
	}
	sort.Ints(lookups)
	if len(lookups) > 0 {
		buf, _ = sh.applyUseFeature(buf, &info, lookups, 0, len(buf))
	}
	return buf
}

// shapeUseSyllable shapes one cluster and reports how much longer or shorter it
// left the buffer.
func (sh shaper) shapeUseSyllable(buf []Glyph, info *[]useInfo, start, end int) ([]Glyph, int) {
	total := 0
	apply := func(tags []string) {
		for _, tag := range tags {
			lookups := sh.l.featureLookups[tag]
			if len(lookups) == 0 {
				continue
			}
			var d int
			buf, d = sh.applyUseFeature(buf, info, lookups, start, end)
			total, end = total+d, end+d
		}
	}
	apply(useBasicFeatures)

	// 'pref' is the font saying "this mark has a form that goes before the
	// letter". Which mark it said it about is not something the categories
	// know — it is the font's decision, made glyph by glyph — so the only way
	// to find out is to look at where the feature applied. Whatever it applied
	// to is treated from here on as a vowel written before the letter, because
	// that is what it now is: the reordering below moves it to the front.
	//
	// Where it *applied*, not where it changed the glyph. Noto Sans Javanese
	// states the rule as cakra → cakra, substituting a glyph for itself, and it
	// means it: the substitution is how the font marks the mark, and a reader
	// that looked for a changed glyph index would see nothing happen and leave
	// every cakra on the wrong side of its letter.
	for _, tag := range usePreBaseFeature {
		lookups := sh.l.featureLookups[tag]
		if len(lookups) == 0 {
			continue
		}
		var d, at int
		buf, d, at = sh.applyUseFeatureAt(buf, info, lookups, start, end)
		total, end = total+d, end+d
		if at >= 0 && at < len(*info) {
			(*info)[at].cat, (*info)[at].pos = useV, usePosPre
		}
	}

	apply(useShapeFeatures)
	reorderUseSyllable(buf, *info, start, end)
	return buf, total
}

// applyUseFeature runs one feature's lookups over one cluster, keeping the
// per-glyph record in step with what the lookups do to the buffer.
//
// The cluster is both the range walked and the range a lookup may look at:
// a rule reaching into the next cluster would join glyphs the font never meant
// to see together, which is the whole reason these features are applied a
// cluster at a time rather than over the run.
func (sh shaper) applyUseFeature(buf []Glyph, info *[]useInfo, lookups []int, start, end int) ([]Glyph, int) {
	out, delta, _ := sh.applyUseFeatureAt(buf, info, lookups, start, end)
	return out, delta
}

// applyUseFeatureAt is applyUseFeature, also reporting the first position a
// lookup applied at — which is how 'pref' is read. It is -1 when none did.
func (sh shaper) applyUseFeatureAt(buf []Glyph, info *[]useInfo, lookups []int, start, end int) ([]Glyph, int, int) {
	total, step, first := 0, 0, -1
	sh.onResize = func(at, d int) {
		*info = respliceUseInfo(*info, at, d)
		step += d
	}
	sh.floor, sh.limit = start, end
	for _, idx := range lookups {
		for i := start; i < end && i < len(buf); {
			step = 0
			consumed, out := sh.applyGSUBAt(idx, buf, i, 0)
			buf = out
			end += step
			total += step
			if consumed > 0 {
				if first < 0 {
					first = i
				}
				i += consumed
				continue
			}
			i++
		}
	}
	return buf, total, first
}

// respliceUseInfo keeps the per-glyph record the same length as the buffer.
//
// A lookup that shortened the run replaced several glyphs with one, which keeps
// the first record; one that lengthened it took a glyph apart, and every piece
// is what the whole was.
func respliceUseInfo(info []useInfo, at, delta int) []useInfo {
	switch {
	case delta == 0 || at < 0 || at >= len(info):
		return info
	case delta < 0:
		cut := -delta
		if at+1+cut > len(info) {
			cut = len(info) - at - 1
		}
		if cut <= 0 {
			return info
		}
		return append(info[:at+1], info[at+1+cut:]...)
	default:
		grown := make([]useInfo, 0, len(info)+delta)
		grown = append(grown, info[:at]...)
		for k := 0; k <= delta; k++ {
			grown = append(grown, info[at])
		}
		return append(grown, info[at+1:]...)
	}
}

// reorderUseSyllable puts a cluster's glyphs into the order they are drawn.
//
// Two things move, and they move in opposite directions:
//
//   - A repha is written before the letter it belongs to and drawn after it, so
//     it goes forward, to just before the first glyph that is drawn after the
//     base.
//   - A vowel written before the letter — the engine calls it a pre-base vowel
//     because of where it is *drawn*, not where it is written — goes back, to
//     the front of the cluster or to just after the last halant, since a halant
//     says the letter after it is the one the vowel belongs to.
//
// Only the first glyph of a decomposition moves. A vowel sign the font took
// apart has its pieces on different sides of the letter, and moving the lot
// would take the piece that belongs after the letter round to the front.
func reorderUseSyllable(buf []Glyph, info []useInfo, start, end int) {
	if start < 0 || end > len(buf) || end > len(info) || end-start < 2 {
		return
	}
	// The repha, forward.
	if info[start].cat == useR {
		to := start
		for i := start + 1; i < end; i++ {
			if isUseBase(info[i].cat) || stacksNext(info[i].cat) {
				to = i
				continue
			}
			break
		}
		if to > start {
			rotateUse(buf, info, start, start+1, to+1)
		}
	}
	// The pre-base vowels, back.
	at := start
	for i := start; i < end; i++ {
		switch {
		case stacksNext(info[i].cat):
			// What follows the halant is the letter the vowel belongs to, so
			// nothing before this point is a destination any more.
			at = i + 1
		case info[i].pos == usePosPre && (info[i].cat == useV || info[i].cat == useVM) &&
			buf[i].lig.comp == 0 && at < i:
			// The destination does not move afterwards. A second vowel written
			// before the letter therefore goes in front of the first, reversing
			// the two — which is what the model says and what looked like an
			// error worth "fixing" until HarfBuzz was asked: for a Balinese
			// letter carrying two of them it gives the same reversal.
			rotateUse(buf, info, at, i, i+1)
		}
	}
}

// rotateUse moves buf[mid:end] to the front of buf[start:end], keeping the order
// within each part and carrying the per-glyph record with it.
//
// Everything it moves becomes one cluster: the glyphs are no longer in the order
// their characters were written, so the smallest piece of text that can honestly
// be pointed at is the whole of what was rearranged.
func rotateUse(buf []Glyph, info []useInfo, start, mid, end int) {
	if start < 0 || start >= mid || mid >= end || end > len(buf) || end > len(info) {
		return
	}
	movedBuf := append([]Glyph(nil), buf[mid:end]...)
	movedInfo := append([]useInfo(nil), info[mid:end]...)
	copy(buf[start+len(movedBuf):end], buf[start:mid])
	copy(info[start+len(movedInfo):end], info[start:mid])
	copy(buf[start:], movedBuf)
	copy(info[start:], movedInfo)
	oneCluster(buf, start, end)
}

// universalScripts are the OpenType script tags the engine is used for.
//
// They are named rather than derived, and the list is the engine's
// specification's. Deriving it — "any script with characters of a complex
// category" — would sweep in Latin, whose combining marks have categories for
// Unicode's purposes and want none of this, and would make adding a script a
// silent change in what a document looks like rather than a decision.
//
// A script with a shaper of its own is not here: Devanagari and its relatives,
// Khmer and Myanmar are each modelled in their own file, because each has rules
// the general model does not carry.
var universalScripts = map[string]bool{
	"adlm": true, "ahom": true, "bali": true, "batk": true, "bhks": true,
	"brah": true, "bugi": true, "buhd": true, "cakm": true, "cham": true,
	"chrs": true, "cpmn": true, "diak": true, "dogr": true, "dupl": true,
	"egyp": true, "elym": true, "gong": true, "gonm": true, "gran": true,
	"hano": true, "hmng": true, "hmnp": true, "java": true, "kali": true,
	"kawi": true, "khar": true, "khoj": true, "kits": true, "kthi": true,
	"lana": true, "lepc": true, "limb": true, "mahj": true, "maka": true,
	"mand": true, "mani": true, "marc": true, "medf": true, "modi": true,
	"mong": true, "mtei": true, "mult": true, "nagm": true, "nand": true,
	"newa": true, "nko ": true, "ougr": true, "phag": true, "phlp": true,
	"plrd": true, "rjng": true, "rohg": true, "saur": true, "shrd": true,
	"sidd": true, "sind": true, "sogd": true, "sogo": true, "soyo": true,
	"sund": true, "sylo": true, "tagb": true, "takr": true, "tale": true,
	"talu": true, "tavt": true, "tfng": true, "tglg": true, "tibt": true,
	"tirh": true, "tnsa": true, "toto": true, "vith": true, "wcho": true,
	"yezi": true, "zanb": true,
}

// usesUniversalShaper reports whether a run's script is set by the engine.
func usesUniversalShaper(script uint16) bool {
	for _, tag := range scriptTags(script) {
		if universalScripts[tag] {
			return true
		}
	}
	return false
}
