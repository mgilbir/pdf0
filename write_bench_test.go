package pdf0

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// What the writing side costs.
//
// None of this was measured. The pieces were built for correctness and the
// costs were reasoned about in comments — Save buffers the whole document
// because the byte-level conformance rules need bytes, subsetting is what keeps
// a 2 MB font from reaching a document, the validator walks every page — but
// reasoning is not measurement, and a cost nobody has looked at is a cost
// nobody knows.
//
// These exist to be run, compared across changes, and to make a regression
// visible. They are not assertions: a benchmark that fails a threshold on a
// loaded machine is a flaky test, and a flaky test is worse than none. Where a
// bound genuinely matters it is asserted in an ordinary test instead — see
// BenchmarkSaveVersusWrite's companion below.

// benchPage draws a page of realistic text and vector work: a heading, a
// paragraph broken to a column, and a small chart.
func benchPage(b *testing.B, face *fonts.Face) *content.Builder {
	b.Helper()
	const (
		pageW, margin = 595.0, 72.0
		body          = "Typesetting a paragraph needs one thing a font can answer " +
			"and a content stream cannot: how wide a word is. Everything else " +
			"follows from it — words are measured, lines are filled until the " +
			"next word would not fit, and each is drawn a leading below the last."
	)
	var c content.Builder
	c.BeginText().SetFont("F0", 18).SetRGB(0.1, 0.1, 0.15).MoveText(margin, 760)
	face.DrawShaped(&c, "A document with some text on it", 18)
	c.EndText()

	y := 730.0
	for i := 0; i < 12; i++ {
		c.BeginText().SetFont("F0", 11).SetRGB(0, 0, 0).MoveText(margin, y)
		face.DrawShaped(&c, body, 11)
		c.EndText()
		y -= 14
	}
	for i := 0; i < 24; i++ {
		x := margin + float64(i)*18
		c.Save().SetRGB(0.2, 0.45, 0.85).Rect(x, 80, 12, float64(20+i*7)).Fill().Restore()
	}
	return &c
}

func benchImage() image.Image {
	const n = 64
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 4), G: uint8(y * 4), B: 200, A: 255})
		}
	}
	return img
}

// buildDoc assembles a document of n pages, which is the unit everything below
// is measured against.
func buildDoc(b *testing.B, level pdfa.Level, conforming bool, pages int) *Document {
	b.Helper()
	var doc *Document
	if conforming {
		doc = NewPDFADocument(level)
	} else {
		doc = NewDocument()
	}
	face, err := fonts.NotoSans()
	if err != nil {
		b.Fatal(err)
	}
	imgRef, err := images.Embed(doc, benchImage())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < pages; i++ {
		c := benchPage(b, face)
		c.Save().Concat(120, 0, 0, 120, 400, 80).Draw("Im0").Restore()
		if _, err := doc.AddPage(Page{
			Width: 595, Height: 842, Content: c,
			Faces:    map[object.Name]*fonts.Face{"F0": face},
			XObjects: map[object.Name]object.Object{"Im0": imgRef},
		}); err != nil {
			b.Fatal(err)
		}
	}
	return doc
}

func BenchmarkBuildDocument(b *testing.B) {
	for _, pages := range []int{1, 10} {
		b.Run(fmt.Sprintf("%dpages", pages), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buildDoc(b, pdfa.PDFA2b, true, pages)
			}
		})
	}
}

// BenchmarkWrite is serialisation alone.
func BenchmarkWrite(b *testing.B) {
	for _, pages := range []int{1, 10} {
		b.Run(fmt.Sprintf("%dpages", pages), func(b *testing.B) {
			doc := buildDoc(b, pdfa.PDFA2b, true, pages)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := doc.Write(io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSave is serialisation plus reading back plus validating against the
// conformance the document claims — the whole of what Save promises.
//
// The gap between this and BenchmarkWrite is the price of that promise, and it
// is the number worth watching: it buffers the document, parses it again and
// walks every page.
func BenchmarkSave(b *testing.B) {
	for _, pages := range []int{1, 10} {
		b.Run(fmt.Sprintf("%dpages", pages), func(b *testing.B) {
			doc := buildDoc(b, pdfa.PDFA2b, true, pages)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := doc.Save(io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSavePlainDocument is the same call on a document claiming no
// conformance, which takes the straight-through path. The difference from
// BenchmarkSave isolates the checking from the writing.
func BenchmarkSavePlainDocument(b *testing.B) {
	doc := buildDoc(b, 0, false, 10)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := doc.Save(io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkShapeAndDraw is the text path on its own: shaping a paragraph and
// emitting it. Laying out a page asks this question hundreds of times.
func BenchmarkShapeAndDraw(b *testing.B) {
	face, err := fonts.NotoSans()
	if err != nil {
		b.Fatal(err)
	}
	const line = "Typesetting a paragraph needs one thing a font can answer."
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var c content.Builder
		c.BeginText().SetFont("F0", 11)
		face.DrawShaped(&c, line, 11)
		c.EndText()
		if _, err := c.Bytes(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMeasure is what line breaking actually calls, once per word.
func BenchmarkMeasure(b *testing.B) {
	face, err := fonts.NotoSans()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		face.Measure("Typesetting", 11)
	}
}

// BenchmarkEmbedImage covers the encoding path, which compresses.
func BenchmarkEmbedImage(b *testing.B) {
	img := benchImage()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		doc := NewDocument()
		if _, err := images.Embed(doc, img); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSubsetBundledFont is what every document with text pays once: the
// bundled face is 2 MB and what reaches a file is a fraction of it.
func BenchmarkSubsetBundledFont(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		face, err := fonts.NotoSans()
		if err != nil {
			b.Fatal(err)
		}
		face.ShapeGlyphs("Typesetting a paragraph needs one thing a font can answer.")
		if _, err := face.Subset(); err != nil {
			b.Fatal(err)
		}
	}
}

// TestSaveCostIsProportionalToTheDocument is the assertion the benchmarks
// cannot make.
//
// Save buffers the whole file to check it, so its memory is the document's
// size. That is a deliberate trade — the byte-level conformance rules need
// bytes — but it must stay *proportional*: a cost that grew faster than the
// document would make Save unusable on the documents it matters for, and the
// buffering makes that easy to do by accident.
//
// Ten pages must not cost more than a generous multiple of one. The bound is
// loose because this runs on shared machines; it is there to catch a change in
// the shape of the cost, not to measure it.
func TestSaveCostIsProportionalToTheDocument(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive")
	}
	sizeOf := func(pages int) int {
		doc := NewPDFADocument(pdfa.PDFA2b)
		face, err := fonts.NotoSans()
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < pages; i++ {
			var c content.Builder
			c.BeginText().SetFont("F0", 11).MoveText(72, 700)
			face.DrawShaped(&c, "Some text on a page.", 11)
			c.EndText()
			if _, err := doc.AddPage(Page{
				Width: 595, Height: 842, Content: &c,
				Faces: map[object.Name]*fonts.Face{"F0": face},
			}); err != nil {
				t.Fatal(err)
			}
		}
		var buf bytes.Buffer
		if err := doc.Save(&buf); err != nil {
			t.Fatalf("save: %v", err)
		}
		return buf.Len()
	}
	one, ten := sizeOf(1), sizeOf(10)
	// The font dominates a small document and is embedded once, so ten pages is
	// nothing like ten times one. What must not happen is superlinear growth.
	if ten > one*10 {
		t.Errorf("ten pages produced %d bytes and one produced %d; the font is shared, "+
			"so ten pages should be well under ten times one", ten, one)
	}
	if ten <= one {
		t.Errorf("ten pages produced %d bytes and one produced %d; pages are not reaching the file", ten, one)
	}
}
