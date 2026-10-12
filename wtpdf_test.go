package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"os"
	"path/filepath"
	"strings"
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
	files := testfiles.WTPDF.Glob(t, "*.pdf")
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

// TestWTPDFValidatePDFUA2 holds the WTPDF examples to ValidatePDFUA2. Every
// one is a PDF/UA-2 document veraPDF 1.30.3 passes (-f ua2), so a finding is
// pdf0's to justify: each file is clean, but for the findings listed here,
// which are pdf0's reading of the standard where veraPDF under-reports.
//
// tb139mitt-deims24: its XMP packet has a U+0001 in a dc:creator item. XML
// 1.0's Char production excludes it, so the packet is not XML (ISO 32000-2
// 14.3.2, which ISO 14289-2 6.2 requires). veraPDF's XMP parser replaces ASCII
// controls before parsing (ParseOptions FIX_CONTROL_CHARS, on by default).
func TestWTPDFValidatePDFUA2(t *testing.T) {
	expected := map[string][]struct{ clause, msg string }{
		"tb139mitt-deims24.pdf": {{"5", "illegal character code U+0001"}},
	}
	files := testfiles.WTPDF.Glob(t, "*.pdf")
	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			want := expected[name]
			matched := make([]bool, len(want))
		findings:
			for _, v := range ValidatePDFUA2(doc) {
				for i, w := range want {
					if !matched[i] && v.Clause == w.clause && strings.Contains(v.Message, w.msg) {
						matched[i] = true
						continue findings
					}
				}
				t.Errorf("finding on a PDF/UA-2 file veraPDF passes: %v", v)
			}
			for i, w := range want {
				if !matched[i] {
					t.Errorf("expected finding not reported (clause %s, %q): update the expectation", w.clause, w.msg)
				}
			}
		})
	}
}
