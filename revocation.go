package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/asn1"
	"math/big"
	"time"
)

// This file checks certificate revocation for PDF signatures using CRLs and OCSP
// responses — either passed in or read from a signature's Document Security Store
// (DSS). CRLs are parsed with the standard library (crypto/x509); OCSP responses
// are parsed here (RFC 6960) because the standard library has no OCSP package and
// this library stays dependency-free.

// RevocationStatus is the outcome of a revocation check.
type RevocationStatus int

const (
	RevocationUnknown RevocationStatus = iota // no usable material said either way
	RevocationGood                            // asserted not revoked
	RevocationRevoked                         // asserted revoked
)

func (s RevocationStatus) String() string {
	switch s {
	case RevocationGood:
		return "good"
	case RevocationRevoked:
		return "revoked"
	}
	return "unknown"
}

// RevocationInfo reports a certificate's revocation status and where it came from.
type RevocationInfo struct {
	Status    RevocationStatus
	Source    string    // "OCSP", "CRL" or "" when unknown
	RevokedAt time.Time // when the certificate was revoked, if revoked
}

// CheckCertRevocation determines the revocation status of cert (issued by issuer)
// from the supplied CRLs and OCSP responses (DER). Each source must be signed by
// issuer to be trusted. OCSP is consulted first; a definite verdict (good or
// revoked) is returned as soon as one source gives it.
func CheckCertRevocation(cert, issuer *x509.Certificate, crls, ocsps [][]byte) RevocationInfo {
	if cert == nil || issuer == nil {
		return RevocationInfo{}
	}
	for _, der := range ocsps {
		if info, ok := revocationFromOCSP(cert, issuer, der); ok {
			return info
		}
	}
	for _, der := range crls {
		if info, ok := revocationFromCRL(cert, issuer, der); ok {
			return info
		}
	}
	return RevocationInfo{}
}

// revocationFromCRL checks cert against a CRL (DER) that must be signed by issuer.
// A missing or mis-signed CRL yields (unknown, false); a valid CRL yields a
// definite good/revoked verdict.
func revocationFromCRL(cert, issuer *x509.Certificate, der []byte) (RevocationInfo, bool) {
	crl, err := x509.ParseRevocationList(der)
	if err != nil {
		return RevocationInfo{}, false
	}
	if crl.CheckSignatureFrom(issuer) != nil {
		return RevocationInfo{}, false
	}
	for _, e := range crl.RevokedCertificateEntries {
		if e.SerialNumber.Cmp(cert.SerialNumber) == 0 {
			return RevocationInfo{Status: RevocationRevoked, Source: "CRL", RevokedAt: e.RevocationTime}, true
		}
	}
	return RevocationInfo{Status: RevocationGood, Source: "CRL"}, true
}

// --- OCSP (RFC 6960) --------------------------------------------------------

var oidOCSPBasic = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}

type ocspResponseASN struct {
	Status        asn1.Enumerated
	ResponseBytes ocspResponseBytesASN `asn1:"explicit,optional,tag:0"`
}

type ocspResponseBytesASN struct {
	ResponseType asn1.ObjectIdentifier
	Response     []byte // OCTET STRING wrapping a BasicOCSPResponse
}

type basicOCSPResponseASN struct {
	TBSResponseData asn1.RawValue
	SignatureAlgo   pkixAlgorithmIdentifier
	Signature       asn1.BitString
	Certs           asn1.RawValue `asn1:"explicit,optional,tag:0"`
}

type responseDataASN struct {
	Version     int           `asn1:"optional,explicit,tag:0,default:0"`
	ResponderID asn1.RawValue // CHOICE [1] byName / [2] byKey
	ProducedAt  time.Time     `asn1:"generalized"`
	Responses   []singleResponseASN
	Extensions  asn1.RawValue `asn1:"optional,explicit,tag:1"`
}

type singleResponseASN struct {
	CertID     certIDASN
	CertStatus asn1.RawValue // CHOICE: [0] good / [1] revoked / [2] unknown (IMPLICIT)
	ThisUpdate time.Time     `asn1:"generalized"`
	NextUpdate time.Time     `asn1:"generalized,optional,explicit,tag:0"`
	Extensions asn1.RawValue `asn1:"optional,explicit,tag:1"`
}

type certIDASN struct {
	HashAlgorithm  pkixAlgorithmIdentifier
	IssuerNameHash []byte
	IssuerKeyHash  []byte
	SerialNumber   *big.Int
}

type revokedInfoASN struct {
	RevocationTime time.Time     `asn1:"generalized"`
	Reason         asn1.RawValue `asn1:"optional,explicit,tag:0"`
}

// revocationFromOCSP checks cert against an OCSP response (DER). The response must
// be signed by issuer (directly, or by a delegated responder certificate issued
// by issuer with the OCSP-signing extended key usage), and carry a status for
// cert. Otherwise it yields (unknown, false).
func revocationFromOCSP(cert, issuer *x509.Certificate, der []byte) (RevocationInfo, bool) {
	var resp ocspResponseASN
	if _, err := asn1.Unmarshal(der, &resp); err != nil {
		return RevocationInfo{}, false
	}
	if resp.Status != 0 || !resp.ResponseBytes.ResponseType.Equal(oidOCSPBasic) {
		return RevocationInfo{}, false // not "successful", or not a basic response
	}
	var basic basicOCSPResponseASN
	if _, err := asn1.Unmarshal(resp.ResponseBytes.Response, &basic); err != nil {
		return RevocationInfo{}, false
	}
	if !verifyOCSPSignature(&basic, issuer) {
		return RevocationInfo{}, false
	}
	var rd responseDataASN
	if _, err := asn1.Unmarshal(basic.TBSResponseData.FullBytes, &rd); err != nil {
		return RevocationInfo{}, false
	}
	for _, sr := range rd.Responses {
		if !matchesCertID(cert, issuer, sr.CertID) {
			continue
		}
		switch {
		case sr.CertStatus.Class == 2 && sr.CertStatus.Tag == 0: // good
			return RevocationInfo{Status: RevocationGood, Source: "OCSP"}, true
		case sr.CertStatus.Class == 2 && sr.CertStatus.Tag == 1: // revoked
			info := RevocationInfo{Status: RevocationRevoked, Source: "OCSP"}
			var ri revokedInfoASN
			if _, err := asn1.UnmarshalWithParams(sr.CertStatus.FullBytes, &ri, "tag:1"); err == nil {
				info.RevokedAt = ri.RevocationTime
			}
			return info, true
		default: // unknown
			return RevocationInfo{Status: RevocationUnknown, Source: "OCSP"}, true
		}
	}
	return RevocationInfo{}, false
}

// verifyOCSPSignature verifies the BasicOCSPResponse signature over its
// ResponseData, by the issuer directly or a delegated OCSP-signing responder.
func verifyOCSPSignature(basic *basicOCSPResponseASN, issuer *x509.Certificate) bool {
	algo, ok := sigAlgoFromOID(basic.SignatureAlgo.Algorithm)
	if !ok {
		return false
	}
	sig := basic.Signature.RightAlign()
	if issuer.CheckSignature(algo, basic.TBSResponseData.FullBytes, sig) == nil {
		return true
	}
	if len(basic.Certs.Bytes) == 0 {
		return false
	}
	certs, err := x509.ParseCertificates(basic.Certs.Bytes)
	if err != nil {
		return false
	}
	for _, c := range certs {
		if c.CheckSignatureFrom(issuer) != nil {
			continue
		}
		delegated := false
		for _, eku := range c.ExtKeyUsage {
			if eku == x509.ExtKeyUsageOCSPSigning {
				delegated = true
			}
		}
		if delegated && c.CheckSignature(algo, basic.TBSResponseData.FullBytes, sig) == nil {
			return true
		}
	}
	return false
}

// matchesCertID reports whether a CertID identifies cert as issued by issuer.
func matchesCertID(cert, issuer *x509.Certificate, id certIDASN) bool {
	h, ok := hashForOID(id.HashAlgorithm.Algorithm)
	if !ok {
		return false
	}
	if !bytes.Equal(hashOf(h, issuer.RawSubject), id.IssuerNameHash) {
		return false
	}
	if !bytes.Equal(hashOf(h, issuerPublicKeyBytes(issuer)), id.IssuerKeyHash) {
		return false
	}
	return id.SerialNumber != nil && id.SerialNumber.Cmp(cert.SerialNumber) == 0
}

func hashOf(h crypto.Hash, b []byte) []byte {
	hh := h.New()
	hh.Write(b)
	return hh.Sum(nil)
}

// issuerPublicKeyBytes returns the raw subjectPublicKey BIT STRING value of a
// certificate (what CertID hashes for issuerKeyHash).
func issuerPublicKeyBytes(c *x509.Certificate) []byte {
	var spki struct {
		Algorithm asn1.RawValue
		PublicKey asn1.BitString
	}
	if _, err := asn1.Unmarshal(c.RawSubjectPublicKeyInfo, &spki); err != nil {
		return nil
	}
	return spki.PublicKey.RightAlign()
}

// sigAlgoFromOID maps a signature-algorithm OID to an x509.SignatureAlgorithm.
func sigAlgoFromOID(oid asn1.ObjectIdentifier) (x509.SignatureAlgorithm, bool) {
	switch oid.String() {
	case "1.2.840.113549.1.1.5":
		return x509.SHA1WithRSA, true
	case "1.2.840.113549.1.1.11":
		return x509.SHA256WithRSA, true
	case "1.2.840.113549.1.1.12":
		return x509.SHA384WithRSA, true
	case "1.2.840.113549.1.1.13":
		return x509.SHA512WithRSA, true
	case "1.2.840.10045.4.3.2":
		return x509.ECDSAWithSHA256, true
	case "1.2.840.10045.4.3.3":
		return x509.ECDSAWithSHA384, true
	case "1.2.840.10045.4.3.4":
		return x509.ECDSAWithSHA512, true
	}
	return 0, false
}

// issuerOf returns the certificate in certs that issued cert (its subject equals
// cert's issuer), or nil.
func issuerOf(cert *x509.Certificate, certs []*x509.Certificate) *x509.Certificate {
	for _, c := range certs {
		if c != cert && bytes.Equal(c.RawSubject, cert.RawIssuer) {
			return c
		}
	}
	return nil
}

// DSSRevocationMaterial returns the CRLs and OCSP responses (DER) stored in the
// document's DSS (Document Security Store), decoded through their stream filters.
func (d *Document) DSSRevocationMaterial() (crls, ocsps [][]byte) {
	cat := getCatalog(d)
	if cat == nil {
		return nil, nil
	}
	dss := d.ResolveDict(cat.Get("DSS"))
	if dss == nil {
		return nil, nil
	}
	collect := func(key Name) [][]byte {
		var out [][]byte
		arr, _ := d.Resolve(dss.Get(key)).(Array)
		for _, ref := range arr {
			if st, ok := d.Resolve(ref).(*Stream); ok {
				out = append(out, decodeContentStream(d, st))
			}
		}
		return out
	}
	return collect("CRLs"), collect("OCSPs")
}

// DSSCerts returns the certificates stored in the document's DSS /Certs (the
// chain material a long-term signature carries).
func (d *Document) DSSCerts() []*x509.Certificate {
	cat := getCatalog(d)
	if cat == nil {
		return nil
	}
	dss := d.ResolveDict(cat.Get("DSS"))
	if dss == nil {
		return nil
	}
	var out []*x509.Certificate
	arr, _ := d.Resolve(dss.Get("Certs")).(Array)
	for _, ref := range arr {
		if st, ok := d.Resolve(ref).(*Stream); ok {
			if c, err := x509.ParseCertificate(decodeContentStream(d, st)); err == nil {
				out = append(out, c)
			}
		}
	}
	return out
}
