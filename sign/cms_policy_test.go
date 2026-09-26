package sign

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/asn1"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/signtest"
)

// cmsVariant chooses how buildTestCMS departs from what pdf0's own signer
// writes.
type cmsVariant struct {
	// pssSalt, when positive, signs with RSASSA-PSS over SHA-256 with this salt
	// length, and declares it in the algorithm's parameters.
	pssSalt int
	// essHash, when set, names this hash explicitly in the ESSCertIDv2 and
	// hashes the certificate with it; zero leaves it at the SHA-256 default,
	// omitted.
	essHash crypto.Hash
}

var hashOIDs = map[crypto.Hash]asn1.ObjectIdentifier{
	crypto.SHA256: {2, 16, 840, 1, 101, 3, 4, 2, 1},
	crypto.SHA384: {2, 16, 840, 1, 101, 3, 4, 2, 2},
	crypto.SHA512: {2, 16, 840, 1, 101, 3, 4, 2, 3},
}

// buildTestCMS builds a detached CMS SignedData over content the way other
// producers do, in the variant v: it is the producing side of the tests
// below, written out rather than borrowed from BuildSignedDataFull so that the
// shapes pdf0 does not itself produce can be built.
func buildTestCMS(t *testing.T, content []byte, v cmsVariant) []byte {
	t.Helper()
	cert, key := signtest.CertKey(t)
	md := sha256.Sum256(content)
	ctVal, _ := asn1.Marshal(oidData)
	mdVal, _ := asn1.Marshal(md[:])
	ctAttr, _ := marshalAttribute(oidContentType, ctVal)
	mdAttr, _ := marshalAttribute(oidMessageDigest, mdVal)

	var scVal []byte
	if v.essHash == 0 {
		sum := sha256.Sum256(cert.Raw)
		scVal, _ = asn1.Marshal(signingCertificateV2{Certs: []essCertIDv2{{CertHash: sum[:]}}})
	} else {
		type id struct {
			HashAlgorithm pkixAlgorithmIdentifier
			CertHash      []byte
		}
		h := v.essHash.New()
		h.Write(cert.Raw)
		scVal, _ = asn1.Marshal(struct{ Certs []id }{[]id{{pkixAlgorithmIdentifier{Algorithm: hashOIDs[v.essHash]}, h.Sum(nil)}}})
	}
	scAttr, _ := marshalAttribute(oidSigningCertificateV2, scVal)
	attrs := derSet([][]byte{ctAttr, mdAttr, scAttr})
	ah := sha256.Sum256(attrs)

	var sig []byte
	var err error
	sigAlg := pkixAlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}}
	if v.pssSalt > 0 {
		sig, err = rsa.SignPSS(rand.Reader, key, crypto.SHA256, ah[:], &rsa.PSSOptions{SaltLength: v.pssSalt})
		sha256ID := pkixAlgorithmIdentifier{Algorithm: hashOIDs[crypto.SHA256]}
		mgfParams, _ := asn1.Marshal(sha256ID)
		params, _ := asn1.Marshal(rsassaPSSParams{
			HashAlgorithm:    sha256ID,
			MaskGenAlgorithm: pkixAlgorithmIdentifier{Algorithm: oidMGF1, Parameters: asn1.RawValue{FullBytes: mgfParams}},
			SaltLength:       v.pssSalt,
			TrailerField:     1,
		})
		sigAlg = pkixAlgorithmIdentifier{Algorithm: oidRSAPSS, Parameters: asn1.RawValue{FullBytes: params}}
	} else {
		sig, err = key.Sign(rand.Reader, ah[:], crypto.SHA256)
	}
	if err != nil {
		t.Fatal(err)
	}
	implicit := append([]byte(nil), attrs...)
	implicit[0] = 0xA0
	siDER, err := asn1.Marshal(signerInfoMarshal{
		Version:         1,
		SID:             issuerAndSerial{Issuer: asn1.RawValue{FullBytes: cert.RawIssuer}, Serial: cert.SerialNumber},
		DigestAlgorithm: pkixAlgorithmIdentifier{Algorithm: hashOIDs[crypto.SHA256]},
		SignedAttrs:     asn1.RawValue{FullBytes: implicit},
		SignatureAlgo:   sigAlg,
		Signature:       sig,
	})
	if err != nil {
		t.Fatal(err)
	}
	sdDER, err := asn1.Marshal(signedDataMarshal{
		Version:          1,
		DigestAlgorithms: asn1.RawValue{FullBytes: derSet([][]byte{mustMarshal(pkixAlgorithmIdentifier{Algorithm: hashOIDs[crypto.SHA256]})})},
		EncapContentInfo: encapContentInfo{ContentType: oidData},
		Certificates:     asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: cert.Raw},
		SignerInfos:      asn1.RawValue{FullBytes: derSet([][]byte{siDER})},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := asn1.Marshal(contentInfoMarshal{ContentType: oidSignedData, Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: sdDER}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRSAPSSParametersAreHonoured pins audit 2026-09-22 C154: an RSASSA-PSS
// signature is verified with the salt length its parameters declare. Mapping
// PSS onto x509's fixed algorithms assumed a salt as long as the hash (32 for
// SHA-256), so a conforming signature with a 20-byte salt — RFC 4055's default
// — failed to verify.
func TestRSAPSSParametersAreHonoured(t *testing.T) {
	content := []byte("bytes signed with RSASSA-PSS")
	for _, salt := range []int{20, 32, 64} {
		cms := buildTestCMS(t, content, cmsVariant{pssSalt: salt})
		if _, _, _, err := VerifyCMS(cms, content); err != nil {
			t.Errorf("PSS with a %d-byte salt: %v", salt, err)
		}
		if _, _, _, err := VerifyCMS(cms, append(append([]byte(nil), content...), '!')); err == nil {
			t.Errorf("PSS with a %d-byte salt verified over different content", salt)
		}
	}
}

// TestESSCertIDv2WithHashAlgorithm pins audit 2026-09-22 C57: an ESSCertIDv2
// that names its hash algorithm — as CAdES producers and RFC 5816 time-stamp
// authorities using SHA-384 or SHA-512 do — binds the certificate under that
// algorithm. Reading only the hash failed to parse it at all.
func TestESSCertIDv2WithHashAlgorithm(t *testing.T) {
	content := []byte("bytes signed with an explicit ESS hash algorithm")
	for _, h := range []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512} {
		cms := buildTestCMS(t, content, cmsVariant{essHash: h})
		if _, _, _, err := VerifyCMS(cms, content); err != nil {
			t.Errorf("ESSCertIDv2 with %v: %v", h, err)
		}
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
	if err := checkESSCertBinding(badAttr, cert); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("mismatched signing-certificate-v2 must be rejected, got %v", err)
	}

	if err := checkESSCertBinding(nil, cert); err != nil {
		t.Errorf("absent ESS attribute should be permitted (a PAdES-level policy): %v", err)
	}
}
