# Resource limits and what a trip means

Three documents cover resource limits, because there are three questions, and
each has exactly one home:

| Question | Read | Code |
| --- | --- | --- |
| *How do I cap what a document costs?* — the `With*` options, the defaults, which entry points take them | [architecture.md](architecture.md#resource-limits) | `limits.go` |
| *What happens when a limit trips?* — the `limit` rule, `IsCheckerFinding`, the per-guard classification | this document | `limits_report.go` |
| *Why is it shaped this way?* — the measurements, the rejected alternatives, which limits were deliberately left internal | [proposals/configurable-limits.md](proposals/configurable-limits.md) | — |

The defaults are stated in `limits.go`, which is the source of truth; the tables
elsewhere restate them for a reader who is already there.

The two meet in one place. Some of the guards below are configurable, so a
trip may be pdf0's own ceiling or the caller's; the trip message says which
(`core.LimitBound`), because "you hit the cap you set" and "you hit our default"
call for different responses.

pdf0 reads untrusted files, so roughly seventy places in the package cap the
work a document can force: work budgets, depth caps, size ceilings, hop
counters, seen-sets. Every one of them can *trip*, and when it does the checker
is left holding an incomplete result.

There are only three honest things to do with an incomplete result.

| Class | What happens | Consequence |
| --- | --- | --- |
| **Loud** | A hard error is returned. | Safe. The caller knows. |
| **Silently lossy** | The check stops and reports nothing. | **False negatives** — a bad file validates clean. |
| **Silently wrong** | The truncated result is then used *as if complete*. | **False positives** — the library accuses a conformant file. |

The third class is the dangerous one, and it is not hypothetical. Audit C46: a
format-4 cmap segment starting at code 0 was dropped, and because `simplefont.TrueTypeGlyph`
treats a non-nil cmap as authoritative, every affected code resolved to "glyph
0" — firing *"does not define a glyph referenced for rendering"* and *"references
the .notdef glyph"* on a font that was fine. The empty-map defect found by
fuzzing was the same shape: a subtable that parsed but mapped nothing returned
an empty non-nil map, so a 16-byte hostile subtable could blank a good cmap.

Both share one shape: **a budget or guard truncated a data structure, and a
downstream rule treated the truncated structure as complete.**

The rule the codebase follows is therefore:

> **A check must never assert a violation on the basis of an incomplete result.**

When a guard truncates a structure, one of three things must happen, in order of
preference:

1. the structure is made self-describing (partial), and the consumer declines
   the dependent check — the `font.ParseCmapSubtable` nil-vs-empty contract is this
   idea, and `font.Program.CmapPartial` / `parseCIDWidths`' second result extend
   it;
2. the consumer skips the dependent check because the input is known-partial;
3. the trip is reported as its own finding rather than the downstream one.

## Reporting: the `limit` rule

`limits_report.go` holds one mechanism. A `core.Recorder` lives on the run
(`core.Run`); any guard with the run in scope notes its trip there (`noteLimit`
from the root package), which is a no-op when no run is in progress. Guards with no `*Document` at all
(the sfnt/CFF parsers) record the trip on the value they return, and whoever
loads that value forwards it. Read-time trips live on `Document.readLimits`,
written only during `Read`, so validation stays non-mutating for the caller.

Every validator flushes the recorder into its own finding type under the rule
identifier `"limit"` — a sibling of the existing `"internal"` used for a
recovered panic. Both mean *the checker had a problem*, never *the file is
non-conforming*; `IsCheckerFinding` tells them apart from real findings.

A `limit` finding fires on no file in the veraPDF corpus. If you see one, the
input is adversarial or a budget needs revisiting — it is never a statement
about conformance.

Each finding names the guard that tripped, in a stable lower-case identifier a
caller can key on — the `limit*` constants in `limits_report.go`, named after
the bound rather than after the rule that was skipped, since one guard can cost
several rules. The recorder is itself bounded (`core.MaxRecordedTrips`), so
a file crafted to trip a guard once per object cannot turn the *report* into the
exhaustion the guards prevent; distinct trips past the cap are counted and
reported in aggregate under `limit-report`, which is a guard identifier like the
rest so that nothing has to special-case it.

### The one non-guard that reports here: cancellation

A caller's `context.Context` ending a run is not a resource guard, but it
produces exactly the same event — *the checker stopped before it had seen
everything* — and so calls for exactly the same honesty. It is therefore
reported through this mechanism, under the same `"limit"` rule, with the guard
identifier `context-canceled`:

> the run was cancelled before it finished (context-canceled): context deadline
> exceeded; the checks that had not yet run were skipped, so this file is
> neither confirmed conformant nor non-conformant

Giving it its own rule identifier would have meant every caller that already
distinguishes "the file is bad" from "pdf0 could not finish" learning a second
way to spell the second one. Instead `IsCheckerFinding` covers it for free, and
the trip is derived in `runLimitTrips` — one place, so no validator can forget
it.

The property this buys is the important one: **a cancelled validation never
returns an empty result.** A caller testing `len(result) == 0` for "conformant"
gets "not conformant"; a caller filtering with `IsCheckerFinding` gets
"unknown". Neither gets a clean bill of health from a run that did not look.

The reach table below extends: `Read` and `Write` report cancellation as a
returned error (the loud class), and `ExtractTextContext` /
`ExtractImagesContext` return their partial result alongside one — which is why
they exist as separate signatures rather than as `Context` variants returning
the bare value. `docs/architecture.md` covers the API shape and the check
granularity; `cancel.go` carries the design record.

### What the mechanism reaches, honestly

| Reached | Not reached |
| --- | --- |
| All PDF/A, PDF/UA, PDF/UA-2, PDF/X, PDF/VT, PDF/R and DPart checks (each installs or joins a run). | The lexer and parser (`maxTokenGap`, `maxParseDepth`): they take bytes, not a `*Document`, and threading state through them for a guard that already surfaces as a parse error would be ceremony, not reach. |
| Read-time object-stream budget trips, via `Document.readLimits`. | `ExtractImages` / `ExtractText`: they return no finding channel. Image decode failures, budget refusals (`image-pixels`) and recovered panics surface per image in `ExtractedImage.Note`; a page whose text is left out — the work budget ran out, or a recovered panic — is a `*PageTextError` in `ExtractText`'s error, never missing text alone. |
| Font-program guards, forwarded from the parsed program. | `Write` / `WriteIncremental`: these return errors, which is the loud class already. |
| Nested embedded-PDF/A validation (6.9), as `embedded-pdfa`. | `object.Equal` / `DocumentEqual`: they return a `bool`, so there is nowhere to say "too deep to tell". `maxCompareDepth` is *silently wrong by construction* (see the parsing table) and stays that way; no validator rule compares structures that deep. |
| Cancellation of any validation run, derived in `runLimitTrips`. | `ReadContext` / `WriteContext`: loud, an error wrapping `ctx.Err()`. `ExtractTextContext` / `ExtractImagesContext`: partial result plus that error. |
| `ValidateFacturXContext` / `ValidateOrderXContext`, on **both** sides of the module seam — see below. | The `Is*` detection predicates in `formalis`: they return a `bool`, which has no room to say "the run stopped", so a context there could only lie. They are bounded by that module's own limits instead. |

### Across the `formalis` seam

The two invoice containers compose two rule engines, and both honour the same
mechanism. pdf0's half reports a trip under `finding.LimitRule`; `formalis` reports one
under `formalis.RuleLimit`, and the two constants are the same string
**deliberately**, so a caller draining `res.Violations` for "the checker stopped"
has one name to look for rather than two.

The consequence that is easy to get wrong: the PDF/A-3 findings this path
composes are prefixed `pdfa-3/…` and the invoice engine's are adopted verbatim,
but a reserved checker identifier is passed through **unprefixed** either way. A
`pdfa-3/limit` — or an `invoice/limit` — is invisible to a caller keying on
`limit`, which is the one failure the reserved identifier exists to prevent.
`TestAdoptPDFAFindingsKeepsReservedRulesBare` and
`TestAdoptedLimitFindingIsACheckerFinding` pin each half.

`formalis.RuleProfile` (`"profile"`) is *not* folded into `IsCheckerFinding`,
although that module's own predicate covers it. pdf0 only ever passes a profile
it read out of the container's XMP, so the finding arises exactly when
`fx:ConformanceLevel` names nothing pdf0 could route — a container defect pdf0
has already reported as a `metadata` finding, not a report that pdf0 stopped.

`formalis` carries its own guards for the reason this package does: it parses
untrusted XML. Its inventory is short (`formalis/limits.go`) — a nesting cap that
replaced a *fatal* stack overflow, which is unrecoverable and so could never have
been caught by the `recover()` pdf0 wraps `ValidateFacturX` in; an element-count
cap; and a work budget on the VAT breakdown sums.

## The work meter: one budget for a whole run

Every guard in the inventory below bounds one unit of work: one stream's
decode, one `/W` range, one table's grid, one XMP packet. None of them bounds
the product, and the product is what a small hostile file controls: a
structure every element names, a function evaluated for every pixel, a chain
walked from every referrer, ranges that overlap two thousand times. The
2026-09-22 audit found fifteen such files (T1; C10, C17-C19, C37-C42, C50-C53,
C79, C84), each within every per-unit bound and each running for minutes, out
of memory, or off the end of the stack.

One mechanism answers all of them: a work meter on the run (`core.Meter`, in
`internal/core/meter.go`). Every walk over the object graph, every expansion,
every tokenisation, every function evaluation and every CMap or ToUnicode
lookup charges it, and a charge is also where the run's context is polled, so a
deadline stops a run inside a check rather than between checks. Text and image
extraction run under a run too, so they are metered and share the run's memo of
decoded streams and parsed CMaps.

When the budget is spent the charge that notices **unwinds the check in
progress**, and every check after it is refused. The unwound check reports
nothing — what it had found so far it found on a partial walk, which is the
"silently wrong" class this document exists to prevent — and the run reports
the trip once, under `limit` with the guard `work`. Extraction stops with an
error that matches `ErrWorkLimit`: a page left out is a `*PageTextError`, and an
image an `ExtractedImage` with `Decoded` false and a `Note`. A cancelled context
stops a run the same way and is reported as it always was
(`context-canceled`).

The budget is `WithMaxWork`; by default it is `core.DefaultMaxWork` plus
`core.DefaultWorkPerSourceByte` a byte of the file the document was read from,
because real work grows with the file and amplification does not.
`TestWorkMeterHeadroom` measures every validator and both extractors over every
corpus the suite can see and holds each run to an eighth of its file's default;
the measurement the defaults were set from is in `internal/core/meter.go`.

A budget is only half of the answer. A bounded worst case must also be rare on
legitimate input, or the meter turns slowness into a refusal: a file that used
to take twenty seconds and pass now takes twenty seconds and says `limit`. The
shape that first did so is common — pages that share their content, a template
or a stamped letterhead, each page with its own copy of the same resources —
and every rule that read content read it once per page. A 104 KB file of twenty
such pages over 50 MB of content ran 21 s at PDF/A-2b and ended in a `limit`
finding. So what is derived from content is derived once per distinct content:
a key per stream, and per distinct sequence of streams for a `/Contents` array
(`View.ContentBytesAndKey`, which memoises the concatenation per run); and,
where the answer depends on the resources the content is drawn with, once per
distinct resources *value* — `core.ResMemo` matches a resource dictionary by
`object.Equal`, and the content interpreter's memo key interns each resource
sub-dictionary by value (`core.DictInterner`). The five PDF/A rules that judge
content by its tokens alone (the content-stream limits, hexadecimal strings,
inline-image filters and entries, marked-content ActualText) share one pass per
stream (`pdfa/content_facts.go`), over each distinct content once. That file now
validates in about two seconds at any page count, with a verdict.
`TestHostileSharedContentIsReadOnce` holds every validator and text extraction
over 200 pages sharing 8 MB of content — one stream, and one array per page —
to a small multiple of one scan of that content, and to no `limit` finding; a
per-page rescan planted back into any of the memoised rules fails it.

The per-unit guards the meter measures the same thing as were folded into it
rather than kept alongside: the per-chain `/RoleMap` step budget
(WithMaxRoleMapSteps), the per-range `/W` span (WithMaxCIDRangeSpan), the
per-evaluation PostScript step budget (WithMaxPostScriptSteps), the content
interpreter's execution count and interpreted-bytes bounds, and the text
extractor's content budget. What stayed local bounds something else — memory
(`WithMaxTableGridFills`, `WithMaxCmapWork`, the decode and object-stream
caps) or stack (the depth guards) — and says so where it is declared. One
work budget stayed local too: the revision diff's, in signature verification,
which is not a run — it has no finding channel, and a spent budget is a
signature's "could not be analysed" verdict rather than a stopped run.

### Depth: one guard for every walk

A walk that recurses once per level of a structure the file controls needs a
stack frame per level, and a visited set does not help: a chain of a hundred
thousand distinct structure elements has no cycle to find, and overflowed a 64 MB
stack (C84). Every recursive walker over the object graph now either is
iterative (the structure tree, the PDF/UA structure walks, the DPM values, the
field tree, the table rows, the object-graph scan) or calls the uniform depth
guard `View.Descend`, which stops a walk deeper than `core.MaxWalkDepth` (1024)
and, inside a run, stops the run as a spent budget does, reported as
`walk-depth`. `TestRecursiveWalkersAreBounded` (`internal/lint`) finds every
recursion over object-graph types and fails for one that neither calls
`Descend` nor compares a depth parameter; the few bounded another way are
listed with the reason.

## The inventory

Guards are grouped by subsystem. "Consumer" names the rule that reads the
truncated value; the message quoted is the one a trip could wrongly emit.

### Fonts and font programs

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| cmap work budget (`WithMaxCmapWork`) | forme's `font/fontprog.go` | **Silently wrong** | `simplefont.TrueTypeGlyph` → `simpleGlyphExists` / `isNotdefGlyph`: *"embedded TrueType font does not define a glyph referenced for rendering (code N)"*, *"text showing operator references the .notdef glyph"* | Fixed. `font.Program.CmapPartial`; the glyph and .notdef rules decline for that font; trip reported as `cmap-work` (one budget, charged by both expanding subtable formats — see [fonts.md](fonts.md#why-formats-4-and-12-share-one-budget)). |
| CID `/W` range span (was WithMaxCIDRangeSpan) | `pdfa/fonts.go` | **Silently wrong** | `checkCIDFontConsistency`: a dropped `/W` range falls back to `/DW` (default 1000) and is compared against the program's real advance → *"width information for glyphs used for rendering is inconsistent"* | Gone. `/W` is no longer expanded: its entries are resolved once into disjoint CID segments (`core.ResolveSpans`) and looked up per CID shown, so a range of any width is read whole and nothing is dropped (audit 2026-09-22 C10: the span limit bounded one range, and 2,000 of them ran out of memory). |
| predefined CJK CMap (`predefined-cmap`) | `internal/core/cmap.go`, `pdfa/fonts.go`, `pdfua/pdfua.go` | **Silently skipped** | `checkCIDFontConsistency`, `checkFontSubsetCompleteness`, `checkUANotdefCID`: a font whose `/Encoding` names `UniJIS-UCS2-H` and the rest has no code-to-CID mapping here, so glyph coverage, `.notdef`, `/W` consistency and `/CIDSet` completeness cannot run. | Fixed. `core.LoadCMap`, the producer, answers `ReasonUnsupported` and reports the skip as `predefined-cmap`, once per font whichever check asked (it used to be noted by each consumer, once per check). It is not a budget — nothing can be raised to make it run, which is why its message reads *data not carried* rather than *resource limit reached*. The data itself is [#269](https://github.com/mgilbir/pdf0/issues/269). An embedded CMap that builds on another (`usecmap`, `/UseCMap`) is read, with its base: a base that is itself embedded is read in full, and one whose data is not carried is opaque, so the codes left to it decode as unknown and the skip is reported when a check first meets one — not for a CMap that defines every code the page shows (audit 2026-09-22 C75). |
| font program, CIDSet, ToUnicode over a limit | `internal/core/fontuse.go`, `pdfa/fonts.go`, `pdfua/pdfua.go` | Was **silently wrong** | A program or CIDSet over `WithMaxContentStreamBytes` came back as the same nil as a damaged one: *"embedded %s font program is damaged and could not be parsed"*, *"CIDFont subset FontDescriptor contains an empty CIDSet stream"*, *"FontDescriptor CIDSet does not list all CIDs used for rendering"* (audit 2026-09-22 C47). | Fixed. `core.LoadFontProgram`, `core.DecodeCIDSet` and the ToUnicode parsers return a `core.Reason`; only `ReasonMalformed` is damage or emptiness, a declined reason declines the check, and the producer reports the trip as `content-stream-size` (or whichever bound stopped it). |
| `font.ParseCmapSubtable` nil-on-unreadable | forme's `font/fontprog.go` | Silently lossy (deliberate) | The subtable is ignored rather than read as "maps nothing". | Unchanged; this is the contract the fix above extends. |
| ToUnicode / CMap range and entry bounds (a range over 65,536 codes, more than 65,536 mappings) | `internal/core/cmap.go`, `internal/core/cmapparse.go` | Silently lossy | Missing `toUni[cid]` *suppresses* the empty-outline rule (fail-open). | Unchanged. The section scanners are gone: every CMap program is read as a token stream (audit 2026-09-22 C75, C76, C78). The mappings are no longer expanded into maps: a ToUnicode CMap's entries and a CID CMap's ranges are resolved once into disjoint segments and looked up by binary search (C52, C53), charged to the work meter. |
| `maxTextFormDepth` | `text.go` | Silently lossy | `ExtractText` only — **no validator consumes it**. | Unchanged. |
| text extraction work (the run's work meter, `WithMaxWork`) | `text.go` | Unbounded before a form was extracted each time it is drawn (audit 2026-09-22 C87) | `ExtractText` only. Every content stream tokenized — each page's, and each form's each time it is drawn, with a floor for entering a stream — is charged to the run's work meter, which a fan-out of forms drawing forms would otherwise make exponential. It was charged against the decoded-content budget before extraction ran under a run. | **Loud**: the page and every one after it are left out and reported as `*PageTextError`, whose message names `work`. |
| sfnt/CFF/Type1 structural bails (`return nil`) | forme's `font/fontprog.go` | Loud | `damagedFontProgramError`: *"embedded %s font program is damaged and could not be parsed"* | Unchanged: a bail is `ReasonMalformed`, reported as a damaged program, which is the loud class. |

### Content scanning

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| aggregate content budget (`WithMaxDecodedContentBytes`) applied to `/Metadata` | `pdfa/pdfa.go` | **Silently wrong** | Every identification rule: *"metadata must contain pdfaid:part"*, *"pdfaid:conformance must be B, got \"\""*, *"Info /Title present but XMP dc:title missing"*, *"file is not identified as PDF/X"*, *"an embedded PDF file is not compliant with PDF/A"* | Fixed. `View.MetadataContent` / `XMPText` decode the document's own identification outside the aggregate budget. |
| any decode a check needs: per-stream decode cap, unimplemented filter, ciphertext | `internal/core/outcome.go` (`View.Decode`, `Content`, `ICCProfileData`, …) | Was **silently lossy** | Every consumer read the nil a failed decode left as "the stream holds nothing": content over 100 MB, a stream under `ASCII85Decode` or `RunLengthDecode` (both then unimplemented), Flate missing its Adler-32 — the device-colour finding in each vanished with no trip (audit 2026-09-22 C46). | Fixed. One mechanism, the typed producer outcome: every producer returns data and a `core.Reason` (ok, absent, malformed, unsupported, limit, locked, canceled), and records a declined one itself, once per object and reason, as `decoded-stream-size`, `unsupported-filter` or `not-decrypted`. A consumer may assert on `ReasonMalformed` where its rule is about malformation, and declines on the rest. `internal/lint` refuses a `Reason` discarded without a `// reason:` comment saying why that is sound. ASCII85Decode and RunLengthDecode are implemented (RunLength's 128-fold expansion is bounded before anything is allocated), indirect `/Filter` and `/DecodeParms` are resolved, and a deflate stream complete to its final block is its content whatever its Adler-32 says (see `core.FlateDecode`). |
| 256-byte token cap, not configurable (was four tokenizers; now the one content lexer) | `internal/core/lex.go` | **Silently wrong** | The cap cut a run and the scan re-entered mid-run, so a binary tail became tokens: a one-byte `k`/`g` fragment → *"DeviceCMYK used without matching OutputIntent or DefaultCMYK"*; an alphabetic fragment → *"content stream contains an operator not defined in ISO 32000"* | Fixed. `core.MaxContentTokenLen`; an over-long run is discarded whole (same single linear pass). |
| per-stream content cap (`WithMaxContentStreamBytes`) | `internal/core/outcome.go` | Silently lossy | Every content-driven rule sees nothing from the stream. This is the failure the old 1 MB cap caused. | Reported as `content-stream-size`. |
| aggregate content budget (`WithMaxDecodedContentBytes`), content proper | `internal/core/outcome.go` | Silently lossy | Same, for every stream after the budget. | Reported as `decoded-content-total`. |
| `maxQDepth` (28) | `pdfa/pdfa.go` | Loud | It *is* the rule (implementation limit), not a work cap. | Unchanged. |
| ICC profile size (`WithMaxICCProfileBytes`) | `internal/core/color.go`, `pdfa/pdfa.go`, `pdfx/pdfx.go` | Was **silently lossy**, fail-open by design | `getOutputIntentCoverage` sets `hasRGB=hasCMYK=true` on an unreadable profile precisely to avoid a false positive — and nothing said the profile had not been read, so a lowered bound turned every ICC rule off with no finding (audit 2026-09-22 C109). | Fixed. `View.ICCProfileData` reports the trip as `icc-profile-size`; the consumers still fail open. It also decodes through the full filter chain now, not Flate alone. |
| XMP packet size (`WithMaxXMPPacketBytes`), XMP nesting depth (`xmp.MaxDepth`, no knob) | `internal/core/xmp.go` (`DocumentXMPPacket`) | Was **silently lossy** | Over the cap `checkXMPProperties` read "no properties to check", and the identification scrapers read the text anyway. | Fixed (audit 2026-09-22). One model, one gate: over either bound the packet is not modelled, an `xmp-packet-size` / `xmp-depth` trip is noted (a "limit" finding), and every reader — property checks, pdfaid/pdfuaid/pdfxid/pdfvtid/fx identification, Info↔XMP — declines rather than guessing or reporting a property missing. Never a violation. Well-formedness still runs: `xmpWellFormed` is O(n) over the token stream and needs no tree. The metadata writers refuse to edit such a packet. |
| embedded PDF/A validation (no bound of its own) | `pdfa/final_rules.go` | Was **silently wrong** | `checkEmbeddedPDFA` treated *any* non-empty result from the nested validation as non-conformance, so a guard trip or a recovered panic inside the embedded document became *"an embedded PDF file is not compliant with PDF/A"* (6.9). | Fixed. `embeddedPDFACompliant` returns completeness alongside the verdict; a nested `IsCheckerFinding` declines the 6.9 finding and reports `embedded-pdfa` instead. The nested read and validation now also inherit the outer document's resolved limits rather than the defaults — the one place a hostile file could otherwise spend a whole second document's budget unconfigured. Because that makes a *lowered* ceiling a possible cause of "did not read" and "declares no level", those two exits also withhold the verdict whenever the limits in force are not the defaults, which is fail-open (a missed finding, never a manufactured one). Under the defaults nothing changes. |
| Device-colour and executed-content seen-sets | `internal/core/interp.go`, `pdfa/content_operators.go` | Silently lossy | A second visit can only add usage, so dropping it hides findings. | Device colour and font usage: replaced by the content interpreter's memo (`internal/core/interp.go`), keyed by (stream, resources, inherited graphics state), so a second visit in a different state is executed, not dropped; a result computed while a caller was still in progress (a cycle) is not memoised. The resources in the key are interned by value, so pages that each write their own copy of the same `/Font` dictionary share an entry. `pdfa/content_operators.go`: the containers walked keep their seen-set; each stream's tokens are read once and judged once per distinct resources value, which reports what a scan per container reported. |
| content interpreter depth (`maxExecDepth` 64) | `internal/core/interp.go` | Loud | Device-colour and font-usage rules over content not executed. | Reported as `content-state-work`. It bounds the stack; the interpreter's work — its executions and the bytes they read, which had bounds of their own — is charged to the run's work meter. The q/Q stack is capped at 256 entries with deeper pushes counted, so it costs a counter rather than memory. |
| embedded CMap building on a CMap that is neither predefined nor embedded | `internal/core/cmap.go` | Was **silently skipped** | Glyph coverage, `.notdef`, widths, CIDSet for the codes left to that CMap. | Reported as `embedded-cmap` when a check first meets such a code. A CMap past its bounds is `cmap-size` (above); one with no codespace is malformed, the file's fault, and not a skip. |

### Structure and PDF/UA

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| table grid fills (`WithMaxTableGridFills`) | `pdfua/pdfua_tablegrid.go` | Silently lossy (correctly designed) | Abandons the layout and discards even the defects already found, rather than reporting a half-laid-out grid. | Reported as `table-grid-fills`; `gridDefects` returns a completeness flag so "no defects" cannot be mistaken for "clean". |
| `/RoleMap` chain steps (was WithMaxRoleMapSteps) | `internal/core/structtree.go`, `pdfua/pdfua.go` | Silently lossy | Remaining `/RoleMap` keys were never examined: *"/RoleMap remaps standard structure type"*, *"contains a circular mapping"* went unreported. | Gone. Each type is resolved once per run (the answer is memoised for every type a chain crosses), so the whole role map costs one walk, charged to the work meter (audit 2026-09-22 C41: the per-chain budget let 2,000 elements each walk a 100,000-long chain). A run that cannot afford it is stopped, not handed a partial answer. |
| `sign.MaxFieldTreeDepth` (64) | `sign/signatures.go`, `sign.go` | Silently lossy | Truncates a reported `sign.Result.Field` name; never flips `Valid` or `CoversWholeDocument`. | Unchanged. |
| `maxPageTreeDepth` (64) | `sign.go` | Loud | `signingTarget` refuses. | Unchanged. |
| Struct-tree / table-row seen-sets | `pdfua/pdfua_struct.go`, `pdfua/pdfua_tablegrid.go` | Silently lossy on well-formed input | An element reachable twice is not a valid structure tree, so these only bite malformed files. | Unchanged (see *Left deliberately*). |

### Parsing and file structure

| Guard | File | Class | Notes |
| --- | --- | --- | --- |
| `maxParseDepth` (1000) | `syntax/parser.go` | Loud | A hard error. In lenient (rebuilt-xref) mode the caller drops the object instead — see the objstm row. |
| decoded-stream cap (`WithMaxDecodedStreamBytes`, default 100 MB) | `internal/core/filters.go` | Loud to a caller, reported to a validator | A hard error (`ErrDecodeLimit`) out of every filter, `StreamData` included. A check that meets it gets `ReasonLimit` and the producer reports `decoded-stream-size`; a cross-reference stream over it makes Read rebuild the table by scanning, and that is reported too. The write-side object-stream cap (`core.Limits.ObjStmMaxRaw`) derives from it so a container pdf0 writes is one the same configuration can read back. |
| `maxSerializeDepth` (1000) | `syntax/serializer.go` | Loud | A hard error; guards an unrecoverable stack overflow. |
| `maxCompareDepth` (1000) | `compare.go` | Silently wrong *by construction* | Beyond the cap two objects are declared **not equal**. Documented as such; no validator rule compares structures that deep. |
| `maxTokenGap` (1 MiB) | `syntax/lexer.go` | Loud-ish | The lexer parks and the parser fails on the next token. Not reachable by the recorder (see *reach*, above). |
| object-stream materialisation budget (`WithMaxObjectStreamBytes`) | `objstm.go`, `syntax/parser.go` | Silently wrong *in aggregate* | The container's objects go missing from `doc.Objects`, and every validator then resolves those objects to `nil`, which is indistinguishable from "absent": *"document does not specify a default language"*, *"encrypted document has no /P permissions entry"*, *"annotation has no alternate description"*, *"a DPart reference does not resolve to a dictionary"*. Reported as `objstm-decompressed-total` in every validator, so the cascade is attributable. A container the budget, a decode limit, an unimplemented filter or undecrypted data stopped is now recorded as *skipped* (`View.SkippedObjStms`), not broken: PDF/A's 6.1.6/6.1.7 "malformed" reads only the broken list (audit 2026-09-22 C47). The budget meters the parser's estimate of what it materialises (`syntax.Parser.Budget`), plus the container being unpacked, instead of decoded bytes, which let the objects take five times the bound (C9). |
| `Document.Resolve` 64-hop cap | `document.go` | Silently wrong in principle | Returns `nil`, indistinguishable from "key absent". A 64-hop reference chain does not occur in real files (measured: 0); the same `nil` arriving from the objstm budget is what actually bites, and that is now reported. See *Left deliberately*. |
| `WriteIncremental` vs `brokenObjStms` | `incremental.go` | Was **silently wrong** | It computed `/Size` from an incomplete object set and wrote the file without a word — the only write path that did not refuse. Now refuses, as `Write` already did, for a broken or a skipped container alike (`missingObjectsErr`). |
| undecrypted content (`not-decrypted`) | `limits_report.go`, `internal/core/outcome.go` | Was **silently wrong** | A Locked document's strings and streams are ciphertext; PDF/UA judged the ciphertext of a valid `/Lang` as a language tag and the metadata as not XML (audit 2026-09-22 C63). | Fixed. Every validator reports it once, up front; the stream producers answer `ReasonLocked` and the string producer `View.StringValue` does the same, which `internal/lint` requires every validator to read strings through. |
| option values (`With*`) | `limits.go` | Was **silently wrong** | `WithMaxDecodedStreamBytes(math.MaxInt)` overflowed the decoders' one-byte read-ahead and every stream decoded to nothing; 0 meant "the default" and −1 made every stream fail (audit 2026-09-22 C48). | Fixed. 0 and negative values are an error (`ErrInvalidOption`) at `Read`; the type's maximum is honoured as "no practical limit". |

### Image codecs

Every guard in `internal/jbig2`, `internal/ccitt`, `images/`,
`internal/core` (PDF functions, stream filters) is at worst a false negative
**for the extraction API**. No PDF/A, PDF/UA, PDF/X, PDF/VT or PDF/R rule reads a
decoded pixel: the image rules read dictionary keys (`/Alternates`,
`/Interpolate`, `/OPI`, `/SMask`, `/Filter`, `/ColorSpace`) and
`core.CheckCSForDevice` judges colour from `/ColorSpace` alone. A budget trip
surfaces as `ExtractedImage.Note`, not as a finding.

All of the image codecs share one configurable budget, `WithMaxImagePixels`
(default 2^26 pixels), checked by `core.Limits.CheckImage` before every
allocation sized from an image's geometry; a refusal's `Note` names the
`image-pixels` guard. The JBIG2 package constants are ceilings the budget
cannot raise, not separate knobs. See [images.md](images.md#resource-budgets).

The type-4 (PostScript calculator) work is in this list because of who calls it,
not where it lives: `core.View.EvalFunction` is reached only from
`images/imagecolor.go`'s tint-transform rendering, and the PDF/A tint-transform
rule compares function *objects* (`object.Equal`), it never evaluates one. Every
operator executed is charged to the run's work meter; the per-evaluation budget
it replaced (WithMaxPostScriptSteps) bounded one evaluation and not the image
(audit 2026-09-22 C51). The tint is also evaluated once per distinct input, not
once per pixel. An evaluation outside any run is still held to 2^20 operators.

One exception, now fixed: `decodeGenericMMR` indexed the CCITT decoder's output
as if it held every row. The CCITT decoder stops early when its data runs out
and still returns a nil error, so a short result produced a slice-bounds panic —
and that panic is not the JBIG2 budget error (now `jbig2.ErrBudget`), so the
JBIG2 decoder's recover re-raised it and it escaped `ExtractImages` to the
caller. A short decode is now a reported decode failure.

## Found alongside, and since fixed

These were found while auditing the limits above. None of them is a resource
limit — they are ordinary defects that happened to surface in the same reading —
so they were parked for separate work with its own corpus verification. That
work is done; each is recorded here with the rule it was really breaking. Every
ratchet was unchanged by the five together: corpus `pass=776 fail=1278
falsePositives=0 missed=0 parseErrors=0`, Isartor `missed=1` (0 since the
extension-schema structure rule), Level A 9/9,
Arlington `5` on 1071 conformant files, `2896` files parsed with 0 failures.

- **`standardStructType` followed exactly one `/RoleMap` hop**
  (`pdfua/pdfua_struct.go`). A role map may reach a standard type through intermediate
  custom types — `MyPara → Para → P` is legal (ISO 32000-1 14.7.3) — and one hop
  declared the type unmapped, firing *"structure type /X is neither standard nor
  mapped in /RoleMap"* and then, because every dependent rule saw the raw type, a
  spray of 7.2 nesting findings on a conformant tree. **Fixed:**
  `core.ResolveRoleMapChain` follows the chain, with a seen-set so a cyclic map
  terminates. It was bounded by the `/RoleMap` step budget then; it is now
  memoised per run and charged to the work meter (above).
- **forme's `font/fontprog.go`'s Type 1 CharStrings loop broke on
  `strings.Contains(name, "end")`** after a *successful* glyph parse, truncating
  the glyph list at the first font defining `endash` (or
  `enfilledcircbullet`, or `endescender`). **Fixed:** `font.Type1CharStringsEnd`
  detects what actually closes the dictionary — the standalone `end` token after
  the entry's `ND`/`|-` (Type 1 Font Format 10.3) — read from the byte stream,
  not from a glyph name.
- **The PDF/X device-colour scanner's memo was keyed on `*Stream` but not on
  `applyGroup`** (since replaced by the content interpreter in
  `internal/core/interp.go`), so whichever visit came first answered for both: a form
  whose isolated calibrated group covers its `DeviceRGB` was reported unmasked
  once an appearance-stream visit had cached the raw value → *"DeviceRGB used
  without a matching OutputIntent, DefaultRGB or covering group colour space"*.
  **Fixed:** the memo key is `(stream, applyGroup)`.
- **`pdfa/filestructure.go`'s 8-byte white-space skip** ahead of an object header.
  The rule (ISO 19005-1 6.1.8, -2 6.1.9, -4 6.1.8) is *"the object number … shall
  be preceded by an EOL marker"* — a statement about the byte before the header,
  which therefore has to be located wherever the recorded offset left it. A byte
  count is the wrong shape for that: a longer run left the object unchecked, and
  when the byte eight in was a space it accused an EOL-preceded header. **Fixed:**
  the skip runs to the header, bounded by the object's own region.
- **`internal/crypt/crypt.go`'s `decrypt` returned the ciphertext unchanged** when AES padding
  validation failed. By then the file key is known good (a wrong password never
  reaches `decrypt` — it leaves the document `Locked()`), so the failure means
  corrupt or never-encrypted data, and the bytes are not the plaintext: passing
  them on put high-entropy noise where a `/Title`, a content stream or an XMP
  packet was expected, which is exactly how a caller ends up validating noise.
  **Fixed:** the value is emptied, the object number recorded in
  `Document.decryptFailures`, and `Write` refuses — the same loud answer it
  already gives for an undecodable object stream. The one place the old
  behaviour was written down (`crypt_signature_test.go`'s note that under AES a
  wrongly-decrypted `/Contents` "survives by accident") is an explanation of why
  that test uses RC4, not an expectation of it; the exemption it guards is by
  key, and unaffected.

## Left deliberately

Two of the defects found alongside were judged not worth fixing. Both were
re-examined when the five above were fixed, with measurements over the 2907-file
corpus; both verdicts stand.

- **Struct-tree and table-row `seen`-set dedup** can drop a subtree and make
  `checkUAHeadings`' consecutive-level comparison report a skipped level. It
  takes an element reachable twice through `/K` to trigger, and that is an
  element with two parents, which contradicts the single `/P` every structure
  element carries (ISO 32000-1 14.7.2, Table 323) — the hierarchy is a tree.
  Measured: of the 588 corpus files with a structure tree, **0** have any node
  reachable twice. The narrower cases are harmless anyway: `collectTableRows`
  allocates its seen-set per table, so a `TR` shared between two tables is
  unaffected, and a shared OBJR, MCR or MCID node carries no `/S` and no
  subtree, so dropping the second visit loses nothing any rule reads. The
  residual effect is a spurious extra finding on a file that is already reported
  invalid — never a false positive on a conformant one.
- **`Document.Resolve`'s 64-hop cap** returning a bare `nil`. The stronger
  argument is not the cost of a fix but that the conflation is already the
  spec's own: *"An indirect reference to an undefined object shall not be
  considered an error by a conforming reader; it shall be treated as a reference
  to the null object"* (ISO 32000-1 7.3.10). Every rule in the package is
  written against that, correctly, so `nil` meaning "absent" is load-bearing;
  distinguishing a *third* state, "unknown", would have to be threaded through
  all of them. And the cap is not reached: across all 2907 corpus files —
  including every adversarial veraPDF and Isartor fail file — the longest
  reference chain measured is **0** hops (no object's value is itself an
  indirect reference), against a bound of 64. The `nil` that actually produces a
  cascade is the one from the object-stream budget, and that is reported. If a
  file ever does reach the cap, the fix is to swap the hop count for a visited
  set so that only a genuine cycle yields `nil` — not to give every rule a third
  state.
