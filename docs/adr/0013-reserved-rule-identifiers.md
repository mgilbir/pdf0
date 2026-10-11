# 0013 — `limit` and `internal` name the checker, and `limit` is shared with formalis

**Status:** accepted, in force.

## Context

A validator can fail to answer: a resource guard trips, the caller cancels, or a
check panics on hostile input and is recovered. Returning fewer findings would
let a stopped run read as a cleaner document; returning an error would discard
the findings that were established. And pdf0 composes validations — the
Factur-X and Order-X containers fold in a PDF/A-3 validation and formalis'
invoice rule engine — so "the checker stopped" has to survive being folded into
another validator's report.

## Decision

Two rule identifiers are reserved for findings about the checker rather than
the document, declared once in `internal/finding` since da0ae87 (2026-08-01): `limit`
(`finding.LimitRule`) for a tripped guard or a cancelled run, and `internal`
(`finding.InternalRule`) for a recovered panic. `IsCheckerFinding` reports
both. A cancellation reuses `limit` rather than a third identifier: it is the
same event, the checker stopping before it had seen everything.

When one validator adopts another's findings, these two keep their bare names;
every other rule is namespaced (`adoptPDFAFindings`). formalis reserves the
same string for the same event, `formalis.RuleLimit`, and its findings are
adopted verbatim (`adoptInvoiceFindings`), so a caller draining one mixed slice
has one name to look for.

## Consequences

- A caller asking "is this file conformant?" reads a checker finding as
  "unknown", not as a failure and never as a pass. A validation that stopped
  short is never an empty result.
- A prefixed `pdfa-3/limit` or `invoice/limit` would be invisible to a caller
  keying on `limit`; that is the one failure the reserved names exist to
  prevent, and the adopters must preserve it by hand.
- The agreement with formalis covers `limit` only. formalis' other reserved
  identifier, `profile`, is not treated as a checker finding here: pdf0 passes
  formalis only a profile read from the container's XMP, so the finding marks a
  container defect pdf0 already reports as its own.
