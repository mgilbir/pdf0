# pdf0 documentation

Start from the question you have.

| I want to… | Read |
|------------|------|
| Decide whether to use pdf0, or find an entry point | [../README.md](../README.md) |
| Build a PDF from scratch, or draw on a page | [building.md](building.md) |
| Find a runnable example of something | [../README.md](../README.md#examples) |
| Understand how a PDF becomes a `Document`, and back | [architecture.md](architecture.md) |
| Find which files hold a subsystem | [architecture.md](architecture.md#where-the-code-is) |
| Pick a validator, or add a rule to one | [validators.md](validators.md) |
| Add or debug a PDF/A rule | [pdfa.md](pdfa.md) |
| Work on accessibility (PDF/UA) checking | [pdfua.md](pdfua.md) |
| Turn HTML and CSS into a PDF, with your own fonts and images | [htmlpdf.md](htmlpdf.md) |
| Work on the HTML backend, or explain a `backend-*` finding | [htmlpdf-backend.md](htmlpdf-backend.md) |
| Set text with a font, or embed a font | [text.md](text.md) |
| Know what the font writer puts in a file, or change it | [font-embedding.md](font-embedding.md) |
| Understand a font finding, or touch font-program parsing | [fonts.md](fonts.md) |
| Work on XMP metadata or a conformance declaration | [xmp.md](xmp.md) |
| Read, write or debug encrypted files | [encryption.md](encryption.md) |
| Sign a PDF, or decide whether a signature can be trusted | [signing.md](signing.md) |
| Pull images out of a PDF, or fix a codec | [images.md](images.md) |
| Embed an image in a page | [images.md](images.md#writing-images) |
| Use the `cmd/pdf0` dev tool | [cli.md](cli.md) |
| Read untrusted PDFs safely: cap cost, set deadlines, read limit findings | [untrusted-input.md](untrusted-input.md) |
| Cap what one untrusted document may cost | [untrusted-input.md](untrusted-input.md#capping-what-one-document-costs) |
| Stop work under a deadline, and know what a cancelled run returns | [untrusted-input.md](untrusted-input.md#bounding-time-the-context-variants) |
| Understand what happens when a resource guard trips | [limits.md](limits.md) |
| Work out why something failed | [troubleshooting.md](troubleshooting.md) |
| Run what CI runs before you push | [testing.md](testing.md#before-you-push) |
| Run the tests that a fresh clone skips | [testing.md](testing.md) |
| Contribute a change | [../CONTRIBUTING.md](../CONTRIBUTING.md) |
| Know why something was built the way it was | [adr/](adr/README.md) |
| Read the design record behind a feature | [design records](#design-records) |
| Read a past findings report | [audits/](audits/README.md) |
| See what changes in the next release, or record a change your pull request makes | [releases/unreleased.md](releases/unreleased.md) |

The per-symbol API reference is the godoc: `go doc github.com/mgilbir/pdf0`, or
[pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/pdf0).

## What each doc is for

**[building.md](building.md)** — tutorial plus how-to. One page from nothing,
then drawing, images, forms, document info, outlines, links, the structure
tree, gradients and patterns, transparency and soft masks, PDF/A, Factur-X,
and what makes `AddPage` refuse a page.

**[architecture.md](architecture.md)** — explanation. The packages and how
they depend on each other, which files hold each subsystem, the Document
lifecycle, the object model, the Read pipeline and its recovery ladder, the
Write pipeline. Read before changing the parser or serializer.

**[validators.md](validators.md)** — reference plus explanation. Every standard,
which entry point returns what, how to read a result (checker findings, what an
empty result is worth), and how the composite validators — PDF/VT, Factur-X and
Order-X — are put together.

**[signing.md](signing.md)** — how-to plus reference. Opens with which verdict to
read, because that is the question with a wrong answer that compiles.

**[images.md](images.md)** — reference plus explanation. The extraction API and
its memory contract, writing images into a document, the codec dispatch, the
resource budgets.

**[pdfa.md](pdfa.md)** — explanation. Inside the PDF/A engine: how a run is
dispatched, the anatomy of a rule, where each rule lives, the executed-content
model, the per-run memos, and how one rule body serves every level.

**[pdfua.md](pdfua.md)** — explanation. The structure tree, the UA rule
families, table-grid reconstruction, and how much a clean result is worth.

**[htmlpdf.md](htmlpdf.md)** — how-to plus reference. The three inputs and the
one call, how a render fails, rendering a document you do not control,
supplying fonts and images, fitting content to one page, and the findings worth
knowing about before you meet them.

**[htmlpdf-backend.md](htmlpdf-backend.md)** — reference plus explanation, for
contributors. What the backend draws for each display-list operation, what it
refuses and why, and how it is held to forme.

**[text.md](text.md)** — how-to. Load a face, draw with it, embed it after
the drawing, bound shaping of untrusted text, the font licence rule, PDF/A,
vertical text, getting the text back, and which PDF font each face becomes.

**[font-embedding.md](font-embedding.md)** — reference plus explanation. What
the font writer puts in a file and why: `/CIDSet`, CID-keyed CFF, variable
fonts and CFF2, PDF/A-1, bitmap and colour glyphs as Type 3 fonts, cost.

**[fonts.md](fonts.md)** — explanation. Font validation: font types and what
each requires, font-program parsing, encodings and CMaps, the CIDSet and
CharSet rules, and the empty-vs-missing-glyph trap.

**[xmp.md](xmp.md)** — explanation. Packet parsing and encodings, the
conformance declarations every standard writes there, and schema validation.

**[encryption.md](encryption.md)** — reference plus explanation. `Encrypted` vs
`Locked()`, what is supported, passwords, key derivation, crypt filters, and the
security notes on what is not guaranteed.

**[untrusted-input.md](untrusted-input.md)** — how-to. Reading files you did
not write: the options that cap what a document costs and the `…Context`
variants that bound how long it takes, both as tables generated from the code;
what a `limit` finding means; and the errors extraction returns instead.

**[limits.md](limits.md)** — reference plus explanation. What a `limit`
finding tells you, how a trip reaches the caller, every resource guard in the
package classified loud / silently lossy / silently wrong, and what each
truncated value feeds into. The design record behind the options is
[proposals/configurable-limits.md](proposals/configurable-limits.md).

**[cli.md](cli.md)** — reference for `cmd/pdf0`, a small command-line front end
used mainly for poking at files during development. pdf0 is a library first; this
tool is not the supported surface and reaches only a fraction of the API. The doc
records every command, flag and exit code with real captured output, and what the
tool does not expose.

**[troubleshooting.md](troubleshooting.md)** — how-to, organised by symptom.

**[testing.md](testing.md)** — reference. What to run before you push, the
test tiers, the external data sets and how to fetch them, the tools and
environment variables, the fuzzers, the developer aids, and what CI does and
does not check.

**[adr/](adr/README.md)** — explanation. Decisions that keep being
re-litigated, with the rejected alternative recorded — among them why limits
are options and deadlines are `…Context` variants
([0009](adr/0009-functional-options.md), [0010](adr/0010-context-variants.md)),
why subsystems see a `core.View` ([0011](adr/0011-view-and-bridge.md)), and why
shaping and layout live in forme
([0017](adr/0017-shaping-and-layout-in-forme.md)).

**[releases/unreleased.md](releases/unreleased.md)** — reference. The draft
of the next release's notes: breaking changes with their migrations, behaviour
changes, additions and fixes, each added by the pull request that makes it.

**[audits/](audits/README.md)** — historical findings reports and the live plans
that work them off. Point-in-time snapshots, not a description of the current
code.

## Design records

The records under `proposals/` say what was intended and why. Each opens with
a status header saying what was built and where the build differs from the
record; the doc it names describes the code as it is.

- **[proposals/html-css-rendering.md](proposals/html-css-rendering.md)** —
  rendering HTML and CSS into a PDF page. It is done: the engine was built
  through its phases and moved to [forme](https://github.com/mgilbir/forme);
  what pdf0 keeps is the backend, [htmlpdf.md](htmlpdf.md).
- **[proposals/bitmap-fonts-type3.md](proposals/bitmap-fonts-type3.md)** —
  writing bitmap-only faces as Type 3 fonts. Implemented; what was built is in
  [font-embedding.md](font-embedding.md#bitmap-only-faces).
- **[proposals/configurable-limits.md](proposals/configurable-limits.md)** —
  the resource-limit options. Implemented; the live description is
  [untrusted-input.md](untrusted-input.md).
