package pdf0

import (
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
)

// Configurable resource limits.
//
// pdf0 parses untrusted input, so nearly every unbounded loop and every
// allocation sized by a number the file supplies is capped. Those caps are the
// package's defaults and they are chosen to be safe: a caller who configures
// nothing gets exactly the behaviour pdf0 has always had.
//
// A fixed number cannot be right for every caller, though. A batch converter on
// a workstation and a public upload endpoint want genuinely different answers to
// "how much may one untrusted document cost me", and no default satisfies both.
// The options below let a caller say which they are.
//
// The shape is variadic functional options rather than an exported struct,
// because for a limit the zero value is meaningful: core.Limits{MaxDecodeBytes: 0}
// reads equally naturally as "no cap at all" and as "give me the default", and
// the caller cannot tell from the type which they get. With options, "unset" is
// simply "the option was never called" — there is no ambiguous value to
// document, and adding a knob later is purely additive.
//
// Values are resolved once at the public entry point — Read, ReadWithPassword,
// their Context variants, and ParseXRefStream — into the unexported limits
// struct below, stored on the Document, and passed explicitly to the code that
// enforces them. Validation and extraction therefore inherit whatever Read was
// given, including the file's own cross-reference streams and any PDF embedded
// in it. Because the struct travels by value and is never mutated after
// resolution, validating one Document from several goroutines stays safe.
//
// No other exported entry point takes options, and that is not an oversight:
// the validators and extractors read the configuration off the Document they
// are given, and the remaining constructors (ParseXRefTable here;
// syntax.NewLexer, syntax.NewParser and syntax.NewSerializer) enforce only
// limits that were deliberately left internal — the depth caps and the
// lexer's token gap. See
// docs/proposals/configurable-limits.md §5, Group D.
//
// This file answers "what is a limit"; limits_report.go answers "what happens
// when one trips" — the recorder every guard reports through, the "limit" rule
// identifier a trip is reported under, and IsCheckerFinding. The two are
// deliberately separate: this half is public API that changes when a knob is
// added, that half is checker-honesty machinery that changes when a guard
// learns to speak. Only the trip messages join them, and only to say whether
// the bound that fired was the default or one the caller chose.
//
// See docs/limits.md for the per-guard classification (which guards are
// configurable, which report a trip, which are structural) and
// docs/proposals/configurable-limits.md for the design record, including which
// limits are deliberately not configurable and the threading cost that
// justified leaving each one internal.

// Option configures a resource limit. Callers do not construct one directly;
// they call a With* function. Options are accepted by Read, ReadWithPassword,
// ReadContext, ReadWithPasswordContext and ParseXRefStream, and the resolved
// values are inherited by every validator and extractor that runs on the
// resulting Document.
type Option interface {
	apply(*core.Limits) error
}

type optionFunc func(*core.Limits) error

func (f optionFunc) apply(l *core.Limits) error { return f(l) }

// ErrInvalidOption is wrapped by the error an entry point returns for an
// option whose value it cannot honour.
var ErrInvalidOption = errors.New("pdf0: invalid option")

// resolveLimits applies options over the zero struct and fills in defaults. An
// option with a value it cannot honour is an error, returned by the entry
// point before anything is read.
func resolveLimits(opts []Option) (core.Limits, error) {
	var l core.Limits
	for _, o := range opts {
		if o != nil {
			if err := o.apply(&l); err != nil {
				return core.Limits{}, err
			}
		}
	}
	return l.WithDefaults(), nil
}

// positiveLimit is the one validation every limit option shares.
//
// A bound is a count of bytes or of steps, and every one of them is positive.
// Zero and negative values are refused rather than given a meaning, because
// every meaning they could have is a surprise to someone: 0 reads as "no
// limit" to one caller and "allow nothing" to another, and internally it meant
// "the default", so WithMaxDecodedStreamBytes(0) silently did nothing; -1 made
// every stream fail with "exceeds maximum size (-1 bytes)" (audit 2026-09-22
// C48). The way to ask for no practical bound is the type's maximum —
// math.MaxInt, or math.MaxInt64 for the int64 options — which is honoured as
// written: every guard compares against it without arithmetic that could
// overflow (the decoders' "+1" read-ahead used to wrap it negative, so
// math.MaxInt decoded every stream to nothing).
func positiveLimit[T int | int64](option string, n T, set func(*core.Limits, T)) Option {
	return optionFunc(func(l *core.Limits) error {
		if n <= 0 {
			return fmt.Errorf("%w: %s(%d): a limit must be positive; for no practical limit pass the type's maximum (math.MaxInt or math.MaxInt64)", ErrInvalidOption, option, n)
		}
		set(l, n)
		return nil
	})
}

// lim returns the resolved limits for a document. Reading through this accessor
// rather than the field directly means a hand-built &Document{...}, whose limits
// field is the zero value, behaves exactly like one produced by Read.
func (d *Document) lim() core.Limits {
	if d == nil {
		return core.DefaultLimits()
	}
	return d.limits.WithDefaults()
}

// WithMaxDecodedStreamBytes caps the decompressed size of any single stream
// (default 100 MB). This is the decompression-bomb ceiling and applies to every
// FlateDecode and LZWDecode stream in the file; lowering it is the main lever
// for a service accepting untrusted uploads. The largest legitimate stream
// measured across the veraPDF corpus and a 978-file Common Crawl sample is
// 29 MB, so values below about 32 MB will start rejecting real documents.
//
// The write-side object-stream cap derives from this value, so lowering it also
// makes Write emit smaller object-stream containers that the same configuration
// can read back.
func WithMaxDecodedStreamBytes(n int) Option {
	return positiveLimit("WithMaxDecodedStreamBytes", n, func(l *core.Limits, n int) { l.DecodedStreamBytes = n })
}

// WithMaxDecodedContentBytes caps the total decoded content one validation run
// will materialize (default 512 MB). The per-stream cap stops a single stream
// exploding; this is the only bound on a whole run, and so the knob for "one
// upload must not exhaust my process". The heaviest real document measured
// needs 218 MB.
func WithMaxDecodedContentBytes(n int64) Option {
	return positiveLimit("WithMaxDecodedContentBytes", n, func(l *core.Limits, n int64) { l.DecodedContentBytes = n })
}

// WithMaxObjectStreamBytes caps the memory Read may spend on the objects it
// unpacks from object streams in one document (default 512 MB): the parser's
// estimate of every object it builds (syntax.MaterialCost, twice the measured
// live cost), plus the decoded bytes of the container being unpacked. Object
// streams are the other compression-amplification path into a document: a
// 403 KB file can carry three containers that inflate to 270 MB of "1 0 R",
// which is five times that in memory. A container that would take the total
// past the bound is not unpacked, its objects are missing, and every validator
// reports the trip under "limit".
//
// The heaviest real document measured — across the veraPDF corpus, the
// Factur-X, WTPDF and PDF/VT suites and a 1000-file Common Crawl sample —
// charges 64 MB, so the default leaves eight times that. The bound used to
// meter decoded bytes instead (the heaviest document then measured needed
// 9 MB of them), which let the objects take five times the bound in memory.
func WithMaxObjectStreamBytes(n int64) Option {
	return positiveLimit("WithMaxObjectStreamBytes", n, func(l *core.Limits, n int64) { l.ObjectStreamBytes = n })
}

// WithMaxContentStreamBytes caps the decoded size of a single content stream or
// image sample buffer that will be scanned (default 64 MB). Larger streams are
// skipped. The largest real content stream measured is 29 MB.
func WithMaxContentStreamBytes(n int) Option {
	return positiveLimit("WithMaxContentStreamBytes", n, func(l *core.Limits, n int) { l.ContentStreamBytes = n })
}

// WithMaxICCProfileBytes caps the decoded size of an ICC profile (default
// 8 MiB). The largest real profile measured is 1.8 MB.
func WithMaxICCProfileBytes(n int) Option {
	return positiveLimit("WithMaxICCProfileBytes", n, func(l *core.Limits, n int) { l.ICCProfileBytes = n })
}

// WithMaxXMPPacketBytes caps the size of an XMP packet that the property checks
// will build a node tree for (default 4 MiB). Larger packets are still checked
// for well-formedness, which streams; only the property-value rules are skipped.
//
// What it bounds is memory. Building the tree is linear in the packet — the
// quadratic text accumulation this comment used to warn of is gone (audit
// 2026-09-22 C40) — but a node per comment or text run adds up: a 4 MiB packet
// of 520,000 comment-interrupted text runs builds in 0.4 s and holds some
// 300 MB while it is read. The largest real packet measured is 1.6 MB.
func WithMaxXMPPacketBytes(n int) Option {
	return positiveLimit("WithMaxXMPPacketBytes", n, func(l *core.Limits, n int) { l.XMPPacketBytes = n })
}

// WithMaxTableGridFills caps the number of grid slots the PDF/UA table checks
// will fill for one table (default 1<<24), bounding a cell whose /RowSpan and
// /ColSpan claim a multi-million-slot area.
func WithMaxTableGridFills(n int64) Option {
	return positiveLimit("WithMaxTableGridFills", n, func(l *core.Limits, n int64) { l.TableGridFills = n })
}

// WithMaxCmapWork caps the work spent expanding one TrueType cmap subtable of
// an expanding format — 4 or 12 — (default 1<<18). A hostile subtable can
// declare segments or groups whose combined character ranges cover the whole
// code space many times over.
//
// A subtable the budget stops is returned as a *prefix* of the font's real
// coverage, marked partial, and the checks that would otherwise read a missing
// mapping as "this code has no glyph" decline instead and report the trip (see
// limits_report.go). Lowering this therefore costs coverage of the
// glyph-presence rules on large fonts; it never turns them into false
// positives.
func WithMaxCmapWork(n int) Option {
	return positiveLimit("WithMaxCmapWork", n, func(l *core.Limits, n int) { l.CmapWork = n })
}

// WithMaxWork caps the work one run may do (default 2^28 units plus 512 for
// every byte of the file the document was read from). A run is one
// validation or one text or image extraction, and a unit is one elementary
// step, some tens of nanoseconds: a node of the object graph visited, an
// entry expanded, an operator executed, a range probed, a few bytes tokenised
// or decoded. The
// default grows with the file because real work does, and amplification — a
// small file asking for work out of all proportion to its size — is what the
// budget exists to stop.
//
// Every other limit bounds one unit of work: one stream, one range, one
// evaluation. This one bounds the product, which is what a small hostile file
// controls — a shared structure referenced from every element, a function
// evaluated for every pixel, a chain walked from every referrer. When the run
// reaches it, the check in progress is abandoned rather than finished on a
// partial result, no further check runs, and the run reports the trip under
// "limit" (guard "work"); an extraction stops with an error naming it. A
// context deadline bounds the same work in time rather than in units; the
// budget is what bounds a caller who set none.
//
// Across the veraPDF corpus, the PDF/VT, WTPDF, Factur-X and PDF 2.0 example
// suites and a 1000-file Common Crawl sample, under every validator and both
// extractors, no run comes within a factor of nine of its default (the
// measurement is recorded with core.DefaultMaxWork).
func WithMaxWork(n int64) Option {
	return positiveLimit("WithMaxWork", n, func(l *core.Limits, n int64) { l.Work = n })
}

// WithMaxImagePixels caps the size of an image that extraction will decode
// (default 1<<26, 67 megapixels — the same bound images.EmbedImage applies to
// an image a caller embeds, so pdf0 reads back anything it writes). An A4 page
// scanned at 600 dpi is 35 megapixels.
//
// It is one budget for every image codec and every buffer sized from an
// image's geometry: the raw, Flate and LZW sample path, CCITT fax, JBIG2 (whose
// per-bitmap bound it sets, with four times as much across all the bitmaps one
// stream decodes), JPEG, JPEG 2000, and the /Mask and /SMask images composited
// onto them. An image with more than four components is also held to four
// samples per pixel. The product is computed with overflow checks, so a
// /Width of 2^60 is refused rather than wrapped.
//
// An image over the budget is not decoded: its ExtractedImage has Decoded
// false, its encoded bytes, and a Note naming the image-pixels guard, and the
// walk continues with the next image.
func WithMaxImagePixels(n int64) Option {
	return positiveLimit("WithMaxImagePixels", n, func(l *core.Limits, n int64) { l.ImagePixels = n })
}
