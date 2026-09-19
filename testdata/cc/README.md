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

**Run it under a cgroup**, which is what `make cc-sweep-limited` does:

```sh
make cc-sweep-limited FIRST=5600 LAST=5619
```

The probe bounds itself — `GOMEMLIMIT=3GiB` and a per-file timeout — but the
sweep also fetches and unzips, and a bug anywhere in that should hit a wall
rather than the machine. The target wraps the sweep in a `systemd-run --user`
unit with `MemoryMax`, `MemorySwapMax=0`, `CPUQuota`, `TasksMax` and
`RuntimeMaxSec`; override them with `CC_MEM`, `CC_CPU` and `CC_SECS`.

`MemorySwapMax=0` is the one that matters most: without it a runaway thrashes
for hours instead of failing. The 2026-09-19 sweep peaked at 4.58 GB against a
6 GB cap.

It runs in the background, so follow it with:

```sh
systemctl --user status pdf0-ccsweep
tail -f testdata/cc/run/p*.log
```

## Reading the results

`run/aggregate.txt` holds the totals, the grouped error strings, and the
quarantine listing; `run/p*.log` hold the per-block detail.

**Errors are not failures.** A sweep of the open web finds genuinely broken
files, and reporting an error on one is the parser working. Around 0.7% is
normal, mostly `startxref not found` and `PDF header not found` — servers hand
out HTML error pages with a `.pdf` name.

**Panics and timeouts are failures**, and the file is copied into
`run/quarantine/` as the reproduction. For a timeout, run `internal/cmd/corpustime` on it
before assuming a hang: it times `Read`, `PageCount`, `Write` and
`ValidatePDFUA` separately, and a large file can simply be slow.

```
go run -tags devtools ./internal/cmd/corpustime testdata/cc/run/quarantine/<file>.pdf
```

## Sweep log

| Date | Blocks | Files | Errors | Panics | Timeouts | Notes |
|------|--------|-------|--------|--------|----------|-------|
| 2026-07-27 | 5100–5101 | 2,000 | 14 | 0 | 0 | Clean. First run of this committed harness. |
| 2026-07-27 | 4200–4211 | 12,000 | 85 | 0 | 2 | Neither timeout was a hang: 71 MB and 117 MB files where `Read`/`Write` take under 0.5 s and `ValidatePDFUA` takes ~25 s, over the probe's 30 s whole-file budget. |
| 2026-09-19 | 5500–5511 | 11,999 | 88 | 0 | 0 | Clean. First sweep run under a cgroup, via `make cc-sweep-limited`: `MemoryMax=6G`, `MemorySwapMax=0`, `CPUQuota=800%`, `TasksMax=256`. Peak usage 4.58 GB, so the cap was not decorative. Errors were 49 `startxref not found` and 26 `PDF header not found` over the first ten blocks, and the same two kinds over the last two. |

Earlier sweeps (before this harness was committed) are recorded in the source:
`grep -rn "Common Crawl" *.go` points at the defects they found, including the
`startxref`-into-the-table recovery in `document.go` and the JPX channel handling
in `imageextract.go`.
