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

	// Code lists (BR-CL-*). The Invoice currency (BT-5) and country codes
	// (BT-40/55) are checked for ISO 4217 / ISO 3166-1 alpha-2 shape; the invoice
	// type code (BT-3) against the EN 16931 UNTDID 1001 subset.
	if cur := settle.orNil().str("InvoiceCurrencyCode"); cur != "" && !isUpperAlpha(cur, 3) {
		add("BR-CL-03", fmt.Sprintf("Invoice currency code (BT-5=%q) shall be a valid ISO 4217 code", cur))
	}
	if tc := doc.orNil().str("TypeCode"); tc != "" && !facturxTypeCodes[tc] {
		add("BR-CL-05", fmt.Sprintf("Invoice type code (BT-3=%q) is not a permitted UNTDID 1001 value", tc))
	}
	if c := agr.orNil().str("SellerTradeParty", "PostalTradeAddress", "CountryID"); c != "" && !isUpperAlpha(c, 2) {
		add("BR-CL-14", fmt.Sprintf("Seller country code (BT-40=%q) shall be a valid ISO 3166-1 code", c))
	}
	if c := agr.orNil().str("BuyerTradeParty", "PostalTradeAddress", "CountryID"); c != "" && !isUpperAlpha(c, 2) {
		add("BR-CL-15", fmt.Sprintf("Buyer country code (BT-55=%q) shall be a valid ISO 3166-1 code", c))
	}

	// Decimals (BR-DEC-*): monetary amounts shall have at most two decimal places.
	if sum != nil {
		for _, fld := range []struct{ rule, name string }{
			{"BR-DEC-12", "LineTotalAmount"}, {"BR-DEC-15", "TaxBasisTotalAmount"},
			{"BR-DEC-16", "TaxTotalAmount"}, {"BR-DEC-17", "GrandTotalAmount"},
			{"BR-DEC-18", "DuePayableAmount"},
		} {
			if v := sum.str(fld.name); v != "" && decimalCount(v) > 2 {
				add(fld.rule, fmt.Sprintf("amount %s (%q) shall have at most two decimals", fld.name, v))
			}
		}
	}

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
		} else if !facturxVATCategories[cat] {
			add("BR-CL-17", fmt.Sprintf("VAT category code (BT-118=%q) is not a valid UNCL 5305 value", cat))
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

	// Document-level allowance (BG-20) and charge (BG-21) rules: each present
	// allowance or charge shall carry an amount, a VAT category code, and a
	// reason or reason code. Applied to every existing entry, so profiles without
	// document allowances/charges are unaffected.
	for _, ac := range settle.orNil().all("SpecifiedTradeAllowanceCharge") {
		amt := ac.str("ActualAmount")
		cat := ac.str("CategoryTradeTax", "CategoryCode")
		hasReason := ac.str("Reason") != "" || ac.str("ReasonCode") != ""
		if strings.EqualFold(ac.str("ChargeIndicator", "Indicator"), "true") {
			if amt == "" {
				add("BR-36", "Each Document level charge (BG-21) shall have a Document level charge amount (BT-99)")
			}
			if cat == "" {
				add("BR-37", "Each Document level charge (BG-21) shall have a Document level charge VAT category code (BT-102)")
			}
			if !hasReason {
				add("BR-38", "Each Document level charge (BG-21) shall have a Document level charge reason (BT-104) or reason code (BT-105)")
			}
		} else {
			if amt == "" {
				add("BR-31", "Each Document level allowance (BG-20) shall have a Document level allowance amount (BT-92)")
			}
			if cat == "" {
				add("BR-32", "Each Document level allowance (BG-20) shall have a Document level allowance VAT category code (BT-95)")
			}
			if !hasReason {
				add("BR-33", "Each Document level allowance (BG-20) shall have a Document level allowance reason (BT-97) or reason code (BT-98)")
			}
		}
	}

	// Line-item rules (BG-25): each invoice line carries its mandatory business
	// terms. This includes the EXTENDED profile's sub-invoice lines, which are
	// flat siblings distinguished by a parent line reference. A grouping line —
	// one referenced as another line's parent — aggregates its sub-lines and need
	// not carry an invoiced quantity or item price, so those two are checked only
	// on leaf lines.
	lines := tx.orNil().all("IncludedSupplyChainTradeLineItem")
	grouping := map[string]bool{}
	for _, li := range lines {
		if p := li.str("AssociatedDocumentLineDocument", "ParentLineID"); p != "" {
			grouping[p] = true
		}
	}
	for i, li := range lines {
		id := li.str("AssociatedDocumentLineDocument", "LineID")
		if id == "" {
			add("BR-21", fmt.Sprintf("Invoice line %d shall have an Invoice line identifier (BT-126)", i+1))
		}
		if li.str("SpecifiedTradeProduct", "Name") == "" {
			add("BR-25", "Each Invoice line shall have an Item name (BT-153)")
		}
		if li.str("SpecifiedLineTradeSettlement", "SpecifiedTradeSettlementLineMonetarySummation", "LineTotalAmount") == "" {
			add("BR-24", "Each Invoice line shall have an Invoice line net amount (BT-131)")
		}
		if grouping[id] {
			continue // grouping line: no direct quantity or price
		}
		qty := li.child("SpecifiedLineTradeDelivery", "BilledQuantity")
		if qty == nil || strings.TrimSpace(qty.text) == "" {
			add("BR-22", "Each Invoice line shall have an Invoiced quantity (BT-129)")
		} else if u := qty.attr("unitCode"); u == "" {
			add("BR-23", "Each Invoice line shall have an Invoiced quantity unit of measure (BT-130)")
		} else if !facturxUnitCodes[u] {
			add("BR-CL-23", fmt.Sprintf("Invoiced quantity unit code (BT-130=%q) is not a valid UNECE Rec 20/21 code", u))
		}
		price := li.str("SpecifiedLineTradeAgreement", "NetPriceProductTradePrice", "ChargeAmount")
		if price == "" {
			add("BR-26", "Each Invoice line shall have an Item net price (BT-146)")
		} else if p, ok := parseAmount(price); ok && p < 0 {
			add("BR-27", "The Item net price (BT-146) shall not be negative")
		}
	}

	// BR-CO-10: Sum of Invoice line net amounts (BT-106) = sum of line net
	// amounts (BT-131). A sub-invoice line's amount is rolled up into its parent
	// (identified by a parent line reference), so only top-level lines are summed.
	if len(lines) > 0 && sum != nil {
		var lineSum float64
		for _, li := range lines {
			if li.str("AssociatedDocumentLineDocument", "ParentLineID") != "" {
				continue
			}
			if v, ok := parseAmount(li.str("SpecifiedLineTradeSettlement", "SpecifiedTradeSettlementLineMonetarySummation", "LineTotalAmount")); ok {
				lineSum += v
			}
		}
		if lt, ok := parseAmount(sum.str("LineTotalAmount")); ok && math.Abs(round2(lineSum)-lt) > 0.005 {
			add("BR-CO-10", fmt.Sprintf("Sum of Invoice line net amount (BT-106=%.2f) shall equal the sum of line net amounts (%.2f)", lt, lineSum))
		}
	}

	// BR-CO-13: Invoice total without VAT (BT-109) = line total (BT-106) minus
	// the allowance total (BT-107) plus the charge total (BT-108). The allowance
	// and charge totals are the summation values, which cover charges (e.g.
	// logistics) not expressed as individual document allowance/charge entries.
	if sum != nil {
		if lt, ok := parseAmount(sum.str("LineTotalAmount")); ok {
			allowances, _ := parseAmount(sum.str("AllowanceTotalAmount"))
			charges, _ := parseAmount(sum.str("ChargeTotalAmount"))
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

// facturxTypeCodes is the EN 16931 permitted subset of UNTDID 1001 invoice type
// codes for BT-3.
var facturxTypeCodes = map[string]bool{
	"71": true, "80": true, "82": true, "84": true, "102": true, "130": true,
	"202": true, "203": true, "204": true, "211": true, "218": true, "219": true,
	"261": true, "262": true, "295": true, "296": true, "308": true, "325": true,
	"326": true, "331": true, "380": true, "381": true, "382": true, "383": true,
	"384": true, "385": true, "386": true, "387": true, "388": true, "389": true,
	"390": true, "393": true, "394": true, "395": true, "456": true, "457": true,
	"458": true, "527": true, "575": true, "623": true, "633": true, "751": true,
	"780": true, "817": true, "870": true, "875": true, "876": true, "877": true,
	"935": true,
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
