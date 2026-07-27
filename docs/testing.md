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

**Tier 2 — self-skips when its data is absent.** Every corpus, oracle and
round-trip test looks for a directory and calls `t.Skip` when it is missing, so a
fresh clone stays green. That is the point, and it is also the hazard:

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

## External datasets

None of these are committed. Where a manifest and a downloader are committed
(`sources.tsv` + `download.sh`), the set is reproducible; where the data is
copyrighted or licence-restricted it has no make target and must be placed by
hand.

| Dataset | What it proves | Tests | Fetch | Env var | Lands in | Source / licence |
|---|---|---|---|---|---|---|
| **veraPDF corpus** | The PDF/A conformance ratchet: no false positives, no missed violations, no parse errors | `TestCorpus`, `TestCorpusIsartor`, `TestCorpusConformanceSuites`, `TestCorpusParsesEntirely`, `TestLevelACorpus`, `TestDecryptCorpusFiles`, `TestEncryptedPassthroughAESCorpus`, `TestReEncryptCorpusRoundTrip`, `TestRepairEncryption`, `TestDevColorScannerMatchesPDFA`, `TestArlingtonCorpusParserFaithful` | `make corpus` | `VERAPDF_CORPUS` | `testdata/verapdf-corpus/` | `git clone` [veraPDF/veraPDF-corpus](https://github.com/veraPDF/veraPDF-corpus) |
| **PDF 2.0 reference PDFs** | Read→Write→Read round-trips over real PDF 2.0 files | `TestRoundTripReferencePDFs`, `TestExtractAndMergePages`, `TestExtractText`, `TestValidateConcurrentSameDoc`, `TestWrittenXrefIs20Bytes` and the rest of `write_conformance_test.go`, `TestArlingtonParserFaithful`; also seeds both fuzzers | `make refpdfs` | — (path is hard-coded) | `testdata/pdf20examples/` | `git clone` [pdf-association/pdf20examples](https://github.com/pdf-association/pdf20examples) |
| **veraPDF validation profiles** | Rule-ID coverage against the reference validator's own rule inventory | `TestRuleCoverage`; `cmd/rulecoverage` | `make profiles` | `VERAPDF_PROFILES` | `spec/verapdf-profiles/` | `git clone` [veraPDF/veraPDF-validation-profiles](https://github.com/veraPDF/veraPDF-validation-profiles), CC BY 4.0 |
| **Arlington PDF Model** | External grammar oracle: the parser/serializer represent objects faithfully (right types, keys, structure) | `TestArlingtonParserFaithful`, `TestArlingtonCorpusParserFaithful`, `TestArlingtonOracleHasTeeth` | `make arlington` | `ARLINGTON_MODEL` (points at the `tsv/2.0` subdirectory) | `testdata/arlington-pdf-model/` | `git clone` [pdf-association/arlington-pdf-model](https://github.com/pdf-association/arlington-pdf-model), Apache-2.0 |
| **WTPDF / PDF/UA-2 examples** | Round-trip and robustness over complex real tagged PDF 2.0 (structure trees, associated files, MathML, role maps) | `TestWTPDFExamples` | `make wtpdf` | — | `testdata/wtpdf/*.pdf` | LaTeX Project, [tagging-project discussion 72](https://github.com/latex3/tagging-project/discussions/72), fetched from Google Drive; licences vary per file (see `sources.tsv`) |
| **CCITT samples** | Decode oracle for the Group 3/4 fax decoder (the veraPDF corpus has no CCITT images) | `TestCCITTRealFiles` | `make ccitt` | — | `testdata/ccitt/*.pdf` | pdf.js (Apache-2.0), PyPDF4 (BSD) |
| **JBIG2 samples** | Decode oracle for the JBIG2 decoder: generic templates, MMR, symbol/text, halftone, refinement | `TestJBIG2GenericCrossCheck`, `TestJBIG2SymbolText`, `TestJBIG2Refinement`, `TestJBIG2Halftone`, `TestJBIG2Huffman`, `TestJBIG2EdgeCases` | `make jbig2` | — | `testdata/jbig2/*.pdf` | pdf.js conformance suite, Apache-2.0 |
| **Common Crawl PDFs** | Robustness: the parser must never panic or hang on real-world input nobody designed. Not a decode or conformance oracle — a crash hunt | `cmd/corpusprobe` via `make cc-sweep`; **no `go test` walks it** | `make cc-sweep` | — | streamed, never stored (`testdata/cc/run/`) | digitalcorpora `CC-MAIN-2021-31-PDF-UNTRUNCATED`, ~8M PDFs from Common Crawl |
| **Factur-X / ZUGFeRD invoices** | Oracle for the Factur-X container checks | `TestValidateFacturXCorpus`, `TestValidateFacturXMutations`, `TestValidateFacturXInvoiceCorpus` | `make facturx` | — | `testdata/facturx/*.pdf` | ZUGFeRD/corpus and ZUGFeRD/mustangproject, Apache-2.0 |
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

Two datasets are the exception and *are* committed: `testdata/xmp-rng/`
(ISO 16684 RelaxNG schemas, MIT, used by `TestXMPTablesMatchRNG`) and the
spec-example JSON. The EN 16931 / CIUS oracle data referenced by the `.gitignore`
(`testdata/en16931-*`, `testdata/xrechnung`, `testdata/peppol`, `testdata/nlcius`)
belongs to `github.com/mgilbir/formalis` now and is fetched by that module's own
Makefile; pdf0 has no targets for it.

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

**Run**

| Target | Effect |
|---|---|
| `make test` | `go test ./...` — the default tier |
| `make test-corpus` | `make corpus`, then `VERAPDF_CORPUS=… go test -v -run TestCorpus -count=1 ./...` |
| `make test-arlington` | `make arlington refpdfs`, then `ARLINGTON_MODEL=…/tsv/2.0 go test -v -run TestArlington -count=1 ./...`; with the corpus also present it additionally sweeps the conformant corpus files |
| `make rule-coverage` | `make profiles`, then `VERAPDF_PROFILES=… go run ./cmd/rulecoverage` |

**Clean**

`make clean-corpus`, `clean-arlington` (both `rm -rf` the cloned directory) and
`clean-wtpdf`, `clean-facturx`, `clean-ccitt`, `clean-jbig2` (each removes the
downloaded `*.pdf` and the `.ok` stamp, keeping the committed manifest and
script). There is no `clean-refpdfs` or `clean-profiles`; remove those by hand.

## Fuzzing

Two fuzz targets live in `fuzz_test.go`. Neither runs under a plain `go test`
beyond replaying its seed corpus.

- **`FuzzRead`** — `Read` must never panic on arbitrary input, and any document it
  returns must survive every validator (`ValidatePDFUA`, `ValidatePDFABytes` at
  all four levels, `ValidatePDFX`, `ValidatePDFVT`, `ValidateDParts`,
  `ValidateFacturX`) and `Write` without panicking. `Read` recovers panics
  internally; the validators do not, so this is their primary crash-safety net.
- **`FuzzRoundTrip`** — whatever `Read` accepts and `Write` emits must read back
  cleanly and losslessly: the output must re-parse, must leave no object stream
  undecodable, and must not drop objects.

Run them one at a time (Go allows only one fuzz target per invocation):

```
go test -run=NONE -fuzz=FuzzRead -fuzztime=5m
go test -run=NONE -fuzz=FuzzRoundTrip -fuzztime=5m
```

Both are seeded from the two structural builders, their AES-256-encrypted forms
(so the fuzzer explores the decrypt/re-encrypt paths), a few degenerate headers,
and any reference PDFs present under `testdata/pdf20examples/` — so
`make refpdfs` first gives the fuzzer a much better starting corpus.

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

- **`cmd/corpusprobe`** — stress-tests the parser against a directory of
  untrusted PDFs, recording parse outcomes and, most importantly, any panics or
  hangs. It runs each file under panic recovery and a 30 s timeout, and also
  exercises `PageCount`, `Write` (to `io.Discard`) and `ValidatePDFUA` on a
  successful parse. Panics and timeouts are reported as bugs; a per-file log of
  every non-ok outcome goes to `$TMPDIR/corpusprobe-failures.tsv`.
  `go run ./cmd/corpusprobe <dir> [workers]` (default 8 workers).
- **`cmd/corpustime`** — times each parse stage of one PDF with a generous budget
  (`Read` 180 s, `PageCount` 60 s, `Write` 180 s, `ValidatePDFUA` 180 s), to
  distinguish a truly-hanging stage from a merely slow huge file.
  `go run ./cmd/corpustime <file.pdf> [file.pdf …]`.
- **`cmd/rulecoverage`** — the human-readable rule-coverage report; see below.
  `go run ./cmd/rulecoverage [srcdir]`.
- **`cmd/extract_spec_examples`** — the two Python extractors (`main.py` for
  ISO 32000-2:2020, `main17.py` for ISO 32000-1:2008) that turn `pdftotext
  -layout` output of a spec PDF into the committed
  `testdata/spec_examples*.json`. You only need these when updating the spec
  fixtures; `TestSpecExamplesRegenerate` re-runs them and diffs against the
  committed JSON whenever the spec PDFs, `pdftotext` and `python3` are all
  present.

## Rule coverage

`cmd/rulecoverage` cross-references the veraPDF validation profiles (the
reference validator's machine-readable inventory of every PDF/A rule) against the
ISO clause strings that appear as quoted literals in pdf0's non-test source — the
rule IDs the validator can emit. `TestRuleCoverage` ratchets the same number with
`ruleCoverageMaxUncovered = 0`.

As of today:

```
$ VERAPDF_PROFILES=spec/verapdf-profiles go run ./cmd/rulecoverage
pdf0 emits 100 distinct rule clauses across the source.

=== PDF/A-1b: 129 rules across 40 clauses — 40/40 clauses covered ===

=== PDF/A-2b: 144 rules across 48 clauses — 48/48 clauses covered ===

=== PDF/A-3b: 146 rules across 48 clauses — 48/48 clauses covered ===

=== PDF/A-4: 109 rules across 45 clauses — 45/45 clauses covered ===

Overall: 181/181 veraPDF clauses matched by a pdf0 rule ID (clause-string match; see caveat).
```

**Read that number carefully — it is a clause-string match, and it is loose in
both directions.**

1. *A clause reported as uncovered may still be implemented.* pdf0 emits some
   rule IDs with ISO 19005-2 numbering even at PDF/A-1, so a clause with no
   textual match may be checked under a different number. The printed rule
   description tells you which pdf0 rule to check by hand. (This direction no
   longer occurs in practice — the count is currently 181/181 — but it is why the
   matcher is deliberately not a hard assertion.)
2. *A matched clause may implement only some of that clause's rules.* This is the
   direction that matters now. The profiles define **528 rules** across those 181
   clauses (129 + 144 + 146 + 109); the match is at clause granularity, so a
   single pdf0 rule emitting `"6.2.11"` marks that whole clause covered no matter
   how many of veraPDF's tests under it are actually implemented. **181/181 is
   not full rule coverage, and it is not a conformance claim.**

The numbering-agnostic check of *actual detection* is the corpus ratchet
(`TestCorpus`, `TestCorpusIsartor`) — that is what measures whether the validator
finds the violations veraPDF finds. Use rule coverage to locate gaps, use the
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
5. **example runs** — `go run ./examples/simple_pdfa`, then asserts `output.pdf`
   is non-empty and removes it, then `go run ./examples/extract_images`, which
   exits non-zero unless the images it wrote come back decoded. These keep the
   examples the docs point at from rotting.

**CI fetches no corpus.** Every dataset in the table above is absent in the CI
container, so every tier-2 test skips there — the conformance ratchet, the
Arlington oracle, the round-trip tests against real PDFs, and all of the image
decode oracles run **only on your machine**. That is why the local ratchet
workflow carries the weight: a green PR check is evidence that the code compiles,
is formatted, vets clean and passes the unit tests, and nothing more. Run
`make test-corpus` yourself before proposing a validator change.
