package pdf0

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// minimalXRechnungUBL is a small but complete, conforming XRechnung (UBL) invoice
// carrying every term XRechnung makes mandatory on top of EN 16931.
const minimalXRechnungUBL = `<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
	xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2">
<cbc:CustomizationID>urn:cen.eu:en16931:2017#compliant#urn:xeinkauf.de:kosit:xrechnung_3.0</cbc:CustomizationID>
<cbc:ID>INV-1</cbc:ID><cbc:IssueDate>2024-01-15</cbc:IssueDate>
<cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode><cbc:DocumentCurrencyCode>EUR</cbc:DocumentCurrencyCode>
<cbc:BuyerReference>04011000-12345-03</cbc:BuyerReference>
<cac:AccountingSupplierParty><cac:Party>
  <cac:PostalAddress><cbc:CityName>Berlin</cbc:CityName><cbc:PostalZone>10115</cbc:PostalZone>
    <cac:Country><cbc:IdentificationCode>DE</cbc:IdentificationCode></cac:Country></cac:PostalAddress>
  <cac:PartyTaxScheme><cbc:CompanyID>DE123456789</cbc:CompanyID><cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme></cac:PartyTaxScheme>
  <cac:PartyLegalEntity><cbc:RegistrationName>Seller Ltd</cbc:RegistrationName></cac:PartyLegalEntity>
  <cac:Contact><cbc:Name>Tim Tester</cbc:Name><cbc:Telephone>012 3456789</cbc:Telephone><cbc:ElectronicMail>tim@test.de</cbc:ElectronicMail></cac:Contact>
</cac:Party></cac:AccountingSupplierParty>
<cac:AccountingCustomerParty><cac:Party>
  <cac:PostalAddress><cbc:CityName>Bonn</cbc:CityName><cbc:PostalZone>53113</cbc:PostalZone>
    <cac:Country><cbc:IdentificationCode>DE</cbc:IdentificationCode></cac:Country></cac:PostalAddress>
  <cac:PartyLegalEntity><cbc:RegistrationName>Buyer Ltd</cbc:RegistrationName></cac:PartyLegalEntity>
</cac:Party></cac:AccountingCustomerParty>
<cac:PaymentMeans><cbc:PaymentMeansCode>58</cbc:PaymentMeansCode>
  <cac:PayeeFinancialAccount><cbc:ID>DE75512108001245126199</cbc:ID></cac:PayeeFinancialAccount></cac:PaymentMeans>
<cac:TaxTotal><cbc:TaxAmount>19.00</cbc:TaxAmount>
  <cac:TaxSubtotal><cbc:TaxableAmount>100.00</cbc:TaxableAmount><cbc:TaxAmount>19.00</cbc:TaxAmount>
    <cac:TaxCategory><cbc:ID>S</cbc:ID><cbc:Percent>19</cbc:Percent><cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme></cac:TaxCategory></cac:TaxSubtotal></cac:TaxTotal>
<cac:LegalMonetaryTotal><cbc:LineExtensionAmount>100.00</cbc:LineExtensionAmount>
  <cbc:TaxExclusiveAmount>100.00</cbc:TaxExclusiveAmount><cbc:TaxInclusiveAmount>119.00</cbc:TaxInclusiveAmount>
  <cbc:PayableAmount>119.00</cbc:PayableAmount></cac:LegalMonetaryTotal>
<cac:InvoiceLine><cbc:ID>1</cbc:ID><cbc:InvoicedQuantity unitCode="C62">1</cbc:InvoicedQuantity>
  <cbc:LineExtensionAmount>100.00</cbc:LineExtensionAmount>
  <cac:Item><cbc:Name>Widget</cbc:Name>
    <cac:ClassifiedTaxCategory><cbc:ID>S</cbc:ID><cbc:Percent>19</cbc:Percent><cac:TaxScheme><cbc:ID>VAT</cbc:ID></cac:TaxScheme></cac:ClassifiedTaxCategory></cac:Item>
  <cac:Price><cbc:PriceAmount>100.00</cbc:PriceAmount></cac:Price></cac:InvoiceLine>
</Invoice>`

func TestValidateXRechnungBaseline(t *testing.T) {
	if v := ValidateXRechnung([]byte(minimalXRechnungUBL)); len(v) != 0 {
		t.Fatalf("baseline XRechnung not clean: %d violations (first %s: %s)", len(v), v[0].Rule, v[0].Message)
	}
}

func TestValidateXRechnungRules(t *testing.T) {
	cases := []struct{ name, from, to, rule string }{
		{"no buyer reference (BR-DE-15)", "<cbc:BuyerReference>04011000-12345-03</cbc:BuyerReference>", "", "BR-DE-15"},
		{"no payment instructions (BR-DE-1)", `<cac:PaymentMeans><cbc:PaymentMeansCode>58</cbc:PaymentMeansCode>
  <cac:PayeeFinancialAccount><cbc:ID>DE75512108001245126199</cbc:ID></cac:PayeeFinancialAccount></cac:PaymentMeans>`, "", "BR-DE-1"},
		{"no seller city (BR-DE-3)", "<cbc:CityName>Berlin</cbc:CityName>", "", "BR-DE-3"},
		{"no seller contact (BR-DE-2)", "<cac:Contact><cbc:Name>Tim Tester</cbc:Name><cbc:Telephone>012 3456789</cbc:Telephone><cbc:ElectronicMail>tim@test.de</cbc:ElectronicMail></cac:Contact>", "", "BR-DE-2"},
		{"bad type code (BR-DE-17)", "<cbc:InvoiceTypeCode>380</cbc:InvoiceTypeCode>", "<cbc:InvoiceTypeCode>999</cbc:InvoiceTypeCode>", "BR-DE-17"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broken := strings.Replace(minimalXRechnungUBL, tc.from, tc.to, 1)
			if broken == minimalXRechnungUBL {
				t.Fatalf("mutation %q not found", tc.from)
			}
			v := ValidateXRechnung([]byte(broken))
			found := false
			for _, x := range v {
				if x.Rule == tc.rule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %s to fire; got %v", tc.rule, v)
			}
		})
	}
}

// TestValidateXRechnungCorpus is the FP=0 oracle: every KoSIT XRechnung test-suite
// instance (which is a conforming invoice) must validate with no violations. The
// oracle is not vendored; the test skips when it is absent (run `make cius-oracles`).
func TestValidateXRechnungCorpus(t *testing.T) {
	root := filepath.Join("testdata", "xrechnung", "testsuite", "src", "test")
	if _, err := os.Stat(root); err != nil {
		t.Skip("XRechnung test suite not present (make cius-oracles)")
	}
	isInvoice := regexp.MustCompile(`(?s)<([\w.]+:)?(CrossIndustryInvoice|Invoice|CreditNote)[\s>]`)
	var files, clean int
	filepath.Walk(root, func(p string, fi os.FileInfo, e error) error {
		if e != nil || !strings.HasSuffix(p, ".xml") {
			return nil
		}
		data, _ := os.ReadFile(p)
		if !isInvoice.Match(data) {
			return nil
		}
		files++
		if v := ValidateXRechnung(data); len(v) != 0 {
			t.Errorf("%s: conforming XRechnung reported %d violations (first: %s: %s)",
				filepath.Base(p), len(v), v[0].Rule, v[0].Message)
		} else {
			clean++
		}
		return nil
	})
	if files == 0 {
		t.Skip("no XRechnung instances found")
	}
	t.Logf("XRechnung corpus: %d/%d instances clean (FP=0)", clean, files)
}
