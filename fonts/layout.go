package fonts

import (
	"github.com/mgilbir/pdf0/internal/font"
)

// Reading the OpenType layout tables: what the font says about how its glyphs
// combine, and turning that into the substitutions and positions a run of text
// needs.
//
// # What is here
//
//   - Pair kerning: 'kern', GPOS lookup type 2, both formats, and the legacy
//     kern table for fonts that predate GPOS.
//   - Single positioning (GPOS 1), mark-to-base (GPOS 4) and mark-to-mark
//     (GPOS 6), so an accent sits over the letter it belongs to and a second
//     accent stacks on the first — see position.go.
//   - Substitution: single (GSUB 1), multiple (GSUB 2), alternate (GSUB 3) and
//     ligature (GSUB 4). An alternate set is taken at its first entry, which is
//     the font's own preference and the only answer available to a lookup no
//     caller asked for.
//   - Contextual and chained-contextual substitution (GSUB 5 and 6), all six
//     formats, which is what makes 'calt' — and any rule that depends on
//     surroundings — do anything at all. See context.go.
//   - Cursive joining: the positional forms Arabic and its neighbours are
//     written in, chosen from Unicode joining types. See arabic.go.
//   - Cursive attachment (GPOS 3), which makes those forms' connecting strokes
//     actually meet — joining picks the shapes, this places them. See
//     position.go.
//   - Indic reordering, for the nine scripts that share the model: cutting a run
//     into syllables, finding each one's base consonant, and putting its glyphs
//     into the order they are drawn — the pre-base vowel sign before its
//     consonant, the reph over the end of the syllable — before applying the
//     features an Indic font declares for each part. See indic.go and
//     indicsyllable.go.
//   - Every single substitution the font declares, keyed by feature tag and
//     applied only when a caller names one (ShapeWith): 'smcp', 'onum' and the
//     rest, which change what the text says it is and so wait to be asked for.
//   - FeatureVariations, the GSUB and GPOS table that gives a feature different
//     lookups at different points in a variable font's design space. A face may
//     state a feature's lookups only there — Noto Sans Oriya states its 'rclt'
//     that way — and reading it as stating none costs that feature entirely.
//   - Normalisation: the run put into whichever spelling this face draws best —
//     composed where the face has the composed character, decomposed where it
//     does not — and each cluster's marks put into canonical order, so that a
//     font's rules match text written either way. See normalize.go.
//   - GDEF glyph classes and the lookup flags that use them, so that a lookup
//     declaring it ignores marks does.
//   - The zero-width joiner and non-joiner: obeyed where they are written about,
//     stepped over by every rule that is not about them, and removed before
//     anything is positioned or drawn. See ignorable.go, which also says which
//     of Unicode's other default-ignorable characters are *not* handled.
//
// # What is not, and what each absence costs
//
//   - Reordering for the scripts the Universal Shaping Engine covers — Tibetan,
//     Javanese, Balinese, Buginese, Tai Tham, Cham, Sinhala and a long tail.
//     Their tags are still selected, because that is where such a font declares
//     its features, but their characters are turned into glyphs in storage
//     order, so text in them is not correctly set here and should be shaped
//     elsewhere and passed in as glyph indices.
//
//     The engine is not one more shaper. It needs a category table over all of
//     Unicode whose two normative override files are not in the published
//     sources, a record of *which* substitution produced each glyph that this
//     package does not keep, and a joining model over syllables that interacts
//     with Arabic joining. Half of it shipped would move glyphs by a grammar
//     that is not theirs, which is worse than leaving them in storage order.
//
//     Devanagari and its eight relatives (indic.go), Khmer (khmer.go) and
//     Myanmar (myanmar.go) *are* reordered; syllabic.go chooses between the
//     three models, and each file says what within its own is left out.
//   - Every point in a variable font's design space but the default one.
//     FeatureVariations is read, and read for the coordinates in force — which
//     are the default instance's, because nothing here instances a font: the
//     subsetter drops fvar and gvar, and what reaches a document is the default
//     instance. A record whose conditions do not cover the default is a rule for
//     a weight this module never sets, and is not applied.
//   - 'rclt' anywhere but an Indic run. It is a required feature and every other
//     shaper applies it generally; here only the Indic pass does, because that
//     is where its absence was measured.
//   - The mark reordering Arabic wants on top of canonical order. Normalisation
//     is done — a run is composed or decomposed to whatever this face draws
//     best, and each cluster's marks are put in canonical order, see
//     normalize.go — but a hamza written after a vowel is drawn before it by a
//     rule that is Arabic's rather than Unicode's, and that rule is not applied.
//   - Choosing a language from the text. Which script a run is in is decidable
//     from its characters; which language it is in is not — "colour" and "color"
//     are the same letters — so the default language system is used unless a
//     caller names one (Face.SetLanguage).
//
// # Script and language selection
//
// A font states its rules per script: a ScriptList names each script it covers,
// each script names its language systems, and each language system names the
// features that apply. Shaping resolves the run's script from Unicode (see
// scripts.go), selects the features that script and language name, and reads
// the tables from those alone — so a Greek run is not given a rule the font
// declares only for Arabic.
//
// A font with no ScriptList, or one that declares nothing for the run's script
// and no default either, falls back to taking every feature whatever declares
// it. That is what this package did before there was any selection at all, and
// it is the right answer for a table that says nothing about scripts.
//
// # Bounds
//
// A font is untrusted input. Every table here is offset-driven and
// self-referential, so each walk is bounded: the number of lookups, subtables,
// pairs, ligatures, variation records and conditions a font may declare are all
// capped, and a malformed offset truncates the walk rather than reaching outside
// the table.

// Layout bounds. These are far above any real face — a large Latin font
// declares a few thousand kern pairs — and exist so that a crafted font cannot
// turn a few bytes of declaration into unbounded work.
const (
	maxLookups   = 512
	maxSubtables = 256
	maxPairs     = 1 << 18
	maxLigatures = 1 << 14
	maxScripts   = 256
	maxLangSys   = 256
	// The FeatureVariations walk. A real face states a handful of records — Noto
	// Sans Oriya states one — and each names a few conditions and a few
	// substituted features.
	maxVariationRecords = 256
	maxConditions       = 64
	maxFeatureSubsts    = 256
)

// featureSet is the set of FeatureList indices a script and language select.
//
// A nil set means no selection was made — the table declares no scripts, or
// none that matched — and every feature is taken, which is what this package
// did before it read the ScriptList. An empty but non-nil set means the
// selection was made and chose nothing, which is a different thing and has to
// stay distinguishable from it.
type featureSet map[int]bool

// selects reports whether a feature at the given index in the FeatureList
// applies.
func (s featureSet) selects(index int) bool { return s == nil || s[index] }

// featureSubst is the lookup list a FeatureVariations record puts in place of
// the one a feature names for itself, keyed by FeatureList index.
//
// An entry present but empty is not the same as no entry: it says the record
// gives that feature *no* lookups here, which silences a feature the FeatureList
// declares. So every reader asks with the two-value form.
type featureSubst map[int][]int

// tableFeatures is what one layout table offers a run: the FeatureList indices
// the run's script and language selected, and the lookup lists FeatureVariations
// substitutes at the coordinates in force.
//
// The two travel together because every reader below needs both, and because
// taking one without the other is a mistake that produces plausible output — a
// feature applied with the wrong lookups, or a feature the run never selected.
type tableFeatures struct {
	sel    featureSet
	varied featureSubst
}

// readFeatureVariations reads the FeatureVariations table: the GSUB or GPOS
// table that gives a feature different lookups at different points in a variable
// font's design space.
//
// # Which coordinates are in force
//
// The default instance's, on every axis. This module does not instance a
// variable font and has no way to be asked to: the subsetter drops fvar, gvar
// and avar, and in such a font the glyf outlines already *are* the default
// instance, so what reaches a document is a static face at the default weight.
//
// In the normalized coordinate space these conditions are written in, the
// default instance is zero on every axis by construction — fvar normalizes each
// axis's default value to zero, and avar's segment map is required to carry
// (0, 0) through. So the coordinates are all zero, there is nothing to read out
// of fvar, and a condition holds exactly when its range spans zero. A record
// stated for some other part of the design space is a rule for a weight this
// module never sets, and applying it would set the text by the wrong rules.
//
// This is not only a variable-font concern. A static instance cut from a
// variable font may keep the table, and a face may state at the default instance
// — Noto Sans Oriya states its whole 'rclt' feature in a record that covers it —
// exactly the case that made this worth reading.
//
// The first record whose conditions hold is the one that applies, and the rest
// are not consulted: the specification makes the records an ordered search, not
// a set to merge.
func readFeatureVariations(t []byte) featureSubst {
	// Version 1.1 of the table header is what carries the offset; a 1.0 header
	// ends before it.
	if len(t) < 14 || font.Be16(t, 0) != 1 || font.Be16(t, 2) < 1 {
		return nil
	}
	off := int(font.Be32(t, 10))
	if off <= 0 || off+8 > len(t) {
		return nil
	}
	fv := t[off:]
	if font.Be16(fv, 0) != 1 {
		return nil
	}
	n := int(font.Be32(fv, 4))
	if n > maxVariationRecords {
		n = maxVariationRecords
	}
	for i := 0; i < n; i++ {
		rec := 8 + 8*i
		if rec+8 > len(fv) {
			break
		}
		if !conditionSetHolds(fv, int(font.Be32(fv, rec))) {
			continue
		}
		return featureTableSubstitution(fv, int(font.Be32(fv, rec+4)))
	}
	return nil
}

// conditionSetHolds reports whether every condition of a set holds at the
// default instance.
//
// A null offset is the empty set, which the specification says matches
// everywhere — a record that applies unconditionally. A set this cannot read
// whole does not hold: half a condition set is not a weaker condition set, it is
// no knowledge of what the record was for.
func conditionSetHolds(fv []byte, off int) bool {
	if off == 0 {
		return true
	}
	if off < 0 || off+2 > len(fv) {
		return false
	}
	cs := fv[off:]
	n := font.Be16(cs, 0)
	if n > maxConditions {
		return false
	}
	for i := 0; i < n; i++ {
		if 2+4*i+4 > len(cs) {
			return false
		}
		co := int(font.Be32(cs, 2+4*i))
		if co <= 0 || co+2 > len(cs) {
			return false
		}
		if !conditionHolds(cs[co:]) {
			return false
		}
	}
	return true
}

// conditionHolds reports whether one condition holds at the default instance.
//
// Only format 1, an axis range, is read. The formats OpenType added later state
// a condition on a variable value, or combine other conditions with and, or and
// not; a condition this cannot evaluate is treated as not holding, because a
// record applied on a guess is a record applied at the wrong weight.
//
// The axis index is not needed. A condition's range is compared against the
// coordinate of one axis, and every coordinate in force is zero — including, per
// the specification, an axis index the font does not have. So the condition
// holds exactly when its range spans zero, whichever axis it names.
func conditionHolds(c []byte) bool {
	if len(c) < 8 || font.Be16(c, 0) != 1 {
		return false
	}
	lo := signed16(font.Be16(c, 4))
	hi := signed16(font.Be16(c, 6))
	return lo <= 0 && hi >= 0
}

// featureTableSubstitution reads the lookup lists a matching record substitutes,
// by FeatureList index.
func featureTableSubstitution(fv []byte, off int) featureSubst {
	if off <= 0 || off+6 > len(fv) {
		return nil
	}
	ts := fv[off:]
	if font.Be16(ts, 0) != 1 {
		return nil
	}
	n := font.Be16(ts, 4)
	if n > maxFeatureSubsts {
		n = maxFeatureSubsts
	}
	out := make(featureSubst, n)
	for i := 0; i < n; i++ {
		rec := 6 + 6*i
		if rec+6 > len(ts) {
			break
		}
		index := font.Be16(ts, rec)
		ao := int(font.Be32(ts, rec+2))
		if ao <= 0 || ao+4 > len(ts) {
			continue
		}
		alt := ts[ao:]
		m := font.Be16(alt, 2)
		if m > maxLookups {
			m = maxLookups
		}
		lookups := make([]int, 0, m)
		for j := 0; j < m; j++ {
			if 4+2*j+2 > len(alt) {
				break
			}
			lookups = append(lookups, font.Be16(alt, 4+2*j))
		}
		out[index] = lookups
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// featureLookupList is the lookup indices one FeatureList entry names — its own,
// unless a FeatureVariations record put another list in its place.
func featureLookupList(list []byte, index int, varied featureSubst) []int {
	if lookups, ok := varied[index]; ok {
		return lookups
	}
	rec := 2 + 6*index
	if rec+6 > len(list) {
		return nil
	}
	off := font.Be16(list, rec+4)
	if off <= 0 || off+4 > len(list) {
		return nil
	}
	feature := list[off:]
	n := font.Be16(feature, 2)
	if n > maxLookups {
		n = maxLookups
	}
	out := make([]int, 0, n)
	for j := 0; j < n; j++ {
		if 4+2*j+2 > len(feature) {
			break
		}
		out = append(out, font.Be16(feature, 4+2*j))
	}
	return out
}

// layout holds what was read out of a font's layout tables.
type layout struct {
	// glyphClass is GDEF's classification of each glyph: 1 base, 2 ligature,
	// 3 mark, 4 component. A glyph GDEF does not name is class 0, unknown.
	glyphClass map[int]int
	// kernFlags and substFlags are the lookup flags of the lookups the kerning
	// and the substitutions came from, so that shaping can skip the glyphs
	// those lookups are declared to ignore.
	kernFlags  int
	substFlags int
	// markAttach is GDEF's mark attachment class per glyph, used by the
	// MarkAttachmentType field of a lookup flag.
	markAttach map[int]int
	// markFlags is the lookup flags of the attachment lookups.
	markFlags int
	// cursive holds each glyph's entry and exit points, and cursFlags the flags
	// of the lookups they came from — whose RightToLeft bit decides which end of
	// a joined run stays on the baseline.
	cursive   map[int]cursiveAnchors
	cursFlags int
	// singlePos holds GPOS type 1 adjustments by glyph.
	singlePos map[int]singleAdjust
	// markAnchors holds each mark's own attachment point and class;
	// markBases and markMarkBases hold where a base or another mark receives a
	// mark of each class.
	markAnchors   map[int]markAnchor
	markBases     map[key2]anchor
	markMarkBases map[key2]anchor
	// kern maps an ordered glyph pair to the horizontal adjustment between
	// them, in font units. Negative pulls the pair together, which is what
	// kerning almost always does.
	kern map[[2]int]int
	// ligatures maps a first glyph to the substitutions that may start with it,
	// longest first so that a greedy match prefers ffi over ff.
	//
	// This serves the span path (Shape) only. The glyph path applies 'liga'
	// through the lookup list below, which honours the lookup's flags — so an
	// accent written between two letters does not stop them ligating.
	ligatures map[int][]ligature
	// single holds one-for-one substitutions per feature tag: small capitals,
	// oldstyle figures and the rest. They are read for every feature the font
	// declares, and applied only when a caller asks for that feature by name.
	single map[string]map[int]int
	// gsub is the substitution lookup list kept whole, indexed as the font
	// indexes it. A contextual rule names a lookup by its index here and has it
	// applied at a position, so these cannot be flattened the way the tables
	// above are — see context.go.
	gsub []rawLookup
	// featureLookups maps a feature tag to the lookup indices it names, which is
	// how a feature is turned into work to do.
	featureLookups map[string][]int
}

// Lookup flags (ISO/IEC 14496-22, LookupFlag). The high byte is a mark
// attachment class rather than a flag, and is handled separately.
const (
	// flagRightToLeft relates only to cursive attachment (GPOS 3): it says the
	// *last* glyph of a joined run stays on the baseline and the earlier ones
	// move to meet it, rather than the first.
	flagRightToLeft      = 0x0001
	flagIgnoreBaseGlyphs = 0x0002
	flagIgnoreLigatures  = 0x0004
	flagIgnoreMarks      = 0x0008
	flagMarkAttachType   = 0xFF00
)

// Glyph classes as GDEF defines them.
const (
	classBase     = 1
	classLigature = 2
	classMark     = 3
	// classComponent is named for completeness: GDEF defines it, and a reader
	// of this list should see the whole set rather than wonder what 4 means.
	classComponent = 4
)

// ignores reports whether a lookup with the given flags skips a glyph.
//
// This is the correctness fix these flags exist for. A kerning lookup almost
// always declares that it ignores marks, because the pair it means to adjust is
// two base letters — and an accent written between them must not break it.
// Reading the pairs and not the flag kerns "A" and "V" but not "Ä" and "V",
// which is a difference a reader sees.
func (l *layout) ignores(flags, gid int) bool {
	class := l.glyphClass[gid]
	switch {
	case flags&flagIgnoreMarks != 0 && class == classMark:
		return true
	case flags&flagIgnoreBaseGlyphs != 0 && class == classBase:
		return true
	case flags&flagIgnoreLigatures != 0 && class == classLigature:
		return true
	}
	// A mark attachment class in the high byte narrows the rule to marks of one
	// class; marks of any other are skipped.
	if attach := (flags & flagMarkAttachType) >> 8; attach != 0 && class == classMark {
		return l.markAttach[gid] != attach
	}
	return false
}

// readGDEF reads the glyph classification, which is what makes a lookup flag
// mean anything: without it there is no way to know which glyphs are marks.
func (l *layout) readGDEF(gdef []byte) {
	if len(gdef) < 12 {
		return
	}
	if off := font.Be16(gdef, 4); off > 0 && off < len(gdef) {
		for gid, class := range classDef(gdef, off) {
			l.glyphClass[gid] = class
		}
	}
	if off := font.Be16(gdef, 10); off > 0 && off < len(gdef) {
		l.markAttach = classDef(gdef, off)
	}
}

// ligature is one substitution: a run of glyphs replaced by a single one.
type ligature struct {
	components []int // the glyphs after the first
	glyph      int   // what they become, together with the first
}

// readPositioning parses everything GDEF, GPOS and the legacy kern table say,
// taking the GPOS features the given selection admits.
//
// It is separate from the substitution half because the two are cached
// separately, and because it is the expensive one: a large face states tens of
// thousands of kern pairs, and it commonly states the same ones for every
// script it covers while stating *different* substitutions for each. Reading it
// once per script would multiply the largest table in the font by the number of
// scripts a document sets, to no end.
//
// It never fails: a table that cannot be understood contributes nothing,
// because text set without kerning is correct text set plainly, while text set
// from a misread table is wrong.
func readPositioning(tables map[string][]byte, sel featureSet) *layout {
	l := &layout{
		kern:          map[[2]int]int{},
		glyphClass:    map[int]int{},
		singlePos:     map[int]singleAdjust{},
		markAnchors:   map[int]markAnchor{},
		markBases:     map[key2]anchor{},
		markMarkBases: map[key2]anchor{},
		cursive:       map[int]cursiveAnchors{},
	}
	l.readGDEF(tables["GDEF"])
	if gpos := tables["GPOS"]; len(gpos) >= 10 {
		feats := tableFeatures{sel: sel, varied: readFeatureVariations(gpos)}
		l.readGPOSKerning(gpos, feats)
		l.readGPOSAttachment(gpos, feats)
	}
	if len(l.kern) == 0 {
		// Only as a fallback: a font with both should be read through GPOS,
		// which is the one a modern shaper honours.
		l.readKernTable(tables["kern"])
	}
	return l
}

// readLayout reads the substitution tables on top of an already-read
// positioning half, taking the GSUB features the given selection admits. The
// selection is the FeatureList indices the run's script and language chose; a
// nil one takes every feature, which is what a table with no ScriptList gets.
//
// The positioning half is copied whole and then the substitution fields are
// reset, rather than the other way about, so that a positioning field added to
// layout later is carried across without this having to be remembered. The maps
// it copies are shared with every other layout built on the same half, and are
// never written to once read.
func readLayout(tables map[string][]byte, gsubSel featureSet, pos *layout) *layout {
	l := new(layout)
	*l = *pos
	l.ligatures = map[int][]ligature{}
	l.single = map[string]map[int]int{}
	l.gsub = nil
	l.featureLookups = nil
	l.substFlags = 0
	if gsub := tables["GSUB"]; len(gsub) >= 10 {
		feats := tableFeatures{sel: gsubSel, varied: readFeatureVariations(gsub)}
		l.readGSUBLigatures(gsub, feats)
		l.readSingleSubstitutions(gsub, feats)
		l.gsub = gsubLookups(gsub)
		l.featureLookups = featureLookupIndices(gsub, feats)
	}
	return l
}

// gsubLookups reads the substitution lookup list whole: each lookup's type, its
// flags and its subtable bytes, at the index the font gives it.
//
// This is the list a contextual rule indexes into. It duplicates what the
// flattened readers above take from the same bytes, which is deliberate: those
// serve the common path cheaply, and a lookup that may be invoked from inside
// another has to survive as something applicable rather than as a map entry.
func gsubLookups(gsub []byte) []rawLookup {
	off := font.Be16(gsub, 8)
	if off <= 0 || off+2 > len(gsub) {
		return nil
	}
	list := gsub[off:]
	n := font.Be16(list, 0)
	if n > maxLookups {
		n = maxLookups
	}
	out := make([]rawLookup, 0, n)
	for i := 0; i < n; i++ {
		if 2+2*i+2 > len(list) {
			break
		}
		lo := font.Be16(list, 2+2*i)
		if lo <= 0 || lo >= len(list) {
			// Keep the slot: a rule names a lookup by index, so the indices of
			// the ones that follow must not shift.
			out = append(out, rawLookup{})
			continue
		}
		kind, flags, subs := subtables(list[lo:], 7)
		out = append(out, rawLookup{kind: kind, flags: flags, subs: subs})
	}
	return out
}

// featureLookupIndices maps each feature tag to the lookup indices it names,
// merging every feature the selection admits — which for a font that declares
// its scripts is every one the run's script and language chose, and for one
// that does not is every feature in the table.
func featureLookupIndices(t []byte, feats tableFeatures) map[string][]int {
	out := map[string][]int{}
	off := font.Be16(t, 6)
	if off <= 0 || off+2 > len(t) {
		return out
	}
	list := t[off:]
	n := font.Be16(list, 0)
	if n > maxLookups {
		n = maxLookups
	}
	for i := 0; i < n; i++ {
		rec := 2 + 6*i
		if rec+6 > len(list) {
			break
		}
		if !feats.sel.selects(i) {
			continue
		}
		tag := string(list[rec : rec+4])
		for _, idx := range featureLookupList(list, i, feats.varied) {
			if !containsInt(out[tag], idx) {
				out[tag] = append(out[tag], idx)
			}
		}
	}
	return out
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// featureLookups returns the lookup-table byte slices reachable from every
// feature with the given tag that the selection admits.
//
// A tag may appear in the FeatureList many times — a face with a dozen 'locl'
// features is ordinary, one per language it corrects letterforms for — and it
// is the selection, not the tag, that says which of them this run gets.
func featureLookups(t []byte, tag string, feats tableFeatures) [][]byte {
	featureListOff := font.Be16(t, 6)
	lookupListOff := font.Be16(t, 8)
	if featureListOff <= 0 || lookupListOff <= 0 ||
		featureListOff+2 > len(t) || lookupListOff+2 > len(t) {
		return nil
	}
	featureList := t[featureListOff:]
	lookupList := t[lookupListOff:]

	// The lookup list, so a feature's indices can be resolved.
	lookupCount := font.Be16(lookupList, 0)
	if lookupCount > maxLookups {
		lookupCount = maxLookups
	}
	lookups := make([][]byte, 0, lookupCount)
	for i := 0; i < lookupCount; i++ {
		if 2+2*i+2 > len(lookupList) {
			break
		}
		off := font.Be16(lookupList, 2+2*i)
		if off <= 0 || off >= len(lookupList) {
			lookups = append(lookups, nil)
			continue
		}
		lookups = append(lookups, lookupList[off:])
	}

	var out [][]byte
	featureCount := font.Be16(featureList, 0)
	if featureCount > maxLookups {
		featureCount = maxLookups
	}
	for i := 0; i < featureCount; i++ {
		rec := 2 + 6*i
		if rec+6 > len(featureList) {
			break
		}
		if string(featureList[rec:rec+4]) != tag || !feats.sel.selects(i) {
			continue
		}
		for _, idx := range featureLookupList(featureList, i, feats.varied) {
			if idx >= 0 && idx < len(lookups) && lookups[idx] != nil {
				out = append(out, lookups[idx])
			}
		}
	}
	return out
}

// subtables returns a lookup's subtables, resolving the extension indirection
// that lets a large font place them beyond the 16-bit offset range.
func subtables(lookup []byte, extensionType int) (kind, flags int, out [][]byte) {
	if len(lookup) < 6 {
		return 0, 0, nil
	}
	kind = font.Be16(lookup, 0)
	flags = font.Be16(lookup, 2)
	count := font.Be16(lookup, 4)
	if count > maxSubtables {
		count = maxSubtables
	}
	// Whether the *lookup* is an extension has to be decided once. Reading it
	// from kind inside the loop stops unwrapping after the first subtable, since
	// unwrapping is what replaces kind with the real type.
	extension := kind == extensionType
	for i := 0; i < count; i++ {
		if 6+2*i+2 > len(lookup) {
			break
		}
		off := font.Be16(lookup, 6+2*i)
		if off <= 0 || off >= len(lookup) {
			continue
		}
		sub := lookup[off:]
		if extension {
			// Extension: format(2), real lookup type(2), 32-bit offset.
			if len(sub) < 8 {
				continue
			}
			kind = font.Be16(sub, 2)
			delta := int(font.Be32(sub, 4))
			if delta <= 0 || delta >= len(sub) {
				continue
			}
			sub = sub[delta:]
		}
		out = append(out, sub)
	}
	return kind, flags, out
}

// readGPOSKerning reads pair adjustments from every 'kern' feature this run's
// script selected.
func (l *layout) readGPOSKerning(gpos []byte, feats tableFeatures) {
	for _, lookup := range featureLookups(gpos, "kern", feats) {
		kind, flags, subs := subtables(lookup, 9) // 9 = extension positioning
		if kind != 2 {                            // 2 = pair adjustment
			continue
		}
		l.kernFlags |= flags
		for _, sub := range subs {
			if len(sub) < 2 {
				continue
			}
			switch font.Be16(sub, 0) {
			case 1:
				l.pairPosFormat1(sub)
			case 2:
				l.pairPosFormat2(sub)
			}
		}
	}
}

// pairPosFormat1: an explicit list of second glyphs for each covered first
// glyph.
func (l *layout) pairPosFormat1(sub []byte) {
	if len(sub) < 10 {
		return
	}
	first := coverageGlyphs(sub, font.Be16(sub, 2))
	fmt1, fmt2 := font.Be16(sub, 4), font.Be16(sub, 6)
	// Only a horizontal advance on the first glyph is kerning; anything else in
	// the record is a positioning this package does not apply, and it is
	// skipped over rather than misread.
	size1, size2 := valueSize(fmt1), valueSize(fmt2)
	pairSetCount := font.Be16(sub, 8)
	for i := 0; i < pairSetCount && i < len(first); i++ {
		if 10+2*i+2 > len(sub) {
			return
		}
		off := font.Be16(sub, 10+2*i)
		if off <= 0 || off+2 > len(sub) {
			continue
		}
		set := sub[off:]
		n := font.Be16(set, 0)
		rec := 2
		for j := 0; j < n; j++ {
			if rec+2+size1+size2 > len(set) || len(l.kern) >= maxPairs {
				break
			}
			second := font.Be16(set, rec)
			if adv, ok := xAdvance(set[rec+2:], fmt1); ok && adv != 0 {
				l.kern[[2]int{first[i], second}] = adv
			}
			rec += 2 + size1 + size2
		}
	}
}

// pairPosFormat2: adjustments by class pair, which is how a large font states
// thousands of pairs compactly.
func (l *layout) pairPosFormat2(sub []byte) {
	if len(sub) < 16 {
		return
	}
	covered := coverageGlyphs(sub, font.Be16(sub, 2))
	fmt1, fmt2 := font.Be16(sub, 4), font.Be16(sub, 6)
	class1 := classDef(sub, font.Be16(sub, 8))
	class2 := classDef(sub, font.Be16(sub, 10))
	n1, n2 := font.Be16(sub, 12), font.Be16(sub, 14)
	size1, size2 := valueSize(fmt1), valueSize(fmt2)
	recSize := size1 + size2
	if n1 <= 0 || n2 <= 0 || recSize == 0 {
		return
	}

	// Invert class 2 once, so each first glyph costs one pass over its own
	// class row rather than a scan of every glyph in the font.
	byClass2 := map[int][]int{}
	for gid, c := range class2 {
		byClass2[c] = append(byClass2[c], gid)
	}
	for _, g1 := range covered {
		c1 := class1[g1]
		if c1 >= n1 {
			continue
		}
		for c2 := 0; c2 < n2; c2++ {
			off := 16 + (c1*n2+c2)*recSize
			if off+recSize > len(sub) {
				break
			}
			adv, ok := xAdvance(sub[off:], fmt1)
			if !ok || adv == 0 {
				continue
			}
			for _, g2 := range byClass2[c2] {
				if len(l.kern) >= maxPairs {
					return
				}
				l.kern[[2]int{g1, g2}] = adv
			}
		}
	}
}

// valueSize is the byte length of a ValueRecord with the given format: two
// bytes per bit set (ISO/IEC 14496-22, ValueFormat).
func valueSize(format int) int {
	n := 0
	for b := 0; b < 8; b++ {
		if format&(1<<b) != 0 {
			n += 2
		}
	}
	return n
}

// xAdvance reads the horizontal advance out of a ValueRecord, which is present
// only when bit 2 of the format says so and sits after any placement fields.
func xAdvance(rec []byte, format int) (int, bool) {
	const xAdvanceBit = 0x0004
	if format&xAdvanceBit == 0 {
		return 0, false
	}
	off := 0
	if format&0x0001 != 0 { // XPlacement
		off += 2
	}
	if format&0x0002 != 0 { // YPlacement
		off += 2
	}
	if off+2 > len(rec) {
		return 0, false
	}
	return signed16(font.Be16(rec, off)), true
}

// coverageGlyphs returns the glyphs a coverage table covers, in coverage-index
// order — which is the order the tables that use it index by.
func coverageGlyphs(base []byte, off int) []int {
	if off <= 0 || off+4 > len(base) {
		return nil
	}
	c := base[off:]
	switch font.Be16(c, 0) {
	case 1:
		n := font.Be16(c, 2)
		out := make([]int, 0, n)
		for i := 0; i < n; i++ {
			if 4+2*i+2 > len(c) {
				break
			}
			out = append(out, font.Be16(c, 4+2*i))
		}
		return out
	case 2:
		n := font.Be16(c, 2)
		var out []int
		for i := 0; i < n; i++ {
			rec := 4 + 6*i
			if rec+6 > len(c) {
				break
			}
			start, end := font.Be16(c, rec), font.Be16(c, rec+2)
			idx := font.Be16(c, rec+4)
			if end < start || end-start > maxPairs {
				continue
			}
			for g := start; g <= end; g++ {
				at := idx + (g - start)
				for len(out) <= at {
					out = append(out, 0)
				}
				out[at] = g
			}
		}
		return out
	}
	return nil
}

// classDef returns the class of each glyph a class-definition table names.
// Glyphs it does not name are class 0, which is the specification's default and
// the zero value here.
func classDef(base []byte, off int) map[int]int {
	out := map[int]int{}
	if off <= 0 || off+4 > len(base) {
		return out
	}
	c := base[off:]
	switch font.Be16(c, 0) {
	case 1:
		start := font.Be16(c, 2)
		n := font.Be16(c, 4)
		for i := 0; i < n && i < maxPairs; i++ {
			if 6+2*i+2 > len(c) {
				break
			}
			out[start+i] = font.Be16(c, 6+2*i)
		}
	case 2:
		n := font.Be16(c, 2)
		for i := 0; i < n; i++ {
			rec := 4 + 6*i
			if rec+6 > len(c) {
				break
			}
			from, to, class := font.Be16(c, rec), font.Be16(c, rec+2), font.Be16(c, rec+4)
			if to < from || to-from > maxPairs {
				continue
			}
			for g := from; g <= to && len(out) < maxPairs; g++ {
				out[g] = class
			}
		}
	}
	return out
}

// readGSUBLigatures reads ligature substitutions from every 'liga' feature this
// run's script selected.
func (l *layout) readGSUBLigatures(gsub []byte, feats tableFeatures) {
	for _, lookup := range featureLookups(gsub, "liga", feats) {
		kind, flags, subs := subtables(lookup, 7) // 7 = extension substitution
		if kind != 4 {                            // 4 = ligature substitution
			continue
		}
		l.substFlags |= flags
		for _, sub := range subs {
			l.ligatureSubst(sub)
		}
	}
}

func (l *layout) ligatureSubst(sub []byte) {
	if len(sub) < 6 || font.Be16(sub, 0) != 1 {
		return
	}
	first := coverageGlyphs(sub, font.Be16(sub, 2))
	setCount := font.Be16(sub, 4)
	for i := 0; i < setCount && i < len(first); i++ {
		if 6+2*i+2 > len(sub) {
			return
		}
		off := font.Be16(sub, 6+2*i)
		if off <= 0 || off+2 > len(sub) {
			continue
		}
		set := sub[off:]
		n := font.Be16(set, 0)
		for j := 0; j < n; j++ {
			if 2+2*j+2 > len(set) {
				break
			}
			lo := font.Be16(set, 2+2*j)
			if lo <= 0 || lo+4 > len(set) {
				continue
			}
			lig := set[lo:]
			glyph := font.Be16(lig, 0)
			compCount := font.Be16(lig, 2)
			if compCount < 2 || compCount > 64 {
				continue
			}
			comps := make([]int, 0, compCount-1)
			ok := true
			for k := 0; k < compCount-1; k++ {
				if 4+2*k+2 > len(lig) {
					ok = false
					break
				}
				comps = append(comps, font.Be16(lig, 4+2*k))
			}
			if !ok || len(l.ligatures) >= maxLigatures {
				continue
			}
			l.ligatures[first[i]] = append(l.ligatures[first[i]], ligature{components: comps, glyph: glyph})
		}
	}
	// Longest first, so a greedy match prefers ffi to ff.
	for g, ligs := range l.ligatures {
		sortLigaturesLongestFirst(ligs)
		l.ligatures[g] = ligs
	}
}

func sortLigaturesLongestFirst(ligs []ligature) {
	for i := 1; i < len(ligs); i++ {
		for j := i; j > 0 && len(ligs[j].components) > len(ligs[j-1].components); j-- {
			ligs[j], ligs[j-1] = ligs[j-1], ligs[j]
		}
	}
}

// readKernTable reads the legacy kern table, format 0, for fonts written before
// GPOS. Only horizontal, non-cross-stream, override-free subtables are taken;
// the rest describe positioning this package does not apply.
func (l *layout) readKernTable(kern []byte) {
	if len(kern) < 4 {
		return
	}
	nTables := font.Be16(kern, 2)
	off := 4
	for i := 0; i < nTables && i < maxSubtables; i++ {
		if off+6 > len(kern) {
			return
		}
		length := font.Be16(kern, off+2)
		coverage := font.Be16(kern, off+4)
		// Bit 0 horizontal, bit 1 minimum, bit 2 cross-stream, bit 3 override;
		// format is the high byte.
		if coverage&0x0001 != 0 && coverage&0x000E == 0 && coverage>>8 == 0 {
			l.kernFormat0(kern[off+6:])
		}
		if length <= 0 {
			return
		}
		off += length
	}
}

func (l *layout) kernFormat0(t []byte) {
	if len(t) < 8 {
		return
	}
	n := font.Be16(t, 0)
	for i := 0; i < n; i++ {
		rec := 8 + 6*i
		if rec+6 > len(t) || len(l.kern) >= maxPairs {
			return
		}
		left, right := font.Be16(t, rec), font.Be16(t, rec+2)
		if v := signed16(font.Be16(t, rec+4)); v != 0 {
			l.kern[[2]int{left, right}] = v
		}
	}
}

// readSingleSubstitutions reads the one-for-one substitutions of every feature
// this run's script selected, keyed by tag.
//
// They are read eagerly and applied only on request. A font's 'smcp' turns
// letters into small capitals and its 'onum' turns lining figures into oldstyle
// ones; both are correct only when a caller asks for them, so unlike 'liga'
// they cannot be applied by default. Reading them all costs one pass and means
// ShapeWith needs no second one.
func (l *layout) readSingleSubstitutions(gsub []byte, feats tableFeatures) {
	if len(gsub) < 10 {
		return
	}
	featureListOff := font.Be16(gsub, 6)
	if featureListOff <= 0 || featureListOff+2 > len(gsub) {
		return
	}
	featureList := gsub[featureListOff:]
	count := font.Be16(featureList, 0)
	if count > maxLookups {
		count = maxLookups
	}
	seen := map[string]bool{}
	for i := 0; i < count; i++ {
		rec := 2 + 6*i
		if rec+6 > len(featureList) {
			break
		}
		tag := string(featureList[rec : rec+4])
		if seen[tag] || !feats.sel.selects(i) {
			continue
		}
		seen[tag] = true
		for _, lookup := range featureLookups(gsub, tag, feats) {
			kind, flags, subs := subtables(lookup, 7)
			if kind != 1 { // 1 = single substitution
				continue
			}
			l.substFlags |= flags
			for _, sub := range subs {
				l.singleSubst(tag, sub)
			}
		}
	}
}

// singleSubst reads one single-substitution subtable. Format 1 shifts every
// covered glyph by a constant; format 2 lists a replacement for each.
func (l *layout) singleSubst(tag string, sub []byte) {
	if len(sub) < 6 {
		return
	}
	covered := coverageGlyphs(sub, font.Be16(sub, 2))
	if l.single[tag] == nil {
		l.single[tag] = map[int]int{}
	}
	switch font.Be16(sub, 0) {
	case 1:
		delta := signed16(font.Be16(sub, 4))
		for _, gid := range covered {
			if to := gid + delta; to >= 0 && to < 0xFFFF {
				l.single[tag][gid] = to
			}
		}
	case 2:
		n := font.Be16(sub, 4)
		for i := 0; i < n && i < len(covered); i++ {
			if 6+2*i+2 > len(sub) {
				break
			}
			l.single[tag][covered[i]] = font.Be16(sub, 6+2*i)
		}
	}
	if len(l.single[tag]) == 0 {
		delete(l.single, tag)
	}
}

// emptyLayout is a layout that says nothing, for a face with no tables to read
// — a standard font, whose metrics are published rather than embedded.
func emptyLayout() *layout {
	return &layout{
		kern:          map[[2]int]int{},
		ligatures:     map[int][]ligature{},
		glyphClass:    map[int]int{},
		single:        map[string]map[int]int{},
		singlePos:     map[int]singleAdjust{},
		markAnchors:   map[int]markAnchor{},
		markBases:     map[key2]anchor{},
		markMarkBases: map[key2]anchor{},
		cursive:       map[int]cursiveAnchors{},
	}
}
