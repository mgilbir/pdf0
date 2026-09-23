package facturx

import (
	"github.com/mgilbir/pdf0/internal/bridge"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/pdfa"
)

// The entry points the root package calls over its internal view. They are
// not exported, because no caller outside this module can name core.View;
// see internal/bridge.
func init() {
	bridge.FacturXValidateContext.Install(validateContext)
	bridge.FacturXValidateOrderContext.Install(validateOrderContext)
	bridge.FacturXEmbed.Install(embedInvoice)
	bridge.FacturXEmbedOrder.Install(embedOrder)
	bridge.FacturXFindAttachment.Install(findAttachment)
	bridge.FacturXSetPDFAChecker.Install(func(v core.View, f func(core.View) []pdfa.Violation) {
		setPDFAChecker(v, f)
	})
}
