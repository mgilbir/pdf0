package pdf0

import (
	"bytes"

	"github.com/mgilbir/pdf0/internal/finding"

	"github.com/mgilbir/pdf0/internal/core"
)

// The recursive embedded-PDF/A check. It lives here rather than with the rest
// of the PDF/A rules because it reads a whole document out of a byte slice and
// validates it — it needs the reader, which the checks themselves do not.

// embeddedPDFACompliant reports whether embedded PDF bytes parse as a PDF/A
// document and validate against their own declared conformance level, and
// whether that verdict is one pdf0 actually reached.
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
// refused. Folding either into the boolean would report "not compliant" for a
// file pdf0 merely failed to finish reading, which is the false positive
// limits_report.go exists to prevent; the caller declines the 6.9 finding and
// reports the incompleteness instead.
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
func embeddedPDFACompliant(cancel core.Canceler, data []byte, lim core.Limits) (compliant, complete bool) {
	// True when a failure below could be the checker's doing rather than the
	// file's, and so must not be reported as non-conformance.
	checkerMayHaveRefused := cancel.Err() != nil || lim != core.DefaultLimits()

	edoc, err := readDocument(cancel, bytes.NewReader(data), int64(len(data)), "", lim)
	if err != nil {
		return false, !checkerMayHaveRefused
	}
	elevel, ok := declaredPDFALevel(edoc.view())
	if !ok {
		// An embedded PDF that is not PDF/A at all — or whose own metadata
		// stream the caller's lowered per-stream cap declined to decode.
		return false, !checkerMayHaveRefused
	}
	edoc.embeddedDepth = 1
	compliant, complete = true, true
	for _, e := range validatePDFABytes(cancel, edoc, elevel, data) {
		if finding.IsCheckerFinding(e) {
			complete = false
			continue
		}
		compliant = false
	}
	return compliant, complete
}

// declaredPDFALevel reads the PDF/A conformance level a document claims via
// its XMP pdfaid:part / pdfaid:conformance identifiers.
func declaredPDFALevel(doc core.View) (PDFALevel, bool) {
	catalog := doc.Catalog()
	if catalog == nil {
		return 0, false
	}
	stream, ok := doc.Resolve(catalog.Get("Metadata")).(*Stream)
	if !ok {
		return 0, false
	}
	xmp := doc.XMPText(stream)
	part := core.ExtractXMPValue(xmp, "pdfaid:part")
	if part == "" {
		part = extractXMPAttr(xmp, "pdfaid:part")
	}
	switch part {
	case "1":
		return PDFA1b, true
	case "2":
		return PDFA2b, true
	case "3":
		return PDFA3b, true
	case "4":
		return PDFA4, true
	}
	return 0, false
}
