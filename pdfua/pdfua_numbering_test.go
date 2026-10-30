package pdfua

import (
	"testing"
	"unicode/utf16"

	"github.com/mgilbir/pdf0/object"
)

// Which clause each finding is reported under, by part: as veraPDF's PDF/UA-1
// and PDF/UA-2 profiles number the rule (TestPDFUARuleCoverageByVeraPDF holds
// the corpus to the same).

// utf16Text is s as a UTF-16BE PDF text string.
func utf16Text(s string) object.String {
	b := []byte{0xFE, 0xFF}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return object.String{Value: b}
}

// TestPDFUA2NumbersTheRulesItShares: a check PDF/UA-1 and -2 share reports
// PDF/UA-1's clause on a part-1 run and ISO 14289-2's on a part-2 one. The
// tagged-PDF rule is 6.2 in both (veraPDF 6.2-1).
func TestPDFUA2NumbersTheRulesItShares(t *testing.T) {
	f := newUAFixture("")
	f.cat.Delete("MarkInfo")
	f.cat.Delete("ViewerPreferences")
	f.cat.Delete("StructTreeRoot")
	for _, c := range []struct{ sub, ua1, ua2 string }{
		{"/MarkInfo", "6.2", "6.2"},
		{"/StructTreeRoot", "7.1", "8.2.1"},
		{"DisplayDocTitle", "7.1", "8.11.2"},
	} {
		if got := findings(f.run("1"), c.ua1, c.sub); len(got) == 0 {
			t.Errorf("PDF/UA-1, %s: no finding under %s", c.sub, c.ua1)
		}
		if got := findings(f.run("2"), c.ua2, c.sub); len(got) == 0 {
			t.Errorf("PDF/UA-2, %s: no finding under %s", c.sub, c.ua2)
		}
		if got := findings(f.run("2"), c.ua1, c.sub); c.ua1 != c.ua2 && len(got) != 0 {
			t.Errorf("PDF/UA-2, %s: still reported under PDF/UA-1's %s: %v", c.sub, c.ua1, got)
		}
	}
}

// TestUAMissingMetadata: both parts require the catalog's /Metadata, PDF/UA-1
// under 7.1 (veraPDF 7.1-8) and PDF/UA-2 under 8.11.1 (veraPDF 8.11.1-2);
// neither says it again under the identification rule (5).
func TestUAMissingMetadata(t *testing.T) {
	f := newUAFixture("")
	if got := findings(f.run("1"), "7.1", "/Metadata"); len(got) != 1 {
		t.Errorf("PDF/UA-1 without /Metadata: %v, want one 7.1 finding", got)
	}
	if got := findings(f.run("1"), "5", ""); len(got) != 0 {
		t.Errorf("PDF/UA-1 without /Metadata: also reported under 5: %v", got)
	}
	if got := findings(f.run("2"), "8.11.1", "/Metadata"); len(got) != 1 {
		t.Errorf("PDF/UA-2 without /Metadata: %v, want one 8.11.1 finding", got)
	}
	if got := findings(f.run("2"), "5", ""); len(got) != 0 {
		t.Errorf("PDF/UA-2 without /Metadata: also reported under 5: %v", got)
	}
}

// TestUAAnnotationNesting: an annotation under the wrong structure element is
// its kind's rule — a widget's 7.18.4, a link's 7.18.5, any other's 7.18.1
// (veraPDF 7.18.4-1, 7.18.5-1).
func TestUAAnnotationNesting(t *testing.T) {
	for _, c := range []struct{ subtype, clause string }{
		{"Widget", "7.18.4"}, {"Link", "7.18.5"}, {"Text", "7.18.1"},
	} {
		f := newUAFixture("")
		f.annot(c.subtype, [4]int{10, 10, 50, 50}, "P")
		if got := findings(f.run("1"), c.clause, "is nested in a <P>"); len(got) != 1 {
			t.Errorf("a /%s under a <P>: %v, want one finding under %s", c.subtype, got, c.clause)
		}
	}
}

// TestUALinkWithNoDescription: a link with neither /Contents nor /Alt fails
// the link rule and the general one (7.18.5 and 7.18.1; veraPDF 7.18.5-2 and
// 7.18.1-2), and one with an /Alt only the link rule.
func TestUALinkWithNoDescription(t *testing.T) {
	f := newUAFixture("")
	f.annot("Link", [4]int{10, 10, 50, 50}, "Link")
	if got := findings(f.run("1"), "7.18.1", "Link annotation has no alternate description"); len(got) != 1 {
		t.Errorf("a link with no /Contents and no /Alt: %v, want a 7.18.1 finding too", got)
	}
	f = newUAFixture("")
	_, el := f.annot("Link", [4]int{10, 10, 50, 50}, "Link")
	el.Set("Alt", str("a link"))
	if got := findings(f.run("1"), "7.18.1", "Link annotation"); len(got) != 0 {
		t.Errorf("a link with an /Alt: %v, want no 7.18.1 finding", got)
	}
	if got := findings(f.run("1"), "7.18.5", "Link annotation"); len(got) != 1 {
		t.Errorf("a link with an /Alt and no /Contents: %v, want the 7.18.5 finding", got)
	}
}

// TestUA2TextStringPUA: a text string meant for a reader that uses the
// Private Use Area fails PDF/UA-2 8.6, which PDF/UA-1 does not have; one that
// does not, or that is not a Unicode string, is fine.
func TestUA2TextStringPUA(t *testing.T) {
	for _, c := range []struct {
		name string
		t    object.String
		want bool
	}{
		{"a PUA title", utf16Text(""), true},
		{"a supplementary PUA title", utf16Text("\U000F0001"), true},
		{"an ordinary Unicode title", utf16Text("Résumé"), false},
		{"a PDFDocEncoded title", str("Title"), false},
	} {
		f := newUAFixture("")
		p, _ := f.elem(f.docEl, f.docElRef, "P")
		p.Set("T", c.t)
		if got := len(findings(f.run("2"), "8.6", "/T")) > 0; got != c.want {
			t.Errorf("PDF/UA-2, %s: finding %v, want %v", c.name, got, c.want)
		}
		if got := findings(f.run("1"), "8.6", ""); len(got) != 0 {
			t.Errorf("PDF/UA-1, %s: %v; 8.6 is PDF/UA-2's", c.name, got)
		}
	}
}

// TestUACIDSystemInfoWithAnUnknownCMap: a CMap that is neither predefined nor
// embedded states no Registry or Ordering, and fails the CIDSystemInfo rule
// (7.21.3.1; veraPDF PDF_UA-1 7.21.3.3-t01-fail-a); one embedded without a
// CIDSystemInfo the same; Identity needs none.
func TestUACIDSystemInfoWithAnUnknownCMap(t *testing.T) {
	cid := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType0")},
		object.Entry{Key: "CIDSystemInfo", Value: object.NewDictionary(
			object.Entry{Key: "Registry", Value: str("Adobe")},
			object.Entry{Key: "Ordering", Value: str("Korea1")},
			object.Entry{Key: "Supplement", Value: object.Integer(2)})},
	)
	for _, c := range []struct {
		name string
		enc  object.Object
		want bool
	}{
		{"a CMap neither predefined nor embedded", object.Name("Adobe-Korea1-2"), true},
		{"an embedded CMap without CIDSystemInfo", object.NewStream(&object.Dictionary{}, nil), true},
		{"Identity-H", object.Name("Identity-H"), false},
	} {
		font := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Font")},
			object.Entry{Key: "Subtype", Value: object.Name("Type0")},
			object.Entry{Key: "Encoding", Value: c.enc},
			object.Entry{Key: "DescendantFonts", Value: object.Array{cid}},
		)
		d := mkView(map[int]*object.IndirectObject{1: {Number: 1, Value: font}}, nil)
		got := len(checkOneUACIDSystemInfo(d, font)) > 0
		if got != c.want {
			t.Errorf("%s: finding %v, want %v", c.name, got, c.want)
		}
	}
}
