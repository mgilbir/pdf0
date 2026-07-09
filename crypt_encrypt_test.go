package pdf0

import (
	"bytes"
	"testing"
)

// TestSetEncryption encrypts a previously-unencrypted document with AES-256 and
// confirms it decrypts back with the user and owner passwords (but not a wrong
// one), and that the plaintext no longer appears in the written bytes.
func TestSetEncryption(t *testing.T) {
	const secret = "pdf0 confidential producer string"

	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	info := &Dictionary{}
	info.Set("Producer", String{Value: []byte(secret)})
	doc.Objects[4] = &IndirectObject{Number: 4, Value: info}
	doc.Trailer.Set("Info", IndirectRef{Number: 4})

	if err := doc.SetEncryption("sesame", "overlord"); err != nil {
		t.Fatalf("SetEncryption: %v", err)
	}
	// The in-memory document stays in the clear.
	if got := producerOf(doc); got != secret {
		t.Errorf("SetEncryption altered the in-memory document: %q", got)
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.Bytes()
	if bytes.Contains(out, []byte(secret)) {
		t.Error("plaintext string appears in the encrypted output")
	}
	if !bytes.Contains(out, []byte("/AESV3")) {
		t.Error("output is not AES-256 encrypted")
	}

	for _, pw := range []string{"sesame", "overlord"} {
		doc2, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), pw)
		if err != nil {
			t.Fatalf("password %q: %v", pw, err)
		}
		if doc2.security == nil {
			t.Errorf("password %q: not decrypted", pw)
		}
		if got := producerOf(doc2); got != secret {
			t.Errorf("password %q: /Producer = %q, want %q", pw, got, secret)
		}
	}

	// The empty (wrong) password leaves it encrypted.
	doc3, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	if doc3.security != nil {
		t.Error("empty password decrypted a password-protected file")
	}
}

func producerOf(doc *Document) string {
	info := doc.ResolveDict(doc.Trailer.Get("Info"))
	if info == nil {
		return ""
	}
	s, _ := info.Get("Producer").(String)
	return string(s.Value)
}
