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

- **What a CSS `filter` draws is tagged as what it is** (#PR). A tagged page
  used to make everything a filter group held one span of the filtered
  element; it is now tagged inside the form the filter is painted through,
  each element as itself, as anywhere else on the page.

## Added

- **`pdf0.FormMCIDKid`** (#PR): a span inside a form XObject as a structure
  element's kid, written as a marked-content reference naming the form's
  stream (`/MCR` with `/Stm`), with the form's `/StructParents` entry in the
  parent tree. Content drawn through a transparency group can be tagged where
  it is drawn.

## Fixed
