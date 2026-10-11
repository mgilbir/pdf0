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
| [docs-audit-2026-10-11.md](docs-audit-2026-10-11.md) | 2026-10-11 | Documentation audit (D1–D69): drift, inverted pyramid, sizing, diagrams; six code defects the docs papered over | **Current** — being remediated by the `fix/a1011-*` stack of pull requests |
| [docs-audit-2026-07-27.md](docs-audit-2026-07-27.md) | 2026-07-27 | Documentation audit (D1–D21) | Resolved — all 21 addressed in seven pull requests (#231–#237), merged 2026-07-27; spot-checked by the 2026-10-11 audit and found fixed |
| [codebase-audit-2026-09-22.md](codebase-audit-2026-09-22.md) | 2026-09-22 | Adversarial full-code audit (C1–C169, 11 parallel readers) plus design tensions T1–T5 | **Current** — remediated by pull requests #295–#313, merged 2026-09-26; [remediation-2026-09-22.md](remediation-2026-09-22.md) maps every ID to its pull request and commits. Its §7 reopens 12 of the 2026-07-26 items as partial fixes |
| [codebase-audit-2026-07-26.md](codebase-audit-2026-07-26.md) | 2026-07-26 | Adversarial full-code audit (C1–C49, 11 parallel readers) | Superseded by the 2026-09-22 report. Every ID has a citing commit, but 12 (C1, C3, C10, C12, C13, C16, C18, C20, C21, C29, C32, C35) were fixed at one site only; see the 2026-09-22 report §7 |
| [docs-audit-2026-07-08.md](docs-audit-2026-07-08.md) | 2026-07-08 | Documentation audit (D1–D8) | Resolved — all eight addressed |
| [codebase-audit-2026-07-07-v2.md](codebase-audit-2026-07-07-v2.md) | 2026-07-07 | Adversarial full-code audit (C1–C37 + design tensions) | Superseded by the 2026-07-26 report; findings largely resolved across PRs #28–#39 |
| [codebase-audit-2026-07-07.md](codebase-audit-2026-07-07.md) | 2026-07-07 | Adversarial full-code audit (first pass) | Superseded by the v2 report |

Finding IDs are stable and are cited by the PRs that fix them: `C…` for codebase
audits, `D…` for documentation audits. IDs are scoped to their report — the
2026-07-08, 2026-07-27 and 2026-10-11 audits each number from D1.

## Plans (live)

| Plan | Addresses | Status |
|------|-----------|--------|
| [remediation-2026-09-22.md](remediation-2026-09-22.md) | `codebase-audit-2026-09-22.md` (169 findings) | Merged (2026-09-26, main at 3f13cb9): every ID is mapped to its pull request and the commits on main, with the residuals the pull requests record |
| [remediation-plan-2026-07-26.md](remediation-plan-2026-07-26.md) | `codebase-audit-2026-07-26.md` (49 findings) | Complete (2026-08-16): every finding has a citing commit. 12 were reopened by the 2026-09-22 audit (§7) and are tracked there |
