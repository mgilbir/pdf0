package pdf0

import (
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// This file begins the EN 16931 semantic validation of the invoice XML embedded
// in a Factur-X document: the UN/CEFACT Cross Industry Invoice (CII). It parses
// the XML and checks the foundational business rules that every profile shares —
// the mandatory document-level business terms and the invoice-total consistency.
// The rule identifiers (BR-*, BR-CO-*) and texts are those of EN 16931, as
// carried by the Factur-X Schematron; deeper rule families (VAT breakdowns, line
// items, allowances/charges, code lists, decimals) are layered on separately.
//
// The XML is walked namespace-agnostically by local element name, so it is
// resilient to the namespace-prefix variation seen across producers.

// ciiNode is a parsed CII XML element addressed by its local name.
type ciiNode struct {
	name     string
	text     string
	attrs    map[string]string // keyed by local attribute name
	children []*ciiNode
}

// attr returns the value of the named attribute (by local name), or "".
func (n *ciiNode) attr(name string) string {
	if n == nil {
		return ""
	}
	return n.attrs[name]
}

// parseCII parses invoice XML into a local-name element tree, or returns nil and
// an error if it is not well-formed.
func parseCII(data []byte) (*ciiNode, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var stack []*ciiNode
	var root *ciiNode
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &ciiNode{name: t.Name.Local}
			if len(t.Attr) > 0 {
				n.attrs = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					n.attrs[a.Name.Local] = a.Value
				}
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	return root, nil
}

// child returns the first descendant reached by following the given local names,
// or nil if any step is missing.
func (n *ciiNode) child(path ...string) *ciiNode {
	cur := n
	for _, name := range path {
		var next *ciiNode
		for _, c := range cur.children {
			if c.name == name {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

// all returns every direct child with the given local name.
func (n *ciiNode) all(name string) []*ciiNode {
	var out []*ciiNode
	for _, c := range n.children {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

// str returns the trimmed text at the given path, or "".
func (n *ciiNode) str(path ...string) string {
	if c := n.child(path...); c != nil {
		return strings.TrimSpace(c.text)
	}
	return ""
}

// ValidateFacturXInvoice checks the CII invoice XML against the foundational
// EN 16931 business rules shared by every profile. It is the semantic layer
// beneath ValidateFacturX's container checks; pass the profile so profile-
// specific expectations can be applied as the rule set grows.
// ValidateFacturXInvoice validates the embedded invoice XML against the EN 16931
// core business rules. It accepts either syntax — a UN/CEFACT Cross Industry
// Invoice (Factur-X/ZUGFeRD) or an OASIS UBL Invoice/CreditNote (Peppol BIS,
// XRechnung UBL) — detecting which from the root element and mapping it onto the
// shared semantic model before running the one rule engine (validateEN16931).
func ValidateFacturXInvoice(xmlData []byte, profile FacturXProfile) []FacturXViolation {
	inv, err := parseEN16931(xmlData)
	if err != nil {
		return []FacturXViolation{{Rule: "syntax", Message: err.Error()}}
	}
	return validateEN16931(inv, profile)
}

// parseEN16931 parses the invoice XML and maps it onto the semantic model,
// dispatching on the root element to the CII or UBL mapper.
func parseEN16931(xmlData []byte) (*en16931Invoice, error) {
	root, err := parseCII(xmlData)
	if err != nil {
		return nil, fmt.Errorf("the invoice XML is not well-formed: %w", err)
	}
	switch root.name {
	case "CrossIndustryInvoice":
		return mapCII(root), nil
	case "Invoice", "CreditNote":
		return mapUBL(root), nil
	}
	return nil, fmt.Errorf("the invoice XML root %q is neither a CrossIndustryInvoice (CII) nor a UBL Invoice/CreditNote", root.name)
}

// mapCII extracts the EN 16931 business terms from a Cross Industry Invoice tree.
func mapCII(root *ciiNode) *en16931Invoice {
	doc := root.child("ExchangedDocument")
	tx := root.child("SupplyChainTradeTransaction")
	agr := tx.orNil().child("ApplicableHeaderTradeAgreement")
	settle := tx.orNil().child("ApplicableHeaderTradeSettlement")
	sum := settle.orNil().child("SpecifiedTradeSettlementHeaderMonetarySummation")

	inv := &en16931Invoice{
		specID:               root.str("ExchangedDocumentContext", "GuidelineSpecifiedDocumentContextParameter", "ID"),
		number:               doc.orNil().str("ID"),
		issueDate:            doc.orNil().str("IssueDateTime", "DateTimeString"),
		typeCode:             doc.orNil().str("TypeCode"),
		currency:             settle.orNil().str("InvoiceCurrencyCode"),
		sellerName:           agr.orNil().str("SellerTradeParty", "Name"),
		buyerName:            agr.orNil().str("BuyerTradeParty", "Name"),
		sellerCountry:        agr.orNil().str("SellerTradeParty", "PostalTradeAddress", "CountryID"),
		sellerAddressPresent: agr.orNil().child("SellerTradeParty", "PostalTradeAddress") != nil,
		buyerCountry:         agr.orNil().str("BuyerTradeParty", "PostalTradeAddress", "CountryID"),
		buyerAddressPresent:  agr.orNil().child("BuyerTradeParty", "PostalTradeAddress") != nil,
	}
	if sum != nil {
		inv.hasTotals = true
		inv.totals = monetaryTotals{
			lineTotal:       sum.str("LineTotalAmount"),
			allowanceTotal:  sum.str("AllowanceTotalAmount"),
			chargeTotal:     sum.str("ChargeTotalAmount"),
			taxBasisTotal:   sum.str("TaxBasisTotalAmount"),
			taxTotal:        sum.str("TaxTotalAmount"),
			grandTotal:      sum.str("GrandTotalAmount"),
			paidAmount:      sum.str("TotalPrepaidAmount"),
			payableRounding: sum.str("RoundingAmount"),
			duePayable:      sum.str("DuePayableAmount"),
		}
	}
	for _, tt := range settle.orNil().all("ApplicableTradeTax") {
		inv.vatBreakdowns = append(inv.vatBreakdowns, vatBreakdown{
			basis:     tt.str("BasisAmount"),
			calc:      tt.str("CalculatedAmount"),
			category:  tt.str("CategoryCode"),
			rate:      tt.str("RateApplicablePercent"),
			hasReason: tt.str("ExemptionReason") != "" || tt.str("ExemptionReasonCode") != "",
		})
	}
	for _, ac := range settle.orNil().all("SpecifiedTradeAllowanceCharge") {
		inv.allowCharges = append(inv.allowCharges, docAllowanceCharge{
			amount:    ac.str("ActualAmount"),
			category:  ac.str("CategoryTradeTax", "CategoryCode"),
			rate:      ac.str("CategoryTradeTax", "RateApplicablePercent"),
			hasReason: ac.str("Reason") != "" || ac.str("ReasonCode") != "",
			isCharge:  strings.EqualFold(ac.str("ChargeIndicator", "Indicator"), "true"),
		})
	}
	for _, li := range tx.orNil().all("IncludedSupplyChainTradeLineItem") {
		line := invoiceLine{
			lineID:       li.str("AssociatedDocumentLineDocument", "LineID"),
			parentLineID: li.str("AssociatedDocumentLineDocument", "ParentLineID"),
			itemName:     li.str("SpecifiedTradeProduct", "Name"),
			netAmount:    li.str("SpecifiedLineTradeSettlement", "SpecifiedTradeSettlementLineMonetarySummation", "LineTotalAmount"),
			price:        li.str("SpecifiedLineTradeAgreement", "NetPriceProductTradePrice", "ChargeAmount"),
			vatCategory:  li.str("SpecifiedLineTradeSettlement", "ApplicableTradeTax", "CategoryCode"),
			vatRate:      li.str("SpecifiedLineTradeSettlement", "ApplicableTradeTax", "RateApplicablePercent"),
		}
		if qty := li.child("SpecifiedLineTradeDelivery", "BilledQuantity"); qty != nil {
			line.quantity = strings.TrimSpace(qty.text)
			line.unitCode = qty.attr("unitCode")
		}
		inv.lines = append(inv.lines, line)
	}
	return inv
}
func round2(f float64) float64 { return math.Round(f*100) / 100 }

// decimalCount returns the number of digits after the decimal point in s.
func decimalCount(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return len(s) - i - 1
	}
	return 0
}

// isUpperAlpha reports whether s is exactly n uppercase ASCII letters (the shape
// of ISO 4217 currency and ISO 3166-1 alpha-2 country codes).
func isUpperAlpha(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < n; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// facturxVATCategories is the UNCL 5305 VAT category code subset used by EN 16931.
var facturxVATCategories = map[string]bool{
	"S": true, "Z": true, "E": true, "AE": true, "K": true,
	"G": true, "O": true, "L": true, "M": true,
}

// orNil lets a possibly-nil node be traversed without panicking.
func (n *ciiNode) orNil() *ciiNode {
	if n == nil {
		return &ciiNode{}
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// parseAmount parses a CII decimal amount.
func parseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// mapUBL extracts the EN 16931 business terms from an OASIS UBL Invoice or
// CreditNote. The tree is parsed namespace-agnostically (parseCII), so the cbc:/
// cac: prefixes are already stripped to local names. The document-type element
// names differ between an Invoice and a CreditNote.
func mapUBL(root *ciiNode) *en16931Invoice {
	typeCodeName, lineName, qtyName := "InvoiceTypeCode", "InvoiceLine", "InvoicedQuantity"
	if root.name == "CreditNote" {
		typeCodeName, lineName, qtyName = "CreditNoteTypeCode", "CreditNoteLine", "CreditedQuantity"
	}
	seller := root.child("AccountingSupplierParty", "Party").orNil()
	buyer := root.child("AccountingCustomerParty", "Party").orNil()
	total := root.child("LegalMonetaryTotal")
	taxTotal := root.child("TaxTotal").orNil()

	inv := &en16931Invoice{
		specID:    root.str("CustomizationID"),
		number:    root.str("ID"),
		issueDate: root.str("IssueDate"),
		typeCode:  root.str(typeCodeName),
		currency:  root.str("DocumentCurrencyCode"),
		// BT-27/BT-44 bind to the legal registration name; some producers carry
		// the name only in cac:PartyName, so fall back to it.
		sellerName:           firstNonEmpty(seller.str("PartyLegalEntity", "RegistrationName"), seller.str("PartyName", "Name")),
		buyerName:            firstNonEmpty(buyer.str("PartyLegalEntity", "RegistrationName"), buyer.str("PartyName", "Name")),
		sellerCountry:        seller.str("PostalAddress", "Country", "IdentificationCode"),
		sellerAddressPresent: seller.child("PostalAddress") != nil,
		buyerCountry:         buyer.str("PostalAddress", "Country", "IdentificationCode"),
		buyerAddressPresent:  buyer.child("PostalAddress") != nil,
	}
	if total != nil {
		inv.hasTotals = true
		inv.totals = monetaryTotals{
			lineTotal:       total.str("LineExtensionAmount"),
			allowanceTotal:  total.str("AllowanceTotalAmount"),
			chargeTotal:     total.str("ChargeTotalAmount"),
			taxBasisTotal:   total.str("TaxExclusiveAmount"),
			taxTotal:        taxTotal.str("TaxAmount"), // BT-110: TaxTotal's direct amount
			grandTotal:      total.str("TaxInclusiveAmount"),
			paidAmount:      total.str("PrepaidAmount"),
			payableRounding: total.str("PayableRoundingAmount"),
			duePayable:      total.str("PayableAmount"),
		}
	}
	for _, ts := range taxTotal.all("TaxSubtotal") {
		inv.vatBreakdowns = append(inv.vatBreakdowns, vatBreakdown{
			basis:     ts.str("TaxableAmount"),
			calc:      ts.str("TaxAmount"),
			category:  ts.str("TaxCategory", "ID"),
			rate:      ts.str("TaxCategory", "Percent"),
			hasReason: ts.str("TaxCategory", "TaxExemptionReason") != "" || ts.str("TaxCategory", "TaxExemptionReasonCode") != "",
		})
	}
	for _, ac := range root.all("AllowanceCharge") {
		inv.allowCharges = append(inv.allowCharges, docAllowanceCharge{
			amount:    ac.str("Amount"),
			category:  ac.str("TaxCategory", "ID"),
			rate:      ac.str("TaxCategory", "Percent"),
			hasReason: ac.str("AllowanceChargeReason") != "" || ac.str("AllowanceChargeReasonCode") != "",
			isCharge:  strings.EqualFold(ac.str("ChargeIndicator"), "true"),
		})
	}
	for _, li := range root.all(lineName) {
		line := invoiceLine{
			lineID:      li.str("ID"),
			itemName:    li.str("Item", "Name"),
			netAmount:   li.str("LineExtensionAmount"),
			price:       li.str("Price", "PriceAmount"),
			vatCategory: li.str("Item", "ClassifiedTaxCategory", "ID"),
			vatRate:     li.str("Item", "ClassifiedTaxCategory", "Percent"),
		}
		if qty := li.child(qtyName); qty != nil {
			line.quantity = strings.TrimSpace(qty.text)
			line.unitCode = qty.attr("unitCode")
		}
		inv.lines = append(inv.lines, line)
	}
	return inv
}
