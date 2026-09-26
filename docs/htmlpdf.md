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

## The API

`htmlpdf` re-exports the handful of names the call above needs, so the common
case takes one import:

| name | is |
|---|---|
| `Input`, `Stylesheet` | the document and its stylesheets |
| `Options`, `PageSize`, `PageSizePt` | the sheet and the refusal thresholds |
| `A4`, `A5`, `Letter` | named sheets, each with a margin already |
| `Finding`, `Size` | what `Result` carries |

`Result` and `RefusedError` are this package's own, since what a render produced
and why it would not are its business rather than the engine's.

They are **aliases**, not wrappers, so they are the same types
`github.com/mgilbir/forme/layout` declares. A caller who outgrows the list — a
resource resolver for images, a font library, a severity policy, the display
list itself — imports `layout` and finds every value already fits. See
`htmlpdf/api.go`.

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

## What the backend cannot draw, and says so

The engine reports what it could not lay out; the backend reports what it
could not *write*. Each of these is a rule with a default severity of `error`,
so the document is refused, and each can be lowered by `Input.Policy` like any
engine rule — `layout.Policy{htmlpdf.RuleLinkDropped: layout.Warn}` gets the
document with the finding, for a caller who can live with the loss:

| rule | what the display list says | why it is refused |
|---|---|---|
| `backend-vertical-text` (`RuleVerticalText`) | a run set down the page: `writing-mode` `vertical-rl`, `vertical-lr`, `sideways-rl`, `sideways-lr`, or `text-orientation: upright` | the glyphs would be drawn across the page, in the wrong place and the wrong way up |
| `backend-link-dropped` (`RuleLinkDropped`) | an `<a href>` | the display list carries no links, so the page would have the link's text and nothing to follow |
| `backend-unknown-op` (`RuleUnknownOp`) | an operation a newer forme added | part of the page would be undrawn |

Every field of every display-list operation is either drawn or refused, and
`drawnFields` in `htmlpdf/pdfout.go` says which; a test holds that list to
forme's types, so a field forme adds fails the tests until the backend
accounts for it.

What is drawn:

- **The sheet** is the one the document was laid out on — `Options.Page` with
  the document's own `@page` rules applied — so `@page { size: 100mm 100mm;
  margin: 0 }` gives a 100 mm page with the content at its corner.
- **Text** is the glyphs layout measured (`layout.ShapedGlyphs`): the run's
  direction, its context either side and the features the document turned off
  (`font-variant-ligatures: none`, `font-kerning: none`) are the ones layout
  used, so the page is the width layout placed. `letter-spacing` goes after
  each typographic character unit, not after each glyph. Every run extracts as
  the text it was set from — a ligature as its letters, a right-to-left word in
  reading order; see [fonts.md](fonts.md#setting-text-and-getting-it-back).
- **Translucency.** A colour's alpha — including the `opacity` layout folds
  into it — is an ExtGState with `/ca` and `/CA`, and a page that uses one is a
  transparency group. A fill at alpha zero is left out; text at alpha zero is
  drawn invisible (render mode 3), so it is still in the page's text, as it is
  selectable in a browser.

## One page

A document is one page. forme composes a document onto a single sheet and
scales it to fit (next section) rather than paginating it, so there is no
second page to write; a document too long to fit legibly is refused by
`min-scale` or `min-font-size`, not continued.

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

The layout engine's oracle is the CSS Working Group reftests, run in forme:
**5,982 of 6,253 documents pass with nothing unsupported reported in either
document**, and the number is a ratchet that a change may not lower. That is a
measurement of the engine, not of this backend.

What is checked here is the seam — that the two ends still meet:

- `TestHTMLAndCSSAndAPageSizeMakeAPDF` renders the three inputs, reads the
  document back, and asserts the page size in points, the text in reading
  order, and the stylesheet's effect on that document's own content stream.
- `htmlpdf/api_test.go` is an external test package importing only
  `pdf0/htmlpdf`, so a name missing from the alias list stops it compiling. It
  also pins the error shape: that a refusal is a `*RefusedError` carrying its
  findings, that the error and the document never disagree, and that checking
  only the error is enough — the last of which panicked under the old shape.
- CI runs `examples/html_to_pdf` and requires a PDF out of it.
