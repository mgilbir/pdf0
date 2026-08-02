package pdf0

// Violation is the common face of every validator finding. Each validator
// keeps its own concrete type — ValidationError (PDF/A), UAViolation (PDF/UA),
// PDFXViolation, PDFVTViolation, PDFRViolation, DPartViolation,
// FacturXViolation, OrderXViolation — with the fields and Error formatting of
// its standard, but all of them satisfy this interface, so findings from
// different validators can be collected, filtered and reported together:
//
//	var all []pdf0.Violation
//	for _, e := range pdf0.ValidatePDFA(doc, pdf0.PDFA2b) {
//		all = append(all, e)
//	}
//	for _, e := range pdf0.ValidatePDFUA(doc) {
//		all = append(all, e)
//	}
//
// There is no longer an exception. The Factur-X and Order-X validators return a
// result struct rather than a slice, because they carry the extracted invoice
// XML and its coverage alongside the findings, but the findings themselves are
// FacturXViolation and OrderXViolation values and satisfy this interface like
// any other. They used to hold formalis.Violation, an external type this package
// could not extend, which also put them outside IsCheckerFinding — so a
// cancelled or panicking run had no way to say "pdf0 stopped early" that a
// caller could tell apart from a conformance failure. That is why those two
// validators had no Context variant, and why they have one now.
type Violation interface {
	error
	// RuleID returns the identifier of the violated rule — an ISO clause like
	// "6.1.3" or a short rule name like "output-intent" — as carried in the
	// concrete type's Rule (or Clause) field.
	RuleID() string
	// ObjectNum returns the object number the finding anchors to, 0 if the
	// finding is not tied to a specific object.
	ObjectNum() int
}

// Every finding type satisfies Violation.
var _ = []Violation{
	ValidationError{},
	UAViolation{},
	PDFXViolation{},
	PDFVTViolation{},
	PDFRViolation{},
	DPartViolation{},
	FacturXViolation{},
	OrderXViolation{},
}
