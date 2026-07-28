# Common Crawl robustness sweep

Parses millions of real-world PDFs looking for one thing: a file that makes the
parser **panic or hang**. Both are bugs. `Read` must return an error on anything
malformed, and every other oracle in this repo is the wrong shape to prove it —
the veraPDF corpus is conformance fixtures written by people who knew the rules,
and the codec sample sets are a few dozen files each. Only the open web supplies
input nobody designed.

```
make cc-sweep FIRST=4200 LAST=4211      # 12 blocks, 12,000 PDFs
```

Nothing here is committed except the script. There is no manifest and no local
corpus: blocks are streamed one at a time and deleted after probing, so a sweep
of any length needs about 1.4 GB of disk.

## The corpus

digitalcorpora's **CC-MAIN-2021-31-PDF-UNTRUNCATED** — roughly 8 million PDFs
extracted from Common Crawl, served as 1000-file zip blocks.

*Untruncated* is the load-bearing word, and the reason this does not sweep
Common Crawl's WARC files directly. Common Crawl truncates response payloads at
a few MiB, so a WARC-sourced sample is several percent incomplete files that fail
with `startxref not found` — indistinguishable at a glance from a parser defect.
Measured on a WARC sample: 40 of 978 files, 37 of them at exactly 5 MiB + 4
bytes, every one ending mid-stream with no `%%EOF`. This extraction has the
complete bytes, so a failure is the file's fault or ours.

Block numbers are arbitrary. The value is in bytes nobody has run through the
parser before, so pick a fresh range rather than re-running one that is already
in the log below.

## Reading the results

`run/aggregate.txt` holds the totals, the grouped error strings, and the
quarantine listing; `run/p*.log` hold the per-block detail.

**Errors are not failures.** A sweep of the open web finds genuinely broken
files, and reporting an error on one is the parser working. Around 0.7% is
normal, mostly `startxref not found` and `PDF header not found` — servers hand
out HTML error pages with a `.pdf` name.

**Panics and timeouts are failures**, and the file is copied into
`run/quarantine/` as the reproduction. For a timeout, run `cmd/corpustime` on it
before assuming a hang: it times `Read`, `PageCount`, `Write` and
`ValidatePDFUA` separately, and a large file can simply be slow.

```
go run ./cmd/corpustime testdata/cc/run/quarantine/<file>.pdf
```

## Sweep log

| Date | Blocks | Files | Errors | Panics | Timeouts | Notes |
|------|--------|-------|--------|--------|----------|-------|
| 2026-07-27 | 5100–5101 | 2,000 | 14 | 0 | 0 | Clean. First run of this committed harness. |
| 2026-07-27 | 4200–4211 | 12,000 | 85 | 0 | 2 | Neither timeout was a hang: 71 MB and 117 MB files where `Read`/`Write` take under 0.5 s and `ValidatePDFUA` takes ~25 s, over the probe's 30 s whole-file budget. |

Earlier sweeps (before this harness was committed) are recorded in the source:
`grep -rn "Common Crawl" *.go` points at the defects they found, including the
`startxref`-into-the-table recovery in `document.go` and the JPX channel handling
in `imageextract.go`.
