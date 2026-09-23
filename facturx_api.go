package pdf0

import (
	"context"

	"github.com/mgilbir/formalis"

	"github.com/mgilbir/pdf0/facturx"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/pdfa"
)

// Factur-X and Order-X, from the document's side. The container rules live in
// the facturx package; these are the entry points that give them a Document and
// the PDF/A-3 base verdict they compose.

// facturxRun prepares a view for one container validation: a private per-run
// cache, and the PDF/A-3 checker the container rules compose but cannot reach
// on their own.
func facturxRun(ctx context.Context, doc *Document, rawData []byte) core.View {
	runDoc := *doc // dictcopy: a shallow per-run copy; it shares Objects and Trailer by design and the validators never write either
	runDoc.valCache = newValidationCache(core.NewCanceler(ctx))
	v := runDoc.view()
	facturx.SetPDFAChecker(v, func(core.View) []pdfa.Violation {
		return ValidatePDFABytesContext(ctx, doc, pdfa.PDFA3b, rawData)
	})
	return v
}

// ValidateFacturX validates a Factur-X invoice container: the PDF/A-3 base, the
// container structure, and the embedded CII invoice XML. rawData must be the
// bytes the document was read from.
func ValidateFacturX(doc *Document, rawData []byte) facturx.Result {
	return ValidateFacturXContext(context.Background(), doc, rawData)
}

// ValidateFacturXContext is ValidateFacturX under a context. The deadline and
// cancellation reach the PDF/A-3 pass and the invoice rule engine alike; a run
// that stops early says so with a "limit" finding rather than reporting a
// conformant document.
func ValidateFacturXContext(ctx context.Context, doc *Document, rawData []byte) facturx.Result {
	return facturx.ValidateContext(ctx, facturxRun(ctx, doc, rawData), rawData)
}

// ValidateOrderX validates an Order-X order container, the Order-X counterpart
// of ValidateFacturX.
func ValidateOrderX(doc *Document, rawData []byte) facturx.OrderXResult {
	return ValidateOrderXContext(context.Background(), doc, rawData)
}

// ValidateOrderXContext is ValidateOrderX under a context.
func ValidateOrderXContext(ctx context.Context, doc *Document, rawData []byte) facturx.OrderXResult {
	return facturx.ValidateOrderContext(ctx, facturxRun(ctx, doc, rawData), rawData)
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
// See facturx.Embed for what it refuses — empty or non-XML input among it. A
// nil or Locked document is refused too, since the invoice would be written in
// the clear under its /Encrypt.
func EmbedFacturX(doc *Document, invoiceXML []byte, profile formalis.Profile, title string) error {
	if doc == nil {
		return errNilDocument
	}
	if doc.Locked() {
		return errLockedTarget("embedding a Factur-X invoice")
	}
	return facturx.Embed(doc.view(), invoiceXML, profile, title)
}

// EmbedOrderX is EmbedFacturX for an Order-X order: the Cross Industry Order
// XML is embedded as order-x.xml and identified in the Order-X XMP namespace.
// docType is ORDER, ORDER_CHANGE or ORDER_RESPONSE.
func EmbedOrderX(doc *Document, orderXML []byte, profile facturx.OrderXProfile, docType, title string) error {
	if doc == nil {
		return errNilDocument
	}
	if doc.Locked() {
		return errLockedTarget("embedding an Order-X order")
	}
	return facturx.EmbedOrder(doc.view(), orderXML, profile, docType, title)
}
