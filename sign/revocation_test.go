package sign

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"github.com/mgilbir/pdf0/internal/signtest"
	"math/big"
	"testing"
)

func TestRevocationCRL(t *testing.T) {
	ca, caKey, leaf := signtest.CAAndLeaf(t)

	revoked := signtest.MakeCRL(t, ca, caKey, []*x509.Certificate{leaf})
	if info, ok := revocationFromCRL(leaf, ca, revoked); !ok || info.Status != RevocationRevoked {
		t.Errorf("revoked CRL: got %+v ok=%v", info, ok)
	} else if !info.RevokedAt.Equal(signtest.RevTime) {
		t.Errorf("revocation time = %v, want %v", info.RevokedAt, signtest.RevTime)
	}

	clean := signtest.MakeCRL(t, ca, caKey, nil)
	if info, ok := revocationFromCRL(leaf, ca, clean); !ok || info.Status != RevocationGood {
		t.Errorf("clean CRL: got %+v ok=%v", info, ok)
	}

	// A CRL not signed by the claimed issuer must be rejected (unknown, false).
	otherCA, otherKey, _ := signtest.CAAndLeaf(t)
	_ = otherCA
	wrong := signtest.MakeCRL(t, ca, otherKey, []*x509.Certificate{leaf})
	if _, ok := revocationFromCRL(leaf, ca, wrong); ok {
		t.Error("CRL with a wrong issuer signature must not be trusted")
	}
}

func TestRevocationOCSP(t *testing.T) {
	ca, caKey, leaf := signtest.CAAndLeaf(t)

	for _, tc := range []struct {
		status string
		want   RevocationStatus
	}{
		{"good", RevocationGood},
		{"revoked", RevocationRevoked},
		{"unknown", RevocationUnknown},
	} {
		der := signtest.MakeOCSP(t, leaf, ca, caKey, tc.status)
		info, ok := revocationFromOCSP(leaf, ca, der)
		if !ok || info.Status != tc.want {
			t.Errorf("OCSP %s: got %+v ok=%v, want %v", tc.status, info, ok, tc.want)
		}
		if tc.status == "revoked" && !info.RevokedAt.Equal(signtest.RevTime) {
			t.Errorf("OCSP revoked time = %v, want %v", info.RevokedAt, signtest.RevTime)
		}
	}

	// A tampered signature must not verify.
	der := signtest.MakeOCSP(t, leaf, ca, caKey, "good")
	der[len(der)-1] ^= 0xFF
	if _, ok := revocationFromOCSP(leaf, ca, der); ok {
		t.Error("a tampered OCSP response must not be trusted")
	}

	// A response signed by the wrong key must not verify.
	_, otherKey, _ := signtest.CAAndLeaf(t)
	wrong := signtest.MakeOCSP(t, leaf, ca, otherKey, "good")
	if _, ok := revocationFromOCSP(leaf, ca, wrong); ok {
		t.Error("an OCSP response signed by the wrong key must not be trusted")
	}

	// A response for a different certificate (different serial, same CA) does not
	// match this one.
	otherKey2, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherTmpl := &x509.Certificate{SerialNumber: big.NewInt(999), Subject: pkix.Name{CommonName: "other leaf"}, NotBefore: signtest.NotBefore, NotAfter: signtest.NotAfter}
	otherDER, _ := x509.CreateCertificate(rand.Reader, otherTmpl, ca, &otherKey2.PublicKey, caKey)
	otherLeaf, _ := x509.ParseCertificate(otherDER)
	mismatch := signtest.MakeOCSP(t, otherLeaf, ca, caKey, "revoked")
	if _, ok := revocationFromOCSP(leaf, ca, mismatch); ok {
		t.Error("an OCSP response for another certificate must not apply")
	}
}
