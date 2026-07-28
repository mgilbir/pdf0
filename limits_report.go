package pdf0

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
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

// limitRule is the rule (clause) identifier carried by every finding that
// reports a resource-guard trip. Like internalRule ("internal", used for a
// recovered panic) it names the *checker*, not the document: "6.2.11.4.1" says
// the file is wrong, "limit" says pdf0 stopped short and therefore cannot say
// whether the file is right.
//
// A cancelled run reports itself here too (limitCanceled). A caller's deadline
// is not a resource guard, but it produces exactly the same event — the checker
// stopped before it had seen everything — and so calls for exactly the same
// honesty. Giving it its own rule identifier would have meant every caller that
// already distinguishes "the file is bad" from "pdf0 could not finish" learning
// a second way to spell the second one. See cancel.go.
const limitRule = "limit"

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
func IsCheckerFinding(v Violation) bool {
	switch v.RuleID() {
	case limitRule, internalRule:
		return true
	}
	return false
}

// Guard identifiers. These are stable strings: they appear in the message of a
// "limit" finding, so a caller can key on the specific guard. They are named
// after the constant that bounds the work, not after the rule that was skipped
// — one guard can cost several rules.
// Each is annotated with the limits field that bounds it and the With* option
// that configures it, because every guard that can report a trip is also one a
// caller can move: a trip on a lowered bound is the caller's own configuration
// answering back, and the message says so (see limitBound).
const (
	limitCmapWork      = "cmap-format4-work"         // limits.cmapWork, WithMaxCmapWork — fontprog.go
	limitCIDWidthRange = "cid-width-range"           // limits.cidRangeSpan, WithMaxCIDRangeSpan — fonts.go
	limitRoleMapWork   = "rolemap-work"              // limits.roleMapSteps, WithMaxRoleMapSteps — pdfua.go
	limitGridFills     = "table-grid-fills"          // limits.tableGridFills, WithMaxTableGridFills — pdfua_tablegrid.go
	limitContentStream = "content-stream-size"       // limits.contentStreamBytes, WithMaxContentStreamBytes — pdfa.go
	limitContentTotal  = "decoded-content-total"     // limits.decodedContentBytes, WithMaxDecodedContentBytes — pdfa.go
	limitObjStmTotal   = "objstm-decompressed-total" // limits.objectStreamBytes, WithMaxObjectStreamBytes — objstm.go

	// limitCanceled is not a resource guard: it is the caller's context ending
	// the run (cancel.go). It is listed among the guards because it is reported
	// through the same recorder and under the same rule, so that a caller
	// filtering on IsCheckerFinding — or keying on the guard name in the message
	// — needs no new case for it.
	limitCanceled = "context-canceled"
)

// limitBound renders the bound a guard tripped on, saying whether it is the
// package default or a value the caller chose. "you hit the 8 MiB cap you set"
// and "you hit our 8 MiB default" call for different responses — the first is a
// configuration decision to revisit, the second a report that pdf0's own
// ceiling was too low for this file — and a message that gives only the number
// cannot tell them apart.
//
// A caller who configures a limit to exactly its default is described as having
// the default. That is the one case the comparison gets wrong, and it costs
// nothing: the advice either message leads to is the same.
func limitBound(effective, def int64) string {
	if effective == def {
		return strconv.FormatInt(effective, 10) + " (pdf0's default)"
	}
	return strconv.FormatInt(effective, 10) + " (configured by the caller)"
}

// limitTrip is one guard trip: which guard, what it left incomplete, and the
// object the incompleteness attaches to (0 when it is document-wide).
type limitTrip struct {
	guard  string
	detail string
	obj    int
}

func (t limitTrip) message() string {
	if t.guard == limitCanceled {
		// Deliberately worded so that it cannot be read as a statement about the
		// file. A cancelled run's findings are true but partial, and the absence
		// of a finding says nothing at all.
		return fmt.Sprintf("the run was cancelled before it finished (%s): %s; the checks that had not yet run were skipped, so this file is neither confirmed conformant nor non-conformant", t.guard, t.detail)
	}
	return fmt.Sprintf("resource limit reached (%s): %s; the checks that depend on it were skipped, so this file is neither confirmed conformant nor non-conformant in that respect", t.guard, t.detail)
}

// maxRecordedLimitTrips bounds the recorder itself. A file crafted to trip a
// guard once per object would otherwise turn the *report* into the resource
// exhaustion the guards exist to prevent. Distinct trips beyond the cap are
// counted, not stored, and reported in aggregate.
const maxRecordedLimitTrips = 64

// limitRecorder collects the guard trips of one run. The zero value is usable
// and a nil *limitRecorder discards, so a guard can report unconditionally
// without knowing whether anything is listening.
//
// The mutex is not there because validation is concurrent — a run is
// single-goroutine, and each run gets its own recorder on its own shallow copy
// of the Document (see validationCache) — but because the recorder is the one
// piece of per-run state that guards write to from arbitrary depth, and a
// future parallel check must not turn that into a data race.
type limitRecorder struct {
	mu      sync.Mutex
	seen    map[limitTrip]bool
	trips   []limitTrip
	dropped int
}

// note records a trip, ignoring repeats of one already recorded. guard is one
// of the limit* identifiers above; detail says, in the file's terms, what was
// left incomplete.
func (r *limitRecorder) note(guard, detail string, obj int) {
	if r == nil {
		return
	}
	t := limitTrip{guard: guard, detail: detail, obj: obj}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[t] {
		return
	}
	if len(r.trips) >= maxRecordedLimitTrips {
		r.dropped++
		return
	}
	if r.seen == nil {
		r.seen = make(map[limitTrip]bool)
	}
	r.seen[t] = true
	r.trips = append(r.trips, t)
}

// snapshot returns the recorded trips in a deterministic order, plus a synthetic
// trip standing for any that were dropped by maxRecordedLimitTrips.
func (r *limitRecorder) snapshot() []limitTrip {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]limitTrip, len(r.trips))
	copy(out, r.trips)
	if r.dropped > 0 {
		out = append(out, limitTrip{
			guard:  "limit-report",
			detail: fmt.Sprintf("%d further distinct guard trips were not reported individually", r.dropped),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].guard != out[j].guard {
			return out[i].guard < out[j].guard
		}
		if out[i].obj != out[j].obj {
			return out[i].obj < out[j].obj
		}
		return out[i].detail < out[j].detail
	})
	return out
}

// noteReadLimit records a guard trip that happened while the file was being
// read, when there is no validation run to attach it to. Every validator merges
// these into its report (runLimitTrips).
func (d *Document) noteReadLimit(guard, detail string, obj int) {
	if d == nil {
		return
	}
	if d.readLimits == nil {
		d.readLimits = &limitRecorder{}
	}
	d.readLimits.note(guard, detail, obj)
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
	doc.valCache.limits.note(guard, detail, obj)
}

// runLimitTrips returns every trip that belongs in this run's report: those the
// run itself recorded, plus those recorded while the file was read.
//
// Read-time trips live on the Document because there is no run to attach them
// to yet; validation only ever reads them, so a run stays non-mutating for the
// caller (a run writes solely to its own per-run recorder).
func runLimitTrips(doc *Document) []limitTrip {
	if doc == nil {
		return nil
	}
	var out []limitTrip
	if doc.readLimits != nil {
		out = append(out, doc.readLimits.snapshot()...)
	}
	if doc.valCache != nil {
		out = append(out, doc.valCache.limits.snapshot()...)
	}
	// A cancelled run is the same event as a tripped guard and is reported the
	// same way (cancel.go). It is derived here rather than recorded by whichever
	// loop noticed first, because the context is authoritative and every
	// validator already funnels its report through this one function: one line
	// here gives all seven of them the finding, and none of them can forget it.
	if err := doc.canceler().err(); err != nil {
		out = append(out, limitTrip{guard: limitCanceled, detail: err.Error()})
	}
	return out
}

// limitValidationErrors renders this run's trips as PDF/A findings.
func limitValidationErrors(doc *Document, level PDFALevel) []ValidationError {
	var out []ValidationError
	for _, t := range runLimitTrips(doc) {
		out = append(out, ValidationError{Rule: limitRule, Level: level, Message: t.message(), Object: t.obj})
	}
	return out
}

// limitUAViolations renders this run's trips as PDF/UA findings.
func limitUAViolations(doc *Document) []UAViolation {
	var out []UAViolation
	for _, t := range runLimitTrips(doc) {
		out = append(out, UAViolation{limitRule, t.message(), t.obj})
	}
	return out
}

// reportLimits renders this run's trips through the add(rule, msg, obj)
// callback the PDF/X, PDF/VT, PDF/R and DPart validators report through.
func reportLimits(doc *Document, add func(rule, msg string, obj int)) {
	for _, t := range runLimitTrips(doc) {
		add(limitRule, t.message(), t.obj)
	}
}

// newValidationCache builds the per-run cache, including the limit recorder
// every guard reports through and the cancellation signal every loop consults.
// The three (now seven) call sites that start a run share it so a new per-run
// field cannot be initialized in one and forgotten in another.
func newValidationCache(cancel canceler) *validationCache {
	return &validationCache{
		pages:   make(map[int][]pageInfo),
		content: make(map[*Stream][]byte),
		limits:  &limitRecorder{},
		cancel:  cancel,
	}
}

// beginRun starts a run that cannot be cancelled. It is what the non-Context
// entry points call, so they behave exactly as they did before contexts
// existed.
func beginRun(doc *Document) *Document { return beginRunCancel(doc, canceler{}) }

// beginRunContext starts a run governed by ctx. Every Context entry point goes
// through here.
func beginRunContext(ctx context.Context, doc *Document) *Document {
	return beginRunCancel(doc, newCanceler(ctx))
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
func beginRunCancel(doc *Document, cancel canceler) *Document {
	if doc == nil || doc.valCache != nil {
		return doc
	}
	runDoc := *doc
	runDoc.valCache = newValidationCache(cancel)
	return &runDoc
}
