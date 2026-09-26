package pdf0

import (
	"context"

	"github.com/mgilbir/formalis"

	"github.com/mgilbir/pdf0/facturx"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/pdfa"
)

// Factur-X and Order-X, from the document's side. The container rules live in
// the facturx package; these are the entry points that give them a Document and
// the PDF/A-3 base verdict they compose.

// facturxRun prepares a view for one container validation: a private per-run
// cache, and the PDF/A-3 checker the container rules compose but cannot reach
// on their own.
func facturxRun(ctx context.Context, doc *Document) core.View {
	runDoc := *doc // dictcopy: a shallow per-run copy; it shares Objects and Trailer by design and the validators never write either
	runDoc.valCache = newValidationCache(doc, core.NewCanceler(ctx))
	v := runDoc.view()
	facturxSetPDFAChecker(v, func(core.View) []pdfa.Violation {
		// PDF/A-3b: what a Factur-X or Order-X container is required to be.
		// A container declaring 3a or 3u satisfies it too — a 3b target
		// accepts the letters above it — and is not held here to the rest of
		// its own claim, which is a PDF/A question: ValidatePDFA at
		// LevelDeclared answers it. The byte-level half reads the file doc
		// was read from, as ValidatePDFA always does.
		return ValidatePDFAContext(ctx, doc, pdfa.PDFA3b)
	})
	return v
}

// ValidateFacturX validates a Factur-X invoice container: the PDF/A-3 base, the
// container structure, and the embedded CII invoice XML. The PDF/A-3 base's
// byte-level rules read the file doc was read from (see ValidatePDFA); it no
// longer takes the bytes as a parameter, which could name a different file.
func ValidateFacturX(doc *Document) facturx.Result {
	return ValidateFacturXContext(context.Background(), doc)
}

// ValidateFacturXContext is ValidateFacturX under a context. The deadline and
// cancellation reach the PDF/A-3 pass and the invoice rule engine alike; a run
// that stops early says so with a "limit" finding rather than reporting a
// conformant document.
func ValidateFacturXContext(ctx context.Context, doc *Document) facturx.Result {
	if doc == nil {
		return facturx.Result{Violations: []facturx.Violation{{Rule: finding.LimitRule, Message: nilDocumentMessage}}}
	}
	return facturxValidateContext(ctx, facturxRun(ctx, doc))
}

// ValidateOrderX validates an Order-X order container, the Order-X counterpart
// of ValidateFacturX.
func ValidateOrderX(doc *Document) facturx.OrderXResult {
	return ValidateOrderXContext(context.Background(), doc)
}

// ValidateOrderXContext is ValidateOrderX under a context.
func ValidateOrderXContext(ctx context.Context, doc *Document) facturx.OrderXResult {
	if doc == nil {
		return facturx.OrderXResult{Violations: []facturx.OrderXViolation{{Rule: finding.LimitRule, Message: nilDocumentMessage}}}
	}
	return facturxValidateOrderContext(ctx, facturxRun(ctx, doc))
}

// EmbedFacturX embeds the CII invoice XML into doc as the associated file a
// Factur-X container requires, and writes the Factur-X XMP extension schema and
// identification into the document's metadata. doc must already be PDF/A-3 (or
// claim no PDF/A part, and then claims 3b); the result is a Factur-X container
// that ValidateFacturX accepts after a round trip through Write and Read.
//
// It edits the document rather than rebuilding any of it: other attachments,
// other metadata and the PDF/A conformance letter are kept, and an invoice the
// document already carries is replaced, so embedding twice leaves one invoice.
// It returns an error, and leaves doc unchanged, when invoiceXML is empty or
// not well-formed XML (facturx.ErrNotXML), the profile is unknown, the title
// cannot be written as XMP text, the document declares a PDF/A part other
// than 3, or its existing metadata or name tree cannot be edited without
// guessing. A nil or Locked document is refused too, since the invoice would
// be written in the clear under its /Encrypt.
func EmbedFacturX(doc *Document, invoiceXML []byte, profile formalis.Profile, title string) error {
	if doc == nil {
		return errNilDocument
	}
	if doc.Locked() {
		return errLockedTarget("embedding a Factur-X invoice")
	}
	return facturxEmbed(doc.view(), invoiceXML, profile, title)
}

// EmbedOrderX is EmbedFacturX for an Order-X order: the Cross Industry Order
// XML is embedded as order-x.xml and identified in the Order-X XMP namespace.
// docType is ORDER, ORDER_CHANGE or ORDER_RESPONSE; any other is refused, as
// are the inputs EmbedFacturX refuses.
func EmbedOrderX(doc *Document, orderXML []byte, profile facturx.OrderXProfile, docType, title string) error {
	if doc == nil {
		return errNilDocument
	}
	if doc.Locked() {
		return errLockedTarget("embedding an Order-X order")
	}
	return facturxEmbedOrder(doc.view(), orderXML, profile, docType, title)
}
