package crypt

import (
	"bytes"
	"crypto/aes"
	"errors"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// r6View builds a document whose /Encrypt is an R6 dictionary for the given
// user password bytes, hashed exactly as given — the shape a producer that
// skips SASLprep writes.
func r6View(t *testing.T, userBytes []byte) core.View {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	salts := []byte("uvsaltXXukeysltXovsaltXXokeysltX")
	uVal, uKey, oVal, oKey := salts[0:8], salts[8:16], salts[16:24], salts[24:32]
	zero := make([]byte, 16)
	u := append(append(hash2B(userBytes, uVal, nil), uVal...), uKey...)
	ue, err := aesCBCNoPadEncrypt(hash2B(userBytes, uKey, nil), zero, key)
	if err != nil {
		t.Fatal(err)
	}
	owner := []byte("owner")
	o := append(append(hash2B(owner, oVal, u), oVal...), oKey...)
	oe, err := aesCBCNoPadEncrypt(hash2B(owner, oKey, u), zero, key)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := encryptPerms(key, -4, true, []byte("tail"))
	if err != nil {
		t.Fatal(err)
	}
	cf := &object.Dictionary{}
	std := &object.Dictionary{}
	std.Set("CFM", object.Name("AESV3"))
	cf.Set("StdCF", std)
	enc := &object.Dictionary{}
	for k, v := range map[object.Name]object.Object{
		"Filter": object.Name("Standard"), "V": object.Integer(5), "R": object.Integer(6),
		"O": object.String{Value: o}, "U": object.String{Value: u},
		"OE": object.String{Value: oe}, "UE": object.String{Value: ue},
		"Perms": object.String{Value: perms}, "P": object.Integer(-4),
		"CF": cf, "StmF": object.Name("StdCF"), "StrF": object.Name("StdCF"),
	} {
		enc.Set(k, v)
	}
	trailer := &object.Dictionary{}
	trailer.Set("Encrypt", enc)
	return core.View{Objects: map[int]*object.IndirectObject{}, Trailer: trailer}
}

// TestR6RawUTF8Candidate: a file hashed with a password's raw UTF-8 bytes,
// not its SASLprep form, opens with the password its author typed (the second
// candidate), and only with that.
func TestR6RawUTF8Candidate(t *testing.T) {
	typed := "open\u00A0sesame" // SASLprep maps the no-break space to SPACE
	v := r6View(t, []byte(typed))
	if h, err := Open(v, typed); err != nil || h == nil {
		t.Fatalf("the typed password does not open a raw-UTF-8 file: %v", err)
	}
	if _, err := Open(v, "open sesame"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("the SASLprep form opened a file hashed with the raw bytes: %v", err)
	}
	if _, err := Open(v, "owner"); err != nil {
		t.Errorf("owner password: %v", err)
	}
}

// TestR6CandidatesTruncate: both candidates are cut to 127 bytes.
func TestR6CandidatesTruncate(t *testing.T) {
	long := bytes.Repeat([]byte("b"), 300)
	for _, c := range r6Candidates(string(long)) {
		if len(c) != 127 {
			t.Errorf("candidate of %d bytes, want 127", len(c))
		}
	}
	if got := len(r6Candidates("\u00A0")); got != 2 {
		t.Errorf("%d candidates for a password SASLprep changes, want 2", got)
	}
	if got := len(r6Candidates("plain")); got != 1 {
		t.Errorf("%d candidates for a password SASLprep leaves alone, want 1", got)
	}
}

// TestPermsMarkerRequired checks validatePerms directly: the "adb" marker is
// what proves the key.
func TestPermsMarkerRequired(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	good, err := encryptPerms(key, -4, true, []byte("abcd"))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{FileKey: key, EncryptMetadata: true}
	if err := h.validatePerms(good, -4); err != nil || len(h.Warnings) != 0 {
		t.Fatalf("valid /Perms: %v, warnings %v", err, h.Warnings)
	}
	wrongKey := &Handler{FileKey: bytes.Repeat([]byte{8}, 32), EncryptMetadata: true}
	if err := wrongKey.validatePerms(good, -4); !errors.Is(err, ErrMalformed) {
		t.Errorf("a wrong key passed Algorithm 13: %v", err)
	}
	// Re-encrypt a block with the marker damaged under the right key.
	block, _ := aes.NewCipher(key)
	var plain [16]byte
	block.Decrypt(plain[:], good)
	plain[10] = 'X'
	bad := make([]byte, 16)
	block.Encrypt(bad, plain[:])
	if err := h.validatePerms(bad, -4); !errors.Is(err, ErrMalformed) {
		t.Errorf("a /Perms without the adb marker passed: %v", err)
	}
}
