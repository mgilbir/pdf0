package pdf0

import (
	"context"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/pdfa"
)

// The PDF/A API. The rules live in the pdfa package and read the document
// through a core.View; this is the boundary that installs the per-run cache,
// hands in the recursive embedded-file check, and appends the guards that
// tripped while the file was read.

// ErrInvalidMetadataText is wrapped by the errors of every metadata writer —
// SetDocumentInfo, NewPDFADocumentWithInfo, pdfa.GenerateXMPMetadata, EmbedFacturX —
// when a value cannot be written as XMP text: it is not valid UTF-8, or holds a
// character XML 1.0 does not allow even as a reference (a C0 control other than
// tab, LF and CR; U+FFFE; U+FFFF). Such a value is refused rather than dropped
// or altered, since a value changed on the way in is not the value the caller
// wrote.
var ErrInvalidMetadataText = xmp.ErrInvalidText

// NewPDFADocument creates a minimal valid PDF/A document for the given level.
// The document has an empty page tree and passes ValidatePDFA.
func NewPDFADocument(level pdfa.Level) (*Document, error) {
	return NewPDFADocumentWithInfo(level, "", "")
}

// NewPDFADocumentWithInfo is NewPDFADocument with the document title and
// author embedded in the generated XMP metadata.
func NewPDFADocumentWithInfo(level pdfa.Level, title, author string) (*Document, error) {
	return NewPDFADocumentWith(pdfa.SkeletonOptions{Level: level, Title: title, Author: author})
}

// NewPDFADocumentWith creates a minimal valid PDF/A document to order.
//
// It is the constructor for a caller who wants their own output intent —
// a press profile, a house sRGB variant, or simply the exact bytes they
// audited — rather than the profile pdf0 embeds:
//
//	doc, err := pdf0.NewPDFADocumentWith(pdfa.SkeletonOptions{
//	    Level: pdfa.PDFA4,
//	    OutputIntent: pdfa.OutputIntentSpec{
//	        ICCProfile:                myProfile,
//	        OutputConditionIdentifier: "FOGRA51",
//	    },
//	})
//
// The profile is embedded as given and /N is read from its header, so nothing
// of pdf0's colour management ends up in the document. A profile that is not
// one, that disagrees with its own declared length, that is in a colour space a
// PDF/A output intent may not use, or that is ICC v4 at a level based on PDF
// 1.4, is an error rather than a document built wrongly.
func NewPDFADocumentWith(opts pdfa.SkeletonOptions) (*Document, error) {
	objs, trailer, version, err := pdfa.SkeletonWith(opts)
	if err != nil {
		return nil, fmt.Errorf("pdf0: building the PDF/A skeleton: %w", err)
	}
	return &Document{Version: version, Objects: objs, Trailer: trailer}, nil // dictcopy: the skeleton's trailer is fresh and returned by value
}

// ValidatePDFA checks doc against the implemented rules for the given PDF/A
// level and returns the violations found. An empty result means "none of the
// implemented checks fired", not a guarantee of full conformance: the validator
// covers a subset of ISO 19005 (see the package README).
//
// The byte-level file-structure rules (the header, cross-reference tables,
// object and stream syntax, stream lengths, data after %%EOF, signature
// coverage) read the file doc was read from, which the document keeps
// (Document.Source). They judge that file as Read found it, whatever has been
// done to doc since: to judge the bytes of an edited document, write it and
// read the result. A document built in memory has no file, so for it those
// rules do not run, and the result says so with a checker finding under the
// rule "limit" (IsCheckerFinding) rather than passing in silence.
//
// There is no variant that takes the file's bytes. There used to be
// (ValidatePDFABytes), and it trusted them: bytes that were not the ones doc
// was read from — a rewritten file, a truncated buffer — were measured against
// doc's offsets, producing findings about neither, or an out-of-range slice
// that collapsed every byte rule into one "internal" finding (audit 2026-09-22
// C69). The only bytes that can be right are the ones the document already
// holds.
func ValidatePDFA(doc *Document, level pdfa.Level) []pdfa.Violation {
	return validatePDFA(core.Canceler{}, doc, level)
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
func ValidatePDFAContext(ctx context.Context, doc *Document, level pdfa.Level) []pdfa.Violation {
	return validatePDFA(core.NewCanceler(ctx), doc, level)
}

func validatePDFA(cancel core.Canceler, doc *Document, level pdfa.Level) []pdfa.Violation {
	return validatePDFABudget(cancel, doc, level, nil)
}

// validatePDFABudget is validatePDFA for a run that may be nested inside
// another: the embedded-PDF/A check validates each embedded document through
// here with the top-level run's budget, so the whole tree of embedded files
// shares one (see embeddedBudget). A nil budget starts a fresh one.
func validatePDFABudget(cancel core.Canceler, doc *Document, level pdfa.Level, budget *embeddedBudget) []pdfa.Violation {
	if doc == nil {
		// A nil document is a caller mistake, and the useful answer is a finding
		// rather than a panic: this is the API a caller reaches for after a
		// failed Read, where the natural shape of the code leaves doc nil and
		// the error unchecked. Reporting it as a checker finding means the
		// result can never be mistaken for a clean bill of health.
		return []pdfa.Violation{{
			Rule:    "limit",
			Level:   level,
			Message: nilDocumentMessage,
		}}
	}
	// Validate against a shallow copy of the Document so the per-run cache is
	// installed on the copy, never on the caller's. The copy shares the
	// (read-only during validation) Objects/Trailer/Offsets, so this is cheap,
	// and it lets a caller validate one Document concurrently — across
	// goroutines and at several levels at once — without a data race.
	//
	// This is the boundary: everything below reads a view.
	//
	// A Document that already carries a run (only ever an internal one: the
	// cache lives on these shallow copies, never on a caller's Document) joins
	// it, as beginRunCancel says.
	runDoc := beginRunCancel(doc, cancel)
	v := runDoc.view()

	// The target profile. LevelDeclared becomes the level the document
	// declares, and a level that names no profile — or a declaration that
	// names none — is one checker finding and nothing else: nothing below
	// runs against a profile it cannot state.
	target, refused := pdfaResolveTarget(v, level)
	if refused != nil {
		return refused
	}

	// The recursive embedded-file check needs the parser, which the checks
	// themselves do not depend on; hand it in for this run.
	if budget == nil {
		budget = newEmbeddedBudget(runDoc.lim())
	}
	pdfaSetEmbeddedChecker(v, embeddedPDFAChecker(budget))

	// The checks each stop at their own boundary when the run's work meter
	// ends the run; Contain is the boundary for the work done between them.
	var errs []pdfa.Violation
	core.Contain(func() { errs = pdfaValidateView(v, target) })

	// Any resource guard that tripped during the run (or while the file was
	// read) is reported under the "limit" rule: the checks that depended on the
	// truncated result declined to assert, so the result is "unknown", not
	// "conformant". Read-time trips live on the Document, so this is here.
	errs = append(errs, limitPDFAViolations(runDoc, target)...)
	finding.Sort(errs)
	return errs
}
