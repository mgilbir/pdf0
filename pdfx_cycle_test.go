package pdf0

import (
	"testing"
)

// TestLevelAValidationDoesNotPanic exercises the Level A pipeline (whose checks
// now run under runCheck, audit C27) on a degenerate document, asserting no
// panic escapes to the caller.
func TestLevelAValidationDoesNotPanic(t *testing.T) {
	// Minimal document with just a catalog: enough to drive the Level A checks
	// (conformance/structure/language) without tripping earlier hard errors.
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	doc := &Document{
		Objects: map[int]*IndirectObject{1: {Number: 1, Value: cat}},
		Trailer: Dictionary{},
	}
	doc.Trailer.Set("Root", IndirectRef{Number: 1})

	for _, lvl := range []PDFALevel{PDFA1a, PDFA2a, PDFA3a} {
		// Must return (any findings are fine); a panic would fail the test.
		_ = ValidatePDFA(doc, lvl)
	}
}
