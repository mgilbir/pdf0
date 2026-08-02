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
func mkView(objs map[int]*object.IndirectObject, trailer object.Dictionary) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	tr := trailer
	return core.View{
		Objects: objs,
		Trailer: &tr,
		Limits:  core.DefaultLimits(),
		Run:     core.NewRun(&core.Recorder{}),
	}
}

// mkViewVersion is mkView with the header version set, for the checks that read
// it.
func mkViewVersion(objs map[int]*object.IndirectObject, trailer object.Dictionary, version string) core.View {
	v := mkView(objs, trailer)
	v.Version = version
	return v
}
