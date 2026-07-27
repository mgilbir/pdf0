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

func selfSignedCert(t *testing.T, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Signer"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// TestChainTrustedUsesWallClock is the C4 guard: chain validation happens at the
// current time and takes no signer-asserted signing-time, so an expired
// certificate cannot be rescued by backdating the (self-asserted) signing-time
// into its old validity window.
func TestChainTrustedUsesWallClock(t *testing.T) {
	expired := selfSignedCert(t, time.Now().Add(-3*time.Hour), time.Now().Add(-1*time.Hour))
	roots := x509.NewCertPool()
	roots.AddCert(expired)
	if err := chainTrusted(expired, nil, roots); err == nil {
		t.Fatal("expired certificate was trusted; chain must validate at wall-clock, not the signing-time attribute")
	}

	valid := selfSignedCert(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	vroots := x509.NewCertPool()
	vroots.AddCert(valid)
	if err := chainTrusted(valid, nil, vroots); err != nil {
		t.Fatalf("currently-valid certificate should be trusted: %v", err)
	}
}

// TestContentsGapIsSignature pins the C12 coverage tightening: "covers the whole
// document" holds only for the canonical two-segment layout whose single gap is
// exactly the /Contents hex window.
func TestContentsGapIsSignature(t *testing.T) {
	contents := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	prefix := []byte("%PDF-1.7\n/Contents ")
	hexStr := []byte("<DEADBEEF>")
	suffix := []byte("\n%%EOF\n")
	raw := append(append(append([]byte{}, prefix...), hexStr...), suffix...)

	gapStart := int64(len(prefix))
	gapEnd := gapStart + int64(len(hexStr))
	seg0 := [2]int64{0, gapStart}
	seg1 := [2]int64{gapEnd, int64(len(raw)) - gapEnd}

	if !contentsGapIsSignature(raw, [][2]int64{seg0, seg1}, contents) {
		t.Fatal("canonical two-segment layout with the /Contents gap should be accepted")
	}
	// Wrong /Contents value in the gap.
	if contentsGapIsSignature(raw, [][2]int64{seg0, seg1}, []byte{0x00}) {
		t.Fatal("a gap whose hex does not equal /Contents must be rejected")
	}
	// More than two segments.
	if contentsGapIsSignature(raw, [][2]int64{seg0, seg1, seg1}, contents) {
		t.Fatal("more than two /ByteRange segments must be rejected")
	}
	// Second segment does not reach the end of the file (bytes left unsigned).
	if contentsGapIsSignature(raw, [][2]int64{seg0, {gapEnd, 1}}, contents) {
		t.Fatal("a second segment not reaching end-of-file must be rejected")
	}
	// First segment does not start at 0.
	if contentsGapIsSignature(raw, [][2]int64{{1, gapStart - 1}, seg1}, contents) {
		t.Fatal("a first segment not starting at offset 0 must be rejected")
	}
}

// TestDocumentUnmodifiedCombinesVerdicts pins the C11 combined verdict.
func TestDocumentUnmodifiedCombinesVerdicts(t *testing.T) {
	cases := []struct {
		valid, covers, want bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, c := range cases {
		got := SignatureResult{Valid: c.valid, CoversWholeDocument: c.covers}.DocumentUnmodified()
		if got != c.want {
			t.Errorf("DocumentUnmodified(Valid=%v,Covers=%v) = %v, want %v", c.valid, c.covers, got, c.want)
		}
	}
}
