# Audit history

Point-in-time audit reports. Findings are worked off in stacked PRs; a report is
a historical snapshot, not the current state of the code — for that, read the
source and the ratchet baselines in `pdfa_test.go`.

| Report | Date | Scope | Status |
|--------|------|-------|--------|
| [codebase-audit-2026-07-07.md](codebase-audit-2026-07-07.md) | 2026-07-07 | Adversarial full-code audit (first pass) | Superseded by the v2 report |
| [codebase-audit-2026-07-07-v2.md](codebase-audit-2026-07-07-v2.md) | 2026-07-07 | Adversarial full-code audit (current; C1–C37 + design tensions) | Findings largely resolved across PRs #28–#39 |
| [docs-audit-2026-07-08.md](docs-audit-2026-07-08.md) | 2026-07-08 | Documentation audit (D1–D8) | Addressed by the architecture/onboarding docs PR |

The v2 codebase audit supersedes the first: it was written against a later commit
and re-triages the earlier findings (most were already fixed). Start there.
