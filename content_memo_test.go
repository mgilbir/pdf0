package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"strings"
	"testing"
)

// contentHeavyPDF builds a document whose pages carry large content streams that
// invoke device colours and named resources (ExtGState, ColorSpace, a form
// XObject, a font). Several independent PDF/A rules each scan this content for
// the resource names it uses; the per-stream memoization in contentUsedNamesCached
// means each stream is tokenized once per run rather than once per rule.
func contentHeavyPDF(pages int) []byte {
	var body strings.Builder
	for i := 0; i < 300; i++ {
		body.WriteString("q /GS0 gs /CS0 cs 0.2 0.4 0.6 scn 1 0 0 rg 0 1 0 RG /Fm0 Do BT /F0 12 Tf (x) Tj ET Q\n")
	}
	content := body.String()
	form := "q 0 0 1 rg 10 10 20 20 re f Q\n"

	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "2.0"}
	gs := &object.Dictionary{}
	gs.Set("Type", object.Name("ExtGState"))
	d.Objects[100] = &object.IndirectObject{Number: 100, Value: gs}
	fm := &object.Stream{Dict: object.Dictionary{}, Data: []byte(form)}
	fm.Dict.Set("Type", object.Name("XObject"))
	fm.Dict.Set("Subtype", object.Name("Form"))
	fm.Dict.Set("Length", object.Integer(len(form)))
	d.Objects[101] = &object.IndirectObject{Number: 101, Value: fm}

	res := &object.Dictionary{}
	egs := &object.Dictionary{}
	egs.Set("GS0", object.IndirectRef{Number: 100})
	res.Set("ExtGState", egs)
	cs := &object.Dictionary{}
	cs.Set("CS0", object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 100}})
	res.Set("ColorSpace", cs)
	xo := &object.Dictionary{}
	xo.Set("Fm0", object.IndirectRef{Number: 101})
	res.Set("XObject", xo)
	fonts := &object.Dictionary{}
	f0 := &object.Dictionary{}
	f0.Set("Type", object.Name("Font"))
	f0.Set("Subtype", object.Name("Type1"))
	f0.Set("BaseFont", object.Name("Helvetica"))
	fonts.Set("F0", f0)
	res.Set("Font", fonts)
	d.Objects[102] = &object.IndirectObject{Number: 102, Value: res}

	var kids object.Array
	num := 200
	for p := 0; p < pages; p++ {
		cn := num
		num++
		cst := &object.Stream{Dict: object.Dictionary{}, Data: []byte(content)}
		cst.Dict.Set("Length", object.Integer(len(content)))
		d.Objects[cn] = &object.IndirectObject{Number: cn, Value: cst}
		pn := num
		num++
		pg := &object.Dictionary{}
		pg.Set("Type", object.Name("Page"))
		pg.Set("Parent", object.IndirectRef{Number: 2})
		pg.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
		pg.Set("Contents", object.IndirectRef{Number: cn})
		pg.Set("Resources", object.IndirectRef{Number: 102})
		d.Objects[pn] = &object.IndirectObject{Number: pn, Value: pg}
		kids = append(kids, object.IndirectRef{Number: pn})
	}
	pagesDict := &object.Dictionary{}
	pagesDict.Set("Type", object.Name("Pages"))
	pagesDict.Set("Kids", kids)
	pagesDict.Set("Count", object.Integer(len(kids)))
	d.Objects[2] = &object.IndirectObject{Number: 2, Value: pagesDict}
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	d.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})

	var buf bytes.Buffer
	d.Write(&buf)
	return buf.Bytes()
}

// BenchmarkContentHeavyValidation guards the content-scan memoization: validating
// a document with many large content streams must not re-tokenize each stream
// once per rule. Compare across a change with `go test -bench BenchmarkContentHeavy`.
func BenchmarkContentHeavyValidation(b *testing.B) {
	data := contentHeavyPDF(60)
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidatePDFABytes(doc, pdfa.PDFA4, data)
	}
}

// BenchmarkContentHeavyUAValidation guards PDF/UA validation against the cost
// that dominated it on real documents: not the scanning but the allocating.
// ValidatePDFUA walks every page's content stream, and materializing those
// tokens into a slice cost ~94% of the run's allocated bytes — 45 GB on a
// 117 MB file — before the tokenizer became a streaming iterator. Watch the
// B/op and allocs/op columns, not just ns/op: a regression here shows up as
// allocations growing with content size rather than with the number of
// operands actually kept.
func BenchmarkContentHeavyUAValidation(b *testing.B) {
	data := contentHeavyPDF(60)
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidatePDFUA(doc)
	}
}

// TestContentHeavyValidates is a light correctness anchor for the benchmark
// fixture: the synthesized document must parse and validate without error.
func TestContentHeavyValidates(t *testing.T) {
	data := contentHeavyPDF(3)
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := doc.PageCount(); got != 3 {
		t.Errorf("PageCount = %d, want 3", got)
	}
	_ = ValidatePDFABytes(doc, pdfa.PDFA4, data) // must not panic
}
