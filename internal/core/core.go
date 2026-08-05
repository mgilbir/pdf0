// Package core holds the run-scoped machinery every other package needs and
// none of them should own: the resolved resource limits, the cancellation
// signal, and the recorder a tripped guard reports through.
//
// It is deliberately the *mechanism* only. What happens to a trip afterwards —
// turning it into a pdfa.Violation or a pdfua.Violation, deciding which rule
// identifier it carries, filtering it with IsCheckerFinding — stays in the root
// package, because that is where findings are defined. Splitting the two is what
// lets a subsystem enforce a budget without depending on the validator that
// reports it.
package core

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
)

// Default values for every configurable limit. These are the values in force
// when a caller passes no options.
// Default values for every configurable limit. These are the values in force
// when a caller passes no options.
const (
	DefaultMaxDecodedStreamBytes  = 100 << 20 // 100 MB
	DefaultMaxDecodedContentBytes = 512 << 20 // 512 MB
	DefaultMaxObjectStreamBytes   = 512 << 20 // 512 MB
	DefaultMaxContentStreamBytes  = 64 << 20  // 64 MB
	DefaultMaxICCProfileBytes     = 8 << 20   // 8 MiB
	DefaultMaxXMPPacketBytes      = 4 << 20   // 4 MiB
	DefaultMaxCIDRangeSpan        = 65536
	DefaultMaxRoleMapSteps        = 1 << 20
	DefaultMaxTableGridFills      = 1 << 24
	DefaultMaxPostScriptSteps     = 1 << 20
	DefaultMaxCmapWork            = 1 << 18
)

// Guard identifiers this package emits itself. The rest live with the guards
// that raise them, in the root package.
const (
	// GuardCanceled is not a resource guard: it is the caller's context ending
	// the run (cancel.go). It is listed among the guards because it is reported
	// through the same recorder and under the same rule, so that a caller
	// filtering on IsCheckerFinding — or keying on the guard name in the message
	// — needs no new case for it.
	GuardCanceled = "context-canceled"

	// GuardReportOverflow is the recorder speaking about itself: the synthetic
	// trip snapshot emits when maxRecordedLimitTrips has dropped distinct trips.
	// It is a guard identifier like the rest so that a caller keying on the name
	// sees the report's own truncation in the same shape as the ones it reports.
	GuardReportOverflow = "limit-report"
)

// Limits is the resolved configuration. It is never exported and never
// constructed by a caller — only by resolveLimits — so it is free to use
// zero-means-default internally without the ambiguity that made an exported
// struct a bad idea.
type Limits struct {
	DecodedStreamBytes  int
	DecodedContentBytes int64
	ObjectStreamBytes   int64
	ContentStreamBytes  int
	ICCProfileBytes     int
	XMPPacketBytes      int
	CIDRangeSpan        int
	RoleMapSteps        int
	TableGridFills      int64
	PostScriptSteps     int
	CmapWork            int
}

// DefaultLimits is the configuration a caller who passes no options gets.
func DefaultLimits() Limits { return Limits{}.WithDefaults() }

// WithDefaults fills any unset (zero) field with its default. It is idempotent,
// so it is safe to apply to an already-resolved struct — which is what lets a
// hand-built Document (whose limits field is the zero value) behave exactly like
// one produced by Read.
func (l Limits) WithDefaults() Limits {
	if l.DecodedStreamBytes == 0 {
		l.DecodedStreamBytes = DefaultMaxDecodedStreamBytes
	}
	if l.DecodedContentBytes == 0 {
		l.DecodedContentBytes = DefaultMaxDecodedContentBytes
	}
	if l.ObjectStreamBytes == 0 {
		l.ObjectStreamBytes = DefaultMaxObjectStreamBytes
	}
	if l.ContentStreamBytes == 0 {
		l.ContentStreamBytes = DefaultMaxContentStreamBytes
	}
	if l.ICCProfileBytes == 0 {
		l.ICCProfileBytes = DefaultMaxICCProfileBytes
	}
	if l.XMPPacketBytes == 0 {
		l.XMPPacketBytes = DefaultMaxXMPPacketBytes
	}
	if l.CIDRangeSpan == 0 {
		l.CIDRangeSpan = DefaultMaxCIDRangeSpan
	}
	if l.RoleMapSteps == 0 {
		l.RoleMapSteps = DefaultMaxRoleMapSteps
	}
	if l.TableGridFills == 0 {
		l.TableGridFills = DefaultMaxTableGridFills
	}
	if l.PostScriptSteps == 0 {
		l.PostScriptSteps = DefaultMaxPostScriptSteps
	}
	if l.CmapWork == 0 {
		l.CmapWork = DefaultMaxCmapWork
	}
	return l
}

// ObjStmMaxRaw bounds one object stream's decompressed (index + bodies) size on
// the write side. It derives from the decoded-stream cap rather than being its
// own knob: a container whose decompressed size exceeds what the reader accepts
// would be written and then rejected on the next read, silently losing every
// object it holds. Setting the two independently is how that happens, so the
// writer follows the reader by construction. Half the cap leaves generous margin
// for the index header.
func (l Limits) ObjStmMaxRaw() int { return l.DecodedStreamBytes / 2 }

type Canceler struct {
	ctx  context.Context
	done <-chan struct{}
}

// NewCanceler builds the signal for ctx. A nil ctx is treated as "never
// cancels" rather than panicking: the package's own internal call sites pass
// canceler{} directly, and a caller who reaches a Context entry point with a nil
// context has made a mistake that should not become a crash inside a validator
// running on untrusted input.
func NewCanceler(ctx context.Context) Canceler {
	if ctx == nil {
		return Canceler{}
	}
	return Canceler{ctx: ctx, done: ctx.Done()}
}

// Cancellable reports whether this signal can ever fire.
//
// It is for the two places where a non-Cancellable run should skip real work,
// not merely a poll: cancelReader, which would otherwise wrap and chunk every
// decode in the package, and lzwDecode's output gate. The token scanners
// deliberately do not use it — see cancelScanBytes.
func (c Canceler) Cancellable() bool { return c.done != nil }

// Stopped reports whether the operation should stop now.
func (c Canceler) Stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Err returns the reason the operation was stopped, or nil. It agrees with
// stopped: a context's Done channel is closed exactly when its Err is non-nil.
func (c Canceler) Err() error {
	if c.ctx == nil {
		return nil
	}
	return c.ctx.Err()
}

// StopErr returns a non-nil error when the operation should stop, and nil
// otherwise, so a loop body reads `if err := cancel.StopErr("reading PDF");
// err != nil { return nil, err }`.
//
// It wraps the context's own error rather than replacing it, so that
// errors.Is(err, context.Canceled) and errors.Is(err, context.DeadlineExceeded)
// work for the caller — the difference between "someone hit stop" and "this
// file took too long" is one they will want to act on differently.
//
// This is the "loud" reporting class of docs/limits.md, and it is what Read and
// Write use: neither has a finding channel, and neither may hand back a
// truncated result that looks whole.
func (c Canceler) StopErr(what string) error {
	if !c.Stopped() {
		return nil
	}
	err := c.Err()
	if err == nil {
		// Unreachable in practice: Done is closed only after Err is set. Kept so
		// a stopped operation can never report success.
		err = context.Canceled
	}
	return fmt.Errorf("%s: %w", what, err)
}

// CancelScanBytes is how many bytes of a content stream one of the token
// scanners covers between cancellation checks.
//
// The scanners are the package's hot loop — forEachContentItem and
// forEachContentToken are together about two thirds of a large file's
// validation time — so the check cannot go per token. It is gated on the scan
// position instead: one comparison of the loop index against a local int per
// token, and the poll itself once per megabyte. A megabyte of content tokenizes
// in roughly 10 ms, which sets the granularity of this level of the design.
//
// The scanners do not branch on cancellable() to skip the bookkeeping, even
// though that would spare a non-cancellable run one poll per megabyte. Two extra
// statements in tokenizeContent's loop pushed its inline cost from under the
// compiler's 800-node budget to 805, so the range-over-func closure stopped
// being inlined into its consumers and PDF/UA validation of a 71 MB file slowed
// by 5% — far more than the poll it was avoiding, which is a non-blocking
// receive on a nil channel taken once per megabyte. If you add anything to
// these loops, check `go build -gcflags=-m=2` for "function too complex" on
// tokenizeContent.func1 before trusting a wall-clock measurement.
const CancelScanBytes = 1 << 20

// CancelReadChunk bounds how much one wrapped Read may produce, and so how
// long a decompression runs between cancellation checks. A megabyte of flate
// inflates in roughly 10 ms.
const CancelReadChunk = 1 << 20

// CancelingReader interrupts a read loop. Decompressing one stream is the
// longest single uninterruptible step in the package — the decoded-stream cap
// is 100 MB by default, which is about a second of flate — and it was the
// dominant residual once the content scanners had been made interruptible: on a
// 71 MB real-world file, cancellation took 900 ms, of which essentially all was
// one stream inflating.
//
// It caps the caller's buffer as well as polling, because io.ReadAll grows its
// read size with the buffer: without the cap a late read could ask flate for
// tens of megabytes and not return until it had them.
type CancelingReader struct {
	r      io.Reader
	cancel Canceler
}

func (c *CancelingReader) Read(p []byte) (int, error) {
	if err := c.cancel.StopErr("decoding stream"); err != nil {
		return 0, err
	}
	if len(p) > CancelReadChunk {
		p = p[:CancelReadChunk]
	}
	return c.r.Read(p)
}

// CancelReader wraps r so the read stops when cancel fires. A signal that can
// never fire returns r unwrapped, so an ordinary decode pays nothing — neither
// the poll nor the extra Read calls the chunk cap implies.
func CancelReader(cancel Canceler, r io.Reader) io.Reader {
	if !cancel.Cancellable() {
		return r
	}
	return &CancelingReader{r: r, cancel: cancel}
}

// LimitBound renders the bound a guard tripped on, saying whether it is the
// package default or a value the caller chose. "you hit the 8 MiB cap you set"
// and "you hit our 8 MiB default" call for different responses — the first is a
// configuration decision to revisit, the second a report that pdf0's own
// ceiling was too low for this file — and a message that gives only the number
// cannot tell them apart.
//
// A caller who configures a limit to exactly its default is described as having
// the default. That is the one case the comparison gets wrong, and it costs
// nothing: the advice either message leads to is the same.
func LimitBound(effective, def int64) string {
	if effective == def {
		return strconv.FormatInt(effective, 10) + " (pdf0's default)"
	}
	return strconv.FormatInt(effective, 10) + " (configured by the caller)"
}

// Trip is one guard trip: which guard, what it left incomplete, and the
// object the incompleteness attaches to (0 when it is document-wide).
type Trip struct {
	guard  string
	detail string
	Obj    int
}

func (t Trip) Message() string {
	if t.guard == GuardCanceled {
		// Deliberately worded so that it cannot be read as a statement about the
		// file. A cancelled run's findings are true but partial, and the absence
		// of a finding says nothing at all.
		return fmt.Sprintf("the run was cancelled before it finished (%s): %s; the checks that had not yet run were skipped, so this file is neither confirmed conformant nor non-conformant", t.guard, t.detail)
	}
	return fmt.Sprintf("resource limit reached (%s): %s; the checks that depend on it were skipped, so this file is neither confirmed conformant nor non-conformant in that respect", t.guard, t.detail)
}

// NewTrip records one guard trip: which guard, what it left incomplete, and the
// object the incompleteness attaches to (0 when it is document-wide). The
// fields stay unexported so that a Trip cannot be assembled half-populated
// outside this package — Message reads all three.
// Guard returns the identifier of the guard that tripped. It is an accessor
// rather than an exported field so that a Trip can only be built through
// NewTrip, which is what keeps Message's three inputs consistent.
func (t Trip) Guard() string { return t.guard }

func NewTrip(guard, detail string, obj int) Trip {
	return Trip{guard: guard, detail: detail, Obj: obj}
}

// MaxRecordedTrips bounds the recorder itself. A file crafted to trip a
// guard once per object would otherwise turn the *report* into the resource
// exhaustion the guards exist to prevent. Distinct trips beyond the cap are
// counted, not stored, and reported in aggregate.
const MaxRecordedTrips = 64

// Recorder collects the guard trips of one run. The zero value is usable
// and a nil *Recorder discards, so a guard can report unconditionally
// without knowing whether anything is listening.
//
// The mutex is not there because validation is concurrent — a run is
// single-goroutine, and each run gets its own recorder on its own shallow copy
// of the Document (see validationCache) — but because the recorder is the one
// piece of per-run state that guards write to from arbitrary depth, and a
// future parallel check must not turn that into a data race.
type Recorder struct {
	mu      sync.Mutex
	seen    map[Trip]bool
	trips   []Trip
	dropped int
}

// Note records a trip, ignoring repeats of one already recorded. guard is one
// of the limit* identifiers above; detail says, in the file's terms, what was
// left incomplete.
func (r *Recorder) Note(guard, detail string, obj int) {
	if r == nil {
		return
	}
	t := Trip{guard: guard, detail: detail, Obj: obj}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen[t] {
		return
	}
	if len(r.trips) >= MaxRecordedTrips {
		r.dropped++
		return
	}
	if r.seen == nil {
		r.seen = make(map[Trip]bool)
	}
	r.seen[t] = true
	r.trips = append(r.trips, t)
}

// Snapshot returns the recorded trips in a deterministic order, plus a synthetic
// trip standing for any that were dropped by maxRecordedLimitTrips.
func (r *Recorder) Snapshot() []Trip {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Trip, len(r.trips))
	copy(out, r.trips)
	if r.dropped > 0 {
		out = append(out, Trip{
			guard:  GuardReportOverflow,
			detail: fmt.Sprintf("%d further distinct guard trips were not reported individually", r.dropped),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].guard != out[j].guard {
			return out[i].guard < out[j].guard
		}
		if out[i].Obj != out[j].Obj {
			return out[i].Obj < out[j].Obj
		}
		return out[i].detail < out[j].detail
	})
	return out
}
