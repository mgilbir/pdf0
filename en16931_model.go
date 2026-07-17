package pdf0

import (
	"fmt"
	"math"
)

// This file holds the syntax-neutral EN 16931 semantic model and the business-
// rule engine that validates it. An EN 16931 invoice may be expressed in either
// of two XML syntaxes — UN/CEFACT Cross Industry Invoice (CII, used by Factur-X/
// ZUGFeRD) or OASIS UBL (used by Peppol BIS and XRechnung UBL). Both carry the
// same business terms (BT-*) and groups (BG-*), so each is mapped into this model
// (mapCII / mapUBL) and the one rule engine below validates it. Keeping a single
// rule set avoids two divergent implementations of the ~200 EN 16931 rules.

// en16931Invoice is the subset of the EN 16931 semantic model the core rules
// examine. Scalar business terms are stored as their raw string values ("" when
// absent); groups are slices.
type en16931Invoice struct {
	specID    string // BT-24 Specification identifier
	number    string // BT-1  Invoice number
	issueDate string // BT-2  Issue date
	typeCode  string // BT-3  Invoice type code
	currency  string // BT-5  Invoice currency code

	sellerName           string // BT-27 Seller name
	buyerName            string // BT-44 Buyer name
	sellerCountry        string // BT-40 Seller country code
	sellerAddressPresent bool   // whether the Seller postal address group (BG-5) is present
	buyerCountry         string // BT-55 Buyer country code

	hasTotals bool           // whether a document monetary summation (BG-22) is present
	totals    monetaryTotals // BG-22 Document totals

	vatBreakdowns []vatBreakdown       // BG-23 VAT breakdown
	allowCharges  []docAllowanceCharge // BG-20 allowances / BG-21 charges
	lines         []invoiceLine        // BG-25 Invoice lines
}

// monetaryTotals holds the document total amounts (BG-22); absent terms are "".
type monetaryTotals struct {
	lineTotal      string // BT-106 Sum of line net amounts
	allowanceTotal string // BT-107 Sum of allowances on document level
	chargeTotal    string // BT-108 Sum of charges on document level
	taxBasisTotal  string // BT-109 Invoice total without VAT
	taxTotal       string // BT-110 Invoice total VAT amount (optional)
	grandTotal     string // BT-112 Invoice total with VAT
	duePayable     string // BT-115 Amount due for payment
}

type vatBreakdown struct {
	basis     string // BT-116 VAT category taxable amount
	calc      string // BT-117 VAT category tax amount
	category  string // BT-118 VAT category code
	rate      string // BT-119 VAT category rate
	hasReason bool   // BT-120 exemption reason or BT-121 exemption reason code present
}

type docAllowanceCharge struct {
	amount    string // BT-92 allowance / BT-99 charge amount
	category  string // BT-95 allowance / BT-102 charge VAT category code
	rate      string // BT-96 allowance / BT-103 charge VAT rate
	hasReason bool   // BT-97/BT-98 or BT-104/BT-105 present
	isCharge  bool   // true = charge (BG-21); false = allowance (BG-20)
}

type invoiceLine struct {
	lineID       string // BT-126 Invoice line identifier
	parentLineID string // EXTENDED sub-invoice-line parent reference
	itemName     string // BT-153 Item name
	netAmount    string // BT-131 Invoice line net amount
	quantity     string // BT-129 Invoiced quantity (trimmed; "" if absent/empty)
	unitCode     string // BT-130 Invoiced quantity unit of measure
	price        string // BT-146 Item net price
	vatCategory  string // BT-151 Invoiced item VAT category code
	vatRate      string // BT-152 Invoiced item VAT rate ("" if absent)
}

// validateEN16931 applies the EN 16931 core business rules to a mapped invoice.
// The rule identifiers, messages and tolerances match the values validators and
// the EN 16931 Schematron report, so the same output holds for either syntax.
func validateEN16931(inv *en16931Invoice, profile FacturXProfile) []FacturXViolation {
	var out []FacturXViolation
	add := func(rule, msg string) { out = append(out, FacturXViolation{Rule: rule, Message: msg}) }
	req := func(rule, msg, val string) {
		if val == "" {
			add(rule, msg)
		}
	}

	// Mandatory document-level business terms (present in every profile).
	req("BR-01", "An Invoice shall have a Specification identifier (BT-24)", inv.specID)
	req("BR-02", "An Invoice shall have an Invoice number (BT-1)", inv.number)
	req("BR-03", "An Invoice shall have an Invoice issue date (BT-2)", inv.issueDate)
	req("BR-04", "An Invoice shall have an Invoice type code (BT-3)", inv.typeCode)
	req("BR-05", "An Invoice shall have an Invoice currency code (BT-5)", inv.currency)
	req("BR-06", "An Invoice shall contain the Seller name (BT-27)", inv.sellerName)
	req("BR-07", "An Invoice shall contain the Buyer name (BT-44)", inv.buyerName)
	// BR-08 is a group-presence test: the Seller postal address (BG-5) element
	// must be present, even if empty. Its content (the country code) is BR-09.
	if !inv.sellerAddressPresent {
		add("BR-08", "An Invoice shall contain the Seller postal address (BG-5)")
	}
	req("BR-09", "The Seller postal address shall contain a Seller country code (BT-40)", inv.sellerCountry)

	req("BR-13", "An Invoice shall have the Invoice total amount without VAT (BT-109)", inv.totals.taxBasisTotal)
	req("BR-14", "An Invoice shall have the Invoice total amount with VAT (BT-112)", inv.totals.grandTotal)
	req("BR-15", "An Invoice shall have the Amount due for payment (BT-115)", inv.totals.duePayable)

	// Code lists (BR-CL-*). The Invoice currency code (BT-5, BR-CL-04) and the
	// Invoice type code (BT-3, BR-CL-01) are checked against the exact EN 16931
	// code lists; the country codes (BT-40/55) against ISO 3166-1 alpha-2 shape.
	if cur := inv.currency; cur != "" && !en16931Currencies[cur] {
		add("BR-CL-04", fmt.Sprintf("Invoice currency code (BT-5=%q) shall be a valid ISO 4217 alpha-3 code", cur))
	}
	if tc := inv.typeCode; tc != "" && !en16931TypeCodes[tc] {
		add("BR-CL-01", fmt.Sprintf("Invoice type code (BT-3=%q) is not a permitted UNTDID 1001 value", tc))
	}
	if c := inv.sellerCountry; c != "" && !isUpperAlpha(c, 2) {
		add("BR-CL-14", fmt.Sprintf("Seller country code (BT-40=%q) shall be a valid ISO 3166-1 code", c))
	}
	if c := inv.buyerCountry; c != "" && !isUpperAlpha(c, 2) {
		add("BR-CL-15", fmt.Sprintf("Buyer country code (BT-55=%q) shall be a valid ISO 3166-1 code", c))
	}

	// Decimals (BR-DEC-*): monetary amounts shall have at most two decimal places.
	if inv.hasTotals {
		for _, fld := range []struct{ rule, name, val string }{
			{"BR-DEC-12", "LineTotalAmount", inv.totals.lineTotal},
			{"BR-DEC-15", "TaxBasisTotalAmount", inv.totals.taxBasisTotal},
			{"BR-DEC-16", "TaxTotalAmount", inv.totals.taxTotal},
			{"BR-DEC-17", "GrandTotalAmount", inv.totals.grandTotal},
			{"BR-DEC-18", "DuePayableAmount", inv.totals.duePayable},
		} {
			if fld.val != "" && decimalCount(fld.val) > 2 {
				add(fld.rule, fmt.Sprintf("amount %s (%q) shall have at most two decimals", fld.name, fld.val))
			}
		}
	}

	// BR-CO-15: Invoice total with VAT (BT-112) = total without VAT (BT-109) +
	// total VAT amount (BT-110).
	if inv.hasTotals {
		basis, okB := parseAmount(inv.totals.taxBasisTotal)
		grand, okG := parseAmount(inv.totals.grandTotal)
		tax, okT := parseAmount(inv.totals.taxTotal)
		if !okT {
			tax = 0 // BT-110 is optional when there is no VAT
		}
		if okB && okG && math.Abs((basis+tax)-grand) > 0.005 {
			add("BR-CO-15", fmt.Sprintf("Invoice total with VAT (BT-112=%.2f) shall equal total without VAT (BT-109=%.2f) + VAT total (BT-110=%.2f)", grand, basis, tax))
		}
	}

	// VAT breakdown (BG-23) rules, applied to each entry present, so profiles
	// without a breakdown (MINIMUM) are naturally skipped.
	var vatTotal float64
	for _, tt := range inv.vatBreakdowns {
		if tt.basis == "" {
			add("BR-45", "Each VAT breakdown (BG-23) shall have a VAT category taxable amount (BT-116)")
		}
		if tt.calc == "" {
			add("BR-46", "Each VAT breakdown (BG-23) shall have a VAT category tax amount (BT-117)")
		}
		if tt.category == "" {
			add("BR-47", "Each VAT breakdown (BG-23) shall be defined through a VAT category code (BT-118)")
		} else if !facturxVATCategories[tt.category] {
			add("BR-CL-17", fmt.Sprintf("VAT category code (BT-118=%q) is not a valid UNCL 5305 value", tt.category))
		}
		// BR-48: a rate is required except for the "Not subject to VAT" category (O).
		if tt.rate == "" && tt.category != "O" {
			add("BR-48", "Each VAT breakdown (BG-23) shall have a VAT category rate (BT-119)")
		}
		// BR-CO-17: BT-117 = BT-116 x (BT-119 / 100), rounded to two decimals.
		b, okB := parseAmount(tt.basis)
		c, okC := parseAmount(tt.calc)
		r, okR := parseAmount(tt.rate)
		if okB && okC && okR && math.Abs(round2(b*r/100)-c) > 0.005 {
			add("BR-CO-17", fmt.Sprintf("VAT category tax amount (BT-117=%.2f) shall equal taxable amount (BT-116=%.2f) x rate (BT-119=%.2f%%)", c, b, r))
		}
		if okC {
			vatTotal += c
		}
	}
	// VAT category rules (BR-S/Z/E/AE/IC/G/O-*): breakdown existence, per-line and
	// per-allowance/charge rate constraints, taxable-amount sums, tax-zero, and
	// exemption-reason presence.
	validateVATCategories(inv, add)
	// BR-CO-14: Invoice total VAT amount (BT-110) = sum of VAT category tax
	// amounts (BT-117), when a breakdown is present.
	if len(inv.vatBreakdowns) > 0 && inv.hasTotals {
		if tax, ok := parseAmount(inv.totals.taxTotal); ok && math.Abs(vatTotal-tax) > 0.005 {
			add("BR-CO-14", fmt.Sprintf("Invoice total VAT (BT-110=%.2f) shall equal the sum of VAT breakdown tax amounts (%.2f)", tax, vatTotal))
		}
	}

	// Document-level allowance (BG-20) and charge (BG-21) rules.
	for _, ac := range inv.allowCharges {
		if ac.isCharge {
			if ac.amount == "" {
				add("BR-36", "Each Document level charge (BG-21) shall have a Document level charge amount (BT-99)")
			}
			if ac.category == "" {
				add("BR-37", "Each Document level charge (BG-21) shall have a Document level charge VAT category code (BT-102)")
			}
			if !ac.hasReason {
				add("BR-38", "Each Document level charge (BG-21) shall have a Document level charge reason (BT-104) or reason code (BT-105)")
			}
		} else {
			if ac.amount == "" {
				add("BR-31", "Each Document level allowance (BG-20) shall have a Document level allowance amount (BT-92)")
			}
			if ac.category == "" {
				add("BR-32", "Each Document level allowance (BG-20) shall have a Document level allowance VAT category code (BT-95)")
			}
			if !ac.hasReason {
				add("BR-33", "Each Document level allowance (BG-20) shall have a Document level allowance reason (BT-97) or reason code (BT-98)")
			}
		}
	}

	// Line-item rules (BG-25). Includes EXTENDED sub-invoice lines (flat siblings
	// distinguished by a parent line reference). A grouping line — one referenced
	// as another line's parent — aggregates its sub-lines and need not carry an
	// invoiced quantity or item price, so those two are checked only on leaves.
	grouping := map[string]bool{}
	for _, li := range inv.lines {
		if li.parentLineID != "" {
			grouping[li.parentLineID] = true
		}
	}
	for i, li := range inv.lines {
		if li.lineID == "" {
			add("BR-21", fmt.Sprintf("Invoice line %d shall have an Invoice line identifier (BT-126)", i+1))
		}
		if li.itemName == "" {
			add("BR-25", "Each Invoice line shall have an Item name (BT-153)")
		}
		if li.netAmount == "" {
			add("BR-24", "Each Invoice line shall have an Invoice line net amount (BT-131)")
		}
		if grouping[li.lineID] {
			continue // grouping line: no direct quantity or price
		}
		if li.quantity == "" {
			add("BR-22", "Each Invoice line shall have an Invoiced quantity (BT-129)")
		} else if li.unitCode == "" {
			add("BR-23", "Each Invoice line shall have an Invoiced quantity unit of measure (BT-130)")
		} else if !facturxUnitCodes[li.unitCode] {
			add("BR-CL-23", fmt.Sprintf("Invoiced quantity unit code (BT-130=%q) is not a valid UNECE Rec 20/21 code", li.unitCode))
		}
		if li.price == "" {
			add("BR-26", "Each Invoice line shall have an Item net price (BT-146)")
		} else if p, ok := parseAmount(li.price); ok && p < 0 {
			add("BR-27", "The Item net price (BT-146) shall not be negative")
		}
	}

	// BR-CO-10: Sum of Invoice line net amounts (BT-106) = sum of line net
	// amounts (BT-131). A sub-invoice line's amount is rolled up into its parent,
	// so only top-level lines are summed.
	if len(inv.lines) > 0 && inv.hasTotals {
		var lineSum float64
		for _, li := range inv.lines {
			if li.parentLineID != "" {
				continue
			}
			if v, ok := parseAmount(li.netAmount); ok {
				lineSum += v
			}
		}
		if lt, ok := parseAmount(inv.totals.lineTotal); ok && math.Abs(round2(lineSum)-lt) > 0.005 {
			add("BR-CO-10", fmt.Sprintf("Sum of Invoice line net amount (BT-106=%.2f) shall equal the sum of line net amounts (%.2f)", lt, lineSum))
		}
	}

	// BR-CO-13: Invoice total without VAT (BT-109) = line total (BT-106) minus
	// the allowance total (BT-107) plus the charge total (BT-108).
	if inv.hasTotals {
		if lt, ok := parseAmount(inv.totals.lineTotal); ok {
			allowances, _ := parseAmount(inv.totals.allowanceTotal)
			charges, _ := parseAmount(inv.totals.chargeTotal)
			if basis, ok := parseAmount(inv.totals.taxBasisTotal); ok &&
				math.Abs(round2(lt-allowances+charges)-basis) > 0.005 {
				add("BR-CO-13", fmt.Sprintf("Invoice total without VAT (BT-109=%.2f) shall equal line total (%.2f) - allowances (%.2f) + charges (%.2f)", basis, lt, allowances, charges))
			}
		}
	}

	_ = profile // profile-specific rules apply per present group, not by gate
	return out
}
