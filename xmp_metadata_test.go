package pdf0

import (
	"strings"
	"testing"
)

// TestNormalizeXMPDateCanonical ensures equivalent instants written in
// different XMP forms canonicalize to the same value as the PDF-date side
// (audit C22).
func TestNormalizeXMPDateCanonical(t *testing.T) {
	cases := []struct{ pdf, xmp string }{
		{"D:20240101120000Z", "2024-01-01T12:00:00+00:00"},
		{"D:202401011200Z", "2024-01-01T12:00Z"},
		{"D:20240101120000-00'00'", "2024-01-01T12:00:00-00:00"},
	}
	for _, c := range cases {
		if got, want := normalizeXMPDate(c.xmp), normalizePDFDate(c.pdf); got != want {
			t.Errorf("XMP %q -> %q, PDF %q -> %q (should match)", c.xmp, got, c.pdf, want)
		}
	}
	// A genuinely different instant must NOT be folded to equal.
	if normalizeXMPDate("2024-01-01T13:00:00Z") == normalizePDFDate("D:20240101120000Z") {
		t.Errorf("distinct instants normalized equal")
	}
}

// TestXMPIsUTF8BOMless ensures BOM-less UTF-16 is not accepted as UTF-8
// (audit C24).
func TestXMPIsUTF8BOMless(t *testing.T) {
	utf8 := []byte("<?xpacket begin=''?><x:xmpmeta/>")
	if !xmpIsUTF8(utf8) {
		t.Errorf("valid UTF-8 packet rejected")
	}
	// "<?" in UTF-16LE / BE with no BOM.
	le := []byte{0x3C, 0x00, 0x3F, 0x00, 0x78, 0x00}
	be := []byte{0x00, 0x3C, 0x00, 0x3F, 0x00, 0x78}
	if xmpIsUTF8(le) {
		t.Errorf("BOM-less UTF-16LE accepted as UTF-8")
	}
	if xmpIsUTF8(be) {
		t.Errorf("BOM-less UTF-16BE accepted as UTF-8")
	}
}

// TestXMPSingleQuotedAttributes ensures single-quoted XML attributes are read
// like double-quoted ones (audit C32).
func TestXMPSingleQuotedAttributes(t *testing.T) {
	xmp := `<rdf:Description pdfaid:part='2' pdfaid:conformance='B'/>`
	if got := extractXMPValue(xmp, "pdfaid:part"); got != "2" {
		t.Errorf("single-quoted pdfaid:part = %q, want 2", got)
	}
	if !xmpHasKey(xmp, "pdfaid:conformance") {
		t.Errorf("single-quoted pdfaid:conformance not detected")
	}
}

// TestA4ConformanceFE ensures pdfaid:conformance F/E is accepted at part 4 but
// other values are still rejected (audit C23).
func TestA4ConformanceFE(t *testing.T) {
	mk := func(conf string) *Document {
		xmp := `<?xpacket?><rdf:Description xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="4" pdfaid:rev="2020"`
		if conf != "" {
			xmp += ` pdfaid:conformance="` + conf + `"`
		}
		xmp += `/>`
		meta := &Stream{Dict: Dictionary{}, Data: []byte(xmp)}
		meta.Dict.Set("Type", Name("Metadata"))
		cat := &Dictionary{}
		cat.Set("Type", Name("Catalog"))
		cat.Set("Metadata", IndirectRef{Number: 2})
		return &Document{Version: "2.0", Objects: map[int]*IndirectObject{
			1: {Number: 1, Value: cat},
			2: {Number: 2, Value: meta},
		}, Trailer: dictWith("Root", IndirectRef{Number: 1})}
	}
	confErrs := func(doc *Document) int {
		n := 0
		for _, e := range checkMetadataVersion(doc, PDFA4) {
			if e.Rule == "6.7.3" && strings.Contains(e.Message, "conformance") {
				n++
			}
		}
		return n
	}
	if confErrs(mk("F")) != 0 {
		t.Errorf("A-4f conformance F was rejected")
	}
	if confErrs(mk("E")) != 0 {
		t.Errorf("A-4e conformance E was rejected")
	}
	if confErrs(mk("")) != 0 {
		t.Errorf("plain A-4 (no conformance) was flagged")
	}
	if confErrs(mk("B")) == 0 {
		t.Errorf("invalid A-4 conformance B was not flagged")
	}
}
