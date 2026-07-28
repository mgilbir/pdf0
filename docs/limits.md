# Resource limits and what a trip means

Two documents cover resource limits, because there are two questions.
[docs/proposals/configurable-limits.md](proposals/configurable-limits.md)
answers *what a limit is* — the eleven `With*` options, the defaults, the
measurements behind them, and which limits were deliberately left internal. This
one answers *what happens when a limit trips*. The code splits the same way:
`limits.go` and `limits_report.go`.

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
format-4 cmap segment starting at code 0 was dropped, and because `trueTypeGID`
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
   the dependent check — the `parseCmapSubtable` nil-vs-empty contract is this
   idea, and `fontProgram.cmapPartial` / `parseCIDWidths`' second result extend
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

### What the mechanism reaches, honestly

| Reached | Not reached |
| --- | --- |
| All PDF/A, PDF/UA, PDF/UA-2, PDF/X, PDF/VT, PDF/R and DPart checks (each installs or joins a run). | The lexer and parser (`maxTokenGap`, `maxParseDepth`): they take bytes, not a `*Document`, and threading state through them for a guard that already surfaces as a parse error would be ceremony, not reach. |
| Read-time object-stream budget trips, via `Document.readLimits`. | `ExtractImages` / `ExtractText`: they return no finding channel. Image decode failures already surface per image in `ExtractedImage.Note`; text truncation surfaces as missing text. |
| Font-program guards, forwarded from the parsed program. | `Equal` / `Write` / `WriteIncremental`: these return errors, which is the loud class already. |

## The inventory

Guards are grouped by subsystem. "Consumer" names the rule that reads the
truncated value; the message quoted is the one a trip could wrongly emit.

### Fonts and font programs

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| cmap work budget (`WithMaxCmapWork`) | `fontprog.go` | **Silently wrong** | `trueTypeGID` → `simpleGlyphExists` / `isNotdefGlyph`: *"embedded TrueType font does not define a glyph referenced for rendering (code N)"*, *"text showing operator references the .notdef glyph"* | Fixed. `fontProgram.cmapPartial`; the glyph and .notdef rules decline for that font; trip reported as `cmap-format4-work`. |
| CID `/W` range span (`WithMaxCIDRangeSpan`) | `fonts.go` | **Silently wrong** | `checkCIDFontConsistency`: a dropped `/W` range falls back to `/DW` (default 1000) and is compared against the program's real advance → *"width information for glyphs used for rendering is inconsistent"* | Fixed. `parseCIDWidths` reports completeness; the width rule declines; trip reported as `cid-width-range`. |
| `parseCmapSubtable` nil-on-unreadable | `fontprog.go` | Silently lossy (deliberate) | The subtable is ignored rather than read as "maps nothing". | Unchanged; this is the contract the fix above extends. |
| ToUnicode / CMap section scanners (`bfrange` ≥ 65536, unterminated sections) | `fonts.go` | Silently lossy | Missing `toUni[cid]` *suppresses* the empty-outline rule (fail-open). | Unchanged. |
| `maxTextFormDepth` | `text.go` | Silently lossy | `ExtractText` only — **no validator consumes it**. | Unchanged. |
| sfnt/CFF/Type1 structural bails (`return nil`) | `fontprog.go` | Loud | `damagedFontProgramError`: *"embedded %s font program is damaged and could not be parsed"* | Unchanged (see *Left deliberately*). |

### Content scanning

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| aggregate content budget (`WithMaxDecodedContentBytes`) applied to `/Metadata` | `pdfa.go` | **Silently wrong** | Every identification rule: *"metadata must contain pdfaid:part"*, *"pdfaid:conformance must be B, got \"\""*, *"Info /Title present but XMP dc:title missing"*, *"file is not identified as PDF/X"*, *"an embedded PDF file is not compliant with PDF/A"* | Fixed. `decodeMetadataStream` / `xmpText` decode the document's own identification outside the aggregate budget. |
| 256-byte token cap, not configurable (four tokenizers) | `pdfa.go`, `fonts.go`, `filestructure.go` | **Silently wrong** | The cap cut a run and the scan re-entered mid-run, so a binary tail became tokens: a one-byte `k`/`g` fragment → *"DeviceCMYK used without matching OutputIntent or DefaultCMYK"*; an alphabetic fragment → *"content stream contains an operator not defined in ISO 32000"* | Fixed. `maxContentTokenLen`; an over-long run is discarded whole (same single linear pass). |
| per-stream content cap (`WithMaxContentStreamBytes`) | `pdfa.go` | Silently lossy | Every content-driven rule sees nothing from the stream. This is the failure the old 1 MB cap caused. | Reported as `content-stream-size`. |
| aggregate content budget (`WithMaxDecodedContentBytes`), content proper | `pdfa.go` | Silently lossy | Same, for every stream after the budget. | Reported as `decoded-content-total`. |
| `maxQDepth` (28) | `pdfa.go` | Loud | It *is* the rule (implementation limit), not a work cap. | Unchanged. |
| ICC profile size (`WithMaxICCProfileBytes`) | `pdfa.go` | Silently lossy, fail-open by design | `getOutputIntentCoverage` sets `hasRGB=hasCMYK=true` on an unreadable profile precisely to avoid a false positive. | Unchanged. |
| Device-colour and executed-content seen-sets | `pdfa.go`, `content_operators.go`, `pdfx_color.go` | Silently lossy | A second visit can only add usage, so dropping it hides findings. | Unchanged. |

### Structure and PDF/UA

| Guard | File | Class before | Consumer / finding at risk | Now |
| --- | --- | --- | --- | --- |
| table grid fills (`WithMaxTableGridFills`) | `pdfua_tablegrid.go` | Silently lossy (correctly designed) | Abandons the layout and discards even the defects already found, rather than reporting a half-laid-out grid. | Reported as `table-grid-fills`; `gridDefects` returns a completeness flag so "no defects" cannot be mistaken for "clean". |
| `/RoleMap` chain steps (`WithMaxRoleMapSteps`) | `pdfua.go` | Silently lossy | Remaining `/RoleMap` keys are never examined: *"/RoleMap remaps standard structure type"*, *"contains a circular mapping"* go unreported. Type resolution uses a separate, uncapped lookup, so no finding is manufactured. | Reported as `rolemap-work`. |
| `maxFieldTreeDepth` (64) | `signatures.go`, `sign.go` | Silently lossy | Truncates a reported `SignatureResult.Field` name; never flips `Valid` or `CoversWholeDocument`. | Unchanged. |
| `maxPageTreeDepth` (64) | `sign.go` | Loud | `signingTarget` refuses. | Unchanged. |
| Struct-tree / table-row seen-sets | `pdfua_struct.go`, `pdfua_tablegrid.go` | Silently lossy on well-formed input | An element reachable twice is not a valid structure tree, so these only bite malformed files. | Unchanged (see *Left deliberately*). |

### Parsing and file structure

| Guard | File | Class | Notes |
| --- | --- | --- | --- |
| `maxParseDepth` (1000) | `parser.go` | Loud | A hard error. In lenient (rebuilt-xref) mode the caller drops the object instead — see the objstm row. |
| decoded-stream cap (`WithMaxDecodedStreamBytes`, default 100 MB) | `xref.go` | Loud | A hard error out of `flateDecode`. Being loud, it needs no trip report; the write-side `objStmMaxRaw` derives from it so a container pdf0 writes is one the same configuration can read back. |
| `maxSerializeDepth` (1000) | `serializer.go` | Loud | A hard error; guards an unrecoverable stack overflow. |
| `maxCompareDepth` (1000) | `compare.go` | Silently wrong *by construction* | Beyond the cap two objects are declared **not equal**. Documented as such; no validator rule compares structures that deep. |
| `maxTokenGap` (1 MiB) | `lexer.go` | Loud-ish | The lexer parks and the parser fails on the next token. Not reachable by the recorder (see *reach*, above). |
| object-stream decompression budget (`WithMaxObjectStreamBytes`) | `objstm.go` | Silently wrong *in aggregate* | The container's objects go missing from `doc.Objects`. PDF/A reports the container (6.1.6/6.1.7), but every other validator then resolves those objects to `nil`, which is indistinguishable from "absent": *"document does not specify a default language"*, *"encrypted document has no /P permissions entry"*, *"annotation has no alternate description"*, *"a DPart reference does not resolve to a dictionary"*. Now reported as `objstm-decompressed-total` in every validator, so the cascade is attributable. |
| `Document.Resolve` 64-hop cap | `document.go` | Silently wrong in principle | Returns `nil`, indistinguishable from "key absent". A 64-hop reference chain does not occur in real files; the same `nil` arriving from the objstm budget is what actually bites, and that is now reported. See *Left deliberately*. |
| `WriteIncremental` vs `brokenObjStms` | `incremental.go` | Was **silently wrong** | It computed `/Size` from an incomplete object set and wrote the file without a word — the only write path that did not refuse. Now refuses, as `Write` already did. |

### Image codecs

Every guard in `jbig2*.go`, `ccitt.go`, `mq.go`, `imagejpeg.go`, `imagemask.go`,
`imagecolor.go` and `filters.go` is at worst a false negative **for the
extraction API**. No PDF/A, PDF/UA, PDF/X, PDF/VT or PDF/R rule reads a decoded
pixel: the image rules read dictionary keys (`/Alternates`, `/Interpolate`,
`/OPI`, `/SMask`, `/Filter`, `/ColorSpace`) and `checkCSForDevice` judges colour
from `/ColorSpace` alone. A budget trip surfaces as `ExtractedImage.Note`, not
as a finding.

One exception, now fixed: `decodeGenericMMR` indexed `decodeCCITT`'s output as if
it held every row. `decodeCCITT` stops early when its data runs out and still
returns a nil error, so a short result produced a slice-bounds panic — and that
panic is not `errJBIG2Budget`, so `decodeJBIG2`'s recover re-raised it and it
escaped `ExtractImages` to the caller. A short decode is now a reported decode
failure.

## Found and deliberately left

These are real, but they are not resource limits — fixing them belongs in
separate work with its own corpus verification.

- **`standardStructType` follows exactly one `/RoleMap` hop** (`pdfua_struct.go`).
  A legal two-step chain (`MyPara → Para → P`) does not resolve, producing
  *"structure type /X is neither standard nor mapped in /RoleMap"* and a spray
  of nesting findings. A modelling gap, not a budget.
- **`fontprog.go`'s Type 1 CharStrings loop breaks on `strings.Contains(name,
  "end")`** after a *successful* glyph parse, so a font defining `endash`
  truncates its glyph list there. A parsing bug; the sibling check twelve lines
  up correctly uses `HasPrefix`.
- **`devColorScanner.memo` is keyed on `*Stream` but not on `applyGroup`**
  (`pdfx_color.go`), so a stream reached first without group masking caches a
  value the group would have masked → *"DeviceRGB used without a matching
  OutputIntent, DefaultRGB or covering group colour space"*. A memoization bug.
- **`filestructure.go`'s 8-byte whitespace skip** ahead of an object header can
  emit *"indirect object number is not preceded by an EOL marker"* for an object
  preceded by more than eight white bytes.
- **Struct-tree and table-row `seen`-set dedup** can drop a subtree and make
  `checkUAHeadings`' consecutive-level comparison report a skipped level. Only
  reachable on a structure tree that is already invalid (an element with two
  parents), so it cannot fire on a conformant file.
- **`crypt.go`'s `decrypt` returns the ciphertext unchanged** when AES padding
  validation fails, handing garbage to the parser rather than reporting a failed
  decrypt.
- **`Document.Resolve`'s 64-hop cap** returning a bare `nil`. Making "unknown"
  distinguishable from "absent" at that level would touch every rule in the
  package; the trips that actually produce the cascade are reported instead.
