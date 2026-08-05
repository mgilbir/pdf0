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

// Stencil masks: a one-bit shape rather than a picture.

// stencilStream finds the image mask a document holds.
func stencilStream(t *testing.T, d *Document) *object.Stream {
	t.Helper()
	for _, iobj := range d.Objects {
		if s, ok := iobj.Value.(*object.Stream); ok && s.Dict.Get("ImageMask") != nil {
			return s
		}
	}
	t.Fatal("no image mask was written")
	return nil
}

// TestStencilIsAShapeNotAPicture pins the dictionary that makes it one. The
// absence of /ColorSpace is as load-bearing as the presence of /ImageMask:
// together they say "paint the current colour where the bits are set". Adding a
// colour space turns it into a one-bit black-and-white image, which paints the
// white pixels white instead of leaving the page alone.
func TestStencilIsAShapeNotAPicture(t *testing.T) {
	doc := NewDocument()
	src := image.NewAlpha(image.Rect(0, 0, 8, 2))
	if _, err := images.EmbedStencil(doc, src); err != nil {
		t.Fatalf("embedding: %v", err)
	}
	s := stencilStream(t, doc)
	for key, want := range map[object.Name]object.Object{
		"Subtype":          object.Name("Image"),
		"ImageMask":        object.Boolean(true),
		"BitsPerComponent": object.Integer(1),
		"Width":            object.Integer(8),
		"Height":           object.Integer(2),
	} {
		if got := s.Dict.Get(key); got != want {
			t.Errorf("/%s = %v, want %v", key, got, want)
		}
	}
	if s.Dict.Get("ColorSpace") != nil {
		t.Error("the stencil was given a colour space; that makes it a picture, not a mask")
	}
	dec, _ := s.Dict.Get("Decode").(object.Array)
	if len(dec) != 2 || dec[0] != object.Integer(1) || dec[1] != object.Integer(0) {
		t.Errorf("/Decode = %v, want [1 0] so that a set bit paints", dec)
	}
}

// TestStencilBitsAreLeftToRightAndRowPadded is the packing, which is the whole
// of the format and the only place it can go wrong invisibly.
//
// Bit 7 of a byte is the leftmost pixel, and each row starts on a byte
// boundary. A packing that runs rows together shears the image diagonally by a
// few pixels per row — which looks like a bad mask rather than like a bug.
func TestStencilBitsAreLeftToRightAndRowPadded(t *testing.T) {
	// Ten pixels wide, so each row needs two bytes and six bits of padding.
	src := image.NewAlpha(image.Rect(0, 0, 10, 2))
	// Row 0: the leftmost pixel only.
	src.SetAlpha(0, 0, color.Alpha{A: 255})
	// Row 1: the ninth pixel, which is the first bit of the second byte.
	src.SetAlpha(8, 1, color.Alpha{A: 255})

	doc := NewDocument()
	if _, err := images.EmbedStencil(doc, src); err != nil {
		t.Fatalf("embedding: %v", err)
	}
	bits := inflate(t, stencilStream(t, doc).Data)
	want := []byte{
		0x80, 0x00, // row 0: the top bit of the first byte
		0x00, 0x80, // row 1: the top bit of the second byte
	}
	if !bytes.Equal(bits, want) {
		t.Errorf("packed as %08b, want %08b", bits, want)
	}
}

// TestStencilTakesAlphaWhereThereIsAlpha pins which question is asked of a
// pixel. An image carrying transparency is a shape already; its alpha is the
// mask and its colour is irrelevant.
func TestStencilTakesAlphaWhereThereIsAlpha(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	// All four are black; only the alpha differs, so a reader of luminance
	// would set every bit.
	src.SetNRGBA(0, 0, color.NRGBA{A: 255}) // opaque   -> set
	src.SetNRGBA(1, 0, color.NRGBA{A: 200}) // mostly   -> set
	src.SetNRGBA(2, 0, color.NRGBA{A: 100}) // mostly not
	src.SetNRGBA(3, 0, color.NRGBA{A: 0})   // clear

	doc := NewDocument()
	if _, err := images.EmbedStencil(doc, src); err != nil {
		t.Fatalf("embedding: %v", err)
	}
	bits := inflate(t, stencilStream(t, doc).Data)
	if len(bits) != 1 || bits[0] != 0xC0 {
		t.Errorf("packed as %08b, want [11000000]: the alpha decides, not the colour", bits)
	}
}

// TestStencilTakesDarknessWhereThereIsNoAlpha pins the other half. A
// black-on-white drawing has no alpha, and its marks are the dark parts.
func TestStencilTakesDarknessWhereThereIsNoAlpha(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 4, 1))
	src.SetGray(0, 0, color.Gray{Y: 0})   // black -> a mark
	src.SetGray(1, 0, color.Gray{Y: 60})  // dark  -> a mark
	src.SetGray(2, 0, color.Gray{Y: 200}) // light
	src.SetGray(3, 0, color.Gray{Y: 255}) // white

	doc := NewDocument()
	if _, err := images.EmbedStencil(doc, src); err != nil {
		t.Fatalf("embedding: %v", err)
	}
	bits := inflate(t, stencilStream(t, doc).Data)
	if len(bits) != 1 || bits[0] != 0xC0 {
		t.Errorf("packed as %08b, want [11000000]: the dark pixels are the marks", bits)
	}
}

// TestStencilRefusesWhatItCannotWrite keeps the same bounds as Embed. A stencil
// is one eighth the size of a colour image, which is not a reason to let a
// hostile caller allocate an unbounded one.
func TestStencilRefusesWhatItCannotWrite(t *testing.T) {
	doc := NewDocument()
	if _, err := images.EmbedStencil(doc, nil); err == nil {
		t.Error("a nil image was accepted")
	}
	if _, err := images.EmbedStencil(doc, image.NewAlpha(image.Rect(0, 0, 0, 5))); err == nil {
		t.Error("an image with no area was accepted")
	}
	huge := image.NewAlpha(image.Rect(0, 0, 1, 1))
	huge.Rect = image.Rect(0, 0, 1<<16, 1<<16) // claims 4 gigapixels without allocating them
	if _, err := images.EmbedStencil(doc, huge); err == nil {
		t.Error("an image past the pixel limit was accepted")
	}
}

// TestStencilPaintsAndValidates is the end-to-end claim: a page that paints
// through a stencil is a file, and a conforming one.
func TestStencilPaintsAndValidates(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			src := image.NewAlpha(image.Rect(0, 0, 16, 16))
			for i := 0; i < 16; i++ {
				src.SetAlpha(i, i, color.Alpha{A: 255})
			}
			ref, err := images.EmbedStencil(doc, src)
			if err != nil {
				t.Fatalf("embedding: %v", err)
			}
			var b content.Builder
			// The fill colour is what the stencil paints in, which is the
			// point: the shape is stored once and coloured where it is used.
			b.Save().SetRGB(0, 0, 1).Concat(64, 0, 0, 64, 20, 20).Draw("Im0").Restore()
			_, err = doc.AddPage(Page{
				Width: 200, Height: 200, Content: &b,
				XObjects: map[object.Name]object.Object{"Im0": ref},
			})
			if err != nil {
				t.Fatalf("adding the page: %v", err)
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			if v := ValidatePDFABytes(rd, level, buf.Bytes()); len(v) != 0 {
				t.Errorf("a page painted through a stencil is not %s: %v", level, v)
			}
		})
	}
}
