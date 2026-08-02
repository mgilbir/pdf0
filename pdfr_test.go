package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfr"
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
	set(5, &object.Stream{Dict: *img, Data: []byte{0x78, 0x9c, 0x00}})

	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description xmlns:pdfr="http://www.iso.org/pdf/r/">` +
		`<pdfr:conformance>PDF/R-1</pdfr:conformance></rdf:Description></rdf:RDF></x:xmpmeta>`
	md := &object.Dictionary{}
	md.Set("Type", object.Name("Metadata"))
	md.Set("Subtype", object.Name("XML"))
	set(6, &object.Stream{Dict: *md, Data: []byte(xmp)})

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
