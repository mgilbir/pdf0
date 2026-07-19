package einvoice

import (
	"strings"
	"testing"
)

// The "Split payment" VAT category (B) is a valid UNCL 5305 code (BR-CL-17/18)
// used by domestic Italian invoices. These tests guard the code-list membership
// (no false positive for B) and the two category-B rules, BR-B-01 (must be a
// domestic Italian invoice) and BR-B-02 (mutually exclusive with "Standard
// rated", S). The official CEN unit-test suite ships no category-B fragments, so
// these are hand-built from the shared minimalUBL template.

// splitPaymentUBL turns the standard-rated minimalUBL into a domestic Italian
// split-payment invoice: countries DE->IT and every category S->B.
func splitPaymentUBL() string {
	s := strings.ReplaceAll(minimalUBL, "<IdentificationCode>DE</IdentificationCode>", "<IdentificationCode>IT</IdentificationCode>")
	s = strings.ReplaceAll(s, "<ID>S</ID>", "<ID>B</ID>")
	return s
}

func TestSplitPaymentCategoryValid(t *testing.T) {
	// A domestic Italian split-payment invoice is clean: category B must not be
	// reported as an invalid VAT category (BR-CL-17/BR-CL-18), and neither B rule
	// should fire.
	v := Validate([]byte(splitPaymentUBL()), ProfileEN16931)
	for _, bad := range []string{"BR-CL-17", "BR-CL-18", "BR-B-01", "BR-B-02"} {
		if hasFacturXRule(v, bad) {
			t.Errorf("a valid domestic split-payment invoice should not report %s; got %v", bad, v)
		}
	}
	if len(v) != 0 {
		t.Errorf("expected a clean split-payment invoice, got %d violations (first %s: %s)", len(v), v[0].Rule, v[0].Message)
	}
}

func TestSplitPaymentNotDomesticItalian(t *testing.T) {
	// Category B with a non-IT country code (the template's DE seller/buyer) must
	// trigger BR-B-01, but still not report B as an invalid category.
	s := strings.ReplaceAll(minimalUBL, "<ID>S</ID>", "<ID>B</ID>")
	v := Validate([]byte(s), ProfileEN16931)
	if !hasFacturXRule(v, "BR-B-01") {
		t.Errorf("a non-Italian split-payment invoice should trigger BR-B-01; got %v", v)
	}
	if hasFacturXRule(v, "BR-CL-17") || hasFacturXRule(v, "BR-CL-18") {
		t.Errorf("category B must be a valid VAT category even when BR-B-01 fires; got %v", v)
	}
}

func TestSplitPaymentExclusiveWithStandard(t *testing.T) {
	// A domestic Italian invoice whose line is "Split payment" (B) while its VAT
	// breakdown is "Standard rated" (S) violates BR-B-02.
	s := strings.ReplaceAll(minimalUBL, "<IdentificationCode>DE</IdentificationCode>", "<IdentificationCode>IT</IdentificationCode>")
	s = strings.Replace(s, "<ClassifiedTaxCategory><ID>S</ID>", "<ClassifiedTaxCategory><ID>B</ID>", 1)
	v := Validate([]byte(s), ProfileEN16931)
	if !hasFacturXRule(v, "BR-B-02") {
		t.Errorf("mixing Split payment (B) and Standard rated (S) should trigger BR-B-02; got %v", v)
	}
}
