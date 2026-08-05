package sign

import (
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"github.com/mgilbir/pdf0/internal/signtest"
	"testing"
)

// TestResolveSignatureAlgorithmPSS is the C36 guard: an RSASSA-PSS signature
// algorithm is honoured rather than forced to PKCS#1 v1.5 (which made valid PSS
// signatures falsely fail); non-PSS OIDs fall back to the public-key mapping.
func TestResolveSignatureAlgorithmPSS(t *testing.T) {
	if a, ok := resolveSignatureAlgorithm(oidRSAPSS, "RSA", crypto.SHA256); !ok || a != x509.SHA256WithRSAPSS {
		t.Fatalf("PSS/SHA-256 = %v ok=%v, want SHA256WithRSAPSS", a, ok)
	}
	if a, ok := resolveSignatureAlgorithm(oidRSAPSS, "RSA", crypto.SHA512); !ok || a != x509.SHA512WithRSAPSS {
		t.Fatalf("PSS/SHA-512 = %v ok=%v, want SHA512WithRSAPSS", a, ok)
	}
	pkcs1 := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11} // sha256WithRSAEncryption
	if a, ok := resolveSignatureAlgorithm(pkcs1, "RSA", crypto.SHA256); !ok || a != x509.SHA256WithRSA {
		t.Fatalf("PKCS1/SHA-256 = %v ok=%v, want SHA256WithRSA", a, ok)
	}
}

// TestSignedContentTypeAndESSBinding pins the C14 checks: the content-type
// attribute is compared to the eContentType, and the ESS signing-certificate
// attribute must bind the signer certificate by hash.
func TestSignedContentTypeAndESSBinding(t *testing.T) {
	// content-type attribute set = { id-data }.
	ctVal, err := asn1.Marshal(oidData)
	if err != nil {
		t.Fatal(err)
	}
	ctAttr, err := marshalAttribute(oidContentType, ctVal)
	if err != nil {
		t.Fatal(err)
	}
	// signedContentTypeIs / checkESSCertBinding receive the CONTENTS of the signed
	// attributes SET (the concatenated Attribute SEQUENCEs), as RawValue.Bytes
	// yields in VerifyCMS — not a SET-wrapped blob.
	if !signedContentTypeIs(ctAttr, oidData) {
		t.Error("id-data content-type not recognized")
	}
	if signedContentTypeIs(ctAttr, oidSignedData) {
		t.Error("content-type must not match a different OID")
	}
	if signedContentTypeIs(nil, oidData) {
		t.Error("absent content-type attribute must not match")
	}

	cert, _ := signtest.CertKey(t)
	sum := sha256.Sum256(cert.Raw)

	good, err := asn1.Marshal(signingCertificateV2{Certs: []essCertIDv2{{CertHash: sum[:]}}})
	if err != nil {
		t.Fatal(err)
	}
	goodAttr, _ := marshalAttribute(oidSigningCertificateV2, good)
	if err := checkESSCertBinding(goodAttr, cert); err != nil {
		t.Errorf("correct signing-certificate-v2 binding rejected: %v", err)
	}

	bad, _ := asn1.Marshal(signingCertificateV2{Certs: []essCertIDv2{{CertHash: make([]byte, 32)}}})
	badAttr, _ := marshalAttribute(oidSigningCertificateV2, bad)
	if err := checkESSCertBinding(badAttr, cert); err == nil {
		t.Error("mismatched signing-certificate-v2 must be rejected")
	}

	if err := checkESSCertBinding(nil, cert); err != nil {
		t.Errorf("absent ESS attribute should be permitted (a PAdES-level policy): %v", err)
	}
}
