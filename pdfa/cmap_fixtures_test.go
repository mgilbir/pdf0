package pdfa

import (
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"testing"

	"github.com/mgilbir/forme/font"
)

// Two tests needing a font program with hand-built cmap tables.
//
// The tables come from forme's fonttest, which is where a font fixture is
// built and is shared rather than copied — a second copy of a fixture both
// modules edit is how a disagreement between the fixtures comes to look like a
// disagreement between the readers.
//
// The tests belong here because what they assert is this module's: one that a
// budget trip is reported through the limit recorder, the other that the
// validator's .notdef determination survives a subtable that outranks a good
// one and maps nothing.

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
