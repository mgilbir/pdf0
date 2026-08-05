package pdf0

import (
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"testing"
)

// TestLevelAValidationDoesNotPanic exercises the Level A pipeline (whose checks
// now run under runCheck, audit C27) on a degenerate document, asserting no
// panic escapes to the caller.
func TestLevelAValidationDoesNotPanic(t *testing.T) {
	// Minimal document with just a catalog: enough to drive the Level A checks
	// (conformance/structure/language) without tripping earlier hard errors.
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	doc := &Document{
		Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: cat}},
		Trailer: object.Dictionary{},
	}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	for _, lvl := range []pdfa.Level{pdfa.PDFA1a, pdfa.PDFA2a, pdfa.PDFA3a} {
		// Must return (any findings are fine); a panic would fail the test.
		_ = ValidatePDFA(doc, lvl)
	}
}
