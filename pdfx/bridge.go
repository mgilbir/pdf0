package pdfx

import "github.com/mgilbir/pdf0/internal/bridge"

// The entry point the root package and pdfvt call over the internal view. It
// is not exported, because no caller outside this module can name core.View;
// see internal/bridge.
func init() {
	bridge.PDFXValidateView.Install(validateView)
}
