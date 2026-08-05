package pdf0

import (
	"bytes"
	"github.com/mgilbir/formalis"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfua"
	"strings"
	"testing"
)

// TestNewPDFADocumentLevelA is the C19 guard: the builder produces a document
// that passes its own validator at Level A (previously it built a part-4,
// conformance-less, untagged document that failed with several errors).
func TestNewPDFADocumentLevelA(t *testing.T) {
	for _, lvl := range []pdfa.Level{pdfa.PDFA1a, pdfa.PDFA2a, pdfa.PDFA3a} {
		doc := NewPDFADocument(lvl)
		if errs := ValidatePDFA(doc, lvl); len(errs) > 0 {
			t.Errorf("NewPDFADocument(%v) is not conformant: %d error(s)", lvl, len(errs))
			for _, e := range errs {
				t.Errorf("  %s", e)
			}
		}
	}
	// The b-levels are unaffected.
	for _, lvl := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		if errs := ValidatePDFA(NewPDFADocument(lvl), lvl); len(errs) > 0 {
			t.Errorf("NewPDFADocument(%v) regressed: %d error(s)", lvl, len(errs))
		}
	}
}

// TestUAPartParameterized is the C39 guard: the shared UA validator selects the
// part-specific requirements structurally (no message-text filtering), so the
// UA-1 entry point demands part 1 and a 1.x header on a UA-2 file, while the
// UA-2 entry point reports neither.
func TestUAPartParameterized(t *testing.T) {
	uaHas := func(v []pdfua.Violation, substr string) bool {
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
	doc := NewPDFADocument(pdfa.PDFA3b)
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
	doc := NewPDFADocument(pdfa.PDFA3b)
	if err := EmbedFacturX(doc, []byte(validCII), formalis.ProfileEN16931, "Invoice"); err != nil {
		t.Fatal(err)
	}
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	md := doc.Resolve(cat.Get("Metadata")).(*object.Stream)
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
