package sign

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/asn1"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
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
	Status RevocationStatus
	// Source is "OCSP" or "CRL": the kind of source that decided Status, or
	// "" when nothing did.
	Source string
	// RevokedAt is when the certificate was revoked, as the deciding source
	// states it; set only when Status is RevocationRevoked. A certificate
	// revoked after the validation time was still unrevoked at it: compare the
	// two when that matters (a time-stamped signature made before the
	// revocation).
	RevokedAt time.Time
}

// CheckCertRevocation reports what the supplied CRLs and OCSP responses (DER)
// say about cert at time at. issuer must be the certificate that issued cert:
// the next certificate in a chain you have verified. A source is consulted only
// if it is authenticated by issuer — a CRL signed by it, or an OCSP response
// signed by it or by a responder certificate it issued with the OCSP-signing
// extended key usage — and cert must itself verify under issuer's key, so a
// look-alike issuer carrying the real one's name decides nothing.
//
// Every authenticated source is read, and a revocation from any of them wins:
// a fresh "good" from one source never hides a "revoked" from another (audit
// 2026-09-22 C154). A revocation needs no freshness: it is permanent, and a
// source issued at any time that records it is evidence of it.
//
// A "good" counts only from a source current at at: thisUpdate <= at <=
// nextUpdate, with five minutes of clock skew either side. RFC 5280 5.1.2.5
// requires a CRL to carry nextUpdate, so a CRL without one is not current at
// any time. An OCSP response without nextUpdate says newer information is
// always available (RFC 6960 4.2.2.1); it is taken as current only within the
// skew of its own thisUpdate — at the moment it was produced, not for ever.
func CheckCertRevocation(cert, issuer *x509.Certificate, crls, ocsps [][]byte, at time.Time) RevocationInfo {
	if cert == nil || issuer == nil || cert.CheckSignatureFrom(issuer) != nil {
		return RevocationInfo{}
	}
	var good, unknown RevocationInfo
	consider := func(info RevocationInfo) (revoked bool) {
		switch info.Status {
		case RevocationRevoked:
			return true
		case RevocationGood:
			if good.Status == RevocationUnknown {
				good = info
			}
		default:
			if info.Source != "" && unknown.Source == "" {
				unknown = info
			}
		}
		return false
	}
	for _, der := range crls {
		if info, ok := revocationFromCRL(cert, issuer, der, at); ok && consider(info) {
			return info
		}
	}
	for _, der := range ocsps {
		if info, ok := revocationFromOCSP(cert, issuer, der, at); ok && consider(info) {
			return info
		}
	}
	if good.Status == RevocationGood {
		return good
	}
	return unknown
}

// revocationClockSkew tolerates modest clock differences when checking the
// validity window of revocation material.
const revocationClockSkew = 5 * time.Minute

// currentAt reports whether revocation material issued at thisUpdate, and
// superseded at nextUpdate (zero when the material names no such time), is
// current at time at. See CheckCertRevocation for what a missing nextUpdate
// means; ocsp selects the OCSP reading of it.
func currentAt(thisUpdate, nextUpdate, at time.Time, ocsp bool) bool {
	if thisUpdate.IsZero() || thisUpdate.After(at.Add(revocationClockSkew)) {
		return false // not yet issued at the validation time
	}
	if nextUpdate.IsZero() {
		return ocsp && !at.After(thisUpdate.Add(revocationClockSkew))
	}
	return !at.After(nextUpdate.Add(revocationClockSkew))
}

// revocationFromCRL checks cert against a CRL (DER) that must be signed by
// issuer. An unparseable or mis-signed CRL yields (unknown, false). A CRL that
// lists cert yields revoked whatever its dates; one that does not yields good
// only if it is current at at (a superseded CRL replayed in the DSS must not
// mask a revocation published after it, audit 2026-07-26 C13).
func revocationFromCRL(cert, issuer *x509.Certificate, der []byte, at time.Time) (RevocationInfo, bool) {
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
	if !currentAt(crl.ThisUpdate, crl.NextUpdate, at, false) {
		return RevocationInfo{}, false
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

// revocationFromOCSP checks cert against an OCSP response (DER). The response
// must be signed by issuer, or by a delegated responder certificate issued by
// issuer, carrying the OCSP-signing extended key usage and valid at at, and
// carry a status for cert. Otherwise it yields (unknown, false). A "revoked"
// status is returned whatever the response's dates; "good" and "unknown" only
// when the response is current at at.
func revocationFromOCSP(cert, issuer *x509.Certificate, der []byte, at time.Time) (RevocationInfo, bool) {
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
	if !verifyOCSPSignature(&basic, issuer, at) {
		return RevocationInfo{}, false
	}
	var rd responseDataASN
	if _, err := asn1.Unmarshal(basic.TBSResponseData.FullBytes, &rd); err != nil {
		return RevocationInfo{}, false
	}
	var found RevocationInfo
	ok := false
	for _, sr := range rd.Responses {
		if !matchesCertID(cert, issuer, sr.CertID) {
			continue
		}
		if sr.CertStatus.Class == 2 && sr.CertStatus.Tag == 1 { // revoked
			info := RevocationInfo{Status: RevocationRevoked, Source: "OCSP"}
			var ri revokedInfoASN
			if _, err := asn1.UnmarshalWithParams(sr.CertStatus.FullBytes, &ri, "tag:1"); err == nil {
				info.RevokedAt = ri.RevocationTime
			}
			return info, true
		}
		// A response outside its validity window is not authoritative — a stale
		// "good" captured before a later revocation must not be honoured.
		if ok || !currentAt(sr.ThisUpdate, sr.NextUpdate, at, true) {
			continue
		}
		switch {
		case sr.CertStatus.Class == 2 && sr.CertStatus.Tag == 0: // good
			found, ok = RevocationInfo{Status: RevocationGood, Source: "OCSP"}, true
		default: // unknown
			found, ok = RevocationInfo{Status: RevocationUnknown, Source: "OCSP"}, true
		}
	}
	return found, ok
}

// verifyOCSPSignature verifies the BasicOCSPResponse signature over its
// ResponseData, by the issuer directly or by a delegated responder: a
// certificate the issuer signed, carrying id-kp-OCSPSigning (RFC 6960 4.2.2.2)
// and valid at at.
func verifyOCSPSignature(basic *basicOCSPResponseASN, issuer *x509.Certificate, at time.Time) bool {
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
		if at.Before(c.NotBefore) || at.After(c.NotAfter) {
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

// DSSRevocationMaterial returns the CRLs and OCSP responses (DER) stored in the
// document's DSS (Document Security Store), decoded through their stream filters.
func DSSRevocationMaterial(d core.View) (crls, ocsps [][]byte) {
	cat := d.Catalog()
	if cat == nil {
		return nil, nil
	}
	dss := d.ResolveDict(cat.Get("DSS"))
	if dss == nil {
		return nil, nil
	}
	collect := func(key object.Name) [][]byte {
		var out [][]byte
		arr, _ := d.Resolve(dss.Get(key)).(object.Array)
		for _, ref := range arr {
			if st, ok := d.Resolve(ref).(*object.Stream); ok {
				data, _ := d.Content(st) // reason: undecoded revocation data is no evidence, and is never read as evidence of anything
				out = append(out, data)
			}
		}
		return out
	}
	return collect("CRLs"), collect("OCSPs")
}

// DSSCerts returns the certificates stored in the document's DSS /Certs (the
// chain material a long-term signature carries).
func DSSCerts(d core.View) []*x509.Certificate {
	cat := d.Catalog()
	if cat == nil {
		return nil
	}
	dss := d.ResolveDict(cat.Get("DSS"))
	if dss == nil {
		return nil
	}
	var out []*x509.Certificate
	arr, _ := d.Resolve(dss.Get("Certs")).(object.Array)
	for _, ref := range arr {
		if st, ok := d.Resolve(ref).(*object.Stream); ok {
			data, _ := d.Content(st) // reason: an undecoded certificate is not a certificate; nothing is concluded from its absence
			if c, err := x509.ParseCertificate(data); err == nil {
				out = append(out, c)
			}
		}
	}
	return out
}
