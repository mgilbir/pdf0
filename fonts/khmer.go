package fonts

import "sort"

// Khmer reordering.
//
// Khmer is written without spaces between syllables and with three things that
// are not drawn where they are stored:
//
//   - A *pre-base vowel sign*. េ (U+17C1) and its neighbours are written after
//     the consonant they belong to and drawn before it, exactly as a Devanagari
//     i-sign is.
//   - A *subscript consonant*. There is no visible virama: the coeng U+17D2
//     says "draw the letter after me under the one before me", and the font
//     supplies the subscript form. The coeng itself is never drawn.
//   - A *subscript Ro*. The one subscript that is not drawn under the base but
//     *before* it — the coeng and Ro together move to the front of the syllable
//     and are asked for the pre-base form, and everything that was between them
//     and the base is then asked for the after-Ro form.
//
// That third rule is why Khmer is its own shaper rather than a row in the Indic
// table. The Indic model finds a base consonant by asking the font which
// consonants it draws below or after one; Khmer has no such search — the base
// is simply the character the syllable opens with, and what moves is decided by
// the characters alone. Its features are its own too: 'cfar' exists in no other
// script, and there is no reph, no half form and no 'rphf' or 'half' anywhere
// in it.
//
// # What is covered
//
// The syllable grammar, the two reorderings, the five features the model names
// ('pref', 'blwf', 'abvf', 'pstf', 'cfar') applied to the parts of the syllable
// each is for, the presentation features, and the vowel signs written as one
// character and drawn as two.
//
// These are not:
//
//   - Ordering the features by the font's lookup order rather than by the
//     model's. The specification applies the five basic features as one step,
//     so a font that declares a single lookup under two of them, or that
//     declares them out of order, has its lookups run in the order this file
//     names rather than the order the font wrote them in. The same narrowing is
//     stated in indic.go, and for the same reason.
//   - Canonical ordering of the marks in a syllable. Unicode allows some of
//     them in either order; this package sets them in the order they were
//     written.
//
// # Where this runs
//
// In place of the default substitutions, from ShapeGlyphs, through
// shapeSyllabic. 'liga' is deliberately not applied: the Khmer specification
// names 'clig' rather than 'liga' as its ligature feature, and a font's
// discretionary Latin ligatures are not written about these glyphs.

// The Khmer characters the rules name directly.
const (
	// khmerCoeng is the sign that subscripts the letter after it. It is the
	// pivot of the whole model — every subscript consonant is one of these
	// followed by a letter — and it is never itself drawn.
	khmerCoeng = 0x17D2
	// khmerRo is the one consonant whose subscript form is drawn before the
	// base rather than under it, which is what 'pref' and 'cfar' are for.
	khmerRo = 0x179A
	// khmerPreVowel is the sign the split vowel signs are drawn with: each of
	// them is drawn as this sign before the letter and its own mark after.
	khmerPreVowel = 0x17C1
)

// maskCfar marks the glyphs the after-Ro feature is for: everything that
// followed a subscript Ro before it was moved to the front. It is a Khmer
// feature and has no counterpart in the Indic model, which is why it is stated
// here rather than beside the masks in indic.go.
const maskCfar uint8 = 1 << 6

// isKhmerScript reports whether a run is Khmer, by the OpenType tag a Khmer
// font declares its rules under.
func isKhmerScript(script uint16) bool { return scriptSelects(script, "khmr") }

// khmerRange is one stretch of characters sharing a shaping category.
type khmerRange struct {
	lo, hi rune
	cat    indicCat
}

// khmerCategories is what each Khmer character is within a syllable.
//
// It is stated here rather than derived from Unicode's Indic categories because
// Khmer disagrees with them about fifteen characters, and the disagreements are
// not a pattern: the visarga and one vowel sign are grouped with the tone marks
// that may follow a vowel, the two register shifters and the robat are grouped
// with the marks that may *precede* one, and the bindu is grouped with neither.
// Those groupings are what the grammar below is written in terms of, so they
// have to be the categories; deriving them and then overriding fifteen of them
// would state the same data less plainly.
//
// The names follow the script development specification: a sign is grouped by
// where it may stand in the syllable, not by what Unicode calls it.
var khmerCategories = [...]khmerRange{
	{0x1780, 0x1799, catConsonant},
	{0x179A, 0x179A, catRa}, // Ro, the one that is drawn before the base
	{0x179B, 0x17A2, catConsonant},
	{0x17A3, 0x17B3, catVowel},
	{0x17B6, 0x17B6, catVPst},
	{0x17B7, 0x17BA, catVAbv},
	{0x17BB, 0x17BD, catVBlw},
	{0x17BE, 0x17BE, catVAbv},
	{0x17BF, 0x17C0, catVPst},
	{0x17C1, 0x17C3, catVPre},
	{0x17C4, 0x17C5, catVPst},
	{0x17C6, 0x17C6, catXgroup},
	{0x17C7, 0x17C8, catYgroup},
	{0x17C9, 0x17CA, catRobatic},
	{0x17CB, 0x17CB, catXgroup},
	{0x17CC, 0x17CC, catRobatic},
	{0x17CD, 0x17D1, catXgroup},
	{0x17D2, 0x17D2, catStacker}, // the coeng
	{0x17D3, 0x17D3, catYgroup},
	{0x17D9, 0x17D9, catPlaceholder},
	{0x17DD, 0x17DD, catYgroup},
	{0x17E0, 0x17E9, catPlaceholder},
	{0x200C, 0x200C, catZWNJ},
	{0x200D, 0x200D, catZWJ},
	{dottedCircle, dottedCircle, catDottedCircle},
}

// khmerCategory reports what a character is within a Khmer syllable. Anything
// the table does not name is not part of one.
func khmerCategory(r rune) indicCat {
	i := sort.Search(len(khmerCategories), func(i int) bool { return khmerCategories[i].hi >= r })
	if i < len(khmerCategories) && r >= khmerCategories[i].lo {
		return khmerCategories[i].cat
	}
	return catOther
}

// khmerSplitVowels are the vowel signs written as one character and drawn as
// two marks, one on each side of the letter.
//
// Unicode gives none of them a canonical decomposition, so unlike the Indic
// split signs they cannot be generated: each is stated here, and each is drawn
// as the pre-base sign U+17C1 followed by *itself*. That is not a mistake —
// the font draws the second mark from the composed character's own glyph, and
// only the leading half has a character of its own.
var khmerSplitVowels = [...]rune{0x17BE, 0x17BF, 0x17C0, 0x17C4, 0x17C5}

// khmerSplitVowelOf reports the marks a Khmer vowel sign is drawn as, if it is
// one of the signs drawn as two.
func khmerSplitVowelOf(r rune) ([]rune, bool) {
	for _, s := range khmerSplitVowels {
		if s == r {
			return []rune{khmerPreVowel, r}, true
		}
	}
	return nil, false
}

// khmerBasicFeatures are applied to one syllable at a time, in this order, to
// the stretch of it each one's mask marks.
//
// The order is the specification's. 'pref' makes the pre-base form of a
// subscript Ro, 'blwf' and 'abvf' and 'pstf' the forms of the subscripts that
// stay where they were, and 'cfar' last because it is written about the glyphs
// a Ro left behind — it can only be asked once the Ro has gone.
var khmerBasicFeatures = []struct {
	tag  string
	mask uint8
}{
	{"pref", maskPref},
	{"blwf", maskBlwf},
	{"abvf", maskAbvf},
	{"pstf", maskPstf},
	{"cfar", maskCfar},
}

// khmerRunFeatures are applied to the whole run once every syllable is in
// drawing order.
//
// The first four turn the reordered pieces into the shapes a reader sees. They
// are written about the joiners, so their lookups see them; the last four are
// the script-independent substitutions, which are not and step over them.
//
// 'liga' is deliberately absent and 'clig' deliberately present: the Khmer
// specification names 'clig' as the feature that forms the ligatures
// typographical correctness needs, and says nothing about 'liga'.
var khmerRunFeatures = []struct {
	tag    string
	manual bool
}{
	{"pres", true},
	{"abvs", true},
	{"blws", true},
	{"psts", true},
	{"rlig", false},
	{"clig", false},
	{"calt", false},
	{"rclt", false},
}

// shapeKhmer is the whole Khmer pass: it replaces the default substitutions for
// a run it handles.
func (sh shaper) shapeKhmer(buf []Glyph, runes []rune) []Glyph {
	// The split vowel signs first, before anything is classified: their two
	// marks go to different places, so there is no single place the sign itself
	// could be given, and the run everything below is built from is the one
	// with the parts in it.
	buf, runes = sh.splitCharacters(buf, runes, khmerSplitVowelOf)

	info := make([]indicInfo, len(runes))
	cats := make([]indicCat, len(runes))
	for i, r := range runes {
		info[i].cat = khmerCategory(r)
		info[i].ignorable = hiddenAfterShaping(r)
		cats[i] = info[i].cat
	}

	// Each syllable is shaped where it lies, and what it does to the buffer's
	// length shifts every syllable after it — so the syllables are walked in
	// order and the shift carried along.
	shift := 0
	dotted, hasDotted := sh.f.GlyphID(dottedCircle)
	for _, syl := range khmerSyllables(cats) {
		if syl.kind == khmerNonKhmer {
			continue
		}
		start, end := syl.start+shift, syl.end+shift
		if syl.kind == khmerBroken && hasDotted {
			buf, info = sh.insertGlyphAt(buf, info, start, dotted,
				indicInfo{cat: catDottedCircle, pos: posBaseC})
			end++
			shift++
		}
		var delta int
		buf, delta = sh.shapeKhmerSyllable(buf, &info, start, end)
		shift += delta
	}

	for _, f := range khmerRunFeatures {
		lookups := sh.l.featureLookups[f.tag]
		if len(lookups) == 0 {
			continue
		}
		buf, _ = sh.applyIndicFeature(buf, &info, lookups, 0, len(buf), 0, len(buf), f.manual)
	}

	// The joiners have now done everything they are for. What is left is a
	// character with no shape, which must not reach the page.
	return dropGlyphs(buf, func(i int) bool {
		return i < len(info) && (indicIsJoiner(info[i].cat) || info[i].ignorable)
	})
}

// shapeKhmerSyllable puts one syllable into drawing order and applies the
// features written for its parts, returning the buffer and how much its length
// changed.
func (sh shaper) shapeKhmerSyllable(buf []Glyph, info *[]indicInfo, start, end int) ([]Glyph, int) {
	total := 0
	grow := func(d int) { total += d; end += d }

	// The reordering comes *first*, before 'locl' and 'ccmp', which is the one
	// place Khmer's order differs from the Indic model's. It can: nothing in the
	// reordering asks the font a question, so there is nothing for those two
	// features to have answered first.
	khmerReorder(buf, *info, start, end)

	// 'locl' corrects letterforms for the language and 'ccmp' composes and
	// decomposes; everything after them is written against what they produce.
	// They are applied per syllable, like the features below, so that neither
	// can join one syllable to the next.
	for _, tag := range []string{"locl", "ccmp"} {
		lookups := sh.l.featureLookups[tag]
		if len(lookups) == 0 {
			continue
		}
		var d int
		buf, d = sh.applyIndicFeature(buf, info, lookups, start, end, start, end, false)
		grow(d)
	}

	for _, f := range khmerBasicFeatures {
		lookups := sh.l.featureLookups[f.tag]
		if len(lookups) == 0 {
			continue
		}
		// A masked feature sees the stretch of the syllable its mask marks and
		// nothing else — not even as context. A lookup that reached past the
		// mask would join glyphs the font never meant to see together: 'pref'
		// is written about a subscript Ro and the two glyphs after it are the
		// base, which 'pref' says nothing about.
		for lo := start; lo < end; {
			if (*info)[lo].mask&f.mask == 0 {
				lo++
				continue
			}
			hi := lo + 1
			for hi < end && (*info)[hi].mask&f.mask != 0 {
				hi++
			}
			var d int
			buf, d = sh.applyIndicFeature(buf, info, lookups, lo, hi, start, end, true)
			grow(d)
			lo = hi + d
		}
	}

	oneCluster(buf, start, end)
	return buf, total
}

// khmerReorder puts one syllable into the order the font's rules are written
// against, and marks which glyphs each feature is for.
//
// There is only one reordering in Khmer, and it happens before any of the
// font's rules run. The Indic model needs a second one afterwards because where
// a reph goes depends on what the font made of it; Khmer has no reph, and both
// of the things that move here — a pre-base vowel sign and a subscript Ro — go
// to the same place, the front of the syllable, whatever the font does with
// them.
func khmerReorder(buf []Glyph, info []indicInfo, start, end int) {
	if start >= end {
		return
	}
	// Everything after the first character hangs off it, and so is asked for
	// its below-, above- and post-base form. The first character is the base
	// and is asked for none of them.
	for i := start + 1; i < end; i++ {
		info[i].mask |= maskBlwf | maskAbvf | maskPstf
	}

	// A syllable takes at most two subscripts, and the specification stops
	// looking after them: a third coeng is a sequence nobody writes, and the
	// count is what keeps it from being read as one.
	coengs := 0
	for i := start + 1; i < end; i++ {
		switch {
		case indicIsHalant(info[i].cat) && coengs <= 2 && i+1 < end:
			coengs++
			if info[i+1].cat != catRa {
				continue
			}
			// The coeng and the Ro are drawn before the base, so they are moved
			// to the front of the syllable and asked for the pre-base form.
			info[i].mask |= maskPref
			info[i+1].mask |= maskPref
			moveGlyphToFront(buf, info, start, i)
			moveGlyphToFront(buf, info, start+1, i+1)
			// What followed the Ro is drawn after it and is asked for the
			// after-Ro form. Those glyphs did not move, so their positions
			// still say where they are. It is what tells a font the difference
			// between a Ro subscripted under a letter and a letter subscripted
			// under a Ro, which is otherwise the same three characters in the
			// same order.
			for j := i + 2; j < end; j++ {
				info[j].mask |= maskCfar
			}
			coengs = 2 // done: no later coeng may be a pre-base one

		case info[i].cat == catVPre:
			// A sign written to the left of the letter, drawn before it.
			moveGlyphToFront(buf, info, start, i)
		}
	}
}

// khmerSyllableKind says what a cluster is, which decides whether it is
// reordered at all.
type khmerSyllableKind uint8

const (
	khmerNonKhmer  khmerSyllableKind = iota // left alone
	khmerConsonant                          // the ordinary case
	khmerBroken                             // dependents with no letter of their own
)

// khmerSyllable is one cluster of a run.
type khmerSyllable struct {
	start, end int
	kind       khmerSyllableKind
}

// Cutting a run of Khmer into syllables.
//
// The grammar is the script development specification's, as every shaper reads
// it, written out one production each:
//
//	c             = C | Ra | V
//	cn            = c ((ZWJ|ZWNJ)? Robatic)?
//	xgroup        = ((ZWJ|ZWNJ)* Xgroup)*
//	ygroup        = Ygroup*
//	matra_group   = VPre? xgroup VBlw? xgroup ((ZWJ|ZWNJ)? VAbv)? xgroup VPst?
//	syllable_tail = xgroup matra_group xgroup (H c)? ygroup
//
//	broken_cluster     = Robatic? (H cn)* (H | syllable_tail)
//	consonant_syllable = (cn | PLACEHOLDER | DOTTEDCIRCLE) broken_cluster
//
// The two alternatives cannot both match: a broken cluster never opens with a
// letter, and a consonant syllable always does. So the cut is a plain
// left-to-right scan with no backtracking, and every production below is
// greedy — each takes as much as it can, which is what the scanner the grammar
// was written for does.
//
// A character of no Khmer category at all — a Latin letter, a space, a Khmer
// punctuation mark — is its own one-character cluster and is left exactly as it
// was.

// khmerSyllables cuts a run of characters into syllables. The result covers the
// input exactly and in order, and every entry is at least one character long,
// so a caller walking it always makes progress.
func khmerSyllables(cats []indicCat) []khmerSyllable {
	var out []khmerSyllable
	for i := 0; i < len(cats); {
		s := khmerScanSyllable(cats, i)
		if s.end-s.start > maxIndicSyllable {
			s.end = s.start + maxIndicSyllable
		}
		if s.end <= s.start {
			s.end = s.start + 1 // a scanner that consumed nothing would not terminate
		}
		out = append(out, s)
		i = s.end
	}
	return out
}

// khmerScanSyllable matches one syllable starting at a position.
func khmerScanSyllable(cats []indicCat, start int) khmerSyllable {
	if i, ok := khmerTakeConsonant(cats, start); ok {
		return khmerSyllable{start, khmerTakeBroken(cats, i), khmerConsonant}
	}
	if cats[start] == catPlaceholder || cats[start] == catDottedCircle {
		return khmerSyllable{start, khmerTakeBroken(cats, start+1), khmerConsonant}
	}
	if i := khmerTakeBroken(cats, start); i > start {
		return khmerSyllable{start, i, khmerBroken}
	}
	return khmerSyllable{start, start + 1, khmerNonKhmer}
}

// khmerIsLetter reports whether a category can open a syllable: a consonant, the
// Ro, or an independent vowel. The specification treats all three alike, which
// is why the grammar names them as one.
func khmerIsLetter(c indicCat) bool {
	return c == catConsonant || c == catRa || c == catVowel
}

// cn = c ((ZWJ|ZWNJ)? Robatic)?
func khmerTakeConsonant(cats []indicCat, i int) (int, bool) {
	if i >= len(cats) || !khmerIsLetter(cats[i]) {
		return i, false
	}
	i++
	j := i
	if j < len(cats) && indicIsJoiner(cats[j]) {
		j++
	}
	if j < len(cats) && cats[j] == catRobatic {
		return j + 1, true
	}
	return i, true
}

// xgroup = ((ZWJ|ZWNJ)* Xgroup)*
//
// The joiners are only consumed when an Xgroup mark follows them: a joiner on
// its own belongs to whatever comes next, or to nothing.
func khmerTakeXgroup(cats []indicCat, i int) int {
	for {
		j := i
		for j < len(cats) && indicIsJoiner(cats[j]) {
			j++
		}
		if j >= len(cats) || cats[j] != catXgroup {
			return i
		}
		i = j + 1
	}
}

// ygroup = Ygroup*
func khmerTakeYgroup(cats []indicCat, i int) int {
	for i < len(cats) && cats[i] == catYgroup {
		i++
	}
	return i
}

// matra_group = VPre? xgroup VBlw? xgroup ((ZWJ|ZWNJ)? VAbv)? xgroup VPst?
func khmerTakeMatraGroup(cats []indicCat, i int) int {
	at := func(k int, c indicCat) bool { return k < len(cats) && cats[k] == c }
	if at(i, catVPre) {
		i++
	}
	i = khmerTakeXgroup(cats, i)
	if at(i, catVBlw) {
		i++
	}
	i = khmerTakeXgroup(cats, i)
	switch {
	case at(i, catVAbv):
		i++
	case i < len(cats) && indicIsJoiner(cats[i]) && at(i+1, catVAbv):
		i += 2
	}
	i = khmerTakeXgroup(cats, i)
	if at(i, catVPst) {
		i++
	}
	return i
}

// syllable_tail = xgroup matra_group xgroup (H c)? ygroup
func khmerTakeTail(cats []indicCat, i int) int {
	i = khmerTakeXgroup(cats, i)
	i = khmerTakeMatraGroup(cats, i)
	i = khmerTakeXgroup(cats, i)
	if i+1 < len(cats) && indicIsHalant(cats[i]) && khmerIsLetter(cats[i+1]) {
		i += 2
	}
	return khmerTakeYgroup(cats, i)
}

// broken_cluster = Robatic? (H cn)* (H | syllable_tail)
//
// The loop is where subscripts come from: a coeng binds the letter after it into
// the same syllable, however many times it is repeated. A coeng with no letter
// after it does not — it ends the syllable instead, which is the bare H below.
func khmerTakeBroken(cats []indicCat, i int) int {
	if i < len(cats) && cats[i] == catRobatic {
		i++
	}
	for i < len(cats) && indicIsHalant(cats[i]) {
		j, ok := khmerTakeConsonant(cats, i+1)
		if !ok {
			break
		}
		i = j
	}
	if i < len(cats) && indicIsHalant(cats[i]) {
		// A coeng the loop above declined is not followed by a letter, so the
		// tail cannot take it either: it ends the syllable on its own.
		return i + 1
	}
	return khmerTakeTail(cats, i)
}
