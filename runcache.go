package pdf0

import (
	"github.com/mgilbir/pdf0/internal/core"
)

// The per-run cache and the run state it carries. These live here rather than
// with any validator: every validator installs one, and the packages below the
// root package reach the shared half through Document.view.

// validationCache memoizes traversals for one ValidatePDFABytes run. It is
// installed at the start of a run and dropped at the end, so documents may
// be mutated freely between validations. The cache lives on a shallow copy of
// the Document, never on the caller's, so validating the same Document from
// several goroutines at once is safe — TestValidateConcurrentSameDoc asserts it
// under -race. (An earlier revision installed the cache on the caller's document
// and this comment said concurrency was unsupported.)
// The memo slots are grouped by the subsystem that owns them rather than held
// in one flat struct. That is not cosmetic: of the sixteen slots this struct
// used to hold flat, fifteen were touched by exactly one subsystem, so what
// looked like shared state was really a box of private caches. Naming the owner
// makes each group travel with its subsystem when that subsystem moves to its
// own package, and makes it a compile error — rather than a silent new coupling —
// for one subsystem to start reading another's memo.
//
// Only run, below, is genuinely shared.
type validationCache struct {
	pdfa pdfaCache
	run  runState
}

// pdfaCache is the PDF/A engine's memoized traversals: the page tree, decoded
// content streams and the executed-content walk's per-stream skeletons.
type pdfaCache struct {
	directAnnots    []annotOccurrence
	hasDirectAnnots bool
}

// runState is the part that is genuinely shared, because it belongs to the run
// rather than to any one subsystem.
type runState struct {
	// limits collects the resource guards that tripped during this run, so a
	// check that declines to assert because its input was truncated still says
	// so. See limits.go; always non-nil when built by newValidationCache.
	limits *core.Recorder

	// cancel is the caller's cancellation signal for this run, or the
	// never-cancelling zero value. It lives here — on state whose lifetime is
	// exactly one run — rather than on the caller's Document, which outlives the
	// operation. See cancel.go.
	cancel core.Canceler

	// shared is the run state handed to the packages below this one, through
	// Document.view. Always build a validationCache with newValidationCache: a
	// hand-built one leaves this nil, and a nil Run makes core.Slot hand back a
	// fresh memo on every call — correct answers, no memoization, and nothing
	// says so. It is built once per run and reused, not rebuilt per view:
	// a View is copied by value and shares its Run pointer, so a fresh Run per
	// call would fork the memo tables it will come to hold. That failure is
	// invisible in the output — the answers stay right — and shows up only as
	// repeated work.
	shared *core.Run
}
