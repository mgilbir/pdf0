// Package pdfvt is PDF/VT (ISO 16612-2), the variable and transactional
// printing profile of PDF: the findings its validator reports. Reach for it
// when a file carries many records — statements, invoices, mailings — for a
// print workflow that expects PDF/VT-1 or PDF/VT-2.
//
// The entry points take a *pdf0.Document, so they live in the root package:
// pdf0.ValidatePDFVT checks PDF/VT-1 and pdf0.ValidatePDFVT2 checks PDF/VT-2,
// each with a Context variant, and both return Violation values. A PDF/VT
// file is a PDF/X file that also carries a document part (DPart) hierarchy
// marking its record boundaries and identifies itself as PDF/VT in its XMP
// metadata, so the validator composes the PDF/X and DPart validators with the
// PDF/VT requirements. PDF/VT-1 is based on PDF/X-4 and PDF/VT-2 on PDF/X-5;
// pdf0 has no PDF/X-5 validator, so PDF/VT-2 is checked against the PDF/X-4
// rules with the reference-XObject prohibition lifted.
//
//	for _, v := range pdf0.ValidatePDFVT(doc) {
//		fmt.Println(v)
//	}
//
// A finding's Rule says where it came from: a PDF/X base finding carries the
// prefix "pdfx-4/" (PDF/VT-1) or "pdfx-5/" (PDF/VT-2), a DPart finding
// "dpart/", and a PDF/VT finding none. An empty result means none of the
// implemented checks fired, not a guarantee of full conformance. A finding
// under the reserved rule "limit" or "internal", never prefixed, says pdf0
// could not finish a check, not that the document is wrong;
// pdf0.IsCheckerFinding tells the two apart.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/validators.md compares the
// validators and says what a result is worth.
package pdfvt
