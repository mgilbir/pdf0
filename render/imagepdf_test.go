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

// pageContent reads back the one page's content stream.
func pageContent(t *testing.T, doc *pdf0.Document) (*object.Dictionary, string) {
	t.Helper()
	pages := doc.PageList()
	if len(pages) != 1 {
		t.Fatalf("the document has %d pages, want 1", len(pages))
	}
	stream, _ := doc.Resolve(pages[0].Get("Contents")).(*object.Stream)
	if stream == nil {
		t.Fatal("the page has no content stream")
	}
	data, err := doc.StreamData(stream)
	if err != nil {
		t.Fatal(err)
	}
	return pages[0], string(data)
}

// TestSingleBackgroundTileIsDrawnDirectly pins that the common case — one tile,
// which is what "no-repeat" produces — costs a clip and a Do rather than a
// pattern object. A pattern for it would be a dictionary, a stream and a
// resource to say what two operators already say.
func TestSingleBackgroundTileIsDrawnDirectly(t *testing.T) {
	got := renderWithImages(t, `<div id="a">x</div>`, noDefaults+
		`#a { width: 200px; height: 100px;
		      background-image: url(wide.png); background-repeat: no-repeat }`)
	doc := reread(t, got.Document)
	page, text := pageContent(t, doc)

	if resources := doc.ResolveDict(page.Get("Resources")); resources != nil {
		if p := doc.ResolveDict(resources.Get("Pattern")); p != nil && len(p.Keys) > 0 {
			t.Errorf("a single tile produced %d patterns, want none", len(p.Keys))
		}
	}
	if !strings.Contains(text, " Do") {
		t.Errorf("the background was not drawn:\n%s", text)
	}
	// The clip is what keeps a tile that overhangs its box inside it, and it has
	// to be there even when the tile happens to fit.
	if !strings.Contains(text, " W n") && !strings.Contains(text, "W\nn") {
		t.Errorf("the background was drawn without a clip:\n%s", text)
	}
}

// TestRepeatingBackgroundBecomesATilingPattern pins the whole reason the display
// list carries a step rather than a list of tiles.
//
// The tile count is (area / tile size), and a stylesheet chooses both ends of
// it. A Do per tile would put that number into the file, so a document could ask
// for a file of any size. A pattern has the count nowhere in it.
func TestRepeatingBackgroundBecomesATilingPattern(t *testing.T) {
	got := renderWithImages(t, `<div id="a">x</div>`, noDefaults+
		`#a { width: 200px; height: 100px;
		      background-image: url(wide.png); background-repeat: repeat }`)
	doc := reread(t, got.Document)
	page, text := pageContent(t, doc)

	// 200 × 100 over a 40 × 20 tile is 5 × 5 = 25 tiles, and the content stream
	// must not mention any of them.
	if n := strings.Count(text, " Do"); n != 0 {
		t.Errorf("the content stream draws the image %d times; a tiling belongs in "+
			"a pattern, where the count does not appear:\n%s", n, text)
	}

	resources := doc.ResolveDict(page.Get("Resources"))
	if resources == nil {
		t.Fatal("the page has no resources")
	}
	patterns := doc.ResolveDict(resources.Get("Pattern"))
	if patterns == nil || len(patterns.Keys) != 1 {
		t.Fatalf("the page names %v patterns, want exactly one", patterns)
	}
	stream, _ := doc.Resolve(patterns.Values[0]).(*object.Stream)
	if stream == nil {
		t.Fatal("the pattern is not a stream")
	}

	for _, want := range []struct {
		key   object.Name
		value object.Object
	}{
		{"PatternType", object.Integer(1)},
		{"PaintType", object.Integer(1)},
	} {
		if got := stream.Dict.Get(want.key); got != want.value {
			t.Errorf("the pattern's /%s is %v, want %v", want.key, got, want.value)
		}
	}
	// The step is the tile size in *points*, because a pattern's own space is
	// the page's default space and the "cm" this engine emits does not apply to
	// it. 40 CSS px is 30pt and 20 is 15 — a pattern whose matrix repeated the
	// page transform would step by 40 and 20 instead, and the background would
	// tile a third too coarsely with no other symptom.
	//
	// The matrix carries the scale, so the numbers here are the layout's own and
	// the matrix is what has to be checked for the conversion.
	if s := stream.Dict.Get("XStep"); s != object.Integer(40) {
		t.Errorf("the pattern's /XStep is %v, want 40 — the tile's width in layout units", s)
	}
	if s := stream.Dict.Get("YStep"); s != object.Integer(20) {
		t.Errorf("the pattern's /YStep is %v, want 20", s)
	}
	matrix, _ := doc.Resolve(stream.Dict.Get("Matrix")).(object.Array)
	if len(matrix) != 6 {
		t.Fatalf("the pattern's /Matrix is %v, want six numbers", matrix)
	}
	// A pattern's matrix maps pattern space to the page's *default* space, so it
	// has to repeat the page transform: 0.75 to convert px to pt, negated on y
	// to flip the axis, and translated into the page margin.
	a, _ := numberValue(matrix[0])
	d, _ := numberValue(matrix[3])
	if a != 0.75 || d != -0.75 {
		t.Errorf("the pattern's matrix scales by (%v, %v), want (0.75, -0.75) — the "+
			"px-to-pt conversion with the y axis flipped", a, d)
	}
	f, _ := numberValue(matrix[5])
	if f <= 0 {
		t.Errorf("the pattern's matrix translates y by %v; it has to put the origin "+
			"at the top of the page", f)
	}

	// The pattern's cell draws the image, and carries its own resources: the
	// page's /XObject is not in scope inside a pattern.
	cell, err := doc.StreamData(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cell), " Do") {
		t.Errorf("the pattern's cell draws nothing:\n%s", cell)
	}
	patternRes := doc.ResolveDict(stream.Dict.Get("Resources"))
	if patternRes == nil || doc.ResolveDict(patternRes.Get("XObject")) == nil {
		t.Error("the pattern names no XObject of its own, so its cell refers to " +
			"a name nothing defines")
	}
}

func numberValue(o object.Object) (float64, bool) {
	switch v := o.(type) {
	case object.Integer:
		return float64(v), true
	case object.Real:
		return float64(v), true
	}
	return 0, false
}
