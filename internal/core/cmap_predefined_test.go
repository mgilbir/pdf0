package core

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// PredefinedCMapName decides when a font's CMap is one this module cannot read,
// which is what turns a silent skip into a reported one. Its contract is as
// much about what it does *not* answer: a guard that fires when nothing was
// skipped trains a reader to ignore it.
func TestPredefinedCMapNameAnswersOnlyForTheCMapsWithNoData(t *testing.T) {
	view := func(enc object.Object) (View, *object.Dictionary) {
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		if enc != nil {
			f.Set("Encoding", enc)
		}
		tr := object.Dictionary{}
		return View{
			Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: f}},
			Trailer: &tr,
			Limits:  DefaultLimits(),
			Run:     NewRun(&Recorder{}),
		}, f
	}

	// The case it exists for: a predefined CJK CMap, whose code-to-CID data is
	// not carried.
	for _, name := range []string{"UniJIS-UCS2-H", "GBK-EUC-H", "ETen-B5-V"} {
		v, f := view(object.Name(name))
		if got, ok := PredefinedCMapName(v, f); !ok || got != name {
			t.Errorf("%s: got (%q, %v), want (%q, true)", name, got, ok, name)
		}
	}

	// Identity-H and -V are predefined too, and LoadCMap answers them, so
	// nothing is skipped and nothing should be reported. This is the assertion
	// the call sites cannot make: they only reach this function when LoadCMap
	// has already failed, which for Identity never happens.
	for _, name := range []string{"Identity-H", "Identity-V"} {
		v, f := view(object.Name(name))
		if _, ok := PredefinedCMapName(v, f); ok {
			t.Errorf("%s is read by LoadCMap, so it is not a skip", name)
		}
	}

	// A name that is not predefined at all is a Table 118 violation, reported
	// as such. Calling it unreadable would be a weaker claim about a file
	// already known to be wrong.
	v, f := view(object.Name("Not-A-Real-CMap"))
	if _, ok := PredefinedCMapName(v, f); ok {
		t.Error("an illegal CMap name was called an unreadable one")
	}

	// An embedded stream is read, and an absent /Encoding names nothing.
	v, f = view(object.IndirectRef{Number: 2})
	v.Objects[2] = &object.IndirectObject{Number: 2, Value: &object.Stream{Dict: object.Dictionary{}}}
	if _, ok := PredefinedCMapName(v, f); ok {
		t.Error("an embedded CMap stream was called a predefined name")
	}
	v, f = view(nil)
	if _, ok := PredefinedCMapName(v, f); ok {
		t.Error("a font with no /Encoding was called a predefined name")
	}

	// And the name may be written indirectly, like almost any value.
	v, f = view(object.IndirectRef{Number: 3})
	v.Objects[3] = &object.IndirectObject{Number: 3, Value: object.Name("UniJIS-UCS2-H")}
	if got, ok := PredefinedCMapName(v, f); !ok || got != "UniJIS-UCS2-H" {
		t.Errorf("an indirect /Encoding gave (%q, %v)", got, ok)
	}
}
