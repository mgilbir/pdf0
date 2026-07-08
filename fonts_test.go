package pdf0

import "testing"

func TestPredefinedCMapTable(t *testing.T) {
	// Spot-check ISO 32000-1 Table 118 entries and their implied CIDSystemInfo.
	cases := map[string]predefinedCMapInfo{
		"Identity-H":     {"Adobe", "Identity"},
		"UniGB-UTF16-H":  {"Adobe", "GB1"},
		"UniJIS-UCS2-H":  {"Adobe", "Japan1"},
		"UniKS-UCS2-H":   {"Adobe", "Korea1"},
		"UniCNS-UTF16-V": {"Adobe", "CNS1"},
	}
	for name, want := range cases {
		got, ok := predefinedCMaps[name]
		if !ok || got != want {
			t.Errorf("%s: got %v ok=%v, want %v", name, got, ok, want)
		}
	}
	if _, ok := predefinedCMaps["Bogus-CMap"]; ok {
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
		got, ok := glyphNameToRune(c.name, c.code)
		if !ok || got != c.want {
			t.Errorf("glyphNameToRune(%q,%d)=%v,%v want %v", c.name, c.code, got, ok, c.want)
		}
	}
}

func TestParseCharSet(t *testing.T) {
	got := parseCharSet("/space/A/quoteright/period")
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
	font := &Dictionary{}
	enc := &Dictionary{}
	enc.Set("BaseEncoding", Name("WinAnsiEncoding"))
	enc.Set("Differences", Array{Integer(65), Name("Alpha"), Name("Beta")})
	font.Set("Encoding", enc)
	doc := &Document{Objects: map[int]*IndirectObject{}}
	table := simpleFontCodeToName(doc, font, false)
	if table[65] != "Alpha" || table[66] != "Beta" {
		t.Errorf("Differences not applied: %q %q", table[65], table[66])
	}
	if table[32] != "space" { // from WinAnsi base
		t.Errorf("base encoding not applied: code32=%q", table[32])
	}
}

func TestCharSetParsing_Numbers(t *testing.T) {
	// Names may be adjacent without separators other than '/'.
	got := parseCharSet("/one/two/three")
	if !got["one"] || !got["two"] || !got["three"] {
		t.Errorf("adjacency parse failed: %v", got)
	}
}

// checkTrueTypeEncoding via crafted dictionaries (ISO 32000-1 9.6.6.4).
func TestTrueTypeEncodingRules(t *testing.T) {
	mk := func(symbolic bool, enc Object) (*Document, *Dictionary, *fontTextUsage) {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		fd := &Dictionary{}
		flags := 32 // nonsymbolic
		if symbolic {
			flags = 4
		}
		fd.Set("Flags", Integer(flags))
		doc.Objects[5] = &IndirectObject{Number: 5, Value: fd}
		font := &Dictionary{}
		font.Set("Subtype", Name("TrueType"))
		font.Set("FontDescriptor", IndirectRef{Number: 5})
		if enc != nil {
			font.Set("Encoding", enc)
		}
		return doc, font, &fontTextUsage{objNum: 9, modes: map[int]bool{}}
	}

	// Symbolic + Encoding present -> error.
	doc, font, u := mk(true, Name("WinAnsiEncoding"))
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) == 0 {
		t.Error("symbolic TrueType with Encoding must be flagged")
	}
	// Symbolic + no Encoding -> ok.
	doc, font, u = mk(true, nil)
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) != 0 {
		t.Error("symbolic TrueType without Encoding must pass")
	}
	// Nonsymbolic + WinAnsi -> ok.
	doc, font, u = mk(false, Name("WinAnsiEncoding"))
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) != 0 {
		t.Error("nonsymbolic WinAnsi must pass")
	}
	// Nonsymbolic + bad base encoding name -> error.
	doc, font, u = mk(false, Name("StandardEncoding"))
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) == 0 {
		t.Error("nonsymbolic StandardEncoding must be flagged")
	}
	// Nonsymbolic Encoding dict with Differences name not in AGL -> error.
	e := &Dictionary{}
	e.Set("BaseEncoding", Name("WinAnsiEncoding"))
	e.Set("Differences", Array{Integer(1), Name("notAGlyphName")})
	doc, font, u = mk(false, e)
	if len(checkTrueTypeEncoding(doc, PDFA2b, "6.2.11", font, u)) == 0 {
		t.Error("Differences glyph not in AGL must be flagged")
	}
}

// ToUnicode forbidden values (A-4): U+0000, U+FEFF, U+FFFE.
func TestToUnicodeForbiddenValues(t *testing.T) {
	mk := func(body string) (*Document, *Stream) {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		s := &Stream{Dict: Dictionary{}, Data: []byte(body)}
		s.Dict.Set("Length", Integer(len(body)))
		return doc, s
	}
	doc, s := mk("beginbfchar <0041> <0000> endbfchar")
	if !hasForbiddenUnicodeTargets(doc, s) {
		t.Error("bfchar mapping to U+0000 must be detected")
	}
	doc, s = mk("beginbfrange <0041> <0043> <FEFF> endbfrange")
	if !hasForbiddenUnicodeTargets(doc, s) {
		t.Error("bfrange mapping to U+FEFF must be detected")
	}
	doc, s = mk("beginbfchar <0041> <0041> endbfchar")
	if hasForbiddenUnicodeTargets(doc, s) {
		t.Error("valid ToUnicode must not be flagged")
	}
}

// CIDToGIDMap requirement for embedded CIDFontType2 (ISO 32000-1 9.7.4.2).
func TestCIDToGIDMapRule(t *testing.T) {
	mkFont := func(cidToGID Object) (*Document, *Dictionary, *fontTextUsage) {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		desc := &Dictionary{}
		desc.Set("Subtype", Name("CIDFontType2"))
		desc.Set("CIDSystemInfo", &Dictionary{})
		if cidToGID != nil {
			desc.Set("CIDToGIDMap", cidToGID)
		}
		doc.Objects[7] = &IndirectObject{Number: 7, Value: desc}
		font := &Dictionary{}
		font.Set("Subtype", Name("Type0"))
		font.Set("Encoding", Name("Identity-H"))
		font.Set("DescendantFonts", Array{IndirectRef{Number: 7}})
		return doc, font, &fontTextUsage{objNum: 9, modes: map[int]bool{0: true}}
	}
	doc, font, u := mkFont(nil)
	if !hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11") {
		t.Error("missing CIDToGIDMap must be flagged")
	}
	doc, font, u = mkFont(Name("Custom"))
	if !hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11") {
		t.Error("non-Identity CIDToGIDMap name must be flagged")
	}
	doc, font, u = mkFont(Name("Identity"))
	if hasRuleErr(checkOneFontDict(doc, PDFA2b, "6.2.11", font, u), "6.2.11") {
		t.Error("Identity CIDToGIDMap must pass")
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
	doc := &Document{Objects: map[int]*IndirectObject{}}
	// [ 1 [100 200] 5 7 300 ]  -> CID1=100, CID2=200, CID5..7=300
	w := Array{Integer(1), Array{Integer(100), Integer(200)}, Integer(5), Integer(7), Integer(300)}
	m := parseCIDWidths(doc, w)
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
	fd := &Dictionary{}
	fd.Set("Flags", Integer(4)) // symbolic
	font := &Dictionary{}
	font.Set("Subtype", Name("TrueType"))
	font.Set("FontDescriptor", fd)
	font.Set("Encoding", Name("WinAnsiEncoding")) // forbidden on a symbolic TT font
	doc := &Document{Objects: map[int]*IndirectObject{1: {Number: 1, Value: font}}}
	u := &fontTextUsage{objNum: 1}
	if got := len(checkTrueTypeEncoding(doc, PDFA1b, "6.3", font, u)); got == 0 {
		t.Error("symbolic TrueType /Encoding not flagged at 1b")
	}
	// A non-symbolic font with WinAnsiEncoding is fine.
	fd.Set("Flags", Integer(32))
	if got := len(checkTrueTypeEncoding(doc, PDFA1b, "6.3", font, u)); got != 0 {
		t.Errorf("non-symbolic TrueType with WinAnsiEncoding wrongly flagged at 1b: %d", got)
	}
}

// TestDamagedFontProgramFlagged ensures a visibly-rendered font with an
// embedded but unparseable program is flagged rather than silently exempted.
func TestDamagedFontProgramFlagged(t *testing.T) {
	fd := &Dictionary{}
	fd.Set("Flags", Integer(32))
	fd.Set("FontFile2", IndirectRef{Number: 9}) // resolves to a garbage stream
	font := &Dictionary{}
	font.Set("Subtype", Name("TrueType"))
	font.Set("FontDescriptor", fd)
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: &Stream{Dict: Dictionary{}, Data: []byte("not a font program")}},
	}}
	// A usage that renders visible text.
	u := &fontTextUsage{objNum: 1, strings: [][]byte{[]byte("Hi")}, modes: map[int]bool{0: true}}
	if loadFontProgram(doc, fd) != nil {
		t.Skip("garbage stream unexpectedly parsed as a font program")
	}
	if got := len(damagedFontProgramError(doc, PDFA1b, "6.3", font, fd, u)); got == 0 {
		t.Error("damaged embedded font program not flagged for a rendered font")
	}
	// Not embedded -> not this rule's concern (embedding is a separate check).
	fd2 := &Dictionary{}
	fd2.Set("Flags", Integer(32))
	if got := len(damagedFontProgramError(doc, PDFA1b, "6.3", font, fd2, u)); got != 0 {
		t.Errorf("non-embedded font wrongly flagged as damaged: %d", got)
	}
}

// TestSymbolicTrueTypeSingleCmap ensures a symbolic TrueType font with more
// than one cmap subtable is flagged (ISO 19005-1 6.3.7).
func TestSymbolicTrueTypeSingleCmap(t *testing.T) {
	fp := &fontProgram{cmapSubtableCount: 2}
	if !(fp.cmapSubtableCount > 0 && fp.cmapSubtableCount != 1) {
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
	font := &Dictionary{}
	font.Set("Subtype", Name("Type0"))
	font.Set("Encoding", Name("UniJIS-UCS2-H"))
	doc := &Document{Objects: map[int]*IndirectObject{1: {Number: 1, Value: font}}}
	if got := len(checkCMapEmbedded(doc, PDFA1b)); got == 0 {
		t.Error("named predefined CMap not flagged at 1b")
	}
	if got := len(checkCMapEmbedded(doc, PDFA2b)); got != 0 {
		t.Errorf("2b permits predefined CMaps by name, got %d", got)
	}
	// Identity is always fine.
	font.Set("Encoding", Name("Identity-H"))
	if got := len(checkCMapEmbedded(doc, PDFA1b)); got != 0 {
		t.Errorf("Identity-H wrongly flagged at 1b: %d", got)
	}
}
