package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestWTPDFExamples exercises the parser and serializer against the LaTeX
// Project's Well Tagged PDF / PDF/UA-2 example documents (all PDF 2.0). The
// files are not committed; fetch them with `make wtpdf` (see
// testdata/wtpdf/sources.tsv). The test self-skips when they are absent.
//
// These are complex, real-world tagged PDF 2.0 files (structure trees,
// associated files, MathML, custom role maps), so they are a strong round-trip
// and robustness check: every file must parse, expose pages, and survive a
// Read -> Write -> Read cycle without error or panic.
func TestWTPDFExamples(t *testing.T) {
	files, _ := filepath.Glob("testdata/wtpdf/*.pdf")
	if len(files) == 0 {
		t.Skip("no WTPDF examples present; run `make wtpdf` to download them")
	}
	for _, path := range files {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if doc.Version != "2.0" {
				t.Errorf("expected PDF 2.0, got %q", doc.Version)
			}
			if doc.PageCount() == 0 {
				t.Error("no pages")
			}
			// Round-trip: serialize and re-parse.
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if _, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
				t.Fatalf("re-Read after Write: %v", err)
			}
		})
	}
}
