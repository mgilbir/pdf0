package pdf0

import (
	"crypto"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/mgilbir/pdf0/internal/bridge"
	"github.com/mgilbir/pdf0/sign"
)

// Digital signatures. The verification logic lives in the sign package, which
// works from the document seen as an object graph and from the file it was
// read from; these are the methods and names that make it reachable from a
// Document. Signature *production* stays in this package (sign.go,
// doctimestamp.go): writing a signed file means laying out a whole new
// document, which is the writer's job, not the verifier's.

// The sign package's plumbing that signature production shares with it,
// reached through internal/bridge rather than exported (docs audit
// 2026-10-11 D60): the detached CMS SignedData builder (with an optional
// signature time-stamp), the RFC 3161 token builder, and the join of a
// qualified field name.
var (
	signBuildSignedData     = bridge.Resolve[func(cert *x509.Certificate, key crypto.Signer, content []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer) ([]byte, error)](bridge.SignBuildSignedData)
	signBuildTimestampToken = bridge.Resolve[func(imprint []byte, tsaCert *x509.Certificate, tsaKey crypto.Signer, genTime time.Time) ([]byte, error)](bridge.SignBuildTimestampToken)
	signJoinFieldName       = bridge.Resolve[func(prefix, part string) string](bridge.SignJoinFieldName)
)

// VerifySignatures verifies every signature and document time-stamp in the
// document against the file it was read from (Document.Source): a signature
// covers bytes of that file, not the object graph, so a Document built in
// memory has no signature that verifies.
//
// Trust is only ever established against opts.Roots. With nil roots no chain
// is built and every result has TrustedChain false: a signature from an
// unknown signer is then indistinguishable from one from your CA. Do not pass
// x509.SystemCertPool() or another web PKI pool: those roots vouch for domain
// names, not for document signers. See sign.Result for what each field
// promises; Intact and DocumentUnmodified are the integrity verdicts.
//
// The error is non-nil only when verification could not run to completion
// (an internal failure on a hostile file, recovered rather than crashing the
// caller); the results are then nil and must not be read as "no signatures".
func (d *Document) VerifySignatures(opts sign.VerifyOptions) (res []sign.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("verifying signatures: recovered from panic: %v", r)
		}
	}()
	return signVerifySignatures(d.view(), d.signedFile(d.canceler()), opts), nil
}

// ValidatePAdES reports, for each approval signature, the PAdES baseline level
// its material reaches and whether it conforms (sign.PAdESResult), against the
// file the document was read from. opts decides trust exactly as for
// VerifySignatures. The error has the same meaning as VerifySignatures's.
func (d *Document) ValidatePAdES(opts sign.VerifyOptions) (res []sign.PAdESResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("validating PAdES signatures: recovered from panic: %v", r)
		}
	}()
	return signValidatePAdES(d.view(), d.signedFile(d.canceler()), opts), nil
}

// DSSRevocationMaterial returns the CRLs and OCSP responses the document's
// Document Security Store carries.
func (d *Document) DSSRevocationMaterial() (crls, ocsps [][]byte) {
	return signDSSRevocationMaterial(d.view())
}

// DSSCerts returns the certificates the document's Document Security Store
// carries.
func (d *Document) DSSCerts() []*x509.Certificate {
	return signDSSCerts(d.view())
}
