package pdf0

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

func testCertKey(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
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

// TestCMSRoundTrip signs content and verifies the detached CMS against it,
// including that a single modified byte is detected.
func TestCMSRoundTrip(t *testing.T) {
	cert, key := testCertKey(t)
	content := []byte("the exact bytes that were signed, over the /ByteRange")

	cms, err := buildSignedData(cert, key, content)
	if err != nil {
		t.Fatalf("buildSignedData: %v", err)
	}
	// The blob must parse as CMS with a certificate and one SignerInfo.
	if info := parseCMSSignedData(cms); !info.parsed || !info.hasCertificate || info.signerInfoCount != 1 {
		t.Fatalf("built CMS is malformed: %+v", info)
	}
	cn, err := verifyCMS(cms, content)
	if err != nil {
		t.Fatalf("verifyCMS: %v", err)
	}
	if cn != "pdf0 test signer" {
		t.Errorf("common name = %q", cn)
	}
	tampered := append(append([]byte(nil), content...), '!')
	if _, err := verifyCMS(cms, tampered); err == nil {
		t.Error("verification succeeded on modified content")
	}
}

// TestVerifySignatures drives the full document path: build the /ByteRange,
// sign the covered bytes, and confirm the signature verifies (and fails after a
// change to the signed region).
func TestVerifySignatures(t *testing.T) {
	cert, key := testCertKey(t)
	prefix := []byte("%PDF-2.0 ... content before the signature value ...")
	suffix := []byte("... content after the signature value ... %%EOF")
	const gap = 200 // placeholder /Contents region, excluded from the digest

	raw := make([]byte, 0, len(prefix)+gap+len(suffix))
	raw = append(raw, prefix...)
	raw = append(raw, make([]byte, gap)...)
	raw = append(raw, suffix...)

	signed := append(append([]byte(nil), prefix...), suffix...)
	cms, err := buildSignedData(cert, key, signed)
	if err != nil {
		t.Fatal(err)
	}

	sig := &Dictionary{}
	sig.Set("Type", Name("Sig"))
	sig.Set("SubFilter", Name("adbe.pkcs7.detached"))
	sig.Set("Contents", String{Value: cms, IsHex: true})
	sig.Set("ByteRange", Array{
		Integer(0), Integer(len(prefix)),
		Integer(len(prefix) + gap), Integer(len(suffix)),
	})
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: sig},
	}}

	results := doc.VerifySignatures(raw)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if !r.Valid || r.Err != nil {
		t.Fatalf("signature did not verify: valid=%v err=%v", r.Valid, r.Err)
	}
	if !r.CoversWholeDocument {
		t.Error("ByteRange should cover the whole document")
	}
	if r.SignerCommonName != "pdf0 test signer" {
		t.Errorf("signer = %q", r.SignerCommonName)
	}

	// Modifying a signed byte invalidates the signature.
	tampered := append([]byte(nil), raw...)
	tampered[0] ^= 0xFF
	if res := doc.VerifySignatures(tampered); res[0].Valid {
		t.Error("modified document still verified")
	}
}
