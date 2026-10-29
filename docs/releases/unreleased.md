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

## Added

## Fixed
