package pdf0

import (
	"crypto/aes"
	"crypto/cipher"
	"testing"
)

// TestAESRejectsInvalidPadding is the C37 guard: AES-CBC decryption validates
// every PKCS#7 padding byte instead of trusting the final length byte, so
// crafted or corrupt ciphertext is rejected rather than silently mis-truncated.
func TestAESRejectsInvalidPadding(t *testing.T) {
	key := make([]byte, 16)
	iv := make([]byte, aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	// A block whose final byte claims 16 bytes of padding, but the rest are not
	// all 0x10 — invalid PKCS#7.
	bad := make([]byte, aes.BlockSize)
	bad[aes.BlockSize-1] = byte(aes.BlockSize)
	ct := make([]byte, aes.BlockSize)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, bad)
	if _, err := aesCBCDecrypt(key, append(append([]byte{}, iv...), ct...)); err == nil {
		t.Fatal("aesCBCDecrypt accepted invalid PKCS#7 padding")
	}

	// A full block of 0x10 is valid padding (an all-padding block); it decrypts to
	// empty plaintext.
	good := make([]byte, aes.BlockSize)
	for i := range good {
		good[i] = byte(aes.BlockSize)
	}
	ct2 := make([]byte, aes.BlockSize)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct2, good)
	out, err := aesCBCDecrypt(key, append(append([]byte{}, iv...), ct2...))
	if err != nil {
		t.Fatalf("valid padding rejected: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("all-padding block should decrypt to empty, got %d bytes", len(out))
	}
}
