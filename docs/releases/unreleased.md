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
