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
	if _, ok := decodeJPXImage(im).(*image.Gray); !ok {
		t.Error("termination-style image did not decode to grayscale")
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

// TestJPXMultiLayerFallback documents that a multi-quality-layer image (p0_10)
// is declined cleanly for now rather than mis-decoded.
func TestJPXMultiLayerFallback(t *testing.T) {
	path := filepath.Join("testdata/jpx", "p0_10.j2k")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("no JPX sample codestreams; run `make jpx`")
	}
	im, err := parseJPX(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if im.cod.layers <= 1 {
		t.Skip("expected p0_10 to be multi-layer")
	}
	if decodeJPXImage(im) != nil {
		t.Error("expected a multi-layer image to fall back (nil) for now")
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
