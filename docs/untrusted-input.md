# Reading untrusted PDFs safely

How to run pdf0 over files you did not write — an upload endpoint, a mail
gateway, a crawler — without letting one file cost you more than you meant to
spend, and how to read the answers honestly when pdf0 stopped short. pdf0 is
already bounded if you configure nothing: every loop and allocation a file
controls is capped by a default chosen for hostile input. This guide is for
moving those bounds, putting a deadline on the work, and telling "the file is
bad" from "pdf0 did not finish".

## In short

```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()

doc, err := pdf0.ReadContext(ctx, r, size,
	pdf0.WithMaxDecodedStreamBytes(48<<20),   // decompression-bomb ceiling
	pdf0.WithMaxDecodedContentBytes(256<<20), // whole-run content budget
)
if err != nil {
	return err // too damaged to read, an invalid option, or the deadline
}
if doc.Locked() {
	return doc.LockReason() // ciphertext: nothing in it can be checked
}

var failed, unknown []pdf0.Violation
for _, v := range pdf0.ValidatePDFAContext(ctx, doc, pdfa.PDFA2b) {
	if pdf0.IsCheckerFinding(v) {
		unknown = append(unknown, v) // pdf0 could not finish this part
	} else {
		failed = append(failed, v)
	}
}
```

Four rules carry most of it:

- **Limits are options on `Read`**, and the `Document` keeps them: every
  validation and extraction run on it inherits what `Read` was given. See
  [Capping what one document costs](#capping-what-one-document-costs).
- **Deadlines are `…Context` variants**, never an option. See
  [Bounding time](#bounding-time-the-context-variants).
- **A `limit` or `internal` finding means "unknown", not "failed".** A
  validation that stopped short always carries one, so an empty result is never
  a stopped run. Filter with `IsCheckerFinding`. See
  [Reading a `limit` finding](#reading-a-limit-finding).
- **Extraction reports a stop as an error**, matching `ErrResourceLimit` when a
  budget stopped it. Use `ExtractTextContext`, `ImagesContext` or
  `ExtractImagesContext`; `ExtractImages` and `Images` cannot report one. See
  [Extraction](#extraction-errors-not-findings).

## Capping what one document costs

The limits worth tuning are variadic options on `Read`, `ReadWithPassword`,
their `…Context` variants and `ParseXRefStream`. They resolve once, at the entry
point, and are stored on the returned `Document`, so they also govern what
`Read` does with the file's own cross-reference streams, and any PDF embedded
in it that PDF/A-4 clause 6.9 makes pdf0 read and validate. Two documents read
with different limits never interfere, and one `Document` may be validated from
several goroutines at once.

The options, generated from their documentation in `limits.go`:

<!-- BEGIN GENERATED: options. From the With* functions in limits.go by TestGeneratedDocSections; regenerate with `go test ./internal/lint -run TestGeneratedDocSections -update`. -->

| Option | Caps | Default |
|---|---|---|
| `WithMaxDecodedStreamBytes` | the decompressed size of any single stream | 100 MiB |
| `WithMaxDecodedContentBytes` | the total decoded content one validation run will materialize | 512 MiB |
| `WithMaxObjectStreamBytes` | the memory Read may spend on the objects it unpacks from object streams in one document: the parser's estimate of every object it builds (syntax.MaterialCost, twice the measured live cost), plus the decoded bytes of the container being unpacked | 512 MiB |
| `WithMaxContentStreamBytes` | the decoded size of a single content stream or image sample buffer that will be scanned | 64 MiB |
| `WithMaxICCProfileBytes` | the decoded size of an ICC profile | 8 MiB |
| `WithMaxXMPPacketBytes` | the size of an XMP packet that the property checks will build a node tree for | 4 MiB |
| `WithMaxTableGridFills` | the number of grid slots the PDF/UA table checks will fill for one table, bounding a cell whose /RowSpan and /ColSpan claim a multi-million-slot area | 1<<24 |
| `WithMaxCmapWork` | the work spent expanding one TrueType cmap subtable of an expanding format — 4 or 12 | 1<<18 |
| `WithMaxWork` | the work one run may do | 2^28 units plus 512 for every byte of the file the document was read from |
| `WithMaxImagePixels` | the size of an image that extraction will decode | 1<<26 |

<!-- END GENERATED: options -->

**Every value must be positive.** `Read` (and `ParseXRefStream`) refuses 0 or a
negative value, before reading anything, with an error wrapping
`ErrInvalidOption`. For no practical limit pass the type's maximum
(`math.MaxInt`, or `math.MaxInt64` for the `int64` options), which is honoured
as written.

**Lowering a limit.** Each option's godoc gives the largest real value measured
for it, where one was measured; a cap below that refuses real documents. The
first measurements were taken over the veraPDF corpus (2,907 files) and a
978-file Common Crawl sample; the later ones, for `WithMaxObjectStreamBytes`
and `WithMaxWork`, over the veraPDF corpus, the Factur-X, WTPDF and PDF/VT
suites and a 1000-file block of Common Crawl. Each godoc names the set its
figure comes from. `WithMaxTableGridFills`, `WithMaxCmapWork` and
`WithMaxImagePixels` have no real-world maximum measured at all: lower them
only against measurements of your own documents.

**What a limit does when it is reached** depends on who reached it. A
validator reports it as a finding under the rule `limit`, naming the guard and
saying whether the bound was pdf0's default or yours
([below](#reading-a-limit-finding)). An extractor returns an error. `Read`
refuses only an option value; what it declines to unpack under a limit it
records, and every later validation reports it.

What is **not** a knob, and why:

- **The write-side object-stream cap** derives from
  `WithMaxDecodedStreamBytes`, so a container `Write` emits is one the same
  configuration can read back.
- **The depth caps** (the parser's, the page tree's, the serializer's, the
  64-hop `Resolve` bound, the walk depth `core.MaxWalkDepth`) guard the
  goroutine stack, whose exhaustion is a fatal error no `recover` can catch.
- **JBIG2's ceilings.** `WithMaxImagePixels` sets JBIG2's per-bitmap bound, and
  four times as much across one stream, but only up to the decoder's own
  ceilings of 2^26 pixels per bitmap and 2^28 per stream: it can lower JBIG2's
  bound and cannot raise it.
- **The lexer's token-gap bound**, which surfaces as a parse error.

The argument for options over a struct or a context is
[ADR 0009](adr/0009-functional-options.md); the measurements, and the cost of
each limit left internal, are in the design record
[proposals/configurable-limits.md](proposals/configurable-limits.md).

## Bounding time: the `…Context` variants

A limit answers "how much may one document cost"; a deadline answers "I need an
answer within five seconds, whatever it costs". Abandoning a goroutine does not
stop its work — it keeps a core and its memory until it finishes by itself —
so the entry points whose cost is the document's take a `context.Context`.
Every one, generated from the exported API:

<!-- BEGIN GENERATED: contexts. From the exported API of the public packages, type-checked by TestGeneratedDocSections; regenerate with `go test ./internal/lint -run TestGeneratedDocSections -update`. -->

| Entry point | Without a context | Returns |
|---|---|---|
| `ReadContext` | `Read` | `(*pdf0.Document, error)` |
| `ReadWithPasswordContext` | `ReadWithPassword` | `(*pdf0.Document, error)` |
| `ValidateDPartsContext` | `ValidateDParts` | `[]dpart.Violation` |
| `ValidateFacturXContext` | `ValidateFacturX` | `facturx.Result` |
| `ValidateOrderXContext` | `ValidateOrderX` | `facturx.OrderXResult` |
| `ValidatePDFAContext` | `ValidatePDFA` | `[]pdfa.Violation` |
| `ValidatePDFRContext` | `ValidatePDFR` | `[]pdfr.Violation` |
| `ValidatePDFUA2Context` | `ValidatePDFUA2` | `[]pdfua.Violation` |
| `ValidatePDFUAContext` | `ValidatePDFUA` | `[]pdfua.Violation` |
| `ValidatePDFVT2Context` | `ValidatePDFVT2` | `[]pdfvt.Violation` |
| `ValidatePDFVTContext` | `ValidatePDFVT` | `[]pdfvt.Violation` |
| `ValidatePDFXContext` | `ValidatePDFX` | `[]pdfx.Violation` |
| `Document.ExtractImagesContext` | `Document.ExtractImages` | `([]images.ExtractedImage, error)` |
| `Document.ExtractTextContext` | `Document.ExtractText` | `(string, error)` |
| `Document.ImagesContext` | `Document.Images` | `iter.Seq2[images.ExtractedImage, error]` |
| `Document.SaveContext` | `Document.Save` | `error` |
| `Document.StreamDataContext` | `Document.StreamData` | `([]byte, error)` |
| `Document.WriteContext` | `Document.Write` | `error` |
| `facturx.Result.ValidateInvoiceXML` | — | `(formalis.Report, error)` |
| `fonts.Face.DrawShapedContext` | `fonts.Face.DrawShaped` | `(int, error)` |
| `fonts.Face.ShapeContext` | `fonts.Face.Shape` | `([]content.TextSpan, int, error)` |
| `fonts.Face.ShapeWithContext` | `fonts.Face.ShapeWith` | `([]content.TextSpan, int, error)` |
| `htmlpdf.RenderContext` | `htmlpdf.Render` | `(htmlpdf.Result, error)` |
| `htmlpdf.RenderIntoContext` | `htmlpdf.RenderInto` | `(htmlpdf.Result, error)` |

<!-- END GENERATED: contexts -->

How each reports that it was stopped follows from what it returns:

| Entry points | A stop is reported as |
|---|---|
| the root package's validators | the findings gathered so far, plus one under the rule `limit` whose message names `context-canceled`. A stopped validation is never an empty result. |
| `ReadContext`, `ReadWithPasswordContext` | a nil `Document` and an error wrapping `ctx.Err()`, never a partial document ([ADR 0016](adr/0016-readcontext-returns-nil.md)). |
| `Document.WriteContext` | an error wrapping `ctx.Err()`. What was written stays written, so write to a temporary file and rename on success. A cancelled `Document.SaveContext` writes nothing at all. |
| `Document.ExtractTextContext`, `Document.ExtractImagesContext` | the text or images reached, and an error wrapping `ctx.Err()`. |
| `Document.ImagesContext` | one last yield, of a zero image and the error. |
| `Document.StreamDataContext`, `htmlpdf.RenderContext`, `fonts.Face.ShapeContext` and its siblings | an error, and no partial result. |
| `facturx.Result.ValidateInvoiceXML` | formalis' report, carrying a violation under its own reserved `formalis.RuleLimit` — the same string, `limit`. |

The errors wrap the context's error, so `errors.Is(err, context.DeadlineExceeded)`
and `errors.Is(err, context.Canceled)` tell the two apart. On a 71 MB,
1256-page file whose full PDF/A validation takes 10.7 s, cancellation takes
effect in about 60 ms.

Deliberately without a variant: structural walks over objects already in memory
(`PageList`, `PageCount`, `Resolve`, `object.Equal`, `DocumentEqual`,
`Repair`, `ExtractPages`, `AppendPages`); `WriteIncremental` and
`SetEncryption`, bounded by what changed; `ExtractPageText`, where one page is
the unit of work and the caller's own loop is the place to check a context;
and signature verification (`VerifySignatures`, `ValidatePAdES`, the
`WriteSigned…` writers), which is linear in the file and puts a work budget on
the one comparison that is not. Why a variant and not an option:
[ADR 0010](adr/0010-context-variants.md).

## Reading a `limit` finding

A validator that could not finish a check does not guess. It reports the
shortfall as a finding under one of two reserved rule identifiers, both of which
`IsCheckerFinding` reports:

- **`internal`** — a check panicked and was recovered so the rest could run. It
  is a bug in pdf0, not in the file.
- **`limit`** — a check could not be completed. Its message names the guard
  that stopped it, in parentheses, and for a configurable bound whether that
  bound was pdf0's default or one the caller set. The message's opening words
  say which kind of guard it was:

<!-- messages -->
```
resource limit reached (<guard>): …; the checks that depend on it were skipped, so this file is neither confirmed conformant nor non-conformant in that respect
the run was cancelled before it finished (<guard>): …  # context-canceled
no file to check (<guard>): …  # no-source-file
not decrypted (<guard>): …  # not-decrypted
data not carried (<guard>): …  # predefined-cmap
not implemented (<guard>): …  # unsupported-filter
data could not be read (<guard>): …  # embedded-cmap
```

A `limit` finding is not, on its own, a sign of a hostile file. The guards are
of two kinds:

- **Resource budgets** — the work meter, the size and depth caps, the content
  and object-stream budgets. Under the defaults one trips on adversarial or
  unusually large input; it also trips whenever you lowered the bound, and the
  message then says the bound was configured by the caller. If the bound was
  yours, raise it for that file; if it was the default, decide whether a file
  this unusual is worth spending more on.
- **Coverage gaps**, which ordinary documents and callers produce, and which no
  option moves:
  - `no-source-file`: a document built or edited in memory and validated before
    it was written has no file for the byte-level rules. Write it and read it
    back to check them.
  - `not-decrypted`: the document is Locked, or some of its objects did not
    decrypt. Supply the password with `ReadWithPassword`.
  - `predefined-cmap`: a font uses a predefined CJK CMap whose data this module
    does not carry, as one file of the veraPDF Isartor suite does.
  - `unsupported-filter`: a stream uses a filter pdf0 does not implement.
  - `context-canceled`: the run's context ended.

Composite validators keep the reserved names bare: a PDF/VT, Factur-X or
Order-X result never carries `pdfx-4/limit` or `pdfa-3/limit`, so one filter
covers every validator. Every guard, what it bounds and what it used to get
wrong is in [limits.md](limits.md#the-inventory).

## Extraction: errors, not findings

The extractors have no finding channel, so they report a shortfall as an error
beside the partial result, and the result is complete exactly when the error is
nil.

- **Text.** `ExtractText` and `ExtractTextContext` leave out a page whose text
  they cannot complete, and report it as a `*PageTextError` naming the page: a
  resource limit (its message names the guard), content pdf0 declined to read
  (over a size limit, in a filter it does not implement, or data that did not
  decrypt), or a recovered panic. When the work budget runs out, the page it
  stopped in is reported, and one more error covers every page after it. The
  other pages' text is returned, still separated by form feeds.
- **Images.** `ImagesContext` yields each image lazily and, if it stopped
  before reaching every image, one last zero image with the error.
  `ExtractImagesContext` returns the images reached and the error.
  `ExtractImages` (deprecated) and `Images` have no error result: on a spent
  budget they stop silently with the images reached so far. An image that was
  reached but not decoded — over `WithMaxImagePixels`, an unsupported codec,
  corrupt data — is not an error: it comes back with `Decoded` false and a
  `Note` saying why.
- **One stream.** `StreamData` and `StreamDataContext` refuse a stream that
  decodes to more than `WithMaxDecodedStreamBytes`.
- **A Locked document.** Every extractor with an error result returns one
  wrapping `LockReason`, and no content.

`errors.Is(err, pdf0.ErrResourceLimit)` is true for a stop a resource limit
caused, and not for a cancelled context, a Locked document or an image left
undecoded.

```go
text, err := doc.ExtractTextContext(ctx)
var pe *pdf0.PageTextError
switch {
case err == nil:
	// every page's text is in text
case errors.Is(err, context.DeadlineExceeded):
	// text holds the pages finished before the deadline
case errors.Is(err, pdf0.ErrResourceLimit):
	// a budget stopped it; the message names the guard
case errors.As(err, &pe):
	// page pe.Page was left out; pe.Err says why
}

for img, err := range doc.ImagesContext(ctx) {
	if err != nil {
		return err // the images before it are all that were reached
	}
	if !img.Decoded {
		log.Printf("image not decoded: %s", img.Note)
	}
}
```
