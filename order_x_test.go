package pdf0

import (
	"bytes"
	"context"
	"github.com/mgilbir/formalis"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validOrderXML = `<SCRDMCCBDACIOMessageStructure>
<ExchangedDocument><ID>ORD-1</ID><TypeCode>220</TypeCode>
<IssueDateTime><DateTimeString format="102">20240115</DateTimeString></IssueDateTime></ExchangedDocument>
<SupplyChainTradeTransaction><ApplicableHeaderTradeAgreement>
<BuyerTradeParty><Name>Buyer Ltd</Name></BuyerTradeParty>
<SellerTradeParty><Name>Seller Ltd</Name></SellerTradeParty>
</ApplicableHeaderTradeAgreement></SupplyChainTradeTransaction></SCRDMCCBDACIOMessageStructure>`

func TestValidateOrderXDocumentValid(t *testing.T) {
	rep, err := formalis.ValidateOrderXML(context.Background(), []byte(validOrderXML))
	if err != nil {
		t.Fatalf("valid order could not be read: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Errorf("valid order flagged: %v", rep.Violations)
	}
	// The order rule engine checks five head terms out of the whole Order-X
	// document rule set, so even a clean order is neither Complete nor
	// Conformant. Pinning it here is what stops a later reading of "no findings"
	// as "conforming order".
	if rep.Complete() {
		t.Error("the Order-X rule set has a published gap; a run over it cannot be complete")
	}
	if len(rep.NotEvaluated) == 0 {
		t.Error("a run that cannot be complete must name what it did not evaluate")
	}
}

func TestValidateOrderXDocumentViolations(t *testing.T) {
	// The identifiers are formalis's own (ORDER-*, ORDER-root); it renamed them
	// out of CEN's BR-O-* numbering, which EN 16931 had already taken for the
	// "not subject to VAT" category family.
	cases := []struct{ name, remove, rule string }{
		{"no order number", "<ID>ORD-1</ID>", "ORDER-01"},
		{"no issue date", `<IssueDateTime><DateTimeString format="102">20240115</DateTimeString></IssueDateTime>`, "ORDER-02"},
		{"no type code", "<TypeCode>220</TypeCode>", "ORDER-03"},
		{"no buyer", "<BuyerTradeParty><Name>Buyer Ltd</Name></BuyerTradeParty>", "ORDER-04"},
		{"no seller", "<SellerTradeParty><Name>Seller Ltd</Name></SellerTradeParty>", "ORDER-05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broken := strings.Replace(validOrderXML, tc.remove, "", 1)
			rep, err := formalis.ValidateOrderXML(context.Background(), []byte(broken))
			if err != nil {
				t.Fatalf("expected a finding, got a read error: %v", err)
			}
			found := false
			for _, e := range rep.Violations {
				if e.Rule == tc.rule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected %s; got %v", tc.rule, rep.Violations)
			}
		})
	}
	// A non-order type code is rejected.
	badType := strings.Replace(validOrderXML, "<TypeCode>220</TypeCode>", "<TypeCode>380</TypeCode>", 1)
	rep, err := formalis.ValidateOrderXML(context.Background(), []byte(badType))
	if err != nil {
		t.Fatalf("expected a finding, got a read error: %v", err)
	}
	if len(rep.Violations) == 0 || rep.Violations[0].Rule != "ORDER-03" {
		t.Errorf("invoice type code 380 should be rejected for an order; got %v", rep.Violations)
	}
}

// TestValidateOrderXMLWrongRootIsAFinding pins the half of the old "not an
// order" case that is still a finding. formalis v0.2.0 split what used to be one
// answer in two: a well-formed document with the wrong root is ORDER-root, a
// definite statement about the document; input that is not XML at all is an
// error, because there is no document to make a finding about.
func TestValidateOrderXMLWrongRootIsAFinding(t *testing.T) {
	invoice := strings.ReplaceAll(validOrderXML, "SCRDMCCBDACIOMessageStructure", "CrossIndustryInvoice")
	rep, err := formalis.ValidateOrderXML(context.Background(), []byte(invoice))
	if err != nil {
		t.Fatalf("a well-formed non-order is a finding, not a read error: %v", err)
	}
	found := false
	for _, v := range rep.Violations {
		if v.Rule == formalis.RuleRoot || v.Rule == "ORDER-root" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a root finding for a Cross Industry Invoice; got %v", rep.Violations)
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
		res := ValidateOrderX(doc, data)
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
