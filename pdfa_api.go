package pdf0

import (
	"context"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfa"
)

// The PDF/A API. The rules live in the pdfa package and read the document
// through a core.View; this is the boundary that installs the per-run cache,
// hands in the recursive embedded-file check, and appends the guards that
// tripped while the file was read.

// GenerateXMPMetadata builds the pdfaid identification XMP packet for a level.
func GenerateXMPMetadata(level pdfa.Level, title, author string) []byte {
	return pdfa.GenerateXMPMetadata(level, title, author)
}

// DefaultSRGBProfile returns the sRGB ICC profile embedded in generated
// PDF/A documents.
func DefaultSRGBProfile() []byte { return pdfa.DefaultSRGBProfile() }

// NewPDFADocument creates a minimal valid PDF/A document for the given level.
// The document has an empty page tree and passes ValidatePDFA.
func NewPDFADocument(level pdfa.Level) *Document {
	return NewPDFADocumentWithInfo(level, "", "")
}

// NewPDFADocumentWithInfo is NewPDFADocument with the document title and
// author embedded in the generated XMP metadata.
func NewPDFADocumentWithInfo(level pdfa.Level, title, author string) *Document {
	objs, trailer, version := pdfa.Skeleton(level, title, author)
	return &Document{Version: version, Objects: objs, Trailer: trailer}
}

// ValidatePDFA checks doc against the implemented rules for the given PDF/A
// level and returns the violations found. An empty result means "none of the
// implemented checks fired", not a guarantee of full conformance: the validator
// covers a subset of ISO 19005 (see the package README). Because it takes no
// raw bytes, it also skips every byte-level file-structure rule — use
// ValidatePDFABytes when you have the file bytes and want those too.
func ValidatePDFA(doc *Document, level pdfa.Level) []pdfa.ValidationError {
	return ValidatePDFABytes(doc, level, nil)
}

// ValidatePDFAContext is ValidatePDFA with cancellation. Validating a large
// document is the package's longest-running operation, so this is the variant a
// caller under a deadline should use.
//
// When ctx ends the run stops and returns the findings gathered so far plus one
// under the rule "limit" recording the cancellation, which IsCheckerFinding
// reports as a checker finding. A cancelled run therefore never looks like a
// clean bill of health: an empty result is impossible, and the caller can tell
// "no violations found" apart from "pdf0 did not get to look". See cancel.go.
func ValidatePDFAContext(ctx context.Context, doc *Document, level pdfa.Level) []pdfa.ValidationError {
	return ValidatePDFABytesContext(ctx, doc, level, nil)
}

// ValidatePDFABytes checks doc against the implemented rules for the given
// PDF/A level and returns the violations found. If rawData is non-nil, the
// byte-level file-structure rules run too (e.g. no data after %%EOF). An empty
// result means no implemented check fired, not a guarantee of full conformance
// (the validator covers a subset of ISO 19005).
func ValidatePDFABytes(doc *Document, level pdfa.Level, rawData []byte) []pdfa.ValidationError {
	return validatePDFABytes(core.Canceler{}, doc, level, rawData)
}

// ValidatePDFABytesContext is ValidatePDFABytes with cancellation; see
// ValidatePDFAContext for how a cancelled run reports itself.
func ValidatePDFABytesContext(ctx context.Context, doc *Document, level pdfa.Level, rawData []byte) []pdfa.ValidationError {
	return validatePDFABytes(core.NewCanceler(ctx), doc, level, rawData)
}
func validatePDFABytes(cancel core.Canceler, doc *Document, level pdfa.Level, rawData []byte) []pdfa.ValidationError {
	// Validate against a shallow copy of the Document so the per-run cache is
	// installed on the copy, never on the caller's. The copy shares the
	// (read-only during validation) Objects/Trailer/Offsets, so this is cheap,
	// and it lets a caller validate one Document concurrently — across
	// goroutines and at several levels at once — without a data race.
	//
	// This is the boundary: everything below reads a view.
	runDoc := *doc
	runDoc.valCache = newValidationCache(cancel)
	v := runDoc.view()

	// The recursive embedded-file check needs the parser, which the checks
	// themselves do not depend on; hand it in for this run.
	pdfa.SetEmbeddedChecker(v, embeddedPDFACompliant)

	errs := pdfa.ValidateView(v, level, rawData)

	// Any resource guard that tripped during the run (or while the file was
	// read) is reported under the "limit" rule: the checks that depended on the
	// truncated result declined to assert, so the result is "unknown", not
	// "conformant". Read-time trips live on the Document, so this is here.
	errs = append(errs, limitValidationErrors(&runDoc, level)...)
	finding.Sort(errs)
	return errs
}
