# pdf0

A PDF 2.0 parser, serializer, and PDF/A validator written in Go, with no
third-party dependencies.

```
go get github.com/mgilbir/pdf0
```

## What it does

- **Parse** a PDF into a typed object model (`Read`), preserving dictionary key
  order for faithful round-tripping.
- **Serialize** the object model back to PDF bytes (`Document.Write`).
- **Validate** a document against PDF/A conformance levels (`ValidatePDFA` /
  `ValidatePDFABytes`): PDF/A-1b, -2b, -3b, and -4.
- **Build** a minimal conformant PDF/A document (`NewPDFADocument`).

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
under `testdata/`).

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
- The reader recovers from common malformations (wrong stream `/Length`,
  offset-shifted xref, broken object streams) and never panics on adversarial
  input.

Known limitations:

- **Decryption is not implemented.** Encrypted files are *detected*
  (`Document.Encrypted` is set and `Write` refuses them), but their strings and
  streams remain in encrypted form.
- **`Write` always emits a traditional cross-reference table**, even for a file
  read from an xref stream; the object model round-trips, the on-disk layout is
  regenerated.
- The PDF/A validator implements a subset of the ISO 19005 rules. Against the
  veraPDF corpus it currently reports no false positives, no missed violations,
  and no parse errors (tracked by `TestCorpus`), but coverage beyond the corpus
  is not guaranteed — an empty validation result is not a conformance guarantee.

See `docs/audits/` for the detailed audit history.

## Layout

| Path | Purpose |
|------|---------|
| `object.go` | The `Object` interface and all PDF value types |
| `lexer.go` / `parser.go` | Tokenizer and recursive-descent parser |
| `serializer.go` | Object model → PDF bytes |
| `xref.go` / `objstm.go` / `filters.go` | Cross-reference tables/streams, object streams, and predictor filters |
| `document.go` | Full document read/write |
| `compare.go` | Deep semantic equality |
| `pdfa.go` | PDF/A validation engine (rule dispatch and most rules) |
| `final_rules.go` / `content_operators.go` / `filestructure.go` | Additional PDF/A rules (catalog, content operators, byte-level structure) |
| `fonts.go` / `fontprog.go` / `font_encodings.go` / `cff_strings.go` | Font-dictionary rules and sfnt/CFF/Type1 program parsing |
| `xmp.go` / `xmp_schemas.go` | XMP metadata parsing and schema validation |
| `pdfa_create.go` | Minimal PDF/A document builder |

## License

See [LICENSE](LICENSE).
