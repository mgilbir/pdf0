package pdfx

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestDevColorScannerType3Cycle is the C3 guard: a Type3 font whose /Resources
// reference itself (directly or through another Type3 font) must not recurse
// forever in the device-colour scanner. A stack overflow is fatal and cannot be
// recovered, so the only way this test passes is if the recursion terminates.
func TestDevColorScannerType3Cycle(t *testing.T) {
	doc := mkView(nil, object.Dictionary{})

	// Two Type3 fonts whose resource /Font entries reference each other, plus a
	// self-reference, forming a cycle of container() calls.
	fontA := &object.Dictionary{}
	fontB := &object.Dictionary{}
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: fontA}
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: fontB}

	fontsA := &object.Dictionary{}
	fontsA.Set("Self", object.IndirectRef{Number: 1})  // A -> A
	fontsA.Set("Other", object.IndirectRef{Number: 2}) // A -> B
	resA := &object.Dictionary{}
	resA.Set("Font", fontsA)
	fontA.Set("Subtype", object.Name("Type3"))
	fontA.Set("Resources", resA)

	fontsB := &object.Dictionary{}
	fontsB.Set("Back", object.IndirectRef{Number: 1}) // B -> A
	resB := &object.Dictionary{}
	resB.Set("Font", fontsB)
	fontB.Set("Subtype", object.Name("Type3"))
	fontB.Set("Resources", resB)

	done := make(chan struct{})
	go func() {
		s := NewDevColorScanner(doc)
		_ = s.container(fontA, nil, nil)
		close(done)
	}()
	<-done // completes only if the cyclic recursion is broken
}
