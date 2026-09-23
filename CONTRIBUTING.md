# Contributing

## Build and test

```
go build ./...
go test ./...     # unit + spec-example tests; corpus/round-trip tests skip if their data is absent
go vet ./...
gofmt -l .        # must print nothing
```

`go test ./...` on a fresh clone stays green — but that is not the same as being
covered. Every test whose (uncommitted) data is absent skips, including the
whole PDF/A conformance ratchet, the Arlington structural oracle, every
round-trip test against real PDFs, and the image-codec decode oracles.
`go test -v ./... 2>&1 | grep SKIP` shows what you are not running.

[docs/testing.md](docs/testing.md) is the reference for the test tiers, every
external dataset and how to fetch it, all the make targets, the fuzzers, and the
`cmd/` developer aids. In short: touched a validator, run `make test-corpus`;
touched the parser or serializer, run `make refpdfs && make test-arlington`;
touched an image codec, fetch that codec's sample set.

## What CI checks

CI fetches every data set that has a make target and runs the whole suite over
it, so the conformance ratchets, the Arlington oracle, the decode oracles and
the rule-coverage ratchet all run on every pull request. What it cannot run is
the data that is not redistributable: the tests over the Cal Poly suite, the
PDF/UA reference files, the Order-X examples and the ISO PDFs skip there, and
run only on a machine where someone has placed that data by hand. A green check
says nothing about those; run them yourself when you touch what they cover.

The section below is generated from `.github/workflows/ci.yml` and
`internal/testfiles/datasets.go`, and a test fails when it disagrees with them.

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

## The corpus ratchet — read this before changing a validation rule

`TestCorpus` runs the validator over the veraPDF corpus and is a **ratcheting
baseline**, not a pass/fail suite. It measures three aggregate counts and fails
only if one gets *worse* than the recorded baseline in `pdfa_test.go`:

- `corpusMaxFalsePositives` (**0**) — pass-files wrongly rejected. This is the
  hard invariant: never raise it.
- `corpusMaxMissed` (**0** for the `PDF_A-*` suites) — fail-files not flagged.
- `corpusMaxParseErrors` (**0**) — files that fail to `Read`.

`TestCorpusIsartor` ratchets the Isartor PDF/A-1b fail suite separately
(`corpusMaxIsartorMissed`), and `TestCorpusParsesEntirely` asserts every corpus
file parses.

CI runs these ratchets too, but read the counts yourself before you push: a
red CI run tells you a count moved, the logged line below tells you which.

**The corpus is the oracle.** Where a spec reading and the corpus disagree, the
corpus wins — it encodes veraPDF's settled interpretation. A rule that looks
correct on paper but raises a false positive on a pass-file is wrong.

Workflow when your change moves the counts:

1. Run `make test-corpus` and read the logged line:
   `corpus results: pass=… fail=… | falsePositives=… missed=… parseErrors=…`
2. If `falsePositives` rose, your rule is too strict — fix the rule, do **not**
   raise the baseline.
3. If `missed` dropped (you caught more), lower `corpusMaxMissed` (or
   `corpusMaxIsartorMissed`) to the new value to lock in the gain.
4. Never raise a baseline to make a red test green.

## Adding a validation rule

1. Write a `func(core.View, pdfa.Level) []pdfa.Violation` and add it to the
   `checks` slice in `validateView` (`pdfa/pdfa.go`); a rule about the file's
   bytes is a `func(*core.FileRecord, pdfa.Level) []pdfa.Violation` in
   `byteChecks` instead, and reads only the file record.
   Group it with related rules by file — see the table in
   [docs/validators.md](docs/validators.md#where-the-rules-live).
2. Resolve indirect references before type-asserting (`doc.Resolve` /
   `resolveName`): a value behind an indirect reference must not evade the rule.
3. Guard every recursion with a visited-set and every `arr[0]` with a length
   check — the validator processes untrusted files and must not panic or hang.
4. Respect the executed-content model where the rule is about *used* resources
   (see [validators.md](docs/validators.md) and [ADR 0004](docs/adr/0004-executed-content-model.md)).
5. Add a unit test, then run `make test-corpus` and follow the ratchet workflow
   above.

For validators other than PDF/A (PDF/UA, PDF/X, PDF/VT, DPart, Factur-X) see
[docs/validators.md](docs/validators.md); for signatures see
[docs/signing.md](docs/signing.md), for image codecs
[docs/images.md](docs/images.md), and for the CLI [docs/cli.md](docs/cli.md).

## Checking rule coverage

`make rule-coverage` fetches the
[veraPDF validation profiles](https://github.com/veraPDF/veraPDF-validation-profiles)
(CC BY 4.0, veraPDF Consortium) and the corpus, and reports rule by rule whether
pdf0 flags each rule's corpus fail files under that rule's own clause. The rules
it rejects only for some other reason are listed with what pdf0 reported
instead. `TestRuleCoverage` ratchets the per-level counts
(`ruleCoverageBaselines`): lower the not-detected baseline when you fix one.
Rules the corpus does not test are not measured, so this is not a conformance
claim either. Details in [docs/testing.md](docs/testing.md#rule-coverage).

## Fuzzing

The fuzz targets (`FUZZ_TARGETS` in the Makefile) are the crash-safety net for
untrusted input. CI fuzzes each for a minute; `make fuzz` runs a longer hunt.
See [docs/testing.md](docs/testing.md#fuzzing) for how to run them safely and
what to do with a crasher.

## Documentation

The docs describe code, and nothing kept them true beyond their links until the
checks in `internal/lint` (`make check-docs` runs them; CI runs them with the
rest of `go test`). They hold the docs to the tree as it is:

- every ```` ```go ```` block, and every Go example in a doc comment,
  type-checks against the module (`TestDocSnippetsCompile`). A fragment is
  completed — a function body around statements, the variables the prose sets
  up (`doc`, `data`, `ctx`, …) declared — and an HTML comment just above the
  fence adjusts that: `<!-- snippet` followed by declarations to add,
  `<!-- snippet in <import path> -->` to check it inside a package,
  `<!-- snippet verbatim <file> -->` for a quotation that must still be in the
  file, `<!-- snippet skip: <reason> -->` for what is not Go;
- every backticked path, identifier, test name, call and make or go command
  resolves (`TestDocCodeSpansResolve`, `TestDocCommandsRun`), and so does every
  camelCase name in a Go comment (`TestCommentsNameWhatExists`). A word that
  looks like a Go name and is not one — an X.509 key usage, an XMP prefix — goes
  in `notGoNames` with what it is;
- a block marked `<!-- messages -->` quotes messages the code emits, and a
  quotation introduced as a godoc's is that godoc (`TestDocQuotedMessagesExist`);
- no prose states how many validators, options, checks or data sets there are
  (`TestDocsQuoteNoDriftingCounts`): a count that matters is generated;
- the sections between `BEGIN GENERATED` and `END GENERATED` markers are
  written by `go test ./internal/lint -run TestGeneratedDocSections -update`
  and checked against their sources.

The audit reports and the design records under `docs/proposals/`, and an ADR
once it is superseded, describe the past, and are not checked.

## Style

- `gofmt`-clean, `go vet`-clean.
- Match the surrounding code's naming and comment density. Comment the *why*
  (especially any corpus-driven decision that contradicts a naive spec reading),
  not the *what*.

## PDF/UA false-positive oracle

`TestUAReferenceFilesNoFalsePositives` runs `ValidatePDFUA` over the PDF
Association's conformant reference files and requires zero violations. The files
(the `PDFUA-Reference-Files` suite from pdfa.org) are **not committed** and have
no make target, so this never runs in CI — place the extracted PDFs under
`spec/pdfua/reference-files/` (gitignored) and the test picks them up,
self-skipping when absent. Add a rule only if these conformant documents stay
clean.
