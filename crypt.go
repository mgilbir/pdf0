package pdf0

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
	"slices"
)

// The PDF standard security handler (ISO 32000-1 §7.6, ISO 32000-2 §7.6), in
// both directions: key derivation and decryption for a user or owner password,
// and the encryption path used when a decrypted document is written back or
// SetEncryption is called. RC4 (V1/V2, R2–R4), AES-128 (V4, /AESV2), and
// AES-256 (V5, /AESV3, R6) are supported; crypt_encrypt.go builds the /Encrypt
// dictionary for a newly encrypted document.

// passwordPad is the 32-byte padding string (ISO 32000-1 §7.6.3.3, Algorithm 2,
// step a). An empty user password pads to exactly this string.
var passwordPad = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56,
	0xFF, 0xFA, 0x01, 0x08, 0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80,
	0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// cryptMethod is the algorithm a crypt filter applies.
type cryptMethod int

const (
	cryptNone  cryptMethod = iota // Identity — no encryption
	cryptRC4                      // V2
	cryptAESV2                    // AES-128-CBC
	cryptAESV3                    // AES-256-CBC (file key used directly)
)

// stdSecurityHandler holds a parsed /Encrypt dictionary and the derived file
// encryption key.
type stdSecurityHandler struct {
	v, r            int
	keyLen          int // file key length in bytes
	fileKey         []byte
	stmMethod       cryptMethod // streams
	strMethod       cryptMethod // strings
	encryptMetadata bool
	encryptObjNum   int // object number of the /Encrypt dict, or -1 if inline

	// failedObjects collects the object numbers whose AES ciphertext this
	// handler could not decrypt (see decrypt). decryptDocument drains it into
	// Document.decryptFailures once the walk is over.
	failedObjects map[int]bool
}

// noteDecryptFailure records that an object's ciphertext did not decrypt.
func (h *stdSecurityHandler) noteDecryptFailure(num int) {
	if h.failedObjects == nil {
		h.failedObjects = map[int]bool{}
	}
	h.failedObjects[num] = true
}

// buildStdSecurityHandler parses the trailer's /Encrypt dictionary and derives
// the file key for the given password (empty for the common case), trying it as
// both the user and owner password. It returns (nil, nil) when the password is
// wrong or the scheme is unsupported, so the caller leaves the document
// encrypted; an error signals malformed encryption metadata.
func buildStdSecurityHandler(doc *Document, password string) (*stdSecurityHandler, error) {
	encObj := doc.Trailer.Get("Encrypt")
	if encObj == nil {
		return nil, nil
	}
	encNum := -1
	if ref, ok := encObj.(IndirectRef); ok {
		encNum = ref.Number
	}
	enc := doc.ResolveDict(encObj)
	if enc == nil {
		return nil, nil // unresolvable /Encrypt — leave the document encrypted
	}
	if f, _ := doc.Resolve(enc.Get("Filter")).(Name); f != "Standard" {
		return nil, nil // only the standard security handler
	}
	v := encInt(doc, enc.Get("V"))
	r := encInt(doc, enc.Get("R"))

	h := &stdSecurityHandler{v: v, r: r, encryptObjNum: encNum, encryptMetadata: true}
	if em, ok := doc.Resolve(enc.Get("EncryptMetadata")).(Boolean); ok {
		h.encryptMetadata = bool(em)
	}
	h.resolveMethods(doc, enc)

	// V5 uses AES-256 with SHA-2 key derivation (ISO 32000-2 §7.6.4.3).
	if v == 5 {
		if r != 6 {
			return nil, nil // R5 (deprecated draft) not handled
		}
		h.keyLen = 32
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
	h.keyLen = encInt(doc, enc.Get("Length")) / 8
	if r == 2 || h.keyLen == 0 {
		h.keyLen = 5 // R2 is always 40-bit; default when /Length is absent
	}
	o, _ := doc.Resolve(enc.Get("O")).(String)
	if len(o.Value) < 32 {
		return nil, nil // malformed /O — leave the document encrypted
	}
	p := int32(uint32(encInt(doc, enc.Get("P"))))
	var id []byte
	if idArr, ok := doc.Resolve(doc.Trailer.Get("ID")).(Array); ok && len(idArr) > 0 {
		if s, ok := idArr[0].(String); ok {
			id = s.Value
		}
	}
	u := resolveBytes(doc, enc.Get("U"))

	// Try the password as the user password, then as the owner password
	// (Algorithm 7 recovers the user password from /O). If neither validates
	// against /U, the password is wrong and the document is left encrypted.
	padded := padPassword(password)
	h.deriveKeyR234(padded, o.Value[:32], p, id)
	if h.userKeyValid(u, id) {
		return h, nil
	}
	userPad := ownerUserPassword(padded, o.Value[:32], h.r, h.keyLen)
	h.deriveKeyR234(userPad, o.Value[:32], p, id)
	if h.userKeyValid(u, id) {
		return h, nil
	}
	return nil, nil
}

// resolveMethods sets the stream and string crypt methods. Below V4 both are
// RC4; V4 selects them via the /StmF and /StrF crypt-filter names in /CF.
func (h *stdSecurityHandler) resolveMethods(doc *Document, enc *Dictionary) {
	if h.v < 4 {
		h.stmMethod, h.strMethod = cryptRC4, cryptRC4
		return
	}
	cf := doc.ResolveDict(enc.Get("CF"))
	methodFor := func(name Name) cryptMethod {
		if name == "" || name == "Identity" || cf == nil {
			return cryptNone
		}
		filt := doc.ResolveDict(cf.Get(name))
		if filt == nil {
			return cryptNone
		}
		switch cfm, _ := doc.Resolve(filt.Get("CFM")).(Name); cfm {
		case "V2":
			return cryptRC4
		case "AESV2":
			return cryptAESV2
		case "AESV3":
			return cryptAESV3
		}
		return cryptNone
	}
	stmF, _ := doc.Resolve(enc.Get("StmF")).(Name)
	strF, _ := doc.Resolve(enc.Get("StrF")).(Name)
	h.stmMethod = methodFor(stmF)
	h.strMethod = methodFor(strF)
}

// deriveKeyR234 computes the file encryption key from the padded password for
// revisions 2–4 (ISO 32000-1 Algorithm 2).
func (h *stdSecurityHandler) deriveKeyR234(paddedPw, o []byte, p int32, id []byte) {
	sum := md5.New()
	sum.Write(paddedPw)
	sum.Write(o)
	var pb [4]byte
	binary.LittleEndian.PutUint32(pb[:], uint32(p))
	sum.Write(pb[:])
	sum.Write(id)
	if h.r >= 4 && !h.encryptMetadata {
		sum.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := sum.Sum(nil)
	if h.r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:h.keyLen])
			key = s[:]
		}
	}
	h.fileKey = append([]byte(nil), key[:h.keyLen]...)
}

// padPassword pads (or truncates) a password to the 32-byte field used by
// revisions 2–4 (ISO 32000-1 Algorithm 2, step a).
func padPassword(password string) []byte {
	out := make([]byte, 32)
	n := copy(out, password)
	copy(out[n:], passwordPad)
	return out
}

// userKeyValid checks that the current file key matches /U, i.e. the password
// used to derive it is the correct user password (ISO 32000-1 Algorithm 4 for
// R2, Algorithm 6 for R3–4).
func (h *stdSecurityHandler) userKeyValid(u, id []byte) bool {
	if len(u) < 16 {
		return false
	}
	if h.r == 2 {
		c, err := rc4.NewCipher(h.fileKey)
		if err != nil {
			return false
		}
		out := make([]byte, 32)
		c.XORKeyStream(out, passwordPad)
		return len(u) >= 32 && bytes.Equal(out, u[:32])
	}
	sum := md5.New()
	sum.Write(passwordPad)
	sum.Write(id)
	val := sum.Sum(nil) // 16 bytes
	c, err := rc4.NewCipher(h.fileKey)
	if err != nil {
		return false
	}
	c.XORKeyStream(val, val)
	for i := 1; i <= 19; i++ {
		key := make([]byte, len(h.fileKey))
		for j := range key {
			key[j] = h.fileKey[j] ^ byte(i)
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
func ownerUserPassword(paddedOwnerPw, o []byte, r, keyLen int) []byte {
	sum := md5.New()
	sum.Write(paddedOwnerPw)
	key := sum.Sum(nil)
	if r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:keyLen])
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

// deriveKeyR6 recovers the AES-256 file key for the given password
// (ISO 32000-2 Algorithm 2.A). It tries the user entry, then the owner entry,
// validating the password against the stored hash before decrypting the
// corresponding /UE or /OE. Returns false if neither validates.
func (h *stdSecurityHandler) deriveKeyR6(pw, u, ue, o, oe []byte) bool {
	if len(u) >= 48 && len(ue) >= 32 {
		validationSalt, keySalt := u[32:40], u[40:48]
		if bytes.Equal(hash2B(pw, validationSalt, nil), u[:32]) {
			ik := hash2B(pw, keySalt, nil)
			h.fileKey = aesCBCNoPadDecrypt(ik, make([]byte, 16), ue[:32])
			return h.fileKey != nil
		}
	}
	if len(o) >= 48 && len(oe) >= 32 && len(u) >= 48 {
		validationSalt, keySalt := o[32:40], o[40:48]
		if bytes.Equal(hash2B(pw, validationSalt, u[:48]), o[:32]) {
			ik := hash2B(pw, keySalt, u[:48])
			h.fileKey = aesCBCNoPadDecrypt(ik, make([]byte, 16), oe[:32])
			return h.fileKey != nil
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

		// E = AES-128-CBC-encrypt(K1) with key K[0:16], IV K[16:32].
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
func resolveBytes(doc *Document, o Object) []byte {
	if s, ok := doc.Resolve(o).(String); ok {
		return s.Value
	}
	return nil
}

// objectKey derives the per-object key (ISO 32000-1 Algorithm 1) for RC4 and
// AES-128; AES-256 uses the file key directly.
func (h *stdSecurityHandler) objectKey(num, gen int, aesv2 bool) []byte {
	sum := md5.New()
	sum.Write(h.fileKey)
	sum.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16)})
	sum.Write([]byte{byte(gen), byte(gen >> 8)})
	if aesv2 {
		sum.Write([]byte{0x73, 0x41, 0x6C, 0x54}) // "sAlT"
	}
	full := sum.Sum(nil)
	n := h.keyLen + 5
	if n > 16 {
		n = 16
	}
	return full[:n]
}

// decrypt returns the plaintext of data encrypted for object (num, gen) under
// the given method. Unrecognised or Identity methods return data unchanged.
//
// An AES blob that does not decrypt — a short or unaligned blob, or one whose
// PKCS#7 padding does not validate — yields nil, and the object number is
// recorded on the handler for decryptDocument to hand to the document.
// Returning the ciphertext unchanged, as this did, is the one answer that
// cannot be right: the file key is known good by then (a wrong password never
// reaches here — buildStdSecurityHandler returns no handler and the document
// reports Locked), so the failure means the blob is corrupt or was never
// encrypted, and either way the bytes are not the plaintext. Handing them on
// dressed as plaintext puts high-entropy noise into a string or a stream body,
// where it reads as a /Title, a content stream, an XMP packet or a font
// program — exactly the "silently wrong" class the package refuses to produce,
// and the shape in which a caller ends up validating noise. nil is at least
// honestly empty, and the recorded failure makes Write refuse rather than
// re-encrypt the blank.
func (h *stdSecurityHandler) decrypt(data []byte, num, gen int, method cryptMethod) []byte {
	switch method {
	case cryptRC4:
		c, err := rc4.NewCipher(h.objectKey(num, gen, false))
		if err != nil {
			return data
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	case cryptAESV2:
		out, err := aesCBCDecrypt(h.objectKey(num, gen, true), data)
		if err != nil {
			h.noteDecryptFailure(num)
			return nil
		}
		return out
	case cryptAESV3:
		out, err := aesCBCDecrypt(h.fileKey, data)
		if err != nil {
			h.noteDecryptFailure(num)
			return nil
		}
		return out
	}
	return data
}

// aesCBCDecrypt decrypts an AES-CBC blob whose first 16 bytes are the IV and
// strips PKCS#7 padding.
func aesCBCDecrypt(key, data []byte) ([]byte, error) {
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

// encrypt is the inverse of decrypt: it enciphers plaintext for object
// (num, gen) under the given method.
func (h *stdSecurityHandler) encrypt(data []byte, num, gen int, method cryptMethod) []byte {
	switch method {
	case cryptRC4:
		c, err := rc4.NewCipher(h.objectKey(num, gen, false))
		if err != nil {
			return data
		}
		out := make([]byte, len(data))
		c.XORKeyStream(out, data)
		return out
	case cryptAESV2:
		if out, err := aesCBCEncrypt(h.objectKey(num, gen, true), data); err == nil {
			return out
		}
	case cryptAESV3:
		if out, err := aesCBCEncrypt(h.fileKey, data); err == nil {
			return out
		}
	}
	return data
}

// aesCBCEncrypt encrypts with AES-CBC, prepending a random IV and applying
// PKCS#7 padding â the format aesCBCDecrypt expects.
func aesCBCEncrypt(key, data []byte) ([]byte, error) {
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

// encryptCopy returns encrypted copies of the given objects, leaving the
// originals (the in-memory plaintext) untouched. The /Encrypt dictionary is
// passed through unencrypted. A stream whose data grows (AES padding) gets its
// direct /Length updated; an indirect /Length is handled by the caller.
func (h *stdSecurityHandler) encryptCopy(objects map[int]*IndirectObject) map[int]*IndirectObject {
	out := make(map[int]*IndirectObject, len(objects))
	for num, iobj := range objects {
		if num == h.encryptObjNum {
			out[num] = iobj
			continue
		}
		out[num] = &IndirectObject{
			Number:     iobj.Number,
			Generation: iobj.Generation,
			Value:      h.encryptObj(iobj.Value, iobj.Number, iobj.Generation),
		}
	}
	return out
}

func (h *stdSecurityHandler) encryptObj(o Object, num, gen int) Object {
	switch v := o.(type) {
	case String:
		if h.strMethod == cryptNone {
			return v
		}
		return String{Value: h.encrypt(v.Value, num, gen, h.strMethod), IsHex: v.IsHex}
	case Array:
		cp := make(Array, len(v))
		for i := range v {
			cp[i] = h.encryptObj(v[i], num, gen)
		}
		return cp
	case *Dictionary:
		return h.encryptDictCopy(v, num, gen)
	case *Stream:
		d := h.encryptDictCopy(&v.Dict, num, gen)
		data := v.Data
		skip := h.stmMethod == cryptNone
		if t, _ := v.Dict.Get("Type").(Name); t == "XRef" || (!h.encryptMetadata && t == "Metadata") {
			skip = true
		}
		if !skip {
			data = h.encrypt(v.Data, num, gen, h.stmMethod)
			if _, isRef := d.Get("Length").(IndirectRef); !isRef {
				d.Set("Length", Integer(len(data)))
			}
		}
		return &Stream{Dict: *d, Data: data}
	}
	return o
}

func (h *stdSecurityHandler) encryptDictCopy(d *Dictionary, num, gen int) *Dictionary {
	cp := &Dictionary{
		Keys:   append([]Name(nil), d.Keys...),
		Values: make([]Object, len(d.Values)),
	}
	sig := isSignatureDict(d)
	for i, val := range d.Values {
		if sig && d.Keys[i] == "Contents" {
			cp.Values[i] = val // the signature value is never encrypted (7.6.2)
			continue
		}
		cp.Values[i] = h.encryptObj(val, num, gen)
	}
	return cp
}

// isSignatureDict reports whether d is a signature (or document time-stamp)
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
func isSignatureDict(d *Dictionary) bool {
	if d.Get("ByteRange") == nil {
		return false
	}
	if _, ok := d.Get("Contents").(String); !ok {
		return false
	}
	if t, _ := d.Get("Type").(Name); t != "" && t != "Sig" && t != "DocTimeStamp" {
		return false
	}
	return true
}

// decryptDocument decrypts every string and stream in the loaded (top-level)
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
// decryptDictStrings).
func (h *stdSecurityHandler) decryptDocument(doc *Document) {
	// The /Encrypt dictionary's own strings (/O, /U, /Perms, …) are never
	// encrypted and must not be decrypted. Skipping by object number alone is
	// not enough: a malformed file can point several xref entries at the
	// /Encrypt dictionary's byte offset, and Read shares one parsed value across
	// those object numbers (bounding re-parse work — the duplicate-offset
	// guard). Only one of those numbers is h.encryptObjNum, so decrypting an
	// alias would mutate the shared /Encrypt dictionary in place and corrupt the
	// key material (AES padding strips /O and /U from 32 to 16 bytes), leaving
	// the rewritten file undecryptable. Skip the dictionary by pointer identity.
	encryptDict := doc.ResolveDict(doc.Trailer.Get("Encrypt"))
	// A parsed value shared by several object numbers (duplicate xref offsets)
	// must be decrypted at most once: decryptDocument mutates streams and
	// dictionaries in place, so visiting the same value under a second number
	// would double-decrypt and corrupt it. seen tracks the mutable reference
	// values already processed; it never matches in a well-formed file, where
	// every object is a distinct value, so behaviour there is unchanged.
	seen := map[any]bool{}
	for num, iobj := range doc.Objects {
		if num == h.encryptObjNum {
			continue // the /Encrypt dictionary's strings are not encrypted
		}
		if d, ok := iobj.Value.(*Dictionary); ok && d == encryptDict {
			continue // an alias of the /Encrypt dictionary at a shared offset
		}
		switch iobj.Value.(type) {
		case *Stream, *Dictionary:
			if seen[iobj.Value] {
				continue
			}
			seen[iobj.Value] = true
		}
		gen := iobj.Generation
		switch v := iobj.Value.(type) {
		case *Stream:
			// Cross-reference streams are never encrypted.
			if t, _ := v.Dict.Get("Type").(Name); t == "XRef" {
				continue
			}
			h.decryptDictStrings(&v.Dict, num, gen)
			if h.stmMethod == cryptNone {
				continue
			}
			// With EncryptMetadata false, the metadata stream stays in the clear.
			if !h.encryptMetadata {
				if t, _ := v.Dict.Get("Type").(Name); t == "Metadata" {
					continue
				}
			}
			v.Data = h.decrypt(v.Data, num, gen, h.stmMethod)
		case *Dictionary:
			h.decryptDictStrings(v, num, gen)
		case Array:
			h.decryptArrayStrings(v, num, gen)
		case String:
			iobj.Value = h.decryptStringValue(v, num, gen)
			doc.Objects[num] = iobj
		}
	}

	// Objects whose ciphertext did not decrypt are now empty rather than noise
	// (see decrypt). Record them on the document: the content is unrecoverable,
	// so a write must refuse rather than emit the blanks.
	if len(h.failedObjects) > 0 {
		doc.decryptFailures = doc.decryptFailures[:0]
		for num := range h.failedObjects {
			doc.decryptFailures = append(doc.decryptFailures, num)
		}
		slices.Sort(doc.decryptFailures)
	}
}

func (h *stdSecurityHandler) decryptDictStrings(d *Dictionary, num, gen int) {
	// A signature dictionary's /Contents is not encrypted (ISO 32000-2, 7.6.2;
	// see isSignatureDict), so it must not be decrypted either. Decrypting it
	// would replace the CMS blob the file actually contains with a transform of
	// it: with RC4 that is always a different value, so every conformant
	// encrypted-and-signed file would fail verification with a misleading
	// "not a CMS SignedData"; with AES it is caught by the padding check ~99.6%
	// of the time and silently truncates the value the rest.
	//
	// This runs for nested dictionaries too, and the exemption is by key within
	// the dictionary rather than by object number, so it is unaffected by the
	// aliasing hazard the /Encrypt skip in decryptDocument documents: whichever
	// object number a shared value is reached under, its /Contents is skipped.
	sig := isSignatureDict(d)
	for i := range d.Values {
		if sig && d.Keys[i] == "Contents" {
			continue
		}
		d.Values[i] = h.decryptValue(d.Values[i], num, gen)
	}
}

func (h *stdSecurityHandler) decryptArrayStrings(a Array, num, gen int) {
	for i := range a {
		a[i] = h.decryptValue(a[i], num, gen)
	}
}

func (h *stdSecurityHandler) decryptValue(o Object, num, gen int) Object {
	switch v := o.(type) {
	case String:
		return h.decryptStringValue(v, num, gen)
	case *Dictionary:
		h.decryptDictStrings(v, num, gen)
	case Array:
		h.decryptArrayStrings(v, num, gen)
	}
	return o
}

func (h *stdSecurityHandler) decryptStringValue(s String, num, gen int) String {
	if h.strMethod == cryptNone {
		return s
	}
	return String{Value: h.decrypt(s.Value, num, gen, h.strMethod), IsHex: s.IsHex}
}

// encInt resolves an object to an int, or 0.
func encInt(doc *Document, o Object) int {
	if n, ok := doc.Resolve(o).(Integer); ok {
		return int(n)
	}
	return 0
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
	if d.security.encryptObjNum >= 0 {
		delete(d.Objects, d.security.encryptObjNum)
	}
	d.security = nil
	d.Encrypted = false
	trailer := d.Trailer.Clone()
	trailer.Delete("Encrypt")
	d.Trailer = *trailer
}
