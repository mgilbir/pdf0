package einvoice

import (
	"fmt"
	"math"
)

// This file implements the EN 16931 VAT-category rule families (BR-S/Z/E/AE/IC/
// G/O-*). Each VAT category code (BT-118, and its line/allowance/charge form
// BT-151/95/102) constrains: which VAT breakdown groups must exist (-01), the
// permitted VAT rate on the lines and document allowances/charges that carry it
// (-05/-06/-07), the breakdown taxable-amount sum (-08), the breakdown tax
// amount (-09), and the presence of an exemption reason (-10). "Not subject to
// VAT" (O) is additionally exclusive (BR-O-11..14).

// vatCatSpec captures the EN 16931 constraints of one VAT category code.
type vatCatSpec struct {
	fam           string // rule-id family (S, Z, E, AE, IC, G, O, AF, AG)
	name          string // category name used in messages
	rate          byte   // rate on lines/allowances/charges: '+' >0, 'g' >=0, '0' ==0, '-' absent
	requireReason bool   // breakdown must (true) / must not (false) carry an exemption reason
	taxZero       bool   // breakdown VAT tax amount (BT-117) must be 0 (else tax = taxable x rate)
}

// vatCatSpecs maps the UNCL 5305 category code to its constraints. The taxed
// categories (S, and the Canary/Ceuta-Melilla IGIC/IPSI) group their breakdown
// taxable-amount sums by rate; the others carry a single (zero) rate.
var vatCatSpecs = map[string]vatCatSpec{
	"S":  {"S", "Standard rated", '+', false, false},
	"Z":  {"Z", "Zero rated", '0', false, true},
	"E":  {"E", "Exempt from VAT", '0', true, true},
	"AE": {"AE", "Reverse charge", '0', true, true},
	"K":  {"IC", "Intra-community supply", '0', true, true},
	"G":  {"G", "Export outside the EU", '0', true, true},
	"O":  {"O", "Not subject to VAT", '-', true, true},
	"L":  {"AF", "IGIC", 'g', false, false},
	"M":  {"AG", "IPSI", 'g', false, false},
}

// vatCatByRate reports whether a category's breakdown taxable-amount sum is
// grouped per VAT rate (the categories that permit more than one positive rate).
func vatCatByRate(fam string) bool {
	return fam == "S" || fam == "AF" || fam == "AG"
}

func validateVATCategories(inv *en16931Invoice, add func(rule, msg string)) {
	// Categories actually used on lines and document allowances/charges. Each is
	// checked against the UNCL 5305 category code list (BR-CL-18; the breakdown
	// category is BR-CL-17).
	used := map[string]bool{}
	clSeen := map[string]bool{}
	useCat := func(cat string) {
		if cat == "" {
			return
		}
		used[cat] = true
		if !validEN16931VATCategory(cat) && !clSeen[cat] {
			clSeen[cat] = true
			add("BR-CL-18", fmt.Sprintf("VAT category code (BT-151/95/102=%q) is not a valid UNCL 5305 value", cat))
		}
	}
	lineCats, allowCats, chargeCats := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, li := range inv.lines {
		useCat(li.vatCategory)
		if li.vatCategory != "" {
			lineCats[li.vatCategory] = true
		}
	}
	for _, ac := range inv.allowCharges {
		useCat(ac.category)
		if ac.category != "" {
			if ac.isCharge {
				chargeCats[ac.category] = true
			} else {
				allowCats[ac.category] = true
			}
		}
	}
	// BR-{fam}-02/03/04: VAT-identifier requirements for a line / allowance /
	// charge carrying a category. The standard, zero-rated, exempt, IGIC and IPSI
	// categories need a Seller VAT/tax registration or tax-representative VAT
	// identifier; export needs the Seller or tax-representative VAT identifier;
	// reverse charge and intra-community additionally need a Buyer identifier;
	// "Not subject to VAT" forbids the VAT identifiers.
	validateVATIdentifiers(inv, lineCats, allowCats, chargeCats, add)

	bdCats := map[string]int{}
	for _, b := range inv.vatBreakdowns {
		bdCats[b.category]++
	}

	// BR-{fam}-01: a category used on a line/allowance/charge must appear in the
	// VAT breakdown (BG-23).
	for cat := range used {
		spec, ok := vatCatSpecs[cat]
		if ok && bdCats[cat] == 0 {
			add("BR-"+spec.fam+"-01", fmt.Sprintf("an Invoice with a %q (%s) line, allowance or charge shall contain a VAT breakdown (BG-23) with that category", spec.name, cat))
		}
	}

	// BR-{fam}-05/06/07: the VAT rate on each line / allowance / charge carrying a
	// category must satisfy that category's rate constraint.
	for _, li := range inv.lines {
		checkVATRate(li.vatCategory, li.vatRate, "05", "the Invoiced item VAT rate (BT-152)", add)
	}
	for _, ac := range inv.allowCharges {
		if ac.isCharge {
			checkVATRate(ac.category, ac.rate, "07", "the Document level charge VAT rate (BT-103)", add)
		} else {
			checkVATRate(ac.category, ac.rate, "06", "the Document level allowance VAT rate (BT-96)", add)
		}
	}

	// Breakdown-level rules per category.
	for _, b := range inv.vatBreakdowns {
		spec, ok := vatCatSpecs[b.category]
		if !ok {
			continue
		}
		// -10: exemption reason present exactly when the category requires it.
		if spec.requireReason && !b.hasReason {
			add("BR-"+spec.fam+"-10", fmt.Sprintf("a VAT breakdown with category %q (%s) shall have a VAT exemption reason", spec.name, b.category))
		} else if !spec.requireReason && b.hasReason {
			add("BR-"+spec.fam+"-10", fmt.Sprintf("a VAT breakdown with category %q (%s) shall not have a VAT exemption reason", spec.name, b.category))
		}
		// -09: tax amount is zero for the non-taxed categories; for the standard
		// category it equals taxable amount x rate.
		calc, okC := parseAmount(b.calc)
		if spec.taxZero {
			if okC && math.Abs(calc) > 0.005 {
				add("BR-"+spec.fam+"-09", fmt.Sprintf("the VAT category tax amount (BT-117) for category %q (%s) shall be 0", spec.name, b.category))
			}
		} else {
			basis, okB := parseAmount(b.basis)
			rate, okR := parseAmount(b.rate)
			if okB && okC && okR && math.Abs(round2(basis*rate/100)-calc) >= vatAmountTolerance {
				add("BR-"+spec.fam+"-09", fmt.Sprintf("the VAT category tax amount (BT-117=%.2f) for category %q shall equal taxable amount x rate", calc, b.category))
			}
		}
	}

	// BR-{fam}-08: each breakdown's taxable amount equals the sum of matching
	// line net amounts + charges - allowances of the same category (and, for the
	// standard category, the same rate). Checked only when it can be computed
	// reliably: at least one line, every line/allowance/charge carrying a
	// category, no EXTENDED sub-line roll-up, and no document-level allowances or
	// charges (which some producers carry only in the summation totals, not as
	// itemizable BG-20/21 entries, so summing entries would understate the base).
	if len(inv.lines) > 0 && vatCategoriesComplete(inv) && !hasSubLines(inv) && !hasDocAllowanceCharge(inv) {
		validateVATTaxableSums(inv, add)
	}

	// BR-B-01/02: the "Split payment" category (B), an Italian domestic mechanism.
	validateSplitPayment(inv, add)

	// BR-O-11..14: "Not subject to VAT" (O) is exclusive of every other category.
	if bdCats["O"] > 0 {
		if len(bdCats) > 1 {
			add("BR-O-11", "an Invoice with a \"Not subject to VAT\" (O) VAT breakdown shall not contain other VAT breakdown groups")
		}
		for _, li := range inv.lines {
			if li.vatCategory != "" && li.vatCategory != "O" {
				add("BR-O-12", "an Invoice with a \"Not subject to VAT\" (O) VAT breakdown shall not contain a line with another VAT category")
				break
			}
		}
		var badAllow, badCharge bool
		for _, ac := range inv.allowCharges {
			if ac.category != "" && ac.category != "O" {
				if ac.isCharge {
					badCharge = true
				} else {
					badAllow = true
				}
			}
		}
		if badAllow {
			add("BR-O-13", "an Invoice with a \"Not subject to VAT\" (O) VAT breakdown shall not contain an allowance with another VAT category")
		}
		if badCharge {
			add("BR-O-14", "an Invoice with a \"Not subject to VAT\" (O) VAT breakdown shall not contain a charge with another VAT category")
		}
	}
}

// validateSplitPayment applies the "Split payment" VAT category rules (BR-B-01/
// 02). Category B (an Italian domestic mechanism) has no breakdown/rate/reason
// family of its own, only these two constraints, so it is handled here rather
// than through vatCatSpecs. hasB/hasS consider lines, document allowances and
// charges, and the VAT breakdown — matching the Schematron bindings.
func validateSplitPayment(inv *en16931Invoice, add func(rule, msg string)) {
	hasB, hasS := false, false
	note := func(cat string) {
		switch cat {
		case "B":
			hasB = true
		case "S":
			hasS = true
		}
	}
	for _, li := range inv.lines {
		note(li.vatCategory)
	}
	for _, ac := range inv.allowCharges {
		note(ac.category)
	}
	for _, b := range inv.vatBreakdowns {
		note(b.category)
	}
	if !hasB {
		return
	}
	// BR-B-01: an Invoice using "Split payment" shall be domestic Italian — every
	// country code present shall be IT. We check the country codes we extract
	// (Seller BT-40, Buyer BT-55, tax representative BT-69, Deliver-to BT-80); a
	// non-IT value among them is a definite violation.
	for _, c := range []string{inv.sellerCountry, inv.buyerCountry, inv.taxRepCountry, inv.deliverToCountry} {
		if c != "" && c != "IT" {
			add("BR-B-01", "an Invoice using the \"Split payment\" (B) VAT category shall be a domestic Italian invoice (all country codes shall be IT)")
			break
		}
	}
	// BR-B-02: "Split payment" (B) and "Standard rated" (S) are mutually exclusive.
	if hasS {
		add("BR-B-02", "an Invoice using the \"Split payment\" (B) VAT category shall not also use the \"Standard rated\" (S) category")
	}
}

// validateVATIdentifiers applies the per-category VAT-identifier requirements
// (BR-{fam}-02 for lines, -03 for allowances, -04 for charges).
func validateVATIdentifiers(inv *en16931Invoice, lineCats, allowCats, chargeCats map[string]bool, add func(rule, msg string)) {
	sellerAny := inv.sellerVATID || inv.sellerTaxReg || inv.taxRepVATID
	sellerVATorRep := inv.sellerVATID || inv.taxRepVATID
	// idFail reports whether a category's identifier requirement is unmet.
	idFail := func(cat string) (bool, bool) {
		switch cat {
		case "S", "Z", "E", "L", "M": // seller VAT / tax registration / tax rep
			return !sellerAny, true
		case "G": // seller or tax representative VAT identifier
			return !sellerVATorRep, true
		case "AE": // seller identifier and a buyer identifier
			return !(sellerAny && (inv.buyerVATID || inv.buyerLegalReg)), true
		case "K": // seller/tax-rep VAT and buyer VAT identifier
			return !(sellerVATorRep && inv.buyerVATID), true
		case "O": // must NOT contain seller/tax-rep/buyer VAT identifiers
			return inv.sellerVATID || inv.taxRepVATID || inv.buyerVATID, true
		}
		return false, false
	}
	emit := func(cats map[string]bool, suffix string) {
		for cat := range cats {
			spec, ok := vatCatSpecs[cat]
			if !ok {
				continue
			}
			if fail, applies := idFail(cat); applies && fail {
				add("BR-"+spec.fam+"-"+suffix, fmt.Sprintf("the VAT identifier requirement for category %q (%s) is not met", spec.name, cat))
			}
		}
	}
	emit(lineCats, "02")
	emit(allowCats, "03")
	emit(chargeCats, "04")
}

// checkVATRate enforces a category's rate constraint on one line/allowance/charge.
func checkVATRate(cat, rate, suffix, label string, add func(rule, msg string)) {
	spec, ok := vatCatSpecs[cat]
	if !ok {
		return
	}
	switch spec.rate {
	case '+': // must be greater than zero
		r, ok := parseAmount(rate)
		if rate == "" || (ok && r <= 0) {
			add("BR-"+spec.fam+"-"+suffix, fmt.Sprintf("for category %q (%s), %s shall be greater than zero", spec.name, cat, label))
		}
	case 'g': // must be present and zero or greater
		r, ok := parseAmount(rate)
		if rate == "" || (ok && r < 0) {
			add("BR-"+spec.fam+"-"+suffix, fmt.Sprintf("for category %q (%s), %s shall be zero or greater", spec.name, cat, label))
		}
	case '0': // must be zero
		if r, ok := parseAmount(rate); ok && math.Abs(r) > 0.005 {
			add("BR-"+spec.fam+"-"+suffix, fmt.Sprintf("for category %q (%s), %s shall be 0", spec.name, cat, label))
		}
	case '-': // must be absent
		if rate != "" {
			add("BR-"+spec.fam+"-"+suffix, fmt.Sprintf("for category %q (%s), %s shall not be present", spec.name, cat, label))
		}
	}
}

// vatCategoriesComplete reports whether every line and document allowance/charge
// carries a VAT category code (the precondition for the -08 sum checks).
func vatCategoriesComplete(inv *en16931Invoice) bool {
	for _, li := range inv.lines {
		if li.parentLineID == "" && li.vatCategory == "" {
			return false
		}
	}
	for _, ac := range inv.allowCharges {
		if ac.category == "" {
			return false
		}
	}
	return true
}

func hasSubLines(inv *en16931Invoice) bool {
	for _, li := range inv.lines {
		if li.parentLineID != "" {
			return true
		}
	}
	return false
}

// hasDocAllowanceCharge reports whether the invoice carries any document-level
// allowance or charge, whether as a BG-20/21 entry or only via a non-zero
// allowance/charge summation total (BT-107/BT-108).
func hasDocAllowanceCharge(inv *en16931Invoice) bool {
	if len(inv.allowCharges) > 0 {
		return true
	}
	if v, ok := parseAmount(inv.totals.allowanceTotal); ok && math.Abs(v) > 0.005 {
		return true
	}
	if v, ok := parseAmount(inv.totals.chargeTotal); ok && math.Abs(v) > 0.005 {
		return true
	}
	return false
}

// validateVATTaxableSums checks BR-{fam}-08 for every breakdown of a known
// category. For the standard category the sum is grouped by VAT rate; the other
// categories have a single (zero) rate.
func validateVATTaxableSums(inv *en16931Invoice, add func(rule, msg string)) {
	for _, b := range inv.vatBreakdowns {
		spec, ok := vatCatSpecs[b.category]
		if !ok {
			continue
		}
		basis, okBasis := parseAmount(b.basis)
		if !okBasis {
			continue
		}
		byRate := vatCatByRate(spec.fam)
		bRate, _ := parseAmount(b.rate)
		sum, complete := 0.0, true
		match := func(cat, rate string) bool {
			if cat != b.category {
				return false
			}
			if byRate {
				r, ok := parseAmount(rate)
				return ok && math.Abs(r-bRate) < 0.005
			}
			return true
		}
		for _, li := range inv.lines {
			if match(li.vatCategory, li.vatRate) {
				v, ok := parseAmount(li.netAmount)
				if !ok {
					complete = false
					break
				}
				sum += v
			}
		}
		for _, ac := range inv.allowCharges {
			if complete && match(ac.category, ac.rate) {
				v, ok := parseAmount(ac.amount)
				if !ok {
					complete = false
					break
				}
				if ac.isCharge {
					sum += v
				} else {
					sum -= v
				}
			}
		}
		if complete && math.Abs(round2(sum)-basis) > 0.005 {
			add("BR-"+spec.fam+"-08", fmt.Sprintf("the VAT category taxable amount (BT-116=%.2f) for category %q shall equal the sum of matching line net amounts + charges - allowances (%.2f)", basis, b.category, sum))
		}
	}
}
