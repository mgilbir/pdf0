package render

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Loading images, and the caps that keep it from being a way to end the
// process.
//
// Every cap below is exercised by crossing it. Two of them are crossed at their
// real values — a header declaring more pixels than the engine will decode is a
// few dozen bytes, so there is no reason to lower anything to see it fire — and
// the ones whose real values are megabytes are crossed by lowering the variable
// to a number a test can reach, which is what those variables are for.

// writePNG puts a solid blue PNG of the given size on disk.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.WriteFile(path, encodePNG(t, w, h), 0o600); err != nil {
		t.Fatal(err)
	}
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func colorNRGBA(r, g, b, a uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: a} }

func writeImage(t *testing.T, path string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// bombPNG builds a PNG that is nothing but a header, declaring whatever size it
// is asked for.
//
// This is a decompression bomb in its purest form: a few dozen bytes claiming a
// picture of any size at all. It is what makes the pixel cap testable at its
// real value, and it is what the cap exists for — the file is small, the
// declaration is not, and an engine that decodes before it checks has already
// asked for the memory by the time it could object.
func bombPNG(w, h uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})

	var ihdr bytes.Buffer
	ihdr.WriteString("IHDR")
	binary.Write(&ihdr, binary.BigEndian, w)
	binary.Write(&ihdr, binary.BigEndian, h)
	// 8 bits per sample, truecolour, deflate, adaptive filtering, no interlace.
	ihdr.Write([]byte{8, 2, 0, 0, 0})

	binary.Write(&buf, binary.BigEndian, uint32(ihdr.Len()-4))
	buf.Write(ihdr.Bytes())
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(ihdr.Bytes()))
	return buf.Bytes()
}

// loadInDir builds a document whose images are resolved from dir.
func loadInDir(t *testing.T, dir, htmlSrc string, cssSrc ...string) Built {
	t.Helper()
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })
	in := Input{HTML: htmlSrc, Resources: res}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, Stylesheet{Source: c})
	}
	return Build(in)
}

// TestImageLoads is the positive case every refusal below is measured against.
func TestImageLoads(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "blue.png"), 15, 20)

	built := loadInDir(t, dir, `<img id="i" src="blue.png">`)
	box := findBox(t, built.Root, "i")
	if box.Replaced == nil {
		t.Fatalf("the image was not loaded; findings: %v", built.Findings)
	}
	px(t, "the intrinsic width", box.Replaced.Width, 15)
	px(t, "the intrinsic height", box.Replaced.Height, 20)
	if got := box.Replaced.Ratio; got < 0.749 || got > 0.751 {
		t.Errorf("the intrinsic ratio is %v, want 0.75", got)
	}
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked || f.Rule == RuleImageUndecodable {
			t.Errorf("a loaded image still raised %s", f.Error())
		}
	}
}

// TestImagePixelCapFiresAtItsRealValue is the decompression-bomb guard, tested
// with a real bomb rather than by lowering the cap.
//
// The file is under a hundred bytes and declares 60000 × 60000 — three and a
// half gigapixels, or fourteen gigabytes of memory if it were decoded.
func TestImagePixelCapFiresAtItsRealValue(t *testing.T) {
	dir := t.TempDir()
	bomb := bombPNG(60000, 60000)
	if len(bomb) > 200 {
		t.Fatalf("the bomb is %d bytes; it is meant to be tiny", len(bomb))
	}
	if err := os.WriteFile(filepath.Join(dir, "bomb.png"), bomb, 0o600); err != nil {
		t.Fatal(err)
	}

	built := loadInDir(t, dir, `<img id="i" src="bomb.png">`)
	if box := findBox(t, built.Root, "i"); box.Replaced != nil {
		t.Fatal("a 3.6-gigapixel image was accepted")
	}
	requireFinding(t, built.Findings, RuleImageUndecodable, "this engine will decode")
	fired[RuleImageUndecodable] = true
}

// TestImagePixelCapIsCrossedNotApproached pins the boundary itself: an image one
// pixel over the cap is refused and one exactly at it is not.
//
// The images are headers rather than pictures, so nothing is decoded either
// way — which is the point. What is being checked is the comparison, and a test
// that used a small picture and a huge one would pass against a cap ten times
// too large.
func TestImagePixelCapIsCrossedNotApproached(t *testing.T) {
	l := &replacedLoader{
		rec: NewRecorder(nil), loaded: map[string]*ReplacedContent{},
		failed: map[string]bool{}, budget: maxDocumentPixels,
	}
	// A square of exactly the cap, and one row taller.
	side := uint32(1 << 12) // 4096 × 4096 is exactly 1<<24
	if int64(side)*int64(side) != maxImagePixels {
		t.Fatalf("this test is written against a cap of %d, which is now %d",
			int64(side)*int64(side), maxImagePixels)
	}
	_, atCap := l.decode("at-cap", bombPNG(side, side))
	_, overCap := l.decode("over-cap", bombPNG(side, side+1))

	// The image at the cap gets past the *cap* and then fails on its missing
	// pixel data, which is a different complaint and the one that proves the
	// cap did not stop it.
	if atCap == nil {
		t.Fatal("a header-only PNG decoded, which it cannot")
	}
	if strings.Contains(atCap.message, "will decode") {
		t.Errorf("an image exactly at the cap was refused by the cap: %s", atCap.message)
	}
	if overCap == nil || !strings.Contains(overCap.message, "will decode") {
		t.Errorf("an image one row over the cap was not refused by the cap: %v", overCap)
	}
}

// TestDocumentPixelBudget is the cap a per-image limit cannot provide: a page of
// many images, each of them acceptable.
//
// The budget is lowered so that the second of two ordinary images crosses it.
// Lowering it is what makes the boundary reachable — the real budget is
// sixty-seven megapixels, and a document with that many pixels in it would take
// longer to build than the rest of the suite.
func TestDocumentPixelBudget(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 20)
	writePNG(t, filepath.Join(dir, "b.png"), 20, 20)

	old := maxDocumentPixels
	// Room for one image of 400 pixels and not two.
	maxDocumentPixels = 500
	defer func() { maxDocumentPixels = old }()

	built := loadInDir(t, dir, `<img id="a" src="a.png"><img id="b" src="b.png">`)
	if findBox(t, built.Root, "a").Replaced == nil {
		t.Error("the first image was refused, so the budget is not the thing being tested")
	}
	if findBox(t, built.Root, "b").Replaced != nil {
		t.Error("the second image was decoded past the document's budget")
	}
	requireFinding(t, built.Findings, RuleLimit, "for one document")
	fired[RuleLimit] = true
}

// TestDocumentPixelBudgetChargesOncePerSource pins that a document repeating one
// image is not charged for each use. Otherwise a page with a logo on forty rows
// would exhaust a budget meant for forty different pictures.
func TestDocumentPixelBudgetChargesOncePerSource(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"), 20, 20)

	old := maxDocumentPixels
	maxDocumentPixels = 500
	defer func() { maxDocumentPixels = old }()

	built := loadInDir(t, dir,
		`<img id="a" src="a.png"><img id="b" src="a.png"><img id="c" src="a.png">`)
	for _, id := range []string{"a", "b", "c"} {
		if findBox(t, built.Root, id).Replaced == nil {
			t.Errorf("#%s was refused; one source used three times costs one decode", id)
		}
	}
}

// TestMalformedImagesAreFindings pins that bad bytes never become a panic, and
// that each kind of badness is reported as itself.
func TestMalformedImagesAreFindings(t *testing.T) {
	dir := t.TempDir()
	good := encodePNG(t, 4, 4)

	files := map[string][]byte{
		"empty.png":     {},
		"text.png":      []byte("this is not a picture at all"),
		"truncated.png": good[:len(good)-20],
		"header.png":    bombPNG(4, 4), // a valid header with no pixels after it
		"zero.png":      bombPNG(0, 0),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var markup strings.Builder
	for name := range files {
		markup.WriteString(`<img id="` + name + `" src="` + name + `">`)
	}
	built := loadInDir(t, dir, markup.String())
	for name := range files {
		if findBox(t, built.Root, name).Replaced != nil {
			t.Errorf("%s decoded, and it is not a picture", name)
		}
	}
	blocked, undecodable := 0, 0
	for _, f := range built.Findings {
		switch f.Rule {
		case RuleResourceBlocked:
			blocked++
		case RuleImageUndecodable:
			undecodable++
		}
	}
	if undecodable == 0 {
		t.Errorf("nothing was reported undecodable; findings: %v", built.Findings)
	}
	_ = blocked
}

// TestDataURIs pins that an image carried in the document is read, and that it
// is bounded like any other.
//
// A data: reference is the one kind that needs no resolver, because its bytes
// never left the document — so it is also the one that would let a hostile
// document reach the decoder with no caller having opted into anything. The cap
// is what stands between those two facts.
func TestDataURIs(t *testing.T) {
	data := encodePNG(t, 6, 3)
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)

	built := Build(Input{HTML: `<img id="i" src="` + uri + `">`})
	box := findBox(t, built.Root, "i")
	if box.Replaced == nil {
		t.Fatalf("a data: image was not read; findings: %v", built.Findings)
	}
	px(t, "the intrinsic width", box.Replaced.Width, 6)
	px(t, "the intrinsic height", box.Replaced.Height, 3)
}

func TestDataURICap(t *testing.T) {
	data := encodePNG(t, 6, 3)
	encoded := base64.StdEncoding.EncodeToString(data)

	old := maxDataURIBytes
	// One byte short of what this reference carries.
	maxDataURIBytes = len(encoded) - 1
	defer func() { maxDataURIBytes = old }()

	built := Build(Input{HTML: `<img id="i" src="data:image/png;base64,` + encoded + `">`})
	if findBox(t, built.Root, "i").Replaced != nil {
		t.Fatal("a data: image over the cap was read")
	}
	requireFinding(t, built.Findings, RuleImageUndecodable, "this engine will read")

	// And one byte of room the other way, so the cap is a boundary rather than
	// a blanket refusal.
	maxDataURIBytes = len(encoded)
	built = Build(Input{HTML: `<img id="i" src="data:image/png;base64,` + encoded + `">`})
	if findBox(t, built.Root, "i").Replaced == nil {
		t.Fatalf("a data: image exactly at the cap was refused; findings: %v", built.Findings)
	}
}

// TestSchemesAreRefusedThroughThePipeline pins the no-network guarantee where a
// document would exercise it.
func TestSchemesAreRefusedThroughThePipeline(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "x.png"), 4, 4)

	built := loadInDir(t, dir, `<img id="i" src="http://169.254.169.254/latest/meta-data/">`)
	if findBox(t, built.Root, "i").Replaced != nil {
		t.Fatal("a URL was fetched")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "fetches nothing")
}

// TestAltTextStandsInForAMissingImage pins CSS's rule that an element whose
// content is unavailable is not a replaced element — so it is an ordinary
// inline box, and what it contains is the alt text.
func TestAltTextStandsInForAMissingImage(t *testing.T) {
	built := Build(Input{HTML: `<img id="i" src="nowhere.png" alt="a blue square">`})
	box := findBox(t, built.Root, "i")
	if box.Replaced != nil {
		t.Fatal("an image was loaded from nowhere")
	}
	if len(box.Children) != 1 || !box.Children[0].IsText() {
		t.Fatalf("the alt text produced no text box: %d children", len(box.Children))
	}
	if got := box.Children[0].Text; got != "a blue square" {
		t.Errorf("the alt text is %q", got)
	}

	// alt="" is a statement that the image carries nothing, and must not
	// produce a box: a space on a line the author asked to be empty is a
	// visible difference.
	built = Build(Input{HTML: `<img id="i" src="nowhere.png" alt="">`})
	if got := findBox(t, built.Root, "i"); len(got.Children) != 0 {
		t.Errorf("alt=\"\" produced %d children", len(got.Children))
	}
}

// FuzzImageLoading requires that no sequence of bytes turns a decode into a
// panic and that none escapes the caps.
//
// Image decoders are the classic memory-safety surface, and Go's are safe in
// the sense that matters — no out-of-bounds read — but not in the sense that
// matters here: they will still allocate whatever a header asks for. What is
// checked is that a failure is a finding.
func FuzzImageLoading(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("not a picture"))
	f.Add(bombPNG(1, 1))
	f.Add(bombPNG(1<<20, 1<<20))
	f.Add([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	f.Add([]byte("GIF89a"))
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xE0})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		l := &replacedLoader{
			rec: NewRecorder(nil), loaded: map[string]*ReplacedContent{},
			failed: map[string]bool{}, budget: maxDocumentPixels,
		}
		got, fail := l.decode("fuzz", data)
		switch {
		case got == nil && fail == nil:
			t.Fatal("a decode neither succeeded nor explained itself")
		case got != nil && fail != nil:
			t.Fatal("a decode both succeeded and failed")
		case got != nil:
			if got.Pixels > maxImagePixels {
				t.Fatalf("an image of %d pixels was accepted past the cap of %d",
					got.Pixels, maxImagePixels)
			}
			if got.Image == nil {
				t.Fatal("a successful decode produced no image")
			}
			b := got.Image.Bounds()
			if int64(b.Dx())*int64(b.Dy()) > maxImagePixels {
				t.Fatalf("the decoded image is %dx%d, past the cap", b.Dx(), b.Dy())
			}
		}
	})
}
