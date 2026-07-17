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
	buyerAddressPresent  bool   // whether the Buyer postal address group (BG-8) is present

	sellerVATID   bool // BT-31 Seller VAT identifier present
	sellerTaxReg  bool // BT-32 Seller tax registration identifier present
	taxRepVATID   bool // BT-63 Seller tax representative VAT identifier present
	buyerVATID    bool // BT-48 Buyer VAT identifier present
	buyerLegalReg bool // BT-47 Buyer legal registration identifier present

	sellerEndpointScheme string   // BT-34 Seller electronic address scheme
	buyerEndpointScheme  string   // BT-49 Buyer electronic address scheme
	paymentMeans         []string // BT-81 Payment means type codes

	taxRepPresent        bool   // BG-11 Seller tax representative party present
	taxRepName           string // BT-62 Seller tax representative name
	taxRepAddressPresent bool   // BG-12 Seller tax representative postal address present
	taxRepCountry        string // BT-69 Tax representative country code
	payeePresent         bool   // BG-10 Payee present
	payeeName            string // BT-59 Payee name
	deliverToPresent     bool   // BG-15 Deliver-to address present
	deliverToCountry     string // BT-80 Deliver-to country code

	paymentInstrPresent  bool   // BG-16 Payment instructions present
	creditAccountPresent bool   // BG-17 Credit transfer (payee financial account) present
	creditAccountID      string // BT-84 Payment account identifier
	taxCurrency          string // BT-6 VAT accounting currency code
	vatInTaxCurrency     bool   // BT-111 VAT total in accounting currency present

	period invoicePeriod // BG-14 Invoicing period

	docRefs        []docReference // BG-24 Additional supporting documents
	billingRefNoID bool           // a Preceding invoice reference (BG-3) missing its ID (BT-25)

	hasTotals bool           // whether a document monetary summation (BG-22) is present
	totals    monetaryTotals // BG-22 Document totals

	vatBreakdowns []vatBreakdown       // BG-23 VAT breakdown
	allowCharges  []docAllowanceCharge // BG-20 allowances / BG-21 charges
	lines         []invoiceLine        // BG-25 Invoice lines
}

// monetaryTotals holds the document total amounts (BG-22); absent terms are "".
type monetaryTotals struct {
	lineTotal       string // BT-106 Sum of line net amounts
	allowanceTotal  string // BT-107 Sum of allowances on document level
	chargeTotal     string // BT-108 Sum of charges on document level
	taxBasisTotal   string // BT-109 Invoice total without VAT
	taxTotal        string // BT-110 Invoice total VAT amount (optional)
	grandTotal      string // BT-112 Invoice total with VAT
	paidAmount      string // BT-113 Paid amount
	payableRounding string // BT-114 Rounding amount
	duePayable      string // BT-115 Amount due for payment
}

type vatBreakdown struct {
	basis      string // BT-116 VAT category taxable amount
	calc       string // BT-117 VAT category tax amount
	category   string // BT-118 VAT category code
	rate       string // BT-119 VAT category rate
	hasReason  bool   // BT-120 exemption reason or BT-121 exemption reason code present
	reasonCode string // BT-121 VAT exemption reason code
}

type docAllowanceCharge struct {
	amount     string // BT-92 allowance / BT-99 charge amount
	category   string // BT-95 allowance / BT-102 charge VAT category code
	rate       string // BT-96 allowance / BT-103 charge VAT rate
	hasReason  bool   // BT-97/BT-98 or BT-104/BT-105 present
	reasonCode string // BT-98 allowance / BT-105 charge reason code
	isCharge   bool   // true = charge (BG-21); false = allowance (BG-20)
}

type invoiceLine struct {
	lineID        string // BT-126 Invoice line identifier
	parentLineID  string // EXTENDED sub-invoice-line parent reference
	itemName      string // BT-153 Item name
	netAmount     string // BT-131 Invoice line net amount
	quantity      string // BT-129 Invoiced quantity (trimmed; "" if absent/empty)
	unitCode      string // BT-130 Invoiced quantity unit of measure
	price         string // BT-146 Item net price
	vatCategory   string // BT-151 Invoiced item VAT category code
	vatRate       string // BT-152 Invoiced item VAT rate ("" if absent)
	originCountry string // BT-159 Item country of origin
	allowCharges  []lineAllowanceCharge
	period        invoicePeriod // BG-26 Invoice line period

	grossPrice   string // BT-148 Item gross price
	itemAttrBad  bool   // an Item attribute (BG-32) missing its name or value
	stdIDPresent bool   // BT-157 Item standard identifier present
	stdIDScheme  string // BT-157 scheme identifier
	classPresent bool   // BT-158 Item classification identifier present
	classListID  string // BT-158 classification scheme (list) identifier
}

// docReference is an Additional supporting document (BG-24).
type docReference struct {
	hasID    bool   // BT-122 Supporting document reference present
	mimeCode string // attachment MIME code
}

// invoicePeriod is a billing period (BG-14 at document level, BG-26 per line).
type invoicePeriod struct {
	present bool   // whether the period group is present
	start   string // start date (BT-73 / BT-134)
	end     string // end date (BT-74 / BT-135)
	desc    string // BT-8 VAT point date code (UBL carries it in the period group)
}

// lineAllowanceCharge is an Invoice line allowance (BG-27) or charge (BG-28).
type lineAllowanceCharge struct {
	amount    string // BT-136 allowance / BT-141 charge amount
	hasReason bool   // BT-139/BT-140 or BT-144/BT-145 present
	isCharge  bool   // true = charge (BG-28); false = allowance (BG-27)
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
	// The Buyer postal address (BG-8) is not mandatory in the reduced MINIMUM CIUS.
	if profile != FacturXMinimum {
		if !inv.buyerAddressPresent {
			add("BR-10", "An Invoice shall contain the Buyer postal address (BG-8)")
		}
		req("BR-11", "The Buyer postal address shall contain a Buyer country code (BT-55)", inv.buyerCountry)
	}

	// Conditional party groups. A Seller tax representative (BG-11), Payee (BG-10)
	// or Deliver-to address (BG-15), when present, must carry its mandatory terms.
	if inv.taxRepPresent {
		req("BR-18", "The Seller tax representative name (BT-62) shall be provided when a tax representative party is present", inv.taxRepName)
		if !inv.taxRepAddressPresent {
			add("BR-19", "The Seller tax representative postal address (BG-12) shall be provided when a tax representative party is present")
		}
		if !inv.taxRepVATID {
			add("BR-56", "Each Seller tax representative party (BG-11) shall have a Seller tax representative VAT identifier (BT-63)")
		}
	}
	if inv.taxRepAddressPresent {
		req("BR-20", "The Seller tax representative postal address (BG-12) shall contain a Tax representative country code (BT-69)", inv.taxRepCountry)
	}
	if inv.payeePresent {
		req("BR-17", "The Payee name (BT-59) shall be provided when the Payee (BG-10) is present", inv.payeeName)
	}
	if inv.deliverToPresent {
		req("BR-57", "Each Deliver to address (BG-15) shall contain a Deliver to country code (BT-80)", inv.deliverToCountry)
	}

	// Payment instructions (BG-16/17).
	if inv.paymentInstrPresent && len(inv.paymentMeans) == 0 {
		add("BR-49", "A Payment instruction (BG-16) shall specify the Payment means type code (BT-81)")
	}
	if inv.creditAccountPresent && inv.creditAccountID == "" {
		add("BR-50", "A Payment account identifier (BT-84) shall be present when Credit transfer (BG-17) information is provided")
	}
	for _, pm := range inv.paymentMeans {
		if (pm == "30" || pm == "58") && inv.creditAccountID == "" {
			add("BR-61", "A credit-transfer payment means (BT-81) requires a Payment account identifier (BT-84)")
			break
		}
	}
	// BR-53: a VAT accounting currency (BT-6) requires the VAT total in that
	// currency (BT-111); BR-CL-05 validates the code itself.
	if inv.taxCurrency != "" {
		if !inv.vatInTaxCurrency {
			add("BR-53", "When a VAT accounting currency code (BT-6) is present, the Invoice total VAT amount in accounting currency (BT-111) shall be provided")
		}
		if !en16931Currencies[inv.taxCurrency] {
			add("BR-CL-05", fmt.Sprintf("VAT accounting currency code (BT-6=%q) shall be a valid ISO 4217 code", inv.taxCurrency))
		}
	}

	// Item detail: gross price (BR-28), item attributes (BR-54), and the item
	// standard/classification identifiers and their scheme code lists (BR-64/65,
	// BR-CL-21/13). Each identifier check is conditional on the element's presence.
	for _, li := range inv.lines {
		if gp := li.grossPrice; gp != "" {
			if p, ok := parseAmount(gp); ok && p < 0 {
				add("BR-28", "The Item gross price (BT-148) shall not be negative")
			}
		}
		if li.itemAttrBad {
			add("BR-54", "Each Item attribute (BG-32) shall contain an Item attribute name (BT-160) and value (BT-161)")
		}
		if li.stdIDPresent && li.stdIDScheme == "" {
			add("BR-64", "The Item standard identifier (BT-157) shall have a Scheme identifier")
		}
		if s := li.stdIDScheme; s != "" && !en16931ICD[s] {
			add("BR-CL-21", fmt.Sprintf("Item standard identifier scheme (%q) shall belong to the ISO 6523 ICD list", s))
		}
		if li.classPresent && li.classListID == "" {
			add("BR-65", "The Item classification identifier (BT-158) shall have a Scheme identifier")
		}
		if l := li.classListID; l != "" && !en16931ItemClassCodes[l] {
			add("BR-CL-13", fmt.Sprintf("Item classification scheme (%q) shall be a valid UNTDID 7143 value", l))
		}
	}

	// Supporting documents (BR-52, mime BR-CL-24), preceding invoice references
	// (BR-55) and the VAT point date code (BR-CL-06).
	for _, d := range inv.docRefs {
		if !d.hasID {
			add("BR-52", "Each Additional supporting document (BG-24) shall contain a Supporting document reference (BT-122)")
		}
		if m := d.mimeCode; m != "" && !en16931MIME[m] {
			add("BR-CL-24", fmt.Sprintf("Attachment MIME code (%q) is not a permitted value", m))
		}
	}
	if inv.billingRefNoID {
		add("BR-55", "Each Preceding Invoice reference (BG-3) shall contain a Preceding Invoice reference (BT-25)")
	}
	if c := inv.period.desc; c != "" && c != "3" && c != "35" && c != "432" {
		add("BR-CL-06", fmt.Sprintf("Value added tax point date code (BT-8=%q) shall be a restriction of UNTDID 2005", c))
	}

	// Full-invoice profiles carry lines and a line-net total; the head-only
	// Factur-X CIUS (MINIMUM, BASIC WL) legitimately omit both, so gate the
	// line-presence rules to profiles that carry lines.
	headOnly := profile == FacturXMinimum || profile == FacturXBasicWL
	if !headOnly {
		if len(inv.lines) == 0 {
			add("BR-16", "An Invoice shall have at least one Invoice line (BG-25)")
		}
		req("BR-12", "An Invoice shall have the Sum of Invoice line net amount (BT-106)", inv.totals.lineTotal)
	}
	// BR-CO-18: at least one VAT breakdown group. MINIMUM carries only totals, no
	// breakdown, so it is exempt.
	if profile != FacturXMinimum && len(inv.vatBreakdowns) == 0 {
		add("BR-CO-18", "An Invoice shall at least have one VAT breakdown group (BG-23)")
	}

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
	// BR-CL-14 covers every postal-address country code (a cac:Country); BR-CL-15
	// is the Item country of origin (BT-159, a cac:OriginCountry).
	for _, c := range []struct{ term, val string }{
		{"Seller country code (BT-40)", inv.sellerCountry},
		{"Buyer country code (BT-55)", inv.buyerCountry},
	} {
		if c.val != "" && !en16931Countries[c.val] {
			add("BR-CL-14", fmt.Sprintf("%s=%q shall be a valid ISO 3166-1 code", c.term, c.val))
		}
	}
	for _, li := range inv.lines {
		if oc := li.originCountry; oc != "" && !en16931Countries[oc] {
			add("BR-CL-15", fmt.Sprintf("Item country of origin (BT-159=%q) shall be a valid ISO 3166-1 code", oc))
		}
	}
	// BR-CL-16: payment means (BT-81) against UNCL 4461.
	for _, pm := range inv.paymentMeans {
		if !en16931PaymentMeans[pm] {
			add("BR-CL-16", fmt.Sprintf("Payment means type code (BT-81=%q) is not a valid UNCL 4461 value", pm))
		}
	}
	// BR-CL-25: the Seller/Buyer electronic address scheme (BT-34/49) against the
	// CEF Electronic Address Scheme code list.
	if s := inv.sellerEndpointScheme; s != "" && !en16931EAS[s] {
		add("BR-CL-25", fmt.Sprintf("Seller electronic address scheme (BT-34=%q) is not a valid EAS value", s))
	}
	if s := inv.buyerEndpointScheme; s != "" && !en16931EAS[s] {
		add("BR-CL-25", fmt.Sprintf("Buyer electronic address scheme (BT-49=%q) is not a valid EAS value", s))
	}
	// BR-CL-22: the VAT exemption reason code (BT-121) against the CEF VATEX list.
	for _, tt := range inv.vatBreakdowns {
		if rc := tt.reasonCode; rc != "" && !en16931VATEX[rc] {
			add("BR-CL-22", fmt.Sprintf("VAT exemption reason code (BT-121=%q) is not a valid VATEX value", rc))
		}
	}

	// Decimals (BR-DEC-*): monetary amounts shall have at most two decimal places.
	dec := func(rule, name, val string) {
		if val != "" && decimalCount(val) > 2 {
			add(rule, fmt.Sprintf("amount %s (%q) shall have at most two decimals", name, val))
		}
	}
	if inv.hasTotals {
		dec("BR-DEC-09", "Sum of line net amounts (BT-106)", inv.totals.lineTotal)
		dec("BR-DEC-10", "Sum of allowances on document level (BT-107)", inv.totals.allowanceTotal)
		dec("BR-DEC-11", "Sum of charges on document level (BT-108)", inv.totals.chargeTotal)
		dec("BR-DEC-12", "Invoice total without VAT (BT-109)", inv.totals.taxBasisTotal)
		dec("BR-DEC-13", "Invoice total VAT amount (BT-110)", inv.totals.taxTotal)
		dec("BR-DEC-14", "Invoice total with VAT (BT-112)", inv.totals.grandTotal)
		dec("BR-DEC-16", "Paid amount (BT-113)", inv.totals.paidAmount)
		dec("BR-DEC-17", "Rounding amount (BT-114)", inv.totals.payableRounding)
		dec("BR-DEC-18", "Amount due for payment (BT-115)", inv.totals.duePayable)
	}
	for _, tt := range inv.vatBreakdowns {
		dec("BR-DEC-19", "VAT category taxable amount (BT-116)", tt.basis)
		dec("BR-DEC-20", "VAT category tax amount (BT-117)", tt.calc)
	}
	for _, li := range inv.lines {
		dec("BR-DEC-23", "Invoice line net amount (BT-131)", li.netAmount)
	}
	for _, ac := range inv.allowCharges {
		if ac.isCharge {
			dec("BR-DEC-05", "Document level charge amount (BT-99)", ac.amount)
		} else {
			dec("BR-DEC-01", "Document level allowance amount (BT-92)", ac.amount)
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
		} else if !en16931VATCategories[tt.category] {
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
	if len(inv.vatBreakdowns) > 0 {
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
				add("BR-CO-22", "Each Document level charge (BG-21) shall contain a Document level charge reason (BT-104) or reason code (BT-105)")
			}
			if rc := ac.reasonCode; rc != "" && !en16931ChargeReasons[rc] {
				add("BR-CL-20", fmt.Sprintf("Document level charge reason code (BT-105=%q) is not a valid UNCL 7161 value", rc))
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
				add("BR-CO-21", "Each Document level allowance (BG-20) shall contain a Document level allowance reason (BT-97) or reason code (BT-98)")
			}
			if rc := ac.reasonCode; rc != "" && !en16931AllowanceReasons[rc] {
				add("BR-CL-19", fmt.Sprintf("Document level allowance reason code (BT-98=%q) is not a valid UNCL 5189 value", rc))
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
			continue // grouping line: no direct quantity, price or item VAT category
		}
		if li.vatCategory == "" {
			add("BR-CO-04", "Each Invoice line (BG-25) shall be categorized with an Invoiced item VAT category code (BT-151)")
		}
		if li.quantity == "" {
			add("BR-22", "Each Invoice line shall have an Invoiced quantity (BT-129)")
		} else if li.unitCode == "" {
			add("BR-23", "Each Invoice line shall have an Invoiced quantity unit of measure (BT-130)")
		} else if !en16931Units[li.unitCode] {
			add("BR-CL-23", fmt.Sprintf("Invoiced quantity unit code (BT-130=%q) is not a valid UNECE Rec 20/21 code", li.unitCode))
		}
		if li.price == "" {
			add("BR-26", "Each Invoice line shall have an Item net price (BT-146)")
		} else if p, ok := parseAmount(li.price); ok && p < 0 {
			add("BR-27", "The Item net price (BT-146) shall not be negative")
		}
		// Invoice line allowances (BG-27) and charges (BG-28).
		for _, ac := range li.allowCharges {
			if ac.isCharge {
				if ac.amount == "" {
					add("BR-43", "Each Invoice line charge (BG-28) shall have an Invoice line charge amount (BT-141)")
				} else {
					dec("BR-DEC-27", "Invoice line charge amount (BT-141)", ac.amount)
				}
				if !ac.hasReason {
					add("BR-44", "Each Invoice line charge (BG-28) shall have an Invoice line charge reason (BT-144) or reason code (BT-145)")
					add("BR-CO-24", "Each Invoice line charge (BG-28) shall contain an Invoice line charge reason (BT-144) or reason code (BT-145)")
				}
			} else {
				if ac.amount == "" {
					add("BR-41", "Each Invoice line allowance (BG-27) shall have an Invoice line allowance amount (BT-136)")
				} else {
					dec("BR-DEC-24", "Invoice line allowance amount (BT-136)", ac.amount)
				}
				if !ac.hasReason {
					add("BR-42", "Each Invoice line allowance (BG-27) shall have an Invoice line allowance reason (BT-139) or reason code (BT-140)")
					add("BR-CO-23", "Each Invoice line allowance (BG-27) shall contain an Invoice line allowance reason (BT-139) or reason code (BT-140)")
				}
			}
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

	// BR-CO-11/12: the allowance (BT-107) and charge (BT-108) totals equal the sum
	// of the document-level allowance (BT-92) and charge (BT-99) amounts. Some
	// EXTENDED producers carry amounts in the totals without an itemizable BG-20/21
	// entry, so these are checked only when every such amount is itemized — i.e.
	// outside the EXTENDED profile.
	if inv.hasTotals && profile != FacturXExtended {
		var allowSum, chargeSum float64
		for _, ac := range inv.allowCharges {
			if v, ok := parseAmount(ac.amount); ok {
				if ac.isCharge {
					chargeSum += v
				} else {
					allowSum += v
				}
			}
		}
		if at, ok := parseAmount(inv.totals.allowanceTotal); ok && math.Abs(round2(allowSum)-at) > 0.005 {
			add("BR-CO-11", fmt.Sprintf("Sum of allowances on document level (BT-107=%.2f) shall equal the sum of Document level allowance amounts (%.2f)", at, allowSum))
		}
		if ct, ok := parseAmount(inv.totals.chargeTotal); ok && math.Abs(round2(chargeSum)-ct) > 0.005 {
			add("BR-CO-12", fmt.Sprintf("Sum of charges on document level (BT-108=%.2f) shall equal the sum of Document level charge amounts (%.2f)", ct, chargeSum))
		}
	}

	// Invoicing period (BG-14) and Invoice line period (BG-26): when present, a
	// start or end date is required (BR-CO-19/20), and a given end must not precede
	// a given start (BR-29/30).
	checkPeriod := func(present, order string, p invoicePeriod) {
		if !p.present {
			return
		}
		// A period group carrying only a VAT point date code (BT-8) is not an
		// invoicing period in the BR-CO-19/20 sense.
		if p.start == "" && p.end == "" && p.desc == "" {
			add(present, "if an invoicing period (BG-14/BG-26) is used, its start or end date shall be present")
		}
		if p.start != "" && p.end != "" && normDate(p.end) < normDate(p.start) {
			add(order, "the invoicing period end date shall not precede its start date")
		}
	}
	checkPeriod("BR-CO-19", "BR-29", inv.period)
	for _, li := range inv.lines {
		checkPeriod("BR-CO-20", "BR-30", li.period)
	}

	// BR-CO-16: Amount due for payment (BT-115) = Invoice total with VAT (BT-112)
	// - Paid amount (BT-113) + Rounding amount (BT-114). MINIMUM omits the paid
	// amount from its reduced summation, so its due may differ without a modeled
	// prepaid; exempt it.
	if inv.hasTotals && profile != FacturXMinimum {
		grand, okG := parseAmount(inv.totals.grandTotal)
		due, okD := parseAmount(inv.totals.duePayable)
		paid, _ := parseAmount(inv.totals.paidAmount)
		rounding, _ := parseAmount(inv.totals.payableRounding)
		if okG && okD && math.Abs(round2(grand-paid+rounding)-due) > 0.005 {
			add("BR-CO-16", fmt.Sprintf("Amount due for payment (BT-115=%.2f) shall equal total with VAT (BT-112=%.2f) - paid (BT-113=%.2f) + rounding (BT-114=%.2f)", due, grand, paid, rounding))
		}
	}

	return out
}
