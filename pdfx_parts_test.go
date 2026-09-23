package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfx"
	"strings"
	"testing"
)

// buildPDFXDoc adapts the conformant PDF/X-4 fixture to another PDF/X level by
// setting the PDF version and the GTS_PDFXVersion identifier (in both the XMP
// packet and the Info dictionary). A PDF 2.0 (PDF/X-6) document records
// /Trapped in XMP pdf:Trapped, since PDF 2.0 deprecates the Info dictionary.
func buildPDFXDoc(version, ident string) *Document {
	d := buildPDFX4Doc()
	d.Version = version
	ms := d.Objects[6].Value.(*object.Stream)
	ms.Data = bytes.Replace(ms.Data, []byte("PDF/X-4"), []byte(ident), 1)
	if version == "2.0" {
		ms.Data = bytes.Replace(ms.Data, []byte("</rdf:Description>"),
			[]byte(`<pdf:Trapped xmlns:pdf="http://ns.adobe.com/pdf/1.3/">False</pdf:Trapped></rdf:Description>`), 1)
	}
	d.Objects[11].Value.(*object.Dictionary).Set("GTS_PDFXVersion", object.String{Value: []byte(ident)})
	return d
}

// buildPDFX1a2001Doc is buildPDFXDoc for PDF/X-1a:2001, which ISO 15930-1
// identifies with the pair GTS_PDFXVersion "PDF/X-1:2001" and
// GTS_PDFXConformance "PDF/X-1a:2001" in the Info dictionary.
func buildPDFX1a2001Doc() *Document {
	d := buildPDFXDoc("1.3", "PDF/X-1:2001")
	d.Objects[11].Value.(*object.Dictionary).Set("GTS_PDFXConformance", object.String{Value: []byte("PDF/X-1a:2001")})
	return d
}

// TestPDFXPartsValid: a document adapted to each part validates clean at that
// level (no transparency present, correct identifier and version).
func TestPDFXPartsValid(t *testing.T) {
	cases := []struct {
		level         pdfx.Level
		version, iden string
	}{
		{pdfx.PDFX1a, "1.4", "PDF/X-1a:2003"},
		{pdfx.PDFX3, "1.3", "PDF/X-3:2002"},
		{pdfx.PDFX3, "1.4", "PDF/X-3:2003"},
		{pdfx.PDFX6, "2.0", "PDF/X-6"},
	}
	for _, tc := range cases {
		t.Run(tc.level.String(), func(t *testing.T) {
			d := buildPDFXDoc(tc.version, tc.iden)
			if v := ValidatePDFX(d, tc.level); len(v) != 0 {
				t.Errorf("expected 0 violations, got %d (first: %s)", len(v), v[0].Error())
			}
		})
	}
}

// TestPDFXTransparencyForbidden: a page transparency group is rejected by the
// no-transparency levels (X-1a, X-3) but accepted by X-4.
func TestPDFXTransparencyForbidden(t *testing.T) {
	withGroup := func(version, iden string) *Document {
		d := buildPDFXDoc(version, iden)
		grp := &object.Dictionary{}
		grp.Set("S", object.Name("Transparency"))
		d.Objects[3].Value.(*object.Dictionary).Set("Group", grp)
		return d
	}
	if v := ValidatePDFX(withGroup("1.4", "PDF/X-1a:2003"), pdfx.PDFX1a); !hasPDFXRule(v, "transparency") {
		t.Errorf("PDF/X-1a should reject a transparency group; got %v", v)
	}
	if v := ValidatePDFX(withGroup("1.4", "PDF/X-3:2003"), pdfx.PDFX3); !hasPDFXRule(v, "transparency") {
		t.Errorf("PDF/X-3 should reject a transparency group; got %v", v)
	}
	// PDF/X-4 permits transparency: the group alone must not be flagged.
	d := buildPDFX4Doc()
	grp := &object.Dictionary{}
	grp.Set("S", object.Name("Transparency"))
	d.Objects[3].Value.(*object.Dictionary).Set("Group", grp)
	if v := ValidatePDFX(d, pdfx.PDFX4); hasPDFXRule(v, "transparency") {
		t.Errorf("PDF/X-4 must permit a transparency group; got %v", v)
	}
}

// TestPDFXPartVersionBound: a version newer than the level allows is flagged.
func TestPDFXPartVersionBound(t *testing.T) {
	// PDF/X-1a is defined for PDF 1.4; declaring 1.6 is out of scope.
	if v := ValidatePDFX(buildPDFXDoc("1.6", "PDF/X-1a:2003"), pdfx.PDFX1a); !hasPDFXRule(v, "version") {
		t.Errorf("PDF/X-1a should reject PDF 1.6; got %v", v)
	}
	// PDF/X-6 requires PDF 2.0; declaring 1.6 is out of scope.
	if v := ValidatePDFX(buildPDFXDoc("1.6", "PDF/X-6"), pdfx.PDFX6); !hasPDFXRule(v, "version") {
		t.Errorf("PDF/X-6 should require PDF 2.0; got %v", v)
	}
}

// TestPDFXPartIdentification: the GTS_PDFXVersion must match the level's family.
func TestPDFXPartIdentification(t *testing.T) {
	// A file identified as PDF/X-4 must not pass a PDF/X-1a check.
	if v := ValidatePDFX(buildPDFXDoc("1.4", "PDF/X-4"), pdfx.PDFX1a); !hasPDFXRule(v, "identification") {
		t.Errorf("PDF/X-1a should reject a PDF/X-4 identifier; got %v", v)
	}
	// The 2001 and 2003 variants of PDF/X-1a both match, each identified as its
	// part says (audit 2026-09-22 C82): 2001 by the version/conformance pair,
	// 2003 by the version alone.
	if v := ValidatePDFX(buildPDFX1a2001Doc(), pdfx.PDFX1a); len(v) != 0 {
		t.Errorf("a PDF/X-1a:2001 file (PDF/X-1:2001 + PDF/X-1a:2001) should validate at PDF/X-1a; got %v", v)
	}
	if v := ValidatePDFX(buildPDFXDoc("1.4", "PDF/X-1a:2003"), pdfx.PDFX1a); hasPDFXRule(v, "identification") {
		t.Errorf("PDF/X-1a:2003 should identify PDF/X-1a; got %v", v)
	}
	// "PDF/X-1:2001" without the conformance key is PDF/X-1, not PDF/X-1a.
	if v := ValidatePDFX(buildPDFXDoc("1.3", "PDF/X-1:2001"), pdfx.PDFX1a); !hasPDFXRule(v, "identification") {
		t.Errorf("PDF/X-1:2001 without GTS_PDFXConformance should not identify PDF/X-1a; got %v", v)
	}
	// PDF/X-4 and PDF/X-4p are different levels, not one prefix.
	if v := ValidatePDFX(buildPDFX4Doc(), pdfx.PDFX4p); !hasPDFXRule(v, "identification") {
		t.Errorf("a PDF/X-4 file should not identify PDF/X-4p; got %v", v)
	}
}

// TestPDFXLevelRules pins the rules that differ between the PDF/X levels
// (audit 2026-09-22 C85), each on a document that differs from a clean one in
// that one respect. The rules are from the ISO 15930 parts as known, not from
// a corpus; see pdfx/levels.go.
func TestPDFXLevelRules(t *testing.T) {
	has := func(v []pdfx.Violation, rule, substr string) bool {
		for _, e := range v {
			if e.Rule == rule && strings.Contains(e.Message, substr) {
				return true
			}
		}
		return false
	}
	outputIntent := func(d *Document) *object.Dictionary { return d.Objects[4].Value.(*object.Dictionary) }

	// PDF/X-1a is CMYK, gray and spot only: RGB is refused even when the
	// output intent's profile is an RGB one that would "cover" it elsewhere.
	rgb := func(iden string, level pdfx.Level) []pdfx.Violation {
		d := buildPDFXDoc("1.4", iden)
		copy(d.Objects[5].Value.(*object.Stream).Data[16:], "RGB ")
		d.Objects[10].Value.(*object.Stream).Data = []byte("1 0 0 rg BT /F1 12 Tf 100 700 Td (hi) Tj ET")
		return ValidatePDFX(d, level)
	}
	if v := rgb("PDF/X-1a:2003", pdfx.PDFX1a); !has(v, "color", "permits only CMYK, gray and spot colour") {
		t.Errorf("PDF/X-1a accepted DeviceRGB under an RGB output intent: %v", v)
	}
	if v := rgb("PDF/X-3:2003", pdfx.PDFX3); has(v, "color", "") {
		t.Errorf("PDF/X-3 refused DeviceRGB its RGB output intent covers: %v", v)
	}

	// PDF/X-1a and -3 name a registered printing condition without embedding
	// its profile; a Custom condition needs one. PDF/X-4 always embeds.
	noProfile := func(iden, version, oci string, level pdfx.Level) []pdfx.Violation {
		d := buildPDFXDoc(version, iden)
		outputIntent(d).Delete("DestOutputProfile")
		outputIntent(d).Set("OutputConditionIdentifier", object.String{Value: []byte(oci)})
		return ValidatePDFX(d, level)
	}
	for _, c := range []struct {
		iden, version string
		level         pdfx.Level
	}{{"PDF/X-1a:2003", "1.4", pdfx.PDFX1a}, {"PDF/X-3:2003", "1.4", pdfx.PDFX3}} {
		if v := noProfile(c.iden, c.version, "FOGRA39", c.level); has(v, "output-intent", "") {
			t.Errorf("%v demanded a profile for a registered condition: %v", c.level, v)
		}
		if v := noProfile(c.iden, c.version, "Custom", c.level); !has(v, "output-intent", "Custom") {
			t.Errorf("%v accepted a Custom condition with no profile: %v", c.level, v)
		}
	}
	if v := noProfile("PDF/X-4", "1.6", "FOGRA39", pdfx.PDFX4); !has(v, "output-intent", "requires an embedded ICC") {
		t.Errorf("PDF/X-4 accepted an output intent with no embedded profile: %v", v)
	}

	// PDF/X-6 reads /Trapped from XMP pdf:Trapped: the Info dictionary's is
	// not it.
	x6 := buildPDFXDoc("2.0", "PDF/X-6")
	if v := ValidatePDFX(x6, pdfx.PDFX6); has(v, "trapped", "") {
		t.Errorf("PDF/X-6 with XMP pdf:Trapped reported: %v", v)
	}
	x6.Objects[11].Value.(*object.Dictionary).Delete("Trapped")
	if v := ValidatePDFX(x6, pdfx.PDFX6); has(v, "trapped", "") {
		t.Errorf("PDF/X-6 needed the Info /Trapped PDF 2.0 deprecates: %v", v)
	}
	x6info := buildPDFXDoc("2.0", "PDF/X-6")
	ms := x6info.Objects[6].Value.(*object.Stream)
	ms.Data = bytes.Replace(ms.Data, []byte(`<pdf:Trapped xmlns:pdf="http://ns.adobe.com/pdf/1.3/">False</pdf:Trapped>`), nil, 1)
	if v := ValidatePDFX(x6info, pdfx.PDFX6); !has(v, "trapped", "XMP pdf:Trapped") {
		t.Errorf("PDF/X-6 with /Trapped only in Info was accepted: %v", v)
	}

	// PDF/X-4 is identified in XMP; an Info-only identification is not one.
	infoOnly := buildPDFX4Doc()
	infoOnly.Objects[6].Value.(*object.Stream).Data = []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description/></rdf:RDF></x:xmpmeta>`)
	if v := ValidatePDFX(infoOnly, pdfx.PDFX4); !has(v, "identification", "identified in the XMP metadata") {
		t.Errorf("PDF/X-4 identified only in Info was accepted: %v", v)
	}
	// PDF/X-1a identifies in Info.
	if v := ValidatePDFX(buildPDFX1a2001Doc(), pdfx.PDFX1a); has(v, "identification", "") {
		t.Errorf("PDF/X-1a:2001 identified in Info was refused: %v", v)
	}

	// A Level that names no PDF/X level is refused, not validated as PDF/X-4.
	if v := ValidatePDFX(buildPDFX4Doc(), pdfx.Level(99)); len(v) != 1 || v[0].Rule != "limit" {
		t.Errorf("Level(99) = %v, want one limit finding", v)
	}
}

func hasPDFXRule(errs []pdfx.Violation, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}
