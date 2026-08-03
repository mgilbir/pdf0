package pdf0

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Fuzzing the writing side.
//
// FuzzRead and FuzzRoundTrip drive the *reader* with arbitrary bytes, which is
// the obvious attack surface and was covered long ago. The writer had none, and
// it is not the safer half: a caller building a document from a form, a
// template or an HTML page is turning somebody else's data into calls on this
// API — page sizes, colours, coordinates, strings, resource names, structure
// tags. Every one of those is an input.
//
// The difficulty is that the write API takes structured values rather than
// bytes, so there is nothing to hand a fuzzer directly. The answer is to read
// the fuzzer's bytes as a little program: each byte selects an operation and
// the bytes after it are its operands. That gives the fuzzer a way to reach
// deep, unlikely sequences — a clip with no path inside a text object inside
// thirty nested saves — which is exactly what hand-written cases do not.
//
// # The property
//
// Not merely "does not panic", though that is asserted too. The property is
// that **whatever this writes, it can read**: if Write or Save returns no
// error, the bytes it produced must parse. A writer that emits a file its own
// reader rejects has produced something no reader can be expected to accept,
// and that is a defect however the file was built.

// fuzzProgram is the fuzzer's byte string, read as operations.
type fuzzProgram struct {
	b  []byte
	at int
}

func (p *fuzzProgram) done() bool { return p.at >= len(p.b) }

func (p *fuzzProgram) byte() byte {
	if p.done() {
		return 0
	}
	v := p.b[p.at]
	p.at++
	return v
}

// num maps two bytes onto a coordinate, deliberately including the values that
// break things: zero, negative, enormous, and fractional.
func (p *fuzzProgram) num() float64 {
	lo, hi := float64(p.byte()), float64(p.byte())
	v := hi*256 + lo
	switch p.byte() % 4 {
	case 0:
		return v
	case 1:
		return -v
	case 2:
		return v / 64
	default:
		return v * 1e6
	}
}

func (p *fuzzProgram) unit() float64 { return float64(p.byte()) / 255 }

// name returns a resource name from a small pool, so that a drawing has a real
// chance of naming one that the page also defines — a name nothing defines is
// interesting, and so is a name that matches.
func (p *fuzzProgram) name() object.Name {
	return []object.Name{"F0", "Im0", "GS0", "P0", "Sh0", "CS0", "MC0", ""}[p.byte()%8]
}

func (p *fuzzProgram) text() string {
	n := int(p.byte() % 24)
	pool := []string{"", "a", "Hello", "café", "日本", "\x00", "(\\)", "—", "�", "ب"}
	out := pool[int(p.byte())%len(pool)]
	for i := 0; i < n && !p.done(); i++ {
		out += string(rune(p.byte()))
	}
	return out
}

// draw runs the program against a content builder. The builder records its
// first error and reports it from Bytes, so an invalid sequence is not a
// failure here — it is one of the things being explored.
func (p *fuzzProgram) draw(b *content.Builder) {
	for steps := 0; !p.done() && steps < 512; steps++ {
		switch p.byte() % 24 {
		case 0:
			b.Save()
		case 1:
			b.Restore()
		case 2:
			b.Rect(p.num(), p.num(), p.num(), p.num())
		case 3:
			b.MoveTo(p.num(), p.num())
		case 4:
			b.LineTo(p.num(), p.num())
		case 5:
			b.CurveTo(p.num(), p.num(), p.num(), p.num(), p.num(), p.num())
		case 6:
			b.ClosePath()
		case 7:
			b.Fill()
		case 8:
			b.Stroke()
		case 9:
			b.FillStroke()
		case 10:
			b.Clip()
		case 11:
			b.EndPath()
		case 12:
			b.SetRGB(p.unit(), p.unit(), p.unit())
		case 13:
			b.SetGray(p.unit())
		case 14:
			b.SetCMYK(p.unit(), p.unit(), p.unit(), p.unit())
		case 15:
			b.SetLineWidth(p.num())
		case 16:
			b.SetDash([]float64{p.num(), p.num()}, p.num())
		case 17:
			b.Concat(p.num(), p.num(), p.num(), p.num(), p.num(), p.num())
		case 18:
			b.BeginText()
		case 19:
			b.EndText()
		case 20:
			b.SetFont(p.name(), p.num())
		case 21:
			b.ShowText([]byte(p.text()))
		case 22:
			b.BeginTagged("P", int(p.byte()))
		case 23:
			b.EndMarked()
		}
	}
}

// FuzzWriteSurface builds a document from the fuzzer's bytes and asserts that
// what it manages to write, it can read back.
func FuzzWriteSurface(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 2, 10, 0, 20, 0, 30, 0, 40, 0, 7})     // save, rect, fill
	f.Add([]byte{18, 20, 0, 12, 0, 21, 4, 19})             // a text object
	f.Add([]byte{22, 0, 2, 1, 1, 1, 1, 1, 1, 1, 1, 7, 23}) // tagged content
	f.Add(bytes.Repeat([]byte{0}, 64))                     // deeply nested saves
	f.Add(bytes.Repeat([]byte{1}, 64))                     // unbalanced restores

	f.Fuzz(func(t *testing.T, script []byte) {
		p := &fuzzProgram{b: script}

		// The flavour is chosen from the input too, so the conformance-checking
		// path in Save is reached as well as the plain one.
		var doc *Document
		switch p.byte() % 3 {
		case 0:
			doc = NewDocument()
		case 1:
			doc = NewPDFADocument(pdfa.PDFA2b)
		default:
			doc = NewPDFADocument(pdfa.PDFA4)
		}

		if p.byte()%2 == 0 {
			// Describing the document exercises the metadata path, including
			// the XML escaping, with fuzzer-chosen text.
			_ = doc.SetDocumentInfo(DocumentInfo{Title: p.text(), Author: p.text()})
		}

		var b content.Builder
		p.draw(&b)

		imgRef, err := images.Embed(doc, fuzzImage(p))
		if err != nil {
			return
		}
		page := Page{
			Width: p.num(), Height: p.num(), Content: &b,
			Rotate:   int(p.byte()%5) * 90,
			Group:    p.byte()%2 == 0,
			XObjects: map[object.Name]object.Object{"Im0": imgRef},
		}
		if p.byte()%2 == 0 {
			page.Links = []Link{{
				Rect: [4]float64{p.num(), p.num(), p.num(), p.num()},
				URI:  p.text(),
			}}
		}
		pageRef, err := doc.AddPage(page)
		if err != nil {
			// A refused page is a correct outcome for most of these inputs.
			return
		}

		if p.byte()%2 == 0 {
			_ = doc.SetOutline([]OutlineItem{{Title: p.text(), Page: pageRef}})
		}
		if p.byte()%2 == 0 {
			_ = doc.SetStructureTree([]StructElem{{
				Tag: "Document", Page: &pageRef,
				Children: []StructElem{{Tag: "P", Alt: p.text()}},
			}}, nil)
		}

		// The property: what is written must be readable.
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			return
		}
		if _, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
			t.Fatalf("Write produced %d bytes that Read rejects: %v", buf.Len(), err)
		}

		// And the same for Save, which additionally checks the document
		// against whatever conformance it claims.
		var saved bytes.Buffer
		if err := doc.Save(&saved); err != nil {
			return
		}
		rd, err := Read(bytes.NewReader(saved.Bytes()), int64(saved.Len()))
		if err != nil {
			t.Fatalf("Save produced %d bytes that Read rejects: %v", saved.Len(), err)
		}
		// Save promises the file meets the conformance it claims. Check it,
		// rather than trusting that the check inside Save ran.
		if level, claimed := rd.Conformance(); claimed {
			if v := ValidatePDFABytes(rd, level, saved.Bytes()); len(v) != 0 {
				t.Fatalf("Save wrote a file claiming %s that does not meet it: %v", level, v)
			}
		}
	})
}

// fuzzImage builds a small image whose type and content come from the input,
// so the embedding paths — alpha, greyscale, paletted — are all reachable.
func fuzzImage(p *fuzzProgram) image.Image {
	w, h := 1+int(p.byte()%4), 1+int(p.byte()%4)
	r := image.Rect(0, 0, w, h)
	switch p.byte() % 4 {
	case 0:
		img := image.NewNRGBA(r)
		for i := range img.Pix {
			img.Pix[i] = p.byte()
		}
		return img
	case 1:
		img := image.NewGray(r)
		for i := range img.Pix {
			img.Pix[i] = p.byte()
		}
		return img
	case 2:
		img := image.NewPaletted(r, color.Palette{
			color.NRGBA{R: p.byte(), G: p.byte(), B: p.byte(), A: p.byte()},
			color.NRGBA{A: p.byte()},
		})
		for i := range img.Pix {
			img.Pix[i] = p.byte() % 2
		}
		return img
	default:
		img := image.NewRGBA(r)
		for i := range img.Pix {
			img.Pix[i] = p.byte()
		}
		return img
	}
}
