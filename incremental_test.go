package pdf0

import (
	"bytes"
	"testing"
)

// TestWriteIncremental appends a new object and confirms the original bytes are
// preserved verbatim while the change is visible on re-read.
func TestWriteIncremental(t *testing.T) {
	original := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	// Add an Info dictionary as a new object and reference it from the trailer.
	info := &Dictionary{}
	info.Set("Producer", String{Value: []byte("incremental-update-marker")})
	doc.Objects[4] = &IndirectObject{Number: 4, Value: info}
	doc.Trailer.Set("Info", IndirectRef{Number: 4})

	var buf bytes.Buffer
	if err := doc.WriteIncremental(&buf, original, []int{4}); err != nil {
		t.Fatalf("WriteIncremental: %v", err)
	}
	out := buf.Bytes()

	// The update must be strictly appended.
	if !bytes.HasPrefix(out, original) {
		t.Fatal("output does not begin with the original bytes verbatim")
	}
	if len(out) <= len(original) {
		t.Fatal("nothing was appended")
	}

	doc2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	info2 := doc2.ResolveDict(doc2.Trailer.Get("Info"))
	if info2 == nil {
		t.Fatal("Info not present after incremental update")
	}
	if p, _ := info2.Get("Producer").(String); string(p.Value) != "incremental-update-marker" {
		t.Errorf("/Producer = %q", p.Value)
	}
	// The original catalog must still resolve through the /Prev chain.
	if doc2.ResolveDict(doc2.Trailer.Get("Root")) == nil {
		t.Error("catalog not resolvable after incremental update")
	}
}
