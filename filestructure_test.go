package pdf0

import (
	"bytes"
	"testing"
)

func hasRuleMsg(errs []ValidationError, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

// ISO 32000-1 7.5.2 header structure (byte-level).
func TestFileHeaderBytes(t *testing.T) {
	bin := "%\xe2\xe3\xcf\xd3\n" // valid binary comment line
	cases := []struct {
		name string
		raw  string
		fail bool
	}{
		{"valid", "%PDF-1.7\n" + bin, false},
		{"valid-2.0", "%PDF-2.0\n" + bin, false},
		{"valid-crlf", "%PDF-1.7\r\n" + bin, false},
		{"not-at-zero", "junk\n%PDF-1.7\n" + bin, true},
		{"bad-version", "%PDF-2.01\n" + bin, true},
		{"trailing-space", "%PDF-2.0   \n" + bin, true},
		{"no-comment", "%PDF-1.7\ninvalid\n", true},
		{"short-comment", "%PDF-1.7\n%\xe2\xe3\xcf\n", true},
		{"ansi-comment", "%PDF-1.7\n%a\xe3\xcf\xd3\n", true},
	}
	for _, c := range cases {
		errs := checkFileHeaderBytes(PDFA2b, []byte(c.raw))
		got := hasRuleMsg(errs, "6.1.2")
		if got != c.fail {
			t.Errorf("%s: got fail=%v want %v (errs=%v)", c.name, got, c.fail, errs)
		}
	}
}

// Indirect object layout via a synthetic Document with offsets.
func TestIndirectObjectSyntax(t *testing.T) {
	build := func(body string) []ValidationError {
		off := int64(bytes.Index([]byte(body), []byte(" 0 obj")) - bytes.LastIndexByte([]byte(body[:bytes.Index([]byte(body), []byte(" 0 obj"))]), '\n'))
		_ = off
		doc := &Document{
			Objects: map[int]*IndirectObject{1: {Number: 1, Value: Integer(1)}},
			Offsets: map[int]int64{},
		}
		// Object header starts right after the first newline.
		nl := bytes.IndexByte([]byte(body), '\n')
		doc.Offsets[1] = int64(nl + 1)
		return checkIndirectObjectSyntax(doc, PDFA2b, []byte(body))
	}
	// Valid.
	if hasRuleMsg(build("%bin\n1 0 obj\n42\nendobj\n"), "6.1.9") {
		t.Error("valid object flagged")
	}
	// Two spaces between number and generation.
	if !hasRuleMsg(build("%bin\n1  0 obj\n42\nendobj\n"), "6.1.9") {
		t.Error("double space objnum/gen not flagged")
	}
	// obj not followed by EOL.
	if !hasRuleMsg(build("%bin\n1 0 obj 42 endobj\n"), "6.1.9") {
		t.Error("obj not followed by EOL not flagged")
	}
	// endobj preceded by space.
	if !hasRuleMsg(build("%bin\n1 0 obj\n42 endobj\n"), "6.1.9") {
		t.Error("space before endobj not flagged")
	}
}

// Cross-reference table format.
func TestXRefTableFormat(t *testing.T) {
	entry := "0000000000 65535 f\r\n0000000009 00000 n\r\n"
	valid := "xref\n0 2\n" + entry + "trailer\n"
	if hasRuleMsg(checkXRefTableFormat(nil, PDFA2b, []byte("\n"+valid)), "6.1.4") {
		t.Error("valid xref flagged")
	}
	// xref keyword followed by space.
	if !hasRuleMsg(checkXRefTableFormat(nil, PDFA2b, []byte("\nxref \n0 2\n"+entry)), "6.1.4") {
		t.Error("xref+space not flagged")
	}
	// Two EOLs after xref.
	if !hasRuleMsg(checkXRefTableFormat(nil, PDFA2b, []byte("\nxref\n\n0 2\n"+entry)), "6.1.4") {
		t.Error("xref double-EOL not flagged")
	}
	// Double space in subsection header.
	if !hasRuleMsg(checkXRefTableFormat(nil, PDFA2b, []byte("\nxref\n0  2\n"+entry)), "6.1.4") {
		t.Error("subsection double space not flagged")
	}
}

func TestHexStringChecks(t *testing.T) {
	if hexStringEven([]byte("4845 4c4f")) != true {
		t.Error("even hex misjudged")
	}
	if hexStringEven([]byte("48455")) != false {
		t.Error("odd hex misjudged")
	}
	if hexStringDigitsOnly([]byte("48 4G")) != false {
		t.Error("non-hex not caught")
	}
	if hexStringDigitsOnly([]byte("48 4a\n")) != true {
		t.Error("valid hex misjudged")
	}
}

func TestScanHexStringsSkipsDicts(t *testing.T) {
	var found []string
	scanHexStrings([]byte("<< /A 1 >> <48455> (a<b>c)"), func(c []byte) {
		found = append(found, string(c))
	})
	if len(found) != 1 || found[0] != "48455" {
		t.Errorf("expected [48455], got %v", found)
	}
}

func TestStreamKeywordFormat(t *testing.T) {
	build := func(region string) []ValidationError {
		var errs []ValidationError
		checkOneStreamKeyword([]byte(region), 0, 1, nil, func(msg string, obj int) {
			errs = append(errs, ValidationError{Rule: "6.1.7.1", Message: msg})
		})
		return errs
	}
	if hasRuleMsg(build("1 0 obj\n<< >>\nstream\nDATA\nendstream\nendobj"), "6.1.7.1") {
		t.Error("valid stream flagged")
	}
	if !hasRuleMsg(build("1 0 obj\n<< >>\nstream\rDATA\nendstream\nendobj"), "6.1.7.1") {
		t.Error("bare CR after stream not flagged")
	}
	if !hasRuleMsg(build("1 0 obj\n<< >>\nstream \nDATA\nendstream\nendobj"), "6.1.7.1") {
		t.Error("extra space after stream not flagged")
	}
	if !hasRuleMsg(build("1 0 obj\n<< >>\nstream\nDATAendstream\nendobj"), "6.1.7.1") {
		t.Error("endstream not preceded by EOL not flagged")
	}
}

func TestInlineImageFilters(t *testing.T) {
	// LZW abbreviation.
	f := inlineImageFilters([]byte("BI /W 1 /H 1 /F /LZW ID xx EI"))
	if len(f) != 1 || f[0][0] != "LZW" {
		t.Errorf("LZW filter not extracted: %v", f)
	}
	// Array form.
	f = inlineImageFilters([]byte("BI /W 1 /F [/AHx /LZW] ID xx EI"))
	if len(f) != 1 || len(f[0]) != 2 || f[0][1] != "LZW" {
		t.Errorf("array filter not extracted: %v", f)
	}
	// Full Filter name.
	f = inlineImageFilters([]byte("BI /Filter /FlateDecode ID xx EI"))
	if len(f) != 1 || f[0][0] != "FlateDecode" {
		t.Errorf("full filter name not extracted: %v", f)
	}
}

func TestNameUTF8(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: Array{Name("Separation"), Name("Spot\xff\xfe"), Name("DeviceCMYK"), IndirectRef{Number: 9}}},
	}}
	if !hasRuleMsg(checkNameUTF8(doc, PDFA2b), "6.1.8") {
		t.Error("invalid-UTF8 Separation colorant not flagged")
	}
	doc2 := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: Array{Name("Separation"), Name("Spot"), Name("DeviceCMYK"), IndirectRef{Number: 9}}},
	}}
	if hasRuleMsg(checkNameUTF8(doc2, PDFA2b), "6.1.8") {
		t.Error("valid colorant flagged")
	}
	// PDF/A-1 has no UTF-8 name rule.
	if len(checkNameUTF8(doc, PDFA1b)) != 0 {
		t.Error("UTF-8 name rule must not apply at PDF/A-1")
	}
}

// TestLinearizedTrailerIDMismatch ensures a linearized file whose first-page
// and last trailer /ID differ is flagged, and a consistent one is not.
func TestLinearizedTrailerIDMismatch(t *testing.T) {
	mk := func(id1, id2 string) []byte {
		return []byte("%PDF-1.4\n<< /Linearized 1 >>\n" +
			"trailer\n<< /ID [<" + id1 + "> <AAAA>] >>\n" +
			"trailer\n<< /ID [<" + id2 + "> <BBBB>] >>\n")
	}
	if got := len(checkLinearizedTrailerID(mk("1111", "2222"), PDFA1b)); got == 0 {
		t.Error("mismatched linearized trailer IDs not flagged")
	}
	if got := len(checkLinearizedTrailerID(mk("1111", "1111"), PDFA1b)); got != 0 {
		t.Errorf("consistent linearized trailer IDs wrongly flagged: %d", got)
	}
	// Not linearized -> not this rule's concern.
	nonLin := []byte("%PDF-1.4\ntrailer\n<< /ID [<1111> <A>] >>\ntrailer\n<< /ID [<2222> <B>] >>\n")
	if got := len(checkLinearizedTrailerID(nonLin, PDFA1b)); got != 0 {
		t.Errorf("non-linearized file wrongly flagged: %d", got)
	}
}
