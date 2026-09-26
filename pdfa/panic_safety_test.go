package pdfa

import (
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
	"testing"
	"time"
)

// TestSelfReferentialDeviceNTerminates ensures a cyclic DeviceN /Colorants does
// not recurse forever (audit C4). A page selects the DeviceN space, so the
// Separation consistency walk reaches it through the executed content; it runs
// capped, since a stack overflow is fatal and cannot be caught by recover.
func TestSelfReferentialDeviceNTerminates(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
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
		doc := mkPageWithContentAndRes("/CS0 cs 1 sc 0 0 10 10 re f",
			dictWith("ColorSpace", dictWith("CS0", object.IndirectRef{Number: 10})))
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: devN}
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: attrs}
		doc.Objects[99] = &object.IndirectObject{Number: 99, Value: object.Null{}}
		// Must return; if the cycle guard is missing this overflows the stack.
		if errs := checkSeparationConsistency(doc, PDFA2b); len(errs) != 0 {
			t.Errorf("a DeviceN with one colorant defines nothing inconsistent: %v", errs)
		}
	})
}
