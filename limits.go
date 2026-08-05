package pdf0

import (
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
// are given, and the remaining constructors (ParseXRefTable, NewLexer,
// NewParser, NewSerializer) enforce only limits that were deliberately left
// internal — the depth caps and the lexer's token gap. See
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
	apply(*core.Limits)
}

type optionFunc func(*core.Limits)

func (f optionFunc) apply(l *core.Limits) { f(l) }

// resolveLimits applies options over the zero struct and fills in defaults.
func resolveLimits(opts []Option) core.Limits {
	var l core.Limits
	for _, o := range opts {
		if o != nil {
			o.apply(&l)
		}
	}
	return l.WithDefaults()
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
	return optionFunc(func(l *core.Limits) { l.DecodedStreamBytes = n })
}

// WithMaxDecodedContentBytes caps the total decoded content one validation run
// will materialize (default 512 MB). The per-stream cap stops a single stream
// exploding; this is the only bound on a whole run, and so the knob for "one
// upload must not exhaust my process". The heaviest real document measured
// needs 218 MB.
func WithMaxDecodedContentBytes(n int64) Option {
	return optionFunc(func(l *core.Limits) { l.DecodedContentBytes = n })
}

// WithMaxObjectStreamBytes caps the aggregate decompressed size of all object
// streams in one document (default 512 MB). Object streams are the other
// compression-amplification path into a document: a small file can carry many
// containers that each inflate near the per-stream cap. The heaviest real
// document measured needs 9 MB.
func WithMaxObjectStreamBytes(n int64) Option {
	return optionFunc(func(l *core.Limits) { l.ObjectStreamBytes = n })
}

// WithMaxContentStreamBytes caps the decoded size of a single content stream or
// image sample buffer that will be scanned (default 64 MB). Larger streams are
// skipped. The largest real content stream measured is 29 MB.
func WithMaxContentStreamBytes(n int) Option {
	return optionFunc(func(l *core.Limits) { l.ContentStreamBytes = n })
}

// WithMaxICCProfileBytes caps the decoded size of an ICC profile (default
// 8 MiB). The largest real profile measured is 1.8 MB.
func WithMaxICCProfileBytes(n int) Option {
	return optionFunc(func(l *core.Limits) { l.ICCProfileBytes = n })
}

// WithMaxXMPPacketBytes caps the size of an XMP packet that the property checks
// will build a node tree for (default 4 MiB). Larger packets are still checked
// for well-formedness, which streams; only the property-value rules are skipped.
//
// Raising this is more expensive than it looks: tree construction is O(n²), so
// the worst case grows quadratically — roughly 3 s at the 4 MiB default and 12 s
// at 8 MiB. The largest real packet measured is 1.6 MB.
func WithMaxXMPPacketBytes(n int) Option {
	return optionFunc(func(l *core.Limits) { l.XMPPacketBytes = n })
}

// WithMaxCIDRangeSpan caps the number of CIDs a single /W range entry may span
// (default 65536, the size of the CID space). Without it a range such as
// [0 2000000000 500] would ask for two billion map insertions.
func WithMaxCIDRangeSpan(n int) Option {
	return optionFunc(func(l *core.Limits) { l.CIDRangeSpan = n })
}

// WithMaxRoleMapSteps caps the total /RoleMap chain-follow steps across one
// PDF/UA structure-type check (default 1<<20), bounding a quadratic blowup on a
// large hostile role map.
func WithMaxRoleMapSteps(n int) Option {
	return optionFunc(func(l *core.Limits) { l.RoleMapSteps = n })
}

// WithMaxTableGridFills caps the number of grid slots the PDF/UA table checks
// will fill for one table (default 1<<24), bounding a cell whose /RowSpan and
// /ColSpan claim a multi-million-slot area.
func WithMaxTableGridFills(n int64) Option {
	return optionFunc(func(l *core.Limits) { l.TableGridFills = n })
}

// WithMaxPostScriptSteps caps the total operators one type-4 (PostScript
// calculator) function evaluation may execute (default 1<<20), bounding a
// function whose loops would otherwise not terminate usefully.
func WithMaxPostScriptSteps(n int) Option {
	return optionFunc(func(l *core.Limits) { l.PostScriptSteps = n })
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
	return optionFunc(func(l *core.Limits) { l.CmapWork = n })
}
