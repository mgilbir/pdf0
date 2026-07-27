# pdf0

A PDF 2.0 parser, serializer, and PDF/A validator written in Go. Its only
dependencies are the author's own pure-Go modules (`formalis` for EN 16931
invoice rules, `golittlecms` for ICC profiles, `gopenjpeg` for JPEG 2000).

```
go get github.com/mgilbir/pdf0
```

## What it does

- **Parse** a PDF into a typed object model (`Read`), preserving dictionary key
  order for faithful round-tripping.
- **Serialize** the object model back to PDF bytes (`Document.Write`),
  regenerating cross-reference streams and object streams where the source used
  them.
- **Validate** against ten conformance standards:

  | Standard | Entry point | Findings satisfy `Violation` |
  |----------|-------------|------------------------------|
  | PDF/A 1a/1b, 2a/2b, 3a/3b, 4 | `ValidatePDFA` / `ValidatePDFABytes` | yes |
  | PDF/UA-1, PDF/UA-2 | `ValidatePDFUA` / `ValidatePDFUA2` | yes |
  | PDF/X-1a/3/4/4p/6 | `ValidatePDFX` | yes |
  | PDF/VT-1, PDF/VT-2 | `ValidatePDFVT` / `ValidatePDFVT2` | yes |
  | PDF/R | `ValidatePDFR` | yes |
  | DPart hierarchy | `ValidateDParts` | yes |
  | Factur-X, Order-X containers | `ValidateFacturX` / `ValidateOrderX` | no — see below |

  The six PDF-standard validators are free functions taking the `*Document`
  first and returning findings that satisfy the shared `Violation` interface, so
  results combine across validators. **Factur-X and Order-X are the exception:**
  they return a result struct whose `Violations` are `formalis.Violation`, an
  external type this package cannot extend — adapt them explicitly.
- **Encrypt / decrypt** with the standard security handler — RC4, AES-128, and
  AES-256, via `ReadWithPassword`, `SetEncryption`, and `RemoveEncryption`
  (`Document.Locked` reports a file that could not be decrypted).
- **Sign and verify** digital signatures (`WriteSigned` / `VerifySignatures`,
  CMS/PKCS#7), including PAdES B-B through B-LTA (`ValidatePAdES`), RFC 3161
  timestamps, and CRL/OCSP revocation. Read the verdict with
  `SignatureResult.DocumentUnmodified()`, not `Valid` alone — `Valid` accepts a
  document altered by a post-signing incremental update. `VerifySignatures`
  performs no trust-chain check; use `VerifySignaturesWithRoots` for that.
- **Extract** text (`ExtractText`) and images (`ExtractImages`, or the lazy
  `Images` iterator for bounded memory on large scan files; decoding
  DCTDecode, CCITTFax, JBIG2 and JPXDecode), **repair** common conformance
  failures (`Repair`), and **manipulate pages** (`ExtractPages`, `AppendPages`).
- **Write incrementally** (`WriteIncremental`) and **build** a minimal
  conformant PDF/A document (`NewPDFADocument`).

A command-line tool (`cmd/pdf0`) exposes a deliberately small slice of the
above — `info`, `validate`, `ua`, `decrypt`, `encrypt`, `extract`, `repair` and
`merge` — with exit codes that distinguish "this file has violations" from "the
run failed". Signing, PDF/X, PDF/VT, PDF/R, DPart, Factur-X, PDF/A Level A,
PDF/UA-2 and image extraction are library-only. See [docs/cli.md](docs/cli.md).

## Quick start

Read, inspect, and re-serialize a PDF:

```go
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/mgilbir/pdf0"
)

func main() {
	data, _ := os.ReadFile("input.pdf")
	doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		panic(err)
	}
	fmt.Printf("version=%s objects=%d\n", doc.Version, len(doc.Objects))

	var out bytes.Buffer
	if err := doc.Write(&out); err != nil {
		panic(err)
	}
}
```

Validate against a PDF/A level:

```go
errs := pdf0.ValidatePDFA(doc, pdf0.PDFA4)
for _, e := range errs {
	fmt.Println(e) // e.g. [PDF/A-4 6.2.10] object 12: font ... must be embedded
}
```

`ValidatePDFA` returns `nil` when none of the implemented checks fire. Note that
the validator does not yet implement every PDF/A rule (see **Status** below), so
an empty result means "nothing I check flagged this," not a guarantee of full
conformance. Use `ValidatePDFABytes` when you have the raw file bytes and want
the additional byte-level checks (e.g. no data after `%%EOF`).

See [`examples/`](examples/) for runnable programs (`simple_pdf`, `simple_pdf17`,
`simple_pdfa`); run one with `go run ./examples/simple_pdfa`.

## Build and test

```
go build ./...
go test ./...          # unit + spec-example tests; the corpus test skips if absent
go vet ./...
gofmt -l .             # should print nothing
```

The default `go test ./...` runs the parser/serializer/validator unit tests and
the PDF 1.7 / 2.0 spec-example tests (the spec examples are committed as JSON
under `testdata/`). The round-trip tests need reference PDFs that are not
committed; fetch them with `make refpdfs` (they self-skip when absent).

[docs/](docs/README.md) is the documentation index: architecture, the validator
family, signing, images, the CLI, troubleshooting, and the test data a fresh
clone does not have. For the corpus-ratchet workflow and how to add a rule, see
[CONTRIBUTING.md](CONTRIBUTING.md).

## PDF/A conformance corpus

`TestCorpus` runs the validator over the
[veraPDF corpus](https://github.com/veraPDF/veraPDF-corpus). The corpus is not
committed; fetch it and run the test with:

```
make corpus        # git clone the corpus into testdata/verapdf-corpus
make test-corpus   # run TestCorpus against it
```

`TestCorpus` is a **ratcheting baseline**: it measures aggregate outcomes
(false positives, missed violations, parse errors) and fails only if any gets
worse than the recorded baseline in `pdfa_test.go`. It skips when the corpus is
absent, so a fresh clone's `go test ./...` stays green.

## Status and limitations

This is a young library. What works:

- Object streams (`/Type /ObjStm`) and cross-reference streams, including the
  PNG/TIFF `/Predictor` filters, are read.
- The reader recovers from common malformations — wrong stream `/Length`,
  offset-shifted xref, a `startxref` pointing *into* the table, broken object
  streams, and a cross-reference section so damaged that the table is rebuilt by
  scanning the file for object headers — and converts any panic into an error
  rather than crashing on adversarial input. See
  [the recovery ladder](docs/architecture.md#the-recovery-ladder) for what is
  actually fatal.

Known limitations:

- **A file that could not be decrypted cannot be modified.** Files using the
  standard security handler are decrypted on `Read` for RC4 (V1/V2), AES-128
  (V4/`AESV2`), and AES-256 (V5/`AESV3`, R6); their strings and streams are then
  available in the clear, and such a document round-trips — `Write` re-encrypts
  with the retained key and re-emits the preserved `/Encrypt`. `Read` uses the
  empty password; `ReadWithPassword` accepts a user or owner password. But a
  wrong password, or a scheme pdf0 does not implement, leaves the file encrypted
  (`Document.Locked`): its structure parses, its strings and streams stay
  ciphertext, and `Write` passes the original bytes through verbatim rather than
  producing a corrupt file.
- **`WriteSigned` currently fails on documents whose pages have content
  streams.** The `/ByteRange` placeholder is located with a plain byte search
  for `/Contents`, which finds a page's content-stream reference first, so
  signing returns `signing: /ByteRange placeholder not found`. The same search
  makes `WriteSignedIncremental` unable to add a second signature to an
  already-signed file. Verification, `ValidatePAdES` and `WriteArchivalTimestamp`
  are unaffected. See [docs/signing.md](docs/signing.md#limitations-and-edge-cases).
- **`Write` regenerates, rather than preserves, the file layout.** A file read
  from a cross-reference stream is written back as one, with compressible
  objects repacked into an object stream (`/ObjStm`); a traditional-table file
  is written with a table. The object model round-trips, but the exact byte
  layout (object order, which objects share a stream) is regenerated, not
  preserved.
- The PDF/A validator implements a subset of the ISO 19005 rules. Against the
  veraPDF corpus it currently reports no false positives, no missed violations,
  and no parse errors (tracked by `TestCorpus`), with one known missed violation
  in the Isartor PDF/A-1b fail suite (`corpusMaxIsartorMissed`, tracked
  separately by `TestCorpusIsartor`). Coverage beyond the corpus is not
  guaranteed — an empty validation result is not a conformance guarantee.
- **No release tags yet.** The API is not frozen; `go get` resolves a
  pseudo-version, and exported names may change until a v1 is tagged.

See [`docs/audits/`](docs/audits/README.md) for the audit history (point-in-time
findings, not a description of how the code works — for that start at
[docs/](docs/README.md)).

## Layout

pdf0 is one flat Go package. The subsystems, and the doc that maps each:

| Subsystem | Files | Map |
|-----------|-------|-----|
| Core object model, parser, serializer | `object.go`, `lexer.go`, `parser.go`, `serializer.go`, `compare.go`, `xref.go`, `objstm.go`, `objstm_write.go`, `filters.go`, `document.go`, `incremental.go` | [architecture.md](docs/architecture.md) |
| PDF/A validation | `pdfa.go`, `pdfa_levela.go`, `final_rules.go`, `content_operators.go`, `filestructure.go`, `pdfa_create.go`, `preflight.go` | [validators.md](docs/validators.md) |
| The other validators | `pdfua*.go`, `pdfx*.go`, `pdfvt.go`, `pdfr.go`, `dpart.go`, `facturx*.go`, `order_x.go`, `violations.go` | [validators.md](docs/validators.md) |
| Fonts and metadata | `fonts.go`, `fontprog.go`, `font_encodings.go`, `cff_strings.go`, `xmp.go`, `xmp_schemas.go` | [validators.md](docs/validators.md) |
| Encryption and signatures | `crypt.go`, `crypt_encrypt.go`, `cms.go`, `signatures.go`, `sign.go`, `pades.go`, `timestamp.go`, `doctimestamp.go`, `revocation.go` | [signing.md](docs/signing.md) |
| Images and codecs | `imageextract.go`, `imagejpeg.go`, `imagecolor.go`, `imagemask.go`, `ccitt.go`, `mq.go`, `jbig2*.go`, `function.go`, `function_ps.go` | [images.md](docs/images.md) |
| Text and pages | `text.go`, `pages.go` | [architecture.md](docs/architecture.md) |
| Command-line tool | `cmd/pdf0` | [cli.md](docs/cli.md) |

Every file carries a header comment saying what it owns and which spec clause it
implements; start there.

## License

See [LICENSE](LICENSE).
