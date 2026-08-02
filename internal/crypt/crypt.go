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
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"slices"
)

// The PDF standard security handler (ISO 32000-1 §7.6, ISO 32000-2 §7.6), in
// both directions: key derivation and decryption for a user or owner password,
// and the encryption path used when a decrypted document is written back or
// SetEncryption is called. RC4 (V1/V2, R2–R4), AES-128 (V4, /AESV2), and
// AES-256 (V5, /AESV3, R6) are supported; crypt_encrypt.go builds the /Encrypt
// dictionary for a newly encrypted document.

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
)

// Handler holds a parsed /Encrypt dictionary and the derived file
// encryption key.
type Handler struct {
	V, R            int
	KeyLen          int // file key length in bytes
	FileKey         []byte
	StmMethod       method // streams
	StrMethod       method // strings
	EncryptMetadata bool
	EncryptObjNum   int // object number of the /Encrypt dict, or -1 if inline

	// failedObjects collects the object numbers whose AES ciphertext this
	// handler could not Decrypt (see Decrypt). DecryptDocument returns them
	// once the walk is over.
	failedObjects map[int]bool
}

// noteDecryptFailure records that an object's ciphertext did not Decrypt.
func (h *Handler) noteDecryptFailure(num int) {
	if h.failedObjects == nil {
		h.failedObjects = map[int]bool{}
	}
	h.failedObjects[num] = true
}

// Open parses the trailer's /Encrypt dictionary and derives
// the file key for the given password (empty for the common case), trying it as
// both the user and owner password. It returns (nil, nil) when the password is
// wrong or the scheme is unsupported, so the caller leaves the document
// encrypted; an error signals malformed encryption metadata.
func Open(doc core.View, password string) (*Handler, error) {
	encObj := doc.Trailer.Get("Encrypt")
	if encObj == nil {
		return nil, nil
	}
	encNum := -1
	if ref, ok := encObj.(object.IndirectRef); ok {
		encNum = ref.Number
	}
	enc := doc.ResolveDict(encObj)
	if enc == nil {
		return nil, nil // unresolvable /Encrypt — leave the document encrypted
	}
	if f, _ := doc.Resolve(enc.Get("Filter")).(object.Name); f != "Standard" {
		return nil, nil // only the standard security handler
	}
	v := encInt(doc, enc.Get("V"))
	r := encInt(doc, enc.Get("R"))

	h := &Handler{V: v, R: r, EncryptObjNum: encNum, EncryptMetadata: true}
	if em, ok := doc.Resolve(enc.Get("EncryptMetadata")).(object.Boolean); ok {
		h.EncryptMetadata = bool(em)
	}
	h.resolveMethods(doc, enc)

	// V5 uses AES-256 with SHA-2 key derivation (ISO 32000-2 §7.6.4.3).
	if v == 5 {
		if r != 6 {
			return nil, nil // R5 (deprecated draft) not handled
		}
		h.KeyLen = 32
		u := resolveBytes(doc, enc.Get("U"))
		ue := resolveBytes(doc, enc.Get("UE"))
		o := resolveBytes(doc, enc.Get("O"))
		oe := resolveBytes(doc, enc.Get("OE"))
		if !h.deriveKeyR6([]byte(password), u, ue, o, oe) {
			return nil, nil // cannot derive the key — leave the document encrypted
		}
		return h, nil
	}

	// V1/V2/V4: RC4/AES-128 with MD5 key derivation (revisions 2–4).
	h.KeyLen = encInt(doc, enc.Get("Length")) / 8
	if r == 2 || h.KeyLen == 0 {
		h.KeyLen = 5 // R2 is always 40-bit; default when /Length is absent
	}
	o, _ := doc.Resolve(enc.Get("O")).(object.String)
	if len(o.Value) < 32 {
		return nil, nil // malformed /O — leave the document encrypted
	}
	p := int32(uint32(encInt(doc, enc.Get("P"))))
	var id []byte
	if idArr, ok := doc.Resolve(doc.Trailer.Get("ID")).(object.Array); ok && len(idArr) > 0 {
		if s, ok := idArr[0].(object.String); ok {
			id = s.Value
		}
	}
	u := resolveBytes(doc, enc.Get("U"))

	// Try the password as the user password, then as the owner password
	// (Algorithm 7 recovers the user password from /O). If neither validates
	// against /U, the password is wrong and the document is left encrypted.
	padded := PadPassword(password)
	h.DeriveKeyR234(padded, o.Value[:32], p, id)
	if h.userKeyValid(u, id) {
		return h, nil
	}
	userPad := ownerUserPassword(padded, o.Value[:32], h.R, h.KeyLen)
	h.DeriveKeyR234(userPad, o.Value[:32], p, id)
	if h.userKeyValid(u, id) {
		return h, nil
	}
	return nil, nil
}

// resolveMethods sets the stream and string crypt methods. Below V4 both are
// RC4; V4 selects them via the /StmF and /StrF crypt-filter names in /CF.
func (h *Handler) resolveMethods(doc core.View, enc *object.Dictionary) {
	if h.V < 4 {
		h.StmMethod, h.StrMethod = RC4, RC4
		return
	}
	cf := doc.ResolveDict(enc.Get("CF"))
	methodFor := func(name object.Name) method {
		if name == "" || name == "Identity" || cf == nil {
			return None
		}
		filt := doc.ResolveDict(cf.Get(name))
		if filt == nil {
			return None
		}
		switch cfm, _ := doc.Resolve(filt.Get("CFM")).(object.Name); cfm {
		case "V2":
			return RC4
		case "AESV2":
			return AESV2
		case "AESV3":
			return AESV3
		}
		return None
	}
	stmF, _ := doc.Resolve(enc.Get("StmF")).(object.Name)
	strF, _ := doc.Resolve(enc.Get("StrF")).(object.Name)
	h.StmMethod = methodFor(stmF)
	h.StrMethod = methodFor(strF)
}

// DeriveKeyR234 computes the file encryption key from the padded password for
// revisions 2–4 (ISO 32000-1 Algorithm 2).
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

// PadPassword pads (or truncates) a password to the 32-byte field used by
// revisions 2–4 (ISO 32000-1 Algorithm 2, step a).
func PadPassword(password string) []byte {
	out := make([]byte, 32)
	n := copy(out, password)
	copy(out[n:], PasswordPad)
	return out
}

// userKeyValid checks that the current file key matches /U, i.e. the password
// used to derive it is the correct user password (ISO 32000-1 Algorithm 4 for
// R2, Algorithm 6 for R3–4).
func (h *Handler) userKeyValid(u, id []byte) bool {
	if len(u) < 16 {
		return false
	}
	if h.R == 2 {
		c, err := rc4.NewCipher(h.FileKey)
		if err != nil {
			return false
		}
		out := make([]byte, 32)
		c.XORKeyStream(out, PasswordPad)
		return len(u) >= 32 && bytes.Equal(out, u[:32])
	}
	sum := md5.New()
	sum.Write(PasswordPad)
	sum.Write(id)
	val := sum.Sum(nil) // 16 bytes
	c, err := rc4.NewCipher(h.FileKey)
	if err != nil {
		return false
	}
	c.XORKeyStream(val, val)
	for i := 1; i <= 19; i++ {
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
// owner password (ISO 32000-1 Algorithm 7).
func ownerUserPassword(paddedOwnerPw, o []byte, r, KeyLen int) []byte {
	sum := md5.New()
	sum.Write(paddedOwnerPw)
	key := sum.Sum(nil)
	if r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:KeyLen])
			key = s[:]
		}
	}
	ownerKey := key[:KeyLen]

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
		k := make([]byte, KeyLen)
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

// deriveKeyR6 recovers the AES-256 file key for the given password
// (ISO 32000-2 Algorithm 2.A). It tries the user entry, then the owner entry,
// validating the password against the stored hash before decrypting the
// corresponding /UE or /OE. Returns false if neither validates.
func (h *Handler) deriveKeyR6(pw, u, ue, o, oe []byte) bool {
	if len(u) >= 48 && len(ue) >= 32 {
		validationSalt, keySalt := u[32:40], u[40:48]
		if bytes.Equal(hash2B(pw, validationSalt, nil), u[:32]) {
			ik := hash2B(pw, keySalt, nil)
			h.FileKey = aesCBCNoPadDecrypt(ik, make([]byte, 16), ue[:32])
			return h.FileKey != nil
		}
	}
	if len(o) >= 48 && len(oe) >= 32 && len(u) >= 48 {
		validationSalt, keySalt := o[32:40], o[40:48]
		if bytes.Equal(hash2B(pw, validationSalt, u[:48]), o[:32]) {
			ik := hash2B(pw, keySalt, u[:48])
			h.FileKey = aesCBCNoPadDecrypt(ik, make([]byte, 16), oe[:32])
			return h.FileKey != nil
		}
	}
	return false
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
		// K1 = (password || K || udata) repeated 64 times.
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

// resolveBytes resolves an object to a string's bytes, or nil.
func resolveBytes(doc core.View, o object.Object) []byte {
	if s, ok := doc.Resolve(o).(object.String); ok {
		return s.Value
	}
	return nil
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
// the given method. Unrecognised or Identity methods return data unchanged.
//
// An AES blob that does not Decrypt — a short or unaligned blob, or one whose
// PKCS#7 padding does not validate — yields nil, and the object number is
// recorded on the handler for DecryptDocument to hand to the document.
// Returning the ciphertext unchanged, as this did, is the one answer that
// cannot be right: the file key is known good by then (a wrong password never
// reaches here — Open returns no handler and the document
// reports Locked), so the failure means the blob is corrupt or was never
// encrypted, and either way the bytes are not the plaintext. Handing them on
// dressed as plaintext puts high-entropy noise into a string or a stream body,
// where it reads as a /Title, a content stream, an XMP packet or a font
// program — exactly the "silently wrong" class the package refuses to produce,
// and the shape in which a caller ends up validating noise. nil is at least
// honestly empty, and the recorded failure makes Write refuse rather than
// re-Encrypt the blank.
func (h *Handler) Decrypt(data []byte, num, gen int, method method) []byte {
	switch method {
	case RC4:
		c, err := rc4.NewCipher(h.ObjectKey(num, gen, false))
		if err != nil {
			return data
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	case AESV2:
		out, err := AESCBCDecrypt(h.ObjectKey(num, gen, true), data)
		if err != nil {
			h.noteDecryptFailure(num)
			return nil
		}
		return out
	case AESV3:
		out, err := AESCBCDecrypt(h.FileKey, data)
		if err != nil {
			h.noteDecryptFailure(num)
			return nil
		}
		return out
	}
	return data
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
// (num, gen) under the given method.
func (h *Handler) Encrypt(data []byte, num, gen int, method method) []byte {
	switch method {
	case RC4:
		c, err := rc4.NewCipher(h.ObjectKey(num, gen, false))
		if err != nil {
			return data
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	case AESV2:
		if out, err := AESCBCEncrypt(h.ObjectKey(num, gen, true), data); err == nil {
			return out
		}
	case AESV3:
		if out, err := AESCBCEncrypt(h.FileKey, data); err == nil {
			return out
		}
	}
	return data
}

// AESCBCEncrypt encrypts with AES-CBC, prepending a random IV and applying
// PKCS#7 padding â the format AESCBCDecrypt expects.
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

// EncryptCopy returns encrypted copies of the given objects, leaving the
// originals (the in-memory plaintext) untouched. The /Encrypt dictionary is
// passed through unencrypted. A stream whose data grows (AES padding) gets its
// direct /Length updated; an indirect /Length is handled by the caller.
func (h *Handler) EncryptCopy(objects map[int]*object.IndirectObject) map[int]*object.IndirectObject {
	out := make(map[int]*object.IndirectObject, len(objects))
	for num, iobj := range objects {
		if num == h.EncryptObjNum {
			out[num] = iobj
			continue
		}
		out[num] = &object.IndirectObject{
			Number:     iobj.Number,
			Generation: iobj.Generation,
			Value:      h.encryptObj(iobj.Value, iobj.Number, iobj.Generation),
		}
	}
	return out
}

func (h *Handler) encryptObj(o object.Object, num, gen int) object.Object {
	switch v := o.(type) {
	case object.String:
		if h.StrMethod == None {
			return v
		}
		return object.String{Value: h.Encrypt(v.Value, num, gen, h.StrMethod), IsHex: v.IsHex}
	case object.Array:
		cp := make(object.Array, len(v))
		for i := range v {
			cp[i] = h.encryptObj(v[i], num, gen)
		}
		return cp
	case *object.Dictionary:
		return h.encryptDictCopy(v, num, gen)
	case *object.Stream:
		d := h.encryptDictCopy(&v.Dict, num, gen)
		data := v.Data
		skip := h.StmMethod == None
		if t, _ := v.Dict.Get("Type").(object.Name); t == "XRef" || (!h.EncryptMetadata && t == "Metadata") {
			skip = true
		}
		if !skip {
			data = h.Encrypt(v.Data, num, gen, h.StmMethod)
			if _, isRef := d.Get("Length").(object.IndirectRef); !isRef {
				d.Set("Length", object.Integer(len(data)))
			}
		}
		return &object.Stream{Dict: *d, Data: data}
	}
	return o
}

func (h *Handler) encryptDictCopy(d *object.Dictionary, num, gen int) *object.Dictionary {
	cp := &object.Dictionary{
		Keys:   append([]object.Name(nil), d.Keys...),
		Values: make([]object.Object, len(d.Values)),
	}
	sig := IsSignatureDict(d)
	for i, val := range d.Values {
		if sig && d.Keys[i] == "Contents" {
			cp.Values[i] = val // the signature value is never encrypted (7.6.2)
			continue
		}
		cp.Values[i] = h.encryptObj(val, num, gen)
	}
	return cp
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

// DecryptDocument decrypts every string and stream in the loaded (top-level)
// objects in place. It must run before object-stream contents are materialised:
// an /ObjStm container is itself an encrypted stream, while the objects inside
// it are not separately encrypted.
//
// ISO 32000-2, 7.6.2 exempts four things from encryption; each is honoured here.
// The trailer's /ID values are safe by construction: the walk covers only
// doc.Objects, and the trailer is not one of them — with a cross-reference
// stream the trailer IS an object, but a /Type /XRef stream is skipped whole
// below. Strings inside an encrypted stream are covered by the stream's own
// decryption and are never visited separately. The /Encrypt dictionary's strings
// and a signature's /Contents are skipped explicitly (see below and
// DecryptDictStrings).
func (h *Handler) DecryptDocument(doc core.View) (failed []int) {
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
	// must be decrypted at most once: DecryptDocument mutates streams and
	// dictionaries in place, so visiting the same value under a second number
	// would double-Decrypt and corrupt it. seen tracks the mutable reference
	// values already processed; it never matches in a well-formed file, where
	// every object is a distinct value, so behaviour there is unchanged.
	seen := map[any]bool{}
	for num, iobj := range doc.Objects {
		if num == h.EncryptObjNum {
			continue // the /Encrypt dictionary's strings are not encrypted
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
			// Cross-reference streams are never encrypted.
			if t, _ := v.Dict.Get("Type").(object.Name); t == "XRef" {
				continue
			}
			h.DecryptDictStrings(&v.Dict, num, gen)
			if h.StmMethod == None {
				continue
			}
			// With EncryptMetadata false, the metadata stream stays in the clear.
			if !h.EncryptMetadata {
				if t, _ := v.Dict.Get("Type").(object.Name); t == "Metadata" {
					continue
				}
			}
			v.Data = h.Decrypt(v.Data, num, gen, h.StmMethod)
		case *object.Dictionary:
			h.DecryptDictStrings(v, num, gen)
		case object.Array:
			h.decryptArrayStrings(v, num, gen)
		case object.String:
			iobj.Value = h.decryptStringValue(v, num, gen)
			doc.Objects[num] = iobj
		}
	}

	// Objects whose ciphertext did not Decrypt are now empty rather than noise
	// (see Decrypt). They are returned rather than written through the view:
	// the caller holds the document, and a write must refuse on the strength of
	// this list rather than emit the blanks.
	for num := range h.failedObjects {
		failed = append(failed, num)
	}
	slices.Sort(failed)
	return failed
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
	for i := range d.Values {
		if sig && d.Keys[i] == "Contents" {
			continue
		}
		d.Values[i] = h.decryptValue(d.Values[i], num, gen)
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

// encInt resolves an object to an int, or 0.
func encInt(doc core.View, o object.Object) int {
	if n, ok := doc.Resolve(o).(object.Integer); ok {
		return int(n)
	}
	return 0
}
