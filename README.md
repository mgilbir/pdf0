# pdf0

A pure-Go library that reads PDF files of any version, checks them against the
conformance standards (PDF/A, PDF/UA, PDF/X, PDF/VT, PDF/R, Factur-X), builds
new ones — text in embedded fonts, images, outlines, tagged structure, HTML and
CSS — signs and verifies them, and writes them back. Reach for it when a Go
program has to produce PDFs that must pass an archival or accessibility check,
or has to take PDFs it did not write and decide whether to trust them. It needs
no cgo and no external tool.

```
go get github.com/mgilbir/pdf0
```

## Quick start

Read a file and validate it against PDF/A-4 (each snippet is the body of a
function returning `error`; `w` is an `io.Writer`):

```go
data, err := os.ReadFile("input.pdf")
if err != nil {
	return err
}
doc, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
if err != nil {
	return err // too damaged to build a document from
}
for _, v := range pdf0.ValidatePDFA(doc, pdfa.PDFA4) {
	fmt.Println(v)
}
```

On the PDF Association's `Simple PDF 2.0 file.pdf` the first finding is
`[PDF/A-4 6.2.10.4.1] object 7: font Helvetica must be embedded (no FontFile/FontFile2/FontFile3 in descriptor)`.
An empty result means no implemented check fired, not a guarantee of
conformance. A finding for which `pdf0.IsCheckerFinding` is true means pdf0
could not finish a check: read it as "unknown", never as a failure
([validators.md](docs/validators.md#reading-a-result)).

Build a page with text in an embedded font, and write it:

```go
doc := pdf0.NewDocument()
face, err := fonts.NotoSans() // bundled with the module
if err != nil {
	return err
}
var page content.Builder
page.BeginText().SetFont("F1", 24).MoveText(72, 720)
face.DrawShaped(&page, "Hello, world", 24)
page.EndText()
if _, err := doc.AddPage(pdf0.Page{
	Width: 595, Height: 842, Content: &page,
	Faces: map[object.Name]*fonts.Face{"F1": face}, // embedded, subset
}); err != nil {
	return err
}
return doc.Save(w)
```

[docs/building.md](docs/building.md) goes on from there: images, outlines,
links, tagged PDF, gradients, transparency, and PDF/A.

Render HTML and CSS onto pages, with [forme](https://github.com/mgilbir/forme)
as the layout engine ([docs/htmlpdf.md](docs/htmlpdf.md)):

<!-- snippet
var html, css string
-->
```go
out, err := htmlpdf.Render(htmlpdf.Input{
	HTML: html,
	CSS:  []htmlpdf.Stylesheet{{Source: css}},
}, htmlpdf.Options{Page: htmlpdf.A4})
if err != nil {
	return err
}
return out.Document.Write(w)
```

## What it does

- **Read and write.** `Read` takes a file of any PDF version into the ISO
  32000-2 object model, repairing what it can work around (wrong offsets, a
  bad `/Length`, a cross-reference table so damaged it is rebuilt by
  scanning), and `Document.Write` serializes it. `WriteIncremental` appends an
  update instead.
- **Validate.** `ValidatePDFA` (PDF/A-1 to PDF/A-4, every level in
  `pdfa.Levels`), `ValidatePDFUA` and `ValidatePDFUA2`, `ValidatePDFX`
  (PDF/X-1a/3/4/4p/6), `ValidatePDFVT` and `ValidatePDFVT2`, `ValidatePDFR`,
  `ValidateDParts`, and `ValidateFacturX` and `ValidateOrderX` for invoice
  containers. Every finding satisfies the shared `pdf0.Violation` interface;
  [docs/validators.md](docs/validators.md) says which to call and how to read
  the result.
- **Build.** `NewDocument`, `AddPage` with a `content.Builder`, fonts from the
  `fonts` package, `images.Embed`, outlines, links, structure trees, forms,
  patterns, gradients and soft masks ([building.md](docs/building.md));
  `NewPDFADocument` and `Document.Save` for PDF/A; `EmbedFacturX` for invoices.
  `Repair` fixes common document-level PDF/A failures in place.
- **Render HTML and CSS** with `htmlpdf.Render` ([htmlpdf.md](docs/htmlpdf.md)).
- **Sign and verify.** `WriteSigned` and `VerifySignatures` (CMS/PKCS#7),
  PAdES B-B through B-LTA (`ValidatePAdES`), RFC 3161 time-stamps, CRL/OCSP
  revocation. Accept a signed file only when there is at least one approval
  signature and every result — signatures and document time-stamps — is
  `Intact()`, has `TrustedChain` and is not revoked; no results means unsigned.
  Trust comes only from the roots you pass in `sign.VerifyOptions`
  ([signing.md](docs/signing.md#which-verdict-do-i-read)).
- **Encrypt and decrypt** with the standard security handler. `Read` and
  `ReadWithPassword` decrypt RC4, AES-128 and AES-256; `SetEncryption` encrypts
  with AES-256 only ([encryption.md](docs/encryption.md)).
- **Extract** text (`ExtractText`) and images (`ImagesContext`, one decoded
  image at a time; DCT, CCITT, JBIG2 and JPEG 2000 decoded), and **move pages**
  between documents (`ExtractPages`, `AppendPages`).

## Examples

Each is one program under [`examples/`](examples/), with a comment at the top
saying what it shows. Those that produce a PDF write it to stdout:
`go run ./examples/simple_pdfa > out.pdf`.

- `simple_pdf` — one PDF 2.0 page with text in a standard font and a rule.
- `simple_pdf17` — the same page as a PDF 1.7 file.
- `simple_pdfa` — a PDF/A-4 page with text in an embedded font, written with `Save`.
- `text` — a paragraph measured and broken into lines in an embedded font.
- `flavored` — one page saved as plain PDF, PDF/A-2b and PDF/A-4, into the directory `-o` names.
- `extract_images` — embeds two images, writes the file to memory, and reads them back.
- `sign_verify` — signs, verifies, then shows `Intact()` catching a post-signing change.
- `html_to_pdf` — an HTML invoice rendered onto an A5 page.

## Untrusted input

Every allocation sized by a number the file supplies, and nearly every loop it
controls, is capped by default. `Read` takes options to move the caps, and the
`…Context` variants take a deadline. A cap or deadline that stops a validator
is reported as a `limit` finding, never as a pass; one that stops an extraction
is an error. [docs/untrusted-input.md](docs/untrusted-input.md) is the guide.

## Status and limitations

- **Pre-v1.** Releases are tagged, but exported names and signatures may change
  in any release until v1. v0.2.0 through v0.3.1 are retracted (`go.mod` says
  why), so `go get` skips them.
- **Every validator covers a subset of its standard.** The PDF/A validator
  reports no false positives, no missed violations and no parse errors on the
  veraPDF corpus, and misses nothing in the Isartor PDF/A-1b suite; ratchet
  tests fail if that changes. Coverage beyond the corpus is not guaranteed.
- **`Write` regenerates the file layout.** The object model round-trips; object
  order, object streams and the cross-reference form are written afresh.
- **A file that could not be decrypted cannot be changed.** A wrong password or
  an unsupported scheme leaves it `Locked`: its structure parses, its strings
  and streams stay ciphertext, and on `Write` the ciphertext is carried
  unchanged under the preserved `/Encrypt` and `/ID`, and the file layout is
  regenerated.
- **Dependencies** are the author's own modules — forme (shaping, font programs,
  HTML/CSS layout), formalis (EN 16931 invoice rules), golittlecms (ICC
  profiles), gopenjpeg (JPEG 2000) — and `golang.org/x/text`.

## Documentation and contributing

[docs/](docs/README.md) is the index, organised by the question you have; the
godoc is the API reference. [docs/architecture.md](docs/architecture.md#where-the-code-is)
maps the packages and which files hold each subsystem.

Before you push, run `make check`, which runs what CI's build job runs
([testing.md](docs/testing.md#before-you-push) lists the rest). The veraPDF
corpus and the other data sets are not committed; `make corpus` fetches the
corpus, and [CONTRIBUTING.md](CONTRIBUTING.md) covers the corpus ratchet and
adding a rule. `cmd/pdf0` is a small development tool, not the supported
surface ([cli.md](docs/cli.md)).

## License

See [LICENSE](LICENSE). Material from elsewhere, with its source and licence,
is listed in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
