package pdf0

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A face with COLR colour glyphs is drawn as Type 3 fonts whose glyphs are the
// PDF translation of its painting (fonts/colr.go). The oracle is HarfBuzz's
// own renderer: hb-view paints the same text with cairo, and Ghostscript and
// poppler must paint what it does from the PDF pdf0 writes.

// colrRow is a line of text in a colour font, the size it is set at in
// pixels, and the text colour.
type colrRow struct {
	text string
	size float64
	rgb  [3]float64
}

// hbView renders a row with hb-view: a white background, a margin, the text
// colour as the foreground.
func hbView(t *testing.T, hb, font string, r colrRow) image.Image {
	t.Helper()
	out := filepath.Join(t.TempDir(), "hb.png")
	fg := fmt.Sprintf("%02x%02x%02x", int(r.rgb[0]*255), int(r.rgb[1]*255), int(r.rgb[2]*255))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, hb, "--font-size="+fmt.Sprint(r.size), "--margin=16", "--background=ffffff",
		"--foreground="+fg, "--output-format=png", "-o", out, font, r.text)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hb-view: %v\n%s", err, b)
	}
	return readPNG(t, out)
}

// colrPage writes a row as pdf0 draws it, on a page of the hb-view image's
// size in points, the baseline where hb-view puts it to within the
// comparison's search.
func colrPage(t *testing.T, face *fonts.Face, r colrRow, w, h int) []byte {
	t.Helper()
	d := face.Descriptor()
	asc := float64(d.Ascent) * r.size / float64(face.UnitsPerEm())
	var b content.Builder
	b.SetRGB(r.rgb[0], r.rgb[1], r.rgb[2])
	b.BeginText().SetFont("F1", r.size).SetTextMatrix(1, 0, 0, 1, 16, float64(h)-16-asc)
	face.DrawShaped(&b, r.text, r.size)
	b.EndText()
	doc := NewDocument()
	if _, err := doc.AddPage(Page{Width: float64(w), Height: float64(h), Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		t.Fatalf("%q: %v", r.text, err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// differ is how many of the pixels either image inks differ by more than
// anti-aliasing does, at the offset of got within reach that matches want
// best. A pixel matches one within a pixel of it in the other image: where an
// edge falls within a pixel, and how it is anti-aliased, differ between
// renderers, and that is not what is compared.
func differ(want, got image.Image, reach int) (bad, ink int, at [2]int) {
	return differWithin(want, got, reach, 72)
}

// differWithin is differ with a pixel matching one whose every channel is
// within tol of it.
func differWithin(want, got image.Image, reach, tol int) (bad, ink int, at [2]int) {
	b := want.Bounds()
	px := func(img image.Image, x, y int) [3]int {
		if !(image.Point{x, y}.In(img.Bounds())) {
			return [3]int{255, 255, 255}
		}
		r, g, bl, _ := img.At(x, y).RGBA()
		return [3]int{int(r >> 8), int(g >> 8), int(bl >> 8)}
	}
	far := func(a, c [3]int) bool {
		for k := range 3 {
			if d := a[k] - c[k]; d > tol || d < -tol {
				return true
			}
		}
		return false
	}
	white := [3]int{255, 255, 255}
	// The two inks' boxes put side by side, which is where hb-view's layout
	// and the page's agree: hb-view sizes and places its image by the ink as
	// well as the advance, and a glyph that advances by nothing it places by
	// its ink alone.
	inkBox := func(img image.Image) (x0, y0 int, ok bool) {
		x0, y0 = math.MaxInt, math.MaxInt
		ib := img.Bounds()
		for y := ib.Min.Y; y < ib.Max.Y; y++ {
			for x := ib.Min.X; x < ib.Max.X; x++ {
				if px(img, x, y) != white {
					x0, y0, ok = min(x0, x), min(y0, y), true
				}
			}
		}
		return x0, y0, ok
	}
	cx, cy := 0, 0
	if wx, wy, ok := inkBox(want); ok {
		if gx, gy, ok := inkBox(got); ok {
			cx, cy = gx-wx, gy-wy
		}
	}
	// Then the offset about that, by plain difference, which is cheap.
	best := -1
	for dy := cy - reach; dy <= cy+reach; dy++ {
		for dx := cx - reach; dx <= cx+reach; dx++ {
			n := 0
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					if far(px(want, x, y), px(got, x+dx, y+dy)) {
						n++
					}
				}
			}
			if best < 0 || n < best {
				best, at = n, [2]int{dx, dy}
			}
		}
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			w, g := px(want, x, y), px(got, x+at[0], y+at[1])
			if w == white && g == white {
				continue
			}
			ink++
			match := false
			for oy := -1; oy <= 1 && !match; oy++ {
				for ox := -1; ox <= 1 && !match; ox++ {
					match = !far(w, px(got, x+at[0]+ox, y+at[1]+oy))
				}
			}
			if !match {
				bad++
			}
		}
	}
	return bad, ink, at
}

// everyGlyphReachable is a font with its cmap replaced by one mapping U+F0000
// plus n to glyph n, so that hb-view, which draws text, can draw any glyph,
// and pdf0 draws the same one from the same text.
func everyGlyphReachable(t *testing.T, data []byte) []byte {
	t.Helper()
	tables := font.SFNTTables(data)
	n := int(binary.BigEndian.Uint16(tables["maxp"][4:6]))
	var cmap []byte
	cmap = binary.BigEndian.AppendUint16(cmap, 0) // version
	cmap = binary.BigEndian.AppendUint16(cmap, 1) // one encoding
	cmap = binary.BigEndian.AppendUint16(cmap, 3) // Windows
	cmap = binary.BigEndian.AppendUint16(cmap, 10)
	cmap = binary.BigEndian.AppendUint32(cmap, 12)
	cmap = binary.BigEndian.AppendUint16(cmap, 12) // format 12
	cmap = binary.BigEndian.AppendUint16(cmap, 0)
	cmap = binary.BigEndian.AppendUint32(cmap, 16+12)
	cmap = binary.BigEndian.AppendUint32(cmap, 0) // language
	cmap = binary.BigEndian.AppendUint32(cmap, 1) // groups
	cmap = binary.BigEndian.AppendUint32(cmap, 0xF0000)
	cmap = binary.BigEndian.AppendUint32(cmap, 0xF0000+uint32(n)-1)
	cmap = binary.BigEndian.AppendUint32(cmap, 0)
	tables["cmap"] = cmap
	return assembleSFNT(tables)
}

// skiaGlyphs renders a font's COLR glyphs with Skia (testdata/colrv1/
// skia_render.py) at scale pixels per font unit: each glyph's image, the box
// it covers in pixels with y up, and the glyphs Skia refused. The Python
// with the pinned packages is PDF0_SKIA_PYTHON; the test skips without it.
func skiaGlyphs(t *testing.T, font string, scale float64) (images map[int]image.Image, boxes map[int][4]int, refused map[int]string) {
	t.Helper()
	py := os.Getenv("PDF0_SKIA_PYTHON")
	if py == "" {
		t.Skip("PDF0_SKIA_PYTHON is not set: no Skia to compare colour glyphs with (pip install -r testdata/colrv1/requirements.txt)")
	}
	dir := t.TempDir()
	cmd := exec.Command(py, filepath.Join("testdata", "colrv1", "skia_render.py"), font, dir, fmt.Sprint(scale))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("skia_render.py: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index map[string]struct {
		Name  string `json:"name"`
		Box   [4]int `json:"box"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	images, boxes, refused = map[int]image.Image{}, map[int][4]int{}, map[int]string{}
	for k, v := range index {
		gid, _ := strconv.Atoi(k)
		if v.Error != "" {
			refused[gid] = v.Error
			continue
		}
		images[gid] = overWhite(readPNG(t, filepath.Join(dir, k+".png")))
		boxes[gid] = v.Box
	}
	return images, boxes, refused
}

// glyphPage draws one glyph of a face whose cmap reaches every glyph
// (everyGlyphReachable) at scale points per font unit, on a page covering box
// in points, y up, as Skia's image covers it in pixels.
func glyphPage(t *testing.T, data []byte, gid int, scale float64, box [4]int, rgb [3]float64) ([]byte, error) {
	t.Helper()
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	size := float64(face.UnitsPerEm()) * scale
	var b content.Builder
	b.SetRGB(rgb[0], rgb[1], rgb[2])
	b.BeginText().SetFont("F1", size).SetTextMatrix(1, 0, 0, 1, -float64(box[0]), -float64(box[1]))
	face.DrawShaped(&b, string(rune(0xF0000+gid)), size)
	b.EndText()
	doc := NewDocument()
	if _, err := doc.AddPage(Page{Width: float64(box[2] - box[0]), Height: float64(box[3] - box[1]), Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), nil
}

// overWhite is an image composited over white, as a page shows it: Skia's
// images are transparent where nothing is painted.
func overWhite(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA() // premultiplied
			out.Set(x, y, color.RGBA{
				R: uint8((r + 0xFFFF - a) >> 8), G: uint8((g + 0xFFFF - a) >> 8),
				B: uint8((bl + 0xFFFF - a) >> 8), A: 0xFF,
			})
		}
	}
	return out
}

// paintSummary is what a glyph's painting uses: its composite modes, and
// whether it paints in the foreground colour.
type paintSummary struct {
	modes      map[shape.CompositeMode]bool
	foreground bool
}

func (s *paintSummary) PushTransform(shape.Transform)       {}
func (s *paintSummary) PopTransform()                       {}
func (s *paintSummary) PushClipGlyph(int)                   {}
func (s *paintSummary) PushClipRect(shape.Rect)             {}
func (s *paintSummary) PopClip()                            {}
func (s *paintSummary) PushGroup()                          {}
func (s *paintSummary) PopGroup(m shape.CompositeMode)      { s.modes[m] = true }
func (s *paintSummary) Solid(_ shape.Color, fg bool)        { s.foreground = s.foreground || fg }
func (s *paintSummary) LinearGradient(shape.LinearGradient) {}
func (s *paintSummary) RadialGradient(shape.RadialGradient) {}
func (s *paintSummary) SweepGradient(shape.SweepGradient)   {}
func (s *paintSummary) Image(shape.Image)                   {}

func summarise(t *testing.T, face *fonts.Face, gid int) paintSummary {
	t.Helper()
	s := paintSummary{modes: map[shape.CompositeMode]bool{}}
	if err := face.PaintGlyph(gid, shape.PaintOptions{}, &s); err != nil {
		t.Fatalf("glyph %d: %v", gid, err)
	}
	return s
}

// inexact are the composite modes PDF has no exact equivalent for, which
// pdf0 refuses rather than approximates.
var inexact = map[shape.CompositeMode]bool{
	shape.CompositeSrcAtop: true, shape.CompositeDestAtop: true,
	shape.CompositeXor: true, shape.CompositePlus: true,
}

// knownRendererDefects are glyphs a renderer draws wrongly from a PDF that is
// right — another renderer draws it as Skia does — with what was found.
var knownRendererDefects = map[string]map[int]string{
	// paint_glyph_nested_identity_rotate_center: Ghostscript 10.02 paints a
	// shading as one flat colour inside two nested clips, either of which
	// alone it draws correctly. Reproduced on a plain page, outside any Type
	// 3 font; poppler draws it as Skia does.
	"ghostscript": {208: "Ghostscript draws a shading inside two nested clips flat"},
}

// TestColourGlyphsAreWhatSkiaPaints holds every glyph of Google's COLRv1
// conformance font (testdata/colrv1) to Skia, the renderer COLRv1 was
// specified against: each is drawn by pdf0 as a Type 3 glyph, rendered by
// Ghostscript and poppler, and compared pixel by pixel with Skia's drawing.
// A glyph that composites in a mode PDF cannot state is refused, and only
// such a glyph.
//
// HarfBuzz's cairo renderer is not the oracle here: it draws the sweeps whose
// angles pass a full turn differently from Skia, and pdf0 draws them as Skia
// does.
func TestColourGlyphsAreWhatSkiaPaints(t *testing.T) {
	rs := rasterisers()
	if len(rs) == 0 {
		t.Skip("neither Ghostscript nor pdftoppm is on this machine")
	}
	const scale = 0.25
	font := filepath.Join("testdata", "colrv1", "test_glyphs-glyf_colr_1.ttf")
	want, boxes, _ := skiaGlyphs(t, font, scale)
	raw, err := os.ReadFile(font)
	if err != nil {
		t.Fatal(err)
	}
	data := everyGlyphReachable(t, raw)
	probe, err := fonts.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	gids := make([]int, 0, len(want))
	for g := range want {
		gids = append(gids, g)
	}
	sort.Ints(gids)
	if len(gids) < 150 {
		t.Fatalf("Skia drew %d glyphs; the font has about 200", len(gids))
	}
	compared := 0
	for _, gid := range gids {
		refuse := false
		for m := range summarise(t, probe, gid).modes {
			refuse = refuse || inexact[m]
		}
		pdf, err := glyphPage(t, data, gid, scale, boxes[gid], [3]float64{0, 0, 0})
		switch {
		case refuse && err == nil:
			t.Errorf("glyph %d composites in a mode PDF cannot state, and was drawn", gid)
			continue
		case refuse:
			if !strings.Contains(err.Error(), "no exact equivalent") {
				t.Errorf("glyph %d was refused for the wrong reason: %v", gid, err)
			}
			continue
		case err != nil:
			t.Errorf("glyph %d: %v", gid, err)
			continue
		}
		path := filepath.Join(t.TempDir(), "glyph.pdf")
		if err := os.WriteFile(path, pdf, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if _, known := knownRendererDefects[r.name][gid]; known {
				continue
			}
			// A glyph Skia draws nothing for, such as a sweep that repeats a
			// colour line of no length, inks nothing: a match, if pdf0 inks
			// nothing either, which bad says.
			got := r.renderAA(t, path, 1, 72)
			bad, ink, at := differ(want[gid], got, 1)
			compared += ink
			if float64(bad) > 0.04*float64(max(ink, 1)) {
				t.Errorf("glyph %d: %s differs from Skia on %d of %d pixels", gid, r.name, bad, ink)
			}
			// Where Skia's drawing is flat or smoothly shaded, which is
			// where a wrong colour, alpha, gradient or composite shows and
			// anti-aliasing does not, the match is close.
			if bad, n := interiorDiffer(want[gid], got, at, 12); float64(bad) > 0.01*float64(max(n, 1)) {
				t.Errorf("glyph %d: %s differs from Skia inside its shapes on %d of %d pixels", gid, r.name, bad, n)
			}
		}
	}
	if compared < 1_000_000 {
		t.Errorf("only %d inked pixels were compared: the comparison is not looking at the glyphs", compared)
	}
}

// TestColourEmojiAreWhatHarfBuzzPaints is the second opinion, on a real
// colour font: Noto Color Emoji's COLRv1 build, as hb-view draws it — radial
// and linear gradients, alpha, a flag that composites with SrcIn and
// SoftLight.
func TestColourEmojiAreWhatHarfBuzzPaints(t *testing.T) {
	hb, err := exec.LookPath("hb-view")
	if err != nil {
		t.Skip("no hb-view on this machine")
	}
	rs := rasterisers()
	if len(rs) == 0 {
		t.Skip("neither Ghostscript nor pdftoppm is on this machine")
	}
	font := testfiles.NotoEmoji.File(t, "Noto-COLRv1.ttf")
	data, err := os.ReadFile(font)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"😀🎉👍🏽", "🇪🇸🌈🔥"} {
		row := colrRow{text, 192, [3]float64{0, 0, 0}}
		want := hbView(t, hb, font, row)
		face, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "emoji.pdf")
		if err := os.WriteFile(path, colrPage(t, face, row, want.Bounds().Dx(), want.Bounds().Dy()), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			got := r.renderAA(t, path, 1, 72)
			bad, ink, at := differ(want, got, 2)
			if ink == 0 || float64(bad) > 0.03*float64(ink) {
				t.Errorf("%q: %s differs from HarfBuzz on %d of %d pixels", text, r.name, bad, ink)
			}
			if bad, n := interiorDiffer(want, got, at, 12); n == 0 || float64(bad) > 0.01*float64(n) {
				t.Errorf("%q: %s differs from HarfBuzz inside its shapes on %d of %d pixels", text, r.name, bad, n)
			}
		}
	}
}

// TestAColourGlyphTakesTheTextColour: a glyph that paints in the foreground
// colour is drawn in the colour the text is shown in, from one font whatever
// that colour is, as hb-view draws it with that foreground.
func TestAColourGlyphTakesTheTextColour(t *testing.T) {
	hb, err := exec.LookPath("hb-view")
	if err != nil {
		t.Skip("no hb-view on this machine")
	}
	rs := rasterisers()
	if len(rs) == 0 {
		t.Skip("neither Ghostscript nor pdftoppm is on this machine")
	}
	data := everyGlyphReachable(t, formeFile(t, "testdata/harfbuzz/fonts/ColourPaint.ttf"))
	probe, err := fonts.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	gid := -1
	for g := range probe.NumGlyphs() {
		if k := probe.GlyphColour(g, 0); (k == shape.ColourPaint || k == shape.ColourLayers) && summarise(t, probe, g).foreground {
			gid = g
			break
		}
	}
	if gid < 0 {
		t.Fatal("the fixture has no colour glyph in the foreground colour")
	}
	font := filepath.Join(t.TempDir(), "paint.ttf")
	if err := os.WriteFile(font, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rgb := range [][3]float64{{0.8, 0, 0}, {0, 0.6, 0.2}} {
		row := colrRow{string(rune(0xF0000 + gid)), 128, rgb}
		want := hbView(t, hb, font, row)
		face, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "fg.pdf")
		if err := os.WriteFile(path, colrPage(t, face, row, want.Bounds().Dx(), want.Bounds().Dy()), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			got := r.renderAA(t, path, 1, 72)
			bad, ink, at := differ(want, got, 2)
			if ink == 0 || float64(bad) > 0.03*float64(ink) {
				t.Errorf("in %v: %s differs from HarfBuzz on %d of %d pixels", rgb, r.name, bad, ink)
			}
			if bad, n := interiorDiffer(want, got, at, 12); n == 0 || float64(bad) > 0.01*float64(n) {
				t.Errorf("in %v: %s differs from HarfBuzz inside its shapes on %d of %d pixels", rgb, r.name, bad, n)
			}
		}
		if n := face.NumSubfonts(); n != 0 {
			t.Errorf("in %v the face drew %d fonts beside its own: a colour glyph is keyed by no colour", rgb, n)
		}
	}
}

// TestColourGlyphDocumentsAreValidPDFA draws every conformance glyph pdf0
// writes and Noto's emoji into one document, which is valid PDF/A-2b and -4,
// and extracts as drawn.
func TestColourGlyphDocumentsAreValidPDFA(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "colrv1", "test_glyphs-glyf_colr_1.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	data := everyGlyphReachable(t, raw)
	notoData, err := os.ReadFile(testfiles.NotoEmoji.File(t, "Noto-COLRv1.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	const emoji = "😀🎉👍🏽🇪🇸🌈🔥"
	for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			face, err := fonts.Load(data)
			if err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for g := 1; g < face.NumGlyphs(); g++ {
				refuse := false
				for m := range summarise(t, face, g).modes {
					refuse = refuse || inexact[m]
				}
				if !refuse {
					text.WriteRune(rune(0xF0000 + g))
				}
			}
			noto, err := fonts.Load(notoData)
			if err != nil {
				t.Fatal(err)
			}
			doc := mustPDFADoc(t, level)
			var b content.Builder
			b.SetRGB(0, 0, 0)
			b.BeginText().SetFont("F1", 20).SetTextMatrix(1, 0, 0, 1, 10, 150)
			face.DrawShaped(&b, text.String(), 20)
			b.EndText()
			b.BeginText().SetFont("F2", 20).SetTextMatrix(1, 0, 0, 1, 10, 10)
			noto.DrawShaped(&b, emoji, 20)
			b.EndText()
			if _, err := doc.AddPage(Page{Width: 600, Height: 200, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F1": face, "F2": noto}}); err != nil {
				t.Fatal(err)
			}
			back, out := writeAndRead(t, doc)
			for _, k := range []string{"/Type3", "/ShadingType 1", "/ShadingType 2", "/ShadingType 3", "/SMask", "/BM"} {
				if !bytes.Contains(out, []byte(k)) {
					t.Errorf("the document has no %s: it does not exercise what it validates", k)
				}
			}
			for _, v := range ValidatePDFA(back, level) {
				t.Errorf("%s", v.Error())
			}
			if got := mustExtractText(t, back); !strings.Contains(strings.ReplaceAll(got, " ", ""), emoji) {
				t.Errorf("the emoji extract as %q", got)
			}
		})
	}
}

// TestColourGlyphsInAPDFA1Document: PDF/A-1 forbids transparency, which
// colour glyphs are drawn with. A colour glyph with an outline of its own is
// drawn as that outline in the text colour, as before colour glyphs were
// drawn at all; one with none is refused rather than drawn blank.
func TestColourGlyphsInAPDFA1Document(t *testing.T) {
	draw := func(t *testing.T, data []byte, text string) (*Document, error) {
		face, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		doc := mustPDFADoc(t, pdfa.PDFA1b)
		var b content.Builder
		b.SetRGB(1, 0, 0)
		b.BeginText().SetFont("F1", 40).SetTextMatrix(1, 0, 0, 1, 10, 10)
		face.DrawShaped(&b, text, 40)
		b.EndText()
		if _, err := doc.AddPage(Page{Width: 100, Height: 100, Content: &b,
			Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
			return nil, err
		}
		back, out := writeAndRead(t, doc)
		if bytes.Contains(out, []byte("/SMask")) || bytes.Contains(out, []byte("/ShadingType")) {
			t.Errorf("the PDF/A-1 document paints the colour glyph in colour")
		}
		return back, nil
	}
	t.Run("with an outline", func(t *testing.T) {
		// Every colour glyph of forme's ColourPaint fixture has an outline
		// of its own.
		data := everyGlyphReachable(t, formeFile(t, "testdata/harfbuzz/fonts/ColourPaint.ttf"))
		probe, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		gid := -1
		for g := range probe.NumGlyphs() {
			if k := probe.GlyphColour(g, 0); k == shape.ColourPaint || k == shape.ColourLayers {
				gid = g
				break
			}
		}
		back, err := draw(t, data, string(rune(0xF0000+gid)))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range ValidatePDFA(back, pdfa.PDFA1b) {
			t.Errorf("%s", v.Error())
		}
	})
	t.Run("without", func(t *testing.T) {
		// Noto Color Emoji's COLRv1 glyphs are paint alone.
		data, err := os.ReadFile(testfiles.NotoEmoji.File(t, "Noto-COLRv1.ttf"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := draw(t, data, "😀"); err == nil || !strings.Contains(err.Error(), "no outline") {
			t.Errorf("a colour glyph with no outline in PDF/A-1: %v", err)
		}
	})
}

// TestColourGlyphsCanBeTheirOutlines: SetColourGlyphs(false) embeds a colour
// face as its outlines, as before colour glyphs were drawn.
func TestColourGlyphsCanBeTheirOutlines(t *testing.T) {
	face, err := fonts.Load(formeFile(t, "testdata/harfbuzz/fonts/ColourInk.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	face.SetColourGlyphs(false)
	doc := NewDocument()
	var b content.Builder
	b.BeginText().SetFont("F1", 40)
	face.DrawShaped(&b, "AB", 40)
	b.EndText()
	if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		t.Fatal(err)
	}
	back, _ := writeAndRead(t, doc)
	res := back.ResolveDict(back.PageList()[0].Get("Resources"))
	if st := back.ResolveDict(back.ResolveDict(res.Get("Font")).Get("F1")).Get("Subtype"); st != object.Name("Type0") {
		t.Errorf("the face is embedded as %v, not its outlines", st)
	}
}

// interiorDiffer counts the pixels inside want's flat or smoothly shaded
// areas — every neighbour within 6 of the pixel in every channel — that got
// does not match to within tol, at the offset differ aligned the two at.
// Anti-aliasing changes an edge, not an interior: this is where a wrong
// colour, alpha or gradient shows, and it is held to a tight tolerance.
func interiorDiffer(want, got image.Image, at [2]int, tol int) (bad, interior int) {
	b := want.Bounds()
	px := func(img image.Image, x, y int) [3]int {
		if !(image.Point{x, y}.In(img.Bounds())) {
			return [3]int{255, 255, 255}
		}
		r, g, bl, _ := img.At(x, y).RGBA()
		return [3]int{int(r >> 8), int(g >> 8), int(bl >> 8)}
	}
	near := func(a, c [3]int, d int) bool {
		for k := range 3 {
			if v := a[k] - c[k]; v > d || v < -d {
				return false
			}
		}
		return true
	}
	white := [3]int{255, 255, 255}
	for y := b.Min.Y + 1; y < b.Max.Y-1; y++ {
		for x := b.Min.X + 1; x < b.Max.X-1; x++ {
			w := px(want, x, y)
			if w == white {
				continue
			}
			flat := true
			for oy := -1; oy <= 1 && flat; oy++ {
				for ox := -1; ox <= 1 && flat; ox++ {
					flat = near(w, px(want, x+ox, y+oy), 6)
				}
			}
			if !flat {
				continue
			}
			interior++
			if !near(w, px(got, x+at[0], y+at[1]), tol) {
				bad++
			}
		}
	}
	return bad, interior
}

// TestAColourDocumentIsTheSameEveryTime: the same colour glyphs drawn into two
// documents write the same bytes. A colour glyph names many resources, and
// writing them in map order would make every run differ.
func TestAColourDocumentIsTheSameEveryTime(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "colrv1", "test_glyphs-glyf_colr_1.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	data := everyGlyphReachable(t, raw)
	notoData, err := os.ReadFile(testfiles.NotoEmoji.File(t, "Noto-COLRv1.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	write := func() []byte {
		face, err := fonts.Load(data)
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for g := 1; g < face.NumGlyphs(); g++ {
			refuse := false
			for m := range summarise(t, face, g).modes {
				refuse = refuse || inexact[m]
			}
			if !refuse {
				text.WriteRune(rune(0xF0000 + g))
			}
		}
		noto, err := fonts.Load(notoData)
		if err != nil {
			t.Fatal(err)
		}
		doc := NewDocument()
		var b content.Builder
		// A colour the stream states: a gradient with a stop in the text
		// colour needs to know it.
		b.SetRGB(0, 0, 0)
		b.BeginText().SetFont("F1", 20)
		face.DrawShaped(&b, text.String(), 20)
		b.EndText()
		// Noto's flags group gradients with soft masks and blend modes, so
		// that one form names several resources of a kind.
		b.BeginText().SetFont("F2", 20)
		noto.DrawShaped(&b, "🇪🇸🇬🇧🇺🇸🇯🇵🇧🇷🇿🇦😀🔥", 20)
		b.EndText()
		if _, err := doc.AddPage(Page{Width: 600, Height: 200, Content: &b,
			Faces: map[object.Name]*fonts.Face{"F1": face, "F2": noto}}); err != nil {
			t.Fatal(err)
		}
		_, out := writeAndRead(t, doc)
		return out
	}
	// The file identifier is made afresh for each file written, by design;
	// everything else is the drawing.
	id := regexp.MustCompile(`/ID \[<[0-9A-F]+> <[0-9A-F]+>\]`)
	if a, b := id.ReplaceAll(write(), nil), id.ReplaceAll(write(), nil); !bytes.Equal(a, b) {
		t.Error("two documents of the same colour glyphs differ")
	}
}
