// Package facturx is the PDF side of Factur-X (also ZUGFeRD 2.x) electronic
// invoices and Order-X orders: hybrid PDF/A-3 documents that carry a
// human-readable invoice or order and its UN/CEFACT Cross Industry XML as an
// embedded associated file. Reach for it to check such a container, or to
// make one.
//
// The entry points take a *pdf0.Document, so they live in the root package:
//
//   - pdf0.ValidateFacturX returns a Result and pdf0.ValidateOrderX an
//     OrderXResult, each with a Context variant. Each checks the container —
//     PDF/A-3 conformance, the embedded XML and its attachment as an
//     associated file, and the fx: XMP metadata that identifies it — and then
//     runs the embedded XML through the formalis rule engine
//     (github.com/mgilbir/formalis): for an invoice, the EN 16931 core at the
//     declared profile or, when the metadata names a CIUS such as XRECHNUNG,
//     the rule set the invoice itself declares; for an order, its mandatory
//     head terms.
//   - pdf0.EmbedFacturX and pdf0.EmbedOrderX make a container: they attach the
//     XML to a PDF/A-3 document and write the metadata that identifies it.
//
// For example:
//
//	res := pdf0.ValidateFacturX(doc)
//	for _, v := range res.Violations {
//		fmt.Println(v)
//	}
//
// The result is a struct rather than a slice of findings because it also
// carries the extracted XML, the profile the container declared, and what the
// rule engine did not evaluate. Result.Violations is the verdict, and an empty
// one is the clean answer, with the caveat every pdf0 validator carries: the
// PDF/A-3 base is a partial validator. Result.InvoiceWarnings holds the
// engine's advisory findings, which do not reject an invoice, and
// Result.InvoiceComplete says whether the engine evaluated every rule it
// could. Violation and OrderXViolation satisfy pdf0.Violation like every
// other finding type, and a finding under the reserved rule "limit" or
// "internal" says pdf0 could not finish a check; pdf0.IsCheckerFinding tells
// it apart.
//
// The container checks are calibrated against a corpus of conforming
// Factur-X and ZUGFeRD invoices across every profile. No public Order-X
// conformance corpus is bundled.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/validators.md compares the
// validators and shows how to read a Result.
package facturx
