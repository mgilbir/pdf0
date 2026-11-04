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
go run golang.org/x/exp/cmd/gorelease@latest -base=v0.7.0
```

## Breaking changes

## Behaviour changes

## Added

### fonts

- **Drawing a face into a content stream of one's own** (#417):
  `Face.Record` does to a face what `Draw` does — marks the glyphs used and
  records what each was drawn for, which the ToUnicode CMap says — and returns
  the run's stretches (`fonts.Segment`) and which need an `/ActualText`.
  `Face.AppendCode` writes a glyph's code and `Face.NominalAdvance` gives the
  width the font dictionary states for it. A face written as Type 3 fonts is
  refused (`fonts.ErrDrawnAsType3`).

## Fixed

### Validation

- **A CIDSet is held to the CIDs shown as veraPDF holds it** (#416). At
  PDF/A-1 a CID shown has to be in the `/CIDSet` only when the embedded
  program has it, and only for a subset; at PDF/A-2 and -3 the set is held to
  the program alone. A `CIDFontType0` whose CFF is not CID-keyed, whose program
  has no CIDs, used to fail every set at PDF/A-2 and -3, and a set of CID 0
  alone, which veraPDF passes at every part, at all of them.
