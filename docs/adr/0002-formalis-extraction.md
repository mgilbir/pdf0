# 0002 — EN 16931 invoice rules live in a separate module

**Status:** accepted, in force.

## Context

Factur-X / ZUGFeRD and Order-X are two things wearing one name: a **PDF
container** (a PDF/A-3 file with an XML invoice as an embedded file, declared in
XMP) and an **invoice document** (EN 16931 semantics, its CIUS profiles —
XRechnung, Peppol BIS 3, NLCIUS — code lists, and Schematron-equivalent business
rules). The second is large, versioned on its own calendar by CEN/TC 434, and has
nothing to do with PDF: it applies identically to a bare UBL or CII XML file.

Growing it inside pdf0 would have made a PDF library carry European invoicing
code lists, and would have forced anyone validating a plain XML invoice to depend
on a PDF parser.

## Decision

The EN 16931 / CIUS engine lives in `github.com/mgilbir/formalis`. pdf0 keeps
only the PDF side: `ValidateFacturX`, `ValidateOrderX` and `EmbedFacturX` check
the container — PDF/A-3 conformance, the embedded-file relationship and
`/AFRelationship`, the XMP declaration, the profile identifier — extract the XML,
and hand it to formalis for the invoice rules.

## Consequences

- The seam is visible in the API, and this is the one wart: `FacturXResult` and
  `OrderXResult` carry `[]formalis.Violation`, an external type pdf0 cannot
  extend, so those findings **do not** satisfy `pdf0.Violation` the way every
  other validator's do. Callers who want one combined list must adapt them. This
  is documented at `violations.go`, in [validators.md](../validators.md) and in
  the README, because the asymmetry is otherwise a compile error waiting to
  happen (documentation audit finding D2).
- New CIUS work goes in formalis, not here. pdf0 changes only when the
  *container* rules change.
- The oracle data (EN 16931 artefacts, code lists, national CIUS suites) is
  fetched by formalis' own Makefile, not this one.
