package pdf0

import (
	"bytes"
	"testing"
)

// TestEmptyArrayColorSpaceNoPanic ensures an empty-array colour space is
// handled without panicking the validator (audit C3).
func TestEmptyArrayColorSpaceNoPanic(t *testing.T) {
	cs := &Dictionary{}
	cs.Set("CS0", Array{}) // empty-array colour space
	res := &Dictionary{}
	res.Set("ColorSpace", cs)
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Resources", res)
	doc := &Document{Version: "2.0", Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: page},
	}, Trailer: Dictionary{}}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ValidatePDFABytes panicked on empty-array colour space: %v", r)
		}
	}()
	_ = ValidatePDFABytes(doc, PDFA2b, nil)
}

// TestSelfReferentialDeviceNTerminates ensures a cyclic DeviceN /Colorants does
// not recurse forever (audit C4). It runs collectSeparationConsistency directly
// since a stack overflow is fatal and cannot be caught by recover.
func TestSelfReferentialDeviceNTerminates(t *testing.T) {
	// obj 10: [ /DeviceN [/A] /DeviceRGB <tint> << /Colorants << /A 10 0 R >> >> ]
	devN := Array{
		Name("DeviceN"),
		Array{Name("A")},
		Name("DeviceRGB"),
		IndirectRef{Number: 99},
		IndirectRef{Number: 11},
	}
	attrs := &Dictionary{}
	colorants := &Dictionary{}
	colorants.Set("A", IndirectRef{Number: 10}) // cycle back to the DeviceN array
	attrs.Set("Colorants", colorants)
	doc := &Document{Objects: map[int]*IndirectObject{
		10: {Number: 10, Value: devN},
		11: {Number: 11, Value: attrs},
		99: {Number: 99, Value: Null{}},
	}}
	tt := map[Name]sepColorantSeen{}
	var errs []ValidationError
	// Must return; if the cycle guard is missing this overflows the stack.
	collectSeparationConsistency(doc, IndirectRef{Number: 10}, tt, 10, PDFA2b, &errs)
}

// TestEqualCyclicNoOverflow ensures Equal on a cyclic direct dictionary returns
// rather than overflowing the stack (audit C15).
func TestEqualCyclicNoOverflow(t *testing.T) {
	d := &Dictionary{}
	d.Set("Self", d)
	d2 := &Dictionary{}
	d2.Set("Self", d2)
	_ = Equal(d, d2) // must return (false), not crash
}

// TestSerializeCyclicErrors ensures WriteObject on a cyclic graph returns an
// error rather than overflowing the stack (audit C15).
func TestSerializeCyclicErrors(t *testing.T) {
	d := &Dictionary{}
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
	var nilDict *Dictionary
	_ = Equal(nilDict, &Dictionary{})
	_ = Equal(nilDict, nilDict)
	var buf bytes.Buffer
	if err := NewSerializer(&buf).WriteObject(nilDict); err == nil {
		t.Fatalf("expected an error serializing a nil *Dictionary, got nil")
	}
}
