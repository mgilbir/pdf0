package sign

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// mkV completes a partially built view the way Document.view does: a non-nil
// object map, a trailer to resolve against, the real limits and a shared run.
// A zero core.Limits is a budget of zero, so a view built without them decodes
// nothing while reporting no error.
func mkV(v core.View) core.View {
	if v.Objects == nil {
		v.Objects = map[int]*object.IndirectObject{}
	}
	if v.Trailer == nil {
		v.Trailer = &object.Dictionary{}
	}
	if v.Limits == (core.Limits{}) {
		v.Limits = core.DefaultLimits()
	}
	if v.Run == nil {
		v.Run = core.NewRun(&core.Recorder{})
	}
	return v
}
