# HTML and CSS to PDF

Three inputs — a document, a stylesheet, a sheet of paper — and a PDF.

<!-- snippet
var document, stylesheet string
-->
```go
out, err := htmlpdf.Render(htmlpdf.Input{
    HTML: document,
    CSS:  []htmlpdf.Stylesheet{{Source: stylesheet}},
}, htmlpdf.Options{Page: htmlpdf.A5})

// Worth reading whether or not a document came of it.
for _, f := range out.Findings {
    log.Printf("%s: %s", f.Rule, f.Message)
}
if err != nil {
    return err
}
return out.Document.Write(w)
```

`examples/html_to_pdf` is that call with a real invoice around it, and CI runs
it on every push.

The rest of this page is what a caller needs: the error shape, rendering a
document you do not control, supplying fonts and images, and what the
findings say. How the backend writes each display-list operation, and which
ones it refuses, is [htmlpdf-backend.md](htmlpdf-backend.md); the layout
engine itself is forme's ([where the work happens](#where-the-work-happens)).

## One way it can fail

`Render` returns a `Result` and an error, and **the error is non-nil exactly
when `Result.Document` is nil**. There is no state where one says yes and the
other no, so the ordinary shape works and cannot go wrong:

```go
out, err := htmlpdf.Render(in, opts)
if err != nil {
    return err
}
return out.Document.Write(w)
```

Two things can go wrong, and a caller who cares which asks:

```go
var refused *htmlpdf.RefusedError
switch {
case errors.As(err, &refused):
    // The document is wrong: it would only have fitted illegibly, a face has
    // no glyph for a character on the page, or the page asks for something
    // the PDF backend cannot draw. refused.Findings says how.
case err != nil:
    // Building the document failed: an image that could not be embedded, a
    // font whose licence forbids embedding it.
}
```

`Render` writes nothing — it returns a `*pdf0.Document` — so there is no I/O
in it to fail. Writing is `Document.Write`, and its error is `Write`'s.

The thresholds behind a refusal are `Options.MinScale` and
`Options.MinFontSizePt`, both with defaults, and `Input.Policy` can lower any
rule's severity if a warning is what you want instead.

`RefusedError.Findings` may not contain the finding that caused the refusal.
The engine counts a rule the moment it fires and only then tries to record it,
so a document that trips enough rules to fill the report can be refused by one
the limit dropped. `Truncated` — on the error and on the `Result` — says when
the list is partial, and the error message says so too rather than leaving a
reader hunting for a reason that is not there.

A count of a cut list is a floor, and the message says which it is: `and 2
more` when the whole list is in hand, `and at least 499 more; the findings were
cut at the reporting limit` when it is not. A document with two thousand
missing glyphs really does reach this — one paragraph per character, since
`glyph-missing` is reported per run of text.

> **This shape changed.** A refused document used to come back as a nil
> `Document` with a **nil error**, on the reasoning that "this needs a
> three-point font to fit, so I have not made one" is not an I/O failure. The
> reasoning is sound and the shape it produced was not: the second check is not
> where anyone looks, and a caller who wrote the five lines above got a nil
> dereference. A refusal is an error now.

## Documents you do not control

Laying a document out shapes all its text, and a hostile font, or a document
that is simply large, can make that cost more than a caller can afford.
`RenderContext` is `Render` under a context and one budget for all the shaping
a document costs: every run, in every face, measured to break lines or shaped
to be drawn (forme's `layout.ComposeContext`).

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
out, err := htmlpdf.RenderContext(ctx, in, opts, htmlpdf.RunLimits{})
switch {
case errors.Is(err, htmlpdf.ErrRunLimit), errors.Is(err, context.DeadlineExceeded):
    // The document would have cost more than allowed; nothing was made.
case err != nil:
    return err
}
_ = out.ShapingWork // what it cost, for a budget kept across documents
```

A zero field of `RunLimits` is a default that grows with the text: no bound on
a paragraph's length (layout shapes a paragraph as one run), glyphs in
proportion to it, and 64 million units of work plus 1,024 a byte, which admits
the costliest real text with room to spare. A field set is a fixed bound. When
the context is done or the budget runs out, the result is zero: no document,
and no findings from a composition that stopped part way. Otherwise it is
`Render`'s, byte for byte, with `Result.ShapingWork` saying what was charged.

## Supplying fonts and images

A render reads nothing the caller has not handed it. `Input.Fonts` is the
typefaces a document may be set in, and `Input.Resources` is where the files
it names come from: its pictures, its linked stylesheets, its `@font-face`
fonts. Left nil, they are the fourteen standard PDF faces and no files at all.
Both types are forme's (`layout.FontSet`, `layout.ResourceResolver`), so a
caller supplying either imports `github.com/mgilbir/forme/layout`:

<!-- snippet
import "github.com/mgilbir/forme/layout"
import "github.com/mgilbir/forme/shape"
-->
```go
// brandSet is a font set of one typeface in two weights. It answers every
// family a document names, CSS's initial "serif" among them.
type brandSet struct{ regular, bold *shape.Face }

func (s brandSet) Face(family string, bold, italic bool) (*shape.Face, bool) {
	if bold {
		return s.bold, true
	}
	return s.regular, true
}

func renderInvoice(w io.Writer, document string, regularTTF, boldTTF []byte, assetDir string) error {
	// Faces are loaded per render: each records the glyphs it set, and the
	// subset embedded in the PDF is cut from that record.
	regular, err := shape.Load(regularTTF)
	if err != nil {
		return err
	}
	bold, err := shape.Load(boldTTF)
	if err != nil {
		return err
	}
	// Pictures, linked stylesheets and @font-face fonts are read from
	// assetDir and from nowhere else.
	assets, err := layout.NewDirResolver(assetDir)
	if err != nil {
		return err
	}
	defer assets.Close()

	out, err := htmlpdf.Render(htmlpdf.Input{
		HTML:      document,
		Fonts:     brandSet{regular: regular, bold: bold},
		Resources: assets,
		// A logo that did not load is a broken invoice, not a warning.
		Policy: layout.Policy{
			layout.RuleResourceBlocked:  htmlpdf.Error,
			layout.RuleImageUndecodable: htmlpdf.Error,
		},
	}, htmlpdf.Options{Page: htmlpdf.A4})
	if err != nil {
		return err
	}
	return out.Document.Write(w)
}
```

### Fonts

- **A font set** is anything with the one method of `layout.FontSet`:
  `Face(family, bold, italic)`, returning a face and whether the set has one.
  forme's one ready-made set is `layout.StandardFonts`, which is what a nil
  set means; any other set is the caller's own type, like the one above. A
  set that can also offer a face for characters the named one lacks
  implements `layout.FallbackFontSet` as well, and the substitution is
  reported.
- **A face** is forme's `*shape.Face`. `shape.Load` reads a TrueType or
  OpenType program (`shape.LoadCollection` one face of a collection), and
  `notosans.Face` in `github.com/mgilbir/forme/fonts/notosans` is the bundled
  Noto Sans: Latin, Greek, Cyrillic and Devanagari. pdf0's own `fonts.Face`
  carries one as its embedded `Face` field, so a face loaded with
  `fonts.Load` goes in a set as `f.Face`.
- **The set replaces the standard faces**; it does not add to them. A box
  whose families the set does not have is set in the set's face for `serif`,
  CSS's initial `font-family`, with a `font-fallback` warning. A set with no
  `serif` either leaves nothing to set text in, and the document is refused
  (`no-face`). Answering every family, as above, is the simplest set; one that
  wants the standard faces for the families it lacks asks
  `layout.StandardFonts` for them from its own `Face`.
- **Every face but a standard one is embedded**, subset to the glyphs the page
  sets where its licence allows. A face records the glyphs it has set — that
  record is the subset — and is not safe for concurrent use, so give each
  render faces of its own rather than sharing one set between documents. A
  face whose licence forbids embedding fails the render with an ordinary
  error, not a `RefusedError`.
- **`@font-face`** in the document's own CSS is layered over the caller's
  set, its fonts read through `Input.Resources`. One that arrives and does not
  become a face (a container forme does not unwrap, bytes that are not a font
  program) is `font-undecodable`, at warning severity, and the text falls back
  as above.

### Images, stylesheets and webfonts

`Input.Resources` is a `layout.ResourceResolver`, whose one method,
`Resolve(ref)`, returns the bytes of a file the document refers to: an
`<img src>`, a CSS `url()`, a `<link rel=stylesheet>`, an `@import`, an
`@font-face` source. `layout.NewDirResolver` is the one to use. It serves the
files under one directory and nothing else, contained by `os.Root`, so a `..`,
an absolute path or a symbolic link out of the directory is refused by the
kernel rather than by a check on the name. It reads at most 16 MiB a file
(`DirResolver.WithMaxBytes` changes that) and holds the directory open until
`Close`.

- **What never reaches a resolver**: a reference naming a scheme (`https:`,
  `file:`) or a host (`//host/x`). The engine refuses those itself, because a
  render that fetched URLs would be a request-forgery tool. Everything else
  does arrive, `/etc/passwd` and `../secret` included, so a resolver written
  by hand must contain them itself; the `layout.ResourceResolver` godoc says
  what that takes.
- **A resolver's error text is published.** It goes verbatim into a finding,
  and findings are what a document's author is shown; keep paths and host
  names out of it.
- **A picture that does not arrive is not an error.** The `<img>` is laid out
  as its alt text and the render reports `resource-blocked` (no resolver, a
  refused reference, a file that could not be read) or `image-undecodable`
  (it arrived and was not a picture forme decodes, or was larger than it will
  decode), both at warning severity. Where a missing picture is a broken
  document, raise both to `htmlpdf.Error`, as above, and the render is refused
  instead.
- **A picture that arrives** is decoded by forme and written losslessly through
  `images.Embed` ([images.md](images.md#writing-images)): a JPEG is stored as
  its Flate-compressed pixels, not passed through. A picture the page draws
  many times is one image in the file.

## One page

A document is one page. forme composes a document onto a single sheet and
scales it to fit (next section) rather than paginating it, so there is no
second page to write; a document too long to fit legibly is refused by
`min-scale` or `min-font-size`, not continued.

## Scale to fit

Content wider or taller than the sheet is scaled down rather than cropped, and
`Result.Scale` says by how much. `Result.NaturalSize` is what it wanted before
scaling, which is the number to look at when adjusting a template.
`Options.AllowScaleUp` lets an underfull page be enlarged; it is off by
default, because it is surprising and it degrades images.

## Findings

Everything the engine could not do is reported rather than guessed at. A
property it does not implement, a character the face has no glyph for, an image
it was not allowed to load, content that ran off the sheet. `Finding` carries
the rule, a message, a severity, and where in the source it happened.

Two are worth knowing before you meet them:

- **`glyph-missing`** fires at error severity. The fourteen standard PDF faces
  cover WinAnsi and nothing else, so a document with a non-breaking hyphen or a
  Greek letter in it will be refused until you supply a face that has the
  character ([`Input.Fonts`](#fonts)) — the alternative is a page silently missing a
  character, and text extraction that silently disagrees with the page.
- **`min-scale`** and **`min-font-size`** are the "made to fit but illegible"
  guards described above.

The backend adds four rules of its own, for what the display list says and a
PDF page cannot: `backend-vertical-text`, `backend-link-dropped`,
`backend-unknown-op` and `backend-undrawable` (`htmlpdf.RuleVerticalText` and
its siblings). Each is an error by default, so the document is refused, and
each can be lowered like any engine rule —
`layout.Policy{htmlpdf.RuleLinkDropped: htmlpdf.Warn}` gets the document with
the finding, for a caller who can live with the loss. What each one means is
in [htmlpdf-backend.md](htmlpdf-backend.md#what-the-backend-cannot-draw-and-says-so).

## The API

`htmlpdf` holds every name the call above needs, so the common case takes one
import:

| name | is |
|---|---|
| `Render`, `RenderContext` | the call, and the call under a context and a shaping budget |
| `Result`, `RefusedError` | what a render produced, and why it would not |
| `Input`, `Stylesheet` | the document and its stylesheets |
| `Options`, `PageSize`, `PageSizePt` | the sheet and the refusal thresholds |
| `A4`, `A5`, `Letter` | named sheets, each with a margin already |
| `Finding`, `Size` | what `Result` carries |
| `Severity`, `Ignore`, `Warn`, `Error` | how much a finding matters, for reading one or writing a policy |
| `RuleVerticalText`, `RuleLinkDropped`, `RuleUnknownOp`, `RuleUndrawable` | the rules the backend raises itself |
| `RunLimits`, `ErrRunLimit` | the bounds `RenderContext` renders under, and what it fails with past them |

`Render`, `RenderContext`, `Result`, `RefusedError` and the four rules are this
package's own, since what a render produced and why it would not are its
business rather than the engine's.

The rest are **aliases**, not wrappers: the same types and values
`github.com/mgilbir/forme/layout` declares (`RunLimits` and `ErrRunLimit` are
`forme/shape`'s). A caller who outgrows the list — a resource resolver, a font
set, a severity policy (`layout.Policy`), the display list itself — imports
`layout` and finds every value already fits. See `htmlpdf/api.go`.

`Render` and `RenderContext` set forme's `Options.TransformGroups` themselves,
whatever the caller passed: it says the backend draws a rotated or skewed box
through its matrix, which this one does, so it is a fact about the backend and
not a choice about the document.

## Where the work happens

The layout engine is not in this repository. The HTML parser, the CSS syntax
and cascade, the box model, floats, tables, line breaking and the bidirectional
algorithm are [forme](https://github.com/mgilbir/forme), which has no idea what
a PDF is. What is here is the backend: `htmlpdf/pdfout.go`, which writes a
display list into a document.

```
forme/html     ─┐
forme/css      ─┤
forme/style    ─┼─▶ forme/layout ─▶ Compose ─▶ []layout.Op ─▶ htmlpdf.Render ─▶ *pdf0.Document
forme/shape    ─┤                  (build, lay out,          (writePage)
forme/bidi     ─┤                   scale, check, paint)
forme/segment  ─┘
```

`layout.Compose` stops one step short of a document, and that step is the only
one that knows what a document is. A caller who wants the display list rather
than a PDF — to draw onto a canvas, to test, to write some other format — calls
`Compose` directly and never imports this package.
