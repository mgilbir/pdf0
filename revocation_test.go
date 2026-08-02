package pdf0

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"github.com/mgilbir/pdf0/internal/signtest"
	"math/big"
	"testing"
)

func TestCheckCertRevocation(t *testing.T) {
	ca, caKey, leaf := signtest.CAAndLeaf(t)
	good := signtest.MakeOCSP(t, leaf, ca, caKey, "good")
	revoked := signtest.MakeCRL(t, ca, caKey, []*x509.Certificate{leaf})

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
	ca, caKey, leaf := signtest.CAAndLeaf(t)
	crl := signtest.MakeCRL(t, ca, caKey, []*x509.Certificate{leaf})
	ocsp := signtest.MakeOCSP(t, leaf, ca, caKey, "good")

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
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pdf0 test CA"}, NotBefore: signtest.NotBefore, NotAfter: signtest.NotAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ = x509.ParseCertificate(caDER)
	leafKey, _ = rsa.GenerateKey(rand.Reader, 2048)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "pdf0 signer"}, NotBefore: signtest.NotBefore, NotAfter: signtest.NotAfter}
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
		signed.Objects[matNum] = &IndirectObject{Number: matNum, Value: &Stream{Dict: Dictionary{}, Data: signtest.MakeCRL(t, ca, caKey, revoked)}}
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
