package pdf0

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
// because for a limit the zero value is meaningful: Limits{MaxDecodeBytes: 0}
// reads equally naturally as "no cap at all" and as "give me the default", and
// the caller cannot tell from the type which they get. With options, "unset" is
// simply "the option was never called" — there is no ambiguous value to
// document, and adding a knob later is purely additive.
//
// Values are resolved once at the public entry point (Read, ReadWithPassword,
// ParseXRefStream) into the unexported limits struct below, stored on the
// Document, and passed explicitly to the code that enforces them. Validation and
// extraction therefore inherit whatever Read was given. Because the struct
// travels by value and is never mutated after resolution, validating one
// Document from several goroutines stays safe.
//
// See docs/proposals/configurable-limits.md for the design record, including
// which limits are deliberately not configurable and the threading cost that
// justified leaving each one internal.

// Option configures a resource limit. Callers do not construct one directly;
// they call a With* function. Options are accepted by Read, ReadWithPassword and
// ParseXRefStream, and the resolved values are inherited by every validator and
// extractor that runs on the resulting Document.
type Option interface {
	apply(*limits)
}

type optionFunc func(*limits)

func (f optionFunc) apply(l *limits) { f(l) }

// Default values for every configurable limit. These are the values in force
// when a caller passes no options.
const (
	defaultMaxDecodedStreamBytes  = 100 << 20 // 100 MB
	defaultMaxDecodedContentBytes = 512 << 20 // 512 MB
	defaultMaxObjectStreamBytes   = 512 << 20 // 512 MB
	defaultMaxContentStreamBytes  = 64 << 20  // 64 MB
	defaultMaxICCProfileBytes     = 8 << 20   // 8 MiB
	defaultMaxXMPPacketBytes      = 4 << 20   // 4 MiB
	defaultMaxCIDRangeSpan        = 65536
	defaultMaxRoleMapSteps        = 1 << 20
	defaultMaxTableGridFills      = 1 << 24
	defaultMaxPostScriptSteps     = 1 << 20
	defaultMaxCmapWork            = 1 << 18
)

// limits is the resolved configuration. It is never exported and never
// constructed by a caller — only by resolveLimits — so it is free to use
// zero-means-default internally without the ambiguity that made an exported
// struct a bad idea.
type limits struct {
	decodedStreamBytes  int
	decodedContentBytes int64
	objectStreamBytes   int64
	contentStreamBytes  int
	iccProfileBytes     int
	xmpPacketBytes      int
	cidRangeSpan        int
	roleMapSteps        int
	tableGridFills      int64
	postScriptSteps     int
	cmapWork            int
}

// resolveLimits applies options over the zero struct and fills in defaults.
func resolveLimits(opts []Option) limits {
	var l limits
	for _, o := range opts {
		if o != nil {
			o.apply(&l)
		}
	}
	return l.withDefaults()
}

// defaultLimits is the configuration a caller who passes no options gets.
func defaultLimits() limits { return limits{}.withDefaults() }

// withDefaults fills any unset (zero) field with its default. It is idempotent,
// so it is safe to apply to an already-resolved struct — which is what lets a
// hand-built Document (whose limits field is the zero value) behave exactly like
// one produced by Read.
func (l limits) withDefaults() limits {
	if l.decodedStreamBytes == 0 {
		l.decodedStreamBytes = defaultMaxDecodedStreamBytes
	}
	if l.decodedContentBytes == 0 {
		l.decodedContentBytes = defaultMaxDecodedContentBytes
	}
	if l.objectStreamBytes == 0 {
		l.objectStreamBytes = defaultMaxObjectStreamBytes
	}
	if l.contentStreamBytes == 0 {
		l.contentStreamBytes = defaultMaxContentStreamBytes
	}
	if l.iccProfileBytes == 0 {
		l.iccProfileBytes = defaultMaxICCProfileBytes
	}
	if l.xmpPacketBytes == 0 {
		l.xmpPacketBytes = defaultMaxXMPPacketBytes
	}
	if l.cidRangeSpan == 0 {
		l.cidRangeSpan = defaultMaxCIDRangeSpan
	}
	if l.roleMapSteps == 0 {
		l.roleMapSteps = defaultMaxRoleMapSteps
	}
	if l.tableGridFills == 0 {
		l.tableGridFills = defaultMaxTableGridFills
	}
	if l.postScriptSteps == 0 {
		l.postScriptSteps = defaultMaxPostScriptSteps
	}
	if l.cmapWork == 0 {
		l.cmapWork = defaultMaxCmapWork
	}
	return l
}

// objStmMaxRaw bounds one object stream's decompressed (index + bodies) size on
// the write side. It derives from the decoded-stream cap rather than being its
// own knob: a container whose decompressed size exceeds what the reader accepts
// would be written and then rejected on the next read, silently losing every
// object it holds. Setting the two independently is how that happens, so the
// writer follows the reader by construction. Half the cap leaves generous margin
// for the index header.
func (l limits) objStmMaxRaw() int { return l.decodedStreamBytes / 2 }

// lim returns the resolved limits for a document. Reading through this accessor
// rather than the field directly means a hand-built &Document{...}, whose limits
// field is the zero value, behaves exactly like one produced by Read.
func (d *Document) lim() limits {
	if d == nil {
		return defaultLimits()
	}
	return d.limits.withDefaults()
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
	return optionFunc(func(l *limits) { l.decodedStreamBytes = n })
}

// WithMaxDecodedContentBytes caps the total decoded content one validation run
// will materialize (default 512 MB). The per-stream cap stops a single stream
// exploding; this is the only bound on a whole run, and so the knob for "one
// upload must not exhaust my process". The heaviest real document measured
// needs 218 MB.
func WithMaxDecodedContentBytes(n int64) Option {
	return optionFunc(func(l *limits) { l.decodedContentBytes = n })
}

// WithMaxObjectStreamBytes caps the aggregate decompressed size of all object
// streams in one document (default 512 MB). Object streams are the other
// compression-amplification path into a document: a small file can carry many
// containers that each inflate near the per-stream cap. The heaviest real
// document measured needs 9 MB.
func WithMaxObjectStreamBytes(n int64) Option {
	return optionFunc(func(l *limits) { l.objectStreamBytes = n })
}

// WithMaxContentStreamBytes caps the decoded size of a single content stream or
// image sample buffer that will be scanned (default 64 MB). Larger streams are
// skipped. The largest real content stream measured is 29 MB.
func WithMaxContentStreamBytes(n int) Option {
	return optionFunc(func(l *limits) { l.contentStreamBytes = n })
}

// WithMaxICCProfileBytes caps the decoded size of an ICC profile (default
// 8 MiB). The largest real profile measured is 1.8 MB.
func WithMaxICCProfileBytes(n int) Option {
	return optionFunc(func(l *limits) { l.iccProfileBytes = n })
}

// WithMaxXMPPacketBytes caps the size of an XMP packet that the property checks
// will build a node tree for (default 4 MiB). Larger packets are still checked
// for well-formedness, which streams; only the property-value rules are skipped.
//
// Raising this is more expensive than it looks: tree construction is O(n²), so
// the worst case grows quadratically — roughly 3 s at the 4 MiB default and 12 s
// at 8 MiB. The largest real packet measured is 1.6 MB.
func WithMaxXMPPacketBytes(n int) Option {
	return optionFunc(func(l *limits) { l.xmpPacketBytes = n })
}

// WithMaxCIDRangeSpan caps the number of CIDs a single /W range entry may span
// (default 65536, the size of the CID space). Without it a range such as
// [0 2000000000 500] would ask for two billion map insertions.
func WithMaxCIDRangeSpan(n int) Option {
	return optionFunc(func(l *limits) { l.cidRangeSpan = n })
}

// WithMaxRoleMapSteps caps the total /RoleMap chain-follow steps across one
// PDF/UA structure-type check (default 1<<20), bounding a quadratic blowup on a
// large hostile role map.
func WithMaxRoleMapSteps(n int) Option {
	return optionFunc(func(l *limits) { l.roleMapSteps = n })
}

// WithMaxTableGridFills caps the number of grid slots the PDF/UA table checks
// will fill for one table (default 1<<24), bounding a cell whose /RowSpan and
// /ColSpan claim a multi-million-slot area.
func WithMaxTableGridFills(n int64) Option {
	return optionFunc(func(l *limits) { l.tableGridFills = n })
}

// WithMaxPostScriptSteps caps the total operators one type-4 (PostScript
// calculator) function evaluation may execute (default 1<<20), bounding a
// function whose loops would otherwise not terminate usefully.
func WithMaxPostScriptSteps(n int) Option {
	return optionFunc(func(l *limits) { l.postScriptSteps = n })
}

// WithMaxCmapWork caps the work spent expanding a TrueType cmap format 4
// subtable (default 1<<18). A hostile subtable can declare segments whose
// combined character ranges cover the whole code space many times over.
func WithMaxCmapWork(n int) Option {
	return optionFunc(func(l *limits) { l.cmapWork = n })
}
