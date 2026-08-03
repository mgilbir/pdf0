package pdf0

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	imagejpeg "image/jpeg"
	"io"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Image embedding checked against this module's own extractor.
//
// The two directions are inverses, so they can check each other: whatever is
// embedded is read back and the pixels compared. That is a much stronger
// statement than "the XObject dictionary has the right keys", and it is the one
// these tests make.

// drawImageDoc builds a PDF/A-2b document whose page draws one embedded image.
func drawImageDoc(t *testing.T, embed func(images.Allocator) (object.IndirectRef, error)) *Document {
	t.Helper()
	doc := NewPDFADocument(pdfa.PDFA2b)
	imgRef, err := embed(doc)
	if err != nil {
		t.Fatalf("embedding: %v", err)
	}

	var b content.Builder
	// An image XObject draws into the unit square, so the matrix is its size.
	b.Save().Concat(200, 0, 0, 100, 72, 600).Draw("Im0").Restore()
	data, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	stream := &object.Stream{Dict: object.Dictionary{}, Data: data}
	stream.Dict.Set("Length", object.Integer(len(data)))
	contentRef := doc.Add(stream)

	xobjects := &object.Dictionary{}
	for _, name := range b.Resources().XObjects {
		xobjects.Set(name, imgRef)
	}
	resources := &object.Dictionary{}
	resources.Set("XObject", xobjects)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{
		object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792),
	})
	page.Set("Resources", resources)
	page.Set("Contents", contentRef)
	pageRef := doc.Add(page)
	pages := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
	pages.Set("Kids", object.Array{pageRef})
	pages.Set("Count", object.Integer(1))
	return doc
}

// gradient builds a test image whose every pixel differs from its neighbours,
// so a row or column that is written in the wrong order cannot go unnoticed.
func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 11), B: uint8(x + y), A: 255})
		}
	}
	return img
}

// TestEmbeddedImageRoundTripsPixelForPixel is the oracle. An image written,
// re-read through the whole file and decoded must be the image that went in —
// same geometry, same pixels, same order.
func TestEmbeddedImageRoundTripsPixelForPixel(t *testing.T) {
	src := gradient(23, 17) // deliberately not a round number
	doc := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.Embed(a, src)
	})

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	got := rd.ExtractImages()
	if len(got) != 1 {
		t.Fatalf("extracted %d images, want 1", len(got))
	}
	ex := got[0]
	if !ex.Decoded || ex.Image == nil {
		t.Fatalf("the embedded image did not decode: %s", ex.Note)
	}
	if ex.Width != 23 || ex.Height != 17 {
		t.Errorf("geometry = %dx%d, want 23x17", ex.Width, ex.Height)
	}
	if ex.ColorSpace != "DeviceRGB" {
		t.Errorf("colour space = %q, want DeviceRGB", ex.ColorSpace)
	}
	for y := 0; y < 17; y++ {
		for x := 0; x < 23; x++ {
			wr, wg, wb, _ := src.At(x, y).RGBA()
			gr, gg, gb, _ := ex.Image.At(x, y).RGBA()
			if wr>>8 != gr>>8 || wg>>8 != gg>>8 || wb>>8 != gb>>8 {
				t.Fatalf("pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
					x, y, gr>>8, gg>>8, gb>>8, wr>>8, wg>>8, wb>>8)
			}
		}
	}
}

// TestEmbeddedGrayStaysGray pins that a greyscale image is not tripled into
// RGB. It is a size question, and the colour space in the file is the only
// place the answer shows.
func TestEmbeddedGrayStaysGray(t *testing.T) {
	src := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			src.SetGray(x, y, color.Gray{Y: uint8(x*32 + y)})
		}
	}
	doc := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.Embed(a, src)
	})
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	got := rd.ExtractImages()
	if len(got) != 1 {
		t.Fatalf("extracted %d images, want 1", len(got))
	}
	if got[0].ColorSpace != "DeviceGray" {
		t.Errorf("colour space = %q, want DeviceGray", got[0].ColorSpace)
	}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			w, _, _, _ := src.At(x, y).RGBA()
			g, _, _, _ := got[0].Image.At(x, y).RGBA()
			if w>>8 != g>>8 {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, g>>8, w>>8)
			}
		}
	}
}

// TestTransparencyBecomesAnSMask pins how alpha reaches the file, and that the
// premultiplication Go's colour model applies is divided back out. An image
// that skips that step looks right over white and wrong over everything else,
// which is the kind of defect that survives a careless eyeball check.
func TestTransparencyBecomesAnSMask(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 128})
	src.SetNRGBA(2, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 1})
	src.SetNRGBA(3, 0, color.NRGBA{R: 200, G: 100, B: 50, A: 0})

	doc := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.Embed(a, src)
	})
	var found *object.Stream
	for _, iobj := range doc.Objects {
		s, ok := iobj.Value.(*object.Stream)
		if ok && s.Dict.Get("SMask") != nil {
			found = s
		}
	}
	if found == nil {
		t.Fatal("no /SMask was written for an image with an alpha channel")
	}
	maskRef, _ := found.Dict.Get("SMask").(object.IndirectRef)
	mask, ok := doc.Resolve(maskRef).(*object.Stream)
	if !ok {
		t.Fatal("/SMask does not point at a stream")
	}
	if got, _ := mask.Dict.Get("ColorSpace").(object.Name); got != "DeviceGray" {
		t.Errorf("the soft mask is %q, want DeviceGray", got)
	}

	// The colour of a nearly transparent pixel must survive in the stored
	// samples. Go's colour model premultiplies, so without dividing alpha back
	// out that pixel would be stored as near black and would show as a dark
	// fringe wherever the image is composited over anything but black.
	//
	// This reads the stream rather than going through ExtractImages, because
	// extraction applies the soft mask: what comes back from there is the
	// composited result, in which a nearly transparent pixel is legitimately
	// dark whatever was stored. The samples are where the question has an
	// answer.
	samples := inflate(t, found.Data)
	if len(samples) != 4*3 {
		t.Fatalf("the colour image holds %d sample bytes, want 12", len(samples))
	}
	for _, px := range []int{0, 1, 2} {
		r, g, b := samples[3*px], samples[3*px+1], samples[3*px+2]
		if r != 200 || g != 100 || b != 50 {
			t.Errorf("pixel %d stored as (%d,%d,%d), want (200,100,50): alpha was not divided out",
				px, r, g, b)
		}
	}

	// And the mask carries the alpha channel itself.
	alpha := inflate(t, mask.Data)
	if want := []byte{255, 128, 1, 0}; !bytes.Equal(alpha, want) {
		t.Errorf("soft mask samples = %v, want %v", alpha, want)
	}
}

// inflate decompresses a FlateDecode stream body.
func inflate(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("opening the compressed samples: %v", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("reading the compressed samples: %v", err)
	}
	return out
}

// TestEmbeddedImageValidatesAsPDFA runs the whole document past the validator,
// so the XObject is judged by the same rules any image in a conforming file is.
func TestEmbeddedImageValidatesAsPDFA(t *testing.T) {
	doc := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.Embed(a, gradient(16, 16))
	})
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	for _, e := range ValidatePDFABytes(rd, pdfa.PDFA2b, buf.Bytes()) {
		t.Errorf("violation on a page with an embedded image: %s", e.Error())
	}
}

// TestJPEGIsEmbeddedWithoutReencoding pins the passthrough. Decoding a JPEG and
// re-encoding it with Flate would make the file larger and lose nothing but
// time; the bytes must go in as they are, under DCTDecode.
func TestJPEGIsEmbeddedWithoutReencoding(t *testing.T) {
	var jbuf bytes.Buffer
	if err := imagejpeg.Encode(&jbuf, gradient(32, 32), nil); err != nil {
		t.Fatalf("encoding the fixture: %v", err)
	}
	original := jbuf.Bytes()

	doc := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.EmbedJPEG(a, original, 32, 32, 3)
	})
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	got := rd.ExtractImages()
	if len(got) != 1 {
		t.Fatalf("extracted %d images, want 1", len(got))
	}
	if got[0].Filter != "DCTDecode" {
		t.Errorf("filter = %q, want DCTDecode", got[0].Filter)
	}
	if !got[0].Decoded {
		t.Fatalf("the JPEG did not decode on the way back: %s", got[0].Note)
	}
	// And the stored bytes are the ones handed in.
	for _, iobj := range rd.Objects {
		s, ok := iobj.Value.(*object.Stream)
		if !ok || s.Dict.Get("Subtype") != object.Name("Image") {
			continue
		}
		if !bytes.Equal(s.Data, original) {
			t.Error("the JPEG was re-encoded rather than stored as handed in")
		}
	}
}

// TestEmbedRefusesWhatItCannotWrite pins the guards. Geometry is attacker-
// controlled in any pipeline that embeds an image described by its input, and
// width × height × components is where a small number becomes an allocation
// that ends the process.
func TestEmbedRefusesWhatItCannotWrite(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	if _, err := images.Embed(doc, nil); err == nil {
		t.Error("a nil image was accepted")
	}
	if _, err := images.Embed(doc, image.NewRGBA(image.Rect(0, 0, 0, 10))); err == nil {
		t.Error("a zero-width image was accepted")
	}
	huge := image.NewRGBA(image.Rect(0, 0, 1, 1))
	huge.Rect = image.Rect(0, 0, 1<<15, 1<<15) // claims 1 gigapixel without allocating it
	if _, err := images.Embed(doc, huge); err == nil {
		t.Error("a gigapixel image was accepted")
	}
	if _, err := images.EmbedJPEG(doc, []byte{0xFF, 0xD8}, 10, 10, 4); err == nil {
		t.Error("a four-component JPEG was accepted")
	}
	if _, err := images.EmbedJPEG(doc, nil, 10, 10, 3); err == nil {
		t.Error("an empty JPEG was accepted")
	}
}

// TestOpaqueImageGetsNoSoftMask pins that an image with nothing transparent in
// it does not carry an alpha channel saying so. A fully opaque /SMask is a
// second image the size of the first, for no observable difference.
func TestOpaqueImageGetsNoSoftMask(t *testing.T) {
	// image.RGBA reaches the generic path and is what most Go code produces.
	doc := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.Embed(a, gradient(8, 8)) // every pixel A=255
	})
	for _, iobj := range doc.Objects {
		if s, ok := iobj.Value.(*object.Stream); ok && s.Dict.Get("SMask") != nil {
			t.Error("an opaque image was given a soft mask")
		}
	}

	// And an image that really is translucent still gets one.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 64})
	withAlpha := drawImageDoc(t, func(a images.Allocator) (object.IndirectRef, error) {
		return images.Embed(a, src)
	})
	var found bool
	for _, iobj := range withAlpha.Objects {
		if s, ok := iobj.Value.(*object.Stream); ok && s.Dict.Get("SMask") != nil {
			found = true
		}
	}
	if !found {
		t.Error("a translucent image lost its soft mask")
	}
}
