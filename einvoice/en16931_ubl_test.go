package einvoice

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateUBLCorpus is the FP=0 oracle for the UBL syntax: every conforming
// EN 16931 UBL example (CEN TC 434 and Peppol BIS) must validate with no core
// business-rule violations. The corpus is not vendored; the test skips when
// testdata/en16931-ubl is absent (run `make en16931-ubl`).
func TestValidateUBLCorpus(t *testing.T) {
	files, _ := filepath.Glob("../testdata/en16931-ubl/*.xml")
	if len(files) == 0 {
		t.Skip("EN 16931 UBL corpus not present (make en16931-ubl)")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		v := Validate(data, ProfileEN16931)
		if len(v) != 0 {
			t.Errorf("%s: expected 0 violations on a conforming UBL invoice, got %d (first: %s: %s)",
				filepath.Base(f), len(v), v[0].Rule, v[0].Message)
		}
	}
}

// TestValidateUBLDetectsSyntax checks both UBL document types and the rejection
// of a non-invoice root.
func TestValidateUBLDetectsSyntax(t *testing.T) {
	inv, err := parseEN16931([]byte(`<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"/>`))
	if err != nil || inv == nil {
		t.Fatalf("UBL Invoice root not recognised: %v", err)
	}
	cn, err := parseEN16931([]byte(`<CreditNote xmlns="urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"/>`))
	if err != nil || cn == nil {
		t.Fatalf("UBL CreditNote root not recognised: %v", err)
	}
	if _, err := parseEN16931([]byte(`<PurchaseOrder/>`)); err == nil {
		t.Error("a non-invoice root must be rejected")
	}
}

// minimalUBL is a small but complete, conforming EN 16931 UBL invoice used to
// verify the rules fire when a mandatory term is removed.
const minimalUBL = `<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2">
<CustomizationID>urn:cen.eu:en16931:2017</CustomizationID>
<ID>INV-1</ID><IssueDate>2024-01-15</IssueDate>
<InvoiceTypeCode>380</InvoiceTypeCode><DocumentCurrencyCode>EUR</DocumentCurrencyCode>
<AccountingSupplierParty><Party>
  <PostalAddress><Country><IdentificationCode>DE</IdentificationCode></Country></PostalAddress>
  <PartyTaxScheme><CompanyID>DE123456789</CompanyID><TaxScheme><ID>VAT</ID></TaxScheme></PartyTaxScheme>
  <PartyLegalEntity><RegistrationName>Seller Ltd</RegistrationName></PartyLegalEntity>
</Party></AccountingSupplierParty>
<AccountingCustomerParty><Party>
  <PostalAddress><Country><IdentificationCode>DE</IdentificationCode></Country></PostalAddress>
  <PartyLegalEntity><RegistrationName>Buyer Ltd</RegistrationName></PartyLegalEntity>
</Party></AccountingCustomerParty>
<TaxTotal><TaxAmount>19.00</TaxAmount>
  <TaxSubtotal><TaxableAmount>100.00</TaxableAmount><TaxAmount>19.00</TaxAmount>
    <TaxCategory><ID>S</ID><Percent>19</Percent></TaxCategory></TaxSubtotal>
</TaxTotal>
<LegalMonetaryTotal><LineExtensionAmount>100.00</LineExtensionAmount>
  <TaxExclusiveAmount>100.00</TaxExclusiveAmount><TaxInclusiveAmount>119.00</TaxInclusiveAmount>
  <PayableAmount>119.00</PayableAmount></LegalMonetaryTotal>
<InvoiceLine><ID>1</ID><InvoicedQuantity unitCode="C62">1</InvoicedQuantity>
  <LineExtensionAmount>100.00</LineExtensionAmount>
  <Item><Name>Widget</Name><ClassifiedTaxCategory><ID>S</ID><Percent>19</Percent></ClassifiedTaxCategory></Item>
  <Price><PriceAmount>100.00</PriceAmount></Price></InvoiceLine>
</Invoice>`

func TestValidateUBLMutations(t *testing.T) {
	// Baseline: the minimal invoice is clean.
	if v := Validate([]byte(minimalUBL), ProfileEN16931); len(v) != 0 {
		t.Fatalf("baseline UBL not clean: %d violations (first %s: %s)", len(v), v[0].Rule, v[0].Message)
	}
	cases := []struct {
		name, remove, wantRule string
	}{
		{"no currency (BR-05)", "<DocumentCurrencyCode>EUR</DocumentCurrencyCode>", "BR-05"},
		{"no invoice number (BR-02)", "<ID>INV-1</ID>", "BR-02"},
		{"no seller name (BR-06)", "<RegistrationName>Seller Ltd</RegistrationName>", "BR-06"},
		{"no seller country (BR-09)", "<IdentificationCode>DE</IdentificationCode>", "BR-09"},
		{"no line item name (BR-25)", "<Name>Widget</Name>", "BR-25"},
		{"no grand total (BR-14)", "<TaxInclusiveAmount>119.00</TaxInclusiveAmount>", "BR-14"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broken := strings.Replace(minimalUBL, tc.remove, "", 1)
			if broken == minimalUBL {
				t.Fatalf("mutation string %q not found", tc.remove)
			}
			v := Validate([]byte(broken), ProfileEN16931)
			found := false
			for _, x := range v {
				if x.Rule == tc.wantRule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %s to fire; got %v", tc.wantRule, v)
			}
		})
	}
}

// TestVATAmountTolerance pins the EN 16931 ±1 tolerance of the VAT-breakdown
// amount check (BR-CO-17): per-line rounding drift within one currency unit is
// accepted, a larger drift is flagged.
func TestVATAmountTolerance(t *testing.T) {
	// Exact tax is 100.00 * 19% = 19.00.
	within := strings.Replace(minimalUBL,
		"<TaxableAmount>100.00</TaxableAmount><TaxAmount>19.00</TaxAmount>",
		"<TaxableAmount>100.00</TaxableAmount><TaxAmount>19.60</TaxAmount>", 1)
	if hasFacturXRule(Validate([]byte(within), ProfileEN16931), "BR-CO-17") {
		t.Error("BR-CO-17 must not fire for a 0.60 rounding drift (within the ±1 tolerance)")
	}
	beyond := strings.Replace(minimalUBL,
		"<TaxableAmount>100.00</TaxableAmount><TaxAmount>19.00</TaxAmount>",
		"<TaxableAmount>100.00</TaxableAmount><TaxAmount>21.00</TaxAmount>", 1)
	if !hasFacturXRule(Validate([]byte(beyond), ProfileEN16931), "BR-CO-17") {
		t.Error("BR-CO-17 should fire for a 2.00 drift (beyond the ±1 tolerance)")
	}
}

// TestBindingRuleIDsPerSyntax verifies that a binding-specific rule is reported
// with the identifier of the invoice's own syntax: the same defect (inconsistent
// payment means codes) is CII-SR-467 on a CII invoice and UBL-SR-47 on a UBL one,
// and neither reports the other syntax's identifier.
func TestBindingRuleIDsPerSyntax(t *testing.T) {
	cii := strings.Replace(validCII, "<InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>",
		"<InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>"+
			"<SpecifiedTradeSettlementPaymentMeans><TypeCode>30</TypeCode></SpecifiedTradeSettlementPaymentMeans>"+
			"<SpecifiedTradeSettlementPaymentMeans><TypeCode>58</TypeCode></SpecifiedTradeSettlementPaymentMeans>", 1)
	v := Validate([]byte(cii), ProfileEN16931)
	if !hasFacturXRule(v, "CII-SR-467") {
		t.Errorf("CII invoice should report CII-SR-467; got %v", v)
	}
	if hasFacturXRule(v, "UBL-SR-47") {
		t.Error("CII invoice must not report the UBL identifier UBL-SR-47")
	}

	ubl := strings.Replace(minimalUBL, "</Invoice>",
		"<PaymentMeans><PaymentMeansCode>30</PaymentMeansCode></PaymentMeans>"+
			"<PaymentMeans><PaymentMeansCode>58</PaymentMeansCode></PaymentMeans></Invoice>", 1)
	v = Validate([]byte(ubl), ProfileEN16931)
	if !hasFacturXRule(v, "UBL-SR-47") {
		t.Errorf("UBL invoice should report UBL-SR-47; got %v", v)
	}
	if hasFacturXRule(v, "CII-SR-467") {
		t.Error("UBL invoice must not report the CII identifier CII-SR-467")
	}
}

// TestValidateUBLCalcMutation confirms a total-consistency rule fires when a
// document total is made inconsistent.
func TestValidateUBLCalcMutation(t *testing.T) {
	broken := bytes.Replace([]byte(minimalUBL),
		[]byte("<TaxInclusiveAmount>119.00</TaxInclusiveAmount>"),
		[]byte("<TaxInclusiveAmount>999.00</TaxInclusiveAmount>"), 1)
	v := Validate(broken, ProfileEN16931)
	found := false
	for _, x := range v {
		if x.Rule == "BR-CO-15" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected BR-CO-15 (total with VAT = without + VAT) to fire; got %v", v)
	}
}
