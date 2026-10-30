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
go run golang.org/x/exp/cmd/gorelease@latest -base=v0.6.0
```

## Breaking changes

## Behaviour changes

### htmlpdf

- **forme v0.13.2** (#398), which reads a TrueType font's post table so a glyph
  can be looked up by name (`font.Program.GlyphByName`, forme#967). No page
  lays out differently. The validators use the names for a non-symbolic
  TrueType font without a (3,1) or (1,0) cmap (see Validation).

- **What a CSS `filter` draws is tagged as what it is** (#386). A tagged page
  used to make everything a filter group held one span of the filtered
  element; it is now tagged inside the form the filter is painted through,
  each element as itself, as anywhere else on the page.

### Validation

- **Rules no corpus file tests, checked against veraPDF** (#407), twenty-seven
  more single-defect files (nineteen PDF/A, eight PDF/UA), and
  what they found:
  - A font file whose `/Subtype` names no kind of program (ISO 32000 Table
    126) is reported at every part, not only at PDF/A-1 (6.2.11.2 /
    6.2.10.2); `/OpenType` is one from PDF/A-2 on. Such a program is still
    read for what it is, and no longer reported damaged besides.
  - A Type 0 font whose descendant is neither a `CIDFontType0` nor a
    `CIDFontType2` is reported (6.3.2 / 6.2.11.2 / 6.2.10.2), and as not
    embedded (6.3.4 / 6.2.11.4.1 / 6.2.10.4.1): there is no program a
    reader knows how to use.
  - LZW in an inline image is ISO 19005-1 6.1.10 at PDF/A-1, as in a
    stream (was 6.1.7).
  - At PDF/A-2a and -3a, a standard structure type the `/RoleMap` remaps to
    a non-standard one is reported (6.7.3.4).
  - At PDF/UA-2, a `Caption` that is neither the first nor the last child
    of its element (8.2.5.27), and a character of real content mapped into
    a Private Use Area with no `/ActualText` or `/Alt` (8.4.3).
  - An element with no `/P` is reported under 8.2.1 at PDF/UA-2 (was 8.2.2),
    and the content it leaves unreachable from the structure tree root is
    reported as untagged (7.1 / 8.2.2), as veraPDF reports it.

- **A `Caption` occurs at most once under any parent at PDF/UA-2** (#406), as ISO
  32000-2 Table 372 says ("The number of captions cannot exceed 1"); it was
  checked only where the ISO/TS 32005 table says 0..1. veraPDF checks it under
  most parents too, but not under a `Sect` or a `Private`.

- **ISO/TS 32005 errata applied at PDF/UA-2** (#405), as the PDF Association's
  working groups resolved them in public (pdf-association/pdf-issues): a
  `Sub` in a `Code` is permitted (#336); a `Reference` has `Link`'s content
  model and each may hold the other (#488), so a `Reference` used as a
  non-grouping element may not hold block-level content such as a `P`, and
  one used as a grouping element may hold `Sect`, `Div` and the like.

- **A `Figure` in a `WP` is permitted at PDF/UA-2** (#404), as ISO 32000-2 Annex L's
  errata (July 2024) give it; the 2023 ISO/TS 32005 table, which veraPDF
  follows, forbids it. pdf0's encoding of that table now agrees with Annex L's
  machine-readable matrix on every pair of PDF 2.0 elements.

- **PDF/UA's font rules the corpus does not test, checked against veraPDF**
  (#403),
  and a TrueType glyph reading fixed in both validators:
  - `ValidatePDFUA` and `ValidatePDFUA2` report a glyph an embedded simple
    font shows but does not define (7.21.4.1 / 8.4.5.5.1), a TrueType
    program's cmap without the subtables the part asks for (7.21.6 /
    8.4.5.7), and a code of a simple font with no Unicode value — no
    `ToUnicode` entry and no glyph name with one (7.21.7 / 8.4.5.8); they
    reported the last only for Identity-encoded Type 0 fonts. The glyph and
    cmap logic is PDF/A's, now shared (`internal/fontcheck`).
  - A TrueType code its encoding names (WinAnsi "B") that the program's
    Unicode cmap has no entry for is a missing glyph, at every PDF/A level
    (6.3.5 / 6.2.11.4.1 / 6.2.10.4.1) and in PDF/UA. It was read as a
    `.notdef` reference (6.2.11.8 / 6.2.10.9), and at PDF/A-1 not reported.
    An unnamed code is `.notdef`'s still.

- **JPEG 2000 images and digital signatures checked against veraPDF on
  single-defect files** (#402): fourteen more fixtures at every PDF/A level, the
  JPEG 2000 data made with gopenjpeg. What changed to agree:
  - A JPEG 2000 image whose channels have different bit depths (a `bpcc`
    box) is reported (6.2.8.3 / 6.2.7.3); it was passed.
  - An image dictionary with `/ColorSpace` exempts the JPEG 2000 data's
    own colour specification from the METH and enumerated-colour-space
    rules, as it already did from the APPROX one.
  - Of the enumerated colour spaces, only 19 (CIEJab) is reported, as ISO
    19005-2 and -4 forbid it alone; 12, 16, 17 and 18 were the only ones
    passed.
  - At PDF/A-1 a JPEG 2000 image is still reported under 6.1.10 (PDF 1.4
    has no JPXDecode), which veraPDF's profile does not check.

- **`ValidatePDFUA2` checks the structure tree against ISO/TS 32005 Table
  5** (#397), the containment rules ISO 14289-2 requires: a child the parent's
  type does not permit, marked content where the type allows none, more
  occurrences than allowed, and a root without exactly one `Document`.
  Findings are named as veraPDF names the rules (`Table 5. Parent-Child`).
  Where the published table forbids what veraPDF permits — a `Code`
  holding `Sub` lines, a `Reference` holding block content — nothing is
  reported. PDF/UA-1's nesting and container rules (7.2) no longer run at
  part 2. Also new at part 2: an `LI` with real content outside `Lbl` and
  `LBody` (8.2.5.25), and an `FENote`'s `NoteType` and its `/Ref` pairing
  with the elements that cite it (8.2.5.14).

- **PDF/UA rules the corpus does not test, checked against veraPDF on
  single-defect files** (#396): fifty-six PDF/UA fixtures at both parts, and what
  pdf0 changed to agree on every one:
  - `ValidatePDFUA2` checks PDF/UA-2's own annotation rules: a link in a
    `Link` or `Reference` element (8.2.5.20), a markup annotation in `Annot`
    and its `/RC` the same text as its `/Contents` (8.9.2.3), NoView
    annotations as artifacts (8.9.2.2), `/Contents` on stamps (or `/Name`),
    ink, screen, 3D and rich media annotations, no `Popup` in the structure
    tree, no `Sound`, `Movie` or `TrapNet`, zero-size widgets, printer's
    marks and watermarks (8.9.2.4.7–19), an annotation's `/Contents` the
    same as its element's `/Alt` (8.9.4.2), a field's widget in `Form` and
    no XFA (8.10.1), a text field's `/RV` beside the same `/V` (8.10.3.3),
    `TOCI` `/Ref` (8.2.5.8), no `Note` (8.2.5.14), and `Ruby` and `Warichu`
    children (8.2.5.23–24).
  - `ValidatePDFUA2` numbers by ISO 14289-2 what it reported under PDF/UA-1's
    clauses: `/Alt` and `/ActualText` in the Private Use Area are 8.4.3 (were
    8.6), a missing catalog `/Metadata` 8.11.1 (was 5), an element with no
    `/P` 8.2.2; and PDF/UA-1's nesting table, `Note` IDs and annotation
    placement are no longer reported at part 2.
  - Both parts: a document not marked as tagged is 6.2 (was 7.1 at part 1).
  - `ValidatePDFUA`: a `Popup` annotation is held to 7.18.1 like any other
    (it was exempt); an annotation outside its page's crop box is exempt
    from the tagging rule, as it was from the description rules; and a
    `Form` element with no `Role` enclosing more than one widget is
    reported (7.18.4).

- **Every single-defect fixture is checked at all eleven PDF/A levels** (#395), not
  only 1b, 2b and 4, and pdf0 agrees with veraPDF on all of them. What
  changed at the levels that need Unicode mappings (1a, 2a, 2u, 3a, 3u):
  - A TrueType font with no `/Encoding` has no Unicode value from its
    encoding, its codes selecting glyphs by code, so it needs a ToUnicode
    CMap (6.3.8 / 6.2.11.7.2).
  - A Type 0 font whose CMap is neither predefined nor embedded is reported
    under the CMap rule alone, not also under the Unicode-mapping one.

- **PDF/A-1 rules the corpus does not test, checked against veraPDF on
  single-defect files** (#393): fifty-three more fixtures at PDF/A-1b, -2b and -4,
  covering stream syntax, implementation limits, output intents, images and
  XObjects, transparency, Type 0 fonts, actions, the metadata stream and
  extension schemas. What pdf0 changed to agree on every one:
  - A TrueType font with no `/Encoding` selects glyphs by code through the
    program's (3,0) or (1,0) cmap, as a symbolic font does (ISO 32000-1
    9.6.6.4): a non-symbolic font without `/Encoding` whose program has only
    a (3,1) cmap shows no glyph, and one with a (3,0) or (1,0) cmap shows
    the code's glyph. This applies to PDF/A and PDF/UA alike.
  - An extension schema property definition missing only its category or
    description still declares the property; one whose value type is
    neither a standard XMP type nor declared by the schema is reported
    (6.7.8 / 6.6.2.3.3) and declares nothing.
  - PDF/A-2 and -3 have no array or dictionary capacity limit (6.1.13),
    and PDF/A-4 no rule on the stream and endstream keywords' line ends.
  - Reported under the clauses veraPDF's PDF/A-1 profile gives them: the
    stream keyword's line ends 6.1.7 (was 6.1.6), a widget's `/A` 6.6.1
    (was 6.6.2), `NeedAppearances` 6.9 (was 6.4.1).

- **PDF/A-2 rules the corpus does not test, checked against veraPDF on
  single-defect files** (#392): thirty-three more fixtures at PDF/A-1b, -2b and -4,
  and what pdf0 changed to agree on every one:
  - An embedded file is judged by its content, not the MIME type its stream
    claims. At PDF/A-2 it must be a PDF/A-1 or PDF/A-2 document (6.8): a
    non-PDF file, or a PDF/A-3 or -4 one, is reported, which was not checked
    at all before. At PDF/A-4, a PDF/A document labelled with another MIME
    type is no longer reported (6.9).
  - A Crypt filter whose decode parameters name anything but Identity is
    reported at PDF/A-2 and -4 (6.1.7.2 / 6.1.6.2).
  - A non-zero real closer to zero than ±1.175×10⁻³⁸ is reported at PDF/A-2
    and -3 (6.1.13).
  - XMP that is not UTF-8 is reported at PDF/A-2 and -3 too (6.6.2.1), not
    only at -4.
  - An optional-content configuration with `/AS` is reported at PDF/A-2 and
    -3 (6.9).
  - A catalog `/Requirements` is reported at PDF/A-2 and -3 (6.11), and
    AlternatePresentations and PresSteps there are 6.10 (were 6.11).
  - At PDF/A-1, a `/DestOutputProfileRef` in an output intent is no longer
    reported (PDF/A-1 predates the key). The identification schema's prefix
    is required only of the properties the part defines, so a `corr` with
    another prefix at PDF/A-1 fails only 6.7.9.

- **PDF/A rules the corpus does not test, checked against veraPDF on
  single-defect files** (#391): eighteen more fixtures at PDF/A-1b, -2b and -4, and
  what pdf0 changed to agree with veraPDF on every one:
  - A font dictionary needs `/Type /Font`, a `/BaseFont` unless it is Type 3,
    and, for a simple font that is not an unembedded standard 14 font,
    `/FirstChar`, `/LastChar` and a `/Widths` of LastChar−FirstChar+1 entries
    (6.3.2 / 6.2.11.2 / 6.2.10.2).
  - A font without `/FirstChar` or `/LastChar` declares no width from its
    `/Widths`, and a code past `/LastChar` takes `/MissingWidth`, in the
    width rule of PDF/A and PDF/UA alike.
  - A dictionary whose `/Subtype` names no kind of font is not judged as a
    font; at PDF/A-4 a `Tf` naming it selects an undefined resource (6.2.2).
  - A non-symbolic TrueType program whose cmap has neither a Microsoft
    Unicode (3,1) nor a Mac Roman (1,0) subtable shows, for a named code, the
    glyph its post table gives that name, and none where it names none (a
    missing glyph, not `.notdef`, its width glyph 0's), as veraPDF reads it;
    a Unicode-platform (0,x) subtable is not consulted. Before forme v0.13.2
    a post table with names left such a font unjudged (#399).
    At PDF/A-4 such a cmap fails 6.2.10.6 whatever the post table holds, and
    at PDF/A-2 and -3 a cmap with no subtable but Microsoft Symbol (3,0) fails
    6.2.11.6.
  - An ICCBased colour space's profile must be an input profile: device
    class `prtr`, `mntr`, `scnr` or `spac`, over RGB, CMYK, Gray or Lab
    (6.2.3.2 / 6.2.4.2).
  - An image's explicit `/Mask` must be 1 bit per sample (6.2.4 / 6.2.8 /
    6.2.7.1).

- **PDF/UA findings follow veraPDF's numbering and verdicts** (#390), at both
  parts: every rule veraPDF fails a PDF/UA corpus file on is now reported
  under the same clause (PDF/UA-1 97/97, PDF/UA-2 48/48), with no file
  veraPDF passes flagged. What a caller sees change:
  - `ValidatePDFUA2` reports the rules it shares with PDF/UA-1 under ISO
    14289-2's clauses: the fonts' 7.21.x are 8.4.5.x, tagging is 6.2, the
    structure tree 8.2.1, untagged content 8.2.2, the role map 8.2.4, tables
    8.2.5.26, a figure's alternate text 8.2.5.28.2, the language 8.4.4,
    Unicode mapping 8.4.5.8, optional content 8.7, the tab order 8.9.3.3,
    `dc:title` 8.11.1 and `DisplayDocTitle` 8.11.2.
  - `ValidatePDFUA2` reports a human-readable text string using the Unicode
    Private Use Area (8.6).
  - `ValidatePDFUA`: a catalog with no `/Metadata` is 7.1 (was 5); a widget
    not under a `Form` is 7.18.4 and a link not under a `Link` 7.18.5 (both
    were 7.18.1); a link with neither `/Contents` nor `/Alt` also fails
    7.18.1; text with no Unicode mapping is 7.21.7 (was 7.2).
  - Both: a CIDFont whose CMap is neither predefined nor embedded, or is
    embedded with no CIDSystemInfo, fails the CIDSystemInfo rule (7.21.3.1,
    8.4.5.3.1).

- **A Level A file without a structure tree is reported under the
  structure-hierarchy clause** (#388), 6.8.3.3 at PDF/A-1a and 6.7.3.3 at
  -2a and -3a, as veraPDF's profiles number it; it was reported with the
  MarkInfo rule under 6.8.2.2 / 6.7.2.2. Rule coverage by veraPDF's verdicts
  now also covers PDF/A-1a, -2a, -2u, -4e and -4f: every rule veraPDF fails a
  corpus file on there is reported under the same clause too, and pdf0 flags
  no file veraPDF passes.

- **Every rule veraPDF fails a corpus file on, pdf0 reports under the same
  clause** (#387), at PDF/A-1b, -2b, -3b and -4, and pdf0 flags no file veraPDF
  passes. What changed to get there, each checked against veraPDF on the
  corpus or on a single-defect file:
  - An output intent with no profile, or with broken profile data, is no
    output intent: device colour on the page is reported, and so is a
    transparent page that names no blending colour space. A profile pdf0
    declined to read is still not judged. A profile whose colour space is
    not RGB or CMYK covers DeviceGray alone.
  - A form XObject that is a transparency group is transparency on the page
    that draws it (6.2.10 / 6.2.9).
  - The output profile's `/N` is checked against its colour space at PDF/A-1
    only, under 6.2.3.2; PDF/A-2 and -4 have no such rule, and a wrong or
    missing `/N` there is no longer reported.
  - A CID the font lacks, a code outside its CMap and an incomplete code are
    `.notdef` references as well as missing glyphs (6.2.11.8 / 6.2.10.9); a
    TrueType code that selects glyph 0 is a `.notdef` reference and not a
    missing glyph.
  - A CIDFont whose CMap states no Registry and Ordering — a name neither
    predefined nor embedded, or an embedded CMap with no CIDSystemInfo — fails
    the CIDSystemInfo rule (6.3.3.1 / 6.2.11.3.1 / 6.2.10.3.1).
  - An extension schema property definition missing a required field, or of
    a value type missing one, declares nothing; a value type with an
    incomplete field definition makes its properties' value type incorrect
    (6.7.9 / 6.6.2.3.1).
  - At PDF/A-4, a shown code that neither the font's ToUnicode CMap nor its
    encoding maps has no Unicode value (6.2.10.7).
  - XMP that is not well-formed also fails the identification rule (6.7.11 /
    6.6.4 / 6.7.3): it identifies no part or level.

### For contributors

- **PDF/UA rule coverage is judged by veraPDF's verdicts too** (#389):
  `TestPDFUARuleCoverageByVeraPDF` holds `ValidatePDFUA` and `ValidatePDFUA2`
  to the rules veraPDF fails each PDF/UA corpus file on, with no false
  positive at either part (`make verapdf-verdicts` now makes the two PDF/UA
  reports too). It starts at 91 of 97 tested rules at PDF/UA-1 and 19 of 48
  at PDF/UA-2, where most findings are reported under PDF/UA-1's clause
  numbers.

- **Rule coverage is also judged by veraPDF's own verdicts** (#385).
  `make verapdf-verdicts` runs veraPDF over the corpus's PDF/A levels and keeps
  its reports (generated, never committed), and `TestRuleCoverageByVeraPDF`
  judges pdf0 against the rules veraPDF fails each file on, where the file's
  name says the wrong rule for a third to two fifths of the files. It fails on
  any file veraPDF passes and pdf0 flags (none today). CI generates the
  verdicts in the corpus job. `testdata/verapdf/verapdf-docker.sh` takes a
  directory, for `--recurse`. `TestSingleDefectFilesAgreeWithVeraPDF` builds
  seventeen one-defect files at PDF/A-1b, -2b and -4 and holds pdf0 to the
  clauses veraPDF fails each on, recorded in the test and checked against
  veraPDF itself where it is available.

## Added

- **`pdf0.FormMCIDKid`** (#386): a span inside a form XObject as a structure
  element's kid, written as a marked-content reference naming the form's
  stream (`/MCR` with `/Stm`), with the form's `/StructParents` entry in the
  parent tree. Content drawn through a transparency group can be tagged where
  it is drawn.

## Fixed
