# Audit remediation — stacked-PR plan (2026-07-26)

Addresses all 49 findings in `codebase-audit-2026-07-26.md`. Plain-git stack: each branch based on the
previous, PR opened with `--base <prev-branch>`, merged bottom-up into `main`. No attribution in commits or PR
bodies. Each PR: fix + a regression test that cites the finding ID + `go test ./...` green + `gofmt`/`vet` clean.

Legend: 🔴 Critical 🟠 High 🟡 Medium 🟢 Low ⚪ Info. Dependencies noted where a PR sits on another's change.

## Stack (bottom = merges first)

| # | Branch | Findings | Why here |
|---|--------|----------|----------|
| 1 | `fix/dict-indexed-lookup` | C20🟡 C22🟡 (roots), enables C34 | Perf foundation: `Dictionary.Get/Set` amortized O(1) via lazy index (the parser already has the threshold). Kills the O(N³)/O(n²) roots at the source. |
| 2 | `fix/unify-traversal-helpers` | C34🟢 C35🟢 | Dedup: one `dictObjNum` (cached, −1 miss) replacing resolveObjNum/objNumForDict; one inline-image skipper honoring `/L`. Later PRs build on the single version. |
| 3 | `fix/font-w-range-dos` | C1🔴 | Self-contained; cap `/W` CID span like the ToUnicode guard. |
| 4 | `fix/jbig2-pixel-budget` | C2🔴 C48⚪ | Shared total-pixel/work budget across page/region/symbol/pattern/halftone; hoist quadratic symbol-list rebuild. |
| 5 | `fix/pdfx-cycle-guard-and-containment` | C3🔴 C27🟢 C16🟡(nil-guard) | Thread `seen map[*Dictionary]bool` into devColorScanner (stack overflow is **not** recover-able); run non-PDF/A validators + Level-A under a `recover` backstop. |
| 6 | `fix/sig-chain-time-and-coverage` | C4🔴 C11🟡 C12🟡 | Anchor chain on a verified timestamp / real clock, not the self-asserted signing-time; combined `Valid ∧ CoversWholeDocument` verdict; tie the ByteRange gap to `/Contents` + 2-segment check. |
| 7 | `fix/tsa-and-revocation-trust` | C10🟠 C13🟡 | Require `id-kp-timeStamping` EKU + chain on the TSA; CRL/OCSP freshness (thisUpdate/nextUpdate/producedAt). Depends on #6 (timestamp plumbing). |
| 8 | `fix/cms-and-crypto-policy` | C14🟡 C36🟢 C37🟢 | content-type + ESS signing-certificate binding; SHA-1 policy + RSA-PSS/Ed25519; AES PKCS#7 pad validation. |
| 9 | `fix/encryption-state-and-cli` | C8🟠 C6🟠 C7🟠 | Exported "was decrypted" accessor; CLI decrypt/extract/validate detect still-locked; merge/encrypt refuse encrypted; SetEncryption double-encrypt guard. |
| 10 | `fix/validator-traversal-dos` | C20🟡 C22🟡 C21🟡 | Apply the #1 index at the RoleMap/tint sites; PostScript type-4 step budget. Depends on #1. |
| 11 | `fix/write-path-correctness` | C5🟠 C40🟢 C38🟢 | xref-stream field-2 width folds type-2 container numbers; reject shared `/Length` target; reject negative `/Index`. |
| 12 | `fix/pages-correctness` | C15🟡 C16🟡 | AppendPages resolves indirect `/Kids`; guard/skip objNum-0 page refs. (nil-panic backstop from #5.) |
| 13 | `fix/extract-image-text` | C30🟢 C24🟡 C28🟢 | Honor `/Decode`+`/ImageMask` on CCITT/JBIG2 branches; ExtractText resolves inherited `/Resources` and recurses into form XObjects. |
| 14 | `fix/parser-compare` | C31🟢 C32🟢 | Reject nested indirect-object *definitions*; relative epsilon / exact-representability for Integer↔Real. Depends on nothing; may move earlier. |
| 15 | `fix/pdfa-soundness` | C17🟡 C18🟡 C23🟡 C41🟢 C43🟢 C49⚪ | JPXDecode rejected at 1b; resolve-before-assert sweep of the remaining ~8 sites; dedup page-level OI; correct rule IDs; profile-identity by bytes; delete dead code. |
| 16 | `fix/builders-and-misc-validators` | C19🟡 C33🟢 C29🟢 C39🟢 C44🟢 | Level-A builder (build Tagged PDF or reject Level-A); single-quote `xmlns` in xmp + pdfa; UA heading RoleMap resolution; UA2 typed-marker filter + scope note; Factur-X/Order-X symmetry + message. |
| 17 | `refactor/validator-api-coherence` | C45⚪ | Converge receiver convention + one `Violation` type; fold ValidatePDFA/ABytes coverage split. Large; last so it rebases nothing critical. **Candidate to defer.** |
| 18 | `chore/docs-dx-ci` | C25🟡 C26🟡 C9🟠 C42🟢 C47⚪ | doc.go/README/architecture truth-up; fix simple_pdfa example; gofmt; CLI polish; reintroduce CI (guards C9/C42/gofmt from recurring — also closes prior-audit C29). |

18 PRs (17 if C45 deferred). Security cluster = #6–#9; DoS cluster = #3,#4,#10 + guards in #5; the four
Criticals land in #3–#6.

## Ordering principle (the main open choice)

- **Foundational-first (as tabled above):** shared infra (#1 index, #2 dedup) at the bottom so every later fix
  sits on a clean base; the self-contained Criticals (#3–#5) follow immediately; security next. Fewer rebases,
  but the worst bugs don't reach `main` until #1/#2 review clears.
- **Risk-first alternative:** move the four Criticals + the encryption-corruption trio to the very bottom so they
  merge to `main` soonest, and slot infra above them. Fastest risk reduction, at the cost of the C20/C22 DoS
  fixes needing the index that now sits *above* them (so they'd get a local map instead, then simplify later).

## Per-PR gate

`go build ./...` · `go vet ./...` · `gofmt -l` empty · `go test ./...` green (with corpus) · new regression test
citing the finding ID · corpus ratchet constants unchanged (or a documented, justified bump).
