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
	got := strings.TrimSpace(mustExtractText(t, doc))
	if !strings.Contains(got, "Hello World") {
		t.Errorf("extracted text %q does not contain %q", got, "Hello World")
	}
}

// mustExtractText is ExtractText for a test that expects every page's text:
// an error fails the test.
func mustExtractText(t testing.TB, d *Document) string {
	t.Helper()
	text, err := d.ExtractText()
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	return text
}
