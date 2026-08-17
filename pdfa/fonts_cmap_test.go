package pdfa

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// A Type 0 font whose /Encoding is a CMap the document carries, rather than
// Identity-H.
//
// Every glyph, .notdef and width check in checkCIDFontConsistency needs to turn
// a string into CIDs, and until the CMap was read there was one way to do that:
// assume two-byte codes equal to their CIDs. For a font with its own CMap that
// assumption is simply wrong, so the checks were skipped — 69 of the Type 0
// fonts in the veraPDF corpus carry one, against 149 using Identity, so this was
// most of what was not being looked at.

// cidTestProgram is a four-glyph CIDFontType2 program: glyph 1 has an outline,
// the rest are empty, and every advance is 500.
func cidTestProgram() []byte {
	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000)
	binary.BigEndian.PutUint16(head[50:], 0) // short loca
	maxp := make([]byte, 6)
	binary.BigEndian.PutUint16(maxp[4:], 4)
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], 4)
	hmtx := make([]byte, 16)
	for gid := 0; gid < 4; gid++ {
		binary.BigEndian.PutUint16(hmtx[4*gid:], 500)
	}
	// Glyph 1 occupies bytes 0..10 of glyf; the others are empty. Short loca
	// stores half-offsets.
	loca := make([]byte, 2*(4+1))
	binary.BigEndian.PutUint16(loca[2:], 0) // glyph 1 starts at 0
	for i := 2; i <= 4; i++ {
		binary.BigEndian.PutUint16(loca[2*i:], 5) // 10 bytes in
	}
	glyf := make([]byte, 10)
	return buildTestSFNT([]sfntTable{
		{"head", head}, {"maxp", maxp}, {"hhea", hhea}, {"hmtx", hmtx},
		{"loca", loca}, {"glyf", glyf},
	})
}

// cmapFontWith builds the font dictionary, with the given CMap as an embedded
// /Encoding stream.
func cmapFontWith(t *testing.T, cmapSrc string, shown []byte) (core.View, *object.Dictionary, *core.FontTextUsage) {
	t.Helper()
	fd := &object.Dictionary{}
	fd.Set("Flags", object.Integer(4))
	fd.Set("FontFile2", object.IndirectRef{Number: 9})
	desc := &object.Dictionary{}
	desc.Set("Subtype", object.Name("CIDFontType2"))
	desc.Set("FontDescriptor", fd)
	desc.Set("CIDToGIDMap", object.Name("Identity"))
	desc.Set("DW", object.Integer(500))

	enc := &object.Stream{Dict: object.Dictionary{}, Data: []byte(cmapSrc)}
	enc.Dict.Set("Length", object.Integer(len(cmapSrc)))

	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("Type0"))
	font.Set("Encoding", object.IndirectRef{Number: 11})
	font.Set("DescendantFonts", object.Array{desc})

	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1:  {Number: 1, Value: font},
		9:  {Number: 9, Value: &object.Stream{Dict: object.Dictionary{}, Data: cidTestProgram()}},
		11: {Number: 11, Value: enc},
	}})
	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{shown}, Modes: map[int]bool{0: true}}
	return doc, font, u
}

// oneByteCMap maps the single byte 0x41 to CID 1 and 0x42 to CID 9.
//
// CID 9 is past the font's four glyphs, so it exists in no sense at all. An
// empty glyph *inside* the font would not do: that is reported only when
// ToUnicode says the character is not whitespace, which is a different rule.
//
// One byte to a code, which is the part that cannot be faked by pretending the
// encoding is Identity: read as Identity-H, "AB" is one two-byte code 0x4142
// naming CID 16706, a glyph this four-glyph font does not have.
const oneByteCMap = `begincmap
1 begincodespacerange
<00> <FF>
endcodespacerange
2 begincidchar
<41> 1
<42> 9
endcidchar
endcmap`

// TestAnEmbeddedCMapIsReadAndItsCodesChecked is the gate opening.
//
// The font has four glyphs. The string draws 0x41 and 0x42, which the CMap
// sends to CID 1 and CID 9 — so the check has to report the second and not the
// first. Under the old code neither was reported, because the whole string was
// skipped for want of a CMap it could read.
func TestAnEmbeddedCMapIsReadAndItsCodesChecked(t *testing.T) {
	doc, font, u := cmapFontWith(t, oneByteCMap, []byte{0x41, 0x42})
	msgs := errMessages(checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u))

	var glyph []string
	for _, m := range msgs {
		if strings.Contains(m, "does not define a glyph") {
			glyph = append(glyph, m)
		}
	}
	if len(glyph) == 0 {
		t.Fatalf("a font whose CMap names a glyph it does not have was not "+
			"reported; the CMap was not read. All findings: %v", msgs)
	}
	// CID 9 is past the end of the font. CID 1 has an outline and must not be
	// reported.
	for _, m := range glyph {
		if strings.Contains(m, "CID 1)") {
			t.Errorf("CID 1 has an outline and was reported: %s", m)
		}
	}
	if !strings.Contains(strings.Join(glyph, " "), "CID 9") {
		t.Errorf("the report does not name CID 9, which the font does not have: %v", glyph)
	}
}

// TestAnEmbeddedCMapCutsCodesItsOwnWay: the same bytes read as Identity-H would
// be one code naming a CID this font does not have, so a checker that ignored
// the CMap and assumed two bytes would report a glyph the document never used.
//
// This is the false positive the old code avoided by skipping, and the one the
// new code has to avoid by being right.
func TestAnEmbeddedCMapCutsCodesItsOwnWay(t *testing.T) {
	// Both codes name glyphs that exist, so a correct reading reports nothing.
	src := `begincmap
1 begincodespacerange
<00> <FF>
endcodespacerange
1 begincidrange
<41> <42> 1
endcidrange
endcmap`
	doc, font, u := cmapFontWith(t, src, []byte{0x41, 0x42})
	// CID 1 and CID 2. Glyph 2 is empty, so the .notdef-style report is
	// expected for it; what must not appear is a complaint about CID 16706,
	// which is what reading the pair as one Identity-H code would produce.
	for _, m := range errMessages(checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)) {
		if strings.Contains(m, "16706") {
			t.Errorf("the string was read as a single two-byte code: %s", m)
		}
	}
}

// TestACodeOutsideTheCMapIsReported: a code the document wrote and its own CMap
// does not define names no glyph at all. It is not CID 0 — that would be a
// .notdef reference, which is a different fault — so it is reported as what it
// is.
func TestACodeOutsideTheCMapIsReported(t *testing.T) {
	doc, font, u := cmapFontWith(t, oneByteCMap, []byte{0x41, 0x7A})
	msgs := errMessages(checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u))
	joined := strings.Join(msgs, " ")
	if !strings.Contains(joined, "outside the font's CMap") {
		t.Errorf("an undefined code was not reported as one: %v", msgs)
	}
	if strings.Contains(joined, "references the .notdef glyph") {
		t.Errorf("an undefined code was reported as a .notdef reference, which is "+
			"a different thing the document did not do: %v", msgs)
	}
}

// TestAPredefinedCMapIsStillSkipped, because the data for it is not here.
//
// The checks must not run against a guess: UniJIS-UCS2-H is two bytes to a code
// and its CIDs are Adobe-Japan1's, and reading it as Identity would report
// every glyph on the page as missing.
func TestAPredefinedCMapIsStillSkipped(t *testing.T) {
	fd := &object.Dictionary{}
	fd.Set("Flags", object.Integer(4))
	fd.Set("FontFile2", object.IndirectRef{Number: 9})
	desc := &object.Dictionary{}
	desc.Set("Subtype", object.Name("CIDFontType2"))
	desc.Set("FontDescriptor", fd)
	desc.Set("CIDToGIDMap", object.Name("Identity"))
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("Type0"))
	font.Set("Encoding", object.Name("UniJIS-UCS2-H"))
	font.Set("DescendantFonts", object.Array{desc})
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: &object.Stream{Dict: object.Dictionary{}, Data: cidTestProgram()}},
	}})
	u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{{0x30, 0x42}}, Modes: map[int]bool{0: true}}

	for _, m := range errMessages(checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)) {
		if strings.Contains(m, "does not define a glyph") {
			t.Errorf("a predefined CMap was decoded as though it were Identity: %s", m)
		}
	}
}

// TestToUnicodeIsKeyedByCodeNotCID is a rule that only becomes visible once a
// non-Identity CMap is read.
//
// §9.10.3 maps a ToUnicode CMap over the character codes a content stream
// writes. Under Identity-H the code and the CID are the same number, so the
// distinction never showed; for a font with its own CMap they are different,
// and asking by CID reads whatever entry happens to sit at that number.
//
// It decides whether an empty glyph is reported. A subset must embed an outline
// for every glyph it renders, except a whitespace one — so the check asks
// ToUnicode what character the code meant. Ask with the wrong key and an empty glyph
// standing for a space gets reported, or one standing for a letter does not.
func TestToUnicodeIsKeyedByCodeNotCID(t *testing.T) {
	// Code 0x41 maps to CID 2, whose glyph is empty. ToUnicode says code 0x41
	// is a space, so the empty glyph is legitimate and must not be reported.
	//
	// The trap: CID 2 as a ToUnicode key is 'B', which is not whitespace — so a
	// checker asking by CID reports a font that is correct.
	const cmapSrc = `begincmap
1 begincodespacerange
<00> <FF>
endcodespacerange
1 begincidchar
<41> 2
endcidchar
endcmap`
	doc, font, u := cmapFontWith(t, cmapSrc, []byte{0x41})

	// ToUnicode: code 0x41 is a space; code 0x02 — the CID read as a code —
	// is 'B'.
	tu := "beginbfchar <0041> <0020> <0002> <0042> endbfchar"
	stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte(tu)}
	stream.Dict.Set("Length", object.Integer(len(tu)))
	doc.Objects[13] = &object.IndirectObject{Number: 13, Value: stream}
	font.Set("ToUnicode", object.IndirectRef{Number: 13})

	for _, m := range errMessages(checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)) {
		if strings.Contains(m, "does not define a glyph") {
			t.Errorf("an empty glyph standing for a space was reported: %s\n"+
				"ToUnicode was read at the CID rather than at the code", m)
		}
	}
}
