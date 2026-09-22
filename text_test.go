package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"os"
	"strings"
	"testing"
)

// TestExtractText checks text extraction against a reference PDF (skips when the
// reference set is absent).
func TestExtractText(t *testing.T) {
	data, err := os.ReadFile(testfiles.PDF20Examples.File(t, "Simple PDF 2.0 file.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(doc.ExtractText())
	if !strings.Contains(got, "Hello World") {
		t.Errorf("extracted text %q does not contain %q", got, "Hello World")
	}
}
