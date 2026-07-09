package pdf0

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// SetEncryption configures the document to be encrypted on the next Write using
// the standard security handler with AES-256 (V5/R6, ISO 32000-2 §7.6.4). The
// user password opens the file for reading; the owner password additionally
// carries full permissions. Either may be empty.
//
// It installs a fresh /Encrypt dictionary and a random file key, replacing any
// existing encryption. Write then enciphers every string and stream; the
// in-memory document stays in the clear, so it remains usable afterwards.
func (d *Document) SetEncryption(userPassword, ownerPassword string) error {
	h, dict, err := newAES256Encryption(userPassword, ownerPassword)
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
	h.encryptObjNum = encNum
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

// newAES256Encryption builds an AES-256 (V5/R6) security handler with a random
// file key and the matching /Encrypt dictionary for the given passwords. It is
// the inverse of the R6 read path (deriveKeyR6): the values it writes are what
// that function validates and decrypts.
func newAES256Encryption(userPw, ownerPw string) (*stdSecurityHandler, *Dictionary, error) {
	fileKey := make([]byte, 32)
	salts := make([]byte, 32) // uValSalt|uKeySalt|oValSalt|oKeySalt, 8 bytes each
	permsTail := make([]byte, 4)
	for _, b := range [][]byte{fileKey, salts, permsTail} {
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
	}
	uValSalt, uKeySalt := salts[0:8], salts[8:16]
	oValSalt, oKeySalt := salts[16:24], salts[24:32]
	up, op := []byte(userPw), []byte(ownerPw)
	zeroIV := make([]byte, 16)

	// /U = Hash(pw, userValSalt) || userValSalt || userKeySalt; /UE encrypts the
	// file key under Hash(pw, userKeySalt) (ISO 32000-2 Algorithm 8).
	u := append(append(append([]byte(nil), hash2B(up, uValSalt, nil)...), uValSalt...), uKeySalt...)
	ue, err := aesCBCNoPadEncrypt(hash2B(up, uKeySalt, nil), zeroIV, fileKey)
	if err != nil {
		return nil, nil, err
	}
	// /O and /OE mirror /U/UE but include /U as additional data (Algorithm 9).
	o := append(append(append([]byte(nil), hash2B(op, oValSalt, u)...), oValSalt...), oKeySalt...)
	oe, err := aesCBCNoPadEncrypt(hash2B(op, oKeySalt, u), zeroIV, fileKey)
	if err != nil {
		return nil, nil, err
	}

	p := int32(-4) // permit everything (advisory; encryption is enforced by key)
	perms, err := encryptPerms(fileKey, p, true, permsTail)
	if err != nil {
		return nil, nil, err
	}

	h := &stdSecurityHandler{
		v: 5, r: 6, keyLen: 32, fileKey: fileKey,
		stmMethod: cryptAESV3, strMethod: cryptAESV3, encryptMetadata: true,
	}

	stdCF := &Dictionary{}
	stdCF.Set("CFM", Name("AESV3"))
	stdCF.Set("Length", Integer(32))
	stdCF.Set("AuthEvent", Name("DocOpen"))
	cf := &Dictionary{}
	cf.Set("StdCF", stdCF)

	dict := &Dictionary{}
	dict.Set("Filter", Name("Standard"))
	dict.Set("V", Integer(5))
	dict.Set("R", Integer(6))
	dict.Set("Length", Integer(256))
	dict.Set("CF", cf)
	dict.Set("StmF", Name("StdCF"))
	dict.Set("StrF", Name("StdCF"))
	dict.Set("O", String{Value: o})
	dict.Set("U", String{Value: u})
	dict.Set("OE", String{Value: oe})
	dict.Set("UE", String{Value: ue})
	dict.Set("P", Integer(p))
	dict.Set("Perms", String{Value: perms})
	dict.Set("EncryptMetadata", Boolean(true))
	return h, dict, nil
}

// aesCBCNoPadEncrypt is the inverse of aesCBCNoPadDecrypt (block-aligned input,
// no padding, caller-supplied IV) — used for the /UE and /OE key blobs.
func aesCBCNoPadEncrypt(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, errors.New("AES input is not block-aligned")
	}
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	return out, nil
}

// encryptPerms builds the /Perms block (ISO 32000-2 Algorithm 11): the
// permissions and flags encrypted with AES-256 in ECB mode (a single block, no
// IV, no padding).
func encryptPerms(fileKey []byte, p int32, encryptMetadata bool, tail4 []byte) ([]byte, error) {
	var b [16]byte
	binary.LittleEndian.PutUint32(b[0:4], uint32(p))
	b[4], b[5], b[6], b[7] = 0xFF, 0xFF, 0xFF, 0xFF // high 32 bits of the 64-bit P
	if encryptMetadata {
		b[8] = 'T'
	} else {
		b[8] = 'F'
	}
	b[9], b[10], b[11] = 'a', 'd', 'b'
	copy(b[12:16], tail4)

	block, err := aes.NewCipher(fileKey)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	block.Encrypt(out, b[:]) // ECB: one block
	return out, nil
}
