package sign

import "github.com/mgilbir/pdf0/internal/bridge"

// The entry points the root package calls over its internal view. They are
// not exported, because no caller outside this module can name core.View;
// see internal/bridge. The CMS and time-stamp builders, the field-name join
// and the bare CMS check name no internal type, but are the signing writers'
// plumbing and the tests', not API (docs audit 2026-10-11 D60), and cross the
// same way.
func init() {
	bridge.SignVerifySignatures.Install(verifySignatures)
	bridge.SignValidatePAdES.Install(validatePAdES)
	bridge.SignDSSCerts.Install(dssCerts)
	bridge.SignDSSRevocationMaterial.Install(dssRevocationMaterial)
	bridge.SignQualifiedFieldName.Install(qualifiedFieldName)
	bridge.SignFieldPartialName.Install(fieldPartialName)
	bridge.SignBuildSignedData.Install(buildSignedDataFull)
	bridge.SignBuildTimestampToken.Install(buildTimestampToken)
	bridge.SignJoinFieldName.Install(joinFieldName)
	bridge.SignVerifyCMS.Install(verifyCMS)
}
