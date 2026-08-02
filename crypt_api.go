package pdf0

import (
	"crypto/rand"
	"errors"

	"github.com/mgilbir/pdf0/internal/crypt"
)

// Encryption, from the document's side. The standard security handler itself
// lives in the crypt package; these are the three things a caller does to a
// Document about it.

// SetEncryption configures the document to be encrypted on the next Write using
// the standard security handler with AES-256 (V5/R6, ISO 32000-2 §7.6.4). The
// user password opens the file for reading; the owner password additionally
// carries full permissions. Either may be empty.
//
// It installs a fresh /Encrypt dictionary and a random file key, replacing any
// existing encryption. Write then enciphers every string and stream; the
// in-memory document stays in the clear, so it remains usable afterwards.
func (d *Document) SetEncryption(userPassword, ownerPassword string) error {
	// Refuse to encrypt a document whose content is still ciphertext (an
	// encrypted file we could not decrypt): enciphering it again would
	// double-encrypt and corrupt it. The caller must decrypt it first.
	if d.Locked() {
		return errors.New("cannot encrypt: the document is already encrypted and was not decrypted")
	}
	h, dict, err := crypt.NewAES256(userPassword, ownerPassword)
	if err != nil {
		return err
	}

	// Attach the /Encrypt dictionary as a new indirect object and point the
	// trailer at it. Its own strings (/O, /U, …) are never encrypted.
	maxObj := 0
	for num := range d.Objects {
		if num > maxObj {
			maxObj = num
		}
	}
	encNum := maxObj + 1
	d.Objects[encNum] = &IndirectObject{Number: encNum, Value: dict}
	h.EncryptObjNum = encNum
	d.security = h
	d.Encrypted = true

	trailer := d.Trailer.Clone()
	trailer.Set("Encrypt", IndirectRef{Number: encNum})
	// A file identifier is expected in an encrypted document; add one if absent.
	if trailer.Get("ID") == nil {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		trailer.Set("ID", Array{String{Value: id}, String{Value: append([]byte(nil), id...)}})
	}
	d.Trailer = *trailer
	return nil
}

// Locked reports whether the document carried encryption that could not be
// removed: it has an /Encrypt dictionary but no usable security handler, because
// the supplied password was wrong or the scheme is unsupported. Its strings and
// streams are still ciphertext.
//
// Encrypted alone does not distinguish this from a successfully decrypted file
// (both keep Encrypted true). Callers that intend to read content, validate,
// extract, or re-encrypt should check Locked first: on a locked document
// RemoveEncryption is a no-op, ExtractText and the validators see ciphertext,
// and SetEncryption/Write refuse.
func (d *Document) Locked() bool {
	return d.Encrypted && d.security == nil
}

// RemoveEncryption drops encryption from a document that was decrypted on Read,
// so a subsequent Write emits it in the clear. It clears the security handler
// and removes /Encrypt from the trailer (and the object graph). It has no
// effect on a document whose content could not be decrypted (see Locked).
func (d *Document) RemoveEncryption() {
	if d.security == nil {
		return
	}
	if d.security.EncryptObjNum >= 0 {
		delete(d.Objects, d.security.EncryptObjNum)
	}
	d.security = nil
	d.Encrypted = false
	trailer := d.Trailer.Clone()
	trailer.Delete("Encrypt")
	d.Trailer = *trailer
}
