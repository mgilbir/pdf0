package pdf0

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"encoding/hex"
	"fmt"
	"testing"
)

// buildAliasedEncryptDictPDF builds an RC4 V2/R3 encrypted PDF whose cross-
// reference table points two object numbers — 4 and 5 — at the same byte offset:
// the /Encrypt dictionary. A malformed producer (or a hostile file) can emit
// such duplicate offsets, and Read shares one parsed value across the two
// numbers to bound re-parsing work. Only object 4 is the trailer's /Encrypt, so
// a decrypt pass that skips the /Encrypt dictionary by object number alone still
// decrypts object 5 — mutating the shared dictionary in place and corrupting the
// /O and /U key material, which makes the file undecryptable when rewritten.
func buildAliasedEncryptDictPDF(t *testing.T, userPw, ownerPw, producer string) []byte {
	t.Helper()
	const keyLen = 16
	id := []byte("0123456789ABCDEF")
	p := int32(-3904)
	userPad := padPassword(userPw)
	ownerPad := padPassword(ownerPw)

	ok := md5.Sum(ownerPad)
	okey := ok[:]
	for i := 0; i < 50; i++ {
		s := md5.Sum(okey[:keyLen])
		okey = s[:]
	}
	oEntry := rc4Cascade(okey[:keyLen], userPad, seq(0, 19))

	h := &stdSecurityHandler{r: 3, keyLen: keyLen, encryptMetadata: true}
	h.deriveKeyR234(userPad, oEntry, p, id)

	m := md5.New()
	m.Write(passwordPad)
	m.Write(id)
	uVal := rc4Cascade(h.fileKey, m.Sum(nil), seq(0, 19))
	uEntry := make([]byte, 32)
	copy(uEntry, uVal)

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

	// Object 5's cross-reference entry points at object 4's offset: the two
	// numbers alias one parsed /Encrypt dictionary.
	xref := buf.Len()
	buf.WriteString("xref\n0 6\n0000000000 65535 f \r\n")
	for _, off := range []int{o1, o2, o3, o4, o4} {
		fmt.Fprintf(&buf, "%010d 00000 n \r\n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R /Encrypt 4 0 R /ID [%s %s] >>\n", hx(id), hx(id))
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xref)
	return buf.Bytes()
}

// TestReadDuplicateOffsetEncryptDict is the regression for the encrypted round-
// trip data loss found by the corpus sweep: when a duplicate cross-reference
// offset aliases the /Encrypt dictionary to a second object number, decrypting
// that alias corrupted the shared /O and /U, so the rewritten file could not be
// decrypted and objects packed into its (now-undecryptable) object streams were
// lost on re-read. The /Encrypt dictionary must be left untouched by decryption,
// identified by pointer identity rather than object number alone.
func TestReadDuplicateOffsetEncryptDict(t *testing.T) {
	const producer = "pdf0 aliased-encrypt-dict producer"
	data := buildAliasedEncryptDictPDF(t, "sesame", "overlord", producer)

	doc, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), "sesame")
	if err != nil {
		t.Fatal(err)
	}
	if doc.security == nil {
		t.Fatal("file did not decrypt")
	}
	// Objects 4 and 5 must share the parsed /Encrypt dictionary (the duplicate
	// offset), and that dictionary's key material must be intact — a decrypt
	// pass must not have mutated it via the alias.
	d4, _ := doc.Objects[4].Value.(*Dictionary)
	d5, _ := doc.Objects[5].Value.(*Dictionary)
	if d4 == nil || d5 == nil || d4 != d5 {
		t.Fatalf("expected objects 4 and 5 to share one /Encrypt dictionary (d4=%p d5=%p)", d4, d5)
	}
	o, _ := d4.Get("O").(String)
	u, _ := d4.Get("U").(String)
	if len(o.Value) != 32 || len(u.Value) != 32 {
		t.Fatalf("/Encrypt /O and /U corrupted by alias decryption: len(O)=%d len(U)=%d, want 32,32", len(o.Value), len(u.Value))
	}

	// End to end: the rewritten file must still decrypt with the same password
	// and preserve content. Before the fix, the corrupted /O and /U left the
	// re-read undecryptable (security == nil).
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.Bytes()
	doc2, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "sesame")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if doc2.security == nil {
		t.Fatal("rewritten file is not decryptable (alias corrupted the key material)")
	}
	cat, _ := doc2.Objects[1].Value.(*Dictionary)
	if s, _ := cat.Get("Producer").(String); string(s.Value) != producer {
		t.Errorf("/Producer after round-trip = %q, want %q", s.Value, producer)
	}
}
