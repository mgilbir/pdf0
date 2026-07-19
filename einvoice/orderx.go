package einvoice

import "fmt"

// This file validates the embedded Cross Industry Order document of an Order-X
// (a.k.a. ZUGFeRD Order) — the order-document sibling of Factur-X. Order-X
// business rules differ from EN 16931 (which is invoice-specific), so this checks
// the order's structure and mandatory head terms, reusing the shared CII parser.
// The PDF container around it is validated in the pdf0 package.

// orderXTypeCodes is the order document type code set (UNTDID 1001): order (220),
// order change (230), order response (231).
var orderXTypeCodes = map[string]bool{"220": true, "230": true, "231": true}

// ValidateOrderXML checks an embedded Cross Industry Order's structure and
// mandatory head business terms, returning any violations.
func ValidateOrderXML(xmlData []byte) []Violation {
	var out []Violation
	add := func(rule, msg string) { out = append(out, Violation{Rule: rule, Message: msg}) }

	root, err := parseCII(xmlData)
	if err != nil || root.name != "SCRDMCCBDACIOMessageStructure" {
		add("order-xml", "the order XML is not a well-formed Cross Industry Order (SCRDMCCBDACIOMessageStructure)")
		return out
	}
	doc := root.child("ExchangedDocument").orNil()
	agr := root.child("SupplyChainTradeTransaction", "ApplicableHeaderTradeAgreement").orNil()

	if doc.str("ID") == "" {
		add("BR-O-01", "an Order shall have an order number (ExchangedDocument/ID)")
	}
	if doc.str("IssueDateTime", "DateTimeString") == "" {
		add("BR-O-02", "an Order shall have an issue date (ExchangedDocument/IssueDateTime)")
	}
	if tc := doc.str("TypeCode"); tc == "" {
		add("BR-O-03", "an Order shall have a document type code (ExchangedDocument/TypeCode)")
	} else if !orderXTypeCodes[tc] {
		add("BR-O-03", fmt.Sprintf("order type code %q is not a permitted UNTDID 1001 order value (220/230/231)", tc))
	}
	if agr.str("BuyerTradeParty", "Name") == "" {
		add("BR-O-04", "an Order shall contain the Buyer name")
	}
	if agr.str("SellerTradeParty", "Name") == "" {
		add("BR-O-05", "an Order shall contain the Seller name")
	}
	return out
}
