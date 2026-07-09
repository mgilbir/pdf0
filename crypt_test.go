package pdf0

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rc4"
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
				s, ok := iobj.Value.(*Stream)
				if !ok {
					continue
				}
				if f, _ := s.Dict.Get("Filter").(Name); f != "FlateDecode" {
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
	h := &stdSecurityHandler{v: 4, r: 4, keyLen: 16, fileKey: bytes.Repeat([]byte{0xAB}, 16)}
	plain := []byte("The quick brown fox jumps over the lazy dog.")

	rc4Key := h.objectKey(7, 0, false)
	c, _ := rc4.NewCipher(rc4Key)
	enc := make([]byte, len(plain))
	c.XORKeyStream(enc, plain)
	if got := h.decrypt(enc, 7, 0, cryptRC4); !bytes.Equal(got, plain) {
		t.Errorf("RC4 round-trip: got %q", got)
	}

	aesEnc := aesCBCEncrypt(t, h.objectKey(7, 0, true), plain)
	if got := h.decrypt(aesEnc, 7, 0, cryptAESV2); !bytes.Equal(got, plain) {
		t.Errorf("AES-128 round-trip: got %q", got)
	}
}

// aesCBCEncrypt is the inverse of aesCBCDecrypt, for the round-trip test.
func aesCBCEncrypt(t *testing.T, key, plain []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := bytes.Repeat([]byte{0x01}, aes.BlockSize)
	out := make([]byte, aes.BlockSize+len(padded))
	copy(out, iv)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out[aes.BlockSize:], padded)
	return out
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
