package pdfa

import (
	"github.com/mgilbir/pdf0/internal/bridge"
	"github.com/mgilbir/pdf0/internal/core"
)

// The entry points the root package calls over its internal view. They are
// not exported, because no caller outside this module can name core.View;
// see internal/bridge.
func init() {
	bridge.PDFAValidateView.Install(validateView)
	bridge.PDFAResolveTarget.Install(resolveTarget)
	bridge.PDFASetEmbeddedChecker.Install(func(v core.View, f func(core.Canceler, []byte, core.Limits, int) (bool, bool)) {
		setEmbeddedChecker(v, f)
	})
}
