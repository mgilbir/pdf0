package pdf0

import (
	"context"
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfr"
)

// The PDF/R API. The checks live in the pdfr package and read the document
// through a core.View; this is the boundary that starts the run and reports the
// guards that tripped while the file was read.

// PDFRViolation is a PDF/R conformance failure.
type PDFRViolation = pdfr.PDFRViolation

// ValidatePDFR checks a document against the PDF/R structural profile.
func ValidatePDFR(d *Document) []PDFRViolation {
	return validatePDFR(core.Canceler{}, d)
}

// ValidatePDFRContext is ValidatePDFR with cancellation; a cancelled run reports
// itself under the rule "limit" (see cancel.go).
func ValidatePDFRContext(ctx context.Context, d *Document) []PDFRViolation {
	return validatePDFR(core.NewCanceler(ctx), d)
}
func validatePDFR(cancel core.Canceler, d *Document) []PDFRViolation {
	// Run against a shallow copy carrying the per-run cache (see beginRun): it
	// memoizes the shared traversals, applies the aggregate content budget,
	// carries the cancellation signal, and gives the resource guards somewhere to
	// report a trip (limits.go).
	rd := beginRunCancel(d, cancel)
	v := rd.view()
	var out []PDFRViolation
	add := func(rule, msg string, obj int) {
		out = append(out, PDFRViolation{Rule: rule, Message: msg, Object: obj})
	}

	// Every check runs under a recover boundary, so a panic on hostile input
	// becomes an "internal" finding instead of crashing the caller, and one bad
	// check (or one bad page) does not discard the others' findings (audit C27).
	// It is also the coarse cancellation boundary (cancel.go).
	run := func(check func()) {
		if v.Cancel.Stopped() {
			return
		}
		finding.Guarded(add, check)
	}

	run(func() {
		if v.Encrypted || v.Trailer.Get("Encrypt") != nil {
			add("encryption", "a PDF/R file shall not be encrypted", 0)
		}
		if maj, _, ok := core.ParsePDFVersion(v.Version); ok && maj != 2 {
			add("version", fmt.Sprintf("PDF/R is defined for PDF 2.0; file declares %s", v.Version), 0)
		}
	})

	cat := v.Catalog()
	if cat == nil {
		add("structure", "document has no catalog", 0)
		reportLimits(rd, add)
		finding.Sort(out)
		return out
	}
	run(func() {
		if xmp := v.DocumentXMP(); xmp == "" {
			add("metadata", "a PDF/R file requires an XMP metadata stream", 0)
		} else if !strings.Contains(strings.ToLower(xmp), "pdf/r") && !strings.Contains(strings.ToLower(xmp), "pdfr") {
			add("identification", "the XMP metadata does not identify the file as PDF/R", 0)
		}
	})

	var pages []PageInfo
	run(func() {
		pages = v.Pages(cat.Get("Pages"))
		if len(pages) == 0 {
			add("structure", "a PDF/R file shall have at least one page", 0)
		}
	})
	for _, page := range pages {
		run(func() { pdfr.CheckPage(v, page.Dict, page.ObjNum, add) })
	}

	// Guard trips are reported under their own rule, not as conformance
	// failures (see limits.go).
	reportLimits(rd, add)

	// The checks iterate map-ordered doc.Objects, so their concatenated output
	// order is nondeterministic; sort for stable, diffable reports.
	finding.Sort(out)
	return out
}
