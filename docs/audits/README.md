# Audit history

Point-in-time findings reports and the plans that work them off. A **report** is
a historical snapshot, not the current state of the code — for that, read the
source, the ratchet baselines in `pdfa_test.go`, and
[docs/architecture.md](../architecture.md). A **plan** is a live working
document and does change.

Start with the newest report in each track.

## Reports (historical)

| Report | Date | Scope | Status |
|--------|------|-------|--------|
| [docs-audit-2026-07-27.md](docs-audit-2026-07-27.md) | 2026-07-27 | Documentation audit (D1–D21) | **Current** — being worked off in a stacked-PR series |
| [codebase-audit-2026-07-26.md](codebase-audit-2026-07-26.md) | 2026-07-26 | Adversarial full-code audit (C1–C49, 11 parallel readers) | **Current** — supersedes the 2026-07-07 reports and re-triages C1–C37 |
| [docs-audit-2026-07-08.md](docs-audit-2026-07-08.md) | 2026-07-08 | Documentation audit (D1–D8) | Resolved — all eight addressed |
| [codebase-audit-2026-07-07-v2.md](codebase-audit-2026-07-07-v2.md) | 2026-07-07 | Adversarial full-code audit (C1–C37 + design tensions) | Superseded by the 2026-07-26 report; findings largely resolved across PRs #28–#39 |
| [codebase-audit-2026-07-07.md](codebase-audit-2026-07-07.md) | 2026-07-07 | Adversarial full-code audit (first pass) | Superseded by the v2 report |

Finding IDs are stable and are cited by the PRs that fix them: `C…` for codebase
audits, `D…` for documentation audits. IDs are scoped to their report — the
2026-07-08 and 2026-07-27 audits both number from D1.

## Plans (live)

| Plan | Addresses | Status |
|------|-----------|--------|
| [remediation-plan-2026-07-26.md](remediation-plan-2026-07-26.md) | `codebase-audit-2026-07-26.md` (49 findings) | In progress — stacked PRs, merged bottom-up |
