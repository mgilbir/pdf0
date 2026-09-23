# Testing

This is the reference for pdf0's test data and test tiers: what runs on a bare
`go test ./...`, what silently skips, and how to fetch the datasets that make the
skipped tiers run. Read it on a fresh clone, and again when you are deciding what
a change needs exercised — a change to the PDF/A rules needs the veraPDF corpus,
a change to the JBIG2 decoder needs the pdf.js samples, and neither is present
until you ask for it.

## The tiers

**Tier 1 — always runs.** `go test ./...` on a fresh clone runs the parser,
serializer, encryption, validator, font, XMP and CLI unit tests, plus the PDF 1.7
and PDF 2.0 spec-example tests. The spec examples *are* committed
(`testdata/spec_examples.json`, `testdata/spec_examples_17.json`), as are the
vendored XMP RelaxNG schemas (`testdata/xmp-rng/`), so those tiers never skip.
This tier is what CI runs.

A committed data file that a test cannot find fails the test: it is part of the
repository, so its absence means a broken checkout or a wrong path, never an
optional download. Tests resolve it against the module root through
`internal/testfiles`, so a test in a subpackage reads the same file as one at
the root. (The XMP RelaxNG guard in `pdfa/` named a package-relative path and
skipped on every run from the package split until this was the rule.)

**Tier 2 — self-skips when its data is absent.** Every corpus, oracle and
round-trip test resolves its data set through `internal/testfiles`, which skips
when the data set was never fetched, so a fresh clone stays green. Anything
short of "never fetched" fails instead, because a skip or an empty walk reads as
a pass:

- a fetched data set counts as present only when its `.ok` stamp is there (the
  make target writes it last, and only when every file arrived), so a fetch that
  was interrupted skips with a message saying so;
- an environment override (`VERAPDF_CORPUS`, `ARLINGTON_MODEL`,
  `VERAPDF_PROFILES`) that names something without its stamp, or nothing at all,
  fails;
- a present data set in which the test finds zero files fails;
- a named fixture missing from a present data set fails: the sets are pinned,
  so that is a layout change, not a missing download.

Data placed by hand (the Cal Poly suite, the PDF/UA reference files, the
Order-X examples, the ISO PDFs) has no stamp; the directory's presence is the
signal, and present-but-empty fails. That is the point of the tier, and skipping
is also its hazard:

> **A green `go test ./...` does not mean your change is covered.** Roughly forty
> test functions skip on a fresh clone, including the entire PDF/A conformance
> ratchet, every round-trip test against real PDFs, the Arlington structural
> oracle, and all of the image-codec decode oracles. `go test -v ./... 2>&1 | grep
> SKIP` tells you what you are not running.

The rule of thumb: if you touched a validator, run `make test-corpus`; if you
touched the parser or serializer, run `make refpdfs && make test-arlington`; if
you touched an image codec, fetch its sample set.

**Tier 3 — opt-in, long-running.** The fuzzers (`go test -fuzz=…`) and the
developer aids under `cmd/` never run as part of `go test`.

## Hostile-input tests

A regression test for a denial-of-service guard feeds the input the guard exists
for. Run in the test process, it protects nothing on the day the guard breaks:
the input then does what it was built to do, and an out-of-memory kill takes the
developer's session or the CI runner with it, or a fatal stack overflow ends the
whole test binary. So every such test runs its hostile part through
`internal/hostile`, which re-executes the test binary for that one test with a
resident-memory cap (Linux), a wall-clock cap and a small goroutine stack, and
reports a breach as an ordinary failure: *over memory*, *timeout* or *fatal*.
The test body still asserts the real result (a bounded error, a specific
finding); the caps are only the net underneath. See the package documentation
for the API and the outcomes, and use it for any new test of this kind.

## External datasets

None of these are committed. Where a manifest and a downloader are committed
(`sources.tsv` + `download.sh`), the set is reproducible; where the data is
copyrighted or licence-restricted it has no make target and must be placed by
hand.

| Dataset | What it proves | Tests | Fetch | Env var | Lands in | Source / licence |
|---|---|---|---|---|---|---|
| **veraPDF corpus** | The PDF/A conformance ratchet: no false positives, no missed violations, no parse errors | `TestCorpus`, `TestCorpusIsartor`, `TestCorpusConformanceSuites`, `TestCorpusParsesEntirely`, `TestLevelACorpus`, `TestDecryptCorpusFiles`, `TestEncryptedPassthroughAESCorpus`, `TestReEncryptCorpusRoundTrip`, `TestRepairEncryption`, `TestDevColorScannerMatchesPDFA`, `TestArlingtonCorpusParserFaithful` | `make corpus` | `VERAPDF_CORPUS` | `testdata/verapdf-corpus/` | `git clone` [veraPDF/veraPDF-corpus](https://github.com/veraPDF/veraPDF-corpus) |
| **PDF 2.0 reference PDFs** | Read→Write→Read round-trips over real PDF 2.0 files | `TestRoundTripReferencePDFs`, `TestExtractAndMergePages`, `TestExtractText`, `TestValidateConcurrentSameDoc`, `TestWrittenXrefIs20Bytes` and the rest of `write_conformance_test.go`, `TestArlingtonParserFaithful`; also seeds both fuzzers | `make refpdfs` | — (path is hard-coded) | `testdata/pdf20examples/` | `git clone` [pdf-association/pdf20examples](https://github.com/pdf-association/pdf20examples) |
| **veraPDF validation profiles** | Per-rule detection coverage against the reference validator's own rule inventory (with the corpus) | `TestRuleCoverage`; `internal/cmd/rulecoverage` | `make profiles` | `VERAPDF_PROFILES` | `spec/verapdf-profiles/` | `git clone` [veraPDF/veraPDF-validation-profiles](https://github.com/veraPDF/veraPDF-validation-profiles), CC BY 4.0 |
| **Arlington PDF Model** | External grammar oracle: the parser/serializer represent objects faithfully (right types, keys, structure) | `TestArlingtonParserFaithful`, `TestArlingtonCorpusParserFaithful`, `TestArlingtonOracleHasTeeth` | `make arlington` | `ARLINGTON_MODEL` (points at the `tsv/2.0` subdirectory) | `testdata/arlington-pdf-model/` | `git clone` [pdf-association/arlington-pdf-model](https://github.com/pdf-association/arlington-pdf-model), Apache-2.0 |
| **WTPDF / PDF/UA-2 examples** | Round-trip and robustness over complex real tagged PDF 2.0 (structure trees, associated files, MathML, role maps) | `TestWTPDFExamples` | `make wtpdf` | — | `testdata/wtpdf/*.pdf` | LaTeX Project, [tagging-project discussion 72](https://github.com/latex3/tagging-project/discussions/72), fetched from Google Drive; licences vary per file (see `sources.tsv`) |
| **CCITT samples** | Decode oracle for the Group 3/4 fax decoder (the veraPDF corpus has no CCITT images) | `TestCCITTRealFiles` | `make ccitt` | — | `testdata/ccitt/*.pdf` | pdf.js (Apache-2.0), PyPDF4 (BSD) |
| **JBIG2 samples** | Decode oracle for the JBIG2 decoder: generic templates, MMR, symbol/text, halftone, refinement | `TestJBIG2GenericCrossCheck`, `TestJBIG2SymbolText`, `TestJBIG2Refinement`, `TestJBIG2Halftone`, `TestJBIG2Huffman`, `TestJBIG2EdgeCases` | `make jbig2` | — | `testdata/jbig2/*.pdf` | pdf.js conformance suite, Apache-2.0 |
| **Common Crawl PDFs** | Robustness: the parser must never panic or hang on real-world input nobody designed. Not a decode or conformance oracle — a crash hunt | `internal/cmd/corpusprobe` via `make cc-sweep`; **no `go test` walks it** | `make cc-sweep` | — | streamed, never stored (`testdata/cc/run/`) | digitalcorpora `CC-MAIN-2021-31-PDF-UNTRUNCATED`, ~8M PDFs from Common Crawl |
| **Factur-X / ZUGFeRD invoices** | FP=0 oracle for the Factur-X **container** checks; the invoice rule engine's findings are ratcheted, not forbidden (`facturxInvoiceRuleFindings`), because which business rules fire is `formalis`' scope decision | `TestValidateFacturXCorpus`, `TestValidateFacturXMutations`, `TestValidateFacturXInvoiceCorpus` | `make facturx` | — | `testdata/facturx/*.pdf` | ZUGFeRD/corpus and ZUGFeRD/mustangproject, Apache-2.0 |
| **Cal Poly PDF/VT-1 suite** | FP=0 oracle for PDF/VT, PDF/X and DPart — conforming files must report zero violations | `TestValidatePDFVTCalPolySuite`, `TestValidateDPartsCalPolySuite`, `TestValidatePDFXCalPolySuite`, `TestDevColorScannerMatchesPDFA` | **no make target — place by hand** | — | `testdata/pdfvt/` | Cal Poly Graphic Communications PDF/VT-1 Test File Suite; copyrighted test content, not redistributable |
| **PDFUA-Reference-Files** | FP=0 oracle for PDF/UA — conformant reference documents must report zero violations | `TestUAReferenceFilesNoFalsePositives` | **no make target — place by hand** | — | `spec/pdfua/reference-files/*.pdf` | PDFUA-Reference-Files suite from pdfa.org |
| **Order-X examples** | Order-X container checks against the conforming examples | `TestValidateOrderXCorpus` | **no make target — place by hand** | — | `spec/order-x/Order-X100_EN/05-ORDER-X EXAMPLES/` | Order-X specification bundle |
| **ISO spec PDFs** | Guards the spec-example pipeline: the committed JSON must still be exactly what the extractors produce | `TestSpecExamplesRegenerate` (also needs `pdftotext` and `python3` on `PATH`) | **no make target — place by hand** | — | `spec/pdf2.0/ISO_32000-2_sponsored-ec2.pdf`, `spec/pdf1.7/PDF32000_2008.pdf` | ISO / Adobe; copyrighted, never committed |

`spec/` as a whole is gitignored, so anything you drop under it stays out of git.

The Common Crawl sweep is the one entry that does not follow the fetch-then-test
shape. There is no manifest and no local corpus: 1000-file blocks are streamed,
probed and deleted, so a sweep of any length needs about 1.4 GB of disk rather
than the eight million files. It is also deliberately not a `go test` — sweeping
untrusted files is memory- and time-hostile, so it runs as a separate
resource-capped process (`GOMEMLIMIT`, a per-file timeout) driven by
`make cc-sweep`. Errors are expected there and are not failures: the open web
serves genuinely broken PDFs, and roughly 0.7% is normal. A **panic or a hang**
is the failure, and the file is quarantined as the reproduction. See
[testdata/cc/README.md](../testdata/cc/README.md).

Three datasets are the exception and *are* committed: `testdata/xmp-rng/`
(ISO 16684 RelaxNG schemas, MIT, used by `TestXMPTablesMatchRNG`), the
spec-example JSON, and `testdata/shaping/corpus.txt` — 12,475 strings over
which `Shape`, `Draw` and `MeasureShaped` have to agree with each other, which
is a self-consistency check and needs no oracle beside it.

Two oracle sets belong to other modules now and are fetched by their own
Makefiles; pdf0 has no targets for either. The EN 16931 / CIUS data
(`testdata/en16931-*`, `testdata/xrechnung`, `testdata/peppol`,
`testdata/nlcius`) is `github.com/mgilbir/formalis`'. The shaping oracles —
HarfBuzz's answers over six fonts, Unicode's UAX #9 conformance suites,
CoreText, and the Universal Shaping Engine's category corrections — are
`github.com/mgilbir/forme`', along with the engine they judge.

## Make targets

**Fetch**

| Target | Effect |
|---|---|
| `make refpdfs` | Clone the PDF 2.0 reference PDFs into `testdata/pdf20examples/` |
| `make corpus` | Clone the veraPDF corpus into `testdata/verapdf-corpus/` |
| `make profiles` | Clone the veraPDF validation profiles into `spec/verapdf-profiles/` |
| `make arlington` | Clone the Arlington PDF Model into `testdata/arlington-pdf-model/` |
| `make wtpdf` | Run `testdata/wtpdf/download.sh` to fetch the WTPDF examples from Google Drive |
| `make facturx` | Run `testdata/facturx/download.sh` to fetch the Factur-X invoices |
| `make ccitt` | Run `testdata/ccitt/download.sh` to fetch the CCITT sample PDFs |
| `make jbig2` | Run `testdata/jbig2/download.sh` to fetch the JBIG2 sample PDFs |
| `make cc-sweep` | Sweep real-world Common Crawl PDFs for parser panics and hangs (`FIRST=`/`LAST=` pick the block range) |

Each fetch target is guarded by a `.ok` stamp file, so re-running is a no-op.
The stamp is written only when the fetch succeeded (the download scripts exit
non-zero if any file failed), and it is what the tests read as "this data set is
complete". If you placed a fetchable data set by hand, run its make target,
which fetches it again and writes the stamp.

**Run**

| Target | Effect |
|---|---|
| `make test` | `go test ./...` — the default tier |
| `make test-corpus` | `make corpus`, then `VERAPDF_CORPUS=… go test -v -run TestCorpus -count=1 ./...` |
| `make test-arlington` | `make arlington refpdfs`, then `ARLINGTON_MODEL=…/tsv/2.0 go test -v -run TestArlington -count=1 ./...`; with the corpus also present it additionally sweeps the conformant corpus files |
| `make rule-coverage` | `make profiles corpus`, then `VERAPDF_PROFILES=… VERAPDF_CORPUS=… go run -tags devtools ./internal/cmd/rulecoverage` |

**Clean**

`make clean-corpus`, `clean-arlington` (both `rm -rf` the cloned directory) and
`clean-wtpdf`, `clean-facturx`, `clean-ccitt`, `clean-jbig2` (each removes the
downloaded `*.pdf` and the `.ok` stamp, keeping the committed manifest and
script). There is no `clean-refpdfs` or `clean-profiles`; remove those by hand.

## Fuzzing

Four fuzz targets live in `fuzz_test.go`. None runs under a plain `go test`
beyond replaying its seed corpus.

Two take a whole file:

- **`FuzzRead`** — `Read` must never panic on arbitrary input, and any document it
  returns must survive text and image extraction (`ExtractText`, `Images`),
  signature verification (`VerifySignatures`, `ValidatePAdES`), every validator
  (`ValidatePDFUA`, `ValidatePDFABytes` at all four levels, `ValidatePDFX`,
  `ValidatePDFVT`, `ValidateDParts`, `ValidateFacturX`) and `Write` without
  panicking. `Read` recovers panics internally; the validators do not, so this
  is their primary crash-safety net. The extractors do recover, per image and
  per page, so the target also fails on a *recovered* panic — an image `Note` or
  a page error naming an internal error: the recover is defence in depth, and
  what is behind it is still a bug. The document is read under `fuzzLimits`,
  every budget well below its default, so that one input costs milliseconds and
  megabytes rather than the defaults' hundreds of megabytes; the target cannot
  run under `internal/hostile`, whose child process could not be handed the
  fuzzer's input.
- **`FuzzRoundTrip`** — whatever `Read` accepts and `Write` emits must read back
  cleanly and losslessly: the output must re-parse, must leave no object stream
  undecodable, and must not drop objects.

Two go straight at the TrueType `cmap` parser, which reads attacker-controlled
binary out of an embedded font program. The whole-file targets reach it only
through a valid-enough PDF carrying a valid-enough sfnt carrying a cmap table,
which no random mutation assembles — so in practice they never exercise it at
all:

- **`FuzzCmapSubtable`** — `font.ParseCmapSubtable` on raw subtable bytes, the deep
  target. Beyond "does not panic" it asserts the invariants the work budgets and
  the recent fixes exist to hold: the returned map never exceeds the cmap work
  budget the target parses at (`defaultMaxCmapWork`, the default behind
  `WithMaxCmapWork`) however many groups the table claims; an unreadable subtable — or one that maps nothing — returns nil rather
  than an empty non-nil map (`font.TrueTypeGID` treats a non-nil cmap as
  authoritative, so an empty one reads as "every code is `.notdef`" and produces
  font-wide false findings); and every key is a Unicode code point mapping to a
  glyph index in 1..0xFFFF, never 0. Seeded from the builders behind the
  hand-written cmap tests: each supported format, the budget-tripping tables, the
  truncated and malformed variants, and formats 2/13/14, which are not parsed.
- **`FuzzSFNTCmap`** — `font.ParseSFNT`, the smallest entry point that exercises
  subtable *selection*. The (3,10) > (3,1) > (0,x) ranking runs on
  attacker-supplied platform ids, encoding ids and offsets and decides which
  subtable becomes the font's authoritative cmap; whichever it picks must satisfy
  the same invariants, and the derived symbol and Mac maps must carry only real
  glyph indices. Seeded from multi-subtable fonts built by
  `buildSFNTWithCmapSubtables`.

Neither cmap target asserts a wall-clock bound: inside a fuzz target that is a
flake, since workers run in parallel under load and seed replay runs under
`-race`. Bounded work is asserted through the size of the returned map instead;
the timing assertions stay in `TestCmapFormat4Budget` and
`TestCmapFormat12Budget`, where the input is fixed.

Run them one at a time (Go allows only one fuzz target per invocation):

```
go test -run=NONE -fuzz=FuzzRead -fuzztime=5m
go test -run=NONE -fuzz=FuzzRoundTrip -fuzztime=5m
go test -run=NONE -fuzz=FuzzCmapSubtable -fuzztime=90s
go test -run=NONE -fuzz=FuzzSFNTCmap -fuzztime=90s
```

The two whole-file targets are seeded from the two structural builders, their
AES-256-encrypted forms (so the fuzzer explores the decrypt/re-encrypt paths), a
few degenerate headers, a signed document, every hostile extraction input the
2026-09-22 audit built (generated by `hostileExtractionInputs`, not committed),
and any reference PDFs present under `testdata/pdf20examples/` — so
`make refpdfs` first gives them a much better starting corpus.

Run a fuzzer under a memory cap, as any hostile-input run: the workers share
the machine with you, and a regressed guard is what fuzzing exists to find.

```
systemd-run --user --pipe --wait -p MemoryMax=4G -p MemorySwapMax=0 \
  --working-directory=$PWD /usr/bin/env PATH=$PATH HOME=$HOME \
  go test -run=NONE -fuzz=FuzzRead -fuzztime=10m -parallel=4
```

**Where the corpus lands.** The generated corpus lives in the Go build cache
(`$(go env GOCACHE)/fuzz`); a crashing input is written to
`testdata/fuzz/<FuzzTarget>/<hash>`. `testdata/fuzz/` is gitignored.

**If you find a crasher.** `go test -run=FuzzRead/<hash>` replays just that input
deterministically. Minimise it, fix the bug, and then — since the crasher file
itself is gitignored — add the minimised input as an explicit `f.Add` seed or as
a plain unit test so the regression is committed. A panic anywhere in `Read`, a
validator, or `Write` is a bug: this library processes untrusted files and must
return an error instead.

## Developer aids under `cmd/`

- **`internal/cmd/corpusprobe`** — stress-tests the parser against a directory of
  untrusted PDFs, recording parse outcomes and, most importantly, any panics or
  hangs. It runs each file under panic recovery and a 30 s timeout, and also
  exercises `PageCount`, `Write` (to `io.Discard`) and `ValidatePDFUA` on a
  successful parse. Panics and timeouts are reported as bugs; a per-file log of
  every non-ok outcome goes to `$TMPDIR/corpusprobe-failures.tsv` (the probe
  refuses to start if it cannot create it, since `testdata/cc/sweep.sh`
  quarantines files from it).
  `go run -tags devtools ./internal/cmd/corpusprobe <dir> [workers]` (default 8
  workers; 1 to 1024, anything else is a usage error). Exit status 0 when nothing
  panicked or hung, 1 when something did, a file could not be read, the walk hit
  an error or there were no PDFs to probe, 2 on a usage error.

  The timeout is a `context.Context` deadline that pdf0 observes
  (`ReadContext` / `WriteContext` / `ValidatePDFUAContext`), *and* a `select` on
  the result channel. Both are needed and they do different jobs. The context
  stops the work: before pdf0 had cancellation this loop abandoned the goroutine
  and it kept burning a core and holding its memory until it finished on its own
  — with eight workers and a 25-second file, a real leak, and the concrete
  motivation for [the cancellation
  design](architecture.md#cancellation). The `select` still bounds the wait,
  because a hang pdf0 does not check for cancellation in would block the worker
  forever, and finding exactly that is what this program is for. A `timeout`
  outcome is therefore a stronger signal than it used to be: the work did not
  stop when told to, not merely that it was slow. Measured: two quarantined
  real-world files needing ~14 s and ~25 s, probed with the timeout lowered to
  300 ms, complete the whole run in 0.42 s wall and 0.53 s of CPU.
- **`internal/cmd/corpustime`** — times each parse stage of one PDF with a generous budget
  (`Read` 180 s, `PageCount` 60 s, `Write` 180 s, `ValidatePDFUA` 180 s), to
  distinguish a truly-hanging stage from a merely slow huge file. A file that
  cannot be read or parsed, a failed `Write` and a hung stage are reported and
  make the exit status 1; a hung stage is abandoned (its goroutine keeps running)
  and that file's later stages are skipped.
  `go run -tags devtools ./internal/cmd/corpustime <file.pdf> [file.pdf …]`.
- **`internal/cmd/rulecoverage`** — the per-rule coverage report; see below.
  `make rule-coverage`, or
  `go run -tags devtools ./internal/cmd/rulecoverage [-v]` from the repository root.

  All three build only with `-tags devtools`. `corpusprobe` and `corpustime` are
  tested by executing the built command (their `main_test.go` files carry no tag,
  so `go test ./...` runs them); `rulecoverage`'s measurement lives in
  `internal/rulecov`, tested there and by `TestRuleCoverage`.
- **`cmd/extract_spec_examples`** — the two Python extractors (`main.py` for
  ISO 32000-2:2020, `main17.py` for ISO 32000-1:2008) that turn `pdftotext
  -layout` output of a spec PDF into the committed
  `testdata/spec_examples*.json`. You only need these when updating the spec
  fixtures; `TestSpecExamplesRegenerate` re-runs them and diffs against the
  committed JSON whenever the spec PDFs, `pdftotext` and `python3` are all
  present.

## Rule coverage

`internal/cmd/rulecoverage` measures, rule by rule, which veraPDF PDF/A rules pdf0
*detects*. For every rule in a level's profile (the reference validator's
machine-readable rule inventory: 528 rules across 1b/2b/3b/4) it takes the corpus
files that must fail that rule — the corpus names each file after its rule, e.g.
`veraPDF test suite 6-2-11-4-1-t02-fail-a.pdf` — validates each at that level,
and asks whether pdf0 reports a violation under the rule's own clause. Each rule
is then one of:

| Status | Meaning |
|---|---|
| detected | every fail file is flagged under the rule's clause |
| elsewhere | every fail file is flagged, but at least one only under other clauses — `TestCorpus` counts the file as caught, yet not for the reason the rule describes: the rule is unimplemented and the file trips something else, or it is implemented under another number |
| missed | a fail file is not flagged at all (`TestCorpus`'s "missed") |
| unreadable | a fail file does not parse |
| untested | the corpus has no fail file for the rule, so nothing here can say whether pdf0 implements it |

`TestRuleCoverage` runs the same measurement and ratchets it per level
(`ruleCoverageBaselines` in `coverage_test.go`): the number of rules detected
under their own clause must not fall, and the number tested but not detected
must not rise. As of today:

```
$ make rule-coverage
=== PDF/A-1b: 129 rules; 50 tested by the corpus: 46 detected, 4 caught only under another clause, 0 missed, 0 unreadable; 79 untested ===
  elsewhere  6.1.12-t09       Maximum number of DeviceN components is 8
                                veraPDF test suite 6-1-12-t09-fail-a.pdf: reported 6.2.3
  …
=== PDF/A-2b: 144 rules; 74 tested by the corpus: 63 detected, 11 caught only under another clause, 0 missed, 0 unreadable; 70 untested ===
=== PDF/A-3b: 146 rules; 2 tested by the corpus: 2 detected, 0 caught only under another clause, 0 missed, 0 unreadable; 144 untested ===
=== PDF/A-4: 109 rules; 79 tested by the corpus: 68 detected, 11 caught only under another clause, 0 missed, 0 unreadable; 30 untested ===
Overall: 528 rules; the corpus tests 205, and pdf0 detects 179 of those under the rule's own clause.
```

`-v` also lists the untested rules. The 26 "elsewhere" rules are the gap list: each
line names the fail files and what pdf0 reported for them instead.

**Why it changed.** The tool used to collect every quoted `6.x.y` literal in the
non-test source and match the profiles' clauses against that set. It read
181/181 at every level and could no longer find anything, for three reasons: the
set was one for all levels, so a literal written for 2b covered the same number
at 1b; it matched clauses, not the 528 rules within them; and it never ran the
validator. Once every clause string had been written somewhere, the number could
only read 181/181 — renaming the clause of a working 2b check (`6.1.3` in
`checkFileTrailerID`) left it at 181/181, because `"6.1.3"` still appears
elsewhere, while the per-rule measure drops 2b to 62 detected and fails.

**What it still does not show.** The measure is as wide as the corpus: the 323
untested rules are unmeasured, in either direction (PDF/A-3b's corpus directory
holds only a handful of files; most 3b behaviour is exercised through 2b). A
clause match is not proof that the right check fired, only that a check under
that clause did. And fail files that name a rule the profile does not define are
listed separately rather than counted. Use rule coverage to locate gaps, use the
corpus to prove you closed them. See
[CONTRIBUTING.md](../CONTRIBUTING.md#the-corpus-ratchet--read-this-before-changing-a-validation-rule).

## CI

`.github/workflows/ci.yml` runs on pushes to `main` and on every pull request,
on `ubuntu-latest` with Go 1.25.x. Five steps, in order:

1. **gofmt** — `gofmt -l .` must print nothing; the job fails and lists the files
   otherwise.
2. **go vet** — `go vet ./...`.
3. **build** — `go build ./...`.
4. **test** — `go test ./... -count=1`.
5. **example runs** — `go run ./examples/simple_pdfa > pdfa.pdf`, then asserts it
   is non-empty and a PDF, then `go run ./examples/extract_images`, which
   exits non-zero unless the images it wrote come back decoded. These keep the
   examples the docs point at from rotting.

**CI fetches no corpus.** Every dataset in the table above is absent in the CI
container, so every tier-2 test skips there — the conformance ratchet, the
Arlington oracle, the round-trip tests against real PDFs, and all of the image
decode oracles run **only on your machine**. That is why the local ratchet
workflow carries the weight: a green PR check is evidence that the code compiles,
is formatted, vets clean and passes the unit tests, and nothing more. Run
`make test-corpus` yourself before proposing a validator change.
