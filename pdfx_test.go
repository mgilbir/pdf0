package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPDFX4Doc constructs a minimal conforming PDF/X-4 document: one page with
// a MediaBox and TrimBox, a GTS_PDFX output intent with an embedded ICC profile,
// a definite /Trapped flag, PDF/X-4 identification in both XMP and the Info
// dictionary, and a single embedded TrueType font shown by the page content.
func buildPDFX4Doc() *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.6"}
	set := func(num int, v Object) { d.Objects[num] = &IndirectObject{Number: num, Value: v} }

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	cat.Set("OutputIntents", Array{IndirectRef{Number: 4}})
	cat.Set("Metadata", IndirectRef{Number: 6})
	set(1, cat)

	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}})
	pages.Set("Count", Integer(1))
	set(2, pages)

	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	page.Set("TrimBox", Array{Integer(10), Integer(10), Integer(602), Integer(782)})
	page.Set("Contents", IndirectRef{Number: 10})
	res := &Dictionary{}
	fontRes := &Dictionary{}
	fontRes.Set("F1", IndirectRef{Number: 7})
	res.Set("Font", fontRes)
	page.Set("Resources", res)
	set(3, page)

	oi := &Dictionary{}
	oi.Set("Type", Name("OutputIntent"))
	oi.Set("S", Name("GTS_PDFX"))
	oi.Set("OutputConditionIdentifier", String{Value: []byte("FOGRA39")})
	oi.Set("DestOutputProfile", IndirectRef{Number: 5})
	set(4, oi)

	icc := &Dictionary{}
	icc.Set("N", Integer(4))
	// A minimal ICC profile whose colour-space signature (bytes 16..19) is CMYK,
	// so the output intent covers DeviceCMYK/DeviceGray but not DeviceRGB.
	iccData := make([]byte, 132)
	copy(iccData[16:], []byte("CMYK"))
	set(5, &Stream{Dict: *icc, Data: iccData})

	xmp := `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description xmlns:pdfxid="http://www.npes.org/pdfx/ns/id/">
<pdfxid:GTS_PDFXVersion>PDF/X-4</pdfxid:GTS_PDFXVersion>
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
	md := &Dictionary{}
	md.Set("Type", Name("Metadata"))
	md.Set("Subtype", Name("XML"))
	set(6, &Stream{Dict: *md, Data: []byte(xmp)})

	font := &Dictionary{}
	font.Set("Type", Name("Font"))
	font.Set("Subtype", Name("TrueType"))
	font.Set("BaseFont", Name("EmbeddedFont"))
	font.Set("FontDescriptor", IndirectRef{Number: 8})
	set(7, font)

	fdesc := &Dictionary{}
	fdesc.Set("Type", Name("FontDescriptor"))
	fdesc.Set("FontName", Name("EmbeddedFont"))
	fdesc.Set("FontFile2", IndirectRef{Number: 9})
	set(8, fdesc)

	set(9, &Stream{Dict: Dictionary{}, Data: bytes.Repeat([]byte{0}, 64)})

	set(10, &Stream{Dict: Dictionary{}, Data: []byte("BT /F1 12 Tf 100 700 Td (hi) Tj ET")})

	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	info := &Dictionary{}
	info.Set("GTS_PDFXVersion", String{Value: []byte("PDF/X-4")})
	info.Set("Trapped", Name("False"))
	set(11, info)
	d.Trailer.Set("Info", IndirectRef{Number: 11})
	return d
}

func TestValidatePDFXValid(t *testing.T) {
	d := buildPDFX4Doc()
	if v := ValidatePDFX(d, PDFX4); len(v) != 0 {
		t.Fatalf("valid PDF/X-4 document reported %d violation(s): %v", len(v), v)
	}
}

func TestValidatePDFXViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(d *Document)
		rule   string
		substr string
	}{
		{"encrypted", func(d *Document) { d.Encrypted = true }, "encryption", "not be encrypted"},
		{"version too new", func(d *Document) { d.Version = "2.0" }, "version", "PDF 1.6"},
		{"no identification", func(d *Document) {
			objDict(d, 1).Delete("Metadata")
			objDict(d, 11).Delete("GTS_PDFXVersion")
		}, "identification", "not identified as PDF/X"},
		{"no output intents", func(d *Document) { objDict(d, 1).Delete("OutputIntents") }, "output-intent", "requires a catalog /OutputIntents"},
		{"no GTS_PDFX intent", func(d *Document) { objDict(d, 4).Set("S", Name("GTS_PDFA1")) }, "output-intent", "GTS_PDFX"},
		{"missing OutputConditionIdentifier", func(d *Document) { objDict(d, 4).Delete("OutputConditionIdentifier") }, "output-intent", "OutputConditionIdentifier"},
		{"missing DestOutputProfile", func(d *Document) { objDict(d, 4).Delete("DestOutputProfile") }, "output-intent", "embedded ICC"},
		{"trapped unknown", func(d *Document) { objDict(d, 11).Set("Trapped", Name("Unknown")) }, "trapped", "True or False"},
		{"trapped absent", func(d *Document) { objDict(d, 11).Delete("Trapped") }, "trapped", "True or False"},
		{"both trim and art", func(d *Document) { objDict(d, 3).Set("ArtBox", Array{Integer(10), Integer(10), Integer(602), Integer(782)}) }, "page-box", "both TrimBox and ArtBox"},
		{"neither trim nor art", func(d *Document) { objDict(d, 3).Delete("TrimBox") }, "page-box", "neither TrimBox nor ArtBox"},
		{"trim outside media", func(d *Document) { objDict(d, 3).Set("TrimBox", Array{Integer(-5), Integer(10), Integer(602), Integer(782)}) }, "page-box", "not within the MediaBox"},
		{"bleed outside media", func(d *Document) { objDict(d, 3).Set("BleedBox", Array{Integer(-5), Integer(-5), Integer(700), Integer(800)}) }, "page-box", "BleedBox is not within"},
		{"font not embedded", func(d *Document) { objDict(d, 8).Delete("FontFile2") }, "font-embedding", "not embedded"},
		{"device rgb uncovered", func(d *Document) {
			// Paint with DeviceRGB (rg) under a CMYK-only output intent, no DefaultRGB.
			d.Objects[10] = &IndirectObject{Number: 10, Value: &Stream{Dict: Dictionary{}, Data: []byte("1 0 0 rg BT /F1 12 Tf 100 700 Td (hi) Tj ET")}}
		}, "color", "DeviceRGB used"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := buildPDFX4Doc()
			tc.mutate(d)
			v := ValidatePDFX(d, PDFX4)
			found := false
			for _, e := range v {
				if e.Rule == tc.rule && strings.Contains(e.Message, tc.substr) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a %s violation containing %q; got %v", tc.rule, tc.substr, v)
			}
		})
	}
}

// TestValidatePDFXCalPolySuite is the FP=0 oracle: the Cal Poly PDF/VT-1 files
// are conforming PDF/X-4, so ValidatePDFX must report no violations. It exercises
// the smaller record-count variants of each theme plus the AcroForm-bearing
// documentation PDF (whose default-appearance fonts must not be flagged); those
// share the PDF/X-4 structure of the multi-100k-page members, which are too slow
// to run the executed-content font walk over in a unit test. Skips when the
// suite is absent.
func TestValidatePDFXCalPolySuite(t *testing.T) {
	all, _ := filepath.Glob("testdata/pdfvt/*.pdf")
	if len(all) == 0 {
		t.Skip("Cal Poly PDF/VT suite not present (testdata/pdfvt)")
	}
	for _, f := range all {
		name := filepath.Base(f)
		isDoc := strings.HasPrefix(name, "Documentation")
		if !isDoc && !(strings.HasSuffix(name, "- 10.pdf") || strings.HasSuffix(name, "- 100.pdf") || strings.HasSuffix(name, "- 500.pdf")) {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		doc, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Errorf("%s: parse failed: %v", name, err)
			continue
		}
		v := ValidatePDFX(doc, PDFX4)
		if isDoc {
			// The documentation PDF is PDF/X-4 but has an AcroForm and uses
			// uncovered DeviceRGB (independently confirmed by the PDF/A device-
			// colour checker), so it is not fully conforming. It is kept here to
			// assert the AcroForm default-appearance fonts are NOT flagged — the
			// executed-content-free font scan must exclude them.
			for _, e := range v {
				if e.Rule == "font-embedding" {
					t.Errorf("%s: AcroForm default fonts should not be flagged, got: %s", name, e.Message)
				}
			}
			continue
		}
		if len(v) != 0 {
			t.Errorf("%s: expected 0 PDF/X-4 violations on a conforming file, got %d (first: %s: %s)",
				name, len(v), v[0].Rule, v[0].Message)
		}
	}
}
