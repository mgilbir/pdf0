package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/formalis"
)

// TestNewPDFADocumentLevelA is the C19 guard: the builder produces a document
// that passes its own validator at Level A (previously it built a part-4,
// conformance-less, untagged document that failed with several errors).
func TestNewPDFADocumentLevelA(t *testing.T) {
	for _, lvl := range []PDFALevel{PDFA1a, PDFA2a, PDFA3a} {
		doc := NewPDFADocument(lvl)
		if errs := ValidatePDFA(doc, lvl); len(errs) > 0 {
			t.Errorf("NewPDFADocument(%v) is not conformant: %d error(s)", lvl, len(errs))
			for _, e := range errs {
				t.Errorf("  %s", e)
			}
		}
	}
	// The b-levels are unaffected.
	for _, lvl := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		if errs := ValidatePDFA(NewPDFADocument(lvl), lvl); len(errs) > 0 {
			t.Errorf("NewPDFADocument(%v) regressed: %d error(s)", lvl, len(errs))
		}
	}
}

// TestPDFAPartConformanceLevelA pins the per-level builder metadata.
func TestPDFAPartConformanceLevelA(t *testing.T) {
	cases := []struct {
		level PDFALevel
		part  int
		conf  string
		ver   string
	}{
		{PDFA1a, 1, "A", "1.4"},
		{PDFA2a, 2, "A", "1.7"},
		{PDFA3a, 3, "A", "1.7"},
		{PDFA1b, 1, "B", "1.4"},
		{PDFA4, 4, "", "2.0"},
	}
	for _, c := range cases {
		if got := pdfaPart(c.level); got != c.part {
			t.Errorf("pdfaPart(%v) = %d, want %d", c.level, got, c.part)
		}
		if got := pdfaConformance(c.level); got != c.conf {
			t.Errorf("pdfaConformance(%v) = %q, want %q", c.level, got, c.conf)
		}
		if got := pdfaVersion(c.level); got != c.ver {
			t.Errorf("pdfaVersion(%v) = %q, want %q", c.level, got, c.ver)
		}
	}
}

// TestCanonicalPrefixSingleQuote is the C33 guard: a single-quoted xmlns
// declaration binding a canonical extension-schema namespace to a wrong prefix
// is flagged, not evaded.
func TestCanonicalPrefixSingleQuote(t *testing.T) {
	// pick any canonical namespace/prefix
	var uri, want string
	for u, w := range canonicalXMPPrefixes {
		uri, want = u, w
		break
	}
	if uri == "" {
		t.Skip("no canonical prefixes configured")
	}

	// Bind the namespace to a deliberately wrong prefix using single quotes.
	xmp := "<x xmlns:WRONG='" + uri + "'></x>"
	errs := checkXMPExtensionContainer(xmp, nil, "6.6.2", PDFA2b)
	flagged := false
	for _, e := range errs {
		if strings.Contains(e.Message, uri) && strings.Contains(e.Message, want) {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("single-quoted xmlns binding %s to a wrong prefix was not flagged", uri)
	}
}

// TestUAHeadingsRoleMapResolved is the C29 guard: the heading-level rules key
// off the /RoleMap-resolved type, like the sibling heading checks, so a level
// skip through custom types (Titre1→H1, Titre3→H3) is caught.
func TestUAHeadingsRoleMapResolved(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	cat := headingDoc(doc, Array{heading(doc, 10, "Titre1"), heading(doc, 11, "Titre3")})
	roleMap := &Dictionary{}
	roleMap.Set("Titre1", Name("H1"))
	roleMap.Set("Titre3", Name("H3"))
	doc.ResolveDict(cat.Get("StructTreeRoot")).Set("RoleMap", roleMap)
	if !hasUAClause(checkUAHeadings(doc, cat), "7.4") {
		t.Error("role-mapped heading skip (Titre1=H1 followed by Titre3=H3) was not flagged")
	}
}

// TestUAPartParameterized is the C39 guard: the shared UA validator selects the
// part-specific requirements structurally (no message-text filtering), so the
// UA-1 entry point demands part 1 and a 1.x header on a UA-2 file, while the
// UA-2 entry point reports neither.
func TestUAPartParameterized(t *testing.T) {
	uaHas := func(v []UAViolation, substr string) bool {
		for _, e := range v {
			if strings.Contains(e.Message, substr) {
				return true
			}
		}
		return false
	}

	d := buildUA2Doc(t) // PDF 2.0, pdfuaid:part 2
	ua1 := ValidatePDFUA(d)
	if !uaHas(ua1, "pdfuaid:part must be 1 for PDF/UA-1, got 2") {
		t.Errorf("PDF/UA-1 validation of a part-2 file should demand part 1; got %v", ua1)
	}
	if !uaHas(ua1, "PDF 1.x header") {
		t.Errorf("PDF/UA-1 validation of a PDF 2.0 file should demand a 1.x header; got %v", ua1)
	}
	for _, e := range ValidatePDFUA2(d) {
		if strings.Contains(e.Message, "must be 1") || strings.Contains(e.Message, "PDF 1.x") {
			t.Errorf("UA-1-specific finding leaked into ValidatePDFUA2: %v", e)
		}
	}
}

// TestFacturXInvoiceRulesInline is the C44 guard: ValidateFacturX runs the
// EN 16931 rules on the embedded invoice inline, as ValidateOrderX does for
// orders — an invoice missing its number (BT-1) is reported by the container
// validator itself.
func TestFacturXInvoiceRulesInline(t *testing.T) {
	doc := NewPDFADocument(PDFA3b)
	bad := strings.Replace(validCII, "<ID>INV-1</ID>", "", 1)
	if err := EmbedFacturX(doc, []byte(bad), formalis.ProfileEN16931, "Invoice"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	res := ValidateFacturX(rt, buf.Bytes())
	found := false
	for _, v := range res.Violations {
		if v.Rule == "BR-02" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the missing invoice number to surface as BR-02; got %v", res.Violations)
	}
}

// TestFacturXRejectsOrderType is the other C44 guard: fx:DocumentType ORDER is
// an Order-X document, not a Factur-X invoice, and is now rejected with the
// message the check always printed.
func TestFacturXRejectsOrderType(t *testing.T) {
	doc := NewPDFADocument(PDFA3b)
	if err := EmbedFacturX(doc, []byte(validCII), formalis.ProfileEN16931, "Invoice"); err != nil {
		t.Fatal(err)
	}
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	md := doc.Resolve(cat.Get("Metadata")).(*Stream)
	md.Data = bytes.Replace(md.Data, []byte("<fx:DocumentType>INVOICE</fx:DocumentType>"), []byte("<fx:DocumentType>ORDER</fx:DocumentType>"), 1)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	res := ValidateFacturX(rt, buf.Bytes())
	found := false
	for _, v := range res.Violations {
		if v.Rule == "metadata" && strings.Contains(v.Message, `"ORDER" is not INVOICE`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fx:DocumentType ORDER to be rejected; got %v", res.Violations)
	}
}
