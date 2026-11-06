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
go run golang.org/x/exp/cmd/gorelease@latest -base=v0.8.0
```

## Breaking changes

## Behaviour changes

- A run drawn partly out of its text's order — a right-to-left word in a
  left-to-right line — has only the reordered stretch wrapped in an
  `/ActualText`, not the whole run. The text extracted is the same; readers
  keep the word breaks either side of the stretch, which poppler lost
  (#426).

## Added

- `fonts.Face.SetMarkReordered(false)` leaves a reordered stretch whose
  clusters each map whole without an `/ActualText`, for an untagged document
  read by a bidi-aware extractor such as poppler, which reverses right-to-left
  text inside an `/ActualText`. pdf0's own `Document.ExtractText` then reads
  such text in drawing order (#426).

## Fixed

- An embedded TrueType program carries only what its glyphs need (forme
  v0.14.1): `post` without glyph names, `hmtx` ending at the last glyph kept,
  and a `cmap` of the characters kept. For a 20-character label in Noto Sans
  the `/FontFile2` falls from 42,754 bytes to 3,459, and writing it is
  correspondingly cheaper. The glyph numbering, `/W`, `/CIDSet` and the
  ToUnicode CMap are unchanged, and the result passes veraPDF at every PDF/A
  level (#425).

- Recording or drawing text no longer copies and inverts the font's whole
  cmap for every document: the reverse map a glyph's ToUnicode entry falls
  back on is built once per font and shared by every wrapper and clone of it
  (keyed by forme's `Face.ID`), and released when none uses it. Recording a
  label through a fresh `Adopt(face.Clone())` falls from 243 µs and 153 KB to
  32 µs and 5 KB (#427).
