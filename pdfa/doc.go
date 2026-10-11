// Package pdfa is PDF/A (ISO 19005), the archival profile of PDF: its
// conformance levels, the findings its validator reports, and the pieces a
// builder needs to produce a conforming file. Reach for it when a document
// must be checked against a PDF/A level, or built to one.
//
// The entry points take a *pdf0.Document, so they live in the root package:
// pdf0.ValidatePDFA (and pdf0.ValidatePDFAContext) checks a document against a
// Level, pdf0.NewPDFADocument starts a document that claims one,
// pdf0.Document.Save writes a document only if it passes the level it claims,
// and pdf0.Document.Repair fixes common failures in place. This package holds
// what they take and return:
//
//   - Level names a target: a part (1 to 4) and a conformance level. Levels
//     lists every one. Level B is the base; Level A adds the accessibility
//     requirements (tagged structure, natural language, Unicode mapping), and
//     Level U adds Unicode mapping alone; PDFA4E and PDFA4F are the PDF/A-4
//     variants. The zero value, LevelDeclared, validates against the level the
//     document itself declares.
//   - Violation is one finding, under the ISO 19005 clause that requires it.
//   - Skeleton, SkeletonWith, GenerateXMPMetadata and DefaultSRGBProfile are
//     for a caller assembling a PDF/A document by hand rather than through
//     pdf0.NewPDFADocument.
//
// For example:
//
//	for _, v := range pdf0.ValidatePDFA(doc, pdfa.PDFA2b) {
//		fmt.Println(v.Rule, v.Object, v.Message)
//	}
//
// An empty result means no implemented rule fired, not a guarantee of full
// conformance: the validator covers a subset of ISO 19005. A finding under the
// reserved rule "limit" or "internal" says pdf0 could not finish a check, not
// that the document is wrong; pdf0.IsCheckerFinding tells the two apart. The
// rules are calibrated against the veraPDF corpus, which is taken as the
// authority wherever it disagrees with a plain reading of the standard: a false
// positive rejects a conforming file and is far worse than a missed violation.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/validators.md compares the
// validators and says what a result is worth;
// https://github.com/mgilbir/pdf0/blob/main/docs/pdfa.md is how this one works
// inside.
package pdfa
