package pdf0

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/pdfa"
	"os"
	"sync"
	"testing"
)

// TestValidateConcurrentSameDoc ensures the same *Document can be validated from
// multiple goroutines at once without a data race: the per-run cache lives on a
// shallow copy, never on the shared input.
//
// Run with -race to actually catch the regression.
func TestValidateConcurrentSameDoc(t *testing.T) {
	files := testfiles.PDF20Examples.Glob(t, "*.pdf")
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("reading reference PDF: %v", err)
	}
	doc, err := Read(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, lvl := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(l pdfa.Level) {
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

// TestValidateConcurrentLargeParsedDict is the C45 regression test (audit
// 2026-09-22). TestValidateConcurrentSameDoc never saw the race because no
// dictionary in its reference file reaches the size at which lookups switch
// to an index. Here the catalog has 70 keys and the document is re-read from
// bytes, so the dictionary is parser-produced, then eight goroutines validate
// it at once. Reads of a Dictionary must be pure; before the fix the first
// Get built the index lazily and -race reported a DATA RACE on it.
//
// Run with -race to catch the regression.
func TestValidateConcurrentLargeParsedDict(t *testing.T) {
	src, err := NewPDFADocument(pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	cat := src.ResolveDict(src.Trailer.Get("Root"))
	if cat == nil {
		t.Fatal("no catalog")
	}
	for i := 0; i < 70; i++ {
		cat.Set(Name(fmt.Sprintf("X%02d", i)), Integer(i))
	}
	var buf bytes.Buffer
	if err := src.Write(&buf); err != nil {
		t.Fatal(err)
	}
	doc, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if n := doc.ResolveDict(doc.Trailer.Get("Root")).Len(); n < 70 {
		t.Fatalf("re-read catalog has %d keys, want at least 70", n)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ValidatePDFA(doc, pdfa.PDFA2b)
		}()
	}
	wg.Wait()
}
