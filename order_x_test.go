package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/einvoice"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validOrderXML = `<SCRDMCCBDACIOMessageStructure>
<ExchangedDocument><ID>ORD-1</ID><TypeCode>220</TypeCode>
<IssueDateTime><DateTimeString>20240115</DateTimeString></IssueDateTime></ExchangedDocument>
<SupplyChainTradeTransaction><ApplicableHeaderTradeAgreement>
<BuyerTradeParty><Name>Buyer Ltd</Name></BuyerTradeParty>
<SellerTradeParty><Name>Seller Ltd</Name></SellerTradeParty>
</ApplicableHeaderTradeAgreement></SupplyChainTradeTransaction></SCRDMCCBDACIOMessageStructure>`

func TestValidateOrderXDocumentValid(t *testing.T) {
	if v := einvoice.ValidateOrderXML([]byte(validOrderXML)); len(v) != 0 {
		t.Errorf("valid order flagged: %v", v)
	}
}

func TestValidateOrderXDocumentViolations(t *testing.T) {
	cases := []struct{ name, remove, rule string }{
		{"not an order", "SCRDMCCBDACIOMessageStructure", "order-xml"},
		{"no order number", "<ID>ORD-1</ID>", "BR-O-01"},
		{"no issue date", "<IssueDateTime><DateTimeString>20240115</DateTimeString></IssueDateTime>", "BR-O-02"},
		{"no type code", "<TypeCode>220</TypeCode>", "BR-O-03"},
		{"no buyer", "<BuyerTradeParty><Name>Buyer Ltd</Name></BuyerTradeParty>", "BR-O-04"},
		{"no seller", "<SellerTradeParty><Name>Seller Ltd</Name></SellerTradeParty>", "BR-O-05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broken := strings.Replace(validOrderXML, tc.remove, "", 1)
			v := einvoice.ValidateOrderXML([]byte(broken))
			found := false
			for _, e := range v {
				if e.Rule == tc.rule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %s; got %v", tc.rule, v)
			}
		})
	}
	// A non-order type code is rejected.
	badType := strings.Replace(validOrderXML, "<TypeCode>220</TypeCode>", "<TypeCode>380</TypeCode>", 1)
	v := einvoice.ValidateOrderXML([]byte(badType))
	if len(v) == 0 || v[0].Rule != "BR-O-03" {
		t.Errorf("invoice type code 380 should be rejected for an order; got %v", v)
	}
}

func TestOrderXProfiles(t *testing.T) {
	for _, p := range []string{"BASIC", "COMFORT", "EXTENDED", "basic", "Extended"} {
		if _, ok := orderXProfileFor(p); !ok {
			t.Errorf("%q should be an Order-X profile", p)
		}
	}
	if _, ok := orderXProfileFor("EN 16931"); ok {
		t.Error("EN 16931 is an invoice profile, not Order-X")
	}
}

// TestValidateOrderXCorpus is the FP=0 oracle for the Order-X container: every
// conforming Order-X example (BASIC/COMFORT, orders/changes/responses) must
// validate with no violations. The examples ship in the (gitignored) Order-X
// specification bundle; the test skips when spec/order-x is absent.
func TestValidateOrderXCorpus(t *testing.T) {
	files, _ := filepath.Glob("spec/order-x/Order-X100_EN/05-ORDER-X EXAMPLES/**/*.pdf")
	if len(files) == 0 {
		t.Skip("Order-X examples not present (spec/order-x)")
	}
	seen := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Errorf("%s: read: %v", filepath.Base(f), err)
			continue
		}
		res := doc.ValidateOrderX(data)
		if res.XMLName == "" {
			continue // a supporting PDF, not an Order-X container
		}
		seen++
		if len(res.Violations) != 0 {
			t.Errorf("%s: expected 0 violations on a conforming Order-X, got %d (first: %s: %s)",
				filepath.Base(f), len(res.Violations), res.Violations[0].Rule, res.Violations[0].Message)
		}
		if res.Profile == "" {
			t.Errorf("%s: no Order-X profile detected", filepath.Base(f))
		}
	}
	if seen < 3 {
		t.Errorf("expected several Order-X example containers, saw %d", seen)
	}
}
