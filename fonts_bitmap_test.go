package pdf0

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A face whose glyphs are only bitmaps is written as Type 3 fonts whose glyphs
// paint the bitmaps (fonts/type3.go, docs/proposals/bitmap-fonts-type3.md).
// These hold the document to three things outside pdf0's drawing code: what
// Ghostscript and poppler paint, which must be forme's own image of each glyph
// pixel for pixel; pdf0's PDF/A validator; and the text a reader extracts.

// placedGlyph is one glyph drawn alone at a known place on the page.
type placedGlyph struct {
	r      rune
	size   float64
	x, y   float64 // the glyph origin, in points from the bottom left
	colour func(*content.Builder)
	// rgb is the colour the text is painted in, 0-255, for the comparison;
	// nil for black.
	rgb []float64
}

const bitmapPageW, bitmapPageH = 300.0, 120.0

// bitmapCases sets a 1-bit strike in black and in red, a 2-bit strike in red,
// green and grey, a 2-bit strike in a colour the stream never set, which is
// drawn as a stencil, and the 24 and 32 ppem strikes.
func bitmapCases() []placedGlyph {
	return []placedGlyph{
		{r: 'A', size: 9, x: 20, y: 80},
		{r: 'H', size: 9, x: 50, y: 80, colour: func(b *content.Builder) { b.SetRGB(1, 0, 0) }, rgb: []float64{255, 0, 0}},
		{r: 'A', size: 12, x: 80, y: 80, colour: func(b *content.Builder) { b.SetRGB(1, 0, 0) }, rgb: []float64{255, 0, 0}},
		{r: 'M', size: 12, x: 120, y: 80, colour: func(b *content.Builder) { b.SetRGB(0, 0.5, 0) }, rgb: []float64{0, 128, 0}},
		{r: 'W', size: 12, x: 160, y: 80, colour: func(b *content.Builder) { b.SetGray(0) }},
		{r: 'B', size: 12, x: 200, y: 80},
		{r: 'C', size: 24, x: 20, y: 30, colour: func(b *content.Builder) { b.SetRGB(0, 0, 1) }, rgb: []float64{0, 0, 255}},
		{r: 'D', size: 18, x: 80, y: 30, colour: func(b *content.Builder) { b.SetRGB(0.5, 0, 0.5) }, rgb: []float64{128, 0, 128}},
	}
}

// bitmapPage draws the cases into one page of doc.
func bitmapPage(t *testing.T, doc *Document, face *fonts.Face, cases []placedGlyph) {
	t.Helper()
	var b content.Builder
	for _, c := range cases {
		b.Save()
		if c.colour != nil {
			c.colour(&b)
		}
		b.BeginText().SetFont("F1", c.size).SetTextMatrix(1, 0, 0, 1, c.x, c.y)
		if missing := face.DrawShaped(&b, string(c.r), c.size); missing != 0 {
			t.Fatalf("%c is missing", c.r)
		}
		b.EndText().Restore()
	}
	if _, err := doc.AddPage(Page{
		Width: bitmapPageW, Height: bitmapPageH, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face},
	}); err != nil {
		t.Fatalf("adding the page: %v", err)
	}
}

func writeAndRead(t *testing.T, doc *Document) (*Document, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	return back, buf.Bytes()
}

// rasteriser renders page n (from 1) of a PDF at a resolution, with
// anti-aliasing off so a stencil's edge is its own.
type rasteriser struct {
	name   string
	render func(t *testing.T, path string, page, dpi int) image.Image
}

func rasterisers() []rasteriser {
	var out []rasteriser
	if gs, err := exec.LookPath("gs"); err == nil {
		out = append(out, rasteriser{"ghostscript", func(t *testing.T, path string, page, dpi int) image.Image {
			png := filepath.Join(t.TempDir(), "page.png")
			cmd := exec.Command(gs, "-q", "-dNOPAUSE", "-dBATCH", "-dSAFER", "-sDEVICE=png16m",
				fmt.Sprintf("-r%d", dpi), "-dTextAlphaBits=1", "-dGraphicsAlphaBits=1",
				fmt.Sprintf("-dFirstPage=%d", page), fmt.Sprintf("-dLastPage=%d", page),
				"-sOutputFile="+png, path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("gs: %v\n%s", err, out)
			}
			return readPNG(t, png)
		}})
	}
	if pp, err := exec.LookPath("pdftoppm"); err == nil {
		out = append(out, rasteriser{"poppler", func(t *testing.T, path string, page, dpi int) image.Image {
			prefix := filepath.Join(t.TempDir(), "page")
			cmd := exec.Command(pp, "-r", fmt.Sprint(dpi), "-png", "-aa", "no", "-aaVector", "no",
				"-f", fmt.Sprint(page), "-l", fmt.Sprint(page), "-singlefile", path, prefix)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("pdftoppm: %v\n%s", err, out)
			}
			return readPNG(t, prefix+".png")
		}})
	}
	return out
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// glyphImage is forme's image of a glyph at the strike a size picks, which is
// what the page must show: the text size in CSS pixels (fonts.ppemFor).
func glyphImage(t *testing.T, face *fonts.Face, r rune, size float64) shape.Image {
	t.Helper()
	gid, ok := face.GlyphID(r)
	if !ok {
		t.Fatalf("no glyph for %c", r)
	}
	var c oneImage
	ppem := int(math.Round(size * 4 / 3))
	if err := face.PaintGlyph(gid, shape.PaintOptions{PPEM: ppem}, &c); err != nil {
		t.Fatal(err)
	}
	if !c.got {
		t.Fatalf("%c has no image at %d ppem", r, ppem)
	}
	return c.img
}

// oneImage is a painter that keeps a bitmap glyph's image, which is all a
// bitmap-only face paints.
type oneImage struct {
	img shape.Image
	got bool
}

func (o *oneImage) PushTransform(shape.Transform)       {}
func (o *oneImage) PopTransform()                       {}
func (o *oneImage) PushClipGlyph(int)                   {}
func (o *oneImage) PushClipRect(shape.Rect)             {}
func (o *oneImage) PopClip()                            {}
func (o *oneImage) PushGroup()                          {}
func (o *oneImage) PopGroup(shape.CompositeMode)        {}
func (o *oneImage) Solid(shape.Color, bool)             {}
func (o *oneImage) LinearGradient(shape.LinearGradient) {}
func (o *oneImage) RadialGradient(shape.RadialGradient) {}
func (o *oneImage) SweepGradient(shape.SweepGradient)   {}
func (o *oneImage) Image(img shape.Image)               { o.img, o.got = img, true }

// compareGlyph samples the rendered page at the centre of every pixel of the
// glyph's bitmap and returns how many differ from what the pixel's coverage
// says: in a colour the stream set, coverage blends the colour over the white
// page; in one it did not, the glyph is a stencil, painted where the coverage
// is at least half. A 1-bit strike is a stencil either way.
func compareGlyph(page image.Image, dpi int, face *fonts.Face, c placedGlyph) (bad int, detail string) {
	img := glyphImageNoT(face, c.r, c.size)
	upem := float64(face.UnitsPerEm())
	box := img.Box
	binary := true
	for _, v := range img.Data {
		if v != 0 && v != 255 {
			binary = false
		}
	}
	colour := c.rgb
	if colour == nil {
		colour = []float64{0, 0, 0}
	}
	stencil := binary || c.colour == nil
	scale := float64(dpi) / 72
	var first string
	for j := range img.Height {
		for i := range img.Width {
			x := box.XMin + (float64(i)+0.5)*(box.XMax-box.XMin)/float64(img.Width)
			y := box.YMax - (float64(j)+0.5)*(box.YMax-box.YMin)/float64(img.Height)
			px := (c.x + x*c.size/upem) * scale
			py := (bitmapPageH - (c.y + y*c.size/upem)) * scale
			got := page.At(int(px), int(py))
			r, g, b, _ := got.RGBA()
			have := []float64{float64(r >> 8), float64(g >> 8), float64(b >> 8)}
			a := float64(img.Data[j*img.Width+i]) / 255
			if stencil {
				a = 0
				if img.Data[j*img.Width+i] >= 128 {
					a = 1
				}
			}
			ok := true
			for k := range 3 {
				want := 255*(1-a) + colour[k]*a
				if math.Abs(have[k]-want) > 40 {
					ok = false
				}
			}
			if !ok {
				bad++
				if first == "" {
					first = fmt.Sprintf("pixel (%d,%d) of %c at %vpt: coverage %d, painted %v",
						i, j, c.r, c.size, img.Data[j*img.Width+i], have)
				}
			}
		}
	}
	return bad, first
}

func glyphImageNoT(face *fonts.Face, r rune, size float64) shape.Image {
	gid, _ := face.GlyphID(r)
	var c oneImage
	_ = face.PaintGlyph(gid, shape.PaintOptions{PPEM: int(math.Round(size * 4 / 3))}, &c)
	return c.img
}

func TestBitmapGlyphsAreRenderedAsTheFontPaintsThem(t *testing.T) {
	rs := rasterisers()
	if len(rs) == 0 {
		t.Skip("neither Ghostscript nor pdftoppm is on this machine; the rendering is not checked")
	}
	for _, file := range []string{"Strikes.ttf", "StrikesApple.ttf"} {
		t.Run(file, func(t *testing.T) {
			face := bitmapFace(t, file)
			cases := bitmapCases()
			for _, c := range cases {
				glyphImage(t, face, c.r, c.size) // each case must have an image to compare
			}
			doc := NewDocument()
			bitmapPage(t, doc, face, cases)
			_, data := writeAndRead(t, doc)
			path := filepath.Join(t.TempDir(), "bitmap.pdf")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			const dpi = 576 // each bitmap pixel is several device pixels
			for _, r := range rs {
				page := r.render(t, path, 1, dpi)
				for _, c := range cases {
					if bad, first := compareGlyph(page, dpi, face, c); bad != 0 {
						t.Errorf("%s: %d pixels differ; first: %s", r.name, bad, first)
					}
				}
			}
		})
	}
}

// TestBitmapFacesAcrossPagesKeepTheirFonts adds pages that each make the face
// gain sub-fonts and glyphs, which rewrites its embedding in place. Every page
// must still name fonts that draw and extract what it drew, and the whole must
// be valid PDF/A.
func TestBitmapFacesAcrossPagesKeepTheirFonts(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			face := bitmapFace(t, "Strikes.ttf")
			doc := mustPDFADoc(t, level)
			pages := [][]placedGlyph{
				{{r: 'A', size: 9, x: 20, y: 80}, {r: 'B', size: 9, x: 40, y: 80}},
				{{r: 'C', size: 12, x: 20, y: 80, colour: func(b *content.Builder) { b.SetRGB(1, 0, 0) }, rgb: []float64{255, 0, 0}},
					{r: 'A', size: 9, x: 40, y: 80}, {r: 'D', size: 24, x: 60, y: 80}},
				{{r: 'E', size: 24, x: 20, y: 80}, {r: 'F', size: 12, x: 60, y: 80,
					colour: func(b *content.Builder) { b.SetGray(0.25) }, rgb: []float64{64, 64, 64}}},
			}
			for _, p := range pages {
				bitmapPage(t, doc, face, p)
			}
			back, data := writeAndRead(t, doc)
			for _, v := range ValidatePDFA(back, level) {
				t.Errorf("%s", v.Error())
			}
			for i, p := range pages {
				var want strings.Builder
				for _, c := range p {
					want.WriteRune(c.r)
				}
				got, err := back.ExtractPageText(back.PageList()[i])
				if err != nil {
					t.Fatal(err)
				}
				if strings.ReplaceAll(strings.TrimSpace(got), " ", "") != want.String() {
					t.Errorf("page %d extracts as %q, want %q", i+1, got, want.String())
				}
			}
			rs := rasterisers()
			if len(rs) == 0 {
				return
			}
			path := filepath.Join(t.TempDir(), "pages.pdf")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			const dpi = 576
			for _, r := range rs {
				for i, p := range pages {
					page := r.render(t, path, i+1, dpi)
					for _, c := range p {
						if c.r == 'E' {
							continue // drawn from another strike than its size picks: see the fonts tests
						}
						if bad, first := compareGlyph(page, dpi, face, c); bad != 0 {
							t.Errorf("%s, page %d: %d pixels differ; first: %s", r.name, i+1, bad, first)
						}
					}
				}
			}
		})
	}
}

// emojiFace is Noto Color Emoji's CBDT build: colour bitmaps and no outlines.
func emojiFace(t *testing.T) *fonts.Face {
	t.Helper()
	data, err := os.ReadFile(testfiles.NotoEmoji.File(t, "NotoColorEmoji.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !f.BitmapOnly() {
		t.Fatal("the emoji face has outlines")
	}
	return f
}

// TestColourBitmapGlyphsAreTheirImages draws emoji — one, a skin-tone sequence
// the font draws as one glyph, a flag — and holds the page to the font's own
// PNG of each glyph composited over white, as Ghostscript and poppler paint
// it; to PDF/A; and to the text a reader gets back.
func TestColourBitmapGlyphsAreTheirImages(t *testing.T) {
	face := emojiFace(t)
	type run struct {
		text string
		x, y float64
	}
	const size = 24
	runs := []run{{"😀", 20, 60}, {"👍🏽", 70, 60}, {"🇪🇸", 120, 60}, {"🎉", 170, 60}}
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			f := face.Clone()
			doc := mustPDFADoc(t, level)
			var b content.Builder
			var want strings.Builder
			for _, r := range runs {
				b.BeginText().SetFont("F1", size).SetTextMatrix(1, 0, 0, 1, r.x, r.y)
				if missing := f.DrawShaped(&b, r.text, size); missing != 0 {
					t.Fatalf("%q: %d missing", r.text, missing)
				}
				b.EndText()
				want.WriteString(r.text)
			}
			if _, err := doc.AddPage(Page{
				Width: bitmapPageW, Height: bitmapPageH, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F1": f},
			}); err != nil {
				t.Fatal(err)
			}
			back, data := writeAndRead(t, doc)
			for _, v := range ValidatePDFA(back, level) {
				t.Errorf("%s", v.Error())
			}
			got := strings.ReplaceAll(strings.TrimSpace(mustExtractText(t, back)), " ", "")
			if got != want.String() {
				t.Errorf("extracted %q, want %q", got, want.String())
			}
			if level != pdfa.PDFA4 {
				return // one rendering is enough
			}
			rs := rasterisers()
			if len(rs) == 0 {
				return
			}
			path := filepath.Join(t.TempDir(), "emoji.pdf")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			const dpi = 1200
			for _, rz := range rs {
				page := rz.render(t, path, 1, dpi)
				for _, r := range runs {
					glyphs, _ := f.ShapeGlyphs(r.text)
					if len(glyphs) != 1 {
						t.Fatalf("%q shaped as %d glyphs; the case needs one", r.text, len(glyphs))
					}
					// Poppler smooths an image it scales up, so its edges are
					// compared only where they are flat; Ghostscript does not,
					// and every pixel is compared.
					bad, total, first := compareColourGlyph(t, page, dpi, f, glyphs[0].GID, size, r.x, r.y, rz.name == "poppler")
					if bad != 0 {
						t.Errorf("%s: %q: %d of %d pixels differ; first: %s", rz.name, r.text, bad, total, first)
					}
				}
			}
		})
	}
}

// compareColourGlyph samples the page at the centre of every pixel of a colour
// glyph's PNG, and counts those that differ from the pixel composited over
// white. smooth compares only the pixels whose neighbours are their colour.
func compareColourGlyph(t *testing.T, page image.Image, dpi int, face *fonts.Face, gid int, size, x0, y0 float64, smooth bool) (bad, total int, first string) {
	t.Helper()
	var c oneImage
	if err := face.PaintGlyph(gid, shape.PaintOptions{PPEM: int(math.Round(size * 4 / 3))}, &c); err != nil || !c.got {
		t.Fatalf("glyph %d has no image: %v", gid, err)
	}
	if c.img.Format != shape.ImagePNG {
		t.Fatalf("glyph %d is not a PNG", gid)
	}
	src, err := png.Decode(bytes.NewReader(c.img.Data))
	if err != nil {
		t.Fatal(err)
	}
	upem := float64(face.UnitsPerEm())
	box := c.img.Box
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	scale := float64(dpi) / 72
	over := func(i, j int) [3]float64 {
		nr, ng, nb, na := src.At(src.Bounds().Min.X+i, src.Bounds().Min.Y+j).RGBA()
		a := float64(na) / 0xFFFF
		// Premultiplied, so composited over white is c + 255(1-a).
		return [3]float64{float64(nr>>8) + 255*(1-a), float64(ng>>8) + 255*(1-a), float64(nb>>8) + 255*(1-a)}
	}
	// interior reports whether every neighbour of a pixel is its colour: a
	// renderer that smooths an image it scales up (poppler does, below 4×)
	// blends a pixel with its neighbours where its centre is not the device
	// pixel's, which is a difference only where the neighbours differ.
	interior := func(i, j int) bool {
		c := over(i, j)
		for dj := -1; dj <= 1; dj++ {
			for di := -1; di <= 1; di++ {
				if i+di < 0 || j+dj < 0 || i+di >= w || j+dj >= h {
					return false
				}
				n := over(i+di, j+dj)
				for k := range 3 {
					if math.Abs(n[k]-c[k]) > 8 {
						return false
					}
				}
			}
		}
		return true
	}
	for j := range h {
		for i := range w {
			if smooth && !interior(i, j) {
				continue
			}
			x := box.XMin + (float64(i)+0.5)*(box.XMax-box.XMin)/float64(w)
			y := box.YMax - (float64(j)+0.5)*(box.YMax-box.YMin)/float64(h)
			px := (x0 + x*size/upem) * scale
			py := (bitmapPageH - (y0 + y*size/upem)) * scale
			r, g, bl, _ := page.At(int(px), int(py)).RGBA()
			have := [3]float64{float64(r >> 8), float64(g >> 8), float64(bl >> 8)}
			want := over(i, j)
			total++
			for k := range 3 {
				if math.Abs(have[k]-want[k]) > 40 {
					bad++
					if first == "" {
						first = fmt.Sprintf("pixel (%d,%d): painted %v, want %v", i, j, have, want)
					}
					break
				}
			}
		}
	}
	return bad, total, first
}

// TestPDFA1TakesTheStencilsAndReportsTheSoftMasks pins a known limit. PDF/A-1
// forbids transparency, so an image with an /SMask is not PDF/A-1 (6.4): a
// stencil glyph is, and a greyscale glyph in a colour, whose anti-aliasing is
// its soft mask, is reported by the validator rather than written as
// something else.
func TestPDFA1TakesTheStencilsAndReportsTheSoftMasks(t *testing.T) {
	red := func(b *content.Builder) { b.SetRGB(1, 0, 0) }
	for _, tc := range []struct {
		name  string
		glyph placedGlyph
		want  string // the rule reported, or "" for none
	}{
		{"1-bit", placedGlyph{r: 'A', size: 9, x: 20, y: 80, colour: red}, ""},
		{"greyscale in an unknown colour", placedGlyph{r: 'A', size: 12, x: 20, y: 80}, ""},
		{"greyscale in a colour", placedGlyph{r: 'A', size: 12, x: 20, y: 80, colour: red}, "6.4"},
	} {
		doc := mustPDFADoc(t, pdfa.PDFA1b)
		bitmapPage(t, doc, bitmapFace(t, "Strikes.ttf"), []placedGlyph{tc.glyph})
		back, _ := writeAndRead(t, doc)
		var rules []string
		for _, v := range ValidatePDFA(back, pdfa.PDFA1b) {
			rules = append(rules, v.Rule)
		}
		switch {
		case tc.want == "" && len(rules) != 0:
			t.Errorf("%s: PDF/A-1b reports %v", tc.name, rules)
		case tc.want != "" && (len(rules) != 1 || rules[0] != tc.want):
			t.Errorf("%s: PDF/A-1b reports %v, want %s alone", tc.name, rules, tc.want)
		}
	}
}
