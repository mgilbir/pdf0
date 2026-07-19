package einvoice

import "strings"

// This file dispatches an invoice to the right validator based on the CIUS it
// declares in its Specification identifier (BT-24, the UBL CustomizationID or the
// CII GuidelineSpecifiedDocumentContextParameter/ID). Callers that do not know
// which national profile an invoice targets can use ValidateCIUS and let the
// document route itself.

// CIUS identifies a Core Invoice Usage Specification the dispatcher recognises.
type CIUS string

const (
	CIUSNone      CIUS = ""          // plain EN 16931 core (no recognised CIUS)
	CIUSXRechnung CIUS = "XRechnung" // German public-sector CIUS
	CIUSPeppol    CIUS = "Peppol"    // OpenPEPPOL BIS Billing 3.0
	CIUSNLCIUS    CIUS = "NLCIUS"    // Dutch SimplerInvoicing / SI-UBL
)

// DetectCIUS reports the CIUS that a Specification identifier (BT-24) declares, or
// CIUSNone when it names no recognised national CIUS. XRechnung is checked before
// Peppol because an XRechnung identifier may also reference the Peppol base.
func DetectCIUS(specID string) CIUS {
	id := strings.ToLower(specID)
	switch {
	case strings.Contains(id, "xrechnung"):
		return CIUSXRechnung
	case strings.Contains(id, "nlcius") || strings.Contains(id, "nen.nl"):
		return CIUSNLCIUS
	case strings.Contains(id, "peppol"):
		return CIUSPeppol
	}
	return CIUSNone
}

// ValidateCIUS validates an invoice against whichever CIUS its Specification
// identifier (BT-24) declares, falling back to the EN 16931 core when none is
// recognised. It routes both syntaxes (CII and UBL).
func ValidateCIUS(xmlData []byte) []Violation {
	inv, err := parseEN16931(xmlData)
	if err != nil {
		return []Violation{{Rule: "syntax", Message: err.Error()}}
	}
	switch DetectCIUS(inv.specID) {
	case CIUSXRechnung:
		return ValidateXRechnung(xmlData)
	case CIUSNLCIUS:
		return ValidateNLCIUS(xmlData)
	case CIUSPeppol:
		return ValidatePeppol(xmlData)
	default:
		return Validate(xmlData, ProfileEN16931)
	}
}
