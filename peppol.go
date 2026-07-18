package pdf0

import (
	"fmt"
	"regexp"
	"strings"
)

// This file validates the OpenPEPPOL BIS Billing 3.0 CIUS on top of the EN 16931
// core. Peppol is the pan-European exchange profile; its rules (PEPPOL-EN16931-*)
// mandate the electronic addresses and business process, restrict identifiers,
// and tie VAT exemption reason codes to their categories. The same syntax-neutral
// model feeds it, so it validates CII and UBL alike.
//
// Not vendored: the OpenPEPPOL Schematron and examples are cloned by
// `make cius-oracles` and used only as the FP=0 oracle.

// peppolSpecID is the Specification identifier a Peppol BIS 3 invoice must carry.
const peppolSpecID = "urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0"

// peppolProfileRE is the required business-process identifier format (BT-23).
var peppolProfileRE = regexp.MustCompile(`^urn:fdc:peppol\.eu:2017:poacc:billing:[0-9]{2}:1\.0$`)

// peppolVATEX maps a CEF VAT exemption reason code to the rule id and the VAT
// category it forces (PEPPOL-EN16931-P0104..P0111).
var peppolVATEX = map[string]struct {
	rule, category string
}{
	"VATEX-EU-G":  {"PEPPOL-EN16931-P0104", "G"},
	"VATEX-EU-O":  {"PEPPOL-EN16931-P0105", "O"},
	"VATEX-EU-IC": {"PEPPOL-EN16931-P0106", "K"},
	"VATEX-EU-AE": {"PEPPOL-EN16931-P0107", "AE"},
	"VATEX-EU-D":  {"PEPPOL-EN16931-P0108", "E"},
	"VATEX-EU-F":  {"PEPPOL-EN16931-P0109", "E"},
	"VATEX-EU-I":  {"PEPPOL-EN16931-P0110", "E"},
	"VATEX-EU-J":  {"PEPPOL-EN16931-P0111", "E"},
}

// ValidatePeppol validates an invoice XML against the OpenPEPPOL BIS Billing 3.0
// CIUS: the EN 16931 core plus the Peppol-specific rules. It accepts either syntax.
func ValidatePeppol(xmlData []byte) []FacturXViolation {
	inv, err := parseEN16931(xmlData)
	if err != nil {
		return []FacturXViolation{{Rule: "syntax", Message: err.Error()}}
	}
	out := validateEN16931(inv, FacturXEN16931)
	out = append(out, validatePeppolRules(inv)...)
	return out
}

func validatePeppolRules(inv *en16931Invoice) []FacturXViolation {
	var out []FacturXViolation
	add := func(rule, msg string) { out = append(out, FacturXViolation{Rule: rule, Message: msg}) }

	// Business process (BT-23) and specification identifier (BT-24).
	if inv.profileID == "" {
		add("PEPPOL-EN16931-R001", "The Business process (BT-23) MUST be provided")
	} else if !peppolProfileRE.MatchString(strings.TrimSpace(inv.profileID)) {
		add("PEPPOL-EN16931-R007", "The Business process MUST be 'urn:fdc:peppol.eu:2017:poacc:billing:NN:1.0'")
	}
	if !strings.HasPrefix(strings.TrimSpace(inv.specID), peppolSpecID) {
		add("PEPPOL-EN16931-R004", "The Specification identifier (BT-24) MUST be the Peppol BIS Billing 3.0 identifier")
	}
	// R003: a Buyer reference or purchase order reference.
	if inv.buyerReference == "" && inv.orderRef == "" {
		add("PEPPOL-EN16931-R003", "A Buyer reference (BT-10) or purchase order reference (BT-13) MUST be provided")
	}
	// R010/R020: electronic addresses.
	if !inv.buyerEndpointPresent {
		add("PEPPOL-EN16931-R010", "The Buyer electronic address (BT-49) MUST be provided")
	}
	if !inv.sellerEndpointPresent {
		add("PEPPOL-EN16931-R020", "The Seller electronic address (BT-34) MUST be provided")
	}
	// R005: the VAT accounting currency must differ from the invoice currency.
	if inv.taxCurrency != "" && inv.currency != "" && inv.taxCurrency == inv.currency {
		add("PEPPOL-EN16931-R005", "The VAT accounting currency code (BT-6) MUST differ from the invoice currency code (BT-5)")
	}
	// R061: a mandate reference for direct debit.
	if inv.directDebitPresent && inv.mandateRef == "" {
		add("PEPPOL-EN16931-R061", "A Mandate reference (BT-89) MUST be provided for a direct debit")
	}

	// P0104..P0111: a VAT exemption reason code forces its VAT category.
	for _, b := range inv.vatBreakdowns {
		if m, ok := peppolVATEX[b.reasonCode]; ok && b.category != m.category {
			add(m.rule, fmt.Sprintf("VAT exemption reason %q requires VAT category %q, not %q", b.reasonCode, m.category, b.category))
		}
	}

	// Line price base quantity (R121/R130) and line periods (R110/R111).
	for _, li := range inv.lines {
		if q := li.baseQty; q != "" {
			if v, ok := parseAmount(q); ok && v <= 0 {
				add("PEPPOL-EN16931-R121", "The price base quantity (BT-149) MUST be greater than zero")
			}
		}
		if li.baseQtyUnit != "" && li.unitCode != "" && li.baseQtyUnit != li.unitCode {
			add("PEPPOL-EN16931-R130", "The price base quantity unit (BT-150) MUST equal the invoiced quantity unit (BT-130)")
		}
		if li.period.present && inv.period.present {
			if s, e := li.period.start, inv.period.start; s != "" && e != "" && normDate(s) < normDate(e) {
				add("PEPPOL-EN16931-R110", "The Invoice line period start date MUST be within the Invoicing period")
			}
			if s, e := li.period.end, inv.period.end; s != "" && e != "" && normDate(s) > normDate(e) {
				add("PEPPOL-EN16931-R111", "The Invoice line period end date MUST be within the Invoicing period")
			}
		}
	}
	return out
}
