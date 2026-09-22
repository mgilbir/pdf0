package pdf0

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/object"
)

// Encryption, from the document's side. The standard security handler itself
// lives in the crypt package; these are the things a caller does to, or asks
// of, a Document about it.

// The reasons a document is Locked, as LockReason reports them. Each
// LockReason error wraps exactly one of the first three; test with errors.Is.
var (
	// ErrWrongPassword: the password given to Read (none) or ReadWithPassword
	// is neither the file's user password nor its owner password.
	ErrWrongPassword = crypt.ErrWrongPassword
	// ErrEncryptionUnsupported: the file uses a security handler, revision or
	// crypt filter method pdf0 does not implement (a public-key handler, the
	// deprecated revision 5, a crypt filter delegated to another handler).
	ErrEncryptionUnsupported = crypt.ErrUnsupported
	// ErrEncryptionMalformed: the /Encrypt dictionary violates ISO 32000-2
	// 7.6 — a missing or mistyped entry, a /Length outside 40–128 bits, a
	// /V–/R pair the spec does not define, an R6 /Perms that does not decrypt
	// under the recovered key.
	ErrEncryptionMalformed = crypt.ErrMalformed
	// ErrPermsMismatch is reported by EncryptionWarnings, never by LockReason:
	// the R6 /Perms entry is intact but disagrees with /P or /EncryptMetadata.
	ErrPermsMismatch = crypt.ErrPermsMismatch
)

// SetEncryption configures the document to be encrypted on the next Write using
// the standard security handler with AES-256 (V5/R6, ISO 32000-2 §7.6.4).
//
// The user password opens the file. It must not be empty: an empty user
// password opens for anyone, and since SetEncryption grants every permission
// (/P −4), the result would be encrypted without being protected.
//
// The owner password also opens the file; ISO 32000-2 reserves it for changing
// the security settings. When it is empty, a random 256-bit owner password is
// generated and discarded, so the file opens with the user password alone. An
// empty owner password is never written as the owner password: every reader
// tries the empty password, so that would let anyone open the file (audit
// 2026-09-22 C23).
//
// Both passwords are prepared as ISO 32000-2 Algorithm 2.A requires: SASLprep
// (RFC 4013), UTF-8, then truncation to 127 bytes — so only the first 127 bytes
// of a longer password protect the file, as with every conforming reader and
// writer. A password SASLprep prohibits (control characters, private-use or
// unassigned code points, mixed-direction text) is refused with an error, since
// no conforming reader could reproduce it.
//
// It installs a fresh /Encrypt dictionary and a random file key, replacing any
// existing encryption: on a document decrypted by Read, the old /Encrypt
// dictionary, the objects that only it referenced, and any per-stream /Crypt
// filters naming its crypt filters are removed. Write then enciphers every
// string and stream; the in-memory document stays in the clear, so it remains
// usable afterwards. It refuses a Locked document.
func (d *Document) SetEncryption(userPassword, ownerPassword string) error {
	// Refuse to encrypt a document whose content is still ciphertext (an
	// encrypted file we could not decrypt): enciphering it again would
	// double-encrypt and corrupt it. The caller must decrypt it first.
	if d.Locked() {
		return errors.New("cannot encrypt: the document is already encrypted and was not decrypted")
	}
	if userPassword == "" {
		return errors.New("cannot encrypt: an empty user password lets anyone open the file, and SetEncryption grants every permission, so the file would not be protected")
	}
	if ownerPassword == "" {
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		ownerPassword = hex.EncodeToString(random)
	}
	h, dict, err := crypt.NewAES256(userPassword, ownerPassword)
	if err != nil {
		return fmt.Errorf("cannot encrypt: %w", err)
	}
	return d.installEncryption(h, dict)
}

// installEncryption replaces the document's encryption with handler h and its
// /Encrypt dictionary. SetEncryption decides which handlers are acceptable;
// this only installs one.
func (d *Document) installEncryption(h *crypt.Handler, dict *object.Dictionary) error {
	// A file identifier is expected in an encrypted document; make one if
	// absent, before anything is changed, so a failure leaves the document as
	// it was.
	var id []byte
	if d.Trailer.Get("ID") == nil {
		id = make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
	}

	// The old scheme goes entirely: its dictionary (and what only it
	// referenced) would otherwise stay in Objects as an orphan, be encrypted as
	// ordinary content and written out, and a stream's /Crypt filter would name
	// a crypt filter the new dictionary does not define. decryptFailures is
	// kept: those objects' content is still missing, whatever the new key.
	d.dropEncryption()

	// Attach the /Encrypt dictionary as a new indirect object and point the
	// trailer at it. Its own strings (/O, /U, …) are never encrypted.
	maxObj := 0
	for num := range d.Objects {
		if num > maxObj {
			maxObj = num
		}
	}
	encNum := maxObj + 1
	d.Objects[encNum] = &object.IndirectObject{Number: encNum, Value: dict}
	h.EncryptObjNum = encNum
	d.security = h
	d.Encrypted = true
	d.lockReason, d.encryptWarnings = nil, nil

	trailer := d.Trailer.Clone()
	trailer.Set("Encrypt", object.IndirectRef{Number: encNum})
	if id != nil {
		trailer.Set("ID", object.Array{object.String{Value: id}, object.String{Value: append([]byte(nil), id...)}})
	}
	d.Trailer = *trailer
	return nil
}

// Locked reports whether the document carries encryption that could not be
// removed: it has an /Encrypt dictionary but no usable security handler,
// because the password was wrong, the scheme is unsupported, or the /Encrypt
// dictionary is malformed. LockReason says which. Its strings and streams are
// still ciphertext.
//
// Encrypted alone does not distinguish this from a successfully decrypted file
// (both keep Encrypted true). Callers that intend to read content, validate,
// extract, or re-encrypt should check Locked first: on a locked document
// RemoveEncryption is a no-op, ExtractText and the validators see ciphertext,
// SetEncryption refuses, and Write writes the file back verbatim.
func (d *Document) Locked() bool {
	return (d.Encrypted || d.Trailer.Get("Encrypt") != nil) && d.security == nil
}

// LockReason reports why the document is Locked, or nil when it is not. The
// error wraps ErrWrongPassword, ErrEncryptionUnsupported or
// ErrEncryptionMalformed, and its text names the entry or value at fault.
func (d *Document) LockReason() error {
	if !d.Locked() {
		return nil
	}
	if d.lockReason != nil {
		return d.lockReason
	}
	// A document built or edited in memory to carry /Encrypt, with no
	// handler behind it: nothing was decrypted, so nothing can be assumed.
	return fmt.Errorf("%w: the document carries /Encrypt but Read built no security handler for it", ErrEncryptionUnsupported)
}

// EncryptionWarnings reports defects in the encryption of a document Read
// decrypted that did not prevent decryption — currently ErrPermsMismatch.
func (d *Document) EncryptionWarnings() []error {
	return slices.Clone(d.encryptWarnings)
}

// DecryptFailures lists, in ascending order, the objects whose content Read
// could not decrypt although the document is not Locked: ciphertext that is
// corrupt, and streams that name a crypt filter pdf0 cannot apply (an embedded
// file under an unsupported /EFF, a /Crypt filter naming an undefined or
// unsupported crypt filter). Their strings and stream bodies are empty rather
// than ciphertext presented as content, and Write refuses the document.
func (d *Document) DecryptFailures() []int {
	return slices.Clone(d.decryptFailures)
}

// RemoveEncryption drops encryption from a document that was decrypted on Read,
// so a subsequent Write emits it in the clear. It clears the security handler,
// removes /Encrypt from the trailer, and removes from the object graph the
// /Encrypt dictionary, the objects only it referenced, and every per-stream
// /Crypt filter (which would otherwise name a crypt filter no dictionary
// defines). It has no effect on a document whose content could not be
// decrypted (see Locked).
func (d *Document) RemoveEncryption() {
	if d.security == nil {
		return
	}
	d.dropEncryption()
	d.security = nil
	d.Encrypted = false
	d.lockReason, d.encryptWarnings = nil, nil
}

// dropEncryption removes every trace of the current encryption from a document
// whose content is plaintext: the trailer's /Encrypt, the objects reachable
// only through it — the dictionary itself, and an indirect /CF or string,
// which would otherwise be written as orphans (and, left in a re-encrypted
// file, be enciphered as if they were content, publishing the old /O and /U
// hashes under the new key) — and /Crypt entries in stream filter chains.
func (d *Document) dropEncryption() {
	if d.Trailer.Get("Encrypt") == nil {
		return
	}
	candidates := d.encryptReachable()
	if d.security != nil && d.security.EncryptObjNum >= 0 {
		candidates[d.security.EncryptObjNum] = true
	}
	trailer := d.Trailer.Clone()
	trailer.Delete("Encrypt")
	d.Trailer = *trailer

	// Keep any candidate something else still references.
	if len(candidates) > 0 {
		referenced := map[int]bool{}
		var walk func(o object.Object)
		walk = func(o object.Object) {
			switch v := o.(type) {
			case object.IndirectRef:
				referenced[v.Number] = true
			case *object.Dictionary:
				for val := range v.Values() {
					walk(val)
				}
			case object.Array:
				for _, e := range v {
					walk(e)
				}
			case *object.Stream:
				walk(&v.Dict)
			}
		}
		walk(&d.Trailer)
		for num, iobj := range d.Objects {
			if !candidates[num] {
				walk(iobj.Value)
			}
		}
		for num := range candidates {
			if !referenced[num] {
				delete(d.Objects, num)
			}
		}
	}

	for _, iobj := range d.Objects {
		if s, ok := iobj.Value.(*object.Stream); ok {
			stripCryptFilter(s)
		}
	}
}

// stripCryptFilter removes a leading /Crypt entry, and its decode parameters,
// from a stream's filter chain. The data is already decrypted; without an
// /Encrypt dictionary (or under a new one) the named crypt filter means
// nothing, and a reader would reject the stream.
func stripCryptFilter(s *object.Stream) {
	switch f := s.Dict.Get("Filter").(type) {
	case object.Name:
		if f == "Crypt" {
			s.Dict.Delete("Filter")
			s.Dict.Delete("DecodeParms")
		}
	case object.Array:
		if len(f) == 0 {
			return
		}
		if n, _ := f[0].(object.Name); n != "Crypt" {
			return
		}
		if len(f) == 1 {
			s.Dict.Delete("Filter")
			s.Dict.Delete("DecodeParms")
			return
		}
		s.Dict.Set("Filter", slices.Clone(f[1:]))
		switch p := s.Dict.Get("DecodeParms").(type) {
		case object.Array:
			if len(p) > 1 {
				s.Dict.Set("DecodeParms", slices.Clone(p[1:]))
			} else {
				s.Dict.Delete("DecodeParms")
			}
		case *object.Dictionary:
			s.Dict.Delete("DecodeParms") // it belonged to the Crypt entry
		}
	}
}
