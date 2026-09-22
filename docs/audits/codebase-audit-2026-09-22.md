# pdf0 Codebase Audit — 2026-09-22

This is an exhaustive, adversarial audit of `github.com/mgilbir/pdf0` at `d61adc4` (main, clean tree). pdf0 is a PDF 2.0 reader, writer, encryptor and signer, with validators for PDF/A, PDF/UA, PDF/X, PDF/VT, PDF/R, DPart and Factur-X/Order-X, and HTML→PDF output via forme.

**Method.** Eleven parallel readers each read one slice of the ~45,600 non-test lines **in full**, plus the matching tests and docs:

1. `syntax` + `object`
2. document read/write/xref/objstm/incremental/filters/limits/cancel
3. the page-tree, structure, outline, annotation, content-builder and text APIs
4. `internal/crypt` + `sign` + signing entry points
5. `pdfa/pdfa.go` and the level/variant model
6. the rest of `pdfa` (fonts, file structure, XMP, builder, Level A)
7. `internal/core`
8. PDF/UA, PDF/X, PDF/VT, PDF/R, DPart and Factur-X
9. images, JBIG2 and CCITT
10. `fonts`, `htmlpdf`, the CLI, dev tools and examples
11. docs, DX, the test suite and CI

**Verification.** Readers were told to disprove each finding before reporting it. Every finding below marked **CONFIRMED (executed)** at Critical or High severity was then **re-run independently by the lead auditor**:
- the repro programs were rebuilt against the working tree;
- anything memory- or time-hungry ran in a `systemd-run` transient unit capped at `MemoryMax=2G`;
- the outcomes (panics, `oom-kill`, timings, finding lists) were checked against each reader's claim.

The repro programs live in the session scratchpad and are not committed.

**Where the lead's re-run differed from a reader's claim, the lead's number is used.** The Type3-DAG scan measured 1.35 s at depth 22 (reader: 3.0 s) and 22.2 s at depth 26 against a 3 s deadline; the exponential shape is confirmed.

**Baseline health.**
- `go build ./...`, `go vet ./...` and `gofmt -l` are clean (Go 1.26.5).
- A fresh clone passes `go test ./...` with 51 skips.
- With corpora present: all packages pass, and the veraPDF corpus stands at `FP=0 missed=0 parseErrors=0`.

**Nearly none of the findings below is caught by the test suite.** The corpus oracles are FP=0 checks over *conforming and single-defect* files. By construction they cannot see:
- adversarial-input guards that are missing;
- read-modify-write paths;
- concurrency;
- the builder APIs on inputs other than pdf0's own flat output.

**Severity counts.**

| Severity | Count |
|---|---|
| Critical | 20 |
| High | 35 |
| Medium | 59 |
| Low | 55 |
| **Total** | **169** |

**Definitions.**
- **Critical:** a security break; a crash, fatal stack overflow or OOM reachable from a public entry point on untrusted input; or silent data loss on a common path.
- **High:** wrong verdicts or wrong output on realistic inputs, super-linear DoS in the seconds-to-minutes range, and APIs whose easy path corrupts data.

The prior audit (`codebase-audit-2026-07-26.md`, C1–C49) was reported as fully worked. **Nine of its items are reopened here as partial fixes or siblings the fix missed.** See §7.

---

## 1. Summary table

| ID | Sev | Area | Issue | file:line | Status |
|----|-----|------|-------|-----------|--------|
| C1 | Critical | sign/security | Tampered, signed doc reports PAdES `Conformant=true` when a self-signed "TSA" DocTimeStamp covers the tampering | sign/pades.go:74,97-128,160; sign/timestamp.go:161-207 | CONFIRMED (executed) |
| C2 | Critical | sign/security | Revocation issuer matched by DN only → forged DSS issuer + OCSP "good" masks a real revoking CRL | sign/revocation.go:301-308; sign/signatures.go:390-395 | CONFIRMED (executed) |
| C3 | Critical | write/data-loss | Incremental writers allocate object numbers still used by the file's ObjStm/XRef objects → update lost, file unreadable, signature vanishes | document.go:465-484,1116-1136; sign.go:225-232; doctimestamp.go:56-64; incremental.go:95-103 | CONFIRMED (executed) |
| C4 | Critical | read/data-loss | Hybrid-reference `/XRefStm` never parsed → compressed objects silently absent; Write drops them with nil error | document.go:199-266,480 | CONFIRMED (executed; 408/408 objects lost on a corpus file) |
| C5 | Critical | sign/crash | `/ByteRange` `s[0]+s[1]` overflow → unrecovered panic in VerifySignatures / ValidatePAdES | sign/signatures.go:370-374; sign/pades.go:114-118 | CONFIRMED (executed) |
| C6 | Critical | sign/DoS | `/ByteRange` segment count/overlap unbounded → 1.5 MB file OOMs VerifySignatures | sign/signatures.go:368-375; sign/pades.go:111-119 | CONFIRMED (executed) |
| C7 | Critical | read/DoS | xref-stream entry count unbounded → 19.6 KB file OOMs Read | xref.go:228-272; document.go:193-250 | CONFIRMED (executed) |
| C8 | Critical | read/DoS | PNG predictor allocates file-chosen 2 GiB row buffer on empty data → 1.6 KB file OOMs Read | internal/core/filters.go:209,269-280 | CONFIRMED (executed) |
| C9 | Critical | read/DoS | ObjStm budget meters decoded bytes, not materialised objects → 403 KB file OOMs Read at default limits | objstm.go:201-256; internal/core/core.go:29 | CONFIRMED (executed) |
| C10 | Critical | pdfa/DoS | CIDFont `/W` has per-range cap but no aggregate cap → 38 KB of `/W` OOMs ValidatePDFA (prior C1 partial) | pdfa/fonts.go:868,1156-1217 | CONFIRMED (executed) |
| C11 | Critical | images/crash | Image `/Width·/Height` overflow bypasses `sampleDataFits` → unrecovered panic/OOM in Images/ExtractImages | images/imagecolor.go:73,111,303-306,662; images/imageextract.go:546,627 | CONFIRMED (executed) |
| C12 | Critical | images/DoS | CCITTFaxDecode caps rows but not stride×rows → ~200-byte stream OOMs extraction | internal/ccitt/ccitt.go:54-57,105 | CONFIRMED (executed) |
| C13 | Critical | images/crash | Self-referencing image `/ColorSpace` → fatal stack overflow | images/imagecolor.go:310-339,383,394,450 | CONFIRMED (executed) |
| C14 | Critical | core/crash | PostScript (Type 4) function parser recursion unbounded → 6.6 KB file fatal stack overflow | internal/core/function_ps.go:110-137 | CONFIRMED (executed) |
| C15 | Critical | core/crash | Type 0 function `/Size` product overflow → negative index → unrecovered panic in ExtractImages | internal/core/function.go (evalType0, readSampleBits) | CONFIRMED (executed) |
| C16 | Critical | core/crash | Inline-image `/L` accumulator overflow → ExtractText panics; ~12 validator checks become "internal" | internal/core/content.go:319-327,382-410 | CONFIRMED (executed) |
| C17 | Critical | dpart/crash | DPM array containing itself → fatal stack overflow in ValidateDParts / ValidatePDFVT | dpart/dpart.go:258-282 | CONFIRMED (executed) |
| C18 | Critical | core/DoS | Struct elements sharing one `/K` array → N² ChildTypes memory; 948 KB file OOMs PDF/UA and PDF/A-a | internal/core/structtree.go:199-243 | CONFIRMED (executed) |
| C19 | Critical | pdfx/DoS | Type3 fonts forming a DAG → exponential colour scan, deadline ignored (acyclic sibling of prior C3) | pdfx/pdfx_color.go:133-234 | CONFIRMED (executed) |
| C20 | Critical | pages/crash | AppendPages nil-derefs when target `/Pages` is a direct dict; `pdf0 merge` crashes (sibling of prior C16) | pages.go:140,178 | CONFIRMED (executed) |
| C21 | High | sign | WriteSigned / WriteSignedTimestamped fail on every xref-stream source ("placeholder not found") | sign.go:47-55; objstm_write.go:73-125 | CONFIRMED (executed) |
| C22 | High | sign/security | Chain built with `ExtKeyUsageAny`, no KeyUsage check → any WebPKI TLS cert is a "trusted" document signer | sign/signatures.go:418-431; docs/signing.md:24 | CONFIRMED (executed) |
| C23 | High | crypt/security | `SetEncryption(user, "")` produces a file anyone opens without a password | crypt_api.go:14-29; internal/crypt/encrypt.go:47 | CONFIRMED (executed) |
| C24 | High | fonts | Shape/ShapeWith/Draw/DrawShaped write GID, not CID, for CID-keyed CFF → CJK unreadable in all htmlpdf output | fonts/shape.go:124,180 | CONFIRMED (executed) |
| C25 | High | fonts | ToUnicode built from the font's cmap, not from drawn glyphs → shaped text (Indic, ligatures) extracts wrong | fonts/embed.go:549-582 | CONFIRMED (executed) |
| C26 | High | htmlpdf | `@page` size/margins ignored: laid out on the CSS sheet, written on the options sheet | htmlpdf/pdfout.go:276 | CONFIRMED (executed) |
| C27 | High | htmlpdf | Backend reshapes runs without layout's context → drawn glyphs/advances differ from measured (ligatures, kerning, joining) | htmlpdf/pdfout.go:396 | CONFIRMED (executed) |
| C28 | High | htmlpdf | `rgba()` alpha and `opacity` ignored → translucent fills paint opaque | htmlpdf/pdfout.go:295,315 | CONFIRMED (traced) |
| C29 | High | pages | AddPage/AppendPages set root `/Count` = direct-kids+1 → wrong page count on any nested tree; new page inherits root `/Rotate` | page_add.go:162-163,215-218; pages.go:147-149 | CONFIRMED (executed) |
| C30 | High | pages | ExtractPages/AppendPages drop inherited MediaBox/Resources/Rotate/CropBox (AppendPages then inherits the target's) | pages.go:123-150 | CONFIRMED (executed) |
| C31 | High | pages/crypt | Page copy from a Locked (undecrypted) source writes ciphertext as plaintext, no error | pages.go:152-189 | CONFIRMED (executed) |
| C32 | High | metadata | SetDocumentInfo regenerates XMP → drops Factur-X, PDF/UA, PDF/X, PDF/VT ids and attribute-form pdfaid (Factur-X file goes 0→6 violations) | create.go:104-107,181-291 | CONFIRMED (executed) |
| C33 | High | pdfa | No 2u/3u levels; `LevelFor(U)`→B guarantees a 6.6.4 FP; `Save` refuses every 2u/3u doc; 2a fails at 2b | pdfa/pdfa.go:31-54,2235-2244; pdfa/create.go:208-249; save.go:48 | CONFIRMED (executed) |
| C34 | High | pdfa | `DeclaredLevel` drops the conformance letter → PDF/A-4 embedding a conforming a/u file gets a 6.9 FP | pdfa/declared.go:10-35; embedded.go:53-61 | CONFIRMED (executed) |
| C35 | High | pdfa | 1b Info↔XMP compare never XML-unescapes → FP on any `&`/`<`/`>` title, incl. pdf0's own output | pdfa/pdfa.go:2983-3071; internal/core/color.go:19-42 | CONFIRMED (executed) |
| C36 | High | pdfa | ~20 rule inputs type-asserted without resolving refs → FPs and evasions (prior C18 reopened) | pdfa/pdfa.go:527,535,701,831,1187-1221,1890,2346-2376,2632,2779,2826,2861,3303,3618-3661,3777,4175 | CONFIRMED (executed) |
| C37 | High | pdfa/DoS | Action `/Next` chain rewalked per referrer, recursive, uncancellable → 1.17 MB file 80 s; stack overflow on long chains | pdfa/pdfa.go:1904-1948,1970-2042 | CONFIRMED (executed) |
| C38 | High | pdfa/DoS | `checkLinearizedTrailerID` reparses from every "trailer" substring → quadratic, uncancellable | pdfa/filestructure.go:1234-1277 | CONFIRMED (executed) |
| C39 | High | pdfa/DoS | Shared AP/content stream re-tokenised once per referrer → 37 KB file 18 s | pdfa/content_operators.go:97-128,177-187 | CONFIRMED (executed) |
| C40 | High | pdfa/DoS | XMP text accumulation `Text += string(t)` quadratic → 4 MiB packet 131 GB allocated | pdfa/xmp.go:167-170 | CONFIRMED (executed) |
| C41 | High | pdfua/DoS | RoleMap chain budget is per call, not per run → C20 back; 1.75 MB file 92 s, context ignored | internal/core/structtree.go:65-100 | CONFIRMED (executed) |
| C42 | High | dpart/DoS | Leaf page ranges iterated leaves×pages, overlaps allowed, context ignored → 8.4 MB file 38 s | dpart/dpart.go:186-220; dpart_api.go:41-51 | CONFIRMED (executed) |
| C43 | High | facturx | Empty or undecodable invoice XML validates clean (rule engine silently skipped) | facturx/facturx.go:247-257,313; facturx/orderx.go:180-192,226 | CONFIRMED (executed) |
| C44 | High | facturx | EmbedFacturX replaces EmbeddedFiles + XMP, duplicates `/AF`; validator then checks the stale invoice | facturx/write.go:165-188 | CONFIRMED (executed) |
| C45 | High | object/concurrency | `Dictionary.Get` lazily writes its index → data race under the advertised concurrent validation | object/object.go:92-113 | CONFIRMED (executed, -race) |
| C46 | High | core | `View.Content` turns every decode error into a silent nil; no ASCII85/RunLength → validators report clean | internal/core/view.go:362-386; internal/core/filters.go:378-422 | CONFIRMED (executed) |
| C47 | High | limits | Limit trips asserted as violations: capped ObjStm → 6.1.7 "malformed"; capped font → "damaged"/"empty CIDSet" | objstm.go:232-235; pdfa/filestructure.go:1214-1225; pdfa/fonts.go:708-719,1547-1557 | CONFIRMED (executed) |
| C48 | High | limits | `WithMaxDecodedStreamBytes(math.MaxInt)` → every Flate/LZW stream decodes to empty with nil error; negatives accepted | internal/core/filters.go:77,412-421; limits.go:99 | CONFIRMED (executed) |
| C49 | High | save | `Save` returns ConformanceError for checker findings (limit/cancel); readback ignores ctx and the doc's limits | save.go:317-347 | CONFIRMED (traced) |
| C50 | High | core/DoS | Type 0 function loops 2^m corners (m = DeviceN colorants, unbounded) → 1×1 image never finishes | internal/core/function.go (evalType0 corners) | CONFIRMED (executed) |
| C51 | High | core/DoS | PostScript step budget is per evaluation; images evaluate per pixel with no memo → hours per image (prior C21 partial) | internal/core/function_ps.go:47; images/imagecolor.go:469-476 | CONFIRMED (executed) |
| C52 | High | core/DoS | Embedded CMap lookup is a linear range scan per code → 295 KB file 82 s | internal/core/cmap.go:92-111,151-164 | CONFIRMED (executed) |
| C53 | High | core/DoS | ToUnicode bfrange: per-range cap, no aggregate cap → linear-in-copies, hours at the 64 MB cap | internal/core/queries.go:394-402,483-510 | CONFIRMED (executed) |
| C54 | High | extraction | Image and text extraction have no recover boundary (docs promise "Note, not panic") | images_api.go:74-85; images/imageextract.go:247; text.go | CONFIRMED (executed via C11/C13/C15/C16) |
| C55 | High | jbig2/DoS | Halftone grid decode work (gw·gh·bpp) not charged to the pixel budget | internal/jbig2/jbig2_halftone.go:101-193,226-236 | PLAUSIBLE |
| C56 | Med | sign | Revocation freshness measured against `time.Now()`: B-LT material goes stale in days; missing nextUpdate = fresh forever | sign/revocation.go:74-83 | CONFIRMED (traced) |
| C57 | Med | sign | ESSCertIDv2 with the optional hashAlgorithm is rejected as malformed; hash always assumed SHA-256 | sign/signatures.go:93-95,678-687 | CONFIRMED (executed) |
| C58 | Med | crypt | R6 passwords not SASLprep'd or truncated to 127 bytes; R2-4 not PDFDocEncoded | internal/crypt/crypt.go:109; internal/crypt/encrypt.go:36 | CONFIRMED (executed) |
| C59 | Med | crypt | Unbounded `/Encrypt /Length` slices a 16-byte key → Read fails via last-resort recover | internal/crypt/crypt.go:116-119,195-201 | CONFIRMED (executed) |
| C60 | Med | sign | WriteArchivalTimestamp replaces `/DSS` (drops prior CRL/OCSP/VRI); no API to add revocation data | doctimestamp.go:66-78,114-115 | CONFIRMED (traced) |
| C61 | Med | crypt | `/EFF` and per-stream `/Crypt` filters ignored → attachments stay ciphertext, not Locked | internal/crypt/crypt.go:151-179,676-691 | PLAUSIBLE |
| C62 | Med | docs/sign | sign_api.go says nil roots = system store; code builds no chain | sign_api.go:21-31 | CONFIRMED (traced) |
| C63 | Med | validators | Undecryptable docs validated on ciphertext with no "limit" finding (PDF/UA asserts 5/7.1/7.2 on garbage) | document.go:325-333; internal/crypt/crypt.go:85-110 | CONFIRMED (traced) |
| C64 | Med | pdfa | PDFA4E/PDFA4F levels never reach the variant gates; `effectiveVariant`'s level branch is dead | pdfa/pdfa4_variants.go:30-80; pdfa/pdfa4_ef.go:176-230 | CONFIRMED (executed) |
| C65 | Med | pdfa | ICCBased overprint rule ignores q/Q and operator order → FP | pdfa/pdfa.go:4755-4787,4878-4953 | CONFIRMED (executed) |
| C66 | Med | pdfa | Separation tint-consistency finding varies run to run (map order) | pdfa/pdfa.go:4252-4284,4361-4384 | CONFIRMED (executed) |
| C67 | Med | pdfa | Type3 width check compares glyph-space `/Widths` to text-space value → FP unless FontMatrix = 0.001 | pdfa/fonts.go:976-983 | CONFIRMED (executed) |
| C68 | Med | pdfa | CIDFontType2 glyph-existence ignores stream CIDToGIDMap (width check honours it) → FP | pdfa/fonts.go:917-918,1106-1120 | CONFIRMED (executed) |
| C69 | Med | pdfa | Byte-level checks trust caller `raw` against stale `doc.Offsets`; two unguarded slices | pdfa_api.go:120-160; pdfa/filestructure.go:610-633,900-921,1294-1343 | CONFIRMED (executed) |
| C70 | Med | pdfa | Legal `>>stream` (no whitespace) → false 6.1.7.1 Length mismatch | pdfa/filestructure.go:1307,1387-1421 | CONFIRMED (executed) |
| C71 | Med | pdfa | Any delimited word "xref" (even in a string) → 6.1.4 FP | pdfa/filestructure.go:446-458 | CONFIRMED (executed) |
| C72 | Med | pdfa | NewPDFADocumentWithInfo accepts invalid UTF-8 / U+FFFE → non-well-formed XMP, no error | pdfa/create.go:305-330 | CONFIRMED (executed) |
| C73 | Med | core | Device-colour: `sh` counted as painting, forms assumed to start in DeviceGray → DeviceGray FP | internal/core/color.go:505-512,628-632; internal/core/devicecolour.go:147-152,296-305 | CONFIRMED (executed) |
| C74 | Med | core | Text render mode not restored by `Q` → `q 3 Tr Q` hides a visible unembedded font | internal/core/fontuse.go:114-137,474-515 | CONFIRMED (executed) |
| C75 | Med | core | The substring "usecmap" anywhere (even a comment) refuses the CMap and silently skips font checks | internal/core/cmap.go:233 | CONFIRMED (executed) |
| C76 | Med | core | `HasForbiddenUnicodeTargets` misaligns on array-form bfrange → A-4 6.2.10.7 FP | internal/core/fontuse.go:242-292 | CONFIRMED (executed) |
| C77 | Med | core | `DecodePDFTextString` has no PDFDocEncoding table → 6.7.3 FP on "Café" | internal/core/queries.go:325-348 | CONFIRMED (executed) |
| C78 | Med | core | Embedded CMap cidrange/cidchar: one entry per `\n` line; CR-only CMaps collapse | internal/core/cmap.go:273-324 | CONFIRMED (traced) |
| C79 | Med | text/DoS | ExtractText installs no Run: a content stream shared by N pages is decoded N times (126 KB → 99 s) | text.go; internal/core/view.go:362 | CONFIRMED (executed) |
| C80 | Med | pdfua | `checkFigureAlt` uses raw `/S` → role-mapped Figure without Alt passes (C29 sibling) | pdfua/pdfua.go:1321 | CONFIRMED (executed) |
| C81 | Med | pdfua | `checkUAAnnotStructType` uses parent's raw `/S` → FP on role-mapped Link | pdfua/pdfua_content.go:329-368 | CONFIRMED (executed) |
| C82 | Med | pdfx | PDF/X-1a:2001 identification (`GTS_PDFXConformance`) never read → FP | pdfx/pdfx.go:60-72,289-316 | CONFIRMED (executed) |
| C83 | Med | validators | Rules iterate `doc.Objects`: direct dicts invisible (inline JS action passes PDF/X), orphans judged | pdfx/pdfx.go:151-191; pdfua/pdfua.go:459-516,1051-1068 | CONFIRMED (executed) |
| C84 | Med | validators | Graph walkers dedupe but have no depth cap (struct tree, UA walks, DPart, pdfa colour/font walkers, core) | internal/core/structtree.go:199; pdfua/pdfua_struct.go:83; dpart/dpart.go:93; pdfa/pdfa.go (several) | CONFIRMED (linear stack growth executed); fatal at default PLAUSIBLE |
| C85 | Med | pdfx | Level gating for X-1a/X-3/X-6 wrong or missing (profile requirement, colour rule, Trapped, invalid level→X-4) | pdfx/pdfx.go:220-240,289-383 | PLAUSIBLE |
| C86 | Med | text | ExtractText uses the first-rune ToUnicode probe → ligatures, non-BMP and array bfrange lost | text.go:192; internal/core/queries.go:353,608-617 | CONFIRMED (executed) |
| C87 | Med | text | ExtractText's cycle guard never unmarks → a form drawn 3× extracts once | text.go:147-157 | CONFIRMED (executed) |
| C88 | Med | text/pdfa | Type0 code splitting hard-coded to 2 bytes (ExtractText; Level A PUA scan) | text.go:188-191,283-287; pdfa/pdfa_levela_content.go:177-193 | PLAUSIBLE |
| C89 | Med | pages | ExtractPages follows back-references (Dest, field /P, /B) → copies the whole source | pages.go:43-91 | CONFIRMED (executed) |
| C90 | Med | pages | ExtractPages with a repeated index lists one page object twice (`/Count` 2, PageCount 1) | pages.go:44-46,158-163 | CONFIRMED (executed) |
| C91 | Med | structure | SetStructureTree leaves stale `/StructParents` → marked content resolves to the wrong element | structure.go:77-86,295-302 | CONFIRMED (executed) |
| C92 | Med | structure | Accepts PDF 2.0-only tags but writes no `/NS` → non-standard in the default namespace | structure.go:183-185,330-361 | CONFIRMED (traced) |
| C93 | Med | preflight | `Repair(level)` ignores level; misses direct-annotation and field `/AA`; deletes A-4-legal `/AA` | preflight.go:36-80 | CONFIRMED (traced) |
| C94 | Med | pages/fonts | `Page.Faces` embeds a new, growing subset on every AddPage → quadratic size | page_add.go:172-198 | CONFIRMED (traced) |
| C95 | Med | build | Output not reproducible: map order leaks into bytes (10 distinct outputs of 20 builds) | page_add.go:192,241-245; structure.go:106-108 | CONFIRMED (executed) |
| C96 | Med | build/perf | `Document.Add` is O(n) → every builder quadratic (40k Adds = 12 s) | document.go:1124-1136 | CONFIRMED (executed) |
| C97 | Med | content | Builder doesn't track BMC/BDC/EMC balance; allows state/colour/`sh` ops inside a path | content/text.go:214-263; content/builder.go:10-28,110-123 | CONFIRMED (executed) |
| C98 | Med | object | Lazy dict index goes stale on in-place Keys/Values edit (only past 64 keys) | object/object.go:67-80,104-113 | CONFIRMED (executed) |
| C99 | Med | object | `Object` accepts value and pointer forms; serializer/Equal/validators each handle only one | object/object.go:31-205; syntax/serializer.go:65-99; object/compare.go | CONFIRMED (executed) |
| C100 | Med | write | Object-stream packing discards `WriteObject` errors → corrupt ObjStm, Write returns nil | objstm_write.go:133-138 | CONFIRMED (executed) |
| C101 | Med | xref | Exported `ParseXRefStream` panics on large `/W` (sum overflow) | xref.go:167-178,233-241 | CONFIRMED (executed) |
| C102 | Med | read | Dangling / non-stream type-2 container fails the whole Read (no rebuild retry) | objstm.go:212-219,241-253 | CONFIRMED (executed) |
| C103 | Med | tests | XMP RelaxNG guard always skips (path relative to `pdfa/`) — off since the package split, in CI too | pdfa/xmp_rng_test.go:317-321 | CONFIRMED (executed) |
| C104 | Med | tests | Corpus ratchets pass green over an empty/partial corpus directory | testdata_test.go:27-30,80-90 | CONFIRMED (executed) |
| C105 | Med | docs | CONTRIBUTING / testing.md describe July's CI (Go 1.25, 5 steps, "no corpus in CI") | CONTRIBUTING.md:24-39; docs/testing.md:17,278-299 | CONFIRMED (traced) |
| C106 | Med | docs | Package-doc examples use `pdf0.PDFA4`, which does not exist | doc.go:70,112 | CONFIRMED (executed) |
| C107 | Med | docs | Four places say Write refuses a Locked doc; it writes the original bytes back | document.go:34-41; docs/architecture.md:186-189; docs/troubleshooting.md:51-57 | CONFIRMED (executed) |
| C108 | Med | docs | README layout, CONTRIBUTING rule recipe, pdfa.md "verbatim" snippet all describe the pre-split flat layout | README.md:258-268; CONTRIBUTING.md:72-73; docs/pdfa.md:14-67 | CONFIRMED (traced) |
| C109 | Med | limits | `WithMaxICCProfileBytes`/`WithMaxXMPPacketBytes` trips are silent (contradicts "every trip is a limit finding") | internal/core/color.go:314-351; README.md:109-114; doc.go:50-54 | CONFIRMED (executed) |
| C110 | Med | fonts | Embedded font programs uncompressed; ToUnicode spans the whole cmap (3 CJK chars → 1.9 MB) | fonts/embed.go:55-195,549-582 | CONFIRMED (executed) |
| C111 | Med | fonts | `ShapeWith` lacks the `composite()` guard → 2-byte codes into a 1-byte font | fonts/shape.go:87 | CONFIRMED (executed) |
| C112 | Med | fonts | OS/2 fsType restrictions ignored → Restricted-License fonts embedded silently | fonts/embed.go:55 | CONFIRMED (executed) |
| C113 | Med | cli/security | CLI passwords only via argv flags (visible in `ps`, shell history) | cmd/pdf0/commands.go:15,36,74,95-96,123,142,211 | CONFIRMED (traced) |
| C114 | Med | htmlpdf | Vertical writing modes drawn horizontally, silently | htmlpdf/pdfout.go | CONFIRMED (executed) |
| C115 | Low | syntax | Serializer writes constructs its own parser rejects (negative objnum; nested `obj`) | syntax/serializer.go:90-96,281-292 | CONFIRMED (executed) |
| C116 | Low | syntax | Integer lookahead fails a valid integer when the *next* object is malformed; `Lexer().Position()` runs ahead | syntax/parser.go:60-63,162-173 | CONFIRMED (executed) |
| C117 | Low | syntax | Integer > int64 / real > float64 aborts the enclosing object | syntax/parser.go:117-121,208-213 | CONFIRMED (executed) |
| C118 | Low | object | `object.Int(Real(1e300))` = MinInt64 (doc promises 0 for non-numbers only) | object/object.go:250-258 | CONFIRMED (executed) |
| C119 | Low | syntax | >1 MiB whitespace gap reported as `unknown keyword ""` | syntax/lexer.go:191-220,484-513 | CONFIRMED (executed) |
| C120 | Low | syntax | `endstreamendobj` (no separator) unparseable on both paths | syntax/parser.go:438,473-480,573-590 | CONFIRMED (executed) |
| C121 | Low | object | `Dictionary.Delete` removes only the first duplicate key | object/object.go:149-159 | CONFIRMED (executed) |
| C122 | Low | object | Real-Real (absolute 1e-10) vs Int-Real (relative) tolerance → non-transitive Equal; NaN ≠ NaN | object/compare.go | CONFIRMED (executed) |
| C123 | Low | syntax | `NewLexerFromReaderAt` panics on negative size; allocates `size` eagerly | syntax/lexer.go:104-114 | CONFIRMED (executed) |
| C124 | Low | syntax | Mismatched Keys/Values lengths or nil panic in serializer/Get/Equal | syntax/serializer.go:240-250; object/object.go:110-116 | CONFIRMED (executed) |
| C125 | Low | write | Generations > 99999 emit a 21-byte xref line | document.go:979; incremental.go:133 | CONFIRMED (executed) |
| C126 | Low | read | >1 KB trailing junk or out-of-range startxref is fatal instead of rebuilding | document.go:170-186,575-587 | CONFIRMED (executed) |
| C127 | Low | incremental | WriteIncremental writes the original before serialising; `/ID` never refreshed; free entries always gen 1 | incremental.go:63-66,84-87,101-103,127 | CONFIRMED (traced) |
| C128 | Low | xref | Subsection start near MaxInt wraps to negative objnums | xref.go:128,244 | PLAUSIBLE |
| C129 | Low | pages | Inline (direct) pages silently dropped by ExtractPages/AppendPages (C16 fix turned panic into loss) | pages.go:125-137 | CONFIRMED (traced) |
| C130 | Low | pattern | Uncoloured tiling pattern accepts `ri`/`sh`/image `Do`; doc says "undefined" (spec: ignored) | content/builder.go:165-171; pattern.go:141-145 | CONFIRMED (traced) |
| C131 | Low | build | NaN/Inf accepted by most builders; fails only at Write after mutation | page_add.go; annotation.go; form.go; shading.go | CONFIRMED (executed) |
| C132 | Low | annot/security | `checkURI` bypassed by `"java\tscript:"` / leading space; non-ASCII URIs written raw | annotation.go:125,146-159 | CONFIRMED (executed) |
| C133 | Low | softmask | Soft-mask builders accept a form that is not a transparency group | softmask.go:22-64; form.go:81-94 | CONFIRMED (traced) |
| C134 | Low | shading | ShadingPattern coordinates are page space; no `/Matrix`; doc says otherwise | shading.go:34-38,88-101 | CONFIRMED (traced) |
| C135 | Low | structure | Huge MCID sizes the ParentTree array → OOM | structure.go:305-316 | CONFIRMED (executed) |
| C136 | Low | outline/annot | Destinations accept any object number as a "page" | outline.go:73-75; annotation.go:115-120; structure.go:300-302 | CONFIRMED (executed) |
| C137 | Low | pdfa | Embedded-PDF/A check goes exactly one level deep (incl. the non-PDF file-type check) | embedded.go:59; pdfa/final_rules.go:498 | CONFIRMED (traced) |
| C138 | Low | pdfa | Direct font dicts reported as `object -1`, duplicated per page | pdfa/pdfa.go:1280,1431-1445,2676 | CONFIRMED (executed) |
| C139 | Low | api | Invalid/zero Level values run a silent hybrid (pdfa: `Level(0)`=1b; pdfx: invalid→X-4) | pdfa/pdfa.go:31-54; pdfx/pdfx.go | CONFIRMED (executed) |
| C140 | Low | pdfa | OutputIntent profile judged twice (6.2.2 + 6.2.3); A-4 page-level profiles never checked; clause IDs disagree | pdfa/pdfa.go:719-782,789-928,4149-4239 | CONFIRMED (executed) |
| C141 | Low | pdfa | pdfaid/conformance read by substring scraping (comments, `=` whitespace, attributes, prefixes) | pdfa/pdfa.go:2194-2204,2282-2292; pdfa/pdfa_levela.go:101-121; pdfa/final_rules.go:558-572 | CONFIRMED (executed, Level A) |
| C142 | Low | pdfa | One inline-image `/Intent` reported twice; three inline-dict parsers | pdfa/filestructure.go:977-997; pdfa/final_rules.go:122-131 | CONFIRMED (executed) |
| C143 | Low | pdfa | Tiling-pattern violations attributed to the loop index, not the object number | pdfa/content_operators.go:223 | CONFIRMED (traced) |
| C144 | Low | api | Nine validators panic on a nil `*Document`; only ValidatePDFA answers | limits_report.go:192-199; facturx_api.go:21 | CONFIRMED (executed) |
| C145 | Low | pdfua/pdfvt | `Error()` prints "PDF/UA-1" for UA-2, "PDF/VT-1" for VT-2; VT-2 base prefixed "pdfx-4/" | pdfua/pdfua.go:24-29; pdfvt/pdfvt.go:34-39 | CONFIRMED (traced) |
| C146 | Low | pdfr | Claims a transparency rule it lacks; identification is a substring; bad version passes | pdfr/pdfr.go:9-20; pdfr_api.go:50-72 | CONFIRMED (traced) |
| C147 | Low | pdfx | Device-colour cache stores results computed mid-cycle | pdfx/pdfx_color.go:240-273 | PLAUSIBLE |
| C148 | Low | facturx | Order-X skips fx:Version; `XMPPacket(ORDER)` emits invoice namespace; case-insensitive names; composition by message substring | facturx/*.go; pdfvt | CONFIRMED (traced) |
| C149 | Low | core | `ScanStreamForDeviceOps`' own inline-image skipper ignores `/L` (prior C35 sibling) | internal/core/color.go:634-724 | PLAUSIBLE |
| C150 | Low | core | `TokenizeContent` string/hex decoding drifts from the shared decoders | internal/core/content.go:131-208 | CONFIRMED (traced) |
| C151 | Low | core | Indirect `/Filter`/`/DecodeParms` not resolved → predictor skipped silently | internal/core/filters.go:146-181,344-376 | CONFIRMED (traced) |
| C152 | Low | core | `ClassifyCalibratedCS` reads ICC `/N` only if direct | internal/core/color.go:418-420 | CONFIRMED (traced) |
| C153 | Low | core | Type3 fonts scanned whether used or not; CharProcs' XObjects missed; dead code | internal/core/devicecolour.go:279-312; internal/core/fontuse.go:141-183 | CONFIRMED (traced) |
| C154 | Low | sign/crypt | Eight smaller signing/crypto defects (OCSP-first masks CRL, PSS params, eContentType, V4 key length, orphan /Encrypt, /Perms, …) | sign/*.go; internal/crypt/crypt.go | mixed (see §3) |
| C155 | Low | cli | merge overwrites an input; no stdin/stdout; wrong-password hint; repair readback | cmd/pdf0/commands.go | CONFIRMED (executed) |
| C156 | Low | devtools | corpustime nil-deref, corpusprobe panics on -1 workers, rulecoverage saturated, check-no-binaries staged-size gap | internal/cmd/*; scripts/check-no-binaries.sh | CONFIRMED (executed) |
| C157 | Low | ci | CI never runs profiles/wtpdf/ccitt/jbig2/notocjk oracles; hand-placed oracles never run, undocumented | .github/workflows/ci.yml:125 | CONFIRMED (traced) |
| C158 | Low | docs | signing.md snippet uses nonexistent `pdf0.RevocationRevoked` | docs/signing.md:34 | CONFIRMED (executed) |
| C159 | Low | docs | README says no release tags; v0.3.2 is @latest | README.md:222-223 | CONFIRMED (executed) |
| C160 | Low | docs | "Root re-exports nothing" false (aliases, PDFAOptions, syntax ctors); "28 methods" is 40 | README.md:237-251; docs/architecture.md:32-34; object_api.go:16 | CONFIRMED (traced) |
| C161 | Low | docs/dx | Headline limits example uses values the godoc says reject real documents | README.md:101-105; limits.go:93-107 | CONFIRMED (traced) |
| C162 | Low | docs | README "one known missed Isartor violation"; baseline is 0 | README.md:217-220 | CONFIRMED (traced) |
| C163 | Low | api | ~30 exported functions take internal `core.View` → uncallable, but shown as API | sign, pdfa, facturx, images, … | CONFIRMED (go doc) |
| C164 | Low | docs | `go run ./internal/cmd/rulecoverage` needs `-tags devtools`; `make fuzz` and three .gitignore make targets don't exist | docs/testing.md:222; ci.yml:165 | CONFIRMED (executed) |
| C165 | Low | docs | Documented cmap fuzz targets moved to forme → "no fuzz tests", exit 0; fuzz-corpus gitignore claim reversed | docs/testing.md:122-191 | CONFIRMED (executed) |
| C166 | Low | dx | Untracked 25 MB `pdf0.test`, 9.8 MB `fonts.test`, `.hbenv` venv in root; .gitignore example list partial | repo root; .gitignore | CONFIRMED (traced) |
| C167 | Low | docs | Bundle of comment/doc drift (counts, file names, identifiers, orphaned comments) | many (§3.J) | CONFIRMED (traced) |
| C168 | Low | htmlpdf | One page only, links dropped — undocumented | htmlpdf/*; docs/htmlpdf.md | CONFIRMED (executed) |
| C169 | Low | examples | examples/flavored shares one Face across three documents, against `NotoSans`'s own doc | examples/flavored/main.go | CONFIRMED (traced) |

---

## 2. System map

### 2.1 Layers (real dependency direction)

```
cmd/pdf0, examples/*, htmlpdf ──────────────► root package pdf0 (facade: Read, Document, Validate*, builders)
                                                  │  Document.view() → core.View{Objects, Trailer, Limits, *Run, Cancel}
          ┌───────────────┬───────────────┬──────┴───────┬──────────────┬──────────────┐
          ▼               ▼               ▼              ▼              ▼              ▼
        pdfa          pdfua/pdfx/      sign          images         facturx         fonts ──► forme (shaping, sfnt/CFF)
     (rules, XMP,     pdfvt/pdfr/    (CMS, PAdES,   (extract,       (container,              formalis (EN 16931)
      builder)          dpart        TSA, revoc.)    codecs)         embed)                  golittlecms, gopenjpeg
          └───────────────┴───────────────┴──────┬───────┴──────────────┘
                                                 ▼
                             internal/core  (View, Resolve*, filters, content tokenizers,
                                             fontuse, cmap, colour, functions, structtree, limits/trips)
                             internal/crypt (standard security handler)   internal/finding (Guarded, Sort)
                             internal/jbig2, internal/ccitt
                                                 ▼
                                   syntax (lexer/parser/serializer) ──► object (value types)
```

Cross-boundary callbacks are installed per run rather than imported: `pdfa.SetEmbeddedChecker` and `facturx.SetPDFAChecker`. That is sound. The cost is that ~30 exported functions in public packages take the internal `core.View`, so they cannot be called from outside the module (C163).

### 2.2 Real execution paths

**Read** (`document.go`):
- It finds `startxref` in the last 1 KB. Anything else is fatal: C126.
- It walks `/Prev` with a cycle guard. Each section is a traditional table or an xref stream.
  - An xref stream is parsed into a `map[int]XRefEntry`, one slot per entry. Nothing bounds the count: C7.
  - A trailer's `/XRefStm` is **ignored**: C4.
  - While walking `/Prev`, older xref-stream objects are inserted into `Objects` by number.
- Objects are loaded by offset, with `/Length` recovery.
- Type-2 entries are materialised from object streams. Decoding is charged to a 512 MB decoded-bytes budget, which does not bound heap: C9.
- `normalizeStructure` drops the `/XRef` and `/ObjStm` objects and strips the xref-stream keys, `/Prev` and `/Size`. **This throws away the file's object-number high-water mark**, which C3 depends on.
- Decryption runs if `/Encrypt` is present and a password works; otherwise `Locked()`.
- Any panic becomes an error at the Read boundary. That is sound, and it is the *only* boundary outside validators: see C54.

**Write:**
- It re-serialises the whole model, choosing the xref form from the source.
- With an xref stream, objects are packed into new ObjStms by `buildWriteSet`. That path swallows serialisation errors (C100) and packs `/Sig` dictionaries (C21).
- Encrypted documents are re-encrypted with a fresh IV.
- A Locked document is written back byte-for-byte. The docs say it is refused: C107.

**WriteIncremental, WriteSignedIncremental, WriteArchivalTimestamp:**
- The original bytes go out first, then new or changed objects numbered `max(Objects)+1` (C3), then a traditional section whose `/Prev` points at the old one.
- `/Size` can go *down*.

**Validation** (the same shape for every standard):
- `beginRun` makes a shallow copy of the `Document` with a fresh `core.Run` (memo tables, trip recorder, cancel).
- Each check runs under `finding.Guarded`, which recovers panics as an `internal` finding.
- Cancellation is polled **between checks**, not inside them. That is why C19, C37, C38, C41 and C42 overrun deadlines.
- Findings are sorted, and limit trips are appended under the rule `limit`.

**Extraction** (`ExtractText`, `ExtractImages`, `Images`):
- These are plain walks over the same View, with **no recover** and, for text, **no Run**. Every decoder panic reaches the caller (C11, C13, C15, C16), and shared content is re-decoded per page (C79).

**Builders** (AddPage, SetDocumentInfo, SetStructureTree, outlines, annotations, patterns):
- They mutate the `Document` directly, calling `Add` once per object. `Add` is O(n): C96.
- None of them cleans up the objects a previous call made.
- `SetDocumentInfo` regenerates XMP from scratch: C32.

**Signing and verification:**
- `WriteSigned` writes a placeholder, finds `/ByteRange`, patches it and signs.
- `VerifySignatures` slices `raw` by `/ByteRange`, which C5 and C6 can crash, then verifies the CMS. Only `…WithRoots` builds a chain, and that chain accepts any EKU: C22.
- Revocation comes from the DSS, with the issuer matched by name only: C2.
- PAdES level logic is presence-based. A covering DocTimeStamp "seals" later changes: C1.

### 2.3 Key invariants: where each is enforced and where it is only assumed

| Invariant | Enforced | Assumed / broken at |
|---|---|---|
| Every allocation sized by a file number is capped (doc.go) | lexer, parser, JBIG2 core, LZW, Flate cap, per-range `/W` | xref entries (C7), predictor rows (C8), ObjStm heap (C9), `/W` aggregate (C10), image dims (C11), CCITT output (C12), ByteRange (C6), struct ChildTypes (C18), MCID array (C135) |
| No unbounded recursion ("prevented at the source") | `Resolve` 64 hops, function depth 32, text depth 32, image depth 16, implementation-limits 28 | colour spaces in images (C13), PS parse (C14), DPM arrays (C17), action chains (C37), struct/DPart/pdfa walkers (C84) |
| A cancelled or limited run is never mistaken for a clean one | the `limit` finding on trip recorder paths | `View.Content` nil on error (C46), ICC/XMP caps (C109), ObjStm cap → 6.1.7 (C47), Factur-X container trips dropped (C43), undecryptable → no signal (C63), Save (C49) |
| Validation is read-only and concurrency-safe | shallow copy + per-run memo | `Dictionary.Get` writes (C45) |
| Output reparses | serializer escaping, `/Length` recompute | packed-ObjStm errors (C100), negative objnum / nested obj (C115), gen > 99999 (C125), incremental numbering (C3) |
| A new object never collides with one in the file (`Add` doc) | — | C3 (ObjStm/XRef numbers dropped by normalisation) |
| Signature verdict = "document is what was signed" | CMS core, `contentsGapIsSignature` for approval sigs | sealed-by-timestamp relaxation (C1), DocTimeStamp coverage without the gap check (C1), issuer forgery (C2), TLS certs (C22) |
| Findings are deterministic | `finding.Sort` | Separation consistency built from map order (C66) |

### 2.4 What is sound

The syntax layer is careful: depth caps, overflow-checked object numbers, `/Length` trust gated on `endstream`, and round-trip escaping. The CMS core checks content-type, messageDigest and the ESS binding, and rejects SHA-1/MD5. Encryption key derivation for R2–R6 matches the spec, with full PKCS#7 padding checks and random IVs. JBIG2's budget design is sound. The validators' panic boundary, sorting and per-run copy are consistent across the family. The PDF/A corpus oracle genuinely has teeth: a planted bug gives FP=26 and missed=515.

The failures cluster in four places, each covered in §4:
- guards scoped per call rather than per run;
- a "silent nil" error convention;
- normalisation discarding facts the writers need;
- page and metadata builders that treat structure as a byte graph rather than as semantics.

---

## 3. Findings by category

Each finding has the same fields: **Sev**, **Where**, **Issue**, **Scenario**, **Status**, **Direction**. "Executed" means a program was run against the working tree, and for Critical and High it was re-run by the lead. Line numbers are at `d61adc4`.

### 3.A Signing and encryption: security semantics

**Expected:** a positive verdict means three things:
- the bytes the signer signed are what the reader sees;
- the signer chains to a caller-chosen root, with a document-signing purpose;
- revocation data is authenticated by the certificate's real issuer.

An encryption call with a user password should make content inaccessible without a password.

#### C1 — A covering self-signed DocTimeStamp "seals" arbitrary post-signature tampering (PAdES Conformant)
- **Sev:** Critical
- **Where:** sign/pades.go:74, 97-128, 160; sign/timestamp.go:161-207; internal/signtest/signtest.go (self-signed `TSACertKey` fixture)
- **Issue:** The prior C10 fix added only the timeStamping-EKU check to `verifyTimestampToken`. The TSA certificate is still never chained to any root. `ValidatePAdES` then treats "a valid DocTimeStamp covers the whole file" as excusing *any* content added after the signature (the `sealed` relaxation). `CoveringDocTimestamp` also checks only `end >= fileLen`, without the C12 `contentsGapIsSignature` layout check.
- **Scenario:**
  1. `WriteSigned`.
  2. Incremental update setting the page `/MediaBox [0 0 10 10]`.
  3. `WriteArchivalTimestamp(…, selfSignedCertWithTimeStampingEKU, key)`.
  4. `ValidatePAdES` returns `valid=true covers=false conformant=true level=B-B issues=[]`, and the page really is `[0 0 10 10]`.

  The same token yields `TimestampValid=true` with an attacker-chosen `TimestampTime`, which docs/signing.md calls "a real time". A chained TSA would not fix this. A public TSA will stamp any hash, and a timestamp proves the bytes existed, not that the change was permitted.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:**
  - Excuse missing coverage only when every later revision adds nothing but DSS/VRI/DocTimeStamp objects (an allowed-changes diff per revision).
  - Chain TSA certificates to caller roots.
  - Apply the gap check in `CoveringDocTimestamp`.

#### C2 — Revocation status forgeable: issuer found by subject DN, never by signature
- **Sev:** Critical
- **Where:** sign/revocation.go:301-308 (`issuerOf`); sign/signatures.go:390-395
- **Issue:** `issuerOf` returns the first certificate whose `RawSubject == leaf.RawIssuer` and never calls `CheckSignatureFrom`. The DSS arrives in an unsigned incremental update, which is the normal B-LT shape, so an attacker can add a fake CA with the same DN and an OCSP "good" signed by the fake key. OCSP is consulted first and the first definite answer wins.
- **Scenario:** The real CA has revoked the leaf.
  - An honest DSS gives `revocation=revoked/CRL`.
  - DSS `Certs=[fakeCA(sameDN), realCA]`, `OCSPs=[good by fake]`, with the real revoking CRL still present, gives `valid=true trusted=true revocation=good/OCSP`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:**
  - Take the issuer from the verified chain, or at minimum require `leaf.CheckSignatureFrom(issuer)`.
  - Let any authenticated "revoked" win over "good".

#### C5 — `/ByteRange` arithmetic overflow panics the public verifier
- **Sev:** Critical
- **Where:** sign/signatures.go:370-374; sign/pades.go:114-118
- **Issue:** The guard `s[0]+s[1] > int64(len(raw))` overflows. Neither VerifySignatures nor ValidatePAdES has a recover.
- **Scenario:** `/ByteRange [1 9223372036854775807 0 1]` → `panic: slice bounds out of range [:-9223372036854775808]` at signatures.go:374, out of `Document.VerifySignatures`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Validate once in `byteRangeSegments` with `s[1] > len(raw)-s[0]`, plus non-negativity. Add a recover at the Verify* entry points.

#### C6 — `/ByteRange` with many overlapping segments → unbounded allocation
- **Sev:** Critical
- **Where:** sign/signatures.go:368-375; sign/pades.go:111-119
- **Issue:** Every segment is appended to one buffer. Segment count, overlap and total size are all unbounded.
- **Scenario:** A 1.5 MB file whose ByteRange repeats `0 1500000` 150,000 times is OOM-killed at the 2 GiB cap in 2.5 s inside `VerifySignatures`.
- **Status:** CONFIRMED (executed under cgroup)
- **Direction:** Require ascending, non-overlapping segments whose total is ≤ len(raw). Ideally require the canonical 2-segment layout, and hash incrementally.

#### C21 — WriteSigned / WriteSignedTimestamped fail on every document read from an xref-stream file
- **Sev:** High
- **Where:** sign.go:47-55; objstm_write.go:73-125
- **Issue:** `buildWriteSet` packs the placeholder `/Sig` dictionary into a Flate-compressed ObjStm, so the placeholder never appears literally in the output.
- **Scenario:** A modern PDF such as veraPDF `PDF_UA-1/7.3 Graphics/7.3-t01-pass-b.pdf` → `signing: /ByteRange placeholder not found`. `WriteSignedTimestamped` is the only B-T producer, so B-T signing of such files is impossible, and the incremental alternative corrupts them (C3).
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Never pack dictionaries where `IsSignatureDict` is true, and add a test signing an xref-stream source.

#### C22 — Trusted chain accepts any EKU and no KeyUsage: a TLS server certificate is a "trusted signer"
- **Sev:** High
- **Where:** sign/signatures.go:418-431; docs/signing.md:24 (recommends `x509.SystemCertPool()`)
- **Scenario:** A leaf with EKU `serverAuth` only and KeyUsage 0 → `signer=acme-bank-legal.example unmodified=true trusted=true`. With the pool the docs recommend, any free WebPKI certificate yields `TrustedChain=true`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:**
  - Require no EKU, or a document-signing EKU (emailProtection, 1.3.6.1.5.5.7.3.36, the Adobe OIDs).
  - Require KeyUsage digitalSignature or contentCommitment.
  - Stop recommending SystemCertPool.

#### C23 — `SetEncryption(user, "")` produces a file anyone can open
- **Sev:** High
- **Where:** crypt_api.go:14-29 ("Either may be empty"); internal/crypt/encrypt.go:47
- **Issue:** An empty owner password is a valid owner password, and every reader (pdf0's Read, poppler, qpdf, pdf.js) tries `""` as the owner password. The CLI is safe here because `-owner` defaults to the user password (cmd/pdf0/commands.go:96); the library is not.
- **Scenario:** `SetEncryption("user-secret", "")` → Write → `Read` with no password → `Locked=false Title="Secret Title"`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Default an empty owner password to a random one, as Acrobat and qpdf do, or to the user password as the CLI does. Document the behaviour.

#### C56 — Revocation freshness is measured against `time.Now()`
- **Sev:** Medium
- **Where:** sign/revocation.go:74-83
- **Issue:** The prior C13 fix compares thisUpdate/nextUpdate to the wall clock.
  - An OCSP response archived in the DSS (nextUpdate is typically 7 days out) reads as Unknown a week after signing, so B-LT material is useless for most of the document's life.
  - A "good" response with no nextUpdate is treated as fresh forever. RFC 6960 reads absence as "newer information is always available".
- **Status:** CONFIRMED (traced)
- **Direction:** Evaluate against a trusted validation time (a verified, chained timestamp), and require `thisUpdate ≥ signing time`.

#### C57 — ESSCertIDv2 carrying the optional hashAlgorithm is rejected
- **Sev:** Medium
- **Where:** sign/signatures.go:93-95, 678-687
- **Issue:** The struct omits the optional leading `hashAlgorithm`, so Go's asn1 fails with "tags don't match". CAdES signatures and RFC 5816 TSA tokens that use SHA-384/512 certificate hashes come back `Valid=false`. When parsing does succeed, SHA-256 is assumed regardless.
- **Status:** CONFIRMED (asn1 behaviour executed, path traced)
- **Direction:** Add an optional `HashAlgorithm` field and hash with it.

#### C58 — R6 password handling
- **Sev:** Medium
- **Where:** internal/crypt/crypt.go:109; internal/crypt/encrypt.go:36
- **Issue:** Algorithm 2.A requires SASLprep, UTF-8 and truncation to 127 bytes; pdf0 uses raw bytes. R2–R4 passwords should be PDFDocEncoding.
- **Scenario:** A 200-byte user password set by pdf0 is rejected by `pdfinfo -upw` with the same string. pdf0 does not accept the 127-byte form that conforming producers use.
- **Status:** CONFIRMED (executed)
- **Direction:** Normalise on both read and write.

#### C59 — Malformed `/Encrypt /Length` makes Read fail via a recovered panic
- **Sev:** Medium
- **Where:** internal/crypt/crypt.go:116-119, 195-201
- **Scenario:** `/Length` −8, 256 or 4096 at R3 → `recovered from panic while reading PDF: slice bounds out of range`. docs/encryption.md says a malformed `/Encrypt` leaves the document Locked; instead the whole file is unreadable.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Require a key length of 5–16 bytes for R2–R4, and otherwise return the handler as unusable (Locked).

#### C60 — WriteArchivalTimestamp replaces the existing `/DSS`
- **Sev:** Medium
- **Where:** doctimestamp.go:66-78, 114-115
- **Issue:** `catClone.Set("DSS", new)` makes the earlier CRLs, OCSPs and VRI unreachable. There is also no API to add OCSP or CRL material, so B-LTA renewal, the normal archival step, loses revocation evidence.
- **Status:** CONFIRMED (traced)
- **Direction:** Merge into the existing DSS, and accept CRLs and OCSPs.

#### C61 — `/EFF` and per-stream `/Crypt` filters ignored
- **Sev:** Medium
- **Where:** internal/crypt/crypt.go:151-179, 676-691
- **Issue:** Acrobat's "encrypt only attachments" mode leaves embedded files as ciphertext, with `Locked()` false and no decrypt failure. A `/Crypt /Name /Identity` stream is "decrypted" anyway. `ValidateFacturX` would then read ciphertext as invoice XML.
- **Status:** PLAUSIBLE (traced; no fixture)
- **Direction:** Resolve `/EFF` for EmbeddedFile streams and honour `/Crypt /Name`. At minimum, report Locked when `/EFF` ≠ `/StmF`.

#### C62 — sign_api.go docstrings contradict the code on trust
- **Sev:** Medium
- **Where:** sign_api.go:21-31
- **Issue:** The docstrings say VerifySignatures checks "against the system's root store" and that "a nil pool means the system store". The code builds no chain for nil roots, so `TrustedChain` is always false. doc.go:144 and docs/signing.md say the opposite of the docstrings.
- **Status:** CONFIRMED (traced)
- **Direction:** Fix the docstrings, or deliberately make nil mean SystemCertPool (but see C22 before doing that).

#### C154 — Smaller signing and crypto defects (Low)
- OCSP is consulted first, so a fresh "good" masks a revoking CRL in the same DSS.
- RSA-PSS parameters (salt length, MGF hash) are ignored, so conforming PSS signatures with other salts fail. PLAUSIBLE.
- `verifyTimestampToken` never checks that eContentType is id-ct-TSTInfo.
- A V4 file with no top-level `/Length` gets a 40-bit key. For AESV2 that derives the wrong key and the file stays Locked. PLAUSIBLE.
- SetEncryption on a previously decrypted document leaves the old `/Encrypt` object in `Objects`, where it is encrypted as ordinary content and written as an orphan.
- Every DocTimeStamp reports `Valid=false` "content was modified". This is documented, but a caller that requires every result to be DocumentUnmodified rejects every B-LTA file.
- R6 `/Perms` is not validated (Algorithm 13).
- `WriteSigned` on an already-signed document silently invalidates the prior signatures. This is documented, but it is the easy path.

Status: CONFIRMED (traced) unless marked. Direction: fix individually. The two worth doing first are the "revoked wins" rule and the PSS parameters.

### 3.B Untrusted-input denial of service and crashes

**Expected:** doc.go promises that every allocation sized by a file number is capped, that unbounded recursion is "prevented at the source", and that a context deadline bounds validation. The findings below break one of those promises, reached through a public entry point. Unless noted, all were re-run by the lead under `MemoryMax=2G`.

#### C7 — Xref-stream entry count unbounded: a 19.6 KB file OOMs Read
- **Sev:** Critical
- **Where:** xref.go:228-272; document.go:193-250 (the merge copies into a second map)
- **Scenario:** `/Type /XRef /W [1 0 0] /Size N` with Flate data of N zero bytes (all free entries), N = 20M → 19,650-byte file → `oom-kill`. N = 5M → 1.16 GiB heap in 5.4 s.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Bound the entry count relative to file size, don't materialise type-0 entries that shadow nothing, and charge xref-stream decodes to an aggregate budget.

#### C8 — PNG predictor allocates a 2 GiB row buffer for empty data
- **Sev:** Critical
- **Where:** internal/core/filters.go:209 (`validate()` checks each factor, never the product), 269-280
- **Issue:** `len(data)==0` passes `0 % (rowLen+1) == 0`, then `make([]byte, rowLen)` runs with rowLen = 64·16·2²⁴/8.
- **Scenario:** Eight such `/ObjStm` objects plus a broken startxref (which forces the rebuild to decode every container) → a 1,613-byte file → `oom-kill` in `pdf0.Read`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Return early on zero rows, and bound rowLen by `len(data)` and the decode cap before allocating.

#### C9 — ObjStm budget meters bytes; object graph is ~5× larger
- **Sev:** Critical
- **Where:** objstm.go:201-256 (the check comes *before* each container, so one read can overshoot by 100 MB); internal/core/core.go:29 (default 512 MB)
- **Scenario:** Three containers, each a 90 MB array of `1 0 R ` (270 MB decoded, under the default budget), make a 403 KB file → `oom-kill` at 2 GiB. The default budget allows five.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Budget on materialised objects and elements, lower the default toward the measured real need (~9 MB), and include the next container in the check.

#### C10 — `/W` aggregate expansion (prior C1 fixed per range only)
- **Sev:** Critical
- **Where:** pdfa/fonts.go:868, 1156-1217 (`parseCIDWidths`)
- **Scenario:** `/W [0 65535 500 65536 131071 500 …]` repeated 2,000 times (~38 KB; the 807 KB file is mostly the embedded TTF) → `ValidatePDFABytes(PDFA2b)` → `oom-kill`. A second route: `/W [0 5 0 R 1000000 5 0 R …]` re-expands one big array per reference.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Cap the total at the 65,536-CID space, ignore CIDs above it, and charge sub-array expansion to the same budget. Better, look widths up lazily for the CIDs actually shown.

#### C11 — Image dimension overflow → unrecovered panic or OOM
- **Sev:** Critical
- **Where:** images/imagecolor.go:303-306 (`sampleDataFits`), 73 (`NewNRGBA`), 111, 662; images/imageextract.go:546, 627
- **Issue:** The raw/Flate/LZW branch never bounds Width×Height. `rowBytes := (w*ncomp*bpc+7)/8` overflows, so the fits-check passes on 4 bytes, and a pixel buffer sized from the declared dimensions is then allocated.
- **Scenario:** DeviceGray, BPC 8, `/Width 2^60 /Height 2`, 4 bytes of data → `panic: image: NewNRGBA Rectangle has huge or negative dimensions`, through `images.buildImage` → `doc.Images()`. Other values OOM instead.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** One pixel budget, with int64 arithmetic, checked before every allocation site. Mirror `images.MaxPixels` (1<<26) from embed.go:33.

#### C12 — CCITT output unbounded
- **Sev:** Critical
- **Where:** internal/ccitt/ccitt.go:54-57 (row cap 2²⁰), 105 (`out = append`)
- **Scenario:** `/K -1 /Columns 2^20 /Height 2^20` with 128 KiB of `0xFF` (about 200 bytes once Flate-wrapped) → one all-white row per bit → `oom-kill` in 2.8 s. docs/images.md's "at most 2^20 rows" describes exactly the cap that is insufficient.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Bound stride×rows (total output bytes) up front and while appending.

#### C13 — Cyclic image ColorSpace → fatal stack overflow
- **Sev:** Critical
- **Where:** images/imagecolor.go:310-339 (`resolveColorSpace`, no depth), 383 (ICCBased `/Alternate`), 394 (Indexed base), 450 (Separation/DeviceN alternate)
- **Scenario:** `10 0 obj [/Separation /Spot 10 0 R <fn>]` as an image's `/ColorSpace` → `fatal error: stack overflow` in `resolveColorSpace` ← `tintColorSpace` ← `separationColorSpace`. Unrecoverable.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Add a depth and visited guard (bail past ~16), as `collectImagesFrom` and `EvalFunction` already do.

#### C14 — PostScript function parser: unbounded `{` nesting
- **Sev:** Critical
- **Where:** internal/core/function_ps.go:110-137 (`psParseProc`)
- **Issue:** `psExec` bounds execution depth, but the parser recurses once per `{`, and Flate compresses braces about 1000:1.
- **Scenario:** A Separation tint function of 3M `{` followed by 3M `}` makes a 6,648-byte file → `fatal error: stack overflow` from `ExtractImages`. At 20M braces (a 40 KB file) it is an OOM instead.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Add a depth counter, and cap program length and token count.

#### C15 — Type 0 function Size overflow → negative index
- **Sev:** Critical
- **Where:** internal/core/function.go (evalType0: `total *= size[i]`, `needBits`, `(base+j)*bps`; readSampleBits)
- **Scenario:** `<</FunctionType 0 /Domain[0 1] /Range[0 1] /Size[4611686018427387904] /BitsPerSample 32>>` as a Separation tint → `ExtractImages` panics with `index out of range [-4]`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Use overflow-checked products, and guard `byteIdx >= 0`.

#### C16 — Inline-image `/L` overflow → ExtractText panic
- **Sev:** Critical
- **Where:** internal/core/content.go:382-410 (`InlineImageDeclaredLength`: `v = v*10 + d`, unbounded), 319-327
- **Scenario:** `BI /W 1 /H 1 /BPC 8 /CS /G /L 9223372036854775807 ID \x00 EI` → `ExtractText` panics with `index out of range [-9223372036854775732]`. `ValidatePDFA` and `ValidatePDFUA` survive, but ~12 tokenizer-based checks become "internal validator error", so the file is effectively unvalidated.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Cap the accumulator, and reject `declLen < 0 || declLen > n-binaryStart`.

#### C17 — DPM self-containing array → fatal stack overflow
- **Sev:** Critical
- **Where:** dpart/dpart.go:258-282 (`validateDPMValue`; `seen` is keyed by `*Dictionary` only)
- **Scenario:** `/DPM << /A 6 0 R >>` with `6 0 obj [6 0 R]` → `fatal error: stack overflow` from both `ValidateDParts` and `ValidatePDFVT`. A DAG of arrays costs 2^depth.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Track seen `IndirectRef.Number`, add a depth cap, and memoise arrays.

#### C18 — Shared `/K` arrays → quadratic memory in the struct tree
- **Sev:** Critical
- **Where:** internal/core/structtree.go:199-243 (`buildStructTree`: `ChildTypes` built per element from the full kids list; no budget, no cancel poll)
- **Scenario:** N elements, each `/K 5 0 R`, where object 5 lists all N elements. N = 5,000 (473 KB) takes 7.9 s against a 1 s deadline. N = 10,000 (948 KB) → `oom-kill` in both ValidatePDFUAContext and ValidatePDFAContext(PDFA2a).
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Treat a re-expanded `/K` as a DAG node, compute ChildTypes lazily or under a per-run budget, and poll cancellation.

#### C19 — Type3 font DAG → exponential PDF/X colour scan, deadline ignored
- **Sev:** Critical
- **Where:** pdfx/pdfx_color.go:133-234 (`container`; `inProgDict` blocks cycles but results are never cached, and every font is visited whether used or not)
- **Scenario:** Font i has `/Resources << /Font << /A i+1 /B i+1 >> >>`. Lead's measurement with ValidatePDFXContext and a 3 s deadline: depth 22 → 1.35 s, depth 26 (6.8 KB) → **22.2 s**. Time doubles per level, so ~7 KB at depth 40 means days. PDF/A and PDF/UA return in under 1 ms on the same file, because they follow only fonts the content uses.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Memoise per dictionary, filter by the fonts actually used, and poll `Cancel` inside `container`.

#### C20 — AppendPages crashes on a direct `/Pages`
- **Sev:** Critical
- **Where:** pages.go:178 (`pagesNum := DictObjNum(pages)` → −1), pages.go:140 (`dst.Objects[pagesNum].Value` on a nil entry)
- **Scenario:** A catalog with `/Pages << /Type /Pages /Kids [2 0 R] /Count 1 >>` inline reads fine (PageCount = 1). `d.AppendPages(other)` then panics with SIGSEGV. `pdf0 merge out.pdf direct.pdf direct.pdf` prints a Go trace and exits 2. The prior C16 fix guarded `dst.Objects[newRef.Number]` but not this sibling. AddPage rejects the same shape cleanly.
- **Status:** CONFIRMED (executed; lead re-run of the library call)
- **Direction:** Promote the direct `/Pages` to an indirect object or return an error. AppendPages needs an `error` return (see C31 and C144).

#### C37 — Action `/Next` chains: per-referrer rewalk, recursive, uncancellable
- **Sev:** High
- **Where:** pdfa/pdfa.go:1970-1978, 2008-2042 (`checkNoForbiddenActions`, `checkActionObject`, `checkActionChain`), 1904-1948 (`isForbiddenAction` builds two map literals per call, ~28% of CPU)
- **Scenario:** N dictionaries whose `/A` points at the head of an M-long chain. With N = M = 4000 (584 KB), `ValidatePDFABytesContext` with a 2 s deadline takes **16.4 s** (lead re-run). N = M = 8000 (1.17 MB) takes 80 s. A 20k chain under a 16 MB max stack gives `fatal error: stack overflow` in `checkActionChain`. At the default 1 GB stack, ~830k chained actions would be enough (a large file).
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Iterative worklist, one visited set per check (or a memoised verdict per action dict), package-level maps, and cancel polling.

#### C38 — `checkLinearizedTrailerID` is quadratic
- **Sev:** High
- **Where:** pdfa/filestructure.go:1234-1277 (called at every level from pdfa/pdfa.go:321)
- **Issue:** For every `trailer` substring it builds a new parser and `ParseObject`s. A `%` comment after each one makes every parse consume the rest of the file. The `/Linearized` gate is a raw `bytes.Contains`, which costs an attacker nothing to satisfy.
- **Scenario:** 40k × `trailer%` (330 KB) takes 5.4 s (lead re-run). 160k (1.29 MB) exceeds 120 s. Cancel is never polled.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Use the trailers Read actually located, and poll Cancel. Also answer §6 Q4: this is a PDF/A-1 rule that runs at parts 2–4.

#### C39 — Shared content stream re-tokenised per referrer
- **Sev:** High
- **Where:** pdfa/content_operators.go:105-108 (annotation `/AP`), 97-99 and 177-187 (pages; `seen` keyed by page dict), 124-128 (Type3 CharProcs)
- **Scenario:** One 16 MB-decoded (16 KB Flate) form used as `/AP /N` by 100 annotations, in a 37 KB file → 17.9 s, 95% in `ForEachContentToken`. It is linear in referrers, so 10k annotations would take about 30 min.
- **Status:** CONFIRMED (executed) for annotations; traced for pages and CharProcs
- **Direction:** Memoise per (stream, resolved resources), as `CollectFontTextUsage` (sfKey) already does.

#### C40 — XMP text accumulation is quadratic
- **Sev:** High
- **Where:** pdfa/xmp.go:167-170 (`Text += string(t)`). The "~3 s at 4 MiB" bound claimed at pdfa/xmp.go:101-104 and docs/xmp.md:226 is wrong.
- **Scenario:** `<dc:format>` plus `a<!---->` × 260k → 4.0 s and **33 GB allocated** (lead re-run). The reader measured × 520k (4.16 MB, under the 4 MiB default cap) at 18 s wall, 45 s CPU and 131 GB allocated. No cancel poll.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Accumulate with `strings.Builder`, and correct the comments.

#### C41 — RoleMap chain budget per call, not per run (prior C20 reopened)
- **Sev:** High
- **Where:** internal/core/structtree.go:65-100 (`ResolveRoleMapChain`), called per element from structtree.go:229/237, pdfua/pdfua_struct.go:104/217 and pdfua/pdfua_tablegrid.go:113/143
- **Scenario:** A chain T0→…→T99999 with M elements tagged `/S /T0`. With ValidatePDFUAContext and a 1 s deadline: M = 500 takes 10 s, M = 2000 (1.75 MB) takes 92 s. No `rolemap-work` limit ever trips. docs/pdfua.md presents the per-chain cap as the C20 fix.
- **Status:** CONFIRMED (executed)
- **Direction:** Resolve type → standard type once per run (a cache) and charge one run-wide budget.

#### C42 — DPart leaves × pages
- **Sev:** High
- **Where:** dpart/dpart.go:186-220; dpart_api.go:41-51 (cancel checked only before the walk; the godoc promises "bounded work")
- **Scenario:** 40k leaves each spanning all 40k pages (8.4 MB) takes 37.8 s against a 1 s deadline. Also reachable via ValidatePDFVT.
- **Status:** CONFIRMED (executed)
- **Direction:** Sweep sorted ranges in O(L+P), and poll cancel.

#### C50 — Type 0 function iterates 2^m corners
- **Sev:** High
- **Where:** internal/core/function.go (`corners := 1 << uint(m)`, where m is the DeviceN colorant count and is unbounded)
- **Scenario:** A 1×1 DeviceN image with 40 colorants and `/Size` of forty 1s → `ExtractImages` still running at the cap (lead re-run: timeout at 20 s; reader: 120 s). Cancellation is checked only between images.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Cap m (Type 0 functions with >16 inputs are absurd), and visit only the dimensions with a non-zero fraction.

#### C51 — PostScript step budget is per evaluation (prior C21 partial)
- **Sev:** High
- **Where:** internal/core/function_ps.go:47; images/imagecolor.go:469-476 (per-pixel `EvalFunction`, no memo)
- **Scenario:** A 16-level `dup true exch if` nest (about 590k steps, 1.6 KB file): a 10×10 image takes 0.68 s and a 40×40 image 12.4 s, so 1000×1000 would take about 2 hours, uncancellable. An 8-bit Separation image has only 256 distinct inputs.
- **Status:** CONFIRMED (executed)
- **Direction:** Memoise tint results per input, and charge a per-run step budget.

#### C52 — Embedded CMap lookup: linear per code
- **Sev:** High
- **Where:** internal/core/cmap.go:151-164 (`lookup`), 92-111 (`Decode`; runs in three PDF/A checks and one PDF/UA check per shown string)
- **Scenario:** 65,000 one-code cidranges and 1M unmapped codes in 295 KB → `ValidatePDFA(PDFA1b)` takes 1 m 22 s. With one range it takes 2.6 ms.
- **Status:** CONFIRMED (executed)
- **Direction:** Sort ranges per code width and binary-search them, and poll cancel.

#### C53 — ToUnicode bfrange aggregate unbounded
- **Sev:** High
- **Where:** internal/core/queries.go:394-402, 483-510
- **Scenario:** 2,000 copies of `<0000><FFFF><0041>` in 914 bytes → `ExtractText` 3.9 s. It is linear in copies, and the 64 MB decoded cap allows ~2.3e11 map writes.
- **Status:** CONFIRMED (executed)
- **Direction:** An aggregate budget with a trip, store ranges rather than expanding them, and memoise per stream.

#### C54 — Extraction has no panic boundary
- **Sev:** High
- **Where:** images_api.go:74-85; images/imageextract.go:247 (`Walk`); text.go (entire path)
- **Issue:** docs/images.md promises that failures are "Note strings rather than panicking; one bad image does not abort the walk". The validators and Read recover; extraction does not. C11, C13, C15 and C16 all unwind into the caller, and any future unguarded index will too.
- **Status:** CONFIRMED (executed via C11, C15, C16)
- **Direction:** Per image, recover to `Decoded=false` plus a Note, and per page for text. This is defence in depth, not a substitute for the caps.

#### C55 — JBIG2 halftone grid work escapes the pixel budget
- **Sev:** High
- **Where:** internal/jbig2/jbig2_halftone.go:101-193, 226-236; jbig2.go:88-101 (`reserve`)
- **Issue:** A halftone region reserves `ri.w*ri.h` (its output), not `gw*gh*bpp` (the grid decode). A 1×1 region with a 1024×1024 grid over a 65,536-pattern dictionary costs ~1.8e7 MQ decodes. Past-EOF bits need no coded data, and region count is bounded only by input size.
- **Status:** PLAUSIBLE (traced)
- **Direction:** Charge `int64(gw)*gh*bpp` against `allocPixels`.

#### C79 — ExtractText has no Run: shared content re-decoded per page
- **Sev:** Medium
- **Where:** text.go (uses `d.view()` without `core.NewRun`); internal/core/view.go:362
- **Scenario:** 200 pages sharing one 50 MB content stream (126 KB file) → `ExtractText` 1 m 39 s. The validators memoise the decode but re-scan per page for device-op use: 20 pages take 27.7 s.
- **Status:** CONFIRMED (executed)
- **Direction:** Install a Run in text extraction, and memoise `ScanStreamForDeviceOps` per stream key.

#### C84 — Deduplicating walkers with no depth cap
- **Sev:** Medium
- **Where:**
  - internal/core/structtree.go:199
  - pdfua/pdfua_struct.go:83; pdfua/pdfua_content.go:305; pdfua/pdfua_tablegrid.go:95; pdfua/pdfua.go:958, 1099
  - dpart/dpart.go:93
  - pdfa `find1bTransparencyXObjects`, `checkColorSpaceValueSeen`, `checkAlternateCSSeen`, `collectSeparationConsistencySeen`, `collectFontsRecursive`
  - core `View.collectPages`, fontuse.go:82-184, devicecolour.go:129-313, color.go:156-254
- **Scenario:** A 100k-element acyclic struct chain overflows a 64 MB stack (~0.7–1.3 KB per level). A 1.6M chain (123 MB file) OOMs at 2 GiB while the stack grows. With more memory, the 1 GB fatal limit is reachable. This contradicts doc.go's "prevented at the source".
- **Status:** CONFIRMED (linear stack growth executed); fatal overflow at the default limit PLAUSIBLE
- **Direction:** Explicit stacks, or a uniform depth cap that reports `limit`.

#### C135 — Huge MCID OOMs SetStructureTree
- **Sev:** Low (caller-supplied)
- **Where:** structure.go:305-316 (the ParentTree array is sized to the max MCID)
- **Scenario:** An MCID of 1<<28 → `oom-kill`.
- **Status:** CONFIRMED (executed)
- **Direction:** Bound it and return an error.

### 3.C Read/Write correctness, limits and data loss

**Expected:** Read either models every object in the file or says it could not. Write and WriteIncremental produce files that pdf0 and other readers read back to the same model. A limit is a knob: it never changes the meaning of the data, and a trip is visible as "unknown", never as "bad" or "clean".

#### C3 — Incremental writers reuse live object numbers
- **Sev:** Critical
- **Where:**
  - Allocators: document.go:1116-1136 (`Add`), sign.go:225-232, doctimestamp.go:56-64, incremental.go:95-103
  - document.go:465-484 (`normalizeStructure` drops `/XRef`/`/ObjStm` objects and `/Size`)
  - document.go:540-542 (older xref-stream objects inserted during the `/Prev` walk)
- **Issue:** After Read, `Objects` lacks the numbers of xref streams and object-stream containers, so every `max+1` allocator hands out numbers the original file still uses.
  - **Reader side:** a newer section's redefinition of an older xref-stream number is skipped as "already loaded", then deleted by normalisation.
  - **Writer side:** redefining a container number breaks every type-2 entry pointing into it, for every reader, not just pdf0.
- **Scenario:**
  - (a) An xref-stream file where the xref stream has the highest number: `Add` returns that number. After WriteIncremental and a re-read, `/Extra` resolves to nil, with no error.
  - (b) `WriteSignedIncremental` on it → re-read → `VerifySignatures` finds **0 signatures** (lead re-run).
  - (c) veraPDF `7.3-t01-pass-b.pdf` (ObjStm 48, `/Size 51`) → the output contains `48 0 obj << /Type /Annot /Subtype /Widget /FT /Sig …` and `/Size 50` → `pdf0.Read` fails with `object stream 48 is not a stream` (lead re-run). Poppler tolerates it, which hides the defect.
  - (d) Any file pdf0 wrote with an xref stream: Add + WriteIncremental produces a file pdf0 cannot read.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Record the file's highest used object number and original `/Size` at Read. Route all allocation (Add, sign, timestamp, SetEncryption's encNum) through one allocator above that mark, and never lower `/Size`. Stop inserting xref-stream objects into `Objects` during the walk. This also fixes C96 if the allocator keeps a hint.

#### C4 — Hybrid-reference files: `/XRefStm` never read
- **Sev:** Critical
- **Where:** document.go:199-266. `XRefStm` appears only as a key to delete (document.go:480, incremental.go:104).
- **Issue:** In a hybrid file the traditional table marks compressed objects free; only the `/XRefStm` stream lists them. ISO 32000 7.5.8.4 requires readers that understand xref streams to merge it before following `/Prev`. Hybrid files are a PDF 1.5 staple and common from Word.
- **Scenario:**
  - Synthetic: object 5 is absent, `/Extra` resolves to nil, and Write returns nil with the object missing from the output.
  - Real file `testdata/verapdf-corpus/Isartor test files/doc/Isartor test suite manual.pdf`: the XRefStm has 408 type-2 entries, **all 408 absent** from `doc.Objects` (lead re-run). Write succeeds.
  - `TestCorpusParsesEntirely` checks only for errors, so it cannot see this.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Parse `/XRefStm` when present and merge its entries at the same precedence as the section's table. Add a regression test on that corpus file that counts objects.

#### C47 — Limit trips are asserted as non-conformance
- **Sev:** High
- **Where:**
  - objstm.go:232-235 (a capped ObjStm goes into `brokenObjStms` like corrupt data) → pdfa/filestructure.go:1214-1225 (6.1.7 "malformed")
  - pdfa/fonts.go:708-719, 750, 847 ("font program is damaged"), 1361, 1547-1557 (empty or incomplete CIDSet)
  - internal/core/view.go:362-386 (the nil source)
- **Scenario:**
  - A valid file read with `WithMaxDecodedStreamBytes(1000)` whose ObjStm is 3 KB → object 5 absent, plus `6.1.7 checker=false: an object stream could not be decoded (malformed stream data)`, and no limit finding.
  - `WithMaxContentStreamBytes(100000)` with a 760 KB TTF → "embedded Type0 font program is damaged", next to the limit note that says the check was skipped.
  - An xref stream over the cap silently falls back to rebuild-by-scan.
  - This breaks the repo's own rule that a check must never assert a violation from an incomplete result.
- **Status:** CONFIRMED (executed; lead re-run of the ObjStm case)
- **Direction:** Use a sentinel error for cap trips, record them as trips rather than as broken containers, and have consumers decline on a trip. Relates to C46.

#### C48 — `WithMaxDecodedStreamBytes(math.MaxInt)` silently empties every stream
- **Sev:** High
- **Where:** internal/core/filters.go:412-421 (Flate: `io.LimitReader(r, int64(max)+1)` overflows negative), 77 (LZW likewise); internal/core/core.go:82-117; limits.go:99
- **Scenario:**
  - `MaxInt` → `StreamData` returns `""` with a nil error for a `BT … Tj ET` stream.
  - `-1` → every stream fails with "exceeds maximum size (-1 bytes)".
  - The natural way to write "no limit" silently makes every document look empty. No option value is validated anywhere.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Validate or clamp option values, avoid the `+1` overflow, and document the meaning of 0 and negative values.

#### C49 — Save conflates checker findings with non-conformance
- **Sev:** High
- **Where:** save.go:317-347
- **Issue:** Any violation, including `limit`/`internal`, yields `*ConformanceError` ("…does not meet it"), without `IsCheckerFinding`. The read-back uses plain `Read`: default limits, not `d.lim()`, and no context.
- **Scenario:**
  - SaveContext's deadline expires during validation → the caller gets a ConformanceError, and `errors.Is(err, context.DeadlineExceeded)` is false.
  - An authored document with a content stream over 64 MB can never be saved.
- **Status:** CONFIRMED (traced: save.go:336-345 → pdfa_api.go:151 → runLimitTrips)
- **Direction:** Split checker findings out, return ctx errors as ctx errors, and read back with the context and `d.lim()`.

#### C100 — Object-stream packing swallows serialisation errors
- **Sev:** Medium
- **Where:** objstm_write.go:133-138
- **Scenario:** On an xref-stream document, `doc.Add(object.Array{1, Real(NaN)})` → `Write` returns nil → re-read fails with `parsing object 3 in object stream 5: unterminated array`. The traditional-table path correctly errors with `cannot serialize non-finite real NaN` (lead re-run).
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Propagate the error out of `buildWriteSet`.

#### C101 — Exported `ParseXRefStream` panics on a large `/W`
- **Sev:** Medium
- **Where:** xref.go:167-178, 233-241
- **Scenario:** `/W [1 9223372036854775807 1]` → `slice bounds out of range`. Read recovers from this; the exported function does not.
- **Status:** CONFIRMED (executed)
- **Direction:** Reject any width over 8.

#### C102 — A dangling or non-stream type-2 container fails the whole Read
- **Sev:** Medium
- **Where:** objstm.go:212-219, 241-253; document.go:338-340
- **Issue:** A missing container, a non-stream container, an out-of-range index or one unparseable object each return a hard error, with no rebuild retry. docs/architecture.md's list of fatal cases omits these. C3's output is exactly this shape.
- **Status:** CONFIRMED (executed)
- **Direction:** Treat these like decode failures (`brokenObjStms` plus continue). Keep the deliberate index/objnum-mismatch hard error, and document it.

#### C125 — Generations above 99999 produce malformed xref lines
- **Sev:** Low
- **Where:** document.go:979; incremental.go:133; parser has no upper bound (syntax/parser.go:218)
- **Scenario:** `2 999999 obj` → `0000000064 999999 n\r\n` (21 bytes).
- **Status:** CONFIRMED (executed)
- **Direction:** Clamp or reject generations over 65535 at parse time, as `rebuildXRefByScan` already does.

#### C126 — Trailing junk or a bad startxref is fatal
- **Sev:** Low
- **Where:** document.go:170-186, 575-587
- **Scenario:** A valid file plus 1100 NUL bytes → `startxref not found`. A startxref past EOF → `outside file`.
- **Status:** CONFIRMED (executed)
- **Direction:** Fall back to `rebuildXRefByScan`.

#### C127 — WriteIncremental hygiene
- **Sev:** Low
- **Where:** incremental.go:63-66, 84-87, 101-103, 127
- **Issue:**
  - The original bytes are written before serialisation, so an error leaves the caller's writer holding a partial file.
  - The second `/ID` string is never refreshed (ISO 32000-2 14.4); full Write also keeps it.
  - Freed entries always get generation 1, and the free list is not linked.
- **Status:** CONFIRMED (traced)
- **Direction:** Serialise into a buffer first, refresh the second `/ID` string, and use gen+1.

#### C128 — Xref subsection start near MaxInt wraps
- **Sev:** Low
- **Where:** xref.go:128, 244
- **Issue:** `9223372036854775807 2` loads an object under a negative key, and Write then refuses the whole document.
- **Status:** PLAUSIBLE (traced)
- **Direction:** Check for overflow, and cap object numbers.

#### C109 — Configurable ICC/XMP limits trip silently
- **Sev:** Medium
- **Where:** internal/core/color.go:314-351 (`ICCProfileData` returns nil, records nothing); README.md:109-114 and doc.go:50-54 promise every trip is a `limit` finding; limits.md:180-181 admits the gap
- **Scenario:** `Read(pdfaDoc, WithMaxICCProfileBytes(1), WithMaxDecodedStreamBytes(1))` → `ValidatePDFABytes(PDFA4)` → 0 findings: no violation, no limit.
- **Status:** CONFIRMED (executed)
- **Direction:** Record trips, or weaken the promise.

### 3.D Page-tree and document-building APIs

**Expected:** page operations produce standalone, renderable pages, keep `/Count` equal to the leaf count, never copy ciphertext as plaintext, and report failure. Metadata setters add to what is there. Builders reject invalid input when called, not at Write.

#### C29 — AddPage/AppendPages set the wrong `/Count` on nested trees
- **Sev:** High
- **Where:** page_add.go:162-163, 215-218; pages.go:147-149
- **Issue:** Root `/Count` is set to `len(root.Kids)+1`, which counts direct children, not leaves. `/Rotate` is written only when non-zero, so the new page inherits the root's rotation. The `pageTree` comment assumes pdf0's own flat trees, but the API accepts any read document.
- **Scenario:**
  - Root with two intermediate nodes of two pages each: after AddPage, `/Count 3` with 5 real pages. After AppendPages of the same shape, `/Count 6` with 8 pages (lead re-run).
  - The new page inherits `Rotate 90`.
  - Via the CLI: `pdf0 merge` of a 2×3 tree plus one page gives root `/Count 3`; `pdfinfo` says "Pages: 3" while `pdf0 info` says 7, and the exit code is 0.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Set `/Count` to old `/Count` + added (or recount the leaves), write `/Rotate 0` explicitly, and materialise inherited attributes on the new page.

#### C30 — ExtractPages/AppendPages drop inherited attributes
- **Sev:** High
- **Where:** pages.go:123-150 (the pages.go:10-13 file comment admits it; the godoc does not)
- **Scenario:** Source root `/Pages` has MediaBox 200×300, `/Rotate 90` and `/Resources` with `/F1`. `ExtractPages([0])` gives a page with `MediaBox=<nil> Resources=<nil> Rotate=<nil>` (lead re-run), so a required key is missing and the font is undefined. AppendPages silently adopts the *target's* MediaBox, Rotate and Resources instead.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Resolve the four keys with `InheritedPageAttr` and set them on the copy, as the C24 fix did for text.

#### C31 — Page copy from a Locked source writes ciphertext as plaintext
- **Sev:** High
- **Where:** pages.go:152-189
- **Scenario:** A file encrypted with user password "user", read without it (`Locked()=true`) → `ExtractPages([0])` returns no error → Write returns no error → the re-read has `Locked()=false` and `ExtractText()==""`, and every stream is garbage (lead re-run). AppendPages with a Locked source behaves the same. The CLI guards this (cmd/pdf0/commands.go:191-204); the library does not.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Refuse a Locked source with an error, which needs an error return on AppendPages.

#### C32 — SetDocumentInfo discards other writers' metadata
- **Sev:** High
- **Where:** create.go:104-107, 181-226, 235-291
- **Issue:** The XMP packet is rebuilt, carrying over only three pdfaid properties. `xmlElementText` sees only the element form, so the attribute form `pdfaid:part="3"` is lost, although `pdfa.DeclaredLevel` handles it (pdfa/declared.go:20-22). Also lost: pdfuaid, pdfxid, pdfvtid, fx:*, extension schemas and dc:language.
- **Scenario:** `testdata/facturx/corpus_EN16931_Einfach.pdf`: Factur-X 0 violations and PDF/A-3b 0 → `SetDocumentInfo({Title:"Invoice"})` → Write → Read → Factur-X **6**, PDF/A-3b **2** (lead re-run). This contradicts the code's own "must not quietly stop it being one".
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Edit the existing packet through the real XMP parser, or at least carry every identification and extension schema forward.

#### C89 — ExtractPages drags in the whole source
- **Sev:** Medium
- **Where:** pages.go:43-91 (only the top page's `/Parent` is skipped)
- **Scenario:** Page 0 has a Link `/Dest [13 0 R /Fit]` → `ExtractPages([0])` gives 15 objects (the source has 14), including all 8 Page/Pages dicts (lead re-run). The link now targets an orphan copy.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Stop at `/Parent` of any Page/Pages node and at annotation `/P`, and rewrite or drop destinations to pages that were not extracted.

#### C90 — Duplicate indices
- **Sev:** Medium
- **Where:** pages.go:44-46, 158-163
- **Scenario:** `ExtractPages([]int{0,0})` → `Kids=[3 0 R 3 0 R] /Count 2`, but `PageCount()` is 1.
- **Status:** CONFIRMED (executed)
- **Direction:** Give the repeat its own copy of the page dict (sharing resources), or reject duplicates.

#### C91 — SetStructureTree leaves stale `/StructParents`
- **Sev:** Medium
- **Where:** structure.go:77-86, 295-302
- **Scenario:** The first call sets A:0 and B:1. A second call tags only B, giving B:0, while A keeps 0, so A's marked content now resolves to B's heading. `SetStructureTree(nil, nil)` leaves both pages with 0.
- **Status:** CONFIRMED (executed)
- **Direction:** Clear `/StructParents` on every page before assigning.

#### C92 — PDF 2.0-only structure tags with no `/NS`
- **Sev:** Medium
- **Where:** structure.go:183-185, 330-361
- **Issue:** Per ISO 32000-2 14.8.6.1, an element with no `/NS` is in the 1.7 namespace. The accepted set includes the Annex M 2.0-only types: DocumentFragment, Aside, Title, FENote, Sub, Em, Strong and Artifact. `StructElem{Tag:"Title"}` is written as bare `/S /Title`, which is non-standard in the default namespace.
- **Status:** CONFIRMED (traced against the spec)
- **Direction:** Emit `/Namespaces` plus `/NS` when a 2.0 tag is used, or restrict the set.

#### C93 — `Repair(level)` ignores `level` and repairs inconsistently
- **Sev:** Medium
- **Where:** preflight.go:36-80
- **Issue:**
  - `level` is unused.
  - Annotation `/AA` is removed only from top-level annotation objects, so direct annotations keep it.
  - Form-field `/AA` is untouched.
  - At PDF/A-4, interaction-event `/AA` is legal under pdf0's own 6.6.3 model, yet it is deleted, contradicting "only deletes something forbidden".
  - The "ensure /ID" block at line 52 is empty.
- **Status:** CONFIRMED (traced)
- **Direction:** Walk resolved `/Annots` and the AcroForm fields, and branch on level.

#### C94 — `Page.Faces` embeds a new growing subset per page
- **Sev:** Medium
- **Where:** page_add.go:172-198
- **Scenario:** 100 pages with the same Face embed 100 programs, with page N containing all glyphs used on pages 1..N. For CJK the size grows quadratically. The docs call Faces "the ordinary way".
- **Status:** CONFIRMED (traced)
- **Direction:** Embed once per document per face, or document the Fonts path for multi-page use.

#### C95 — Output depends on map iteration order
- **Sev:** Medium
- **Where:** page_add.go:241-245 (unused resources), page_add.go:192 (face order decides object numbers), structure.go:106-108 (role map)
- **Scenario:** 20 identical builds (with `/ID` removed) give **10 distinct outputs** (lead re-run). pdfa_reproducible_test.go makes byte-reproducible output a stated goal.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Sort names before writing.

#### C96 — `Document.Add` is O(n)
- **Sev:** Medium
- **Where:** document.go:1124-1136
- **Scenario:** 10k Adds take 0.92 s, 20k take 3.2 s, 40k take 10.7–12.1 s. structure.go, outline.go and page_add.go call Add once per element.
- **Status:** CONFIRMED (executed)
- **Direction:** A next-number hint, the same allocator as C3.

#### C97 — Content builder omits marked-content and path-state rules
- **Sev:** Medium
- **Where:** content/text.go:214-263; content/builder.go:10-28, 110-123
- **Scenario:**
  - `BeginTagged` without `EndMarked` → no error.
  - A lone `EndMarked()` → `"EMC\n"`.
  - `MoveTo, SetRGB, SetExtGState, BeginMarked, LineTo, Stroke` emits `rg`, `gs` and `BMC` inside a path (ISO 32000-2 Fig. 9 violation).
  - `sh` with a path open is accepted (Draw refuses the same case).
  - The `pending` and `maxDep` fields are never read.
- **Status:** CONFIRMED (executed)
- **Direction:** Track marked-content depth and check it in `Bytes()`, and refuse non-path operators while a path is open.

#### C129 — Inline pages silently dropped
- **Sev:** Low
- **Where:** pages.go:125-137
- **Issue:** The C16 fix turned a panic into silent loss: extracting an inline page returns 0 pages plus a stray null, and the regression test asserts only "no panic".
- **Status:** CONFIRMED (traced)
- **Direction:** Promote the direct dict to an indirect object and copy it.

#### C130 — Uncoloured tiling patterns
- **Sev:** Low
- **Where:** content/builder.go:165-171; pattern.go:141-145
- **Issue:** `SetsColor` ignores `ri`, `sh` and non-stencil image `Do`, all excluded by ISO 32000-2 8.6.8. The doc says "undefined" where the spec says the operators are ignored.
- **Status:** CONFIRMED (traced)
- **Direction:** Add these to the watched set and fix the wording.

#### C131 — NaN/Inf accepted until Write
- **Sev:** Low
- **Where:** page_add.go (size, Opacity), annotation.go (Rect), form.go (BBox, Matrix), shading.go (coords, stops)
- **Scenario:** `AddPage` with Width +Inf, a NaN link Rect, `Opacity(NaN,1)` and a NaN `LinearGradient` all return nil. Write later fails, after the document has been mutated. `checkFinite` exists but is used only by destinations and patterns.
- **Status:** CONFIRMED (executed)
- **Direction:** Run every float through `checkFinite` at the call.

#### C132 — `checkURI` bypass and raw non-ASCII URIs
- **Sev:** Low
- **Where:** annotation.go:125, 146-159
- **Scenario:** `"java\tscript:alert(1)"` and `" javascript:alert(1)"` are accepted (lead re-run); browsers strip tabs and leading space. The code claims protection for untrusted input such as web pages. UTF-8 URIs are written raw, although ISO 32000-2 12.6.4.8 requires 7-bit ASCII.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Normalise per the WHATWG URL parser before checking the scheme, allowlist schemes, and percent-encode.

#### C133 — Soft-mask builders accept a non-group form
- **Sev:** Low
- **Where:** softmask.go:22-64; form.go:81-94
- **Issue:** Table 142 requires `/G` to be a transparency group, and a luminosity mask needs `/CS`. `Form.Group` defaults false. The test comment at outline_softmask_test.go:234 knows this; the API does not check it.
- **Status:** CONFIRMED (traced)
- **Direction:** Validate, or document the requirement.

#### C134 — ShadingPattern space
- **Sev:** Low
- **Where:** shading.go:34-38, 88-101
- **Issue:** Pattern coordinates are fixed to the default space of the page or form; `cm` is ignored and there is no `/Matrix` parameter. The LinearGradient doc is true only for `sh`.
- **Status:** CONFIRMED (traced)
- **Direction:** Add a Matrix parameter and document the anchoring.

#### C136 — Destinations accept any object number
- **Sev:** Low
- **Where:** outline.go:73-75; annotation.go:115-120; structure.go:300-302
- **Scenario:** An outline pointing at object 1 (the catalog) or at 9999 (nonexistent) is accepted. The structure writer puts `/StructParents` on whatever the reference resolves to.
- **Status:** CONFIRMED (executed)
- **Direction:** Require membership in `PageList()`.

### 3.E PDF/A validator correctness

**Expected:** a conforming file validates clean at the level it declares, including levels pdf0 maps from others (U→B). Each rule reads resolved values, identical inputs give identical findings, and a finding is never produced by pdf0's own builder output.

#### C33 — No PDF/A-2u/3u; `LevelFor(U)` guarantees a false positive
- **Sev:** High
- **Where:** pdfa/pdfa.go:31-54 (Level enum), 2235-2244 (requires conformance exactly "B"); pdfa/create.go:208-249 (`LevelFor` documents U→B as "the part of the claim this package can actually verify"); save.go:48
- **Scenario:**
  - A 2b skeleton with the XMP conformance changed to U, validated at `LevelFor("2","U")` → exactly `[PDF/A-2b 6.6.4] pdfaid:conformance must be B, got "U"` (lead re-run).
  - `doc.Save` refuses every 2u/3u document, so read-modify-Save of real Factur-X/ZUGFeRD files (commonly 3u) can never succeed.
  - A 2a document validated at 2b fails with "got A", although Level A includes Level B.
  - pdfa_test.go:1315 already runs the PDF_A-2u suite with `checkPassFP=false`, which acknowledges the problem.
  - The "u" Unicode requirement is never checked at any level.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Add PDFA2u/3u, reusing `checkLevelAToUnicode`, or at minimum accept A/U/B at the B check. Carry the claimed letter through the run.

#### C34 — `DeclaredLevel` is a lossy second `LevelFor`
- **Sev:** High
- **Where:** pdfa/declared.go:10-35; embedded.go:53-61; pdfa/final_rules.go:536-549
- **Scenario:** A plain PDF/A-4 file embedding a 2u file → `[PDF/A-4 6.9] object 7: an embedded PDF file is not compliant with PDF/A`. The control, embedding the unmodified 2b file, is clean (lead re-run). This affects any embedded 1a/2a/3a/2u/3u file.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Delete `DeclaredLevel` and use `LevelFor(part, conformance)` after fixing C33.

#### C35 — 1b Info↔XMP comparison never XML-unescapes
- **Sev:** High
- **Where:** pdfa/pdfa.go:2983-3017, 3042-3071; internal/core/color.go:19-42 (`ExtractXMPValue`)
- **Scenario:** `NewPDFADocumentWithInfo(PDFA1b, "Smith & Sons", "")` correctly writes `Smith &amp; Sons`. With Info `/Title (Smith & Sons)` added → `[PDF/A-1b 6.7.3] Info /Title ("Smith & Sons") does not match XMP dc:title ("Smith &amp; Sons")` (lead re-run). Numeric character references fail the same way. `extractXMPListValue` also takes the first `rdf:li`, not the x-default one. The corpus has no such file, so FP=0 does not cover it.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Read through the encoding/xml-based parser in pdfa/xmp.go. Corpus risk is low, because the fix only removes mismatches.

#### C36 — Rule inputs type-asserted without resolving (prior C18 reopened)
- **Sev:** High
- **Where (pdfa/pdfa.go):**
  - 527, 535: metadata `/Type`, `/Subtype`
  - 701: OI `/S`; 831: OI `/N`
  - 1187-1221: `hasFilter` / `getNonStandardFilter`
  - 1890: NeedAppearances
  - 2346, 2360, 2376: 1b SMask/BM/CA
  - 2632: catalog `/Version`
  - 2779: TR2; 2826: BM; 2861: HalftoneType
  - 3303: image SMask
  - 3618, 3640, 3661: OC config names and `/Order` refs
  - 3777: `isZeroAreaRect`
  - 4175: ICC `/N`
- **Scenario (2b, lead re-run):**
  - Indirect `/Type /Metadata` → FP "metadata stream must have /Type /Metadata".
  - Indirect `/TR2 /Default` → FP "/TR2 must be /Default".
  - Indirect NeedAppearances `true` → 0 findings (the direct control is caught).
  - Indirect `/Filter /LZWDecode` → 0 findings.
  - Indirect `/BM /Bogus` → 0 findings.
- C18 itself named TR2/BM and the metadata Type/Subtype, and all are still unfixed, although the memory index records the audit as fully worked.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Route every rule input through `Resolve*`, and add a lint test that flags `.Get(...).(object.` in `pdfa/`. The FP fixes carry no corpus risk; the evasion fixes add findings only where a value is indirect.

#### C64 — PDFA4E/PDFA4F never reach the variant gates
- **Sev:** Medium
- **Where:** pdfa/pdfa4_variants.go:30-80; pdfa/pdfa4_ef.go:176-230; pdfa/pdfa.go:1544, 1956, 3412-3416; pdfa/final_rules.go:504
- **Issue:** `ValidateVariant4View` runs the base pipeline at `BaseB()` = PDFA4. The following are therefore decided by the file's own `pdfaid:conformance`, never by the requested level:
  - the 4f EmbeddedFiles requirement
  - the 4e 3D Subtype rule
  - the annotation and action relaxations
  - AF
  - the embedded-PDF/A exemption

  The `effectiveVariant` comment and docs/pdfa.md say PDFA4F "applies 4f whatever the document says".
- **Scenario:** A plain PDF/A-4 skeleton validated at PDFA4F gets only "declares no pdfaid:conformance", not "must contain /EmbeddedFiles" (lead re-run). `TestTheVariantRequirementsApplyFromTheLevelAsWellAsTheFile` passes only because it calls the check directly.
- **Status:** CONFIRMED (executed for 4f; traced for 4e)
- **Direction:** Carry the requested variant on the Run, gate on `effectiveVariant`, and test through `ValidatePDFABytes`.

#### C65 — ICCBased overprint rule ignores q/Q and order
- **Sev:** Medium
- **Where:** pdfa/pdfa.go:4755-4787, 4878-4953 (`checkICCBasedUsageRules`; its doc also claims the profile-identity rule, which lives elsewhere)
- **Scenario:** `q /GSopm gs Q /GSop gs /CS0 cs … f`, with OPM 1 only inside q/Q → `6.2.4.2 overprint mode must not be 1…` (lead re-run). OPM is 0 at the fill, and form XObjects are not examined at all.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Evaluate on the executed-content walk with a graphics-state stack.

#### C66 — Separation tint-consistency finding is nondeterministic
- **Sev:** Medium
- **Where:** pdfa/pdfa.go:4252-4284, 4361-4384 (first definition taken from `range doc.Objects`)
- **Scenario:** Three undrawn forms define `/Spot` with different tints → 50 validations of one parsed document give **3 distinct reports** (lead re-run). This breaks doc.go's determinism promise. It also scans undrawn content, outside the executed-content model.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Iterate `SortedObjectNums()`, and restrict to executed content (check that the corpus stays FP=0).

#### C67 — Type3 widths compared in the wrong space
- **Sev:** Medium
- **Where:** pdfa/fonts.go:976-983
- **Issue:** ISO 32000-1 Table 112 puts Type3 `/Widths` in glyph space. The code scales the d0/d1 width by `fm[0]*1000`, so the two sides match only when fm[0] is 0.001.
- **Scenario:** FontMatrix `[1/2048 0 0 1/2048 0 0]` (matplotlib), `/Widths [1200]`, `1200 0 d0` → `6.2.11.5 width information … inconsistent` (lead re-run). A rotated FontMatrix (fm[0]=0) always fails.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Compare in glyph space.

#### C68 — CIDFontType2 existence ignores CIDToGIDMap
- **Sev:** Medium
- **Where:** pdfa/fonts.go:917-918, 1106-1120 (vs `cidGlyphWidth` at :1087, which maps)
- **Scenario:** A map sending CID 0x9000 to GID 36 → "does not define a glyph referenced for rendering (CID 36864)".
- **Status:** CONFIRMED (executed)
- **Direction:** Resolve the GID once per iteration and use it for every lookup.

#### C69 — Byte-level checks trust caller bytes against stale Offsets
- **Sev:** Medium
- **Where:** pdfa_api.go:120-160; pdfa/filestructure.go:610-633, 900-921, 1294-1343
- **Scenario:** Read a clean file, set `/Lang`, Write, then `ValidatePDFABytes(d, 2b, newBytes)` → false `6.1.6 object 4: hexadecimal string …`; re-reading the same bytes gives 0 findings (lead re-run). With shorter raw bytes, the unguarded `raw[int(off):…]` panics and all 8 byte checks collapse into one "internal". A built-in-memory Document (Offsets nil) silently skips the offset checks.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Derive offsets from `rawData` inside the byte pass, bounds-guard, and wrap each check separately. Better still, keep the raw bytes on the Document (§4 T3).

#### C70 — `>>stream` false Length mismatch
- **Sev:** Medium
- **Where:** pdfa/filestructure.go:1307 (`allDelimitedKeywords(raw,"stream",true)` requires leading whitespace), 1387-1421
- **Scenario:** `/Length 542  >>stream` (legal) → `6.1.7.1 Length does not match`, because the scan picks the next object's `stream` keyword. `checkStreamKeywordFormat` finds the right keyword, so the two checks disagree.
- **Status:** CONFIRMED (executed)
- **Direction:** Accept `>` as a delimiter, and bound the search to the object.

#### C71 — Any "xref" word is a cross-reference table
- **Sev:** Medium
- **Where:** pdfa/filestructure.go:446-458
- **Scenario:** An object containing `(the xref table)` → `6.1.4 xref keyword is not followed by a single EOL` at 1b, 2b and 4.
- **Status:** CONFIRMED (executed)
- **Direction:** Check only the xref sections Read located.

#### C72 — Builder accepts XML-illegal metadata
- **Sev:** Medium
- **Where:** pdfa/create.go:305-330 (`XMLEscape` drops only C0 controls)
- **Scenario:** Title `"bad\xffutf8"` or one containing U+FFFE → no error from the constructor, then "XMP packet is not well-formed XML" (plus "not UTF-8" at A-4). The docs say constructor output passes ValidatePDFA. Prior C19 is otherwise fixed: all 9 levels validate clean with ordinary titles.
- **Status:** CONFIRMED (executed)
- **Direction:** Return an error, or sanitise.

#### C137 — Embedded PDF/A check goes one level deep
- **Sev:** Low
- **Where:** embedded.go:59 (depth fixed at 1); pdfa/final_rules.go:498 (returns nothing at depth > 0)
- **Scenario:** A-4 embeds A-4, which embeds a non-PDF → passes.
- **Status:** CONFIRMED (traced)
- **Direction:** Pass depth+1 with a cap, and keep the file-type check at every depth.

#### C138 — `object -1` in findings
- **Sev:** Low
- **Where:** pdfa/pdfa.go:1431-1445 (`collectFontsFromResources` keys direct fonts as `-len(fonts)-1`), used by 1280 and 2676
- **Scenario:** A direct unembedded font dict → `[6.2.11.4.1] object -1: font must have a /FontDescriptor`, reported once per page that reaches it (lead re-run).
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Report 0 and dedup by pointer.

#### C139 — Invalid and zero Level values
- **Sev:** Low
- **Where:** pdfa/pdfa.go:31-54; pdfx/pdfx.go (invalid → X-4)
- **Scenario:** `ValidatePDFABytes(doc, pdfa.Level(99), raw)` → `[PDFALevel(99) 6.6.4] pdfaid:part must be , got 2`. `Level(0)` is PDFA1b, so an unset config field validates at the oldest part. `SkeletonOptions{}` builds 1b, and `pdfx.Level(0)` is X-4.
- **Status:** CONFIRMED (executed)
- **Direction:** Reject invalid levels at the boundary with a single checker finding. Consider a zero value that means "unset".

#### C140 — Output-intent duplication and gaps
- **Sev:** Low
- **Where:** pdfa/pdfa.go:719-731 vs 768-782; 789-928; 4149-4239 (every stream with an integer `/N` is treated as an ICC profile)
- **Issue:**
  - A 2b skeleton at 1b reports both `6.2.2 … ICC profile version 4.3` and `6.2.3 object 5: ICCBased profile version 4.x`, with no ICCBased colour space in the file (lead re-run).
  - A GTS_PDFA1 intent with neither DestOutputProfile nor OutputConditionIdentifier gets two findings.
  - `checkOutputIntentProfile` never examines A-4 page-level profiles.
  - Clause IDs disagree within one check (6.2.4/6.2.3 vs 6.2.4.2/6.2.3.2).
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Find profiles via ICCBased arrays, include page-level intents, and use one clause helper. Check that the corpus stays FP=0.

#### C141 — pdfaid read by string scraping
- **Sev:** Low
- **Where:** pdfa/pdfa.go:2194-2204, 2282-2292; pdfa/pdfa_levela.go:101-121; pdfa/final_rules.go:558-572; core `ExtractXMPValue`
- **Scenario:** `<!-- was <pdfaid:conformance>B</pdfaid:conformance> -->` placed before the real value → `[2a] must be A, got "B"`. Whitespace around `=` in `xmlns:pdfaid = "…"`, attribute-carrying elements and non-canonical prefixes are misread in the same way. The conformance flag then gates the relaxations.
- **Status:** CONFIRMED (executed for Level A); others traced
- **Direction:** Use `parseXMPProperties`, and scrape only above the size cap.

#### C142 — Inline `/Intent` reported twice
- **Sev:** Low
- **Where:** pdfa/filestructure.go:977-997; pdfa/final_rules.go:122-131
- **Issue:** `BI … /Intent /Foo ID … EI` → two 6.2.6 findings with different wording. There are three inline-image dictionary parsers (`inlineImageDictValue`, `parseInlineImageFilter`, `parseInlineDictEntries`).
- **Status:** CONFIRMED (executed)
- **Direction:** Parse once, and drop `checkInlineImageIntent`.

#### C143 — Tiling-pattern violations carry a loop index
- **Sev:** Low
- **Where:** pdfa/content_operators.go:223 (passes `i`; the XObject branch above uses `resolveObjNum`)
- **Status:** CONFIRMED (traced)
- **Direction:** Use `resolveObjNum`.

### 3.F Shared engine (`internal/core`) correctness

#### C46 — `View.Content` turns every decode failure into a silent nil
- **Sev:** High
- **Where:** internal/core/view.go:362-386 (no `err != nil` arm, despite a docstring saying "both refusals are reported"); internal/core/filters.go:378-422 (Flate, LZW and ASCIIHex only; any zlib error discards everything)
- **Scenario (content `1 0 0 rg …`, PDFA2b):** unfiltered, it correctly reports 6.2.4.3 DeviceRGB. Each of the following makes the finding vanish, with **no trip**:
  - `/Filter /ASCII85Decode` (legal in every PDF/A part; lead re-run)
  - Flate with the Adler-32 trimmed
  - 110 MB of decoded content (at 70 MB it correctly trips `content-stream-size`)

  `MetadataContent` is the same, and Ghostscript output commonly uses `[/ASCII85Decode /FlateDecode]`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:**
  - Return data plus a reason, and record a trip on every error.
  - Implement ASCII85Decode and RunLengthDecode (RunLength needs an output cap).
  - Consider keeping partial Flate output with a trip.

#### C73 — Device-colour false positives
- **Sev:** Medium
- **Where:** internal/core/color.go:505-512, 628-632; internal/core/devicecolour.go:147-152, 296-305
- **Issue:**
  - `sh` counts as painting, but it paints in the shading's own space.
  - Forms, tiling patterns and CharProcs are assumed to start in DeviceGray, but they inherit the caller's colour (ISO 32000 8.10.1).
  - `sc` on the default space counts as setting a colour, so real gray use is missed.
- **Scenario:**
  - `/Sh0 sh` with a Lab shading → 6.2.4.3 DeviceGray.
  - `/CS0 cs 50 0 0 sc /Fm0 Do` (Lab), where the form is `0 0 10 10 re f` → DeviceGray.
- **Status:** CONFIRMED (executed)
- **Direction:** Drop `sh` from the paint set, propagate "uses the inherited colour" to the caller, and track stroke and fill separately.

#### C74 — Text render mode not restored by `Q`
- **Sev:** Medium
- **Where:** internal/core/fontuse.go:114-137, 474-515
- **Scenario:** `BT /F1 12 Tf (y) Tj ET` with a non-embedded Helvetica reports 6.2.11.4.1. Prefixing `q 3 Tr Q` removes it (lead re-run). OCR layers commonly wrap `3 Tr` in q/Q, so this is an evasion path for real files.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Emit q/Q events, replay them with a font/mode stack, and pass the caller's text state into forms.

#### C75 — "usecmap" substring refuses the CMap silently
- **Sev:** Medium
- **Where:** internal/core/cmap.go:233; callers pdfa/fonts.go:854-862, 1351-1360; pdfua/pdfua.go:852
- **Scenario:** Adding `% this CMap does not usecmap anything` removes a real "6.3.5 CIDSet does not list all CIDs" finding with no trip. It also adds a bogus "6.3.3.3 references CMap /ProcSet" from pdfa's own usecmap parser.
- **Status:** CONFIRMED (executed)
- **Direction:** Match `usecmap` only as an operator token, and record a trip when an embedded CMap is refused.

#### C76 — `HasForbiddenUnicodeTargets` misaligns on array-form bfrange
- **Sev:** Medium
- **Where:** internal/core/fontuse.go:242-292
- **Scenario:** `<0001> <0002> [<0041> <0042>]` followed by `<0000> <0000> <0020>` → false A-4 6.2.10.7. The control without the array line is clean.
- **Status:** CONFIRMED (executed)
- **Direction:** Reuse `bfItems`, which `ParseToUnicodeRunes` already parses correctly.

#### C77 — No PDFDocEncoding
- **Sev:** Medium
- **Where:** internal/core/queries.go:325-348 (used by pdfa 6.7.3, Lang, sign and facturx)
- **Scenario:** Info `/Title (Caf\351)` with XMP "Café" at 1b → false `6.7.3 … ("Caf\xe9") does not match`.
- **Status:** CONFIRMED (executed)
- **Direction:** Add the Annex D.3 table.

#### C78 — Embedded CMap parsing is line-based
- **Sev:** Medium
- **Where:** internal/core/cmap.go:273-324
- **Scenario:** `<00> <7F> 0 <80> <FF> 200` on one line, or CR-only line endings → one range 00–7F mapped to CID 200. All later ranges are lost, and single codes are keyed without their byte width. The codespace and ToUnicode parsers were already fixed for exactly this.
- **Status:** CONFIRMED (traced)
- **Direction:** Parse a flat token stream, as `bfItems` does.

#### C149 — `ScanStreamForDeviceOps` has its own inline-image skipper, which ignores `/L`
- **Sev:** Low
- **Where:** internal/core/color.go:634-724
- **Issue:** This is a sibling of prior C35/C25. Sample bytes after a stray ` EI ` are read as operators, so `k` or `g` can produce device-colour FPs.
- **Status:** PLAUSIBLE
- **Direction:** Reuse `SkipInlineImage`.

#### C150 — `TokenizeContent` decoding drift
- **Sev:** Low
- **Where:** internal/core/content.go:131-208
- **Issue:** No backslash-EOL continuation, non-hex bytes become zero nibbles, and there is no token cap. PDF/UA and PDF/A can therefore see different code sequences for the same Identity-H string.
- **Status:** CONFIRMED (traced)
- **Direction:** Reuse the shared literal and hex decoders.

#### C151 — Indirect `/Filter`/`/DecodeParms`
- **Sev:** Low
- **Where:** internal/core/filters.go:146-181, 344-376; internal/core/queries.go:642
- **Issue:** An indirect `/DecodeParms` is legal, but the predictor is silently skipped and the content decodes to garbage. `StreamFiltersSupported` still says the stream is fine.
- **Status:** CONFIRMED (traced)
- **Direction:** Resolve both before dispatch.

#### C152 — ICC `/N` read only if direct
- **Sev:** Low
- **Where:** internal/core/color.go:418-420
- **Issue:** Group `/CS` coverage is lost, which leads to a device-colour FP.
- **Status:** CONFIRMED (traced)
- **Direction:** Use `ResolveInt`.

#### C153 — Type3 handling and dead code in core
- **Sev:** Low
- **Where:** internal/core/devicecolour.go:279-312; internal/core/fontuse.go:141-183
- **Issue:**
  - Every Type3 font in `/Font` is scanned whether or not it is used.
  - CharProc XObjects are never counted, and CharProc results bypass Default* masking.
  - Font usage never descends into CharProcs.
  - Dead code: `IsIdentityEncoding` and `pdfa.scanContentsForDeviceOps` (pdfa/pdfa.go:4101) have no callers.
- **Status:** CONFIRMED (traced)
- **Direction:** Treat CharProcs as containers invoked by Tf plus a show operator, and delete the dead code.

### 3.G Other validators (PDF/UA, PDF/X, PDF/VT, PDF/R, DPart, Factur-X)

#### C43 — Empty or undecodable invoice XML validates clean
- **Sev:** High
- **Where:** facturx/facturx.go:247-257, 313; facturx/orderx.go:180-192, 226; facturx_api.go:20-27
- **Issue:** `res.XML = doc.Content(st)` is nil on a decode failure (C46), and the rule engine runs only `if len(res.XML) > 0`. The container run's own trip recorder is never flushed.
- **Scenario:**
  - corpus_EN16931_Einfach.pdf with the invoice stream emptied or given garbage Flate → **0 violations, XML len 0** (lead re-run).
  - `NewPDFADocument(PDFA3b)` + `EmbedFacturX(doc, nil, …)` → no error, then 0 violations.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** A finding when the attachment is present but empty or undecodable, flush the container's trips, and have Embed reject empty or non-XML input.

#### C44 — EmbedFacturX discards the document's attachments and metadata
- **Sev:** High
- **Where:** facturx/write.go:165-188
- **Issue:**
  - `names.Set("EmbeddedFiles", …)` replaces the existing tree.
  - `/AF` is appended without dedup.
  - XMP is replaced wholesale, with the conformance letter hard-coded to B, so 3a/3u inputs are downgraded.
- **Scenario:** A corpus invoice with one extra attachment: AF goes 1→2, name-tree files 2→1, and CreatorTool and CreateDate disappear (lead re-run). ValidateFacturX then reports 0 violations while validating the **old** `factur-x.xml`, because `FindAttachment` takes the first match. Duplicate `factur-x.xml` entries are not flagged.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Merge, or refuse when an invoice already exists. Preserve the conformance letter, and flag duplicates in the validator.

#### C63 — Undecryptable documents validated as if plaintext
- **Sev:** Medium
- **Where:** document.go:325-333; internal/crypt/crypt.go:85-110 (`return nil, nil` for non-Standard handlers, R5, or when the key cannot be derived); `View.DecryptFailures` is read by no validator
- **Issue:** PDF/UA, the one standard here that permits encryption, asserts 5, 7.1 and 7.2 (no pdfuaid, no dc:title, invalid `/Lang`) on ciphertext, with no checker finding.
- **Status:** CONFIRMED (traced; grep shows zero reads of `DecryptFailures`/`Locked` in pdfua, dpart and facturx)
- **Direction:** One `limit` finding at run start when content is undecrypted, and have string- and stream-based checks decline.

#### C80 — `checkFigureAlt` keys on raw `/S`
- **Sev:** Medium
- **Where:** pdfua/pdfua.go:1321
- **Issue:** This is the unfixed sibling of prior C29, and it contradicts docs/pdfua.md.
- **Scenario:** RoleMap `/Img /Figure` with an `/Img` element lacking `/Alt` → 0 findings; the same element as `/Figure` → 1.
- **Status:** CONFIRMED (executed)
- **Direction:** Use `StdType`.

#### C81 — `checkUAAnnotStructType` keys on the parent's raw `/S`
- **Sev:** Medium
- **Where:** pdfua/pdfua_content.go:329-368
- **Scenario:** `/MyLink → /Link` with an OBJR to a Link annotation → "7.18.1 … nested in a <MyLink> element, expected <Link>". The same file using `/Link` is clean.
- **Status:** CONFIRMED (executed)
- **Direction:** Resolve through the RoleMap.

#### C82 — PDF/X-1a:2001 identification
- **Sev:** Medium
- **Where:** pdfx/pdfx.go:60-72, 289-316
- **Issue:** X-1a:2001 declares `GTS_PDFXVersion (PDF/X-1:2001)` plus `GTS_PDFXConformance (PDF/X-1a:2001)`. pdf0 never reads the conformance key and requires the "PDF/X-1a" prefix on the version.
- **Scenario:** A correctly identified X-1a:2001 file → FP "does not identify PDF/X-1a".
- **Status:** CONFIRMED (executed; the spec reading of ISO 15930-1 is from knowledge)
- **Direction:** Accept `GTS_PDFXConformance`.

#### C83 — Rules that iterate `doc.Objects`
- **Sev:** Medium
- **Where:** pdfx/pdfx.go:151-191; pdfua/pdfua.go:459-516, 1051-1068; pdfua/pdfua_content.go:344
- **Issue:** Direct dictionaries are invisible, and orphan objects are judged.
- **Scenario:** A Link with inline `/A << /S /JavaScript … >>`, the common producer shape, passes ValidatePDFX. Only a top-level JS object is caught.
- **Status:** CONFIRMED (executed for pdfx; traced for UA)
- **Direction:** Use reachable walks that include direct dicts. pdfua's `walkAllDicts` already exists.

#### C85 — PDF/X level gating
- **Sev:** Medium
- **Where:** pdfx/pdfx.go:220-240, 289-306, 322-356, 371-383
- **Issue:**
  - X-1a and X-3 require an embedded DestOutputProfile, where a registered condition should suffice.
  - The X-1a CMYK/gray/spot-only rule is not enforced (RGB with an RGB intent passes).
  - X-6 demands Info `/Trapped` rather than XMP.
  - X-4 accepts Info-only identification.
  - An invalid level silently validates as X-4.
- **Status:** PLAUSIBLE (from knowledge of ISO 15930; no corpus)
- **Direction:** Per-level rule tables, or document the actual coverage.

#### C144 — Nine validators panic on a nil `*Document`
- **Sev:** Low
- **Where:** limits_report.go:192-199 (`doc.valCache`); facturx_api.go:21
- **Issue:** `ValidatePDFA(nil)` returns a limit finding, and its comment argues this is what a caller reaches for after a failed Read. PDFUA, PDFUA2, PDFX, PDFVT, PDFVT2, PDFR, DParts, FacturX and OrderX panic before the recover boundary.
- **Status:** CONFIRMED (executed)
- **Direction:** Handle nil in `beginRunCancel` and `facturxRun`.

#### C145 — Wrong part in `Error()`
- **Sev:** Low
- **Where:** pdfua/pdfua.go:24-29; pdfvt/pdfvt.go:34-39
- **Issue:** UA-2 findings print "PDF/UA-1" (documented). VT-2 findings print "PDF/VT-1" (undocumented). VT-2 base findings are prefixed "pdfx-4/", although VT-2's base is X-5.
- **Status:** CONFIRMED (traced)
- **Direction:** Carry the part on the finding.

#### C146 — PDF/R claims and identification
- **Sev:** Low
- **Where:** pdfr/pdfr.go:9-20; pdfr_api.go:50-72
- **Issue:**
  - "No transparency" is listed as covered, but no such check exists.
  - Identification is any "pdfr" or "pdf/r" substring in the XMP, so a producer string "Acme PDF/Reader" matches.
  - An unparseable version passes (`ok && maj != 2`); UA-2 has the same pattern.
  - Inline-image filters are not checked.
- **Status:** CONFIRMED (traced)
- **Direction:** Parse the XMP, fail closed on version, and fix the header.

#### C147 — PDF/X device-colour cache poisoned mid-cycle
- **Sev:** Low
- **Where:** pdfx/pdfx_color.go:240-273
- **Scenario:** Form A draws RGB and invokes B; B invokes A. B's result is cached without A's colours, so a page drawing only B misses the RGB.
- **Status:** PLAUSIBLE (traced)
- **Direction:** Don't cache results computed while an ancestor is in progress.

#### C148 — Factur-X/Order-X asymmetries and composition by message substring
- **Sev:** Low
- **Where:** facturx/*.go; pdfvt
- **Issue:**
  - Order-X doesn't check fx:Version, and there is no Order-X embed.
  - `facturx.XMPPacket(…, ORDER, …)` emits the invoice namespace.
  - Attachment names are matched case-insensitively.
  - VT-2 drops the reference-XObject rule by matching the message text "reference XObjects", and Factur-X drops 6.6.4 by matching "pdfaid:conformance". This is the prior C39 pattern, and it is brittle to rewording.
- **Status:** CONFIRMED (traced)
- **Direction:** Key composition on rule IDs or typed flags.

### 3.H Text and image extraction correctness

#### C86 — ExtractText uses the first-rune whitespace probe as its ToUnicode map
- **Sev:** Medium
- **Where:** text.go:192; internal/core/queries.go:353, 608-617
- **Scenario:** Codes mapped to "fi", 😀 and an array bfrange X/Y/Z, plus "A", extract as `f�A` instead of `fi😀AXYZ`. `core.ParseToUnicodeRunes`, which is correct, is already used by Level A.
- **Status:** CONFIRMED (executed)
- **Direction:** Switch to `ParseToUnicodeRunes`.

#### C87 — The cycle guard suppresses repeats
- **Sev:** Medium
- **Where:** text.go:147-157 (`seen[st] = true` is never cleared)
- **Scenario:** `/X1 Do /X1 Do /X1 Do`, with the form showing "A", extracts "A" once.
- **Status:** CONFIRMED (executed)
- **Direction:** `delete(seen, st)` after the recursion, keeping the depth cap.

#### C88 — Type0 code length hard-coded to 2 bytes
- **Sev:** Medium
- **Where:** text.go:188-191, 283-287; pdfa/pdfa_levela_content.go:177-193 (the Level A PUA scan)
- **Issue:** Mixed 1/2-byte embedded CMaps (Shift-JIS, EUC) are split wrongly, and predefined non-Identity CMaps without ToUnicode extract nothing. pdfa/fonts.go already uses `core.LoadCMap(...).Decode` correctly.
- **Status:** PLAUSIBLE (path unambiguous; no fixture font built)
- **Direction:** Cut codes with `LoadCMap(...).Decode`.

(C54, missing recover, and C79, no Run, are in §3.B.)

### 3.I Fonts, HTML→PDF, CLI and tools

**Expected:**
- Every path that writes glyphs into a content stream agrees with the font dictionary it references.
- Extraction recovers the text that was drawn.
- htmlpdf draws what forme laid out.
- The CLI keeps secrets out of `ps`.

#### C24 — Shape/Draw write the GID where a CID-keyed CFF font needs the CID
- **Sev:** High
- **Where:** fonts/shape.go:124 (`spansFromGlyphs`) and :180 (`Draw`) both write `byte(g.GID>>8), byte(g.GID)`. `Encode`, `/W`, `/CIDSet` and `/ToUnicode` all use `f.GlyphCode(gid)`, the CID.
- **Scenario:** NotoSansJP with "ｱ日本":
  - `Encode` writes code 59158 and extracts "ｱ日本".
  - `Shape` and `Draw` write the GID bytes, and pdf0's own `ExtractText` returns `""` (lead re-run).
  - PDF/A-4 validation reports 6.2.10.4.1 (CID 15435 undefined) and 6.2.10.4.2 (incomplete CIDSet).
  - Ghostscript draws .notdef boxes.
  - htmlpdf uses DrawShaped (pdfout.go:396), so **every htmlpdf page with a CID-keyed CJK face** extracts `""` (lead re-run).
  - The only CJK test (fonts_embed_test.go:788) covers `Encode` alone, and skips without `make notocjk`.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** `f.GlyphCode(g.GID)` in both places. Add a test that draws through Shape and DrawShaped and round-trips ExtractText, run in CI with notocjk fetched.

#### C25 — ToUnicode is built from the font's cmap, not from what was drawn
- **Sev:** High
- **Where:** fonts/embed.go:549-582 (`toUnicodeCMap` inverts the cmap)
- **Issue:** Glyphs produced by shaping (conjuncts, ligatures, contextual forms) have no cmap entry, so they get no ToUnicode entry, and no glyph ever maps to more than one character.
- **Scenario:** fonts.NotoSans (lead re-run):
  - "office affluent" → "oﬃce aﬄuent"
  - "क्षत्रिय" → "य"
  - "नमस्ते" → "नमते"

  PDF/A-4 still passes. htmlpdf always shapes, so all HTML output is affected for these scripts.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Record glyph → cluster text while shaping and emit bfchar entries for multi-codepoint clusters. For glyphs reused in different clusters, use ActualText.

#### C26 — `@page` ignored by htmlpdf
- **Sev:** High
- **Where:** htmlpdf/pdfout.go:276. Its comment says Compose doesn't return the page; forme returns `Composed.Page`.
- **Scenario:** `@page{size:100mm 100mm;margin:0}` → laid out on 283.5 pt, written with an A4 MediaBox (`[0 0 595.28 841.89]`, lead re-run) and offset by the A4 margin.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** Write the sheet from `Composed.Page`.

#### C27 — Backend reshapes without layout's context
- **Sev:** High
- **Where:** htmlpdf/pdfout.go:396 (`DrawShaped` → `ShapeGlyphs`, not `layout.ShapedGlyphs(v)`). forme's docs warn that a backend doing this "gets that wrong silently".
- **Scenario:**
  - `font-variant-ligatures:none`: layout measured 6 glyphs for "office", but the backend draws the ffi ligature.
  - `font-kerning:none`: drawn advances 599/560/569 against measured 639/600/639.
  - Cross-element Arabic joining breaks the same way.
- **Status:** CONFIRMED (executed)
- **Direction:** Draw the glyphs layout returned.

#### C28 — Alpha and opacity dropped
- **Sev:** High
- **Where:** htmlpdf/pdfout.go:295, 315 (`SetRGB(R,G,B)`; `Color.A` never read; no ExtGState emitted)
- **Scenario:** `rgba(0,0,0,.05)` zebra striping becomes solid black under black text.
- **Status:** CONFIRMED (traced by the lead; executed by the reader)
- **Direction:** Emit `/ca`/`/CA` ExtGStates. Until then, refuse with an Error finding.

#### C110 — Embedded fonts uncompressed; ToUnicode spans the whole cmap
- **Sev:** Medium
- **Where:** fonts/embed.go:55-195, 549-582
- **Scenario:** Three CJK characters give a 1.92 MB PDF: a 1.68 MB subset program (forme's subsetter keeps 1.47 MB of CFF plus the full cmap) and 232 KB of ToUnicode. examples/text is 311 KB, and zlib takes it to 118 KB.
- **Status:** CONFIRMED (executed)
- **Direction:** Flate the program and ToUnicode streams, and limit ToUnicode to kept glyphs. The subsetter fix belongs to forme.

#### C111 — `ShapeWith` on a simple or standard face writes 2-byte codes
- **Sev:** Medium
- **Where:** fonts/shape.go:87 (lacks the `composite()` guard that `Shape` has)
- **Scenario:** Emits `[0 102]` plus bogus adjustments into a 1-byte font.
- **Status:** CONFIRMED (executed)
- **Direction:** Apply the same guard.

#### C112 — fsType embedding restrictions ignored
- **Sev:** Medium
- **Where:** fonts/embed.go:55
- **Issue:** Neither pdf0 nor forme reads OS/2 fsType, so a Restricted-License (0x0002) font is subsetted and embedded silently.
- **Status:** CONFIRMED (executed)
- **Direction:** Refuse 0x0002, and honour the no-subsetting (0x0100) and bitmap-only (0x0200) bits.

#### C113 — CLI passwords only on argv
- **Sev:** Medium
- **Where:** cmd/pdf0/commands.go:15, 36, 74, 95-96, 123, 142, 211
- **Issue:** Passwords are visible in `ps` and shell history, and there is no env, file or prompt alternative. This sits badly with the user's own "never materialise a secret" rule.
- **Status:** CONFIRMED (traced)
- **Direction:** Add `-password-file` or `PDF0_PASSWORD`, or a no-echo prompt on a TTY.

#### C114 — Vertical writing modes drawn horizontally
- **Sev:** Medium
- **Where:** htmlpdf/pdfout.go (`Sideways`, `Anticlockwise` and `Upright` are unread)
- **Status:** CONFIRMED (executed)
- **Direction:** Implement vertical writing, or refuse.

#### C155 — CLI rough edges
- **Sev:** Low
- **Where:** cmd/pdf0/commands.go
- **Issue:**
  - `pdf0 merge a.pdf b.pdf` silently overwrites a.pdf: output comes first, and a single input is accepted.
  - No command guards against overwriting, and decrypted plaintext is written 0644.
  - There is no stdin/stdout support; `-` is taken as a filename.
  - A wrong password still says "supply -password".
  - `repair` treats a failed re-read of its own output as "0 violations remain" (PLAUSIBLE).
- **Status:** CONFIRMED (executed) except as marked
- **Direction:** Refuse output == input, write 0600 when decrypting, and support `-`.

#### C156 — Dev tools
- **Sev:** Low
- **Where:** internal/cmd/corpustime; internal/cmd/corpusprobe; internal/cmd/rulecoverage; scripts/check-no-binaries.sh
- **Issue:**
  - `corpustime` nil-derefs on a missing file (the `os.Stat` error is ignored).
  - `corpusprobe` panics with `-workers -1` and reports nothing with 0.
  - `rulecoverage` reports 181/181 at every level, so it can no longer find a gap.
  - `check-no-binaries.sh` checks types in the index but sizes at HEAD, so a staged oversize file passes. It does catch a planted ELF.
- **Status:** CONFIRMED (executed)
- **Direction:** Fix each one individually.

#### C168 — htmlpdf is one page only and drops links, undocumented
- **Sev:** Low
- **Where:** htmlpdf/*; docs/htmlpdf.md
- **Status:** CONFIRMED (executed)
- **Direction:** Document both limits, or refuse multi-page input.

#### C169 — examples/flavored shares one Face across three documents
- **Sev:** Low
- **Where:** examples/flavored/main.go
- **Issue:** The `NotoSans` doc tells callers not to do this, and this is the example callers copy.
- **Status:** CONFIRMED (traced)
- **Direction:** One Face per document.

### 3.J Object model, syntax and concurrency

**Expected:**
- The value types are inert data.
- Reads are pure.
- The serializer's output always reparses to an equal model.
- An `Object` has one representation.

#### C45 — `Dictionary.Get` writes a lazily-built index on the read path
- **Sev:** High
- **Where:** object/object.go:92-113 (`buildIndex` assigns `d.index`/`d.indexLen` from `Get` once a dict has ≥ 64 keys). The promise is at doc.go:118 and runcache.go:13-17.
- **Scenario:** Take a document whose catalog has 70 keys (a big font dictionary or RoleMap works too). Re-read it from bytes so the dict is parser-produced, then call `ValidatePDFA(doc, PDFA2b)` from 8 goroutines. `-race` reports `DATA RACE` at object.go:99/100 against 106/107 (lead re-run). On weakly ordered CPUs a reader can see the map pointer before the map is filled. `TestValidateConcurrentSameDoc` misses it because its reference file has no dict with 64 or more keys.
- **Status:** CONFIRMED (executed with -race; lead re-run)
- **Direction:** Build the index eagerly in the parser (it already builds a local one at parser.go:297 and throws it away), or guard it with `atomic.Pointer`/`sync.Once`. Add a 64+-key dict to the concurrency test.

#### C98 — The lazy index goes stale on in-place edits
- **Sev:** Medium
- **Where:** object/object.go:67-80, 104-113 (invalidated only when `len(Keys)` changes)
- **Scenario:** On a 70-key dict, call `Get`, then sort Keys/Values in parallel. `Get("K00")` now returns 69. After `d.Keys[5]="Renamed"`, `Get("Renamed")` is nil. On a 10-key dict the same edits are correct, so behaviour flips silently at 64 keys.
- **Status:** CONFIRMED (executed)
- **Direction:** Verify `d.Keys[i]==key` on a hit and rebuild on a miss, or make the fields private.

#### C99 — Two representations of one Object
- **Sev:** Medium
- **Where:** object/object.go:31-205 (value-receiver `pdfObject`); syntax/serializer.go:65-99; object/compare.go
- **Scenario:**
  - `WriteObject(Array{object.Dictionary{…}})` → "unsupported object type: object.Dictionary".
  - `Array{&name}` → "unsupported object type: *object.Name".
  - `Equal(Dictionary{}, &Dictionary{})` → false.
  - Validators type-switch on `*Dictionary`, so a value dictionary is invisible to them.
- **Status:** CONFIRMED (executed)
- **Direction:** Pointer receivers on the composite types' marker method, so the compiler enforces one form.

#### C115 — The serializer writes what the parser rejects
- **Sev:** Low
- **Where:** syntax/serializer.go:90-96, 281-292 (the file comment promises round-trip)
- **Issue:** `IndirectObject{Number:-1}` becomes `-1 0 obj`, and a nested `*IndirectObject` becomes `[3 0 obj 1 endobj]`. The parser rejects both.
- **Status:** CONFIRMED (executed)
- **Direction:** Mirror the parser's rules in `WriteObject`.

#### C116 — Integer lookahead errors
- **Sev:** Low
- **Where:** syntax/parser.go:60-63, 162-173; callers objstm.go:157-160, 248-253
- **Scenario:**
  - `"5 <ZZ> "` fails with "invalid hex string", although `/Name <ZZ>` parses. In an object stream, a valid integer object followed by a malformed one is dropped, or fails the Read.
  - `Lexer().Position()` after parsing `5` from `"5 /Next 7"` is 7, past a still-buffered token.
- **Status:** CONFIRMED (executed)
- **Direction:** Fall back to the plain integer on a lookahead error, and expose the consumed offset.

#### C117 — Numeric range errors abort the object
- **Sev:** Low
- **Where:** syntax/parser.go:117-121, 208-213
- **Issue:** `9223372036854775808` and a 400-digit real are fatal to the enclosing object.
- **Status:** CONFIRMED (executed)
- **Direction:** Promote to Real, or clamp.

#### C118 — `object.Int` of an out-of-range real
- **Sev:** Low
- **Where:** object/object.go:250-258
- **Issue:** `Int(Real(1e300))` is MinInt64 on amd64 (implementation-defined). It is applied to untrusted `/Width`, `/Height`, `/BitsPerComponent` and `/Size` at images/imagecolor.go:175, images/imageextract.go:349 and internal/core/function.go:172.
- **Status:** CONFIRMED (executed); downstream impact PLAUSIBLE
- **Direction:** Saturate, or return 0 or an ok flag.

#### C119 — Misleading error on a >1 MiB whitespace gap
- **Sev:** Low
- **Where:** syntax/lexer.go:191-220, 484-513
- **Issue:** The DoS bound is right, but the error reads `unknown keyword "" at offset 1048584`.
- **Status:** CONFIRMED (executed)
- **Direction:** A distinct error.

#### C120 — `endstreamendobj`
- **Sev:** Low
- **Where:** syntax/parser.go:438, 473-480, 573-590
- **Issue:** Fails on both the trusted-Length path and the search path.
- **Status:** CONFIRMED (executed)
- **Direction:** Advance exactly `len("endstream")` once it is located.

#### C121 — `Delete` removes only the first duplicate
- **Sev:** Low
- **Where:** object/object.go:149-159
- **Issue:** On `{A:1, A:2}`, `Delete("A")` then `Get("A")` returns 2.
- **Status:** CONFIRMED (executed)
- **Direction:** Delete all occurrences.

#### C122 — Equality tolerances
- **Sev:** Low
- **Where:** object/compare.go
- **Issue:** Real-Real uses an absolute 1e-10 and Int-Real a relative tolerance (the prior C32 fix was half-applied):
  - `Equal(Real 1e-11, Real 0)` is true, but `Equal(Int 0, Real 1e-11)` is false.
  - `Equal(Real 1e20, Real 1e20+16384)` is false.
  - NaN ≠ NaN.
- **Status:** CONFIRMED (executed)
- **Direction:** One relative tolerance with an absolute floor.

#### C123 — `NewLexerFromReaderAt` with a negative size
- **Sev:** Low
- **Where:** syntax/lexer.go:104-114
- **Issue:** `makeslice` panics on a negative size, and a large size is allocated before anything is read.
- **Status:** CONFIRMED (executed)
- **Direction:** Return an error.

#### C124 — Mismatched Keys/Values, or nil
- **Sev:** Low
- **Where:** syntax/serializer.go:240-250; object/object.go:110-116
- **Issue:** `&Dictionary{Keys:{"A","B"},Values:{1}}` panics Write. `WriteDictionary(nil)` and `WriteIndirectObject(nil)` panic too.
- **Status:** CONFIRMED (executed)
- **Direction:** Return an error.

### 3.K Documentation, tests, CI and DX

#### C103 — The XMP RelaxNG guard never runs
- **Sev:** Medium
- **Where:** pdfa/xmp_rng_test.go:317-321 (`dir := "testdata/xmp-rng"` is resolved relative to `pdfa/`; the data is at the repo root)
- **Issue:** It has always skipped since the package split, in a fresh clone, the dev tree and CI's corpus job (lead re-run: `SKIP … vendored XMP RNG schemas not present`). docs/testing.md:15-16 says this tier "never skips". Run via an overlay pointing at `../testdata/xmp-rng`, it passes, so nothing is hidden yet.
- **Status:** CONFIRMED (executed; lead re-run)
- **Direction:** `filepath.Join("..","testdata","xmp-rng")`, and `t.Fatal` when committed data is missing.

#### C104 — Corpus ratchets pass over an empty corpus
- **Sev:** Medium
- **Where:** testdata_test.go:27-30, 80-90 (`marker=""`, so any existing dir counts as present); pdfa_test.go:1471-1560 and siblings (skip missing levels; no minimum file count)
- **Scenario:** `VERAPDF_CORPUS` set to a directory an interrupted `make corpus` left behind (only `.git`) → TestCorpus prints `pass=0 fail=0 falsePositives=0 missed=0` and PASSes; ParsesEntirely, LevelA and ConformanceSuites pass too. CI is protected by its ≥2800-PDF count (ci.yml:132-154); local runs are not.
- **Status:** CONFIRMED (executed)
- **Direction:** Use the `.ok` stamp as the marker, and fail when a suite sees zero files.

#### C105 — CI documentation is from July
- **Sev:** Medium
- **Where:** CONTRIBUTING.md:24-39; docs/testing.md:17, 278-299
- **Issue:** The docs say Go 1.25.x, five steps, and "no corpus in CI". The actual workflow runs Go 1.26.x with three jobs:
  - build-test, which also runs devtools, check-links, check-mermaid, check-no-binaries and four examples;
  - a corpus job, which fetches the corpora and counts files;
  - a fuzz job, which runs three targets for 60 s each.
- **Status:** CONFIRMED (traced)
- **Direction:** Rewrite both sections from ci.yml.

#### C106 — Package-doc examples don't compile
- **Sev:** Medium
- **Where:** doc.go:70, 112 (`pdf0.PDFA4`; `go doc . PDFA4` → no symbol, lead re-run)
- **Issue:** This is the first code a pkg.go.dev reader sees.
- **Status:** CONFIRMED (executed)
- **Direction:** `pdfa.PDFA4`. Turn the snippets into `Example` tests.

#### C107 — "Write refuses a Locked document" appears in four places and is false
- **Sev:** Medium
- **Where:** document.go:34-41 (`Encrypted` field comment); the `Document.Locked` godoc; docs/architecture.md:186-189 (flowchart); docs/troubleshooting.md:51-57 (contradicted by its own :74-78)
- **Issue:** Write returns nil, and the output is byte-identical to the input (748/748). SetEncryption on a Locked document does refuse. doc.go describes the passthrough correctly.
- **Status:** CONFIRMED (executed)
- **Direction:** Fix all four texts.

#### C108 — Contributor docs still describe the flat layout
- **Sev:** Medium
- **Where:** README.md:258-268; CONTRIBUTING.md:72-73; docs/pdfa.md:14-30, 67; docs/validators.md:166, 213-229; docs/limits.md; docs/signing.md; docs/encryption.md; docs/xmp.md
- **Issue:**
  - The README table names 19 root files that have moved. Three exist nowhere: `crypt_encrypt.go`, `order_x.go` and `pdfa_create.go`.
  - CONTRIBUTING's rule recipe gives `func(*Document, pdfa.Level)` and the `checks` slice in `ValidatePDFABytes`. The real signature is `func(core.View, Level)`, in `pdfa.ValidateView` (pdfa/pdfa.go:198).
  - pdfa.md calls its snippet "verbatim", with the wrong signature, and gives pdfa.go as "~6,600" lines (it is 5,141).
  - Vanished identifiers:
    - `limitRecorder`
    - `sortViolations`
    - `maxCmapFormat4Work`
    - `decodeJBIG2`
    - `buildStdSecurityHandler`
    - about 25 identifiers renamed when they were exported
- **Status:** CONFIRMED (scripted cross-check against the repo, forme@v0.3.0 and formalis@v0.3.1)
- **Direction:** One path-update pass, and extend check-links.sh to resolve code-span paths and identifiers.

#### C157 — CI never runs several oracles
- **Sev:** Low
- **Where:** .github/workflows/ci.yml:125 (`make corpus arlington refpdfs facturx` only)
- **Issue:**
  - Not fetched in CI:
    - `profiles`, which feeds TestRuleCoverage's `ruleCoverageMaxUncovered=0` ratchet
    - `wtpdf`
    - `ccitt`
    - `jbig2`
    - `notocjk`, which is the only coverage for C24
  - The hand-placed sets (Cal Poly, PDF/UA reference, Order-X) can never run in CI, and nothing says so.
- **Status:** CONFIRMED (traced)
- **Direction:** Add the fetchable sets to CI, and document which checks are local-only.

#### C158 — signing.md snippet
- **Sev:** Low
- **Where:** docs/signing.md:34
- **Issue:** `pdf0.RevocationRevoked` should be `sign.RevocationRevoked`.
- **Status:** CONFIRMED (executed)
- **Direction:** Fix the name.

#### C159 — README says there are no release tags
- **Sev:** Low
- **Where:** README.md:222-223
- **Issue:** v0.1.0 and v0.3.2 exist, and the proxy's `@latest` is v0.3.2. `retract v0.3.0` in go.mod is also redundant with `retract [v0.2.0, v0.3.1]`.
- **Status:** CONFIRMED (executed)
- **Direction:** Update the README, and drop the redundant retraction.

#### C160 — "The root re-exports nothing"
- **Sev:** Low
- **Where:** README.md:237-240, 250-251; docs/architecture.md:32-34; object_api.go:16
- **Issue:** The root re-exports:
  - 12 object type aliases (deliberate)
  - `PDFAOptions` and `PDFAOutputIntent`
  - the five syntax constructors, which return `*syntax.*` types, so callers need the second import anyway
  - `Equal` (compare.go)

  `Document` has 40 exported methods, not 28. compare.go:13-20 is an orphaned comment for `maxCompareDepth`, and it says refs compare "by number alone" when they compare number and generation.
- **Status:** CONFIRMED (traced)
- **Direction:** Decide the re-export policy before v1, and make the docs state it.

#### C161 — Headline limits example is unsafe to copy
- **Sev:** Low
- **Where:** README.md:101-105 (repeated in doc.go, architecture.md and troubleshooting.md); limits.go:93-107
- **Issue:** The example uses 8 MB/64 MB. The godoc says below ~32 MB real documents are rejected, and the heaviest measured needs 218 MB.
- **Status:** CONFIRMED (traced)
- **Direction:** Use values within the safe range.

#### C162 — README Isartor status
- **Sev:** Low
- **Where:** README.md:217-220
- **Issue:** It says "one known missed violation"; `corpusMaxIsartorMissed` is 0 (pdfa_test.go:1156).
- **Status:** CONFIRMED (traced)
- **Direction:** Update the README.

#### C163 — Uncallable exported API
- **Sev:** Low
- **Where:** `sign.VerifySignatures(d core.View…)`, `sign.ValidatePAdES`, `pdfa.ValidateView`, `pdfa.SetEmbeddedChecker`, `facturx.Validate`, `facturx.Embed`, `images.Walk`, `pdfua.RunCheck`, …
- **Issue:** About 30 functions take `internal/core.View`, split as:
  - pdfa 6
  - sign 8
  - facturx 8
  - dpart 2
  - pdfx 2
  - pdfua 1
  - pdfvt 1
  - pdfr 1
  - images 1

  They render on pkg.go.dev as API that no caller can use.
- **Status:** CONFIRMED (`go doc`)
- **Direction:** See §4 T5.

#### C164 — Commands in docs that don't run
- **Sev:** Low
- **Where:** docs/testing.md:222; ci.yml:165; .gitignore
- **Issue:**
  - `go run ./internal/cmd/rulecoverage` fails with "build constraints exclude all Go files"; it needs `-tags devtools`.
  - `make fuzz` (ci.yml:165) does not exist.
  - Nor do the targets .gitignore names: `make en16931-artefacts`, `en16931-codelists` and `cius-oracles`.
- **Status:** CONFIRMED (executed)
- **Direction:** Fix the commands, or add the targets.

#### C165 — Fuzz documentation
- **Sev:** Low
- **Where:** docs/testing.md:122-191
- **Issue:** `go test -run=NONE -fuzz=FuzzCmapSubtable .` prints "no fuzz tests to fuzz" and exits 0, because that target moved to forme. `FuzzWriteSurface`, which CI runs, is not mentioned. testing.md:184 says `testdata/fuzz/` is gitignored; .gitignore:60-64 says it deliberately is not.
- **Status:** CONFIRMED (executed)
- **Direction:** Update the doc.

#### C166 — Stray artefacts in the working tree
- **Sev:** Low
- **Where:** repo root
- **Issue:**
  - Untracked `pdf0.test` (25 MB, dated 2026-09-19) and `fonts.test` (9.8 MB, dated 2026-08-04), left by `go test -c` or profiling runs.
  - `.hbenv`, a Python venv for the shaping oracle that now lives in forme, which hides itself with its own `*` .gitignore.
  - The `.gitignore` list of root example binaries misses five examples.

  All of these are ignored and none ships, and check-no-binaries backstops them.
- **Status:** CONFIRMED (traced)
- **Direction:** Delete the files (the user's call), and prefer `-o` into a build dir.

#### C167 — Doc and comment drift
- **Sev:** Low
- **Where:** many
- **Issue:**
  - Check-count claims: "59 checks" in four places, and "~50" in architecture.md:365. The real count is 61.
  - Validator counts are given as 9, 10 and 11.
  - The docs say "around forty" skips; a fresh clone has 51.
  - Level lists omit 4e/4f.
  - validators.md says Level A has no ratchet, but TestCorpusLevelA exists. It also points at the empty `validator_guard.go` and at `sortViolations` (now `finding.Sort`).
  - pdfua.md places `buildStructTree` in pdfua, calls `Dictionary.Get` linear, and says pdfua2.go is 34 lines calling validatePDFUA (it is 18 lines of comment).
  - facturx/adopt.go:20 documents a nonexistent `runUACheck`.
  - dpart_api promises "DPM key/value constraints" (keys are deliberately unchecked) and "bounded work" (refuted by C42).
  - docs/encryption.md:
    - it says a signature's `/Contents` is not exempted from decryption, but the code exempts it;
    - it cites the old root file names;
    - it says a malformed `/Encrypt` never errors, which C59 refutes.
  - pades.go:42 describes the old CoversDocument semantics.
  - examples/sign_verify/main.go:129-133 describes a fixed bug.
  - fonts/face.go:131 says NotoSans covers Arabic and Hebrew; it has neither.
  - docs/cli.md says there are no version tags.
  - htmlpdf/pdfout.go:150 has a `Render` sentence fused into `RefusedError`'s godoc.
  - docs/htmlpdf.md:88-90 describes Render errors as I/O errors, but Render writes nothing.
  - docs/xmp.md uses the pre-split names, and repeats the refuted "~3 s at 4 MiB" bound (C40).
  - Core comments:
    - the duplicated blocks at core.go:22-25, devicecolour.go:110-128 and function.go:17-24;
    - an orphaned `decodeContentStream` doc at devicecolour.go:315-344;
    - `NewTrip`'s doc sitting on `Guard`;
    - filters.go saying the dispatch lives in xref.go.
  - docs/audits/README.md still marks the 2026-07-26 plan "In progress".
  - docs/README.md:77-80 says HTML phase 2 is next, but the proposal says it is done.
  - readpath_test.go:117-118 `t.Skip`s a security regression test when its fixture changes; it should `t.Fatal`.
  - The `profiles` and `notocjk` fetches are unpinned, despite the Makefile's pinning rationale.
- **Status:** CONFIRMED (traced)
- **Direction:** Stop quoting counts. Make check-links resolve code spans. Turn snippets into Examples.

---

## 4. Design tensions

These are the approach-level causes. Fixing findings one line at a time without addressing them will reproduce the same classes. The 2026-07-26 → 2026-09-22 interval already shows this happening: C1/C10/C18/C20/C21/C29 were each "fixed" at one site and reappear here at a sibling.

### T1 — Guards are scoped per call; attacks multiply the call

Every DoS guard bounds one unit:
- one `/W` range
- one RoleMap chain
- one PostScript evaluation
- one bfrange
- one stream decode
- one XMP packet
- one object stream's bytes

The remaining attacks multiply a bounded unit by something the file controls: references, pixels, pages, elements, overlapping ranges, "trailer" substrings. Cancellation is polled between checks, but the cost sits inside one check. So `ValidatePDFAContext` with a 2 s deadline ran 16 s in the lead's re-run, and the Type3 DAG ran 22 s at depth 26. This covers C7, C9, C10, C18, C19, C37-C42, C50-C53 and C79.

**Alternative:** one per-run **work meter** on `core.Run`, charged by every graph walk, expansion, decode and tokeniser and polled for cancellation at each charge. Tripping it produces the existing `limit` finding. Memoisation keyed by (object, context) becomes the default, not a per-site optimisation. The existing knobs (`CIDRangeSpan`, `PostScriptSteps`, the RoleMap budget) become derived defaults, not separate mechanisms. Extraction (text, images) gets a Run too, which closes C79 and gives C54's recover boundary a natural home. The cost is one pass over the walkers to thread the meter through. The benefit is that the promise "context bounds wall time" becomes true by construction.

### T2 — "Silent nil" is the error convention, but the contract is "never look clean when you skipped"

These all return nil or empty on failure:
- `View.Content`
- `MetadataContent`
- `LoadCMap`
- `ParseToUnicode*`
- `ICCProfileData`
- `DecodeCIDSet`
- `crypt.Open`'s `(nil, nil)`

The consumers then either skip silently or assert a violation. Skipping silently makes the verdict falsely clean: C43, C46, C63, C75, C109. Asserting a violation makes it falsely non-conformant: C47, C49. The same nil means both "the file is broken" and "you told me to stop".

**Alternative:** have producers return a typed outcome, `(data, reason)` where the reason is one of ok, malformed, unsupported, limit or locked, and make the `limit`/"unknown" finding a property of the *producer*, not something each of ~100 consumers must remember. `brokenObjStms` splits into "corrupt" and "not decoded by request". Save and Factur-X then read the same outcome. This is a larger refactor than T1. It also makes "an empty result means no implemented check fired" honest.

### T3 — Read normalises away facts that later operations need

`normalizeStructure` discards:
- the `/XRef` and `/ObjStm` object numbers
- `/Size`
- `/XRefStm` (never read at all)

Read also does not keep the source bytes. After that:
- every writer's `max+1` allocator collides with live numbers (C3);
- hybrid files lose objects (C4);
- byte-level PDF/A checks need the caller to pass the right bytes again, and trust stale `Offsets` when they don't (C69);
- Factur-X requires raw bytes as a parameter, PDF/A offers two functions, and UA/X/VT take none (§5).

**Alternative:** keep an immutable **source record** on `Document`:
- the raw bytes (or a ReaderAt)
- the parsed xref sections with their object-number high-water mark and `/Size`
- the offsets
- the revision boundaries

Then all allocation goes through one allocator above the high-water mark. The byte checks read the record, not a parameter, so `ValidatePDFABytes` collapses into `ValidatePDFA`. Signature verification and allowed-changes analysis (C1) can diff revisions. The Document stops pretending the file has no history.

### T4 — Builders operate on the object graph, not on the semantics they name

The operations named for semantic actions are implemented as graph edits:
- `ExtractPages` and `AppendPages` treat a page as "its reachable graph minus `/Parent`". The consequences: inheritance lost (C30), back-references drag in the whole document (C89), duplicates (C90), inline pages dropped (C129), ciphertext copied (C31), and `/Count` computed from direct children (C29).
- `SetDocumentInfo` and `EmbedFacturX` regenerate XMP wholesale (C32, C44).
- `EmbedFacturX` replaces the EmbeddedFiles name tree.
- `SetStructureTree` doesn't clear the previous tree's traces (C91).
- `Repair` edits top-level objects only (C93).

None of these returns an error for "I can't do this safely". `AppendPages` has no error return at all.

**Alternative:** semantic helpers on top of the graph:
- a page importer (in the style of qpdf's `QPDFPageObjectHelper`) that materialises inherited attributes, stops at page-tree and annotation back-edges, and remaps or drops destinations;
- one XMP model with merge semantics (the parser in `pdfa/xmp.go` already exists) that every writer edits instead of replacing;
- a name-tree helper that inserts rather than overwrites.

Give every mutator an `error` return while the module is pre-v1.

### T5 — The level, identity and composition model is spread across strings, declarations and packages

The model is spread across four mechanisms:
- **Declared vs requested levels.** Some gates read the requested `Level`, which is flattened to `BaseB` before checks see it (C64). Others read the file's declaration by substring scraping (`pdfaConformanceFlag`, `DeclaredLevel`, `ExtractXMPValue`: C34, C141). The mapping `LevelFor` claims (U→B) is contradicted by a check requiring exactly "B" (C33).
- **Composition by message text.** Level A, PDF/A-4 variants, VT-2 and Factur-X derive their rules from a base validator's findings by `strings.Contains` on messages (C148), so rewording a message changes another standard's verdict.
- **The public/internal seam leaks.** ~30 exported functions in public packages take `internal/core.View` (C163). Validator signatures differ in raw-bytes handling, receiver vs free function, parameter name and nil handling (C144).
- **Two representations of one Object.** Value and pointer forms (C99) mean every type switch picks one.

**Alternative:**
- A single **target profile** value (part, conformance, variant, revision) carried on the Run, plus exactly one rule comparing the file's declaration (read by the real XMP parser) against it.
- Findings carry a stable sub-rule ID so composition keys on IDs, not text.
- An `internal/bridge` package for the cross-boundary calls, which unexports the View-taking functions from public packages.
- Pointer receivers on the composite object types.

---

## 5. Expectation gaps (affordance, docs, DX)

Each line reads "expected X; found Y".

**Reading and writing**
- **Read of a hybrid file (Word output):** expected every object. Found that compressed objects vanish, and Write drops them without error (C4).
- **Add:** expected "never collides with an object already read from a file" (its doc). Found collisions with ObjStm/XRef numbers that corrupt incremental output (C3).
- **`WithMaxDecodedStreamBytes(math.MaxInt)`:** expected "no limit". Found every stream decoded to empty with a nil error (C48).
- **"Every allocation sized by a file number is capped" (doc.go):** expected it to hold. Found ≥ 9 uncapped sites, with OOMs from 1.6 KB, 19.6 KB and 403 KB files (§3.B).
- **"A stack overflow … prevented at the source" (doc.go):** expected no fatal recursion. Found four executed fatal stack overflows (C13, C14, C17, C37).
- **A context deadline on validation:** expected it to bound wall time. Found 16 s against 2 s, 22 s against 3 s, and 92 s against 1 s (T1).
- **A limit trip:** expected an "unknown" (`limit`) finding. Found silent clean results (C46, C109), asserted violations (C47) and a ConformanceError from Save (C49).

**Signing and encryption**
- **`VerifySignaturesWithRoots(x509.SystemCertPool())`, as signing.md recommends:** expected "signed by someone I trust to sign documents". Found any WebPKI TLS certificate accepted (C22).
- **`ValidatePAdES(...).Conformant`:** expected "unmodified since signing, except archival additions". Found that a self-minted timestamp launders arbitrary changes (C1).
- **`SetEncryption(user, "")`:** expected "no owner restrictions". Found the file openable without any password (C23).
- **`WriteSigned` on a typical modern (xref-stream) PDF:** expected a signed file. Found an error, and the incremental alternative corrupts the file (C21, C3).

**Page and metadata building**
- **`ExtractPages([0])`:** expected a standalone, renderable page. Found no MediaBox or Resources when they were inherited, plus copies of the rest of the document (C30, C89).
- **`AddPage` on a read document:** expected a valid page tree. Found `/Count` wrong on any nested tree, and inherited rotation (C29).
- **`AppendPages`:** expected an error for impossible or unsafe input. Found no error return: it panics (C20), copies ciphertext (C31) and drops inline pages (C129).
- **`SetDocumentInfo({Title})`:** expected a title added. Found Factur-X, PDF/UA, PDF/X and PDF/VT identification destroyed (C32).
- **`EmbedFacturX` on a document with attachments:** expected the invoice added. Found other attachments and metadata removed, and the validator then checking the stale invoice (C44).
- **`Repair(level)`:** expected level-dependent repair. Found `level` unused (C93).
- **Builders given NaN:** expected an error at the call. Found one at Write, after the mutation (C131).

**Validation verdicts**
- **`LevelFor("3","U")`, with `Save` on a Factur-X invoice:** expected success on a conforming file. Found a guaranteed 6.6.4 failure and a Save refusal (C33).
- **`ValidatePDFA(doc, PDFA4F)` on a file declaring nothing:** expected 4f requirements applied, as documented. Found them decided by the file's declaration (C64).
- **Two validations of the same document:** expected identical output, as doc.go promises. Found three different Separation reports across 50 runs (C66).
- **Concurrent validation of one Document:** expected it to be safe, as doc.go promises. Found a data race on any dictionary with 64+ keys (C45).
- **`ValidatePDFUA(nil)`:** expected the same answer as `ValidatePDFA(nil)`. Found a panic (C144).

**Extraction and output**
- **`ExtractImages` on hostile input:** expected Notes, not panics, as images.md promises. Found three distinct unrecovered crashes (C54).
- **`ExtractText` on shaped or ligated text, or pdf0's own htmlpdf CJK output:** expected the drawn text. Found lost ligatures and conjuncts (C25, C86), and an empty string for CJK (C24).
- **htmlpdf with `@page`, `rgba()`, `font-kerning:none` or vertical text:** expected the CSS honoured. Found each silently ignored (C26–C28, C114).

**Docs and tooling**
- **Copying doc.go's first example:** expected it to compile. Found `undefined: pdf0.PDFA4` (C106).
- **CONTRIBUTING's "adding a rule":** expected current instructions. Found a signature and location from before the split (C108).
- **`go test ./...` running the XMP RNG tier "that never skips":** expected it to run. Found it always skips (C103).
- **An empty corpus directory:** expected a skip or failure. Found a green ratchet (C104).
- **Where the raw bytes go, across validators:** expected one convention. Found:
  - optional for PDF/A (a separate `…Bytes` function);
  - required for Factur-X;
  - a parameter for signatures;
  - not accepted by UA, X, VT, R or DParts.

  Nothing checks that the bytes belong to the document (C69).
- **pkg.go.dev for `sign`, `pdfa`, `facturx`:** expected a usable API. Found ~30 exported functions that take an internal type and cannot be called (C163).
- **`Save`, the conformance-enforcing writer:** expected it in the README and package doc. Found it undocumented there; the README's PDF/A path uses `Write`.

---

## 6. Open questions (what the code alone cannot resolve)

1. **Trust model for signatures:**
   - Should nil roots mean "no chain" (current code, docs) or "system pool" (current docstrings) (C62)?
   - Is WebPKI ever an acceptable trust source for document signatures (C22)?
   - Should pdf0 ship an RFC 3161 client, or keep accepting externally obtained tokens?
2. **Allowed changes after signing:** What post-signature modifications should `Conformant`/`DocumentUnmodified` tolerate? DSS/VRI/DocTimeStamp only (PAdES), or ISO 32000 DocMDP P-levels (form fill, annotations)? This decides the shape of the fix for C1.
3. **PDF/A-u levels:** Add PDFA2u/3u as first-class levels with a real Unicode check, or accept U at B and document "u unverified"? The first is more work; the second keeps a claim pdf0 does not verify (C33).
4. **PDF/A-1 rules at parts 2–4:** Should `checkLinearizedTrailerID` run at parts 2–4? veraPDF's profile would answer this (C38).
5. **Readers disagree on CID-keyed OpenType CFF:** Poppler renders pdf0's spec-correct `Encode` output blank; Ghostscript renders it correctly. Should pdf0 embed bare CFF as `CIDFontType0C`, or renumber the subset so CID equals GID, to satisfy both? Acrobat was not available to arbitrate (C24, C110).
6. **Partial Flate output:** Is it acceptable evidence for validation (a truncated Adler-32, C46), or must such streams be "unknown"?
7. **Default ObjStm budget:** Should it be re-derived from measured heap rather than decoded bytes? Real need is ~9 MB against a 512 MB default (C9).
8. **Page-tree operations on read documents:** Are AddPage/AppendPages meant to support arbitrary read page trees and document-level structures (AcroForm, StructTreeRoot, named destinations, OutputIntents)? These are currently dropped without notice (C29-C31).
9. **PDF 2.0 structure namespaces:** Should pdf0 write `/Namespaces` and `/NS` when the document is 2.0? This affects both the builder (C92) and PDF/UA-2 validation.
10. **Spec readings not checkable without the standards:**
    - ISO 19005-4 6.9: does it accept an embedded file conforming to *any* part and conformance level (C34)?
    - Does PDF/R (ISO 23504) permit LZWDecode, or forbid an invisible OCR text layer?
    - Does veraPDF agree on Type3 glyph-space widths when FontMatrix ≠ 0.001 (C67)?
    - Do the PDF/X-1a/3/6 per-level requirements match ISO 15930 (C85)?
11. **Stray local artefacts:** Were `pdf0.test` and `fonts.test` left by the 2026-09-19 profiling session? Can they, and `.hbenv`, be deleted (C166)? This is the user's call; this audit changed nothing.
12. **Hand-placed oracles:** Should the hand-placed oracles (Cal Poly, PDF/UA reference, Order-X) be declared local-only in CONTRIBUTING, or fetched in CI (C157)?

---

## 7. Prior audit (2026-07-26) — items reopened

The project memory records C1–C49 of the prior audit as "all worked". Against HEAD, these are partial or have unfixed siblings:

| Prior ID (2026-07-26) | Prior issue | Now | This audit |
|---|---|---|---|
| 07-26/C1 | `/W` CID range OOM | per-range cap only; aggregate still OOMs | C10 |
| 07-26/C3 | cyclic Type3 resources, stack overflow in the PDF/X scan | cycle fixed; the acyclic DAG is exponential | C19 |
| 07-26/C10 | forged TSA accepted | EKU checked, no chain; the sealed path is exploitable | C1 |
| 07-26/C12 | CoversWholeDocument gap check | fixed for approval signatures, missing in `CoveringDocTimestamp` | C1 |
| 07-26/C13 | stale CRL/OCSP | freshness measured against `time.Now()`; missing nextUpdate is fresh forever | C56 |
| 07-26/C16 | ExtractPages/AppendPages panic on an inline page | turned into silent loss; the direct-`/Pages` sibling still panics | C20, C129 |
| 07-26/C18 | resolve-before-assert | still ~20 sites, including the ones C18 named | C36 |
| 07-26/C20 | RoleMap O(N³) | per-chain budget; per-run cost still unbounded | C41 |
| 07-26/C21 | PostScript step budget | per evaluation; per-image cost unbounded | C51 |
| 07-26/C29 | role-mapped headings | headings fixed; Figure and annot siblings still use raw `/S` | C80, C81 |
| 07-26/C32 | cross-type epsilon | Int↔Real made relative; Real↔Real still absolute | C122 |
| 07-26/C35 | divergent inline-image skippers | one more unfixed copy in `ScanStreamForDeviceOps` | C149 |

Confirmed fixed and not re-reported (prior IDs): 07-26/C4, C8, C11, C14, C19, C24, C27, C36, C37, plus the CMS core. The status of each was traced by the reader of that area.

---

## 8. Scope notes

- **Out of scope:** the author's dependency modules — `forme` (shaping, sfnt/CFF/Type 1 parsing, subsetting), `formalis` (EN 16931), `golittlecms` and `gopenjpeg`. Findings that depend on forme (C110 subsetting, C112 fsType) are reported from pdf0's side of the boundary only. JPX decode goes through gopenjpeg and was not fuzzed here.
- **Unavailable arbiters:** no veraPDF binary and no Acrobat. Spec readings marked "from knowledge" (C82, C85) should be checked against the standards before fixing.
- **Tree state:** the audit made no changes to the repository. Repro programs are in the session scratchpad only. This report is left uncommitted.

