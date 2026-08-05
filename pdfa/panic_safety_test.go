package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestSelfReferentialDeviceNTerminates ensures a cyclic DeviceN /Colorants does
// not recurse forever (audit C4). It runs collectSeparationConsistency directly
// since a stack overflow is fatal and cannot be caught by recover.
func TestSelfReferentialDeviceNTerminates(t *testing.T) {
	// obj 10: [ /DeviceN [/A] /DeviceRGB <tint> << /Colorants << /A 10 0 R >> >> ]
	devN := object.Array{
		object.Name("DeviceN"),
		object.Array{object.Name("A")},
		object.Name("DeviceRGB"),
		object.IndirectRef{Number: 99},
		object.IndirectRef{Number: 11},
	}
	attrs := &object.Dictionary{}
	colorants := &object.Dictionary{}
	colorants.Set("A", object.IndirectRef{Number: 10}) // cycle back to the DeviceN array
	attrs.Set("Colorants", colorants)
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		10: {Number: 10, Value: devN},
		11: {Number: 11, Value: attrs},
		99: {Number: 99, Value: object.Null{}},
	}})
	tt := map[object.Name]sepColorantSeen{}
	var errs []Violation
	// Must return; if the cycle guard is missing this overflows the stack.
	collectSeparationConsistency(doc, object.IndirectRef{Number: 10}, tt, 10, PDFA2b, &errs)
}
