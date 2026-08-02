package pdf0

import (
	"bytes"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rc4"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
	"strings"
	"testing"
)

// A signature dictionary's /Contents is exempt from encryption (ISO 32000-2,
// 7.6.2): the signature is computed over the file's own bytes, so the value in
// the file must be the CMS blob itself. These tests build a conformant
// encrypted-and-signed file the way a third-party signer does — encrypt first,
// then append the signature as an incremental update with /Contents in the
// clear — and check that reading it back does not transform the signature value.
//
// RC4 is used because it makes the bug deterministic: decryption is a bijection,
// so decrypting an unencrypted /Contents always yields a different value. Under
// AES the wrong decryption is usually rejected by the PKCS#7 padding check and
// the value survives by accident (only ~1 file in 250 is corrupted).

// rc4EncryptedDoc returns a document set up for RC4 (V2/R3, 128-bit) encryption
// with the empty user password, mirroring the read path's key derivation.
func rc4EncryptedDoc(t *testing.T) *Document {
	t.Helper()
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	o := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(o); err != nil {
		t.Fatal(err)
	}
	const perm = -1

	h := &crypt.Handler{V: 2, R: 3, KeyLen: 16, EncryptMetadata: true,
		StmMethod: crypt.RC4, StrMethod: crypt.RC4}
	h.DeriveKeyR234(crypt.PadPassword(""), o, perm, id)

	// /U for the empty user password (ISO 32000-1 Algorithm 5), the inverse of
	// the userKeyValid check the reader runs.
	sum := md5.New()
	sum.Write(crypt.PasswordPad)
	sum.Write(id)
	u := sum.Sum(nil)
	c, err := rc4.NewCipher(h.FileKey)
	if err != nil {
		t.Fatal(err)
	}
	c.XORKeyStream(u, u)
	for i := 1; i <= 19; i++ {
		key := make([]byte, len(h.FileKey))
		for j := range key {
			key[j] = h.FileKey[j] ^ byte(i)
		}
		c, err := rc4.NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		c.XORKeyStream(u, u)
	}
	u = append(u, make([]byte, 16)...) // 32 bytes: the value plus arbitrary padding

	enc := &object.Dictionary{}
	enc.Set("Filter", object.Name("Standard"))
	enc.Set("V", object.Integer(2))
	enc.Set("R", object.Integer(3))
	enc.Set("Length", object.Integer(128))
	enc.Set("O", object.String{Value: o, IsHex: true})
	enc.Set("U", object.String{Value: u, IsHex: true})
	enc.Set("P", object.Integer(perm))

	maxObj := 0
	for num := range doc.Objects {
		if num > maxObj {
			maxObj = num
		}
	}
	encNum := maxObj + 1
	doc.Objects[encNum] = &object.IndirectObject{Number: encNum, Value: enc}
	h.EncryptObjNum = encNum
	doc.security = h
	doc.Encrypted = true
	tr := doc.Trailer.Clone()
	tr.Set("Encrypt", object.IndirectRef{Number: encNum})
	tr.Set("ID", object.Array{object.String{Value: id, IsHex: true}, object.String{Value: append([]byte(nil), id...), IsHex: true}})
	doc.Trailer = *tr
	return doc
}

// appendClearSignature appends a signature as an incremental update to an
// encrypted file, leaving /Contents unencrypted as ISO 32000-2, 7.6.2 requires,
// and fills in /ByteRange and the CMS over the resulting bytes.
func appendClearSignature(t *testing.T, enc []byte, cert *x509.Certificate, key crypto.Signer) []byte {
	t.Helper()
	edoc, err := Read(bytes.NewReader(enc), int64(len(enc)))
	if err != nil {
		t.Fatalf("read the encrypted base file: %v", err)
	}
	if edoc.security == nil {
		t.Fatal("the encrypted base file did not decrypt with the empty password")
	}
	maxObj := 0
	for num := range edoc.Objects {
		if num > maxObj {
			maxObj = num
		}
	}
	sigNum := maxObj + 1
	prevXref, err := findStartXref(enc)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	out.Write(enc)
	if enc[len(enc)-1] != '\n' {
		out.WriteByte('\n')
	}
	sigOff := out.Len()
	fmt.Fprintf(&out, "%d 0 obj\n<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /adbe.pkcs7.detached "+
		"/ByteRange [%s] /Contents <%s> >>\nendobj\n",
		sigNum, byteRangePlaceholder, strings.Repeat("0", 2*sigContentsBytes))
	xrefOff := out.Len()
	out.WriteString("xref\n0 1\n0000000000 65535 f \r\n")
	fmt.Fprintf(&out, "%d 1\n%010d 00000 n \r\n", sigNum, sigOff)
	out.WriteString("trailer\n")
	trailer := edoc.Trailer.Clone()
	trailer.Set("Size", object.Integer(sigNum+1))
	trailer.Set("Prev", object.Integer(prevXref))
	s := NewSerializer(&out)
	if err := s.WriteDictionary(trailer); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&out, "\nstartxref\n%d\n%%%%EOF\n", xrefOff)

	data, err := patchSignature(out.Bytes(), cert, key, nil, nil)
	if err != nil {
		t.Fatalf("patchSignature: %v", err)
	}
	return data
}

// rawContentsWindow returns the bytes the file's /Contents hex window decodes to.
func rawContentsWindow(t *testing.T, file []byte) []byte {
	t.Helper()
	ci := bytes.Index(file, []byte("/Contents"))
	if ci < 0 {
		t.Fatal("no /Contents in the file")
	}
	lt := ci + bytes.IndexByte(file[ci:], '<')
	gt := lt + bytes.IndexByte(file[lt:], '>')
	raw := make([]byte, (gt-lt-1)/2)
	if _, err := hex.Decode(raw, file[lt+1:gt]); err != nil {
		t.Fatalf("decoding the /Contents window: %v", err)
	}
	return raw
}

// TestEncryptedSignedFileVerifies reads an encrypted file carrying a conformant
// (unencrypted) signature value and requires the signature to verify. Before the
// /Contents exemption the crypt layer decrypted the signature value, so
// verification failed with "not a CMS SignedData" on a perfectly good file.
func TestEncryptedSignedFileVerifies(t *testing.T) {
	cert, key := signtest.CertKey(t)
	doc := rc4EncryptedDoc(t)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	file := appendClearSignature(t, buf.Bytes(), cert, key)

	// Control: the file's own bytes really do carry a valid signature, checked
	// without going through the object model.
	rawContents := rawContentsWindow(t, file)
	ci := bytes.Index(file, []byte("/Contents"))
	lt := ci + bytes.IndexByte(file[ci:], '<')
	gt := lt + bytes.IndexByte(file[lt:], '>')
	signedBytes := append(append([]byte(nil), file[:lt]...), file[gt+1:]...)
	if _, _, _, err := sign.VerifyCMS(bytes.TrimRight(rawContents, "\x00"), signedBytes); err != nil {
		t.Fatalf("control: the raw file bytes should carry a valid signature: %v", err)
	}

	got, err := Read(bytes.NewReader(file), int64(len(file)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.security == nil {
		t.Fatal("the file did not decrypt with the empty password")
	}
	var found bool
	for _, iobj := range got.Objects {
		d, ok := iobj.Value.(*object.Dictionary)
		if !ok || d.Get("ByteRange") == nil {
			continue
		}
		found = true
		c, _ := d.Get("Contents").(object.String)
		if !bytes.Equal(c.Value, rawContents) {
			t.Fatalf("the signature value was transformed on read: file has %x…, document has %x…",
				rawContents[:16], c.Value[:16])
		}
	}
	if !found {
		t.Fatal("no signature dictionary in the re-read document")
	}

	res := got.VerifySignatures(file)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if !res[0].Valid || res[0].Err != nil {
		t.Fatalf("signature in an encrypted document did not verify: valid=%v err=%v", res[0].Valid, res[0].Err)
	}
	if !res[0].CoversWholeDocument {
		t.Error("the signature should cover the whole document")
	}
}

// TestWriteEncryptedSignedKeepsContentsClear covers the write side: encrypting a
// signed document must leave the signature value in the clear (7.6.2), not
// encipher it into a file no verifier could read.
func TestWriteEncryptedSignedKeepsContentsClear(t *testing.T) {
	cert, key := signtest.CertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var signedBuf bytes.Buffer
	if err := doc.WriteSigned(&signedBuf, cert, key); err != nil {
		t.Fatal(err)
	}
	signed := signedBuf.Bytes()
	sdoc, err := Read(bytes.NewReader(signed), int64(len(signed)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sdoc.SetEncryption("", "owner"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := sdoc.Write(&out); err != nil {
		t.Fatal(err)
	}

	var plain []byte
	for _, iobj := range sdoc.Objects {
		if d, ok := iobj.Value.(*object.Dictionary); ok && d.Get("ByteRange") != nil {
			c, _ := d.Get("Contents").(object.String)
			plain = c.Value
		}
	}
	if len(plain) == 0 {
		t.Fatal("no signature value in the source document")
	}
	if got := rawContentsWindow(t, out.Bytes()); !bytes.Equal(got, plain) {
		t.Errorf("the signature value was encrypted on write: wrote %x…, want %x…", got[:16], plain[:16])
	}
}

// TestEncryptionExemptionIsNarrow guards against the exemption swallowing
// ordinary strings: only a signature dictionary's own /Contents is spared, and a
// dictionary of another /Type is not a signature however it is decorated.
func TestEncryptionExemptionIsNarrow(t *testing.T) {
	cases := []struct {
		name string
		dict func() *object.Dictionary
		want bool
	}{
		{"signature", func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Type", object.Name("Sig"))
			d.Set("ByteRange", object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)})
			d.Set("Contents", object.String{Value: []byte("cms"), IsHex: true})
			return d
		}, true},
		{"doc-timestamp", func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Type", object.Name("DocTimeStamp"))
			d.Set("ByteRange", object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)})
			d.Set("Contents", object.String{Value: []byte("tst"), IsHex: true})
			return d
		}, true},
		{"untyped-signature", func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("ByteRange", object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)})
			d.Set("Contents", object.String{Value: []byte("cms"), IsHex: true})
			return d
		}, true},
		{"annotation-with-byterange", func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Type", object.Name("Annot"))
			d.Set("ByteRange", object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)})
			d.Set("Contents", object.String{Value: []byte("note text")})
			return d
		}, false},
		{"page-contents", func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Type", object.Name("Page"))
			d.Set("Contents", object.IndirectRef{Number: 7})
			return d
		}, false},
		{"no-byterange", func() *object.Dictionary {
			d := &object.Dictionary{}
			d.Set("Type", object.Name("Sig"))
			d.Set("Contents", object.String{Value: []byte("cms"), IsHex: true})
			return d
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crypt.IsSignatureDict(tc.dict()); got != tc.want {
				t.Fatalf("crypt.IsSignatureDict = %v, want %v", got, tc.want)
			}
			// Cross-check the behaviour the predicate gates: /Contents survives
			// decryption exactly when the dictionary is a signature.
			d := tc.dict()
			before, isString := d.Get("Contents").(object.String)
			if !isString {
				return
			}
			rc4EncryptedDoc(t).security.DecryptDictStrings(d, 3, 0)
			after, _ := d.Get("Contents").(object.String)
			if same := bytes.Equal(before.Value, after.Value); same != tc.want {
				t.Fatalf("/Contents preserved = %v, want %v", same, tc.want)
			}
		})
	}
}

// TestTrailerIDNotDecrypted pins the other 7.6.2 exemption that concerns the
// object walk: the trailer's /ID values are not encrypted, and decryptDocument
// must leave them alone. They are safe because the walk covers only doc.Objects
// — including when the trailer is a cross-reference stream dictionary, which is
// skipped as a /Type /XRef stream.
func TestTrailerIDNotDecrypted(t *testing.T) {
	for _, xrefStream := range []bool{false, true} {
		doc := encMatrixDoc(xrefStream, false)
		if err := doc.SetEncryption("", ""); err != nil {
			t.Fatal(err)
		}
		want, _ := doc.Trailer.Get("ID").(object.Array)
		w0, _ := want[0].(object.String)
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Fatal(err)
		}
		enc := buf.Bytes()
		back, err := Read(bytes.NewReader(enc), int64(len(enc)))
		if err != nil {
			t.Fatal(err)
		}
		if back.security == nil {
			t.Fatalf("xref stream %v: the file did not decrypt", xrefStream)
		}
		got, _ := back.Trailer.Get("ID").(object.Array)
		if len(got) != 2 {
			t.Fatalf("xref stream %v: /ID has %d entries", xrefStream, len(got))
		}
		g0, _ := got[0].(object.String)
		if !bytes.Equal(w0.Value, g0.Value) {
			t.Errorf("xref stream %v: /ID changed across the encrypted round-trip: %x -> %x",
				xrefStream, w0.Value, g0.Value)
		}
	}
}
