package pdf0

import (
	"context"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfua"
)

// The PDF/UA API. The checks live in the pdfua package and read the document
// through a core.View; the functions here are the boundary that starts the run,
// builds that view, and appends the findings that can only be made from the
// Document — the resource guards that tripped while the file was read.

// UAViolation is a PDF/UA (ISO 14289) accessibility conformance failure.
type UAViolation = pdfua.UAViolation

// ValidatePDFUA checks a document against the foundational PDF/UA-1 (ISO
// 14289-1) requirements: the document must be tagged, carry a structure tree
// and a default language, be configured to display its title, and give every
// figure alternate text. It is a partial validator — a clean result means the
// implemented checks passed, not full PDF/UA conformance.
func ValidatePDFUA(doc *Document) []UAViolation {
	return validatePDFUA(core.Canceler{}, doc, "1")
}

// ValidatePDFUAContext is ValidatePDFUA with cancellation. When ctx ends the run
// stops and returns the findings gathered so far plus one under the clause
// "limit" recording the cancellation, which IsCheckerFinding reports as a
// checker finding — so a cancelled run cannot be mistaken for a clean one. See
// cancel.go.
func ValidatePDFUAContext(ctx context.Context, doc *Document) []UAViolation {
	return validatePDFUA(core.NewCanceler(ctx), doc, "1")
}

// validatePDFUA runs the checks shared by PDF/UA-1 and PDF/UA-2, parameterized
// by the pdfuaid:part the file must declare. The two UA-1-only requirements —
// part 1 and a PDF 1.x header — are selected here by part rather than filtered
// out of the result by message text afterwards (audit C39).
func validatePDFUA(cancel core.Canceler, doc *Document, part string) []UAViolation {
	// Install a per-run cache (page tree, decoded content, font-usage map) on a
	// shallow copy so the original document is never mutated. Many checks walk
	// the same structures — core.CollectFontTextUsage alone runs in nine font
	// checks — and without the cache a large document's content was decoded and
	// tokenized dozens of times, making validation quadratic in practice.
	//
	// This is the boundary: everything below reads the document through a view.
	rd := beginRunCancel(doc, cancel)
	v := pdfua.ValidateView(rd.view(), part)

	// Resource guards that tripped during the run (or while the file was read)
	// are reported under the "limit" clause, so a caller can tell "a check could
	// not be completed" from "the file is non-conforming". Read-time trips live
	// on the Document, which is why this is here and not below.
	v = append(v, limitUAViolations(rd)...)
	finding.Sort(v)
	return v
}

// ValidatePDFUA2 checks a document against PDF/UA-2. Findings reuse the UAViolation
// type; clause identifiers follow ISO 14289-2.
func ValidatePDFUA2(d *Document) []UAViolation {
	return validatePDFUA2(core.Canceler{}, d)
}

// ValidatePDFUA2Context is ValidatePDFUA2 with cancellation; see
// ValidatePDFUAContext for how a cancelled run reports itself.
func ValidatePDFUA2Context(ctx context.Context, d *Document) []UAViolation {
	return validatePDFUA2(core.NewCanceler(ctx), d)
}
func validatePDFUA2(cancel core.Canceler, d *Document) []UAViolation {
	// The shared checks (tagging, structure tree, default language, displayed
	// title, Unicode mapping, artifacts, headings), parameterized for part 2 so
	// the identification rule requires pdfuaid:part 2 and the UA-1 header rule
	// is not run at all.
	out := validatePDFUA(cancel, d, "2")

	// PDF/UA-2 is defined against PDF 2.0.
	out = append(out, pdfua.RunCheck(func() []UAViolation {
		if maj, _, ok := core.ParsePDFVersion(d.Version); ok && maj != 2 {
			return []UAViolation{{Clause: "4", Message: fmt.Sprintf("PDF/UA-2 is defined for PDF 2.0; file declares %s", d.Version), Object: 0}}
		}
		return nil
	})...)

	// validatePDFUA sorted its own findings; re-sort now that the UA-2 rule has
	// appended to them (audit C27).
	finding.Sort(out)
	return out
}
