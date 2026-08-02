package sign

import (
	"encoding/hex"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestCMSRoundTrip signs content and verifies the detached CMS against it,
// including that a single modified byte is detected.
func TestCMSRoundTrip(t *testing.T) {
	cert, key := signtest.CertKey(t)
	content := []byte("the exact bytes that were signed, over the /ByteRange")

	cms, err := buildSignedData(cert, key, content)
	if err != nil {
		t.Fatalf("buildSignedData: %v", err)
	}
	// The blob must parse as CMS with a certificate and one SignerInfo.
	if info := core.ParseCMSSignedData(cms); !info.Parsed || !info.HasCertificate || info.SignerInfoCount != 1 {
		t.Fatalf("built CMS is malformed: %+v", info)
	}
	signer, _, _, err := VerifyCMS(cms, content)
	if err != nil {
		t.Fatalf("VerifyCMS: %v", err)
	}
	if signer.Subject.CommonName != "pdf0 test signer" {
		t.Errorf("common name = %q", signer.Subject.CommonName)
	}
	tampered := append(append([]byte(nil), content...), '!')
	if _, _, _, err := VerifyCMS(cms, tampered); err == nil {
		t.Error("verification succeeded on modified content")
	}
}

// TestVerifySignatures drives the full document path: build the /ByteRange,
// sign the covered bytes, and confirm the signature verifies (and fails after a
// change to the signed region).
func TestVerifySignatures(t *testing.T) {
	cert, key := signtest.CertKey(t)
	prefix := []byte("%PDF-2.0 ... content before the signature value ...")
	suffix := []byte("... content after the signature value ... %%EOF")

	signed := append(append([]byte(nil), prefix...), suffix...)
	cms, err := buildSignedData(cert, key, signed)
	if err != nil {
		t.Fatal(err)
	}

	// Serialize the signature the way a real file does: the /Contents value is a
	// hex string <…> occupying the gap between the two /ByteRange segments, so the
	// coverage check can confirm the gap is exactly the signature value.
	hexContents := []byte("<" + hex.EncodeToString(cms) + ">")
	raw := make([]byte, 0, len(prefix)+len(hexContents)+len(suffix))
	raw = append(raw, prefix...)
	raw = append(raw, hexContents...)
	raw = append(raw, suffix...)

	sig := &object.Dictionary{}
	sig.Set("Type", object.Name("Sig"))
	sig.Set("SubFilter", object.Name("adbe.pkcs7.detached"))
	sig.Set("Contents", object.String{Value: cms, IsHex: true})
	sig.Set("ByteRange", object.Array{
		object.Integer(0), object.Integer(len(prefix)),
		object.Integer(len(prefix) + len(hexContents)), object.Integer(len(suffix)),
	})
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: sig},
	}})

	results := VerifySignatures(doc, raw)
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
	if res := VerifySignatures(doc, tampered); res[0].Valid {
		t.Error("modified document still verified")
	}
}
