# Unreleased

The running draft of the next release's notes. The pull request that changes
the API or a behaviour a caller could notice adds its entry here, in the
section it belongs to, as it should read to a user. When a tag is cut, this
file is emptied into the GitHub release notes (summary and what changed are
written then, from these entries) and starts again empty.

A removed or renamed identifier is written in **bold**, not in backticks: the
docs lint resolves every backticked name against the tree as it is, and a
removed one is no longer in it. Before tagging, check the Breaking section
against what gorelease reports, run from a clean checkout of the commit to be
tagged (it refuses a tree with untracked files):

```sh
go run golang.org/x/exp/cmd/gorelease@latest -base=v0.5.0
```

## Breaking changes

`gorelease -base=v0.5.0` reports the incompatible changes below and suggests
v0.6.0.

### `pdfx`

- **The zero `pdfx.Level` is `pdfx.LevelDeclared`, not `pdfx.PDFX4`, and every
  level constant's value moved up by one** (#346) (`pdfx.PDFX4` was 0 and is 1,
  `pdfx.PDFX4p` 1 → 2, `pdfx.PDFX1a` 2 → 3, `pdfx.PDFX3` 3 → 4, `pdfx.PDFX6`
  4 → 5). A level left unset
  used to validate PDF/X-4 whatever the file was; it now validates the level
  the document's own `GTS_PDFXVersion` declares, and a document declaring none
  gets a `limit` finding instead of PDF/X-4 results.
  *Migration:* name `pdfx.PDFX4` wherever an unset or zero `pdfx.Level` meant
  PDF/X-4 — a struct field never assigned, a `var level pdfx.Level`, a
  converted `0`. Code that names the constants is unaffected; a level stored or
  sent as an integer must be re-mapped (add one), or stored by name instead.

### `pdfua`

- **RunCheck and WithPart are unexported** (#336). They were exported only for
  the root package's use. *Migration:* a caller that ran its own check through
  RunCheck recovers its own panics; one that used WithPart sets
  `pdfua.Violation.Part` directly.

### `sign`

- **BuildSignedDataFull, BuildTimestampToken, JoinFieldName and VerifyCMS are
  unexported** (#333). They were the root package's signing plumbing, not a
  supported API. *Migration:* sign with `Document.WriteSigned`,
  `Document.WriteSignedIncremental` or `Document.WriteArchivalTimestamp`, and
  verify with `Document.VerifySignatures` or `Document.ValidatePAdES`. What
  VerifyCMS checked — the CMS signature over its content, and nothing about
  trust — is `sign.Result.Valid`, made over the signature's byte range, with
  trust reported beside it in `TrustedChain`, `ChainErr` and `Revocation`.

### `htmlpdf`

- **htmlpdf.Options is its own struct, not an alias of forme's layout.Options,
  and has no TransformGroups field** (#364). `layout.Options` describes the
  backend as well as the document, and htmlpdf overwrote the field it
  described the backend with whatever a caller passed. `Render` and
  `RenderContext` take the new type. *Migration:* a literal such as
  `htmlpdf.Options{Page: htmlpdf.A4}` compiles unchanged: `Page`, `MinScale`,
  `MinFontSizePt` and `AllowScaleUp` keep their names and meanings. Code that
  passes a `layout.Options` value to `Render` or `RenderContext` builds an
  `htmlpdf.Options` from the same four fields. Code that set TransformGroups
  drops it; it had no effect.

### Behaviour that breaks callers (not reported by gorelease)

- **pdf0 needs Go 1.27** (#365). go.mod's `go` line is 1.27, up from 1.26, and
  its `toolchain` line, which CI builds and tests with, is go1.27.3. forme's
  main branch names Go 1.27, so its next release does too, and pdf0 has to
  match to take it.
  *Migration:* build with Go 1.27 or later. The `toolchain` line has no effect
  on a module that imports pdf0.

- **`Write`, `WriteContext`, `Save` and `SaveContext` refuse a signed
  document.** (#347) A document read from a file that carries a signature or document
  time-stamp is refused with an error wrapping `ErrWouldInvalidateSignatures`,
  and nothing is written. These writers regenerate the whole file, which moves
  every byte a signature covers: they used to return nil and write a file whose
  signatures no longer verified, even when nothing had changed. A document
  built in memory is unaffected.
  *Migration:* to keep the signatures, write the changes as an incremental
  update with `WriteIncremental` (add signatures and time-stamps with
  `WriteSignedIncremental` and `WriteArchivalTimestamp`). To rewrite the file
  and discard them on purpose, call `AllowInvalidatingSignatures` on the
  document first.
- **`SetEncryption` refuses a signed document** (#347), with an error wrapping
  `ErrWouldInvalidateSignatures`, and changes nothing. Encryption can only be
  applied by rewriting the whole file, and an incremental update cannot add it,
  so "sign, then encrypt" produced a file whose signature did not verify.
  *Migration:* call `AllowInvalidatingSignatures` first to encrypt it anyway;
  pdf0 cannot produce a file that is both encrypted and validly signed.
- **The incremental writers refuse a document read from an encrypted file,
  even after `RemoveEncryption`.** (#347) `WriteIncremental`,
  `WriteSignedIncremental` and `WriteArchivalTimestamp` checked only whether
  the document was still encrypted (see Fixed).
  *Migration:* write the decrypted document whole with `Write`; if it is
  signed, that needs `AllowInvalidatingSignatures` too.

### Deprecated, not removed

- **`Document.ExtractImages` is deprecated** (#335). It has no error result,
  so a short slice could not say it was short. Use `ExtractImagesContext`, the
  same slice with an error saying why it stopped, or the new `ImagesContext`,
  which iterates lazily with at most one decoded image live.

## Behaviour changes

### PDF/A documents are dated

- **Every document from `NewPDFADocument`, `NewPDFADocumentWithInfo` and
  `NewPDFADocumentWith` carries `xmp:CreateDate`** (#367), the moment it was
  built unless `SkeletonOptions.Created` says otherwise. No PDF/A level
  requires one, but an archived document without a date is less use. A build
  that must be byte-identical pins `Created` along with `FileID`; with only
  `FileID` pinned, two builds now differ in the date.

### htmlpdf

- **forme v0.11.0** (#366). A page refused for a font nothing supplies now gets
  one `no-face` finding per face to supply. Each names the families the
  element asked for and points at that element, and `Finding.Font` holds the
  families, weight, style and characters as a `layout.FontNeed`. Before, one
  finding per document quoted "serif" and pointed at nothing. Together with
  `Options.EmbedAllFonts` (#364), a caller learns exactly which faces to
  supply. forme's layout fixes also change some pages: segment breaks across
  element boundaries, the letter-spacing at the end of aligned and
  right-to-left lines, and floats around formatting characters.

### Validation

- **`ValidatePDFX` with an unset level validates what the document declares** (#346)
  (see Breaking changes). The declaration is the XMP
  `pdfxid:GTS_PDFXVersion` when it names a level, and otherwise the Info
  dictionary's `/GTS_PDFXVersion` (where PDF/X-1a and -3 identify); an Info-only
  "PDF/X-4" is held to PDF/X-4, whose identification rule then reports it.
- **PDF/UA rules read as veraPDF reads them** (#337). On the veraPDF corpus
  this removes every false positive on its pass files and catches more fail
  files ([pdfua.md](../pdfua.md)). What a caller sees change:
  - PDF/UA-1 no longer requires a catalog `/Lang`. It requires that each
    text's language be determinable: from the catalog, or a `/Lang` on its
    structure element or marked content. `ValidatePDFUA2` still requires the
    catalog entry.
  - `/Lang` values are checked as RFC 3066 language tags, property-list
    `/Lang` values included.
  - An annotation's description may come from its enclosing structure
    element's `/Alt`; an annotation outside the crop box is exempt; the
    annotation-description rules of 7.18.1 and 7.18.5 are PDF/UA-1's only.
  - An empty `/ActualText` satisfies the Figure rule (7.3).
  - PDF/UA-1 adds the Formula rule (7.7): a Formula element needs a non-empty
    `/Alt` or an `/ActualText`, an empty one included.
  - A composite font with an Adobe-GB1, CNS1, Japan1 or Korea1 collection needs
    no ToUnicode.
  - Table headers are checked from the data cells (`/Headers` naming header
    IDs; for PDF/UA-2 a positional search; `/Scope` as written), not by
    requiring each TH to carry a scope or an ID.
  - Untagged paths, inline images, image XObjects and what a form paints are
    now real content that must be tagged or an artifact; shadings are not.
  - PDF/UA-2 adds 8.2.5.20: Link annotations with different structure
    destinations in one structure element.
- **A nested `internal` finding from PDF/VT keeps its reserved name** (#336).
  A panic recovered inside the PDF/X or DPart run that PDF/VT composes came out
  as `pdfx-4/internal` or `dpart/internal`, which `IsCheckerFinding` did not
  recognise; it is now `internal`.
- **An older cross-reference section that `Read` declined to decode is
  reported** (#334). One over the decoded-stream limit, or under a filter pdf0
  does not implement, used to cut the chain of updates silently; it is now a
  read-time trip, reported by every validator as a `limit` finding.
- **`ValidatePDFUA` and `ValidatePDFUA2` check two more font rules** (#349), so a
  document that passed may now get findings: 7.21.5 (part 2: 8.4.5.6), a shown
  glyph whose declared width differs from the embedded program's by more than
  1/1000 (only text in rendering mode 3 is exempt); and 7.21.6, a non-symbolic
  TrueType font whose `/Differences` names a glyph outside the Adobe Glyph
  List, or whose embedded program has no (3,1) cmap subtable. Both read the
  font as veraPDF does. PDF/A behaviour is unchanged.
- **`ValidatePDFUA2` checks the ISO 14289-2 rules only part 2 has** (#350):
  `pdfuaid:rev` is `2024` under the `pdfuaid` prefix (5); one `Document` root
  in the PDF 2.0 namespace (8.2.5.2); no `H` (8.2.5.12); a list with `Lbl`
  items has a `ListNumbering` other than `None` (8.2.5.25); intra-document
  destinations and GoTo actions are structure destinations (8.8); an invisible
  annotation in the structure tree is an artifact (8.9.2.2); a file attachment
  has an `AFRelationship` (8.9.2.4.10); one widget per `Form` (8.10.1); a field
  widget has a label or `/Contents` (8.10.2.3); an embedded file has `/Desc`
  (8.14.1). A document that passed may now get these findings. With the font
  rules above, every fail file of veraPDF's PDF/UA-1 and PDF/UA-2 suites is
  caught and none of their pass files is reported.
- **`ValidatePDFA` reads a non-symbolic TrueType font's `/Differences` names
  against Adobe's glyphlist.txt itself** (#353) (PDF/A-2/3/4), as veraPDF does: new
  findings for names not on it — `uniXXXX`/`uXXXX`, the ZapfDingbats
  `a1`–`a205`, `bracket`, `isinferior` — and none any more for names it carries
  (for example `afii10017`) or for `.notdef`; each unlisted name is reported
  once per array. An unembedded font is still checked, as the clause reads
  (veraPDF checks embedded fonts only).
- **PDF/A-1 forbids `/Differences` in a non-symbolic TrueType font** (#353) (6.3.7):
  the key's presence, even an empty array, is the finding; the per-name findings
  are no longer made at part 1.
- **PDF/A-2/3/4 require a (3,1) cmap subtable** (#353) in the embedded program of a
  non-symbolic TrueType font with `/Differences` (6.2.11.6 / 6.2.10.6).
- **Rendering mode 7 is no longer exempt** (#353) from the font embedding,
  glyph-coverage, width, damaged-program, PDF/A-1 CIDSet-completeness and Type 3
  checks; only mode 3 (invisible text) is.
- **The width rule** (#353) (PDF/A 6.3.6 / 6.2.11.5 / 6.2.10.5, PDF/UA 7.21.5) compares
  a glyph the program lacks at the width of the glyph drawn in its place
  (.notdef, or CID 0 under Identity-H/V), a code outside `/Widths` against the
  descriptor's `/MissingWidth` (0 by default), and now checks codes an Identity
  CMap maps to no CID. New findings are possible.
- **`ValidatePDFUA2` no longer reports PDF/UA-1's heading rules** (#354): the heading
  sequence ("first heading is H*n*", a skipped level — ISO 14289-2 8.2.5.12 NOTE
  2 drops the requirement) and 7.4.4 (one `H` per node, mixing `H` with
  `H1`–`H6` — every `H` is already reported under 8.2.5.12).
- **`ValidatePDFUA2` no longer reports an untagged annotation** (#354) ("annotation is
  not tagged"): under ISO 14289-2 8.9.2.2 an annotation outside the structure
  tree is an artifact.
- **`ValidatePDFUA` and `ValidatePDFUA2` read a table cell's Scope, RowSpan,
  ColSpan and Headers from the classes its `/C` names in `/ClassMap`** (#354) as well
  as from `/A` (which wins), per ISO 32000-2 14.7.6.2: tables scoped through
  classes no longer draw 7.5 findings, and spans given by a class count in the
  7.2 grid checks.
- **`ValidatePDFUA` reports a skipped heading level under clause `7.4.2`** (#354) (was
  `7.4`), where ISO 14289-1 states it; filter on `7.4.2`. The first-heading
  message now reads "first heading is H*n*; numbered headings must start at H1".
- **A malformed XMP packet's finding names the cause** (#354) — `ValidatePDFUA` /
  `ValidatePDFUA2` (clause 5) and `ValidatePDFA` (6.7.9, 6.6.2.1, 6.7.2.1)
  append the parser's reason, e.g. "(XML syntax error on line 289: illegal
  character code U+0001)". The packet is not repaired: pdf0 stays strict where
  veraPDF silently fixes control characters. Callers matching the whole message
  should match its start. Metadata writers that refuse a malformed packet word a
  mismatched, unexpected or unclosed tag with its line.

### Signatures

- **An added signature is a permitted change only if it is itself intact**
  (#333). A signature field added after a signature, or a value given to an
  empty signature field, used to be accepted if its value merely had
  `/ByteRange` and `/Contents`; now its own result must be `Valid` with
  `ChangesAllowed`. Otherwise it is a disallowed change, and the earlier
  signature is no longer `Intact`.
- **`sign.Result.ChainErr` is non-nil when no trust roots were given** (#333).
  A `Valid` signature verified with nil `VerifyOptions.Roots` came back with
  `TrustedChain` false and `ChainErr` nil; `ChainErr` now says "no trust roots
  were supplied". It is nil exactly when `TrustedChain` is set or the signature
  is not `Valid`.

- **`WriteSigned` finds an existing signature as `VerifySignatures` does.** (#347) Its
  `ErrAlreadySigned` refusal now asks what the file it was read from carries,
  by the verifier's test. It used to miss a signature whose `/Contents` is
  indirect, and rewrite the file without a word; it no longer refuses a
  dictionary whose indirect `/Type` names no signature, nor a document built in
  memory. It also accepts the document's `AllowInvalidatingSignatures` consent,
  as it accepts `InvalidatingExistingSignatures()`.

### Extraction

- **The extractors return an error for a Locked document** (#335). `ExtractText`,
  `ExtractTextContext`, `ExtractPageText`, `ExtractImagesContext` and
  `ImagesContext` return or yield an error wrapping `LockReason`, where the text
  extractors returned "" and a nil error. `ExtractImages` and `Images` yield
  nothing, where they handed back ciphertext as encoded image bytes.
- **Declined content is an error, not missing text** (#335). A page whose
  content stream, form content, or the ToUnicode or encoding CMap of a font it
  shows text in was declined — over a size limit, under a filter pdf0 does not
  implement, not decrypted — was left out with a nil error. It is now a
  `*PageTextError` naming the guard and the object. Every limit error the
  extractors and `StreamData` return matches `ErrResourceLimit`.

### Building

- **`content.Builder`'s `SetColor` and `SetPattern` fail in an uncoloured
  pattern space** (#345), and so do their stroking twins. Only a space selected
  with the new `SetUncoloredPatternSpace` is affected, so no existing code is:
  there, either alone writes an `scn` a reader rejects, so the Builder refuses
  it. Use `SetUncoloredPattern`.
- **A COLR face whose licence permits bitmap embedding only is no longer drawn
  from its outlines** (#338). OS/2 `fsType` bit 9 forbids embedding outline
  data, and a colour face was written as Type 3 path procedures regardless. It
  is now drawn from its bitmap strikes, or refused with
  `fonts.ErrBitmapEmbeddingOnly` when it has none, at every level.
- **In a document whose metadata declares PDF/UA-2, `SetStructureTree` writes
  the top-level `Document` element in the PDF 2.0 structure namespace** (#351), as
  part 2 requires. Other documents are unchanged.

### The `pdf0` command

- **A run whose only findings are checker findings exits 3** (#346) (`validate`, `ua`,
  `repair`). A `limit` or `internal` finding says a check could not be
  completed; with no violation beside it, conformance is unknown, which is
  "the run failed", not "violations". `pdf0 validate -level declared` on a file
  that declares no level is the common case; it exited 1. Any violation is still
  exit 1, with or without checker findings, and `repair`'s summary counts
  checker findings apart. See [cli.md](../cli.md#exit-codes).
- **`decrypt`, `encrypt`, `repair` and `merge` refuse a signed input** (#347), exit 3,
  naming the input and nothing written: each rewrites the whole file, which
  breaks every signature in it. `merge` refuses a signed input in any position.
  They used to write the file, and its signatures no longer verified. The new
  `-invalidate-signatures` flag writes it anyway. See
  [cli.md](../cli.md#signed-inputs).
- **`pdf0 -h` lists every `-level`** (#340): each PDF/A level `pdfa.Levels`
  returns, and `declared` for `validate`, where it offered only 1b, 2b, 3b and
  4.
- **A redirect of stdout onto an input names what the redirect did** (#340):
  truncated it (`>`), or would append to it (`>>`) or overwrite it in place
  (`1<>`).

### For contributors

- **`make test-corpus` runs what CI's corpus job runs** (#340): `go test ./...`
  with the corpus named, then `TestCorpus`'s counts. It ran only
  `-run TestCorpus`, which missed the corpus tests named otherwise.
- **`make check` is CI's build-test job**, in its order (#340), and
  `make check-examples` runs every example; `TestMakeTargetsAreWhatCIRuns` holds
  both to CI.
- **The ISO 32000 spec-example tests need the hand-placed spec PDFs.** (#352)
  `TestSpecExamples*` and `TestSpec17Examples*` extract the examples on the spot
  from `spec/pdf2.0/ISO_32000-2_sponsored-ec2.pdf` and
  `spec/pdf1.7/PDF32000_2008.pdf` (with `pdftotext` and `python3`) into the
  gitignored `testdata/spec-examples/`, and skip without them.
  `testdata/spec_examples.json` and `testdata/spec_examples_17.json` are no
  longer in the repository or the module zip: they held ISO text there is no
  permission to redistribute.
- **`testdata/pdfref17_examples.json`** (#352) holds the example code of Adobe's PDF
  Reference 1.7, committed under that book's §1.4 permission;
  `TestPDFRef17Examples` round-trips 250 objects, 11 bare objects and 2 xref
  sections from it on every run, CI included.
  `cmd/extract_spec_examples/pdfref17.py` regenerates it; the optional
  `PDF_REFERENCE_17` / `spec/pdfref1.7` dataset enables
  `TestPDFRef17ExamplesRegenerate`.

## Added

- **`htmlpdf.RenderInto` and `htmlpdf.RenderIntoContext`** (#PR): lay an HTML
  document out as a page added to a `*pdf0.Document` of the caller's, after its
  pages, so several HTML documents become one PDF and an HTML page can join
  pages from elsewhere. The document is changed only if the render succeeds. A
  document claiming PDF/A-2b, 2u, 3b, 3u, 4, 4e or 4f keeps its claim (every
  font embedded, its own output intents applying); PDF/A-1 and the a levels are
  refused with `htmlpdf.ErrUnsupportedLevel`. A document claiming no level
  and older than PDF 1.4 is raised to 1.4.

- **`pdfa.SkeletonOptions.Created` and `pdfa.SkeletonOptions.Language`**
  (#367), so a document made by `NewPDFADocumentWith` is dated and has a
  language from the start. `Created` is written as `xmp:CreateDate`. Left
  zero, it is the moment the document is built, and the Unix epoch is refused
  with `pdfa.ErrEpochCreated`, because that is what an unset integer
  timestamp becomes. `Language` is written as the catalog's `/Lang`, under the
  same rules as `SetDocumentInfo`.

- **`htmlpdf.Options.EmbedAllFonts`** (#364): every font on the page is
  embedded, with none of the fourteen standard fonts. The page is laid out in
  the caller's `Input.Fonts` and the document's `@font-face` faces only. A nil
  set is read as an empty one rather than as the standard fonts, and a family
  nothing supplies refuses the document (`no-face`). A standard face from a
  caller's own set is refused under `htmlpdf.RuleStandardFont`
  (`backend-standard-font`), which `Input.Policy` does not lower. Without the
  option, output is unchanged byte for byte.

- **`pdfx.LevelDeclared`** (#346), the zero `pdfx.Level`: validate against the level
  the document declares.
- **`Document.ImagesContext`** (#335): `Images` with cancellation, and a final
  error when the iteration stops short — the work budget (matching
  `ErrResourceLimit`), a cancelled context, or a Locked document.
- **`ErrResourceLimit`** (#335): the `errors.Is` target for an extraction or a
  stream decode stopped by a resource limit.
- **`sign.Result.LaterVisibleSignatures` and
  `sign.PAdESResult.LaterVisibleSignatures`** (#333): the later signatures
  `Intact` accepted whose widget a reader can see, by field name. A later
  signature may draw anything over the page; whether to trust what it draws is
  the caller's decision.
- **`sign.PAdESResult.Revocation`** (#333): the signer certificate's revocation
  status, as `sign.Result.Revocation` reports it. `Conformant` stays
  trust-free: accept a signature on `Conformant`, `TrustedChain` and a
  revocation status that is not `RevocationRevoked` together.
- **Uncoloured tiling patterns in `content.Builder`** (#345):
  `SetUncoloredPatternSpace` selects a `/Pattern` space with its device
  underlying space, `SetUncoloredPattern` paints the pattern in a colour in one
  `scn`, and `SetStrokeUncoloredPatternSpace` and `SetStrokeUncoloredPattern`
  do the same for stroking. `content.Color.Underlying` records the underlying
  space.

- **`ErrWouldInvalidateSignatures`** (#347): the `errors.Is` target for a refusal to
  rewrite a document whose file carries signatures.
- **`Document.AllowInvalidatingSignatures`** (#347): the consent to rewrite such a
  document anyway, held by the document
  ([ADR 0019](../adr/0019-signature-consent-on-the-document.md)).
- **`pdf0`'s `-invalidate-signatures` flag** (#347) on `decrypt`, `encrypt`, `repair`
  and `merge`: the same consent, from the command line.
- **`THIRD_PARTY_NOTICES`** (#348) at the module root lists everything the repository
  keeps that came from elsewhere — its source, pin and licence — and carries
  the notices those licences require; it ships in the module zip, and a test
  holds it to the tree. The XMP RelaxNG schemas' MIT licence text, which was
  not shipped before, is now included, and `internal/saslprep/tables.go`
  carries RFC 3454's copyright notice and permission paragraph, as the RFC
  requires of derivative works.
- **`DocumentInfo.Language`** (#351): `SetDocumentInfo` writes the document's
  language tag as the catalog `/Lang`. A value that is not a language tag is
  refused; in a PDF/A-1 document its subtags must be letters only.
- **`Document.DeclarePDFUA`** (#351) declares a document PDF/UA-1 or PDF/UA-2: it
  writes the `pdfuaid` identification (with `pdfuaid:rev` for part 2), the
  extension schema PDF/A requires for it, and `/ViewerPreferences
  /DisplayDocTitle`, and for part 2 puts the structure tree's `Document`
  element in the PDF 2.0 namespace. It refuses a document with no title, a PDF
  version the part is not defined for, part 2 without a language, and a Locked
  document. With `SetStructureTree` and `SetDocumentInfo`, a tagged document
  passes `ValidatePDFUA` / `ValidatePDFUA2` and veraPDF; docs/building.md has
  the recipe.

## Fixed

- **An incremental update of a file read encrypted no longer garbles it.** (#347)
  After `RemoveEncryption`, `WriteIncremental`, `WriteSignedIncremental` and
  `WriteArchivalTimestamp` appended plaintext objects to the file's ciphertext
  under a trailer without `/Encrypt`, so every reader took the ciphertext for
  content, while a signature over the original bytes still verified. They now
  refuse.

- **`Read` opens a rebuilt file whose catalog is in an object stream** (#334).
  A file whose cross-reference data is unusable is rebuilt by scanning; `/Root`
  was looked for before object streams were unpacked, so such a file failed to
  open.
- **A rebuilt encrypted file with a cross-reference stream can be decrypted**
  (#334). Its `/Encrypt` and `/ID` are only in the stream's dictionary, which
  the rebuild did not read.
- **`content.Builder` paints an uncoloured tiling pattern** (#345). It wrote the
  colour and the pattern name as two `scn` operators, where ISO 32000-2 8.7.3.3
  requires one, and poppler painted nothing.
- **The examples write PDF 2.0 a reader accepts** (#345): `simple_pdf`,
  `simple_pdf17`, `text` and `extract_images` start from `NewDocument`, so
  their output has the trailer `/ID` ISO 32000-2 requires.
- **A signing error names the end-of-file marker `%%EOF`** (#333), as the file
  spells it.
- **The developer tools under `internal/cmd` answer `-h`** (#340) rather than
  run: `genicc` and `gensaslprep` regenerated their files, and `gensaslprep`
  fetched the RFC, when asked for help.
- **A symbolic TrueType font's cmap is judged by each PDF/A part's own rule.** (#353)
  PDF/A-2/3 accept several subtables when one is (3,0); PDF/A-4 requires a (3,0)
  or (1,0) (a font with only (3,1) is a new finding there). A (1,0)+(3,0) font
  is no longer reported at PDF/A-2 to -4.
- **A non-symbolic TrueType font's codes left undefined by its Encoding
  dictionary are named from StandardEncoding** (#353) (ISO 32000-1 9.6.6.4), removing
  false glyph-missing, `.notdef` and width findings in `ValidatePDFA`,
  `ValidatePDFUA` and `ValidatePDFUA2` (for example under `/BaseEncoding
  /Identity`). PDF/UA 7.21.5 now also compares a code that selects `.notdef`
  against `.notdef`'s width.
