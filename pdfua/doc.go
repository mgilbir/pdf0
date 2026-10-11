// Package pdfua is PDF/UA (ISO 14289), the accessibility profile of PDF: the
// findings its validator reports. Reach for it when a document must be usable
// with assistive technology — its reading order, headings, tables, alternate
// text and Unicode text recoverable from the file.
//
// The entry points take a *pdf0.Document, so they live in the root package:
// pdf0.ValidatePDFUA checks PDF/UA-1 (ISO 14289-1) and pdf0.ValidatePDFUA2
// checks PDF/UA-2 (ISO 14289-2), each with a Context variant. Both return
// Violation values. Most of the checks walk the tagged structure tree, or walk
// page content to correlate it with the tree; PDF/UA-2 runs the PDF/UA-1
// checks with its own identification and version rules, and the differences
// the two standards have.
//
//	for _, v := range pdf0.ValidatePDFUA(doc) {
//		fmt.Println(v.Clause, v.Message)
//	}
//
// A finding names its ISO 14289 clause in Violation.Clause (where the other
// validators' findings have Rule), and the part it is against in
// Violation.Part. The checks shared by both parts keep their ISO 14289-1
// clause numbers in a PDF/UA-2 result. An empty result means the implemented
// checks passed, not full PDF/UA conformance. A finding under the reserved
// clause "limit" or "internal" says pdf0 could not finish a check, not that the
// document is wrong; pdf0.IsCheckerFinding tells the two apart.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/pdfua.md lists the rule
// families, the clause each reports under, and the corpora the checks are
// measured against.
package pdfua
