package fonts

import (
	"sort"
	"unicode"
)

// Indic reordering: setting text whose characters are not stored in the order
// they are drawn.
//
// Every other script this package sets is drawn in the order it is written.
// Devanagari and its relatives are not. Three things move:
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
// The nine scripts that share the model: Bengali, Devanagari, Gujarati,
// Gurmukhi, Kannada, Malayalam, Oriya, Tamil and Telugu.
//
// The model is shared; the data is not, and the data is most of the work. Each
// script names its own virama and its own letter that can become a reph, states
// where in the syllable that reph is drawn and what sequence asks for one, says
// which consonants the below-base forms feature is for, and disagrees with the
// others about how far out from the base a vowel sign is drawn. All of that is
// in indicConfigs and indicMatraPosition, stated per script rather than branched
// on, and a script with none of it stated is not reordered at all.
//
// Both generations of each script's rules are covered. A script that has two
// OpenType specifications has two tags, and the tag a font declares its rules
// under says which of the two it was written against — see indicOldSpec.
//
// Khmer and Myanmar do *not* share this model and are deliberately absent from
// it. Each is its own shaper, with its own categories, its own syllable grammar
// and its own reordering — see khmer.go and myanmar.go. Shaping either by these
// rules would be worse than leaving it, since it would move glyphs by a grammar
// that is not theirs.
//
// The scripts the Universal Shaping Engine covers are absent and have no shaper
// of their own: they are set as they were before, their characters turned into
// glyphs in storage order with the font's default features applied. Text in
// them is not correctly set by this package and should be shaped elsewhere and
// passed in as glyph indices.
//
// These are not done:
//
//   - Asking the font about a consonant *with* surrounding context, which the
//     first-generation rules allow and the second do not. The question is always
//     put as a bare pair of glyphs, so a font that states its below-base or
//     post-base forms only as a contextual rule is read as stating none — its
//     conjuncts then come out as loose letters rather than in the wrong place.
//   - Canonical ordering of the marks in a syllable. Unicode allows a nukta and
//     a virama to be written in either order and says they mean the same thing;
//     this package sets them in the order they were written, so the one order a
//     font's rules are written against gets its conjunct and the other does not.
//     Splitting a vowel sign into the marks it is drawn as *is* done, because
//     the model cannot place such a sign at all while it is one character.
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
// the characters, and a syllable is the smallest piece of these scripts that can
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
	catMPst                         // a matra a syllable modifier may stand before
	catSMPst                        // a modifier with no side of its own

	// Khmer and Myanmar group some characters differently from the Indic
	// model, and name groups it has none of. The categories below are theirs
	// (khmer.go, myanmar.go); they are here because they are the same kind of
	// statement — what a character is within its syllable — and because the
	// per-glyph record and the feature machinery are shared.
	//
	// The four vowel-sign categories say which side of the letter a sign is
	// drawn on, which those two models read off the character rather than
	// asking the font, as the Indic one does.
	catVAbv     // a vowel sign drawn above the letter
	catVBlw     // one drawn below it
	catVPre     // one drawn before it, stored after
	catVPst     // one drawn after it
	catRobatic  // Khmer: a mark that may stand between a letter and its subscripts
	catXgroup   // Khmer: a mark that may stand before a vowel sign
	catYgroup   // Khmer: a mark that may stand only at the end of a syllable
	catAsat     // Myanmar: the asat, which kills the vowel of the letter before it
	catMedialY  // Myanmar: medial Ya, and the Mon letters written like it
	catMedialR  // Myanmar: medial Ra, which is drawn before the base
	catMedialW  // Myanmar: medial Wa, and the Shan Wa
	catMedialH  // Myanmar: medial Ha
	catMedialL  // Myanmar: the Mon medial La
	catPTone    // Myanmar: a Pwo or other tone mark
	catVS       // a variation selector, which takes the place of what it follows
	catAnusvara // Myanmar: a sign drawn over the syllable that the reordering counts
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

// indicRephMode says how a script writes the reph — the stroke that stands for
// a syllable-initial Ra.
type indicRephMode uint8

const (
	// rephImplicit: any syllable-opening Ra and virama makes one, if the font
	// has a form for the pair.
	rephImplicit indicRephMode = iota
	// rephExplicit: only Ra, virama and a zero-width joiner. Telugu writes a
	// bare Ra and virama for something else, so the joiner is how a writer asks
	// for the reph.
	rephExplicit
	// rephLogRepha: the script has a character of its own for the stroke, which
	// is written where it is read — before the syllable — and drawn where the
	// reph goes. Malayalam's chillu Ra is the one.
	rephLogRepha
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
	// virama is the character the font's conjunct rules are written against, and
	// so the one to ask those rules about. It is a plain letter by every Unicode
	// property it has, so it can only be named. (Which letter can become a reph
	// is named too, but per character rather than per script — see
	// indicCatOverrides.)
	virama rune
	// hasOldSpec says the script had a first-generation OpenType specification,
	// and so that a font declaring the older of its two tags means the older
	// rules. A script with only one specification never does.
	hasOldSpec bool
	// blwfMode says which consonants 'blwf' is asked for.
	blwfMode indicBlwfMode
	// rephPos is where in the syllable the reph is drawn, and rephMode how the
	// script writes it.
	rephPos  indicPos
	rephMode indicRephMode
	// doubleHalantBlocksMove says a virama already standing after the last
	// consonant stops the first-generation post-base virama move. Reports
	// differ script by script, so only the one known to want it says so.
	doubleHalantBlocksMove bool
	// hasEyelashRa says the script's first-generation rules ask for a below-base
	// form of a pre-base Ra.
	hasEyelashRa bool
	// swapsRaHalantJoiner says the script is written with the joiner after the
	// virama where the model expects it before, and that the two are to be
	// swapped. Kannada is written that way often enough that every shaper
	// accepts it.
	swapsRaHalantJoiner bool
	// hasHalfForms says the script draws half forms at all. Malayalam and Tamil
	// do not: what their 'half' feature makes is a chillu or a ligated virama,
	// which a pre-base vowel sign is drawn *after* rather than before, so
	// there is nothing for the sign to be moved back past.
	hasHalfForms bool
	// skipsUnformedBelowForms says the base moves on past a below-base consonant
	// the font declined to make a form for. Malayalam alone asks for this.
	skipsUnformedBelowForms bool
}

// indicConfigs is every script this file reorders, by its second-generation tag.
//
// Every one of them had a first-generation specification, so every one reads its
// font's tag to decide which rules the font means.
var indicConfigs = map[string]*indicConfig{
	"dev2": {
		tag: "dev2", virama: 0x094D, hasOldSpec: true,
		rephPos: posBeforePost, rephMode: rephImplicit, blwfMode: blwfPreAndPost,
		hasEyelashRa: true, hasHalfForms: true,
	},
	"bng2": {
		tag: "bng2", virama: 0x09CD, hasOldSpec: true,
		rephPos: posAfterSub, rephMode: rephImplicit, blwfMode: blwfPreAndPost,
		hasHalfForms: true,
	},
	"gur2": {
		tag: "gur2", virama: 0x0A4D, hasOldSpec: true,
		rephPos: posBeforeSub, rephMode: rephImplicit, blwfMode: blwfPreAndPost,
		hasHalfForms: true,
	},
	"gjr2": {
		tag: "gjr2", virama: 0x0ACD, hasOldSpec: true,
		rephPos: posBeforePost, rephMode: rephImplicit, blwfMode: blwfPreAndPost,
		hasHalfForms: true,
	},
	"ory2": {
		tag: "ory2", virama: 0x0B4D, hasOldSpec: true,
		rephPos: posAfterMain, rephMode: rephImplicit, blwfMode: blwfPreAndPost,
		hasHalfForms: true,
	},
	"tml2": {
		tag: "tml2", virama: 0x0BCD, hasOldSpec: true,
		rephPos: posAfterPost, rephMode: rephImplicit, blwfMode: blwfPreAndPost,
	},
	"tel2": {
		tag: "tel2", virama: 0x0C4D, hasOldSpec: true,
		rephPos: posAfterPost, rephMode: rephExplicit, blwfMode: blwfPostOnly,
		hasHalfForms: true,
	},
	"knd2": {
		tag: "knd2", virama: 0x0CCD, hasOldSpec: true,
		rephPos: posAfterPost, rephMode: rephImplicit, blwfMode: blwfPostOnly,
		hasHalfForms: true, swapsRaHalantJoiner: true, doubleHalantBlocksMove: true,
	},
	"mlm2": {
		tag: "mlm2", virama: 0x0D4D, hasOldSpec: true,
		rephPos: posAfterMain, rephMode: rephLogRepha, blwfMode: blwfPreAndPost,
		skipsUnformedBelowForms: true,
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
	case indicSylConsonant, indicSylConsonantDead,
		indicSylConsonantHeadLetter, indicSylConsonantInitialPostfixed:
		cat = catConsonant
	// A final, a subjoined and a succeeding repha are all consonants that hang
	// off the base rather than being one, which is what a medial consonant is.
	case indicSylConsonantMedial, indicSylConsonantFinal,
		indicSylConsonantSubjoined, indicSylConsonantSucceedingRepha:
		cat = catCM
	case indicSylConsonantWithStacker:
		cat = catCS
	case indicSylConsonantPrecedingRepha:
		cat = catRepha
	case indicSylConsonantPlaceholder, indicSylNumber,
		indicSylNumberJoiner, indicSylBrahmiJoiningNumber:
		cat = catPlaceholder
	case indicSylVowelIndependent, indicSylVowel:
		cat = catVowel
	// A killer takes the vowel off the letter before it, which is what a vowel
	// sign does to the letter's inherent vowel, so it is placed like one.
	case indicSylVowelDependent, indicSylPureKiller, indicSylConsonantKiller:
		cat = catMatra
	case indicSylNukta, indicSylToneMark:
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
		indicSylSyllableModifier:
		cat = catSM
		if pos == indicPosNotApplicable {
			// A modifier with no side of its own is drawn after the syllable
			// rather than over it, and the grammar lets one stand where a matra
			// would: the Gurmukhi bindi before its vowel sign is the case.
			cat = catSMPst
		}
	case indicSylCantillationMark:
		cat = catVD
	case indicSylAvagraha:
		cat = catSymbol
	case indicSylRegisterShifter:
		cat = catRS
	}
	if o, ok := indicCatOverrides[r]; ok {
		cat = o
	}
	return cat, indicPositionOf(r, cat, pos)
}

// indicCatOverrides are the characters whose shaping category is not the one
// their Unicode category implies.
//
// Each is a statement about that character in particular, which is why it can
// only be named. They come from the same reading of the script development
// specifications that every shaper works from.
var indicCatOverrides = map[rune]indicCat{
	// The one consonant of each script that can become a reph. Its Unicode
	// category is plain Consonant, like every other letter's. Bengali has two,
	// the second being the Assamese letter.
	0x0930: catRa, // Devanagari
	0x09B0: catRa, // Bengali
	0x09F0: catRa, // Bengali, Assamese
	0x0A30: catRa, // Gurmukhi
	0x0AB0: catRa, // Gujarati
	0x0B30: catRa, // Oriya
	0x0BB0: catRa, // Tamil
	0x0C30: catRa, // Telugu
	0x0CB0: catRa, // Kannada
	0x0D30: catRa, // Malayalam

	dottedCircle: catDottedCircle,

	// The two Devanagari accents behave as the bindus do, not as the
	// cantillation marks their category would suggest.
	0x0953: catSM,
	0x0954: catSM,

	// The Gurmukhi vowel sign II may be preceded by the bindi, which is what
	// the post-matra category is for.
	0x0A40: catMPst,
	// Two Gurmukhi letters that Unicode classifies as neither consonant nor
	// vowel but that a syllable is built on exactly as it is on a consonant.
	0x0A72: catConsonant,
	0x0A73: catConsonant,
	// A Gurmukhi sign that behaves as a vowel sign rather than a mark.
	0x0A51: catMatra,

	// Marks that take their own cluster, as the avagraha does.
	0xA8F2: catSymbol, 0xA8F3: catSymbol, 0xA8F4: catSymbol,
	0xA8F5: catSymbol, 0xA8F6: catSymbol, 0xA8F7: catSymbol,
	0x1CE9: catSymbol, 0x1CEA: catSymbol, 0x1CEB: catSymbol,
	0x1CEC: catSymbol, 0x1CEE: catSymbol, 0x1CEF: catSymbol,
	0x1CF0: catSymbol, 0x1CF1: catSymbol,

	// Vedic marks that are only valid after particular signs. Treating them as
	// ordinary tone marks is not right, but it is what every shaper does, and
	// the alternative is a rule about which sign each may follow.
	0x1CE2: catVD, 0x1CE3: catVD, 0x1CE4: catVD, 0x1CE5: catVD,
	0x1CE6: catVD, 0x1CE7: catVD, 0x1CE8: catVD, 0x1CED: catVD,

	// Grantha marks that Tamil also uses, so the Indic model has to know them.
	0x11301: catSM, 0x11302: catSM, 0x11303: catSM,
	0x1133B: catNukta, 0x1133C: catNukta,

	// Signs that modify the letter before them rather than standing alone.
	0x0AFB: catNukta, // Gujarati
	0x0B55: catNukta, // Oriya

	// Marks a syllable can be built on that are not letters.
	0x09FC: catPlaceholder, // Bengali
	0x0C80: catPlaceholder, // Kannada
	0x0D04: catPlaceholder, // Malayalam
}

// indicPosOverrides are the characters drawn somewhere other than where their
// Unicode positional category puts them.
var indicPosOverrides = map[rune]indicPos{
	0x0A51: posBelowC,    // Gurmukhi udaat
	0x0B01: posBeforeSub, // the Oriya bindu, which the specification places here
}

// indicCategories reports Unicode's two Indic categories for a character.
func indicCategories(r rune) (indicSyllabic, indicPosition) {
	i := sort.Search(len(indicRanges), func(i int) bool { return indicRanges[i].hi >= r })
	if i < len(indicRanges) && r >= indicRanges[i].lo {
		return indicRanges[i].syl, indicRanges[i].pos
	}
	return indicSylOther, indicPosNotApplicable
}

// indicPositionOf turns a character, its category and Unicode's positional
// category into the place a glyph takes in its syllable.
//
// A consonant is the base until the reordering decides otherwise; a syllable
// modifier or Vedic mark is always last; and a mark that attaches to the letter
// takes its place from which side of the letter it is drawn on. The compound
// positions — top-and-right, bottom-and-right — resolve to the last part of
// what they name, because that is the part whose place in the sequence decides
// where the whole thing goes.
//
// Only the categories that are drawn *relative to* the letter keep a place of
// their own: a medial consonant, a modifier, a register shifter, a virama and a
// vowel sign. Everything else has none, because nothing in the model asks — a
// nukta and a joiner take the place of what they follow, and a consonant's is
// decided by the base search.
//
// A vowel sign is the one that is not general: which side of the letter it is
// written on is Unicode's to say, but how far out from the base it is *drawn* is
// each script's own, and the scripts disagree — see indicMatraPosition.
func indicPositionOf(r rune, cat indicCat, pos indicPosition) indicPos {
	if p, ok := indicPosOverrides[r]; ok {
		return p
	}
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
	case indicIsMatra(cat):
		return indicMatraPosition(r, side)
	case cat == catSM || cat == catSMPst || cat == catVD || cat == catSymbol:
		return posSMVD
	case cat == catCM || cat == catRS || indicIsHalant(cat):
		return side
	}
	return posEnd
}

// indicScript names the blocks whose vowel signs are placed differently. It is
// the character's block rather than the run's script because the question is
// about the character: a Bengali vowel sign is drawn where Bengali draws it
// wherever it is written.
type indicScript uint8

const (
	scriptNotIndic indicScript = iota
	scriptDevanagariBlock
	scriptBengaliBlock
	scriptGurmukhiBlock
	scriptGujaratiBlock
	scriptOriyaBlock
	scriptTamilBlock
	scriptTeluguBlock
	scriptKannadaBlock
	scriptMalayalamBlock
)

// indicBlockOf reports which of the nine blocks a character is in. They are
// contiguous and 128 apart, which is why this is arithmetic rather than a table.
func indicBlockOf(r rune) indicScript {
	if r < 0x0900 || r > 0x0D7F {
		return scriptNotIndic
	}
	return scriptDevanagariBlock + indicScript((r-0x0900)/0x80)
}

// indicMatraPosition reports how far out from the base a vowel sign is drawn,
// given which side of the letter Unicode says it is written on.
//
// This is where the scripts disagree most, and it is not derivable: the same
// side means a different place in the drawing order in each of them. A Bengali
// right-side sign is drawn after everything, a Devanagari one after the
// below-base forms, a Telugu one before them. Getting it wrong puts a vowel sign
// on the wrong side of a conjunct, which every reader of the script sees at
// once and no test of Devanagari alone would catch.
//
// A sign written to the left is drawn before the base in every script, and that
// is the one rule they all share.
func indicMatraPosition(r rune, side indicPos) indicPos {
	if side == posPreC || side == posPreM {
		return posPreM
	}
	block := indicBlockOf(r)
	switch side {
	case posPostC:
		switch block {
		case scriptBengaliBlock, scriptGurmukhiBlock, scriptGujaratiBlock,
			scriptOriyaBlock, scriptTamilBlock, scriptMalayalamBlock:
			return posAfterPost
		case scriptTeluguBlock:
			if r <= 0x0C42 {
				return posBeforeSub
			}
			return posAfterSub
		case scriptKannadaBlock:
			if r < 0x0CC3 || r > 0x0CD6 {
				return posBeforeSub
			}
			return posAfterSub
		}
	case posAboveC:
		// Bengali and Malayalam have no above-base vowel signs, so neither
		// states a place for one.
		switch block {
		case scriptGurmukhiBlock:
			return posAfterPost
		case scriptOriyaBlock:
			return posAfterMain
		case scriptTeluguBlock, scriptKannadaBlock:
			return posBeforeSub
		}
	case posBelowC:
		switch block {
		case scriptGurmukhiBlock, scriptGujaratiBlock, scriptTamilBlock,
			scriptMalayalamBlock:
			return posAfterPost
		case scriptTeluguBlock, scriptKannadaBlock:
			return posBeforeSub
		}
	}
	// Devanagari's place, and the one every script falls back to.
	return posAfterSub
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

// indicIsMatra reports whether a character is a vowel sign — including the one
// kind a syllable modifier may stand in front of, which is a matra in every
// respect but where the grammar admits it.
func indicIsMatra(c indicCat) bool { return c == catMatra || c == catMPst }

// indicIsModifier reports whether a character is a syllable modifier.
func indicIsModifier(c indicCat) bool { return c == catSM || c == catSMPst }

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
	// ligated says the glyph is what a ligature substitution made of several
	// others, and has not since been taken apart again.
	//
	// It is a property of the *glyph*, not of the character, and only the
	// pre-base-reordering Ra needs it: a font may declare that consonant's
	// pre-base form generally and then decline to make it in some context, and
	// the only way to tell whether it declined is to look at what came out.
	// Moving a Ra the font left as an ordinary letter would draw a plain
	// consonant before the base.
	ligated bool
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
	{"rclt", false},
}

// shapeIndic is the whole Indic pass: it replaces both the joining pass and the
// default substitutions for a run it handles.
func (sh shaper) shapeIndic(buf []Glyph, runes []rune, plan *indicPlan) []Glyph {
	// Before anything is classified: a vowel followed by a sign that spells a
	// different vowel is shown against a dotted circle. It has to happen on the
	// characters, because it is about which characters were written, and it
	// changes the run that everything below is built from.
	buf, runes = sh.markInvalidVowels(buf, runes)
	buf, runes = sh.splitMatras(buf, runes)

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

// markInvalidVowels shows a dotted circle inside any sequence that spells a
// vowel nobody writes — an independent vowel followed by a sign that would make
// it look like a different vowel (indicvowel.go).
//
// It runs on the characters, before they are classified, because it is a claim
// about which characters were written rather than about the syllable they form,
// and because it changes the run everything below is built from: the circle it
// inserts is an ordinary character of the text from that point on, and the
// syllable cut sees it as one.
//
// A face with no U+25CC cannot show one, and the sequence is then set as it
// stands — which is what it would have been anyway.
func (sh shaper) markInvalidVowels(buf []Glyph, runes []rune) ([]Glyph, []rune) {
	if len(runes) < 2 {
		return buf, runes
	}
	gid, ok := sh.f.GlyphID(dottedCircle)
	if !ok {
		return buf, runes
	}
	outBuf := make([]Glyph, 0, len(buf)+4)
	outRunes := make([]rune, 0, len(runes)+4)
	for i := 0; i < len(runes); i++ {
		outBuf = append(outBuf, buf[i])
		outRunes = append(outRunes, runes[i])
		n := indicInvalidClusterAt(runes, i)
		if n == 0 {
			continue
		}
		// A three-character entry names the letter that opens it, the virama
		// that follows and the vowel it would spell; the circle goes before the
		// vowel, so the whole of the sequence up to it is copied first.
		for k := 1; k < n-1; k++ {
			i++
			outBuf = append(outBuf, buf[i])
			outRunes = append(outRunes, runes[i])
		}
		outBuf = append(outBuf, Glyph{
			GID: gid, Cluster: buf[i].Cluster, XAdvance: sh.f.advanceGID(gid),
		})
		outRunes = append(outRunes, dottedCircle)
	}
	return outBuf, outRunes
}

// splitMatras replaces each vowel sign that is written as one character and
// drawn as two or three marks by the marks it is drawn as (indicmatra.go).
//
// It is the shaping model's own second step, and it has to happen before
// anything is placed: the parts of a split sign go to *different* places, one
// before the letter and one after, so there is no single place the sign itself
// could be given. Tamil's o-sign is the plain case — U+0BCA is one character and
// two marks, and a shaper that kept it whole would draw the letter with both
// marks on the same side of it.
//
// A sign is only taken apart when the face has a glyph for every part. A face
// that draws the sign whole and has no glyph for one of its halves would
// otherwise lose that half altogether, which is worse than drawing the sign
// where the model would rather it were not.
func (sh shaper) splitMatras(buf []Glyph, runes []rune) ([]Glyph, []rune) {
	return sh.splitCharacters(buf, runes, indicSplitMatraOf)
}

// indicSplitMatraOf reports the marks a vowel sign is drawn as, if it is one of
// the signs drawn as more than one.
func indicSplitMatraOf(r rune) ([]rune, bool) {
	i := sort.Search(len(indicSplitMatras), func(i int) bool {
		return indicSplitMatras[i].r >= r
	})
	if i >= len(indicSplitMatras) || indicSplitMatras[i].r != r {
		return nil, false
	}
	parts := indicSplitMatras[i].parts[:]
	for len(parts) > 0 && parts[len(parts)-1] == 0 {
		parts = parts[:len(parts)-1]
	}
	return parts, true
}

// indicInvalidClusterAt reports the length of the invalid cluster starting at a
// position, or zero if none does.
func indicInvalidClusterAt(runes []rune, at int) int {
	i := sort.Search(len(indicInvalidClusters), func(i int) bool {
		return indicInvalidClusters[i][0] >= runes[at]
	})
	for ; i < len(indicInvalidClusters) && indicInvalidClusters[i][0] == runes[at]; i++ {
		c := indicInvalidClusters[i]
		n := len(c)
		if c[n-1] == 0 {
			n--
		}
		if at+n > len(runes) {
			continue
		}
		match := true
		for k := 1; k < n; k++ {
			if runes[at+k] != c[k] {
				match = false
				break
			}
		}
		if match {
			return n
		}
	}
	return 0
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
	return sh.insertGlyphAt(buf, info, at, gid, indicInfo{cat: catDottedCircle, pos: posEnd})
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

	// 'locl' and then 'ccmp' first, in that order: the one corrects letterforms
	// for the language, the other composes and decomposes, and everything after
	// them is written against what they produce. A script whose letters differ
	// from the shapes Unicode's chart shows — Odia is the case — states nearly
	// all of that difference in 'locl', so a run that skipped it would be set in
	// letters no reader of the language writes.
	//
	// They are applied per syllable, like the features below, so that neither can
	// join one syllable to the next.
	for _, tag := range []string{"locl", "ccmp"} {
		lookups := sh.l.featureLookups[tag]
		if len(lookups) == 0 {
			continue
		}
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

	sh.indicFinalReorder(buf, *info, plan, start, end)

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
		// Which glyphs are ligatures, which the pre-base-reordering Ra turns
		// on. A lookup that shortened the run ligated what it consumed; one
		// that lengthened it took a glyph apart, and the pieces are not
		// ligatures whatever the glyph they came from was.
		switch {
		case d < 0 && at < len(*info):
			(*info)[at].ligated = true
		case d > 0:
			for k := 0; k <= d && at+k < len(*info); k++ {
				(*info)[at+k].ligated = false
			}
		}
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

	// Kannada writes the eyelash Ra as Ra, virama, joiner where the model
	// expects Ra, joiner, virama, and enough text does that the specification
	// accepts it. The two are swapped so that the base search sees the sequence
	// it is written against.
	if plan.cfg.swapsRaHalantJoiner && start+3 <= end &&
		info[start].cat == catRa && info[start+1].cat == catHalant &&
		info[start+2].cat == catZWJ {
		buf[start+1], buf[start+2] = buf[start+2], buf[start+1]
		info[start+1], info[start+2] = info[start+2], info[start+1]
	}

	// A syllable opening with Ra + virama, with something after it, draws that
	// Ra as a reph — provided the font has one, which only the font can say.
	//
	// Which sequence asks for it is the script's. Most scripts take any Ra and
	// virama; Telugu writes that pair for something else, so a writer asks for
	// the reph with a joiner after it and the font is asked about all three.
	// Malayalam has a character of its own for the stroke, written before the
	// syllable and drawn at the end of it, so there is nothing to ask the font.
	rphf := sh.l.featureLookups["rphf"]
	switch {
	case plan.cfg.rephMode == rephLogRepha && info[start].cat == catRepha:
		limit++
		for limit < end && indicIsJoiner(info[limit].cat) {
			limit++
		}
		base = start
		hasReph = true

	case len(rphf) > 0 && start+3 <= end && info[start].cat == catRa &&
		indicIsHalant(info[start+1].cat) &&
		(plan.cfg.rephMode == rephImplicit && !indicIsJoiner(info[start+2].cat) ||
			plan.cfg.rephMode == rephExplicit && info[start+2].cat == catZWJ):

		probe := []int{buf[start].GID, buf[start+1].GID}
		if plan.cfg.rephMode == rephExplicit {
			probe = append(probe, buf[start+2].GID)
		}
		if sh.wouldSubstitute(rphf, probe[:2]) ||
			(plan.cfg.rephMode == rephExplicit && sh.wouldSubstitute(rphf, probe)) {
			limit += 2
			for limit < end && indicIsJoiner(info[limit].cat) {
				limit++
			}
			base = start
			hasReph = true
		}
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
		if !indicIsMatra(info[i].cat) {
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
			// A modifier written *before* the vowel sign it belongs with — the
			// Gurmukhi bindi and its II sign — goes where the sign goes, rather
			// than to the end with the other modifiers.
			if info[i].cat == catMPst && i > start && indicIsModifier(info[i-1].cat) {
				info[i-1].pos = info[i].pos
			}
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
		} else if indicIsMatra(info[i].cat) {
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

	// The pre-base-reordering Ra: a consonant standing *after* the base that is
	// nonetheless drawn before it. Which consonant that is, is the font's to
	// say and not the script's — Telugu and Kannada have one and Devanagari has
	// none — so the question put here is whether the font's 'pref' rules cover a
	// virama and the consonant after it, anywhere after the base. Only the first
	// such pair in a syllable is one: a syllable has at most one base to be
	// drawn before.
	//
	// Marking it is all that happens now. Whether it *moves* is settled after
	// the feature has run, since a font may decline to make the form.
	if len(plan.pref) > 0 && base+2 < end {
		for i := base + 1; i+1 < end; i++ {
			if sh.wouldSubstitute(plan.pref, []int{buf[i].GID, buf[i+1].GID}) {
				info[i].mask |= maskPref
				info[i+1].mask |= maskPref
				break
			}
		}
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
func (sh shaper) indicFinalReorder(buf []Glyph, info []indicInfo, plan *indicPlan, start, end int) {
	if start >= end {
		return
	}

	// The base again: the font may have ligated it into something else, so it
	// is found from the positions rather than remembered.
	// Whether there is still a pre-base-reordering Ra to move. The marked pair
	// may have been marked and then not formed, and the search below is where
	// that is found out.
	tryPref := len(plan.pref) > 0

	base := start
	for ; base < end; base++ {
		if info[base].pos >= posBaseC {
			// A pair marked as a pre-base-reordering Ra that the font declined
			// to make a form for is not one, and what stands there is an
			// ordinary consonant — which means the base is further on than the
			// search had it. The virama before that consonant is stepped over,
			// since a virama is never a base.
			if tryPref && base+1 < end {
				for i := base + 1; i < end; i++ {
					if info[i].mask&maskPref == 0 {
						continue
					}
					if !info[i].ligated {
						base = i
						for base < end && indicIsHalant(info[base].cat) {
							base++
						}
						if base < end {
							info[base].pos = posBaseC
						}
						tryPref = false
					}
					break
				}
				if base == end {
					break
				}
			}
			// Malayalam draws no half forms, so a consonant the font declined to
			// give a below-base form to is still a letter and is the base — the
			// search moves on to it rather than stopping at what precedes it.
			if plan.cfg.skipsUnformedBelowForms {
				for i := base + 1; i < end; i++ {
					for i < end && indicIsJoiner(info[i].cat) {
						i++
					}
					if i == end || !indicIsHalant(info[i].cat) {
						break
					}
					i++
					for i < end && indicIsJoiner(info[i].cat) {
						i++
					}
					if i < end && indicIsBaseCandidate(info[i].cat) && info[i].pos == posBelowC {
						base = i
						info[base].pos = posBaseC
					}
				}
			}
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
		// A script with no half forms has nothing for the sign to be moved back
		// past: what its 'half' feature makes is a chillu or a ligated virama,
		// and the sign is drawn after that rather than before it.
		for plan.cfg.hasHalfForms {
			for newPos > start && !indicIsMatra(info[newPos].cat) && !indicIsHalant(info[newPos].cat) {
				newPos--
			}
			if !(indicIsHalant(info[newPos].cat) && info[newPos].pos != posPreM) {
				newPos = start // no virama survived, so nothing moves
				break
			}
			// A joiner after that virama asked for the letters there to be
			// joined, so the sign does not stop at it and the search goes on.
			// A non-joiner is the opposite and stops it — which the syllable cut
			// has already taken care of, a virama and a non-joiner ending a
			// syllable, so any sign after one belongs to the next.
			if newPos+1 < end && info[newPos+1].cat == catZWJ && newPos > start {
				newPos--
				continue
			}
			break
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
	// that is where the font's rule for making it is written; where it is drawn
	// is each script's own answer.
	if start+1 < end && info[start].pos == posRaToBecomeReph {
		if newPos := indicRephPosition(info, plan, start, end, base); newPos > start {
			if start < base && base <= newPos {
				base--
			}
			rotateIndicLeft(buf, info, start, newPos)
		}
	}

	// The pre-base-reordering Ra. It was left where it was written through the
	// substitutions, because that is where the font's rule for making it is
	// written; it is drawn before the base, in the same place a pre-base vowel
	// sign is drawn — after the half forms, immediately before the base.
	//
	// It moves only if the font actually made the form. A font may declare the
	// pre-base form for a consonant generally and block it in some context, and
	// a Ra it left as an ordinary letter is an ordinary letter: moving it would
	// draw a plain consonant before the base, which is not a thing the script
	// writes.
	if tryPref && base+1 < end {
		for i := base + 1; i < end; i++ {
			if info[i].mask&maskPref == 0 {
				continue
			}
			if info[i].ligated {
				newPos := base
				// A script with no half forms has nothing for the consonant to
				// be moved back past, exactly as for a pre-base vowel sign.
				if plan.cfg.hasHalfForms {
					for newPos > start && !indicIsMatra(info[newPos-1].cat) &&
						info[newPos-1].cat != catHalant {
						newPos--
					}
				}
				// A joiner after that virama asked for the letters there to be
				// joined, so the consonant is drawn after it.
				if newPos > start && indicIsHalant(info[newPos-1].cat) &&
					newPos < end && indicIsJoiner(info[newPos].cat) {
					newPos++
				}
				if newPos < i {
					rotateIndicRight(buf, info, newPos, i)
					if newPos <= base && base < i {
						base++
					}
				}
			}
			break
		}
	}
}

// rotateIndicRight moves the glyph at from back to to, shifting what lies
// between forward by one. It is the mirror of rotateIndicLeft, and the
// pre-base-reordering Ra is the one thing that travels this way: everything
// else the final reordering moves is drawn later than it was written.
func rotateIndicRight(buf []Glyph, info []indicInfo, to, from int) {
	if to >= from || to < 0 || from >= len(buf) || from >= len(info) {
		return
	}
	g, f := buf[from], info[from]
	copy(buf[to+1:from+1], buf[to:from])
	copy(info[to+1:from+1], info[to:from])
	buf[to], info[to] = g, f
}

// indicRephPosition reports where in a syllable the reph is drawn.
//
// The scripts disagree, and the disagreement is the point of the reph_pos field:
// Oriya draws it straight after the main consonant, Bengali after the subjoined
// forms, Gurmukhi before them, Devanagari and Gujarati before the post-base
// forms, and Tamil, Telugu and Kannada after everything.
//
// The steps below are the specification's, in its order. Reading them:
//
//   - A script that draws the reph after everything skips straight to the last
//     two, since no earlier place can apply to it.
//   - Otherwise the first explicit virama still standing between the reph and
//     the base takes it: that is where a half form ends and the reph can sit on
//     what the half form made. This is the usual answer for a modern font.
//   - Failing that, the script's own class decides: after the main consonant, or
//     after the subjoined forms, whichever it states.
//   - Failing that, the end of the syllable — but inside the modifiers, which
//     are drawn last of all. An anusvara is drawn over the syllable and the reph
//     belongs under it, and a font commonly has one glyph for the two together
//     which it can only make if they are in that order.
func indicRephPosition(info []indicInfo, plan *indicPlan, start, end, base int) int {
	// The first virama still standing before the base. Two of the steps want it,
	// so it is asked for once.
	afterFirstHalant := func() (int, bool) {
		at := start + 1
		for at < base && !indicIsHalant(info[at].cat) {
			at++
		}
		if at >= base || !indicIsHalant(info[at].cat) {
			return 0, false
		}
		// A joiner after that virama belongs with it, and the reph goes past
		// both — the joiner asked for the form the reph is to sit on.
		if at+1 < base && indicIsJoiner(info[at+1].cat) {
			at++
		}
		return at, true
	}

	if plan.cfg.rephPos != posAfterPost {
		if at, ok := afterFirstHalant(); ok {
			return at
		}
		switch plan.cfg.rephPos {
		case posAfterMain:
			at := base
			for at+1 < end && info[at+1].pos <= posAfterMain {
				at++
			}
			return at
		case posAfterSub:
			at := base
			for at+1 < end && !indicAfterReph(info[at+1].pos) {
				at++
			}
			return at
		}
	} else if at, ok := afterFirstHalant(); ok {
		return at
	}

	// The end of the syllable, before the modifiers.
	at := end - 1
	for at > start && info[at].pos == posSMVD {
		at--
	}
	// A reph that would land after a matra and its virama goes before that
	// virama instead, so that it can combine with the matra. A plain consonant
	// and virama are not this case.
	if indicIsHalant(info[at].cat) {
		for i := base + 1; i < at; i++ {
			if indicIsMatra(info[i].cat) {
				at--
			}
		}
	}
	return at
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
