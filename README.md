# pdf0

A PDF parser, serializer, and conformance validator written in Go. The object
model is ISO 32000-2 (PDF 2.0); files of any version are read into it, and most
of the standards below are defined against PDF 1.x — PDF/A-1, -2 and -3 require
a 1.x header, PDF/X-1a and -3 require 1.3/1.4. It is pure Go. Its dependencies
are the author's own modules (`forme` for text shaping, font programs and the
HTML/CSS layout engine, `formalis` for EN 16931 invoice rules, `golittlecms` for
ICC profiles, `gopenjpeg` for JPEG 2000) and one from the Go project,
`golang.org/x/text`, for the Unicode normalisation that PDF 2.0 password
preparation (SASLprep) requires.

```
go get github.com/mgilbir/pdf0
```

## What it does

- **Parse** a PDF into a typed object model (`Read`), preserving dictionary key
  order for faithful round-tripping.
- **Serialize** the object model back to PDF bytes (`Document.Write`),
  regenerating cross-reference streams and object streams where the source used
  them.
- **Render** HTML and CSS onto a page (`htmlpdf.Render`). The engine — the HTML
  parser, the cascade, the box model, floats, tables, bidirectional text — is
  [forme](https://github.com/mgilbir/forme); what is here is the backend that
  writes its display list into a document. See [htmlpdf.md](docs/htmlpdf.md).
- **Validate** against the PDF conformance standards:

  | Standard | Entry point | Findings satisfy `Violation` |
  |----------|-------------|------------------------------|
  | PDF/A-1 to PDF/A-4, every conformance level (`pdfa.Levels`) | `ValidatePDFA` | yes |
  | PDF/UA-1, PDF/UA-2 | `ValidatePDFUA` / `ValidatePDFUA2` | yes |
  | PDF/X-1a/3/4/4p/6 | `ValidatePDFX` | yes |
  | PDF/VT-1, PDF/VT-2 | `ValidatePDFVT` / `ValidatePDFVT2` | yes |
  | PDF/R | `ValidatePDFR` | yes |
  | DPart hierarchy | `ValidateDParts` | yes |
  | Factur-X, Order-X containers | `ValidateFacturX` / `ValidateOrderX` | yes — in a result struct, see below |

  The PDF-standard validators are free functions taking the `*Document`
  first and returning findings that satisfy the shared `Violation` interface, so
  results combine across validators. Factur-X and Order-X return a result
  *struct* rather than a slice, because they also carry the extracted invoice
  XML, the conformance level the container declared, and what the invoice rule
  engine did not evaluate — but `res.Violations` holds `facturx.Violation` /
  `facturx.OrderXViolation`, which satisfy `Violation` like every other finding type.
- **Encrypt / decrypt** with the standard security handler — RC4, AES-128, and
  AES-256, via `ReadWithPassword`, `SetEncryption`, and `RemoveEncryption`
  (`Document.Locked` reports a file that could not be decrypted).
- **Sign and verify** digital signatures (`WriteSigned` / `VerifySignatures`,
  CMS/PKCS#7), including PAdES B-B through B-LTA (`ValidatePAdES`), RFC 3161
  timestamps, and CRL/OCSP revocation. Read the verdict with
  `sign.Result.Intact()` (or `DocumentUnmodified()`), not `Valid` alone —
  `Valid` accepts a document altered by a post-signing incremental update.
  Trust is established only against the roots you pass in
  `sign.VerifyOptions`; with none, no signer is trusted.
- **Extract** text (`ExtractText`, whose error names any page it had to leave
  out) and images (`ExtractImages`, or the lazy
  `Images` iterator for bounded memory on large scan files; decoding
  DCTDecode, CCITTFax, JBIG2 and JPXDecode), **repair** common conformance
  failures at a level (`Repair`), and **manipulate pages** (`ExtractPages`,
  `AppendPages`, each returning an `ImportReport` of what was not carried).
- **Write incrementally** (`WriteIncremental`), **build** a minimal PDF/A
  document (`NewPDFADocument`), and **save** it only if it passes the level it
  claims (`Document.Save`).

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
errs := pdf0.ValidatePDFA(doc, pdfa.PDFA4)
for _, e := range errs {
	fmt.Println(e) // e.g. [PDF/A-4 6.2.10] object 12: font ... must be embedded
}
```

`ValidatePDFA` returns `nil` when none of the implemented checks fire. Note that
the validator does not yet implement every PDF/A rule (see **Status** below), so
an empty result means "nothing I check flagged this," not a guarantee of full
conformance. The byte-level checks (e.g. no data after `%%EOF`) read the file
the document was read from, which the `Document` keeps; a document you built in
memory has no file, and its result carries a checker finding saying those
checks did not run — write it and read it back to check them.

For untrusted input, every unbounded loop and every file-sized allocation is
already capped, and the caps worth tuning are settable per document as options
on `Read`. Each option's documentation gives the largest real value measured —
a cap below it refuses real documents. These are stricter than the defaults and
still above it:

```go
doc, err := pdf0.Read(r, size,
	pdf0.WithMaxDecodedStreamBytes(48<<20),   // decompression-bomb ceiling
	pdf0.WithMaxDecodedContentBytes(256<<20), // whole-run content budget
)
```

They resolve once and are stored on the `Document`, so every later validation and
extraction inherits them. When a cap does stop a check, the trip is reported as a
finding under the rule `"limit"` rather than guessed at — `IsCheckerFinding`
separates that from a real non-conformance, and it means **unknown**, never
*failed*. See [docs/limits.md](docs/limits.md) and
[docs/architecture.md](docs/architecture.md#resource-limits).

Under a deadline, use the `…Context` variants — `ReadContext`,
`Document.WriteContext`, `ValidatePDFAContext`, `ValidatePDFUAContext`,
`Document.ExtractTextContext` and the rest:

```go
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
errs := pdf0.ValidatePDFAContext(ctx, doc, pdfa.PDFA4)
```

A cancelled validation returns the findings it had gathered plus one under the
rule `"limit"`, so it can never be mistaken for a clean result; `Read`, `Write`
and the extractors return an error wrapping `ctx.Err()` instead. See
[docs/architecture.md](docs/architecture.md#cancellation) for which entry points
have a variant, why, and the measured cancellation latency.

To write a PDF/A document, build it and write it with `Save` rather than
`Write`: `Save` checks the written bytes against the level the document claims
and refuses to write one that fails.

See [`examples/`](examples/) for runnable programs, one per directory, each
with a comment at the top saying what it shows. The ones that produce a PDF
write it to stdout (`flavored`, which produces several, to the directory `-o`
names, or a fresh temporary one), so run one as
`go run ./examples/simple_pdfa > out.pdf` rather than building a binary into
the repository.

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
family, signing, images, fonts, XMP, encryption, troubleshooting, and the test
data a fresh clone does not have. For the corpus-ratchet workflow and how to add
a rule, see [CONTRIBUTING.md](CONTRIBUTING.md).

`cmd/pdf0` is a small command-line front end used mainly for poking at files
during development — `go run ./cmd/pdf0 -h`. pdf0 is a library first, and the
tool reaches only a fraction of the API; it is documented in
[docs/cli.md](docs/cli.md) but is not the supported surface.

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
absent, so a fresh clone's `go test ./...` stays green; CI fetches the corpus
and runs it (see [CONTRIBUTING.md](CONTRIBUTING.md#what-ci-checks)).

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
- **`Write` regenerates, rather than preserves, the file layout.** A file read
  from a cross-reference stream is written back as one, with compressible
  objects repacked into an object stream (`/ObjStm`); a traditional-table file
  is written with a table. The object model round-trips, but the exact byte
  layout (object order, which objects share a stream) is regenerated, not
  preserved.
- The PDF/A validator implements a subset of the ISO 19005 rules. Against the
  veraPDF corpus it reports no false positives, no missed violations and no
  parse errors, and it misses nothing in the Isartor PDF/A-1b fail suite; the
  ratchets `TestCorpus` and `TestCorpusIsartor` fail if any of that changes.
  Coverage beyond the corpus is not guaranteed — an empty validation result is
  not a conformance guarantee.
- **Pre-v1.** Releases are tagged (`go get` resolves the newest), but the API is
  not frozen: exported names and signatures may change in any release until a
  v1. v0.2.0 through v0.3.1 are retracted (`go.mod` says why), so `go get`
  skips them.

See [`docs/audits/`](docs/audits/README.md) for the audit history (point-in-time
findings, not a description of how the code works — for that start at
[docs/](docs/README.md)).

## Layout

The entry points are in the root `pdf0` package — `Read`, `Document`, and one
validator function per standard. The types they work in are declared in
subpackages and named from there, so a caller that builds an object graph or
inspects a finding imports the package that owns it. Underneath the root:

- **Regular packages** for pieces that carry public API — `object` (the value
  types), `syntax` (lexer, parser, serializer), `content` (the content-stream
  builder), `fonts`, `simplefont` (ISO 32000's simple-font rules: the
  standard encodings, the Latin and Symbol sets, TrueType code-to-glyph),
  `htmlpdf`, `images`, `sign`, `facturx`, and one per
  validator (`pdfa`, `pdfua`, `pdfx`, `pdfvt`, `pdfr`, `dpart`). Each type is
  declared in exactly one place and named from there: a dictionary is
  `object.Dictionary`, a PDF/UA finding is `pdfua.Violation`, a conformance
  level is `pdfa.PDFA2b`. The root package gives a second name to one group
  only, the object model's types (`pdf0.Dictionary` is `object.Dictionary`),
  because every caller writes them; everything else — `pdfa.SkeletonOptions`,
  `syntax.NewParser`, `object.Equal`, `sign.CheckCertRevocation` — has one
  name, in the package that owns it. The lint
  `TestRootReexportsOnlyTheObjectModel` holds that policy.
- **`internal/`** for implementation whose API is not meant for callers:
  `core` (the document seen from below — see below), `finding` (the shared
  validator harness), `crypt` (the standard security handler, reached only
  through `Document`), `xmp` (the one XMP model every writer edits), `bridge`
  (how the root package reaches each subsystem's `core.View` entry point),
  `ccitt`, `jbig2`.

A subsystem does not name `Document`. It takes a `core.View`: the object graph,
the trailer, the source record of the file it was read from, the resolved
budget, the cancellation signal, and a per-run state for memos. `Document`
stays at the top as the facade — a method must be declared in the package that
declares its type, and `Document`'s exported methods are the public API, so it
cannot move below the packages that would need it. Passing a view *down* keeps
the dependency arrows pointing one way. The functions that take a `core.View`
are not exported from the public packages — a caller outside the module could
not construct one — and reach the root through `internal/bridge`;
`TestPublicAPIMentionsNoInternalType` fails if one leaks back.

The subsystems, and the doc that maps each:

| Subsystem | Where | Map |
|-----------|-------|-----|
| Core object model, parser, serializer | `object/`, `syntax/`, `document.go`, `xref.go`, `objstm.go`, `objstm_write.go`, `source.go`, `incremental.go`, `compare.go`, `internal/core/filters.go` | [architecture.md](docs/architecture.md) |
| PDF/A validation | `pdfa/` (rules in `pdfa/pdfa.go`, `pdfa/final_rules.go`, `pdfa/fonts.go`, `pdfa/content_operators.go`, `pdfa/filestructure.go`, `pdfa/pdfa_levela.go`; the builder in `pdfa/create.go`), with `pdfa_api.go`, `embedded.go`, `preflight.go` and `save.go` in the root | [pdfa.md](docs/pdfa.md) |
| The other validators | `pdfua/`, `pdfx/`, `pdfvt/`, `pdfr/`, `dpart/`, `facturx/`, with their `*_api.go` entry points in the root, `violations.go`, `internal/finding` | [validators.md](docs/validators.md), [pdfua.md](docs/pdfua.md) |
| Content, pages and building | `content/`, `pages.go`, `pagetree.go`, `pageimport.go`, `page_add.go`, `create.go`, `structure.go`, `outline.go`, `annotation.go`, `form.go` | [architecture.md](docs/architecture.md) |
| Fonts | `fonts/`, `simplefont/`, `faceembed.go`, with shaping and program parsing in [forme](https://github.com/mgilbir/forme) | [fonts.md](docs/fonts.md) |
| HTML and CSS to PDF | `htmlpdf/`, with the whole layout engine in [forme](https://github.com/mgilbir/forme) | [htmlpdf.md](docs/htmlpdf.md) |
| XMP metadata | `internal/xmp` (the model every writer edits), `pdfa/xmp.go`, `pdfa/xmp_schemas.go` (the validator's reading) | [xmp.md](docs/xmp.md) |
| Signatures and PAdES | `sign/`, with `sign.go`, `sign_api.go`, `signedfile.go` and `doctimestamp.go` in the root | [signing.md](docs/signing.md) |
| Encryption (standard security handler) | `crypt_api.go`, `internal/crypt`, `internal/saslprep`, `internal/pdfdoc` | [encryption.md](docs/encryption.md) |
| Images and codecs | `images/`, `images_api.go`, `internal/ccitt`, `internal/jbig2`, `internal/core/function.go` (PDF functions) | [images.md](docs/images.md) |
| Text extraction | `text.go`, `internal/core/tounicode.go`, `internal/core/cmap.go` | [fonts.md](docs/fonts.md) |
| Command-line front end (dev aid, not the supported surface) | `cmd/pdf0` | [cli.md](docs/cli.md) |

Every file carries a header comment saying what it owns and which spec clause it
implements; start there.

## License

See [LICENSE](LICENSE).
