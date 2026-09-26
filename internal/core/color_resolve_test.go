package core

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// Colour-space and transparency queries read values any of which a file may
// write indirectly. These pin that every one of them is resolved before it is
// asked what type it is (audit 2026-09-22 C152, and the siblings found with
// TestResolveBeforeAssert).

func resolveTestView(objs map[int]object.Object) View {
	v := View{Objects: map[int]*object.IndirectObject{}, Limits: DefaultLimits(), Run: NewRun(&Recorder{})}
	for n, o := range objs {
		v.Objects[n] = &object.IndirectObject{Number: n, Value: o}
	}
	return v
}

// TestClassifyCalibratedCSIndirectN: an ICCBased profile whose /N is an
// indirect reference to 3 covers DeviceRGB exactly as a direct 3 does. It used
// to be read only when direct, so a page group /CS written this way lost its
// coverage and the page was accused of uncovered DeviceRGB.
func TestClassifyCalibratedCSIndirectN(t *testing.T) {
	for _, tc := range []struct {
		n             int
		rgb, cmyk, gr bool
	}{{1, false, false, true}, {3, true, false, false}, {4, false, true, false}} {
		profile := object.NewStream(object.NewDictionary(object.Entry{Key: "N", Value: object.IndirectRef{Number: 6}}), nil)
		v := resolveTestView(map[int]object.Object{5: profile, 6: object.Integer(tc.n)})
		cs := object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 5}}
		r, c, g := ClassifyCalibratedCS(v, cs)
		if r != tc.rgb || c != tc.cmyk || g != tc.gr {
			t.Errorf("/N %d 0 R -> %d: covers (RGB %v, CMYK %v, Gray %v), want (%v, %v, %v)", 6, tc.n, r, c, g, tc.rgb, tc.cmyk, tc.gr)
		}
		// The family name itself may be indirect too.
		v.Objects[7] = &object.IndirectObject{Number: 7, Value: object.Name("ICCBased")}
		cs = object.Array{object.IndirectRef{Number: 7}, object.IndirectRef{Number: 5}}
		if r2, c2, g2 := ClassifyCalibratedCS(v, cs); r2 != r || c2 != c || g2 != g {
			t.Errorf("indirect /ICCBased name: covers (%v, %v, %v), want (%v, %v, %v)", r2, c2, g2, r, c, g)
		}
	}
}

// TestTransparencyIndirectValues: an ExtGState or annotation whose /BM, /CA,
// /ca or /SMask is indirect is read through the reference. Unresolved, an
// indirect /Multiply blend mode or /ca 0.5 read as "absent" and the page was
// called opaque, and an indirect /SMask /None read as a soft mask.
func TestTransparencyIndirectValues(t *testing.T) {
	page := func(gs *object.Dictionary) *object.Dictionary {
		return object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Page")},
			object.Entry{Key: "Resources", Value: object.NewDictionary(
				object.Entry{Key: "ExtGState", Value: object.NewDictionary(object.Entry{Key: "GS0", Value: gs})})},
		)
	}
	objs := map[int]object.Object{
		10: object.Name("Multiply"),
		11: object.Real(0.5),
		12: object.Name("None"),
		13: object.Name("Normal"),
	}
	for _, tc := range []struct {
		name string
		gs   *object.Dictionary
		want bool
	}{
		{"indirect BM Multiply", object.NewDictionary(object.Entry{Key: "BM", Value: object.IndirectRef{Number: 10}}), true},
		{"indirect ca 0.5", object.NewDictionary(object.Entry{Key: "ca", Value: object.IndirectRef{Number: 11}}), true},
		{"indirect SMask None", object.NewDictionary(object.Entry{Key: "SMask", Value: object.IndirectRef{Number: 12}}), false},
		{"indirect BM Normal", object.NewDictionary(object.Entry{Key: "BM", Value: object.IndirectRef{Number: 13}}), false},
	} {
		v := resolveTestView(objs)
		if got := PageUsesTransparency(v, page(tc.gs)); got != tc.want {
			t.Errorf("%s: PageUsesTransparency = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The same values on an annotation dictionary.
	for _, tc := range []struct {
		name  string
		annot *object.Dictionary
		want  bool
	}{
		{"annot indirect BM", object.NewDictionary(object.Entry{Key: "BM", Value: object.IndirectRef{Number: 10}}), true},
		{"annot indirect CA", object.NewDictionary(object.Entry{Key: "CA", Value: object.IndirectRef{Number: 11}}), true},
	} {
		v := resolveTestView(objs)
		p := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Page")},
			object.Entry{Key: "Annots", Value: object.Array{tc.annot}},
		)
		if got := PageUsesTransparency(v, p); got != tc.want {
			t.Errorf("%s: PageUsesTransparency = %v, want %v", tc.name, got, tc.want)
		}
	}
}
