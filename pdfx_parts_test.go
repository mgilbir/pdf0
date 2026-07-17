package pdf0

import (
	"bytes"
	"testing"
)

// buildPDFXDoc adapts the conformant PDF/X-4 fixture to another PDF/X level by
// setting the PDF version and the GTS_PDFXVersion identifier (in both the XMP
// packet and the Info dictionary).
func buildPDFXDoc(version, ident string) *Document {
	d := buildPDFX4Doc()
	d.Version = version
	ms := d.Objects[6].Value.(*Stream)
	ms.Data = bytes.Replace(ms.Data, []byte("PDF/X-4"), []byte(ident), 1)
	d.Objects[11].Value.(*Dictionary).Set("GTS_PDFXVersion", String{Value: []byte(ident)})
	return d
}

// TestPDFXPartsValid: a document adapted to each part validates clean at that
// level (no transparency present, correct identifier and version).
func TestPDFXPartsValid(t *testing.T) {
	cases := []struct {
		level         PDFXLevel
		version, iden string
	}{
		{PDFX1a, "1.4", "PDF/X-1a:2003"},
		{PDFX3, "1.4", "PDF/X-3:2003"},
		{PDFX6, "2.0", "PDF/X-6"},
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
		grp := &Dictionary{}
		grp.Set("S", Name("Transparency"))
		d.Objects[3].Value.(*Dictionary).Set("Group", grp)
		return d
	}
	if v := ValidatePDFX(withGroup("1.4", "PDF/X-1a:2003"), PDFX1a); !hasPDFXRule(v, "transparency") {
		t.Errorf("PDF/X-1a should reject a transparency group; got %v", v)
	}
	if v := ValidatePDFX(withGroup("1.4", "PDF/X-3:2003"), PDFX3); !hasPDFXRule(v, "transparency") {
		t.Errorf("PDF/X-3 should reject a transparency group; got %v", v)
	}
	// PDF/X-4 permits transparency: the group alone must not be flagged.
	d := buildPDFX4Doc()
	grp := &Dictionary{}
	grp.Set("S", Name("Transparency"))
	d.Objects[3].Value.(*Dictionary).Set("Group", grp)
	if v := ValidatePDFX(d, PDFX4); hasPDFXRule(v, "transparency") {
		t.Errorf("PDF/X-4 must permit a transparency group; got %v", v)
	}
}

// TestPDFXPartVersionBound: a version newer than the level allows is flagged.
func TestPDFXPartVersionBound(t *testing.T) {
	// PDF/X-1a is defined for PDF 1.4; declaring 1.6 is out of scope.
	if v := ValidatePDFX(buildPDFXDoc("1.6", "PDF/X-1a:2003"), PDFX1a); !hasPDFXRule(v, "version") {
		t.Errorf("PDF/X-1a should reject PDF 1.6; got %v", v)
	}
	// PDF/X-6 requires PDF 2.0; declaring 1.6 is out of scope.
	if v := ValidatePDFX(buildPDFXDoc("1.6", "PDF/X-6"), PDFX6); !hasPDFXRule(v, "version") {
		t.Errorf("PDF/X-6 should require PDF 2.0; got %v", v)
	}
}

// TestPDFXPartIdentification: the GTS_PDFXVersion must match the level's family.
func TestPDFXPartIdentification(t *testing.T) {
	// A file identified as PDF/X-4 must not pass a PDF/X-1a check.
	if v := ValidatePDFX(buildPDFXDoc("1.4", "PDF/X-4"), PDFX1a); !hasPDFXRule(v, "identification") {
		t.Errorf("PDF/X-1a should reject a PDF/X-4 identifier; got %v", v)
	}
	// The 2001 and 2003 variants of PDF/X-1a both match.
	for _, id := range []string{"PDF/X-1a:2001", "PDF/X-1a:2003"} {
		if v := ValidatePDFX(buildPDFXDoc("1.4", id), PDFX1a); hasPDFXRule(v, "identification") {
			t.Errorf("%s should identify PDF/X-1a; got %v", id, v)
		}
	}
}

func hasPDFXRule(errs []PDFXViolation, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}
