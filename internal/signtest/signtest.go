// Package signtest builds the certificates, CRLs and OCSP responses that
// signing tests verify against. It lives outside the sign package because the
// tests that need these fixtures are split across two packages — the revocation
// rules in sign, the produce-then-verify flows in the root package, which need
// the writer — and a fixture defined in one package's _test.go is invisible to
// the other.
//
// The ASN.1 shapes below are declared here rather than borrowed from sign's
// parser. They are the *producing* side, and keeping them separate means a
// fixture cannot agree with a broken parser by construction: if either drifts,
// the round trip fails loudly.
package signtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"testing"
	"time"
)

// The validity window the fixtures issue certificates over.
var (
	tstOIDSHA1      = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	tstOIDSHA256RSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	// Revocation material is dated relative to the current time so it is within
	// its validity window at verification time: CheckCertRevocation now rejects
	// stale (expired/superseded) material (audit C13), and a live check validates
	// freshness against the present.
	// Truncated to whole seconds in UTC so values survive the ASN.1
	// GeneralizedTime round-trip (which drops sub-second precision and the
	// monotonic clock reading) for exact RevokedAt comparisons.
	Base      = time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	RevTime   = time.Now().Add(-12 * time.Hour).UTC().Truncate(time.Second)
	NotBefore = Base.Add(-24 * time.Hour)
	NotAfter  = Base.Add(365 * 24 * time.Hour)
)

// id-pkix-ocsp-basic (RFC 6960).
var oidOCSPBasic = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}

// The OCSP wire shapes, producing side (RFC 6960).
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
type pkixAlgorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
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

// CAAndLeaf builds a CA certificate and a leaf certificate issued by it.
func CAAndLeaf(t *testing.T) (ca *x509.Certificate, caKey *rsa.PrivateKey, leaf *x509.Certificate) {
	t.Helper()
	caKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pdf0 test CA"},
		NotBefore:             NotBefore,
		NotAfter:              NotAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(caDER)

	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "pdf0 test leaf"},
		NotBefore:    NotBefore,
		NotAfter:     NotAfter,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ = x509.ParseCertificate(leafDER)
	return ca, caKey, leaf
}

func MakeCRL(t *testing.T, ca *x509.Certificate, caKey crypto.Signer, revoked []*x509.Certificate) []byte {
	t.Helper()
	var entries []x509.RevocationListEntry
	for _, c := range revoked {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: c.SerialNumber, RevocationTime: RevTime})
	}
	tmpl := &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                Base,
		NextUpdate:                Base.Add(30 * 24 * time.Hour),
		RevokedCertificateEntries: entries,
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// MakeOCSP hand-builds a signed OCSP response (RFC 6960) for cert, with the given
// status ("good", "revoked", "unknown"), signed by issuerKey.
func MakeOCSP(t *testing.T, cert, issuer *x509.Certificate, issuerKey crypto.Signer, status string) []byte {
	t.Helper()
	nameHash := sha1.Sum(issuer.RawSubject)
	keyHash := sha1.Sum(issuerPublicKeyBytes(issuer))
	certID := certIDASN{
		HashAlgorithm:  pkixAlgorithmIdentifier{Algorithm: tstOIDSHA1},
		IssuerNameHash: nameHash[:],
		IssuerKeyHash:  keyHash[:],
		SerialNumber:   cert.SerialNumber,
	}

	var cs asn1.RawValue
	switch status {
	case "good":
		cs = asn1.RawValue{Class: 2, Tag: 0} // [0] IMPLICIT NULL
	case "revoked":
		ri, _ := asn1.Marshal(struct {
			T time.Time `asn1:"generalized"`
		}{RevTime})
		ri[0] = 0xA1 // retag SEQUENCE -> [1] IMPLICIT
		cs = asn1.RawValue{FullBytes: ri}
	case "unknown":
		cs = asn1.RawValue{Class: 2, Tag: 2} // [2] IMPLICIT NULL
	}

	sr := singleResponseASN{CertID: certID, CertStatus: cs, ThisUpdate: Base}
	rd := responseDataASN{
		ResponderID: asn1.RawValue{Class: 2, Tag: 1, IsCompound: true, Bytes: issuer.RawSubject},
		ProducedAt:  Base,
		Responses:   []singleResponseASN{sr},
	}
	rdDER, err := asn1.Marshal(rd)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(rdDER)
	sig, err := issuerKey.Sign(rand.Reader, h[:], crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	basic := basicOCSPResponseASN{
		TBSResponseData: asn1.RawValue{FullBytes: rdDER},
		SignatureAlgo:   pkixAlgorithmIdentifier{Algorithm: tstOIDSHA256RSA},
		Signature:       asn1.BitString{Bytes: sig, BitLength: len(sig) * 8},
	}
	basicDER, _ := asn1.Marshal(basic)
	resp := ocspResponseASN{Status: 0, ResponseBytes: ocspResponseBytesASN{ResponseType: oidOCSPBasic, Response: basicDER}}
	der, _ := asn1.Marshal(resp)
	return der
}

func CertKey(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "pdf0 test signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// TSACertKey creates a certificate carrying the id-kp-timeStamping extended
// key usage, suitable as an RFC 3161 time-stamp authority.
func TSACertKey(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(43),
		Subject:      pkix.Name{CommonName: "pdf0 test TSA"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}
