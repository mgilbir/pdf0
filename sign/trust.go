package sign

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"
)

// This file decides whom a signature is from, in the one sense a verifier can
// establish: a chain from the signer's certificate to a root the caller chose,
// valid at the validation time, for a purpose that includes signing
// documents. Time-stamp authorities get the same treatment with the
// time-stamping purpose.
//
// A chain that ends at a trusted root is not enough on its own. A root store
// trusts CAs for many purposes, and a web server's TLS certificate chains to
// the same roots as a document signer's: accepting any extended key usage made
// a TLS certificate a "trusted signer" (audit 2026-09-22 C22). The purpose is
// checked on the leaf and on every intermediate, the way RFC 5280 path
// validation constrains a path.

// Extended key usages that permit signing a document. emailProtection is
// S/MIME, which CAdES (and so PAdES) signing certificates have long used; the
// rest name document signing outright.
var (
	oidEKUDocumentSigning          = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 36}       // id-kp-documentSigning (RFC 9336)
	oidEKUAdobeAuthenticDocuments  = asn1.ObjectIdentifier{1, 2, 840, 113583, 1, 1, 5}       // Adobe Authentic Documents Trust
	oidEKUMicrosoftDocumentSigning = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 10, 3, 12} // Microsoft Document Signing
	oidExtKeyUsage                 = asn1.ObjectIdentifier{2, 5, 29, 15}
)

// permitsDocumentSigning reports whether a certificate's extended key usage
// allows it to sign documents: it carries no EKU extension (no restriction,
// RFC 5280 4.2.1.12), anyExtendedKeyUsage, emailProtection, or one of the
// document-signing purposes above.
func permitsDocumentSigning(c *x509.Certificate) bool {
	if len(c.ExtKeyUsage) == 0 && len(c.UnknownExtKeyUsage) == 0 {
		return true
	}
	for _, u := range c.ExtKeyUsage {
		if u == x509.ExtKeyUsageAny || u == x509.ExtKeyUsageEmailProtection {
			return true
		}
	}
	for _, u := range c.UnknownExtKeyUsage {
		if u.Equal(oidEKUDocumentSigning) || u.Equal(oidEKUAdobeAuthenticDocuments) || u.Equal(oidEKUMicrosoftDocumentSigning) {
			return true
		}
	}
	return false
}

// hasKeyUsageExtension reports whether c carries a key-usage extension; Go
// reports an absent one as KeyUsage 0, indistinguishable from an empty one.
func hasKeyUsageExtension(c *x509.Certificate) bool {
	for _, e := range c.Extensions {
		if e.Id.Equal(oidExtKeyUsage) {
			return true
		}
	}
	return false
}

// signerPolicy checks that a verified chain may sign documents: the leaf's key
// usage includes digitalSignature or contentCommitment (the purposes ETSI EN
// 319 412-2 gives a signing key), and neither the leaf nor any intermediate
// restricts its extended key usage to purposes that exclude document signing.
// The root, a trust anchor the caller chose, is not constrained.
func signerPolicy(chain []*x509.Certificate) error {
	leaf := chain[0]
	if !hasKeyUsageExtension(leaf) || leaf.KeyUsage&(x509.KeyUsageDigitalSignature|x509.KeyUsageContentCommitment) == 0 {
		return errors.New("the signer certificate's key usage includes neither digitalSignature nor contentCommitment")
	}
	for i, c := range chain {
		if i == len(chain)-1 && i > 0 {
			break // the trust anchor
		}
		if !permitsDocumentSigning(c) {
			who := "the signer certificate"
			if i > 0 {
				who = fmt.Sprintf("intermediate certificate %q", c.Subject.CommonName)
			}
			return fmt.Errorf("%s's extended key usage does not permit document signing", who)
		}
	}
	return nil
}

// signerChains builds cert's chains to roots at time at, using pool as
// intermediates, and keeps those the signing policy allows. A nil roots builds
// nothing: there is no default trust store (see VerifyOptions).
func signerChains(cert *x509.Certificate, pool []*x509.Certificate, roots *x509.CertPool, at time.Time) ([][]*x509.Certificate, error) {
	if roots == nil {
		return nil, errors.New("no trust roots were supplied")
	}
	chains, err := cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediatePool(cert, pool),
		CurrentTime:   at,
		// The purpose is checked by signerPolicy: x509 cannot express the
		// document-signing OIDs it does not know.
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return nil, err
	}
	var ok [][]*x509.Certificate
	var policyErr error
	for _, ch := range chains {
		if err := signerPolicy(ch); err != nil {
			policyErr = err
			continue
		}
		ok = append(ok, ch)
	}
	if len(ok) == 0 {
		return nil, policyErr
	}
	return ok, nil
}

// tsaChains builds a time-stamp authority's chains to roots at time at. The
// authority's own certificate must name id-kp-timeStamping (x509 would accept
// one with no extended key usage at all), and x509 checks that every
// certificate above it permits the purpose.
func tsaChains(cert *x509.Certificate, pool []*x509.Certificate, roots *x509.CertPool, at time.Time) ([][]*x509.Certificate, error) {
	if roots == nil {
		return nil, errors.New("no trust roots were supplied for time-stamp authorities")
	}
	if err := requireTimeStampingEKU(cert); err != nil {
		return nil, err
	}
	return cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediatePool(cert, pool),
		CurrentTime:   at,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	})
}

func intermediatePool(leaf *x509.Certificate, pool []*x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range pool {
		if c != leaf {
			p.AddCert(c)
		}
	}
	return p
}

// chainIssuers returns the certificate that issued each chain's leaf: the next
// certificate in the chain, whose key the chain's verification has checked the
// leaf's signature against. A self-signed leaf has no issuer to ask about it.
func chainIssuers(chains [][]*x509.Certificate) []*x509.Certificate {
	var out []*x509.Certificate
	seen := map[*x509.Certificate]bool{}
	for _, ch := range chains {
		if len(ch) < 2 || seen[ch[1]] {
			continue
		}
		seen[ch[1]] = true
		out = append(out, ch[1])
	}
	return out
}

// signingIssuers returns the certificates among candidates whose key verifies
// cert's signature. Without trust roots there is no verified chain, but an
// issuer found this way is at least the key that issued the certificate: a
// forged CA carrying the real one's name is not (audit 2026-09-22 C2), so its
// revocation answers are never consulted.
func signingIssuers(cert *x509.Certificate, candidates []*x509.Certificate) []*x509.Certificate {
	var out []*x509.Certificate
	seen := map[*x509.Certificate]bool{}
	for _, c := range candidates {
		if c == cert || seen[c] {
			continue
		}
		seen[c] = true
		if cert.CheckSignatureFrom(c) == nil {
			out = append(out, c)
		}
	}
	return out
}

// revocationMaterial is the Document Security Store's validation material.
type revocationMaterial struct {
	certs       []*x509.Certificate
	crls, ocsps [][]byte
}

// unrevokedChains drops every chain with a revoked certificate between its
// leaf and its trust anchor, each checked against the next certificate of the
// chain as its issuer at time at. When none is left it reports the revocation
// and an error naming the certificate.
func unrevokedChains(chains [][]*x509.Certificate, m revocationMaterial, at time.Time) ([][]*x509.Certificate, RevocationInfo, error) {
	if len(m.crls) == 0 && len(m.ocsps) == 0 {
		return chains, RevocationInfo{}, nil
	}
	var ok [][]*x509.Certificate
	var revoked RevocationInfo
	var err error
chains:
	for _, ch := range chains {
		for i := 1; i < len(ch)-1; i++ {
			info := CheckCertRevocation(ch[i], ch[i+1], m.crls, m.ocsps, at)
			if info.Status == RevocationRevoked {
				revoked = info
				err = fmt.Errorf("intermediate certificate %q is revoked (%s, at %v)", ch[i].Subject.CommonName, info.Source, info.RevokedAt)
				continue chains
			}
		}
		ok = append(ok, ch)
	}
	if len(ok) == 0 {
		return nil, revoked, err
	}
	return ok, RevocationInfo{}, nil
}

// leafRevocation reports cert's status through any of its issuers: several
// verified chains can name different ones (a re-issued CA certificate,
// cross-certification). A revocation through any of them wins; otherwise the
// first "good", otherwise the first answer at all.
func leafRevocation(cert *x509.Certificate, issuers []*x509.Certificate, m revocationMaterial, at time.Time) RevocationInfo {
	var good, unknown RevocationInfo
	for _, iss := range issuers {
		info := CheckCertRevocation(cert, iss, m.crls, m.ocsps, at)
		switch {
		case info.Status == RevocationRevoked:
			return info
		case info.Status == RevocationGood && good.Status != RevocationGood:
			good = info
		case info.Source != "" && unknown.Source == "":
			unknown = info
		}
	}
	if good.Status == RevocationGood {
		return good
	}
	return unknown
}
