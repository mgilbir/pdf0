package render

import (
	"bytes"
	"image"
	"path/filepath"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
)

// Images in the finished PDF.
//
// The display list is tested next door; this is the other half of §7's
// separation — whether what the display list said becomes a correct content
// stream and a correct object in the file. The two failure modes look like each
// other's symptom on a rendered page, so they are told apart here.

// renderWithImages renders a document whose images come from a directory
// holding one 40 × 20 picture called "wide.png".
func renderWithImages(t *testing.T, htmlSrc string, cssSrc ...string) Result {
	t.Helper()
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "wide.png"), 40, 20)
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })

	in := Input{HTML: htmlSrc, Resources: res}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, Stylesheet{Source: c})
	}
	got, err := Render(in, Options{})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if got.Document == nil {
		t.Fatalf("no document: %v", got.Findings)
	}
	return got
}

// reread writes a document out and reads it back, which is the only way to be
// sure what is in the file rather than what is in memory.
func reread(t *testing.T, doc *pdf0.Document) *pdf0.Document {
	t.Helper()
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	out, err := pdf0.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	return out
}

// TestImageBecomesAnXObject is the end-to-end check: the picture is in the file,
// with the geometry it was drawn at, and pdf0's own extractor reads it back.
func TestImageBecomesAnXObject(t *testing.T) {
	got := renderWithImages(t,
		`<div><img src="wide.png"></div>`, noDefaults)
	doc := reread(t, got.Document)

	extracted := doc.ExtractImages()
	if len(extracted) != 1 {
		t.Fatalf("the file holds %d images, want 1", len(extracted))
	}
	im := extracted[0]
	if im.Width != 40 || im.Height != 20 {
		t.Errorf("the embedded image is %dx%d, want 40x20", im.Width, im.Height)
	}
	if im.Image == nil {
		t.Fatal("the embedded image did not decode")
	}
	// The picture is solid blue, and it has to still be blue after a round trip
	// through the sample encoder.
	r, g, b, _ := im.Image.At(im.Image.Bounds().Min.X, im.Image.Bounds().Min.Y).RGBA()
	if r>>8 != 0 || g>>8 != 0 || b>>8 != 255 {
		t.Errorf("the embedded pixel is (%d,%d,%d), want (0,0,255)", r>>8, g>>8, b>>8)
	}
}

// TestImagePlacementInTheContentStream pins the transform.
//
// An image XObject is painted into the unit square, so the matrix before the Do
// *is* the placement. The vertical scale is negative and that is not a mirror:
// layout's y increases downwards, so the image's own bottom edge belongs at the
// rectangle's largest y. Getting the sign wrong draws the picture upside down
// above the box instead of the right way up inside it — which is invisible in a
// solid-colour test image and obvious in a photograph.
func TestImagePlacementInTheContentStream(t *testing.T) {
	got := renderWithImages(t,
		`<div><img id="i" src="wide.png"></div>`, noDefaults)
	doc := reread(t, got.Document)

	pages := doc.PageList()
	stream, _ := doc.Resolve(pages[0].Get("Contents")).(*object.Stream)
	if stream == nil {
		t.Fatal("the page has no content stream")
	}
	data, err := doc.StreamData(stream)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	var placement []string
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasSuffix(strings.TrimSpace(line), " Do") && i > 0 {
			placement = strings.Fields(lines[i-1])
		}
	}
	if len(placement) != 7 || placement[6] != "cm" {
		t.Fatalf("no transform immediately before the Do:\n%s", text)
	}
	// 40 wide, 20 tall, and the vertical scale negative.
	if placement[0] != "40" || placement[3] != "-20" {
		t.Errorf("the image transform is %v; want a 40 by -20 scale", placement[:4])
	}
	if placement[1] != "0" || placement[2] != "0" {
		t.Errorf("the image transform is skewed: %v", placement[:4])
	}
}

// TestRepeatedImageIsEmbeddedOnce pins the deduplication: a logo drawn on forty
// rows is one object in the file, not forty.
func TestRepeatedImageIsEmbeddedOnce(t *testing.T) {
	got := renderWithImages(t,
		`<div><img src="wide.png"><img src="wide.png"><img src="wide.png"></div>`,
		noDefaults)
	doc := reread(t, got.Document)

	pages := doc.PageList()
	resources := doc.ResolveDict(pages[0].Get("Resources"))
	if resources == nil {
		t.Fatal("the page has no resources")
	}
	xobjects := doc.ResolveDict(resources.Get("XObject"))
	if xobjects == nil {
		t.Fatal("the page names no XObjects")
	}
	if n := len(xobjects.Keys); n != 1 {
		t.Errorf("three draws of one file produced %d XObjects, want 1", n)
	}

	// And the drawing really does paint it three times.
	stream, _ := doc.Resolve(pages[0].Get("Contents")).(*object.Stream)
	data, _ := doc.StreamData(stream)
	if n := strings.Count(string(data), " Do"); n != 3 {
		t.Errorf("the content stream paints the image %d times, want 3", n)
	}
}

// TestImageWithTransparencyGetsASoftMask pins that an alpha channel survives
// into the file, since a PNG with transparency is the everyday case and an
// engine that dropped the channel would paint a black rectangle behind every
// rounded logo.
func TestImageWithTransparencyGetsASoftMask(t *testing.T) {
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetNRGBA(x, y, colorNRGBA(0, 0, 255, uint8(x*60)))
		}
	}
	writeImage(t, filepath.Join(dir, "fade.png"), img)

	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	got, err := Render(Input{
		HTML:      `<div><img src="fade.png"></div>`,
		CSS:       []Stylesheet{{Source: noDefaults}},
		Resources: res,
	}, Options{})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if got.Document == nil {
		t.Fatalf("no document: %v", got.Findings)
	}
	doc := reread(t, got.Document)

	pages := doc.PageList()
	resources := doc.ResolveDict(pages[0].Get("Resources"))
	xobjects := doc.ResolveDict(resources.Get("XObject"))
	if xobjects == nil || len(xobjects.Keys) != 1 {
		t.Fatal("the page does not name exactly one XObject")
	}
	stream, _ := doc.Resolve(xobjects.Values[0]).(*object.Stream)
	if stream == nil {
		t.Fatal("the XObject is not a stream")
	}
	if stream.Dict.Get("SMask") == nil {
		t.Error("a translucent image was embedded without a soft mask, so its " +
			"transparent parts will paint as solid colour")
	}
}
