package pdf0

import (
	"context"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfx"
)

// The PDF/X API. The checks live in the pdfx package and read the document
// through a core.View; this is the boundary that starts the run and reports the
// guards that tripped while the file was read.

// PDFXViolation is a PDF/X conformance failure.
type PDFXViolation = pdfx.PDFXViolation

// PDFXLevel names a PDF/X conformance level.
type PDFXLevel = pdfx.PDFXLevel

// The PDF/X conformance levels.
const (
	PDFX1a = pdfx.PDFX1a
	PDFX3  = pdfx.PDFX3
	PDFX4  = pdfx.PDFX4
	PDFX4p = pdfx.PDFX4p
	PDFX6  = pdfx.PDFX6
)

// ValidatePDFX checks whether v conforms to the given PDF/X level. An empty
// result means no violations were found.
func ValidatePDFX(v *Document, level PDFXLevel) []PDFXViolation {
	return validatePDFX(core.Canceler{}, v, level)
}

// ValidatePDFXContext is ValidatePDFX with cancellation; a cancelled run reports
// itself under the rule "limit" (see cancel.go).
func ValidatePDFXContext(ctx context.Context, v *Document, level PDFXLevel) []PDFXViolation {
	return validatePDFX(core.NewCanceler(ctx), v, level)
}
func validatePDFX(cancel core.Canceler, doc *Document, level PDFXLevel) []PDFXViolation {
	// Run against a shallow copy carrying the per-run cache, as the PDF/A and
	// PDF/UA validators do: it memoizes the traversals this validator shares
	// with them, applies the same aggregate content budget, carries the
	// cancellation signal, and gives the resource guards somewhere to report a
	// trip (see limits.go).
	//
	// This is the boundary: the checks below it read a view.
	rd := beginRunCancel(doc, cancel)
	out := pdfx.ValidateView(rd.view(), level)

	// Guard trips are reported under their own rule, not as conformance
	// failures. Read-time trips live on the Document, so this is here.
	add := func(rule, msg string, obj int) {
		out = append(out, PDFXViolation{Rule: rule, Message: msg, Object: obj})
	}
	reportLimits(rd, add)
	finding.Sort(out)
	return out
}
