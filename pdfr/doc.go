// Package pdfr is PDF/R (ISO 23504-1), the raster profile of PDF for scanned
// documents and a modern replacement for fax: the findings its validator
// reports, and the namespace of its XMP identification. Reach for it when a
// file must be pages of raster images and nothing else.
//
// The entry point takes a *pdf0.Document, so it lives in the root package:
// pdf0.ValidatePDFR (and pdf0.ValidatePDFRContext) checks a document and
// returns Violation values.
//
//	for _, v := range pdf0.ValidatePDFR(doc) {
//		fmt.Println(v)
//	}
//
// It checks the structural profile: PDF 2.0, no encryption, pdfrid:part
// identification in XMP (namespace NSPDFRID), and at least one page, each
// drawing raster images only, through permitted image filters. ISO 23504 is
// not openly published and no conformance corpus is available, so the rules
// are written from knowledge of the standard, and transparency is not checked.
// An empty result means none of the implemented checks fired, not a guarantee
// of full conformance. A finding under the reserved rule "limit" or "internal"
// says pdf0 could not finish a check, not that the document is wrong;
// pdf0.IsCheckerFinding tells the two apart.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/validators.md compares the
// validators and says what a result is worth.
package pdfr
