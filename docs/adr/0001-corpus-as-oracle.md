# 0001 — The veraPDF corpus outranks a spec reading

**Status:** accepted, in force.

## Context

ISO 19005 is prose. Several of its clauses admit more than one defensible
reading, and a validator that picks the wrong one is not "strict" — it is wrong,
because it rejects files the rest of the world accepts. veraPDF is the reference
implementation the industry actually validates against, and its corpus encodes
its settled interpretation as pass-files and fail-files.

pdf0 hit this repeatedly. A rule that looks correct on paper produced false
positives on conformant files: PDF/A-1b turned out to be header-version-agnostic
(a `%PDF-2.0` header passes); `/Resources` inheritance is legal despite a naive
reading of 6.2.2 forbidding it (56 false positives when forbidden); device-colour
coverage counts only `GTS_PDFA1` output intents.

## Decision

Where a spec reading and the corpus disagree, **the corpus wins**. A rule that
raises a false positive on a corpus pass-file is wrong and gets fixed or removed,
regardless of how well it reads against the ISO text.

`TestCorpus` enforces this as a ratchet rather than a pass/fail suite. Three
aggregate counts are recorded as baselines in `pdfa_test.go`:
`corpusMaxFalsePositives` (0, the hard invariant), `corpusMaxMissed`, and
`corpusMaxParseErrors`. A change may lower a baseline; raising one to make a red
test green is prohibited.

Corpus files are also the primary source for a rule's *exact* semantics: veraPDF
files carry their own test description in an outline entry titled
"expected message: …", which pins what the rule is actually about.

## Consequences

- The validator is calibrated to a reference implementation, not to an
  interpretation. This is the point.
- Two costs. Coverage is bounded by the corpus: a rule the corpus does not
  exercise is unmeasured, which is why an empty validation result is documented
  everywhere as "nothing I check fired", not a conformance guarantee. And the
  corpus is not committed (it is large and separately licensed), so this gate
  cannot run in CI — it runs locally via `make test-corpus`, which is why the
  ratchet workflow is written down in CONTRIBUTING rather than automated.
- Where pdf0 deliberately diverges from a naive spec reading, the reason belongs
  in a comment at the rule, so the next reader does not "fix" it back.
