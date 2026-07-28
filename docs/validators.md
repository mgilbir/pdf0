# Validators

pdf0 validates a `*Document` against ten conformance standards. Reach for this
doc to pick an entry point, to understand what a result type does and does not
promise, or before adding a rule.

Every validator is **read-only**: each installs its per-run cache on a shallow
copy, so the caller's document is never mutated and the same document can be
validated concurrently (`TestValidateConcurrentSameDoc`, `TestUAValidationCacheIsolation`).

Three further properties hold across the family:

- **Panic safety.** Every check runs behind a `recover()` boundary — `runCheck`
  for PDF/A, the helpers in `validator_guard.go` for the rest. A check that
  panics on hostile input is reported as a finding with the rule `internal`
  rather than crashing the caller, and findings collected before the panic
  survive. The honest limit, the same one `runCheck` has always carried: a stack
  overflow from unbounded recursion is *not* recoverable, so those are prevented
  at their source instead. (This closed C27 from the 2026-07-26 codebase audit;
  before that, only PDF/A had a boundary.)
- **Deterministic order.** Every validator sorts its findings by rule, then
  object, then message before returning — through the one shared
  `sortViolations`, on every return path including the early ones — so results
  are stable across runs and safe to diff or snapshot.
- **A checker that stops early says so.** A tripped resource guard, a recovered
  panic and a cancelled context are all reported as findings under a *reserved*
  rule identifier — `limit` or `internal` — which `IsCheckerFinding` separates
  from a real non-conformance. The findings gathered before the stop are kept,
  and the result is never empty, so a run that did not finish looking can never
  be read as a clean bill of health. Treat such a finding as **unknown**, not as
  a failure. See [limits.md](limits.md) for the classification and
  [architecture.md](architecture.md#cancellation) for the `…Context` variants.

```go
var real []pdf0.Violation
for _, e := range pdf0.ValidatePDFAContext(ctx, doc, pdf0.PDFA2b) {
	if !pdf0.IsCheckerFinding(e) {
		real = append(real, e)
	}
}
```

## Pick an entry point

| Standard | Entry point | Returns | Findings satisfy `Violation` | `…Context` variant |
|----------|-------------|---------|------------------------------|--------------------|
| PDF/A (ISO 19005) 1a/1b/2a/2b/3a/3b/4 | `ValidatePDFA(doc, level)`<br/>`ValidatePDFABytes(doc, level, raw)` | `[]ValidationError` | yes | yes (both) |
| PDF/UA-1 (ISO 14289-1) | `ValidatePDFUA(doc)` | `[]UAViolation` | yes | yes |
| PDF/UA-2 (ISO 14289-2) | `ValidatePDFUA2(doc)` | `[]UAViolation` | yes | yes |
| PDF/X-1a/3/4/4p/6 (ISO 15930) | `ValidatePDFX(doc, level)` | `[]PDFXViolation` | yes | yes |
| PDF/VT-1 (ISO 16612-2) | `ValidatePDFVT(doc)` | `[]PDFVTViolation` | yes | yes |
| PDF/VT-2 | `ValidatePDFVT2(doc)` | `[]PDFVTViolation` | yes | yes |
| PDF/R | `ValidatePDFR(doc)` | `[]PDFRViolation` | yes | yes |
| DPart hierarchy (ISO 32000-2 §14.12) | `ValidateDParts(doc)` | `[]DPartViolation` | yes | yes |
| Factur-X / ZUGFeRD container | `ValidateFacturX(doc, raw)` | `FacturXResult` | **no** — see below | **no** — same reason |
| Order-X container | `ValidateOrderX(doc, raw)` | `OrderXResult` | **no** — see below | **no** — same reason |

The last column is not a coincidence: the nine validators whose findings satisfy
`Violation` are exactly the nine with a `…Context` variant. Cancellation is
reported *as a finding* under the reserved rule `limit`, so an entry point that
cannot carry a finding `IsCheckerFinding` can classify has no honest way to
report a cancelled run — see
[architecture.md](architecture.md#which-entry-points-have-one).

Signature and PAdES assessment are `*Document` methods with their own result
types; see [signing.md](signing.md).

```mermaid
flowchart TD
    Doc[("*Document")]

    subgraph pdfstd["PDF-standard validators — free functions, findings satisfy pdf0.Violation"]
        A["ValidatePDFA / ValidatePDFABytes<br/>→ []ValidationError"]
        UA["ValidatePDFUA / ValidatePDFUA2<br/>→ []UAViolation"]
        X["ValidatePDFX<br/>→ []PDFXViolation"]
        VT["ValidatePDFVT / ValidatePDFVT2<br/>→ []PDFVTViolation"]
        R["ValidatePDFR<br/>→ []PDFRViolation"]
        DP["ValidateDParts<br/>→ []DPartViolation"]
    end

    subgraph invoice["Invoice containers — result structs, findings are formalis.Violation"]
        FX["ValidateFacturX(doc, raw)<br/>→ FacturXResult{Violations, Profile, XMLName, XML}"]
        OX["ValidateOrderX(doc, raw)<br/>→ OrderXResult{Violations, Profile, XMLName, XML}"]
    end

    Doc --> pdfstd
    Doc --> invoice

    pdfstd --> V["[]pdf0.Violation<br/>RuleID() + ObjectNum()<br/>— combinable across standards"]
    invoice -. "NOT convertible: formalis.Violation<br/>has no ObjectNum()" .-> V
```

### Combining findings — and the one exception

The six PDF-standard validators keep their own concrete finding types but all
satisfy `pdf0.Violation` (`error` + `RuleID()` + `ObjectNum()`), so a
multi-standard report is a plain append:

```go
var all []pdf0.Violation
for _, e := range pdf0.ValidatePDFA(doc, pdf0.PDFA2b) {
	all = append(all, e)
}
for _, e := range pdf0.ValidatePDFUA(doc) {
	all = append(all, e)
}
```

**Factur-X and Order-X are the exception.** They return a result *struct*, not a
slice, and its `Violations` field holds `formalis.Violation` — a type owned by
`github.com/mgilbir/formalis`, which this package cannot extend with the
interface methods. The values carry `Rule`, `Message` and `Object` fields, so
adapt them explicitly if you need one combined list:

```go
res := pdf0.ValidateFacturX(doc, raw)
for _, v := range res.Violations {
	fmt.Printf("[Factur-X %s] object %d: %s\n", v.Rule, v.Object, v.Message)
}
```

The EN 16931 / CIUS *invoice-content* rules live in `formalis`; pdf0 validates
the PDF container (PDF/A-3 conformance, the embedded-file relationship, the XMP
declaration) and hands the extracted XML over. See
[ADR 0002](adr/0002-formalis-extraction.md).

## What an empty result means

Nothing fired that pdf0 checks. It is **not** a conformance guarantee: the PDF/A
validator implements a subset of ISO 19005, and the other validators are
narrower still (`ValidatePDFVT2` does not assert the PDF/X-5 external-reference
rules; `ValidatePDFUA2` does not assert full ISO 14289-2). The measured claim is
the corpus ratchet, not the API — see [CONTRIBUTING](../CONTRIBUTING.md#the-corpus-ratchet--read-this-before-changing-a-validation-rule).

## How PDF/A validation runs

`ValidatePDFABytes` (`pdfa.go`) runs a fixed list of 59 check functions, then —
if raw bytes are supplied — the byte-level file-structure checks. Each check runs
behind a `recover()` boundary so a bug or an adversarial structure in one check
cannot crash the caller. Validation runs against a shallow copy of the
`Document`, so it never mutates the caller's document and is safe to run
concurrently on the same document.

```mermaid
flowchart TD
    A[ValidatePDFABytes doc, level, rawData] --> LA{level is 1a/2a/3a?}
    LA -->|yes| LB["validatePDFALevelA:<br/>run the Level B pipeline at level.baseB(),<br/>drop the 'conformance must be B' finding,<br/>add tagged-structure + language + conformance checks"]
    LA -->|no| B[shallow-copy doc,<br/>install per-run cache]
    B --> C[for each of 59 checks]
    C --> D[runCheck: recover panic -> 'internal' violation]
    D --> C
    C --> E{rawData != nil?}
    E -->|yes| F[byte-level checks<br/>runByteCheck: recover]
    E -->|no| G
    F --> G[sort violations by Rule, Object, Message]
    G --> I[return violation list]
    LB --> I
```

`ValidatePDFA(doc, level)` is `ValidatePDFABytes(doc, level, nil)`: it skips the
byte-level rules because they need the file bytes. Use `ValidatePDFABytes`
whenever you have them.

**Level A** (1a/2a/3a) is Level B plus the accessibility requirements. It is
validated by running the Level B pipeline and adding the Level A families
(`pdfa_levela.go`), so every Level B rule applies at Level A too.

Be precise about how much that adds: there are **three** Level A checks —
`checkLevelAConformance` (the `A` conformance declaration),
`checkLevelAStructure` (`/MarkInfo` and `/StructTreeRoot` present) and
`checkLevelALanguage` (catalog `/Lang` syntax). Unicode character mapping is
*not* an extra Level A rule; the ToUnicode requirements live in the Level B
font rules. Level A also has no corpus oracle: `TestCorpus` runs the
`PDF_A-1b/2b/3b/4` suites, and `TestCorpusConformanceSuites` validates the
`PDF_A-1a`/`PDF_A-2a` directories at Level *B*, so `validatePDFALevelA` is
unratcheted. Treat a clean Level A result more cautiously than a Level B one.

**Executed-content model.** Many PDF/A rules apply only to content that is
actually *used*, not merely present. (Two font rules are deliberate exceptions
and scan `Document.Objects` directly: `checkCMapEmbedded` and
`checkCMapCIDLimit`.) Colour spaces, fonts, and ExtGState
parameters are checked when a page (or a form XObject / pattern / Type3 glyph it
invokes) actually references them — see `walkExecutedContent` and
`collectFontTextUsage`. A form XObject that is never drawn does not trigger
font-embedding or colour rules. This mirrors what veraPDF does, and it is why the
corpus is the oracle for rule semantics
([ADR 0001](adr/0001-corpus-as-oracle.md), [ADR 0004](adr/0004-executed-content-model.md)).

## Where the rules live

All PDF/A checks are dispatched from the `checks` slice in `ValidatePDFABytes`.
They are grouped across files by concern:

| File | Rules |
|------|-------|
| `pdfa.go` | Dispatch + most rules (font embedding, colour, metadata, annotations, output intents, transparency) |
| `pdfa_levela.go` | Level A: conformance declaration, tagged structure, language |
| `final_rules.go` | Catalog prohibitions, trigger events, halftones, inherited XObjects |
| `content_operators.go` | Content-stream operator whitelist, named resources |
| `filestructure.go` | Byte-level structure rules over the raw file (`Document.Offsets`) |
| `fonts.go` / `fontprog.go` / `font_encodings.go` / `cff_strings.go` | Font-dictionary rules; sfnt/CFF/Type1 program parsing |
| `xmp.go` / `xmp_schemas.go` | XMP metadata parsing and schema validation |
| `function.go` / `function_ps.go` | PDF function objects (types 0/2/3/4), used by tint transforms and shadings |

The other standards each own their file(s): `pdfua.go`, `pdfua_content.go`,
`pdfua_struct.go`, `pdfua_tablegrid.go`, `pdfua2.go`, `pdfx.go`, `pdfx_color.go`,
`pdfvt.go`, `pdfr.go`, `dpart.go`, `facturx.go`, `order_x.go`. `violations.go`
holds the shared `Violation` interface and is the canonical statement of the
contract above.

To add a rule, see
[CONTRIBUTING](../CONTRIBUTING.md#adding-a-validation-rule).
