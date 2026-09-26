package core

import (
	"errors"
	"math"
	"strconv"
)

// The per-run work meter (audit 2026-09-22 T1).
//
// Every other guard in this package bounds one unit of work: one /W range, one
// RoleMap chain, one PostScript evaluation, one bfrange, one stream decode, one
// XMP packet. A file does not have to exceed any of them to be expensive. It
// only has to multiply a bounded unit by something it controls — references,
// pixels, pages, structure elements, overlapping ranges — and each check does
// the bounded unit once per multiple. Cancellation was polled between checks,
// so a check that did a million bounded units under a two-second deadline ran
// for sixteen.
//
// The meter is the one bound on a whole run. Every walk over the object graph,
// every expansion, every tokenisation, every function evaluation, every
// CMap and ToUnicode lookup charges it, and a charge is also where the run's
// context is polled. When the budget is spent, or the context ends, the charge
// that notices does not return: it unwinds the check in progress with a
// panic carrying abortRun, and the check's boundary (runCheck and its
// siblings) discards that check's partial result and carries on to find that
// every later check is refused too.
//
// Unwinding is deliberate, and it is what makes the meter safe to charge from
// anywhere. A walker that merely stopped would hand its caller a truncated
// result, and a check reading "no heading found" off a truncated structure
// tree would accuse a conformant file: the one failure the limit machinery
// exists to prevent (limits_report.go). A check that is unwound reports
// nothing, and the trip — recorded once, under GuardWork or GuardCanceled —
// tells the caller the run did not finish.
//
// # Units
//
// One unit is one elementary step, of the order of tens of nanoseconds: a node
// visited, an entry expanded, an operator executed, a range probed. Work over
// bytes is charged in proportion to its cost rather than one unit a byte:
// scanning or decoding a byte costs a nanosecond or two (ChargeScan, a unit
// per scanBytesPerUnit), and copying one a fraction of that (ChargeCopy, a
// unit per copyBytesPerUnit). A unit charged per byte made the budget a count
// of bytes, and the pages of one real document concatenating their /Contents
// arrays for each check charged a gigabyte in two seconds.
//
// The rates are calibrated to cost (2026-09-23, the corpora and a Common Crawl
// sample): inflating a stream measured 25 ns a unit, tokenising a content
// stream 26, and a CMap program — nearly all hexadecimal strings and numbers —
// 102, so a CMap is charged four times a content stream's rate. Over every
// validator and text-extraction run above 5 million units, a unit costs 32 ns
// at the median and 55 at the 90th percentile. The worst, 225 ns, is a real
// PDF/VT file whose run is dominated by allocating 28,000 findings, which is
// output, not amplification. Image extraction is not calibrated this way: a
// decoded image is bounded by the pixel budget rather than the meter, and its
// codecs cost per pixel, not per byte, so an extraction charges about 200 ns a
// unit on average; charging pixels instead would charge a legitimate scan
// (a JBIG2 page is 8 million pixels from 30 KB) past its file's budget.
//
// The steps still differ in cost, by a few times, which does not matter: the
// default is set far above what any real document charges (see
// DefaultMaxWork) and far below what an amplifying file asks for.
//
// # Where a charge may be made
//
// A charge panics only inside a run, and every entry point that creates a run
// runs its checks behind a boundary that recovers abortRun (IsAbort). Outside
// a run — a View with a nil Run, or a Canceler that carries no meter — a charge
// is free and never unwinds.

// The default work budget of one run, in units, is DefaultMaxWork plus
// DefaultWorkPerSourceByte for every byte of the file the document was read
// from (DefaultWork).
//
// It grows with the file because real work does: a document's content, its
// structure tree and its pages are in proportion to its bytes, and a 300 MB
// print job does three hundred times the work of a 1 MB one, legitimately.
// What the meter exists to stop is amplification — a small file asking for
// work out of all proportion to its size — and a fixed budget large enough
// for the biggest real document would let a kilobyte of hostile input run for
// minutes. The base covers what any document costs regardless of size; the
// per-byte allowance covers the rest.
//
// Both are set from measurement (TestWorkMeterHeadroom, 2026-09-23): 4,032
// files — the veraPDF corpus, the PDF 2.0 examples, the Cal Poly PDF/VT suite's
// variants up to 32 MB, WTPDF, Factur-X and a 1000-file Common Crawl sample —
// under every validator and both extractors, 36,036 runs. The heaviest
// relative to its budget was PDF/A-2a over a 5.3 MB Common Crawl file: 306
// million units in 13 seconds, 58 units a byte of the file, and a tenth of its
// default. No file under 4 MB charged more than 23 million. The per-byte
// allowance is eight times the heaviest ratio measured, and the base is ten
// times the heaviest small file. Since content that pages share is read once
// (see View.ContentBytesAndKey), the same file charges 199 million units in 5
// seconds, and every run is at least fifteen times inside its default. At the
// median unit cost the base is about nine seconds of work: the ceiling a small
// file that reaches it pays before the run stops.
const (
	DefaultMaxWork           int64 = 1 << 28
	DefaultWorkPerSourceByte int64 = 512
)

// DefaultWork is the default work budget for a document read from sourceLen
// bytes (0 for one built in memory): DefaultMaxWork plus
// DefaultWorkPerSourceByte a byte, saturating rather than wrapping.
func DefaultWork(sourceLen int64) int64 {
	if sourceLen <= 0 {
		return DefaultMaxWork
	}
	if sourceLen > (math.MaxInt64-DefaultMaxWork)/DefaultWorkPerSourceByte {
		return math.MaxInt64
	}
	return DefaultMaxWork + DefaultWorkPerSourceByte*sourceLen
}

// WorkBudget is the work budget of a run over a document read from sourceLen
// bytes, and the default it is measured against in a trip's message: the
// caller's WithMaxWork when set, else DefaultWork(sourceLen).
func (l Limits) WorkBudget(sourceLen int64) (budget, def int64) {
	def = DefaultWork(sourceLen)
	if l.Work > 0 {
		return l.Work, def
	}
	return def, def
}

// GuardWork is the meter's guard identifier.
const GuardWork = "work" // Limits.Work, WithMaxWork

// GuardWalkDepth is the uniform depth guard's identifier (Descend).
const GuardWalkDepth = "walk-depth" // MaxWalkDepth; not configurable

// MaxWalkDepth bounds how deep any recursive walk over the object graph may
// go. A walk that recurses once per level of a structure the file controls
// needs a stack frame per level, and a visited set does not help: an acyclic
// chain of a million distinct objects has no cycle to find. The deepest real
// structure measured across the corpora (structure trees, page trees, form
// nesting, colour spaces, outline and action chains) is far inside it, and at
// about a kilobyte a frame the bound keeps every walk's stack to a few
// megabytes (audit 2026-09-22 C84).
//
// It is not configurable: it bounds stack, which is not a resource a caller
// can trade for coverage, and a structure deeper than this is not a document.
const MaxWalkDepth = 1024

// meterPoll is how many units may pass between two polls of the context.
const meterPoll = 1 << 14

// abortRun is the value a charge panics with when the run must stop.
type abortRun struct{ guard string }

// IsAbort reports whether r, a value recovered from a panic, is the meter
// ending the run rather than a fault. A boundary that recovers it discards the
// unwound check's partial result and reports nothing for it: the trip that
// ended the run is already recorded.
func IsAbort(r any) bool {
	_, ok := r.(abortRun)
	return ok
}

// Contain runs fn and swallows the meter's unwinding, reporting whether fn
// was unwound. Any other panic propagates. It is the outermost boundary of an
// entry point that starts a run, for the work done outside any one check.
func Contain(fn func()) (aborted bool) {
	defer func() {
		if r := recover(); r != nil {
			if !IsAbort(r) {
				panic(r)
			}
			aborted = true
		}
	}()
	fn()
	return false
}

// Meter is one run's work budget. It lives on Run, and the run's Canceler
// carries a pointer to it so that the scanners and decoders, which take a
// Canceler rather than a View, charge the same budget.
//
// A run is single-goroutine (see Recorder), so the counters need no lock.
type Meter struct {
	limit    int64
	def      int64 // the default budget, for the trip's message
	used     int64
	nextPoll int64
	cancel   Canceler // without the meter pointer, to poll the context
	trips    *Recorder
	dead     bool
	why      string
}

// NewMeter builds a meter with the given budget (DefaultMaxWork when limit is
// not positive) polling cancel, and recording its trip in trips. def is the
// default budget, which a trip's message compares the budget against.
func NewMeter(limit, def int64, cancel Canceler, trips *Recorder) *Meter {
	if limit <= 0 {
		limit = DefaultMaxWork
	}
	if def <= 0 {
		def = DefaultMaxWork
	}
	cancel.meter = nil
	return &Meter{limit: limit, def: def, nextPoll: min(meterPoll, limit+1), cancel: cancel, trips: trips}
}

// NewMeteredRun builds the state of one operation with a work budget of work
// units (DefaultMaxWork when not positive), polling cancel. It returns the run
// and the operation's Canceler, which carries the run's meter so that the
// scanners and decoders the operation hands it to charge the same budget: the
// operation must use that Canceler, not cancel, from here on.
//
// A cancel that already carries a meter belongs to a run in progress — the
// nested validation of an embedded file — and the new run charges that meter
// rather than starting a budget of its own: a file carrying a thousand
// embedded documents gets one budget, not a thousand.
func NewMeteredRun(trips *Recorder, work int64, cancel Canceler) (*Run, Canceler) {
	return newMeteredRun(trips, work, DefaultMaxWork, cancel)
}

// NewDocumentRun is NewMeteredRun for a document read from sourceLen bytes
// under limits l: its budget is l.WorkBudget(sourceLen).
func NewDocumentRun(trips *Recorder, l Limits, sourceLen int64, cancel Canceler) (*Run, Canceler) {
	work, def := l.WorkBudget(sourceLen)
	return newMeteredRun(trips, work, def, cancel)
}

func newMeteredRun(trips *Recorder, work, def int64, cancel Canceler) (*Run, Canceler) {
	if cancel.meter != nil {
		r := NewRun(trips)
		r.Meter = cancel.meter
		return r, cancel
	}
	m := NewMeter(work, def, cancel, trips)
	r := NewRun(trips)
	r.Meter = m
	return r, cancel.Metered(m)
}

// Used is the work charged so far.
func (m *Meter) Used() int64 {
	if m == nil {
		return 0
	}
	return m.used
}

// Limit is the meter's budget.
func (m *Meter) Limit() int64 {
	if m == nil {
		return 0
	}
	return m.limit
}

// Dead reports whether the run has been stopped: the budget spent or the
// context ended.
func (m *Meter) Dead() bool {
	if m == nil {
		return false
	}
	if !m.dead && m.cancel.Stopped() {
		m.dead, m.why = true, GuardCanceled
	}
	return m.dead
}

// ErrWorkLimit is matched (errors.Is) by the error an extraction returns when
// its run's work budget (WithMaxWork) was spent, or a walk went deeper than
// MaxWalkDepth: a *LimitError naming GuardWork or GuardWalkDepth.
var ErrWorkLimit = errors.New("the run's work meter stopped it")

// stopError is the error for a meter that stopped the run itself.
func (m *Meter) stopError() error {
	switch m.why {
	case GuardWork:
		return &LimitError{Guard: GuardWork, What: "the run's work — graph walks, expansions, tokenisation, evaluations and lookups together —", Unit: "units", Need: -1, Bound: m.limit, def: m.def}
	case GuardWalkDepth:
		return &LimitError{Guard: GuardWalkDepth, What: "the depth of a walk over the document's structure", Unit: "levels", Need: -1, Bound: MaxWalkDepth, def: MaxWalkDepth}
	}
	return nil
}

// Charge takes n units. When the budget is spent or the context has ended, it
// does not return: see the file comment.
func (m *Meter) Charge(n int64) {
	if m == nil {
		return
	}
	if n > 0 {
		m.used += n
	}
	if m.used >= m.nextPoll {
		m.slow()
	}
}

// slow is the rare half of Charge: a poll, or a stop.
func (m *Meter) slow() {
	if m.dead {
		panic(abortRun{m.why})
	}
	if m.used > m.limit {
		m.dead, m.why = true, GuardWork
		m.nextPoll = 0
		m.trips.Note(GuardWork, "this run did more than "+LimitBound(m.limit, m.def)+" units of work — graph walks, expansions, tokenisation, function evaluations and lookups together — so the check that reached the budget and every check after it were not run", 0)
		panic(abortRun{GuardWork})
	}
	if m.cancel.Stopped() {
		// The cancellation itself is reported by the entry point, from the
		// context (runLimitTrips); nothing is recorded here.
		m.dead, m.why = true, GuardCanceled
		m.nextPoll = 0
		panic(abortRun{GuardCanceled})
	}
	m.nextPoll = min(m.used+meterPoll, m.limit+1)
}

// Check unwinds the run if it has been stopped, and is otherwise free. It is
// for the point after a sub-computation that may have been cut short — a
// nested validation, a scanner that stops on cancellation — where a caller is
// about to draw a conclusion from the result.
func (m *Meter) Check() {
	if m == nil {
		return
	}
	if m.Dead() {
		m.nextPoll = 0
		panic(abortRun{m.why})
	}
}

// scanBytesPerUnit and copyBytesPerUnit are how many bytes of scanning (or
// decoding), and of copying, one unit of work stands for. See Units, above.
const (
	scanBytesPerUnit = 8
	copyBytesPerUnit = 64
)

// ChargeScan charges scanning or decoding n bytes, plus one unit for starting.
func (v View) ChargeScan(n int) { v.Charge(n/scanBytesPerUnit + 1) }

// ChargeCopy charges copying n bytes, plus one unit for starting.
func (v View) ChargeCopy(n int) { v.Charge(n/copyBytesPerUnit + 1) }

// ChargeScan is View.ChargeScan for the scanners that are handed a Canceler.
func (c Canceler) ChargeScan(n int) { c.Charge(n/scanBytesPerUnit + 1) }

// Charge charges the run's meter, when this view is part of a run. See Meter.
func (v View) Charge(n int) {
	if v.Run != nil {
		v.Run.Meter.Charge(int64(n))
	}
}

// Stopped reports whether this view's run should do no more work: its context
// ended, or its work budget is spent. The check loops consult it between
// checks; inside a check, a charge unwinds instead.
func (v View) Stopped() bool {
	if v.Run != nil && v.Run.Meter.Dead() {
		return true
	}
	return v.Cancel.Stopped()
}

// CheckStopped unwinds the run if it has been stopped. See Meter.Check.
func (v View) CheckStopped() {
	if v.Run != nil {
		v.Run.Meter.Check()
	}
}

// Descend is the uniform depth guard for a recursive walk over the object
// graph. A walker calls it on entry with its depth (0 at the root) and stops
// descending when it reports false.
//
// Inside a run a walk past MaxWalkDepth does not return: the trip is recorded
// under GuardWalkDepth and the run is stopped as a spent budget stops it,
// because a walk that silently stopped would hand a check a truncated
// structure to draw conclusions from. Outside a run it returns false, and the
// walker's caller — a builder, which has an error to return — says so.
func (v View) Descend(depth int) bool {
	if depth <= MaxWalkDepth {
		return true
	}
	if v.Run != nil && v.Run.Meter != nil {
		m := v.Run.Meter
		if !m.dead {
			m.dead, m.why, m.nextPoll = true, GuardWalkDepth, 0
			m.trips.Note(GuardWalkDepth, "a walk over the document's structure went deeper than "+strconv.Itoa(MaxWalkDepth)+" levels, so the check that met it and every check after it were not run", 0)
		}
		panic(abortRun{GuardWalkDepth})
	}
	return false
}

// Charge charges the meter this signal carries, if any. See Meter.
func (c Canceler) Charge(n int) {
	if c.meter != nil {
		c.meter.Charge(int64(n))
	}
}

// Metered returns c carrying m, so that the scanners and decoders given c
// charge m. It is how a run's canceler is built.
func (c Canceler) Metered(m *Meter) Canceler {
	c.meter = m
	return c
}

// stopScan is what a scanner that polled c and found it stopped does before
// it gives up: inside a run it unwinds, so that the truncated scan is not
// read as the whole stream; outside a run it returns, and the scanner stops.
func (c Canceler) stopScan() {
	if c.meter != nil {
		c.meter.Check()
	}
}
