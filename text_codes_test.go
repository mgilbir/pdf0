package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/pdfa"
)

// Type 0 codes are cut by the font's CMap (audit 2026-09-22 C88). Text
// extraction and the PDF/A Level A Private Use scan both cut every composite
// string into two-byte codes, which is Identity-H and nothing else: a
// Shift-JIS or EUC CMap mixes one- and two-byte codes, and after the first
// one-byte character every code is read out of step. The fonts and CMaps here
// are built in the test.

// toUnicodeCMap is a ToUnicode CMap program with the given codespace and bfchar
// entries ("<41> <0041>" lines).
func toUnicodeCMap(codespace string, bfchar ...string) string {
	return "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange " + codespace + " endcodespacerange\n" +
		itoa(len(bfchar)) + " beginbfchar\n" + strings.Join(bfchar, "\n") + "\nendbfchar\nendcmap\n"
}

// type0Page draws content with a Type 0 font F1 (object 5) whose /Encoding is
// enc — a name, or "stream" for the embedded CMap cmap at object 8 — and whose
// ToUnicode, when toUnicode is not empty, is object 7.
func type0Page(content, enc, cmap, toUnicode string) []byte {
	font := "<</Type/Font/Subtype/Type0/BaseFont/X/DescendantFonts[6 0 R]"
	if enc == "stream" {
		font += "/Encoding 8 0 R"
	} else {
		font += "/Encoding/" + enc
	}
	if toUnicode != "" {
		font += "/ToUnicode 7 0 R"
	}
	objs := rawPage(content, "<</Font<</F1 5 0 R>>>>")
	objs = append(objs,
		rawObj{dict: font + ">>"},
		rawObj{dict: "<</Type/Font/Subtype/CIDFontType0/BaseFont/X/CIDSystemInfo<</Registry(Adobe)/Ordering(Japan1)/Supplement 2>>/FontDescriptor<</Type/FontDescriptor/FontName/X/Flags 4>>>>"},
		rawObj{dict: "<<>>", stream: []byte(toUnicode)},
	)
	if enc == "stream" {
		objs = append(objs, rawObj{dict: "<</Type/CMap/CMapName/X/CIDSystemInfo<</Registry(Adobe)/Ordering(Japan1)/Supplement 2>>>>", stream: []byte(cmap)})
	}
	return buildRawPDF(objs)
}

func extractTextOf(t *testing.T, pdf []byte) string {
	t.Helper()
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	return mustExtractText(t, doc)
}

func TestExtractTextCutsType0CodesByTheCMap(t *testing.T) {
	// A Shift-JIS-shaped CMap: one-byte ASCII, two-byte kanji.
	sjisToUnicode := toUnicodeCMap("<00> <80>", "<41> <0041>", "<42> <0042>", "<889F> <4E9C>")
	embedded := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"2 begincodespacerange <00> <80> <8140> <9FFC> endcodespacerange\n" +
		"2 begincidrange <20> <7E> 231 <889F> <889F> 1125 endcidrange\nendcmap\n"
	for _, c := range []struct {
		name, content, enc, cmap, toUnicode, want string
	}{
		{"embedded mixed-width CMap", "BT /F1 12 Tf <41889F42> Tj ET", "stream", embedded, sjisToUnicode, "A亜B"},
		{"predefined Shift-JIS CMap", "BT /F1 12 Tf <41889F42> Tj ET", "90ms-RKSJ-H", "", sjisToUnicode, "A亜B"},
		// No ToUnicode at all: a Uni* CMap's codes are Unicode themselves.
		{"predefined UCS-2 CMap, no ToUnicode", "BT /F1 12 Tf <30423044> Tj ET", "UniJIS-UCS2-H", "", "", "あい"},
		{"predefined UTF-16 CMap, no ToUnicode", "BT /F1 12 Tf <0041D83DDE00> Tj ET", "UniJIS-UTF16-H", "", "", "A😀"},
		// Identity is unchanged.
		{"Identity-H", "BT /F1 12 Tf <00410042> Tj ET", "Identity-H", "",
			toUnicodeCMap("<0000> <FFFF>", "<0041> <0061>", "<0042> <0062>"), "ab"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := extractTextOf(t, type0Page(c.content, c.enc, c.cmap, c.toUnicode)); got != c.want {
				t.Errorf("ExtractText = %q, want %q", got, c.want)
			}
		})
	}
}

// TestExtractTextReadsWholeToUnicodeEntries is audit 2026-09-22 C86, fixed in
// the base of this change by moving text extraction onto ParseToUnicodeRunes:
// a ToUnicode entry can name several characters (a ligature), a character
// outside the BMP (a surrogate pair), or come from an array-form bfrange. The
// first-rune probe extraction used to read kept one character of the first
// two and dropped the third.
func TestExtractTextReadsWholeToUnicodeEntries(t *testing.T) {
	toUnicode := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"3 beginbfchar\n<01> <00660069>\n<02> <D83DDE00>\n<41> <0041>\nendbfchar\n" +
		"1 beginbfrange\n<03> <05> [<0058> <0059> <005A>]\nendbfrange\nendcmap\n"
	objs := rawPage("BT /F1 12 Tf <0102410304 05> Tj ET", "<</Font<</F1 5 0 R>>>>")
	objs = append(objs,
		rawObj{dict: "<</Type/Font/Subtype/Type1/BaseFont/Helvetica/ToUnicode 6 0 R>>"},
		rawObj{dict: "<<>>", stream: []byte(toUnicode)})
	if got := extractTextOf(t, buildRawPDF(objs)); got != "fi😀AXYZ" {
		t.Errorf("ExtractText = %q, want %q", got, "fi😀AXYZ")
	}
}

// The Level A scan for Private Use characters reads the same codes. A
// one-byte code mapped to U+E000 in a Shift-JIS font was invisible to a
// two-byte cut, and so was the finding.
func TestLevelAPrivateUseScanCutsType0CodesByTheCMap(t *testing.T) {
	pdf := type0Page("BT /F1 12 Tf <4142> Tj ET", "90ms-RKSJ-H", "", toUnicodeCMap("<00> <80>", "<41> <E000>", "<42> <0042>"))
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range ValidatePDFA(doc, pdfa.PDFA2a) {
		if strings.Contains(v.Message, "U+E000, a Private Use Area code point") {
			found = true
		}
	}
	if !found {
		t.Error("the Private Use character behind a one-byte Shift-JIS code was not found")
	}
}
