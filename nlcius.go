package pdf0

import "fmt"

// This file validates the Dutch NLCIUS (SimplerInvoicing / SI-UBL) Core Invoice
// Usage Specification on top of the EN 16931 core. NLCIUS makes several
// EN 16931-optional terms mandatory for invoices issued by a supplier in the
// Netherlands (the BR-NL-* rules) and restricts the invoice type and payment
// means code lists. The same syntax-neutral model feeds it, so it validates CII
// (the NLCIUS-CII binding) and UBL (SI-UBL) alike.
//
// Every BR-NL rule is conditional on the supplier being in the Netherlands
// (Seller country BT-40 = NL); a non-NL supplier is out of scope for the CIUS.
//
// Only the CIUS's fatal (validity-affecting) rules are emitted. NLCIUS also
// defines a set of advisory "not recommended" rules (BR-NL-19 … BR-NL-35) that
// warn against using discouraged optional terms; those do not make an invoice
// non-conformant and are intentionally not reported here.
//
// Not vendored: the SimplerInvoicing Schematron and instance test suite are
// downloaded by `make cius-oracles` and used only as the FP=0 oracle.

// nlciusPaymentMeans is the payment means code set NLCIUS permits (BR-NL-12):
// 30 credit transfer, 48 bank card, 49 direct debit, 57 standing agreement,
// 58 SEPA credit transfer, 59 SEPA direct debit.
var nlciusPaymentMeans = map[string]bool{"30": true, "48": true, "49": true, "57": true, "58": true, "59": true}

// nlciusTypeCodes is the invoice type code set NLCIUS permits (BR-NL-7).
var nlciusTypeCodes = map[string]bool{"380": true, "381": true, "384": true, "389": true}

// ValidateNLCIUS validates an invoice XML against the Dutch NLCIUS (SimplerInvoicing)
// CIUS: the EN 16931 core plus the NLCIUS-specific rules. It accepts either syntax.
func ValidateNLCIUS(xmlData []byte) []FacturXViolation {
	inv, err := parseEN16931(xmlData)
	if err != nil {
		return []FacturXViolation{{Rule: "syntax", Message: err.Error()}}
	}
	out := validateEN16931(inv, FacturXEN16931)
	out = append(out, validateNLCIUSRules(inv)...)
	return out
}

// validateNLCIUSRules applies the mandatory NLCIUS rules. All are gated on the
// supplier being in the Netherlands.
func validateNLCIUSRules(inv *en16931Invoice) []FacturXViolation {
	if inv.sellerCountry != "NL" {
		return nil
	}
	var out []FacturXViolation
	add := func(rule, msg string) { out = append(out, FacturXViolation{Rule: rule, Message: msg}) }

	// BR-NL-1: the supplier must identify its legal entity with a KVK (scheme
	// 0106) or OIN (scheme 0190) number.
	if !((inv.sellerLegalScheme == "0106" || inv.sellerLegalScheme == "0190") && inv.sellerLegalReg != "") {
		add("BR-NL-1", "the Seller legal registration identifier (BT-30) must be a KVK (scheme 0106) or OIN (scheme 0190) number")
	}

	// BR-NL-2: the invoice must carry a buyer reference (BT-10) or a purchase
	// order reference (BT-13).
	if inv.buyerReference == "" && inv.orderRef == "" {
		add("BR-NL-2", "the invoice must contain a Buyer reference (BT-10) or a Purchase order reference (BT-13)")
	}

	// BR-NL-3: the Seller postal address must contain a street (BT-35), city
	// (BT-37) and post code (BT-38).
	if inv.sellerStreet == "" || inv.sellerCity == "" || inv.sellerPostCode == "" {
		add("BR-NL-3", "the Seller postal address must contain a street (BT-35), city (BT-37) and post code (BT-38)")
	}

	// BR-NL-4: a Dutch Buyer's postal address must contain a street, city and post code.
	if inv.buyerCountry == "NL" && (inv.buyerStreet == "" || inv.buyerCity == "" || inv.buyerPostCode == "") {
		add("BR-NL-4", "a Dutch Buyer postal address must contain a street (BT-50), city (BT-52) and post code (BT-53)")
	}

	// BR-NL-5: a Dutch tax representative's postal address must contain a street,
	// city and post code.
	if inv.taxRepCountry == "NL" && (inv.taxRepStreet == "" || inv.taxRepCity == "" || inv.taxRepPostCode == "") {
		add("BR-NL-5", "a Dutch tax representative postal address must contain a street, city and post code")
	}

	// BR-NL-7: the invoice type code must be 380, 381, 384 or 389.
	if !nlciusTypeCodes[inv.typeCode] {
		add("BR-NL-7", fmt.Sprintf("the invoice type code (BT-3=%q) must be one of 380, 381, 384, 389", inv.typeCode))
	}

	// BR-NL-8: the type code must match the UBL document element — 381 (credit
	// note) belongs to a CreditNote, every other permitted code to an Invoice.
	if inv.syntax == "UBL" {
		if inv.isCreditNote && inv.typeCode != "381" {
			add("BR-NL-8", fmt.Sprintf("a CreditNote document must use type code 381, not %q", inv.typeCode))
		} else if !inv.isCreditNote && inv.typeCode == "381" {
			add("BR-NL-8", "type code 381 (credit note) must not be used in an Invoice document")
		}
	}

	// BR-NL-9: a corrective invoice (type 384) must reference the preceding
	// invoice it corrects (BT-25).
	if inv.typeCode == "384" && !(inv.hasBillingRef && !inv.billingRefNoID) {
		add("BR-NL-9", "a corrective invoice (type 384) must contain a Preceding Invoice reference (BT-25)")
	}

	// BR-NL-10: a Dutch Buyer must identify its legal entity with a (non-empty)
	// KVK number (scheme 0106).
	if inv.buyerCountry == "NL" && !(inv.buyerLegalScheme == "0106" && inv.buyerLegalReg) {
		add("BR-NL-10", "a Dutch Buyer legal registration identifier (BT-47) must be a KVK (scheme 0106) number")
	}

	// BR-NL-11: unless the amount due is not positive or the document is a credit
	// note (type 381), the invoice must state a means of payment.
	duePositive := true
	if d, ok := parseAmount(inv.totals.duePayable); ok && d <= 0 {
		duePositive = false
	}
	if duePositive && inv.typeCode != "381" && len(inv.paymentMeans) == 0 {
		add("BR-NL-11", "the invoice must provide a means of payment (BG-16)")
	}

	// BR-NL-12: each payment means code must be one NLCIUS permits.
	for _, code := range inv.paymentMeans {
		if !nlciusPaymentMeans[code] {
			add("BR-NL-12", fmt.Sprintf("the payment means code (BT-81=%q) must be one of 30, 48, 49, 57, 58, 59", code))
			break
		}
	}

	// BR-NL-13: if an order line reference (BT-132) is used, a document-level
	// order reference (BT-13) must be present.
	if inv.hasOrderLineRef && inv.orderRef == "" {
		add("BR-NL-13", "an order line reference (BT-132) requires a document-level Purchase order reference (BT-13)")
	}

	return out
}
