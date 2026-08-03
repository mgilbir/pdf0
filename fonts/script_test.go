package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Script and language selection: giving a run the rules the font wrote for it,
// and not the ones it wrote for something else.
//
// The fixture below is the shape of the whole problem in four glyphs. One
// character, 'x', is substituted by two different rules carrying the same
// feature tag — which is what a real face looks like, Noto Sans declaring
// twelve separate 'locl' features — and the only thing that decides which of
// them applies is the ScriptList. A reader that does not consult it applies the
// first, always, and the test that catches it is one where the two rules
// disagree.

const (
	scX     = 1 + iota // 'x', the glyph both rules cover
	scY                // what the first rule makes of it
	scZ                // what the second rule makes of it
	scAlpha            // 'α', so that a run can be Greek
)

// scriptFace builds a face whose GSUB carries two 'ccmp' features — one
// substituting x with y, the other x with z — selected by the given script
// list.
func scriptFace(t *testing.T, scripts map[string]fonttest.Script) *Face {
	t.Helper()
	return scriptFaceTagged(t, "ccmp", scripts)
}

// scriptFaceTagged is scriptFace with the tag the two rules carry chosen by the
// caller: 'ccmp' for a feature applied to every run, 'salt' for one a caller
// has to ask for by name.
func scriptFaceTagged(t *testing.T, tag string, scripts map[string]fonttest.Script) *Face {
	t.Helper()
	gsub := fonttest.GSUBTable(
		[]fonttest.Lookup{
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scY})}},
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scZ})}},
		},
		[]fonttest.Feature{
			{Tag: tag, Lookups: []int{0}},
			{Tag: tag, Lookups: []int{1}},
		},
		scripts,
	)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Scripts",
		Glyphs: []fonttest.Glyph{
			{Rune: 'x', Advance: 500, HasShape: true},
			{Rune: 'y', Advance: 500, HasShape: true},
			{Rune: 'z', Advance: 500, HasShape: true},
			{Rune: 'α', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{"GSUB": gsub},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// lastGID is the glyph a string's final character shaped to, which for these
// fixtures is always the x the two rules disagree about.
func lastGID(t *testing.T, f *Face, s string) int {
	t.Helper()
	gids := shapedGIDs(t, f, s)
	if len(gids) == 0 {
		t.Fatalf("shaping %q produced no glyphs", s)
	}
	return gids[len(gids)-1]
}

// TestScriptSelectsItsOwnRule is the point of the whole file: a Latin run gets
// the rule the font declares for Latin, and a Greek run the one it declares for
// Greek, though both rules cover the same glyph and carry the same tag.
func TestScriptSelectsItsOwnRule(t *testing.T) {
	f := scriptFace(t, map[string]fonttest.Script{
		"latn": {Required: fonttest.NoFeature, Features: []int{0}},
		"grek": {Required: fonttest.NoFeature, Features: []int{1}},
	})

	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("Latin run: x shaped to glyph %d, want %d — the rule declared for 'latn'", got, scY)
	}
	// The Greek letter decides the run's script; the x in it is then set by
	// what the font declares for Greek.
	if got := lastGID(t, f, "αx"); got != scZ {
		t.Errorf("Greek run: x shaped to glyph %d, want %d — the rule declared for 'grek'", got, scZ)
	}
}

// TestUndeclaredScriptFallsBackToDefault pins the conventional default tag. A
// font that declares only 'DFLT' means its rules for everything, so a Greek run
// takes them — and takes only them, which is why the fixture puts the second
// rule there and not the first.
func TestUndeclaredScriptFallsBackToDefault(t *testing.T) {
	f := scriptFace(t, map[string]fonttest.Script{
		"DFLT": {Required: fonttest.NoFeature, Features: []int{1}},
	})
	if got := lastGID(t, f, "αx"); got != scZ {
		t.Errorf("x shaped to glyph %d, want %d — 'DFLT' is what an undeclared script falls back to", got, scZ)
	}
}

// TestLatinIsTheLastResort covers the font that states everything under 'latn'
// and declares no default script at all. There are a great many of them, they
// mean their features generally, and a reader that stopped at 'dflt' would set
// every non-Latin run in them with no features whatever — so 'latn' is tried
// last, after the two default tags, exactly as every shaper tries it.
func TestLatinIsTheLastResort(t *testing.T) {
	f := scriptFace(t, map[string]fonttest.Script{
		"latn": {Required: fonttest.NoFeature, Features: []int{1}},
	})
	if got := lastGID(t, f, "αx"); got != scZ {
		t.Errorf("Greek run in a Latin-only font: x shaped to glyph %d, want %d — "+
			"'latn' is the last resort, and it selects only the second rule", got, scZ)
	}
}

// TestScriptWithNoLanguageSystemIsSkipped covers a script that names neither a
// default language system nor the one asked for. There is nothing to take from
// it, so the next candidate — the default script — decides, rather than the run
// being shaped with no rules at all.
func TestScriptWithNoLanguageSystemIsSkipped(t *testing.T) {
	f := scriptFace(t, map[string]fonttest.Script{
		"grek": {Required: fonttest.NoFeature, NoDefault: true},
		"DFLT": {Required: fonttest.NoFeature, Features: []int{1}},
	})
	if got := lastGID(t, f, "αx"); got != scZ {
		t.Errorf("x shaped to glyph %d, want %d: 'grek' selects nothing, so 'DFLT' decides", got, scZ)
	}
}

// TestNoScriptListTakesEveryFeature is the fallback that keeps a font which
// says nothing about scripts working exactly as it did. Every feature applies,
// in the order the font lists them, whatever the run is written in.
func TestNoScriptListTakesEveryFeature(t *testing.T) {
	// An empty map is an empty ScriptList — a well-formed table that declares
	// no scripts — which is different from not building one.
	f := scriptFace(t, map[string]fonttest.Script{})

	for _, s := range []string{"x", "αx"} {
		if got := lastGID(t, f, s); got != scY {
			t.Errorf("%q: x shaped to glyph %d, want %d — with no script list every feature applies, first one winning", s, got, scY)
		}
	}
}

// TestLanguageSelectsItsOwnRule pins language systems. The same script sets the
// same letters differently in different languages, and a font that knows the
// difference says so through a LangSysRecord.
func TestLanguageSelectsItsOwnRule(t *testing.T) {
	f := scriptFace(t, map[string]fonttest.Script{
		"latn": {
			Required: fonttest.NoFeature,
			Features: []int{0},
			Langs: map[string]fonttest.LangSys{
				"ROM ": {Required: fonttest.NoFeature, Features: []int{1}},
			},
		},
	})

	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("default language: x shaped to glyph %d, want %d", got, scY)
	}
	f.SetLanguage("ROM ")
	if f.Language() != "ROM " {
		t.Errorf("Language() = %q after SetLanguage(%q)", f.Language(), "ROM ")
	}
	if got := lastGID(t, f, "x"); got != scZ {
		t.Errorf("Romanian: x shaped to glyph %d, want %d — the rule the language system names", got, scZ)
	}
	// A language the font does not declare falls back to the default system
	// rather than selecting nothing.
	f.SetLanguage("NAV ")
	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("undeclared language: x shaped to glyph %d, want %d — the default language system", got, scY)
	}
}

// TestRequiredFeatureAppliesUnasked pins what "required" means: the language
// system's required feature applies although it is in no list of selected
// features and no caller named it.
func TestRequiredFeatureAppliesUnasked(t *testing.T) {
	f := scriptFace(t, map[string]fonttest.Script{
		"latn": {Required: 1}, // and nothing in Features
	})
	if got := lastGID(t, f, "x"); got != scZ {
		t.Errorf("x shaped to glyph %d, want %d — a required feature applies unconditionally", got, scZ)
	}
}

// TestScriptSelectionSurvivesMalformedScriptList checks the bounds. A font is
// untrusted input, and a ScriptList whose offsets point past the end of the
// table must truncate the walk rather than reach outside it — and must leave the
// run shaped by the fallback rather than not shaped at all.
func TestScriptSelectionSurvivesMalformedScriptList(t *testing.T) {
	gsub := fonttest.GSUBTable(
		[]fonttest.Lookup{{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scY})}}},
		[]fonttest.Feature{{Tag: "ccmp", Lookups: []int{0}}},
		map[string]fonttest.Script{"latn": {Required: fonttest.NoFeature, Features: []int{0}}},
	)
	// Every prefix of the table, so that the ScriptList, a Script table and a
	// LangSys are each cut in half by some case.
	for n := 0; n <= len(gsub); n++ {
		truncated := append([]byte(nil), gsub[:n]...)
		l := &layout{
			kern: map[[2]int]int{}, ligatures: map[int][]ligature{},
			glyphClass: map[int]int{}, single: map[string]map[int]int{},
			singlePos: map[int]singleAdjust{}, markAnchors: map[int]markAnchor{},
			markBases: map[key2]anchor{}, markMarkBases: map[key2]anchor{},
			cursive: map[int]cursiveAnchors{},
		}
		sel, _ := scriptFeatures(truncated, []string{"latn"}, "")
		if len(truncated) >= 10 {
			l.readSingleSubstitutions(truncated, sel)
			featureLookupIndices(truncated, sel)
		}
	}
}

// TestScriptTagsFromUnicode pins the mapping from a run's characters to the
// tags a font declares its rules under.
func TestScriptTagsFromUnicode(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
		why  string
	}{
		{"hello", []string{"latn"}, "Latin"},
		{"Ελλάδα", []string{"grek"}, "Greek"},
		{"Москва", []string{"cyrl"}, "Cyrillic"},
		{"مرحبا", []string{"arab"}, "Arabic"},
		{"日本", []string{"hani"}, "Han"},
		{"ひらがな", []string{"kana"}, "Hiragana shares Katakana's tag"},
		{"カタカナ", []string{"kana"}, "Katakana"},
		{"नमस्ते", []string{"dev2", "deva"}, "Devanagari: the newer tag first"},
		{"ᐃᓄᒃᑎᑐᑦ", []string{"cans"}, "Canadian Aboriginal syllabics"},
		{"123 —", nil, "digits and punctuation are in no script of their own"},
		{"", nil, "nothing at all"},
		{"  αβ", []string{"grek"}, "leading spaces do not decide; the first letter does"},
		{"12 αβ", []string{"grek"}, "nor do leading digits"},
		{"αβ 12", []string{"grek"}, "and trailing ones do not change the answer"},
	} {
		got := scriptTags(runScript(tc.text))
		if len(got) != len(tc.want) {
			t.Errorf("%q: tags %v, want %v (%s)", tc.text, got, tc.want, tc.why)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: tags %v, want %v (%s)", tc.text, got, tc.want, tc.why)
				break
			}
		}
	}
}

// TestScriptTagsArePaddedToFour pins the scripts whose ISO 15924 code is
// shorter than the four bytes a tag has to be. A tag written without its
// padding matches nothing in any font, silently.
func TestScriptTagsArePaddedToFour(t *testing.T) {
	for _, text := range []string{"ດ", "ꆈ", "ߒ", "ꕉ"} { // Lao, Yi, N'Ko, Vai
		tags := scriptTags(runScript(text))
		if len(tags) == 0 {
			t.Errorf("%q: no tag at all", text)
			continue
		}
		for _, tag := range tags {
			if len(tag) != 4 {
				t.Errorf("%q: tag %q is %d bytes, want 4", text, tag, len(tag))
			}
		}
	}
}

// TestEveryShapingEntryPointResolvesTheScript covers the paths that are not
// ShapeGlyphs. Shape and MeasureShaped go through the flattened ligature table
// and ShapeWith through the by-tag substitutions, and each reads a layout of
// its own — so each has to resolve the run's script, and each is a place the
// resolution can be left out without the others noticing.
func TestEveryShapingEntryPointResolvesTheScript(t *testing.T) {
	const (
		ligX, ligY   = 1, 2
		latinLig     = 3 // what x+y becomes for Latin
		greekLig     = 4 // and for Greek
		ligAlpha     = 5
		latinLigAdv  = 600
		greekLigAdv  = 700
		plainAdvance = 500
	)
	gsub := fonttest.GSUBTable(
		[]fonttest.Lookup{
			{Type: 4, Subtables: [][]byte{fonttest.LigatureSubst(
				[]fonttest.Ligature{{Components: []int{ligX, ligY}, Glyph: latinLig}})}},
			{Type: 4, Subtables: [][]byte{fonttest.LigatureSubst(
				[]fonttest.Ligature{{Components: []int{ligX, ligY}, Glyph: greekLig}})}},
		},
		[]fonttest.Feature{
			{Tag: "liga", Lookups: []int{0}},
			{Tag: "liga", Lookups: []int{1}},
		},
		map[string]fonttest.Script{
			"latn": {Required: fonttest.NoFeature, Features: []int{0}},
			"grek": {Required: fonttest.NoFeature, Features: []int{1}},
		},
	)
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Ligatures",
		Glyphs: []fonttest.Glyph{
			{Rune: 'x', Advance: plainAdvance, HasShape: true},
			{Rune: 'y', Advance: plainAdvance, HasShape: true},
			{Rune: 'A', Advance: latinLigAdv, HasShape: true},
			{Rune: 'B', Advance: greekLigAdv, HasShape: true},
			{Rune: 'α', Advance: plainAdvance, HasShape: true},
		},
		Extra: map[string][]byte{"GSUB": gsub},
	}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	// Shape: the span path, over the flattened ligature table.
	codes := spanCodes(t, f, "xy")
	if want := []byte{0, latinLig}; string(codes) != string(want) {
		t.Errorf("Shape(%q) emitted codes %v, want %v — the Latin ligature", "xy", codes, want)
	}
	codes = spanCodes(t, f, "αxy")
	if want := []byte{0, ligAlpha, 0, greekLig}; string(codes) != string(want) {
		t.Errorf("Shape(%q) emitted codes %v, want %v — the Greek ligature", "αxy", codes, want)
	}

	// MeasureShaped has to agree with Shape, or a caller lays out to one width
	// and draws another.
	if got, want := f.MeasureShaped("xy", 1000), float64(latinLigAdv); got != want {
		t.Errorf("MeasureShaped(%q) = %v, want %v", "xy", got, want)
	}
	if got, want := f.MeasureShaped("αxy", 1000), float64(plainAdvance+greekLigAdv); got != want {
		t.Errorf("MeasureShaped(%q) = %v, want %v", "αxy", got, want)
	}

	// ShapeWith: the features a caller asks for by name are selected by script
	// too, so asking for one gets the script's own version of it.
	g := scriptFaceTagged(t, "salt", map[string]fonttest.Script{
		"latn": {Required: fonttest.NoFeature, Features: []int{0}},
		"grek": {Required: fonttest.NoFeature, Features: []int{1}},
	})
	if got := withCodes(t, g, "x"); string(got) != string([]byte{0, scY}) {
		t.Errorf("ShapeWith(%q, salt) emitted %v, want the Latin rule's glyph %d", "x", got, scY)
	}
	if got := withCodes(t, g, "αx"); string(got) != string([]byte{0, scAlpha, 0, scZ}) {
		t.Errorf("ShapeWith(%q, salt) emitted %v, want the Greek rule's glyph %d", "αx", got, scZ)
	}
}

// spanCodes is the character codes Shape emits, with the displacements dropped.
func spanCodes(t *testing.T, f *Face, s string) []byte {
	t.Helper()
	spans, missing := f.Shape(s)
	if missing != 0 {
		t.Fatalf("Shape(%q): %d runes have no glyph", s, missing)
	}
	var out []byte
	for _, sp := range spans {
		out = append(out, sp.Codes...)
	}
	return out
}

func withCodes(t *testing.T, f *Face, s string) []byte {
	t.Helper()
	spans, missing := f.ShapeWith(s, "salt")
	if missing != 0 {
		t.Fatalf("ShapeWith(%q): %d runes have no glyph", s, missing)
	}
	var out []byte
	for _, sp := range spans {
		out = append(out, sp.Codes...)
	}
	return out
}

// The cross-script cursive fixture: two cursive attachment lookups, one per
// script, with opposite RightToLeft flags. A Latin word and an Arabic word join
// in opposite directions in the same font, which is the case that decides
// whether attachment can be read script-blind.
const (
	arabAlef, arabBeh = 4, 5

	alefExitY = 100
	behEntryY = 40
)

func crossScriptCursiveFace(t *testing.T, scripts map[string]fonttest.Script) *Face {
	t.Helper()
	latin := fonttest.CursivePosSubtable(joinedAnchors())
	arabic := fonttest.CursivePosSubtable([]fonttest.CursiveAnchor{
		{Glyph: arabAlef, HasExit: true, Exit: fonttest.Anchor{X: 400, Y: alefExitY}},
		{Glyph: arabBeh, HasEntry: true, Entry: fonttest.Anchor{X: 50, Y: behEntryY}},
	})
	gpos := fonttest.GPOSTable(
		[]fonttest.Lookup{
			{Type: 3, Flag: 0, Subtables: [][]byte{latin}},
			{Type: 3, Flag: flagRightToLeft, Subtables: [][]byte{arabic}},
		},
		[]fonttest.Feature{
			{Tag: "curs", Lookups: []int{0}},
			{Tag: "curs", Lookups: []int{1}},
		},
		scripts,
	)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "TwoScripts",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: curAdvance, HasShape: true},
			{Rune: 'b', Advance: curAdvance, HasShape: true},
			{Rune: 'c', Advance: curAdvance, HasShape: true},
			{Rune: 'ا', Advance: curAdvance, HasShape: true},
			{Rune: 'ب', Advance: curAdvance, HasShape: true},
		},
		Extra: map[string][]byte{"GPOS": gpos},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestAttachmentTakesTheScriptsOwnLookupFlag is the case that settled how far
// the "a mark's place is a fact about the font" argument reaches.
//
// It reaches over feature tags: attachment is still read from every tag rather
// than only from 'mark', 'mkmk' and 'curs', because a font is free to declare
// it under a script-specific one. It does not reach over scripts, because the
// lookup *flags* are merged into one set — and the RightToLeft bit of a cursive
// lookup decides which end of a joined run stays on the baseline. Read
// script-blind, a font carrying both Latin and Arabic cursive attachment sets
// its Latin words from the Arabic lookup's flag, and every Latin word in the
// document sits off the line.
func TestAttachmentTakesTheScriptsOwnLookupFlag(t *testing.T) {
	f := crossScriptCursiveFace(t, map[string]fonttest.Script{
		"latn": {Required: fonttest.NoFeature, Features: []int{0}},
		"arab": {Required: fonttest.NoFeature, Features: []int{1}},
	})

	// Latin: the flag is clear, so the first glyph is the anchored end and the
	// rest climb away from it.
	latin, _ := f.ShapeGlyphs("abc")
	if len(latin) != 3 {
		t.Fatalf("Latin run gave %d glyphs, want 3", len(latin))
	}
	if latin[0].YOffset != 0 {
		t.Errorf("Latin: first glyph sits at %v, want 0 — 'latn' declares no RightToLeft", latin[0].YOffset)
	}
	if latin[2].YOffset == 0 {
		t.Errorf("Latin: last glyph sits at 0, so nothing was lifted and the fixture proves nothing")
	}

	// Arabic: the flag is set, so the last glyph is the anchored end.
	arabic, _ := f.ShapeGlyphs("اب")
	if len(arabic) != 2 {
		t.Fatalf("Arabic run gave %d glyphs, want 2", len(arabic))
	}
	if arabic[1].YOffset != 0 {
		t.Errorf("Arabic: last glyph sits at %v, want 0 — 'arab' declares RightToLeft", arabic[1].YOffset)
	}
	if want := float64(-(alefExitY - behEntryY)); arabic[0].YOffset != want {
		t.Errorf("Arabic: first glyph sits at %v, want %v", arabic[0].YOffset, want)
	}
}

// TestAttachmentIsStillReadFromEveryTag pins the half of the old reasoning that
// survived. The lookup is under 'abvm', a tag no caller names and no default
// feature list contains, and it must still be applied.
func TestAttachmentIsStillReadFromEveryTag(t *testing.T) {
	gpos := fonttest.GPOSTable(
		[]fonttest.Lookup{{Type: 3, Subtables: [][]byte{fonttest.CursivePosSubtable(joinedAnchors())}}},
		[]fonttest.Feature{{Tag: "abvm", Lookups: []int{0}}},
		map[string]fonttest.Script{"latn": {Required: fonttest.NoFeature, Features: []int{0}}},
	)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "OddTag",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: curAdvance, HasShape: true},
			{Rune: 'b', Advance: curAdvance, HasShape: true},
			{Rune: 'c', Advance: curAdvance, HasShape: true},
		},
		Extra: map[string][]byte{"GPOS": gpos},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	glyphs, _ := f.ShapeGlyphs("abc")
	if glyphs[0].XAdvance != aExitX {
		t.Errorf("a advances %v, want %d — attachment under an unnamed tag was not read",
			glyphs[0].XAdvance, aExitX)
	}
}
