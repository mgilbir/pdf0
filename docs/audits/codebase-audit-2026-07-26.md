# pdf0 Codebase Audit — 2026-07-26

Exhaustive adversarial full-read audit of `github.com/mgilbir/pdf0` (PDF 2.0 parser /
serializer / encryptor / signer / PDF/A / PDF/UA / PDF/X / PDF/VT / PDF/R / Factur-X validator).
Method: eleven parallel readers covered every non-test `.go` file in full — (a) object/lexer/parser,
(b) document/xref/objstm/serializer/filters/pages, (c) crypt/CMS/signatures/PAdES/timestamps/revocation,
(d) the 6,570-line `pdfa.go` + builder + Level-A, (e) fonts + font programs, (f) content-operators +
file-structure + functions + text, (g) the PDF/UA family, (h) xmp, (i) image codecs (JBIG2/CCITT/MQ/JPEG/color),
(j) PDF/X / VT / R / Factur-X / Order-X, (k) CLI + docs + examples + DX. A twelfth reader triaged the
prior audit (`codebase-audit-2026-07-07-v2.md`, C1–C37) against HEAD. Critical/High findings and every
finding marked EXECUTED were re-verified independently (compiled repros against the module, run under the
current toolchain). Findings that did not survive an attempt to disprove them were discarded.

Baseline health: `go test ./...` passes (93 s with corpus present); `go build`/`go vet`/`go mod tidy -diff`
are clean. **None of the findings below are caught by the existing tests** — the corpus is an FP=0 oracle over
*conforming* files, so it structurally cannot see adversarial-input guards that were dropped or never added.

---

## 1. Summary table

| ID | Sev | Area | Issue | file:line | Status |
|----|-----|------|-------|-----------|--------|
| C1 | Critical | fonts/DoS | Unbounded `/W` CID range → ~2e9 map inserts → OOM, via public ValidatePDFA | fonts.go:1725 | CONFIRMED |
| C2 | Critical | images/DoS | JBIG2 allocates/loops on declared w×h (per-dim capped, product uncapped) → up to 1 TiB alloc / infinite MQ loop | jbig2.go:41,114,379,691 | CONFIRMED |
| C3 | Critical | pdfx/DoS | Cyclic Type3 `/Resources` → stack overflow (fatal) in device-colour scanner, via public ValidatePDFX/VT | pdfx_color.go:155 | CONFIRMED (EXECUTED) |
| C4 | Critical | sign/security | Trust-chain validity anchored on the signer-asserted `signing-time` attribute | signatures.go:140 | CONFIRMED |
| C5 | High | write | xref-stream field-2 width sized from byte offsets only → type-2 container number truncated → corrupt output | document.go:598,637 | CONFIRMED |
| C6 | High | cli/security | `decrypt`/`extract`/`validate` on wrong/absent password exit 0 over still-encrypted bytes (silent passthrough) | cmd/pdf0/commands.go:74,114,38 | CONFIRMED (EXECUTED) |
| C7 | High | cli/data-loss | `merge`/`encrypt` accept an undecrypted encrypted input → silently corrupt output | cmd/pdf0/commands.go:151,92 | CONFIRMED (EXECUTED) |
| C8 | High | api | No way to observe "was decrypted": `Encrypted` true whether decryption succeeded or failed; `security` unexported; `RemoveEncryption` silently no-ops | document.go:16,47; crypt.go:647 | CONFIRMED |
| C9 | High | dx | README-advertised `go run ./examples/simple_pdfa` fails (exit 1, writes nothing) | examples/simple_pdfa/main.go:55 | CONFIRMED (EXECUTED) |
| C10 | High | sign/security | Forged/self-signed TSA accepted: no `id-kp-timeStamping` EKU or chain check → attacker-controlled DocTimeStamp time and PAdES "sealed" | timestamp.go:161; pades.go:66 | CONFIRMED |
| C11 | Med | sign/security | `Valid` (per its comment "content is intact") ignores incremental-update tampering; caller must AND with `CoversWholeDocument` | signatures.go:56 | CONFIRMED |
| C12 | Med | sign/security | `CoversWholeDocument` = `end>=fileLen` only; never ties the unsigned gap to the `/Contents` window or checks segment count | signatures.go:154 | CONFIRMED |
| C13 | Med | sign/security | Revocation accepts stale CRL/OCSP: no thisUpdate/nextUpdate/producedAt freshness check → replayed pre-revocation "good" | revocation.go:68,137 | CONFIRMED |
| C14 | Med | sign/security | verifyCMS omits RFC 5652 content-type attr check and ESS signing-certificate binding (pdf0 advertises CAdES cert-binding but only checks presence) | signatures.go:200 | CONFIRMED |
| C15 | Med | pages | `AppendPages` drops all existing pages when target `/Kids` is an indirect ref | pages.go:119 | CONFIRMED |
| C16 | Med | pages/DoS | `ExtractPages`/`AppendPages` panic (no recover) on an inline (direct-dict) page in `/Kids` | pages.go:115 | CONFIRMED |
| C17 | Med | pdfa/FN | JPXDecode never rejected at PDF/A-1b despite the comment claiming it is (not a PDF 1.4 filter) | pdfa.go:6499,1149 | CONFIRMED |
| C18 | Med | pdfa/FN | Audit-C12 resolve-before-assert still applied inconsistently → indirect-ref evasion of widget/action/JS rules | pdfa.go:1770,1574,1658,2070 | CONFIRMED |
| C19 | Med | pdfa/contract | `NewPDFADocument(PDFA1a/2a/3a)` builds a document that fails its own validator (6 errors) | pdfa_create.go:89 | CONFIRMED (EXECUTED) |
| C20 | Med | validator/DoS | RoleMap chain-follow is O(N³) (O(N) keys × O(N) hops × O(N) `Dictionary.Get`), no work cap | pdfua.go:381 | CONFIRMED |
| C21 | Med | functions/DoS | PostScript type-4 calculator has no execution-step budget; invoked per-pixel by image extraction | function_ps.go:146 | PLAUSIBLE |
| C22 | Med | compare/DoS | `dictionaryEqualDepth` is O(n²) over attacker-resolved dicts (tint-transform consistency path) | compare.go:156 | PLAUSIBLE |
| C23 | Med | pdfa | Page-level OutputIntent errors reported twice (no dedup) | pdfa.go:661,767 | CONFIRMED |
| C24 | Med | text/FN | `ExtractText` ignores inherited `/Resources` → drops text on legal page trees | text.go:95 | CONFIRMED |
| C25 | Med | docs | `doc.go` "dependency-free"/"Write always emits a traditional xref table"/"Encrypted documents are refused" all false | doc.go:1,28; document.go:434 | CONFIRMED |
| C26 | Med | docs | README/architecture.md substantially stale (understate features; wrong Write & encrypt flow; "~50 checks" now 60) | README.md:10; docs/architecture.md:55 | CONFIRMED |
| C27 | Low-Med | pdfa/crash | Level-A checks bypass `runCheck` panic containment (asymmetry the runCheck comment forbids) | pdfa_levela.go:31 | CONFIRMED |
| C28 | Low-Med | text/FN | `ExtractText` never recurses into form XObjects (`Do`) → silent partial/empty text | text.go:47 | CONFIRMED |
| C29 | Low-Med | pdfua/FN | Heading checks key off raw `/S`, disagreeing with sibling checks that resolve RoleMap → role-mapped headings escape level rules | pdfua.go:155 | PLAUSIBLE |
| C30 | Low | images/FN | `/Decode` and `/ImageMask` ignored on the CCITT/JBIG2 extraction branches (honored on the default branch) | imageextract.go:242 | CONFIRMED |
| C31 | Low | parser | Indirect-object *definitions* accepted as array/dict values → surprising object graph | parser.go:177 | CONFIRMED |
| C32 | Low | compare | Cross-type numeric equality uses an absolute epsilon (1e-10) → false equal at large/tiny magnitudes | compare.go:188 | CONFIRMED |
| C33 | Low | xmp/pdfa/FN+FP | Canonical `xmlns` prefix checks match double quotes only → single-quoted decls evade (FN in xmp) / falsely flag (FP in pdfa) | xmp_schemas.go:927; pdfa.go:2129 | CONFIRMED |
| C34 | Low | design | Three drifted reverse-lookup helpers (resolveObjNum/objNumForDict/dictObjNum) with different miss semantics (0/0/-1) and cache use; quadratic on halftones | pdfa.go:1272; sign.go:252; content_operators.go:292 | CONFIRMED |
| C35 | Low | text | Four divergent inline-image skippers for one grammar; text.go's ignores `/L`/dict → mis-tokenizes binary data | text.go:355 | CONFIRMED |
| C36 | Low | sign/security | SHA-1 accepted as signature digest; valid RSA-PSS force-mapped to PKCS#1v1.5 → false negative | signatures.go:311 | CONFIRMED |
| C37 | Low | crypt | AES-CBC PKCS#7 unpadding trusts the pad-length byte without validating the pad bytes | crypt.go:426 | CONFIRMED |
| C38 | Low | xref | `ParseXRefStream` accepts negative `/Index` start-object, unlike the traditional table | xref.go:169 | CONFIRMED |
| C39 | Low | pdfua | `ValidatePDFUA2` filters reused UA-1 findings by fragile message substring; reuses UA-1 machinery that misfires on real UA-2 namespaced structure | pdfua2.go:30 | PLAUSIBLE |
| C40 | Low | write | Two streams sharing one indirect `/Length` target serialize a nondeterministic wrong length | document.go:492 | CONFIRMED |
| C41 | Low | pdfa | `checkAnnotationAA` hardcodes Rule "6.6.3" at 1b/2b/3b (remap helper exists, unused); other rule-ID drift | pdfa.go:2042 | CONFIRMED |
| C42 | Low | dx | `gofmt -l .` prints 8 files (incl. non-test `signatures.go`) on the current toolchain; README/CONTRIBUTING claim it prints nothing; no CI enforces it | (repo) | CONFIRMED |
| C43 | Low | pdfa/FN | `pdfaid` namespace-identity via equal MD5 Profile-ID trusted; same-profile rule compares only IndirectRef.Number | pdfa.go:6319,735 | PLAUSIBLE |
| C44 | Low | facturx | `ValidateFacturX` runs no invoice-XML rules while sibling `ValidateOrderX` does; message says "not INVOICE" while ORDER is accepted; stale "downstream" doc residue | facturx.go:44; pdfx.go:108 | CONFIRMED |
| C45 | Info | api | Validator family is incoherent: free-function vs method receivers, 6 bespoke violation structs with identical fields, `ValidatePDFA` vs `ValidatePDFABytes` silently different coverage | (package) | CONFIRMED |
| C46 | Info | fonts | cmap format-4 segment starting at code 0 dropped entirely (`c != 0` terminates loop); PFB `int(uint32)` negative on 32-bit | fontprog.go:253,711 | PLAUSIBLE |
| C47 | Info | dx | CLI has zero test files; exit codes conflate violations with errors; several usage/observability gaps | cmd/pdf0/ | CONFIRMED |
| C48 | Info | images | Quadratic symbol-list rebuild in JBIG2 refine/aggregate; TPGDON SLTP context assumes nominal AT | jbig2_symbol.go:194 | PLAUSIBLE |
| C49 | Info | pdfa | Dead code (getInfoString, checkPermsDict fallbacks, `_ = i`, unreachable jpx-1b entry) | pdfa.go:2962,996 | CONFIRMED |

**Trajectory since the last audit.** The prior report (`codebase-audit-2026-07-07-v2.md`, C1–C37) was worked
systematically: **31 of 37 FIXED** with regression tests that cite the audit ID (`read_hardening_test.go`,
`panic_safety_test.go`, `write_conformance_test.go`, `misc_fixes_test.go`); the Isartor miss count went 18 → 1 at
FP=0. Every prior Critical/High is fixed. **5 remain PARTIAL** — prior-C18 (non-LZW filters like RunLengthDecode
on the *first* xref stream still hard-abort `Read`), prior-C32 (single-quoted `xmlns` still evades the
canonical-prefix scan — resurfaces here as **C33**), prior-C36 (examples still `os.Create("output.pdf")` in cwd),
prior-C37 (4f/4e suites deliberately parse-only). **1 OPEN**: prior-C29, no CI (a deliberate removal, PR #15) —
which is precisely why the new C9/C42 (broken example, gofmt drift) went unnoticed. Note also that prior-C12
(resolve-before-assert) was marked fixed after spot-checks of `/Subtype`/`/F`, but the full sweep in this audit
found it **still inconsistent across ~8 other call sites** — carried forward as **C18**.

The new findings below therefore cluster in the areas the last sweep did *not* cover: the image codecs, the
signature/PAdES trust layer, the non-PDF/A validators (pdfx/pdfua/pdfvt), the page-manipulation and text-extraction
APIs, and the CLI/docs surface — plus the two validator-traversal DoS classes rooted in `Dictionary.Get`.

---

## 2. System map

**Public surface.** `Read`/`ReadWithPassword` → typed object model (`Document`); `Document.Write`
(+`WriteIncremental`/`WriteSigned*`/`WriteArchivalTimestamp`); `SetEncryption`/`RemoveEncryption`;
`VerifySignatures[WithRoots]`/`ValidatePAdES`; a fleet of validators — `ValidatePDFA`/`ValidatePDFABytes`,
`ValidatePDFUA`, `(*Document).ValidatePDFUA2`, `ValidatePDFX`, `ValidatePDFVT`/`ValidatePDFVT2`,
`(*Document).ValidatePDFR`, `ValidateDParts`, `ValidateFacturX`, `(*Document).ValidateOrderX`; extractors
`ExtractText`/`ExtractImages`/`ExtractPages`/`AppendPages`; builders `NewPDFADocument*`; `Repair`.

**Read** (`document.go`): slurp → parse header (+`headerOffset`) → `findStartXref` → probe absolute-vs-relative
offset → follow `/Prev` (cycle-guarded, newest wins) → parse each uncompressed object recording `Offsets[num]`
→ materialize object-stream (type-2) entries as gen-0 objects (undecodable containers → `brokenObjStms`,
non-fatal) → `normalizeStructure` drops `/XRef` & `/ObjStm` and strips xref-stream trailer keys → decrypt
strings/streams if a standard handler matched (`security != nil`). Decompression-bomb defenses (per-stream/
aggregate/objstm budgets, `parsedByOffset` dedup, overflow-safe `/N`-`/First` guard, negative-offset guards)
are thorough and well-tested.

**Write** (`document.go`): refuse only if `Encrypted && security==nil`; else serialize each object (re-encrypting
via `security.encryptCopy` when present), regenerating a traditional xref table *or* an xref stream when the
source used one (`usedXRefStream`). Non-mutating and idempotent for well-formed input.

**Validate** (`pdfa.go`): runs ~60 check functions over a per-run shallow *copy* of the Document (so the memoizing
`validationCache` never touches the caller's Document — genuinely race-safe, tested under `-race`). Each check
runs under `runCheck`, which converts a panic into an "internal" finding. Byte-level checks run only when
`rawData != nil` — i.e. `ValidatePDFA` silently skips them, `ValidatePDFABytes` includes them. Output sorted for
determinism. **The Level-A path (`pdfa_levela.go`) and every non-PDF/A validator (pdfua/pdfx/pdfvt/pdfr/facturx)
run outside `runCheck`, so a panic there is fatal to the caller** — C3, C16, C27 are direct consequences.

**Key invariants** (enforced vs assumed): parser bounds depth/token-gap/overflow (enforced); content-decode
work is budgeted at three layers (enforced); *image-codec* and *validator-traversal* work is **not** uniformly
budgeted (assumed — C1/C2/C20/C21/C22); `Dictionary` preserves key order via parallel slices, but `Get`/`Set`
are O(n) linear even though the parser switches to a map above 64 keys (assumed-cheap, is the root of C20/C22/C34);
signature "validity" is byte-range integrity, *not* whole-document coverage or trust (assumed by callers — C11/C12).

---

## 3. Findings by category (severity order)

### 3.1 Denial of service on untrusted input

**C1 — Unbounded `/W` CID range → OOM. CONFIRMED. fonts.go:1725.**
`parseCIDWidths` expands the `c cLast w` range form with `for cid := c; cid <= cLast; cid++ { out[cid] = w }`,
both bounds taken from the PDF `/W` array with no cap. It runs at `checkCIDFontConsistency:1399`, *before* the
`renders` gate (1410), so any Type0 CIDFont merely selected by `Tf` triggers it. Input `/W [0 2000000000 500]`
→ ~2e9 iterations + ~2e9 map entries → CPU + memory exhaustion via public `ValidatePDFA`. This is the one
unguarded count in the file; the sibling ToUnicode (`hi-lo < 65536`) and CIDSet paths are explicitly bounded.
*Direction:* cap the span against the 16-bit CID ceiling, mirroring the ToUnicode guard.

**C2 — JBIG2 decodes on declared dimensions, product uncapped. CONFIRMED. jbig2.go:41,114,379,405,691 (+halftone/symbol).**
`width`/`height` are each capped to `1<<20` but `w*h` is never bounded; `newJBBitmap` does `make([]byte, w*h)`
(up to 2^40 = 1 TiB) and `decodeGenericInto` loops `w*h` MQ decodes. The MQ decoder returns `0xFF` past `end`
and keeps producing bits, so a truncated ~20-byte immediate-generic-region segment declaring `w=h=1<<20` never
terminates early. `readPageInfo` sizes the canvas from the image dict's `/Width`,`/Height` with the same gap.
Parallel unbounded products in `jbig2_halftone.go` (grayscale planes `bpp×gw*gh`, `[]int` of `gw*gh`, collective
bitmap `numPats*pw*ph`) and `jbig2_symbol.go` (per-symbol `symWidth*hcHeight`, `numNew` up to `1<<20`). A realistic
40000×40000 image is a reliable ~1.6 GB OOM / multi-second hang. *Direction:* enforce one shared total-pixel
budget up front across page/region/symbol/pattern/gray paths.

**C3 — Cyclic Type3 resources → stack overflow. CONFIRMED (EXECUTED). pdfx_color.go:155.**
`devColorScanner.container()` recurses into Type3-font `/Resources` (`s.container(fd, nil, nil)`) with a
recursion guard keyed only on `*Stream` (`inProg`); the Type3 branch passes a `*Dictionary`, unguarded. A page
whose Type3 font's `/Resources /Font` references itself makes `ValidatePDFX`/`ValidatePDFVT`/`ValidatePDFVT2`
`fatal error: stack overflow` (reproduced). The PDF/A twin `scanContainerForDeviceCS`/`scanResourcesForDeviceCS`
threads a `seen map[*Dictionary]bool` that prevents exactly this — the fast-path duplicate dropped it. The
equivalence test `TestDevColorScannerMatchesPDFA` can't catch it (conforming corpus has no cycle). *Direction:*
thread a `*Dictionary` cycle guard through `container()`; see design tension §4.1.

**C20 — RoleMap chain-follow is O(N³). CONFIRMED. pdfua.go:381.**
`checkUARoleMapIntegrity` loops all N keys, follows up to N hops per key, each hop calling
`d.Resolve(roleMap.Get(cur))` where `Dictionary.Get` is an O(N) linear scan → O(N³). A ~100 KB `/RoleMap`
forming one chain (N≈10⁴) → ~10¹² ops, minutes-to-hours hang. The `seen` map guards infinite looping, not
compute. *Direction:* build a `map[Name]Name` once and follow with O(1) lookups; or cap N.

**C21 — PostScript calculator has no step budget. PLAUSIBLE. function_ps.go:146.**
`psExec`/`psApply` cap recursion depth (32) and stack (4096) but never count total operations. A type-4 tint
function is invoked per-pixel by image extraction (`imagecolor.go:455,469 → toRGB → evalType4`). A balanced
`if`/`ifelse` fan-out approaches ~2^depth executions under the depth cap while keeping the stack small; even
without exponential blowup, per-pixel O(program²) over a large image is unbounded. No test exercises a step
budget. *Direction:* add a global step counter that aborts past a fixed budget.

**C22 — O(n²) dictionary comparison on attacker input. PLAUSIBLE. compare.go:156.**
`dictionaryEqualDepth` does multiset matching with a `used[]` array and a nested key scan — O(n²). A Separation
colorspace whose tint-transform is a function stream with a 200k-key dictionary, referenced by two same-named
colorants with different object numbers, reaches `Equal(...)` at pdfa.go's tint-consistency check → ~4e10 ops.
Parsing that dict is only O(n) (the 64-key index kicks in), so the comparison is the bottleneck. *Direction:*
build a name→[]index map for one side before matching, or cap the key count.

**C16 — Panic on inline page dict. CONFIRMED. pages.go:115.** (see §3.3.)

*(XMP unbounded mutual recursion — xmp.go:248/173 — is defended today only by the 2 MiB `xmpPropertyMaxBytes`
cap, a coupling the cap's own comment never mentions; a test already raises the cap to 1 GiB. Latent; noted under
design tension §4.2.)*

### 3.2 Signature / cryptographic trust

**C4 — Chain validity anchored on self-asserted `signing-time`. CONFIRMED. signatures.go:140.**
`verifyChain` sets `opts.CurrentTime = signingTime`, where `signingTime` is the CMS `signing-time` *signed
attribute* — signed only by the signer (the adversary), never by a trusted timestamp. An expired-or-revoked
holder signs today and backdates `signing-time` into the cert's old validity window; `cert.Verify` then builds
the chain as of the fake time and reports `TrustedChain=true`. *Direction:* anchor on a verified B-T timestamp
time (pades.go already computes `TimestampTime`) or real wall-clock; treat `signing-time` as untrusted.

**C10 — Forged TSA accepted. CONFIRMED. timestamp.go:161; pades.go:66.**
`verifyTimestampToken` checks the CMS signature and imprint but requires neither the critical
`id-kp-timeStamping` EKU nor any chain, so any self-signed cert is accepted as a TSA. An attacker appends
malicious content via incremental update, then a whole-file DocTimeStamp signed by their throwaway cert;
`coveringDocTimestamp` returns true, `assessPAdES` drops the coverage issue and can report `Conformant=true`
(`sealed`) for a tampered document — asserting any time. *Direction:* require the timeStamping EKU and chain the
TSA to trusted roots before honoring `sealed`/`TimestampTime`.

**C11 — `Valid` ≠ document unmodified. CONFIRMED. signatures.go:56.**
A signed PDF gets an incremental update overriding `/Root`/pages. The original signed byte range is intact, so
`verifyCMS` returns `Valid=true`; the field's doc comment ("the document content is intact") invites a caller to
accept a document whose rendered content changed. `CoversWholeDocument` does go false, but nothing forces the
AND. *Direction:* document that `Valid` must be combined with `CoversWholeDocument`, or expose a single verdict.

**C12 — Coverage check is incomplete. CONFIRMED. signatures.go:154.**
`byteRangeSegments` computes `covers` purely as `maxEnd >= fileLen`; it never verifies there are exactly two
segments nor that the single gap coincides with the `/Contents` value bytes. In pdf0's own model the ByteRange
sits inside the signed region, which limits exploitability, but the standard defense is absent. *Direction:*
locate the `/Contents` bytes and assert the gap equals them.

**C13 — Stale revocation accepted. CONFIRMED. revocation.go:68,137.**
Neither `revocationFromCRL` nor `revocationFromOCSP` checks `thisUpdate`/`nextUpdate`/`producedAt`. A "good"
OCSP response captured before revocation (or a superseded CRL) replayed in the DSS returns `RevocationGood`,
masking a later revocation. *Direction:* reject responses whose `nextUpdate` is past (or older than the
signing/timestamp time).

**C14 — CAdES binding advertised, not enforced. CONFIRMED. signatures.go:200.**
`verifyCMS` never compares the signed content-type attribute to the eContentType, nor validates the ESS
signing-certificate(-v2) attribute against the SID-selected cert; `cmsPAdESFacts` only checks *presence*. A CMS
with a mismatched/absent content-type or a signing-certificate attribute whose hash doesn't match the cert still
verifies. Bounded (the signer cert must still verify the signature) but the binding pdf0 claims to check is not
checked. *Direction:* assert content-type == id-data and ESSCertIDv2.CertHash == SHA-256(cert.Raw).

**C36 — SHA-1 accepted; RSA-PSS rejected. CONFIRMED. signatures.go:311.**
A `SHA1WithRSA` signature verifies (collision-weak); a valid RSA-PSS signature is force-mapped to PKCS#1v1.5 and
reported as not verifying (false negative). *Direction:* flag/deprecate SHA-1; honor `SignatureAlgo` for PSS/Ed25519.

**C37 — AES-CBC pad bytes unverified. CONFIRMED. crypt.go:426.**
`aesCBCDecrypt` trusts `out[n-1]` as the pad length without checking the preceding bytes. No padding oracle is
exposed, so impact is data-corruption robustness, not secrecy. *Direction:* validate all pad bytes; error on mismatch.

### 3.3 Correctness — write, pages, extraction

**C5 — xref-stream field-2 truncation → corrupt file. CONFIRMED. document.go:598,637.**
`maxField2` is computed only from the byte-`offsets` map, but type-2 entries write the *container object number*
`e[0]` into that field (`put(uint64(e[0]), w[1])`). The field-3 sizing loop was deliberately widened to cover
type-2 indices/generations (comment at 604-608); the parallel field-2 case was missed. A sparse-numbered
xref-stream input (e.g. objects 1 and 100000 in a small file) packs the write set into an object stream numbered
100001, written in a 1-byte field → truncated to 161; re-reading fails "object stream 161 … not present."
*Direction:* fold every `type2[num][0]` into `maxField2` before `byteWidth`.

**C15 — AppendPages drops pages on indirect `/Kids`. CONFIRMED. pages.go:119.**
`kids, _ := pages.Get("Kids").(Array)` fails when `/Kids` is an indirect ref (legal); `kids==nil`, so the code
overwrites `/Kids` with a fresh array holding only the new page and sets `/Count` to 1 — silently discarding all
existing pages. The read side (`collectPagesRecursive`) resolves `/Kids`; the write side doesn't. *Direction:*
resolve `/Kids` and mutate the underlying array.

**C16 — Panic on inline page dict. CONFIRMED. pages.go:115.**
A parsed doc with `/Kids [<< /Type /Page >>]` (direct dict, no object number — the parser accepts it, and
`PageCount()` counts it) makes `pageRefsOf` yield `IndirectRef{Number:0}`; `copyRef(0)` installs a `Null{}`
placeholder; `appendPageInto` then does `...Value.(*Dictionary)` → panic. `ExtractPages`/`AppendPages` have no
`recover` (unlike `Read`), so malformed input crashes the caller. *Direction:* skip/error on `objNum==0` page refs.

**C24 — ExtractText ignores inherited `/Resources`. CONFIRMED. text.go:95.**
`pageFontMaps` reads `page.Get("Resources")` directly; when Resources are inherited from the `/Pages` parent
(legal, common, and what the rest of the codebase honors via `inheritedPageAttr`), no ToUnicode maps load and
text is dropped or degraded to Latin-1 — contradicting the "visible text of every page" doc comment.

**C28 — ExtractText drops form-XObject text. CONFIRMED. text.go:47.**
The operator loop handles text operators but not `Do`, so text drawn inside a form XObject invoked with `Do`
(very common) is silently omitted, with no signal. *Direction:* recurse into invoked forms (mirror walkExecutedContent).

**C30 — `/Decode`/`/ImageMask` ignored on codec branches. CONFIRMED. imageextract.go:242.**
The CCITT and JBIG2 extraction branches hardcode `samplesToImage(..., 1, "DeviceGray")` with fixed 0=black,
ignoring `/Decode [1 0]` (wrong polarity) and routing an `/ImageMask true` CCITT/JBIG2 image through an opaque
raster instead of `imageMaskToImage`. The default (Flate/raw) branch handles both. The codec branches drifted.

**C40 — Shared `/Length` target nondeterministic. CONFIRMED. document.go:492.**
Two streams pointing `/Length` at one integer object with different `len(Data)` write `lengthOverrides` in
map-iteration order → the shared object gets one stream's length (varies run-to-run), wrong for the other.
Malformed input, but silently incoherent rather than rejected.

### 3.4 Validator soundness (false negatives / positives / rule IDs)

**C17 — JPXDecode passes at 1b. CONFIRMED. pdfa.go:6499,1149.** `checkJPXImages` returns nil at 1b with a
comment "JPXDecode is forbidden outright at PDF/A-1 (6.1.10)", but the filter whitelist (`isStandardFilter`)
accepts JPXDecode (and Crypt) at every level, and neither exists in PDF 1.4 — so a 1b file with `/Filter
/JPXDecode` validates clean. *Direction:* reject JPXDecode at 1b in the filter check.

**C18 — Resolve-before-assert still inconsistent. CONFIRMED. pdfa.go:1574,1658,1770,2070 (et al).** `resolveName`
exists and is used in some checks, but `checkWidgetNoAction` (`/Subtype 9 0 R`→Widget with `/A` passes),
`checkActionChain`/`checkNoForbiddenActions` (`/S 9 0 R`→JavaScript passes), `isWidgetOrField`, `annotFieldType`,
`checkExtGState /TR2`/`/BM`, `isAnnotation`, `checkMetadataStream` type/subtype all direct-assert — indirect refs
evade them. (Prior-audit C12 partially open.) *Direction:* route every rule-input type-assertion through Resolve.

**C19 — Level-A builder produces a non-conformant doc. CONFIRMED (EXECUTED). pdfa_create.go:89.**
`pdfaVersion`/`pdfaPart`/`pdfaConformance` have no Level-A cases and fall to defaults (version 2.0, part 4, no
conformance). `ValidatePDFA(NewPDFADocument(PDFA1a), PDFA1a)` returns 6 errors (wrong part, ICC v4 at v2-max, no
conformance A, no MarkInfo, no StructTreeRoot), violating the doc-comment contract "passes ValidatePDFA".
*Direction:* either implement Level-A construction (Tagged PDF + structure tree) or reject Level-A in the builder.

**C23 — Duplicate page-level OI errors. CONFIRMED. pdfa.go:661,767.** An A-4 doc with catalog `/OutputIntents`
plus a page-level intent whose `/S != GTS_PDFA1` emits each page-level error twice (`errs := errsPageLevel` at
661 and `append(errs, errsPageLevel...)` at 767); the final sort doesn't dedup.

**C27 — Level-A bypasses panic containment. CONFIRMED. pdfa_levela.go:31.** Every Level-B check runs under
`runCheck` (panic → "internal" finding); `checkLevelAConformance`/`Structure`/`Language` are called bare, so a
panic there (or in `decodeXMPToUTF8`/`ResolveDict` on hostile input) crashes the caller — the exact asymmetry the
`runCheck` comment says must not exist. Currently low-exposure (the checks are simple). Same class as C3/C16.

**C29 — Heading checks disagree on RoleMap. PLAUSIBLE. pdfua.go:155.** `checkUAHeadings` keys off raw `/S`, so a
document role-mapping `Titre1→H1`, `Titre3→H3` never trips the level-skip / start-at-H1 rules, while
`checkUAStrongWeak` and `checkUAOneHPerNode` operate on the *resolved* type. Three heading rules, two RoleMap
policies. *Direction:* use the resolved `stdType` in `checkUAHeadings`.

**C33 — `xmlns` prefix checks are double-quote-only. CONFIRMED. xmp_schemas.go:927; pdfa.go:2129.**
`checkXMPExtensionContainer` enforces canonical prefixes by scanning for `="`+uri+`"` (double quotes); a
single-quoted `xmlns:WRONG='…/pdfa/ns/schema#'` evades the rule (FN). The mirror bug in `checkMetadataVersion`
hardcodes `xmlns:pdfaid="…"` and *falsely flags* a legal single-quoted declaration (FP). pdf0's attribute
helpers are already single-quote-aware; the raw-text checks drifted. *Direction:* match both quote styles.

**C39 — UA-2 wrapper fragile and over-promising. PLAUSIBLE. pdfua2.go:30.** `ValidatePDFUA2` suppresses reused
UA-1 findings via `strings.Contains(e.Message, "pdfuaid:part must be 1")` — a reword silently reintroduces
spurious violations. It also reuses UA-1 structure-type machinery that resolves against ISO 32000-1 types, so a
genuine UA-2 namespaced/RoleMapNS file would be flagged "neither standard nor mapped." No UA-2 corpus exercises
it. *Direction:* filter on a typed marker; gate structure-type checks off for UA-2 or document the limitation.

**C41 — Wrong rule IDs. CONFIRMED. pdfa.go:2042.** `checkAnnotationAA` hardcodes Rule "6.6.3" at 1b/2b/3b though
`annotActionClause("catalogAA")` exists precisely to remap it; the coverage ratchet only checks that clause
strings appear *somewhere*, so it can't catch a mis-attributed report.

**C43 — Weak profile-identity checks. PLAUSIBLE. pdfa.go:6319,735.** `sameICCProfile` trusts equal non-zero MD5
Profile-IDs (forgeable); the A-4 same-profile rule compares only `IndirectRef.Number`, ignoring generation and
direct-stream entries. Corpus pins FP=0 on real files only.

### 3.5 Parser / model / compare

**C31 — Nested indirect-object definitions accepted. CONFIRMED. parser.go:177.** `<< /K 1 0 obj (x) endobj >>`
yields a dict whose `/K` value is an `*IndirectObject` — a shape Resolve/serializer/validators don't expect for a
nested value. Tolerant but produces a surprising graph. *Direction:* treat "N G obj" as a definition only at the
top-level entry.

**C32 — Absolute epsilon in numeric equality. CONFIRMED. compare.go:188.** `realEqual` uses `|a-b| < 1e-10`, so
`Equal(Integer(0), Real(1e-11))` is true and two float64s differing by 1 ULP at 9e15 compare equal — masking
real differences in `DocumentEqual`/round-trip/tint-consistency. *Direction:* relative tolerance, or exact-representability
check for Integer↔Real.

**C38 — Negative `/Index` accepted. CONFIRMED. xref.go:169.** `ParseXRefStream` doesn't reject a negative
start-object in `/Index` (traditional `ParseXRefTable` rejects `startObj < 0` at xref.go:66); a negative object
number flows downstream and degrades to a bogus-offset parse error rather than a clean rejection. *Direction:*
reject `startObj < 0` / `count < 0` for parity.

### 3.6 Documentation & developer experience

**C6/C7/C8/C9** — see summary table; the CLI/DX findings are covered under §3.3/§3.2 root causes. All EXECUTED.

**C25 — `doc.go` triple-wrong. CONFIRMED. doc.go:1,28; document.go:434.** "dependency-free" (go.mod requires
formalis/gopenjpeg/golittlecms), "Write always emits a traditional cross-reference table" (regenerates an xref
stream when the source used one), and `Write`'s own godoc "Encrypted documents are refused … (decryption is not
implemented)" (contradicted 8-11 lines below by the re-encrypt/passthrough paths).

**C26 — README/architecture.md stale. CONFIRMED.** README's feature list and Layout table omit ~30 of 60 non-test
files (image extraction/decoders, Factur-X/Order-X, PDF/X, PDF/VT, PDF/R, PDF/UA-2, PAdES/timestamps/revocation/
DSS, incremental writes, DPart, functions) and don't expose the a/e/f PDF/A levels the validator supports;
`docs/architecture.md` says Write "rewrites cross-reference-stream inputs as a traditional table" (false), its
flowchart shows "Encrypted → return error" (false), and "~50 check functions" is now 60.

**C42 — gofmt invariant fails; no CI. CONFIRMED.** `gofmt -l .` prints 8 files (incl. non-test `signatures.go`)
on the current toolchain, contradicting README.md:86 / CONTRIBUTING.md:9. CI was removed (PR #15), so the
gofmt/example/format invariants documented in the README are enforced only by humans running them — which is why
C9 (broken example) and this drifted unnoticed.

**C44 — Factur-X/Order-X asymmetry & residue. CONFIRMED. facturx.go:44; pdfx.go:108.** `ValidateOrderX` runs
order-XML business rules inline (`formalis.ValidateOrderXML`) but `ValidateFacturX` runs none — same verb, two
meanings. The type-check message says "%q is not INVOICE" while `DocumentType ORDER` is silently accepted. Doc
comments still promise "downstream EN 16931 validation" that pdf0 no longer wires for invoices (engine moved to
`formalis`). *Direction:* pick one contract; fix the message; drop the stale residue.

**C47 — CLI/test/DX gaps. CONFIRMED.** `cmd/pdf0` has zero test files; `repair` exits 0 with no summary when it
fixes nothing and violations remain; `info` counts pages by scanning `/Type /Page` instead of `PageCount()`
(orphans inflate); usage omits `-password`; exit codes conflate usage (2) with violations+I/O errors (1);
`corpustime` exits 0 silently with no args.

*(Dead code C49 and low-value font/image nits C46/C48 are itemized in the summary table.)*

---

## 4. Design tensions

### 4.1 Hand-maintained "fast-path" duplicates that silently drop guards
The device-colour scanner exists twice — the trusted `scanContainerForDeviceCS` (pdfa.go) with a
`seen map[*Dictionary]bool` cycle guard, and the `devColorScanner` (pdfx_color.go) that dropped the dict guard
(C3, an executed stack overflow). The reverse-dict-lookup helper exists three times (`resolveObjNum` /
`objNumForDict` / `dictObjNum`) with three miss semantics (0/0/-1) and inconsistent cache use (C34, quadratic on
halftones). The inline-image skipper exists four times (text.go / pdfa.go / filestructure.go / fonts.go) with
different `/L`-awareness (C35). The equivalence test between the two colour scanners compares *outputs on
conforming corpus files* and so structurally cannot detect a dropped adversarial-input guard. **Alternative to
weigh:** collapse each concept to one implementation (one traversal with a pluggable visitor); if a fast path is
truly needed for scale, make the shared one carry the guard and have the fast path *be* the shared one with a
cheaper accumulator, not a re-implementation. Every duplicate is a place a guard can rot unobserved.

### 4.2 `Dictionary.Get`/`Set` are O(n) while the parser thinks they're cheap
`object.go:53,64` scan the parallel key slice linearly, but the parser builds a lookup map above 64 keys and the
validators call `Get` inside per-key/per-node loops over attacker-controlled dictionaries. That mismatch is the
*root cause* of C20 (O(N³) RoleMap) and C22 (O(n²) tint compare), and a latent hazard anywhere a large
`/Names` tree, resource dict, or XObject map is walked. **Alternative:** give `Dictionary` an optional lazily-built
index (the parser already has the threshold logic) so `Get` is amortized O(1) on large dicts, or expose a
`GetIndexed`/map view for hot loops. The recursion cases (XMP C-note, colour spaces) want the same treatment: a
single explicit depth budget rather than accidental protection from a byte cap.

### 4.3 Image codecs budget *input size* but not *output work*
The content-decode layer is budgeted at three levels (per-stream 64 MB, per-run 512 MB, 2 MB ICC) — genuinely
good. The image codecs are not: JBIG2/halftone/symbol size every allocation and loop from *declared dimensions*
(C2), and the PostScript calculator counts depth but not steps (C21), amplified per-pixel. The per-dimension
`1<<20`/`1<<16` caps read as protection but don't bound the product. **Alternative:** a single "total pixels ×
bytes-per-pixel" and "total decode operations" budget threaded through the codec entry points, consistent with
the content-decode budget, negative-cached the same way.

### 4.4 Signature verification's affordances promise trust it doesn't establish
The names invite a one-field trust decision — `Valid`, `TrustedChain`, `sealed`, `Conformant` — but each rests on
an *unauthenticated* input: `Valid` is byte-range integrity not document integrity (C11); `TrustedChain` is
anchored on the signer's own `signing-time` (C4); `sealed`/`TimestampTime` trust an unverified TSA (C10);
`CoversWholeDocument` doesn't tie the gap to `/Contents` (C12); revocation ignores freshness (C13). The easy path
(read `res.Valid`) is the dangerous one. **Alternative:** collapse to a single conservative verdict that is true
only when signature integrity **and** whole-document coverage **and** a trusted, EKU-checked timestamp (or
real-clock chain) **and** fresh revocation all hold; expose the sub-facts for callers who want to override, but
make the safe reading the default one.

### 4.5 The validator family has no common shape
Same concept ("validate a document against a profile"), five shapes: free functions
(`ValidatePDFX/VT/A/UA/DParts/FacturX`) vs methods (`ValidatePDFR/OrderX/PDFUA2` — and `ValidatePDFUA` is a
function while `ValidatePDFUA2` is a method); six bespoke violation structs with identical `Rule/Message/Object`
fields that don't interoperate; and `ValidatePDFA` silently covering fewer rules than `ValidatePDFABytes` (C45).
A newcomer can't predict the call shape or combine results. **Alternative:** one receiver convention, one
`Violation` type (or interface), and fold the bytes-vs-no-bytes split into one entry point that takes the raw
bytes optionally rather than two functions with divergent coverage.

---

## 5. Expectation gaps (expected X, found Y)

- **Decrypt affordance.** Expected: `decrypt wrong.pdf` errors or the library exposes "did it decrypt?" Found:
  `Encrypted` is true whether decryption succeeded or failed, `security` is unexported, and `decrypt`/`merge`/
  `encrypt` exit 0 producing still-encrypted or corrupt output (C6/C7/C8). The one state a caller most needs is
  unobservable.
- **"dependency-free".** Expected: `go get` pulls one module. Found: three (C25).
- **`Write` godoc.** Expected: the most-read method's comment matches its body. Found: it says encrypted docs are
  refused and a traditional xref is always emitted; both are false (C25).
- **`go run ./examples/simple_pdfa`.** Expected: the README's headline example runs and writes a PDF. Found:
  exit 1, nothing written — its placeholder font no longer parses (C9).
- **`NewPDFADocument(PDFA1a)`.** Expected (per its doc comment): a document that passes `ValidatePDFA`. Found: 6
  self-inflicted validation errors (C19).
- **`ExtractText`.** Expected: "visible text of every page." Found: text via inherited resources or form XObjects
  silently dropped (C24/C28).
- **`gofmt -l .`.** Expected (per docs): prints nothing. Found: 8 files (C42).
- **Signature `Valid`.** Expected (per its comment): "the document content is intact." Found: only the signed
  byte-range is intact; post-signing incremental tampering leaves `Valid=true` (C11).

---

## 6. Open questions (code alone can't resolve)

1. **Intended threat model for the validators.** Are `ValidatePDFA`/`PDFUA`/`PDFX` expected to be safe on fully
   hostile input (then C1/C2/C3/C20/C21/C22 are release-blockers), or only on plausibly-real files (then they're
   hardening debt)? The DoS-sweep history suggests the former, but the image/font/validator-traversal layers
   weren't swept.
2. **Is signature *verification* meant to make a trust decision or just report facts?** C4/C10–C14 are bugs
   under the first reading and documentation gaps under the second. The field names imply the first; the
   implementation behaves like the second.
3. **Level-A scope.** Is `NewPDFADocument(PDFA1a/2a/3a)` supposed to build Tagged PDF, or should Level-A be
   rejected at construction (C19)? The enum admits the levels; the builder ignores them.
4. **PDF/UA-2 scope.** Is `ValidatePDFUA2` intended as real UA-2 validation or UA-1-rules-plus-id/version? No
   UA-2 corpus is bundled, so the answer isn't in the tests (C39).
5. **gofmt/toolchain.** Is the 8-file gofmt drift a toolchain-version mismatch (go.mod says 1.25; the installed
   compiler differs) or genuine formatting debt? A pinned Go version + CI would settle it (C42).

---

*No repository files were modified by this audit. Repros were built in a scratch module with a `replace`
directive against the working tree.*
