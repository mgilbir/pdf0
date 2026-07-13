package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPDFVT1Doc extends the conforming PDF/X-4 document with the two things
// PDF/VT-1 adds: a document part hierarchy over its page and PDF/VT-1
// identification in XMP.
func buildPDFVT1Doc() *Document {
	d := buildPDFX4Doc()

	xmp := `<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description xmlns:pdfxid="http://www.npes.org/pdfx/ns/id/" xmlns:pdfvtid="http://www.npes.org/pdfvt/ns/id/">
<pdfxid:GTS_PDFXVersion>PDF/X-4</pdfxid:GTS_PDFXVersion>
<pdfvtid:GTS_PDFVTVersion>PDF/VT-1</pdfvtid:GTS_PDFVTVersion>
</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`
	md := &Dictionary{}
	md.Set("Type", Name("Metadata"))
	md.Set("Subtype", Name("XML"))
	d.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Dict: *md, Data: []byte(xmp)}}

	cat := d.Objects[1].Value.(*Dictionary)
	cat.Set("DPartRoot", IndirectRef{Number: 12})

	root := &Dictionary{}
	root.Set("Type", Name("DPartRoot"))
	root.Set("DPartRootNode", IndirectRef{Number: 13})
	d.Objects[12] = &IndirectObject{Number: 12, Value: root}

	leaf := &Dictionary{}
	leaf.Set("Type", Name("DPart"))
	leaf.Set("Parent", IndirectRef{Number: 12})
	leaf.Set("Start", IndirectRef{Number: 3})
	d.Objects[13] = &IndirectObject{Number: 13, Value: leaf}

	d.Objects[3].Value.(*Dictionary).Set("DPart", IndirectRef{Number: 13})
	return d
}

func TestValidatePDFVTValid(t *testing.T) {
	d := buildPDFVT1Doc()
	if v := ValidatePDFVT(d); len(v) != 0 {
		t.Fatalf("valid PDF/VT-1 document reported %d violation(s): %v", len(v), v)
	}
}

func TestValidatePDFVTViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(d *Document)
		rule   string
		substr string
	}{
		{"no VT identification", func(d *Document) {
			md := &Dictionary{}
			md.Set("Type", Name("Metadata"))
			// XMP with only the PDF/X identification, no pdfvtid.
			d.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Dict: *md, Data: []byte("<pdfxid:GTS_PDFXVersion>PDF/X-4</pdfxid:GTS_PDFXVersion>")}}
		}, "identification", "not identified as PDF/VT"},
		{"wrong VT version", func(d *Document) {
			md := &Dictionary{}
			md.Set("Type", Name("Metadata"))
			d.Objects[6] = &IndirectObject{Number: 6, Value: &Stream{Dict: *md, Data: []byte("<pdfxid:GTS_PDFXVersion>PDF/X-4</pdfxid:GTS_PDFXVersion><pdfvtid:GTS_PDFVTVersion>PDF/VT-2</pdfvtid:GTS_PDFVTVersion>")}}
		}, "identification", "does not identify PDF/VT-1"},
		{"no DPart hierarchy", func(d *Document) {
			d.Objects[1].Value.(*Dictionary).Delete("DPartRoot")
		}, "dpart", "requires a document part hierarchy"},
		{"broken DPart propagates", func(d *Document) {
			d.Objects[13].Value.(*Dictionary).Delete("Parent")
		}, "dpart/14.12.4.1", "missing the required /Parent"},
		{"X-4 base violation propagates", func(d *Document) {
			d.Objects[11].Value.(*Dictionary).Set("Trapped", Name("Unknown"))
		}, "pdfx-4/trapped", "True or False"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := buildPDFVT1Doc()
			tc.mutate(d)
			v := ValidatePDFVT(d)
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

// TestValidatePDFVTCalPolySuite is the FP=0 oracle: the Cal Poly PDF/VT-1 test
// files are conforming PDF/VT-1, so ValidatePDFVT must report no violations. The
// documentation PDF is deliberately excluded — it is PDF/X-4 but not PDF/VT (no
// DPart hierarchy, no PDF/VT identification), so it is a negative control here.
// Uses the smaller record-count variants; skips when the suite is absent.
func TestValidatePDFVTCalPolySuite(t *testing.T) {
	all, _ := filepath.Glob("testdata/pdfvt/*.pdf")
	if len(all) == 0 {
		t.Skip("Cal Poly PDF/VT suite not present (testdata/pdfvt)")
	}
	var vtFiles []string
	sawDoc := false
	for _, f := range all {
		b := filepath.Base(f)
		if strings.HasPrefix(b, "Documentation") {
			// negative control: PDF/X-4 but not PDF/VT — must be flagged.
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				continue
			}
			if len(ValidatePDFVT(doc)) == 0 {
				t.Errorf("%s: expected PDF/VT violations (it is not a PDF/VT file), got none", b)
			}
			sawDoc = true
			continue
		}
		if strings.HasSuffix(b, "- 10.pdf") || strings.HasSuffix(b, "- 100.pdf") || strings.HasSuffix(b, "- 500.pdf") {
			vtFiles = append(vtFiles, f)
		}
	}
	if !sawDoc {
		t.Log("documentation PDF not present; skipping negative control")
	}
	for _, f := range vtFiles {
		name := filepath.Base(f)
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
		if v := ValidatePDFVT(doc); len(v) != 0 {
			t.Errorf("%s: expected 0 PDF/VT-1 violations on a conforming file, got %d (first: %s: %s)",
				name, len(v), v[0].Rule, v[0].Message)
		}
	}
}
