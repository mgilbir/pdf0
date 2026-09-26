package pdf0

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/pdfa"
)

// Saving a document against the conformance it claims.
//
// Write serialises what is in the model, exactly. That is the right thing for a
// document that was *read*: a file that does not conform is still a file, and
// reading it, changing one thing and writing it back should not fail because of
// a fault that was already there.
//
// It is the wrong thing for a document that was *authored*. A caller who asked
// for PDF/A-2b and then drew something that level forbids got a file that says
// PDF/A-2b in its metadata and is not — and nothing anywhere said so. The claim
// and the content had drifted apart with no step in between to notice.
//
// Save is that step. It is the verb for a document you built.
//
// # Where the level comes from
//
// Not from an argument. The document already carries its claim, in the pdfaid
// identification of its own XMP, and that is what is checked — so the thing
// enforced and the thing a reader will believe are the same thing by
// construction. A caller cannot pass the wrong level, because a caller does not
// pass one.

// Conformance reports the PDF/A level the document's metadata claims, and
// whether it claims one at all.
//
// A document from NewDocument claims nothing and reports false. One from
// NewPDFADocument claims the level it was made at. One that was read claims
// whatever its metadata says, which is how a file's own assertion about itself
// becomes checkable: the part and conformance are mapped by pdfa.LevelFor, so a
// file declaring 2u claims PDFA2u. A declaration that names no level — an
// unknown part, a part-4 "B", no letter at parts 1-3 — reports false.
func (d *Document) Conformance() (pdfa.Level, bool) {
	id := d.existingPDFAIdentification()
	if id.part == "" {
		return pdfa.LevelDeclared, false
	}
	return pdfa.LevelFor(id.part, id.conformance)
}

// maxReportedViolations bounds how many are named in a Save error. A document
// with a systematic fault has one per page, and an error message thousands of
// lines long is not more useful than one that says so.
const maxReportedViolations = 10

// Save writes the document, refusing to write one that fails the conformance it
// claims.
//
// A document claiming no conformance is written as Write would write it. One
// claiming a PDF/A level is serialised, read back and checked against that
// level — including the byte-level rules, which is why it is checked as bytes
// rather than as a model — and written only if it passes. Nothing reaches w
// unless the whole file passed, so a failed Save leaves no partial output.
//
// What it promises is bounded by what this package checks: an empty violation
// list means no implemented rule fired, not that ISO 19005 has been satisfied
// in full (see ValidatePDFA). What it rules out is the case that used to be
// silent — a file whose metadata claims a level its content contradicts.
//
// Write remains available for a caller who means to write exactly what is in
// the model, and is what a read-modify-write of someone else's file should use.
func (d *Document) Save(w io.Writer) error {
	return d.save(core.Canceler{}, w)
}

// SaveContext is Save with cancellation. Both the write and the validation
// respect it; see ValidatePDFAContext for how a cancelled validation reports
// itself, and note that a cancelled Save writes nothing at all.
func (d *Document) SaveContext(ctx context.Context, w io.Writer) error {
	return d.save(core.NewCanceler(ctx), w)
}

func (d *Document) save(cancel core.Canceler, w io.Writer) error {
	level, claimed := d.Conformance()
	if id := d.existingPDFAIdentification(); id.status == core.XMPMalformed || id.status == core.XMPLimit {
		// Whatever the metadata claims, pdf0 cannot read it, so it cannot
		// check the document against it — and writing it unchecked would be
		// Save quietly becoming Write.
		return fmt.Errorf("pdf0: the document's XMP metadata cannot be read (not well-formed, or over the XMP packet limit), so the conformance it claims cannot be checked; use Write to write it anyway")
	}
	if !claimed {
		if id := d.existingPDFAIdentification(); id.part != "" {
			// The document says it is PDF/A, but the part and conformance it
			// declares name no level this package knows. Writing it would put a
			// claim in the file that nothing here can stand behind.
			return fmt.Errorf(
				"pdf0: the document claims PDF/A part %q with conformance %q, which names no level pdf0 can check; "+
					"use Write to write it anyway", id.part, id.conformance)
		}
		return d.write(cancel, w)
	}

	// Serialised to memory rather than straight to w: the byte-level rules need
	// the bytes, and a file that turns out not to conform must not have reached
	// the caller's writer. The cost is the document's size in memory, which is
	// the price of not emitting a file that lies about itself.
	var buf bytes.Buffer
	if err := d.write(cancel, &buf); err != nil {
		return err
	}
	// Read back under the same context and the document's own limits: a caller
	// who raised a limit to build a large document must not have the check
	// refuse it under the defaults, and a deadline must reach the read too.
	reparsed, err := readDocument(cancel, bytes.NewReader(buf.Bytes()), int64(buf.Len()), "", d.lim())
	if err != nil {
		return fmt.Errorf("pdf0: the document could not be read back for checking: %w", err)
	}
	if err := saveVerdict(cancel, level, validatePDFABytes(cancel, reparsed, level, buf.Bytes())); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// saveVerdict turns the check of a document claiming level into Save's answer:
// nil to write it, or the error that says why not.
//
// A cancelled Save is the caller's own context answering: it returns that
// context's error, which errors.Is recognises. It used to come back as a
// ConformanceError saying the document did not meet its claim, which nothing
// had shown (audit 2026-09-22 C49). Of the rest, only a finding about the
// document is non-conformance. A checker finding — a resource limit that
// stopped a check, a check that failed internally — means the document was
// not fully checked, which is a different answer and a different error
// (IncompleteCheckError).
func saveVerdict(cancel core.Canceler, level pdfa.Level, violations []pdfa.Violation) error {
	if err := cancel.StopErr("pdf0: saving"); err != nil {
		return err
	}
	var real, unchecked []pdfa.Violation
	for _, v := range violations {
		if IsCheckerFinding(v) {
			unchecked = append(unchecked, v)
		} else {
			real = append(real, v)
		}
	}
	switch {
	case len(real) > 0:
		return &ConformanceError{Level: level, Violations: real, Unchecked: unchecked}
	case len(unchecked) > 0:
		return &IncompleteCheckError{Level: level, Findings: unchecked}
	}
	return nil
}

// ConformanceError is what Save returns when a document does not meet the
// conformance it claims. It carries the violations so a caller can act on them
// rather than only print them. Violations holds only findings about the
// document; checker findings from the same check (IsCheckerFinding) are in
// Unchecked.
type ConformanceError struct {
	Level      pdfa.Level
	Violations []pdfa.Violation
	Unchecked  []pdfa.Violation
}

// IncompleteCheckError is what Save returns when the check found nothing wrong
// with the document but could not finish: a resource limit stopped a check, or
// a check failed internally. Nothing was written, because Save writes only a
// document it has verified; the document is not known to be non-conformant
// either. Findings are the checker findings (IsCheckerFinding), which say what
// was not checked. A caller who accepts that can raise the limit on the
// Document, or write it with Write.
type IncompleteCheckError struct {
	Level    pdfa.Level
	Findings []pdfa.Violation
}

func (e *IncompleteCheckError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pdf0: the document claims %s and pdf0 could not finish checking it (%d checker findings); nothing was written, and the document is not known to be non-conformant", e.Level, len(e.Findings))
	shown := e.Findings
	if len(shown) > maxReportedViolations {
		shown = shown[:maxReportedViolations]
	}
	for _, v := range shown {
		fmt.Fprintf(&b, "\n  %s", v)
	}
	if len(e.Findings) > len(shown) {
		fmt.Fprintf(&b, "\n  … and %d more", len(e.Findings)-len(shown))
	}
	return b.String()
}

func (e *ConformanceError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pdf0: the document claims %s and does not meet it (%d problems)",
		e.Level, len(e.Violations))
	shown := e.Violations
	if len(shown) > maxReportedViolations {
		shown = shown[:maxReportedViolations]
	}
	for _, v := range shown {
		fmt.Fprintf(&b, "\n  %s", v)
	}
	if len(e.Violations) > len(shown) {
		fmt.Fprintf(&b, "\n  … and %d more", len(e.Violations)-len(shown))
	}
	return b.String()
}
