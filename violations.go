package pdf0

import (
	"github.com/mgilbir/pdf0/dpart"
	"github.com/mgilbir/pdf0/facturx"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfr"
	"github.com/mgilbir/pdf0/pdfua"
	"github.com/mgilbir/pdf0/pdfvt"
	"github.com/mgilbir/pdf0/pdfx"
)

// Violation is the common face of every validator finding. Each validator keeps
// its own concrete type — pdfa.Violation, pdfua.Violation, pdfx.Violation,
// pdfvt.Violation, pdfr.Violation, dpart.Violation, facturx.Violation and
// facturx.OrderXViolation — with the fields and Error formatting of its
// standard, but all of them satisfy this interface, so findings from
// different validators can be collected, filtered and reported together:
//
//	var all []pdf0.Violation
//	for _, e := range pdf0.ValidatePDFA(doc, pdfa.PDFA2b) {
//		all = append(all, e)
//	}
//	for _, e := range pdf0.ValidatePDFUA(doc) {
//		all = append(all, e)
//	}
//
// The Factur-X and Order-X validators return a result struct rather than a
// slice, because they carry the extracted invoice XML and its coverage
// alongside the findings, but the findings themselves are facturx.Violation
// and facturx.OrderXViolation values and satisfy this interface like any
// other, IsCheckerFinding included.
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
	pdfa.Violation{},
	pdfua.Violation{},
	pdfx.Violation{},
	pdfvt.Violation{},
	pdfr.Violation{},
	dpart.Violation{},
	facturx.Violation{},
	facturx.OrderXViolation{},
}
