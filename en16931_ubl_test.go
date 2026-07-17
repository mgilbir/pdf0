package pdf0

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
	files, _ := filepath.Glob("testdata/en16931-ubl/*.xml")
	if len(files) == 0 {
		t.Skip("EN 16931 UBL corpus not present (make en16931-ubl)")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		v := ValidateFacturXInvoice(data, FacturXEN16931)
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
  <PartyLegalEntity><RegistrationName>Seller Ltd</RegistrationName></PartyLegalEntity>
</Party></AccountingSupplierParty>
<AccountingCustomerParty><Party>
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
  <Item><Name>Widget</Name></Item><Price><PriceAmount>100.00</PriceAmount></Price></InvoiceLine>
</Invoice>`

func TestValidateUBLMutations(t *testing.T) {
	// Baseline: the minimal invoice is clean.
	if v := ValidateFacturXInvoice([]byte(minimalUBL), FacturXEN16931); len(v) != 0 {
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
			v := ValidateFacturXInvoice([]byte(broken), FacturXEN16931)
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

// TestValidateUBLCalcMutation confirms a total-consistency rule fires when a
// document total is made inconsistent.
func TestValidateUBLCalcMutation(t *testing.T) {
	broken := bytes.Replace([]byte(minimalUBL),
		[]byte("<TaxInclusiveAmount>119.00</TaxInclusiveAmount>"),
		[]byte("<TaxInclusiveAmount>999.00</TaxInclusiveAmount>"), 1)
	v := ValidateFacturXInvoice(broken, FacturXEN16931)
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
