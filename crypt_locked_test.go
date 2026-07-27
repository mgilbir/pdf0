package pdf0

import (
	"bytes"
	"testing"
)

// TestLockedDocumentState is the C6/C7/C8 root-cause guard: a document read from
// an encrypted file without the correct password is observably Locked, so
// callers can refuse to operate on ciphertext instead of silently producing
// garbage. RemoveEncryption and SetEncryption must not act on a locked document.
func TestLockedDocumentState(t *testing.T) {
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetEncryption("secret", "secret"); err != nil {
		t.Fatalf("SetEncryption: %v", err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	enc := buf.Bytes()

	// Reading without the password yields a locked document.
	locked, err := Read(bytes.NewReader(enc), int64(len(enc)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !locked.Encrypted {
		t.Fatal("expected Encrypted")
	}
	if !locked.Locked() {
		t.Fatal("a wrong/absent-password read must report Locked")
	}
	// RemoveEncryption must not silently "unlock" ciphertext.
	locked.RemoveEncryption()
	if !locked.Encrypted || !locked.Locked() {
		t.Fatal("RemoveEncryption must be a no-op on a locked document")
	}
	// SetEncryption must refuse (re-encrypting ciphertext would corrupt it).
	if err := locked.SetEncryption("new", "new"); err == nil {
		t.Fatal("SetEncryption must refuse a locked document")
	}

	// Reading with the correct password yields a usable, non-locked document.
	ok, err := ReadWithPassword(bytes.NewReader(enc), int64(len(enc)), "secret")
	if err != nil {
		t.Fatalf("ReadWithPassword: %v", err)
	}
	if ok.Locked() {
		t.Fatal("a correct-password read must not be Locked")
	}
	ok.RemoveEncryption()
	if ok.Encrypted {
		t.Fatal("RemoveEncryption should drop encryption on a decrypted document")
	}
}
