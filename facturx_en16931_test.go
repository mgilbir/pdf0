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
      <AssociatedDocumentLineDocument><LineID>1</LineID></AssociatedDocumentLineDocument>
      <SpecifiedTradeProduct><Name>Widget</Name></SpecifiedTradeProduct>
      <SpecifiedLineTradeAgreement><NetPriceProductTradePrice><ChargeAmount>100.00</ChargeAmount></NetPriceProductTradePrice></SpecifiedLineTradeAgreement>
      <SpecifiedLineTradeDelivery><BilledQuantity unitCode="C62">1</BilledQuantity></SpecifiedLineTradeDelivery>
      <SpecifiedLineTradeSettlement><ApplicableTradeTax><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent></ApplicableTradeTax><SpecifiedTradeSettlementLineMonetarySummation><LineTotalAmount>100.00</LineTotalAmount></SpecifiedTradeSettlementLineMonetarySummation></SpecifiedLineTradeSettlement>
    </IncludedSupplyChainTradeLineItem>
    <ApplicableHeaderTradeAgreement>
      <SellerTradeParty><Name>Seller Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></SellerTradeParty>
      <BuyerTradeParty><Name>Buyer Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></BuyerTradeParty>
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

// withAllowanceCharge injects a document-level allowance or charge into validCII
// (whose amount is zero, keeping the invoice totals consistent).
func withAllowanceCharge(ac string) string {
	return strings.Replace(validCII, "<InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>",
		"<InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>"+ac, 1)
}

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
		{"not CII or UBL", `<Foo/>`, "syntax"},
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
		{"vat breakdown no category", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode>", "<BasisAmount>100.00</BasisAmount>", 1), "BR-47"},
		{"vat breakdown no rate", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent>", "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode>", 1), "BR-48"},
		{"vat calc wrong", strings.Replace(validCII, "<CalculatedAmount>20.00</CalculatedAmount>", "<CalculatedAmount>99.00</CalculatedAmount>", 1), "BR-CO-17"},
		{"vat total mismatch", strings.Replace(validCII, "<TaxTotalAmount>20.00</TaxTotalAmount>", "<TaxTotalAmount>25.00</TaxTotalAmount>", 1), "BR-CO-14"},
		{"line sum mismatch", strings.Replace(validCII, "<LineTotalAmount>100.00</LineTotalAmount>\n        <TaxBasisTotalAmount>", "<LineTotalAmount>77.00</LineTotalAmount>\n        <TaxBasisTotalAmount>", 1), "BR-CO-13"},
		{"zero rated with tax", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode>", "<BasisAmount>100.00</BasisAmount><CategoryCode>Z</CategoryCode>", 1), "BR-Z-09"},
		{"standard with exemption reason", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode>", "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode><ExemptionReason>oops</ExemptionReason>", 1), "BR-S-10"},
		{"exempt without reason", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode>", "<BasisAmount>100.00</BasisAmount><CategoryCode>E</CategoryCode>", 1), "BR-E-10"},
		{"line no identifier", strings.Replace(validCII, "<AssociatedDocumentLineDocument><LineID>1</LineID></AssociatedDocumentLineDocument>", "", 1), "BR-21"},
		{"line no item name", strings.Replace(validCII, "<SpecifiedTradeProduct><Name>Widget</Name></SpecifiedTradeProduct>", "", 1), "BR-25"},
		{"line no unit code", strings.Replace(validCII, `<BilledQuantity unitCode="C62">1</BilledQuantity>`, "<BilledQuantity>1</BilledQuantity>", 1), "BR-23"},
		{"line negative price", strings.Replace(validCII, "<ChargeAmount>100.00</ChargeAmount>", "<ChargeAmount>-5.00</ChargeAmount>", 1), "BR-27"},
		{"allowance no amount", withAllowanceCharge(`<SpecifiedTradeAllowanceCharge><ChargeIndicator><Indicator>false</Indicator></ChargeIndicator><CategoryTradeTax><CategoryCode>S</CategoryCode></CategoryTradeTax><Reason>r</Reason></SpecifiedTradeAllowanceCharge>`), "BR-31"},
		{"allowance no reason", withAllowanceCharge(`<SpecifiedTradeAllowanceCharge><ChargeIndicator><Indicator>false</Indicator></ChargeIndicator><ActualAmount>0.00</ActualAmount><CategoryTradeTax><CategoryCode>S</CategoryCode></CategoryTradeTax></SpecifiedTradeAllowanceCharge>`), "BR-33"},
		{"charge no category", withAllowanceCharge(`<SpecifiedTradeAllowanceCharge><ChargeIndicator><Indicator>true</Indicator></ChargeIndicator><ActualAmount>0.00</ActualAmount><Reason>r</Reason></SpecifiedTradeAllowanceCharge>`), "BR-37"},
		{"bad currency code", strings.Replace(validCII, "<InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>", "<InvoiceCurrencyCode>EU</InvoiceCurrencyCode>", 1), "BR-CL-04"},
		{"bad type code", strings.Replace(validCII, "<TypeCode>380</TypeCode>", "<TypeCode>999</TypeCode>", 1), "BR-CL-01"},
		{"bad country code", strings.Replace(validCII, "<CountryID>FR</CountryID>", "<CountryID>F</CountryID>", 1), "BR-CL-14"},
		{"bad vat category", strings.Replace(validCII, "<BasisAmount>100.00</BasisAmount><CategoryCode>S</CategoryCode>", "<BasisAmount>100.00</BasisAmount><CategoryCode>X</CategoryCode>", 1), "BR-CL-17"},
		{"bad unit code", strings.Replace(validCII, `<BilledQuantity unitCode="C62">1</BilledQuantity>`, `<BilledQuantity unitCode="ZZZ">1</BilledQuantity>`, 1), "BR-CL-23"},
		{"too many decimals", strings.Replace(validCII, "<GrandTotalAmount>120.00</GrandTotalAmount>", "<GrandTotalAmount>120.001</GrandTotalAmount>", 1), "BR-DEC-14"},
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

// subLineCII is an EXTENDED-style invoice with two sub-invoice lines (11, 12)
// under a grouping line (1). The grouping line rolls up its children (60+40=100)
// and carries no quantity/price; the summation line total is the top-level sum.
const subLineCII = `<CrossIndustryInvoice>
  <ExchangedDocumentContext><GuidelineSpecifiedDocumentContextParameter><ID>urn:factur-x.eu:1p0:extended</ID></GuidelineSpecifiedDocumentContextParameter></ExchangedDocumentContext>
  <ExchangedDocument><ID>INV-2</ID><TypeCode>380</TypeCode><IssueDateTime><DateTimeString>20240101</DateTimeString></IssueDateTime></ExchangedDocument>
  <SupplyChainTradeTransaction>
    <IncludedSupplyChainTradeLineItem>
      <AssociatedDocumentLineDocument><LineID>1</LineID></AssociatedDocumentLineDocument>
      <SpecifiedTradeProduct><Name>Group</Name></SpecifiedTradeProduct>
      <SpecifiedLineTradeSettlement><SpecifiedTradeSettlementLineMonetarySummation><LineTotalAmount>100.00</LineTotalAmount></SpecifiedTradeSettlementLineMonetarySummation></SpecifiedLineTradeSettlement>
    </IncludedSupplyChainTradeLineItem>
    <IncludedSupplyChainTradeLineItem>
      <AssociatedDocumentLineDocument><LineID>11</LineID><ParentLineID>1</ParentLineID></AssociatedDocumentLineDocument>
      <SpecifiedTradeProduct><Name>Child A</Name></SpecifiedTradeProduct>
      <SpecifiedLineTradeAgreement><NetPriceProductTradePrice><ChargeAmount>60.00</ChargeAmount></NetPriceProductTradePrice></SpecifiedLineTradeAgreement>
      <SpecifiedLineTradeDelivery><BilledQuantity unitCode="C62">1</BilledQuantity></SpecifiedLineTradeDelivery>
      <SpecifiedLineTradeSettlement><ApplicableTradeTax><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent></ApplicableTradeTax><SpecifiedTradeSettlementLineMonetarySummation><LineTotalAmount>60.00</LineTotalAmount></SpecifiedTradeSettlementLineMonetarySummation></SpecifiedLineTradeSettlement>
    </IncludedSupplyChainTradeLineItem>
    <IncludedSupplyChainTradeLineItem>
      <AssociatedDocumentLineDocument><LineID>12</LineID><ParentLineID>1</ParentLineID></AssociatedDocumentLineDocument>
      <SpecifiedTradeProduct><Name>Child B</Name></SpecifiedTradeProduct>
      <SpecifiedLineTradeAgreement><NetPriceProductTradePrice><ChargeAmount>40.00</ChargeAmount></NetPriceProductTradePrice></SpecifiedLineTradeAgreement>
      <SpecifiedLineTradeDelivery><BilledQuantity unitCode="C62">1</BilledQuantity></SpecifiedLineTradeDelivery>
      <SpecifiedLineTradeSettlement><ApplicableTradeTax><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent></ApplicableTradeTax><SpecifiedTradeSettlementLineMonetarySummation><LineTotalAmount>40.00</LineTotalAmount></SpecifiedTradeSettlementLineMonetarySummation></SpecifiedLineTradeSettlement>
    </IncludedSupplyChainTradeLineItem>
    <ApplicableHeaderTradeAgreement>
      <SellerTradeParty><Name>Seller Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></SellerTradeParty>
      <BuyerTradeParty><Name>Buyer Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></BuyerTradeParty>
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

func TestValidateFacturXSubLines(t *testing.T) {
	if v := ValidateFacturXInvoice([]byte(subLineCII), FacturXExtended); len(v) != 0 {
		t.Fatalf("valid sub-invoice-line document reported %d violation(s): %v", len(v), v)
	}
	// Breaking a child amount so the top-level rollup no longer matches must fire.
	bad := strings.Replace(subLineCII, "<LineTotalAmount>100.00</LineTotalAmount>\n        <TaxBasisTotalAmount>", "<LineTotalAmount>90.00</LineTotalAmount>\n        <TaxBasisTotalAmount>", 1)
	found := false
	for _, e := range ValidateFacturXInvoice([]byte(bad), FacturXExtended) {
		if e.Rule == "BR-CO-10" {
			found = true
		}
	}
	if !found {
		t.Error("expected BR-CO-10 when the top-level line total is inconsistent")
	}
}
