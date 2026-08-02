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
	runDoc := *doc
	runDoc.valCache = newValidationCache(core.NewCanceler(ctx))
	v := runDoc.view()
	facturx.SetPDFAChecker(v, func(core.View) []pdfa.ValidationError {
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
// identification into the document's metadata. doc must already be PDF/A-3; the
// result is a Factur-X container that ValidateFacturX accepts after a round
// trip through Write and Read.
func EmbedFacturX(doc *Document, invoiceXML []byte, profile formalis.Profile, title string) error {
	return facturx.Embed(doc.view(), invoiceXML, profile, title)
}
