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

- **Every rule veraPDF fails a corpus file on, pdf0 reports under the same
  clause** (#PR), at PDF/A-1b, -2b, -3b and -4, and pdf0 flags no file veraPDF
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
