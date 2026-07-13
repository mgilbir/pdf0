package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// validCII is a minimal EN 16931 invoice with every foundational business term
// present and consistent totals (100 + 20 = 120). It is written without
// namespace prefixes, which the local-name parser reads directly.
const validCII = `<CrossIndustryInvoice>
  <ExchangedDocumentContext><GuidelineSpecifiedDocumentContextParameter><ID>urn:cen.eu:en16931:2017</ID></GuidelineSpecifiedDocumentContextParameter></ExchangedDocumentContext>
  <ExchangedDocument><ID>INV-1</ID><TypeCode>380</TypeCode><IssueDateTime><DateTimeString>20240101</DateTimeString></IssueDateTime></ExchangedDocument>
  <SupplyChainTradeTransaction>
    <IncludedSupplyChainTradeLineItem>
      <SpecifiedLineTradeSettlement><SpecifiedTradeSettlementLineMonetarySummation><LineTotalAmount>100.00</LineTotalAmount></SpecifiedTradeSettlementLineMonetarySummation></SpecifiedLineTradeSettlement>
    </IncludedSupplyChainTradeLineItem>
    <ApplicableHeaderTradeAgreement>
      <SellerTradeParty><Name>Seller Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></SellerTradeParty>
      <BuyerTradeParty><Name>Buyer Co</Name></BuyerTradeParty>
    </ApplicableHeaderTradeAgreement>
    <ApplicableHeaderTradeSettlement>
      <InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>
      <ApplicableTradeTax><CalculatedAmount>20.00</CalculatedAmount><BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent></ApplicableTradeTax>
      <SpecifiedTradeSettlementHeaderMonetarySummation>
        <LineTotalAmount>100.00</LineTotalAmount>
        <TaxBasisTotalAmount>100.00</TaxBasisTotalAmount>
        <TaxTotalAmount>20.00</TaxTotalAmount>
        <GrandTotalAmount>120.00</GrandTotalAmount>
        <DuePayableAmount>120.00</DuePayableAmount>
      </SpecifiedTradeSettlementHeaderMonetarySummation>
    </ApplicableHeaderTradeSettlement>
  </SupplyChainTradeTransaction>
</CrossIndustryInvoice>`

func TestValidateFacturXInvoiceValid(t *testing.T) {
	if v := ValidateFacturXInvoice([]byte(validCII), FacturXEN16931); len(v) != 0 {
		t.Fatalf("valid CII reported %d violation(s): %v", len(v), v)
	}
}

func TestValidateFacturXInvoiceViolations(t *testing.T) {
	cases := []struct {
		name    string
		xml     string
		rule    string
	}{
		{"not CII", `<Foo/>`, "cii"},
		{"missing spec id", strings.Replace(validCII, "<ID>urn:cen.eu:en16931:2017</ID>", "", 1), "BR-01"},
		{"missing invoice number", strings.Replace(validCII, "<ID>INV-1</ID>", "", 1), "BR-02"},
		{"missing issue date", strings.Replace(validCII, "<IssueDateTime><DateTimeString>20240101</DateTimeString></IssueDateTime>", "", 1), "BR-03"},
		{"missing type code", strings.Replace(validCII, "<TypeCode>380</TypeCode>", "", 1), "BR-04"},
		{"missing currency", strings.Replace(validCII, "<InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>", "", 1), "BR-05"},
		{"missing seller name", strings.Replace(validCII, "<Name>Seller Co</Name>", "", 1), "BR-06"},
		{"missing buyer name", strings.Replace(validCII, "<Name>Buyer Co</Name>", "", 1), "BR-07"},
		{"missing seller country", strings.Replace(validCII, "<CountryID>FR</CountryID>", "", 1), "BR-09"},
		{"missing grand total", strings.Replace(validCII, "<GrandTotalAmount>120.00</GrandTotalAmount>", "", 1), "BR-14"},
		{"missing due amount", strings.Replace(validCII, "<DuePayableAmount>120.00</DuePayableAmount>", "", 1), "BR-15"},
		{"totals inconsistent", strings.Replace(validCII, "<GrandTotalAmount>120.00</GrandTotalAmount>", "<GrandTotalAmount>999.00</GrandTotalAmount>", 1), "BR-CO-15"},
		{"vat breakdown no taxable", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount>", "", 1), "BR-45"},
		{"vat breakdown no tax", strings.Replace(validCII, "<CalculatedAmount>20.00</CalculatedAmount>", "", 1), "BR-46"},
		{"vat breakdown no category", strings.Replace(validCII, "<CategoryCode>S</CategoryCode>", "", 1), "BR-47"},
		{"vat breakdown no rate", strings.Replace(validCII, "<RateApplicablePercent>20.00</RateApplicablePercent>", "", 1), "BR-48"},
		{"vat calc wrong", strings.Replace(validCII, "<CalculatedAmount>20.00</CalculatedAmount>", "<CalculatedAmount>99.00</CalculatedAmount>", 1), "BR-CO-17"},
		{"vat total mismatch", strings.Replace(validCII, "<TaxTotalAmount>20.00</TaxTotalAmount>", "<TaxTotalAmount>25.00</TaxTotalAmount>", 1), "BR-CO-14"},
		{"line sum mismatch", strings.Replace(validCII, "<LineTotalAmount>100.00</LineTotalAmount>\n        <TaxBasisTotalAmount>", "<LineTotalAmount>77.00</LineTotalAmount>\n        <TaxBasisTotalAmount>", 1), "BR-CO-13"},
		{"zero rated with tax", strings.Replace(validCII, "<CategoryCode>S</CategoryCode>", "<CategoryCode>Z</CategoryCode>", 1), "BR-Z-09"},
		{"standard with exemption reason", strings.Replace(validCII, "<CategoryCode>S</CategoryCode>", "<CategoryCode>S</CategoryCode><ExemptionReason>oops</ExemptionReason>", 1), "BR-S-10"},
		{"exempt without reason", strings.Replace(validCII, "<CategoryCode>S</CategoryCode>", "<CategoryCode>E</CategoryCode>", 1), "BR-E-10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ValidateFacturXInvoice([]byte(tc.xml), FacturXEN16931)
			found := false
			for _, e := range v {
				if e.Rule == tc.rule {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected rule %s; got %v", tc.rule, v)
			}
		})
	}
}

// TestValidateFacturXInvoiceCorpus is the FP=0 oracle: the invoice XML of every
// conforming Factur-X / ZUGFeRD sample must pass the foundational EN 16931 rules.
// Skips when the corpus is absent.
func TestValidateFacturXInvoiceCorpus(t *testing.T) {
	files, _ := filepath.Glob("testdata/facturx/*.pdf")
	if len(files) == 0 {
		t.Skip("Factur-X corpus not present")
	}
	sort.Strings(files)
	for _, f := range files {
		name := filepath.Base(f)
		if strings.HasPrefix(name, "FAIL") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			continue
		}
		res := ValidateFacturX(doc, data)
		if len(res.XML) == 0 {
			continue
		}
		if v := ValidateFacturXInvoice(res.XML, res.Profile); len(v) != 0 {
			t.Errorf("%s [%s]: expected 0 EN 16931 violations, got %d (first: %s: %s)",
				name, res.Profile, len(v), v[0].Rule, v[0].Message)
		}
	}
}
