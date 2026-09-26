package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
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
	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description rdf:about='' xmlns:pdfaid='http://www.aiim.org/pdfa/ns/id/' pdfaid:part='2' pdfaid:conformance='B'/></rdf:RDF></x:xmpmeta>`
	id := readPDFAIdentification(docWithXMP([]byte(xmp)))
	if id.part != "2" {
		t.Errorf("single-quoted pdfaid:part = %q, want 2", id.part)
	}
	if !id.hasConformance || id.conformance != "B" {
		t.Errorf("single-quoted pdfaid:conformance not read: %q %v", id.conformance, id.hasConformance)
	}
}

// TestA4ConformanceFE: each part-4 target accepts exactly its own declaration
// — F at 4f, E at 4e, none at plain PDF/A-4 (veraPDF 6.7.3 t03) — and every
// other value is rejected. A compliant 4f or 4e file embedded in a PDF/A-4
// document is validated at the level it declares (LevelFor), which is what
// keeps it from being rejected for carrying its letter (the concern of audit
// C23, which accepted F and E at plain PDF/A-4 instead).
func TestA4ConformanceFE(t *testing.T) {
	mk := func(conf string) core.View {
		xmp := `<?xpacket?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
			`<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/" pdfaid:part="4" pdfaid:rev="2020"`
		if conf != "" {
			xmp += ` pdfaid:conformance="` + conf + `"`
		}
		xmp += `/></rdf:RDF></x:xmpmeta>`
		meta := &object.Stream{Dict: object.Dictionary{}, Data: []byte(xmp)}
		meta.Dict.Set("Type", object.Name("Metadata"))
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Metadata", object.IndirectRef{Number: 2})
		return mkV(core.View{Version: "2.0", Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: cat},
			2: {Number: 2, Value: meta},
		}, Trailer: dictWith("Root", object.IndirectRef{Number: 1})})
	}
	confErrs := func(doc core.View, level Level) int {
		n := 0
		for _, e := range checkIdentification(doc, level) {
			if e.Rule == "6.7.3" && strings.Contains(e.Message, "conformance") {
				n++
			}
		}
		return n
	}
	for _, tc := range []struct {
		conf  string
		level Level
		ok    bool
	}{
		{"F", PDFA4F, true}, {"E", PDFA4E, true}, {"", PDFA4, true},
		{"F", PDFA4, false}, {"E", PDFA4, false}, {"B", PDFA4, false},
		{"E", PDFA4F, false}, {"", PDFA4E, false},
	} {
		if got := confErrs(mk(tc.conf), tc.level) == 0; got != tc.ok {
			t.Errorf("conformance %q at %s: accepted = %v, want %v", tc.conf, tc.level, got, tc.ok)
		}
	}
}

// TestXMPPacketHeaderAt1b ensures the xpacket bytes/encoding-attribute and
// well-formedness rules apply at PDF/A-1b (ISO 19005-1 6.7.5 / 6.7.9).
func TestXMPPacketHeaderAt1b(t *testing.T) {
	mk := func(xmp string) core.View {
		meta := &object.Stream{Dict: object.Dictionary{}, Data: []byte(xmp)}
		meta.Dict.Set("Type", object.Name("Metadata"))
		cat := &object.Dictionary{}
		cat.Set("Type", object.Name("Catalog"))
		cat.Set("Metadata", object.IndirectRef{Number: 2})
		return mkV(core.View{Version: "1.7", Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: cat},
			2: {Number: 2, Value: meta},
		}, Trailer: dictWith("Root", object.IndirectRef{Number: 1})})
	}
	wf := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"></rdf:RDF></x:xmpmeta>`
	if got := len(checkXMPWellFormed(mk(`<?xpacket begin="" bytes="47"?>`+wf), PDFA1b)); got == 0 {
		t.Error("xpacket bytes attribute not flagged at 1b")
	}
	if got := len(checkXMPWellFormed(mk(`<?xpacket begin="" encoding="UTF-8"?>`+wf), PDFA1b)); got == 0 {
		t.Error("xpacket encoding attribute not flagged at 1b")
	}
	if got := len(checkXMPWellFormed(mk(`<?xpacket begin=""?>`+wf), PDFA1b)); got != 0 {
		t.Errorf("clean XMP flagged at 1b: %d", got)
	}
}
