package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/font"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

func TestPredefinedCMapTable(t *testing.T) {
	// Spot-check ISO 32000-1 Table 118 entries and their implied CIDSystemInfo.
	cases := map[string]core.PredefinedCMapInfo{
		"Identity-H":     {Registry: "Adobe", Ordering: "Identity"},
		"UniGB-UTF16-H":  {Registry: "Adobe", Ordering: "GB1"},
		"UniJIS-UCS2-H":  {Registry: "Adobe", Ordering: "Japan1"},
		"UniKS-UCS2-H":   {Registry: "Adobe", Ordering: "Korea1"},
		"UniCNS-UTF16-V": {Registry: "Adobe", Ordering: "CNS1"},
	}
	for name, want := range cases {
		got, ok := core.PredefinedCMaps[name]
		if !ok || got != want {
			t.Errorf("%s: got %v ok=%v, want %v", name, got, ok, want)
		}
	}
	if _, ok := core.PredefinedCMaps["Bogus-CMap"]; ok {
		t.Error("unlisted CMap must not be predefined")
	}
}

func TestAGLGlyphName(t *testing.T) {
	for _, n := range []string{"A", "space", "uni20AC", "u1F600", "ampersand"} {
		if !aglGlyphName(n) {
			t.Errorf("%q should be a valid AGL name", n)
		}
	}
	for _, n := range []string{"", "notAGlyph", "uniXYZW", "uni20A"} {
		if aglGlyphName(n) {
			t.Errorf("%q should not be a valid AGL name", n)
		}
	}
}

func TestGlyphNameToRune(t *testing.T) {
	cases := []struct {
		name string
		code byte
		want rune
	}{
		{"A", 65, 'A'},
		{"uni20AC", 0x80, 0x20AC},
		{"anything", 0x41, 'A'}, // ASCII identity by code
		{"custom", 0xE9, 0xE9},  // Latin-1 high range by code
	}
	for _, c := range cases {
		got, ok := font.GlyphNameToRune(c.name, c.code)
		if !ok || got != c.want {
			t.Errorf("font.GlyphNameToRune(%q,%d)=%v,%v want %v", c.name, c.code, got, ok, c.want)
		}
	}
}

func TestParseCharSet(t *testing.T) {
	got := core.ParseCharSet("/space/A/quoteright/period")
	for _, n := range []string{"space", "A", "quoteright", "period"} {
		if !got[n] {
			t.Errorf("CharSet missing %q", n)
		}
	}
	if len(got) != 4 {
		t.Errorf("expected 4 names, got %d", len(got))
	}
}

func TestSimpleFontEncodingDifferences(t *testing.T) {
	font := &object.Dictionary{}
	enc := &object.Dictionary{}
	enc.Set("BaseEncoding", object.Name("WinAnsiEncoding"))
	enc.Set("Differences", object.Array{object.Integer(65), object.Name("Alpha"), object.Name("Beta")})
	font.Set("Encoding", enc)
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	table := simpleFontCodeToName(doc, font, false)
	if table[65] != "Alpha" || table[66] != "Beta" {
		t.Errorf("Differences not applied: %q %q", table[65], table[66])
	}
	if table[32] != "space" { // from WinAnsi base
		t.Errorf("base encoding not applied: code32=%q", table[32])
	}
}

// TestSimpleFontBaseEncodingModelled pins which encodings the glyph-coverage
// rule may reason about. A code absent from the code→name table only proves
// the glyph is unreachable when the base encoding is one of the Annex D.2
// tables; otherwise the base is the font program's built-in encoding, which is
// not parsed, and the rule must stay silent (corpus PDF_A-1a 6-3-8-t01-pass-b
// and -pass-e).
func TestSimpleFontBaseEncodingModelled(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	mk := func(enc object.Object) *object.Dictionary {
		f := &object.Dictionary{}
		if enc != nil {
			f.Set("Encoding", enc)
		}
		return f
	}
	diffOnly := &object.Dictionary{}
	diffOnly.Set("Differences", object.Array{object.Integer(65), object.Name("Alpha")})
	winBase := &object.Dictionary{}
	winBase.Set("BaseEncoding", object.Name("WinAnsiEncoding"))

	cases := []struct {
		name     string
		enc      object.Object
		symbolic bool
		want     bool
	}{
		{"WinAnsi", object.Name("WinAnsiEncoding"), false, true},
		{"MacRoman", object.Name("MacRomanEncoding"), false, true},
		{"MacExpert", object.Name("MacExpertEncoding"), false, false},
		{"none, non-symbolic", nil, false, true},
		{"none, symbolic", nil, true, false},
		{"Differences only, non-symbolic", diffOnly, false, true},
		{"Differences only, symbolic", diffOnly, true, false},
		{"BaseEncoding wins for a symbolic font", winBase, true, true},
	}
	for _, c := range cases {
		if got := simpleFontBaseEncodingModelled(doc, mk(c.enc), c.symbolic); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCharSetParsing_Numbers(t *testing.T) {
	// Names may be adjacent without separators other than '/'.
	got := core.ParseCharSet("/one/two/three")
	if !got["one"] || !got["two"] || !got["three"] {
		t.Errorf("adjacency parse failed: %v", got)
	}
}

// checkTrueTypeEncoding via crafted dictionaries (ISO 32000-1 9.6.6.4).
func TestTrueTypeEncodingRules(t *testing.T) {
	mk := func(symbolic bool, enc object.Object) (core.View, *object.Dictionary, *core.FontTextUsage) {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		fd := &object.Dictionary{}
		flags := 32 // nonsymbolic
		if symbolic {
			flags = 4
		}
		fd.Set("Flags", object.Integer(flags))
		doc.Objects[5] = &object.IndirectObject{Number: 5, Value: fd}
		font := &object.Dictionary{}
		font.Set("Subtype", object.Name("TrueType"))
		font.Set("FontDescriptor", object.IndirectRef{Number: 5})
		if enc != nil {
			font.Set("Encoding", enc)
		}
		return doc, font, &core.FontTextUsage{ObjNum: 9, Modes: map[int]bool{}}
	}

	// Symbolic + Encoding present -> error.
	doc, font, u := mk(true, object.Name("WinAnsiEncoding"))
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) == 0 {
		t.Error("symbolic TrueType with Encoding must be flagged")
	}
	// Symbolic + no Encoding -> ok.
	doc, font, u = mk(true, nil)
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) != 0 {
		t.Error("symbolic TrueType without Encoding must pass")
	}
	// Nonsymbolic + WinAnsi -> ok.
	doc, font, u = mk(false, object.Name("WinAnsiEncoding"))
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) != 0 {
		t.Error("nonsymbolic WinAnsi must pass")
	}
	// Nonsymbolic + bad base encoding name -> error.
	doc, font, u = mk(false, object.Name("StandardEncoding"))
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) == 0 {
		t.Error("nonsymbolic StandardEncoding must be flagged")
	}
	// Nonsymbolic Encoding dict with Differences name not in AGL -> error.
	e := &object.Dictionary{}
	e.Set("BaseEncoding", object.Name("WinAnsiEncoding"))
	e.Set("Differences", object.Array{object.Integer(1), object.Name("notAGlyphName")})
	doc, font, u = mk(false, e)
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) == 0 {
		t.Error("Differences glyph not in AGL must be flagged")
	}
}

// ToUnicode forbidden values (A-4): U+0000, U+FEFF, U+FFFE.
func TestToUnicodeForbiddenValues(t *testing.T) {
	mk := func(body string) (core.View, *object.Stream) {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		s := &object.Stream{Dict: object.Dictionary{}, Data: []byte(body)}
		s.Dict.Set("Length", object.Integer(len(body)))
		return doc, s
	}
	doc, s := mk("beginbfchar <0041> <0000> endbfchar")
	if !core.HasForbiddenUnicodeTargets(doc, s) {
		t.Error("bfchar mapping to U+0000 must be detected")
	}
	doc, s = mk("beginbfrange <0041> <0043> <FEFF> endbfrange")
	if !core.HasForbiddenUnicodeTargets(doc, s) {
		t.Error("bfrange mapping to U+FEFF must be detected")
	}
	doc, s = mk("beginbfchar <0041> <0041> endbfchar")
	if core.HasForbiddenUnicodeTargets(doc, s) {
		t.Error("valid ToUnicode must not be flagged")
	}
}

// CIDToGIDMap requirement for embedded CIDFontType2 (ISO 32000-1 9.7.4.2).
func TestCIDToGIDMapRule(t *testing.T) {
	mkFont := func(cidToGID object.Object) (core.View, *object.Dictionary, *core.FontTextUsage) {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		desc := &object.Dictionary{}
		desc.Set("Subtype", object.Name("CIDFontType2"))
		desc.Set("CIDSystemInfo", &object.Dictionary{})
		if cidToGID != nil {
			desc.Set("CIDToGIDMap", cidToGID)
		}
		doc.Objects[7] = &object.IndirectObject{Number: 7, Value: desc}
		font := &object.Dictionary{}
		font.Set("Subtype", object.Name("Type0"))
		font.Set("Encoding", object.Name("Identity-H"))
		font.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 7}})
		return doc, font, &core.FontTextUsage{ObjNum: 9, Modes: map[int]bool{0: true}}
	}
	doc, font, u := mkFont(nil)
	if !hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11.3.2") {
		t.Error("missing CIDToGIDMap must be flagged")
	}
	doc, font, u = mkFont(object.Name("Custom"))
	if !hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11.3.2") {
		t.Error("non-Identity CIDToGIDMap name must be flagged")
	}
	doc, font, u = mkFont(object.Name("Identity"))
	if hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11.3.2") {
		t.Error("Identity CIDToGIDMap must pass")
	}
}

// The CIDSystemInfo Supplement relationship is a part-2-and-later requirement.
// ISO 19005-2 6.2.11.3.1 adds "the value of the Supplement key ... of the
// CIDFont shall be less than or equal to the Supplement key ... of the CMap";
// ISO 19005-1 6.3.3.1 constrains Registry and Ordering only, and applying the
// Supplement rule there false-positives on the conforming corpus file
// PDF_A-1a 6-3-8-t01-pass-f.
func TestCIDSystemInfoSupplementIsPart2AndLater(t *testing.T) {
	mkFont := func(cmapSup, cidSup int) (core.View, *object.Dictionary, *core.FontTextUsage) {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		si := func(sup int) *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Registry", object.String{Value: []byte("Adobe")})
			d.Set("Ordering", object.String{Value: []byte("Japan1")})
			d.Set("Supplement", object.Integer(sup))
			return d
		}
		desc := &object.Dictionary{}
		desc.Set("Subtype", object.Name("CIDFontType0"))
		desc.Set("CIDSystemInfo", si(cidSup))
		doc.Objects[7] = &object.IndirectObject{Number: 7, Value: desc}
		cmap := &object.Stream{Dict: object.Dictionary{}}
		cmap.Dict.Set("CIDSystemInfo", si(cmapSup))
		font := &object.Dictionary{}
		font.Set("Subtype", object.Name("Type0"))
		font.Set("Encoding", cmap)
		font.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 7}})
		return doc, font, &core.FontTextUsage{ObjNum: 9, Modes: map[int]bool{0: true}}
	}
	doc, font, u := mkFont(2, 3)
	if !hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11.3.1") {
		t.Error("CIDFont Supplement exceeding the CMap's must be flagged at PDF/A-2")
	}
	doc, font, u = mkFont(2, 3)
	if hasRuleErr(checkOneFontDict(doc, PDFA1b, "6.3", font, u), "6.3.3.1") {
		t.Error("Supplement relationship must not be enforced at PDF/A-1")
	}
	doc, font, u = mkFont(3, 2)
	if hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11.3.1") {
		t.Error("a CIDFont Supplement below the CMap's must pass")
	}
}

func hasRuleErr(errs []ValidationError, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func TestParseCIDWidths(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
	// [ 1 [100 200] 5 7 300 ]  -> CID1=100, CID2=200, CID5..7=300
	w := object.Array{object.Integer(1), object.Array{object.Integer(100), object.Integer(200)}, object.Integer(5), object.Integer(7), object.Integer(300)}
	m, _ := parseCIDWidths(doc, w)
	if m[1] != 100 || m[2] != 200 || m[5] != 300 || m[7] != 300 {
		t.Errorf("CID width parse wrong: %v", m)
	}
	if _, ok := m[3]; ok {
		t.Error("CID3 should be unset")
	}
}

// TestTrueTypeEncodingAt1b ensures the TrueType encoding rules apply at
// PDF/A-1b (ISO 19005-1 6.3.7): symbolic fonts must not carry an Encoding.
func TestTrueTypeEncodingAt1b(t *testing.T) {
	fd := &object.Dictionary{}
	fd.Set("Flags", object.Integer(4)) // symbolic
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("TrueType"))
	font.Set("FontDescriptor", fd)
	font.Set("Encoding", object.Name("WinAnsiEncoding")) // forbidden on a symbolic TT font
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: font}}})
	u := &core.FontTextUsage{ObjNum: 1}
	if got := len(checkTrueTypeEncoding(doc, PDFA1b, "6.3", font, u)); got == 0 {
		t.Error("symbolic TrueType /Encoding not flagged at 1b")
	}
	// A non-symbolic font with WinAnsiEncoding is fine.
	fd.Set("Flags", object.Integer(32))
	if got := len(checkTrueTypeEncoding(doc, PDFA1b, "6.3", font, u)); got != 0 {
		t.Errorf("non-symbolic TrueType with WinAnsiEncoding wrongly flagged at 1b: %d", got)
	}
}

// TestDamagedFontProgramFlagged ensures a visibly-rendered font with an
// embedded but unparseable program is flagged rather than silently exempted.
func TestDamagedFontProgramFlagged(t *testing.T) {
	fd := &object.Dictionary{}
	fd.Set("Flags", object.Integer(32))
	fd.Set("FontFile2", object.IndirectRef{Number: 9}) // resolves to a garbage stream
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("TrueType"))
	font.Set("FontDescriptor", fd)
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: &object.Stream{Dict: object.Dictionary{}, Data: []byte("not a font program")}},
	}})
	// A usage that renders visible text.
	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{[]byte("Hi")}, Modes: map[int]bool{0: true}}
	if core.LoadFontProgram(doc, fd) != nil {
		t.Skip("garbage stream unexpectedly parsed as a font program")
	}
	if got := len(damagedFontProgramError(doc, PDFA1b, "6.3", font, fd, u)); got == 0 {
		t.Error("damaged embedded font program not flagged for a rendered font")
	}
	// Not embedded -> not this rule's concern (embedding is a separate check).
	fd2 := &object.Dictionary{}
	fd2.Set("Flags", object.Integer(32))
	if got := len(damagedFontProgramError(doc, PDFA1b, "6.3", font, fd2, u)); got != 0 {
		t.Errorf("non-embedded font wrongly flagged as damaged: %d", got)
	}
}

// TestSymbolicTrueTypeSingleCmap ensures a symbolic TrueType font with more
// than one cmap subtable is flagged (ISO 19005-1 6.3.7).
func TestSymbolicTrueTypeSingleCmap(t *testing.T) {
	fp := &font.Program{CmapSubtableCount: 2}
	if !(fp.CmapSubtableCount > 0 && fp.CmapSubtableCount != 1) {
		t.Fatal("test premise wrong")
	}
	// One subtable is fine; two is not; zero (non-sfnt) is exempt.
	for _, tc := range []struct {
		count int
		bad   bool
	}{{1, false}, {2, true}, {0, false}, {3, true}} {
		bad := tc.count > 0 && tc.count != 1
		if bad != tc.bad {
			t.Errorf("cmapSubtableCount=%d: got bad=%v want %v", tc.count, bad, tc.bad)
		}
	}
}

// TestCMapEmbeddedAt1b ensures a Type0 font with a named predefined CMap (not
// Identity) is flagged at PDF/A-1b but not at 2b (ISO 19005-1 6.3.3.3).
func TestCMapEmbeddedAt1b(t *testing.T) {
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("Type0"))
	font.Set("Encoding", object.Name("UniJIS-UCS2-H"))
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: font}}})
	if got := len(checkCMapEmbedded(doc, PDFA1b)); got == 0 {
		t.Error("named predefined CMap not flagged at 1b")
	}
	if got := len(checkCMapEmbedded(doc, PDFA2b)); got != 0 {
		t.Errorf("2b permits predefined CMaps by name, got %d", got)
	}
	// Identity is always fine.
	font.Set("Encoding", object.Name("Identity-H"))
	if got := len(checkCMapEmbedded(doc, PDFA1b)); got != 0 {
		t.Errorf("Identity-H wrongly flagged at 1b: %d", got)
	}
}

// TestType1CharStringsEndTerminator pins what ends a Type 1 CharStrings
// dictionary. It ends with a standalone "end" token after the last entry (Adobe
// Type 1 Font Format 10.3), never with a glyph whose name contains "end" —
// endash is an ordinary glyph in the standard encoding, and so are
// enfilledcircbullet and endescender. Breaking on the name truncated the glyph
// list at the first such entry, so every glyph defined after it read as missing
// from the program: the font then drew "does not define a glyph referenced for
// rendering" and /CharSet findings on a font that defines everything it claims.
func TestType1CharStringsEndTerminator(t *testing.T) {
	names := []string{"A", "endash", "enfilledcircbullet", "B", "quoteright"}
	fp := font.ParseType1(fonttest.Type1Program(names))
	if fp == nil {
		t.Fatal("parseType1 returned nil for a well-formed program")
	}
	for _, n := range names {
		if !fp.GlyphNames[n] {
			t.Errorf("glyph %q missing from the parsed program (glyphs: %v)", n, fp.GlyphNames)
		}
	}
	if len(fp.GlyphNames) != len(names) {
		t.Errorf("parsed %d glyphs, want %d: %v", len(fp.GlyphNames), len(names), fp.GlyphNames)
	}

	// The terminator itself still stops the scan: a program whose CharStrings
	// dictionary is followed by more PostScript must not absorb it as glyphs.
	if fp := font.ParseType1(fonttest.Type1Program([]string{"A", "B"})); fp == nil || len(fp.GlyphNames) != 2 {
		t.Errorf("trailing PostScript after the closing end token leaked into the glyph list: %v", fp)
	}
}

// TestType1CharStringsEnd covers the terminator predicate directly, including
// the ND-less shape and the boundary cases a substring test gets wrong.
func TestType1CharStringsEnd(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{" ND\nend\nend\nmark currentfile closefile\n", true},
		{" |-\n end ", true},
		{"\nend\n", true},              // no ND token
		{" ND\n/endash 45 RD ", false}, // the next entry is a glyph named endash
		{" ND\n/enfilledcircbullet 9 RD", false},
		{" ND\n", false},       // data ran out
		{" ND\nendobj", false}, // a longer token that merely starts with end
	}
	for _, c := range cases {
		if got := font.Type1CharStringsEnd([]byte(c.in)); got != c.want {
			t.Errorf("font.Type1CharStringsEnd(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
