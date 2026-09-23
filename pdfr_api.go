package pdf0

import (
	"context"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfr"
)

// The PDF/R API. The checks live in the pdfr package and read the document
// through a core.View; this is the boundary that starts the run and reports the
// guards that tripped while the file was read.

// ValidatePDFR checks a document against the PDF/R structural profile.
func ValidatePDFR(doc *Document) []pdfr.Violation {
	return validatePDFR(core.Canceler{}, doc)
}

// ValidatePDFRContext is ValidatePDFR with cancellation; a cancelled run reports
// itself under the rule "limit" (see cancel.go).
func ValidatePDFRContext(ctx context.Context, doc *Document) []pdfr.Violation {
	return validatePDFR(core.NewCanceler(ctx), doc)
}
func validatePDFR(cancel core.Canceler, d *Document) []pdfr.Violation {
	if d == nil {
		return []pdfr.Violation{{Rule: finding.LimitRule, Message: nilDocumentMessage}}
	}
	// Run against a shallow copy carrying the per-run cache (see beginRun): it
	// memoizes the shared traversals, applies the aggregate content budget,
	// carries the cancellation signal, and gives the resource guards somewhere to
	// report a trip (limits.go).
	//
	// This is the boundary: the checks below read a view.
	rd := beginRunCancel(d, cancel)
	out := pdfrValidateView(rd.view())

	// Guard trips are reported under their own rule, not as conformance
	// failures (see limits.go). Read-time trips live on the Document, so this
	// is here.
	add := func(rule, msg string, obj int) {
		out = append(out, pdfr.Violation{Rule: rule, Message: msg, Object: obj})
	}
	reportLimits(rd, add)
	finding.Sort(out)
	return out
}
