package pdf0

import (
	"bytes"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfa"
)

// The recursive embedded-PDF/A check. It lives here rather than with the rest
// of the PDF/A rules because it reads a whole document out of a byte slice and
// validates it — it needs the reader, which the checks themselves do not.

// maxEmbeddedPDFADocs bounds how many embedded documents one top-level run
// validates, at every depth together. Following embedded files into embedded
// files multiplies the work by each level's fan-out: a small file can embed
// many PDFs, each of which decompresses to one embedding many more. The depth
// cap (pdfa.MaxEmbeddedDepth) alone does not bound that, and the per-document
// limits bound each document, not their number. Past the budget a document is
// not validated and the run says so under the "limit" rule; nothing is
// reported against the file on the strength of a check that did not run.
const maxEmbeddedPDFADocs = 64

// embeddedBudget is shared by every nested validation of one top-level run:
// the documents still to be validated, and the bytes, which are capped at the
// run's own per-stream decode limit — the most a single embedded file could
// have been decoded to — across the whole tree.
type embeddedBudget struct {
	docs  int
	bytes int64
}

func newEmbeddedBudget(lim core.Limits) *embeddedBudget {
	return &embeddedBudget{docs: maxEmbeddedPDFADocs, bytes: int64(lim.DecodedStreamBytes)}
}

// take charges one document of n bytes, reporting false, and charging
// nothing, when the budget cannot cover it.
func (b *embeddedBudget) take(n int) bool {
	if b.docs <= 0 || int64(n) > b.bytes {
		return false
	}
	b.docs--
	b.bytes -= int64(n)
	return true
}

// embeddedPDFAChecker returns the check the embedded-PDF/A rule calls for each
// embedded PDF of one run, sharing budget with every run nested below it.
//
// It reports whether the embedded bytes parse as a PDF/A document and
// validate against the level they declare (Document.Conformance, which is
// pdfa.LevelFor — the one mapping from a declaration to a level, so a 2u or 3a
// file is held to 2u or 3a rather than to a lossy b: audit 2026-09-22 C34),
// and whether that verdict is one pdf0 actually reached.
//
// The outer run's cancellation signal *and* its resolved limits are carried
// into the nested read and validation. Both for the same reason: this is a
// whole second document's worth of work on bytes the outer file supplied, so a
// caller's deadline and a caller's ceilings have to govern it exactly as they
// govern the outer document. Threading only the context would leave the one
// place a hostile file can spend an unconfigured budget.
//
// The second result is false when the verdict is not one pdf0 reached: the
// nested run produced a checker finding — "limit" or "internal"
// (IsCheckerFinding) — or it never got that far because the checker itself
// refused, or the budget was spent. Folding either into the boolean would
// report "not compliant" for a file pdf0 merely failed to finish reading,
// which is the false positive limits_report.go exists to prevent; the caller
// declines the 6.9 finding and reports the incompleteness instead.
//
// The two early exits deserve their own note. "This did not read" and "this
// declares no PDF/A level" are statements about the bytes — unless the checker
// is what refused, which happens when the shared context ended, or when a
// ceiling the caller lowered is what the embedded document ran into. Neither
// cause is recoverable from the error (the decode chain reports over-limit as
// an ordinary error, with no sentinel), so when either is possible the verdict
// is withheld rather than guessed. Under the defaults — every caller who
// configures nothing, and the whole corpus — that condition is false and both
// exits behave exactly as they always have.
func embeddedPDFAChecker(budget *embeddedBudget) pdfa.EmbeddedChecker {
	return func(cancel core.Canceler, data []byte, lim core.Limits, depth int) (compliant, complete bool) {
		if !budget.take(len(data)) {
			return false, false
		}
		// True when a failure below could be the checker's doing rather than
		// the file's, and so must not be reported as non-conformance.
		checkerMayHaveRefused := cancel.Err() != nil || lim != core.DefaultLimits()

		edoc, err := readDocument(cancel, bytes.NewReader(data), int64(len(data)), "", lim)
		if err != nil {
			return false, !checkerMayHaveRefused
		}
		if id := edoc.existingPDFAIdentification(); id.status == core.XMPLimit {
			// The embedded document's metadata is over the XMP packet limit,
			// so what level it declares is unknown to pdf0 — not "none".
			// Withheld, like every other verdict the checker could not reach.
			return false, false
		}
		elevel, ok := edoc.Conformance()
		if !ok {
			// An embedded PDF that is not PDF/A at all — or whose own metadata
			// stream the caller's lowered per-stream cap declined to decode.
			return false, !checkerMayHaveRefused
		}
		edoc.embeddedDepth = depth
		compliant, complete = true, true
		for _, e := range validatePDFABudget(cancel, edoc, elevel, budget) {
			if finding.IsCheckerFinding(e) {
				complete = false
				continue
			}
			compliant = false
		}
		return compliant, complete
	}
}
