package fonts

import "sort"

// Myanmar reordering.
//
// Myanmar is written with three things that are not drawn where they are
// stored:
//
//   - A *pre-base vowel sign*. ေ (U+1031) is written after its consonant and
//     drawn before it, as a Devanagari i-sign is.
//   - A *medial Ra*. ြ (U+103C) is written after the consonant it wraps and
//     drawn before it, and unlike the vowel sign it is a consonant sign rather
//     than a vowel — a syllable may carry it and a pre-base vowel both, and
//     then the vowel is drawn outside it.
//   - A *kinzi*. A syllable opening with ရ်္ — Ra, asat and virama — is drawn
//     not as three letters at the front but as one mark over the letter that
//     follows, and so is drawn after it.
//
// Myanmar is its own shaper rather than a row in the Indic table for two
// reasons. The base is not searched for: it is simply the first consonant of
// the syllable, and no question is put to the font about it, because Myanmar
// has no conjunct forms whose existence would settle where the base is. And
// what follows the base is placed by walking it once and carrying a *state* —
// the below-base signs and the marks between them are drawn in the order they
// are written until a below-base sign has been seen, after which everything is
// drawn after the subjoined forms. That walk is the whole of Myanmar
// reordering, and it has no counterpart in the Indic model.
//
// # What is covered
//
// The syllable grammar, the base search, the kinzi, the reordering walk, the
// four features the model names ('rphf', 'pref', 'blwf', 'pstf') applied one at
// a time to the syllable, and the presentation features.
//
// These are not:
//
//   - Declining to treat a consonant as one because the font ligated it away.
//     The specification says the base search skips a glyph that 'ccmp' turned
//     into something else; this package does not track which glyphs those were,
//     so a font that composes a consonant into a mark in 'ccmp' has that mark
//     read as a consonant.
//   - Zawgyi, the pre-Unicode encoding that puts Myanmar glyphs at Myanmar
//     characters' code points and is shaped by not shaping at all. Text in it
//     is set by these rules, which is what a shaper that cannot tell the two
//     apart must do.
//   - Canonical ordering of the marks in a syllable, as in indic.go.
//
// # Where this runs
//
// In place of the default substitutions, from ShapeGlyphs, through
// shapeSyllabic.

// The Myanmar characters the rules name directly.
const (
	// myanmarRa is the letter a kinzi is made from.
	myanmarRa = 0x1004
	// myanmarAsat kills the vowel of the letter before it, and is the second
	// character of a kinzi.
	myanmarAsat = 0x103A
	// myanmarVirama is the third. It is an invisible stacker: never drawn, and
	// present only to say that what precedes it is subjoined.
	myanmarVirama = 0x1039
)

// isMyanmarScript reports whether a run is Myanmar, by the OpenType tags a
// Myanmar font declares its rules under.
func isMyanmarScript(script uint16) bool {
	return scriptSelects(script, "mym2") || scriptSelects(script, "mymr")
}

// myanmarRange is one stretch of characters sharing a shaping category.
type myanmarRange struct {
	lo, hi rune
	cat    indicCat
}

// myanmarCategories is what each Myanmar character is within a syllable.
//
// As with Khmer it is stated rather than derived: Myanmar names five medial
// consonants, an asat and a class of tone marks that Unicode's Indic categories
// have no equivalents for, and the grammar below is written in terms of those
// names. Two of the vowel signs — U+1032 and U+1036 — are grouped with neither
// the vowels nor the marks but on their own, because the reordering walk counts
// them where it counts nothing else.
//
// The ranges follow the script development specification, which covers the
// Myanmar block and the two extended blocks that hold the Shan, Mon and Aiton
// letters written with the same rules.
var myanmarCategories = [...]myanmarRange{
	{0x1000, 0x1003, catConsonant},
	{0x1004, 0x1004, catRa}, // the letter a kinzi is made from
	{0x1005, 0x101A, catConsonant},
	{0x101B, 0x101B, catRa},
	{0x101C, 0x1020, catConsonant},
	{0x1021, 0x102A, catVowel},
	{0x102B, 0x102C, catVPst},
	{0x102D, 0x102E, catVAbv},
	{0x102F, 0x1030, catVBlw},
	{0x1031, 0x1031, catVPre},
	{0x1032, 0x1032, catAnusvara},
	{0x1033, 0x1035, catVAbv},
	{0x1036, 0x1036, catAnusvara},
	{0x1037, 0x1037, catNukta}, // the dot below
	{0x1038, 0x1038, catSM},
	{0x1039, 0x1039, catStacker},
	{0x103A, 0x103A, catAsat},
	{0x103B, 0x103B, catMedialY},
	{0x103C, 0x103C, catMedialR},
	{0x103D, 0x103D, catMedialW},
	{0x103E, 0x103E, catMedialH},
	{0x103F, 0x103F, catConsonant},
	{0x1040, 0x104B, catPlaceholder},
	{0x104E, 0x104E, catConsonant},
	{0x1050, 0x1051, catConsonant},
	{0x1052, 0x1055, catVowel},
	{0x1056, 0x1057, catVPst},
	{0x1058, 0x1059, catVBlw},
	{0x105A, 0x105A, catRa},
	{0x105B, 0x105D, catConsonant},
	{0x105E, 0x105F, catMedialY},
	{0x1060, 0x1060, catMedialL},
	{0x1061, 0x1061, catConsonant},
	{0x1062, 0x1062, catVPst},
	{0x1063, 0x1064, catPTone},
	{0x1065, 0x1066, catConsonant},
	{0x1067, 0x1068, catVPst},
	{0x1069, 0x106D, catPTone},
	{0x106E, 0x1070, catConsonant},
	{0x1071, 0x1074, catVAbv},
	{0x1075, 0x1081, catConsonant},
	{0x1082, 0x1082, catMedialW},
	{0x1083, 0x1083, catVPst},
	{0x1084, 0x1084, catVPre},
	{0x1085, 0x1086, catVAbv},
	{0x1087, 0x108D, catSM},
	{0x108E, 0x108E, catConsonant},
	{0x108F, 0x108F, catSM},
	{0x1090, 0x1099, catPlaceholder},
	{0x109A, 0x109C, catSM},
	{0x109D, 0x109D, catVAbv},
	{0x200C, 0x200C, catZWNJ},
	{0x200D, 0x200D, catZWJ},
	{dottedCircle, dottedCircle, catDottedCircle},
	{0xA9E0, 0xA9E4, catConsonant},
	{0xA9E5, 0xA9E5, catVAbv},
	{0xA9E7, 0xA9EF, catConsonant},
	{0xA9F0, 0xA9F9, catPlaceholder},
	{0xA9FA, 0xA9FE, catConsonant},
	{0xAA60, 0xAA6F, catConsonant},
	{0xAA71, 0xAA73, catConsonant},
	{0xAA74, 0xAA76, catPlaceholder},
	{0xAA7A, 0xAA7A, catConsonant},
	{0xAA7B, 0xAA7B, catPTone},
	{0xAA7C, 0xAA7D, catNukta},
	{0xAA7E, 0xAA7F, catConsonant},
	// The variation selectors, which Myanmar uses to name a letterform the
	// font draws differently for one language. They belong to whatever they
	// follow and are placed with it.
	{0xFE00, 0xFE0F, catVS},
}

// myanmarCategory reports what a character is within a Myanmar syllable.
// Anything the table does not name is not part of one.
func myanmarCategory(r rune) indicCat {
	i := sort.Search(len(myanmarCategories), func(i int) bool { return myanmarCategories[i].hi >= r })
	if i < len(myanmarCategories) && r >= myanmarCategories[i].lo {
		return myanmarCategories[i].cat
	}
	return catOther
}

// myanmarBasicFeatures are applied to one syllable at a time, in this order,
// each to the whole of it.
//
// Unlike the Indic and Khmer models there are no masks: the specification names
// no stretch of the syllable any of these is for, so each sees the syllable it
// was applied to and the reordering has already put its glyphs where the font's
// rules expect them.
//
// The order is the specification's: the kinzi is made first, before anything
// can consume the Ra it is made from, and the pre-, below- and post-base forms
// are built from what is left.
var myanmarBasicFeatures = []string{"rphf", "pref", "blwf", "pstf"}

// myanmarRunFeatures are applied to the whole run once every syllable is in
// drawing order.
//
// 'liga' is here, and is not in indic.go or khmer.go. That is not an oversight
// in either direction: the Myanmar specification names no ligature feature of
// its own, so a Myanmar font that wants one declares it under 'liga' and means
// it, whereas the Indic and Khmer specifications both name their own and a
// font's 'liga' is then something else.
var myanmarRunFeatures = []struct {
	tag    string
	manual bool
}{
	{"pres", true},
	{"abvs", true},
	{"blws", true},
	{"psts", true},
	{"rlig", false},
	{"liga", false},
	{"clig", false},
	{"calt", false},
	{"rclt", false},
}

// shapeMyanmar is the whole Myanmar pass: it replaces the default substitutions
// for a run it handles.
func (sh shaper) shapeMyanmar(buf []Glyph, runes []rune) []Glyph {
	info := make([]indicInfo, len(runes))
	cats := make([]indicCat, len(runes))
	for i, r := range runes {
		info[i].cat = myanmarCategory(r)
		info[i].ignorable = hiddenAfterShaping(r)
		cats[i] = info[i].cat
	}

	shift := 0
	dotted, hasDotted := sh.f.GlyphID(dottedCircle)
	for _, syl := range myanmarSyllables(cats) {
		if syl.kind == myanmarNonMyanmar {
			continue
		}
		start, end := syl.start+shift, syl.end+shift
		if syl.kind == myanmarBroken && hasDotted {
			buf, info = sh.insertGlyphAt(buf, info, start, dotted,
				indicInfo{cat: catDottedCircle, pos: posBaseC})
			end++
			shift++
		}
		var delta int
		buf, delta = sh.shapeMyanmarSyllable(buf, &info, start, end)
		shift += delta
	}

	for _, f := range myanmarRunFeatures {
		lookups := sh.l.featureLookups[f.tag]
		if len(lookups) == 0 {
			continue
		}
		buf, _ = sh.applyIndicFeature(buf, &info, lookups, 0, len(buf), 0, len(buf), f.manual)
	}

	return dropGlyphs(buf, func(i int) bool {
		return i < len(info) && (indicIsJoiner(info[i].cat) || info[i].ignorable)
	})
}

// shapeMyanmarSyllable puts one syllable into drawing order and applies the
// features written for it, returning the buffer and how much its length
// changed.
func (sh shaper) shapeMyanmarSyllable(buf []Glyph, info *[]indicInfo, start, end int) ([]Glyph, int) {
	total := 0
	grow := func(d int) { total += d; end += d }

	// 'locl' and then 'ccmp', before the reordering: the one corrects
	// letterforms for the language and the other composes and decomposes, and
	// the reordering is written against what they produce. They are applied per
	// syllable so that neither can join one syllable to the next.
	for _, tag := range []string{"locl", "ccmp"} {
		lookups := sh.l.featureLookups[tag]
		if len(lookups) == 0 {
			continue
		}
		var d int
		buf, d = sh.applyIndicFeature(buf, info, lookups, start, end, start, end, false)
		grow(d)
	}

	myanmarReorder(buf, *info, start, end)

	for _, tag := range myanmarBasicFeatures {
		lookups := sh.l.featureLookups[tag]
		if len(lookups) == 0 {
			continue
		}
		var d int
		buf, d = sh.applyIndicFeature(buf, info, lookups, start, end, start, end, true)
		grow(d)
	}

	oneCluster(buf, start, end)
	return buf, total
}

// myanmarIsBase reports whether a character can be a syllable's base.
//
// An independent vowel and a placeholder can, and are treated exactly as a
// consonant is — which is safe because neither can occur in a syllable that
// also has a consonant, and which is what lets one reordering serve every kind
// of syllable.
func myanmarIsBase(c indicCat) bool {
	switch c {
	case catConsonant, catRa, catCS, catVowel, catPlaceholder, catDottedCircle:
		return true
	}
	return false
}

// myanmarReorder puts one syllable into the order it is drawn in.
//
// There is one reordering and it happens before the font's rules run: nothing
// in it depends on what the font makes, because Myanmar's model asks the font
// no questions.
func myanmarReorder(buf []Glyph, info []indicInfo, start, end int) {
	if start >= end {
		return
	}

	// The kinzi: a syllable opening with Ra, asat and virama draws that Ra as a
	// mark over the letter that follows. The three characters stay together and
	// are drawn after the base, which is what posAfterMain says.
	limit, hasKinzi := start, false
	if start+3 <= end && info[start].cat == catRa &&
		info[start+1].cat == catAsat && indicIsHalant(info[start+2].cat) {
		limit, hasKinzi = start+3, true
	}

	// The base is the first consonant after the kinzi, and there is no question
	// to put to the font: Myanmar has no conjunct whose existence would move it.
	base := limit
	for i := limit; i < end; i++ {
		if myanmarIsBase(info[i].cat) {
			base = i
			break
		}
	}

	i := start
	if hasKinzi {
		for ; i < start+3; i++ {
			info[i].pos = posAfterMain
		}
	}
	for ; i < base; i++ {
		info[i].pos = posPreC
	}
	if i < end {
		info[i].pos = posBaseC
		i++
	}

	// Everything after the base, in one walk carrying a state. The state is
	// what makes this Myanmar's own: a below-base sign moves it, and where a
	// later mark is drawn depends on whether one has been seen. Written out as
	// separate rules it would be a table of pairs; written as a walk it is the
	// specification's own sentence — "the below-base forms, then what follows
	// them".
	pos := posAfterMain
	for ; i < end; i++ {
		switch {
		case info[i].cat == catMedialR:
			// The medial Ra wraps the base and is drawn before it.
			info[i].pos = posPreC
		case info[i].cat == catVPre:
			// A vowel sign written to the left, drawn before everything.
			info[i].pos = posPreM
		case info[i].cat == catVS:
			// A variation selector names a form of what it follows and has to
			// stay with it.
			info[i].pos = info[i-1].pos
		case pos == posAfterMain && info[i].cat == catVBlw:
			pos = posBelowC
			info[i].pos = pos
		case pos == posBelowC && info[i].cat == catAnusvara:
			info[i].pos = posBeforeSub
		case pos == posBelowC && info[i].cat == catVBlw:
			info[i].pos = pos
		case pos == posBelowC:
			pos = posAfterSub
			info[i].pos = pos
		default:
			info[i].pos = pos
		}
	}

	sortIndicByPosition(buf, info, start, end)

	// The pre-base signs come out of the sort in the order they were written,
	// and are drawn in the opposite one: a syllable carrying two of them draws
	// the second outside the first. Reversing the whole stretch does that, and
	// then each sign's own trailing marks — a variation selector — are put back
	// the right way round, since those were never meant to be reversed.
	first, last := end, end
	for k := start; k < end; k++ {
		if info[k].pos == posPreM {
			if first == end {
				first = k
			}
			last = k
		}
	}
	if first < last {
		reverseGlyphRange(buf, info, first, last+1)
		at := first
		for j := first; j <= last; j++ {
			if info[j].cat == catVPre {
				reverseGlyphRange(buf, info, at, j+1)
				at = j + 1
			}
		}
	}
}

// reverseGlyphRange reverses a half-open stretch of the buffer and its record
// together.
func reverseGlyphRange(buf []Glyph, info []indicInfo, from, to int) {
	if from < 0 || to > len(buf) || to > len(info) {
		return
	}
	for i, j := from, to-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
		info[i], info[j] = info[j], info[i]
	}
}

// myanmarSyllableKind says what a cluster is, which decides whether it is
// reordered at all.
type myanmarSyllableKind uint8

const (
	myanmarNonMyanmar myanmarSyllableKind = iota // left alone
	myanmarConsonant                             // the ordinary case
	myanmarBroken                                // dependents with no letter of their own
)

// myanmarSyllable is one cluster of a run.
type myanmarSyllable struct {
	start, end int
	kind       myanmarSyllableKind
}

// Cutting a run of Myanmar into syllables.
//
// The grammar is the script development specification's, as every shaper reads
// it, written out one production each:
//
//	j             = ZWJ | ZWNJ
//	k             = Ra Asat H                       the kinzi
//	c             = C | Ra
//
//	medial_group  = MY? Asat? MR? ((MW MH? ML? | MH ML? | ML) Asat?)?
//	main_vowels   = (VPre VS?)* VAbv* VBlw* A* (DB Asat?)?
//	post_vowels   = VPst MH? ML? Asat* VAbv* A* (DB Asat?)?
//	tone_group    = SM | PT A* DB? Asat?
//
//	complex_tail  = Asat* medial_group main_vowels post_vowels* tone_group* j?
//	syllable_tail = (H (c|V) VS?)* (H | complex_tail)
//
//	consonant_syllable = (k|CS)? (c|V|PLACEHOLDER|DOTTEDCIRCLE) VS? syllable_tail
//	broken_cluster     = k? VS? syllable_tail
//
// Unlike the Khmer grammar the two alternatives are not disjoint: a kinzi opens
// with a Ra, and a Ra is also a consonant, so a run beginning Ra + asat + virama
// may be read either as a kinzi with no letter after it — a broken cluster — or
// as an ordinary consonant syllable whose first letter is that Ra. The scanner
// the grammar is written for takes the longest match, so both readings are
// tried and the longer taken; that is the one place here where a production is
// not simply greedy.
//
// A character of no Myanmar category at all is its own one-character cluster
// and is left exactly as it was, and so is a lone join control — the grammar
// names one as a cluster of its own before it names a broken cluster, so a
// joiner with nothing to join is not shown against a dotted circle.

// myanmarSyllables cuts a run of characters into syllables. The result covers
// the input exactly and in order, and every entry is at least one character
// long, so a caller walking it always makes progress.
func myanmarSyllables(cats []indicCat) []myanmarSyllable {
	var out []myanmarSyllable
	for i := 0; i < len(cats); {
		s := myanmarScanSyllable(cats, i)
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

// myanmarScanSyllable matches one syllable starting at a position, taking the
// longest of the grammar's alternatives.
func myanmarScanSyllable(cats []indicCat, start int) myanmarSyllable {
	// A lone join control is named as its own cluster ahead of a broken one, so
	// it is tried before the broken cluster and after the consonant syllable.
	consonant := myanmarTakeConsonantSyllable(cats, start)
	if indicIsJoiner(cats[start]) && consonant <= start+1 {
		return myanmarSyllable{start, start + 1, myanmarNonMyanmar}
	}
	broken := myanmarTakeBrokenCluster(cats, start)
	switch {
	case consonant > start && consonant >= broken:
		return myanmarSyllable{start, consonant, myanmarConsonant}
	case broken > start:
		return myanmarSyllable{start, broken, myanmarBroken}
	}
	return myanmarSyllable{start, start + 1, myanmarNonMyanmar}
}

// myanmarTakeKinzi reports the end of a kinzi at a position, if one stands
// there.
func myanmarTakeKinzi(cats []indicCat, i int) (int, bool) {
	if i+2 < len(cats) && cats[i] == catRa && cats[i+1] == catAsat && indicIsHalant(cats[i+2]) {
		return i + 3, true
	}
	return i, false
}

// consonant_syllable = (k|CS)? (c|V|PLACEHOLDER|DOTTEDCIRCLE) VS? syllable_tail
//
// Both readings of a leading kinzi are tried, because a kinzi and the consonant
// it is made from start the same way. It returns start for no match.
func myanmarTakeConsonantSyllable(cats []indicCat, start int) int {
	best := start
	try := func(i int) {
		if i >= len(cats) || !myanmarIsBase(cats[i]) {
			return
		}
		i++
		if i < len(cats) && cats[i] == catVS {
			i++
		}
		if e := myanmarTakeSyllableTail(cats, i); e > best {
			best = e
		}
	}
	if k, ok := myanmarTakeKinzi(cats, start); ok {
		try(k)
	}
	if cats[start] == catCS {
		try(start + 1)
	}
	try(start)
	return best
}

// broken_cluster = k? VS? syllable_tail
func myanmarTakeBrokenCluster(cats []indicCat, start int) int {
	best := start
	try := func(i int) {
		if i < len(cats) && cats[i] == catVS {
			i++
		}
		if e := myanmarTakeSyllableTail(cats, i); e > best {
			best = e
		}
	}
	if k, ok := myanmarTakeKinzi(cats, start); ok {
		try(k)
	}
	try(start)
	return best
}

// syllable_tail = (H (c|V) VS?)* (H | complex_tail)
//
// The loop is where subjoined letters come from: a virama binds the letter
// after it into the same syllable. A virama with no letter after it does not —
// it ends the syllable instead, which is the bare H.
func myanmarTakeSyllableTail(cats []indicCat, i int) int {
	for i+1 < len(cats) && indicIsHalant(cats[i]) &&
		(cats[i+1] == catConsonant || cats[i+1] == catRa || cats[i+1] == catVowel) {
		i += 2
		if i < len(cats) && cats[i] == catVS {
			i++
		}
	}
	if i < len(cats) && indicIsHalant(cats[i]) {
		return i + 1
	}
	return myanmarTakeComplexTail(cats, i)
}

// complex_tail = Asat* medial_group main_vowels post_vowels* tone_group* j?
func myanmarTakeComplexTail(cats []indicCat, i int) int {
	at := func(k int, c indicCat) bool { return k < len(cats) && cats[k] == c }
	for at(i, catAsat) {
		i++
	}
	i = myanmarTakeMedialGroup(cats, i)
	i = myanmarTakeMainVowels(cats, i)
	for {
		j := myanmarTakePostVowels(cats, i)
		if j == i {
			break
		}
		i = j
	}
	for {
		j := myanmarTakeToneGroup(cats, i)
		if j == i {
			break
		}
		i = j
	}
	if i < len(cats) && indicIsJoiner(cats[i]) {
		i++
	}
	return i
}

// medial_group = MY? Asat? MR? ((MW MH? ML? | MH ML? | ML) Asat?)?
func myanmarTakeMedialGroup(cats []indicCat, i int) int {
	at := func(k int, c indicCat) bool { return k < len(cats) && cats[k] == c }
	if at(i, catMedialY) {
		i++
	}
	if at(i, catAsat) {
		i++
	}
	if at(i, catMedialR) {
		i++
	}
	switch {
	case at(i, catMedialW):
		i++
		if at(i, catMedialH) {
			i++
		}
		if at(i, catMedialL) {
			i++
		}
	case at(i, catMedialH):
		i++
		if at(i, catMedialL) {
			i++
		}
	case at(i, catMedialL):
		i++
	default:
		return i
	}
	if at(i, catAsat) {
		i++
	}
	return i
}

// main_vowels = (VPre VS?)* VAbv* VBlw* A* (DB Asat?)?
func myanmarTakeMainVowels(cats []indicCat, i int) int {
	at := func(k int, c indicCat) bool { return k < len(cats) && cats[k] == c }
	for at(i, catVPre) {
		i++
		if at(i, catVS) {
			i++
		}
	}
	for at(i, catVAbv) {
		i++
	}
	for at(i, catVBlw) {
		i++
	}
	for at(i, catAnusvara) {
		i++
	}
	if at(i, catNukta) {
		i++
		if at(i, catAsat) {
			i++
		}
	}
	return i
}

// post_vowels = VPst MH? ML? Asat* VAbv* A* (DB Asat?)?
func myanmarTakePostVowels(cats []indicCat, i int) int {
	at := func(k int, c indicCat) bool { return k < len(cats) && cats[k] == c }
	if !at(i, catVPst) {
		return i
	}
	i++
	if at(i, catMedialH) {
		i++
	}
	if at(i, catMedialL) {
		i++
	}
	for at(i, catAsat) {
		i++
	}
	for at(i, catVAbv) {
		i++
	}
	for at(i, catAnusvara) {
		i++
	}
	if at(i, catNukta) {
		i++
		if at(i, catAsat) {
			i++
		}
	}
	return i
}

// tone_group = SM | PT A* DB? Asat?
func myanmarTakeToneGroup(cats []indicCat, i int) int {
	at := func(k int, c indicCat) bool { return k < len(cats) && cats[k] == c }
	switch {
	case i < len(cats) && indicIsModifier(cats[i]):
		return i + 1
	case at(i, catPTone):
		i++
		for at(i, catAnusvara) {
			i++
		}
		if at(i, catNukta) {
			i++
		}
		if at(i, catAsat) {
			i++
		}
		return i
	}
	return i
}
