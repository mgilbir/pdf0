// Package sign holds what PDF digital signatures report: the verdicts of
// signature verification, the PAdES baseline levels, certificate revocation,
// and the caller's trust decisions. Reach for it to decide whether a signed
// PDF can be believed: that it is unchanged since signing, or changed only in
// permitted ways, and that its signer is one you trust.
//
// The entry points take a *pdf0.Document, so they live in the root package.
// pdf0.Document.VerifySignatures returns one Result per signature and document
// time-stamp, and pdf0.Document.ValidatePAdES one PAdESResult per approval
// signature; both are judged against the file the document was read from.
// pdf0.Document.WriteSigned, WriteSignedIncremental and
// WriteArchivalTimestamp produce signatures, from PAdES B-B to B-LTA.
//
//	roots := x509.NewCertPool() // then AddCert the roots of the signers you accept
//	results, err := doc.VerifySignatures(sign.VerifyOptions{Roots: roots})
//	if err != nil {
//		return err // verification could not run; this is not "no signatures"
//	}
//	for _, r := range results {
//		trusted := r.Intact() && r.TrustedChain && r.Revocation.Status != sign.RevocationRevoked
//		fmt.Println(r.Field, trusted)
//	}
//
// A Result's fields are independent claims, and no one of them implies
// another. Read integrity with Result.Intact — the signature verifies and every
// change made after signing is a permitted one — or Result.DocumentUnmodified
// when nothing may have changed at all: Valid alone accepts a document whose
// content was changed by an incremental update after signing. Read who signed
// with TrustedChain and Revocation. Trust comes only from the roots passed in
// VerifyOptions: with none, TrustedChain is always false. Pass the roots of the
// signers you accept, never a web PKI pool such as x509.SystemCertPool.
// Revocation is checked against the CRLs and OCSP responses the document
// carries; nothing is fetched from the network.
//
// https://github.com/mgilbir/pdf0/blob/main/docs/signing.md says which verdict
// to read, how to sign, and what each check covers.
package sign
