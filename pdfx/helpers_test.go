package pdfx

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mkView builds the view these tests check against, with resolved limits and a
// live run — a zero core.Limits is a budget of zero, and a nil run makes every
// memo a fresh one.
func mkView(objs map[int]*object.IndirectObject, trailer object.Dictionary) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	tr := trailer
	return core.View{Objects: objs, Trailer: &tr, Limits: core.DefaultLimits(), Run: core.NewRun(&core.Recorder{})}
}
