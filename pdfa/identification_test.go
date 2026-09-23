package pdfa

import (
	"strings"
	"testing"
)

func idPacket(descAttrs, body string) []byte {
	return []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about=""` + descAttrs + `>` + body + `</rdf:Description></rdf:RDF></x:xmpmeta>`)
}

const pdfaidDecl = ` xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/"`

// TestLevelAConformanceReadByModel is the audit's executed C141 scenario: a
// stale conformance inside a comment before the real one made the Level A rule
// read "B". The spellings the scraper misread are read as what they say.
func TestLevelAConformanceReadByModel(t *testing.T) {
	cases := map[string][]byte{
		"comment before the value": idPacket(pdfaidDecl, `<pdfaid:part>2</pdfaid:part><!-- was <pdfaid:conformance>B</pdfaid:conformance> --><pdfaid:conformance>A</pdfaid:conformance>`),
		"whitespace around =":      idPacket(pdfaidDecl+` pdfaid:part = "2" pdfaid:conformance = "A"`, ``),
		"escaped value":            idPacket(pdfaidDecl, `<pdfaid:part>2</pdfaid:part><pdfaid:conformance>&#65;</pdfaid:conformance>`),
	}
	for name, packet := range cases {
		t.Run(name, func(t *testing.T) {
			if errs := checkLevelAConformance(docWithXMP(packet), PDFA2a); len(errs) != 0 {
				t.Errorf("checkLevelAConformance = %v", errs)
			}
		})
	}
	// A value only inside a comment is no value.
	commented := idPacket(pdfaidDecl, `<pdfaid:part>2</pdfaid:part><!-- <pdfaid:conformance>A</pdfaid:conformance> -->`)
	if errs := checkLevelAConformance(docWithXMP(commented), PDFA2a); len(errs) == 0 {
		t.Error("a conformance inside a comment was read as declared")
	}
}

// TestIdentificationPrefixAndNamespace: the identification schema's prefix is
// normative, and a pdfaid prefix bound to another namespace is not an
// identification at all.
func TestIdentificationPrefixAndNamespace(t *testing.T) {
	has := func(errs []Violation, sub string) bool {
		for _, e := range errs {
			if strings.Contains(e.Message, sub) {
				return true
			}
		}
		return false
	}
	other := idPacket(` xmlns:id="http://www.aiim.org/pdfa/ns/id/"`, `<id:part>2</id:part><id:conformance>B</id:conformance>`)
	errs := checkMetadataVersion(docWithXMP(other), PDFA2b)
	if !has(errs, `must use the namespace prefix pdfaid, found "id"`) {
		t.Errorf("a non-canonical prefix was not reported: %v", errs)
	}
	if has(errs, "must contain pdfaid:part") {
		t.Errorf("the part was not read through its namespace: %v", errs)
	}
	impostor := idPacket(` xmlns:pdfaid="urn:not-pdfa"`, `<pdfaid:part>2</pdfaid:part>`)
	if errs := checkMetadataVersion(docWithXMP(impostor), PDFA2b); !has(errs, "pdfaid namespace must be") {
		t.Errorf("a pdfaid prefix bound to another namespace was accepted: %v", errs)
	}
}

// TestConformanceFindingCarriesItsCheck: the composing validators (Level A,
// the PDF/A-4 variants, Factur-X) find the conformance-letter finding by its
// Check, so the finding must carry it — and only it.
func TestConformanceFindingCarriesItsCheck(t *testing.T) {
	a := idPacket(pdfaidDecl, `<pdfaid:part>2</pdfaid:part><pdfaid:conformance>A</pdfaid:conformance>`)
	var checked []Violation
	for _, e := range checkMetadataVersion(docWithXMP(a), PDFA2b) {
		if e.Check == CheckPDFAIDConformance {
			checked = append(checked, e)
		}
	}
	if len(checked) != 1 || !strings.Contains(checked[0].Message, "must be B") {
		t.Errorf("findings carrying CheckPDFAIDConformance: %v", checked)
	}
	// Level A drops that finding and makes its own.
	for _, e := range ValidateLevelAView(docWithXMP(a), PDFA2a, nil) {
		if strings.Contains(e.Message, "must be B") {
			t.Errorf("Level A kept the base conformance finding: %v", e)
		}
	}
}
