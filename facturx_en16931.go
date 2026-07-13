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
	children []*ciiNode
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
func ValidateFacturXInvoice(xmlData []byte, profile FacturXProfile) []FacturXViolation {
	var out []FacturXViolation
	add := func(rule, msg string) { out = append(out, FacturXViolation{Rule: rule, Message: msg}) }

	root, err := parseCII(xmlData)
	if err != nil || root.name != "CrossIndustryInvoice" {
		add("cii", "the invoice XML is not a well-formed CrossIndustryInvoice")
		return out
	}

	doc := root.child("ExchangedDocument")
	tx := root.child("SupplyChainTradeTransaction")
	agr := tx.orNil().child("ApplicableHeaderTradeAgreement")
	settle := tx.orNil().child("ApplicableHeaderTradeSettlement")

	req := func(rule, msg, val string) {
		if val == "" {
			add(rule, msg)
		}
	}
	// Mandatory document-level business terms (present in every profile).
	req("BR-01", "An Invoice shall have a Specification identifier (BT-24)",
		root.str("ExchangedDocumentContext", "GuidelineSpecifiedDocumentContextParameter", "ID"))
	req("BR-02", "An Invoice shall have an Invoice number (BT-1)", doc.orNil().str("ID"))
	req("BR-03", "An Invoice shall have an Invoice issue date (BT-2)",
		doc.orNil().str("IssueDateTime", "DateTimeString"))
	req("BR-04", "An Invoice shall have an Invoice type code (BT-3)", doc.orNil().str("TypeCode"))
	req("BR-05", "An Invoice shall have an Invoice currency code (BT-5)",
		settle.orNil().str("InvoiceCurrencyCode"))
	req("BR-06", "An Invoice shall contain the Seller name (BT-27)",
		agr.orNil().str("SellerTradeParty", "Name"))
	req("BR-07", "An Invoice shall contain the Buyer name (BT-44)",
		agr.orNil().str("BuyerTradeParty", "Name"))
	req("BR-08", "An Invoice shall contain the Seller postal address (BG-5)",
		firstNonEmpty(agr.orNil().str("SellerTradeParty", "PostalTradeAddress", "CountryID"),
			agr.orNil().str("SellerTradeParty", "PostalTradeAddress", "CityName")))
	req("BR-09", "The Seller postal address shall contain a Seller country code (BT-40)",
		agr.orNil().str("SellerTradeParty", "PostalTradeAddress", "CountryID"))

	sum := settle.orNil().child("SpecifiedTradeSettlementHeaderMonetarySummation")
	req("BR-13", "An Invoice shall have the Invoice total amount without VAT (BT-109)",
		sum.orNil().str("TaxBasisTotalAmount"))
	req("BR-14", "An Invoice shall have the Invoice total amount with VAT (BT-112)",
		sum.orNil().str("GrandTotalAmount"))
	req("BR-15", "An Invoice shall have the Amount due for payment (BT-115)",
		sum.orNil().str("DuePayableAmount"))

	// BR-CO-15: Invoice total with VAT (BT-112) = total without VAT (BT-109) +
	// total VAT amount (BT-110).
	if sum != nil {
		basis, okB := parseAmount(sum.str("TaxBasisTotalAmount"))
		grand, okG := parseAmount(sum.str("GrandTotalAmount"))
		tax, okT := parseAmount(sum.str("TaxTotalAmount"))
		if !okT {
			tax = 0 // BT-110 is optional when there is no VAT
		}
		if okB && okG && math.Abs((basis+tax)-grand) > 0.005 {
			add("BR-CO-15", fmt.Sprintf("Invoice total with VAT (BT-112=%.2f) shall equal total without VAT (BT-109=%.2f) + VAT total (BT-110=%.2f)", grand, basis, tax))
		}
	}

	// VAT breakdown (BG-23) rules, applied to each ApplicableTradeTax present, so
	// profiles without a breakdown (MINIMUM) are naturally skipped.
	taxes := settle.orNil().all("ApplicableTradeTax")
	var vatTotal float64
	for _, tt := range taxes {
		basis := tt.str("BasisAmount")
		calc := tt.str("CalculatedAmount")
		cat := tt.str("CategoryCode")
		rate := tt.str("RateApplicablePercent")
		if basis == "" {
			add("BR-45", "Each VAT breakdown (BG-23) shall have a VAT category taxable amount (BT-116)")
		}
		if calc == "" {
			add("BR-46", "Each VAT breakdown (BG-23) shall have a VAT category tax amount (BT-117)")
		}
		if cat == "" {
			add("BR-47", "Each VAT breakdown (BG-23) shall be defined through a VAT category code (BT-118)")
		}
		// BR-48: a rate is required except for the "Not subject to VAT" category (O).
		if rate == "" && cat != "O" {
			add("BR-48", "Each VAT breakdown (BG-23) shall have a VAT category rate (BT-119)")
		}
		// BR-CO-17: BT-117 = BT-116 x (BT-119 / 100), rounded to two decimals.
		b, okB := parseAmount(basis)
		c, okC := parseAmount(calc)
		r, okR := parseAmount(rate)
		if okB && okC && okR && math.Abs(round2(b*r/100)-c) > 0.005 {
			add("BR-CO-17", fmt.Sprintf("VAT category tax amount (BT-117=%.2f) shall equal taxable amount (BT-116=%.2f) x rate (BT-119=%.2f%%)", c, b, r))
		}
		if okC {
			vatTotal += c
		}

		// VAT category rules: the tax amount is zero for the non-taxed
		// categories, the zero-rated category carries a zero rate, and the
		// exemption reason is present exactly when the category requires one
		// (BT-120/BT-121).
		hasReason := tt.str("ExemptionReason") != "" || tt.str("ExemptionReasonCode") != ""
		zeroTax := okC && math.Abs(c) > 0.005
		switch cat {
		case "S": // Standard rate
			if hasReason {
				add("BR-S-10", "A VAT breakdown with category \"Standard rate\" (S) shall not have a VAT exemption reason")
			}
		case "Z": // Zero rated
			if okR && math.Abs(r) > 0.005 {
				add("BR-Z-08", "A VAT breakdown with category \"Zero rated\" (Z) shall have a VAT rate of 0")
			}
			if zeroTax {
				add("BR-Z-09", "The VAT category tax amount for category \"Zero rated\" (Z) shall be 0")
			}
			if hasReason {
				add("BR-Z-10", "A VAT breakdown with category \"Zero rated\" (Z) shall not have a VAT exemption reason")
			}
		case "E": // Exempt from VAT
			if zeroTax {
				add("BR-E-09", "The VAT category tax amount for category \"Exempt from VAT\" (E) shall be 0")
			}
			if !hasReason {
				add("BR-E-10", "A VAT breakdown with category \"Exempt from VAT\" (E) shall have a VAT exemption reason")
			}
		case "AE": // Reverse charge
			if zeroTax {
				add("BR-AE-09", "The VAT category tax amount for category \"Reverse charge\" (AE) shall be 0")
			}
			if !hasReason {
				add("BR-AE-10", "A VAT breakdown with category \"Reverse charge\" (AE) shall have a VAT exemption reason")
			}
		case "K": // Intra-community supply
			if zeroTax {
				add("BR-IC-09", "The VAT category tax amount for category \"Intra-community supply\" (K) shall be 0")
			}
			if !hasReason {
				add("BR-IC-10", "A VAT breakdown with category \"Intra-community supply\" (K) shall have a VAT exemption reason")
			}
		case "G": // Export outside the EU
			if zeroTax {
				add("BR-G-09", "The VAT category tax amount for category \"Export outside the EU\" (G) shall be 0")
			}
			if !hasReason {
				add("BR-G-10", "A VAT breakdown with category \"Export outside the EU\" (G) shall have a VAT exemption reason")
			}
		case "O": // Not subject to VAT
			if zeroTax {
				add("BR-O-09", "The VAT category tax amount for category \"Not subject to VAT\" (O) shall be 0")
			}
			if !hasReason {
				add("BR-O-10", "A VAT breakdown with category \"Not subject to VAT\" (O) shall have a VAT exemption reason")
			}
		}
	}
	// BR-CO-14: Invoice total VAT amount (BT-110) = sum of VAT category tax
	// amounts (BT-117), when a breakdown is present.
	if len(taxes) > 0 && sum != nil {
		if tax, ok := parseAmount(sum.str("TaxTotalAmount")); ok && math.Abs(vatTotal-tax) > 0.005 {
			add("BR-CO-14", fmt.Sprintf("Invoice total VAT (BT-110=%.2f) shall equal the sum of VAT breakdown tax amounts (%.2f)", tax, vatTotal))
		}
	}

	// BR-CO-10 and BR-CO-13 assume the flat EN 16931 line model. The EXTENDED
	// profile adds sub-invoice lines (a parent line rolls up its children, so a
	// naive sum double-counts) and richer document-level allowance/charge
	// structures; validating those correctly is deferred, so these two totals are
	// checked only for the non-EXTENDED profiles here.
	flatModel := profile != FacturXExtended

	// BR-CO-10: Sum of Invoice line net amounts (BT-106) = sum of line net
	// amounts (BT-131), when line items are present.
	lines := tx.orNil().all("IncludedSupplyChainTradeLineItem")
	if flatModel && len(lines) > 0 && sum != nil {
		var lineSum float64
		for _, li := range lines {
			if v, ok := parseAmount(li.str("SpecifiedLineTradeSettlement", "SpecifiedTradeSettlementLineMonetarySummation", "LineTotalAmount")); ok {
				lineSum += v
			}
		}
		if lt, ok := parseAmount(sum.str("LineTotalAmount")); ok && math.Abs(round2(lineSum)-lt) > 0.005 {
			add("BR-CO-10", fmt.Sprintf("Sum of Invoice line net amount (BT-106=%.2f) shall equal the sum of line net amounts (%.2f)", lt, lineSum))
		}
	}

	// BR-CO-13: Invoice total without VAT (BT-109) = sum of line net amounts
	// (BT-106) minus document allowances (BT-107) plus document charges (BT-108),
	// when the line total is present.
	if flatModel && sum != nil {
		if lt, ok := parseAmount(sum.str("LineTotalAmount")); ok {
			var allowances, charges float64
			for _, ac := range settle.orNil().all("SpecifiedTradeAllowanceCharge") {
				amt, ok := parseAmount(ac.str("ActualAmount"))
				if !ok {
					continue
				}
				if strings.EqualFold(ac.str("ChargeIndicator", "Indicator"), "true") {
					charges += amt
				} else {
					allowances += amt
				}
			}
			if basis, ok := parseAmount(sum.str("TaxBasisTotalAmount")); ok &&
				math.Abs(round2(lt-allowances+charges)-basis) > 0.005 {
				add("BR-CO-13", fmt.Sprintf("Invoice total without VAT (BT-109=%.2f) shall equal line total (%.2f) - allowances (%.2f) + charges (%.2f)", basis, lt, allowances, charges))
			}
		}
	}

	return out
}

// round2 rounds to two decimal places, half away from zero.
func round2(f float64) float64 { return math.Round(f*100) / 100 }

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
