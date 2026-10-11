// Package pdfx is PDF/X (ISO 15930), the print-exchange profile of PDF: its
// levels and the findings its validator reports. Reach for it when a file is
// going to a printer that expects PDF/X-1a, -3, -4, -4p or -6.
//
// The entry point takes a *pdf0.Document, so it lives in the root package:
// pdf0.ValidatePDFX (and pdf0.ValidatePDFXContext) checks a document against a
// Level and returns Violation values. Name the level the file must meet; the
// zero Level, LevelDeclared, validates against the level the file itself
// declares, and a file that declares none gets a "limit" finding instead of
// results.
//
//	for _, v := range pdf0.ValidatePDFX(doc, pdfx.PDFX4) {
//		fmt.Println(v)
//	}
//
// The checks are conservative and structural: identification, the PDF/X
// output intent and its ICC destination profile, the /Trapped flag, the page
// geometry boxes, font embedding, device colour coverage, forbidden features,
// transparency where the level predates it, and the prohibition on encryption.
// The PDF/X-4 rules are calibrated against the valid Cal Poly PDF/VT-1 test
// suite, whose files are conforming PDF/X-4. An empty result means none of the
// implemented checks fired, not a guarantee of full conformance. A finding
// under the reserved rule "limit" or "internal" says pdf0 could not finish a
// check, not that the document is wrong; pdf0.IsCheckerFinding tells the two
// apart.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/validators.md compares the
// validators and says what a result is worth.
package pdfx
