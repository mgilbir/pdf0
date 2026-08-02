package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"testing"
)

// TestEmptyArrayColorSpaceNoPanic ensures an empty-array colour space is
// handled without panicking the validator (audit C3).
func TestEmptyArrayColorSpaceNoPanic(t *testing.T) {
	cs := &object.Dictionary{}
	cs.Set("CS0", object.Array{}) // empty-array colour space
	res := &object.Dictionary{}
	res.Set("ColorSpace", cs)
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Resources", res)
	doc := &Document{Version: "2.0", Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: page},
	}, Trailer: object.Dictionary{}}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ValidatePDFABytes panicked on empty-array colour space: %v", r)
		}
	}()
	_ = ValidatePDFABytes(doc, pdfa.PDFA2b, nil)
}

// TestEqualCyclicNoOverflow ensures Equal on a cyclic direct dictionary returns
// rather than overflowing the stack (audit C15).
func TestEqualCyclicNoOverflow(t *testing.T) {
	d := &object.Dictionary{}
	d.Set("Self", d)
	d2 := &object.Dictionary{}
	d2.Set("Self", d2)
	_ = Equal(d, d2) // must return (false), not crash
}

// TestSerializeCyclicErrors ensures WriteObject on a cyclic graph returns an
// error rather than overflowing the stack (audit C15).
func TestSerializeCyclicErrors(t *testing.T) {
	d := &object.Dictionary{}
	d.Set("Self", d)
	var buf bytes.Buffer
	if err := NewSerializer(&buf).WriteObject(d); err == nil {
		t.Fatalf("expected a depth-limit error serializing a cyclic dictionary, got nil")
	}
}

// TestTypedNilNoPanic ensures typed-nil pointers do not panic (audit C30).
func TestTypedNilNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("typed-nil handling panicked: %v", r)
		}
	}()
	var nilDict *object.Dictionary
	_ = Equal(nilDict, &object.Dictionary{})
	_ = Equal(nilDict, nilDict)
	var buf bytes.Buffer
	if err := NewSerializer(&buf).WriteObject(nilDict); err == nil {
		t.Fatalf("expected an error serializing a nil *Dictionary, got nil")
	}
}
