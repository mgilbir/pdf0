package core

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// LoadCMap decides when a font's CMap is one this module cannot read, and — as
// the producer — reports that skip itself. Its contract is as much about what
// it does *not* report: a guard that fires when nothing was skipped trains a
// reader to ignore it.
func TestLoadCMapReportsOnlyTheCMapsWithNoData(t *testing.T) {
	view := func(enc object.Object) (View, *object.Dictionary, *Recorder) {
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		if enc != nil {
			f.Set("Encoding", enc)
		}
		tr := object.Dictionary{}
		rec := &Recorder{}
		return View{
			Objects: map[int]*object.IndirectObject{1: {Number: 1, Value: f}},
			Trailer: &tr,
			Limits:  DefaultLimits(),
			Run:     NewRun(rec),
		}, f, rec
	}
	check := func(what string, v View, f *object.Dictionary, rec *Recorder, want Reason, wantTrip bool) {
		t.Helper()
		// Asked twice, as two checks of one font would: one report at most.
		LoadCMap(v, f)
		if _, got := LoadCMap(v, f); got != want {
			t.Errorf("%s: reason %v, want %v", what, got, want)
		}
		trips := rec.Snapshot()
		switch {
		case wantTrip && (len(trips) != 1 || trips[0].Guard() != GuardPredefinedCMap || trips[0].Obj != 1):
			t.Errorf("%s: trips %v, want one %s on the font", what, trips, GuardPredefinedCMap)
		case !wantTrip && len(trips) != 0:
			t.Errorf("%s: trips %v, want none", what, trips)
		}
	}

	// The case it exists for: a predefined CJK CMap, whose code-to-CID data is
	// not carried.
	for _, name := range []string{"UniJIS-UCS2-H", "GBK-EUC-H", "ETen-B5-V"} {
		v, f, rec := view(object.Name(name))
		check(name, v, f, rec, ReasonUnsupported, true)
	}

	// Identity-H and -V are predefined too, and are built in, so nothing is
	// skipped and nothing is reported.
	for _, name := range []string{"Identity-H", "Identity-V"} {
		v, f, rec := view(object.Name(name))
		check(name, v, f, rec, ReasonOK, false)
	}

	// A name that is not predefined at all is a Table 118 violation, reported
	// as such by the CMap-legality rule. Calling it unreadable would be a
	// weaker claim about a file already known to be wrong.
	v, f, rec := view(object.Name("Not-A-Real-CMap"))
	check("an illegal name", v, f, rec, ReasonMalformed, false)

	// An embedded stream is read (this one has no codespace, so it is
	// malformed), and an absent /Encoding names nothing.
	v, f, rec = view(object.IndirectRef{Number: 2})
	v.Objects[2] = &object.IndirectObject{Number: 2, Value: &object.Stream{Dict: object.Dictionary{}}}
	check("an embedded stream", v, f, rec, ReasonMalformed, false)
	v, f, rec = view(nil)
	check("no /Encoding", v, f, rec, ReasonMalformed, false)

	// And the name may be written indirectly, like almost any value.
	v, f, rec = view(object.IndirectRef{Number: 3})
	v.Objects[3] = &object.IndirectObject{Number: 3, Value: object.Name("UniJIS-UCS2-H")}
	check("an indirect name", v, f, rec, ReasonUnsupported, true)
}
