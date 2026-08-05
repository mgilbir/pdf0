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
// becomes checkable.
func (d *Document) Conformance() (pdfa.Level, bool) {
	id := d.existingPDFAIdentification()
	if id.part == "" {
		return 0, false
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
	if !claimed {
		if id := d.existingPDFAIdentification(); id.part != "" {
			// The document says it is a part of ISO 19005 that names no level
			// this package knows. Writing it would put a claim in the file that
			// nothing here can stand behind.
			return fmt.Errorf(
				"pdf0: the document claims PDF/A part %q, which pdf0 does not know how to check; "+
					"use Write to write it anyway", id.part)
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
	reparsed, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return fmt.Errorf("pdf0: the document could not be read back for checking: %w", err)
	}
	if violations := validatePDFABytes(cancel, reparsed, level, buf.Bytes()); len(violations) > 0 {
		return &ConformanceError{Level: level, Violations: violations}
	}
	_, err = w.Write(buf.Bytes())
	return err
}

// ConformanceError is what Save returns when a document does not meet the
// conformance it claims. It carries the violations so a caller can act on them
// rather than only print them.
type ConformanceError struct {
	Level      pdfa.Level
	Violations []pdfa.Violation
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
