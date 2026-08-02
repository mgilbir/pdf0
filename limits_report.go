package pdf0

import (
	"context"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
)

// Reporting a resource guard that stopped short.
//
// limits.go says what a limit *is* — the defaults, the With* options, the
// resolved limits struct. This file says what happens when one *trips*.
//
// This file gives every resource guard in the package one way to say "I
// stopped short". The package processes untrusted files, so ~70 guards cap the
// work a hostile document can force: work budgets, depth caps, size ceilings,
// hop counters. A guard that trips leaves the checker with an *incomplete*
// picture, and there are only three honest things to do with that:
//
//   - fail loudly (a returned error) — always safe;
//   - decline to assert, and say so — what this file is for;
//   - assert anyway — never safe, and the source of the worst class of bug in
//     a validator: a false positive, where the library accuses a conformant
//     file on the strength of a truncated intermediate result. Audit C46 (a
//     dropped cmap segment made every affected code resolve to "glyph 0", so a
//     good font was reported as referencing .notdef) and the empty-cmap defect
//     found by fuzzing are both exactly this shape.
//
// The rule the codebase now follows is: **a check must never assert a
// violation on the basis of an incomplete result.** When a guard truncates a
// structure, the structure is made self-describing (partial), the consumer
// declines the dependent check, and the trip itself is reported here — under
// its own rule identifier, so a caller can tell "pdf0 could not finish" apart
// from "this file is non-conforming".
//
// docs/limits.md carries the full per-guard classification.

// IsCheckerFinding reports whether a finding describes a problem in the checker
// rather than a non-conformance of the document. Two rule identifiers are
// reserved for this: "internal" (a check panicked and was recovered) and
// "limit" (a resource guard tripped, so a check could not be completed). A
// caller that wants "is this file conformant?" should treat a checker finding
// as "unknown", not as a failure:
//
//	var real []pdf0.Violation
//	for _, e := range pdf0.ValidatePDFA(doc, pdf0.PDFA2b) {
//		if !pdf0.IsCheckerFinding(e) {
//			real = append(real, e)
//		}
//	}
//
// Neither kind fires on any file in the veraPDF corpus; both mean the input is
// adversarial or the checker has a bug.
func IsCheckerFinding(v Violation) bool { return finding.IsCheckerFinding(v) }

// Guard identifiers. These are stable strings: they appear in the message of a
// "limit" finding, so a caller can key on the specific guard. They are named
// after the constant that bounds the work, not after the rule that was skipped
// — one guard can cost several rules.
// Each is annotated with the limits field that bounds it and the With* option
// that configures it, because every guard that can report a trip is also one a
// caller can move: a trip on a lowered bound is the caller's own configuration
// answering back, and the message says so (see limitBound).
const (
	limitCmapWork      = "cmap-work"                 // limits.cmapWork, WithMaxCmapWork — fontprog.go
	limitCIDWidthRange = "cid-width-range"           // limits.cidRangeSpan, WithMaxCIDRangeSpan — fonts.go
	limitContentStream = "content-stream-size"       // limits.contentStreamBytes, WithMaxContentStreamBytes — pdfa.go
	limitContentTotal  = "decoded-content-total"     // limits.decodedContentBytes, WithMaxDecodedContentBytes — pdfa.go
	limitObjStmTotal   = "objstm-decompressed-total" // limits.objectStreamBytes, WithMaxObjectStreamBytes — objstm.go
	limitEmbeddedPDFA  = "embedded-pdfa"             // no bound of its own — final_rules.go, see checkEmbeddedPDFA
)

// noteReadLimit records a guard trip that happened while the file was being
// read, when there is no validation run to attach it to. Every validator merges
// these into its report (runLimitTrips).
func (d *Document) noteReadLimit(guard, detail string, obj int) {
	if d == nil {
		return
	}
	if d.readLimits == nil {
		d.readLimits = &core.Recorder{}
	}
	d.readLimits.Note(guard, detail, obj)
}

// noteLimit reports a guard trip against the run doc belongs to. It is a no-op
// when no run is in progress (no per-run cache installed), which is why a guard
// deep in unexported code can call it unconditionally.
//
// Guards with no *Document in scope at all — the sfnt/CFF parsers, the lexer —
// cannot use this. Those record the trip on the value they return instead (see
// fontProgram.cmapPartial), and whoever loads that value, which does have a
// Document, forwards it here (noteFontProgramLimits).
func noteLimit(doc *Document, guard, detail string, obj int) {
	if doc == nil || doc.valCache == nil {
		return
	}
	doc.valCache.run.limits.Note(guard, detail, obj)
}

// runLimitTrips returns every trip that belongs in this run's report: those the
// run itself recorded, plus those recorded while the file was read.
//
// Read-time trips live on the Document because there is no run to attach them
// to yet; validation only ever reads them, so a run stays non-mutating for the
// caller (a run writes solely to its own per-run recorder).
func runLimitTrips(doc *Document) []core.Trip {
	if doc == nil {
		return nil
	}
	var out []core.Trip
	if doc.readLimits != nil {
		out = append(out, doc.readLimits.Snapshot()...)
	}
	if doc == nil || doc.valCache != nil {
		out = append(out, doc.valCache.run.limits.Snapshot()...)
	}
	// A cancelled run is the same event as a tripped guard and is reported the
	// same way (cancel.go). It is derived here rather than recorded by whichever
	// loop noticed first, because the context is authoritative and every
	// validator already funnels its report through this one function: one line
	// here gives all nine of them the finding, and none of them can forget it.
	if err := doc.canceler().Err(); err != nil {
		out = append(out, core.NewTrip(core.GuardCanceled, err.Error(), 0))
	}
	return out
}

// limitValidationErrors renders this run's trips as PDF/A findings.
func limitValidationErrors(doc *Document, level PDFALevel) []ValidationError {
	var out []ValidationError
	for _, t := range runLimitTrips(doc) {
		out = append(out, ValidationError{Rule: finding.LimitRule, Level: level, Message: t.Message(), Object: t.Obj})
	}
	return out
}

// limitUAViolations renders this run's trips as PDF/UA findings.
func limitUAViolations(doc *Document) []UAViolation {
	var out []UAViolation
	for _, t := range runLimitTrips(doc) {
		out = append(out, UAViolation{Clause: finding.LimitRule, Message: t.Message(), Object: t.Obj})
	}
	return out
}

// reportLimits renders this run's trips through the add(rule, msg, obj)
// callback the PDF/X, PDF/VT, PDF/R and DPart validators report through.
func reportLimits(doc *Document, add func(rule, msg string, obj int)) {
	for _, t := range runLimitTrips(doc) {
		add(finding.LimitRule, t.Message(), t.Obj)
	}
}

// newValidationCache builds the per-run cache, including the limit recorder
// every guard reports through and the cancellation signal every loop consults.
// The three (now seven) call sites that start a run share it so a new per-run
// field cannot be initialized in one and forgotten in another.
func newValidationCache(cancel core.Canceler) *validationCache {
	rec := &core.Recorder{}
	return &validationCache{
		run: runState{
			limits: rec,
			cancel: cancel,
			shared: core.NewRun(rec),
		},
	}
}

// beginRun starts a run that cannot be cancelled. It is what the non-Context
// entry points call, so they behave exactly as they did before contexts
// existed.
func beginRun(doc *Document) *Document { return beginRunCancel(doc, core.Canceler{}) }

// beginRunContext starts a run governed by ctx. Every Context entry point goes
// through here.
func beginRunContext(ctx context.Context, doc *Document) *Document {
	return beginRunCancel(doc, core.NewCanceler(ctx))
}

// beginRunCancel returns the Document a validation run should work against: a
// shallow copy carrying a fresh per-run cache, so the caller's Document is never
// mutated and the same Document can be validated from several goroutines at
// once. An already-installed cache is kept, so a nested check joins the run in
// progress rather than starting a second one — and inherits its cancellation
// signal along with its memoized traversals, which is why a cancelled outer run
// also stops the embedded-PDF/A validation it started (checkEmbeddedPDFA).
//
// The cache is the only place a context is held, and its lifetime is exactly
// the run's: the shallow copy is discarded when the validator returns, so the
// caller's Document never ends up owning one (see cancel.go).
func beginRunCancel(doc *Document, cancel core.Canceler) *Document {
	if doc.valCache != nil {
		return doc
	}
	runDoc := *doc
	runDoc.valCache = newValidationCache(cancel)
	return &runDoc
}
