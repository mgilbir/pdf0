package pdf0

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"encoding/hex"
	"fmt"
	"testing"
)

// rc4Cascade applies the 20-round RC4 cascade of Algorithm 3/5/7: RC4 with the
// key XOR'd by each round index, in the given order.
func rc4Cascade(base, data []byte, rounds []int) []byte {
	out := append([]byte(nil), data...)
	for _, i := range rounds {
		k := make([]byte, len(base))
		for j := range k {
			k[j] = base[j] ^ byte(i)
		}
		c, _ := rc4.NewCipher(k)
		c.XORKeyStream(out, out)
	}
	return out
}

func seq(from, to int) []int {
	var r []int
	if from <= to {
		for i := from; i <= to; i++ {
			r = append(r, i)
		}
	} else {
		for i := from; i >= to; i-- {
			r = append(r, i)
		}
	}
	return r
}

// buildRC4R3EncryptedPDF constructs a minimal PDF encrypted with RC4, V2/R3,
// 128-bit, whose catalog carries an encrypted /Producer string. It implements
// the producer side of Algorithms 2/3/5, the inverses of the reader's paths.
func buildRC4R3EncryptedPDF(t *testing.T, userPw, ownerPw, producer string) []byte {
	t.Helper()
	const keyLen = 16
	id := []byte("0123456789ABCDEF")
	p := int32(-3904)
	userPad := padPassword(userPw)
	ownerPad := padPassword(ownerPw)

	// /O (Algorithm 3): owner key = MD5^50(ownerPad)[:keyLen]; O = RC4 cascade
	// 0..19 over the padded user password.
	ok := md5.Sum(ownerPad)
	okey := ok[:]
	for i := 0; i < 50; i++ {
		s := md5.Sum(okey[:keyLen])
		okey = s[:]
	}
	oEntry := rc4Cascade(okey[:keyLen], userPad, seq(0, 19))

	// File key from the user password (Algorithm 2).
	h := &stdSecurityHandler{r: 3, keyLen: keyLen, encryptMetadata: true}
	h.deriveKeyR234(userPad, oEntry, p, id)

	// /U (Algorithm 5): MD5(pad+id), then RC4 cascade 0..19 with the file key.
	m := md5.New()
	m.Write(passwordPad)
	m.Write(id)
	uVal := rc4Cascade(h.fileKey, m.Sum(nil), seq(0, 19))
	uEntry := make([]byte, 32)
	copy(uEntry, uVal)

	// Encrypt the catalog's /Producer for object 1, generation 0.
	c, _ := rc4.NewCipher(h.objectKey(1, 0, false))
	encProducer := make([]byte, len(producer))
	c.XORKeyStream(encProducer, []byte(producer))

	hx := func(b []byte) string { return "<" + hex.EncodeToString(b) + ">" }
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")
	o1 := buf.Len()
	fmt.Fprintf(&buf, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /Producer %s >>\nendobj\n", hx(encProducer))
	o2 := buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	o3 := buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")
	o4 := buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Filter /Standard /V 2 /R 3 /Length 128 /O %s /U %s /P %d >>\nendobj\n",
		hx(oEntry), hx(uEntry), p)

	xref := buf.Len()
	buf.WriteString("xref\n0 5\n0000000000 65535 f \r\n")
	for _, off := range []int{o1, o2, o3, o4} {
		fmt.Fprintf(&buf, "%010d 00000 n \r\n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 5 /Root 1 0 R /Encrypt 4 0 R /ID [%s %s] >>\n", hx(id), hx(id))
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xref)
	return buf.Bytes()
}

// TestReadWithPassword exercises the user- and owner-password paths and the
// rejection of a wrong password against a real encrypted PDF.
func TestReadWithPassword(t *testing.T) {
	const producer = "pdf0 secret producer string"
	data := buildRC4R3EncryptedPDF(t, "sesame", "overlord", producer)

	got := func(doc *Document) string {
		d, _ := doc.Objects[1].Value.(*Dictionary)
		s, _ := d.Get("Producer").(String)
		return string(s.Value)
	}

	for _, pw := range []string{"sesame", "overlord"} {
		doc, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), pw)
		if err != nil {
			t.Fatalf("password %q: %v", pw, err)
		}
		if doc.security == nil {
			t.Errorf("password %q: expected decryption", pw)
		}
		if p := got(doc); p != producer {
			t.Errorf("password %q: /Producer = %q, want %q", pw, p, producer)
		}
	}

	// A wrong password and the empty password leave the file encrypted.
	for _, pw := range []string{"wrong", ""} {
		doc, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), pw)
		if err != nil {
			t.Fatalf("password %q: %v", pw, err)
		}
		if doc.security != nil {
			t.Errorf("password %q: expected the file to stay encrypted", pw)
		}
		if p := got(doc); p == producer {
			t.Errorf("password %q: string decrypted with the wrong password", pw)
		}
	}
}
