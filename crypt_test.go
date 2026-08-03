package pdf0

import (
	"bytes"
	"compress/zlib"
	"crypto/rc4"
	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/object"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecryptCorpusFiles decrypts the encrypted files in the veraPDF corpus and
// checks that their FlateDecode streams inflate — a wrong key or algorithm
// yields bytes that zlib rejects, so a clean inflate is strong evidence the
// decryption is correct. Self-skips when the corpus is absent.
func TestDecryptCorpusFiles(t *testing.T) {
	corpus := corpusRoot(t)
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
				t.Fatal("expected the file to be decrypted")
			}
			checked := 0
			for _, iobj := range doc.Objects {
				s, ok := iobj.Value.(*object.Stream)
				if !ok {
					continue
				}
				if f, _ := s.Dict.Get("Filter").(object.Name); f != "FlateDecode" {
					continue
				}
				zr, err := zlib.NewReader(bytes.NewReader(s.Data))
				if err != nil {
					t.Errorf("object %d: zlib header rejected after decryption: %v", iobj.Number, err)
					continue
				}
				if _, err := io.ReadAll(zr); err != nil {
					t.Errorf("object %d: inflate failed after decryption (wrong key?): %v", iobj.Number, err)
				}
				checked++
			}
			if checked == 0 {
				t.Skip("no FlateDecode streams to verify")
			}
		})
	}
}

// TestDecryptRoundTrip exercises the per-object key derivation and ciphers
// without the corpus: encrypt known plaintext, then confirm decrypt recovers it.
func TestDecryptRoundTrip(t *testing.T) {
	h := &crypt.Handler{V: 4, R: 4, KeyLen: 16, FileKey: bytes.Repeat([]byte{0xAB}, 16)}
	plain := []byte("The quick brown fox jumps over the lazy dog.")

	rc4Key := h.ObjectKey(7, 0, false)
	c, _ := rc4.NewCipher(rc4Key)
	enc := make([]byte, len(plain))
	c.XORKeyStream(enc, plain)
	if got := h.Decrypt(enc, 7, 0, crypt.RC4); !bytes.Equal(got, plain) {
		t.Errorf("RC4 round-trip: got %q", got)
	}

	aesEnc, err := crypt.AESCBCEncrypt(h.ObjectKey(7, 0, true), plain)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Decrypt(aesEnc, 7, 0, crypt.AESV2); !bytes.Equal(got, plain) {
		t.Errorf("AES-128 round-trip: got %q", got)
	}
}

// TestAESDecryptFailureIsNotPlaintext pins what happens when AES decryption
// fails under a key that is known good. A wrong password never reaches decrypt
// — buildStdSecurityHandler returns no handler and the document reports Locked
// — so a padding failure here means the blob is corrupt or was never encrypted.
// Returning it unchanged, as this used to, dressed high-entropy ciphertext as a
// /Title, a content stream or an XMP packet and handed it to the parser and
// every validator: noise presented as content. The value is emptied instead,
// the object number recorded, and Write refuses rather than committing the
// blank to a file.
func TestAESDecryptFailureIsNotPlaintext(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	h := &crypt.Handler{
		V: 5, R: 6, KeyLen: 32, FileKey: key,
		StmMethod: crypt.AESV3, StrMethod: crypt.AESV3,
		EncryptMetadata: true, EncryptObjNum: -1,
	}
	// A 32-byte blob (IV + one block) that does not decrypt: AES is
	// deterministic, so this is a fixed input, but assert it rather than assume.
	bad := make([]byte, 32)
	for i := range bad {
		bad[i] = byte(i)
	}
	if _, err := crypt.AESCBCDecrypt(key, bad); err == nil {
		t.Fatal("fixture does not exercise the failure: the blob decrypts cleanly")
	}

	st := &object.Stream{Dict: object.Dictionary{}, Data: append([]byte(nil), bad...)}
	st.Dict.Set("Length", object.Integer(len(bad)))
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Title", object.String{Value: append([]byte(nil), bad...)})
	doc := &Document{
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: cat},
			4: {Number: 4, Value: st},
		},
		Trailer:   object.Dictionary{},
		Encrypted: true,
		security:  h,
	}
	doc.Trailer.Set("Root", object.IndirectRef{Number: 1})

	doc.decryptFailures = h.DecryptDocument(doc.graph())

	if bytes.Equal(st.Data, bad) {
		t.Error("stream ciphertext was handed on unchanged as plaintext")
	}
	if len(st.Data) != 0 {
		t.Errorf("undecryptable stream data = %x, want empty", st.Data)
	}
	if s, _ := cat.Get("Title").(object.String); bytes.Equal(s.Value, bad) {
		t.Error("string ciphertext was handed on unchanged as plaintext")
	}
	want := []int{1, 4}
	if len(doc.decryptFailures) != len(want) {
		t.Fatalf("decryptFailures = %v, want %v", doc.decryptFailures, want)
	}
	for i, n := range want {
		if doc.decryptFailures[i] != n {
			t.Fatalf("decryptFailures = %v, want %v (sorted)", doc.decryptFailures, want)
		}
	}

	// The content is unrecoverable, so writing would replace it with blanks.
	err := doc.Write(&bytes.Buffer{})
	if err == nil {
		t.Fatal("Write accepted a document whose objects could not be decrypted")
	}
	if !strings.Contains(err.Error(), "could not be decrypted") {
		t.Errorf("refusal message = %q, want it to name the failed decrypt", err)
	}
}

// TestDecryptSuccessRecordsNoFailure is the other half: a document that
// decrypts cleanly must record nothing, so the Write refusal above cannot fire
// on a good file.
func TestDecryptSuccessRecordsNoFailure(t *testing.T) {
	key := bytes.Repeat([]byte{0x22}, 32)
	h := &crypt.Handler{
		V: 5, R: 6, KeyLen: 32, FileKey: key,
		StmMethod: crypt.AESV3, StrMethod: crypt.AESV3,
		EncryptMetadata: true, EncryptObjNum: -1,
	}
	plain := []byte("a page's worth of content")
	ct, err := crypt.AESCBCEncrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	st := &object.Stream{Dict: object.Dictionary{}, Data: ct}
	st.Dict.Set("Length", object.Integer(len(ct)))
	doc := &Document{
		Objects:   map[int]*object.IndirectObject{4: {Number: 4, Value: st}},
		Trailer:   object.Dictionary{},
		Encrypted: true,
		security:  h,
	}
	doc.decryptFailures = h.DecryptDocument(doc.graph())
	if !bytes.Equal(st.Data, plain) {
		t.Errorf("stream data = %q, want %q", st.Data, plain)
	}
	if len(doc.decryptFailures) != 0 {
		t.Errorf("clean decrypt recorded failures %v", doc.decryptFailures)
	}
}

func findCorpusFile(root, sub string) string {
	var found string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(p, sub) && strings.HasSuffix(p, ".pdf") {
			found = p
		}
		return nil
	})
	return found
}
