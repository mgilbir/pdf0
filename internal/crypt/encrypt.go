package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"github.com/mgilbir/pdf0/object"
)

// This file is the write side of the standard security handler (ISO 32000-2
// §7.6): it arms a document for encryption on the next Write by building a
// fresh AES-256 (V5/R6) /Encrypt dictionary — /U, /UE, /O, /OE and /Perms —
// around a randomly generated file key. Only that one scheme is ever produced;
// the legacy RC4 and AES-128 revisions are read-only and live in crypt.go,
// which also performs the actual per-object enciphering. The values written
// here are precisely what crypt.go's R6 read path re-derives and validates, so
// the two sides have to change together.

// NewAES256 builds an AES-256 (V5/R6) security handler with a random
// file key and the matching /Encrypt dictionary for the given passwords. It is
// the inverse of the R6 read path (deriveKeyR6): the values it writes are what
// that function validates and decrypts.
func NewAES256(userPw, ownerPw string) (*Handler, *object.Dictionary, error) {
	FileKey := make([]byte, 32)
	salts := make([]byte, 32) // uValSalt|uKeySalt|oValSalt|oKeySalt, 8 bytes each
	permsTail := make([]byte, 4)
	for _, b := range [][]byte{FileKey, salts, permsTail} {
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
	ue, err := aesCBCNoPadEncrypt(hash2B(up, uKeySalt, nil), zeroIV, FileKey)
	if err != nil {
		return nil, nil, err
	}
	// /O and /OE mirror /U/UE but include /U as additional data (Algorithm 9).
	o := append(append(append([]byte(nil), hash2B(op, oValSalt, u)...), oValSalt...), oKeySalt...)
	oe, err := aesCBCNoPadEncrypt(hash2B(op, oKeySalt, u), zeroIV, FileKey)
	if err != nil {
		return nil, nil, err
	}

	p := int32(-4) // permit everything (advisory; encryption is enforced by key)
	perms, err := encryptPerms(FileKey, p, true, permsTail)
	if err != nil {
		return nil, nil, err
	}

	h := &Handler{V: 5, R: 6, KeyLen: 32, FileKey: FileKey,
		StmMethod: AESV3, StrMethod: AESV3, EncryptMetadata: true,
	}

	stdCF := &object.Dictionary{}
	stdCF.Set("CFM", object.Name("AESV3"))
	stdCF.Set("Length", object.Integer(32))
	stdCF.Set("AuthEvent", object.Name("DocOpen"))
	cf := &object.Dictionary{}
	cf.Set("StdCF", stdCF)

	dict := &object.Dictionary{}
	dict.Set("Filter", object.Name("Standard"))
	dict.Set("V", object.Integer(5))
	dict.Set("R", object.Integer(6))
	dict.Set("Length", object.Integer(256))
	dict.Set("CF", cf)
	dict.Set("StmF", object.Name("StdCF"))
	dict.Set("StrF", object.Name("StdCF"))
	dict.Set("O", object.String{Value: o})
	dict.Set("U", object.String{Value: u})
	dict.Set("OE", object.String{Value: oe})
	dict.Set("UE", object.String{Value: ue})
	dict.Set("P", object.Integer(p))
	dict.Set("Perms", object.String{Value: perms})
	dict.Set("EncryptMetadata", object.Boolean(true))
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
func encryptPerms(FileKey []byte, p int32, EncryptMetadata bool, tail4 []byte) ([]byte, error) {
	var b [16]byte
	binary.LittleEndian.PutUint32(b[0:4], uint32(p))
	b[4], b[5], b[6], b[7] = 0xFF, 0xFF, 0xFF, 0xFF // high 32 bits of the 64-bit P
	if EncryptMetadata {
		b[8] = 'T'
	} else {
		b[8] = 'F'
	}
	b[9], b[10], b[11] = 'a', 'd', 'b'
	copy(b[12:16], tail4)

	block, err := aes.NewCipher(FileKey)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	block.Encrypt(out, b[:]) // ECB: one block
	return out, nil
}
