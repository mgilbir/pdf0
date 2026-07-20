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
