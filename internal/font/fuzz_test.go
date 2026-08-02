package font

import (
	"encoding/binary"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"testing"
)

// Cmap fuzz targets, moved here with the parser they exercise.

func checkCmapInvariants(t *testing.T, what string, m map[rune]int, emptyAllowed bool) {
	t.Helper()
	if m == nil {
		return
	}
	if len(m) == 0 && !emptyAllowed {
		t.Fatalf("%s: returned an empty non-nil map; unreadable subtables must return nil", what)
	}
	// Formats 4 and 12 share one configurable budget (WithMaxCmapWork); these
	// targets always parse at the default, so that is the bound to assert.
	budget := generousCmapWork
	if len(m) > budget {
		t.Fatalf("%s: %d entries exceeds the work budget of %d", what, len(m), budget)
	}
	for r, gid := range m {
		if r < 0 || r > 0x10FFFF {
			t.Fatalf("%s: key U+%X is not a Unicode code point", what, r)
		}
		if gid <= 0 || gid > 0xFFFF {
			t.Fatalf("%s: key U+%X maps to glyph %d, outside 1..0xFFFF", what, r, gid)
		}
	}
}

// cmapSubtableSeeds returns one subtable of every shape the hand-written tests
// cover: each supported format, the budget-tripping tables, the truncated and
// malformed variants, and a few formats the parser does not handle.
func cmapSubtableSeeds() [][]byte {
	// Disjoint groups covering the whole of Unicode: ~1.1M mappings if the
	// budget does not stop it.
	var wideOpen [][3]uint32
	for start := uint32(0); start <= 0x10FFFF; start += 4096 {
		wideOpen = append(wideOpen, [3]uint32{start, start + 4095, 1})
	}
	// Sixty-four groups each spanning all of Unicode: 71M iterations unbudgeted.
	overlapping := make([][3]uint32, 64)
	for i := range overlapping {
		overlapping[i] = [3]uint32{0, 0x10FFFF, 1}
	}
	// Format 4 with many segments each spanning almost the whole BMP.
	var wideSegs [][3]int
	for i := 0; i < 400; i++ {
		wideSegs = append(wideSegs, [3]int{1, 0xFFFE, 0})
	}

	format12 := fonttest.CmapFormat12([][3]uint32{{0x41, 0x42, 7}})
	overstated := append([]byte(nil), format12...)
	binary.BigEndian.PutUint32(overstated[12:], 1<<20) // nGroups it does not carry
	longLength := append([]byte(nil), format12...)
	binary.BigEndian.PutUint32(longLength[4:], uint32(len(longLength))+1)

	seeds := [][]byte{
		// Format 0: byte encoding, plus the truncated form.
		buildCmapFormat0(map[byte]byte{0x00: 3, 0x41: 4, 0xFF: 5}),
		buildCmapFormat0(nil),
		make([]byte, 100),
		// Format 4: segment at code 0, terminal segment, inverted segment,
		// sentinel alone, and the budget-tripper.
		fonttest.CmapFormat4([][3]int{{0x0000, 0x0002, 100}, {0x0041, 0x0042, 200}, {0xFFFF, 0xFFFF, 1}}),
		fonttest.CmapFormat4([][3]int{{0xFFFE, 0xFFFF, 0x8000}}),
		fonttest.CmapFormat4([][3]int{{0x0050, 0x0040, 300}, {0x0041, 0x0041, 200}}),
		fonttest.CmapFormat4([][3]int{{0xFFFF, 0xFFFF, 1}}),
		fonttest.CmapFormat4(wideSegs),
		// Format 6: trimmed table, and one running past the BMP.
		buildCmapFormat6(0x41, []int{7, 8, 9}),
		buildCmapFormat6(0xFFFE, []int{7, 8, 9, 10}),
		// Format 12: ordinary groups, astral groups, malformed groups, the two
		// budget-trippers, an empty table, and the truncated variants.
		format12,
		fonttest.CmapFormat12([][3]uint32{{0x0000, 0x0002, 100}, {0x0041, 0x0043, 200}, {0x0100, 0x0100, 0}}),
		fonttest.CmapFormat12([][3]uint32{{0xFFFE, 0x10001, 900}, {0x1F600, 0x1F601, 1000}}),
		fonttest.CmapFormat12([][3]uint32{{0x50, 0x40, 300}, {0x110000, 0x110002, 400}, {0x60, 0x60, 0x10000}, {0x41, 0x41, 200}}),
		fonttest.CmapFormat12(wideOpen),
		fonttest.CmapFormat12(overlapping),
		fonttest.CmapFormat12(nil), // maps nothing: the FuzzCmapSubtable finding
		// The same finding as the fuzzer minimised it, kept verbatim because
		// testdata/fuzz is gitignored and this is the only place the regression
		// can live: one group, [0x30303030, 0x30303030] → 0x30303030, entirely
		// outside Unicode, so every mapping is skipped and the map comes back
		// empty. Its shape is not the one a human would have written down.
		[]byte("\x00\x0c00\x00\x00\x00\x1c0000\x00\x00\x00\x01000000000000"),
		format12[:len(format12)-4],
		format12[:12],
		overstated,
		longLength,
		// Formats the parser does not handle, and degenerate input.
		{0, 2, 0, 0}, {0, 13, 0, 0}, {0, 14, 0, 0}, {0, 8}, {0},
		{},
	}
	return seeds
}

// FuzzCmapSubtable fuzzes ParseCmapSubtable on raw subtable bytes — the deep
// target, straight at the parser that reads attacker-controlled binary out of an
// embedded font program. It asserts no panic and the invariants in
// checkCmapInvariants: the budget holds, the map is nil rather than empty, and
// every key and value is in range.
func FuzzCmapSubtable(f *testing.F) {
	for _, s := range cmapSubtableSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, partial := ParseCmapSubtable(data, generousCmapWork)
		checkCmapInvariants(t, "ParseCmapSubtable", m, false)
		// A partial result is only meaningful alongside a map: reporting "this
		// is a prefix" while returning nothing would leave a consumer unable to
		// tell "truncated" from "unreadable", which is the distinction the flag
		// exists to draw.
		if partial && m == nil {
			t.Fatalf("ParseCmapSubtable reported partial with a nil map")
		}
	})
}

// sfntCmapSeeds returns fonts carrying several cmap subtables at once, so the
// fuzzer starts from inputs where subtable ranking actually has a choice to make.
func sfntCmapSeeds() [][]byte {
	type sub = fonttest.CmapSub
	bmp := fonttest.CmapFormat4([][3]int{{0x0041, 0x0041, 100 - 0x41}, {0xFFFF, 0xFFFF, 1}})
	full := fonttest.CmapFormat12([][3]uint32{{0x0041, 0x0041, 500}, {0x1F600, 0x1F600, 600}})
	mac := buildCmapFormat0(map[byte]byte{0x41: 9})
	symbol := fonttest.CmapFormat4([][3]int{{0xF041, 0xF041, 12}, {0xFFFF, 0xFFFF, 1}})

	var seeds [][]byte
	for _, subs := range [][]sub{
		{{Plat: 3, Enc: 1, Data: bmp}, {Plat: 3, Enc: 10, Data: full}},
		{{Plat: 3, Enc: 10, Data: full}, {Plat: 3, Enc: 1, Data: bmp}},
		{{Plat: 1, Enc: 0, Data: mac}, {Plat: 3, Enc: 1, Data: bmp}},
		{{Plat: 3, Enc: 0, Data: symbol}, {Plat: 1, Enc: 0, Data: mac}},
		{{Plat: 0, Enc: 4, Data: full}},
		{{Plat: 0, Enc: 0, Data: bmp}, {Plat: 0, Enc: 6, Data: full}, {Plat: 3, Enc: 1, Data: bmp}},
		{{Plat: 3, Enc: 1, Data: bmp}, {Plat: 3, Enc: 10, Data: make([]byte, 16)}},           // unreadable preferred subtable
		{{Plat: 3, Enc: 1, Data: bmp}, {Plat: 3, Enc: 10, Data: fonttest.CmapFormat12(nil)}}, // preferred subtable maps nothing
		{{Plat: 3, Enc: 10, Data: fonttest.CmapFormat12(nil)}, {Plat: 3, Enc: 1, Data: bmp}}, // …in the other order
		{{Plat: 3, Enc: 1, Data: bmp}, {Plat: 3, Enc: 10, Data: bmp}, {Plat: 0, Enc: 3, Data: mac}, {Plat: 1, Enc: 0, Data: mac}},
		nil,
	} {
		seeds = append(seeds, fonttest.SFNTWithCmapSubtables(subs))
	}
	// Not an sfnt at all, and a header claiming tables it does not carry.
	seeds = append(seeds, []byte("%PDF-2.0\n"), []byte{0, 1, 0, 0, 0xFF, 0xFF, 0, 0, 0, 0, 0, 0})
	return seeds
}

// FuzzSFNTCmap fuzzes parseSFNT, the smallest entry point that exercises
// subtable *selection*: the (3,10) > (3,1) > (0,x) ranking added with format 12
// runs on attacker-controlled platform ids, encoding ids and offsets, and picks
// which of several subtables becomes the font's authoritative cmap. Whatever it
// picks must satisfy the same invariants as a single subtable, and the derived
// symbol and Mac maps must carry only real glyph indices. It also drives
// TrueTypeGID over the resulting font, since that is what the PDF/A rules do
// with it.
func FuzzSFNTCmap(f *testing.F) {
	for _, s := range sfntCmapSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fp := ParseSFNT(data, generousCmapWork)
		if fp == nil {
			return
		}
		checkCmapInvariants(t, "parseSFNT cmap", fp.Cmap, false)
		// The symbol and Mac maps are narrowed from a subtable by dropping the
		// codes that do not fit their key type, so they may legitimately end up
		// empty; every lookup on them is guarded by the comma-ok, so an empty one
		// is not the standing "everything is .notdef" claim that fp.cmap is.
		symbol := make(map[rune]int, len(fp.SymbolCmap))
		for c, gid := range fp.SymbolCmap {
			symbol[rune(c)] = gid
		}
		checkCmapInvariants(t, "parseSFNT symbolCmap", symbol, true)
		mac := make(map[rune]int, len(fp.MacCmap))
		for c, gid := range fp.MacCmap {
			mac[rune(c)] = gid
		}
		checkCmapInvariants(t, "parseSFNT macCmap", mac, true)
		for _, code := range []byte{0, 'A', 0xFF} {
			for _, symbolic := range []bool{false, true} {
				_, _ = TrueTypeGID(fp, symbolic, code, "A")
				_, _ = TrueTypeGID(fp, symbolic, code, "")
			}
		}
	})
}
