package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"testing"

	"github.com/mgilbir/forme/font"
)

// Cmap subtable fixtures for the tests in this package that need a font
// program with hand-built cmap tables. The font package has its own copies for
// its parser tests; a test helper cannot cross a package boundary, and these
// two tests belong here — one asserts a budget trip is reported through the
// limit recorder, the other exercises the validator's .notdef determination.

// TestCmapEmptySubtableDoesNotDisplace ensures a higher-ranked subtable that
// maps nothing leaves the font's mappings alone. Before the nil-not-empty fix a
// sixteen-byte (3,10) table blanked a perfectly good (3,1) one, because the
// ranking accepted the empty map as the better source.
func TestCmapEmptySubtableDoesNotDisplace(t *testing.T) {
	type sub = fonttest.CmapSub
	bmp := fonttest.CmapFormat4([][3]int{{0x0041, 0x0041, 100 - 0x41}, {0xFFFF, 0xFFFF, 1}})
	fp := font.ParseSFNT(fonttest.SFNTWithCmapSubtables([]sub{
		{Plat: 3, Enc: 1, Data: bmp},
		{Plat: 3, Enc: 10, Data: fonttest.CmapFormat12(nil)}, // well formed, maps nothing
	}), core.DefaultMaxCmapWork)
	if fp.Cmap[0x41] != 100 {
		t.Errorf("empty (3,10): cmap[U+0041] = %d, want the (3,1) mapping 100", fp.Cmap[0x41])
	}
	if isNotdefGlyph(fp, "TrueType", false, 'A', "A") {
		t.Errorf("an empty preferred subtable made every code read as .notdef")
	}
}
