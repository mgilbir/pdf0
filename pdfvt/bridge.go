package pdfvt

import (
	"github.com/mgilbir/pdf0/dpart"
	"github.com/mgilbir/pdf0/internal/bridge"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/pdfx"
)

// The entry point the root package calls over its internal view. It is not
// exported, because no caller outside this module can name core.View; see
// internal/bridge.
func init() {
	bridge.PDFVTValidateView.Install(validateView)
}

// The PDF/X and DPart passes PDF/VT is built on, which those packages keep
// unexported for the same reason. They are resolved when this package is
// initialised, after pdfx and dpart have installed them.
var (
	pdfxValidateView  = bridge.Resolve[func(core.View, pdfx.Level) []pdfx.Violation](bridge.PDFXValidateView)
	dpartValidateView = bridge.Resolve[func(core.View) []dpart.Violation](bridge.DPartValidateView)
)
