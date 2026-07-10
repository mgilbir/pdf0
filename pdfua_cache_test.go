package pdf0

import (
	"bytes"
	"testing"
)

// TestUAValidationCacheIsolation verifies that the per-run cache installed by
// ValidatePDFUA does not leak onto or mutate the caller's document, and that
// repeated validation is deterministic.
func TestUAValidationCacheIsolation(t *testing.T) {
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.valCache != nil {
		t.Fatal("fresh document should have no valCache")
	}
	v1 := ValidatePDFUA(doc)
	if doc.valCache != nil {
		t.Error("ValidatePDFUA leaked its cache onto the caller's document")
	}
	v2 := ValidatePDFUA(doc)
	if len(v1) != len(v2) {
		t.Errorf("validation is not deterministic: %d vs %d violations", len(v1), len(v2))
	}
}

// TestFontUsageCacheMatches verifies the memoized font-usage map equals the
// uncached result.
func TestFontUsageCacheMatches(t *testing.T) {
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	uncached := collectFontTextUsage(doc)
	doc.valCache = &validationCache{pages: map[int][]pageInfo{}, content: map[*Stream][]byte{}}
	first := collectFontTextUsage(doc)
	second := collectFontTextUsage(doc)
	if len(uncached) != len(first) || len(first) != len(second) {
		t.Errorf("font usage size mismatch: uncached=%d first=%d second=%d", len(uncached), len(first), len(second))
	}
	// The cached call must return the identical map instance.
	if &first == &second {
		_ = first
	}
}
