package pdf0

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"encoding/binary"
	"errors"
)

// The PDF standard security handler (ISO 32000-1 §7.6, ISO 32000-2 §7.6).
// Decryption for the empty user password: RC4 (V1/V2, R2–R4) and AES-128
// (V4, /AESV2). AES-256 (V5, /AESV3, R6) is handled separately.

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
}

// buildStdSecurityHandler parses the trailer's /Encrypt dictionary and derives
// the file key for the empty user password. It returns (nil, nil) when the
// scheme is one decryption does not support, so the caller leaves the document
// encrypted; an error signals malformed encryption metadata.
func buildStdSecurityHandler(doc *Document) (*stdSecurityHandler, error) {
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
	if v >= 5 || r >= 5 {
		return nil, nil // AES-256 / R6 not handled here
	}

	h := &stdSecurityHandler{v: v, r: r, encryptObjNum: encNum, encryptMetadata: true}
	if em, ok := doc.Resolve(enc.Get("EncryptMetadata")).(Boolean); ok {
		h.encryptMetadata = bool(em)
	}
	h.keyLen = encInt(doc, enc.Get("Length")) / 8
	if r == 2 || h.keyLen == 0 {
		h.keyLen = 5 // R2 is always 40-bit; default when /Length is absent
	}
	h.resolveMethods(doc, enc)

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
	h.deriveKeyR234(o.Value[:32], p, id)
	return h, nil
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

// deriveKeyR234 computes the file encryption key for revisions 2–4
// (ISO 32000-1 Algorithm 2) with the empty user password.
func (h *stdSecurityHandler) deriveKeyR234(o []byte, p int32, id []byte) {
	sum := md5.New()
	sum.Write(passwordPad) // empty password → the padding string
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
		if out, err := aesCBCDecrypt(h.objectKey(num, gen, true), data); err == nil {
			return out
		}
	case cryptAESV3:
		if out, err := aesCBCDecrypt(h.fileKey, data); err == nil {
			return out
		}
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
	if n := len(out); n > 0 {
		if pad := int(out[n-1]); pad >= 1 && pad <= aes.BlockSize && pad <= n {
			out = out[:n-pad]
		}
	}
	return out, nil
}

// decryptDocument decrypts every string and stream in the loaded (top-level)
// objects in place. It must run before object-stream contents are materialised:
// an /ObjStm container is itself an encrypted stream, while the objects inside
// it are not separately encrypted.
func (h *stdSecurityHandler) decryptDocument(doc *Document) {
	for num, iobj := range doc.Objects {
		if num == h.encryptObjNum {
			continue // the /Encrypt dictionary's strings are not encrypted
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
}

func (h *stdSecurityHandler) decryptDictStrings(d *Dictionary, num, gen int) {
	for i := range d.Values {
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
