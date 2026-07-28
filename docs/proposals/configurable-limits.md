# Configurable resource limits — design record

**Status:** implemented. This document records the design and the measurements
behind it; the code is in `limits.go`, and `docs/architecture.md#resource-limits`
is the user-facing description.

Two pieces of work this document scoped *out* have since been built, and both
sections are updated to record what exists rather than what was proposed:

- §6 recommended making a limit trip **observable**. Built, in
  `limits_report.go` and documented in [docs/limits.md](../limits.md). Where
  this document says trips are still silent, read §6.
- §8 recorded **cancellation** as a real gap for a separate proposal. Built, in
  `cancel.go` and documented in
  [docs/architecture.md](../architecture.md#cancellation). It needed no new
  reporting mechanism, because §6 had already built the honest channel: a
  cancelled run reports itself under the same `"limit"` rule.

pdf0 carries roughly seventy hardcoded resource guards. Every one is a fixed
number chosen by whoever added it, usually in response to a specific hostile
file. That is the right default posture for a library that parses untrusted
input, but it left a caller with no way to say "this deployment is stricter than
yours" or "this document is legitimate, let it through".

Eleven of them are now settable. The guiding constraint from the maintainer:

> if we are going to add any limits I want them to be optionally configurable
> and have sane defaults.

and, on scope:

> once you have the functional options there is little benefit in restricting
> it to two.

The measurements throughout come from the veraPDF corpus (2,907 files) and a
978-file Common Crawl sample; 3,845 of 3,885 files parsed. They are collected in
[the headroom table](#measured-headroom).

## What was built

- `Option`, `limits`, `resolveLimits`, `defaultLimits` and the eleven `With*`
  functions in `limits.go`.
- `Read`, `ReadWithPassword` and `ParseXRefStream` gained `opts ...Option`.
  Every existing call site compiles and behaves unchanged. (`ReadContext` and
  `ReadWithPasswordContext`, added later by §8's work, take them too.)
- `Document.limits`, read through `(*Document).lim()`.
- Two defaults raised (§4); nine limits left internal with the cost that
  justified each (§5, Group D).
- `limits_test.go` covers zero-value safety, per-option isolation, per-document
  independence, `-race` concurrent validation, and leaf enforcement.

Four package-level `var`s existed *only* so tests could lower them
(`maxDecodedContentTotal`, `maxObjStmDecompressedTotal`, `xmpPropertyMaxBytes`,
`objStmMaxRaw`). All four are gone: those tests now go through the public option
path, which is a better test than mutating a global.

**Not** in scope for *this* change: what happens when a limit trips. That was
built separately, and landed alongside it — `limits_report.go`, the `"limit"`
rule identifier, `IsCheckerFinding`, and the false positives that observability
exposed. See §6 and [docs/limits.md](../limits.md). The two halves meet in one
place only: a trip message names the bound that fired and says whether it was
pdf0's default or a value the caller chose (`limitBound`), because those call
for different responses.

---

## 1. Public API shape: variadic functional options

```go
// Existing signatures gain a variadic parameter. Every current call site
// compiles unchanged; no parallel ReadWithOptions is needed.
func Read(r io.ReaderAt, size int64, opts ...Option) (*Document, error)
func ReadWithPassword(r io.ReaderAt, size int64, password string, opts ...Option) (*Document, error)
func ParseXRefStream(stream *Stream, opts ...Option) (*XRefTable, error)

// Option is opaque. Callers never construct one; they call a With* function.
type Option interface{ apply(*limits) }

type optionFunc func(*limits)

func (f optionFunc) apply(l *limits) { f(l) }

func WithMaxDecodedStreamBytes(n int) Option {
	return optionFunc(func(l *limits) { l.maxDecodedStreamBytes = n })
}
```

Usage:

```go
doc, err := pdf0.Read(r, size,
	pdf0.WithMaxDecodedStreamBytes(8<<20),
	pdf0.WithMaxDecodedContentBytes(64<<20),
)
```

### Why not an exported `Limits` struct with zero-value-means-default

Because for a *limit*, zero is a meaningful value. `Limits{MaxDecodeBytes: 0}`
reads equally naturally as "no cap at all" and as "give me the default", and the
caller has no way to tell from the type which one they get. Escaping the
ambiguity requires `*int` fields or magic sentinels, both of which are worse
than the problem they solve, and both of which have to be re-explained in the
godoc of every field forever.

With functional options, "unset" is simply "the option was never called". There
is no ambiguous value to document, and adding a knob later is purely additive:
a new `With*` function, no struct field whose zero-value semantics have to be
retrofitted onto existing callers' understanding.

This is settled; the rest of this document assumes it.

---

## 2. The real question: how the resolved value reaches the enforcement point

Options resolve **once**, at the public entry point, into a plain unexported
struct:

```go
// limits is the resolved configuration. It is never exported and never
// constructed by a caller — only by resolveLimits — so it is free to use
// zero-means-default internally without the ambiguity that made an exported
// struct a bad idea.
type limits struct {
	maxDecodedStreamBytes int
	maxDecodedContentBytes int64
}

func resolveLimits(opts []Option) limits {
	var l limits
	for _, o := range opts {
		o.apply(&l)
	}
	return l.withDefaults()
}

func (l limits) withDefaults() limits {
	if l.maxDecodedStreamBytes == 0 {
		l.maxDecodedStreamBytes = defaultMaxDecodedStreamBytes // 100 << 20
	}
	if l.maxDecodedContentBytes == 0 {
		l.maxDecodedContentBytes = defaultMaxDecodedContentBytes // 512 << 20
	}
	return l
}
```

Note the asymmetry that makes this work: the *public* surface has no struct, so
the zero-value objection does not apply; the *internal* struct can then use
zero-means-default freely, because the only writer is `resolveLimits`.

That struct is stored on `Document` and passed as a parameter to the functions
that enforce limits. The open question is only how it travels down.

### The two candidate transports

**(a) Explicit parameter.** `limits` is threaded through the decode chain.
Forget it and the code does not compile.

**(b) Carried on a `context.Context`.** Retrieved with an unexported key type
and a type assertion, falling back to defaults when absent.

(b) has a genuinely good argument here, and it should not be dismissed with the
usual "context.Value is for request-scoped data" reflex. That objection is
really about *discoverability*: config hidden in a context is invisible in the
signature, so a caller cannot tell the knob exists. Under the shape in §1 that
objection does not apply — the knob is visible in `Read`'s signature, because
`Read` is where it is set. (b) also handles the no-`Document`-in-scope case
uniformly through one fallback, and if cancellation is ever added the plumbing
already exists.

The decisive objection to (b) is that limits are a **security control with a
silent failure mode**. If any internal path drops the ctx, or a new code path
added two years from now forgets to propagate it, the limit silently reverts to
the default: a caller who asked for an 8 MB cap gets 100 MB, with no compile
error, no runtime error, and nothing in the output to indicate it. The counter
— "propagate the ctx properly" — is true, but it is a discipline that has to be
maintained forever across every future code path, versus a property the
compiler enforces once and for free.

Cost is a secondary point but worth recording: `context.Value` is a linked-list
walk per lookup. That is irrelevant once per stream in `flateDecode`, and
unacceptable in a per-token loop. If a limit is ever added inside
`tokenizeContent` or the MQ decoder inner loop, (b) is not an option for it and
the value would have to be hoisted out of the loop anyway.

### Deciding on the count, not in the abstract

The threading burden is only worth paying if it is small. **The threshold: if
the enforcement chain reaches four or five signatures that each bottom out in a
`*Document` method within a hop or two, (a) is clearly right. If it were twenty
signatures scattered across the image codecs with no common owner, (b) would
start to win.** Traced against the current tree:

Functions that would gain a `limits` parameter:

| # | function | file | has `*Document` today? |
|---|----------|------|------------------------|
| 1 | `flateDecode(data []byte)` | xref.go:504 | no (leaf) |
| 2 | `lzwDecode(data []byte, earlyChange int)` | filters.go:28 | no (leaf) |
| 3 | `applyFilter(name, data, parms)` | xref.go:316 | no |
| 4 | `decodeStreamData(stream *Stream)` | xref.go:282 | no |
| 5 | `parseObjStmIndex(stream *Stream)` | objstm.go:52 | no |
| 6 | `decodeImageSamples(st *Stream)` | imageextract.go:444 | no |
| 7 | `getICCProfileData(stream *Stream)` | pdfa.go:4436 | no |
| 8 | `parseXMPProperties(data []byte)` | xmp.go:198 | no |
| 9 | `ParseXRefStream(stream *Stream)` | xref.go:143 | no — **and exported** |

Direct call sites of `decodeStreamData`/`applyFilter`, and whether a
`*Document` is already in scope at each:

| site | enclosing function | `*Document` in scope? |
|------|--------------------|------------------------|
| xref.go:213 | `ParseXRefStream(stream *Stream)` | no (exported entry point) |
| objstm.go:65 | `parseObjStmIndex(stream *Stream)` | no |
| xmp_schemas.go:1134 | `checkXMPWellFormed(doc, level)` | yes |
| final_rules.go:505 | `checkEmbeddedPDFA(doc, level)` | yes |
| imageextract.go:445 | `decodeImageSamples(st *Stream)` | no |
| imageextract.go:506 | `ccittEncodedAndParams(d *Document, ...)` | yes |
| imageextract.go:549, :558 | `jbig2EncodedAndGlobals(d *Document, ...)` | yes |
| pdfa.go:843 | `checkOutputIntentProfile(doc, level)` | yes |
| pdfa.go:4955 | `decodeContentStream(doc, stream)` | yes |

The three unexported functions with no `*Document` all resolve in exactly one
hop — every one of their own callers is a `*Document` method:

- `parseObjStmIndex` ← `(*Document).materializeScannedObjStms`, `(*Document).loadCompressedObjects`
- `decodeImageSamples` ← `(*Document).extractImage`, `(*Document).stencilMask`, `(*Document).decodeAlphaMask`
- `getICCProfileData` ← `checkOutputIntentProfile(doc, ...)`

**So the ripple is nine signatures and about fifteen call sites, with no
function more than one hop from a `*Document`.** That is larger than a first
glance suggests but well inside the threshold, and it is a one-time mechanical
change. **Recommend (a), the explicit parameter.**

This chain is the *most expensive* limit on the menu, not a typical one. Most
of the limits proposed in §5 are enforced inside a function that already holds a
`*Document` and cost nothing to reach. The chain above is priced here because
the transport decision has to be made against the worst case, not the average
one — and it survives the worst case.

### Designing so the transport can change later

The enforcement sites must read a plain struct value, never reach for the
transport themselves. Concretely:

```go
// GOOD — flateDecode knows nothing about where lim came from.
func flateDecode(data []byte, lim limits) ([]byte, error) {
	limited := io.LimitReader(r, int64(lim.maxDecodedStreamBytes)+1)
	...
}

// BAD — bakes the transport into the leaf.
func flateDecode(ctx context.Context, data []byte) ([]byte, error) {
	lim := limitsFrom(ctx) // now every enforcement site depends on ctx
	...
}
```

If (b) ever wins later, the change is confined to the *callers*: they do
`lim := limitsFrom(ctx)` once and pass the same struct down. No enforcement site
is touched. This is the property that makes the decision reversible, and it is
worth the small discipline of never passing a `context.Context` below the
functions listed in the table above.

### `ParseXRefStream`: the one exported casualty

It is exported and takes only a `*Stream`. The variadic form solves it exactly
as it solves `Read`:

```go
func ParseXRefStream(stream *Stream, opts ...Option) (*XRefTable, error)
```

Existing callers compile unchanged and get the defaults; `(*Document)` passes
its own resolved value through.

---

## 3. What happens when there is no `Document`

Two distinct cases, and they want different answers.

**A caller hand-builds a `Document`.** `&Document{Objects: ...}` then
`ValidatePDFA(doc, level)`. The `limits` field is the zero struct. Never read it
directly; read it through an accessor that re-applies defaults:

```go
func (d *Document) lim() limits {
	if d == nil {
		return defaultLimits()
	}
	return d.limits.withDefaults()
}
```

Because `withDefaults` is idempotent and a resolved struct never contains a zero
field, this is correct for both a `Read`-produced document and a hand-built one,
with no `configured bool` flag to keep in sync.

**A helper is called directly, outside any `Read`.** These are exactly the
exported entry points, which get their own `opts ...Option` (see
`ParseXRefStream` above). Unexported helpers cannot be in this case once the
threading is done — that is the compile-time guarantee (a) buys.

Note this is a real advantage over (b): here "no configuration in scope" is a
handful of *named, enumerable* entry points that each resolve their own, rather
than an open-ended set of code paths that might or might not have propagated a
value.

---

## 4. The defaults

Separate question from exposure, and settled first: two numbers were simply
wrong on the evidence. Both were changed.

### Measured headroom

How close real files come to each limit. Max observed across 3,845 parsed files
from both corpora; nothing tripped anything.

| limit | value | max observed | % |
|-------|-------|--------------|---|
| `maxICCProfileSize` | 2 MiB | 1,829,093 | **87.2%** |
| `xmpPropertyMaxBytes` | 2 MiB | 1,639,865 | **78.2%** |
| `maxContentStreamSize` | 64 MB | 29,327,130 | 43.7% |
| `maxDecodedContentTotal` | 512 MB | 228,331,568 | 42.5% |
| `maxDecodeSize` | 100 MB | 29,327,130 | 28.0% |
| `objStmMaxRaw` | 50 MB | 9,225,466 | 17.6% |
| `maxPageTreeDepth` | 64 | 4 | 6.3% |
| `maxObjStmDecompressedTotal` | 512 MB | 9,225,466 | 1.7% |
| `maxParseDepth` | 1000 | 6 | 0.6% |

### Changed

**`maxICCProfileSize`: 2 MiB → 8 MiB** (now `defaultMaxICCProfileBytes`). At 87.2% utilisation this is one
slightly-fatter profile away from silently returning nil and dropping the ICC
rules for that file. The cost of raising it is bounded and linear — an ICC
profile is read once and scanned, not expanded — so 4x headroom is cheap
insurance.

**`xmpPropertyMaxBytes`: 2 MiB → 4 MiB, not 8** (now `defaultMaxXMPPacketBytes`). The asymmetry is deliberate,
because unlike ICC this limit guards a superlinear cost. Its own comment records
the shape:

> Building the tree is O(n²) […] A 14 MB packet took ~37 s.

Extrapolating on that curve: the 1.64 MB packet actually observed costs ~0.5 s,
4 MiB costs ~3 s, 8 MiB costs ~12 s. 4 MiB buys 2.5x headroom for a worst case
still inside a few seconds; 8 MiB does not.

That comment also contains a claim the measurements now falsify:

> Real-world XMP is tiny — the largest in the veraPDF corpus is 66 KB — so this
> bound is orders of magnitude above any legitimate packet

The largest in the veraPDF corpus is indeed ~66 KB. The largest in the 978-file
Common Crawl sample is **1,639,865 bytes — 25x that**, and 78% of the cap. The
conformance corpus was not a guide to the real-world tail. The sentence has been
struck and replaced with the measured figure.

Both raises are behaviour changes, so the full corpus suite was re-run: every
ratchet holds — `falsePositives=0 missed=0 parseErrors=0` across all suites,
2,896 files parsed with 0 failures, Arlington conformant-with-finding 5 at
baseline 5.

### Why this is an argument for exposure, not a substitute for it

Both corpora are small and neither samples the tail: 2,907 files are
*conformance fixtures*, deliberately small and hand-built, and 978 web files is
a thin sample of a population in the billions. `xmpPropertyMaxBytes` is the
worked example — one corpus said 66 KB, adding a second corpus multiplied the
observed maximum by 25. A third would plausibly move it again.

So the raises above are *informed* but not *conclusive*. Picking a better fixed
number narrows the gap; it cannot close it, because there is no sample size at
which a fixed number becomes right for every caller. That is exactly the case
for making them settable as well as raising them.

---

## 5. Which knobs to expose

### The organising principle: the cost is plumbing, not API

The instinct to expose as few options as possible is a reflex borrowed from
libraries where each option is a branch in the implementation. That is not the
situation here. Once the mechanism in §1 exists, an additional option is a
`With*` function and a struct field — a few lines, no new control flow, no new
interaction between options because limits are independent scalars.

What actually costs something is **getting the value from the entry point to the
enforcement site**. That cost is wildly unequal between limits, and it is not
visible from the limit's name or its value. `maxCIDRange` is enforced inside a
function that already takes a `*Document`; `maxJBIG2Pixels` is enforced inside a
free function reached from eighteen call sites across six files in the middle of
a codec. Those are not the same proposal, and pricing them the same is what
produces either an over-austere list or an over-ambitious one.

So the list below is grouped by **threading cost**, with the count for each.
Groups A, B and C were built — eleven options. Group D was left internal, on
cost.

### Group A — free: the enforcement site already has a `*Document`

Zero signature changes. The site swaps `maxFoo` for `d.lim().maxFoo`. **Built.**

| limit | enforced in | hops |
|-------|-------------|------|
| `maxDecodedContentTotal` | `decodeContentStream(doc, stream)` (pdfa.go:4939) | 0 |
| `maxObjStmDecompressedTotal` | `(*Document).materializeScannedObjStms`, `(*Document).loadCompressedObjects` | 0 |
| `objStmMaxRaw` | `(*Document).buildWriteSet` (objstm_write.go:71) | 0 |
| `maxCIDRange` | `parseCIDWidths(doc, wObj)` (fonts.go:1760) | 0 |
| `maxRoleMapWork` | `(*Document).checkUARoleMapIntegrity` (pdfua.go:408) | 0 |

```go
func WithMaxDecodedContentBytes(n int64) Option   // 512 MB — whole-run content budget
func WithMaxObjectStreamBytes(n int64) Option     // 512 MB — whole-run ObjStm budget
func WithMaxCIDRangeSpan(n int) Option            // 65536 — CIDs one /W range may span
func WithMaxRoleMapSteps(n int) Option            // 1<<20 — /RoleMap chain-follow steps
```

`objStmMaxRaw` is the exception in this group and got **no** option. It is a
write-side constraint whose only job is to keep pdf0's output readable by pdf0's
reader, so it *derives* from the effective decoded-stream cap rather than varying
independently: setting the two inconsistently produces files that write cleanly
and then fail to read. It is now the method `limits.objStmMaxRaw()` returning
`decodedStreamBytes / 2`, so lowering `WithMaxDecodedStreamBytes` lowers both
together. `TestObjStmMaxRawDerivesFromDecodedStreamLimit` pins it, and
`TestObjectStreamSplitBudget` now forces a container split through the public
option instead of by mutating a package `var`.

Four options, no plumbing.

### Group B — one or two hops, one or two signatures. **Built.**

| limit | enforced in | reached from | signatures | call sites |
|-------|-------------|--------------|-----------|-----------|
| `maxContentStreamSize` | `decodeContentStream(doc,…)` **and** `decodeImageSamples(st)` | 3 `*Document` methods | 1 | 3 |
| `maxICCProfileSize` | `getICCProfileData(stream)` (pdfa.go:4437) | 4 `*Document` functions | 1 | 5 |
| `xmpPropertyMaxBytes` | `parseXMPProperties(data)` (xmp.go:198) | `checkXMPProperties(doc,…)` | 1 | 1 |
| `maxGridFills` | `gridDefects(rows)` (pdfua_tablegrid.go:178) | `(*Document).checkUATableGrid` | 1 | 1 |
| `maxPSSteps`, `maxStack` | `psExec`, `psApply` (function_ps.go:179, :209) | `(*Document).evalType4` | 2 | ~5 |
| `maxCmapFormat4Work` | `parseCmapSubtable(b)` (fontprog.go:230) | `parseSFNT` ← `loadFontProgram(doc, fd)` | 2 | 2 |

```go
func WithMaxContentStreamBytes(n int) Option      // 64 MB  — per-stream scan cap
func WithMaxICCProfileBytes(n int) Option         // → 8 MiB after §4
func WithMaxXMPPacketBytes(n int) Option          // → 4 MiB after §4
func WithMaxTableGridFills(n int64) Option        // 1<<24  — PDF/UA grid slots
func WithMaxPostScriptSteps(n int) Option         // 1<<20  — type-4 function operators
func WithMaxCmapWork(n int) Option                // 1<<18  — cmap segment expansions
```

Two of these deserve a note.

**`maxPSSteps` was cheaper than it looked.** `psExec` and `psApply` already
threaded a `steps *int` counter through the recursion — the budget travelled,
only the *limit* it was compared against was a constant. Widening `steps *int` to
a `psBudget{steps, max}` struct carrying both was mechanical and confined to
function_ps.go, entered at `(*Document).evalType4`. This is the one place where
reading the existing code before pricing the work changed the estimate
materially.

**`maxCmapFormat4Work` is two hops but a straight line.** `parseCmapSubtable` ←
`parseSFNT` ← `loadFontProgram(doc, fd)`, one call site each. This is the
limit whose failure mode is silent truncation of a font's character map (see
§6), which is an argument for reaching it even at two hops. It shipped as
`WithMaxCmapWork`, not `WithMaxCmapFormat4Work`: the format-4 and format-12
budgets were collapsed into one on the measurement in
[fonts.md](../fonts.md#why-formats-4-and-12-share-one-budget), so the option —
and the `cmap-work` guard identifier a trip is reported under — names the work,
not one of the two formats that charge it.

### Group C — the decode chain. **Built.**

`maxDecodeSize`, priced in §2: **9 signatures, ~15 call sites**, no function more
than one hop from a `*Document`.

```go
func WithMaxDecodedStreamBytes(n int) Option      // 100 MB — the bomb ceiling
```

Much the largest single piece of plumbing, and the option most worth having: it
is the decompression-bomb ceiling, it applies to every stream in the file, and
with 3.6x headroom the realistic use is *lowering* it for an untrusted-upload
endpoint. Built.

Threading it is what made three Group B entries free — see
[the overlap](#the-groups-overlap-which-changes-the-ordering).

### Group D — deep in a codec. **Left internal, on cost.**

**The JBIG2 budgets** (`maxJBIG2Pixels`, `maxJBIG2TotalPixels`,
`maxJBIG2GrayCells`). The decoder is entered at `decodeJBIG2(globals, data, w, h)`
from `(*Document).extractImage` — one hop — but the enforcement is not at the
entry:

- `(*jbig2Decoder).reserve` and `(*jbig2Decoder).readHalftoneRegion` are
  *methods*, so a `limits` field on `jbig2Decoder` reaches them for **1 struct
  field + 1 parameter on `decodeJBIG2` + 1 call site**. Cheap.
- `newJBBitmap` is a *free function* carrying its own independent
  `maxJBIG2Pixels` check, called from **18 sites across 6 files**, of which 9
  (`decodeGenericMMR`, `decodeGrayScaleMMR`, `decodeGenericInto`,
  `decodeRefinement`, `decodeAggregateArith`, `decodeAggregateHuff`,
  `readUncompressedBitmap`, `(*mmrPlaneReader).plane`, `(*jbBitmap).subregion`)
  have no `*jbig2Decoder` receiver to hang the value on.

So the honest options are: thread it fully (**~10 signatures, ~21 call sites**,
all inside a codec, touching the MQ-decoder inner paths), or take the cheap
version and accept that the option can only ever *lower* the effective cap,
because `newJBBitmap`'s compiled-in check remains an absolute backstop.

A lower-only knob is a documentation trap. **All three stay internal**, to be
revisited only if a real file is reported that the 64 Mpx per-bitmap cap rejects.
This is a cost judgement, not a taste one — if `newJBBitmap` took a decoder
receiver, this would have been a Group B entry and would have shipped.

**`maxTokenGap`** (lexer.go:191). Would become a field on `Lexer`, which is
constructed at 8 sites across 5 files including two exported constructors. It is
also checked in `skipWhitespaceAndComments`, i.e. the tokenizer's hot path,
where an `alloc_space` profile of a PDF/UA run already puts `tokenizeContent` at
44.5% of allocated bytes. Not worth perturbing for a limit no real file
approaches. Internal.

**Every depth cap**: `maxParseDepth` (1000; real files reach 6),
`maxPageTreeDepth` (64; real files reach 4), `maxSerializeDepth`,
`maxCompareDepth`, `maxFunctionDepth`, `maxFieldTreeDepth`, `maxTextFormDepth`,
the 64-hop `Resolve` bound. These are not resource budgets in the same sense —
they stop a crafted file exhausting the goroutine stack, which is an
**uncatchable** fatal error. Raising one trades a clean error for a process
abort. Lowering one buys nothing measurable. This is the one group where the
"nobody has a legitimate reason to set this" argument genuinely holds. Internal.

**The Annex C `implLimits` set** (`nameLen`, `stringLen`, `dictEnt`, `arrayLen`,
`nesting`, `realLimit`). ISO 19005 spec constants. Changing them changes
validation *verdicts* — a conformance question, not a resource question. They
belong to the level, not the caller. Internal.

### Summary of the menu, and what it actually cost

| group | options | estimated plumbing | built |
|-------|---------|--------------------|-------|
| A | 4 | none | yes |
| B | 6 | 8 signatures, 17 call sites | yes |
| C | 1 | 9 signatures, ~15 call sites | yes |
| D | 0 | ~10 signatures inside a codec | no — left internal |

**Eleven options.** The estimate for the B+C union was 14 signatures (the groups
overlap; see below). The actual change touched **14 unexported signatures**, plus
**3 exported ones that gained `opts ...Option`** — source-compatible, so no call
site needed updating — plus one new unexported `parseXRefStream` split out of the
exported wrapper:

| | |
|---|---|
| exported, variadic (compatible) | `Read`, `ReadWithPassword`, `ParseXRefStream` |
| decode chain | `readDocument`, `parseXRefStream`, `decodeStreamData`, `applyFilter`, `flateDecode`, `lzwDecode`, `parseObjStmIndex`, `decodeImageSamples`, `getICCProfileData` |
| Group B leaves | `parseXMPProperties`, `gridDefects`, `parseCmapSubtable`, `parseSFNT`, `psExec`, `psApply` |
| Group A | *none* — all five sites already had a `*Document`, as predicted |

The estimate held. The two functions it missed were `readDocument` (an internal
entry point, not an enforcement site) and the `parseXRefStream` split needed to
give the exported wrapper somewhere to resolve options into.

### The groups overlap, which changes the ordering

Three of Group B's six entries are enforced in functions that **already appear
in Group C's table** — they are on the flate chain because they call
`decodeStreamData`:

| Group B entry | enforced in | also in Group C's chain? |
|---------------|-------------|--------------------------|
| `maxICCProfileSize` | `getICCProfileData(stream)` | yes (§2 row 7) |
| `xmpPropertyMaxBytes` | `parseXMPProperties(data)` | yes (§2 row 8) |
| `maxContentStreamSize` | `decodeImageSamples(st)` | yes (§2 row 6) |

So the costs are not additive, and the order matters:

- **Group C first:** those three functions gain a `limits` parameter for
  `maxDecodeSize` anyway, and the three Group B options then cost **zero**
  additional signatures — just another field read. Group B drops from 8
  signatures to 5.
- **Group B first:** the same three signatures get changed, and Group C later
  finds three of its nine already done.

Either way the union is **14 signatures, not 17**. If the whole menu is on the
table, do C first: it is the only entry whose plumbing subsidises others.

Built in that sequence: **defaults (§4) → A → C → B**, with D and the depth caps
staying internal. Doing the defaults first meant the behaviour change was
verified against the corpus on its own, before any API existed to confuse the
attribution.

---

## 6. Separately: silent limits should be observable

**Status: built.** This section is kept as the argument that motivated the work;
[docs/limits.md](../limits.md) is the description of what exists. The shape
chosen was the second of the two below — a distinguished class of finding — and
building it turned up four false positives that no amount of tunability would
have fixed, which is the strongest evidence for the argument that follows.

This recommendation is **independent of tunability** and should not be folded
into the options work. It applies whether or not a limit is ever exposed.

Several limits do not fail loudly. They skip, truncate, or return a partial
result, and validation then continues and reports a clean verdict:

(The left column names each limit as it was called when this was written; the
option that now configures it is in parentheses, and the guard identifier a trip
is reported under, where one is, is in [docs/limits.md](../limits.md).)

| limit | what silently happens |
|-------|----------------------|
| `maxContentStreamSize` (`WithMaxContentStreamBytes`) | stream is not scanned at all |
| `maxDecodedContentTotal` (`WithMaxDecodedContentBytes`) | remaining content treated as undecodable |
| `maxCmapFormat4Work` (`WithMaxCmapWork`) | character map **truncated** mid-parse |
| `maxRoleMapWork` (`WithMaxRoleMapSteps`) | partial `/RoleMap` result |
| `maxGridFills` (`WithMaxTableGridFills`) | **all** table-grid defects suppressed for that table |
| `maxICCProfileSize` (`WithMaxICCProfileBytes`) | returns nil; ICC rules drop out |
| `xmpPropertyMaxBytes` (`WithMaxXMPPacketBytes`) | property checks skipped entirely |
| `maxJBIG2*` (not exposed) | image reported as unsupported |

A validator that stops checking and still says "conformant" is worse than one
that errors, because the caller has no way to know the difference between "this
file is fine" and "I gave up".

This is not hypothetical here. The repo's own history records it happening —
`maxContentStreamSize`'s comment:

> The previous 1 MB cap (and Flate-only, no-filter-array decoding) **silently hid
> ordinary content from every scanner** — an oversize or `[/FlateDecode]`-wrapped
> stream full of DeviceRGB validated clean.

A false-clean PDF/A verdict on a file that plainly used device colour. It was
found by someone eventually noticing; an observability signal would have
surfaced it the first time it happened, and would have pointed straight at the
cap rather than at the colour rules.

Shape, deliberately not specified in detail here because it interacts with the
existing `ValidationError` / `UAViolation` types:

- a `Truncated []string` (or similar) on the validation result naming each check
  that stopped early and why; **or**
- a distinguished finding severity — "not checked" as a third state alongside
  pass and fail.

The second is more honest and more invasive. Either way the requirement is that
a caller can distinguish *checked and clean* from *not checked*, and that the
budget which stopped it is named.

**What was built:** the second, in the shape the existing types already
supported. A trip becomes an ordinary finding under the reserved rule identifier
`"limit"` (a sibling of `"internal"`, used for a recovered panic), so it flows
through every validator's existing return type with no new API surface;
`IsCheckerFinding` is the exported predicate that separates the two reserved
identifiers from real conformance findings. The message names the guard and the
bound it tripped on. The mechanism is `limits_report.go`; the per-guard
classification, including the guards deliberately left silent and why, is in
[docs/limits.md](../limits.md).

Note the interaction with §5: once a limit is both tunable and observable, a
caller who hits one has a diagnosis **and** a remedy. Observability without
tunability leaves them stuck; tunability without observability means they never
learn they need it. Observability is the more important half, and the one worth
doing even if no option ships.

---

## 7. Rejected alternative: package-level `var`s

The shape that makes the whole threading problem in §2 disappear:

```go
var MaxDecodeSize = 100 << 20 // caller assigns directly
```

`flateDecode` reads the global. No signature changes, no `Document` field, no
accessor, nothing to thread. Four limits are already unexported `var`s for
exactly this reason (`maxObjStmDecompressedTotal`, `maxDecodedContentTotal`,
`xmpPropertyMaxBytes`, `objStmMaxRaw`), each carrying a comment saying it is a
`var` only so tests can lower it.

It loses on concurrency, and specifically on a property pdf0 supports and tests:
validating one `Document` from several goroutines
(`TestValidateConcurrentSameDoc`). With package-level vars:

- Two goroutines validating two different documents with different limits cannot
  both get what they asked for. There is one value.
- Assigning a limit while another goroutine is validating is a data race in the
  strict sense — unsynchronized concurrent read and write of an `int`. `-race`
  will flag it, and it is genuinely undefined, not merely untidy.
- A library setting the global on behalf of its caller silently changes the
  behaviour of every other user of pdf0 in the same process. That is exactly the
  global-mutable-state failure that makes this shape unacceptable in a library
  as opposed to an application.

The existing four `var`s are tolerable only because they are unexported and
lowered by tests that do not run concurrently with the concurrency test.
Exporting them would make that an API promise.

Rejected.

---

## 8. `context.Context`

### As a config carrier: no

Covered in §2 as transport (b) and rejected there. The short version for anyone
arriving at this section directly: it is not primarily a discoverability
problem, because under the functional-options shape the knob *is* visible in
`Read`'s signature. It is that limits are a security control and `context.Value`
degrades silently — a dropped or unpropagated ctx reverts to the default with no
compile error and no runtime signal. An explicit parameter makes that failure
mode impossible rather than merely discouraged.

Please do not re-propose it without new information; the tradeoff is recorded
above in full.

### For cancellation: a real gap — since closed

**Status: built.** This section is kept as the argument that scoped it out of
*this* change; the design record is `cancel.go`, the user-facing description is
[architecture.md](../architecture.md#cancellation), and what a cancelled run
reports is in [limits.md](../limits.md). Every prediction below held, and the
one open API question is answered at the end.

pdf0 offered a caller, when this was written, **no way to stop work in
progress**. The evidence is in the project's own tooling — `cmd/corpusprobe`
needs a per-file timeout and the best it could do was:

```go
select {
case res := <-ch:
	return outcome{res.kind, res.detail, path}
case <-time.After(perFileTimeout):
	return outcome{"timeout", "", path}
}
```

That abandons the goroutine. The work keeps burning CPU and holding memory until
it finishes on its own; only the *waiting* is bounded. If pdf0's own harness
cannot do better, neither can a caller with a request deadline.

This is worth fixing, and it is deliberately not folded into this proposal. The
split is not arbitrary — the two are different in kind and about an order of
magnitude apart in surface:

- **Limits threading** reaches nine signatures in one decode chain (§2), all
  within a hop of a `*Document`.
- **Cancellation** has to reach every long-running loop: the content walkers,
  each validator's check loop, the JBIG2 and CCITT inner loops, the xref scan.
  There is no single chain.
- Cancellation raises its own API question that limits do not — whether to add
  ctx-first variants (`ReadContext`, `ValidatePDFAContext`), which is Go's usual
  answer and which the variadic-options trick does *not* solve, since a ctx
  belongs first in the signature by convention.
- It has a hot-loop cost. A ctx check in `tokenizeContent`'s inner loop is not
  free, so it would be every N iterations, which means choosing N and
  documenting the resulting granularity.

Recommend tracking it as its own proposal.

**What was built, against those four predictions.** `cancel.go`, and a `…Context`
variant of every entry point whose cost is the document's — fourteen of them.

- *No single chain:* correct. The signal rides on the per-run `validationCache`
  (`limits_report.go`) so a check deep in unexported code reads it off the
  `*Document`; `Read` and `Write`, which have no run, thread a `canceler`
  parameter down their loops instead.
- *The API question:* answered `…Context` variants, not `WithContext(ctx)`. An
  `Option` is *stored* on the `Document` and inherited by every later call, which
  is the right lifetime for a limit and the wrong one for a context, and it would
  make cancellation invisible at the call site. So the two mechanisms stay
  separate, exactly as §2's decisive objection to transport (b) implied they
  would: **a limit says what this document may cost, a context says how long this
  operation may take.**
- *The hot-loop cost:* real, and N was chosen as a byte count rather than an
  iteration count — `cancelScanBytes`, 1 MiB of scan position, plus
  `cancelReadChunk` inside flate and LZW. On a 71 MB file cancellation takes
  effect in ~60 ms with PDF/A and PDF/UA timings indistinguishable from before.
- *Reporting:* a cancelled run is the same event as a tripped guard — the
  checker stopped before it had seen everything — so it is reported through §6's
  machinery, under the same `"limit"` rule with the guard identifier
  `context-canceled`. This is why the two halves of this document turned out to
  be three: §5 made the ceilings settable, §6 made a trip visible, and
  cancellation needed nothing new because §6 had already built the honest channel.

The one entry-point asymmetry it left: `ValidateFacturX` and `ValidateOrderX`
have no variant, because their findings are `formalis.Violation` values that
`IsCheckerFinding` cannot classify, so there is nowhere honest to report the
cancellation. Recorded in
[architecture.md](../architecture.md#which-entry-points-have-one).

---

## 9. Summary — as built

**Public API.** Variadic functional options on existing constructors:
`Read(r, size, opts ...Option)`, `ReadWithPassword(..., opts ...Option)`,
`ParseXRefStream(stream, opts ...Option)`. No exported `Limits` struct, no
`ReadWithOptions`, no zero-value-means-default in a public type. Every existing
call site compiles and behaves unchanged.

**Transport.** Options resolve once into the unexported `limits` struct, stored
on `Document`, passed as an explicit parameter to the enforcement sites. Cost:
14 unexported signatures, 3 exported ones made variadic. Enforcement sites read
a plain struct and never reach for the transport, so swapping it later would
touch only callers.

**No `Document` in scope.** `(*Document).lim()` re-applies defaults, so a
hand-built `&Document{...}` behaves like one `Read` produced; exported helpers
take their own `opts ...Option`.

**Defaults changed** (both verified against the corpus with every ratchet
holding):

| limit | was | now | why |
|-------|-----|-----|-----|
| `maxICCProfileSize` | 2 MiB | 8 MiB | 87.2% utilisation; cost of raising is linear |
| `xmpPropertyMaxBytes` | 2 MiB | 4 MiB | 78.2% utilisation; cost is O(n²), so 4 not 8 |

The falsified "largest in the veraPDF corpus is 66 KB — orders of magnitude
above any legitimate packet" comment in `xmp.go` is struck and replaced with the
measured Common Crawl figure.

**Eleven options**, grouped by what it cost to reach the enforcement site. The
defaults below are the ones this change shipped with; `limits.go` is the source
of truth for what they are *now*, and
[architecture.md](../architecture.md#resource-limits) is the user-facing table.
The column that belongs to this document is the last one.

| option | default | group |
|--------|---------|-------|
| `WithMaxDecodedStreamBytes` | 100 MB | C |
| `WithMaxDecodedContentBytes` | 512 MB | A |
| `WithMaxObjectStreamBytes` | 512 MB | A |
| `WithMaxCIDRangeSpan` | 65536 | A |
| `WithMaxRoleMapSteps` | 1<<20 | A |
| `WithMaxContentStreamBytes` | 64 MB | B |
| `WithMaxICCProfileBytes` | 8 MiB | B |
| `WithMaxXMPPacketBytes` | 4 MiB | B |
| `WithMaxTableGridFills` | 1<<24 | B |
| `WithMaxPostScriptSteps` | 1<<20 | B |
| `WithMaxCmapWork` | 1<<18 | B |

**Not exposed, on cost:** the JBIG2 trio — `newJBBitmap` is a free function
reached from 18 sites across 6 files, 9 with no `*jbig2Decoder` receiver, so a
knob costs ~10 signatures inside a codec or can only *lower* the cap, which is a
documentation trap. Also `maxTokenGap` (8 construction sites, and it sits in the
tokenizer's hot path).

**Not exposed, on principle:** every depth cap (they guard *uncatchable* stack
exhaustion — raising one trades a clean error for a process abort) and the
Annex C `implLimits` (spec constants that change verdicts, not resources).

**Not exposed, by derivation:** `objStmMaxRaw` is now `limits.objStmMaxRaw()` =
`decodedStreamBytes / 2`. Independent settings produce files that write cleanly
and then fail to read.

**Rejected:** package-level `var`s — concurrent validation of one `Document` is
supported and tested, and the four `var`s that existed only for test overrides
are gone, their tests moved to the public option path. `context.Context` as a
config carrier — silent degradation of a security control.

**Since built, separately:** §6, making silent limit trips observable —
`limits_report.go`, the `"limit"` rule, `IsCheckerFinding`, and the four false
positives that finding them exposed. Nothing in *this* change altered what
happens when a limit trips; that change did. See
[docs/limits.md](../limits.md).

**Since built, separately again:** cancellation via `context.Context` (§8), which
this document scoped out. It is `cancel.go` plus a `…Context` variant of every
document-scale entry point, and it needed no new reporting mechanism: a cancelled
run is reported through §6's `"limit"` rule, under the guard identifier
`context-canceled`. The `Option`-vs-`ctx`-parameter question was decided the same
way §2 decided transport — a limit is configuration with the lifetime of a
document, a context is the lifetime of one operation — so the two stay separate.
See [architecture.md](../architecture.md#cancellation).

**Still open:** nothing from this document. The two knobs left internal on cost
(the JBIG2 trio, `maxTokenGap`) are to be revisited only if a real file is
reported that they reject.
