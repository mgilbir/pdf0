package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestDictionaryEqualDuplicateKeys guards audit C26 (first-occurrence
// comparison gave false positives on duplicate keys). A Dictionary now holds
// at most one entry per key — a duplicate keeps the first position and the
// last value — so the shapes that fooled the comparison cannot be built.
func TestDictionaryEqualDuplicateKeys(t *testing.T) {
	e := func(k object.Name, v int) object.Entry { return object.Entry{Key: k, Value: object.Integer(v)} }
	dup11 := object.NewDictionary(e("A", 1), e("A", 1))
	a1b99 := object.NewDictionary(e("A", 1), e("B", 99))
	if dup11.Len() != 1 {
		t.Errorf("{A:1,A:1} holds %d entries, want 1", dup11.Len())
	}
	if Equal(dup11, a1b99) {
		t.Errorf("{A:1,A:1} must not equal {A:1,B:99}")
	}
	dup12 := object.NewDictionary(e("A", 1), e("A", 2))
	if !Equal(dup12, dup12) {
		t.Errorf("a dictionary built with a duplicate key must equal itself")
	}
	if !Equal(dup12, object.NewDictionary(e("A", 2))) || Equal(dup12, object.NewDictionary(e("A", 1))) {
		t.Errorf("{A:1,A:2} must equal {A:2} and not {A:1}")
	}
}

// TestWriteNameRejectsNUL ensures a NUL in a name is refused rather than emitted
// as unparseable "#00" (audit C31).
func TestWriteNameRejectsNUL(t *testing.T) {
	var buf bytes.Buffer
	if err := NewSerializer(&buf).WriteObject(object.Name("a\x00b")); err == nil {
		t.Errorf("expected an error serializing a name containing NUL, got nil")
	}
}

// TestCmapFormat4Budget ensures a hostile format-4 cmap terminates quickly
// (audit C10).
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

	// Drive core.SkipInlineImage from just after "BI".
	p := 2
	core.SkipInlineImage(content, &p)
	rest := string(content[p:])
	if rest != " Q" {
		t.Errorf("after inline image, remaining = %q, want %q (false EI in data mis-detected?)", rest, " Q")
	}
	_ = pos
}
