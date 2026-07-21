package pdf0

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// TestJPXParseCodestream parses the OpenJPEG conformance codestream p0_01.j2k
// (128x128 grayscale, 8-bit, reversible 5/3, no quantization) and checks the
// codestream parser extracts the documented structure.
func TestJPXParseCodestream(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_01.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parseJPX: %v", err)
	}
	if im.xsiz != 128 || im.ysiz != 128 {
		t.Errorf("size = %dx%d, want 128x128", im.xsiz, im.ysiz)
	}
	if len(im.comps) != 1 {
		t.Fatalf("components = %d, want 1", len(im.comps))
	}
	if c := im.comps[0]; c.depth != 8 || c.signed || c.dx != 1 || c.dy != 1 {
		t.Errorf("comp0 = %+v, want depth 8 unsigned 1x1", c)
	}
	if im.cod.levels != 3 {
		t.Errorf("levels = %d, want 3", im.cod.levels)
	}
	if im.cod.transform != 1 {
		t.Errorf("transform = %d, want 1 (reversible 5/3)", im.cod.transform)
	}
	if im.cod.cbW != 64 || im.cod.cbH != 64 {
		t.Errorf("code-block = %dx%d, want 64x64", im.cod.cbW, im.cod.cbH)
	}
	if im.qcd.style != 0 || im.qcd.guardBits != 2 {
		t.Errorf("qcd style=%d guard=%d, want style 0 guard 2", im.qcd.style, im.qcd.guardBits)
	}
	if im.numXTiles() != 1 || im.numYTiles() != 1 {
		t.Errorf("tiles = %dx%d, want 1x1", im.numXTiles(), im.numYTiles())
	}
	if len(im.tileParts) != 1 {
		t.Errorf("tile-parts = %d, want 1", len(im.tileParts))
	}
}

// TestJPXTier2 decodes the packets of each sample's first tile and checks the
// geometry and that code-block data is extracted without error. p0_01 (128x128,
// 3 levels, 64x64 code-blocks) has exactly 1+3+3+3 = 10 code-blocks.
func TestJPXTier2(t *testing.T) {
	files, _ := filepath.Glob("testdata/jpx/*.j2k")
	if len(files) == 0 {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		im, err := parseJPX(data)
		if err != nil {
			t.Errorf("%s: parse: %v", filepath.Base(p), err)
			continue
		}
		// Single-component tile-0 check; multi-component files interleave
		// packets and files using unsupported progressions decline — skip those.
		if len(im.comps) != 1 || (im.cod.progOrder != 0 && im.cod.progOrder != 1) {
			continue
		}
		x0, y0, x1, y1 := im.tileCoords(0)
		tc := buildTileComp(im, 0, x0, y0, x1, y1)
		if err := decodeTilePackets(im, []*jpxTileComp{tc}, im.tileData(0)); err != nil {
			t.Errorf("%s: tier-2: %v", filepath.Base(p), err)
			continue
		}
		if len(tc.resolutions) != im.cod.levels+1 {
			t.Errorf("%s: %d resolutions, want %d", filepath.Base(p), len(tc.resolutions), im.cod.levels+1)
		}
		blocks, withData := 0, 0
		for _, res := range tc.resolutions {
			for _, sb := range res.subbands {
				for i := range sb.blocks {
					blocks++
					if len(sb.blocks[i].segs) > 0 {
						withData++
					}
				}
			}
		}
		if blocks == 0 || withData == 0 {
			t.Errorf("%s: no code-block data (blocks=%d withData=%d)", filepath.Base(p), blocks, withData)
		}
		if filepath.Base(p) == "p0_01.j2k" && blocks != 10 {
			t.Errorf("p0_01: %d code-blocks, want 10", blocks)
		}
	}
}

// TestJPXDecodeGray decodes the single-component reversible p0_01 all the way to
// pixels (tier-1 EBCOT + inverse 5/3 DWT + level shift) and checks the result is a
// real, non-uniform grayscale image. p0_01 is a photograph; a broken entropy
// coder or wavelet transform cannot produce coherent tonal content.
func TestJPXDecodeGray(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_01.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := decodeJPXImage(im)
	g, ok := m.(*image.Gray)
	if !ok {
		t.Fatalf("decoded %T, want *image.Gray", m)
	}
	var sum, sumSq int64
	dark, light := 0, 0
	for _, p := range g.Pix {
		sum += int64(p)
		sumSq += int64(p) * int64(p)
		if p < 96 {
			dark++
		} else if p > 160 {
			light++
		}
	}
	n := int64(len(g.Pix))
	if variance := sumSq/n - (sum/n)*(sum/n); variance < 500 {
		t.Errorf("decoded image is nearly uniform (variance=%d) — likely a decode error", variance)
	}
	if dark < 500 || light < 500 {
		t.Errorf("decoded image lacks tonal spread (dark=%d light=%d)", dark, light)
	}
	// Golden pixels (OpenJPEG via Pillow): reversible 5/3 must be bit-exact. These
	// guard the horizontal-then-vertical inverse-DWT ordering — the wrong order
	// still yields a plausible photo but with ±1..4 rounding drift everywhere.
	for _, p := range []struct{ x, y, v int }{
		{0, 0, 185}, {64, 64, 208}, {127, 127, 34}, {10, 20, 194}, {127, 0, 183},
	} {
		if got := int(g.GrayAt(p.x, p.y).Y); got != p.v {
			t.Errorf("pixel (%d,%d) = %d, want %d", p.x, p.y, got, p.v)
		}
	}
}

// TestJPX97 decodes p0_09 (single-component, irreversible 9/7, expounded scalar
// quantization) end to end, exercising the float wavelet path and dequantization,
// and checks it yields a non-uniform grayscale image.
func TestJPX97(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_09.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if im.cod.transform != 0 {
		t.Skip("expected p0_09 to be the irreversible 9/7 transform")
	}
	g, ok := decodeJPXImage(im).(*image.Gray)
	if !ok {
		t.Fatal("9/7 image did not decode to grayscale")
	}
	first := g.Pix[0]
	uniform := true
	for _, p := range g.Pix {
		if p != first {
			uniform = false
			break
		}
	}
	if uniform {
		t.Error("decoded 9/7 image is uniform — decode/dequant failure")
	}
}

// TestJPXTermination decodes p0_12, which uses the termination-on-each-coding-pass
// code-block style (each pass is its own arithmetic segment), and checks it
// produces a grayscale image rather than falling back.
func TestJPXTermination(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_12.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if im.cod.cbStyle&0x04 == 0 {
		t.Skip("expected p0_12 to use termination on each pass")
	}
	g, ok := decodeJPXImage(im).(*image.Gray)
	if !ok {
		t.Fatal("termination-style image did not decode to grayscale")
	}
	// p0_12 is a 3×5 image; assert the full pixel set bit-exactly (OpenJPEG/Pillow).
	want := []byte{55, 98, 159, 50, 89, 95, 45, 109, 46, 160, 81, 50, 157, 111, 125}
	if b := g.Bounds(); b.Dx() != 3 || b.Dy() != 5 {
		t.Fatalf("size = %dx%d, want 3x5", b.Dx(), b.Dy())
	}
	for i, v := range want {
		if got := g.Pix[i]; got != v {
			t.Errorf("pixel %d = %d, want %d", i, got, v)
		}
	}
}

// TestJPXColorTransform checks the inverse colour transforms are exact inverses
// of their forward transforms (T.800 G.1, G.2), validating the RCT/ICT maths
// independently of the full decode.
func TestJPXColorTransform(t *testing.T) {
	// Reversible colour transform round-trip (integer, exact).
	planes := [][]float64{{100, 5, 250}, {200, 128, 10}, {60, 90, 130}}
	orig := [][]float64{append([]float64{}, planes[0]...), append([]float64{}, planes[1]...), append([]float64{}, planes[2]...)}
	// Forward RCT: Y=floor((R+2G+B)/4), Cb=B-G, Cr=R-G.
	fwd := make([][]float64, 3)
	for c := range fwd {
		fwd[c] = make([]float64, 3)
	}
	for i := 0; i < 3; i++ {
		r, g, b := orig[0][i], orig[1][i], orig[2][i]
		fwd[0][i] = float64(int(r+2*g+b) >> 2)
		fwd[1][i] = b - g
		fwd[2][i] = r - g
	}
	inverseRCT(fwd)
	for c := 0; c < 3; c++ {
		for i := 0; i < 3; i++ {
			if fwd[c][i] != orig[c][i] {
				t.Errorf("RCT round-trip: plane %d idx %d got %v want %v", c, i, fwd[c][i], orig[c][i])
			}
		}
	}
}

// TestJPXMultiLayerColor decodes p0_10 — a multi-quality-layer (6-layer) RGB image
// with 4×-subsampled components and the reversible RCT — and checks it reconstructs
// bit-exactly against known reference pixels (from OpenJPEG via Pillow). This
// exercises the whole colour pipeline: continuous-MQ decoding across quality
// layers, sub-sampled component assembly, the inverse RCT and the DC level shift.
func TestJPXMultiLayerColor(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_10.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if im.cod.layers <= 1 || len(im.comps) < 3 {
		t.Skip("expected p0_10 to be multi-layer RGB")
	}
	m, ok := decodeJPXImage(im).(*image.RGBA)
	if !ok {
		t.Fatalf("decoded %T, want *image.RGBA", decodeJPXImage(im))
	}
	// Golden reference pixels: reversible 5/3 + RCT must reconstruct exactly.
	for _, p := range []struct{ x, y, r, g, b int }{
		{0, 0, 128, 128, 255}, {4, 0, 113, 128, 255}, {100, 100, 56, 35, 223},
		{200, 50, 128, 128, 0}, {255, 255, 85, 108, 0}, {128, 64, 99, 128, 0},
	} {
		c := m.RGBAAt(p.x, p.y)
		if int(c.R) != p.r || int(c.G) != p.g || int(c.B) != p.b {
			t.Errorf("pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
				p.x, p.y, c.R, c.G, c.B, p.r, p.g, p.b)
		}
	}
}

// TestJPXUnsupportedFallback checks that a codestream using code-block style
// features the baseline tier-1 does not handle (p0_02: per-pass termination +
// segmentation symbols) is declined cleanly rather than decoded to garbage.
func TestJPXUnsupportedFallback(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_02.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if im.cod.cbStyle == 0 {
		t.Skip("expected p0_02 to use advanced code-block styles")
	}
	if decodeJPXImage(im) != nil {
		t.Error("expected an unsupported-code-block-style image to fall back (nil)")
	}
}

// TestJPXMoreExact decodes further conformance codestreams that exercise
// combinations already supported (multi-component LRCP, multi-layer RLCP) and
// checks them bit-exactly against known reference pixels (OpenJPEG via Pillow).
func TestJPXMoreExact(t *testing.T) {
	rgb := func(m image.Image, x, y int) (int, int, int) {
		r, g, b, _ := m.At(x, y).RGBA()
		return int(r >> 8), int(g >> 8), int(b >> 8)
	}
	cases := []struct {
		file  string
		gray  bool
		gold  [][]int // {x,y,v} for gray, {x,y,r,g,b} for RGB
	}{
		{"p0_14.j2k", false, [][]int{{0, 0, 128, 128, 128}, {24, 24, 0, 255, 0}, {48, 48, 128, 128, 128}, {7, 11, 128, 128, 128}}},
		{"p0_16.j2k", true, [][]int{{0, 0, 185}, {64, 64, 208}, {127, 127, 34}, {7, 11, 192}}},
	}
	for _, tc := range cases {
		data, err := os.ReadFile(filepath.Join("testdata/jpx", tc.file))
		if err != nil {
			t.Skip("no JPX sample codestreams; run `make jpx`")
		}
		im, err := parseJPX(data)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.file, err)
		}
		m := decodeJPXImage(im)
		if m == nil {
			t.Errorf("%s: declined, expected a decode", tc.file)
			continue
		}
		for _, g := range tc.gold {
			if tc.gray {
				got := int(m.(*image.Gray).GrayAt(g[0], g[1]).Y)
				if got != g[2] {
					t.Errorf("%s pixel (%d,%d) = %d, want %d", tc.file, g[0], g[1], got, g[2])
				}
			} else {
				r, gr, b := rgb(m, g[0], g[1])
				if r != g[2] || gr != g[3] || b != g[4] {
					t.Errorf("%s pixel (%d,%d) = (%d,%d,%d), want (%d,%d,%d)", tc.file, g[0], g[1], r, gr, b, g[2], g[3], g[4])
				}
			}
		}
	}
}

// TestJPXPrecincts decodes p0_11 — a single-component image using non-maximal
// precincts (and segmentation symbols) — and checks it bit-exactly against
// OpenJPEG. This exercises the precinct partition of the tier-2 packet reader.
func TestJPXPrecincts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata/jpx", "p0_11.j2k"))
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !im.cod.precinctsUsed {
		t.Skip("expected p0_11 to use precincts")
	}
	g, ok := decodeJPXImage(im).(*image.Gray)
	if !ok {
		t.Fatal("precinct image did not decode to grayscale")
	}
	// Every 8th pixel of the 128×1 strip (OpenJPEG via Pillow).
	want := []byte{127, 106, 90, 96, 103, 117, 123, 104, 88, 90, 93, 98, 94, 90, 85, 81}
	for i, v := range want {
		if got := g.Pix[i*8]; got != v {
			t.Errorf("pixel %d = %d, want %d", i*8, got, v)
		}
	}
}

// TestJPXPOC checks the POC (progression-order change) marker is parsed and that
// its stages drive the packet order: p0_03 declares PCRL in its COD but a POC
// overrides the whole image to LRCP. With the POC applied the tile-0 packets read
// without a desync (they land on the SOP markers); the COD order alone desyncs.
func TestJPXPOC(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_03.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(im.poc) != 1 {
		t.Fatalf("POC stages = %d, want 1", len(im.poc))
	}
	if p := im.poc[0]; p.prog != 0 || p.layerEnd != 8 {
		t.Errorf("POC[0] = %+v, want prog 0 (LRCP), layerEnd 8", p)
	}
	// The POC order must read tile 0 without overrunning the coded data.
	x0, y0, x1, y1 := im.tileCoords(0)
	tc := buildTileComp(im, 0, x0, y0, x1, y1)
	if err := decodeTilePackets(im, []*jpxTileComp{tc}, im.tileData(0)); err != nil {
		t.Errorf("POC-ordered tile-0 packet read failed: %v", err)
	}
}

// TestJPXParseAll parses every sample codestream without error (a smoke test of
// the marker parser over diverse real files).
func TestJPXParseAll(t *testing.T) {
	files, _ := filepath.Glob("testdata/jpx/*.j2k")
	if len(files) == 0 {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		im, err := parseJPX(data)
		if err != nil {
			t.Errorf("%s: parseJPX: %v", filepath.Base(p), err)
			continue
		}
		t.Logf("%s: %dx%d comps=%d levels=%d transform=%d tiles=%dx%d",
			filepath.Base(p), im.xsiz, im.ysiz, len(im.comps), im.cod.levels,
			im.cod.transform, im.numXTiles(), im.numYTiles())
	}
}
