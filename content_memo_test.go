package pdf0

import (
	"bytes"
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

	d := &Document{Objects: map[int]*IndirectObject{}, Version: "2.0"}
	gs := &Dictionary{}
	gs.Set("Type", Name("ExtGState"))
	d.Objects[100] = &IndirectObject{Number: 100, Value: gs}
	fm := &Stream{Dict: Dictionary{}, Data: []byte(form)}
	fm.Dict.Set("Type", Name("XObject"))
	fm.Dict.Set("Subtype", Name("Form"))
	fm.Dict.Set("Length", Integer(len(form)))
	d.Objects[101] = &IndirectObject{Number: 101, Value: fm}

	res := &Dictionary{}
	egs := &Dictionary{}
	egs.Set("GS0", IndirectRef{Number: 100})
	res.Set("ExtGState", egs)
	cs := &Dictionary{}
	cs.Set("CS0", Array{Name("ICCBased"), IndirectRef{Number: 100}})
	res.Set("ColorSpace", cs)
	xo := &Dictionary{}
	xo.Set("Fm0", IndirectRef{Number: 101})
	res.Set("XObject", xo)
	fonts := &Dictionary{}
	f0 := &Dictionary{}
	f0.Set("Type", Name("Font"))
	f0.Set("Subtype", Name("Type1"))
	f0.Set("BaseFont", Name("Helvetica"))
	fonts.Set("F0", f0)
	res.Set("Font", fonts)
	d.Objects[102] = &IndirectObject{Number: 102, Value: res}

	var kids Array
	num := 200
	for p := 0; p < pages; p++ {
		cn := num
		num++
		cst := &Stream{Dict: Dictionary{}, Data: []byte(content)}
		cst.Dict.Set("Length", Integer(len(content)))
		d.Objects[cn] = &IndirectObject{Number: cn, Value: cst}
		pn := num
		num++
		pg := &Dictionary{}
		pg.Set("Type", Name("Page"))
		pg.Set("Parent", IndirectRef{Number: 2})
		pg.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
		pg.Set("Contents", IndirectRef{Number: cn})
		pg.Set("Resources", IndirectRef{Number: 102})
		d.Objects[pn] = &IndirectObject{Number: pn, Value: pg}
		kids = append(kids, IndirectRef{Number: pn})
	}
	pagesDict := &Dictionary{}
	pagesDict.Set("Type", Name("Pages"))
	pagesDict.Set("Kids", kids)
	pagesDict.Set("Count", Integer(len(kids)))
	d.Objects[2] = &IndirectObject{Number: 2, Value: pagesDict}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})

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
		_ = ValidatePDFABytes(doc, PDFA4, data)
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
	_ = ValidatePDFABytes(doc, PDFA4, data) // must not panic
}
