package fontcheck

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mkView builds the view these tests check against: an object graph, a
// trailer, resolved limits and a live run. A zero core.Limits is a budget of
// zero, and a nil run makes every memo a fresh one; the root package's
// Document.view supplies both, and this is the equivalent here.
func mkView(objs map[int]*object.IndirectObject) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	return core.View{
		Objects: objs,
		Trailer: &object.Dictionary{},
		Limits:  core.DefaultLimits(),
		Run:     core.NewRun(&core.Recorder{}),
	}
}
