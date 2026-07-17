package pdf0

import "testing"

// buildPDFRDoc builds a minimal conformant PDF/R document: a PDF 2.0 file with
// one page whose content draws a single FlateDecode image XObject, and an XMP
// packet identifying it as PDF/R.
func buildPDFRDoc() *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "2.0"}
	set := func(n int, v Object) { d.Objects[n] = &IndirectObject{Number: n, Value: v} }

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
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
	page.Set("Contents", IndirectRef{Number: 4})
	res := &Dictionary{}
	xo := &Dictionary{}
	xo.Set("Im0", IndirectRef{Number: 5})
	res.Set("XObject", xo)
	page.Set("Resources", res)
	set(3, page)

	set(4, &Stream{Dict: Dictionary{}, Data: []byte("q 612 0 0 792 0 0 cm /Im0 Do Q")})

	img := &Dictionary{}
	img.Set("Type", Name("XObject"))
	img.Set("Subtype", Name("Image"))
	img.Set("Width", Integer(2))
	img.Set("Height", Integer(2))
	img.Set("Filter", Name("FlateDecode"))
	set(5, &Stream{Dict: *img, Data: []byte{0x78, 0x9c, 0x00}})

	xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
		`<rdf:Description xmlns:pdfr="http://www.iso.org/pdf/r/">` +
		`<pdfr:conformance>PDF/R-1</pdfr:conformance></rdf:Description></rdf:RDF></x:xmpmeta>`
	md := &Dictionary{}
	md.Set("Type", Name("Metadata"))
	md.Set("Subtype", Name("XML"))
	set(6, &Stream{Dict: *md, Data: []byte(xmp)})

	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

func hasPDFRRule(errs []PDFRViolation, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func TestValidatePDFRValid(t *testing.T) {
	if v := buildPDFRDoc().ValidatePDFR(); len(v) != 0 {
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
			d.Objects[4].Value.(*Stream).Data = []byte("BT /F0 12 Tf (hi) Tj ET q /Im0 Do Q")
		}, "raster-only"},
		{"vector fill", func(d *Document) {
			d.Objects[4].Value.(*Stream).Data = []byte("0 0 100 100 re f")
		}, "raster-only"},
		{"form XObject", func(d *Document) {
			d.Objects[5].Value.(*Stream).Dict.Set("Subtype", Name("Form"))
		}, "raster-only"},
		{"forbidden image filter", func(d *Document) {
			d.Objects[5].Value.(*Stream).Dict.Set("Filter", Name("ASCII85Decode"))
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
			d.Objects[6].Value.(*Stream).Data = []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"></x:xmpmeta>`)
		}, "identification"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := buildPDFRDoc()
			tc.mutate(d)
			if v := d.ValidatePDFR(); !hasPDFRRule(v, tc.rule) {
				t.Errorf("expected %q violation; got %v", tc.rule, v)
			}
		})
	}
}
