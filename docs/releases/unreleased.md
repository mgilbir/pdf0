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

## Added

## Fixed

- An embedded TrueType program carries only what its glyphs need (forme
  v0.14.1): `post` without glyph names, `hmtx` ending at the last glyph kept,
  and a `cmap` of the characters kept. For a 20-character label in Noto Sans
  the `/FontFile2` falls from 42,754 bytes to 3,459, and writing it is
  correspondingly cheaper. The glyph numbering, `/W`, `/CIDSet` and the
  ToUnicode CMap are unchanged, and the result passes veraPDF at every PDF/A
  level (#425).
