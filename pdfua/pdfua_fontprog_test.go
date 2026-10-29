package pdfua

import (
	"encoding/binary"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// fontPage is a one-page document showing content with the font font, named
// /F1 and stored as object 5, plus whatever further objects the font
// references. The validator runs over it end to end, so a rule is reached
// through the dispatch and the executed-content model, as on a real file.
func fontPage(font *object.Dictionary, content string, extra map[int]object.Object) core.View {
	doc := mkView(map[int]*object.IndirectObject{}, nil)
	fontRes := object.NewDictionary(object.Entry{Key: "F1", Value: object.IndirectRef{Number: 5}})
	page := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Page")},
		object.Entry{Key: "Resources", Value: object.NewDictionary(object.Entry{Key: "Font", Value: fontRes})},
		object.Entry{Key: "Contents", Value: object.IndirectRef{Number: 6}},
	)
	pages := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Pages")},
		object.Entry{Key: "Kids", Value: object.Array{object.IndirectRef{Number: 3}}},
		object.Entry{Key: "Count", Value: object.Integer(1)},
	)
	cat := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Catalog")},
		object.Entry{Key: "Pages", Value: object.IndirectRef{Number: 2}},
	)
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: pages}
	doc.Objects[3] = &object.IndirectObject{Number: 3, Value: page}
	doc.Objects[5] = &object.IndirectObject{Number: 5, Value: font}
	doc.Objects[6] = &object.IndirectObject{Number: 6, Value: object.NewStream(&object.Dictionary{}, []byte(content))}
	for n, o := range extra {
		doc.Objects[n] = &object.IndirectObject{Number: n, Value: o}
	}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return doc
}

// clauseFindings is the findings validateView reports under clause, for each
// part: the font rules are shared by PDF/UA-1 and PDF/UA-2.
//
// clause is PDF/UA-1's; at PDF/UA-2 the finding is under the clause ISO
// 14289-2 numbers the rule by (ua2Fonts).
func clauseFindings(t *testing.T, doc func() core.View, clause string) (ua1, ua2 []Violation) {
	t.Helper()
	pick := func(vs []Violation, clause string) []Violation {
		var out []Violation
		for _, v := range vs {
			if v.Clause == clause {
				out = append(out, v)
			}
		}
		return out
	}
	clause2 := clause
	if c, ok := ua2Fonts[clause]; ok {
		clause2 = c
	}
	return pick(validateView(doc(), "1"), clause), pick(validateView(doc(), "2"), clause2)
}

// widthProgram is a TrueType program whose glyph 1 maps U+0041 and advances
// 500 of 1000 units per em, and whose glyph 2 maps U+0042 and advances 600.
func widthProgram() []byte {
	return fonttest.SFNT(fonttest.SFNTOptions{Glyphs: []fonttest.Glyph{
		{Rune: 'A', Advance: 500, HasShape: true},
		{Rune: 'B', Advance: 600, HasShape: true},
	}})
}

// simpleTrueType is a non-symbolic WinAnsi TrueType font over widthProgram
// (object 7) declaring widths for the codes from 65 ("A") on.
func simpleTrueType(widths ...object.Object) *object.Dictionary {
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("Test")},
		object.Entry{Key: "Flags", Value: object.Integer(32)},
		object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 7}},
	)
	return object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("TrueType")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "Encoding", Value: object.Name("WinAnsiEncoding")},
		object.Entry{Key: "FirstChar", Value: object.Integer(65)},
		object.Entry{Key: "Widths", Value: object.Array(widths)},
		object.Entry{Key: "FontDescriptor", Value: fd},
	)
}

// TestUAGlyphWidthsMatchTheProgram: the width a font dictionary declares for a
// shown glyph must agree with the embedded program's to within one unit in
// 1000 (ISO 14289-1 7.21.5, ISO 14289-2 8.4.5.6; veraPDF allows a difference
// of at most 1). Only text in rendering mode 3 is exempt: clip-only mode 7 is
// not, although the PDF/A engine's gate exempts it too (corpus PDF_UA-1
// 7.21.5-t01-fail-a, PDF_UA-2 8.4.5.6-t01-fail-a).
func TestUAGlyphWidthsMatchTheProgram(t *testing.T) {
	prog := map[int]object.Object{7: object.NewStream(&object.Dictionary{}, widthProgram())}
	cases := []struct {
		name    string
		font    *object.Dictionary
		content string
		want    bool
	}{
		{"declared widths agree", simpleTrueType(object.Integer(500), object.Integer(600)), "BT /F1 12 Tf (AB) Tj ET", false},
		{"a width one unit off is within tolerance", simpleTrueType(object.Real(501), object.Integer(600)), "BT /F1 12 Tf (A) Tj ET", false},
		{"a width more than one unit off", simpleTrueType(object.Real(501.5), object.Integer(600)), "BT /F1 12 Tf (A) Tj ET", true},
		{"the corpus file's defect: a much narrower width", simpleTrueType(object.Integer(192), object.Integer(600)), "BT /F1 12 Tf (A) Tj ET", true},
		{"a wrong width for a glyph never shown", simpleTrueType(object.Integer(500), object.Integer(999)), "BT /F1 12 Tf (A) Tj ET", false},
		{"invisible text (mode 3) is exempt", simpleTrueType(object.Integer(192), object.Integer(600)), "BT 3 Tr /F1 12 Tf (A) Tj ET", false},
		{"clip-only text (mode 7) is not", simpleTrueType(object.Integer(192), object.Integer(600)), "BT 7 Tr /F1 12 Tf (A) Tj ET", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ua1, ua2 := clauseFindings(t, func() core.View { return fontPage(c.font, c.content, prog) }, "7.21.5")
			if got := len(ua1) > 0; got != c.want {
				t.Errorf("PDF/UA-1: reported = %v, want %v (%v)", got, c.want, ua1)
			}
			if got := len(ua2) > 0; got != c.want {
				t.Errorf("PDF/UA-2: reported = %v, want %v (%v)", got, c.want, ua2)
			}
			if len(ua1) > 1 {
				t.Errorf("one finding per font, got %d: %v", len(ua1), ua1)
			}
		})
	}
}

// TestUAGlyphWidthsTrueTypeNameTable: a non-symbolic TrueType font's code is
// compared through the code-to-name table ISO 32000-1 9.6.6.4 builds — an
// Encoding dictionary's BaseEncoding, updated by Differences, with "any
// undefined entries ... filled using StandardEncoding" — and a name the (3,1)
// cmap lacks selects glyph 0, whose width is then the program's. veraPDF
// 1.30.3 reads it the same way: a /BaseEncoding of /Identity (corpus PDF_UA-1
// 7.21.6-t02-fail-a) initializes nothing, so code 65 is StandardEncoding's
// "A", and a name with no glyph is compared against .notdef's advance.
func TestUAGlyphWidthsTrueTypeNameTable(t *testing.T) {
	prog := map[int]object.Object{7: object.NewStream(&object.Dictionary{}, widthProgram())}
	identity := func(wA object.Object) *object.Dictionary {
		f := simpleTrueType(wA, object.Integer(600))
		f.Set("Encoding", object.NewDictionary(object.Entry{Key: "BaseEncoding", Value: object.Name("Identity")}))
		return f
	}
	euro := func(wA object.Object) *object.Dictionary {
		f := simpleTrueType(wA, object.Integer(600))
		f.Set("Encoding", object.NewDictionary(
			object.Entry{Key: "BaseEncoding", Value: object.Name("WinAnsiEncoding")},
			object.Entry{Key: "Differences", Value: object.Array{object.Integer(65), object.Name("Euro")}},
		))
		return f
	}
	cases := []struct {
		name string
		font *object.Dictionary
		want bool
	}{
		{"an unknown BaseEncoding falls back to StandardEncoding: A's width agrees", identity(object.Integer(500)), false},
		{"an unknown BaseEncoding falls back to StandardEncoding: A's width differs", identity(object.Integer(777)), true},
		{"a name the cmap lacks is compared against .notdef's width", euro(object.Integer(500)), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ua1, ua2 := clauseFindings(t, func() core.View { return fontPage(c.font, "BT /F1 12 Tf (A) Tj ET", prog) }, "7.21.5")
			if got := len(ua1) > 0; got != c.want {
				t.Errorf("PDF/UA-1: reported = %v, want %v (%v)", got, c.want, ua1)
			}
			if got := len(ua2) > 0; got != c.want {
				t.Errorf("PDF/UA-2: reported = %v, want %v (%v)", got, c.want, ua2)
			}
		})
	}
}

// identityType2Font is an Identity-H Type 0 font over a CIDFontType2 on
// widthProgram (object 7) — .notdef 0 wide, CID 1 500, CID 2 600 — with
// CIDToGIDMap Identity, /DW dw and, when not nil, /W w.
func identityType2Font(w object.Object, dw int) *object.Dictionary {
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("Test")},
		object.Entry{Key: "Flags", Value: object.Integer(4)},
		object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 7}},
	)
	desc := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "FontDescriptor", Value: fd},
		object.Entry{Key: "CIDToGIDMap", Value: object.Name("Identity")},
		object.Entry{Key: "DW", Value: object.Integer(dw)},
	)
	if w != nil {
		desc.Set("W", w)
	}
	return object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
		object.Entry{Key: "DescendantFonts", Value: object.Array{desc}},
	)
}

// TestUAGlyphWidthsCIDFont: the same rule for a composite font, where the
// declared width is the CIDFont's /W entry for the CID, or /DW.
func TestUAGlyphWidthsCIDFont(t *testing.T) {
	mk := identityType2Font
	prog := map[int]object.Object{7: object.NewStream(&object.Dictionary{}, widthProgram())}
	cases := []struct {
		name string
		font *object.Dictionary
		want bool
	}{
		{"/W states both widths", mk(object.Array{object.Integer(1), object.Array{object.Integer(500), object.Integer(600)}}, 1000), false},
		{"/W states one wrongly", mk(object.Array{object.Integer(1), object.Array{object.Integer(500), object.Integer(650)}}, 1000), true},
		{"/DW covers a CID /W omits, wrongly", mk(object.Array{object.Integer(1), object.Array{object.Integer(500)}}, 1000), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ua1, ua2 := clauseFindings(t, func() core.View { return fontPage(c.font, "BT /F1 12 Tf <00010002> Tj ET", prog) }, "7.21.5")
			if got := len(ua1) > 0; got != c.want {
				t.Errorf("PDF/UA-1: reported = %v, want %v (%v)", got, c.want, ua1)
			}
			if got := len(ua2) > 0; got != c.want {
				t.Errorf("PDF/UA-2: reported = %v, want %v (%v)", got, c.want, ua2)
			}
		})
	}
}

// TestUAGlyphWidthsType3: a Type 3 glyph's width is the wx operand of the d0
// or d1 that opens its CharProc, in glyph space like the font's /Widths.
func TestUAGlyphWidthsType3(t *testing.T) {
	mk := func(width int) *object.Dictionary {
		return object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Font")},
			object.Entry{Key: "Subtype", Value: object.Name("Type3")},
			object.Entry{Key: "FontBBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(750), object.Integer(750)}},
			object.Entry{Key: "FontMatrix", Value: object.Array{object.Real(0.001), object.Integer(0), object.Integer(0), object.Real(0.001), object.Integer(0), object.Integer(0)}},
			object.Entry{Key: "CharProcs", Value: object.NewDictionary(object.Entry{Key: "a", Value: object.IndirectRef{Number: 8}})},
			object.Entry{Key: "Encoding", Value: object.NewDictionary(object.Entry{Key: "Differences", Value: object.Array{object.Integer(65), object.Name("a")}})},
			object.Entry{Key: "FirstChar", Value: object.Integer(65)},
			object.Entry{Key: "LastChar", Value: object.Integer(65)},
			object.Entry{Key: "Widths", Value: object.Array{object.Integer(width)}},
			object.Entry{Key: "Resources", Value: &object.Dictionary{}},
		)
	}
	procs := map[int]object.Object{8: object.NewStream(&object.Dictionary{}, []byte("750 0 d0 0 0 750 750 re f"))}
	for _, c := range []struct {
		width int
		want  bool
	}{{750, false}, {700, true}} {
		ua1, _ := clauseFindings(t, func() core.View { return fontPage(mk(c.width), "BT /F1 12 Tf (A) Tj ET", procs) }, "7.21.5")
		if got := len(ua1) > 0; got != c.want {
			t.Errorf("/Widths [%d] against d0 750: reported = %v, want %v (%v)", c.width, got, c.want, ua1)
		}
	}
}

// differencesTrueType is a non-symbolic TrueType font whose encoding is
// WinAnsi updated by diffs, over the program in object 7.
func differencesTrueType(diffs object.Array) *object.Dictionary {
	f := simpleTrueType(object.Integer(500), object.Integer(600))
	f.Set("Encoding", object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Encoding")},
		object.Entry{Key: "BaseEncoding", Value: object.Name("WinAnsiEncoding")},
		object.Entry{Key: "Differences", Value: diffs},
	))
	return f
}

// macRomanOnlyProgram is a TrueType program whose cmap has a single (1,0)
// Macintosh Roman subtable and no Microsoft Unicode (3,1) one.
func macRomanOnlyProgram() []byte {
	sub := make([]byte, 6+256) // format 0: format, length, language, 256 glyph ids
	binary.BigEndian.PutUint16(sub[2:], uint16(len(sub)))
	sub[6+'A'] = 1
	return fonttest.SFNTWithCmapSubtables([]fonttest.CmapSub{{Plat: 1, Enc: 0, Data: sub}})
}

// TestUADifferencesNameAGLGlyphs: a non-symbolic TrueType font may have a
// /Differences array only if every glyph name in it is in the Adobe Glyph List
// and, when the program is embedded, the program has a Microsoft Unicode (3,1)
// cmap subtable (ISO 14289-1 7.21.6, ISO 14289-2 8.4.5.7). veraPDF reads "in
// the Adobe Glyph List" as membership of the list itself — its own copy, which
// also carries .notdef — so a uniXXXX name is not in it, whatever the Adobe
// Glyph Naming convention makes of it (corpus PDF_UA-1 7.21.6-t02-fail-d,
// PDF_UA-2 8.4.5.7-t02-fail-d, whose /Differences names /gravee).
func TestUADifferencesNameAGLGlyphs(t *testing.T) {
	withProgram := func(p []byte) map[int]object.Object {
		return map[int]object.Object{7: object.NewStream(&object.Dictionary{}, p)}
	}
	cases := []struct {
		name  string
		diffs object.Array
		prog  []byte
		want  bool
	}{
		{"listed names", object.Array{object.Integer(96), object.Name("grave"), object.Name("Euro")}, widthProgram(), false},
		{"the corpus file's /gravee", object.Array{object.Integer(96), object.Name("gravee")}, widthProgram(), true},
		{"a name from the full list beyond the Latin sets", object.Array{object.Integer(200), object.Name("afii10017")}, widthProgram(), false},
		{".notdef, which veraPDF's copy of the list carries", object.Array{object.Integer(200), object.Name(".notdef")}, widthProgram(), false},
		{"a uniXXXX name is not in the list", object.Array{object.Integer(200), object.Name("uni0041")}, widthProgram(), true},
		{"listed names, but no (3,1) cmap", object.Array{object.Integer(96), object.Name("grave")}, macRomanOnlyProgram(), true},
		{"an empty /Differences still asks for the (3,1) cmap", object.Array{}, macRomanOnlyProgram(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ua1, ua2 := clauseFindings(t, func() core.View {
				return fontPage(differencesTrueType(c.diffs), "BT /F1 12 Tf (A) Tj ET", withProgram(c.prog))
			}, "7.21.6")
			if got := len(ua1) > 0; got != c.want {
				t.Errorf("PDF/UA-1: reported = %v, want %v (%v)", got, c.want, ua1)
			}
			if got := len(ua2) > 0; got != c.want {
				t.Errorf("PDF/UA-2: reported = %v, want %v (%v)", got, c.want, ua2)
			}
		})
	}

	// Without an embedded program veraPDF finds the names compliant: the
	// requirement is on what the program can look up. (The font is reported
	// anyway, under 7.21.4.1, for not being embedded.)
	f := differencesTrueType(object.Array{object.Integer(96), object.Name("gravee")})
	f.Get("FontDescriptor").(*object.Dictionary).Delete("FontFile2")
	if ua1, _ := clauseFindings(t, func() core.View { return fontPage(f, "BT /F1 12 Tf (A) Tj ET", nil) }, "7.21.6"); len(ua1) != 0 {
		t.Errorf("a font with no program is held to its program's cmap: %v", ua1)
	}

	// A symbolic TrueType font is not asked: its rule is that it has no
	// /Encoding at all, and that is the finding.
	s := differencesTrueType(object.Array{object.Integer(96), object.Name("gravee")})
	s.Get("FontDescriptor").(*object.Dictionary).Set("Flags", object.Integer(4))
	ua1, _ := clauseFindings(t, func() core.View {
		return fontPage(s, "BT /F1 12 Tf (A) Tj ET", map[int]object.Object{7: object.NewStream(&object.Dictionary{}, widthProgram())})
	}, "7.21.6")
	if len(ua1) != 1 {
		t.Errorf("symbolic TrueType with an /Encoding: want the one finding for the /Encoding, got %v", ua1)
	}
}

// TestUAGlyphWidthsAskEachCIDOnce: the composite-font walk asks about each CID
// once, and a code the font's CMap does not define is not one: it names no
// CID. Counting it as CID 0 would excuse a real CID 0 shown after it.
func TestUAGlyphWidthsAskEachCIDOnce(t *testing.T) {
	// One byte to a code; 0x41 names CID 0 and 0x43 is in the codespace but
	// names nothing.
	const cmapSrc = "begincmap 1 begincodespacerange <00> <FF> endcodespacerange 1 begincidchar <41> 0 endcidchar endcmap"
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("Test")},
		object.Entry{Key: "Flags", Value: object.Integer(4)},
		object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 7}},
	)
	desc := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "FontDescriptor", Value: fd},
		object.Entry{Key: "CIDToGIDMap", Value: object.Name("Identity")},
		object.Entry{Key: "DW", Value: object.Integer(1000)}, // .notdef advances 0
	)
	font := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "Encoding", Value: object.IndirectRef{Number: 9}},
		object.Entry{Key: "DescendantFonts", Value: object.Array{desc}},
	)
	extra := map[int]object.Object{
		7: object.NewStream(&object.Dictionary{}, widthProgram()),
		9: object.NewStream(&object.Dictionary{}, []byte(cmapSrc)),
	}
	ua1, _ := clauseFindings(t, func() core.View { return fontPage(font, "BT /F1 12 Tf <4341> Tj ET", extra) }, "7.21.5")
	if len(ua1) == 0 {
		t.Error("CID 0, shown after a code the CMap does not define, was not compared")
	}
}

// TestUAGlyphWidthsSubstituteCID0: a code the CMap maps to no CID — here the
// odd byte that ends an Identity-H string — and a CID the program has no glyph
// for are drawn with CID 0's glyph (ISO 32000-1 9.7.6.3), and advance by its
// width. veraPDF compares that width with the declared one, and so does pdf0.
func TestUAGlyphWidthsSubstituteCID0(t *testing.T) {
	prog := map[int]object.Object{7: object.NewStream(&object.Dictionary{}, widthProgram())}
	w := object.Array{object.Integer(1), object.Array{object.Integer(500), object.Integer(600)}}
	cases := []struct {
		name    string
		dw      int
		content string
		want    bool
	}{
		{"an incomplete code, .notdef's 0 against /DW 1000", 1000, "BT /F1 12 Tf <000100> Tj ET", true},
		{"an incomplete code, /DW 0", 0, "BT /F1 12 Tf <000100> Tj ET", false},
		{"CID 9, which the program lacks, against /DW 1000", 1000, "BT /F1 12 Tf <0009> Tj ET", true},
		{"CID 9, /DW 0", 0, "BT /F1 12 Tf <0009> Tj ET", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ua1, ua2 := clauseFindings(t, func() core.View { return fontPage(identityType2Font(w, c.dw), c.content, prog) }, "7.21.5")
			if got := len(ua1) > 0; got != c.want {
				t.Errorf("PDF/UA-1: reported = %v, want %v (%v)", got, c.want, ua1)
			}
			if got := len(ua2) > 0; got != c.want {
				t.Errorf("PDF/UA-2: reported = %v, want %v (%v)", got, c.want, ua2)
			}
		})
	}
}
