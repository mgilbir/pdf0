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

- The seam is visible in the API, but it is no longer a wart. `facturx.Result`
  and `facturx.OrderXResult` used to carry `[]formalis.Violation`, an external type pdf0
  cannot extend, so those findings did **not** satisfy `pdf0.Violation` the way
  every other validator's do, and callers who wanted one combined list had to
  adapt them (documentation audit finding D2). formalis v0.2.0 removed that
  type's `Object` field — which pdf0 had been borrowing for PDF object numbers,
  always a smell — and pdf0 now has finding types of its own,
  `facturx.Violation` and `facturx.OrderXViolation`. They satisfy `pdf0.Violation`, so
  `IsCheckerFinding` applies to them and both entry points could finally get a
  `…Context` variant.
- The seam is where two rule namespaces meet, and both modules pay for it. Each
  adopted finding carries `Source`, the authority that wrote the rule, because
  an identifier like `BR-01` is unique only within its author; the reserved
  identifier `limit` is spelled the same on both sides so that one mixed slice
  needs one name for "the checker stopped"; and the invoice engine's advisory
  findings are kept out of `Violations`, in `InvoiceWarnings`, because
  `pdf0.Violation` carries no severity and a warning folded into a combined
  report is indistinguishable from a PDF/A-3 failure.
- Which rules fire on the invoice is formalis' scope decision, not pdf0's. pdf0's
  Factur-X corpus oracle therefore asserts FP=0 on **container** findings and
  merely ratchets the count of files the invoice engine reports on
  (`facturxInvoiceRuleFindings`). Folding both into one FP=0 claim let a rule
  this repository does not own break an oracle about containers, which is what
  the v0.2.0 bump did: formalis began evaluating CEN's CII syntax bindings, and
  17 of 75 official FNFE/ZUGFeRD samples — almost all EXTENDED — report against
  the EN 16931 core.
- New CIUS work goes in formalis, not here. pdf0 changes only when the
  *container* rules change.
- The oracle data (EN 16931 artefacts, code lists, national CIUS suites) is
  fetched by formalis' own Makefile, not this one.
