package dpart

import "github.com/mgilbir/pdf0/internal/bridge"

// The entry points the root package and pdfvt call over the internal view.
// They are not exported, because no caller outside this module can name
// core.View; see internal/bridge.
func init() {
	bridge.DPartValidateView.Install(validateView)
	bridge.DPartValidateHierarchy.Install(validateHierarchy)
}
