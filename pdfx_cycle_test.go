package pdf0

import "testing"

// TestDevColorScannerType3Cycle is the C3 guard: a Type3 font whose /Resources
// reference itself (directly or through another Type3 font) must not recurse
// forever in the device-colour scanner. A stack overflow is fatal and cannot be
// recovered, so the only way this test passes is if the recursion terminates.
func TestDevColorScannerType3Cycle(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}

	// Two Type3 fonts whose resource /Font entries reference each other, plus a
	// self-reference, forming a cycle of container() calls.
	fontA := &Dictionary{}
	fontB := &Dictionary{}
	doc.Objects[1] = &IndirectObject{Number: 1, Value: fontA}
	doc.Objects[2] = &IndirectObject{Number: 2, Value: fontB}

	fontsA := &Dictionary{}
	fontsA.Set("Self", IndirectRef{Number: 1})  // A -> A
	fontsA.Set("Other", IndirectRef{Number: 2}) // A -> B
	resA := &Dictionary{}
	resA.Set("Font", fontsA)
	fontA.Set("Subtype", Name("Type3"))
	fontA.Set("Resources", resA)

	fontsB := &Dictionary{}
	fontsB.Set("Back", IndirectRef{Number: 1}) // B -> A
	resB := &Dictionary{}
	resB.Set("Font", fontsB)
	fontB.Set("Subtype", Name("Type3"))
	fontB.Set("Resources", resB)

	done := make(chan struct{})
	go func() {
		s := newDevColorScanner(doc)
		_ = s.container(fontA, nil, nil)
		close(done)
	}()
	<-done // completes only if the cyclic recursion is broken
}

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
