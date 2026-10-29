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

### Validation

- **PDF/A rules the corpus does not test, checked against veraPDF on
  single-defect files**: eighteen more fixtures at PDF/A-1b, -2b and -4, and
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
    Unicode (3,1) nor a Mac Roman (1,0) subtable, and whose post table names
    no glyphs, has no glyph for a named code (a missing glyph, not `.notdef`).
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

## Fixed
