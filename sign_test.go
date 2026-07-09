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

// TestSignIncremental signs as an incremental update: the original bytes must be
// preserved verbatim and the signature must verify.
func TestSignIncremental(t *testing.T) {
	cert, key := testCertKey(t)
	original := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSignedIncremental(&buf, original, cert, key); err != nil {
		t.Fatalf("WriteSignedIncremental: %v", err)
	}
	out := buf.Bytes()

	if !bytes.HasPrefix(out, original) {
		t.Fatal("incremental signature altered the original bytes")
	}
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res := signed.VerifySignatures(out)
	if len(res) != 1 || !res[0].Valid {
		t.Fatalf("incremental signature did not verify: %+v", res)
	}
	if !res[0].CoversWholeDocument {
		t.Error("signature should cover the whole document")
	}
}
