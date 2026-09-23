package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// withPages gives d n pages that share one resource dictionary — an indirect
// object, so a direct dictionary inside it stays one dictionary through a
// write and a read — and one content stream.
func withPages(d *Document, n int, resDict *object.Dictionary, content string) {
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	pagesRef := cat.Get("Pages")
	pages := d.ResolveDict(pagesRef)
	res := d.Add(resDict)
	cs := d.Add(object.NewStream(&object.Dictionary{}, []byte(content)))
	var kids object.Array
	for i := 0; i < n; i++ {
		p := &object.Dictionary{}
		p.Set("Type", object.Name("Page"))
		p.Set("Parent", pagesRef)
		p.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(200), object.Integer(200)})
		p.Set("Resources", res)
		p.Set("Contents", cs)
		kids = append(kids, d.Add(p))
	}
	pages.Set("Kids", kids)
	pages.Set("Count", object.Integer(n))
}

// TestADirectFontIsOneFontAtNoObject (C138): a font written directly in a
// /Font dictionary is one font, reported once and anchored to no object — not
// once per page that reaches it, as "object -1", "object -2".
func TestADirectFontIsOneFontAtNoObject(t *testing.T) {
	d := mustPDFADoc(t, pdfa.PDFA2b)
	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type1"))
	font.Set("BaseFont", object.Name("Helvetica"))
	res := &object.Dictionary{}
	res.Set("Font", object.NewDictionary(object.Entry{Key: "F1", Value: font}))
	withPages(d, 3, res, "BT /F1 12 Tf 10 10 Td (Hi) Tj ET\n")
	r, _ := profileBytes(t, d)
	var fd []pdfa.Violation
	for _, v := range ValidatePDFA(r, pdfa.PDFA2b) {
		if v.Object < 0 {
			t.Errorf("a finding anchored to a negative object: %v", v)
		}
		if strings.Contains(v.Message, "/FontDescriptor") {
			fd = append(fd, v)
		}
	}
	if len(fd) != 1 || fd[0].Object != 0 {
		t.Errorf("the direct unembedded font: want one /FontDescriptor finding at object 0, got %v", fd)
	}
}
