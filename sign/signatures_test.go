package sign

import (
	"encoding/hex"
	"fmt"
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

// TestCMSSurvivesTheZeroPaddingItIsWrittenWith pins the reason /Contents may be
// passed to VerifyCMS exactly as the file holds it.
//
// A signature value is a hole reserved in the file *before* the bytes that go
// in it exist, so it is sized generously and the DER is zero-filled to the end.
// The DER says how long it is, so the padding is neither part of the signature
// nor in the way of reading it, and a verifier should hand the window over
// whole.
//
// The temptation is to strip the padding first, and it is wrong in a way that
// hides: a DER whose own last byte is legitimately 0x00 loses it, and that
// signature stops verifying. It is one in 256, which is why it surfaces as a
// flake rather than a failure — it was one, in this suite's own control,
// reproduced at 2 in 400 before it was understood.
//
// So the test does not sign once and hope. It signs until it has a signature
// ending in 0x00, which is the only kind that can tell a verifier that reads
// the DER length from one that trims first. Both then have to hold: padding
// must not break verification, and a blob genuinely one byte short must not
// verify, or this would pass for a VerifyCMS that had stopped looking at the
// end of the signature.
func TestCMSSurvivesTheZeroPaddingItIsWrittenWith(t *testing.T) {
	cert, key := signtest.CertKey(t)

	// The last byte of the DER is the last byte of the RSA signature, so it is
	// 0x00 about once in 256 and varying the content is how to get one.
	var cms, content []byte
	for i := 0; i < 4000 && cms == nil; i++ {
		c := []byte(fmt.Sprintf("the exact bytes that were signed, over the /ByteRange #%d", i))
		der, err := buildSignedData(cert, key, c)
		if err != nil {
			t.Fatalf("buildSignedData: %v", err)
		}
		if der[len(der)-1] == 0x00 {
			cms, content = der, c
		}
	}
	if cms == nil {
		t.Fatal("no signature ending in 0x00 in 4000 tries; that is a one-in-256 event, so something is wrong")
	}

	if _, _, _, err := VerifyCMS(cms, content); err != nil {
		t.Fatalf("the unpadded signature does not verify: %v", err)
	}
	for _, pad := range []int{1, 2, 64, 8192 - len(cms)} {
		padded := append(append([]byte(nil), cms...), make([]byte, pad)...)
		if _, _, _, err := VerifyCMS(padded, content); err != nil {
			t.Errorf("padded with %d zero bytes: %v\n"+
				"The signature ends in 0x00, so something between here and the parser "+
				"is stripping trailing zeros and taking a byte of it with them.", pad, err)
		}
	}

	// And the end of the DER is genuinely being read, rather than ignored.
	if _, _, _, err := VerifyCMS(cms[:len(cms)-1], content); err == nil {
		t.Error("a signature one byte short verified, so its length is not being read")
	}
}
