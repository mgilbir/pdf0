package pdf0

// Violation is the common face of every validator finding. Each validator
// keeps its own concrete type — ValidationError (PDF/A), UAViolation (PDF/UA),
// PDFXViolation, PDFVTViolation, PDFRViolation, DPartViolation — with the
// fields and Error formatting of its standard, but all of them satisfy this
// interface, so findings from different validators can be collected, filtered
// and reported together:
//
//	var all []pdf0.Violation
//	for _, e := range pdf0.ValidatePDFA(doc, pdf0.PDFA2b) {
//		all = append(all, e)
//	}
//	for _, e := range pdf0.ValidatePDFUA(doc) {
//		all = append(all, e)
//	}
//
// (The Factur-X and Order-X results carry formalis.Violation values, an
// external type this package cannot extend.)
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

// RuleID returns the ISO 19005 clause identifier.
func (e ValidationError) RuleID() string { return e.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (e ValidationError) ObjectNum() int { return e.Object }

// RuleID returns the ISO 14289 clause identifier.
func (v UAViolation) RuleID() string { return v.Clause }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v UAViolation) ObjectNum() int { return v.Object }

// RuleID returns the PDF/X rule identifier.
func (v PDFXViolation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v PDFXViolation) ObjectNum() int { return v.Object }

// RuleID returns the PDF/VT rule identifier.
func (v PDFVTViolation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v PDFVTViolation) ObjectNum() int { return v.Object }

// RuleID returns the PDF/R rule identifier.
func (v PDFRViolation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v PDFRViolation) ObjectNum() int { return v.Object }

// RuleID returns the ISO 32000-2 DPart subclause.
func (v DPartViolation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v DPartViolation) ObjectNum() int { return v.Object }

// Every finding type satisfies Violation.
var _ = []Violation{
	ValidationError{},
	UAViolation{},
	PDFXViolation{},
	PDFVTViolation{},
	PDFRViolation{},
	DPartViolation{},
}
