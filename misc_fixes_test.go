package pdf0

import (
	"bytes"
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
		_, _ = parseCmapSubtable(b)
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
	m, _ := parseCmapSubtable(buildCmapFormat4([][3]int{
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
	m, _ := parseCmapSubtable(buildCmapFormat4([][3]int{{0xFFFE, 0xFFFF, 0x8000}}))
	if m[0xFFFE] != 0x7FFE || m[0xFFFF] != 0x7FFF {
		t.Errorf("terminal segment: got %v, want U+FFFE->0x7FFE, U+FFFF->0x7FFF", m)
	}
}

// TestCmapFormat4InvertedSegment ensures a malformed segment with start > end is
// skipped without disturbing the segments around it.
func TestCmapFormat4InvertedSegment(t *testing.T) {
	m, _ := parseCmapSubtable(buildCmapFormat4([][3]int{
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
	fmt12 := make([]byte, 16+12) // header + one group
	fmt12[1] = 12                // format
	if m, _ := parseCmapSubtable(fmt12); m != nil {
		t.Errorf("format 12 subtable: got %v, want nil", m)
	}
	if m, _ := parseCmapSubtable(make([]byte, 100)); m != nil {
		t.Errorf("truncated format 0 subtable: got %v, want nil", m)
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
	m, _ := parseCmapSubtable(b)
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
