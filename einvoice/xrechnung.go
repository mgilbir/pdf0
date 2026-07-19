package einvoice

import (
	"fmt"
	"strings"
)

// This file validates the XRechnung CIUS (Core Invoice Usage Specification) on
// top of the EN 16931 core. XRechnung is the German public-sector profile: it
// makes several EN 16931-optional terms mandatory (the BR-DE-* rules) and, in its
// EXTENSION and CVD sub-profiles, relaxes a few CEN rules (party/item identifier
// schemes, the amount-due formula). The same syntax-neutral model feeds it, so it
// validates CII (ZUGFeRD/XRechnung-CII) and UBL (XRechnung-UBL) alike.
//
// Not vendored: the KoSIT Schematron and instance test suite are cloned by
// `make cius-oracles` and used only as the FP=0 oracle.

// xrechnungTypeCodes is the restricted UNTDID 1001 set XRechnung permits (BR-DE-17).
var xrechnungTypeCodes = map[string]bool{
	"326": true, "380": true, "384": true, "389": true,
	"381": true, "875": true, "876": true, "877": true,
}

// xrExtItemSchemes are the item standard-identifier schemes the XRechnung
// EXTENSION adds to the ISO 6523 ICD list (BR-CL-21 override).
var xrExtItemSchemes = map[string]bool{"XR01": true, "XR02": true, "XR03": true}

// ValidateXRechnung validates an invoice XML against the XRechnung CIUS: the
// EN 16931 core (with the XRechnung sub-profile overrides applied) plus the
// BR-DE-* rules. It accepts either syntax.
func ValidateXRechnung(xmlData []byte) []Violation {
	inv, err := parseEN16931(xmlData)
	if err != nil {
		return []Violation{{Rule: "syntax", Message: err.Error()}}
	}
	ext := strings.Contains(inv.specID, "extension")
	cvd := strings.Contains(inv.specID, "cvd")

	var out []Violation
	for _, v := range validateEN16931(inv, ProfileXRechnung) {
		switch {
		// The EXTENSION and CVD sub-profiles extend the item identifier code lists;
		// re-checked below against the XRechnung-extended sets.
		case v.Rule == "BR-CL-21" || v.Rule == "BR-CL-13":
			continue
		// The EXTENSION replaces the amount-due formula (BR-CO-16) with BR-DEX-09,
		// which accounts for third-party payments.
		case ext && v.Rule == "BR-CO-16":
			continue
		}
		out = append(out, v)
	}
	// Re-apply the item identifier scheme checks with the XRechnung extensions.
	for _, li := range inv.lines {
		if s := li.stdIDScheme; s != "" && !en16931ICD[s] && !(ext && xrExtItemSchemes[s]) {
			out = append(out, Violation{Rule: "BR-CL-21", Message: fmt.Sprintf("Item standard identifier scheme (%q) is not permitted", s)})
		}
		if l := li.classListID; l != "" && !en16931ItemClassCodes[l] && !(cvd && l == "CVD") {
			out = append(out, Violation{Rule: "BR-CL-13", Message: fmt.Sprintf("Item classification scheme (%q) is not permitted", l)})
		}
	}
	out = append(out, validateXRechnungRules(inv, ext, cvd)...)
	return out
}

// validateXRechnungRules applies the mandatory-term and format rules XRechnung
// adds on top of EN 16931 (the BR-DE-* family).
func validateXRechnungRules(inv *en16931Invoice, ext, cvd bool) []Violation {
	var out []Violation
	add := func(rule, msg string) { out = append(out, Violation{Rule: rule, Message: msg}) }
	req := func(rule, msg, val string) {
		if val == "" {
			add(rule, msg)
		}
	}

	if !inv.paymentInstrPresent {
		add("BR-DE-1", "An Invoice shall contain Payment instructions (BG-16)")
	}
	if !inv.sellerContactPresent {
		add("BR-DE-2", "The Seller contact group (BG-6) shall be provided")
	}
	req("BR-DE-3", "The Seller city (BT-37) shall be provided", inv.sellerCity)
	req("BR-DE-4", "The Seller post code (BT-38) shall be provided", inv.sellerPostCode)
	req("BR-DE-5", "The Seller contact point (BT-41) shall be provided", inv.sellerContactName)
	req("BR-DE-6", "The Seller contact telephone number (BT-42) shall be provided", inv.sellerPhone)
	req("BR-DE-7", "The Seller contact email address (BT-43) shall be provided", inv.sellerEmail)
	req("BR-DE-8", "The Buyer city (BT-52) shall be provided", inv.buyerCity)
	req("BR-DE-9", "The Buyer post code (BT-53) shall be provided", inv.buyerPostCode)
	if inv.deliverToPresent {
		req("BR-DE-10", "The Deliver to city (BT-77) shall be provided when a Deliver to address is present", inv.deliverToCity)
		req("BR-DE-11", "The Deliver to post code (BT-78) shall be provided when a Deliver to address is present", inv.deliverToPostCode)
	}
	for _, b := range inv.vatBreakdowns {
		if b.rate == "" {
			add("BR-DE-14", "The VAT category rate (BT-119) shall be provided in each VAT breakdown")
		}
	}
	req("BR-DE-15", "The Buyer reference (BT-10) shall be provided", inv.buyerReference)

	if tc := inv.typeCode; tc != "" && !xrechnungTypeCodes[tc] {
		add("BR-DE-17", fmt.Sprintf("Invoice type code (BT-3=%q) is not one of the codes XRechnung permits", tc))
	}
	if s := inv.specID; !strings.Contains(s, "kosit") || !strings.Contains(s, "xrechnung") {
		add("BR-DE-21", "The Specification identifier (BT-24) shall be an XRechnung identifier")
	}
	// BR-DE-22: attachment file names shall be unique.
	names := map[string]bool{}
	for _, d := range inv.docRefs {
		if d.filename == "" {
			continue
		}
		if names[d.filename] {
			add("BR-DE-22", fmt.Sprintf("Attachment file name %q is not unique", d.filename))
		}
		names[d.filename] = true
	}
	// BR-DE-27: a telephone number shall contain at least three digits.
	if p := inv.sellerPhone; p != "" && countDigits(p) < 3 {
		add("BR-DE-27", "The Seller contact telephone number (BT-42) shall contain at least three digits")
	}
	// BR-DE-28: an email address shall contain exactly one @.
	if e := inv.sellerEmail; e != "" && strings.Count(e, "@") != 1 {
		add("BR-DE-28", "The Seller contact email address (BT-43) shall contain exactly one @ sign")
	}

	// BR-DE-16: a taxed VAT category requires the Seller VAT / tax registration /
	// tax representative identifier.
	usedTaxed := false
	for _, li := range inv.lines {
		if xrechnungVATCodes[li.vatCategory] {
			usedTaxed = true
		}
	}
	for _, ac := range inv.allowCharges {
		if xrechnungVATCodes[ac.category] {
			usedTaxed = true
		}
	}
	if usedTaxed && !(inv.sellerVATID || inv.sellerTaxReg || inv.taxRepVATID) {
		add("BR-DE-16", "A taxed VAT category requires the Seller VAT identifier, tax registration or tax representative VAT identifier")
	}

	// BR-DE-19/20: the credit-transfer / direct-debit account identifiers shall be
	// a valid IBAN for the SEPA payment means.
	for _, pm := range inv.paymentMeans {
		if pm == "58" && inv.creditAccountID != "" && !validIBAN(inv.creditAccountID) {
			add("BR-DE-19", "The Payment account identifier (BT-84) shall be a valid IBAN for a SEPA credit transfer")
		}
		if pm == "59" && inv.debitedAccount != "" && !validIBAN(inv.debitedAccount) {
			add("BR-DE-20", "The Debited account identifier (BT-91) shall be a valid IBAN for a SEPA direct debit")
		}
	}

	// BR-DE-26: a corrected invoice (type 384) requires a preceding invoice reference.
	if inv.typeCode == "384" && !inv.hasBillingRef {
		add("BR-DE-26", "A corrected invoice (type code 384) shall contain a Preceding Invoice reference (BG-3)")
	}

	// BR-DE-30/31: a direct debit requires the creditor identifier and the debited
	// account identifier.
	if inv.directDebitPresent {
		req("BR-DE-30", "A direct debit (BG-19) shall carry the Bank assigned creditor identifier (BT-90)", inv.creditorID)
		req("BR-DE-31", "A direct debit (BG-19) shall carry the Debited account identifier (BT-91)", inv.debitedAccount)
	}
	return out
}

// xrechnungVATCodes are the taxed VAT category codes that trigger BR-DE-16.
var xrechnungVATCodes = map[string]bool{
	"S": true, "Z": true, "E": true, "AE": true, "K": true, "G": true, "L": true, "M": true,
}

// validIBAN reports whether s is a syntactically valid IBAN (ISO 13616): two
// letters, two check digits, up to 30 alphanumerics, passing the mod-97 check.
func validIBAN(s string) bool {
	s = strings.ToUpper(strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, s))
	if len(s) < 5 || len(s) > 34 {
		return false
	}
	if s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' || s[2] < '0' || s[2] > '9' || s[3] < '0' || s[3] > '9' {
		return false
	}
	// Move the first four characters to the end and reduce mod 97.
	rearranged := s[4:] + s[:4]
	rem := 0
	for _, r := range rearranged {
		var d int
		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= 'A' && r <= 'Z':
			d = int(r-'A') + 10
		default:
			return false
		}
		if d >= 10 {
			rem = (rem*100 + d) % 97
		} else {
			rem = (rem*10 + d) % 97
		}
	}
	return rem == 1
}

// countDigits returns the number of ASCII digits in s.
func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}
