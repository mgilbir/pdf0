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
		parseCmapSubtable(b)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("parseCmapSubtable did not terminate within the work budget")
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
