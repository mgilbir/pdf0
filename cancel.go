package pdf0

import (
	"context"
	"fmt"
	"io"
)

// Cancellation.
//
// pdf0 does work whose cost is set by the document, not by the caller:
// validating a 71 MB, 1256-page file takes about ten seconds, and a hostile
// file can sit at the resource ceilings for longer still. A caller with a
// deadline — an HTTP handler, a batch worker, this repository's own
// cmd/corpusprobe — needs to be able to stop that work, not merely to stop
// waiting for it. Abandoning the goroutine keeps the CPU and the memory busy
// until the work finishes on its own; with several workers and a 25-second
// file that is real leakage, and it was the concrete motivation for this file.
//
// # The API shape, and the one that was rejected
//
// Cancellation is exposed as *Context variants of the existing entry points —
// ReadContext, ValidatePDFAContext, ExtractTextContext and the rest — with
// ctx as the explicit first parameter. This package already has variadic
// functional options (limits.go), so a WithContext(ctx) Option was possible and
// would have been a smaller diff. It was rejected for two reasons, and the
// second is the one that decided it:
//
//   - An Option is *stored*: resolveLimits folds the options into a struct that
//     lives on the Document and is inherited by every validator and extractor
//     that later runs on it. A context is not configuration with the lifetime of
//     a document; it is the lifetime of one operation. A context stored at Read
//     would still be governing a validation run started minutes later, and
//     cancelling the read would silently fail an unrelated call. This is exactly
//     the case the Go documentation has in mind when it says not to store a
//     Context inside a struct type.
//   - It would make cancellation invisible at the call site. `ValidatePDFA(doc,
//     level)` would be cancellable or not depending on an option passed to a
//     `Read` twenty lines earlier. `ValidatePDFAContext(ctx, doc, level)` says
//     what governs it, in the signature, where the reader is.
//
// The limits and the context therefore stay separate mechanisms, because they
// have separate scopes: a limit says how much this *document* may cost, a
// context says how long this *operation* may take.
//
// # Where the signal lives
//
// A validation run already carries per-run state on a shallow copy of the
// Document (validationCache, see limits_report.go), and that state's lifetime is
// exactly the run's. The canceler rides there, so the caller's Document never
// holds a context. Read and Write have no run, so they thread a canceler down
// their loops as an ordinary parameter.
//
// # Where the check happens
//
// Layered, coarsest first, because ctx.Err() in the innermost loop is a
// measurable cost:
//
//   - per check, in every validator's check loop;
//   - per page, per content stream, per image, per embedded PDF;
//   - per cancelScanBytes inside the three token scanners, which are two thirds
//     of a large document's validation time;
//   - per cancelReadChunk inside flate (cancelReader) and LZW decoding, which is
//     the longest single uninterruptible step there is;
//   - per object in Read's load loops and Write's emit loop.
//
// On a 71 MB, 1256-page file whose full PDF/A validation takes 10.7 s,
// cancellation takes effect in about 60 ms, and an already-cancelled context
// returns in about 10 µs. The decompression layer earns its place: with only the
// scanner checks the same measurement was 903 ms, essentially all of it one
// stream inflating.
//
// # What a cancelled run reports
//
// A cancelled validation reports itself the same way a tripped resource guard
// does: a finding under the rule identifier "limit", which IsCheckerFinding
// reports as a checker finding rather than a non-conformance. The reasoning in
// limits_report.go applies unchanged — the checker stopped early, so the honest
// answer is "unknown", not "conformant" — and routing cancellation through
// runLimitTrips means every validator reports it without each having to learn a
// second mechanism. The findings gathered before the cancellation are kept; they
// are true, just incomplete. What cannot happen is an empty result: a cancelled
// validation never looks like a clean bill of health.
//
// Read and Write instead return an error wrapping ctx.Err(), because they are
// already in the "loud" class docs/limits.md describes: they have no finding
// channel, and a truncated document or a truncated file must never be handed
// back as if it were whole. ExtractTextContext and ExtractImagesContext return
// an error alongside the partial result for the same reason.

// canceler is one operation's cancellation signal.
//
// The zero value never cancels, which is what lets every internal helper take
// one unconditionally: a non-cancellable call passes canceler{} rather than
// branching around the parameter.
//
// ctx.Done() is hoisted into a field at construction because the hot content
// scanners test it. Both context.Context.Done and context.Context.Err take the
// context's mutex on a cancellable context; a receive-with-default on an
// already-obtained channel does not, so the test in the scanner costs a
// non-blocking channel poll rather than a lock. A context that can never be
// cancelled (context.Background) has a nil Done channel, and a receive on a nil
// channel blocks, so the default case is taken — the zero value and a
// background context behave alike, for free.
type canceler struct {
	ctx  context.Context
	done <-chan struct{}
}

// newCanceler builds the signal for ctx. A nil ctx is treated as "never
// cancels" rather than panicking: the package's own internal call sites pass
// canceler{} directly, and a caller who reaches a Context entry point with a nil
// context has made a mistake that should not become a crash inside a validator
// running on untrusted input.
func newCanceler(ctx context.Context) canceler {
	if ctx == nil {
		return canceler{}
	}
	return canceler{ctx: ctx, done: ctx.Done()}
}

// cancellable reports whether this signal can ever fire.
//
// It is for the two places where a non-cancellable run should skip real work,
// not merely a poll: cancelReader, which would otherwise wrap and chunk every
// decode in the package, and lzwDecode's output gate. The token scanners
// deliberately do not use it — see cancelScanBytes.
func (c canceler) cancellable() bool { return c.done != nil }

// stopped reports whether the operation should stop now.
func (c canceler) stopped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// err returns the reason the operation was stopped, or nil. It agrees with
// stopped: a context's Done channel is closed exactly when its Err is non-nil.
func (c canceler) err() error {
	if c.ctx == nil {
		return nil
	}
	return c.ctx.Err()
}

// stopErr returns a non-nil error when the operation should stop, and nil
// otherwise, so a loop body reads `if err := cancel.stopErr("reading PDF");
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
func (c canceler) stopErr(what string) error {
	if !c.stopped() {
		return nil
	}
	err := c.err()
	if err == nil {
		// Unreachable in practice: Done is closed only after Err is set. Kept so
		// a stopped operation can never report success.
		err = context.Canceled
	}
	return fmt.Errorf("%s: %w", what, err)
}

// cancelScanBytes is how many bytes of a content stream one of the token
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
const cancelScanBytes = 1 << 20

// cancelReadChunk bounds how much one wrapped Read may produce, and so how
// long a decompression runs between cancellation checks. A megabyte of flate
// inflates in roughly 10 ms.
const cancelReadChunk = 1 << 20

// cancelingReader interrupts a read loop. Decompressing one stream is the
// longest single uninterruptible step in the package — the decoded-stream cap
// is 100 MB by default, which is about a second of flate — and it was the
// dominant residual once the content scanners had been made interruptible: on a
// 71 MB real-world file, cancellation took 900 ms, of which essentially all was
// one stream inflating.
//
// It caps the caller's buffer as well as polling, because io.ReadAll grows its
// read size with the buffer: without the cap a late read could ask flate for
// tens of megabytes and not return until it had them.
type cancelingReader struct {
	r      io.Reader
	cancel canceler
}

func (c *cancelingReader) Read(p []byte) (int, error) {
	if err := c.cancel.stopErr("decoding stream"); err != nil {
		return 0, err
	}
	if len(p) > cancelReadChunk {
		p = p[:cancelReadChunk]
	}
	return c.r.Read(p)
}

// cancelReader wraps r so the read stops when cancel fires. A signal that can
// never fire returns r unwrapped, so an ordinary decode pays nothing — neither
// the poll nor the extra Read calls the chunk cap implies.
func cancelReader(cancel canceler, r io.Reader) io.Reader {
	if !cancel.cancellable() {
		return r
	}
	return &cancelingReader{r: r, cancel: cancel}
}

// canceler returns the cancellation signal of the validation run d belongs to,
// or the never-cancelling zero value when no run is in progress. Reading
// through this accessor is what lets a check deep in unexported code obtain the
// signal without every helper having to carry it as a parameter.
func (d *Document) canceler() canceler {
	if d == nil || d.valCache == nil {
		return canceler{}
	}
	return d.valCache.cancel
}

// stopped reports whether the validation run d belongs to has been cancelled.
// It is the coarse gate: the check loops of every validator consult it between
// checks, and the traversals consult it between pages, streams and images.
func (d *Document) stopped() bool { return d.canceler().stopped() }
