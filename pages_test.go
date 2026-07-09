package pdf0

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func readRef(t *testing.T, name string) *Document {
	t.Helper()
	data, err := os.ReadFile("testdata/pdf20examples/" + name)
	if err != nil {
		t.Skip("reference PDFs not present; run `make refpdfs`")
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestExtractAndMergePages extracts a page into a fresh document and merges two
// documents, verifying page counts, text, and a write round-trip.
func TestExtractAndMergePages(t *testing.T) {
	src := readRef(t, "Simple PDF 2.0 file.pdf")
	if src.PageCount() != 1 {
		t.Fatalf("source page count = %d", src.PageCount())
	}

	// Extract page 0 into a new document.
	sub, err := src.ExtractPages([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	if sub.PageCount() != 1 {
		t.Errorf("extracted page count = %d, want 1", sub.PageCount())
	}
	var buf bytes.Buffer
	if err := sub.Write(&buf); err != nil {
		t.Fatalf("write extracted: %v", err)
	}
	re, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("re-read extracted: %v", err)
	}
	if !strings.Contains(re.ExtractText(), "Hello World") {
		t.Errorf("extracted page lost its text: %q", re.ExtractText())
	}

	// Merge two copies → two pages.
	other := readRef(t, "Simple PDF 2.0 file.pdf")
	merged, _ := src.ExtractPages([]int{0})
	merged.AppendPages(other)
	if merged.PageCount() != 2 {
		t.Errorf("merged page count = %d, want 2", merged.PageCount())
	}
	var mbuf bytes.Buffer
	if err := merged.Write(&mbuf); err != nil {
		t.Fatalf("write merged: %v", err)
	}
	remerged, err := Read(bytes.NewReader(mbuf.Bytes()), int64(mbuf.Len()))
	if err != nil {
		t.Fatalf("re-read merged: %v", err)
	}
	if remerged.PageCount() != 2 {
		t.Errorf("re-read merged page count = %d, want 2", remerged.PageCount())
	}
}
