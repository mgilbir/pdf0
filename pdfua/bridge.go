package pdfua

import "github.com/mgilbir/pdf0/internal/bridge"

// The entry point the root package calls over its internal view. It is not
// exported, because no caller outside this module can name core.View; see
// internal/bridge.
//
// runCheck and withPart name no internal type, but they are plumbing the root
// package needs for the findings it adds at the boundary, not API: they were
// exported for that alone, and are carried here instead (docs audit 2026-10-11
// D60).
func init() {
	bridge.PDFUAValidateView.Install(validateView)
	bridge.PDFUARunCheck.Install(runCheck)
	bridge.PDFUAWithPart.Install(withPart)
}
