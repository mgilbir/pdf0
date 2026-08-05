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

// CheckCertRevocation reports what the supplied CRLs and OCSP responses say
// about cert, which issuer is expected to have issued.
func CheckCertRevocation(cert, issuer *x509.Certificate, crls, ocsps [][]byte) sign.RevocationInfo {
	return sign.CheckCertRevocation(cert, issuer, crls, ocsps)
}

// VerifySignatures verifies every signature in the document against the
// system's root store. raw must be the bytes the document was read from: a
// signature covers a byte range of the file, not the object graph.
func (d *Document) VerifySignatures(raw []byte) []sign.Result {
	return sign.VerifySignatures(d.view(), raw)
}

// VerifySignaturesWithRoots is VerifySignatures against a caller-supplied trust
// anchor set. A nil pool means the system store.
func (d *Document) VerifySignaturesWithRoots(raw []byte, roots *x509.CertPool) []sign.Result {
	return sign.VerifySignaturesWithRoots(d.view(), raw, roots)
}

// ValidatePAdES reports the PAdES baseline level each signature reaches.
func (d *Document) ValidatePAdES(raw []byte) []sign.PAdESResult {
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
