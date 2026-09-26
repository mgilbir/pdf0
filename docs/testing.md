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

> **A green `go test ./...` does not mean your change is covered.** On a fresh
> clone every test over a data set skips, including the entire PDF/A conformance
> ratchet, every round-trip test against real PDFs, the Arlington structural
> oracle, and all of the image-codec decode oracles. `go test -v ./... 2>&1 | grep
> SKIP` tells you what you are not running. CI fetches every data set that has a
> make target and runs these (see [CI](#ci)); the hand-placed ones run only where
> someone has placed them.

The rule of thumb: if you touched a validator, run `make test-corpus`; if you
touched the parser or serializer, run `make refpdfs && make test-arlington`; if
you touched an image codec, fetch its sample set.

**Tier 3 — opt-in, long-running.** Fuzzing (`make fuzz`) and the developer aids
under `internal/cmd/` never run as part of `go test`.

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

| Dataset | What it proves | Source / licence |
|---|---|---|
| **veraPDF corpus** | The PDF/A conformance ratchet: no false positives, no missed violations, no parse errors | [veraPDF/veraPDF-corpus](https://github.com/veraPDF/veraPDF-corpus) |
| **PDF 2.0 reference PDFs** | Read→Write→Read round-trips over real PDF 2.0 files; also seeds the fuzzers | [pdf-association/pdf20examples](https://github.com/pdf-association/pdf20examples) |
| **veraPDF validation profiles** | Per-rule detection coverage against the reference validator's own rule inventory (with the corpus) | [veraPDF/veraPDF-validation-profiles](https://github.com/veraPDF/veraPDF-validation-profiles), CC BY 4.0 |
| **Arlington PDF Model** | External grammar oracle: the parser/serializer represent objects faithfully (right types, keys, structure) | [pdf-association/arlington-pdf-model](https://github.com/pdf-association/arlington-pdf-model), Apache-2.0 |
| **WTPDF / PDF/UA-2 examples** | Round-trip and robustness over complex real tagged PDF 2.0 (structure trees, associated files, MathML, role maps) | LaTeX Project, [tagging-project discussion 72](https://github.com/latex3/tagging-project/discussions/72), fetched from Google Drive; licences vary per file (see `sources.tsv`) |
| **CCITT samples** | Decode oracle for the Group 3/4 fax decoder (the veraPDF corpus has no CCITT images) | pdf.js (Apache-2.0), PyPDF4 (BSD) |
| **JBIG2 samples** | Decode oracle for the JBIG2 decoder: generic templates, MMR, symbol/text, halftone, refinement | pdf.js conformance suite, Apache-2.0 |
| **Factur-X / ZUGFeRD invoices** | FP=0 oracle for the Factur-X **container** checks; the invoice rule engine's findings are ratcheted, not forbidden (`facturxInvoiceRuleFindings`), because which business rules fire is `formalis`' scope decision | ZUGFeRD/corpus and ZUGFeRD/mustangproject, Apache-2.0 |
| **Noto Sans CJK face** | The only CID-keyed CFF face the embedding tests have: a charset that is not the identity is what tells a CID from a glyph index | [notofonts/noto-cjk](https://github.com/notofonts/noto-cjk), OFL |
| **Cal Poly PDF/VT-1 suite** | FP=0 oracle for PDF/VT, PDF/X and DPart — conforming files must report zero violations | Cal Poly Graphic Communications PDF/VT-1 Test File Suite; copyrighted test content, not redistributable |
| **PDFUA-Reference-Files** | FP=0 oracle for PDF/UA — conformant reference documents must report zero violations | PDFUA-Reference-Files suite from pdfa.org |
| **Order-X examples** | Order-X container checks against the conforming examples | Order-X specification bundle |
| **ISO spec PDFs** | Guards the spec-example pipeline: the committed JSON must still be exactly what the extractors produce (also needs `pdftotext` and `python3` on `PATH`) | ISO / Adobe; copyrighted, never committed |
| **Common Crawl PDFs** | Robustness: the parser must never panic or hang on real-world input nobody designed. Not a decode or conformance oracle — a crash hunt, and no `go test` walks it | digitalcorpora `CC-MAIN-2021-31-PDF-UNTRUNCATED`, ~8M PDFs from Common Crawl, streamed by `make cc-sweep` and never stored |

Where each one lives, how it arrives, and which tests read it — generated from
`internal/testfiles/datasets.go` (the one list the tests resolve data sets
through) and from the tests themselves:

<!-- BEGIN GENERATED: datasets. From internal/testfiles/datasets.go and the tests that read each data set by TestGeneratedDocSections; regenerate with `go test ./internal/lint -run TestGeneratedDocSections -update`. -->

| Data set | Location | Override | How it arrives | Read by |
|---|---|---|---|---|
| veraPDF corpus | `testdata/verapdf-corpus` | `VERAPDF_CORPUS` | `make corpus` | `TestAnAdoptedCIDFaceGetsItsOwnCollection`, `TestArlingtonCorpusParserFaithful`, `TestCFFSubsetDropsUnusedOutlines`, `TestCFFSubsetIsSmallerAndStillParses`, `TestCFFSubsetValidatesAtEveryLevel`, `TestCIDKeyedSystemInfoIsTheFontsOwn`, `TestCorpus`, `TestCorpusConformanceSuites`, `TestCorpusContentLexerDifferential`, `TestCorpusContentStateMemoSound`, `TestCorpusExtractEveryPage`, `TestCorpusIncrementalUpdatesOfXRefStreamFiles`, `TestCorpusIsartor`, `TestCorpusLevelA`, `TestCorpusParsesEntirely`, `TestCorpusReadKeepsEveryXRefObject`, `TestCorpusTWGA025UseCMapIsReadNotSkipped`, `TestCorpusWriteSignedOfXRefStreamFiles`, `TestCorpusXMPRoundTrip`, `TestDecryptCorpusFiles`, `TestEncryptedPassthroughAESCorpus`, `TestFindingsAreInvariantUnderIndirection`, `TestHybridCorpusManual`, `TestLevelACorpus`, `TestLoadSimpleRefusesWhatCannotBeOne`, `TestOpenTypeCFFEmbedsAndValidates`, `TestReEncryptCorpusRoundTrip`, `TestRepairEncryption`, `TestRuleCoverage`, `TestWorkMeterHeadroom` |
| PDF 2.0 reference PDFs | `testdata/pdf20examples` | — | `make refpdfs` | `TestArlingtonParserFaithful`, `TestExtractAndMergePages`, `TestExtractText`, `TestOffsetsMatchObjects`, `TestReadSimplePDF`, `TestRoundTripReferencePDFs`, `TestValidateConcurrentSameDoc`, `TestWorkMeterHeadroom`, `TestWriteIsIdempotent`, `TestWriteObjectNumberMatchesKey` |
| Arlington PDF model | `testdata/arlington-pdf-model/tsv/2.0` | `ARLINGTON_MODEL` | `make arlington` | `TestArlingtonCorpusParserFaithful`, `TestArlingtonOracleHasTeeth`, `TestArlingtonParserFaithful` |
| veraPDF validation profiles | `spec/verapdf-profiles` | `VERAPDF_PROFILES` | `make profiles` | `TestRuleCoverage` |
| WTPDF / PDF/UA-2 examples | `testdata/wtpdf` | — | `make wtpdf` | `TestWTPDFExamples`, `TestWorkMeterHeadroom` |
| Factur-X corpus | `testdata/facturx` | — | `make facturx` | `TestCorpusFacturXSetDocumentInfoKeepsFindings`, `TestCorpusXMPRoundTrip`, `TestValidateFacturXCorpus`, `TestValidateFacturXInvoiceCorpus`, `TestValidateFacturXMutations`, `TestWorkMeterHeadroom` |
| CCITT samples | `testdata/ccitt` | — | `make ccitt` | `TestCCITTRealFiles` |
| JBIG2 samples | `testdata/jbig2` | — | `make jbig2` | `TestJBIG2EdgeCases`, `TestJBIG2GenericCrossCheck`, `TestJBIG2Halftone`, `TestJBIG2Huffman`, `TestJBIG2Refinement`, `TestJBIG2SymbolText` |
| Noto Sans CJK face | `testdata/notocjk` | — | `make notocjk` | `TestACJKDocumentIsWrittenAndReadsBack`, `TestAnAdoptedCIDFaceIsKeyedCorrectly`, `TestCIDKeyedSetIsKeyedByCID`, `TestCIDKeyedWidthsAreKeyedByCID`, `TestEveryDrawingPathRoundTripsInEveryFaceKind`, `TestEveryDrawingPathValidatesAtEveryLevel`, `fonts: TestAGlyphDrawnForTwoTextsCarriesBoth`, `fonts: TestEncodeAgreesWithFormesEncode`, `fonts: TestEveryPathWritesTheCodeTheFontIsAddressedBy`, `fonts: TestToUnicodeCoversOnlyTheGlyphsDrawn`, `htmlpdf: TestRenderInEveryFaceKindRoundTrips` |
| Cal Poly PDF/VT-1 suite | `testdata/pdfvt` | — | by hand | `TestCorpusContentStateMemoSound`, `TestValidateDPartsCalPolySuite`, `TestValidatePDFVTCalPolySuite`, `TestValidatePDFXCalPolySuite`, `TestWorkMeterHeadroom` |
| PDFUA-Reference-Files | `spec/pdfua/reference-files` | — | by hand | `TestUAReferenceFilesNoFalsePositives` |
| Order-X examples | `spec/order-x/Order-X100_EN/05-ORDER-X EXAMPLES` | — | by hand | `TestValidateOrderXCorpus` |
| ISO 32000-2 PDF | `spec/pdf2.0` | — | by hand | `TestSpecExamplesRegenerate` |
| ISO 32000-1 PDF | `spec/pdf1.7` | — | by hand | `TestSpecExamplesRegenerate` |

<!-- END GENERATED: datasets -->

`spec/` as a whole is gitignored, so anything you drop under it stays out of git.

Every fetch is pinned, because the ratchet baselines are measurements of
specific documents: the git checkouts at a commit (the `*_REF` variables in the
Makefile), the manifest downloads at a commit in each URL, the Noto face at a
commit and a SHA-256 digest, and the WTPDF files — which Google Drive serves by
an id whose content its owner can replace — by a SHA-256 digest in their
manifest. A changed upstream file is a failed fetch, not a silently different
test set. To move to a newer revision, change the pin and update the baselines
it moves in the same change.

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

A few data sets are the exception and *are* committed: `testdata/xmp-rng/`
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
| `make notocjk` | Download the Noto Sans JP face into `testdata/notocjk/` and check its digest |
| `make cc-sweep` | Sweep real-world Common Crawl PDFs for parser panics and hangs (`FIRST=`/`LAST=` pick the block range); `make cc-sweep-limited` runs the same sweep inside a memory- and CPU-capped cgroup |

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
| `make fuzz` | Fuzz every target in `FUZZ_TARGETS` in turn, for `FUZZTIME` each (see [Fuzzing](#fuzzing)) |
| `make check-docs` | `make check-links check-mermaid check-doc-code`: every relative link and anchor resolves, every Mermaid diagram renders, and the documentation says what the code does — every Go snippet type-checks, every code span, command and quoted message resolves, no prose states a count of the code, and the generated sections (CI, data sets, options) are current |

**Clean**

`make clean-corpus`, `clean-arlington`, `clean-notocjk` (each `rm -rf` the
fetched directory), `clean-wtpdf`, `clean-facturx`, `clean-ccitt`, `clean-jbig2`
(each removes the downloaded `*.pdf` and the `.ok` stamp, keeping the committed
manifest and script) and `clean-cc` (the sweep's working directory). There is
no `clean-refpdfs` or `clean-profiles`; remove those by hand.

## Fuzzing

The fuzz targets are listed in `FUZZ_TARGETS` in the Makefile, and
`TestFuzzTargetsAllRun` fails if a fuzz target exists that the list leaves out.
Under a plain `go test` a target only replays its seed corpus; it is fuzzed
only by `make fuzz`, which CI runs for a minute per target on every pull
request.

- **`FuzzRead`** (`fuzz_test.go`) — `Read` must never panic on arbitrary input,
  and any document it returns must survive text and image extraction
  (`ExtractText`, `Images`), signature verification (`VerifySignatures`,
  `ValidatePAdES`), every validator and `Write` without panicking. The
  extractors and signature verification recover panics at their boundary, so
  the target also fails on a *recovered* panic — an image `Note`, a page error
  or a verification error naming an internal error: the recover is defence in
  depth, and what is behind it is still a bug. The document is read under
  `fuzzLimits`, every budget well below its default, so that one input costs
  milliseconds and megabytes rather than the defaults' hundreds of megabytes;
  the target cannot run under `internal/hostile`, whose child process could not
  be handed the fuzzer's input.
- **`FuzzRoundTrip`** (`fuzz_test.go`) — whatever `Read` accepts and `Write`
  emits must read back cleanly and losslessly: the output must re-parse, must
  leave no object stream undecodable, and must not drop objects.
- **`FuzzWriteSurface`** (`fuzz_write_test.go`) — builds a document from the
  fuzzer's bytes through the content builder and the page API, and asserts that
  what it manages to write, it can read back.

- **`FuzzTrueTypeGlyph`** (`fuzz_truetype_test.go`) — reads the fuzzer's bytes
  as an sfnt with forme's parser and asks `simplefont.TrueTypeGlyph` (ISO
  32000-2 9.6.6.4) for a spread of codes and names, symbolic and not: an
  answer must be a glyph the font has, and ignorance must be glyph 0.

The TrueType `cmap` fuzzers went to `github.com/mgilbir/forme` with the font
program parser they fuzz (`FuzzCmapSubtable` and `FuzzSFNTCmap`, in forme's
`font/fuzz_test.go`); run them there. Pointing `go test -fuzz` at a name that is
not in the package prints "no fuzz tests to fuzz" and exits 0, which reads as a
pass — `TestDocCommandsRun` fails on any command in these docs that does that.

The two whole-file targets are seeded from the structural builders, their
AES-256-encrypted forms (so the fuzzer explores the decrypt/re-encrypt paths), a
few degenerate headers, a signed document, every hostile extraction input the
2026-09-22 audit built (generated by `hostileExtractionInputs`, not committed),
and any reference PDFs present under `testdata/pdf20examples/` — so
`make refpdfs` first gives them a much better starting corpus.

Run a longer hunt with `make fuzz` (`FUZZTIME` per target, ten minutes by
default), or one target at a time, and under a memory cap, as any hostile-input
run: the workers share the machine with you, and a regressed guard is what
fuzzing exists to find.

```
systemd-run --user --pipe --wait -p MemoryMax=4G -p MemorySwapMax=0 \
  --working-directory=$PWD /usr/bin/env PATH=$PATH HOME=$HOME \
  go test -run=NONE -fuzz=FuzzRead -fuzztime=10m -parallel=4
```

**Where the corpus lands.** The generated corpus lives in the Go build cache
(`$(go env GOCACHE)/fuzz`), which CI carries between runs. A failing input is
written to `testdata/fuzz/<FuzzTarget>/<hash>`, and CI uploads that directory
as an artifact when the job fails. `testdata/fuzz/` is deliberately *not*
gitignored: Go replays every file there as a seed on each plain `go test`, so a
committed crasher is a regression test.

**If you find a crasher.** `go test -run=FuzzRead/<hash>` replays just that input
deterministically. Minimise it, fix the bug, and commit the input under
`testdata/fuzz/` (or, if it is large, a minimised form as an explicit `f.Add`
seed or a plain unit test) so the regression stays covered. A panic anywhere in
`Read`, a validator, or `Write` is a bug: this library processes untrusted files
and must return an error instead.

## Developer aids under `internal/cmd/`

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
- **`internal/cmd/genicc`** and **`internal/cmd/gensaslprep`** — the generators
  of the embedded ICC profiles and the SASLprep tables. Run them only to
  regenerate those files; each file's test fails if the generator would now
  produce something different.

  Everything under `internal/cmd/` builds only with `-tags devtools`, so that
  `go install` of the module cannot fetch it; CI builds it with the tag so it
  does not rot. `corpusprobe` and `corpustime` are tested by executing the built
  command (`internal/cmd/corpusprobe/main_test.go` and its sibling carry no tag, so `go test ./...` runs
  them); `rulecoverage`'s measurement lives in `internal/rulecov`, tested there
  and by `TestRuleCoverage`.
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
machine-readable rule inventory, per level) it takes the corpus
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
must not rise. The report has one summary line per level, then one line per
rule that is not detected under its own clause:

```
$ make rule-coverage
=== PDF/A-1b: <n> rules; <n> tested by the corpus: <n> detected, <n> caught only under another clause, <n> missed, <n> unreadable; <n> untested ===
  elsewhere  6.1.12-t09       Maximum number of DeviceN components is 8
                                veraPDF test suite 6-1-12-t09-fail-a.pdf: reported 6.2.3
  …
Overall: <n> rules; the corpus tests <n>, and pdf0 detects <n> of those under the rule's own clause.
```

Run it for today's numbers; `ruleCoverageBaselines` holds the ratcheted ones.
`-v` also lists the untested rules. The "elsewhere" rules are the gap list: each
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

**What it still does not show.** The measure is as wide as the corpus: the
untested rules are unmeasured, in either direction (PDF/A-3b's corpus directory
holds only a handful of files; most 3b behaviour is exercised through 2b). A
clause match is not proof that the right check fired, only that a check under
that clause did. And fail files that name a rule the profile does not define are
listed separately rather than counted. Use rule coverage to locate gaps, use the
corpus to prove you closed them. See
[CONTRIBUTING.md](../CONTRIBUTING.md#the-corpus-ratchet--read-this-before-changing-a-validation-rule).

## CI

CI fetches every data set with a make target, counts what arrived, and runs the
whole suite over it; it also fuzzes every target for a minute. What no CI run
can cover is the hand-placed data — the Cal Poly suite, the PDF/UA reference
files, the Order-X examples and the ISO PDFs — whose tests skip there. Before
you propose a change those cover (PDF/VT, PDF/X, DPart, PDF/UA, Order-X, the
spec-example pipeline), run them on a machine that has the data.

What each job runs, generated from `.github/workflows/ci.yml`:

<!-- BEGIN GENERATED: ci. From .github/workflows/ci.yml and internal/testfiles/datasets.go by TestGeneratedDocSections; regenerate with `go test ./internal/lint -run TestGeneratedDocSections -update`. -->

CI runs on every `push` (branches: [main]) and every `pull_request`, as 3 jobs.

**`build-test`** (Go `1.26.x`):

1. gofmt: `gofmt -l .`
2. go vet: `go vet ./...`
3. no unintended Dictionary copies: `./scripts/check-dict-copies.sh`
4. build: `go build ./...`
5. build development tools: `go build -tags devtools ./internal/cmd/...`
6. test: `go test ./... -count=1`
7. doc links resolve: `./scripts/check-links.sh`
8. mermaid diagrams render: `./scripts/check-mermaid.sh`
9. no binaries in the tree: `./scripts/check-no-binaries.sh`
10. example runs: `go run ./examples/simple_pdfa`, `go run ./examples/extract_images`, `go run ./examples/sign_verify`, `go run ./examples/html_to_pdf`

**`corpus`** (Go `1.26.x`):

1. restore corpora: `testdata/verapdf-corpus`, `testdata/arlington-pdf-model`, `testdata/pdf20examples`, `spec/verapdf-profiles`, `testdata/notocjk`, `testdata/facturx/*.pdf`, `testdata/facturx/.ok`, `testdata/wtpdf/*.pdf`, `testdata/wtpdf/.ok`, `testdata/ccitt/*.pdf`, `testdata/ccitt/.ok`, `testdata/jbig2/*.pdf`, `testdata/jbig2/.ok`
2. fetch corpora (only if `steps.corpora.outputs.cache-hit != 'true'`): `make corpus arlington refpdfs facturx profiles wtpdf ccitt jbig2 notocjk`
3. corpora are present and complete
4. conformance ratchets: `go test ./... -count=1`

**`fuzz`** (Go `1.26.x`):

1. restore the fuzz corpus: `~/.cache/go-build/fuzz`
2. fuzz: `make fuzz FUZZTIME=60s`
3. keep any failing input (only if `failure()`): `testdata/fuzz/`

The data sets the tests read, and whether CI has them. A data set CI does
not fetch is one whose tests skip there: those run only on a machine that
has the data.

| Data set | Location | How it arrives | In CI |
|---|---|---|---|
| veraPDF corpus | `testdata/verapdf-corpus` | `make corpus` | fetched; fails below 2800 `*.pdf` files |
| PDF 2.0 reference PDFs | `testdata/pdf20examples` | `make refpdfs` | fetched; fails below 7 `*.pdf` files |
| Arlington PDF model | `testdata/arlington-pdf-model/tsv/2.0` | `make arlington` | fetched; fails below 600 `*.tsv` files |
| veraPDF validation profiles | `spec/verapdf-profiles` | `make profiles` | fetched; fails below 700 `*.xml` files |
| WTPDF / PDF/UA-2 examples | `testdata/wtpdf` | `make wtpdf` | fetched; fails below the manifest's count of `*.pdf` files |
| Factur-X corpus | `testdata/facturx` | `make facturx` | fetched; fails below the manifest's count of `*.pdf` files |
| CCITT samples | `testdata/ccitt` | `make ccitt` | fetched; fails below the manifest's count of `*.pdf` files |
| JBIG2 samples | `testdata/jbig2` | `make jbig2` | fetched; fails below the manifest's count of `*.pdf` files |
| Noto Sans CJK face | `testdata/notocjk` | `make notocjk` | fetched; fails below 1 `*.otf` files |
| Cal Poly PDF/VT-1 suite | `testdata/pdfvt` | It is copyrighted and placed by hand | **no**: local only |
| PDFUA-Reference-Files | `spec/pdfua/reference-files` | It is placed by hand | **no**: local only |
| Order-X examples | `spec/order-x/Order-X100_EN/05-ORDER-X EXAMPLES` | They come with the Order-X specification bundle and are placed by hand | **no**: local only |
| ISO 32000-2 PDF | `spec/pdf2.0` | It is copyrighted and placed by hand | **no**: local only |
| ISO 32000-1 PDF | `spec/pdf1.7` | It is copyrighted and placed by hand | **no**: local only |

<!-- END GENERATED: ci -->
