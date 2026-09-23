package pdf0

import (
	"bytes"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Reproducible output: the same inputs giving the same bytes.
//
// Two things in a generated PDF/A document used to vary between runs. The sRGB
// profile carried its creation timestamp, which embedding fixed. The file
// identifier is the other, and it is meant to vary — it exists to tell this
// file from every other — so it is not fixed, it is *offered*: a caller who
// needs the bytes to be a function of the content supplies one.

// reproDoc builds the same document twice over, varying only what the caller
// controls.
func reproDoc(t *testing.T, opts pdfa.SkeletonOptions) []byte {
	t.Helper()
	doc, err := NewPDFADocumentWith(opts)
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	var b content.Builder
	b.Rect(10, 10, 120, 40).Fill()
	b.Rect(20, 60, 80, 20).Fill()
	if _, err := doc.AddPage(Page{Width: 200, Height: 200, Content: &b}); err != nil {
		t.Fatalf("adding a page: %v", err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	return buf.Bytes()
}

func TestAPinnedFileIDMakesTheOutputReproducible(t *testing.T) {
	opts := pdfa.SkeletonOptions{
		Level:  pdfa.PDFA2b,
		Title:  "Invoice",
		Author: "pdf0",
		FileID: []byte("0123456789abcdef"),
	}

	a, b := reproDoc(t, opts), reproDoc(t, opts)
	if !bytes.Equal(a, b) {
		n := 0
		for i := range a {
			if i < len(b) && a[i] != b[i] {
				n++
			}
		}
		t.Fatalf("two builds with the same inputs differ in %d bytes (%d vs %d); "+
			"a pinned file ID is supposed to be the last thing that varies", n, len(a), len(b))
	}

	// And the document is still a conforming one, rather than reproducible at
	// the cost of being wrong.
	doc, err := Read(bytes.NewReader(a), int64(len(a)))
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if errs := ValidatePDFA(doc, pdfa.PDFA2b); len(errs) != 0 {
		t.Errorf("a reproducible document does not validate: %v", errs)
	}

	// The ID in the file is the one that was asked for, in both halves.
	arr, _ := doc.Resolve(doc.Trailer.Get("ID")).(object.Array)
	if len(arr) != 2 {
		t.Fatalf("/ID has %d entries, want 2", len(arr))
	}
	for i, e := range arr {
		s, ok := doc.Resolve(e).(object.String)
		if !ok || string(s.Value) != "0123456789abcdef" {
			t.Errorf("/ID[%d] is %q, want the supplied bytes", i, s.Value)
		}
	}
}

// TestWithoutAPinnedIDTheOutputVaries, so the test above is measuring the
// option and not a library that happens to be deterministic anyway.
func TestWithoutAPinnedIDTheOutputVaries(t *testing.T) {
	opts := pdfa.SkeletonOptions{Level: pdfa.PDFA2b}
	a, b := reproDoc(t, opts), reproDoc(t, opts)
	if bytes.Equal(a, b) {
		t.Error("two builds with no file ID produced identical bytes; either the " +
			"identifier stopped being random, which is a defect, or the test above " +
			"proves nothing")
	}
	// They differ only in the identifier: same length, and a handful of bytes.
	if len(a) != len(b) {
		t.Errorf("the two builds are %d and %d bytes; the identifier is fixed-width, "+
			"so something else varies too", len(a), len(b))
	}
}

// TestAPinnedIDIsCopied, since a caller that reuses its buffer would otherwise
// change a document it has already built.
func TestAPinnedIDIsCopied(t *testing.T) {
	id := []byte("0123456789abcdef")
	doc, err := NewPDFADocumentWith(pdfa.SkeletonOptions{Level: pdfa.PDFA4, FileID: id})
	if err != nil {
		t.Fatal(err)
	}
	for i := range id {
		id[i] = 'z'
	}
	arr, _ := doc.Trailer.Get("ID").(object.Array)
	s, _ := arr[0].(object.String)
	if string(s.Value) != "0123456789abcdef" {
		t.Errorf("/ID is %q; writing to the caller's slice changed the document", s.Value)
	}
}

// TestTheFileIDIsRandomRatherThanDerivedFromTheClock.
//
// It used to be md5(time.Now()). Two documents built inside one clock tick
// shared an identifier, which is the one thing the identifier exists to
// prevent. Both builders now take it from the same random source.
func TestTheFileIDIsRandomRatherThanDerivedFromTheClock(t *testing.T) {
	idOf := func(d *Document) string {
		arr, _ := d.Trailer.Get("ID").(object.Array)
		if len(arr) == 0 {
			return ""
		}
		s, _ := arr[0].(object.String)
		return string(s.Value)
	}
	for _, tc := range []struct {
		name string
		mk   func() *Document
	}{
		{"NewPDFADocument", func() *Document { return mustPDFADoc(t, pdfa.PDFA2b) }},
		{"NewDocument", NewDocument},
	} {
		seen := map[string]bool{}
		const n = 2000
		for i := 0; i < n; i++ {
			id := idOf(tc.mk())
			if len(id) != 16 {
				t.Fatalf("%s: identifier is %d bytes, want 16", tc.name, len(id))
			}
			if seen[id] {
				t.Fatalf("%s: two of %d documents share an identifier", tc.name, n)
			}
			seen[id] = true
		}
	}
}
