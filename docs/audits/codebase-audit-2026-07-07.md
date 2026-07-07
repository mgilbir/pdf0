# pdf0 Codebase Audit — 2026-07-07

Adversarial, full-read audit of the `github.com/mgilbir/pdf0` PDF 2.0 parser / serializer /
PDF/A validator. Every finding carries a stable ID (`C1`, `C2`, … in severity order) that a
fixing agent can cite. Findings marked **CONFIRMED** were traced to specific lines and, in most
cases, reproduced by executing the code; **PLAUSIBLE** were traced but not executed.

Method: four parallel readers covered (a) lexer/parser/object/compare, (b) serializer/xref/document,
(c) the 4,795-line PDF/A engine, (d) builder/examples/docs/DX. The auditor independently
re-verified the highest-value findings by compiling standalone programs against the module and by
running the test suite and veraPDF corpus.

---

## 1. Summary table

| ID  | Sev | Area | Issue | file:line | Status |
|-----|-----|------|-------|-----------|--------|
| C1  | Critical | xref/read | Object streams (type-2 xref entries) silently dropped — no ObjStm parser exists | document.go:131 | CONFIRMED |
| C2  | Critical | xref/read | No PNG/TIFF `/Predictor` support — real-world xref streams decode to garbage | xref.go:180,248 | CONFIRMED |
| C3  | Critical | pdfa/DoS | Cyclic Pages/Kids tree → uncatchable stack overflow in validator | pdfa.go:2815,957 | CONFIRMED |
| C4  | Critical | read/DoS | Cyclic `/Prev` xref chain → infinite loop in `Read` | document.go:72 | CONFIRMED |
| C5  | Critical | write | Doc read via xref stream is written back malformed (stale `/XRef` object + polluted trailer) | document.go:117,239 | CONFIRMED |
| C6  | High | test-suite | `make test` never passes: `TestInspectFPs` hard-fails w/o corpus; `TestCorpus` 813-red with it | inspect_fps_test.go:44, pdfa_test.go:1134 | CONFIRMED |
| C7  | High | parse/DoS | No recursion-depth limit in parser → stack overflow on deeply nested input | parser.go:170,196 | CONFIRMED |
| C8  | High | xref/DoS | Odd-length `/Index` panics (`index out of range`) | xref.go:187 | CONFIRMED |
| C9  | High | xref/DoS | Negative/absurd `/W` widths panic (`slice bounds out of range`) | xref.go:142,192 | CONFIRMED |
| C10 | High | correctness | Overflowing object/gen numbers silently accepted as garbage (`Atoi` error dropped) | parser.go:145,333 | CONFIRMED |
| C11 | High | read | Encrypted PDFs neither detected nor errored — parsed as plaintext garbage | document.go:18 | CONFIRMED |
| C12 | Medium | correctness | Indirect/absent `/Length` stream delimited by first `endstream` substring in binary data | parser.go:242 | CONFIRMED |
| C13 | Medium | serializer | Non-finite `Real` serialized as `NaN.0`/`+Inf.0` — invalid PDF token | serializer.go:77 | CONFIRMED |
| C14 | Medium | serializer | `writeStream`/`Write` mutate the caller's dict via shared backing arrays | serializer.go:212, document.go:284 | CONFIRMED |
| C15 | Medium | compare | `Equal` returns false for value (non-pointer) `Dictionary`/`Stream` | compare.go:65 | CONFIRMED |
| C16 | Medium | correctness | Lexer error after an integer look-ahead is swallowed | parser.go:122 | CONFIRMED |
| C17 | Medium | correctness | `1.2.3` lexes into two Reals instead of erroring | lexer.go:408 | CONFIRMED |
| C18 | Medium | pdfa/incoherence | Cycle guards present in some graph walkers, absent in others | pdfa.go:2633,1795 vs 2815,957 | CONFIRMED |
| C19 | Medium | write | Traditional xref free-list linkage not spec-conformant; no subsection compaction | document.go:305 | CONFIRMED |
| C20 | Medium | docs | `compare.go` docstring claims it follows indirect refs; it does not | compare.go:8 | CONFIRMED |
| C21 | Low | affordance | `NewPDFADocument` hardcodes empty title/author despite XMP support | pdfa_create.go:33 | CONFIRMED |
| C22 | Low | correctness | `Equal(Integer(1), Real(1.0))` == true; asymmetric epsilon vs Real↔Real | compare.go:23 | CONFIRMED |
| C23 | Low | dx | 3 files not gofmt-clean; no CI; 6-byte README | (repo) | CONFIRMED |
| C24 | Low | dead-code | `trimSpace` (xref.go:352), `putBEUint32` (pdfa_create.go:347) unused | xref.go:352 | CONFIRMED |
| C25 | Low | correctness | `#00` (NUL) accepted in names; no token-size limits anywhere | lexer.go:371 | CONFIRMED |
| C26 | Low | correctness | Duplicate dict keys silently merged (last wins) with no diagnostic | parser.go:226 | CONFIRMED |
| C27 | Low | read | `NewLexerFromReaderAt` ignores short-read count; trailing NULs masked as whitespace | lexer.go:89 | PLAUSIBLE |
| C28 | Low | xref/find | `startxref`/`trailer` located by unbounded substring search; can match stream data | document.go:176,214 | PLAUSIBLE |
| C29 | Low | builder | `DefaultSRGBProfile` / example font are stubs never checked against real ICC/veraPDF | pdfa_create.go:198 | PLAUSIBLE |
| C30 | Low | builder | `xmlEscape` passes through XML-illegal control chars into XMP | pdfa_create.go:174 | PLAUSIBLE |
| C31 | Low | dx | Reference PDFs, spec, corpus all gitignored; core round-trip test skips on fresh clone | .gitignore | CONFIRMED |
| C32 | Info | repro | Spec-extraction pipeline (`pdftotext` step, gitignored `spec/`) undocumented | cmd/extract_spec_examples | CONFIRMED |
| A4  | Critical | pdfa/DoS | `normalizePDFDate` unguarded `s[17:]` → panic on 15/16-char date with tz sign | pdfa.go:2368 | CONFIRMED |
| A2  | Critical | pdfa/DoS | `checkCSForDevice` recurses Indexed/Pattern base with no cycle guard → stack overflow | pdfa.go:3934 | CONFIRMED |
| A3  | Critical | pdfa/DoS | `checkAlternateCS` recurses Separation/DeviceN alternates with no cycle guard | pdfa.go:4617 | CONFIRMED |
| A5  | High | pdfa/FP | `/Type /ObjStm` streams (integer `/N`) misdetected as ICC profiles → false positive | pdfa.go:4299 | CONFIRMED |
| A7  | High | pdfa/FP+FN | `Resolve` is single-step; ref→ref chains yield FP ("must be a stream") or silent FN | document.go:352 | CONFIRMED |
| A8  | High | pdfa/FN | `checkFontsEmbedded` `continue`s on Type0, making DescendantFonts handling dead code | pdfa.go:891 | CONFIRMED |
| A9  | High | pdfa/FN | Annotation/action checks scan only top-level objects; direct-dict annots bypass all | pdfa.go:1075 | CONFIRMED |
| A11 | High | pdfa/FP | `FileAttachment` missing from allowed subtypes → breaks PDF/A-3's core use case | pdfa.go:1033 | CONFIRMED |
| A12 | High | pdfa/FP (diff regression) | DeviceN >8-colorant limit applied at all levels; correct only for A-1 | pdfa.go:4561 | CONFIRMED |
| A13 | High | pdfa/FN | Separation/DeviceN checks only fire when Resources is indirect (common direct case dead) | pdfa.go:4395 | CONFIRMED |
| A15 | High | pdfa/FP | `checkQNestingDepth` counts `q`/`Q` inside string literals → false positive | pdfa.go:3282 | CONFIRMED |
| A16 | High | pdfa/FN (corpus) | Deprecated actions checked as `set-state`/`no-op`, real files use `SetState`/`NOP` | pdfa.go:1374 | CONFIRMED |
| A17 | Medium | pdfa/FP (diff regression) | New ICC-decode-error FP fires on legal ASCII85/RunLength/array-filtered profiles | pdfa.go:608 | CONFIRMED |
| A27 | High | pdfa/missing | Whole rule families unimplemented (fonts, XMP schemas, operators, XObjects…) → FN≈817 | pdfa.go (aggregate) | CONFIRMED |

*(Full PDF/A-engine findings A1–A32, with the requirement-family inventory, in §3.9.)*

---

## 2. System map

**Read path** (`document.go:Read`): the whole file is slurped into memory. `parseHeader` finds
`%PDF-` in the first 1024 bytes and records a `headerOffset` for files with leading junk.
`findStartXref` scans the last 1024 bytes for the final `startxref` and reads the offset (adding
`headerOffset`). One token is peeked at that offset: `xref` → traditional table
(`ParseXRefTable`, line-based, tolerant of non-20-byte entries) + `findTrailer` (forward substring
search for `trailer`) + a `/Prev`-chain merge loop; an integer → an indirect object is parsed,
expected to be a `*Stream`, and handed to `ParseXRefStream`, with **the stream dict used verbatim
as the trailer**. Finally every uncompressed, non-free xref entry is parsed at
`entry.Offset + headerOffset`. **Compressed (object-stream) entries are skipped entirely — never
loaded.**

**Lexer → parser → object model**: `lexer.go` tokenizes `[]byte` in place; `Token.Value` holds the
*decoded* payload (escapes/hex/`#XX` resolved) and `Token.Offset` an absolute index. `parser.go`
wraps one lexer with a slice look-ahead buffer; `ParseObject` is recursive descent, using 3-token
look-ahead to disambiguate `N G R` / `N G obj` / bare integer. `String.IsHex` is reconstructed
after the fact by re-reading `data[tok.Offset]=='<'`. Streams read raw bytes by `/Length` (Integer)
or by scanning forward for the literal `endstream`.

**Write path** (`document.go:Write`): fresh header at offset 0, objects sorted (insertion sort) and
emitted with recorded byte offsets, then **always** a traditional xref table, then the trailer
(with `/Size` injected), `startxref`, `%%EOF`. Byte-offset accounting is correct.

**PDF/A validation** (`pdfa.go`): `ValidatePDFA` → `ValidatePDFABytes` runs a fixed slice of ~40
`check*` functions, each returning `[]ValidationError`; results are concatenated. Checks resolve
indirect refs through `doc.Resolve`/`doc.ResolveDict`, walk the page/resource graph, and scan
decoded content streams with a proper operator tokenizer (`scanStreamForDeviceOps`).

**Key invariants (explicit or assumed):**
- `l.pos` is the single source of truth for lexer position; the parser reads it directly for stream
  bodies, so the look-ahead buffer must be empty when a stream body starts (it is, in the dict flow).
- Dictionary key insertion order is preserved via parallel `Keys`/`Values` slices — but those slices
  are shared on struct copy, which C14 exploits.
- `doc.Objects` is the object table; anything not in it (dropped ObjStm objects, missing refs)
  resolves to `nil` silently.

**What is sound (verified, stated once):** balanced-paren literal-string scanning (escapes, 1–3
digit octal, line continuations, CR/CRLF→LF); hex odd-digit padding; name `#XX` bounds checking;
byte-offset accounting and complete string/name escaping on write; finite-Real serialization never
emits exponent form; the content-stream operator tokenizer correctly skips strings, comments, hex,
names, and inline-image binary; validation is idempotent across repeated calls; the validator does
**not** panic on wrong-typed catalog keys (Root-as-array, Pages-as-int, etc. all handled). The seven
bundled reference PDFs (all traditional xref, no object streams) round-trip Read→Write→Read→equal.

---

## 3. Findings by category (severity order)

### 3.1 Critical

**C1 — Object streams silently dropped — document.go:131-134, xref.go:222-228 — CONFIRMED.**
Type-2 (compressed) xref entries are recorded (`XRefEntry.Compressed`) then `continue`d past in the
Read loop; there is no `/Type /ObjStm` parser anywhere in the package. Any PDF 1.5+/2.0 that packs
its catalog, pages, or metadata into object streams (the overwhelming majority of modern files)
loads as a `Document` missing those objects — `doc.Resolve` of them returns `nil` — and a subsequent
`Write` emits a structurally broken file. Reproduced: a hand-built xref stream referencing two
compressed objects yields `doc.Objects` missing both. **Direction:** parse `/Type /ObjStm`
(`/N`, `/First`, the leading `objnum offset` index pairs) and materialize compressed objects during
Read.

**C2 — No `/Predictor` support — xref.go:180, 248-290 — CONFIRMED.**
`decodeStreamData` inflates FlateDecode data but never applies the PNG/TIFF predictor reversal that
`/DecodeParms /Predictor 12` demands — and virtually every real-world xref stream uses a predictor.
The word `Predictor`/`DecodeParms`/`Columns` appears nowhere in non-test code. `ParseXRefStream`
therefore reads predictor-encoded bytes as raw entries → garbage offsets/types → wrong objects or
`xref stream data truncated`. This is the root cause of the 9 corpus files (including a valid
PDF/A-2b *pass* file) that fail to parse. **Direction:** implement predictor reversal in
`decodeStreamData` (keyed off `/DecodeParms`), applied before xref-field parsing and to any Flate
stream carrying DecodeParms.

**C3 — Cyclic page tree crashes the validator — pdfa.go:2815-2835, 957-976 — CONFIRMED.**
`collectPagesRecursive` and `collectFontsRecursive` recurse through `Kids` with no visited-set. A
Pages node whose `Kids` references an ancestor (a two-node A→B→A cycle suffices) causes unbounded
recursion → `fatal error: stack overflow`, which `recover()` cannot catch — the whole process dies.
Reproduced: `ValidatePDFA` on a 3-object doc with a cyclic Pages tree aborts with a stack overflow.
Validating an untrusted PDF is a documented use case, so this is a remote DoS. **Direction:** thread
a `seen map[int]bool` (by object number) through both walkers, matching the guards already present in
`resourcesUseTransparency` and `collectAllExtGState` (see C18).

**C4 — Cyclic `/Prev` chain hangs Read — document.go:72-100 — CONFIRMED.**
The `/Prev` following loop has no visited-set and no iteration cap. A trailer whose `/Prev` points at
its own xref offset loops forever. Reproduced: `Read` on a crafted PDF with a self-referential
`/Prev` never returns (killed at timeout). **Direction:** track visited offsets or cap iterations.

**C5 — Xref-stream document written back malformed — document.go:117-118, 239-347 — CONFIRMED.**
When a file is read via an xref stream, its stream dictionary is used verbatim as `d.Trailer`, and
`Write` always emits a *traditional* xref table. The result: (a) the written trailer carries
xref-stream-only keys — reproduced output `trailer << /Type /XRef /Root 1 0 R /W [1 2 1] /Size 2 >>`
— and (b) the original `/XRef` stream object is re-emitted as a stale regular object whose binary
Data now encodes obsolete offsets. Any conformant reader sees contradictory/garbage xref+trailer
data. **Direction:** on write, strip xref-stream-only keys (`/Type /XRef /W /Index /Filter /Length
/DecodeParms /Prev`) from the trailer and drop or regenerate the `/XRef` object; or emit a proper
xref stream.

### 3.2 High

**C6 — `make test` cannot pass — inspect_fps_test.go:44, pdfa_test.go:1134-1203 — CONFIRMED.**
Two independent failures make the default suite red in every state of the world. (1) `TestInspectFPs`
(committed debug scaffold, commit d51b70d) `t.Fatalf`s on four hardcoded paths under the *gitignored*
`testdata/verapdf-corpus/`, so a fresh clone + `go test ./...` fails immediately, looking like a
broken checkout. (2) Once `make corpus` is run, `TestCorpus` — whose default path is that same dir,
needing no env var — fails 813/2055 subtests (804 fail-files the validator passes + 9 parse errors),
matching the FN≈817 in project memory. **Direction:** delete or skip-gate `inspect_fps_test.go`;
convert `TestCorpus` to a detection-rate threshold or known-failures allowlist so regressions surface
against a green baseline. Its `fmt.Printf` dumps should be `t.Logf`.

**C7 — No parser recursion limit — parser.go:65, 170-193, 196-239 — CONFIRMED.**
`ParseObject`/`parseArray`/`parseDictOrStream` recurse with no depth bound. ~2M nested `[` aborts the
process with `fatal error: stack overflow` (uncatchable). DoS on untrusted input. **Direction:**
thread a depth counter and error past a bound (~1000).

**C8 — Odd-length `/Index` panics — xref.go:187-189 — CONFIRMED.**
`ParseXRefStream` reads `indices[i+1]` without checking the array has even length. An odd `/Index`
panics `index out of range [1] with length 1`. Reproduced. **Direction:** validate
`len(indices)%2==0` and error cleanly.

**C9 — Negative/huge `/W` widths panic — xref.go:142-150, 192-199 — CONFIRMED.**
`/W` values are used as slice bounds unvalidated; a negative `w[0]` makes `streamData[offset+w[0]:]`
panic `slice bounds out of range [-1:]`. Reproduced. **Direction:** reject `w[i] < 0` and
bound-check `entrySize` against `len(streamData)`.

**C10 — Overflowing object numbers silently accepted — parser.go:145-146, 333, 342 — CONFIRMED.**
Object/generation/ref numbers parse with `strconv.Atoi` and drop the error.
`99999999999999999999999999 0 R` becomes `IndirectRef{Number: 9223372036854775807}` with no error —
while a bare integer of the same magnitude *does* error via `toInteger`, an internal inconsistency.
**Direction:** propagate the error (reuse `toInteger`) and reject out-of-range object numbers.

**C11 — Encryption unsupported but not detected — document.go:18-149 — CONFIRMED.**
`Read` never inspects the trailer `/Encrypt` key; an encrypted PDF is parsed as if its strings and
streams were plaintext, producing silently wrong objects and output. The only `/Encrypt` handling in
the repo is a PDF/A rule that *rejects* its presence. No doc comment states the limitation.
**Direction:** detect `/Encrypt` in `Read` and return an explicit "encryption unsupported" error;
document it on `Read`/`Document`.

### 3.3 Medium — correctness

**C12 — Indirect/absent `/Length` stream delimited by substring — parser.go:242-309 — CONFIRMED.**
When `/Length` is an indirect ref or absent, the stream body ends at the first literal `endstream`
substring, which can legitimately occur in binary data. Body `ABendstreamCD` with `/Length 2 0 R`
truncates to `AB` and then fails lexing `endstreamCD`. **Direction:** resolve the indirect Length
before reading the body (two-pass over the object table), and require `endstream` to be
EOL/whitespace-delimited rather than a raw substring.

**C13 — Non-finite Real serialized invalidly — serializer.go:77-85 — CONFIRMED.**
`writeReal` emits `Real(NaN)`→`NaN.0`, `Real(+Inf)`→`+Inf.0`, `-Inf`→`-Inf.0`; none are valid PDF
numeric tokens (and the parser rejects a leading `+` on `Inf`). **Direction:** error or coerce
non-finite reals on write.

**C14 — Serialization mutates the caller's objects — serializer.go:212-215, document.go:284 — CONFIRMED.**
`writeStream` does `dict := stream.Dict` (a struct copy that *shares* the backing `Keys`/`Values`
arrays), then `dict.Set("Length", …)`; when `/Length` already exists, `Set` overwrites the shared
array in place, mutating the caller's `Stream`. Reproduced: a stream whose `/Length` was
`IndirectRef{9}` had it silently rewritten to the inline byte count after serialization; a `/Length
999` became `6`. `Document.Write` likewise mutates `d.Trailer` via `trailer.Set("Size", …)`.
Serializing is expected to be read-only. **Direction:** deep-copy `Keys`/`Values` before mutating,
or build a fresh output dict.

**C15 — `Equal` false for value dict/stream — compare.go:65-81 — CONFIRMED.**
The type switch only has `case *Dictionary` / `case *Stream`; two identical *value*
`Dictionary`/`Stream` (both valid `Object`s via value-receiver `pdfObject()`) fall through to
`default: return false`. `Equal(Dictionary{}, Dictionary{})` is `false` while the pointer forms are
`true`. **Direction:** add value-type cases, or normalize to pointers and document it.

**C16 — Swallowed lexer error after integer — parser.go:122-129 — CONFIRMED.**
`parseIntegerOrRef` treats any `peekToken(1)` error as "just return the integer," so a genuine lexer
error on the following token is lost forever (the lexer already advanced). `5 <zz` returns
`Integer(5)` with `err=nil`; the invalid-hex error never surfaces. **Direction:** treat only
`TokenEOF` as a benign look-ahead failure; propagate real errors. (Same pattern duplicated at
parser.go:131-137, 230-238.)

**C17 — `1.2.3` splits into two Reals — lexer.go:408-439 — CONFIRMED.**
`scanNumber` stops at the second `.` and re-lexes the remainder, so `1.2.3` → `Real 1.2` + `Real .3`
with no error, silently changing element counts inside arrays. **Direction:** after a number,
consume any trailing regular char into the token and let `strconv` reject it, or emit a
malformed-number error.

### 3.4 Medium — structure / incoherence

**C18 — Inconsistent cycle guards across graph walkers — pdfa.go:2633/1795 vs 2815/957 — CONFIRMED.**
`resourcesUseTransparency` and `collectAllExtGState` carry `seen map[*Dictionary]bool` guards, but
`collectPagesRecursive` and `collectFontsRecursive` do not (see C3). The same "walk the page tree"
problem is solved two ways in the same file, one safe and one crash-prone. **Direction:** factor a
single cycle-safe page/resource traversal and use it everywhere.

**C19 — Non-conformant free-list & no subsection compaction — document.go:305-347 — CONFIRMED.**
Every gap/free object is written as `0000000000 00000 f` (no next-free linkage, generation not
incremented) and the head `…65535 f` doesn't point at the first free object; `writeXRefTable` always
emits a single `0..maxObj` run, so one sparse high object number balloons the table with physical
free lines. Strict readers relying on free-list traversal see a broken chain. **Direction:** build
proper free-list links or emit multiple subsections for sparse numbering.

### 3.5 Medium — docs

**C20 — `compare.go` docstring lies about indirect refs — compare.go:8-9 — CONFIRMED.**
"compares values deeply, including through indirect references" — but `Equal` has no resolver and
only compares `IndirectRef` numbers structurally. **Direction:** fix the comment or add a
resolver-aware variant.

### 3.6 Low

**C21 — Builder can't set title/author — pdfa_create.go:33 — CONFIRMED.** `NewPDFADocument` calls
`GenerateXMPMetadata(level, "", "")` with hardcoded empty strings; the exported XMP generator
supports title/author but the builder gives no way to pass them, so the `simple_pdfa` example has to
regenerate the metadata stream by hand. **Direction:** add an options struct or title/author params.

**C22 — Cross-type numeric equality — compare.go:23-40 — CONFIRMED.** `Equal(Integer(1),
Real(1.0))` is `true`, collapsing two distinct PDF types, and the Int↔Real path uses exact `==` while
Real↔Real uses an epsilon — asymmetric tolerance. **Direction:** decide the policy explicitly and
route both through `realEqual`.

**C23 — DX hygiene: gofmt/CI/README — CONFIRMED.** `gofmt -l` flags `lexer_test.go`, `pdfa.go`,
`spec_examples_test.go`; there is no `.github/` CI; README is 6 bytes. `go build`/`go vet` are clean.
**Direction:** `gofmt -w`, add a trivial Actions workflow (`build && vet && gofmt -l && go test -skip
TestCorpus`), write a real README.

**C24 — Dead code — xref.go:352, pdfa_create.go:347 — CONFIRMED.** `trimSpace` and `putBEUint32`
have no callers. **Direction:** delete.

**C25 — `#00` in names; no token-size limits — lexer.go:371-406 — CONFIRMED.** `/A#00B` yields
`Name("A\x00B")`; the spec disallows NUL in names, and no name/string length cap exists (memory
amplification). **Direction:** reject `#00`; add a configurable max token length.

**C26 — Duplicate dict keys silently merged — parser.go:226 — CONFIRMED.** `<< /A 1 /A 2 >>` →
`{A:2}` with no diagnostic. **Direction:** document, or offer a strict mode.

**C27 — Short-read masking — lexer.go:89-97 — PLAUSIBLE.** `NewLexerFromReaderAt` ignores the byte
count `n`; a `ReaderAt` shorter than `size` leaves the buffer zero-padded, and NUL is PDF whitespace,
so truncation is silently tolerated. **Direction:** cap `size` to `n` or error.

**C28 — Unbounded keyword search for structure — document.go:176-211, 214-237 — PLAUSIBLE.**
`findStartXref` (`LastIndex("startxref")`) and `findTrailer` (`Index("trailer")`) can match the
keyword inside stream data; `startxref` offsets aren't bounds-checked before seeking. **Direction:**
bounds-check offsets; anchor the trailer search to immediately after the xref entries.

**C29 — Stub ICC profile / example font never validated externally — pdfa_create.go:198-337 — PLAUSIBLE.**
`DefaultSRGBProfile` is a hand-rolled ICC v2.1 stub (single-value gamma curves, D50 white point)
labeled "sRGB IEC61966-2.1", and the `simple_pdfa` example embeds a 1-byte "font program" with no
width metrics. Both pass the repo's own (lenient) validator but have never been run through veraPDF
or a real ICC parser; real validators would likely reject them. **Direction:** validate once against
veraPDF and record it, or embed a known-good compact sRGB profile / real font; soften the "conformant"
claim printed by the example.

**C30 — XMP control-char pass-through — pdfa_create.go:174-193 — PLAUSIBLE.** `xmlEscape` handles
the 5 XML metacharacters (injection is covered) but passes through control bytes 0x00–0x1F that are
illegal in XML 1.0, producing malformed XMP for such titles. **Direction:** strip/replace control
chars.

**C31 — Test artifacts gitignored; round-trip skips on fresh clone — .gitignore — CONFIRMED.**
`testdata/pdf20examples/`, `spec/`, and `testdata/verapdf-corpus/` are all gitignored, so a fresh
clone has no reference PDFs and `TestRoundTripReferencePDFs`/`TestReadSimplePDF` silently `t.Skip` —
the library's core correctness guarantee is untested on a clean checkout. **Direction:** commit a
minimal set of freely-licensed reference PDFs, or document the acquisition step and fail loudly if
absent when explicitly requested.

**C32 — Undocumented spec-extraction pipeline — cmd/extract_spec_examples — CONFIRMED (info).**
`main.py`/`main17.py` read `/tmp/pdf_spec.txt` from an undocumented `pdftotext -layout spec/…` step;
`spec/` is gitignored (copyrighted ISO PDFs), so the committed testdata JSONs can't be regenerated by
a newcomer. The scripts *do* match the committed data. **Direction:** document the pipeline.

### 3.9 PDF/A engine deep findings (A1–A32)

The validator dispatches ~40 `check*` functions unconditionally (each self-gates on level) and finds
objects two ways — reachability walks from the catalog and flat scans over `doc.Objects`. Indirect
refs resolve via single-step `Resolve`/`ResolveDict`; roughly half the call sites add a manual
direct-`*Dictionary` fallback and half don't (A29). The corpus false-negative count (≈817) is
explained by whole unimplemented rule families (A27), compounded by the parser never loading
object-stream contents (A6/C1).

**Requirement-family inventory (implemented / partial / missing), by ISO 19005 clause family:**

| Family | 1b | 2b/3b | 4 | Notes |
|---|---|---|---|---|
| File header (6.1.2) | partial | partial | full | version-only; byte-0 + binary-comment line never checked |
| File trailer (6.1.3) | partial | partial | full | ID/Encrypt/EOF; xref-consistency absent |
| Cross-reference (6.1.4) | none | none | none | needs raw bytes |
| Info wording / string / name / object layout (6.1.5-9) | none | none | none | parser-level rules |
| Filters / inline-image dicts (6.1.10) | partial | partial | partial | LZW + non-standard names only |
| Implementation limits (6.1.12/13) | partial | wrong constants | should not exist | A12/A14 |
| Permissions | n/a | new ✓ | new ✓ | `checkPermsDict` (uncommitted) |
| Content-stream operators (6.2.2/10) | none | none | none | undefined-operator rule absent |
| Output intent (6.2.2/3) | partial | partial | partial | structure + ICC header; internals not parsed |
| Colour spaces (6.2.3/4) | partial | partial | partial | device-CS heuristic; JPX/Lab/CalRGB params absent |
| Form/PS/Ref XObjects (6.2.5/9) | none | none | none | PostScript/`/Ref`/`/Subtype2 /PS` never checked |
| Rendering intents (6.2.6/9) | none | none | none | `ri`/`/RI`/`/Intent` never checked |
| Images (6.2.8/4) | partial | partial | partial | Alternates/Interpolate/OPI only |
| Transparency (6.4 / 6.2.10 / 6.2.9) | broken (A10) | partial | partial | |
| Fonts (6.3 / 6.2.11 / 6.2.10) | partial | partial | partial | embedding for simple fonts only; encoding/CMap/Widths/glyph absent — **largest FN family** |
| Annotations (6.5 / 6.3) | partial | partial | partial | subtype-list bugs (A11), inline-annot bypass (A9) |
| Actions & triggers (6.6 / 6.5) | partial | partial | partial | SetState/NOP FN (A16/A19) |
| Metadata (6.7 / 6.6) | partial | partial | partial | pdfaid + 1b Info only; XMP well-formedness, value types, **extension schemas (6.7.8)** absent |
| Interactive forms (6.9 / 6.4) | partial | partial | partial | |
| Optional content | ✓ (1b prohibition) | none (A21) | partial | |
| Embedded files (6.8 / 6.9) | partial | partial | partial | A26 |
| A-4 alternate presentations / transitions / doc requirements (6.11/6.12) | — | — | none | |

**Critical (crash / silent-pass):**

- **A1 — pdfa.go:957,2815 — no cycle detection in page/font tree walkers → stack overflow.**
  Same as C3; CONFIRMED. Cyclic `/Kids` kills the process.
- **A2 — pdfa.go:3934-3968 — `checkCSForDevice` recurses `Indexed`/`Pattern` base with no guard.**
  A self-referential `[/Indexed 7 0 R …]` (obj 7 = itself) → stack overflow. CONFIRMED.
- **A3 — pdfa.go:4617-4659, 4603 — `checkAlternateCS`/Colorants recursion, no guard.** Two
  Separations whose alternates reference each other → stack overflow. CONFIRMED.
- **A4 — pdfa.go:2359-2368 — `normalizePDFDate` unguarded `rest := s[17:]`.** The `if len(s) >= 17`
  guard only protects `s[15:17]` on the line above; a date of length exactly 15 or 16 with a `+`/`-`
  at index 14 panics `slice bounds out of range [17:15]`. Reproduced end-to-end: an Info `/ModDate`
  of `D:20240101120000+` plus an XMP `xmp:ModifyDate` (so the values are compared) crashes
  `ValidatePDFA`. CONFIRMED (executed). **Direction:** bound-check before every date slice.
- **A6 — document.go:131-134 — object streams never loaded (see C1) → silent pass.** When the
  catalog itself is compressed, `getCatalog` returns nil and most checks return nil → a broken/
  non-PDF/A file validates clean. The single most dangerous silent-pass path. Also no `/Predictor`
  support (PE=9). CONFIRMED.

**High (false negatives / false positives / diff regressions):**

- **A5 — pdfa.go:4299-4334 — any stream with integer `/N` treated as an ICC profile.** A
  `/Type /ObjStm` stream (whose `/N` is its object count, e.g. 10) triggers FP *"ICCBased profile
  /N must be 1, 3, or 4, got 10"*. Real object-stream PDFs load the ObjStm as a normal object and
  hit this. CONFIRMED. **Direction:** find ICC streams by reference from ColorSpace arrays, not by
  the `/N` heuristic; at minimum skip `ObjStm`/`XRef`/`Metadata`.
- **A7 — document.go:352-363 — single-step `Resolve`.** An indirect object whose value is itself an
  `IndirectRef` resolves to the ref, not the target. `/Metadata → 4 0 R → (obj 4 = "5 0 R") →
  stream` yields FP *"/Metadata must be a stream"*; via `ResolveDict` it silently skips (FN). Legal
  PDF. CONFIRMED. **Direction:** loop with a visited-set in `Resolve` (also fixes generation-number
  neglect).
- **A8 — pdfa.go:891-909 — `checkFontsEmbedded` `continue`s on `Type0`**, so the DescendantFonts
  branch below is dead code and every unembedded CID/composite font passes. CONFIRMED (0 errors on a
  Type0-unembedded doc).
- **A9 — pdfa.go:1075,1104,1189,1268,1409,1476,1515 — annotation/widget/action checks iterate only
  top-level `doc.Objects`.** Annotations written as direct dicts in a page's `/Annots` (and direct
  action dicts) bypass every check (a forbidden `/Screen` subtype at 2b passes). CONFIRMED.
  **Direction:** enumerate annotations by reachability from page `/Annots`.
- **A10 — pdfa.go:1691-1783 — 1b transparency check misses XObject `/SMask` and form-XObject
  `/Group`.** An image with `/SMask` validates clean at PDF/A-1b. CONFIRMED (0 errors).
- **A11 — pdfa.go:1033-1066 — `FileAttachment` missing from allowed subtypes for 2b/3b/4.** It's
  *the* PDF/A-3 embedding mechanism, so any A-3 file using it gets FP *"annotation subtype
  //FileAttachment is not allowed"*. CONFIRMED.
- **A12 — pdfa.go:4561-4570 (uncommitted diff) — DeviceN >8-colorant check applied at all levels.**
  The 8-colorant limit is A-1 only; A-2/3 allow 32, A-4 has no limits clause. A 9-colorant DeviceN
  at 2b is falsely flagged. CONFIRMED. **This is a regression in the uncommitted working tree —
  fix before committing.**
- **A13 — pdfa.go:4395-4426,4481-4498 — Separation/DeviceN rules only reach ColorSpace when
  Resources is an indirect object.** The common direct-dict Resources case is never visited, so
  these rules (including A12's) are largely dead. CONFIRMED. **Direction:** walk from pages like the
  device-CS scanner.
- **A15 — pdfa.go:3282-3351 — `checkQNestingDepth` counts `q`/`Q` inside string literals/comments.**
  31 `q`s inside `(…)` → FP *"nesting depth 31 exceeds maximum 28"*. CONFIRMED. Incoherence: a
  correct tokenizer (`scanStreamForDeviceOps`) already exists in the same file. Also FN: only page
  `/Contents` scanned, not form XObjects/patterns/appearances.
- **A16 — pdfa.go:1374-1390 — deprecated actions checked as `set-state`/`no-op`**, but real files
  and the corpus use `/S /SetState` and `/S /NOP`; corpus fail files `6-6-1-t01-fail-k/l.pdf` (A-4)
  return 0 errors. CONFIRMED by running them.
- **A17 — pdfa.go:608-616 + xref.go:281-290 (uncommitted diff) — new "ICC data cannot be decoded"
  FP** fires whenever `decodeStreamData` errors, but that decoder supports only Flate/ASCIIHex. A
  legal profile using ASCII85Decode/RunLengthDecode or a filter array → FP on a compliant file, and
  it's incoherent with `getOutputIntentCoverage`, which on the same failure assumes coverage.
  CONFIRMED (diff regression).
- **A27 — aggregate — entire unimplemented rule families** (fonts encoding/CMap/Widths/glyph, XMP
  well-formedness/value-types/extension-schemas, content-stream operator whitelist, rendering
  intents, PostScript/reference XObjects, JPX, xref/file-structure byte rules, A-4 6.11/6.12, page
  `/PresSteps`). This is the bulk of FN≈817. **Direction:** implement by corpus family, largest
  blocks first (fonts, then metadata/extension-schemas).

**Medium:**

- **A14 — pdfa.go:3121-3146 — implementation-limit constants wrong per level** (A-1-only limits
  applied everywhere; all limits applied at A-4, which has none); `Rule:"6.1.7"` matches no ISO
  clause (A-1 is 6.1.12, A-2 is 6.1.13). PLAUSIBLE FP.
- **A18 — pdfa.go:193-217 — header version rule wrong both ways:** 2b/3b reject legal `%PDF-1.0..1.3`
  (FP); 1b accepts `2.0` (FN). `TestValidatePDFA_Header` codifies the wrong rule. CONFIRMED (trace).
- **A19 — pdfa.go:1396-1436 — action traversal ignores `/Next` chains and `/AA` descent**; page-level
  `/AA` is not forbidden though the spec forbids it. FN.
- **A20 — pdfa.go:2069-2072 — `checkExtGState` returns nil for 1b claiming "handled by
  checkNoTransparency"**, which never checks `/TR`,`/TR2`,`/HTO`/halftones → FN + misleading comment.
- **A21 — pdfa.go:2192,1977,2992 — rules gated to one level that apply to more:** Info↔XMP
  consistency and subset CharSet/CIDSet run only at 1b (spec keeps them at 2b/3b); OC-config rules
  run only at A-4 though 2b/3b have them. FN.
- **A22 — pdfa.go:2288-2297 — `getInfoString` compares raw PDF-string bytes to UTF-8 XMP**;
  UTF-16BE-with-BOM Info values can never match → FP on conformant non-ASCII 1b files.
- **A23 — pdfa.go:1266-1287 — `checkWidgetNoAction` flags widget `/A` at A-4** though A-4 permits it
  (its own `checkWidgetAA` exempts A-4) — internally inconsistent. PLAUSIBLE FP.
- **A24 — pdfa.go:3970-4014,3520,3987 — content/ICC scanning silently skips streams >1 MB, filter
  arrays (even `[/FlateDecode]`), and non-Flate filters** — an oversize or array-wrapped stream
  hides DeviceRGB/q-nesting from the validator → clean report. Boundary/affordance.
- **A25 — pdfa.go:771,848,1868,1830 — stream/annotation/ExtGState checks scan all objects incl.
  unreachable ones** left by incremental updates → FP where veraPDF validates only the reachable
  graph. PLAUSIBLE.
- **A26 — pdfa.go:2840-2967 — embedded-file rules:** A-1 `/EF`-anywhere prohibition only checks the
  Names tree; A-2 flat-forbids `Names/EmbeddedFiles` though 19005-2 permits embedded PDF/A; filespec
  detection requires `/Type /Filespec`; MIME `/Subtype` gated to A-4 only. PLAUSIBLE.
- **A32 — pdfa.go:502-539 — OutputIntent rules:** unconditionally demands a `GTS_PDFA1` intent when
  any intents exist (FP for a device-independent doc with only a PDF/X intent), and the "same ICC
  profile" rule compares only `GTS_PDFA1` entries though the spec covers all intents with
  DestOutputProfile. PLAUSIBLE.

**Low:**

- **A28 — throughout — rule-ID / message drift:** `Rule` strings use 19005-2 numbering even at 1b;
  `fmt.Sprintf("/%s", name)` prints `//Name` because `Name.String()` already prepends `/`
  (confirmed in live output); error order is nondeterministic (map iteration).
- **A29 — pdfa.go:433,4425,542,747 — dead/duplicated code:** `firstPdfaProfile` computed and
  discarded; `_ = dict` no-op; the GTS_PDFA1 profile requirement re-scans the array in a duplicate
  loop; the direct-dict fallback is copy-pasted ~20× and *absent* at half the `ResolveDict` sites →
  inconsistent direct-vs-indirect handling. **Direction:** fold the fallback into `ResolveDict`.
- **A30 — pdfa.go:53-62 — API docs overclaim.** Given A6/A27, "returns nil if conformant" really
  means "none of the implemented checks fired on the objects the parser loaded." Distinguish
  checked-and-passed from not-checkable (unsupported filter, oversize stream, unresolvable ref).
- **A31 — pdfa.go:129,2413,3300 — no caching / no cancellation:** `collectPages` runs in ≥6 checks,
  content streams re-inflate up to 3× per page, `doc.Objects` scanned ~15×; `checkPageSizeLimits`/
  `resolveResources` ignore inherited attributes (MediaBox/Resources) → FN.

**Priority order (engine):** A6 (unblocks everything + silent-pass hazard) → A1–A4 (crash on
malformed/adversarial input) → A12/A17 (uncommitted-diff regressions — fix before committing) →
A8/A9/A13/A16 (cheap high-yield FN fixes) → A11 (FP breaking A-3's core use case) → A27 families by
corpus size.

---

## 4. Design tensions

**T1 — The library advertises "PDF 2.0" but implements only the 1.4-era file structure it was
tested against.** Object streams (C1), xref-stream predictors (C2), and xref-stream writing (C5) —
the three pillars of every modern PDF's physical structure — are unimplemented or broken, yet
`XRefEntry.Compressed`, `ParseXRefStream`, and project memory all advertise support. The seven
bundled reference files happen to use 1.4-style traditional xref, so the test suite never exercises
the gap. *Alternative to weigh:* either scope the library honestly to traditional-xref PDFs (and
error clearly on ObjStm/predictor input) or invest in the full modern read/write path — the current
middle ground silently produces wrong results.

**T2 — Untrusted input is a first-class use case (a PDF/A validator exists precisely to judge files
you didn't create), yet the parser and validator are written as if input were trusted.** Five
distinct crash/hang vectors (C3, C4, C7, C8, C9) turn a malformed or malicious PDF into a process
kill, and several `Atoi`/look-ahead errors are swallowed (C10, C16). *Alternative:* adopt a
"hostile input" posture repo-wide — depth/iteration caps, visited-sets, size limits, and error
propagation as invariants enforced at the boundary, with fuzz tests.

**T3 — `Dictionary` uses shared parallel slices, so an innocent struct copy aliases mutable state.**
This is elegant for order preservation but makes `dict := stream.Dict; dict.Set(...)` a silent
action-at-a-distance bug (C14), and duplicate-key handling (C15, C26) becomes ad hoc. *Alternative:*
make `Set`/`Delete` copy-on-write, or give `Dictionary` a real value-semantics `Clone`, or back it
with an ordered-map type whose copy is deep.

**T4 — `TestCorpus` encodes an aspiration, not a contract, so the test suite is permanently red and
regressions are invisible.** With 804 undetected fail-files, the signal "did I break something" is
drowned by "we haven't finished." *Alternative:* pin a detection-rate baseline (e.g. "≥ N pass,
≤ M regressions") that ratchets upward, so every commit is measured against green.

**T5 — "Passes our validator" is the repo's definition of PDF/A conformance, but the validator's
own corpus score says it catches ~40% of violations.** The builder (`NewPDFADocument`), the example,
and the round-trip test all assert conformance by running the *same* lenient validator that misses
most rules (C29) — a closed loop that can't detect its own blind spots. *Alternative:* gate the
builder's "conformant" claim on an external oracle (veraPDF) in CI, even if only nightly.

---

## 5. Expectation gaps (expected X, found Y)

- **Expected** `Read` on a modern PDF 2.0 to return all objects; **found** it silently drops every
  object stored in an object stream (C1) and mis-decodes predictor-encoded xref streams (C2).
- **Expected** serializing to be read-only; **found** it mutates the caller's stream `/Length` and
  document `/Size` in place (C14).
- **Expected** `Equal(x, x)` to be reflexive for all `Object`s; **found** it returns false for value
  `Dictionary`/`Stream` (C15) and true across `Integer`/`Real` (C22).
- **Expected** (from the docstring) `Equal` to follow indirect references; **found** it does not
  (C20).
- **Expected** `make test` to pass on a fresh clone; **found** it fails immediately (C6), and there
  is no documented state in which it passes.
- **Expected** a validator to survive hostile input; **found** a two-node page-tree cycle kills the
  process (C3).
- **Expected** `NewPDFADocument`'s output to be real PDF/A; **found** it only passes the repo's own
  lenient validator, using a stub ICC profile (C29).
- **Expected** a README to tell a newcomer how to build/test; **found** 6 bytes and no CI (C23).

---

## 6. Open questions (code alone can't resolve)

1. **Scope intent:** is pdf0 meant to be a general modern-PDF library, or a focused PDF/A validator
   over traditional-xref files? The answer decides whether C1/C2/C5 are P0 or "won't fix / error out."
2. **Corpus target:** what detection rate is "good enough" for `TestCorpus` to become a gate, and
   which of the 804 undetected fail-files are in-scope for the b-level conformance this validator
   targets?
3. **Encryption:** hard-error on `/Encrypt` (C11), or eventually decrypt? Affects how much of the
   corpus is even reachable.
4. **ICC/font fidelity:** is `DefaultSRGBProfile` intended as a real profile or a placeholder? A
   veraPDF run on `NewPDFADocument` output would settle C29 immediately.
5. **Mutability contract:** are `Object` values meant to be immutable after construction? That
   decision drives the fix shape for C14/T3.
