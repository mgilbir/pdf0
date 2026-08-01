package pdf0

import (
	"encoding/binary"
	"github.com/mgilbir/pdf0/internal/core"
	"testing"

	"github.com/mgilbir/pdf0/internal/font"
)

// Cmap subtable fixtures for the tests in this package that need a font
// program with hand-built cmap tables. The font package has its own copies for
// its parser tests; a test helper cannot cross a package boundary, and these
// two tests belong here — one asserts a budget trip is reported through the
// limit recorder, the other exercises the validator's .notdef determination.

func buildCmapFormat4(segs [][3]int) []byte {
	segX2 := len(segs) * 2
	b := make([]byte, 16+4*segX2)
	put16 := func(off, v int) { b[off] = byte(v >> 8); b[off+1] = byte(v) }
	put16(0, 4)      // format
	put16(2, len(b)) // length
	put16(6, segX2)  // segCountX2
	endBase := 14
	startBase := endBase + segX2 + 2
	deltaBase := startBase + segX2
	rangeBase := deltaBase + segX2
	for i, seg := range segs {
		put16(startBase+2*i, seg[0])
		put16(endBase+2*i, seg[1])
		put16(deltaBase+2*i, seg[2]&0xFFFF)
		put16(rangeBase+2*i, 0)
	}
	return b
}

func buildCmapFormat12(groups [][3]uint32) []byte {
	b := make([]byte, 16+12*len(groups))
	b[1] = 12                                               // format
	binary.BigEndian.PutUint32(b[4:], uint32(len(b)))       // length
	binary.BigEndian.PutUint32(b[12:], uint32(len(groups))) // nGroups
	for i, g := range groups {
		p := 16 + 12*i
		binary.BigEndian.PutUint32(b[p:], g[0])
		binary.BigEndian.PutUint32(b[p+4:], g[1])
		binary.BigEndian.PutUint32(b[p+8:], g[2])
	}
	return b
}

func buildSFNTWithCmapSubtables(subs []struct {
	plat, enc int
	data      []byte
}) []byte {
	cmap := make([]byte, 4+8*len(subs))
	binary.BigEndian.PutUint16(cmap[2:], uint16(len(subs)))
	for i, s := range subs {
		binary.BigEndian.PutUint16(cmap[4+8*i:], uint16(s.plat))
		binary.BigEndian.PutUint16(cmap[4+8*i+2:], uint16(s.enc))
		binary.BigEndian.PutUint32(cmap[4+8*i+4:], uint32(len(cmap)))
		cmap = append(cmap, s.data...)
	}
	font := make([]byte, 12+16)
	binary.BigEndian.PutUint32(font, 0x00010000) // sfnt version 1.0
	binary.BigEndian.PutUint16(font[4:], 1)      // numTables
	copy(font[12:], "cmap")                      // tag
	binary.BigEndian.PutUint32(font[12+8:], 28)  // offset
	binary.BigEndian.PutUint32(font[12+12:], uint32(len(cmap)))
	return append(font, cmap...)
}

// TestCmapEmptySubtableDoesNotDisplace ensures a higher-ranked subtable that
// maps nothing leaves the font's mappings alone. Before the nil-not-empty fix a
// sixteen-byte (3,10) table blanked a perfectly good (3,1) one, because the
// ranking accepted the empty map as the better source.
func TestCmapEmptySubtableDoesNotDisplace(t *testing.T) {
	type sub = struct {
		plat, enc int
		data      []byte
	}
	bmp := buildCmapFormat4([][3]int{{0x0041, 0x0041, 100 - 0x41}, {0xFFFF, 0xFFFF, 1}})
	fp := font.ParseSFNT(buildSFNTWithCmapSubtables([]sub{
		{3, 1, bmp},
		{3, 10, buildCmapFormat12(nil)}, // well formed, maps nothing
	}), core.DefaultMaxCmapWork)
	if fp.Cmap[0x41] != 100 {
		t.Errorf("empty (3,10): cmap[U+0041] = %d, want the (3,1) mapping 100", fp.Cmap[0x41])
	}
	if isNotdefGlyph(fp, "TrueType", false, 'A', "A") {
		t.Errorf("an empty preferred subtable made every code read as .notdef")
	}
}
