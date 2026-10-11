# 0018 — v0.2.0 through v0.3.1 are retracted, not deleted

**Status:** accepted, in force.

## Context

v0.2.0 and v0.3.1 shipped three compiled Linux executables committed to the
repository root by accident; in v0.3.1 they were 81% of the module's bytes,
none of it reachable Go code. Removing them from the history moved every commit
from 3 August onward, so both tags named commits that no longer existed. v0.3.0,
between them, had a correct tree but was published from a commit the same
rewrite removed.

A Go module version cannot be withdrawn by deleting its tag: the module proxy
keeps what it has cached, and a consumer's `go.sum` pins it.

## Decision

`go.mod` retracts the range `[v0.2.0, v0.3.1]` with one directive, whose comment
is the reason `go list -m -u` prints. v0.3.0 was retracted first, on its own
(4c4cc4d, 2026-09-19); the range followed (d61adc4, 2026-09-20), and v0.3.0's
directive was folded into it (0ded13e, 2026-09-23). v0.3.2 is the same code without the binaries. The rationale lives
in that comment, beside the directive it explains.

## Consequences

- `go get @latest` and every upgrade skip a retracted version; `go list -m -u`
  reports it as retracted with the reason. An explicit `go get @v0.3.0` still
  resolves — Go honours a pin it is asked for — so retraction is a strong
  recommendation, not a block.
- One directive for the range keeps the reason in one place.
- `scripts/check-no-binaries.sh`, run in CI, and the lint
  `TestGitignoreCoversEveryCommand` exist so the cause cannot recur.
