// Package einvoice validates electronic invoices against the EN 16931 semantic
// model and the national Core Invoice Usage Specifications (CIUS) layered on top
// of it. One syntax-neutral rule engine (fed by parseEN16931) serves both XML
// syntaxes — UN/CEFACT Cross Industry Invoice (CII, used by Factur-X/ZUGFeRD) and
// OASIS UBL (Peppol BIS, XRechnung UBL, NLCIUS) — and each CIUS adds its own rule
// layer in its own file (xrechnung.go, peppol.go, nlcius.go). The package is free
// of any PDF dependency; the pdf0 package wraps it for the Factur-X container.
package einvoice

import (
	"fmt"
	"strings"
)

// Profile is an EN 16931 conformance profile, in increasing data richness. The
// first five are the Factur-X/ZUGFeRD profiles; XRechnung is the German CIUS.
type Profile string

const (
	ProfileMinimum   Profile = "MINIMUM"
	ProfileBasicWL   Profile = "BASIC WL"
	ProfileBasic     Profile = "BASIC"
	ProfileEN16931   Profile = "EN 16931"
	ProfileExtended  Profile = "EXTENDED"
	ProfileXRechnung Profile = "XRECHNUNG" // ZUGFeRD 2.x German XRechnung CIUS of EN 16931
)

// ProfileFor maps an XMP ConformanceLevel string to a profile. The value is
// matched case- and space-insensitively, since producers write both "EN 16931"
// and "EN16931", and "BASIC WL" and "BASICWL".
func ProfileFor(level string) (Profile, bool) {
	switch strings.ToUpper(strings.ReplaceAll(level, " ", "")) {
	case "MINIMUM":
		return ProfileMinimum, true
	case "BASICWL":
		return ProfileBasicWL, true
	case "BASIC":
		return ProfileBasic, true
	case "EN16931":
		return ProfileEN16931, true
	case "EXTENDED":
		return ProfileExtended, true
	case "XRECHNUNG":
		return ProfileXRechnung, true
	}
	return "", false
}

// Violation reports one way in which an invoice departs from the EN 16931 core or
// a CIUS rule set. Rule is the rule identifier (e.g. "BR-CO-15", "BR-NL-1").
type Violation struct {
	Rule    string
	Message string
	Object  int
}

func (v Violation) Error() string {
	if v.Object != 0 {
		return fmt.Sprintf("%s: %s (object %d)", v.Rule, v.Message, v.Object)
	}
	return fmt.Sprintf("%s: %s", v.Rule, v.Message)
}
