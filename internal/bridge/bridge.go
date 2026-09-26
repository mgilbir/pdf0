// Package bridge carries the calls that cross from the root package — and
// from one public package to another — into a public package's
// implementation over the internal document view, core.View.
//
// The public packages (pdfa, pdfua, pdfx, pdfvt, pdfr, dpart, facturx, sign,
// images) hold their rules over core.View, which only this module can name.
// Exporting those entry points put about thirty functions on pkg.go.dev that
// no caller outside the module could call (audit 2026-09-22 C163). They are
// unexported now, and each package installs them here from an init function;
// the package that calls one resolves it into a typed variable of its own
// when it is initialised. Go runs a package's init functions before any
// package that imports it starts initialising, so every entry is installed
// before anything can resolve it.
//
// The entries are typed as any because the public package's own types
// (pdfa.Violation, sign.Result, …) appear in the signatures, and this package
// cannot import the public packages that import it. The type is checked once,
// by Resolve, when the consumer is initialised: a mismatch or a missing
// install panics at the start of every program and test binary that links
// the consumer, never on a later call. After initialisation the entries are
// read-only, so the bridge holds no mutable global state; the per-run hooks
// (pdfa's embedded-file checker, facturx's PDF/A-3 checker) are still
// installed on each run's core.Run, as before.
package bridge

import "fmt"

// Entry is one entry point a public package installs for another package to
// call. Its zero value is not usable: every Entry is one of the variables
// below, named after the function it carries.
type Entry struct {
	name     string
	fn       any
	resolved bool
}

// Install sets the entry's function. It is called from the owning package's
// init, once.
func (e *Entry) Install(fn any) {
	if e.fn != nil {
		panic("bridge: " + e.name + " installed twice")
	}
	if e.resolved {
		panic("bridge: " + e.name + " installed after it was resolved")
	}
	if fn == nil {
		panic("bridge: " + e.name + " installed as nil")
	}
	e.fn = fn
}

// Resolve returns e's function as F. It panics when the owner has not
// installed it or installed a function of another type; both are programming
// errors this module makes, and both surface when the consumer is
// initialised.
func Resolve[F any](e *Entry) F {
	e.resolved = true
	f, ok := e.fn.(F)
	if !ok {
		panic(fmt.Sprintf("bridge: %s is %T, and its caller expects %T", e.name, e.fn, *new(F)))
	}
	return f
}

// Name is the entry's function, as "package.function".
func (e *Entry) Name() string { return e.name }

// Installed reports whether the owning package has installed the entry.
func (e *Entry) Installed() bool { return e.fn != nil }

// Resolved reports whether a consumer has resolved the entry.
func (e *Entry) Resolved() bool { return e.resolved }

// Entries is every entry, for the test that each one is installed and
// resolved: an entry nobody installs panics only when resolved, and one
// nobody resolves is dead.
func Entries() []*Entry {
	return []*Entry{
		DPartValidateView, DPartValidateHierarchy,
		FacturXValidateContext, FacturXValidateOrderContext, FacturXEmbed, FacturXEmbedOrder,
		FacturXFindAttachment, FacturXSetPDFAChecker,
		ImagesWalk,
		PDFAValidateView, PDFAResolveTarget, PDFASetEmbeddedChecker,
		PDFRValidateView, PDFUAValidateView, PDFVTValidateView, PDFXValidateView,
		SignVerifySignatures, SignValidatePAdES, SignDSSCerts, SignDSSRevocationMaterial,
		SignQualifiedFieldName, SignFieldPartialName,
	}
}

// The entries, by owning package.
var (
	DPartValidateView      = &Entry{name: "dpart.validateView"}
	DPartValidateHierarchy = &Entry{name: "dpart.validateHierarchy"}

	FacturXValidateContext      = &Entry{name: "facturx.validateContext"}
	FacturXValidateOrderContext = &Entry{name: "facturx.validateOrderContext"}
	FacturXEmbed                = &Entry{name: "facturx.embed"}
	FacturXEmbedOrder           = &Entry{name: "facturx.embedOrder"}
	FacturXFindAttachment       = &Entry{name: "facturx.findAttachment"}
	FacturXSetPDFAChecker       = &Entry{name: "facturx.setPDFAChecker"}
	ImagesWalk                  = &Entry{name: "images.walk"}
	PDFAValidateView            = &Entry{name: "pdfa.validateView"}
	PDFAResolveTarget           = &Entry{name: "pdfa.resolveTarget"}
	PDFASetEmbeddedChecker      = &Entry{name: "pdfa.setEmbeddedChecker"}
	PDFRValidateView            = &Entry{name: "pdfr.validateView"}
	PDFUAValidateView           = &Entry{name: "pdfua.validateView"}
	PDFVTValidateView           = &Entry{name: "pdfvt.validateView"}
	PDFXValidateView            = &Entry{name: "pdfx.validateView"}
	SignVerifySignatures        = &Entry{name: "sign.verifySignatures"}
	SignValidatePAdES           = &Entry{name: "sign.validatePAdES"}
	SignDSSCerts                = &Entry{name: "sign.dssCerts"}
	SignDSSRevocationMaterial   = &Entry{name: "sign.dssRevocationMaterial"}
	SignQualifiedFieldName      = &Entry{name: "sign.qualifiedFieldName"}
	SignFieldPartialName        = &Entry{name: "sign.fieldPartialName"}
)
