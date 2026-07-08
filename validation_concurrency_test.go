package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestValidateConcurrentSameDoc ensures the same *Document can be validated from
// multiple goroutines at once without a data race: the per-run cache lives on a
// shallow copy, never on the shared input.
//
// Run with -race to actually catch the regression.
func TestValidateConcurrentSameDoc(t *testing.T) {
	files, _ := filepath.Glob("testdata/pdf20examples/*.pdf")
	if len(files) == 0 {
		t.Skip("testdata/pdf20examples not present")
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Skipf("reading reference PDF: %v", err)
	}
	doc, err := Read(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, lvl := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(l PDFALevel) {
				defer wg.Done()
				ValidatePDFABytes(doc, l, b)
			}(lvl)
		}
	}
	wg.Wait()

	// Validation must not have mutated the caller's Document.
	if doc.valCache != nil {
		t.Errorf("ValidatePDFABytes left a cache on the caller's Document")
	}
}
