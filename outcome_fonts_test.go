package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/pdfa"
)

func hasFinding(vs []pdfa.Violation, rule, substr string) bool {
	for _, v := range vs {
		if (rule == "" || v.Rule == rule) && strings.Contains(v.Message, substr) {
			return true
		}
	}
	return false
}

// TestFontProgramOverALimitIsNotDamaged is audit 2026-09-22 C47: a font
// program over the scanning limit came back as the same nil as one that does
// not parse, and PDF/A reported "embedded TrueType font program is damaged"
// next to the limit note saying the check had been skipped. The fixture's
// program is garbage, so the control — read with no limit — must report it
// damaged; read under a limit it is smaller than, it must not.
func TestFontProgramOverALimitIsNotDamaged(t *testing.T) {
	program := bytes.Repeat([]byte("not a font program "), 200_000/19)
	file := buildRawPDF(onePage(
		rawObj{dict: "<<>>", stream: []byte("BT /F1 12 Tf 10 10 Td (Hi) Tj ET")},
		"<</Font<</F1 5 0 R>>>>",
		rawObj{dict: "<</Type/Font/Subtype/TrueType/BaseFont/Garbage/FirstChar 72/LastChar 105/Widths[" + strings.Repeat("500 ", 34) + "]/FontDescriptor 6 0 R>>"},
		rawObj{dict: "<</Type/FontDescriptor/FontName/Garbage/Flags 32/FontBBox[0 0 1000 1000]/ItalicAngle 0/Ascent 800/Descent -200/CapHeight 700/StemV 80/FontFile2 7 0 R>>"},
		rawObj{dict: "<<>>", stream: program},
	))

	if vs := ValidatePDFA(readRaw(t, file), pdfa.PDFA2b); !hasFinding(vs, "", "font program is damaged") {
		t.Fatalf("control: the garbage program is not reported damaged: %v", findingKeys(vs))
	}
	vs := ValidatePDFA(readRaw(t, file, WithMaxContentStreamBytes(100_000)), pdfa.PDFA2b)
	if hasFinding(vs, "", "font program is damaged") {
		t.Error("a font program pdf0 did not read under its scanning limit was reported damaged")
	}
	if !hasFinding(vs, "limit", "content-stream-size") {
		t.Errorf("no limit finding for the program that was not read: %v", findingKeys(vs))
	}
}

// TestCIDSetOverALimitIsNotEmpty is the CIDSet half of C47: a CIDSet over the
// scanning limit decoded to nothing, and nothing is exactly what an empty
// CIDSet is, so PDF/A-1b reported "an empty CIDSet stream" and PDF/A-2b "does
// not list all CIDs used". The control CIDSet is all zeros, which really is
// empty and really does not list CID 1; under a limit smaller than the CIDSet
// and larger than the program, neither may be asserted.
func TestCIDSetOverALimitIsNotEmpty(t *testing.T) {
	cff := fonttest.CFF(fonttest.CFFOptions{Glyphs: 5, CIDKeyed: true})
	cidset := make([]byte, len(cff)+4096)
	file := buildRawPDF(onePage(
		rawObj{dict: "<<>>", stream: []byte("BT /F1 12 Tf 10 10 Td <0001> Tj ET")},
		"<</Font<</F1 5 0 R>>>>",
		rawObj{dict: "<</Type/Font/Subtype/Type0/BaseFont/ABCDEF+Test/Encoding/Identity-H/DescendantFonts[6 0 R]>>"},
		rawObj{dict: "<</Type/Font/Subtype/CIDFontType0/BaseFont/ABCDEF+Test/CIDSystemInfo<</Registry(Adobe)/Ordering(Identity)/Supplement 0>>/FontDescriptor 7 0 R/DW 1000>>"},
		rawObj{dict: "<</Type/FontDescriptor/FontName/ABCDEF+Test/Flags 4/FontBBox[0 0 1000 1000]/ItalicAngle 0/Ascent 800/Descent -200/CapHeight 700/StemV 80/FontFile3 8 0 R/CIDSet 9 0 R>>"},
		rawObj{dict: "<</Subtype/CIDFontType0C>>", stream: cff},
		rawObj{dict: "<<>>", stream: cidset},
	))

	checks := []struct {
		level pdfa.Level
		msg   string
	}{
		{pdfa.PDFA1b, "contains an empty CIDSet stream"},
		{pdfa.PDFA2b, "CIDSet does not list all CIDs used for rendering"},
	}
	for _, c := range checks {
		if vs := ValidatePDFA(readRaw(t, file), c.level); !hasFinding(vs, "", c.msg) {
			t.Fatalf("%s control: an all-zero CIDSet is not reported (%q): %v", c.level, c.msg, findingKeys(vs))
		}
	}
	limit := WithMaxContentStreamBytes(len(cff) + 100)
	for _, c := range checks {
		vs := ValidatePDFA(readRaw(t, file, limit), c.level)
		if hasFinding(vs, "", c.msg) {
			t.Errorf("%s: a CIDSet pdf0 did not read was reported (%q)", c.level, c.msg)
		}
		if !hasFinding(vs, "limit", "content-stream-size") {
			t.Errorf("%s: no limit finding for the CIDSet that was not read: %v", c.level, findingKeys(vs))
		}
	}
}
