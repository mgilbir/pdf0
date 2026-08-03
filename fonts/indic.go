package fonts

import (
	"sort"
	"unicode"
)

// Indic reordering: setting text whose characters are not stored in the order
// they are drawn.
//
// Every other script this package sets is drawn in the order it is written.
// Devanagari is not. Three things move:
//
//   - A *pre-base matra*. The vowel sign ि (U+093F) is stored after the
//     consonant it belongs to and drawn before it, so कि — ka then the i-sign —
//     is two glyphs in the opposite order to its two characters.
//   - A *reph*. A syllable opening with र् — Ra followed by a virama — is not
//     drawn as two letters at the front. It becomes a single stroke drawn at
//     the *end* of the syllable, above it.
//   - A *conjunct*. Consonants joined by viramas are drawn as one compound
//     letterform, which the font supplies and which the shaper has to ask for
//     by putting the pieces where the font's rules expect them.
//
// None of this is decidable from the glyphs. It needs the syllable's structure:
// which character is the base consonant, which are its dependents, and where
// each dependent sits. So the run is cut into syllables, each syllable is
// classified from Unicode's Indic categories (indiccategory.go), its glyphs are
// put into drawing order, and the font's features are applied to the parts of
// it each feature is for. This is the model OpenType calls Indic2 — the one a
// font declaring 'dev2' is written against.
//
// # What is covered
//
// Devanagari, and Devanagari alone.
//
// A run whose Unicode script is Devanagari is reordered here. Every other
// script — Bengali, Gujarati, Gurmukhi, Kannada, Malayalam, Oriya, Tamil,
// Telugu, and the Khmer, Myanmar and Universal-Shaping-Engine scripts — is
// *not*, and is set exactly as it was before: its characters are turned into
// glyphs in storage order and the font's default features are applied. Text in
// those scripts is still not correctly set by this package and should be shaped
// elsewhere and passed in as glyph indices.
//
// That is a deliberate scope. The model is shared but the data is not: each
// script states its own base-consonant rule, its own reph position and mode,
// and its own set of characters that behave unlike their category suggests. A
// shallow pass over nine scripts would be wrong in each of them in a way only a
// reader of that script would catch. One script done properly is worth more.
//
// Both generations of the Devanagari rules are covered. A script that has two
// OpenType specifications has two tags, and the tag a font declares its rules
// under says which of the two it was written against — see indicOldSpec.
//
// Within Devanagari, these are not done:
//
//   - Pre-base-reordering Ra ('pref'). Devanagari has none; the feature is
//     applied where the font asks for it but no consonant is moved for it.
//   - 'locl', the localised forms a font declares per language. It is applied to
//     every other script this package sets, and an Indic run does not get it.
//   - Asking the font about a consonant *with* surrounding context, which the
//     first-generation rules allow and the second do not. The question is always
//     put as a bare pair of glyphs, so a font that states its below-base or
//     post-base forms only as a contextual rule is read as stating none — its
//     conjuncts then come out as loose letters rather than in the wrong place.
//
// # Where this runs
//
// Before every other substitution, from ShapeGlyphs, in place of the joining
// pass — the two are alternatives, since no script both joins cursively and
// reorders. 'liga' is not applied to an Indic run: a font's discretionary Latin
// ligatures are not written about these glyphs, and the features that *are* —
// 'pres', 'abvs', 'blws', 'psts', 'haln' — are applied here instead.
//
// # Clusters
//
// A syllable's glyphs all take the cluster of its first character. They have to:
// once the glyphs are in drawing order they no longer correspond one-for-one to
// the characters, and a syllable is the smallest piece of Devanagari that can
// honestly be mapped back to a position in the text.

// indicCat is what a character is within a syllable — the shaping category,
// which is Unicode's Indic_Syllabic_Category collapsed onto the distinctions
// the reordering actually makes.
type indicCat uint8

const (
	catOther        indicCat = iota // not part of any syllable
	catConsonant                    // C
	catRa                           // the consonant that can become a reph
	catVowel                        // an independent vowel: a syllable of its own
	catMatra                        // a dependent vowel sign
	catNukta                        // N, a dot that modifies the letter before it
	catHalant                       // H, the virama
	catStacker                      // an invisible stacker: a virama that is never drawn
	catZWJ                          // zero width joiner
	catZWNJ                         // zero width non-joiner
	catSM                           // bindu, visarga, gemination, syllable modifier, tone
	catVD                           // a cantillation (Vedic) mark
	catPlaceholder                  // something a syllable can hang off that is not a letter
	catDottedCircle                 // U+25CC, what a syllable with no base is shown against
	catSymbol                       // avagraha and its kind: a cluster of its own
	catRepha                        // a repha written as its own character
	catCM                           // a medial consonant
	catCS                           // a consonant that carries its own stacker
	catRS                           // a register shifter
)

// indicPos is where a glyph goes within its syllable. The order of these is the
// order the glyphs are drawn in, and sorting a syllable by them *is* the initial
// reordering — which is why they are numbered rather than named alone.
type indicPos uint8

const (
	posStart indicPos = iota
	posRaToBecomeReph
	posPreM  // a pre-base matra: drawn before the base, stored after it
	posPreC  // anything else that precedes the base
	posBaseC // the base consonant
	posAfterMain
	posAboveC
	posBeforeSub
	posBelowC
	posAfterSub
	posBeforePost
	posPostC
	posAfterPost
	posFinalC
	posSMVD // a syllable modifier or Vedic mark: always last
	posEnd
)

// The characters Devanagari's rules name directly. Everything else this file
// decides from Unicode's categories; these three it cannot, because what they
// mean is particular to the script rather than general to their category.
const (
	// devanagariRa is the one consonant that becomes a reph. Its category is
	// plain Consonant like every other letter's, so it can only be named.
	devanagariRa = 0x0930
	// devanagariVirama is the character the font's conjunct rules are written
	// against, and so the one to ask those rules about.
	devanagariVirama = 0x094D
	// dottedCircle is what a reader shows a syllable against when the syllable
	// has no base of its own. Unicode calls it a consonant placeholder; the
	// shaping model treats it as its own thing.
	dottedCircle = 0x25CC
)

// indicBlwfMode says which consonants a font's below-base forms feature is for.
type indicBlwfMode uint8

const (
	// blwfPreAndPost: below-base forms are asked for on both sides of the base.
	blwfPreAndPost indicBlwfMode = iota
	// blwfPostOnly: only after it.
	blwfPostOnly
)

// indicConfig is what one script states about its own reordering: the two
// characters the rules name directly, and the ways its behaviour differs from
// the others'.
//
// The model is shared; the data is not. Keeping the data here rather than in the
// code is what lets a second script be added by stating what it does rather than
// by branching on which script it is.
type indicConfig struct {
	// tag is the script's second-generation OpenType tag, which is how a config
	// is found: it is the first tag the script selects, so a run finds its
	// config without this file naming an index into the generated script table.
	tag string
	// ra is the consonant that can become a reph, and virama the character the
	// font's conjunct rules are written against. Both are plain letters by every
	// Unicode property they have, so both can only be named.
	ra, virama rune
	// hasOldSpec says the script had a first-generation OpenType specification,
	// and so that a font declaring the older of its two tags means the older
	// rules. A script with only one specification never does.
	hasOldSpec bool
	// blwfMode says which consonants 'blwf' is asked for.
	blwfMode indicBlwfMode
	// doubleHalantBlocksMove says a virama already standing after the last
	// consonant stops the first-generation post-base virama move. Reports
	// differ script by script, so only the one known to want it says so.
	doubleHalantBlocksMove bool
	// hasEyelashRa says the script's first-generation rules ask for a below-base
	// form of a pre-base Ra.
	hasEyelashRa bool
}

// indicConfigs is every script this file reorders, by its second-generation tag.
var indicConfigs = map[string]*indicConfig{
	"dev2": {
		tag: "dev2", ra: 0x0930, virama: 0x094D,
		hasOldSpec: true, blwfMode: blwfPreAndPost, hasEyelashRa: true,
	},
}

// indicConfigFor reports the reordering model for a run's script, or nil for a
// script this file does not reorder.
//
// It asks the script's OpenType tags rather than naming an index into the
// generated script table, so that each script is identified by the tag a font
// declares its rules under.
func indicConfigFor(script uint16) *indicConfig {
	for _, tag := range scriptTags(script) {
		if c, ok := indicConfigs[tag]; ok {
			return c
		}
	}
	return nil
}

// maxIndicSyllable bounds how many characters one syllable may hold.
//
// The reordering sorts a syllable by insertion, which is quadratic, and the
// grammar below lets a syllable grow without limit — a consonant and a virama
// repeated is a legal, if meaningless, sequence, and text is untrusted input
// exactly as a font is. A syllable longer than this is cut, and the remainder
// starts a new one, which changes where its glyphs go. Real Devanagari
// syllables run to a handful of characters; the longest conjunct anyone writes
// is nowhere near this.
const maxIndicSyllable = 64

// reordersIndic reports whether a run in this script is reordered here.
func reordersIndic(script uint16) bool { return indicConfigFor(script) != nil }

// indicOldSpec reports whether the font means the first-generation rules for
// this script.
//
// A script that has two OpenType specifications has two tags, and the tag a font
// declares its rules under says which of the two it was written against: a font
// carrying only 'deva' was written before 'dev2' existed. The difference is not
// cosmetic — the two disagree about where the reph goes and about which
// consonants the below-base forms feature is for — so a font written for the
// older rules and shaped by the newer ones sets some conjuncts differently from
// the way its author drew them.
//
// The question is asked of the font rather than of the text, and asked the same
// way feature selection asks it, so that the rules applied are the rules of the
// script table they came from. A font that declares nothing for the script falls
// back to the default table, which is not a second-generation declaration and so
// means the older rules — the same reading every other shaper takes.
func (f *Face) indicOldSpec(cfg *indicConfig, script uint16) bool {
	if !cfg.hasOldSpec {
		return false
	}
	tag := f.chosenScriptTag(script)
	return len(tag) != 4 || tag[3] != '2'
}

// indicProperties reports a character's shaping category and where it sits.
func indicProperties(r rune) (indicCat, indicPos) {
	syl, pos := indicCategories(r)
	cat := catOther
	switch syl {
	case indicSylConsonant, indicSylConsonantDead, indicSylConsonantFinal,
		indicSylConsonantHeadLetter, indicSylConsonantInitialPostfixed,
		indicSylConsonantSubjoined, indicSylConsonantPrefixed:
		cat = catConsonant
	case indicSylConsonantMedial:
		cat = catCM
	case indicSylConsonantWithStacker:
		cat = catCS
	case indicSylConsonantPrecedingRepha:
		cat = catRepha
	case indicSylConsonantPlaceholder, indicSylModifyingLetter, indicSylNumber:
		cat = catPlaceholder
	case indicSylVowelIndependent, indicSylVowel:
		cat = catVowel
	case indicSylVowelDependent:
		cat = catMatra
	case indicSylNukta:
		cat = catNukta
	case indicSylVirama:
		cat = catHalant
	case indicSylInvisibleStacker:
		cat = catStacker
	case indicSylJoiner:
		cat = catZWJ
	case indicSylNonJoiner:
		cat = catZWNJ
	case indicSylBindu, indicSylVisarga, indicSylGeminationMark,
		indicSylSyllableModifier, indicSylToneMark:
		cat = catSM
	case indicSylCantillationMark:
		cat = catVD
	case indicSylAvagraha:
		cat = catSymbol
	case indicSylRegisterShifter:
		cat = catRS
	}
	switch r {
	case devanagariRa:
		cat = catRa
	case dottedCircle:
		cat = catDottedCircle
	}
	return cat, indicPositionOf(cat, pos)
}

// indicCategories reports Unicode's two Indic categories for a character.
func indicCategories(r rune) (indicSyllabic, indicPosition) {
	i := sort.Search(len(indicRanges), func(i int) bool { return indicRanges[i].hi >= r })
	if i < len(indicRanges) && r >= indicRanges[i].lo {
		return indicRanges[i].syl, indicRanges[i].pos
	}
	return indicSylOther, indicPosNotApplicable
}

// indicPositionOf turns a category and Unicode's positional category into the
// place a glyph takes in its syllable.
//
// A consonant is the base until the reordering decides otherwise; a syllable
// modifier or Vedic mark is always last; and a mark that attaches to the letter
// takes its place from which side of the letter it is drawn on. The compound
// positions — top-and-right, bottom-and-right — resolve to the last part of
// what they name, because that is the part whose place in the sequence decides
// where the whole thing goes.
//
// The matra rule is Devanagari's, which is the script this file reorders: a
// left-side matra is drawn before the base and everything else after the
// below-base forms. Another script would need its own rule here.
func indicPositionOf(cat indicCat, pos indicPosition) indicPos {
	side := posEnd
	switch pos {
	case indicPosLeft:
		side = posPreC
	case indicPosTop, indicPosTopAndLeft:
		side = posAboveC
	case indicPosBottom, indicPosTopAndBottom, indicPosBottomAndLeft, indicPosTopAndBottomAndLeft:
		side = posBelowC
	case indicPosRight, indicPosBottomAndRight, indicPosLeftAndRight,
		indicPosTopAndRight, indicPosTopAndLeftAndRight, indicPosTopAndBottomAndRight:
		side = posPostC
	case indicPosOverstruck:
		side = posAfterMain
	case indicPosVisualOrderLeft:
		side = posPreM
	}
	switch {
	case indicIsBaseCandidate(cat):
		return posBaseC
	case cat == catMatra:
		if side == posPreC {
			return posPreM
		}
		return posAfterSub
	case cat == catSM || cat == catVD:
		return posSMVD
	case cat == catRepha:
		return posRaToBecomeReph
	}
	return side
}

// indicIsBaseCandidate reports whether a character can be a syllable's base.
// A vowel and a placeholder can: a syllable does not need a consonant.
func indicIsBaseCandidate(c indicCat) bool {
	switch c {
	case catConsonant, catRa, catCS, catCM, catVowel, catPlaceholder, catDottedCircle:
		return true
	}
	return false
}

// indicIsHalant reports whether a character kills the vowel of the consonant
// before it, whether or not it is itself drawn.
func indicIsHalant(c indicCat) bool { return c == catHalant || c == catStacker }

// indicIsJoiner reports whether a character is one of the two zero-width
// controls, which a syllable's structure has to step over but which also change
// what the font is asked for.
func indicIsJoiner(c indicCat) bool { return c == catZWJ || c == catZWNJ }

// indicIsAttached reports whether a character has no place of its own and takes
// the one of whatever it follows.
func indicIsAttached(c indicCat) bool {
	switch c {
	case catZWJ, catZWNJ, catNukta, catRS, catCM, catHalant, catStacker:
		return true
	}
	return false
}

// indicInfo is what the reordering knows about one glyph: what character it
// came from, where it goes, and which features are for it.
type indicInfo struct {
	cat  indicCat
	pos  indicPos
	mask uint8
}

// The features that apply to part of a syllable rather than to all of it. A
// font declares each of them for a range the syllable's structure decides — the
// half-forms feature is for the consonants before the base and nothing else —
// and applying one where it was not meant substitutes glyphs the font never
// intended to appear together.
const (
	maskRphf uint8 = 1 << iota
	maskPref
	maskBlwf
	maskAbvf
	maskHalf
	maskPstf
)

// indicBasicFeatures are applied to one syllable at a time, in this order,
// before the syllable is finally reordered. A zero mask means the feature is
// for the whole syllable.
//
// The order is the OpenType Indic2 order and is not negotiable: 'nukt'
// composes a letter with its dot so the rest see one glyph, 'rphf' makes the
// reph before anything can consume its Ra, the half and conjunct forms are
// built from what is left, and 'cjct' comes last so that it sees the forms the
// earlier ones made.
var indicBasicFeatures = []struct {
	tag  string
	mask uint8
}{
	{"nukt", 0},
	{"akhn", 0},
	{"rphf", maskRphf},
	{"rkrf", 0},
	{"pref", maskPref},
	{"blwf", maskBlwf},
	{"abvf", maskAbvf},
	{"half", maskHalf},
	{"pstf", maskPstf},
	{"vatu", 0},
	{"cjct", 0},
}

// indicRunFeatures are applied to the whole run once every syllable is in
// drawing order, in this order.
//
// The first five are the presentation features: what turns the reordered pieces
// into the shapes a reader sees — the pre-, above-, below- and post-base
// substitutions, and the form a consonant takes when its virama is drawn rather
// than swallowed. They are written about the joiners, so their lookups see them.
//
// The last three are the script-independent substitutions an Indic run still
// takes. They are not written about joiners and step over them, as they do over
// marks. 'liga' is deliberately not among them, and 'ccmp' is not either — it is
// applied per syllable, before the reordering, because everything after it is
// written against what it produces.
var indicRunFeatures = []struct {
	tag    string
	manual bool
}{
	{"pres", true},
	{"abvs", true},
	{"blws", true},
	{"psts", true},
	{"haln", true},
	{"rlig", false},
	{"clig", false},
	{"calt", false},
}

// shapeIndic is the whole Indic pass: it replaces both the joining pass and the
// default substitutions for a run it handles.
func (sh shaper) shapeIndic(buf []Glyph, runes []rune, plan *indicPlan) []Glyph {
	info := make([]indicInfo, len(runes))
	cats := make([]indicCat, len(runes))
	for i, r := range runes {
		info[i].cat, info[i].pos = indicProperties(r)
		cats[i] = info[i].cat
	}

	// Each syllable is shaped where it lies, and what it does to the buffer's
	// length shifts every syllable after it — so the syllables are walked in
	// order and the shift carried along, rather than their bounds recorded up
	// front and then found to be stale.
	shift := 0
	dotted, hasDotted := sh.f.GlyphID(dottedCircle)
	for _, syl := range indicSyllables(cats) {
		if syl.kind == sylNonIndic || syl.kind == sylSymbol {
			continue
		}
		start, end := syl.start+shift, syl.end+shift
		if syl.kind == sylBroken && hasDotted {
			buf, info = sh.insertDottedCircle(buf, info, start, end, dotted)
			end++
			shift++
		}
		var delta int
		buf, delta = sh.shapeIndicSyllable(buf, &info, runes, plan, syl.start, start, end)
		shift += delta
	}

	// The features that see the whole run rather than one syllable. They go
	// through applyIndicFeature rather than applyContextual so that the
	// per-glyph record stays in step with the buffer — which is what says where
	// the joiners are, and so what lets these lookups step over them.
	for _, f := range indicRunFeatures {
		lookups := sh.l.featureLookups[f.tag]
		if len(lookups) == 0 {
			continue
		}
		buf, _ = sh.applyIndicFeature(buf, &info, lookups, 0, len(buf), 0, len(buf), f.manual)
	}

	// The joiners have now done everything they are for: the forms they forced
	// or forbade are made, and nothing below is written about them. What is left
	// is a character with no shape, which must not reach the page.
	return dropGlyphs(buf, func(i int) bool {
		return i < len(info) && indicIsJoiner(info[i].cat)
	})
}

// insertDottedCircle puts U+25CC at the front of a syllable that has no base
// consonant of its own.
//
// A matra or a virama written with nothing to attach to is not text anyone
// meant to write, but it has to be *shown* — and a mark drawn on its own floats
// at the height it would have sat at, over nothing, where a reader cannot tell
// it from a mark on the letter before. The dotted circle is the placeholder
// every reader of these scripts knows: it says "a mark, and the letter it
// belongs to is missing".
//
// It goes after a repha, which is written before the letter it belongs to and
// so belongs before the placeholder too. Its own place is deliberately left at
// the end of the syllable rather than set to the base: the font is asked which
// consonants it draws below the base before the syllable is reordered, and the
// dotted circle is not a consonant the font has anything to say about. The base
// search picks it up from there.
//
// A face with no U+25CC cannot show one, and the caller checks that first.
func (sh shaper) insertDottedCircle(buf []Glyph, info []indicInfo, start, end, gid int) ([]Glyph, []indicInfo) {
	at := start
	for at < end && at < len(info) && info[at].cat == catRepha {
		at++
	}
	cluster := 0
	switch {
	case at < len(buf):
		cluster = buf[at].Cluster
	case len(buf) > 0:
		cluster = buf[len(buf)-1].Cluster
	}
	g := Glyph{GID: gid, Cluster: cluster, XAdvance: sh.f.advanceGID(gid)}

	buf = append(buf, Glyph{})
	copy(buf[at+1:], buf[at:])
	buf[at] = g

	info = append(info, indicInfo{})
	copy(info[at+1:], info[at:])
	info[at] = indicInfo{cat: catDottedCircle, pos: posEnd}
	return buf, info
}

// shapeIndicSyllable puts one syllable into drawing order and applies the
// features written for its parts, returning the buffer and how much its length
// changed.
//
// textStart is where the syllable begins in the original characters, which the
// word-initial rule below needs and the buffer can no longer say.
func (sh shaper) shapeIndicSyllable(buf []Glyph, info *[]indicInfo, runes []rune,
	plan *indicPlan, textStart, start, end int) ([]Glyph, int) {

	total := 0
	grow := func(d int) { total += d; end += d }

	// 'ccmp' first: it composes and decomposes so that everything after it has
	// the glyphs those rules are written against. It is applied per syllable,
	// like the features below, so that it cannot join one syllable to the next.
	if lookups := sh.l.featureLookups["ccmp"]; len(lookups) > 0 {
		var d int
		buf, d = sh.applyIndicFeature(buf, info, lookups, start, end, start, end, false)
		grow(d)
	}

	// Which consonants the font draws below or after the base, which is what
	// the base search needs and only the font can say. It has to come after
	// 'ccmp', because a consonant that rule composed is the one to ask about.
	plan.refine(buf, *info, start, end)

	// The base index it reports is not kept: the font may ligate the base away
	// while the features below run, so the final reordering finds it again from
	// the positions rather than from a remembered number.
	sh.indicInitialReorder(buf, *info, plan, start, end)

	for _, f := range indicBasicFeatures {
		lookups := sh.l.featureLookups[f.tag]
		if len(lookups) == 0 {
			continue
		}
		if f.mask == 0 {
			var d int
			buf, d = sh.applyIndicFeature(buf, info, lookups, start, end, start, end, true)
			grow(d)
			continue
		}
		// A masked feature sees the stretch of the syllable its mask marks and
		// nothing else — not even as context. The stretches are contiguous by
		// construction: the reph at the front, the consonants before the base,
		// the ones after it.
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
			buf, d = sh.applyIndicFeature(buf, info, lookups, lo, hi, lo, hi, true)
			grow(d)
			lo = hi + d
		}
	}

	sh.indicFinalReorder(buf, *info, start, end)

	// 'init' is for a pre-base matra that opens a word — the i-sign at the
	// start of a word is drawn differently from the same sign mid-word. What
	// counts as a word start is what precedes the syllable in the *text*: a
	// letter or a mark continues a word, a space or a stop does not.
	if lookups := sh.l.featureLookups["init"]; len(lookups) > 0 &&
		start < end && (*info)[start].pos == posPreM && indicWordStart(runes, textStart) {
		var d int
		buf, d = sh.applyIndicFeature(buf, info, lookups, start, start+1, start, end, true)
		grow(d)
	}

	// One cluster for the syllable: its glyphs are no longer in the order its
	// characters are, so the syllable is the smallest piece that can be mapped
	// back to the text at all.
	if start < end {
		cluster := buf[start].Cluster
		for i := start; i < end; i++ {
			if buf[i].Cluster < cluster {
				cluster = buf[i].Cluster
			}
		}
		for i := start; i < end; i++ {
			buf[i].Cluster = cluster
		}
	}
	return buf, total
}

// indicWordStart reports whether the character before a syllable ends a word.
func indicWordStart(runes []rune, at int) bool {
	if at <= 0 || at > len(runes) {
		return true
	}
	r := runes[at-1]
	return !unicode.In(r, unicode.L, unicode.M, unicode.Cf)
}

// applyIndicFeature runs a feature's lookups over part of a syllable.
//
// from..to is where a lookup may start; floor..ceil is everything it may see,
// backtrack and lookahead included. A feature for the whole syllable sees the
// syllable; a masked one sees only the stretch its mask marks, which is what
// keeps a ligature declared for the half forms from swallowing the base that
// happens to follow them.
//
// manual says the feature wants the join controls visible to its input rather
// than stepped over — true of every feature the Indic model names, and false of
// 'ccmp' and of the general substitutions, which are not written about joiners
// at all. See ignorable.go.
//
// The per-glyph record is kept in step as lookups change the buffer's length:
// a ligature that swallows three glyphs into one has to swallow their three
// records too, or every position after it would describe the wrong glyph. It is
// also what says where the joiners are, since a face commonly gives them the
// same glyph as the space.
func (sh shaper) applyIndicFeature(buf []Glyph, info *[]indicInfo, lookups []int, from, to, floor, ceil int, manual bool) ([]Glyph, int) {
	total, step := 0, 0
	sh.onResize = func(at, d int) {
		*info = respliceIndicInfo(*info, at, d)
		step += d
	}
	sh.joinerAt = func(at int) joinerKind {
		if at < 0 || at >= len(*info) {
			return notJoiner
		}
		switch (*info)[at].cat {
		case catZWJ:
			return joinerZWJ
		case catZWNJ:
			return joinerZWNJ
		}
		return notJoiner
	}
	sh.manualJoiners = manual
	sh.floor = floor
	for _, idx := range lookups {
		for i := from; i < to; {
			step = 0
			sh.limit = ceil
			consumed, out := sh.applyGSUBAt(idx, buf, i, 0)
			buf = out
			if consumed <= 0 {
				i++
				continue
			}
			to += step
			ceil += step
			total += step
			i += consumed
		}
	}
	return buf, total
}

// respliceIndicInfo does to the per-glyph record what a lookup did to the
// buffer: a negative delta means glyphs after at were ligated into it, so their
// records go; a positive one means the glyph at at became several, and the new
// ones are the same thing said more than once.
func respliceIndicInfo(info []indicInfo, at, delta int) []indicInfo {
	if at < 0 || at >= len(info) || delta == 0 {
		return info
	}
	if delta < 0 {
		n := -delta
		if at+1+n > len(info) {
			n = len(info) - at - 1
		}
		if n <= 0 {
			return info
		}
		return append(info[:at+1], info[at+1+n:]...)
	}
	out := make([]indicInfo, 0, len(info)+delta)
	out = append(out, info[:at+1]...)
	for k := 0; k < delta; k++ {
		out = append(out, info[at])
	}
	return append(out, info[at+1:]...)
}

// wouldSubstitute reports whether a feature's lookups would change a given
// sequence of glyphs.
//
// It is how a shaper asks the font a question the characters cannot answer.
// Whether a syllable's opening Ra becomes a reph is not a property of the text
// — it is whether *this font* has a reph for *this* Ra — and a shaper that
// assumed it does would take the Ra out of the base search of a font that would
// then draw it as an ordinary letter in the wrong place.
//
// The question asked is "would anything happen here", not "would exactly this
// sequence be consumed": a lookup that shortens the run has ligated it, and one
// that changes a glyph without shortening it has covered it, and either answers
// yes. A lookup that changed only the virama in the probe would answer yes
// wrongly, and no font's below-base or reph rules are written that way.
func (sh shaper) wouldSubstitute(lookups []int, gids []int) bool {
	if len(gids) == 0 {
		return false
	}
	sh.floor, sh.limit, sh.onResize, sh.joinerAt = 0, 0, nil, nil
	for _, idx := range lookups {
		probe := make([]Glyph, len(gids))
		for i, g := range gids {
			probe[i] = Glyph{GID: g}
		}
		_, out := sh.applyGSUBAt(idx, probe, 0, 0)
		if len(out) != len(probe) {
			return true
		}
		for i, g := range gids {
			if out[i].GID != g {
				return true
			}
		}
	}
	return false
}

// indicPlan is what one run of one script needs that neither the text nor the
// shared model can say: the script's own data, which generation of the
// specification this font was written against, and what the font answers when
// asked about a consonant.
//
// The last of those is what the base search turns on and cannot decide from the
// characters. In त्र — Ta, virama, Ra — the Ra is an ordinary consonant by
// every Unicode property it has, and yet a Devanagari font draws it as a stroke
// under the Ta, which makes the *Ta* the base. What settles it is whether the
// font's below-base, post-base or pre-base-form features cover the consonant
// alongside a virama, which is a question only the font can be asked.
//
// The answers are cached because they are properties of the font, and a page of
// Devanagari asks about the same forty consonants over and over.
type indicPlan struct {
	sh                     shaper
	cfg                    *indicConfig
	oldSpec                bool
	virama                 int
	haveVirama             bool
	blwf, pstf, pref, vatu []int
	cache                  map[int]indicPos
}

func (sh shaper) indicPlan(cfg *indicConfig, oldSpec bool) *indicPlan {
	p := &indicPlan{
		sh:      sh,
		cfg:     cfg,
		oldSpec: oldSpec,
		blwf:    sh.l.featureLookups["blwf"],
		pstf:    sh.l.featureLookups["pstf"],
		pref:    sh.l.featureLookups["pref"],
		vatu:    sh.l.featureLookups["vatu"],
		cache:   map[int]indicPos{},
	}
	p.virama, p.haveVirama = sh.f.GlyphID(cfg.virama)
	return p
}

// refine replaces the place of every consonant in a stretch of the buffer with
// what the font says about it.
func (p *indicPlan) refine(buf []Glyph, info []indicInfo, start, end int) {
	if !p.haveVirama || len(p.blwf)+len(p.pstf)+len(p.pref) == 0 {
		return
	}
	for i := start; i < end && i < len(info); i++ {
		if info[i].pos != posBaseC {
			continue
		}
		info[i].pos = p.of(buf[i].GID)
	}
}

func (p *indicPlan) of(gid int) indicPos {
	if pos, ok := p.cache[gid]; ok {
		return pos
	}
	// Both orders are tried. The second-generation rules are written virama
	// first and the first-generation ones consonant first, and enough fonts
	// carry the older lookups under the newer tag that every shaper matches
	// either.
	covers := func(lookups []int) bool {
		return p.sh.wouldSubstitute(lookups, []int{p.virama, gid}) ||
			p.sh.wouldSubstitute(lookups, []int{gid, p.virama})
	}
	// 'vatu' is asked alongside 'blwf' because it is the other way a font says
	// "this consonant is drawn under the base": the vattu is a below-base Ra, and
	// a font that declares its below-base forms only under 'vatu' means the same
	// thing about them.
	pos := posBaseC
	switch {
	case covers(p.blwf), covers(p.vatu):
		pos = posBelowC
	case covers(p.pstf), covers(p.pref):
		pos = posPostC
	}
	p.cache[gid] = pos
	return pos
}

// indicInitialReorder puts a syllable's characters into the order the font's
// rules are written against, and marks which glyphs each feature is for. It
// returns the index of the base consonant.
//
// This is the first of the two reorderings, and the one that moves characters:
// a pre-base matra written after its consonant is put before it, and an opening
// Ra is marked as the reph it is going to become. The second reordering, after
// the font's rules have run, moves *glyphs* — by then the Ra may be one stroke
// and three consonants may be one conjunct, and where those go depends on what
// the font actually made.
func (sh shaper) indicInitialReorder(buf []Glyph, info []indicInfo, plan *indicPlan, start, end int) int {
	if start >= end {
		return end
	}
	base := end
	hasReph := false
	limit := start

	// A syllable opening with Ra + virama, with something after it, draws that
	// Ra as a reph — provided the font has one, which only the font can say.
	if rphf := sh.l.featureLookups["rphf"]; len(rphf) > 0 && start+3 <= end &&
		info[start].cat == catRa && indicIsHalant(info[start+1].cat) &&
		!indicIsJoiner(info[start+2].cat) &&
		sh.wouldSubstitute(rphf, []int{buf[start].GID, buf[start+1].GID}) {
		limit += 2
		for limit < end && indicIsJoiner(info[limit].cat) {
			limit++
		}
		base = start
		hasReph = true
	}

	// The base is found from the end of the syllable backwards: the first
	// consonant the font does not draw below or after the base, or failing
	// that the first consonant there is. Everything before it is a half form or
	// a conjunct piece; everything after it hangs off it.
	{
		i := end
		seenBelow := false
		for {
			i--
			if indicIsBaseCandidate(info[i].cat) {
				if info[i].pos != posBelowC && (info[i].pos != posPostC || seenBelow) {
					base = i
					break
				}
				if info[i].pos == posBelowC {
					seenBelow = true
				}
				base = i
			} else if start < i && info[i].cat == catZWJ && indicIsHalant(info[i-1].cat) {
				// A joiner written after a virama asks for an explicit half
				// form, which settles the base: the search stops here.
				break
			}
			if i <= limit {
				break
			}
		}
	}
	if hasReph && base == start && limit-base <= 2 {
		// Nothing but the Ra: with no other consonant there is no syllable for
		// a reph to sit above, so the Ra is an ordinary letter and the base.
		hasReph = false
	}

	for i := start; i < base; i++ {
		if info[i].pos > posPreC {
			info[i].pos = posPreC
		}
	}
	if base < end {
		info[base].pos = posBaseC
	}
	// A consonant written after a matra is a final consonant, and is drawn
	// after everything the matra brings with it.
	for i := base + 1; i < end; i++ {
		if info[i].cat != catMatra {
			continue
		}
		for j := i + 1; j < end; j++ {
			if indicIsBaseCandidate(info[j].cat) {
				info[j].pos = posFinalC
				break
			}
		}
		break
	}
	if hasReph {
		info[start].pos = posRaToBecomeReph
	}

	// The first-generation rules expected the shaper to move a post-base virama
	// to after the last consonant, and fonts written against them declare their
	// conjunct lookups in that order. Leaving it where it stands sets those
	// conjuncts as loose letters with a visible virama between them.
	//
	// Reports differ on whether a virama already sitting after the last consonant
	// suppresses the move. It is known to for Kannada and known not to for
	// Devanagari, Bengali and Malayalam, so only the script known to want it is
	// given it, and the rest move unconditionally.
	if plan.oldSpec {
		for i := base + 1; i < end; i++ {
			if info[i].cat != catHalant {
				continue
			}
			j := end - 1
			for ; j > i; j-- {
				if indicIsBaseCandidate(info[j].cat) ||
					(plan.cfg.doubleHalantBlocksMove && info[j].cat == catHalant) {
					break
				}
			}
			if info[j].cat != catHalant && j > i {
				moved := info[i]
				copy(info[i:j], info[i+1:j+1])
				info[j] = moved
				g := buf[i]
				copy(buf[i:j], buf[i+1:j+1])
				buf[j] = g
			}
			break
		}
	}

	// A virama, a nukta or a joiner has no place of its own: it belongs to the
	// character before it and has to move with it, or the sort below would
	// strand it among glyphs it says nothing about.
	lastPos := posStart
	for i := start; i < end; i++ {
		if indicIsAttached(info[i].cat) {
			info[i].pos = lastPos
			if indicIsHalant(info[i].cat) && info[i].pos == posPreM {
				// A virama does not travel with a pre-base matra: it belongs to
				// the consonant, which stays where it is. U+092B U+093F U+094D
				// is the case, and every shaper agrees on it.
				for j := i; j > start; j-- {
					if info[j-1].pos != posPreM {
						info[i].pos = info[j-1].pos
						break
					}
				}
			}
		} else if info[i].pos != posSMVD {
			lastPos = info[i].pos
		}
	}
	// A consonant after the base owns whatever lies between it and the last
	// consonant or matra, for the same reason.
	last := base
	for i := base + 1; i < end; i++ {
		if indicIsBaseCandidate(info[i].cat) {
			for j := last + 1; j < i; j++ {
				if info[j].pos < posSMVD {
					info[j].pos = info[i].pos
				}
			}
			last = i
		} else if info[i].cat == catMatra {
			last = i
		}
	}

	sortIndicByPosition(buf, info, start, end)

	base = end
	for i := start; i < end; i++ {
		if info[i].pos == posBaseC {
			base = i
			break
		}
	}

	// Which feature is for which glyph. The reph is made from the front of the
	// syllable; the half and below-base forms from the consonants before the
	// base; the below-, above- and post-base forms from those after it.
	//
	// Whether the consonants *before* the base are asked for a below-base form
	// is the other half of what the two generations disagree about. The
	// first-generation rules asked for it only after the base; a script whose
	// second-generation rules also say so states blwfPostOnly.
	preBase := maskHalf
	if !plan.oldSpec && plan.cfg.blwfMode == blwfPreAndPost {
		preBase |= maskBlwf
	}
	for i := start; i < end && info[i].pos == posRaToBecomeReph; i++ {
		info[i].mask |= maskRphf
	}
	for i := start; i < base; i++ {
		info[i].mask |= preBase
	}
	for i := base + 1; i < end; i++ {
		info[i].mask |= maskBlwf | maskAbvf | maskPstf
	}

	// The eyelash Ra, which only the first-generation Devanagari rules produce.
	// Their below-base forms feature is stated as applying to consonants that
	// follow the base — "the exception is vattu, which may appear below half
	// forms as well as below the base glyph", and Ra is exactly that exception,
	// so a Ra bound by a virama before the base is asked for its below-base form
	// too. A joiner after the virama is the way to ask for the eyelash instead,
	// and is left alone.
	if plan.oldSpec && plan.cfg.hasEyelashRa {
		for i := start; i+1 < base; i++ {
			if info[i].cat == catRa && info[i+1].cat == catHalant &&
				(i+2 == base || info[i+2].cat != catZWJ) {
				info[i].mask |= maskBlwf
				info[i+1].mask |= maskBlwf
			}
		}
	}

	// A non-joiner asks for the letters around it *not* to be joined, which for
	// Devanagari means the half form is not to be made.
	for i := start + 1; i < end; i++ {
		if info[i].cat != catZWNJ {
			continue
		}
		for j := i; ; {
			j--
			info[j].mask &^= maskHalf
			if j <= start || indicIsBaseCandidate(info[j].cat) {
				break
			}
		}
	}
	return base
}

// indicFinalReorder moves glyphs into their drawn positions once the font's
// rules have made whatever forms it has.
//
// It is separate from the first reordering because it can only be done now. A
// reph's final place depends on whether the syllable has an explicit virama
// left in it, and a pre-base matra's on how far the half forms reach — both of
// which are answers the font gave by substituting, or declining to substitute,
// a moment ago.
func (sh shaper) indicFinalReorder(buf []Glyph, info []indicInfo, start, end int) {
	if start >= end {
		return
	}

	// The base again: the font may have ligated it into something else, so it
	// is found from the positions rather than remembered.
	base := start
	for ; base < end; base++ {
		if info[base].pos >= posBaseC {
			if start < base && info[base].pos > posBaseC {
				base--
			}
			break
		}
	}
	if base == end && start < base && info[base-1].cat == catZWJ {
		base--
	}
	if base < end {
		for start < base && (info[base].cat == catNukta || indicIsHalant(info[base].cat)) {
			base--
		}
	}

	// The pre-base matras were put at the very front of the syllable so that
	// the font's rules would see them there. They belong closer in than that:
	// after the half forms, immediately before the base. Which glyph that is,
	// is the last virama before the base — if the font left one.
	if start+1 < end && start < base {
		newPos := base - 1
		if base == end {
			newPos = base - 2
		}
		for newPos > start && info[newPos].cat != catMatra && !indicIsHalant(info[newPos].cat) {
			newPos--
		}
		if newPos >= start && indicIsHalant(info[newPos].cat) && info[newPos].pos != posPreM {
			if newPos+1 < end && indicIsJoiner(info[newPos+1].cat) {
				newPos++
			}
		} else {
			newPos = start // no virama survived, so nothing moves
		}
		if start < newPos && info[newPos].pos != posPreM {
			for i := newPos; i > start; i-- {
				if info[i-1].pos != posPreM {
					continue
				}
				old := i - 1
				if old < base && base <= newPos {
					base--
				}
				rotateIndicLeft(buf, info, old, newPos)
				newPos--
			}
		}
	}

	// The reph. It was left at the front through the substitutions, because
	// that is where the font's rule for making it is written; it is drawn at
	// the end of the syllable, above it.
	if info[start].pos == posRaToBecomeReph {
		// After the first virama still standing between the reph and the base,
		// which is where a half form ends and the reph can sit on it.
		newPos := start + 1
		for newPos < base && !indicIsHalant(info[newPos].cat) {
			newPos++
		}
		switch {
		case newPos < base && indicIsHalant(info[newPos].cat):
			if newPos+1 < base && indicIsJoiner(info[newPos+1].cat) {
				newPos++
			}
		default:
			// Otherwise immediately *before* the first post-base matra,
			// syllable modifier or Vedic mark — an anusvara is drawn over the
			// syllable and the reph belongs under it, and a font commonly has a
			// single glyph for the two together which it can only make if they
			// are in that order.
			newPos = base + 1
			for newPos < end && !indicAfterReph(info[newPos].pos) {
				newPos++
			}
			if newPos < end {
				newPos--
			} else {
				// Nothing to sit before: the end of the syllable, still inside
				// any modifiers, which are always drawn last of all.
				newPos = end - 1
				for newPos > start && info[newPos].pos == posSMVD {
					newPos--
				}
			}
		}
		if newPos > start {
			if start < base && base <= newPos {
				base--
			}
			rotateIndicLeft(buf, info, start, newPos)
		}
	}
}

// indicAfterReph reports whether a position is one the reph must be drawn
// before, which is what decides where it stops when no virama placed it.
func indicAfterReph(p indicPos) bool {
	return p == posPostC || p == posAfterPost || p == posSMVD
}

// sortIndicByPosition puts a syllable into drawing order. The sort is stable
// and by insertion: a syllable is a handful of glyphs, and stability is not an
// optimisation here but the definition — two glyphs in the same position keep
// the order they were written in, which is what makes a run of consonants
// before the base stay a run rather than a shuffle.
func sortIndicByPosition(buf []Glyph, info []indicInfo, start, end int) {
	for i := start + 1; i < end; i++ {
		g, f := buf[i], info[i]
		j := i
		for j > start && info[j-1].pos > f.pos {
			buf[j], info[j] = buf[j-1], info[j-1]
			j--
		}
		buf[j], info[j] = g, f
	}
}

// rotateIndicLeft moves the glyph at from to to, shifting what lies between
// back by one. It is how both reorderings move a single glyph.
func rotateIndicLeft(buf []Glyph, info []indicInfo, from, to int) {
	if from >= to || from < 0 || to >= len(buf) || to >= len(info) {
		return
	}
	g, f := buf[from], info[from]
	copy(buf[from:to], buf[from+1:to+1])
	copy(info[from:to], info[from+1:to+1])
	buf[to], info[to] = g, f
}
