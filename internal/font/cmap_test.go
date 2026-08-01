package font

import (
	"encoding/binary"
	"testing"
	"time"
)

// Cmap subtable parsing tests, moved here with the parser they exercise.

// generousCmapWork is a budget large enough that these tests never trip it.
// The two that do exercise the budget pass their own, smaller value. Nothing
// here depends on it matching the package default in limits.go.
const generousCmapWork = 1 << 18

func TestCmapFormat4Budget(t *testing.T) {
	// Build a format-4 subtable with many segments each spanning 1..0xFFFE.
	const segs = 400
	segX2 := segs * 2
	b := make([]byte, 16+4*segX2+64)
	put16 := func(off, v int) { b[off] = byte(v >> 8); b[off+1] = byte(v) }
	put16(0, 4)     // format
	put16(6, segX2) // segCountX2
	endBase := 14
	startBase := endBase + segX2 + 2
	deltaBase := startBase + segX2
	rangeBase := deltaBase + segX2
	for s := 0; s < segX2; s += 2 {
		put16(endBase+s, 0xFFFE)
		put16(startBase+s, 0x0001)
		put16(deltaBase+s, 0)
		put16(rangeBase+s, 0)
	}
	done := make(chan struct{})
	go func() {
		_, _ = ParseCmapSubtable(b, generousCmapWork)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("ParseCmapSubtable did not terminate within the work budget")
	}
}

// buildCmapFormat4 assembles a format-4 cmap subtable from {startCode, endCode,
// idDelta} segments, with idRangeOffset zero throughout (glyph = code + delta).
func TestCmapFormat4SegmentStartingAtZero(t *testing.T) {
	m, _ := ParseCmapSubtable(buildCmapFormat4([][3]int{
		{0x0000, 0x0002, 100},
		{0x0041, 0x0042, 200},
		{0xFFFF, 0xFFFF, 1}, // sentinel, maps nothing
	}), generousCmapWork)
	want := map[rune]int{0: 100, 1: 101, 2: 102, 0x41: 0x41 + 200, 0x42: 0x42 + 200}
	for r, gid := range want {
		if m[r] != gid {
			t.Errorf("cmap[U+%04X] = %d, want %d", r, m[r], gid)
		}
	}
	if len(m) != len(want) {
		t.Errorf("cmap has %d entries, want %d: %v", len(m), len(want), m)
	}
}

// TestCmapFormat4TerminalSegment ensures a segment that runs up to 0xFFFF maps
// its last code and still terminates.
func TestCmapFormat4TerminalSegment(t *testing.T) {
	m, _ := ParseCmapSubtable(buildCmapFormat4([][3]int{{0xFFFE, 0xFFFF, 0x8000}}), generousCmapWork)
	if m[0xFFFE] != 0x7FFE || m[0xFFFF] != 0x7FFF {
		t.Errorf("terminal segment: got %v, want U+FFFE->0x7FFE, U+FFFF->0x7FFF", m)
	}
}

// TestCmapFormat4InvertedSegment ensures a malformed segment with start > end is
// skipped without disturbing the segments around it.
func TestCmapFormat4InvertedSegment(t *testing.T) {
	m, _ := ParseCmapSubtable(buildCmapFormat4([][3]int{
		{0x0050, 0x0040, 300}, // inverted
		{0x0041, 0x0041, 200},
	}), generousCmapWork)
	if len(m) != 1 || m[0x41] != 0x41+200 {
		t.Errorf("inverted segment: got %v, want only U+0041", m)
	}
}

// TestCmapUnsupportedFormatIsNil ensures an unparsed subtable format yields nil
// rather than an empty map: the glyph checks treat a non-nil cmap as
// authoritative, so an empty one would make every code look like .notdef.
func TestCmapUnsupportedFormatIsNil(t *testing.T) {
	for _, format := range []int{2, 13, 14} {
		sub := make([]byte, 64)
		sub[0], sub[1] = byte(format>>8), byte(format)
		if m, _ := ParseCmapSubtable(sub, generousCmapWork); m != nil {
			t.Errorf("format %d subtable: got %v, want nil", format, m)
		}
	}
	if m, _ := ParseCmapSubtable(make([]byte, 100), generousCmapWork); m != nil {
		t.Errorf("truncated format 0 subtable: got %v, want nil", m)
	}
}

// buildCmapFormat12 assembles a format-12 (segmented coverage) cmap subtable
// from {startCharCode, endCharCode, startGlyphID} groups.
func TestCmapFormat12Groups(t *testing.T) {
	m, _ := ParseCmapSubtable(buildCmapFormat12([][3]uint32{
		{0x0000, 0x0002, 100},
		{0x0041, 0x0043, 200},
		{0x0100, 0x0100, 0}, // maps to .notdef: recorded as no mapping at all
	}), generousCmapWork)
	want := map[rune]int{0: 100, 1: 101, 2: 102, 0x41: 200, 0x42: 201, 0x43: 202}
	for r, gid := range want {
		if m[r] != gid {
			t.Errorf("cmap[U+%04X] = %d, want %d", r, m[r], gid)
		}
	}
	if len(m) != len(want) {
		t.Errorf("cmap has %d entries, want %d: %v", len(m), len(want), m)
	}
}

// TestCmapFormat12Astral ensures a group crossing out of the BMP keeps its
// supra-BMP code points: reaching those is the whole point of format 12.
func TestCmapFormat12Astral(t *testing.T) {
	m, _ := ParseCmapSubtable(buildCmapFormat12([][3]uint32{
		{0xFFFE, 0x10001, 900},
		{0x1F600, 0x1F601, 1000},
	}), generousCmapWork)
	want := map[rune]int{
		0xFFFE: 900, 0xFFFF: 901, 0x10000: 902, 0x10001: 903,
		0x1F600: 1000, 0x1F601: 1001,
	}
	for r, gid := range want {
		if m[r] != gid {
			t.Errorf("cmap[U+%04X] = %d, want %d", r, m[r], gid)
		}
	}
	if len(m) != len(want) {
		t.Errorf("cmap has %d entries, want %d", len(m), len(want))
	}
}

// TestCmapFormat12Budget ensures a subtable whose groups would expand to tens of
// millions of entries stays bounded and returns promptly: nGroups is a uint32
// and one group may span the whole of Unicode, so the expansion is entirely
// attacker-controlled.
func TestCmapFormat12Budget(t *testing.T) {
	// Disjoint 4096-code groups covering the whole of Unicode, every code
	// mapping to a distinct in-range glyph: without a budget this expands to
	// ~1.1M map entries, so the assertion below fails outright rather than
	// merely running slowly.
	var disjoint [][3]uint32
	for start := uint32(0); start <= 0x10FFFF; start += 4096 {
		disjoint = append(disjoint, [3]uint32{start, start + 4095, 1})
	}
	// Sixty-four groups each spanning the whole of Unicode: 71M iterations if
	// nothing stops the loop.
	overlapping := make([][3]uint32, 64)
	for i := range overlapping {
		overlapping[i] = [3]uint32{0, 0x10FFFF, 1}
	}
	for _, tc := range []struct {
		name   string
		groups [][3]uint32
	}{
		{"disjoint groups covering Unicode", disjoint},
		{"overlapping full-range groups", overlapping},
	} {
		b := buildCmapFormat12(tc.groups)
		done := make(chan int, 1)
		start := time.Now()
		go func() {
			mm, _ := ParseCmapSubtable(b, generousCmapWork)
			done <- len(mm)
		}()
		select {
		case n := <-done:
			t.Logf("%s: %d entries in %v", tc.name, n, time.Since(start))
			if n == 0 {
				t.Errorf("%s: returned nothing; want the partial map read so far", tc.name)
			}
			if n > 1<<18 {
				t.Errorf("%s: expanded to %d entries, want at most %d", tc.name, n, 1<<18)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: ParseCmapSubtable did not terminate within the work budget", tc.name)
		}
	}
}

// TestCmapFormat12Truncated ensures a table that claims more groups (or more
// bytes) than it carries is refused outright rather than half-read: a partial
// map read as authoritative turns unmapped codes into .notdef findings.
func TestCmapFormat12Truncated(t *testing.T) {
	full := buildCmapFormat12([][3]uint32{{0x41, 0x42, 7}})
	cases := map[string][]byte{
		"body cut off":   full[:len(full)-4],
		"header cut off": full[:12],
		"nGroups overstated": func() []byte {
			b := append([]byte(nil), full...)
			binary.BigEndian.PutUint32(b[12:], 1<<20)
			return b
		}(),
		"length past buffer": func() []byte {
			b := append([]byte(nil), full...)
			binary.BigEndian.PutUint32(b[4:], uint32(len(b))+1)
			return b
		}(),
		"length too small": func() []byte {
			b := append([]byte(nil), full...)
			binary.BigEndian.PutUint32(b[4:], 8)
			return b
		}(),
	}
	for name, b := range cases {
		if m, _ := ParseCmapSubtable(b, generousCmapWork); m != nil {
			t.Errorf("%s: got %v, want nil", name, m)
		}
	}
}

// TestCmapFormat12MalformedGroups ensures an inverted group is skipped without
// disturbing its neighbours, and that a glyph id beyond the 16-bit range is not
// recorded as if it named a glyph.
func TestCmapFormat12MalformedGroups(t *testing.T) {
	m, _ := ParseCmapSubtable(buildCmapFormat12([][3]uint32{
		{0x0050, 0x0040, 300},     // inverted
		{0x110000, 0x110002, 400}, // past the end of Unicode
		{0x0060, 0x0060, 0x10000}, // glyph id wider than 16 bits
		{0x0041, 0x0041, 200},
	}), generousCmapWork)
	if len(m) != 1 || m[0x41] != 200 {
		t.Errorf("malformed groups: got %v, want only U+0041->200", m)
	}
}

// buildSFNTWithCmapSubtables wraps cmap subtables, each tagged with its
// (platform, encoding), into a minimal sfnt font carrying only a cmap table.
func TestCmapSubtablePreference(t *testing.T) {
	type sub = struct {
		plat, enc int
		data      []byte
	}
	bmp := buildCmapFormat4([][3]int{{0x0041, 0x0041, 100 - 0x41}, {0xFFFF, 0xFFFF, 1}})
	full := buildCmapFormat12([][3]uint32{{0x0041, 0x0041, 500}, {0x1F600, 0x1F600, 600}})

	for _, order := range [][]sub{
		{{3, 1, bmp}, {3, 10, full}},
		{{3, 10, full}, {3, 1, bmp}},
	} {
		fp := ParseSFNT(buildSFNTWithCmapSubtables(order), generousCmapWork)
		if fp == nil {
			t.Fatalf("parseSFNT returned nil")
		}
		if fp.Cmap[0x41] != 500 {
			t.Errorf("order %v: cmap[U+0041] = %d, want 500 from the (3,10) subtable", order, fp.Cmap[0x41])
		}
		if fp.Cmap[0x1F600] != 600 {
			t.Errorf("order %v: astral code point lost: %v", order, fp.Cmap[0x1F600])
		}
	}

	// A font with only the historically handled subtables must resolve through
	// the same one as before: (3,1), never the Mac or symbol table.
	fp := ParseSFNT(buildSFNTWithCmapSubtables([]sub{
		{1, 0, buildCmapFormat4([][3]int{{0x0041, 0x0041, 1}})},
		{3, 1, bmp},
	}), generousCmapWork)
	if fp.Cmap[0x41] != 100 {
		t.Errorf("(3,1)-only font: cmap[U+0041] = %d, want 100", fp.Cmap[0x41])
	}

	// An unreadable preferred subtable must not displace a readable lesser one,
	// and must not leave an empty map behind.
	fp = ParseSFNT(buildSFNTWithCmapSubtables([]sub{
		{3, 1, bmp},
		{3, 10, make([]byte, 16)}, // format 0 in a (3,10) slot, truncated
	}), generousCmapWork)
	if fp.Cmap[0x41] != 100 {
		t.Errorf("unreadable (3,10): cmap[U+0041] = %d, want the (3,1) mapping 100", fp.Cmap[0x41])
	}

	// A Unicode-platform subtable is used when there is no Windows one.
	fp = ParseSFNT(buildSFNTWithCmapSubtables([]sub{{0, 4, full}}), generousCmapWork)
	if fp.Cmap[0x1F600] != 600 {
		t.Errorf("(0,4) subtable ignored: %v", fp.Cmap)
	}
}

// buildCmapFormat0 assembles a format-0 (byte encoding) cmap subtable from a
// code→glyph table; codes absent from gidByCode map to nothing.
func TestCmapFormat6PastBMP(t *testing.T) {
	// firstCode 0xFFFE with four entries runs to 0x10001.
	m, _ := ParseCmapSubtable(buildCmapFormat6(0xFFFE, []int{7, 8, 9, 10}), generousCmapWork)
	if len(m) != 2 || m[0xFFFE] != 7 || m[0xFFFF] != 8 {
		t.Errorf("format 6 past the BMP: got %v, want only U+FFFE->7 and U+FFFF->8", m)
	}
}

// TestCmapMappingNothingIsNil ensures a subtable that is perfectly well formed
// but maps no character comes back as nil rather than as an empty map. Found by
// FuzzCmapSubtable. The distinction is load-bearing: trueTypeGID treats a
// non-nil cmap as authoritative, so an empty one answers ".notdef" for every
// code — a font-wide false PDF/A finding from sixteen bytes of input.
func TestCmapMappingNothingIsNil(t *testing.T) {
	cases := map[string][]byte{
		// A 16-byte format-12 header declaring no groups at all.
		"format 12, nGroups 0": buildCmapFormat12(nil),
		// The sentinel segment a conformant format-4 table always ends with,
		// alone: it maps nothing by definition.
		"format 4, sentinel only": buildCmapFormat4([][3]int{{0xFFFF, 0xFFFF, 1}}),
		"format 0, all .notdef":   buildCmapFormat0(nil),
		"format 6, no entries":    buildCmapFormat6(0x41, nil),
		// What the fuzzer actually minimised to: one group lying wholly outside
		// Unicode, so every mapping in it is skipped.
		"format 12, sole group outside Unicode": buildCmapFormat12([][3]uint32{{0x30303030, 0x30303030, 0x30303030}}),
		// Budget exhaustion with nothing recorded: every group's glyph ids are
		// wider than 16 bits, so no mapping survives, and the loop gives up on
		// the work cap rather than on the end of the table.
		"format 12, budget spent on out-of-range glyphs": buildCmapFormat12(func() [][3]uint32 {
			g := make([][3]uint32, 64)
			for i := range g {
				g[i] = [3]uint32{0, 0x10FFFF, 0x10000}
			}
			return g
		}()),
	}
	for name, b := range cases {
		if m, _ := ParseCmapSubtable(b, generousCmapWork); m != nil {
			t.Errorf("%s: got a non-nil map with %d entries, want nil", name, len(m))
		}
	}
}

// TestOffsetsMatchObjects ensures normalizeStructure prunes doc.Offsets in
// lockstep with doc.Objects, so the byte-level checks never key on a removed
// object (audit C9).

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

// TestCmapFormat4SegmentStartingAtZero ensures a format-4 segment whose start
// code is 0 contributes its mappings; a bogus wrap guard used to drop the whole
// segment (audit C46).
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

// TestCmapFormat12Groups covers an ordinary multi-group format-12 subtable,
// including a group starting at code 0 (the class of bug behind audit C46) and
// one whose glyph ids start at 0.
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

// TestCmapSubtablePreference ensures a font carrying both a (3,10) full
// repertoire subtable and a (3,1) BMP one resolves through the (3,10) superset,
// whichever order the records appear in, while a font carrying only the
// subtables handled before format 12 existed resolves exactly as it did.
func buildCmapFormat0(gidByCode map[byte]byte) []byte {
	b := make([]byte, 262)
	b[1] = 0 // format
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	for code, gid := range gidByCode {
		b[6+int(code)] = gid
	}
	return b
}

// buildCmapFormat6 assembles a format-6 (trimmed table) cmap subtable mapping
// first, first+1, … to the given glyph ids.
func buildCmapFormat6(first int, gids []int) []byte {
	b := make([]byte, 10+2*len(gids))
	put16 := func(off, v int) { b[off] = byte(v >> 8); b[off+1] = byte(v) }
	put16(0, 6)         // format
	put16(2, len(b))    // length
	put16(6, first)     // firstCode
	put16(8, len(gids)) // entryCount
	for i, gid := range gids {
		put16(10+2*i, gid)
	}
	return b
}

// TestCmapFormat6PastBMP ensures a format-6 subtable whose entries run past
// 0xFFFF drops the out-of-range codes instead of aliasing them onto low codes
// when the caller narrows the map to uint16.
