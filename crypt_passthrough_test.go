package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEncryptedPassthroughRoundTrip verifies that a document pdf0 cannot decrypt
// (here, an RC4-encrypted file read without its password) is written back as a
// lossless passthrough: the still-encrypted strings and streams are re-emitted
// verbatim under the preserved /Encrypt and /ID, so the result decrypts to the
// original content with the correct password and stays encrypted without it.
// Before the passthrough, Write refused such a document outright.
func TestEncryptedPassthroughRoundTrip(t *testing.T) {
	const producer = "pdf0 passthrough producer string"
	data := buildRC4R3EncryptedPDF(t, "sesame", "overlord", producer)

	// Read without the password: the document stays encrypted.
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.security != nil {
		t.Fatal("expected the file to stay encrypted (no password)")
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("passthrough Write: %v", err)
	}
	out := buf.Bytes()

	// The rewritten file decrypts with the original password to the original
	// content: the ciphertext and key material were preserved verbatim.
	dec, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "sesame")
	if err != nil {
		t.Fatalf("re-read with password: %v", err)
	}
	if dec.security == nil {
		t.Fatal("passthrough output does not decrypt with the correct password")
	}
	cat, _ := dec.Objects[1].Value.(*Dictionary)
	if s, _ := cat.Get("Producer").(String); string(s.Value) != producer {
		t.Errorf("/Producer after passthrough = %q, want %q", s.Value, producer)
	}

	// Without the password the output is still encrypted (ciphertext preserved),
	// and a wrong password must not decrypt it.
	if enc, _ := ReadWithPassword(bytes.NewReader(out), int64(len(out)), ""); enc.security != nil {
		t.Error("passthrough output decrypted with the empty password")
	}
	if wrong, _ := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "wrong"); wrong.security != nil {
		t.Error("passthrough output decrypted with a wrong password")
	}

	// Write must not have mutated the in-memory ciphertext.
	cat0, _ := doc.Objects[1].Value.(*Dictionary)
	if s, _ := cat0.Get("Producer").(String); string(s.Value) == producer {
		t.Error("passthrough Write decrypted/mutated the in-memory model")
	}
}

// TestEncryptedPassthroughRefusesIncompleteModel confirms the passthrough refuses
// when an object stream could not be decoded on read: its compressed objects are
// locked inside the still-encrypted container and absent from the model, so a
// re-serialization would silently drop them. Refusing is preferable to writing a
// lossy file.
func TestEncryptedPassthroughRefusesIncompleteModel(t *testing.T) {
	// A resolvable /Encrypt dictionary (object 9) so the passthrough reaches the
	// incomplete-model check rather than the unresolvable-/Encrypt refusal.
	encDict := &Dictionary{}
	encDict.Set("Filter", Name("Standard"))
	d := &Document{
		Objects: map[int]*IndirectObject{
			1: {Number: 1, Value: &Dictionary{}},
			9: {Number: 9, Value: encDict},
		},
		Encrypted:     true,
		brokenObjStms: []int{5},
	}
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	d.Trailer.Set("Encrypt", IndirectRef{Number: 9})
	var buf bytes.Buffer
	err := d.Write(&buf)
	if err == nil {
		t.Fatal("expected Write to refuse an encrypted document with a broken object stream")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("could not be decrypted")) {
		t.Errorf("refusal message = %q, want it to mention the undecryptable object stream", err)
	}
}

// TestEncryptedPassthroughAESCorpus exercises the passthrough on real AES files:
// each encrypted corpus file is read with a deliberately wrong password (so it
// stays encrypted), written as a passthrough, then re-read with the empty
// password it actually uses — which must still decrypt. Gated on the corpus.
func TestEncryptedPassthroughAESCorpus(t *testing.T) {
	corpus := os.Getenv("VERAPDF_CORPUS")
	if corpus == "" {
		corpus = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpus); err != nil {
		t.Skip("veraPDF corpus not found; run `make corpus`")
	}
	cases := []string{
		filepath.Join("PDF_A-2b", "6.1 File structure", "6.1.3 File trailer", "veraPDF test suite 6-1-3-t02-fail-a"),
		filepath.Join("PDF_A-4", "6.1 File structure", "6.1.3 File trailer", "veraPDF test suite 6-1-3-t02-fail-a"),
	}
	ran := 0
	for _, sub := range cases {
		p := findCorpusFile(corpus, sub)
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// Confirm the empty password decrypts it (baseline), then read it with a
		// wrong password so the passthrough path is taken.
		if base, _ := Read(bytes.NewReader(data), int64(len(data))); base == nil || base.security == nil {
			continue // not an empty-password-decryptable encrypted file
		}
		doc, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), "definitely-wrong")
		if err != nil {
			t.Fatalf("%s: read with wrong password: %v", sub, err)
		}
		if doc.security != nil {
			t.Fatalf("%s: wrong password unexpectedly decrypted", sub)
		}
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			// An object-stream file the wrong password could not decrypt is
			// legitimately refused (incomplete model); skip those.
			if len(doc.brokenObjStms) > 0 {
				continue
			}
			t.Fatalf("%s: passthrough Write: %v", sub, err)
		}
		out := buf.Bytes()
		back, err := Read(bytes.NewReader(out), int64(len(out)))
		if err != nil {
			t.Fatalf("%s: re-read passthrough: %v", sub, err)
		}
		if back.security == nil {
			t.Errorf("%s: passthrough output no longer decrypts with the empty password", sub)
		}
		ran++
	}
	if ran == 0 {
		t.Skip("no empty-password-decryptable AES corpus file available for the passthrough check")
	}
}
