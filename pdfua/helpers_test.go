package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mkView builds the view these tests check against: an object graph, a trailer,
// resolved limits and a live run.
//
// The limits and the run both matter. A zero core.Limits is a budget of zero,
// so a view built without them decodes nothing while reporting no error; and a
// nil run makes every memo a fresh one, which turns the memoization tests into
// tautologies. The root package's Document.view supplies both, and this is the
// equivalent for a package that does not have a Document.
func mkView(objs map[int]*object.IndirectObject, trailer *object.Dictionary) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	if trailer == nil {
		trailer = &object.Dictionary{}
	}
	return core.View{
		Objects: objs,
		Trailer: trailer,
		Limits:  core.DefaultLimits(),
		Run:     core.NewRun(&core.Recorder{}),
	}
}

// mkViewVersion is mkView with the header version set, for the checks that read
// it.
func mkViewVersion(objs map[int]*object.IndirectObject, trailer *object.Dictionary, version string) core.View {
	v := mkView(objs, trailer)
	v.Version = version
	return v
}

// referenced makes objects part of the document and returns the view. A rule
// judges what the document reaches (core.View.ReachableDicts), so an object
// dropped into the table with nothing pointing at it is an orphan, which is
// deliberately not judged (audit 2026-09-22 C83). The references hang from a
// trailer entry no rule reads, which keeps a fixture about the rule it tests;
// the tests of reachability itself build real structure. Call it before the
// first check: the reachable set is computed once per run.
func referenced(v core.View, nums ...int) core.View {
	arr, _ := v.Trailer.Get("PDF0TestFixture").(object.Array)
	for _, n := range nums {
		arr = append(arr, object.IndirectRef{Number: n})
	}
	v.Trailer.Set("PDF0TestFixture", arr)
	return v
}
