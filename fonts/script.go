package fonts

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/pdf0/internal/font"
)

// Choosing which of a font's rules apply to a run of text.
//
// A font that covers several scripts states different rules for each, and says
// so: its GSUB and GPOS tables begin with a ScriptList, which names each script
// the font covers, the language systems within it, and the features each of
// those selects. Reading the features without reading that list gives a Greek
// word the rules written for Arabic — which is what this package used to do, and
// what this file exists to stop.
//
// # Three questions, answered separately
//
// Which script is the text in? That is Unicode's Script property, generated
// into scripts.go from the UCD. Common and Inherited characters — a space, a
// digit, a combining accent — are not text in a script of their own and take
// the script of what they are written among.
//
// What does OpenType call that script? A four-byte tag, derived from the ISO
// 15924 code, also in scripts.go. Some scripts have two, and both are tried.
//
// What does the font select for it? The ScriptList walk below. A script that
// the font does not declare falls back to 'DFLT', the conventional tag for "any
// script"; a font with no ScriptList at all, or one that declares nothing this
// run can use, falls back to taking every feature — the behaviour this package
// had before, so that no font that worked stops working.
//
// # Language
//
// A language system narrows a script's selection further, and its *required*
// feature applies whether or not anything asked for it. Which language a run is
// in cannot be read off its characters, so the default language system is used
// unless a caller names one with Face.SetLanguage.

// defaultScriptTags are tried, in order, after the run's own script.
//
// 'DFLT' is the registered tag for "whatever script this is". 'dflt' is what a
// font written by a tool that got the case wrong carries, and enough of them
// exist that every shaper accepts it. 'latn' is the last resort, and is there
// for the same reason every shaper has it: a great many fonts state all their
// features under Latin and nowhere else, meaning them generally, and a reader
// that stopped at 'dflt' would set their text with no features at all.
var defaultScriptTags = []string{"DFLT", "dflt", "latn"}

// noRequiredFeature is the value a language system uses to say it has no
// feature that applies unconditionally.
const noRequiredFeature = 0xFFFF

// scriptOf reports the Unicode script of a character.
func scriptOf(r rune) uint16 {
	i := sort.Search(len(scriptRanges), func(i int) bool { return scriptRanges[i].hi >= r })
	if i < len(scriptRanges) && r >= scriptRanges[i].lo {
		return scriptRanges[i].script
	}
	return scriptUnknown
}

// decides reports whether a script settles what a run is written in. Common,
// Inherited and Unknown do not: a run of digits, or an accent on its own, is
// written in whatever surrounds it.
func decides(script uint16) bool {
	return script != scriptCommon && script != scriptInherited && script != scriptUnknown
}

// runScript reports the script of a run of text: the first one that decides
// anything, or scriptUnknown if none does.
//
// The first rather than the most common, because a run is meant to be in one
// script — Stack.ShapeRuns splits text so that it is — and where a caller shapes
// mixed text through a Face directly, the opening script is the one whose rules
// that text was written to be set by.
func runScript(s string) uint16 {
	for _, r := range s {
		if sc := scriptOf(r); decides(sc) {
			return sc
		}
	}
	return scriptUnknown
}

// scriptTags is the OpenType tags a script selects, most specific first.
func scriptTags(script uint16) []string {
	if int(script) < len(scriptOpenTypeTags) {
		return scriptOpenTypeTags[script]
	}
	return nil
}

// scriptFeatures returns the FeatureList indices the given script tags and
// language select in a GSUB or GPOS table, and whether the table settled the
// question at all.
//
// A false second result means the table declares no scripts, or none that
// matched even the default — so there is nothing to select by and the caller
// should take every feature.
func scriptFeatures(t []byte, tags []string, lang string) (featureSet, bool) {
	if len(t) < 10 {
		return nil, false
	}
	off := font.Be16(t, 4)
	if off <= 0 || off+2 > len(t) {
		return nil, false
	}
	list := t[off:]
	byTag := scriptOffsets(list)
	for _, tag := range append(append(make([]string, 0, len(tags)+2), tags...), defaultScriptTags...) {
		so, ok := byTag[tag]
		if !ok {
			continue
		}
		ls, ok := readLangSys(list[so:], lang)
		if !ok {
			continue
		}
		sel := make(featureSet, len(ls.features)+1)
		// The required feature applies whether or not anything asked for it,
		// which is the whole of what "required" means here.
		if ls.required != noRequiredFeature {
			sel[ls.required] = true
		}
		for _, i := range ls.features {
			sel[i] = true
		}
		return sel, true
	}
	return nil, false
}

// scriptOffsets maps each script tag a ScriptList names to its Script table's
// offset within the list. A tag declared twice keeps its first table, which is
// the one a reader walking the list in order would find.
func scriptOffsets(list []byte) map[string]int {
	n := font.Be16(list, 0)
	if n > maxScripts {
		n = maxScripts
	}
	out := make(map[string]int, n)
	for i := 0; i < n; i++ {
		rec := 2 + 6*i
		if rec+6 > len(list) {
			break
		}
		tag := string(list[rec : rec+4])
		off := font.Be16(list, rec+4)
		if off <= 0 || off+4 > len(list) {
			continue
		}
		if _, dup := out[tag]; !dup {
			out[tag] = off
		}
	}
	return out
}

// langSys is what one language system of a script selects.
type langSys struct {
	// required is the feature that applies unconditionally, or
	// noRequiredFeature.
	required int
	features []int
}

// readLangSys reads a Script table's language system: the named one if the
// script declares it, and the default otherwise.
//
// A script that declares neither is unusable, and reports so rather than
// selecting nothing — the caller then tries the next script tag, and failing
// that falls back to taking every feature. Selecting nothing would set the text
// with no ligatures, no kerning and no joining at all, which is a worse answer
// than the one this package gave before it read scripts.
func readLangSys(script []byte, lang string) (langSys, bool) {
	if len(script) < 4 {
		return langSys{}, false
	}
	off := 0
	if lang != "" {
		n := font.Be16(script, 2)
		if n > maxLangSys {
			n = maxLangSys
		}
		for i := 0; i < n; i++ {
			rec := 4 + 6*i
			if rec+6 > len(script) {
				break
			}
			if string(script[rec:rec+4]) != lang {
				continue
			}
			off = font.Be16(script, rec+4)
			break
		}
	}
	if off == 0 {
		off = font.Be16(script, 0) // DefaultLangSys
	}
	if off <= 0 || off+6 > len(script) {
		return langSys{}, false
	}
	ls := script[off:]
	out := langSys{required: font.Be16(ls, 2)}
	n := font.Be16(ls, 4)
	if n > maxLookups {
		n = maxLookups
	}
	for i := 0; i < n; i++ {
		if 6+2*i+2 > len(ls) {
			break
		}
		out.features = append(out.features, font.Be16(ls, 6+2*i))
	}
	return out, true
}

// SetLanguage names the OpenType language system to shape in: "TRK " for
// Turkish, "ROM " for Romanian, and so on — four bytes, space-padded, as the
// OpenType language system registry spells them.
//
// It matters because a font states some rules per language. The same letters
// are drawn differently in Romanian and in French, and a font that knows the
// difference says so through a language system; without one it is set the
// default way, which is right for most text and wrong for the text the font
// went to the trouble of correcting.
//
// The empty string, the initial value, means the default language system.
// A language the font does not declare falls back to it too, so a caller may
// name one without first checking that the face has it.
func (f *Face) SetLanguage(tag string) { f.language = tag }

// Language reports the language system set with SetLanguage.
func (f *Face) Language() string { return f.language }

// Scripts lists the OpenType script tags the face declares layout rules for,
// in sorted order — "latn", "cyrl", "deva" and so on, plus "DFLT" where the
// font names a default.
//
// It is the question a caller asks when assembling a fallback stack: a face may
// have the *glyphs* for a script and none of the rules that make it legible,
// and for Devanagari or Arabic the difference between the two is a row of
// unjoined letters. Covers answers the first question; this answers the second.
//
// A face whose tables name no scripts at all returns nothing, which is not the
// same as covering nothing: such a font's features apply to everything.
func (f *Face) Scripts() []string {
	seen := map[string]bool{}
	for _, table := range []string{"GSUB", "GPOS"} {
		for tag := range scriptOffsets(scriptList(f.layoutTables[table])) {
			seen[tag] = true
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sortStrings(out)
	return out
}

// HasScript reports whether the face declares layout rules for a script tag.
func (f *Face) HasScript(tag string) bool {
	for _, table := range []string{"GSUB", "GPOS"} {
		if _, ok := scriptOffsets(scriptList(f.layoutTables[table]))[tag]; ok {
			return true
		}
	}
	return false
}

// scriptList is the ScriptList of a GSUB or GPOS table, which is where the
// script tags live — the table itself only points at it.
//
// It returns nothing rather than the table when the offset is missing or out of
// range, so a caller reading tags from a malformed font finds none rather than
// reading the header as though it were a list of tags.
func scriptList(t []byte) []byte {
	if len(t) < 10 {
		return nil
	}
	off := font.Be16(t, 4)
	if off <= 0 || off+2 > len(t) {
		return nil
	}
	return t[off:]
}

// shaper is a face together with the rules that apply to the run being shaped.
//
// The two are separate because a face has more than one set of rules: one per
// script it covers, and per language within that. Everything that reads the
// layout tables hangs off this rather than off the face, so that it cannot
// reach the wrong one — a Face has no single layout to reach for.
type shaper struct {
	f *Face
	l *layout

	// rtl says the run will be drawn right to left, and is what the positioning
	// pass needs to know. Everything before positioning works in the order the
	// text is written and is the same either way; positioning states where a
	// glyph sits relative to the pen, and the pen will meet the run's glyphs in
	// the opposite order.
	rtl bool

	// floor and limit bound the glyphs a lookup may look at: it may not match,
	// or backtrack, outside [floor, limit). They exist for the Indic pass,
	// which applies a font's features one syllable at a time — a ligature
	// reaching into the next syllable would join glyphs the font never meant to
	// see together. A zero limit means the whole buffer, which is what every
	// other script gets.
	floor, limit int

	// onResize, when set, is told wherever a lookup changes the buffer's
	// length: at which position, and by how much. The Indic pass sets it to
	// keep its per-glyph record — categories, positions, feature masks — in
	// step with a buffer that ligatures and decompositions are reshaping under
	// it. Nothing else needs it, and it is nil everywhere else.
	onResize func(at, delta int)
}

// resized reports a change in the buffer's length at a position, for a caller
// keeping something in step with it.
func (sh shaper) resized(at, delta int) {
	if sh.onResize != nil && delta != 0 {
		sh.onResize(at, delta)
	}
}

// end is one past the last glyph a lookup may look at.
func (sh shaper) end(buf []Glyph) int {
	if sh.limit > 0 && sh.limit < len(buf) {
		return sh.limit
	}
	return len(buf)
}

// layoutFor reads the font's layout tables as the given script selects them,
// caching the result.
//
// The caches are keyed by what was *selected* rather than by the script, so two
// scripts a font treats identically — the common case, a face declaring the
// same features for 'latn', 'grek' and 'cyrl' — share one reading of tables
// that run to tens of kilobytes. The positioning half is cached on its own key,
// because a font that varies its substitutions per script usually does not vary
// its kerning, and the kerning is the large table.
func (f *Face) layoutFor(script uint16) *layout {
	if len(f.layoutTables) == 0 {
		return f.layout
	}
	tags := scriptTags(script)
	gsubSel, gsubOK := scriptFeatures(f.layoutTables["GSUB"], tags, f.language)
	gposSel, gposOK := scriptFeatures(f.layoutTables["GPOS"], tags, f.language)
	if !gsubOK && !gposOK {
		// Neither table says anything about scripts, so there is nothing to
		// select by: every feature applies, which is what f.layout already is.
		return f.layout
	}
	gsubKey, gposKey := selectionKey(gsubSel), selectionKey(gposSel)
	if l, ok := f.scriptLayouts[gsubKey+"/"+gposKey]; ok {
		return l
	}
	pos, ok := f.positionings[gposKey]
	if !ok {
		pos = readPositioning(f.layoutTables, gposSel)
		if f.positionings == nil {
			f.positionings = map[string]*layout{}
		}
		f.positionings[gposKey] = pos
	}
	l := readLayout(f.layoutTables, gsubSel, pos)
	if f.scriptLayouts == nil {
		f.scriptLayouts = map[string]*layout{}
	}
	f.scriptLayouts[gsubKey+"/"+gposKey] = l
	return l
}

// selectionKey names a selection, so that two scripts selecting the same
// features share one layout. A nil selection — take everything — is named
// distinctly from an empty one, which takes nothing.
func selectionKey(sel featureSet) string {
	if sel == nil {
		return "*"
	}
	idx := make([]int, 0, len(sel))
	for i := range sel {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	var b strings.Builder
	for _, i := range idx {
		b.WriteString(strconv.Itoa(i))
		b.WriteByte(',')
	}
	return b.String()
}
