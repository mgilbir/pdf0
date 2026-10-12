# Remediation of the 2026-10-11 documentation audit

This index maps every finding of
[docs-audit-2026-10-11.md](docs-audit-2026-10-11.md) to the pull request that
addresses it, and records what the remediation found that the audit did not, and
what it deliberately left for later. It is a record of the stack as opened; the
pull requests' commits cite the IDs they address, so `git log --grep 'D12'`
finds the change on main.

## The stack

Thirteen pull requests, stacked bottom to top. Each is reviewable on its own and
each passed every CI step locally (build-test, the corpus job, a run without
datasets) at its own head.

| # | PR | Branch | What it does |
|---|----|--------|--------------|
| 1 | #333 | `fix/a1011-01-signing` | Commits the audit. Signing code: a later signature must itself be intact (D2), visible later signatures surfaced, PAdES carries revocation (D3), signing plumbing unexported (D60, breaking) |
| 2 | #334 | `fix/a1011-02-read-recovery` | Read finds a rebuilt file's catalog inside an object stream (D4); an older xref section declined for a limit is reported (D36); Read/Write godoc |
| 3 | #335 | `fix/a1011-03-extraction-api` | `ErrResourceLimit`, `ImagesContext`, `ExtractImages` deprecated (D5); extractors error on a Locked document and on declined content (D6); option, Repair and Locked godoc |
| 4 | #336 | `fix/a1011-04-validators-code` | Nested `internal` findings keep their reserved name (D25); pdfua plumbing unexported (D60, breaking); validator godoc |
| 5 | #337 | `fix/a1011-05-pdfua-rules` | PDF/UA rules read as veraPDF reads them: 38 false positives on veraPDF pass files removed, a corpus ratchet added (D12) |
| 6 | #338 | `fix/a1011-07-fonts-docs` | fonts.md split into text.md, font-embedding.md and font validation (D8, D42); a COLR face honours a bitmap-only licence (code) |
| 7 | #339 | `fix/a1011-08-htmlpdf-images-docs` | Supplying fonts and images to htmlpdf (D28); writing images (D29); htmlpdf-backend.md split |
| 8 | #340 | `fix/a1011-12-testing-cli-adr` | `make check`, `make test-corpus` as CI runs them (D13, D31); CLI help and generated transcripts (D14, D35); tools/env table (D30); ADRs 0009–0018 (D34); audit index (D52) |
| 9 | #341 | `fix/a1011-10-signing-docs` | The verdict gate rejects an unsigned file (D1), B-LTA time-stamps (D24), signing how-to (D45), diagrams; encryption and troubleshooting drift |
| 10 | #342 | `fix/a1011-06-package-docs` | A package doc for every public package and a lint that requires one (D9); root godoc restructured (D39) |
| 11 | #343 | `fix/a1011-09-validators-docs` | validators.md leads with the entry-point table (D40); one rule map in pdfa.md (D32); PDF/VT and Factur-X drawn |
| 12 | #344 | `fix/a1011-11-limits-architecture` | untrusted-input.md with generated option and Context tables (D41, D20); recovery ladder and Write redrawn; limits.md leads with what a `limit` finding means (D10) |
| 13 | #345 | `fix/a1011-13-building-readme` | building.md (D7); README restructured (D38); examples write conformant PDF 2.0 (D18); index (D43); this map |

## Every finding

| ID | Sev | Addressed in | Status |
|----|-----|--------------|--------|
| D1 | High | 9 | fixed |
| D2 | High | 1 (code), 9 (docs) | fixed — the maintainer chose "verify + surface": an added signature is a permitted change only if it is itself intact, and later visible signatures are listed in `LaterVisibleSignatures` |
| D3 | High | 1, 9 | fixed — `PAdESResult.Revocation`; `Conformant` stays trust-free, as documented |
| D4 | High | 2 (code), 12 (diagram) | fixed |
| D5 | High | 3 (code), 12 (images.md) | fixed — maintainer chose "deprecate + doc": `ExtractImages` deprecated, `ImagesContext` added |
| D6 | High | 3 | fixed |
| D7 | High | 13 | fixed |
| D8 | High | 6 | fixed |
| D9 | High | 10 | fixed, with `TestEveryPublicPackageHasADoc` |
| D10 | Med | 4 (godoc), 12 (limits.md) | fixed |
| D11 | Med | 4 | fixed |
| D12 | Med | 5 | fixed — maintainer chose "investigate + fix"; see below |
| D13 | Med | 8 | fixed |
| D14 | Med | 8 | fixed |
| D15 | Med | 8 | fixed |
| D16 | Med | 3 | fixed |
| D17 | Med | 8 | fixed |
| D18 | Med | 8 (CI runs every example), 10 (doc.go), 13 (examples) | fixed |
| D19 | Med | 12 | fixed — the record is frozen with a drift-since block |
| D20 | Med | 10, 12 | fixed — the table is generated from the exported API |
| D21 | Med | 2 (godoc), 12 (diagram) | fixed |
| D22 | Med | 3, 12 | fixed |
| D23 | Med | 9 | fixed |
| D24 | Med | 9 | fixed |
| D25 | Med | 4 (code), 11 (docs) | fixed |
| D26 | Med | 6 | fixed |
| D27 | Med | 6 | fixed (see "Left for later" for a repository-wide notice) |
| D28 | Med | 7 | fixed |
| D29 | Med | 7 | fixed |
| D30 | Med | 8 | fixed, with `TestTestEnvironmentIsDocumented` |
| D31 | Med | 8, 13 | fixed, with `TestMakeTargetsAreWhatCIRuns` |
| D32 | Med | 11 | fixed |
| D33 | Med | 8 | fixed |
| D34 | Med | 8 | fixed — maintainer default: ADRs carry dated amendments; supersede when the decision changes |
| D35 | Med | 8 | fixed — transcripts are generated and test-checked |
| D36 | Med | 2 (code), 12 (doc) | fixed — the older-section cut is now reported, not just documented |
| D37 | Med | 2, 3 | fixed |
| D38 | Med | 13 | fixed |
| D39 | Med | 10 | fixed |
| D40 | Med | 11 | fixed |
| D41 | Med | 12 | fixed |
| D42 | Med | 6 | fixed |
| D43 | Med | 6, 7, 12, 13 | fixed |
| D44 | Med | 9 | fixed |
| D45 | Med | 9 | fixed |
| D46 | Med | 6, 9, 11, 12, 13 | fixed — the §5 backlog is drawn: recovery ladder, Write, lifecycle, View/bridge, verification, signing sequence, limit reporting, font writer, PDF/VT, Factur-X, test-data decision |
| D47 | Low | 2, 3, 9, 10, 13 | fixed |
| D48 | Low | 13 | fixed |
| D49 | Low | 9 | fixed |
| D50 | Low | 9 | fixed |
| D51 | Low | 1, 9 | fixed — `ChainErr` now says "no trust roots were supplied" rather than staying nil |
| D52 | Low | 8 | fixed — and 11 cited hashes that were not on main were remapped |
| D53 | Low | 6 | fixed |
| D54 | Low | 12 | fixed, with `TestLimitsDocNamesEveryGuard` |
| D55 | Low | 3, 10, 12, 13 | fixed |
| D56 | Low | 11 | fixed |
| D57 | Low | 11 | fixed |
| D58 | Low | 5 | fixed |
| D59 | Low | 1, 2, 3, 4, 6, 7, 10 | fixed |
| D60 | Low | 1, 4 | fixed — maintainer chose "move behind internal" (breaking) |
| D61 | Low | 13 | fixed |
| D62 | Low | 10, 13 | fixed |
| D63 | Low | 8 | fixed |
| D64 | Low | 8, #346 | fixed — and in the follow-up #346, a run whose only findings are checker findings exits 3 |
| D65 | Low | 6, 7 | fixed |
| D66 | Low | 4, 11, #346 | fixed — the follow-up #346 makes the zero `pdfx.Level` `LevelDeclared` (breaking; in the release notes) |
| D67 | Low | 8, 10, 13 | fixed |
| D68 | Info | 1, 4, 8 | fixed, except the `%%EOF` item, which was a misreading (below) |
| D69 | Info | 8 | confirmed and amended |

## Where the audit was wrong or incomplete

- **D68, `%%EOF`.** The audit read `data after %%EOF` as an unformatted Sprintf
  escape. It is the PDF end-of-file marker, which is two percent signs (ISO
  32000-2 7.5.5). The real inconsistency was the opposite one: a signing message
  that wrote `%EOF`, fixed in 1.
- **D8, CFF2 under PDF/A-1.** The audit's §5.4 draft said a CFF2 face is refused
  under PDF/A-1. forme hands every CFF2 face over as a CFF program, which is
  written bare and validates. COLR faces are Type 3 at every level, with
  `Forms.Opaque` under PDF/A-1. Both diagrams were corrected before publishing.
- **D12** marked the 38 pass-file findings PLAUSIBLE. All 38 were confirmed
  false positives, and veraPDF 1.30.3 passes every one of them.
- **D55, "978 vs 1000".** Both figures are true, for different Common Crawl
  samples. The docs now say which figure came from which.
- **D52** understated the problem: beyond the placeholder, 11 cited hashes in
  the 2026-09-22 remediation index were not on main.

## Found while fixing

- **COLR faces ignored a bitmap-only licence** (OS/2 fsType 0x0200). A face
  drawn in colour embedded its outlines as Type 3 paths. Fixed test-first in 6.
- **A rebuilt encrypted cross-reference-stream file lost its `/Encrypt`.** Only
  keyword trailers were read when rebuilding by scan, and an xref-stream file
  keeps `/Encrypt` and `/ID` only in its stream dictionary. Fixed in 2.
- **Text extraction swallowed declined content.** A page whose content stream,
  form or ToUnicode CMap pdf0 declined (over a limit, an unimplemented filter, a
  failed decryption) came back with a nil error and wrong or missing text. It is
  now a `*PageTextError` naming the guard. Fixed in 3.
- **The PDF/UA fail-file catch rate fell from 155 to 150 unnoticed.** The cause
  was d0c9bde (UTF-16 `/Lang` decoding): five fail files had been caught only by
  a false positive. Three are now caught by the rule they test, and a ratchet
  holds the rate.
- **`content.Builder` could not paint an uncoloured tiling pattern.** It wrote
  the pattern name and the colour components as two operators where ISO 32000-2
  8.7.3.3 requires one (`c1 … cn /P scn`), and poppler rejected the content.
  Fixed test-first in 13.
- **Devtools ran on `-h`.** `genicc` and `gensaslprep` regenerated their files,
  and `gensaslprep` fetched the RFC from the network, when asked for help. They
  now refuse operands. Fixed in 8.

## PDF/UA after 5

| Suite | Pass files flagged | Fail files caught |
|---|---|---|
| PDF_UA-1, before | 30 of 141 | 150 of 155 |
| PDF_UA-1, after | 0 of 141 | 153 of 155 |
| PDF_UA-2, before | 8 of 54 | 67 of 84 |
| PDF_UA-2, after | 0 of 54 | 68 of 84 |

The ten PDF Association reference files stay clean. pdf0 deliberately differs
from veraPDF in two places, both to keep those files clean: an untagged `sh`
operator is not reported (Reference-06 paints its page backgrounds that way),
and a stub head is exempt wherever it sits (Reference-08). Both are documented
in pdfua.md.

## Left for later, and then done

Each item this stack left for later was then done in a follow-up stack on top
of it, #346–#351:

| Item | Follow-up |
|------|-----------|
| The remaining PDF/UA misses: four font fail files, twelve PDF/UA-2-only rules | #349 shares PDF/A's font machinery through `internal/fontcheck` and adds 7.21.5/7.21.6; #350 adds the twelve part-2 rules. Both suites are now fully caught (PDF_UA-1 155/155, PDF_UA-2 84/84) with no pass file reported |
| A repository-wide third-party notice | #348: `THIRD_PARTY_NOTICES`, held to the tree by a test |
| `Write` on a signed document after `SetEncryption` | #347, generalised: every whole-file rewrite of a signed document is refused without `AllowInvalidatingSignatures()` (breaking; ADR 0019). It also fixed incremental writers that garbled a file read encrypted |
| Nothing writes PDF/UA's document-level entries | #351: `DocumentInfo.Language` and `Document.DeclarePDFUA` |
| `LaterVisibleSignatures` reports visibility, not content | unchanged by design |

Breaking and behaviour changes of both stacks are collected for the next
release in [docs/releases/unreleased.md](../releases/unreleased.md), added in
#346.

## Open questions for the maintainer

The two this stack left open are answered: D64 (exit 3, #346) and D66
(`pdfx.LevelDeclared`, #346). The follow-up stack raised these:

1. **The spec-example JSON.** `testdata/spec_examples*.json` carries about
   300 KB of ISO 32000 text with no reproduction permission found (#348 says
   so in THIRD_PARTY_NOTICES). Seek permission, rely on the implementation
   exception, or stop committing it and generate it locally from the spec PDFs.
2. **PDF/A font checks that differ from veraPDF** (found by #349, PDF/A left
   unchanged): the AGL table holds `bracket` and `isinferior`, which are in
   neither Adobe list, and accepts `uniXXXX`; PDF/A-1 6.3.7 is applied more
   weakly than veraPDF's; a TrueType code read as `.notdef` is compared against
   `.notdef`'s width; mode 7 is exempt from the width rule; the (3,1)-cmap
   requirement is not applied at parts 2–4. No ratchet moves either way.
3. **Four WTPDF PDF/UA-2 examples flagged by shared rules that veraPDF passes**
   (found by #350): a first heading of H3 (7.4), table headers (7.5), an
   untagged annotation (7.18.1), an XMP packet pdf0 cannot parse (5).
