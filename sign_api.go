package pdf0

import (
	"crypto/x509"

	"github.com/mgilbir/pdf0/sign"
)

// Digital signatures. The verification logic lives in the sign package, which
// works from the document seen as an object graph; these are the methods and
// names that make it reachable from a Document. Signature *production* stays in
// this package (sign.go, doctimestamp.go): writing a signed file means laying
// out a whole new document, which is the writer's job, not the verifier's.

// SignatureResult reports on one signature found in the document.
type SignatureResult = sign.SignatureResult

// PAdESLevel is the ETSI EN 319 142 conformance level a signature reaches.
type PAdESLevel = sign.PAdESLevel

// The PAdES baseline levels, in increasing order of what they carry.
const (
	PAdESNone = sign.PAdESNone
	PAdESBB   = sign.PAdESBB
	PAdESBT   = sign.PAdESBT
	PAdESBLT  = sign.PAdESBLT
	PAdESBLTA = sign.PAdESBLTA
)

// PAdESResult reports the PAdES level one signature reaches, and why.
type PAdESResult = sign.PAdESResult

// RevocationStatus is what the available revocation material says about a
// certificate.
type RevocationStatus = sign.RevocationStatus

// The revocation verdicts.
const (
	RevocationUnknown = sign.RevocationUnknown
	RevocationGood    = sign.RevocationGood
	RevocationRevoked = sign.RevocationRevoked
)

// RevocationInfo is the verdict on one certificate together with the material
// it was reached from.
type RevocationInfo = sign.RevocationInfo

// CheckCertRevocation reports what the supplied CRLs and OCSP responses say
// about cert, which issuer is expected to have issued.
func CheckCertRevocation(cert, issuer *x509.Certificate, crls, ocsps [][]byte) RevocationInfo {
	return sign.CheckCertRevocation(cert, issuer, crls, ocsps)
}

// VerifySignatures verifies every signature in the document against the
// system's root store. raw must be the bytes the document was read from: a
// signature covers a byte range of the file, not the object graph.
func (d *Document) VerifySignatures(raw []byte) []SignatureResult {
	return sign.VerifySignatures(d.view(), raw)
}

// VerifySignaturesWithRoots is VerifySignatures against a caller-supplied trust
// anchor set. A nil pool means the system store.
func (d *Document) VerifySignaturesWithRoots(raw []byte, roots *x509.CertPool) []SignatureResult {
	return sign.VerifySignaturesWithRoots(d.view(), raw, roots)
}

// ValidatePAdES reports the PAdES baseline level each signature reaches.
func (d *Document) ValidatePAdES(raw []byte) []PAdESResult {
	return sign.ValidatePAdES(d.view(), raw)
}

// DSSRevocationMaterial returns the CRLs and OCSP responses the document's
// Document Security Store carries.
func (d *Document) DSSRevocationMaterial() (crls, ocsps [][]byte) {
	return sign.DSSRevocationMaterial(d.view())
}

// DSSCerts returns the certificates the document's Document Security Store
// carries.
func (d *Document) DSSCerts() []*x509.Certificate {
	return sign.DSSCerts(d.view())
}
