package fontcheck

import (
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the tests of the two widths the width rule compares when a
// shown glyph is not simply there: the program's, for a glyph the program
// lacks (the glyph drawn in its place is .notdef's), and the dictionary's, for
// a code outside /Widths (the descriptor's /MissingWidth, 0 by default).

// type1Simple is a Type 1 font over a program defining .notdef (250 wide) and
// A (600), declaring widths for codes from 65 on.
func type1Simple(t *testing.T, enc object.Object, symbolic bool, widths object.Array, missingWidth object.Object) *Simple {
	t.Helper()
	flags := 32
	if symbolic {
		flags = 4
	}
	fd := object.NewDictionary(object.Entry{Key: "Flags", Value: object.Integer(flags)})
	if missingWidth != nil {
		fd.Set("MissingWidth", missingWidth)
	}
	f := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("Type1")},
		object.Entry{Key: "FirstChar", Value: object.Integer(65)},
		object.Entry{Key: "FontDescriptor", Value: fd},
	)
	if enc != nil {
		f.Set("Encoding", enc)
	}
	if widths != nil {
		f.Set("Widths", widths)
	}
	fp := &font.Program{
		GlyphNames:  map[string]bool{".notdef": true, "A": true},
		WidthByName: map[string]float64{".notdef": 250, "A": 600},
	}
	return newSimple(mkView(nil), f, fd, fp)
}

// TestMissingSimpleGlyphIsComparedAsNotdef: "If an encoding maps to a
// character name that does not exist in the Type 1 font program, the .notdef
// glyph shall be substituted" (ISO 32000-1 9.6.6.2), and so is a code the
// encoding names no glyph for. The advance drawn is .notdef's, and that is the
// program width the declared one is compared with. Where the base encoding is
// the program's own, which pdf0 does not read, a code without a name is not
// known to select .notdef, and nothing is compared.
func TestMissingSimpleGlyphIsComparedAsNotdef(t *testing.T) {
	winAnsi := object.Name("WinAnsiEncoding")
	for _, c := range []struct {
		name     string
		s        *Simple
		code     byte
		progW    float64
		haveProg bool
		differs  bool
	}{
		{"a glyph the program has", type1Simple(t, winAnsi, false, object.Array{object.Integer(600), object.Integer(250)}, nil), 'A', 600, true, false},
		{"B is missing: .notdef's 250 agrees", type1Simple(t, winAnsi, false, object.Array{object.Integer(600), object.Integer(250)}, nil), 'B', 250, true, false},
		{"B is missing: .notdef's 250 against 600", type1Simple(t, winAnsi, false, object.Array{object.Integer(600), object.Integer(600)}, nil), 'B', 250, true, true},
		{"a code WinAnsiEncoding leaves undefined", type1Simple(t, winAnsi, false, object.Array{object.Integer(600)}, nil), 0x81, 250, true, true},
		{"a symbolic font's built-in encoding is not read", type1Simple(t, nil, true, object.Array{object.Integer(600), object.Integer(600)}, nil), 'B', 0, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, ok := c.s.ProgramWidth(c.code)
			if ok != c.haveProg || w != c.progW {
				t.Errorf("ProgramWidth(%#x) = %v, %v; want %v, %v", c.code, w, ok, c.progW, c.haveProg)
			}
			if got := c.s.WidthDiffers(mkView(nil), c.code); got != c.differs {
				t.Errorf("WidthDiffers(%#x) = %v, want %v", c.code, got, c.differs)
			}
		})
	}
	// A program without .notdef gives nothing to compare a missing glyph with.
	s := type1Simple(t, winAnsi, false, object.Array{object.Integer(600), object.Integer(600)}, nil)
	delete(s.Program.GlyphNames, ".notdef")
	delete(s.Program.WidthByName, ".notdef")
	if w, ok := s.ProgramWidth('B'); ok {
		t.Errorf("no .notdef in the program: ProgramWidth = %v, true; want no width", w)
	}
}

// TestCodeOutsideWidthsDeclaresMissingWidth: "For character codes outside the
// range FirstChar to LastChar, the value of MissingWidth from the
// FontDescriptor entry for this font shall be used" (ISO 32000-1 Table 111),
// and MissingWidth's "Default value: 0" (Table 122). A dictionary with no
// /Widths at all declares nothing unless it names a MissingWidth.
func TestCodeOutsideWidthsDeclaresMissingWidth(t *testing.T) {
	winAnsi := object.Name("WinAnsiEncoding")
	for _, c := range []struct {
		name string
		s    *Simple
		w    float64
		ok   bool
	}{
		{"Widths, no MissingWidth", type1Simple(t, winAnsi, false, object.Array{object.Integer(600)}, nil), 0, true},
		{"Widths and MissingWidth", type1Simple(t, winAnsi, false, object.Array{object.Integer(600)}, object.Integer(250)), 250, true},
		{"no Widths, no MissingWidth", type1Simple(t, winAnsi, false, nil, nil), 0, false},
		{"no Widths, MissingWidth", type1Simple(t, winAnsi, false, nil, object.Integer(250)), 250, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if w, ok := c.s.DeclaredWidth(mkView(nil), 'B'); w != c.w || ok != c.ok {
				t.Errorf("DeclaredWidth('B') = %v, %v; want %v, %v", w, ok, c.w, c.ok)
			}
		})
	}
}

// cidType2 is an Identity-H Type 0 font over a CIDFontType2 whose program has
// .notdef (0 wide) and glyph 1 (500), with CIDToGIDMap Identity, /DW dw and a
// /W giving CID 1 its 500. cmap, when not empty, is an embedded CMap that
// replaces Identity-H.
func cidType2(t *testing.T, dw int, cmap string) *CID {
	t.Helper()
	doc := mkView(map[int]*object.IndirectObject{
		7: {Number: 7, Value: object.NewStream(&object.Dictionary{}, fonttest.SFNT(fonttest.SFNTOptions{Glyphs: []fonttest.Glyph{{Rune: 'A', Advance: 500, HasShape: true}}}))},
		8: {Number: 8, Value: object.NewStream(&object.Dictionary{}, []byte(cmap))},
	})
	desc := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "CIDToGIDMap", Value: object.Name("Identity")},
		object.Entry{Key: "DW", Value: object.Integer(dw)},
		object.Entry{Key: "W", Value: object.Array{object.Integer(1), object.Array{object.Integer(500)}}},
		object.Entry{Key: "FontDescriptor", Value: object.NewDictionary(object.Entry{Key: "FontFile2", Value: object.IndirectRef{Number: 7}})},
	)
	f := object.NewDictionary(
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
		object.Entry{Key: "DescendantFonts", Value: object.Array{desc}},
	)
	if cmap != "" {
		f.Set("Encoding", object.IndirectRef{Number: 8})
	}
	c, _, r := LoadCID(doc, f)
	if c == nil {
		t.Fatalf("fixture: LoadCID: %v", r)
	}
	return c
}

// TestMissingCIDGlyphIsComparedAsCID0: a CID whose glyph the CIDFont lacks,
// and a code the CMap maps to no CID, are drawn with "the glyph for CID 0"
// (ISO 32000-1 9.7.6.3), after any notdef mapping the CMap has. Identity-H has
// none, so the program width compared is CID 0's glyph's. A CMap of the
// document's own may have notdef mappings, which pdf0 does not consult for a
// missing glyph, and nothing is compared there.
func TestMissingCIDGlyphIsComparedAsCID0(t *testing.T) {
	differs := func(c *CID, s []byte) bool {
		for g := range c.Shown(s) {
			if c.WidthDiffers(g) {
				return true
			}
		}
		return false
	}
	c := cidType2(t, 1000, "")
	if differs(c, []byte{0, 1}) {
		t.Error("CID 1, which the program has at its declared 500, was reported")
	}
	if !differs(c, []byte{0, 5}) {
		t.Error("CID 5, which the program lacks: .notdef's 0 against /DW 1000 was not reported")
	}
	if !differs(c, []byte{0}) {
		t.Error("an incomplete code: CID 0's 0 against /DW 1000 was not reported")
	}
	c = cidType2(t, 0, "")
	if differs(c, []byte{0, 5}) || differs(c, []byte{0}) {
		t.Error("a missing glyph declared as wide as .notdef was reported")
	}
	cmap := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <0000> <FFFF> endcodespacerange\n" +
		"1 begincidrange <0000> <FFFF> 0 endcidrange\nendcmap\n"
	c = cidType2(t, 1000, cmap)
	if !c.HaveCMap || c.CMap.Identity() {
		t.Fatal("fixture: the embedded CMap was not read")
	}
	if differs(c, []byte{0, 5}) {
		t.Error("a missing glyph under a CMap of the document's own was compared")
	}
}

// TestType3CodeOutsideWidthsDeclaresMissingWidth: a Type 3 font's codes
// outside /Widths take its descriptor's /MissingWidth too, 0 by default.
func TestType3CodeOutsideWidthsDeclaresMissingWidth(t *testing.T) {
	mk := func(missingWidth object.Object) (core.View, *object.Dictionary) {
		cp := object.NewStream(&object.Dictionary{}, []byte("500 0 d0"))
		f := object.NewDictionary(
			object.Entry{Key: "Subtype", Value: object.Name("Type3")},
			object.Entry{Key: "CharProcs", Value: object.NewDictionary(
				object.Entry{Key: "A", Value: cp}, object.Entry{Key: "B", Value: cp})},
			object.Entry{Key: "Encoding", Value: object.NewDictionary(
				object.Entry{Key: "Differences", Value: object.Array{object.Integer(65), object.Name("A"), object.Name("B")}})},
			object.Entry{Key: "FirstChar", Value: object.Integer(65)},
			object.Entry{Key: "Widths", Value: object.Array{object.Integer(500)}},
		)
		if missingWidth != nil {
			f.Set("FontDescriptor", object.NewDictionary(object.Entry{Key: "MissingWidth", Value: missingWidth}))
		}
		return mkView(nil), f
	}
	u := &core.FontTextUsage{Strings: [][]byte{[]byte("AB")}}
	if doc, f := mk(nil); !Type3WidthDiffers(doc, f, u) {
		t.Error("B outside /Widths: its 500 against the default MissingWidth 0 was not reported")
	}
	if doc, f := mk(object.Integer(500)); Type3WidthDiffers(doc, f, u) {
		t.Error("B outside /Widths, MissingWidth 500: reported")
	}
}
