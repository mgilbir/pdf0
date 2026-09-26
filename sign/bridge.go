package sign

import "github.com/mgilbir/pdf0/internal/bridge"

// The entry points the root package calls over its internal view. They are
// not exported, because no caller outside this module can name core.View;
// see internal/bridge.
func init() {
	bridge.SignVerifySignatures.Install(verifySignatures)
	bridge.SignValidatePAdES.Install(validatePAdES)
	bridge.SignDSSCerts.Install(dssCerts)
	bridge.SignDSSRevocationMaterial.Install(dssRevocationMaterial)
	bridge.SignQualifiedFieldName.Install(qualifiedFieldName)
	bridge.SignFieldPartialName.Install(fieldPartialName)
}
