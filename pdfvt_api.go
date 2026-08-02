package pdf0

import (
	"context"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfvt"
)

// The PDF/VT API. The checks live in the pdfvt package and read the document
// through a core.View; this is the boundary that starts the run and reports the
// guards that tripped while the file was read.

// PDFVTViolation is a PDF/VT conformance failure.
type PDFVTViolation = pdfvt.PDFVTViolation

// ValidatePDFVT checks whether doc conforms to PDF/VT-1 (ISO 16612-2). It
// requires conformance to the PDF/X-4 base profile, a valid document part
// hierarchy, and PDF/VT-1 identification in XMP. An empty result means no
// violations were found.
func ValidatePDFVT(doc *Document) []PDFVTViolation {
	return validatePDFVTImpl(core.Canceler{}, doc, "PDF/VT-1", false)
}

// ValidatePDFVTContext is ValidatePDFVT with cancellation; a cancelled run
// reports itself under the rule "limit" (see cancel.go).
func ValidatePDFVTContext(ctx context.Context, doc *Document) []PDFVTViolation {
	return validatePDFVTImpl(core.NewCanceler(ctx), doc, "PDF/VT-1", false)
}

// ValidatePDFVT2 checks whether doc conforms to PDF/VT-2 (ISO 16612-2). PDF/VT-2
// is based on PDF/X-5 rather than PDF/X-4, so it additionally permits externally
// referenced content (reference XObjects); it is otherwise validated like
// PDF/VT-1. pdf0 has no PDF/X-5 validator, so the PDF/X-4 base is used with the
// reference-XObject prohibition relaxed — the PDF/X-5-specific external-reference
// rules are not asserted.
func ValidatePDFVT2(doc *Document) []PDFVTViolation {
	return validatePDFVTImpl(core.Canceler{}, doc, "PDF/VT-2", true)
}

// ValidatePDFVT2Context is ValidatePDFVT2 with cancellation; a cancelled run
// reports itself under the rule "limit" (see cancel.go).
func ValidatePDFVT2Context(ctx context.Context, doc *Document) []PDFVTViolation {
	return validatePDFVTImpl(core.NewCanceler(ctx), doc, "PDF/VT-2", true)
}

func validatePDFVTImpl(cancel core.Canceler, doc *Document, versionPrefix string, allowRefXObjects bool) []PDFVTViolation {
	// This is the boundary: the checks below read a view.
	rd := beginRunCancel(doc, cancel)
	out := pdfvt.ValidateView(rd.view(), versionPrefix, allowRefXObjects)

	// Guard trips are reported under their own rule; read-time trips live on
	// the Document, so this is here.
	add := func(rule, msg string, obj int) {
		out = append(out, PDFVTViolation{Rule: rule, Message: msg, Object: obj})
	}
	reportLimits(rd, add)
	finding.Sort(out)
	return out
}
