# Contributing

## Build and test

```
go build ./...
go test ./...     # unit + spec-example tests; corpus/round-trip tests skip if their data is absent
go vet ./...
gofmt -l .        # must print nothing
```

`go test ./...` on a fresh clone stays green — but that is not the same as being
covered. Around forty test functions self-skip when their (uncommitted) data is
absent, including the whole PDF/A conformance ratchet, the Arlington structural
oracle, every round-trip test against real PDFs, and the image-codec decode
oracles. `go test -v ./... 2>&1 | grep SKIP` shows what you are not running.

[docs/testing.md](docs/testing.md) is the reference for the test tiers, every
external dataset and how to fetch it, all the make targets, the fuzzers, and the
`cmd/` developer aids. In short: touched a validator, run `make test-corpus`;
touched the parser or serializer, run `make refpdfs && make test-arlington`;
touched an image codec, fetch that codec's sample set.

## What CI checks

`.github/workflows/ci.yml` runs five steps on every push to `main` and every pull
request (Go 1.25.x, `ubuntu-latest`):

1. `gofmt -l .` must print nothing
2. `go vet ./...`
3. `go build ./...`
4. `go test ./... -count=1`
5. `go run ./examples/simple_pdfa` must produce a non-empty `output.pdf`, and
   `go run ./examples/extract_images` must exit 0

**No conformance corpus is fetched in CI**, so every corpus-gated test skips
there. A green check means the code compiles, is formatted, vets clean and passes
the unit tests — it says nothing about conformance. The ratchet below runs only
on your machine, which is why it carries the weight.

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

1. Write a `func(*Document, PDFALevel) []ValidationError` (or reuse a byte-level
   signature) and add it to the `checks` slice in `ValidatePDFABytes` (`pdfa.go`).
   Group it with related rules by file — see the table in
   [docs/architecture.md](docs/architecture.md#where-the-rules-live).
2. Resolve indirect references before type-asserting (`doc.Resolve` /
   `resolveName`): a value behind an indirect reference must not evade the rule.
3. Guard every recursion with a visited-set and every `arr[0]` with a length
   check — the validator processes untrusted files and must not panic or hang.
4. Respect the executed-content model where the rule is about *used* resources
   (see architecture.md).
5. Add a unit test, then run `make test-corpus` and follow the ratchet workflow
   above.

For validators other than PDF/A (PDF/UA, PDF/X, PDF/VT, DPart, Factur-X) see
[docs/validators.md](docs/validators.md); for signatures see
`signatures.go` and `pades.go`, for image codecs
[docs/images.md](docs/images.md), and for the CLI [docs/cli.md](docs/cli.md).

## Checking rule coverage

`make rule-coverage` fetches the
[veraPDF validation profiles](https://github.com/veraPDF/veraPDF-validation-profiles)
(CC BY 4.0, veraPDF Consortium) and cross-references them against the ISO clause
strings pdf0's source emits. It currently reports **181/181 clauses matched**, and
`TestRuleCoverage` ratchets that with `ruleCoverageMaxUncovered = 0`.

That number is loose in both directions, and the second one is the one to
remember: the match is at *clause* granularity, so one pdf0 rule emitting
`"6.2.11"` marks that whole clause covered no matter how many of veraPDF's tests
under it are implemented — the profiles define 528 rules across those 181
clauses. **181/181 is not full rule coverage and not a conformance claim.** (In
the other direction, a clause reported as uncovered may still be implemented
under different numbering; that no longer occurs today.) The corpus ratchet above
is the numbering-agnostic check of actual detection. Details in
[docs/testing.md](docs/testing.md#rule-coverage).

## Fuzzing

`FuzzRead` and `FuzzRoundTrip` (`fuzz_test.go`) are the crash-safety net for
untrusted input; see [docs/testing.md](docs/testing.md#fuzzing) for how to run
them and what to do with a crasher.

## Style

- `gofmt`-clean, `go vet`-clean.
- Match the surrounding code's naming and comment density. Comment the *why*
  (especially any corpus-driven decision that contradicts a naive spec reading),
  not the *what*.

## PDF/UA false-positive oracle

`TestUAReferenceFilesNoFalsePositives` runs `ValidatePDFUA` over the PDF
Association's conformant reference files and requires zero violations. The files
(the `PDFUA-Reference-Files` suite from pdfa.org) are **not committed** and have
no make target — place the extracted PDFs under `spec/pdfua/reference-files/`
(gitignored) and the test picks them up, self-skipping when absent. Add a rule
only if these conformant documents stay clean.
