# The htmlpdf backend: what it draws, and what it refuses

This is the reference for `htmlpdf/pdfout.go` and the files beside it: how
each operation of forme's display list becomes PDF, which ones are refused
and why, and what holds the backend to forme's types. Read it before changing
the backend, or when a `backend-*` finding needs explaining. To render a
document — the call, its errors, fonts, images and findings — read
[htmlpdf.md](htmlpdf.md) instead.

## What the backend cannot draw, and says so

The engine reports what it could not lay out; the backend reports what it
could not *write*. Each of these is a rule with a default severity of `error`,
so the document is refused, and each can be lowered by `Input.Policy` like any
engine rule — `layout.Policy{htmlpdf.RuleLinkDropped: htmlpdf.Warn}` gets the
document with the finding, for a caller who can live with the loss:

| rule | what the display list says | why it is refused |
|---|---|---|
| `backend-vertical-text` (`RuleVerticalText`) | a run turned in a way forme does not turn text: `Anticlockwise` or `Upright` without `Sideways`, or `Upright` with `Anticlockwise` | no writing mode produces it (CSS Writing Modes 5.1: `sideways-lr` turns every character, so nothing on it is upright), and the backend does not guess what it means. Every run forme sets down the page — turned either way, or upright in any face — is drawn |
| `backend-link-dropped` (`RuleLinkDropped`) | a link whose target a PDF link cannot carry: a reference left relative to the HTML document (`other.html`, `/a/b`, with no `<base href>` or under one that is a path), a fragment with no such base (`#section`), or a URI [`pdf0.LinkURI`](../annotation.go) refuses | the HTML document's address is not given to the backend, and a PDF reader resolves a relative URI against the PDF's own; the display list does not say where a fragment's target is; so the page would have the link's text and nothing to follow |
| `backend-unknown-op` (`RuleUnknownOp`) | an operation a newer forme added | part of the page would be undrawn |
| `backend-undrawable` (`RuleUndrawable`) | an operation the backend knows and ISO 32000-2 cannot state exactly; the message names the first one and why | the page would show something other than what layout composed |

Every field of every display-list operation is either drawn or refused, and
`drawnFields` in `htmlpdf/pdfout.go` says which;
`TestEveryDrawOpFieldIsAccountedFor` holds that list to forme's types, so a
field forme adds fails the tests until the backend accounts for it.

## What is drawn

Everything else in the display list is written, as follows.

- **The sheet** is the one the document was laid out on — `Options.Page` with
  the document's own `@page` rules applied — so `@page { size: 100mm 100mm;
  margin: 0 }` gives a 100 mm page with the content at its corner.
- **Text** is the glyphs layout measured (`layout.ShapedGlyphs`): the run's
  direction, its context either side and the features the document turned off
  (`font-variant-ligatures: none`, `font-kerning: none`) are the ones layout
  used, so the page is the width layout placed. `letter-spacing` goes after
  each typographic character unit, not after each glyph. Every run extracts as
  the text it was set from — a ligature as its letters, a right-to-left word in
  reading order; see [text.md](text.md#getting-the-text-back).
- **Sideways text.** A run a vertical writing mode lays along the line —
  Latin in `vertical-rl` or `vertical-lr`, everything in `sideways-rl` and
  `sideways-lr` — is a horizontal run turned a quarter, and is drawn as one:
  the same glyphs and displacements, with the text matrix turned clockwise
  (`[0 1 1 0]` in layout's coordinates) or, for `sideways-lr`, anticlockwise
  (`[0 -1 -1 0]`), at the pen position layout gave the run.
- **Upright text.** A character a vertical writing mode stands upright — CJK
  by default, anything under `text-orientation: upright` — is shaped with the
  vertical rules and metrics (`shape.Features.Vertical`: `vert`, and the
  face's `vmtx` and `VORG`) and drawn by `fonts.Face.DrawUpright` in the
  face's vertical form, an `Identity-V` font whose `/W2` states each glyph's
  own vertical metrics, each glyph hung from its vertical origin at the pen
  layout gave it. A face that states vertical metrics is drawn by them, as
  layout measured it. One that states none (Noto Sans, the standard faces) is
  measured by layout at an em a character, CSS Writing Modes 4.4's synthesis,
  and drawn on those em boxes as `layout.ShapedGlyphs` states them: an em
  down the line for each character, each glyph hung where shaping hangs it
  from the top of its box, which centres its ink in the box. A standard face
  has no vertical font, and its glyphs are placed one by one in the
  horizontal one.
  See [text.md](text.md#upright-text-down-the-page).
- **Bitmap and colour faces.** A face whose glyphs are only bitmaps (EBDT,
  bdat, CBDT, sbix) and a face with COLR colour glyphs are drawn as Type 3
  fonts whose glyphs paint forme's image of each one, as
  [font-embedding.md](font-embedding.md#bitmap-only-faces) and
  [font-embedding.md](font-embedding.md#colour-glyphs-colr) describe; the backend draws them
  through the same `fonts.Face` methods as any other face. The content
  stream is in CSS pixels, so a bitmap face's strike is the one a browser at
  1× would draw the text from (see Transforms below for text under a matrix).
- **Rounded corners.** A rounded background or border is a `FillPath` and a
  box that clips to its rounded border a `ClipPath`: lines and arcs of
  axis-aligned ellipses, filled by the even-odd rule (`f*`) and clipped by it
  (`W* n`, around a `q`/`Q`, so the clip cannot outlive what it holds; ISO
  32000-2 8.5.3.3.3, 8.5.4). PDF has no arc, and each arc is written as cubic
  Béziers of at most 45°, within 4.2·10⁻⁶ of the radius of the ellipse — under
  a hundredth of a pixel below a radius of 2,300 px. An arc sweeping past a
  whole turn, or with angles that are not numbers, is refused, and so is a link
  inside a curved clip, since its annotation's rectangle cannot follow the
  curve; forme makes neither.
- **Gradients.** Linear, radial and conic gradients, repeating or not, in a
  background tiled or not, are shadings (ISO 32000-2 8.7.4.5): axial (type 2)
  along the gradient line, radial (type 3) about the centre under a matrix
  that makes CSS's ellipse of PDF's circles, and function-based (type 1) for a
  conic gradient, whose PostScript calculator function (type 4) takes the
  angle with `atan`. The colour is exactly forme's
  `layout.Gradient.ColorAtOffset`: the line the tile reaches is cut at every
  stop and every period into pieces joined by a stitching function (type 3),
  each piece an exponential function (type 2) whose `/N` is the transition
  hint's exponent, or a type 4 function where two stops' alphas differ and the
  blend has to be premultiplied (CSS Color 4 13.4). A gradient CSS
  interpolates in another colour space arrives from forme restated as sRGB
  stops, within 0.4/255, and is drawn as those. The alpha is `/ca` where every
  stop has the same one, and otherwise a luminosity soft mask of the same
  shading over the alphas, in a DeviceGray group so that no colour management
  touches it. A gradient that would take more than 65,536 pieces (4,096 for a
  conic one, each a branch of a program a reader runs per pixel) is refused.
  Ghostscript 10.02 cannot render some of what this writes and ISO 32000-2
  allows, so the file works around it: a type 1 shading states its `/Matrix`,
  has one function per colour component, and every number in a type 4 program
  is at most 15 characters. It still paints a conic gradient with a hard stop,
  or with alphas that differ, with bands of wrong colour across the tile (it
  subdivides the tile into patches it takes to be smooth); Poppler draws them
  as written.
- **Filters.** forme folds into the marks every filter it can apply to them
  exactly and leaves a `FilterGroup` for the rest. `opacity()` is drawn as a
  form XObject that is an isolated transparency group, painted at `/ca` (ISO
  32000-2 11.4, 11.6.4.4), so overlapping marks composite before they fade. A
  `drop-shadow()` with no blur is the group's alpha moved by the offset — an
  alpha soft mask (11.6.5.2) — flooded with the shadow's colour under the
  group. A chain is a group per step. `blur()`, a blurred drop shadow and the
  colour-matrix functions forme could not fold (over a picture, or marks that
  overlap) are refused: PDF has no convolution, and no combination of its
  blend modes and transfer functions is a colour matrix.
- **Transforms.** forme applies a `transform` that keeps rectangles
  rectangles — a move, a scale along the axes, a quarter turn — to the marks
  themselves. Any other 2D transform (`rotate(30deg)`, a skew, a mirror) is a
  `TransformGroup`, which htmlpdf draws by concatenating its matrix (`cm`,
  ISO 32000-2 8.4.4) around the box's marks, after the clip of whatever cuts
  the box from outside; a group inside a group concatenates again. htmlpdf
  sets forme's `Options.TransformGroups` itself, and its own `Options` has no
  such field, since it says what the backend draws. What PDF states against a stream's
  default coordinates rather than the current ones takes the matrix with it:
  a tiling pattern's `/Matrix`, and the bounding box of the form a filter
  draws into, which is the sheet as the group's coordinates see it. A link
  stays a rectangle of the page (forme puts it ahead of the group, the
  rectangle around where the matrix draws it). A matrix that is not
  invertible draws nothing, as CSS Transforms 1 §6 says; one that, with the
  groups around it, scales by more than a millionfold or less than a
  millionth, or moves by more than a billion pixels, is refused
  (`backend-undrawable`), since a reader's numbers do not carry it exactly.
  forme still reports 3D transforms, and a transform on the root or on a
  table's parts, and draws those boxes untransformed. A bitmap font's strike
  is chosen for the size the run is shown at, through the matrix, in the
  direction it stretches most: text at 12px under `rotate(10deg) scale(2)` is
  drawn from the strike a browser draws 24px text from.
- **Text shadows and emphasis marks.** A sharp `text-shadow` is the run's
  glyphs moved by the offset in the shadow's colour, under the run, and a
  `text-emphasis` mark is its character drawn where forme puts it. Neither is
  the document's text: each is marked as an artifact (ISO 32000-2 14.8.2.2)
  and drawn inside an `/ActualText` that says nothing (14.9.4), so the page
  extracts as its words once, with no mark among them. A blurred text shadow is
  refused: PDF has no blur.
- **MathML's stretched operators.** A stretched operator is a size variant of
  its glyph or an assembly of pieces from the font's MATH table — glyphs no
  character maps to, drawn by index (`DrawGlyphs`) through the same glyph-code
  path as all text, each at the offsets forme placed it. The pieces stand for
  the operator's character once, in one `/ActualText`; each piece's ToUnicode
  entry is the character it is a piece of. Each piece is recorded as used when
  it is drawn, so the face is still subset
  (see [text.md](text.md#glyphs-drawn-by-index)).
  A simple or standard face, whose codes are characters, cannot draw a glyph
  by index, and is refused.
- **Squeezed text.** A `text-combine-upright` composition wider than its em,
  which forme squeezes to fit (`DrawText.WidthScale`, CSS Writing Modes
  9.1.3), is written with PDF's horizontal scaling, `Tz` at a hundred times the
  squeeze, which scales the glyphs and every displacement along the run (ISO
  32000-2 9.3.4). Letter-spacing, which forme does not squeeze, is written
  divided by the squeeze so that `Tz` brings it back to what layout measured.
  A squeezed upright run cannot be written this way (`Tz` scales across the
  page and the run advances down it) and is refused; forme makes none.
- **Links.** Each `<a href>` forme lays out is a `Link` in the display list,
  with one area per fragment of the `<a>`: a line of an inline link, the box of
  a block one, an image or inline-block inside one. Each area is a link
  annotation with a URI action, placed through the same transform as the
  drawing, and written by pdf0's own builder (`Page.Links`), so the URI is
  checked against its scheme allowlist, normalised and percent-encoded to
  7-bit ASCII as ISO 32000-2 12.6.4.8 requires. A link broken across lines is
  one annotation per line rather than one with `/QuadPoints`, because a reader
  that does not honour `/QuadPoints` activates the whole `/Rect`, which covers
  the middle of every line between. An href forme will not make a link of
  (`javascript:`, `data:`, `file:` and every scheme but http, https and
  mailto) is reported by forme as `link-refused`, at warning severity, and the
  words are drawn without a link. A relative href is resolved by forme against
  the document's `<base href>` where that is an http or https URL (HTML
  §4.2.3, RFC 3986 §5.2) — `<base href="https://example.com/docs/">` makes
  `intro.html` `https://example.com/docs/intro.html` and `#terms`
  `https://example.com/docs/#terms` — and is written as that URL. One that
  stays relative, and a fragment with no such base, is refused
  (`backend-link-dropped`, above):
  forme is not told the document's own address, and does not say where on
  the page a fragment's target is, so there is no in-document destination
  to write.
- **Translucency.** A colour's alpha — including the `opacity` layout folds
  into it — is an ExtGState with `/ca` and `/CA`, and a page that uses one is a
  transparency group. A fill at alpha zero is left out; text at alpha zero is
  drawn invisible (render mode 3), so it is still in the page's text, as it is
  selectable in a browser.

## How it is known to work

The layout engine's oracle is the CSS Working Group reftests, run in forme as a
ratchet a change may not lower; the current count is in
[forme's README](https://github.com/mgilbir/forme#how-it-is-known-to-be-right).
That is a measurement of the engine, not of this backend.

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
