package pdf0

import (
	"bytes"
	"github.com/mgilbir/formalis"
	"strings"
	"testing"
)

// validCII is a minimal EN 16931-conforming Cross Industry Invoice (every
// foundational business term present, consistent 100 + 20 = 120 totals). Since
// ValidateFacturX runs the EN 16931 rules inline (audit C44), the round-trip
// test needs a payload that actually passes them, not just container filler.
const validCII = `<CrossIndustryInvoice>
  <ExchangedDocumentContext><GuidelineSpecifiedDocumentContextParameter><ID>urn:cen.eu:en16931:2017</ID></GuidelineSpecifiedDocumentContextParameter></ExchangedDocumentContext>
  <ExchangedDocument><ID>INV-1</ID><TypeCode>380</TypeCode><IssueDateTime><DateTimeString format="102">20240101</DateTimeString></IssueDateTime></ExchangedDocument>
  <SupplyChainTradeTransaction>
    <IncludedSupplyChainTradeLineItem>
      <AssociatedDocumentLineDocument><LineID>1</LineID></AssociatedDocumentLineDocument>
      <SpecifiedTradeProduct><Name>Widget</Name></SpecifiedTradeProduct>
      <SpecifiedLineTradeAgreement><NetPriceProductTradePrice><ChargeAmount>100.00</ChargeAmount></NetPriceProductTradePrice></SpecifiedLineTradeAgreement>
      <SpecifiedLineTradeDelivery><BilledQuantity unitCode="C62">1</BilledQuantity></SpecifiedLineTradeDelivery>
      <SpecifiedLineTradeSettlement><ApplicableTradeTax><TypeCode>VAT</TypeCode><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent></ApplicableTradeTax><SpecifiedTradeSettlementLineMonetarySummation><LineTotalAmount>100.00</LineTotalAmount></SpecifiedTradeSettlementLineMonetarySummation></SpecifiedLineTradeSettlement>
    </IncludedSupplyChainTradeLineItem>
    <ApplicableHeaderTradeAgreement>
      <SellerTradeParty><Name>Seller Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress><SpecifiedTaxRegistration><ID schemeID="VA">FR12345678</ID></SpecifiedTaxRegistration></SellerTradeParty>
      <BuyerTradeParty><Name>Buyer Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></BuyerTradeParty>
    </ApplicableHeaderTradeAgreement>
    <ApplicableHeaderTradeSettlement>
      <InvoiceCurrencyCode>EUR</InvoiceCurrencyCode>
      <ApplicableTradeTax><CalculatedAmount>20.00</CalculatedAmount><BasisAmount>100.00</BasisAmount><TypeCode>VAT</TypeCode><CategoryCode>S</CategoryCode><RateApplicablePercent>20.00</RateApplicablePercent></ApplicableTradeTax>
      <SpecifiedTradeSettlementHeaderMonetarySummation>
        <LineTotalAmount>100.00</LineTotalAmount>
        <TaxBasisTotalAmount>100.00</TaxBasisTotalAmount>
        <TaxTotalAmount currencyID="EUR">20.00</TaxTotalAmount>
        <GrandTotalAmount>120.00</GrandTotalAmount>
        <DuePayableAmount>120.00</DuePayableAmount>
      </SpecifiedTradeSettlementHeaderMonetarySummation>
    </ApplicableHeaderTradeSettlement>
  </SupplyChainTradeTransaction>
</CrossIndustryInvoice>`

// ciiForProfile returns validCII carrying the specification identifier (BT-24)
// of the given Factur-X tier.
//
// Each tier names itself in ram:GuidelineSpecifiedDocumentContextParameter/ram:ID,
// and Factur-X's own data model checks the value against that tier's code list —
// so a fixture that hard-codes one identifier is only conformant for one profile.
// It went unnoticed until formalis v0.3.0 began evaluating the Factur-X rule set
// rather than CEN's alone.
func ciiForProfile(p formalis.Profile) string {
	id := map[formalis.Profile]string{
		formalis.ProfileMinimum:  "urn:factur-x.eu:1p0:minimum",
		formalis.ProfileBasicWL:  "urn:factur-x.eu:1p0:basicwl",
		formalis.ProfileBasic:    "urn:cen.eu:en16931:2017#compliant#urn:factur-x.eu:1p0:basic",
		formalis.ProfileEN16931:  "urn:cen.eu:en16931:2017",
		formalis.ProfileExtended: "urn:cen.eu:en16931:2017#conformant#urn:factur-x.eu:1p0:extended",
	}[p]
	cii := strings.Replace(validCII, "<ID>urn:cen.eu:en16931:2017</ID>", "<ID>"+id+"</ID>", 1)
	if p == formalis.ProfileMinimum {
		// MINIMUM carries no buyer postal address: its data model marks the
		// element unused at that tier, and neither authority sample has one.
		cii = strings.Replace(cii,
			"<BuyerTradeParty><Name>Buyer Co</Name><PostalTradeAddress><CountryID>FR</CountryID></PostalTradeAddress></BuyerTradeParty>",
			"<BuyerTradeParty><Name>Buyer Co</Name></BuyerTradeParty>", 1)
	}
	return cii
}

// TestEmbedFacturXRoundTrip is the writer's core guarantee: a Factur-X document
// built by embedding invoice XML into a PDF/A-3 base validates as a conforming
// Factur-X container after a Write/Read round trip, with the invoice XML
// recovered intact, for every profile.
func TestEmbedFacturXRoundTrip(t *testing.T) {
	for _, profile := range []formalis.Profile{
		formalis.ProfileMinimum, formalis.ProfileBasicWL, formalis.ProfileBasic, formalis.ProfileEN16931, formalis.ProfileExtended,
	} {
		t.Run(string(profile), func(t *testing.T) {
			doc := NewPDFADocument(PDFA3b)
			if err := EmbedFacturX(doc, []byte(ciiForProfile(profile)), profile, "Invoice INV-1"); err != nil {
				t.Fatalf("EmbedFacturX: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("Write: %v", err)
			}
			rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("re-Read: %v", err)
			}
			res := ValidateFacturX(rt, buf.Bytes())
			if len(res.Violations) != 0 {
				t.Fatalf("produced file has %d Factur-X violation(s): %s: %s",
					len(res.Violations), res.Violations[0].Rule, res.Violations[0].Message)
			}
			if res.Profile != profile {
				t.Errorf("detected profile %q, want %q", res.Profile, profile)
			}
			if res.XMLName != "factur-x.xml" {
				t.Errorf("embedded file name %q, want factur-x.xml", res.XMLName)
			}
			if want := ciiForProfile(profile); !bytes.Equal(res.XML, []byte(want)) {
				t.Errorf("recovered invoice XML does not match the input (%d vs %d bytes)", len(res.XML), len(want))
			}
		})
	}
}

func TestEmbedFacturXUnknownProfile(t *testing.T) {
	doc := NewPDFADocument(PDFA3b)
	if err := EmbedFacturX(doc, []byte(validCII), formalis.Profile("BOGUS"), ""); err == nil {
		t.Error("expected an error for an unknown profile")
	}
}

// TestFacturXXMPPacket checks the generated metadata declares the fx extension
// schema and the Factur-X properties for the profile.
func TestFacturXXMPPacket(t *testing.T) {
	xmp := string(facturxXMPPacket(formalis.ProfileBasic, "INVOICE", "Some & Title"))
	for _, want := range []string{
		"<pdfaid:part>3</pdfaid:part>",
		"urn:factur-x:pdfa:CrossIndustryDocument:invoice:1p0#",
		"<pdfaSchema:prefix>fx</pdfaSchema:prefix>",
		"<fx:DocumentType>INVOICE</fx:DocumentType>",
		"<fx:DocumentFileName>factur-x.xml</fx:DocumentFileName>",
		"<fx:ConformanceLevel>BASIC</fx:ConformanceLevel>",
		"Some &amp; Title", // title is XML-escaped
	} {
		if !strings.Contains(xmp, want) {
			t.Errorf("XMP packet missing %q", want)
		}
	}
}

func TestEncodeUTF16BE(t *testing.T) {
	got := encodeUTF16BE("factur-x.xml")
	if got[0] != 0xFE || got[1] != 0xFF {
		t.Fatal("missing UTF-16BE byte-order mark")
	}
	if decodePDFTextString(got) != "factur-x.xml" {
		t.Errorf("round trip failed: %q", decodePDFTextString(got))
	}
}
