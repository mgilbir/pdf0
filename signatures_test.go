package pdf0

import (
	"bytes"
	"crypto/x509"
	"github.com/mgilbir/pdf0/internal/signtest"
	"testing"
)

// TestVerifySignaturesWithRoots checks the optional trust-chain verification: a
// self-signed signer verifies against a root store that contains it, does not
// against an empty store, and the trust outcome never changes the integrity
// verdict (Valid).
func TestVerifySignaturesWithRoots(t *testing.T) {
	cert, key := signtest.CertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(cert)
	res := signed.VerifySignaturesWithRoots(out, roots)
	if len(res) != 1 || !res[0].Valid {
		t.Fatalf("expected one valid signature, got %+v", res)
	}
	if !res[0].TrustedChain {
		t.Errorf("chain should be trusted against its own root: %v", res[0].ChainErr)
	}

	res = signed.VerifySignaturesWithRoots(out, x509.NewCertPool())
	if res[0].TrustedChain {
		t.Error("an empty root store must not trust the chain")
	}
	if res[0].ChainErr == nil {
		t.Error("expected a chain error with no trusted roots")
	}
	if !res[0].Valid {
		t.Error("Valid (content integrity) must hold regardless of trust")
	}
}
