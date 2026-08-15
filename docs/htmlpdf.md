# HTML and CSS to PDF

Three inputs — a document, a stylesheet, a sheet of paper — and a PDF.

```go
out, err := render.Render(render.Input{
    HTML: document,
    CSS:  []render.Stylesheet{{Source: stylesheet}},
}, render.Options{Page: render.A5})
if err != nil {
    return err
}
for _, f := range out.Findings {
    log.Printf("%s: %s", f.Rule, f.Message)
}
if out.Document == nil {
    return errors.New("a rule fired at error severity; nothing was produced")
}
return out.Document.Write(w)
```

`examples/html_to_pdf` is that call with a real invoice around it, and CI runs
it on every push.

## Where the work happens

The layout engine is not in this repository. The HTML parser, the CSS syntax
and cascade, the box model, floats, tables, line breaking and the bidirectional
algorithm are [forme](https://github.com/mgilbir/forme), which has no idea what
a PDF is. What is here is the backend: `render/pdfout.go`, which writes a
display list into a document.

```
forme/html     ─┐
forme/css      ─┤
forme/style    ─┼─▶ forme/layout ─▶ Compose ─▶ []layout.Op ─▶ render.Render ─▶ *pdf0.Document
forme/shape    ─┤                  (build, lay out,          (writePage)
forme/bidi     ─┤                   scale, check, paint)
forme/segment  ─┘
```

`layout.Compose` stops one step short of a document, and that step is the only
one that knows what a document is. A caller who wants the display list rather
than a PDF — to draw onto a canvas, to test, to write some other format — calls
`Compose` directly and never imports this package.

## The API

`render` re-exports the handful of names the call above needs, so the common
case takes one import:

| name | is |
|---|---|
| `Input`, `Stylesheet` | the document and its stylesheets |
| `Options`, `PageSize`, `PageSizePt` | the sheet and the refusal thresholds |
| `A4`, `A5`, `Letter` | named sheets, each with a margin already |
| `Finding`, `Size` | what `Result` carries |

They are **aliases**, not wrappers, so they are the same types
`github.com/mgilbir/forme/layout` declares. A caller who outgrows the list — a
resource resolver for images, a font library, a severity policy, the display
list itself — imports `layout` and finds every value already fits. See
`render/api.go`.

## Two ways it can decline

`Render` returns an error only when writing the document failed. A document
that should not be produced comes back as **`Result.Document == nil` with a
`nil` error**, and the reason is in `Findings`. Check both:

```go
if err != nil { ... }          // the writing failed
if out.Document == nil { ... } // the engine refused, see out.Findings
```

That is deliberate. "This page needs a 3pt font to fit, so I have not made it"
is not an I/O failure, and a caller that only checked `err` would ship a blank
or illegible page. The threshold is `Options.MinScale` and
`Options.MinFontSizePt`; both have defaults, and `Input.Policy` can lower any
rule's severity if a warning is what you want instead.

## Findings

Everything the engine could not do is reported rather than guessed at. A
property it does not implement, a character the face has no glyph for, an image
it was not allowed to load, content that ran off the sheet. `Finding` carries
the rule, a message, a severity, and where in the source it happened.

Two are worth knowing before you meet them:

- **`glyph-missing`** fires at error severity. The fourteen standard PDF faces
  cover WinAnsi and nothing else, so a document with a non-breaking hyphen or a
  Greek letter in it will be refused until you supply a face that has the
  character (`Input.Fonts`) — the alternative is a page silently missing a
  character, and text extraction that silently disagrees with the page.
- **`min-scale`** and **`min-font-size`** are the "made to fit but illegible"
  guards described above.

## Fonts

A `nil` `Input.Fonts` means the fourteen standard faces, which every PDF reader
is required to have and none of which is embedded. That keeps a document small
and is enough for Latin text. For anything else, or to embed, build a font set
and put the faces in it; `@font-face` in the document's own CSS is layered over
whatever the caller supplied.

## Scale to fit

Content wider or taller than the sheet is scaled down rather than cropped, and
`Result.Scale` says by how much. `Result.NaturalSize` is what it wanted before
scaling, which is the number to look at when adjusting a template.
`Options.AllowScaleUp` lets an underfull page be enlarged; it is off by
default, because it is surprising and it degrades images.

## How it is known to work

The layout engine's oracle is the W3C Web Platform Tests, run in forme: **4,438
of 5,177 CSS reftests pass cleanly**, and the number is a ratchet that a change
may not lower. That is a measurement of the engine, not of this backend.

What is checked here is the seam — that the two ends still meet:

- `TestHTMLAndCSSAndAPageSizeMakeAPDF` renders the three inputs, reads the
  document back, and asserts the page size in points, the text in reading
  order, and the stylesheet's effect on that document's own content stream.
- `render/api_test.go` is an external test package importing only
  `pdf0/render`, so a name missing from the alias list stops it compiling.
- CI runs `examples/html_to_pdf` and requires a PDF out of it.
