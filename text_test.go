package pdf0

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestExtractText checks text extraction against a reference PDF (skips when the
// reference set is absent).
func TestExtractText(t *testing.T) {
	const path = "testdata/pdf20examples/Simple PDF 2.0 file.pdf"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("reference PDFs not present; run `make refpdfs`")
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
