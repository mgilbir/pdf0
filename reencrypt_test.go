package pdf0

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestReEncryptRoundTrip reads a password-protected file, writes it (which
// re-encrypts with the retained key and re-emits /Encrypt), and confirms the
// result still decrypts to the same content with the same password — and not
// with a wrong one.
func TestReEncryptRoundTrip(t *testing.T) {
	const producer = "pdf0 round-trip producer string"
	data := buildRC4R3EncryptedPDF(t, "sesame", "overlord", producer)

	doc, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), "sesame")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.Bytes()

	doc2, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "sesame")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if doc2.security == nil {
		t.Fatal("re-written file is not encrypted")
	}
	d, _ := doc2.Objects[1].Value.(*Dictionary)
	if s, _ := d.Get("Producer").(String); string(s.Value) != producer {
		t.Errorf("/Producer after round-trip = %q, want %q", s.Value, producer)
	}
	// The in-memory plaintext must be untouched by Write.
	d0, _ := doc.Objects[1].Value.(*Dictionary)
	if s, _ := d0.Get("Producer").(String); string(s.Value) != producer {
		t.Errorf("Write mutated the in-memory plaintext: %q", s.Value)
	}

	doc3, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if doc3.security != nil {
		t.Error("wrong password decrypted the re-written file")
	}
}

// TestReEncryptCorpusRoundTrip round-trips each encrypted scheme in the corpus:
// decrypt on Read, re-encrypt on Write, then re-read and confirm the streams
// still decrypt (their FlateDecode content inflates).
func TestReEncryptCorpusRoundTrip(t *testing.T) {
	corpus := os.Getenv("VERAPDF_CORPUS")
	if corpus == "" {
		corpus = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpus); err != nil {
		t.Skip("veraPDF corpus not found; run `make corpus`")
	}
	cases := []struct{ name, sub string }{
		{"RC4 V2/R3", filepath.Join("PDFA-1b", "6.1 File structure", "6.1.3 File trailer", "isartor-6-1-3-t02-fail-a")},
		{"AES-128 V4/R4", filepath.Join("PDF_A-2b", "6.1 File structure", "6.1.3 File trailer", "veraPDF test suite 6-1-3-t02-fail-a")},
		{"AES-256 V5/R6", filepath.Join("PDF_A-4", "6.1 File structure", "6.1.3 File trailer", "veraPDF test suite 6-1-3-t02-fail-a")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := findCorpusFile(corpus, c.sub)
			if p == "" {
				t.Skipf("%s not found", c.sub)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if doc.security == nil {
				t.Skip("file did not decrypt")
			}
			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("write: %v", err)
			}
			out := buf.Bytes()
			doc2, err := Read(bytes.NewReader(out), int64(len(out)))
			if err != nil {
				t.Fatalf("re-read: %v", err)
			}
			if doc2.security == nil {
				t.Fatal("re-written file is not encrypted")
			}
			checked := 0
			for _, iobj := range doc2.Objects {
				s, ok := iobj.Value.(*Stream)
				if !ok {
					continue
				}
				if f, _ := s.Dict.Get("Filter").(Name); f != "FlateDecode" {
					continue
				}
				zr, err := zlib.NewReader(bytes.NewReader(s.Data))
				if err != nil {
					t.Errorf("object %d: re-encrypted stream does not decrypt/inflate: %v", iobj.Number, err)
					continue
				}
				if _, err := io.ReadAll(zr); err != nil {
					t.Errorf("object %d: inflate after round-trip failed: %v", iobj.Number, err)
				}
				checked++
			}
			if checked == 0 {
				t.Skip("no FlateDecode streams to verify")
			}
		})
	}
}
