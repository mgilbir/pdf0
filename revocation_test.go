package pdf0

import (
	"bytes"
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

var (
	tstOIDSHA1      = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	tstOIDSHA256RSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	tstRevTime      = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	tstBase         = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tstNotBefore    = tstBase.Add(-24 * time.Hour)
	tstNotAfter     = tstBase.Add(365 * 24 * time.Hour)
)

// caAndLeaf builds a CA certificate and a leaf certificate issued by it.
func caAndLeaf(t *testing.T) (ca *x509.Certificate, caKey *rsa.PrivateKey, leaf *x509.Certificate) {
	t.Helper()
	caKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pdf0 test CA"},
		NotBefore:             tstNotBefore,
		NotAfter:              tstNotAfter,
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
		NotBefore:    tstNotBefore,
		NotAfter:     tstNotAfter,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ = x509.ParseCertificate(leafDER)
	return ca, caKey, leaf
}

func makeCRL(t *testing.T, ca *x509.Certificate, caKey crypto.Signer, revoked []*x509.Certificate) []byte {
	t.Helper()
	var entries []x509.RevocationListEntry
	for _, c := range revoked {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: c.SerialNumber, RevocationTime: tstRevTime})
	}
	tmpl := &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                tstBase,
		NextUpdate:                tstBase.Add(30 * 24 * time.Hour),
		RevokedCertificateEntries: entries,
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// makeOCSP hand-builds a signed OCSP response (RFC 6960) for cert, with the given
// status ("good", "revoked", "unknown"), signed by issuerKey.
func makeOCSP(t *testing.T, cert, issuer *x509.Certificate, issuerKey crypto.Signer, status string) []byte {
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
		}{tstRevTime})
		ri[0] = 0xA1 // retag SEQUENCE -> [1] IMPLICIT
		cs = asn1.RawValue{FullBytes: ri}
	case "unknown":
		cs = asn1.RawValue{Class: 2, Tag: 2} // [2] IMPLICIT NULL
	}

	sr := singleResponseASN{CertID: certID, CertStatus: cs, ThisUpdate: tstBase}
	rd := responseDataASN{
		ResponderID: asn1.RawValue{Class: 2, Tag: 1, IsCompound: true, Bytes: issuer.RawSubject},
		ProducedAt:  tstBase,
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

func TestRevocationCRL(t *testing.T) {
	ca, caKey, leaf := caAndLeaf(t)

	revoked := makeCRL(t, ca, caKey, []*x509.Certificate{leaf})
	if info, ok := revocationFromCRL(leaf, ca, revoked); !ok || info.Status != RevocationRevoked {
		t.Errorf("revoked CRL: got %+v ok=%v", info, ok)
	} else if !info.RevokedAt.Equal(tstRevTime) {
		t.Errorf("revocation time = %v, want %v", info.RevokedAt, tstRevTime)
	}

	clean := makeCRL(t, ca, caKey, nil)
	if info, ok := revocationFromCRL(leaf, ca, clean); !ok || info.Status != RevocationGood {
		t.Errorf("clean CRL: got %+v ok=%v", info, ok)
	}

	// A CRL not signed by the claimed issuer must be rejected (unknown, false).
	otherCA, otherKey, _ := caAndLeaf(t)
	_ = otherCA
	wrong := makeCRL(t, ca, otherKey, []*x509.Certificate{leaf})
	if _, ok := revocationFromCRL(leaf, ca, wrong); ok {
		t.Error("CRL with a wrong issuer signature must not be trusted")
	}
}

func TestRevocationOCSP(t *testing.T) {
	ca, caKey, leaf := caAndLeaf(t)

	for _, tc := range []struct {
		status string
		want   RevocationStatus
	}{
		{"good", RevocationGood},
		{"revoked", RevocationRevoked},
		{"unknown", RevocationUnknown},
	} {
		der := makeOCSP(t, leaf, ca, caKey, tc.status)
		info, ok := revocationFromOCSP(leaf, ca, der)
		if !ok || info.Status != tc.want {
			t.Errorf("OCSP %s: got %+v ok=%v, want %v", tc.status, info, ok, tc.want)
		}
		if tc.status == "revoked" && !info.RevokedAt.Equal(tstRevTime) {
			t.Errorf("OCSP revoked time = %v, want %v", info.RevokedAt, tstRevTime)
		}
	}

	// A tampered signature must not verify.
	der := makeOCSP(t, leaf, ca, caKey, "good")
	der[len(der)-1] ^= 0xFF
	if _, ok := revocationFromOCSP(leaf, ca, der); ok {
		t.Error("a tampered OCSP response must not be trusted")
	}

	// A response signed by the wrong key must not verify.
	_, otherKey, _ := caAndLeaf(t)
	wrong := makeOCSP(t, leaf, ca, otherKey, "good")
	if _, ok := revocationFromOCSP(leaf, ca, wrong); ok {
		t.Error("an OCSP response signed by the wrong key must not be trusted")
	}

	// A response for a different certificate (different serial, same CA) does not
	// match this one.
	otherKey2, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherTmpl := &x509.Certificate{SerialNumber: big.NewInt(999), Subject: pkix.Name{CommonName: "other leaf"}, NotBefore: tstNotBefore, NotAfter: tstNotAfter}
	otherDER, _ := x509.CreateCertificate(rand.Reader, otherTmpl, ca, &otherKey2.PublicKey, caKey)
	otherLeaf, _ := x509.ParseCertificate(otherDER)
	mismatch := makeOCSP(t, otherLeaf, ca, caKey, "revoked")
	if _, ok := revocationFromOCSP(leaf, ca, mismatch); ok {
		t.Error("an OCSP response for another certificate must not apply")
	}
}

func TestCheckCertRevocation(t *testing.T) {
	ca, caKey, leaf := caAndLeaf(t)
	good := makeOCSP(t, leaf, ca, caKey, "good")
	revoked := makeCRL(t, ca, caKey, []*x509.Certificate{leaf})

	// OCSP is consulted first; a good OCSP wins even if a CRL would say revoked.
	if info := CheckCertRevocation(leaf, ca, [][]byte{revoked}, [][]byte{good}); info.Status != RevocationGood || info.Source != "OCSP" {
		t.Errorf("expected OCSP good to win: %+v", info)
	}
	// CRL is used when no OCSP is available.
	if info := CheckCertRevocation(leaf, ca, [][]byte{revoked}, nil); info.Status != RevocationRevoked || info.Source != "CRL" {
		t.Errorf("expected CRL revoked: %+v", info)
	}
	// No material -> unknown.
	if info := CheckCertRevocation(leaf, ca, nil, nil); info.Status != RevocationUnknown {
		t.Errorf("expected unknown with no material: %+v", info)
	}
}

func TestDSSRevocationMaterial(t *testing.T) {
	ca, caKey, leaf := caAndLeaf(t)
	crl := makeCRL(t, ca, caKey, []*x509.Certificate{leaf})
	ocsp := makeOCSP(t, leaf, ca, caKey, "good")

	d := &Document{Objects: map[int]*IndirectObject{}, Version: "2.0"}
	set := func(n int, v Object) { d.Objects[n] = &IndirectObject{Number: n, Value: v} }
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("DSS", IndirectRef{Number: 2})
	set(1, cat)
	dss := &Dictionary{}
	dss.Set("CRLs", Array{IndirectRef{Number: 3}})
	dss.Set("OCSPs", Array{IndirectRef{Number: 4}})
	set(2, dss)
	set(3, &Stream{Dict: Dictionary{}, Data: crl})
	set(4, &Stream{Dict: Dictionary{}, Data: ocsp})
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})

	crls, ocsps := d.DSSRevocationMaterial()
	if len(crls) != 1 || len(ocsps) != 1 {
		t.Fatalf("DSS material: got %d CRLs, %d OCSPs", len(crls), len(ocsps))
	}
	if info := CheckCertRevocation(leaf, ca, crls, ocsps); info.Status != RevocationGood {
		t.Errorf("DSS-driven revocation check: %+v", info)
	}
}

// caLeafWithKey builds a CA and a leaf (returning the leaf key) so the leaf can
// sign a document while the CA acts as revocation issuer.
func caLeafWithKey(t *testing.T) (ca *x509.Certificate, caKey *rsa.PrivateKey, leaf *x509.Certificate, leafKey *rsa.PrivateKey) {
	t.Helper()
	caKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pdf0 test CA"}, NotBefore: tstNotBefore, NotAfter: tstNotAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ = x509.ParseCertificate(caDER)
	leafKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "pdf0 signer"}, NotBefore: tstNotBefore, NotAfter: tstNotAfter}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	leaf, _ = x509.ParseCertificate(leafDER)
	return
}

// TestSignatureRevocationFromDSS signs with a CA-issued leaf, injects a DSS whose
// CRL revokes the leaf, and checks VerifySignatures reports the revocation while
// the signature itself still verifies.
func TestSignatureRevocationFromDSS(t *testing.T) {
	ca, caKey, leaf, leafKey := caLeafWithKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, leaf, leafKey); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}

	inject := func(status string) []SignatureResult {
		maxN := 0
		for n := range signed.Objects {
			if n > maxN {
				maxN = n
			}
		}
		caNum, matNum, dssNum := maxN+1, maxN+2, maxN+3
		signed.Objects[caNum] = &IndirectObject{Number: caNum, Value: &Stream{Dict: Dictionary{}, Data: ca.Raw}}
		var revoked []*x509.Certificate
		if status == "revoked" {
			revoked = []*x509.Certificate{leaf}
		}
		signed.Objects[matNum] = &IndirectObject{Number: matNum, Value: &Stream{Dict: Dictionary{}, Data: makeCRL(t, ca, caKey, revoked)}}
		dss := &Dictionary{}
		dss.Set("Certs", Array{IndirectRef{Number: caNum}})
		dss.Set("CRLs", Array{IndirectRef{Number: matNum}})
		signed.Objects[dssNum] = &IndirectObject{Number: dssNum, Value: dss}
		signed.ResolveDict(signed.Trailer.Get("Root")).Set("DSS", IndirectRef{Number: dssNum})
		return signed.VerifySignatures(out)
	}

	res := inject("revoked")
	if len(res) != 1 || !res[0].Valid {
		t.Fatalf("signature should still verify: %+v", res)
	}
	if res[0].Revocation.Status != RevocationRevoked || res[0].Revocation.Source != "CRL" {
		t.Errorf("expected the signer to be reported revoked, got %+v", res[0].Revocation)
	}

	res = inject("good")
	if res[0].Revocation.Status != RevocationGood {
		t.Errorf("expected the signer to be reported good, got %+v", res[0].Revocation)
	}
}
