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

// ValidatePDFX checks doc against the implemented rules for the given PDF/X
// level. pdfx.LevelDeclared, the zero pdfx.Level, takes the level from the
// document's own GTS_PDFXVersion identification (XMP first, then the Info
// dictionary) and validates that level exactly as if it had been named; a
// document that declares no level pdf0 checks, or whose declaration cannot be
// read, gets a "limit" finding saying so and is not validated (beside it, the
// guard that stopped the read, if one did). A level that names no PDF/X level
// is refused the same way.
//
// An empty result means none of the implemented checks fired, not a guarantee
// of full conformance: the checks are structural (identification, the output
// intent and its profile, /Trapped, page boxes, font embedding, device colour,
// forbidden features, transparency where the level predates it, encryption).
// The result may include checker findings, under the rules "limit" and
// "internal", which say pdf0 could not finish a check rather than that the
// document is wrong (IsCheckerFinding).
func ValidatePDFX(doc *Document, level pdfx.Level) []pdfx.Violation {
	return validatePDFX(core.Canceler{}, doc, level)
}

// ValidatePDFXContext is ValidatePDFX with cancellation; a cancelled run reports
// itself under the rule "limit" (see cancel.go). The level is read as
// ValidatePDFX reads it, pdfx.LevelDeclared included.
func ValidatePDFXContext(ctx context.Context, doc *Document, level pdfx.Level) []pdfx.Violation {
	return validatePDFX(core.NewCanceler(ctx), doc, level)
}
func validatePDFX(cancel core.Canceler, doc *Document, level pdfx.Level) []pdfx.Violation {
	if doc == nil {
		return []pdfx.Violation{{Rule: finding.LimitRule, Message: nilDocumentMessage}}
	}
	// Run against a shallow copy carrying the per-run cache, as the PDF/A and
	// PDF/UA validators do: it memoizes the traversals this validator shares
	// with them, applies the same aggregate content budget, carries the
	// cancellation signal, and gives the resource guards somewhere to report a
	// trip (see limits.go).
	//
	// This is the boundary: the checks below it read a view.
	rd := beginRunCancel(doc, cancel)
	// Contain is the work meter's boundary outside the checks' own.
	var out []pdfx.Violation
	core.Contain(func() { out = pdfxValidateView(rd.view(), level) })

	// Guard trips are reported under their own rule, not as conformance
	// failures. Read-time trips live on the Document, so this is here.
	add := func(rule, msg string, obj int) {
		out = append(out, pdfx.Violation{Rule: rule, Message: msg, Object: obj})
	}
	reportLimits(rd, add)
	finding.Sort(out)
	return out
}
