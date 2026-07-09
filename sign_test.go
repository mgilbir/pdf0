package pdf0

import (
	"bytes"
	"testing"
)

// TestSignAndVerify signs a document and verifies the resulting signature.
func TestSignAndVerify(t *testing.T) {
	cert, key := testCertKey(t)
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
		t.Fatalf("re-read signed: %v", err)
	}
	results := signed.VerifySignatures(out)
	if len(results) != 1 {
		t.Fatalf("got %d signatures, want 1", len(results))
	}
	r := results[0]
	if !r.Valid || r.Err != nil {
		t.Fatalf("signature invalid: valid=%v err=%v", r.Valid, r.Err)
	}
	if !r.CoversWholeDocument {
		t.Error("signature should cover the whole document")
	}
	if r.SignerCommonName != "pdf0 test signer" {
		t.Errorf("signer = %q", r.SignerCommonName)
	}

	// Any change to the signed bytes must break verification.
	out[len(out)/2] ^= 0xFF
	if again, err := Read(bytes.NewReader(out), int64(len(out))); err == nil {
		if res := again.VerifySignatures(out); len(res) == 1 && res[0].Valid {
			t.Error("tampered signed document still verified")
		}
	}
}
