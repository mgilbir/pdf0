package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfr"
	"strings"
	"testing"
)

// buildPDFRDoc builds a minimal conformant PDF/R document: a PDF 2.0 file with
// one page whose content draws a single FlateDecode image XObject, and an XMP
// packet identifying it as PDF/R.
func buildPDFRDoc() *Document {
	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "2.0"}
	set := func(n int, v object.Object) { d.Objects[n] = &object.IndirectObject{Number: n, Value: v} }

	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	cat.Set("Metadata", object.IndirectRef{Number: 6})
	set(1, cat)

	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}})
	pages.Set("Count", object.Integer(1))
	set(2, pages)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	page.Set("Contents", object.IndirectRef{Number: 4})
	res := &object.Dictionary{}
	xo := &object.Dictionary{}
	xo.Set("Im0", object.IndirectRef{Number: 5})
	res.Set("XObject", xo)
	page.Set("Resources", res)
	set(3, page)

	set(4, &object.Stream{Dict: object.Dictionary{}, Data: []byte("q 612 0 0 792 0 0 cm /Im0 Do Q")})

	img := &object.Dictionary{}
	img.Set("Type", object.Name("XObject"))
	img.Set("Subtype", object.Name("Image"))
	img.Set("Width", object.Integer(2))
	img.Set("Height", object.Integer(2))
	img.Set("Filter", object.Name("FlateDecode"))
	set(5, object.NewStream(img, []byte{0x78, 0x9c, 0x00}))

	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description xmlns:pdfrid="http://www.aiim.org/pdfr/ns/id/">` +
		`<pdfrid:part>1</pdfrid:part></rdf:Description></rdf:RDF></x:xmpmeta>`
	md := &object.Dictionary{}
	md.Set("Type", object.Name("Metadata"))
	md.Set("Subtype", object.Name("XML"))
	set(6, object.NewStream(md, []byte(xmp)))

	d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

func hasPDFRRule(errs []pdfr.Violation, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func TestValidatePDFRValid(t *testing.T) {
	if v := ValidatePDFR(buildPDFRDoc()); len(v) != 0 {
		t.Errorf("conformant PDF/R flagged: %d violations (first: %s)", len(v), v[0].Error())
	}
}

func TestValidatePDFRViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Document)
		rule   string
	}{
		{"text operator", func(d *Document) {
			d.Objects[4].Value.(*object.Stream).Data = []byte("BT /F0 12 Tf (hi) Tj ET q /Im0 Do Q")
		}, "raster-only"},
		{"vector fill", func(d *Document) {
			d.Objects[4].Value.(*object.Stream).Data = []byte("0 0 100 100 re f")
		}, "raster-only"},
		{"form XObject", func(d *Document) {
			d.Objects[5].Value.(*object.Stream).Dict.Set("Subtype", object.Name("Form"))
		}, "raster-only"},
		{"forbidden image filter", func(d *Document) {
			d.Objects[5].Value.(*object.Stream).Dict.Set("Filter", object.Name("ASCII85Decode"))
		}, "image-filter"},
		{"encrypted", func(d *Document) {
			d.Encrypted = true
		}, "encryption"},
		{"wrong version", func(d *Document) {
			d.Version = "1.7"
		}, "version"},
		{"no metadata", func(d *Document) {
			d.ResolveDict(d.Trailer.Get("Root")).Delete("Metadata")
		}, "metadata"},
		{"not identified as PDF/R", func(d *Document) {
			d.Objects[6].Value.(*object.Stream).Data = []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"></x:xmpmeta>`)
		}, "identification"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := buildPDFRDoc()
			tc.mutate(d)
			if v := ValidatePDFR(d); !hasPDFRRule(v, tc.rule) {
				t.Errorf("expected %q violation; got %v", tc.rule, v)
			}
		})
	}
}

// TestPDFRReadsWhatItClaims is C146: identification is a property in the
// PDF/R identification namespace, not the letters "pdf/r" anywhere in the
// packet; a version that cannot be read is not PDF 2.0 (the same holds for
// PDF/UA-2), while a 1.7 header raised to 2.0 by the catalog is; and an inline
// image's filters are held to the same list as an image XObject's.
func TestPDFRReadsWhatItClaims(t *testing.T) {
	d := buildPDFRDoc()
	d.Objects[6].Value.(*object.Stream).Data = []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description xmlns:xmp="http://ns.adobe.com/xap/1.0/"><xmp:CreatorTool>Acme PDF/Reader pdfr</xmp:CreatorTool></rdf:Description></rdf:RDF></x:xmpmeta>`)
	if v := ValidatePDFR(d); !hasPDFRRule(v, "identification") {
		t.Errorf("a producer string naming PDF/Reader identified the file as PDF/R: %v", v)
	}

	for _, ver := range []string{"2.x", "", "2"} {
		d := buildPDFRDoc()
		d.Version = ver
		if v := ValidatePDFR(d); !hasPDFRRule(v, "version") {
			t.Errorf("PDF/R version %q passed: %v", ver, v)
		}
		ua := false
		for _, e := range ValidatePDFUA2(d) {
			ua = ua || (e.Clause == "4" && strings.Contains(e.Message, "defined for PDF 2.0"))
		}
		if !ua {
			t.Errorf("PDF/UA-2 version %q passed", ver)
		}
	}
	raised := buildPDFRDoc()
	raised.Version = "1.7"
	raised.ResolveDict(raised.Trailer.Get("Root")).Set("Version", object.Name("2.0"))
	if v := ValidatePDFR(raised); hasPDFRRule(v, "version") {
		t.Errorf("a 1.7 header with catalog /Version 2.0 is PDF 2.0: %v", v)
	}

	for _, c := range []struct {
		content string
		bad     bool
	}{
		{"q 1 0 0 1 0 0 cm BI /W 1 /H 1 /BPC 1 /IM true /F /AHx ID 00> EI Q", true},
		{"q 1 0 0 1 0 0 cm BI /W 1 /H 1 /BPC 1 /IM true /F [/AHx /CCF] ID 00> EI Q", true},
		{"q 1 0 0 1 0 0 cm BI /W 1 /H 1 /BPC 1 /IM true /F /CCF ID \x00 EI Q", false},
	} {
		d := buildPDFRDoc()
		d.Objects[4].Value.(*object.Stream).Data = []byte(c.content)
		if got := hasPDFRRule(ValidatePDFR(d), "image-filter"); got != c.bad {
			t.Errorf("%q: image-filter reported = %v, want %v", c.content, got, c.bad)
		}
	}
}
