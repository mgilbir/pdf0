package facturx

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/pdfa"
)

// PDFAChecker validates the container as PDF/A-3 and returns its findings. A
// Factur-X or Order-X file shall be PDF/A-3, so both validators here compose
// that verdict into their own report — but reaching it needs the reader (the
// recursive embedded-file rule) and the read-time limit report, neither of
// which this package depends on. The caller hands one in per run.
type PDFAChecker func(doc core.View) []pdfa.ValidationError

type pdfaSlot struct{}

type pdfaHolder struct{ check PDFAChecker }

// SetPDFAChecker installs the PDF/A-3 validation for this run. It is per run
// rather than a package-level variable so that nothing is shared between
// concurrent validations.
func SetPDFAChecker(v core.View, f PDFAChecker) {
	core.Slot[pdfaHolder](v.Run, pdfaSlot{}).check = f
}

// pdfaFindings runs the run's checker. With none installed it reports nothing,
// which leaves the container findings this package makes itself — the safe
// direction: a missing base validation must not invent PDF/A non-conformances.
func pdfaFindings(v core.View) []pdfa.ValidationError {
	if h := core.Slot[pdfaHolder](v.Run, pdfaSlot{}); h.check != nil {
		return h.check(v)
	}
	return nil
}
