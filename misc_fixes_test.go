package pdf0

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// TestDictionaryEqualDuplicateKeys ensures duplicate keys are compared as a
// multiset, not by first-occurrence (audit C26).
func TestDictionaryEqualDuplicateKeys(t *testing.T) {
	dup11 := &Dictionary{Keys: []Name{"A", "A"}, Values: []Object{Integer(1), Integer(1)}}
	a1b99 := &Dictionary{Keys: []Name{"A", "B"}, Values: []Object{Integer(1), Integer(99)}}
	if Equal(dup11, a1b99) {
		t.Errorf("{A:1,A:1} must not equal {A:1,B:99}")
	}
	dup12 := &Dictionary{Keys: []Name{"A", "A"}, Values: []Object{Integer(1), Integer(2)}}
	if !Equal(dup12, dup12) {
		t.Errorf("a dictionary with duplicate keys must equal itself")
	}
}

// TestWriteNameRejectsNUL ensures a NUL in a name is refused rather than emitted
// as unparseable "#00" (audit C31).
func TestWriteNameRejectsNUL(t *testing.T) {
	var buf bytes.Buffer
	if err := NewSerializer(&buf).WriteObject(Name("a\x00b")); err == nil {
		t.Errorf("expected an error serializing a name containing NUL, got nil")
	}
}

// TestCmapFormat4Budget ensures a hostile format-4 cmap terminates quickly
// (audit C10).
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
		parseCmapSubtable(b)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("parseCmapSubtable did not terminate within the work budget")
	}
}

// buildCmapFormat4 assembles a format-4 cmap subtable from {startCode, endCode,
// idDelta} segments, with idRangeOffset zero throughout (glyph = code + delta).
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
func TestCmapFormat4SegmentStartingAtZero(t *testing.T) {
	m := parseCmapSubtable(buildCmapFormat4([][3]int{
		{0x0000, 0x0002, 100},
		{0x0041, 0x0042, 200},
		{0xFFFF, 0xFFFF, 1}, // sentinel, maps nothing
	}))
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
	m := parseCmapSubtable(buildCmapFormat4([][3]int{{0xFFFE, 0xFFFF, 0x8000}}))
	if m[0xFFFE] != 0x7FFE || m[0xFFFF] != 0x7FFF {
		t.Errorf("terminal segment: got %v, want U+FFFE->0x7FFE, U+FFFF->0x7FFF", m)
	}
}

// TestCmapFormat4InvertedSegment ensures a malformed segment with start > end is
// skipped without disturbing the segments around it.
func TestCmapFormat4InvertedSegment(t *testing.T) {
	m := parseCmapSubtable(buildCmapFormat4([][3]int{
		{0x0050, 0x0040, 300}, // inverted
		{0x0041, 0x0041, 200},
	}))
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
		if m := parseCmapSubtable(sub); m != nil {
			t.Errorf("format %d subtable: got %v, want nil", format, m)
		}
	}
	if m := parseCmapSubtable(make([]byte, 100)); m != nil {
		t.Errorf("truncated format 0 subtable: got %v, want nil", m)
	}
}

// buildCmapFormat12 assembles a format-12 (segmented coverage) cmap subtable
// from {startCharCode, endCharCode, startGlyphID} groups.
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
func TestCmapFormat12Groups(t *testing.T) {
	m := parseCmapSubtable(buildCmapFormat12([][3]uint32{
		{0x0000, 0x0002, 100},
		{0x0041, 0x0043, 200},
		{0x0100, 0x0100, 0}, // maps to .notdef: recorded as no mapping at all
	}))
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
	m := parseCmapSubtable(buildCmapFormat12([][3]uint32{
		{0xFFFE, 0x10001, 900},
		{0x1F600, 0x1F601, 1000},
	}))
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
			done <- len(parseCmapSubtable(b))
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
			t.Fatalf("%s: parseCmapSubtable did not terminate within the work budget", tc.name)
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
		if m := parseCmapSubtable(b); m != nil {
			t.Errorf("%s: got %v, want nil", name, m)
		}
	}
}

// TestCmapFormat12MalformedGroups ensures an inverted group is skipped without
// disturbing its neighbours, and that a glyph id beyond the 16-bit range is not
// recorded as if it named a glyph.
func TestCmapFormat12MalformedGroups(t *testing.T) {
	m := parseCmapSubtable(buildCmapFormat12([][3]uint32{
		{0x0050, 0x0040, 300},     // inverted
		{0x110000, 0x110002, 400}, // past the end of Unicode
		{0x0060, 0x0060, 0x10000}, // glyph id wider than 16 bits
		{0x0041, 0x0041, 200},
	}))
	if len(m) != 1 || m[0x41] != 200 {
		t.Errorf("malformed groups: got %v, want only U+0041->200", m)
	}
}

// buildSFNTWithCmapSubtables wraps cmap subtables, each tagged with its
// (platform, encoding), into a minimal sfnt font carrying only a cmap table.
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
		fp := parseSFNT(buildSFNTWithCmapSubtables(order))
		if fp == nil {
			t.Fatalf("parseSFNT returned nil")
		}
		if fp.cmap[0x41] != 500 {
			t.Errorf("order %v: cmap[U+0041] = %d, want 500 from the (3,10) subtable", order, fp.cmap[0x41])
		}
		if fp.cmap[0x1F600] != 600 {
			t.Errorf("order %v: astral code point lost: %v", order, fp.cmap[0x1F600])
		}
	}

	// A font with only the historically handled subtables must resolve through
	// the same one as before: (3,1), never the Mac or symbol table.
	fp := parseSFNT(buildSFNTWithCmapSubtables([]sub{
		{1, 0, buildCmapFormat4([][3]int{{0x0041, 0x0041, 1}})},
		{3, 1, bmp},
	}))
	if fp.cmap[0x41] != 100 {
		t.Errorf("(3,1)-only font: cmap[U+0041] = %d, want 100", fp.cmap[0x41])
	}

	// An unreadable preferred subtable must not displace a readable lesser one,
	// and must not leave an empty map behind.
	fp = parseSFNT(buildSFNTWithCmapSubtables([]sub{
		{3, 1, bmp},
		{3, 10, make([]byte, 16)}, // format 0 in a (3,10) slot, truncated
	}))
	if fp.cmap[0x41] != 100 {
		t.Errorf("unreadable (3,10): cmap[U+0041] = %d, want the (3,1) mapping 100", fp.cmap[0x41])
	}

	// A Unicode-platform subtable is used when there is no Windows one.
	fp = parseSFNT(buildSFNTWithCmapSubtables([]sub{{0, 4, full}}))
	if fp.cmap[0x1F600] != 600 {
		t.Errorf("(0,4) subtable ignored: %v", fp.cmap)
	}
}

// TestCmapFormat6PastBMP ensures a format-6 subtable whose entries run past
// 0xFFFF drops the out-of-range codes instead of aliasing them onto low codes
// when the caller narrows the map to uint16.
func TestCmapFormat6PastBMP(t *testing.T) {
	b := make([]byte, 10+2*4)
	put16 := func(off, v int) { b[off] = byte(v >> 8); b[off+1] = byte(v) }
	put16(0, 6)      // format
	put16(2, len(b)) // length
	put16(6, 0xFFFE) // firstCode
	put16(8, 4)      // entryCount, runs to 0x10001
	for i := 0; i < 4; i++ {
		put16(10+2*i, 7+i)
	}
	m := parseCmapSubtable(b)
	if len(m) != 2 || m[0xFFFE] != 7 || m[0xFFFF] != 8 {
		t.Errorf("format 6 past the BMP: got %v, want only U+FFFE->7 and U+FFFF->8", m)
	}
}

// TestOffsetsMatchObjects ensures normalizeStructure prunes doc.Offsets in
// lockstep with doc.Objects, so the byte-level checks never key on a removed
// object (audit C9).
func TestOffsetsMatchObjects(t *testing.T) {
	b := loadRefPDF(t)
	doc, err := Read(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for num := range doc.Offsets {
		if _, ok := doc.Objects[num]; !ok {
			t.Errorf("doc.Offsets holds object %d that is not in doc.Objects", num)
		}
	}
}

// TestInlineImageHonorsLength ensures binary data containing "EI" is skipped
// correctly when /L is declared (audit C25).
func TestInlineImageHonorsLength(t *testing.T) {
	// Binary section is 5 bytes that themselves contain " EI ".
	binary := []byte{'x', ' ', 'E', 'I', ' '} // 5 bytes, contains a false EI
	var content []byte
	content = append(content, []byte("BI /W 1 /H 1 /L 5 ID ")...)
	content = append(content, binary...)
	content = append(content, []byte("EI Q")...) // the real EI
	pos := len("BI")                             // start skip at the BI-consumed position simulated below

	// Drive skipInlineImage from just after "BI".
	p := 2
	skipInlineImage(content, &p)
	rest := string(content[p:])
	if rest != " Q" {
		t.Errorf("after inline image, remaining = %q, want %q (false EI in data mis-detected?)", rest, " Q")
	}
	_ = pos
}
