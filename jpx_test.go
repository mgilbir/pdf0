package pdf0

import (
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
		x0, y0, x1, y1 := im.tileCoords(0)
		tc := buildTileComp(im, 0, x0, y0, x1, y1)
		if err := decodeTilePackets(im, tc, im.tileData(0)); err != nil {
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
					if len(sb.blocks[i].data) > 0 {
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

// TestJPXDecodeGray decodes the single-component reversible conformance images
// all the way to pixels (tier-1 EBCOT + inverse 5/3 DWT + level shift) and checks
// the result is a real, non-uniform image. p0_01 is a grayscale photograph; a
// broken entropy coder or wavelet transform cannot produce coherent tonal
// content, so a healthy dark/light spread is a strong correctness signal.
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
	gray := decodeJPXGray(im)
	if gray == nil {
		t.Fatal("decodeJPXGray returned nil for a single-component reversible image")
	}
	if len(gray) != im.xsiz*im.ysiz {
		t.Fatalf("decoded %d samples, want %d", len(gray), im.xsiz*im.ysiz)
	}
	var sum, sumSq int64
	dark, light := 0, 0
	for _, p := range gray {
		sum += int64(p)
		sumSq += int64(p) * int64(p)
		if p < 96 {
			dark++
		} else if p > 160 {
			light++
		}
	}
	n := int64(len(gray))
	variance := sumSq/n - (sum/n)*(sum/n)
	if variance < 500 {
		t.Errorf("decoded image is nearly uniform (variance=%d) — likely a decode error", variance)
	}
	if dark < 500 || light < 500 {
		t.Errorf("decoded image lacks tonal spread (dark=%d light=%d)", dark, light)
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
	if decodeJPXGray(im) != nil {
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
