# Resource limits and what a trip means

Three documents cover resource limits, because there are three questions, and
each has exactly one home:

| Question | Read | Code |
| --- | --- | --- |
| *How do I cap what a document costs?* — the eleven `With*` options, the defaults, which entry points take them | [architecture.md](architecture.md#resource-limits) | `limits.go` |
| *What happens when a limit trips?* — the `limit` rule, `IsCheckerFinding`, the per-guard classification | this document | `limits_report.go` |
| *Why is it shaped this way?* — the measurements, the rejected alternatives, which limits were deliberately left internal | [proposals/configurable-limits.md](proposals/configurable-limits.md) | — |

The defaults are stated in `limits.go`, which is the source of truth; the tables
elsewhere restate them for a reader who is already there.

The two meet in one place. Eleven of the guards below are configurable, so a
trip may be pdf0's own ceiling or the caller's; the trip message says which
(`limitBound`), because "you hit the cap you set" and "you hit our default" call
for different responses.

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
format-4 cmap segment starting at code 0 was dropped, and because `font.TrueTypeGID`
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

`limits_report.go` holds one mechanism. A `limitRecorder` lives on the per-run
`validationCache`; any guard with a `*Document` in scope calls `noteLimit`,
which is a no-op when no run is in progress. Guards with no `*Document` at all
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
several rules. The recorder is itself bounded (`maxRecordedLimitTrips`, 64), so
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
the trip is derived in `runLimitTrips` — one place, so none of the nine
validators can forget it.

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
| Read-time object-stream budget trips, via `Document.readLimits`. | `ExtractImages` / `ExtractText`: they return no finding channel. Image decode failures already surface per image in `ExtractedImage.Note`; text truncation surfaces as missing text. |
| Font-program guards, forwarded from the parsed program. | `Write` / `WriteIncremental`: these return errors, which is the loud class already. |
| Nested embedded-PDF/A validation (6.9), as `embedded-pdfa`. | `Equal` / `DocumentEqual`: they return a `bool`, so there is nowhere to say "too deep to tell". `maxCompareDepth` is *silently wrong by construction* (see the parsing table) and stays that way; no validator rule compares structures that deep. |
| Cancellation of any validation run, derived in `runLimitTrips`. | `ReadContext` / `WriteContext`: loud, an error wrapping `ctx.Err()`. `ExtractTextContext` / `ExtractImagesContext`: partial result plus that error. |
| `ValidateFacturXContext` / `ValidateOrderXContext`, on **both** sides of the module seam — see below. | The `Is*` detection predicates in `formalis`: they return a `bool`, which has no room to say "the run stopped", so a context there could only lie. They are bounded by that module's own limits instead. |

### Across the `formalis` seam

The two invoice containers compose two rule engines, and both honour the same
mechanism. pdf0's half reports a trip under `limitRule`; `formalis` reports one
under `formalis.RuleLimit`, and the two constants are the same string
**deliberately**, so a caller draining `res.Violations` for "the checker stopped"
has one name to look for rather than two.

The consequence that is easy to get wrong: the PDF/A-3 findings this path
composes are prefixed `pdfa-3/` and the invoice engine's are adopted verbatim,
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

## The inventory

Guards are grouped by subsystem. "Consumer" names the rule that reads the
truncated value; the message quoted is the one a trip could wrongly emit.

### Fonts and font programs

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| cmap work budget (`WithMaxCmapWork`) | `internal/font/fontprog.go` | **Silently wrong** | `font.TrueTypeGID` → `simpleGlyphExists` / `isNotdefGlyph`: *"embedded TrueType font does not define a glyph referenced for rendering (code N)"*, *"text showing operator references the .notdef glyph"* | Fixed. `font.Program.CmapPartial`; the glyph and .notdef rules decline for that font; trip reported as `cmap-work` (one budget, charged by both expanding subtable formats — see [fonts.md](fonts.md#why-formats-4-and-12-share-one-budget)). |
| CID `/W` range span (`WithMaxCIDRangeSpan`) | `fonts.go` | **Silently wrong** | `checkCIDFontConsistency`: a dropped `/W` range falls back to `/DW` (default 1000) and is compared against the program's real advance → *"width information for glyphs used for rendering is inconsistent"* | Fixed. `parseCIDWidths` reports completeness; the width rule declines; trip reported as `cid-width-range`. |
| `font.ParseCmapSubtable` nil-on-unreadable | `internal/font/fontprog.go` | Silently lossy (deliberate) | The subtable is ignored rather than read as "maps nothing". | Unchanged; this is the contract the fix above extends. |
| ToUnicode / CMap section scanners (`bfrange` ≥ 65536, unterminated sections) | `fonts.go` | Silently lossy | Missing `toUni[cid]` *suppresses* the empty-outline rule (fail-open). | Unchanged. |
| `maxTextFormDepth` | `text.go` | Silently lossy | `ExtractText` only — **no validator consumes it**. | Unchanged. |
| sfnt/CFF/Type1 structural bails (`return nil`) | `internal/font/fontprog.go` | Loud | `damagedFontProgramError`: *"embedded %s font program is damaged and could not be parsed"* | Unchanged: a bail is reported as a damaged program, which is the loud class. |

### Content scanning

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| aggregate content budget (`WithMaxDecodedContentBytes`) applied to `/Metadata` | `pdfa.go` | **Silently wrong** | Every identification rule: *"metadata must contain pdfaid:part"*, *"pdfaid:conformance must be B, got \"\""*, *"Info /Title present but XMP dc:title missing"*, *"file is not identified as PDF/X"*, *"an embedded PDF file is not compliant with PDF/A"* | Fixed. `decodeMetadataStream` / `xmpText` decode the document's own identification outside the aggregate budget. |
| 256-byte token cap, not configurable (four tokenizers) | `pdfa.go`, `fonts.go`, `filestructure.go` | **Silently wrong** | The cap cut a run and the scan re-entered mid-run, so a binary tail became tokens: a one-byte `k`/`g` fragment → *"DeviceCMYK used without matching OutputIntent or DefaultCMYK"*; an alphabetic fragment → *"content stream contains an operator not defined in ISO 32000"* | Fixed. `maxContentTokenLen`; an over-long run is discarded whole (same single linear pass). |
| per-stream content cap (`WithMaxContentStreamBytes`) | `pdfa.go` | Silently lossy | Every content-driven rule sees nothing from the stream. This is the failure the old 1 MB cap caused. | Reported as `content-stream-size`. |
| aggregate content budget (`WithMaxDecodedContentBytes`), content proper | `pdfa.go` | Silently lossy | Same, for every stream after the budget. | Reported as `decoded-content-total`. |
| `maxQDepth` (28) | `pdfa.go` | Loud | It *is* the rule (implementation limit), not a work cap. | Unchanged. |
| ICC profile size (`WithMaxICCProfileBytes`) | `pdfa.go` | Silently lossy, fail-open by design | `getOutputIntentCoverage` sets `hasRGB=hasCMYK=true` on an unreadable profile precisely to avoid a false positive. | Unchanged. |
| XMP packet size (`WithMaxXMPPacketBytes`) | `xmp.go`, `xmp_schemas.go` | Silently lossy, fail-open by design | `parseXMPProperties` errors over the cap and `checkXMPProperties` reads that as "no properties to check" — **never** a violation, so an oversized valid packet is not failed. Well-formedness still runs: `xmpWellFormed` is O(n) over the token stream and needs no tree. | Unchanged. The two rules that survive the cap are the two a caller most needs, and the skipped ones are value checks that cannot fire without a tree. |
| embedded PDF/A validation (no bound of its own) | `final_rules.go` | Was **silently wrong** | `checkEmbeddedPDFA` treated *any* non-empty result from the nested validation as non-conformance, so a guard trip or a recovered panic inside the embedded document became *"an embedded PDF file is not compliant with PDF/A"* (6.9). | Fixed. `embeddedPDFACompliant` returns completeness alongside the verdict; a nested `IsCheckerFinding` declines the 6.9 finding and reports `embedded-pdfa` instead. The nested read and validation now also inherit the outer document's resolved limits rather than the defaults — the one place a hostile file could otherwise spend a whole second document's budget unconfigured. Because that makes a *lowered* ceiling a possible cause of "did not read" and "declares no level", those two exits also withhold the verdict whenever the limits in force are not the defaults, which is fail-open (a missed finding, never a manufactured one). Under the defaults nothing changes. |
| Device-colour and executed-content seen-sets | `pdfa.go`, `content_operators.go`, `pdfx_color.go` | Silently lossy | A second visit can only add usage, so dropping it hides findings. | Unchanged. |

### Structure and PDF/UA

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| table grid fills (`WithMaxTableGridFills`) | `pdfua_tablegrid.go` | Silently lossy (correctly designed) | Abandons the layout and discards even the defects already found, rather than reporting a half-laid-out grid. | Reported as `table-grid-fills`; `gridDefects` returns a completeness flag so "no defects" cannot be mistaken for "clean". |
| `/RoleMap` chain steps (`WithMaxRoleMapSteps`) | `pdfua.go`, `pdfua_struct.go` | Silently lossy | Remaining `/RoleMap` keys are never examined: *"/RoleMap remaps standard structure type"*, *"contains a circular mapping"* go unreported. | Reported as `rolemap-work`. Type resolution (`resolveRoleMapChain`) shares the same budget and returns a completeness flag; on a trip `checkUARoleMap` declines rather than reporting *"neither standard nor mapped"*. |
| `maxFieldTreeDepth` (64) | `signatures.go`, `sign.go` | Silently lossy | Truncates a reported `SignatureResult.Field` name; never flips `Valid` or `CoversWholeDocument`. | Unchanged. |
| `maxPageTreeDepth` (64) | `sign.go` | Loud | `signingTarget` refuses. | Unchanged. |
| Struct-tree / table-row seen-sets | `pdfua_struct.go`, `pdfua_tablegrid.go` | Silently lossy on well-formed input | An element reachable twice is not a valid structure tree, so these only bite malformed files. | Unchanged (see *Left deliberately*). |

### Parsing and file structure

| Guard | File | Class | Notes |
| --- | --- | --- | --- |
| `maxParseDepth` (1000) | `syntax/parser.go` | Loud | A hard error. In lenient (rebuilt-xref) mode the caller drops the object instead — see the objstm row. |
| decoded-stream cap (`WithMaxDecodedStreamBytes`, default 100 MB) | `xref.go` | Loud | A hard error out of `flateDecode`. Being loud, it needs no trip report; the write-side `objStmMaxRaw` derives from it so a container pdf0 writes is one the same configuration can read back. |
| `maxSerializeDepth` (1000) | `syntax/serializer.go` | Loud | A hard error; guards an unrecoverable stack overflow. |
| `maxCompareDepth` (1000) | `compare.go` | Silently wrong *by construction* | Beyond the cap two objects are declared **not equal**. Documented as such; no validator rule compares structures that deep. |
| `maxTokenGap` (1 MiB) | `syntax/lexer.go` | Loud-ish | The lexer parks and the parser fails on the next token. Not reachable by the recorder (see *reach*, above). |
| object-stream decompression budget (`WithMaxObjectStreamBytes`) | `objstm.go` | Silently wrong *in aggregate* | The container's objects go missing from `doc.Objects`. PDF/A reports the container (6.1.6/6.1.7), but every other validator then resolves those objects to `nil`, which is indistinguishable from "absent": *"document does not specify a default language"*, *"encrypted document has no /P permissions entry"*, *"annotation has no alternate description"*, *"a DPart reference does not resolve to a dictionary"*. Now reported as `objstm-decompressed-total` in every validator, so the cascade is attributable. |
| `Document.Resolve` 64-hop cap | `document.go` | Silently wrong in principle | Returns `nil`, indistinguishable from "key absent". A 64-hop reference chain does not occur in real files (measured: 0); the same `nil` arriving from the objstm budget is what actually bites, and that is now reported. See *Left deliberately*. |
| `WriteIncremental` vs `brokenObjStms` | `incremental.go` | Was **silently wrong** | It computed `/Size` from an incomplete object set and wrote the file without a word — the only write path that did not refuse. Now refuses, as `Write` already did. |

### Image codecs

Every guard in `internal/jbig2`, `internal/ccitt`, `imagejpeg.go`, `imagemask.go`,
`imagecolor.go`, `internal/core` (PDF functions, stream filters) is at worst a false negative
**for the extraction API**. No PDF/A, PDF/UA, PDF/X, PDF/VT or PDF/R rule reads a
decoded pixel: the image rules read dictionary keys (`/Alternates`,
`/Interpolate`, `/OPI`, `/SMask`, `/Filter`, `/ColorSpace`) and
`checkCSForDevice` judges colour from `/ColorSpace` alone. A budget trip
surfaces as `ExtractedImage.Note`, not as a finding.

The type-4 budget is in this list because of who calls it, not where it lives.
The type-4 (PostScript calculator) work budget — `WithMaxPostScriptSteps`, the
eleventh configurable limit — bounds `psExec`, and `evalFunction` is reached only
from `imagecolor.go`'s tint-transform rendering. The PDF/A tint-transform rule
compares function *objects* (`Equal`), it never evaluates one, so a trip costs
pixel fidelity and no finding. This is why the budget reports no trip: there is
no rule to decline.

One exception, now fixed: `decodeGenericMMR` indexed `decodeCCITT`'s output as if
it held every row. `decodeCCITT` stops early when its data runs out and still
returns a nil error, so a short result produced a slice-bounds panic — and that
panic is not `errJBIG2Budget`, so `decodeJBIG2`'s recover re-raised it and it
escaped `ExtractImages` to the caller. A short decode is now a reported decode
failure.

## Found alongside, and since fixed

These were found while auditing the limits above. None of them is a resource
limit — they are ordinary defects that happened to surface in the same reading —
so they were parked for separate work with its own corpus verification. That
work is done; each is recorded here with the rule it was really breaking. Every
ratchet was unchanged by the five together: corpus `pass=776 fail=1278
falsePositives=0 missed=0 parseErrors=0`, Isartor `missed=1`, Level A 9/9,
Arlington `5` on 1071 conformant files, `2896` files parsed with 0 failures.

- **`standardStructType` followed exactly one `/RoleMap` hop**
  (`pdfua_struct.go`). A role map may reach a standard type through intermediate
  custom types — `MyPara → Para → P` is legal (ISO 32000-1 14.7.3) — and one hop
  declared the type unmapped, firing *"structure type /X is neither standard nor
  mapped in /RoleMap"* and then, because every dependent rule saw the raw type, a
  spray of 7.2 nesting findings on a conformant tree. **Fixed:**
  `resolveRoleMapChain` follows the chain, reusing the `WithMaxRoleMapSteps`
  budget rather than inventing a second knob, with a seen-set so a cyclic map
  terminates. It returns a completeness flag, so a budget trip declines the
  finding instead of manufacturing one (the rule at the top of this document).
- **`internal/font/fontprog.go`'s Type 1 CharStrings loop broke on
  `strings.Contains(name, "end")`** after a *successful* glyph parse, truncating
  the glyph list at the first font defining `endash` (or
  `enfilledcircbullet`, or `endescender`). **Fixed:** `type1CharStringsEnd`
  detects what actually closes the dictionary — the standalone `end` token after
  the entry's `ND`/`|-` (Type 1 Font Format 10.3) — read from the byte stream,
  not from a glyph name.
- **`devColorScanner.memo` was keyed on `*Stream` but not on `applyGroup`**
  (`pdfx_color.go`), so whichever visit came first answered for both: a form
  whose isolated calibrated group covers its `DeviceRGB` was reported unmasked
  once an appearance-stream visit had cached the raw value → *"DeviceRGB used
  without a matching OutputIntent, DefaultRGB or covering group colour space"*.
  **Fixed:** the memo key is `(stream, applyGroup)`.
- **`filestructure.go`'s 8-byte white-space skip** ahead of an object header.
  The rule (ISO 19005-1 6.1.8, -2 6.1.9, -4 6.1.8) is *"the object number … shall
  be preceded by an EOL marker"* — a statement about the byte before the header,
  which therefore has to be located wherever the recorded offset left it. A byte
  count is the wrong shape for that: a longer run left the object unchecked, and
  when the byte eight in was a space it accused an EOL-preceded header. **Fixed:**
  the skip runs to the header, bounded by the object's own region.
- **`crypt.go`'s `decrypt` returned the ciphertext unchanged** when AES padding
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
