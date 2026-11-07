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

- Right-to-left text is no longer wrapped in an `/ActualText` by default:
  PDFium (Chrome, Edge) and poppler both reorder an `/ActualText` as if it
  were in drawing order and returned a marked word backwards (#435). A
  reordered stretch is marked only where a reader applying the bidirectional
  algorithm would not read it right (Latin drawn backwards under an
  override). `fonts.Face.SetMarkReordered(true)` restores the marking, and
  `htmlpdf` sets it for a tagged page, whose marked content must be in
  logical order.
- `Document.ExtractText` puts each line in reading order by the Unicode
  bidirectional algorithm, as PDFium and poppler do, so right-to-left text
  drawn in visual order extracts in the order it is read. Lines with no
  right-to-left text extract exactly as before (byte for byte over 2,958
  corpus files). Text drawn in its written order, as `fonts.Face.Encode`
  draws it unshaped, now extracts as the page shows it — reversed — as it
  does in those readers.

- Where a reordered stretch is marked (`SetMarkReordered(true)`, or text a
  bidirectional reader cannot read right), only that stretch is wrapped in
  an `/ActualText`, not the whole run, so the words either side of it keep
  their breaks (#426).

## Added

- `Document.ExtractText` honours `/ReversedChars` marked content (ISO 32000-2
  14.8.2.5.3): each string shown inside it is read code by code in reverse,
  so a producer's right-to-left text written that way extracts in reading
  order.

- `fonts.Face.SetMarkReordered(true)` wraps each stretch of a run drawn out
  of its text's order in an `/ActualText` with its text in reading order, as
  a tagged PDF needs (#426, #435).

## Fixed

- Embedding a font no longer copies and counts the whole font's widths for
  every document: the advance most glyphs share (`/DW`) and the width table's
  length are worked out once per font and shared, like the reverse cmap, and
  `/W`, `/W2` and `/Widths` read the kept glyphs' advances without a copy.
  Embedding a label in Noto Sans through a fresh `Adopt(face.Clone())`
  allocates about 225 KB, from 350 KB. The fonts written are unchanged
  (#439).

- `fonts.Face.Record`, `Draw` and the other drawing paths plan a run without
  allocating scratch: what the planner works in is kept on the face between
  runs, a run in its text's order is read straight off its glyphs, and a
  cluster is compared with its text without building a string. Recording a
  22-glyph label again allocates once (the result), not 36 times, and takes
  1.2 µs, not 4 µs (#434).

- forme v0.15.0. An embedded font's `name` table keeps the copyright,
  trademark and licence notices and the names the font goes by, and drops
  the rest: the Noto Sans label below falls further, to 2,741 bytes. The
  simple-font `/Widths` and ToUnicode CMap, and the reverse map a glyph's
  ToUnicode entry falls back on, read the font's cmap through forme's
  `CmapGlyph` and `CmapAll` instead of copying it: a simple face's tables
  are written in 35 µs and 45 KB, from 184 µs and 193 KB, and the reverse
  map is built in 120 µs, from 200 µs.

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
