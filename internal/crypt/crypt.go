package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The PDF standard security handler (ISO 32000-1 §7.6, ISO 32000-2 §7.6), in
// both directions: key derivation and decryption for a user or owner password,
// and the encryption path used when a decrypted document is written back or
// SetEncryption is called. RC4 (V1/V2, R2–R4), AES-128 (V4, /AESV2), and
// AES-256 (V5, /AESV3, R6) are supported; encrypt.go builds the /Encrypt
// dictionary for a newly encrypted document, params.go validates a file's, and
// password.go prepares passwords.

// PasswordPad is the 32-byte padding string (ISO 32000-1 §7.6.3.3, Algorithm 2,
// step a). An empty user password pads to exactly this string.
var PasswordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56,
	0xFF, 0xFA, 0x01, 0x08, 0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// method is the algorithm a crypt filter applies.
type method int

const (
	None  method = iota // Identity — no encryption
	RC4                 // V2
	AESV2               // AES-128-CBC
	AESV3               // AES-256-CBC (file key used directly)

	// invalid marks a crypt filter this handler cannot apply. It never
	// reaches a cipher: data it would govern is reported as not decrypted.
	invalid method = -1
)

// Handler holds a validated /Encrypt dictionary and the derived file
// encryption key.
type Handler struct {
	V, R            int
	KeyLen          int // file key length in bytes
	FileKey         []byte
	StmMethod       method // streams (StmF)
	StrMethod       method // strings (StrF)
	EFFMethod       method // embedded file streams (EFF, defaulting to StmF)
	EncryptMetadata bool
	EncryptObjNum   int // object number of the /Encrypt dict, or -1 if inline

	// filters maps every /CF crypt filter name to its method, for streams that
	// name their own crypt filter (a /Crypt entry in /Filter).
	filters map[object.Name]method

	// exempt holds the objects that carry the /Encrypt dictionary's own
	// values, which are never encrypted (ISO 32000-2 7.6.2): the dictionary
	// itself and any value it references indirectly.
	exempt map[int]bool

	// Warnings are defects that do not prevent decryption (ErrPermsMismatch).
	Warnings []error

	// failedObjects collects the object numbers whose ciphertext this handler
	// could not decrypt: AES data that does not decrypt, and streams whose
	// crypt filter the handler cannot apply (see Decrypt and streamMethod).
	failedObjects map[int]bool
}

// noteDecryptFailure records that an object's ciphertext did not decrypt.
func (h *Handler) noteDecryptFailure(num int) {
	if h.failedObjects == nil {
		h.failedObjects = map[int]bool{}
	}
	h.failedObjects[num] = true
}

// Open validates the trailer's /Encrypt dictionary and derives the file key
// for the given password (empty for the common case), trying it as both the
// user and the owner password.
//
// It returns (nil, nil) for a file with no /Encrypt. Otherwise exactly one of
// the results is non-nil: a handler, or the reason the document must stay
// Locked, wrapping ErrWrongPassword, ErrUnsupported or ErrMalformed. It never
// fails in any other way, so an /Encrypt dictionary cannot make Read fail.
func Open(doc core.View, password string) (*Handler, error) {
	encObj := doc.Trailer.Get("Encrypt")
	if encObj == nil {
		return nil, nil
	}
	p, err := parseParams(doc, encObj)
	if err != nil {
		return nil, err
	}
	h := &Handler{
		V: p.v, R: p.r, KeyLen: p.keyLen,
		StmMethod: p.stm, StrMethod: p.str, EFFMethod: p.eff,
		EncryptMetadata: p.encryptMetadata, EncryptObjNum: p.encNum,
		filters: p.filters, exempt: p.exempt,
	}

	if p.r == 6 {
		for _, pw := range r6Candidates(password) {
			if h.deriveKeyR6(pw, p.u, p.ue, p.o, p.oe) {
				if err := h.validatePerms(p.perms, p.p); err != nil {
					return nil, err
				}
				return h, nil
			}
		}
		return nil, wrongPassword(password)
	}

	// Revisions 2–4: try each candidate as the user password, then as the
	// owner password (Algorithm 7 recovers the user password from /O). If none
	// validates against /U, the password is wrong.
	for _, padded := range r4Candidates(password) {
		h.DeriveKeyR234(padded, p.o, p.p, p.id)
		if h.userKeyValid(p.u, p.id) {
			return h, nil
		}
		for _, truncated := range ownerRoundVariants(h.R, h.KeyLen) {
			userPad := ownerUserPassword(padded, p.o, h.R, h.KeyLen, truncated)
			h.DeriveKeyR234(userPad, p.o, p.p, p.id)
			if h.userKeyValid(p.u, p.id) {
				return h, nil
			}
		}
	}
	h.FileKey = nil
	return nil, wrongPassword(password)
}

// ownerRoundVariants lists the readings of Algorithm 3 step c to try, as
// values of ownerUserPassword's truncatedRounds. The text of ISO 32000-2 (and
// of the PDF 1.7 reference before it) re-hashes the whole 16-byte digest in
// each of the 50 rounds; poppler, MuPDF and qpdf re-hash only the first keyLen
// bytes, mirroring Algorithm 2 step h — measured, not assumed: poppler 24.02
// and MuPDF 1.26 accept an owner password only in that form. The two agree for
// 128-bit keys, so the difference exists only below 16 bytes, where the
// readers' form is tried first and the spec's literal form second. Either must
// still reproduce /U, so the second cannot open anything the owner password
// does not.
func ownerRoundVariants(r, keyLen int) []bool {
	if r >= 3 && keyLen < 16 {
		return []bool{true, false}
	}
	return []bool{false}
}

func wrongPassword(password string) error {
	if password == "" {
		return fmt.Errorf("%w: the file has a user password and none was supplied", ErrWrongPassword)
	}
	return ErrWrongPassword
}

// DeriveKeyR234 computes the file encryption key from the padded password for
// revisions 2–4 (ISO 32000-1 Algorithm 2). h.KeyLen must be 5–16, which
// parseParams guarantees for every handler Open builds.
func (h *Handler) DeriveKeyR234(paddedPw, o []byte, p int32, id []byte) {
	sum := md5.New()
	sum.Write(paddedPw)
	sum.Write(o)
	var pb [4]byte
	binary.LittleEndian.PutUint32(pb[:], uint32(p))
	sum.Write(pb[:])
	sum.Write(id)
	if h.R >= 4 && !h.EncryptMetadata {
		sum.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := sum.Sum(nil)
	if h.R >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:h.KeyLen])
			key = s[:]
		}
	}
	h.FileKey = append([]byte(nil), key[:h.KeyLen]...)
}

// userKeyValid checks that the current file key matches /U, i.e. the password
// used to derive it is the correct user password (ISO 32000-1 Algorithm 4 for
// R2, Algorithm 6 for R3–4). u is the validated 32-byte /U.
func (h *Handler) userKeyValid(u, id []byte) bool {
	if h.R == 2 {
		c, err := rc4.NewCipher(h.FileKey)
		if err != nil {
			return false
		}
		out := make([]byte, 32)
		c.XORKeyStream(out, PasswordPad)
		return bytes.Equal(out, u[:32])
	}
	sum := md5.New()
	sum.Write(PasswordPad)
	sum.Write(id)
	val := sum.Sum(nil) // 16 bytes
	for i := 0; i <= 19; i++ {
		key := make([]byte, len(h.FileKey))
		for j := range key {
			key[j] = h.FileKey[j] ^ byte(i)
		}
		c, err := rc4.NewCipher(key)
		if err != nil {
			return false
		}
		c.XORKeyStream(val, val)
	}
	// Only the first 16 bytes of /U are the checkable value (the rest is
	// arbitrary padding under R3–4).
	return bytes.Equal(val, u[:16])
}

// ownerUserPassword recovers the padded user password from /O given the padded
// owner password (ISO 32000-1 Algorithm 7). keyLen is 5–16; truncatedRounds
// selects the variant of Algorithm 3 step c described at ownerRoundVariants.
func ownerUserPassword(paddedOwnerPw, o []byte, r, keyLen int, truncatedRounds bool) []byte {
	sum := md5.New()
	sum.Write(paddedOwnerPw)
	key := sum.Sum(nil)
	if r >= 3 {
		for i := 0; i < 50; i++ {
			in := key
			if truncatedRounds {
				in = key[:keyLen]
			}
			s := md5.Sum(in)
			key = s[:]
		}
	}
	ownerKey := key[:keyLen]

	userPad := append([]byte(nil), o...)
	if r == 2 {
		c, err := rc4.NewCipher(ownerKey)
		if err != nil {
			return userPad
		}
		c.XORKeyStream(userPad, userPad)
		return userPad
	}
	for i := 19; i >= 0; i-- {
		k := make([]byte, keyLen)
		for j := range k {
			k[j] = ownerKey[j] ^ byte(i)
		}
		c, err := rc4.NewCipher(k)
		if err != nil {
			return userPad
		}
		c.XORKeyStream(userPad, userPad)
	}
	return userPad
}

// deriveKeyR6 recovers the AES-256 file key for a prepared password
// (ISO 32000-2 Algorithm 2.A). It tries the user entry, then the owner entry,
// validating the password against the stored hash before decrypting the
// corresponding /UE or /OE. u and o are the validated 48-byte entries, ue and
// oe the validated 32-byte ones. Returns false if neither validates.
func (h *Handler) deriveKeyR6(pw, u, ue, o, oe []byte) bool {
	zeroIV := make([]byte, aes.BlockSize)
	if validationSalt, keySalt := u[32:40], u[40:48]; bytes.Equal(hash2B(pw, validationSalt, nil), u[:32]) {
		h.FileKey = aesCBCNoPadDecrypt(hash2B(pw, keySalt, nil), zeroIV, ue)
		return h.FileKey != nil
	}
	if validationSalt, keySalt := o[32:40], o[40:48]; bytes.Equal(hash2B(pw, validationSalt, u), o[:32]) {
		h.FileKey = aesCBCNoPadDecrypt(hash2B(pw, keySalt, u), zeroIV, oe)
		return h.FileKey != nil
	}
	return false
}

// validatePerms is ISO 32000-2 Algorithm 13. /UE and /OE are not authenticated
// — a password that matches /U decrypts whatever /UE holds — so /Perms, which
// is encrypted under the file key and carries the fixed marker "adb", is the
// only proof that the recovered key is the file's key. Without the marker the
// key is unproven, and decrypting with it would turn every string and stream
// into noise: the document stays Locked. A readable /Perms whose permissions
// or EncryptMetadata flag differ from the dictionary ("should match") is a
// warning (ErrPermsMismatch): the key is proven and the content decrypts, but
// /P is not authentic. Decryption keeps following /EncryptMetadata, the entry
// ISO 32000-2 defines as governing the metadata stream.
func (h *Handler) validatePerms(perms []byte, p int32) error {
	block, err := aes.NewCipher(h.FileKey)
	if err != nil {
		return malformed("the file key recovered from /UE or /OE is unusable: %v", err)
	}
	var b [16]byte
	block.Decrypt(b[:], perms) // ECB: one block
	if string(b[9:12]) != "adb" {
		h.FileKey = nil
		return malformed("/Perms does not decrypt under the file key recovered from /UE or /OE (ISO 32000-2 Algorithm 13), so the key is unproven")
	}
	if got := binary.LittleEndian.Uint32(b[0:4]); got != uint32(p) {
		h.Warnings = append(h.Warnings, fmt.Errorf("%w: /Perms grants permissions %#08x, /P says %#08x", ErrPermsMismatch, got, uint32(p)))
	}
	want := byte('F')
	if h.EncryptMetadata {
		want = 'T'
	}
	if b[8] != want {
		h.Warnings = append(h.Warnings, fmt.Errorf("%w: /Perms byte 8 is %q, /EncryptMetadata is %v", ErrPermsMismatch, b[8], h.EncryptMetadata))
	}
	return nil
}

// hash2B is the R6 password hash (ISO 32000-2 Algorithm 2.B). It seeds with
// SHA-256 and then iterates an AES-128 round whose output selects SHA-256/384/512
// for the next round, stopping once at least 64 rounds have run and the last
// output byte is small enough.
func hash2B(password, salt, udata []byte) []byte {
	first := sha256.New()
	first.Write(password)
	first.Write(salt)
	first.Write(udata)
	k := first.Sum(nil)

	for round := 1; ; round++ {
		// K1 = (password || K || udata) repeated 64 times. 64 repetitions of
		// anything is a whole number of AES blocks.
		seq := make([]byte, 0, len(password)+len(k)+len(udata))
		seq = append(seq, password...)
		seq = append(seq, k...)
		seq = append(seq, udata...)
		k1 := bytes.Repeat(seq, 64)

		// E = AES-128-CBC-Encrypt(K1) with key K[0:16], IV K[16:32].
		block, err := aes.NewCipher(k[:16])
		if err != nil {
			return nil
		}
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1)

		// The first 16 bytes of E as a big-endian integer, mod 3, selects the
		// digest. Since 256 ≡ 1 (mod 3), that equals the byte sum mod 3.
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		case 2:
			s := sha512.Sum512(e)
			k = s[:]
		}

		if round >= 64 && int(e[len(e)-1]) <= round-32 {
			break
		}
	}
	return k[:32]
}

// aesCBCNoPadDecrypt decrypts with AES-CBC and no padding removal (used for the
// fixed-length /UE, /OE key blobs).
func aesCBCNoPadDecrypt(key, iv, data []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil || len(data)%aes.BlockSize != 0 {
		return nil
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	return out
}

// ObjectKey derives the per-object key (ISO 32000-1 Algorithm 1) for RC4 and
// AES-128; AES-256 uses the file key directly.
func (h *Handler) ObjectKey(num, gen int, aesv2 bool) []byte {
	sum := md5.New()
	sum.Write(h.FileKey)
	sum.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16)})
	sum.Write([]byte{byte(gen), byte(gen >> 8)})
	if aesv2 {
		sum.Write([]byte{0x73, 0x41, 0x6C, 0x54}) // "sAlT"
	}
	full := sum.Sum(nil)
	n := h.KeyLen + 5
	if n > 16 {
		n = 16
	}
	return full[:n]
}

// Decrypt returns the plaintext of data encrypted for object (num, gen) under
// the given method. Identity returns data unchanged.
//
// Data that does not decrypt — an AES blob that is short, unaligned, or whose
// PKCS#7 padding does not validate, or a method this handler cannot apply —
// yields nil, and the object number is recorded on the handler for
// DecryptDocument to hand to the document. Returning the ciphertext unchanged
// is the one answer that cannot be right: the file key is known good by then (a
// wrong password never reaches here — Open returns no handler and the document
// reports Locked), so the failure means the blob is corrupt or was never
// encrypted, and either way the bytes are not the plaintext. Handing them on
// dressed as plaintext puts high-entropy noise into a string or a stream body,
// where it reads as a /Title, a content stream, an XMP packet or a font program.
// nil is at least honestly empty, and the recorded failure makes Write refuse
// rather than re-encrypt the blank.
func (h *Handler) Decrypt(data []byte, num, gen int, m method) []byte {
	switch m {
	case None:
		return data
	case RC4:
		c, err := rc4.NewCipher(h.ObjectKey(num, gen, false))
		if err != nil {
			break
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	case AESV2:
		if out, err := AESCBCDecrypt(h.ObjectKey(num, gen, true), data); err == nil {
			return out
		}
	case AESV3:
		if out, err := AESCBCDecrypt(h.FileKey, data); err == nil {
			return out
		}
	}
	h.noteDecryptFailure(num)
	return nil
}

// AESCBCDecrypt decrypts an AES-CBC blob whose first 16 bytes are the IV and
// strips PKCS#7 padding.
func AESCBCDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) < aes.BlockSize {
		return nil, errors.New("AES data shorter than the IV")
	}
	iv, ct := data[:aes.BlockSize], data[aes.BlockSize:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("AES ciphertext is not block-aligned")
	}
	out := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ct)
	// Strip and validate the PKCS#7 padding. The final byte gives the pad length,
	// and every padding byte must equal it; trusting the length byte alone (the
	// previous behaviour) would mis-truncate crafted or corrupt ciphertext to a
	// wrong, silently-accepted plaintext (audit C37).
	n := len(out)
	if n == 0 {
		return nil, errors.New("AES plaintext is empty")
	}
	pad := int(out[n-1])
	if pad < 1 || pad > aes.BlockSize || pad > n {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, b := range out[n-pad:] {
		if int(b) != pad {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return out[:n-pad], nil
}

// Encrypt is the inverse of Decrypt: it enciphers plaintext for object
// (num, gen) under the given method. It fails rather than return the plaintext
// when it cannot encipher: an encrypted file with a plaintext object in it is
// the one output that must never be produced.
func (h *Handler) Encrypt(data []byte, num, gen int, m method) ([]byte, error) {
	switch m {
	case None:
		return data, nil
	case RC4:
		c, err := rc4.NewCipher(h.ObjectKey(num, gen, false))
		if err != nil {
			return nil, fmt.Errorf("object %d: %w", num, err)
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out, nil
	case AESV2:
		out, err := AESCBCEncrypt(h.ObjectKey(num, gen, true), data)
		if err != nil {
			return nil, fmt.Errorf("object %d: %w", num, err)
		}
		return out, nil
	case AESV3:
		out, err := AESCBCEncrypt(h.FileKey, data)
		if err != nil {
			return nil, fmt.Errorf("object %d: %w", num, err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("object %d: no crypt filter this handler can apply", num)
}

// AESCBCEncrypt encrypts with AES-CBC, prepending a random IV and applying
// PKCS#7 padding — the format AESCBCDecrypt expects.
func AESCBCEncrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	pad := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(append([]byte(nil), data...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, aes.BlockSize+len(padded))
	if _, err := rand.Read(out[:aes.BlockSize]); err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(block, out[:aes.BlockSize]).CryptBlocks(out[aes.BlockSize:], padded)
	return out, nil
}

// StreamContext is what choosing a stream's crypt filter needs to know about
// the whole document: which streams are embedded files (governed by /EFF) and
// which is the document-level metadata stream (exempt when /EncryptMetadata is
// false). Neither can be decided from a stream alone — a file specification
// that names an embedded file may sit inside an object stream — so it is
// computed over the complete object graph, on the read side after object
// streams are materialized and on the write side from the document model.
type StreamContext struct {
	embedded map[int]bool
	rootMeta int          // object number of the catalog's /Metadata stream, 0 if none
	exempt   map[int]bool // see encryptValueObjects
}

// StreamContext computes the context for doc. The graph walk that finds
// embedded files runs only when /EFF selects a different method from /StmF;
// otherwise the answer cannot change any stream's method.
func (h *Handler) StreamContext(doc core.View) StreamContext {
	sc := StreamContext{exempt: encryptValueObjects(doc)}
	if !h.EncryptMetadata {
		if cat := doc.ResolveDict(doc.Trailer.Get("Root")); cat != nil {
			if ref, ok := cat.Get("Metadata").(object.IndirectRef); ok {
				sc.rootMeta = ref.Number
			}
		}
	}
	if h.EFFMethod != h.StmMethod {
		sc.embedded = embeddedFileStreams(doc)
	}
	return sc
}

// encryptValueObjects returns the objects that hold the /Encrypt dictionary
// and the values it references indirectly — directly from the dictionary, or
// from its /CF dictionary and the crypt filter dictionaries in it. ISO 32000-2
// 7.6.2: "The values of the keys defined in Table 20 shall not be encrypted",
// and the spec requires them to be direct objects; when a producer makes one
// indirect anyway, its object must be left alone on both sides — decrypting an
// indirect /O would destroy the key material, and encrypting it on write would
// produce a file no reader can open. Computed from the document as it stands,
// so a dictionary edited after Read (or built by SetEncryption and then
// restructured) is described correctly on write.
func encryptValueObjects(doc core.View) map[int]bool {
	out := map[int]bool{}
	encObj := doc.Trailer.Get("Encrypt")
	markRefs := func(d *object.Dictionary) {
		for v := range d.Values() {
			if ref, ok := v.(object.IndirectRef); ok {
				out[ref.Number] = true
			}
		}
	}
	if ref, ok := encObj.(object.IndirectRef); ok {
		out[ref.Number] = true
	}
	enc := doc.ResolveDict(encObj)
	if enc == nil {
		return out
	}
	markRefs(enc)
	if cf := doc.ResolveDict(enc.Get("CF")); cf != nil {
		markRefs(cf)
		for v := range cf.Values() {
			if fd := doc.ResolveDict(v); fd != nil {
				markRefs(fd)
			}
		}
	}
	return out
}

// embeddedFileStreams returns the object numbers of every embedded file stream:
// each stream a file specification's /EF or /RF references (ISO 32000-2 Table
// 43, 7.11.4.2), and each stream typed /EmbeddedFile.
func embeddedFileStreams(doc core.View) map[int]bool {
	out := map[int]bool{}
	mark := func(o object.Object) {
		if ref, ok := o.(object.IndirectRef); ok {
			out[ref.Number] = true
		}
	}
	var walk func(o object.Object)
	walkDict := func(d *object.Dictionary) {
		if ef := doc.ResolveDict(d.Get("EF")); ef != nil {
			for v := range ef.Values() {
				mark(v)
			}
		}
		if rf := doc.ResolveDict(d.Get("RF")); rf != nil {
			for v := range rf.Values() {
				if arr, ok := doc.Resolve(v).(object.Array); ok {
					for i := 1; i < len(arr); i += 2 {
						mark(arr[i]) // [name1 stream1 name2 stream2 …]
					}
				}
			}
		}
		for v := range d.Values() {
			walk(v)
		}
	}
	walk = func(o object.Object) {
		switch v := o.(type) {
		case *object.Dictionary:
			walkDict(v)
		case object.Array:
			for _, e := range v {
				walk(e)
			}
		}
	}
	for num, iobj := range doc.Objects {
		switch v := iobj.Value.(type) {
		case *object.Stream:
			if t, _ := v.Dict.Get("Type").(object.Name); t == "EmbeddedFile" {
				out[num] = true
			}
			walkDict(&v.Dict)
		default:
			walk(v)
		}
	}
	return out
}

// streamMethod chooses the crypt filter for a stream (ISO 32000-2 7.6.6):
// cross-reference streams are never encrypted; a stream that names its own
// crypt filter with a /Crypt entry in /Filter uses that one (Identity by
// default); the document-level metadata stream is left in the clear when
// /EncryptMetadata is false; an embedded file stream uses /EFF; everything
// else uses /StmF. ok is false when the chosen filter is one the handler
// cannot apply, so the stream's data is not what it appears to be.
func (h *Handler) streamMethod(num int, s *object.Stream, sc StreamContext) (m method, ok bool) {
	if t, _ := s.Dict.Get("Type").(object.Name); t == "XRef" {
		return None, true
	}
	if name, pos, has := cryptFilter(s); has {
		// /Crypt must be the first filter (7.4.10): data is decrypted before
		// any other decoding. Elsewhere in the chain it would have to be
		// applied mid-decode, which the model — decrypted bytes in
		// Stream.Data — cannot represent.
		if pos != 0 {
			return invalid, false
		}
		if name == "Identity" {
			return None, true
		}
		m, found := h.filters[name]
		return m, found && m != invalid
	}
	if !h.EncryptMetadata && num != 0 && num == sc.rootMeta {
		return None, true
	}
	if sc.embedded[num] {
		return h.EFFMethod, h.EFFMethod != invalid
	}
	return h.StmMethod, h.StmMethod != invalid
}

// cryptFilter finds a /Crypt entry in a stream's /Filter and the crypt filter
// name its decode parameters give (Table 14: /Name, default Identity).
func cryptFilter(s *object.Stream) (name object.Name, pos int, found bool) {
	var filters object.Array
	switch f := s.Dict.Get("Filter").(type) {
	case object.Name:
		filters = object.Array{f}
	case object.Array:
		filters = f
	default:
		return "", 0, false
	}
	for i, f := range filters {
		if n, _ := f.(object.Name); n != "Crypt" {
			continue
		}
		var parms *object.Dictionary
		switch p := s.Dict.Get("DecodeParms").(type) {
		case *object.Dictionary:
			if i == 0 {
				parms = p
			}
		case object.Array:
			if i < len(p) {
				parms, _ = p[i].(*object.Dictionary)
			}
		}
		name = "Identity"
		if parms != nil {
			if n, ok := parms.Get("Name").(object.Name); ok {
				name = n
			}
		}
		return name, i, true
	}
	return "", 0, false
}

// EncryptCopy returns encrypted copies of the given objects, leaving the
// originals (the in-memory plaintext) untouched. The /Encrypt dictionary and
// the values it references are passed through unencrypted. A stream whose
// data grows (AES padding) gets its direct /Length updated; an indirect
// /Length is handled by the caller. sc must describe the document model the
// objects were taken from (see StreamContext).
func (h *Handler) EncryptCopy(objects map[int]*object.IndirectObject, sc StreamContext) (map[int]*object.IndirectObject, error) {
	out := make(map[int]*object.IndirectObject, len(objects))
	for num, iobj := range objects {
		if num == h.EncryptObjNum || sc.exempt[num] {
			out[num] = iobj
			continue
		}
		v, err := h.encryptObj(iobj.Value, iobj.Number, iobj.Generation, sc)
		if err != nil {
			return nil, err
		}
		out[num] = &object.IndirectObject{Number: iobj.Number, Generation: iobj.Generation, Value: v}
	}
	return out, nil
}

func (h *Handler) encryptObj(o object.Object, num, gen int, sc StreamContext) (object.Object, error) {
	switch v := o.(type) {
	case object.String:
		if h.StrMethod == None {
			return v, nil
		}
		b, err := h.Encrypt(v.Value, num, gen, h.StrMethod)
		if err != nil {
			return nil, err
		}
		return object.String{Value: b, IsHex: v.IsHex}, nil
	case object.Array:
		cp := make(object.Array, len(v))
		for i := range v {
			e, err := h.encryptObj(v[i], num, gen, sc)
			if err != nil {
				return nil, err
			}
			cp[i] = e
		}
		return cp, nil
	case *object.Dictionary:
		return h.encryptDictCopy(v, num, gen, sc)
	case *object.Stream:
		d, err := h.encryptDictCopy(&v.Dict, num, gen, sc)
		if err != nil {
			return nil, err
		}
		m, ok := h.streamMethod(num, v, sc)
		if !ok {
			return nil, fmt.Errorf("object %d: the stream names a crypt filter the security handler cannot apply", num)
		}
		data := v.Data
		if m != None {
			if data, err = h.Encrypt(v.Data, num, gen, m); err != nil {
				return nil, err
			}
			if _, isRef := d.Get("Length").(object.IndirectRef); !isRef {
				d.Set("Length", object.Integer(len(data)))
			}
		}
		return object.NewStream(d, data), nil
	}
	return o, nil
}

func (h *Handler) encryptDictCopy(d *object.Dictionary, num, gen int, sc StreamContext) (*object.Dictionary, error) {
	cp := &object.Dictionary{}
	sig := IsSignatureDict(d)
	for key, val := range d.All() {
		if sig && key == "Contents" {
			cp.Set(key, val) // the signature value is never encrypted (7.6.2)
			continue
		}
		e, err := h.encryptObj(val, num, gen, sc)
		if err != nil {
			return nil, err
		}
		cp.Set(key, e)
	}
	return cp, nil
}

// IsSignatureDict reports whether d is a signature (or document time-stamp)
// dictionary holding a signature value in a direct /Contents string.
//
// ISO 32000-2, 7.6.2 lists "any hexadecimal strings representing the value of
// the Contents key in a Signature dictionary" among the values encryption does
// not apply to, alongside the trailer /ID and the /Encrypt dictionary's strings.
// The exemption exists because the signature is computed over the file's own
// bytes — everything outside the /ByteRange gap that /Contents sits in — so
// enciphering the signature value would be circular: a verifier reads the bytes
// that are in the file, and those bytes must be the CMS blob itself.
//
// The test deliberately matches the one VerifySignatures uses to find
// signatures (/ByteRange and /Contents present, /Type absent or /Sig or
// /DocTimeStamp). The two must agree: if the crypt layer transformed a value
// that verification then treats as a signature, verification would be run
// against something the file does not contain. /Contents must be a direct
// string — 7.6.2's exemption is about the string itself, and Table 255 requires
// a (hexadecimal) string value whenever /ByteRange is present. IsHex is not
// required: a producer writing the value as a literal string still means it as
// the signature value, and leniency here only preserves bytes.
func IsSignatureDict(d *object.Dictionary) bool {
	if d.Get("ByteRange") == nil {
		return false
	}
	if _, ok := d.Get("Contents").(object.String); !ok {
		return false
	}
	if t, _ := d.Get("Type").(object.Name); t != "" && t != "Sig" && t != "DocTimeStamp" {
		return false
	}
	return true
}

// Pending is a decryption in progress: strings and object-stream containers
// are done, the remaining streams are not. See DecryptDocument.
type Pending struct {
	h       *Handler
	streams []pendingStream
}

type pendingStream struct {
	num, gen int
	s        *object.Stream
}

// DecryptDocument decrypts every string in the loaded (top-level) objects in
// place, and the body of every object-stream container, and returns the rest
// of the streams as Pending. It must run before object-stream contents are
// materialised — an /ObjStm container is itself an encrypted stream, while the
// objects inside it are not separately encrypted — and Pending.Finish must run
// after: which crypt filter a stream uses can depend on the whole graph (an
// embedded file is named by a file specification that may live inside an
// object stream), so those streams wait until the graph is complete.
//
// containers reports whether an object is an object-stream container; the
// caller knows this from the cross-reference section, which a container's
// dictionary alone need not reveal.
//
// ISO 32000-2, 7.6.2 exempts four things from encryption; each is honoured here.
// The trailer's /ID values are safe by construction: the walk covers only
// doc.Objects, and the trailer is not one of them — with a cross-reference
// stream the trailer IS an object, but a /Type /XRef stream is skipped whole.
// Strings inside an encrypted stream are covered by the stream's own
// decryption and are never visited separately. The /Encrypt dictionary's
// values and a signature's /Contents are skipped explicitly (see below and
// DecryptDictStrings).
func (h *Handler) DecryptDocument(doc core.View, containers func(num int, s *object.Stream) bool) *Pending {
	// The /Encrypt dictionary's own strings (/O, /U, /Perms, …) are never
	// encrypted and must not be decrypted. Skipping by object number alone is
	// not enough: a malformed file can point several xref entries at the
	// /Encrypt dictionary's byte offset, and Read shares one parsed value across
	// those object numbers (bounding re-parse work — the duplicate-offset
	// guard). Only one of those numbers is h.EncryptObjNum, so decrypting an
	// alias would mutate the shared /Encrypt dictionary in place and corrupt the
	// key material (AES padding strips /O and /U from 32 to 16 bytes), leaving
	// the rewritten file undecryptable. Skip the dictionary by pointer identity.
	encryptDict := doc.ResolveDict(doc.Trailer.Get("Encrypt"))
	// A parsed value shared by several object numbers (duplicate xref offsets)
	// must be decrypted at most once: decryption mutates streams and
	// dictionaries in place, so visiting the same value under a second number
	// would double-decrypt and corrupt it. seen tracks the mutable reference
	// values already processed; it never matches in a well-formed file, where
	// every object is a distinct value, so behaviour there is unchanged.
	seen := map[any]bool{}
	p := &Pending{h: h}
	nums := make([]int, 0, len(doc.Objects))
	for num := range doc.Objects {
		nums = append(nums, num)
	}
	slices.Sort(nums) // deterministic: the first number an alias is met under wins
	for _, num := range nums {
		iobj := doc.Objects[num]
		if num == h.EncryptObjNum || h.exempt[num] {
			continue // the /Encrypt dictionary's values are not encrypted
		}
		if d, ok := iobj.Value.(*object.Dictionary); ok && d == encryptDict {
			continue // an alias of the /Encrypt dictionary at a shared offset
		}
		switch iobj.Value.(type) {
		case *object.Stream, *object.Dictionary:
			if seen[iobj.Value] {
				continue
			}
			seen[iobj.Value] = true
		}
		gen := iobj.Generation
		switch v := iobj.Value.(type) {
		case *object.Stream:
			// Cross-reference streams are never encrypted, dictionary included.
			if t, _ := v.Dict.Get("Type").(object.Name); t == "XRef" {
				continue
			}
			h.DecryptDictStrings(&v.Dict, num, gen)
			if containers(num, v) {
				h.decryptStream(num, gen, v, StreamContext{})
			} else {
				p.streams = append(p.streams, pendingStream{num, gen, v})
			}
		case *object.Dictionary:
			h.DecryptDictStrings(v, num, gen)
		case object.Array:
			h.decryptArrayStrings(v, num, gen)
		case object.String:
			iobj.Value = h.decryptStringValue(v, num, gen)
			doc.Objects[num] = iobj
		}
	}
	return p
}

// Finish decrypts the streams DecryptDocument deferred, now that doc holds the
// complete object graph, and returns the object numbers whose content could not
// be decrypted — corrupt ciphertext, or a crypt filter the handler cannot
// apply. Their strings and stream bodies are empty rather than noise.
func (p *Pending) Finish(doc core.View) (failed []int) {
	sc := p.h.StreamContext(doc)
	for _, ps := range p.streams {
		p.h.decryptStream(ps.num, ps.gen, ps.s, sc)
	}
	for num := range p.h.failedObjects {
		failed = append(failed, num)
	}
	slices.Sort(failed)
	return failed
}

func (h *Handler) decryptStream(num, gen int, s *object.Stream, sc StreamContext) {
	m, ok := h.streamMethod(num, s, sc)
	if !ok {
		// A crypt filter the handler cannot apply: the bytes are ciphertext
		// under a key or algorithm we do not have. Never hand them on as
		// content (see Decrypt).
		s.Data = nil
		h.noteDecryptFailure(num)
		return
	}
	s.Data = h.Decrypt(s.Data, num, gen, m)
}

func (h *Handler) DecryptDictStrings(d *object.Dictionary, num, gen int) {
	// A signature dictionary's /Contents is not encrypted (ISO 32000-2, 7.6.2;
	// see IsSignatureDict), so it must not be decrypted either. Decrypting it
	// would replace the CMS blob the file actually contains with a transform of
	// it: with RC4 that is always a different value, so every conformant
	// encrypted-and-signed file would fail verification with a misleading
	// "not a CMS SignedData"; with AES it is caught by the padding check ~99.6%
	// of the time and silently truncates the value the rest.
	//
	// This runs for nested dictionaries too, and the exemption is by key within
	// the dictionary rather than by object number, so it is unaffected by the
	// aliasing hazard the /Encrypt skip in DecryptDocument documents: whichever
	// object number a shared value is reached under, its /Contents is skipped.
	sig := IsSignatureDict(d)
	for key, val := range d.All() {
		if sig && key == "Contents" {
			continue
		}
		d.Set(key, h.decryptValue(val, num, gen)) // replacing a value mid-iteration is allowed
	}
}

func (h *Handler) decryptArrayStrings(a object.Array, num, gen int) {
	for i := range a {
		a[i] = h.decryptValue(a[i], num, gen)
	}
}

func (h *Handler) decryptValue(o object.Object, num, gen int) object.Object {
	switch v := o.(type) {
	case object.String:
		return h.decryptStringValue(v, num, gen)
	case *object.Dictionary:
		h.DecryptDictStrings(v, num, gen)
	case object.Array:
		h.decryptArrayStrings(v, num, gen)
	}
	return o
}

func (h *Handler) decryptStringValue(s object.String, num, gen int) object.String {
	if h.StrMethod == None {
		return s
	}
	return object.String{Value: h.Decrypt(s.Value, num, gen, h.StrMethod), IsHex: s.IsHex}
}

// Exempt reports whether object num holds one of the /Encrypt dictionary's own
// values, which are never encrypted.
func (h *Handler) Exempt(num int) bool {
	return num == h.EncryptObjNum || h.exempt[num]
}
