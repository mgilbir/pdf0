# pdf0 Codebase Audit — 2026-07-07 (v2)

Adversarial, full-read audit of `github.com/mgilbir/pdf0` (PDF 2.0 parser / serializer /
PDF/A-1b/2b/3b/4 validator). This **supersedes** the earlier same-day audit
(`codebase-audit-2026-07-07.md`), which predates the parser-recovery / object-stream /
predictor / rule-family work now on `HEAD` — roughly 90% of its findings (C1–C22, A1–A16,
A27) are fixed (see §7 for the triage). This report catalogues what is *still* wrong or newly
introduced.

Method: nine parallel readers covered every `.go` file in full — (a) lexer/parser/object,
(b) document/xref/objstm/filters, (c) serializer/compare, (d,e) the 5,936-line `pdfa.go` in two
halves, (f) fonts, (g) content-operators + byte-level file-structure, (h) xmp + builder +
final-rules, (i) docs/DX/tests/examples. The auditor then independently re-verified every
Critical and most High findings by compiling standalone programs and tests against the module and
running them (marked **EXECUTED**). Findings that did not survive an attempt to disprove them were
discarded (e.g. a suspected TIFF-16-bit predictor off-by-one — `rowLen` is provably even for
`BitsPerComponent==16`, so the loop covers every pair).

The whole test suite passes (`go test ./...`, 2.9s with corpus present); `go vet` and `gofmt -l`
are clean. None of the findings below are caught by the existing tests — that is itself the story.

---

## 1. Summary table

| ID | Sev | Area | Issue | file:line | Status |
|----|-----|------|-------|-----------|--------|
| C1 | Critical | parser/DoS | Negative xref/`/Prev` offset → `Read` panics (lexer has no `pos<0` guard) | lexer.go:124,139; document.go:123,138 | CONFIRMED (EXECUTED) |
| C2 | Critical | objstm/DoS | `/N`=MaxInt64 → `int64(n)*4` overflows guard → `makeslice` panic in `Read` | objstm.go:41,46 | CONFIRMED (EXECUTED) |
| C3 | Critical | pdfa/DoS | Empty-array colour space → `arr[0]` panic via public `ValidatePDFABytes` | pdfa.go:5040 | CONFIRMED (EXECUTED) |
| C4 | Critical | pdfa/DoS | Self-referential DeviceN `/Colorants` → unbounded recursion → stack overflow | pdfa.go:5048 | CONFIRMED (EXECUTED) |
| C5 | Critical | design/DoS | No `recover()` anywhere: every parser/validator panic is fatal to the caller | (whole package) | CONFIRMED |
| C6 | High | serializer | Writer emits 21-byte xref lines → pdf0's own 6.1.4 rule rejects pdf0's own output; builder can't produce a conformant file | document.go:409,415 | CONFIRMED (EXECUTED) |
| C7 | High | write | `Write` prints `obj.Number`, keyed by map key; mismatch → body numbered N under xref slot M → dangling for external readers | document.go:355; serializer.go:241 | CONFIRMED (EXECUTED) |
| C8 | High | write | Indirect `/Length` target never updated on `Write` → serialized stream length wrong | serializer.go:222 | CONFIRMED (EXECUTED) |
| C9 | High | filestructure | `normalizeStructure` prunes `doc.Objects` but not `doc.Offsets` → byte-level region math spans dropped objects; last content object's endobj format never checked, mis-attribution possible | filestructure.go:130-159; document.go:168 | CONFIRMED |
| C10 | High | fonts/DoS | cmap format-4 parser is O(numSegs×65535), no work budget → multi-second CPU DoS on a normal-sized font | fontprog.go:200-217 | CONFIRMED (EXECUTED, 29.6s) |
| C11 | High | pdfa/FN | PDF/A-1b transparency check ignores image `/SMask` and form transparency groups → false PASS on Isartor 6.4 fail files | pdfa.go:1901-1988 | CONFIRMED (EXECUTED) |
| C12 | High | pdfa/FN | Systemic: rule inputs type-asserted without `Resolve` → forbidden values behind an indirect ref evade checks (e.g. `/Screen` annotation) | pdfa.go:1235,1681,523,914,1280,2087 | CONFIRMED (EXECUTED) |
| C13 | High | pdfa/FN | `/AA` on non-widget annotations never flagged at 1b/2b/3b | pdfa.go:1716-1749 | CONFIRMED (EXECUTED) |
| C14 | High | docs | README "Status and limitations" is factually inverted (objstm, predictors, encryption all claimed missing but implemented) | README.md:100-106 | CONFIRMED |
| C15 | Medium | compare/serializer/DoS | Cyclic direct `Dictionary` → `Equal`/`WriteObject` stack overflow (no depth cap) | compare.go:64; serializer.go:188 | CONFIRMED (EXECUTED) |
| C16 | Medium | write | In-use object 0 written to body but xref marks it free → dropped on re-read (silent loss) | document.go:433 | CONFIRMED (EXECUTED) |
| C17 | Medium | read | `Real`/other-typed `/Length` is a hard error aborting `Read`, despite the recovery design | parser.go:310 | CONFIRMED (EXECUTED) |
| C18 | Medium | read | Legal but unsupported filter (e.g. LZW) on the *first* xref/object stream aborts `Read` entirely | xref.go:303; document.go:104 | CONFIRMED |
| C19 | Medium | read/write | Broken object stream silently drops its objects; a later `Write` emits dangling refs with no warning | objstm.go:111 | CONFIRMED |
| C20 | Medium | pdfa/FN | A-4 page-level OutputIntent errors dropped on three early returns | pdfa.go:483-508 | CONFIRMED (EXECUTED) |
| C21 | Medium | pdfa/FN | Fonts reachable only via form-XObject/pattern resources are never embedding-checked | pdfa.go:976,1079 | CONFIRMED |
| C22 | Medium | pdfa/FP | XMP date normalized one-sidedly (`normalizeXMPDate` = `TrimSpace`) → 6.7.3 false mismatch on equal instants | pdfa.go:2687 | CONFIRMED (EXECUTED) |
| C23 | Medium | pdfa/FP | Compliant PDF/A-4f embedded file reported non-compliant (conformance F/E rejected) | pdfa.go checkMetadataVersion; final_rules.go:552 | CONFIRMED (EXECUTED) |
| C24 | Medium | pdfa/FN | BOM-less UTF-16/32-LE XMP passes the A-4 "must be UTF-8" rule | xmp_schemas.go:1121 | CONFIRMED (EXECUTED) |
| C25 | Medium | pdfa/FP | `BI…ID` inside a string literal fabricates an inline image → 6.2.8/6.2.9 false positives | final_rules.go:127; pdfa.go:4798 | CONFIRMED (EXECUTED) |
| C26 | Medium | compare | `dictionaryEqual` false-positives on duplicate keys and breaks reflexivity | compare.go:130 | CONFIRMED (EXECUTED) |
| C27 | Medium | filestructure | `collectContentStreamData` omits Type3 CharProcs despite its doc comment | filestructure.go:693-720 | CONFIRMED |
| C28 | Medium | test | Test asserts with `t.Skip()` instead of `t.Error()` → level-gating regressions pass green | final_rules_test.go:32 | CONFIRMED (EXECUTED) |
| C29 | Medium | dx | No CI; the corpus ratchet (the project's core contract) runs only locally, by hand | (repo) | CONFIRMED |
| C30 | Low | serializer/compare | Typed-nil pointer (`(*Dictionary)(nil)`) panics in `WriteObject`/`Equal` | serializer.go:53; compare.go:75 | CONFIRMED (EXECUTED) |
| C31 | Low | serializer | Emits `/#00` for `Name("\x00")`, which its own parser rejects | serializer.go:165 | CONFIRMED (EXECUTED) |
| C32 | Low | pdfa/FP+FN | Raw-string XMP layer mishandles single-quoted attrs (FP) and comment/CDATA URIs (FP), misses single-quoted prefixes (FN) | pdfa.go:1779,1876; xmp_schemas.go:904 | CONFIRMED (EXECUTED) |
| C33 | Low | fonts | `/lenIV` parsed correctly then discarded and re-parsed wrong → `lenIV=0` | fontprog.go:693 | CONFIRMED (EXECUTED) |
| C34 | Low | pdfa/FN | `checkProhibitedCatalogEntries` gated A-4-only; 6.11 also applies at 2b/3b | final_rules.go:17 | PLAUSIBLE |
| C35 | Low | pdfa | Rule-ID drift (halftone `6.2.5`/`6.2.8` at 1b), `Object:0` on halftone errors, dead branches | pdfa.go:2365; final_rules.go:364 | CONFIRMED |
| C36 | Low | dx | Docs long tail: godoc overclaims "nil if conformant", Layout table omits >half the files, spec pipeline undocumented, examples clobber `output.pdf`, ICC stub never veraPDF-verified | README/examples/pdfa_create.go | CONFIRMED |
| C37 | Info | pdfa/coverage | Isartor + PDF_A-4f/4e suites are parse-only; ~18 Isartor fail files validate to 0 errors, invisible to the ratchet | pdfa_test.go | CONFIRMED (EXECUTED) |

Long-tail Low/Info items (denormal-real expansion, absolute epsilon, `WriteObject` vs `Equal`
type asymmetry, nil-value-vs-absent-key, `findStartXref` 1024-byte tail, negative `startObj`,
`NewLexerFromReaderAt` dead export, `hasForbiddenUnicodeTargets` array-dst / `lo>hi`, dead
`localSubrs`/`getInfoString`/`checkPermsDict` fallbacks, direct-font synthetic negative object
numbers, `isXMPDate` no range check) are itemised in §3.

---

## 2. System map

**Read** (`document.go:Read`): slurp file → `parseHeader` (version + `headerOffset` for leading
junk) → `findStartXref` (last 1 KB) → probe absolute-vs-header-relative offset via
`xrefLooksValid` (`adjust`) → follow the `/Prev` chain with a `visitedXref` cycle guard, newest
section wins, trailer from the first section → step 4 parses every uncompressed object at its
offset (recording `doc.Offsets[num]` = absolute offset) → step 5 `loadCompressedObjects`
materialises type-2 (object-stream) entries as generation-0 objects, recording undecodable
containers in `brokenObjStms` (non-fatal) → step 6 `normalizeStructure` drops `/Type /XRef` and
`/ObjStm` objects from `doc.Objects` and strips xref-stream keys from the trailer → set
`Encrypted`. **Soft-recovery** exists for wrong stream `/Length` (endstream search) and broken
object streams; **hard aborts** for xref-structure errors, uncompressed-object parse errors, and —
inconsistently — a non-Integer `/Length` (C17) or an unsupported filter on the newest xref stream
(C18). The read path bounds-checks only the *first* xref offset, not `/Prev` or per-entry offsets
(the gap behind C1).

**Write** (`document.go:Write`): refuse if `Encrypted` → header → sort object numbers, serialize
each recording offsets → traditional xref via `writeXRefTable` (contiguous subsections, object 0
synthesized as the free-list head) → clone trailer, set `/Size` → trailer + `startxref` + `%%EOF`.
Write always regenerates a *traditional* xref, so xref-stream/hybrid inputs round-trip through a
table. It has no document-context fix-up for indirect `/Length` (C8) and prints `obj.Number`
rather than the map key (C7). Crucially it emits **21-byte** entry lines (C6).

**Validate** (`pdfa.go:ValidatePDFABytes(doc, level, rawData)`): runs ~50 independent check
functions from a static slice, then — only if `rawData != nil` — the byte-level checks, then sorts
errors `(Rule, Object, Message)` for determinism. `ValidatePDFA(doc, level)` is the same with
`rawData == nil`, so it silently skips **all** byte-level rules (see §4-D3). A per-run
`validationCache` (`pages`, decoded `content`, `directAnnots`) is installed on `doc.valCache` and
`defer`-cleared. **The cache is written onto the input `*Document`** — validating the same document
from two goroutines races (§4-D2); different documents are race-clean (EXECUTED with `-race`).

**Key invariants relied on:** `Resolve` returns the same `*Dictionary` pointer for repeated
resolution (pointer-keyed `seen` sets and the `collectFonts`↔`collectFontTextUsage` join depend on
this); `doc.Objects` holds only content objects post-Read; type-2 objects are generation-0. **Key
invariant assumed but not enforced:** every `lexer.SetPosition` caller passes a non-negative
in-range offset (violated → C1); every `arr[0]` in a validator is preceded by a length check
(violated once → C3); every recursive colour-space walker threads a visited-set (violated once →
C4).

---

## 3. Findings by category (severity order)

### Critical — process-fatal on untrusted input

**C1 — lexer.go:124,139 + document.go:123,138 — negative offset panics `Read`.**
`peek()`/`advance()` guard only `pos >= size`, never `pos < 0` (`peekAt` does guard it —
inconsistent). `SetPosition` stores unchecked. A trailer `<< /Prev -5 >>` sets
`sectionOffset = int64(prevOffset)+adjust` with no bounds check (document.go:123); a traditional
xref entry `-000000010 00000 n` is accepted by `ParseInt` (xref.go:100) then fed to
`SetPosition(entry.Offset+adjust)` (document.go:138). The same negative value is reachable from an
xref *stream* whose 8-byte `/W` field has the high bit set (`readField` overflows `int`). **EXECUTED:**
a minimal PDF with a negative entry offset panics `Read` with `index out of range [-10]`.
*Direction:* bounds-check `/Prev` and every entry offset in `Read`; add `pos < 0` guards to
`peek`/`advance`/`atEnd` as defence in depth.

**C2 — objstm.go:41,46 — huge `/N` overflows the sanity guard and panics.**
`if int64(n)*4 > int64(firstInt)` is meant to reject absurd `/N`, but for `/N`≈`MaxInt64`,
`int64(n)*4` wraps negative, the guard passes, and `make([]objStmEntry, 0, int(n))` panics
`makeslice: cap out of range`. `loadCompressedObjects` recovers from *errors*, not panics, so
`Read` crashes. **EXECUTED.** *Direction:* bound `n` by `firstInt/4` using division, before the
multiply/`make`.

**C3 — pdfa.go:5040 — empty-array colour space panics the validator.**
`collectSeparationConsistency` does `csType, _ := arr[0].(Name)` with no length check; its sibling
walkers all guard `len(arr) < 2`. A page with `/Resources << /ColorSpace << /CS0 [] >> >>` reaches
it via `checkSeparationDeviceN → collectTintTransforms`. **EXECUTED through the public
`ValidatePDFABytes`:** `index out of range [0] with length 0`. *Direction:* `if !ok || len(arr) == 0 { return }`.

**C4 — pdfa.go:5048 — self-referential DeviceN recurses without a cycle guard.**
`collectSeparationConsistency` recurses through DeviceN `/Colorants` threading **no** `seen` set,
unlike every other colour-space walker (`checkColorSpaceValueSeen`, `checkAlternateCSSeen`,
`checkCSForDeviceSeen`, all of which carry `seen map[int]bool`). A DeviceN whose `/Colorants`
indirectly references itself recurses infinitely → `fatal error: stack overflow`, which
`recover()` cannot catch. **EXECUTED** (agent). *Direction:* thread a visited-set keyed on the
IndirectRef number.

**C5 — package-wide — no panic boundary.**
`grep -rn "recover()"` over non-test code returns nothing. The check loop at pdfa.go:180 runs ~50
functions over untrusted structure with zero isolation, and `Read` likewise. Every panic in
C1–C4 (and C15, C30) therefore takes down the calling process. This is the structural reason the
individual panics are Critical rather than Medium. *Direction:* wrap each check (and `Read`) in a
`recover()` that converts a panic into a validation error / read error; a validator for hostile
files must never crash its host. (See design tension §4-D1.)

### High

**C6 — document.go:409,415 — the serializer writes non-conformant 21-byte xref entries.**
`entryLine` emits `"%010d %05d n \r\n"` = 21 bytes (a space **and** CRLF after the type char); the
free head `"0000000000 65535 f \r\n"` is likewise 21. ISO 32000-1 §7.5.4 requires exactly 20 bytes
with a 2-char EOL (`n\r\n`, or `n \r`, or `n \n`). pdf0's own `validXRefEntryLine`
(filestructure.go:516) accepts only those three forms. **EXECUTED:** `NewPDFADocument(level)` →
`Write` → `Read` → `ValidatePDFABytes` fails at **all four levels** with `[6.1.4] a cross-reference
entry is not in the fixed 20-byte format`. The library's headline "build a conformant PDF/A"
feature cannot produce a file that passes the library's own byte-level validator; pdf0's tolerant
line-based reader is why round-trip tests never noticed. Two independent readers found this.
*Direction:* drop the space before CRLF (`"%010d %05d n\r\n"`); add a builder→Write→ValidatePDFABytes test.

**C7 — document.go:355 / serializer.go:241 — Write trusts `obj.Number`, not the map key.**
`Read` stores `doc.Objects[num] = iobj` without asserting `iobj.Number == num`; `Write` records the
offset under the map key but `WriteIndirectObject` prints `obj.Number`. **EXECUTED:** an in-memory
`Objects[4]` holding an object numbered 3 writes a body `3 0 obj` under xref slot 4 — the written
file's `3 0 obj` is unreachable via the xref and object 4 is dangling for any external reader
(pdf0 itself resolves via the map, so `DocumentEqual` stays true and the round-trip test is blind).
A file whose xref points a slot at a mismatched body body triggers it on Read. *Direction:* on
Read, renumber (`iobj.Number = num`) or reject; on Write, assert key == number.

**C8 — serializer.go:222 — stale indirect `/Length` on Write.**
`writeStream` only synthesizes `/Length` when it is absent or inline; a `/Length 5 0 R` is
preserved and the referenced integer object is never updated to `len(Data)`. **EXECUTED:** replace
`Stream.Data`, `Write`, and the output still declares the old length; also, a file whose wrong
indirect Length the parser *recovered* is re-emitted with the wrong length. `Serializer` has no
document context, so the fix belongs in `Document.Write`. *Direction:* resolve and rewrite the
target length object (or inline it).

**C9 — filestructure.go:130-159 + document.go:168 — `doc.Offsets` desyncs from `doc.Objects`.**
`normalizeStructure` deletes XRef-stream and ObjStm container objects from `doc.Objects` but leaves
their entries in `doc.Offsets`. `checkIndirectObjectSyntax` builds its region list only from
offsets whose object still lives in `doc.Objects`, so a dropped object's offset is excluded and the
*previous* surviving object's region extends **through** it. Consequences: (a) the last content
object before a trailing xref stream — present in essentially every modern PDF — has its region run
to EOF, so its own endobj EOL formatting (6.1.8/6.1.9) is never checked; (b) a malformed endobj in
the dropped xref/ObjStm object is attributed to the content object. The corpus passes only because
those dropped objects' bytes happen to be well-formed. *Direction:* prune `doc.Offsets` in
`normalizeStructure`, or bound each region at the first `endobj` inside `[off, regionEnd)`.

**C10 — fontprog.go:200-217 — cmap format-4 CPU-exhaustion DoS.**
The inner `for c := start; c <= end && c != 0; c++` runs up to 65 534 iterations per segment, and
segment count scales with subtable size, with no total-work budget. **EXECUTED:** a 156 KB crafted
subtable took 29.6 s; a normal ~1 MB `FontFile2` scales to minutes. Reachable for any font that
shows text, with no `recover()` (C5). *Direction:* cap total emitted mappings / iterations.

**C11 — pdfa.go:1901-1988 — 1b transparency check misses SMasks and form groups.**
`checkNoTransparency` inspects only page-level `/Group` and ExtGState; image `/SMask` and form
`/Group /S /Transparency` are examined only by `resourcesUseTransparency`, which runs solely for
the 2b+ blending rule. **EXECUTED** against Isartor ground truth: `isartor-6-4-t01-fail-b` (image
soft mask) and `-t02-fail-a` (form transparency group) both validate to **0 errors**. The ratchet
walks only `PDF_A-{1b,2b,3b,4}` dirs, so Isartor never guards this. *Direction:* at 1b, also flag
image `/SMask` and form transparency groups (and annotation `CA`/`ca`/`BM`).

**C12 — pdfa.go:1235,1681,523,914,1280,2087 — un-`Resolve`d `Get()` lets indirect refs evade rules.**
A systemic pattern: values are type-asserted straight off a dictionary without `doc.Resolve`, so
placing a value behind an indirect reference bypasses the rule. **EXECUTED:** an annotation with
`/Subtype 101 0 R → /Screen` produces **0** subtype errors at 2b, while the direct `/Screen`
produces 1. The same shape hides `/S`/`/N` action names, OutputIntent `/S` (also causes an FP
"must be a name" on a *legal* indirect `/S`), `/Filter` (hiding `LZWDecode`), and non-Integer/
indirect `/F` annotation flags. *Direction:* one sweep resolving before asserting; corpus-safe
(only adds strictness for indirect forms the corpus doesn't use).

**C13 — pdfa.go:1716-1749 — `/AA` on non-widget annotations unflagged at 1b/2b/3b.**
`checkWidgetAA` requires `Subtype == Widget` or an `FT` key, but ISO 19005-1/-2 forbid `/AA` in
*any* annotation; `checkA4TriggerEvents` covers only A-4. **EXECUTED:** a 2b `Link` annotation with
`/AA` yields 0 errors. The function's own comment promises coverage it doesn't implement.
*Direction:* extend to all annotation dicts at 1b–3b.

**C14 — README.md:100-106 — "Status and limitations" is inverted on all three bullets.**
It claims object streams and xref-stream predictors are "not yet implemented" (both are —
`objstm.go`, `filters.go`) and "encrypted PDFs are not detected" (they are — `document.go:156`,
and `Write` refuses them). A newcomer rejects the library for gaps it doesn't have, or files
already-fixed bugs. *Direction:* rewrite from the current ratchet (FP=0, missed=0, parseErrors=0);
state the real limits — no decryption, always writes a traditional xref, corpus-scoped rule coverage.

### Medium

**C15 — compare.go:64 / serializer.go:188 — cyclic direct objects overflow the stack.**
`Equal`, `dictionaryEqual`, and `WriteObject` recurse with no depth cap (unlike the parser's
`maxParseDepth`). **EXECUTED:** `d.Set("Self", d); Equal(d, d2)` → `fatal error: stack overflow`
(unrecoverable). The parser can't *build* direct cycles, but `Dictionary` fields are exported and
`pdfa_create.go` constructs trees programmatically. *Direction:* depth cap in `Equal`/`WriteObject`.

**C16 — document.go:433 — an in-use object 0 is silently lost.**
`Write` serializes all of `d.Objects` including key 0, but `writeXRefTable` skips `num <= 0` and
hardcodes entry 0 as the free-list head. **EXECUTED:** a file with an in-use object 0 reads,
writes a `0 0 obj` body the xref marks free, re-reads without it; `DocumentEqual` = false, no
error. *Direction:* reject/renumber object 0 on Read, or error in Write.

**C17 — parser.go:310 — a non-Integer `/Length` aborts Read.**
`<< /Length 11.0 >>` (a `Real`) returns `invalid Length type` and fails the whole `Read`, though
the endstream-search recovery path could handle it. **EXECUTED.** Contradicts the stated
recover-from-malformed-structure goal. *Direction:* treat unexpected `/Length` types as `-1` (search).

**C18 — xref.go:303 / document.go:104 — unsupported filter on the newest xref/object stream aborts Read.**
`decodeStreamData` knows only Flate/ASCIIHex; a legitimate LZW-compressed xref stream makes
`parseXRefSection` error, and because it is the first (newest) section, `Read` returns the error
rather than tolerating it (older sections are tolerated). *Direction:* distinguish
unsupported-but-legal from corrupt, as the validator paths already do via `streamFiltersSupported`.

**C19 — objstm.go:111 — a broken object stream silently drops its objects.**
On `parseObjStmIndex` failure, `loadCompressedObjects` records `brokenObjStms` and continues
without adding the objects. Intended so the validator can *report* it, but nothing compensates the
Read/Write path: `Resolve` returns nil and `Write` emits a document whose `/Root`/`/Pages` may
reference now-absent objects. A Read→Write user who never validates gets silent data loss.
*Direction:* have `Write` refuse or warn when `brokenObjStms != nil`.

**C20 — pdfa.go:483-508 — A-4 page-level OutputIntent errors dropped on early returns.**
`errsPageLevel` is computed, but the "OI target not found", "not an array", and `len(arr)==0`
branches all `return` without appending it. **EXECUTED:** a page `/OutputIntents [<</S /GTS_PDFX>>]`
with an empty catalog OI array yields 0 errors. *Direction:* append `errsPageLevel` on every path.

**C21 — pdfa.go:976,1079 — form-XObject-only fonts escape embedding checks.**
`collectFonts` reads `Resources/Font` on Page/Pages nodes only; embedding is enforced nowhere
else. An unembedded font used solely inside a form XObject (or pattern/appearance stream) passes.
`checkFontSubsets` shares the gap. *Direction:* also iterate `collectFontTextUsage` keys.

**C22 — pdfa.go:2687 — one-sided XMP date normalization → 6.7.3 FP.**
`normalizePDFDate` folds `±00:00→Z` and pads seconds; `normalizeXMPDate` is just `TrimSpace`.
**EXECUTED:** Info `D:20240101120000Z` vs XMP `2024-01-01T12:00:00+00:00` (same instant) is
flagged inconsistent. *Direction:* canonicalize both sides identically.

**C23 — pdfa.go checkMetadataVersion / final_rules.go:552 — A-4f/4e embedded file wrongly rejected.**
`declaredPDFALevel` maps any `pdfaid:part 4` to `PDFA4`, and `checkMetadataVersion` at PDFA4 flags
*any* `pdfaid:conformance`. So a valid 4f/4e embedded file fails 6.9. **EXECUTED.** The 4f/4e corpus
suites are never validated (C37), so the ratchet is blind. *Direction:* accept conformance F/E at
part 4 (or model distinct 4f/4e levels).

**C24 — xmp_schemas.go:1121 — BOM-less UTF-16/32-LE XMP passes the A-4 UTF-8 rule.**
NUL bytes are valid UTF-8, so ASCII-content UTF-16LE without a BOM survives `utf8Valid`, and the
UTF-16/32 branches only check for BOMs. **EXECUTED:** a BOM-less UTF-16LE XMP packet → 0 errors at
A-4. *Direction:* mirror `decodeXMPToUTF8`'s BOM-less heuristics in `xmpIsUTF8`.

**C25 — final_rules.go:127 / pdfa.go:4798 — `BI…ID` in a string literal fabricates an inline image.**
The inline-image scanners run over raw content bytes with no literal-string/comment skipping.
**EXECUTED:** content `BT (see BI /I true ID hello EI done) Tj ET` → `[6.2.8] an inline image uses
Interpolate true`. The `skipInlineImage` heuristic also ends the binary section at the first
whitespace-delimited `EI`, so binary sample data containing `EI` truncates the image and spews the
remainder as bogus operators/hex strings. None of the three inline-image code paths consult `/L`.
*Direction:* use a literal-aware tokenizer and honour `/L`.

**C26 — compare.go:130 — `dictionaryEqual` is wrong on duplicate keys.**
It checks length + "every key of `a` exists in `b` with equal value", and `Get` returns the first
match. **EXECUTED:** `Equal({A:1,A:1},{A:1,B:99})` = true; `Equal(d,d)` for `{A:1,A:2}` = false.
Parser-sourced dicts are deduped, but the exported `Keys`/`Values` and the serializer can produce
duplicates. *Direction:* iterate both dicts, or normalize duplicates.

**C27 — filestructure.go:693-720 — Type3 CharProcs omitted from content scanning.**
The doc comment claims `collectContentStreamData` returns "Type3 CharProcs", but the loop accepts
only Form XObjects and patterns. Every consumer (hex-string format, inline-image filter/intent,
content implementation-limits) therefore never inspects Type3 glyph streams — a CharProc with an
odd-length hex string, LZW inline image, or over-limit operand goes uncaught. The operator
whitelist *does* handle CharProcs separately (proving the omission is unintended). *Direction:*
add CharProc streams to the collector.

**C28 — final_rules_test.go:32 — a test that cannot fail.**
The "not applied at other levels" branch of `TestProhibitedCatalogEntries` does
`if len(checkProhibitedCatalogEntries(..., PDFA2b)) != 0 { t.Skip() }`. If level gating regresses
(6.11/6.12 fires at 2b), the test **skips** instead of failing. **EXECUTED-confirmed** by reading
+ compiling. Almost certainly a typo for `t.Error`. *Direction:* `t.Error`.

**C29 — no CI.** `.github` is absent (`Remove CI workflow`, f46df7a). The corpus ratchet, `gofmt`,
and `vet` run only when a developer with a local corpus remembers to. A PR regressing
FP/missed/parseErrors merges green. The corpus suite is ~3 s, so the usual "too slow" excuse
doesn't apply. *Direction:* restore build/vet/gofmt/test + an optional cached-corpus job.

### Low / Info

- **C30 — serializer.go:53 / compare.go:75** — `WriteObject((*Dictionary)(nil))` and
  `Equal((*Dictionary)(nil), d)` nil-pointer panic; the untyped-`nil` guard doesn't catch a
  non-nil interface holding a nil pointer. **EXECUTED.**
- **C31 — serializer.go:165** — emits `/#00` for a NUL in a name, which lexer.go:404 then rejects;
  serializer output should be reparseable. **EXECUTED.** (Mirror `writeReal`'s NaN/Inf refusal.)
- **C32 — pdfa.go:1779,1876 / xmp_schemas.go:904** — the raw-string XMP layer only recognises
  double-quoted attributes: single-quoted `pdfaid:part='2'` FP-fails "must contain pdfaid:part";
  a URI inside an XML comment FP-fires the extension-schema prefix rule; a single-quoted `xmlns`
  prefix FN-escapes it. All **EXECUTED**. (The DOM parser in `xmp.go` handles quotes correctly;
  only the string layer is affected — see §4-D4.)
- **C33 — fontprog.go:693** — `/lenIV` is parsed correctly by `sscanInt`, the value discarded, then
  `parseLeadingInt` re-parses starting on the space and returns 0 → any Type1 font with explicit
  `/lenIV` gets `lenIV=0`, corrupting charstring width extraction. **EXECUTED.**
- **C34 — final_rules.go:17** — `checkProhibitedCatalogEntries` returns nil unless `PDFA4`, but ISO
  19005-2/-3 §6.11 also prohibits AlternatePresentations/PresSteps at 2b/3b. PLAUSIBLE (corpus-silent).
- **C35** — rule-ID drift: `checkHalftoneErrors` hardcodes `6.2.5` while the 1b caller wants `6.2.8`
  (pdfa.go:2365); `checkType5Halftones` has a no-op `if level==PDFA4 { rule="6.2.5" }` and never
  assigns `num`, so halftone errors report `Object: 0` (final_rules.go:364-385).
- **C36 — docs long tail:** godoc on `ValidatePDFA`/`ValidatePDFABytes` says "returns nil if
  conformant", overclaiming vs the README's careful hedge (pdfa.go:55,63); the README Layout table
  omits `fonts.go`, `filestructure.go`, `content_operators.go`, `xmp*.go`, `fontprog.go`, etc.; the
  spec-extraction pipeline and `spec/` acquisition are undocumented (C32-prior); all three examples
  write `output.pdf` into the cwd and `simple_pdfa` prints "PDF/A-4 conformant" over a 1-byte
  placeholder font and an unverified stub ICC profile.
- **C37 — coverage:** `TestCorpus` validates only `PDF_A-{1b,2b,3b,4}`; Isartor, PDF_A-4f, and
  PDF_A-4e are parse-only. An executed sweep found **18 of 204 Isartor fail files** validate to 0
  errors at 1b (transparency C11, XMP well-formedness, damaged TrueType, CMap, encodings, extension
  schemas, `/Length` mismatch, appearance operators). A `TestCorpusIsartor` ratchet (missed
  baseline 18) would stop these regressing invisibly.
- **Other Low (traced, not itemized above):** denormal `Real` serializes to 300+ chars
  (serializer.go:83); absolute epsilon makes all sub-1e-10 values equal, `Equal(x,x)` unreliable
  for NaN (compare.go:146); `WriteObject` rejects value `Dictionary`/`Stream` that `Equal` accepts;
  a stored `nil` value is indistinguishable from a missing key in `dictionaryEqual`;
  `findStartXref` scans only the last 1024 bytes; negative `startObj` in an xref subsection header
  is accepted; `NewLexerFromReaderAt` is exported and unused; `hasForbiddenUnicodeTargets` lacks the
  `lo>hi` guard its siblings have and misaligns on array-destination bfrange (fonts.go:604); dead
  `localSubrs` (fontprog.go:415), `getInfoString` (pdfa.go:2535), `checkPermsDict` fallback
  branches (pdfa.go:842); direct fonts get synthetic negative object numbers surfacing as
  "object -1"; `isXMPDate` accepts `2024-13-45T99:99Z`.

---

## 4. Design tensions

**D1 — No panic boundary for a hostile-input processor.** The library's stated purpose is reading
and validating untrusted PDFs, yet there is not a single `recover()` in non-test code, and the
validator dispatches ~50 checks that freely index slices and assert types over
attacker-controlled structure. Four confirmed panics (C1–C4) and the stack overflows (C4, C15) are
all fatal to the host. *Alternative:* a small `runCheck` wrapper that `recover()`s each check into
a synthetic `ValidationError{Rule:"internal"}`, and a `recover()` in `Read` returning an error.
This converts the entire class from "crash the process" to "report a defect" and is cheaper than
auditing every index site — though both should be done. This is the single highest-leverage change.

**D2 — Validation mutates its "read-only" input.** `ValidatePDFABytes(doc, …)` installs
`doc.valCache` on the shared `*Document`, so the natural affordance — "validate the same parsed doc
against several levels concurrently", or share one immutable doc across request handlers — is a
**data race** (EXECUTED with `-race`; different-doc parallelism is clean). The signature invites
what the implementation forbids, and the only guard is a doc comment. *Alternative:* thread a
`*validationRun` context struct (holding the caches) through the check functions instead of hanging
it off `Document`; the doc becomes truly immutable and concurrency-safe.

**D3 — Two validation entry points give different verdicts on the same document.**
`ValidatePDFA(doc)` silently skips *every* byte-level file-structure rule (they require `rawData`);
`ValidatePDFABytes(doc, level, bytes)` runs them. So `NewPDFADocument` "passes ValidatePDFA" (true,
in-memory) while its serialized form fails `ValidatePDFABytes` at 6.1.4 (C6). A caller who reaches
for the shorter-named, simpler function gets a materially weaker check with no signal that it is
weaker. *Alternative:* fold the two into one entry point taking an optional `[]byte`, and have it
*report* (not silently skip) that byte-level rules were not run when bytes are absent.

**D4 — Two parallel XMP access layers that disagree.** A real DOM parser (`xmp.go`, used only by
`checkXMPProperties` and extension schemas) coexists with a pile of raw-string scanners
(`extractXMPValue`, `xmpHasKey`, `countXMPListEntries`, `checkMetadataVersion`'s literal
`xmlns:pdfaid="…"` match). Every quote-handling and comment-awareness bug (C32) lives only in the
string layer; the version/consistency/date/author-list checks that matter most for conformance all
sit on the fragile layer. *Alternative:* migrate the version and consistency checks onto the DOM
parser and delete the string scanners — this closes an entire bug class at once.

**D5 — `DocumentEqual` measures model fidelity, not file conformance, and is the only round-trip
oracle.** `roundtrip_test.go` asserts `Read→Write→Read→DocumentEqual`, which compares in-memory
object graphs. It is therefore structurally blind to every case where the *written bytes* are worse
than the input: stale `/Length` (C8), duplicate/dangling object bodies (C7), lost object 0 (C16),
and 21-byte xref lines (C6) all pass the round-trip test. *Alternative:* add a
`Read→Write→ValidatePDFABytes` golden test and a byte-level assertion for at least the reference
corpus; treat "the writer's output validates" as a first-class invariant.

---

## 5. Expectation gaps (expected X, found Y)

- **Builder.** Expected `NewPDFADocument` to produce a conformant PDF/A file (README: "Build a
  minimal conformant PDF/A document"). Found output that fails the package's own `ValidatePDFABytes`
  6.1.4 at every level (C6); it only "passes ValidatePDFA" because that entry point skips byte-level
  rules (D3).
- **Validate is read-only.** Expected `ValidatePDFABytes(doc, …)` to be a pure query safe to run
  concurrently on one doc. Found it mutates `doc.valCache` and races (D2).
- **Recovery is uniform.** Expected the "recover from malformed structure" design to apply to a
  wrong-typed `/Length` and an unsupported-filter xref stream. Found both are hard aborts (C17, C18)
  while wrong-*valued* Length and broken object streams are soft.
- **Indirect references are transparent.** Expected a `/Subtype 12 0 R` to be validated like a
  direct `/Screen`. Found it evades the rule entirely (C12) — indirection is a validation-bypass.
- **Docs describe the code.** Expected README "Status" to list real gaps. Found it lists three
  *non*-gaps as gaps (C14); the Layout table and the linked audit both describe the February
  skeleton, not July `HEAD` (C36, and this file supersedes the stale audit).
- **A green test suite means the invariants hold.** Found `go test ./...` passes while the
  round-trip writer emits malformed files (D5), a level-gating test can only skip (C28), the
  headline round-trip guarantee is untested on a fresh clone (no reference PDFs committed), and
  nothing runs the examples.

---

## 6. Open questions (code alone can't resolve)

1. **Should `Write` ever emit an xref *stream*?** It always downgrades to a traditional table, so a
   >10 GB file (offset > 9,999,999,999) silently produces >20-byte lines (a Low today). Is
   traditional-only a deliberate scope limit or a gap?
2. **Is the 4f/4e corpus intentionally excluded from the ratchet**, or an oversight? C23 and C37
   hinge on this — validating those suites would surface real conformance semantics but might
   require modelling F/E as distinct levels.
3. **What is the intended contract of `brokenObjStms` for `Write`?** The read path deliberately
   tolerates broken object streams for the validator's benefit, but whether a downstream `Write`
   should refuse, warn, or best-effort emit (C19) is a product decision.
4. **How much of the "systemic un-Resolve" (C12) is corpus-safe to fix in one sweep?** The claim is
   that resolving-before-asserting only adds strictness for indirect forms the corpus doesn't use —
   this needs a full corpus re-run to confirm no new false positives.
5. **Is the absolute-epsilon numeric comparison (C36) ever load-bearing** for legitimate tiny
   coordinate/matrix values, or is 1e-10 always safe? A relative epsilon would change round-trip
   equality semantics.

---

*Prior-audit triage (which of C1–C32 / A1–A32 are fixed) is in §7.*

## 7. Prior-audit (`codebase-audit-2026-07-07.md`) status

Fixed since that audit — verified by reading/executing on `HEAD`: **C1** (ObjStm parser exists,
`objstm.go`), **C2** (PNG/TIFF predictors, `filters.go`), **C3/C18/A1/A2/A3** (page-tree and
colour-space walkers now carry `seen` sets — *except* `collectSeparationConsistency`, which is this
audit's C4), **C4** (`/Prev` visited-set), **C5** (`normalizeStructure`), **C6** (`inspect_fps_test`
deleted; ratchet green on fresh clone), **C7** (`maxParseDepth=1000`), **C8** (even-`/Index`
check), **C9** (negative-`/W` check), **C10** (object-number overflow rejected), **C11** (encryption
detected + Write refuses), **C12** (delimited-`endstream` + `endstreamFollowsAt`), **C13** (NaN/Inf
refused), **C14** (`Clone()` before mutate), **C15** (value Dict/Stream cases), **C16** (EOF vs
error), **C17** (`1.2.3` rejected — EXECUTED), **C19** (compact subsections — but the 21-byte bug
C6 remains), **C20** (compare docstring), **C21** (`NewPDFADocumentWithInfo`), **C22** (shared
epsilon), **C23** (gofmt clean, real README — but CI *removed*, this audit's C29), **C24** (dead
`trimSpace`/`putBEUint32` gone), **C25** (`#00` rejected — EXECUTED), **C27** (short-read errors),
**A4** (date-slice guards — EXECUTED no panic on truncated dates), **A7** (64-hop Resolve), **A8**
(Type0 DescendantFonts now handled), **A9** (direct-dict annotations collected into the cache),
**A11** (FileAttachment allowed), **A16** (SetState/NOP).

Still open (carried into this audit): **C26** (duplicate dict keys, now C26/§3), **C28**
(unbounded keyword search — mostly mitigated; latent in C-series filestructure findings), **C29**
(stub ICC never veraPDF-verified — C36), **C30** (xmlEscape — now drops illegal control chars, low
residual), **C31/C36-prior** (gitignored reference PDFs / undocumented spec pipeline — C36),
**A25** (flat `doc.Objects` scans vs reachable-graph), **A28** (1b rule-ID numbering — see C35),
**A30** (godoc "nil if conformant" overclaim — C36). **A27** (unimplemented rule families) is
closed *against the corpus* (missed=0) but C11/C12/C13/C37 show meaningful false-negatives outside
the corpus's coverage.
