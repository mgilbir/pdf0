package einvoice

import (
	"strings"
	"testing"
)

func TestDetectCIUS(t *testing.T) {
	cases := []struct {
		specID string
		want   CIUS
	}{
		{"urn:cen.eu:en16931:2017", CIUSNone},
		{"urn:cen.eu:en16931:2017#compliant#urn:xoev-de:kosit:standard:xrechnung_3.0", CIUSXRechnung},
		{"urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0", CIUSPeppol},
		{"urn:cen.eu:en16931:2017#compliant#urn:fdc:nen.nl:nlcius:v1.0", CIUSNLCIUS},
		{"", CIUSNone},
	}
	for _, tc := range cases {
		if got := DetectCIUS(tc.specID); got != tc.want {
			t.Errorf("DetectCIUS(%q) = %q, want %q", tc.specID, got, tc.want)
		}
	}
}

// TestValidateCIUSRoutes checks the dispatcher applies the CIUS the document
// declares: an XRechnung invoice missing a buyer reference (BR-DE-15) is caught
// via ValidateCIUS, while the same document validated as plain EN 16931 is not.
func TestValidateCIUSRoutes(t *testing.T) {
	if DetectCIUS(mustSpecID(t, minimalXRechnungUBL)) != CIUSXRechnung {
		t.Fatal("the XRechnung fixture should declare the XRechnung CIUS")
	}
	// The XRechnung fixture is conformant, so both routes are clean.
	if v := ValidateCIUS([]byte(minimalXRechnungUBL)); len(v) != 0 {
		t.Errorf("conformant XRechnung invoice via ValidateCIUS: %v", v)
	}
	// Remove the buyer reference: XRechnung's BR-DE-15 must fire through the
	// dispatcher, but the EN 16931 core (no CIUS) must not.
	broken := strings.Replace(minimalXRechnungUBL, "<cbc:BuyerReference>04011000-12345-03</cbc:BuyerReference>", "", 1)
	if broken == minimalXRechnungUBL {
		t.Fatal("buyer reference not found in the XRechnung fixture")
	}
	if !hasFacturXRule(ValidateCIUS([]byte(broken)), "BR-DE-15") {
		t.Error("ValidateCIUS should apply XRechnung's BR-DE-15 to an XRechnung document")
	}
	if hasFacturXRule(Validate([]byte(broken), ProfileEN16931), "BR-DE-15") {
		t.Error("the EN 16931 core must not emit the XRechnung rule BR-DE-15")
	}
}

func mustSpecID(t *testing.T, xml string) string {
	t.Helper()
	inv, err := parseEN16931([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	return inv.specID
}
