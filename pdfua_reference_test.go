package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"os"
	"path/filepath"
	"testing"
)

// TestUAReferenceFilesNoFalsePositives runs the PDF/UA validator over the PDF
// Association's conformant reference files and requires zero violations — the
// false-positive oracle for the accessibility checks. The files are the
// PDFUA-Reference-Files suite (pdfa.org), kept locally under spec/pdfua/
// (gitignored, not committed); the test self-skips when they are absent.
func TestUAReferenceFilesNoFalsePositives(t *testing.T) {
	files := testfiles.PDFUAReference.Glob(t, "*.pdf")
	for _, p := range files {
		p := p
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			for _, e := range ValidatePDFUA(doc) {
				t.Errorf("false positive on a conformant file: %s", e)
			}
		})
	}
}
