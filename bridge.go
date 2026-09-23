package pdf0

import (
	"context"
	"crypto/x509"

	"github.com/mgilbir/formalis"

	"github.com/mgilbir/pdf0/facturx"
	"github.com/mgilbir/pdf0/images"
	"github.com/mgilbir/pdf0/internal/bridge"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfr"
	"github.com/mgilbir/pdf0/pdfua"
	"github.com/mgilbir/pdf0/pdfvt"
	"github.com/mgilbir/pdf0/pdfx"
	"github.com/mgilbir/pdf0/sign"
)

// The public packages' entry points over the internal view. They are not
// exported from those packages, since no caller outside this module can name
// core.View; each package installs them in internal/bridge, and they are
// resolved here, into typed variables, when this package is initialised —
// which is also when a missing or mistyped entry panics (see bridge.Resolve).
// The Document methods and validator functions of this package are the API.

// embeddedPDFACheck is the per-run hook the PDF/A embedded-file rule calls
// (ISO 19005-2/3 6.9, 19005-4 6.9): whether embedded PDF bytes are a
// conforming PDF/A file, and whether that verdict is complete.
type embeddedPDFACheck = func(cancel core.Canceler, data []byte, lim core.Limits, depth int) (compliant, complete bool)

var (
	dpartValidateHierarchy = bridge.Resolve[func(core.View, func(rule, msg string, obj int))](bridge.DPartValidateHierarchy)

	facturxValidateContext      = bridge.Resolve[func(context.Context, core.View) facturx.Result](bridge.FacturXValidateContext)
	facturxValidateOrderContext = bridge.Resolve[func(context.Context, core.View) facturx.OrderXResult](bridge.FacturXValidateOrderContext)
	facturxEmbed                = bridge.Resolve[func(core.View, []byte, formalis.Profile, string) error](bridge.FacturXEmbed)
	facturxEmbedOrder           = bridge.Resolve[func(core.View, []byte, facturx.OrderXProfile, string, string) error](bridge.FacturXEmbedOrder)
	facturxFindAttachment       = bridge.Resolve[func(core.View, *object.Dictionary) (*object.Dictionary, string, int)](bridge.FacturXFindAttachment)
	facturxSetPDFAChecker       = bridge.Resolve[func(core.View, func(core.View) []pdfa.Violation)](bridge.FacturXSetPDFAChecker)

	imagesWalk = bridge.Resolve[func(core.View, func(images.ExtractedImage) bool)](bridge.ImagesWalk)

	pdfaValidateView       = bridge.Resolve[func(core.View, pdfa.Level) []pdfa.Violation](bridge.PDFAValidateView)
	pdfaResolveTarget      = bridge.Resolve[func(core.View, pdfa.Level) (pdfa.Level, []pdfa.Violation)](bridge.PDFAResolveTarget)
	pdfaSetEmbeddedChecker = bridge.Resolve[func(core.View, embeddedPDFACheck)](bridge.PDFASetEmbeddedChecker)

	pdfrValidateView  = bridge.Resolve[func(core.View) []pdfr.Violation](bridge.PDFRValidateView)
	pdfuaValidateView = bridge.Resolve[func(core.View, string) []pdfua.Violation](bridge.PDFUAValidateView)
	pdfvtValidateView = bridge.Resolve[func(core.View, string) []pdfvt.Violation](bridge.PDFVTValidateView)
	pdfxValidateView  = bridge.Resolve[func(core.View, pdfx.Level) []pdfx.Violation](bridge.PDFXValidateView)

	signVerifySignatures      = bridge.Resolve[func(core.View, core.SignedFile, sign.VerifyOptions) []sign.Result](bridge.SignVerifySignatures)
	signValidatePAdES         = bridge.Resolve[func(core.View, core.SignedFile, sign.VerifyOptions) []sign.PAdESResult](bridge.SignValidatePAdES)
	signDSSCerts              = bridge.Resolve[func(core.View) []*x509.Certificate](bridge.SignDSSCerts)
	signDSSRevocationMaterial = bridge.Resolve[func(core.View) (crls, ocsps [][]byte)](bridge.SignDSSRevocationMaterial)
	signQualifiedFieldName    = bridge.Resolve[func(core.View, *object.Dictionary) string](bridge.SignQualifiedFieldName)
	signFieldPartialName      = bridge.Resolve[func(core.View, *object.Dictionary) string](bridge.SignFieldPartialName)
)
