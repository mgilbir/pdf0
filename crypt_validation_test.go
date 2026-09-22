package pdf0

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// v2File is a valid V2/R3 128-bit RC4 file with user password "u".
func v2File(t *testing.T) (*Document, legacy) {
	return cryptTestDoc(false), legacy{v: 2, r: 3, keyLen: 16, length: 128, user: []byte("u"), owner: []byte("o"), p: -4}
}

// lockedBytes writes d as a Locked passthrough — its trailer names the
// /Encrypt dictionary, but no handler is installed — so the dictionary lands
// in the file exactly as mutated.
func lockedBytes(t *testing.T, d *Document) []byte {
	t.Helper()
	d.security, d.Encrypted = nil, true
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("passthrough Write: %v", err)
	}
	return buf.Bytes()
}

// TestMalformedEncryptLocksNotFails is the boundary: every value read from
// /Encrypt is validated, and one ISO 32000-2 7.6 does not define leaves the
// document Locked with the reason recorded. Read never fails, never panics,
// and never derives a key from an unvalidated number (audit 2026-09-22 C59:
// /Length −8, 256 or 4096 sliced a 16-byte digest out of range and Read failed
// with a recovered panic).
func TestMalformedEncryptLocksNotFails(t *testing.T) {
	type mutation func(enc *object.Dictionary)
	set := func(k string, v object.Object) mutation {
		return func(enc *object.Dictionary) { enc.Set(object.Name(k), v) }
	}
	del := func(k string) mutation { return func(enc *object.Dictionary) { enc.Delete(object.Name(k)) } }
	str := func(n int) object.String { return object.String{Value: bytes.Repeat([]byte{1}, n)} }
	v4 := func(enc *object.Dictionary) { // a V4/R4 AESV2 shell to mutate further
		enc.Set("V", object.Integer(4))
		enc.Set("R", object.Integer(4))
		enc.Set("CF", cf(map[string][2]any{"StdCF": {"AESV2", 16}}))
		enc.Set("StmF", object.Name("StdCF"))
		enc.Set("StrF", object.Name("StdCF"))
	}
	then := func(ms ...mutation) mutation {
		return func(enc *object.Dictionary) {
			for _, m := range ms {
				m(enc)
			}
		}
	}
	setCFM := func(cfm string) mutation {
		return func(enc *object.Dictionary) {
			enc.Get("CF").(*object.Dictionary).Get("StdCF").(*object.Dictionary).Set("CFM", object.Name(cfm))
		}
	}
	cases := []struct {
		name   string
		mutate mutation
		want   error
	}{
		{"Length -8", set("Length", object.Integer(-8)), ErrEncryptionMalformed},
		{"Length 256", set("Length", object.Integer(256)), ErrEncryptionMalformed},
		{"Length 4096", set("Length", object.Integer(4096)), ErrEncryptionMalformed},
		{"Length 41", set("Length", object.Integer(41)), ErrEncryptionMalformed},
		{"Length 32", set("Length", object.Integer(32)), ErrEncryptionMalformed},
		{"Length real", set("Length", object.Real(128)), ErrEncryptionMalformed},
		{"V missing", del("V"), ErrEncryptionMalformed},
		{"R missing", del("R"), ErrEncryptionMalformed},
		{"V 3", set("V", object.Integer(3)), ErrEncryptionUnsupported},
		{"V 6", set("V", object.Integer(6)), ErrEncryptionUnsupported},
		{"V 2 R 2", set("R", object.Integer(2)), ErrEncryptionMalformed},
		{"V 2 R 4", set("R", object.Integer(4)), ErrEncryptionMalformed},
		{"V 5 R 5", then(set("V", object.Integer(5)), set("R", object.Integer(5))), ErrEncryptionUnsupported},
		{"R 7", set("R", object.Integer(7)), ErrEncryptionUnsupported},
		{"P missing", del("P"), ErrEncryptionMalformed},
		{"P over 32 bits", set("P", object.Integer(1<<33)), ErrEncryptionMalformed},
		{"P under 32 bits", set("P", object.Integer(-(1 << 32))), ErrEncryptionMalformed},
		{"O short", set("O", str(31)), ErrEncryptionMalformed},
		{"O integer", set("O", object.Integer(7)), ErrEncryptionMalformed},
		{"U missing", del("U"), ErrEncryptionMalformed},
		{"Filter public-key", set("Filter", object.Name("Adobe.PubSec")), ErrEncryptionUnsupported},
		{"Filter missing", del("Filter"), ErrEncryptionMalformed},
		{"V4 CFM None", then(v4, setCFM("None")), ErrEncryptionUnsupported},
		{"V4 CFM AESV3", then(v4, setCFM("AESV3")), ErrEncryptionUnsupported},
		{"V4 CFM unknown", then(v4, setCFM("Rot13")), ErrEncryptionUnsupported},
		{"V4 StmF undefined", then(v4, set("StmF", object.Name("Nope"))), ErrEncryptionMalformed},
		{"V4 StrF undefined", then(v4, set("StrF", object.Name("Nope"))), ErrEncryptionMalformed},
		{"V4 CF missing", then(v4, del("CF")), ErrEncryptionMalformed},
		{"V4 EncryptMetadata integer", then(v4, set("EncryptMetadata", object.Integer(0))), ErrEncryptionMalformed},
		{"V4 RC4 CF Length 3", then(v4, setCFM("V2"), func(enc *object.Dictionary) {
			enc.Get("CF").(*object.Dictionary).Get("StdCF").(*object.Dictionary).Set("Length", object.Integer(3))
		}), ErrEncryptionMalformed},
		{"V5 without Perms", then(set("V", object.Integer(5)), set("R", object.Integer(6)), set("O", str(48)), set("U", str(48)),
			set("OE", str(32)), set("UE", str(32))), ErrEncryptionMalformed},
		{"V5 UE short", then(set("V", object.Integer(5)), set("R", object.Integer(6)), set("O", str(48)), set("U", str(48)),
			set("OE", str(32)), set("UE", str(31)), set("Perms", str(16))), ErrEncryptionMalformed},
	}

	// The control: the unmutated dictionary opens, so each case locks because
	// of its mutation and nothing else.
	d, l := v2File(t)
	ctl := readPW(t, encryptLegacy(t, d, l), "u")
	if ctl.Locked() {
		t.Fatalf("control file is Locked: %v", ctl.LockReason())
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, l := v2File(t)
			ids, _ := d.Trailer.Get("ID").(object.Array)
			id, _ := ids[0].(object.String)
			enc, _ := l.dict(id.Value)
			c.mutate(enc)
			d.Objects[50] = &object.IndirectObject{Number: 50, Value: enc}
			d.Trailer.Set("Encrypt", object.IndirectRef{Number: 50})
			data := lockedBytes(t, d)

			for _, pw := range []string{"", "u", "o"} {
				doc, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), pw)
				if err != nil {
					t.Fatalf("password %q: Read failed instead of leaving the document Locked: %v", pw, err)
				}
				if !doc.Locked() {
					t.Fatalf("password %q: the document is not Locked", pw)
				}
				if reason := doc.LockReason(); !errors.Is(reason, c.want) {
					t.Fatalf("password %q: LockReason = %v, want %v", pw, reason, c.want)
				}
				// A Locked document is still written back verbatim.
				var out bytes.Buffer
				if err := doc.Write(&out); err != nil {
					t.Fatalf("password %q: passthrough Write: %v", pw, err)
				}
			}
		})
	}
}

// TestEncryptNotADictionaryLocks: an /Encrypt that resolves to something other
// than a dictionary is recorded as a malformed-encryption lock, not a failure.
func TestEncryptNotADictionaryLocks(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n")
	offs := []int{}
	for i, body := range []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"42",
	} {
		offs = append(offs, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := buf.Len()
	buf.WriteString("xref\n0 4\n0000000000 65535 f \r\n")
	for _, o := range offs {
		fmt.Fprintf(&buf, "%010d 00000 n \r\n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 4 /Root 1 0 R /Encrypt 3 0 R /ID [<00> <00>] >>\nstartxref\n%d\n%%%%EOF\n", xref)
	data := buf.Bytes()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !doc.Locked() || !errors.Is(doc.LockReason(), ErrEncryptionMalformed) {
		t.Fatalf("Locked = %v, LockReason = %v; want Locked with ErrEncryptionMalformed", doc.Locked(), doc.LockReason())
	}
}

// TestLockReason pins each reason and the nil for a document that is not
// Locked.
func TestLockReason(t *testing.T) {
	d, l := v2File(t)
	data := encryptLegacy(t, d, l)

	if doc := readPW(t, data, "u"); doc.Locked() || doc.LockReason() != nil {
		t.Errorf("right password: Locked = %v, LockReason = %v", doc.Locked(), doc.LockReason())
	}
	for _, pw := range []string{"", "wrong"} {
		doc := readPW(t, data, pw)
		if !doc.Locked() || !errors.Is(doc.LockReason(), ErrWrongPassword) {
			t.Errorf("password %q: Locked = %v, LockReason = %v; want ErrWrongPassword", pw, doc.Locked(), doc.LockReason())
		}
	}
	plain := buildMinimalPDF()
	if doc := readPW(t, plain, ""); doc.Locked() || doc.LockReason() != nil {
		t.Errorf("plaintext file: Locked = %v, LockReason = %v", doc.Locked(), doc.LockReason())
	}
}

// TestV4KeyLength: a V4 file's key is 128 bits unless an RC4 crypt filter says
// otherwise, whether or not the dictionary carries the top-level /Length the
// spec reserves for V2 and V3. pdf0 defaulted a missing /Length to 40 bits
// (audit 2026-09-22 C154), deriving the wrong key for every such AESV2 file.
func TestV4KeyLength(t *testing.T) {
	cases := []struct {
		name   string
		keyLen int
		length int
		cf     *object.Dictionary
	}{
		{"AESV2, no /Length anywhere", 16, 0, cf(map[string][2]any{"StdCF": {"AESV2", 0}})},
		{"AESV2, /Length 16 in the filter", 16, 0, cf(map[string][2]any{"StdCF": {"AESV2", 16}})},
		{"AESV2, top-level /Length 128", 16, 128, cf(map[string][2]any{"StdCF": {"AESV2", 128}})},
		{"RC4, no /Length anywhere", 16, 0, cf(map[string][2]any{"StdCF": {"V2", 0}})},
		{"RC4, filter /Length in bytes", 5, 0, cf(map[string][2]any{"StdCF": {"V2", 5}})},
		{"RC4, filter /Length in bits", 5, 0, cf(map[string][2]any{"StdCF": {"V2", 40}})},
		{"RC4, top-level /Length only", 10, 80, cf(map[string][2]any{"StdCF": {"V2", 0}})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := cryptTestDoc(false)
			l := legacy{v: 4, r: 4, keyLen: c.keyLen, length: c.length, user: []byte("pw"), p: -4,
				encryptMetadata: true, cf: c.cf, stmF: "StdCF", strF: "StdCF"}
			data := encryptLegacy(t, d, l)
			back := readPW(t, data, "pw")
			if back.Locked() {
				t.Fatalf("Locked: %v", back.LockReason())
			}
			if got := streamText(t, back, objContent); !strings.Contains(got, sentinelContent) {
				t.Errorf("content stream = %q", got)
			}
			if bytes.Contains(data, []byte(sentinelContent)) {
				t.Error("the content stream was written in the clear")
			}
		})
	}
}

// TestEmbeddedFilesFollowEFF: an embedded file stream is enciphered with the
// crypt filter /EFF names, not /StmF (ISO 32000-2 Table 20), on both sides.
// pdf0 ignored /EFF (audit 2026-09-22 C61): Acrobat's "encrypt only
// attachments" files — /StmF and /StrF Identity, /EFF a real filter — read
// back with the attachment's ciphertext handed on as its content, with no
// failure recorded.
func TestEmbeddedFilesFollowEFF(t *testing.T) {
	aes := cf(map[string][2]any{"StdCF": {"AESV2", 16}})
	cases := []struct {
		name            string
		stmF, strF, eff object.Name
		clear, secret   []string // sentinels that must / must not appear in the bytes
	}{
		{"attachments only", "Identity", "Identity", "StdCF",
			[]string{sentinelContent, sentinelTitle}, []string{sentinelEmbedded, sentinelUntyped}},
		{"all but attachments", "StdCF", "StdCF", "Identity",
			[]string{sentinelEmbedded, sentinelUntyped}, []string{sentinelContent, sentinelTitle}},
	}
	for _, c := range cases {
		for _, objStm := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/objstm=%v", c.name, objStm), func(t *testing.T) {
				d := cryptTestDoc(objStm)
				l := legacy{v: 4, r: 4, keyLen: 16, length: 128, user: []byte("pw"), owner: []byte("pw"), p: -4,
					encryptMetadata: true, cf: aes, stmF: c.stmF, strF: c.strF, eff: c.eff}
				data := encryptLegacy(t, d, l)
				// With object streams the Info string is packed into a
				// compressed container, so only the uncompressed layout can be
				// checked byte by byte.
				for _, s := range c.clear {
					if objStm && s == sentinelTitle {
						continue
					}
					if !bytes.Contains(data, []byte(s)) {
						t.Errorf("%s was enciphered; its crypt filter is Identity", s)
					}
				}
				for _, s := range c.secret {
					if objStm && s == sentinelTitle {
						continue
					}
					if bytes.Contains(data, []byte(s)) {
						t.Errorf("%s was written in the clear", s)
					}
				}

				back := readPW(t, data, "pw")
				if back.Locked() {
					t.Fatalf("Locked: %v", back.LockReason())
				}
				if f := back.DecryptFailures(); len(f) != 0 {
					t.Fatalf("DecryptFailures = %v", f)
				}
				if got := streamText(t, back, objEmbedded); got != sentinelEmbedded {
					t.Errorf("typed embedded file = %q, want %q", got, sentinelEmbedded)
				}
				if got := streamText(t, back, objUntyped); got != sentinelUntyped {
					t.Errorf("embedded file named only by /EF = %q, want %q", got, sentinelUntyped)
				}
				if got := streamText(t, back, objContent); !strings.Contains(got, sentinelContent) {
					t.Errorf("content stream = %q", got)
				}
			})
		}
	}
}

// TestUnsupportedEFFIsReported: when /EFF names a crypt filter the standard
// handler cannot apply, the rest of the document decrypts and the embedded
// files are reported in DecryptFailures, with empty bodies — never ciphertext
// presented as an attachment.
func TestUnsupportedEFFIsReported(t *testing.T) {
	d := cryptTestDoc(false)
	filters := cf(map[string][2]any{"StdCF": {"AESV2", 16}, "NoneF": {"None", 0}})
	l := legacy{v: 4, r: 4, keyLen: 16, length: 128, user: []byte("pw"), p: -4, encryptMetadata: true,
		cf: filters, stmF: "StdCF", strF: "StdCF", eff: "StdCF"}
	data := encryptLegacy(t, d, l)
	// Point /EFF at the /CFM /None filter in place (same length, so every
	// offset stays valid): the attachments are now under a filter only some
	// other security handler could decrypt.
	patched := bytes.Replace(data, []byte("/EFF /StdCF"), []byte("/EFF /NoneF"), 1)
	if bytes.Equal(patched, data) {
		t.Fatal("fixture: /EFF /StdCF not found in the written file")
	}

	back := readPW(t, patched, "pw")
	if back.Locked() {
		t.Fatalf("an unusable /EFF locked the whole document: %v", back.LockReason())
	}
	if got := fmt.Sprint(back.DecryptFailures()); got != fmt.Sprint([]int{objEmbedded, objUntyped}) {
		t.Errorf("DecryptFailures = %s, want the two embedded files", got)
	}
	for _, n := range []int{objEmbedded, objUntyped} {
		if got := streamText(t, back, n); got != "" {
			t.Errorf("object %d: undecryptable attachment handed on as %q", n, got)
		}
	}
	if got := streamText(t, back, objContent); !strings.Contains(got, sentinelContent) {
		t.Errorf("content stream = %q", got)
	}
	if err := back.Write(&bytes.Buffer{}); err == nil {
		t.Error("Write accepted a document with undecrypted attachments")
	}
	// Re-encrypting does not bring the content back: Write must still refuse
	// rather than encipher the blanks under a new key.
	if err := back.SetEncryption("new", ""); err != nil {
		t.Fatal(err)
	}
	if err := back.Write(&bytes.Buffer{}); err == nil {
		t.Error("SetEncryption cleared the record of undecrypted attachments")
	}
}

// TestStreamCryptFilter: a stream that names its own crypt filter — a /Crypt
// entry in /Filter (ISO 32000-2 7.4.10) — is enciphered with that filter, not
// /StmF, and one naming a filter the handler cannot apply is reported.
func TestStreamCryptFilter(t *testing.T) {
	crypted := func(d *Document, num int, name object.Name) {
		s := d.Objects[num].Value.(*object.Stream)
		s.Dict.Set("Filter", object.Array{object.Name("Crypt")})
		p := &object.Dictionary{}
		p.Set("Type", object.Name("CryptFilterDecodeParms"))
		if name != "" {
			p.Set("Name", name)
		}
		s.Dict.Set("DecodeParms", object.Array{p})
	}
	aes := cf(map[string][2]any{"StdCF": {"AESV2", 16}})

	t.Run("Identity keeps a stream clear under an AESV2 StmF", func(t *testing.T) {
		d := cryptTestDoc(false)
		crypted(d, objUntyped, "") // /Name defaults to Identity
		l := legacy{v: 4, r: 4, keyLen: 16, length: 128, user: []byte("pw"), p: -4, encryptMetadata: true, cf: aes, stmF: "StdCF", strF: "StdCF"}
		data := encryptLegacy(t, d, l)
		if !bytes.Contains(data, []byte(sentinelUntyped)) {
			t.Error("an Identity crypt-filter stream was enciphered")
		}
		if bytes.Contains(data, []byte(sentinelContent)) {
			t.Error("the content stream was written in the clear")
		}
		back := readPW(t, data, "pw")
		if got := streamText(t, back, objUntyped); got != sentinelUntyped {
			t.Errorf("Identity stream read back as %q", got)
		}
		dec, err := back.StreamData(back.Objects[objUntyped].Value.(*object.Stream))
		if err != nil || string(dec) != sentinelUntyped {
			t.Errorf("StreamData = %q, %v; the Crypt filter must decode as a no-op after decryption", dec, err)
		}
	})

	t.Run("a named filter enciphers a stream under an Identity StmF", func(t *testing.T) {
		d := cryptTestDoc(false)
		crypted(d, objUntyped, "StdCF")
		l := legacy{v: 4, r: 4, keyLen: 16, length: 128, user: []byte("pw"), p: -4, encryptMetadata: true, cf: aes, stmF: "Identity", strF: "Identity"}
		data := encryptLegacy(t, d, l)
		if bytes.Contains(data, []byte(sentinelUntyped)) {
			t.Error("a stream naming StdCF was written in the clear")
		}
		if !bytes.Contains(data, []byte(sentinelContent)) {
			t.Error("an Identity-StmF stream was enciphered")
		}
		back := readPW(t, data, "pw")
		if got := streamText(t, back, objUntyped); got != sentinelUntyped {
			t.Errorf("StdCF stream read back as %q", got)
		}
	})

	t.Run("an undefined filter is reported", func(t *testing.T) {
		d := cryptTestDoc(false)
		crypted(d, objUntyped, "StdCF")
		l := legacy{v: 4, r: 4, keyLen: 16, length: 128, user: []byte("pw"), p: -4, encryptMetadata: true, cf: aes, stmF: "StdCF", strF: "StdCF"}
		data := encryptLegacy(t, d, l)
		patched := bytes.Replace(data, []byte("/Name /StdCF"), []byte("/Name /Nope1"), 1)
		if bytes.Equal(patched, data) {
			t.Fatal("fixture: /Name /StdCF not found")
		}
		back := readPW(t, patched, "pw")
		if back.Locked() {
			t.Fatalf("Locked: %v", back.LockReason())
		}
		if got := fmt.Sprint(back.DecryptFailures()); got != fmt.Sprint([]int{objUntyped}) {
			t.Errorf("DecryptFailures = %s, want [%d]", got, objUntyped)
		}
		if got := streamText(t, back, objUntyped); got != "" {
			t.Errorf("undecryptable stream handed on as %q", got)
		}
	})
}

// TestOnlyDocumentMetadataIsExempt: /EncryptMetadata false leaves the
// document-level metadata stream in the clear (ISO 32000-2 Table 21, Algorithm
// 2 NOTE 1: "pertains only to document-level XMP metadata"); every other
// /Type /Metadata stream is encrypted like any stream. pdf0 exempted every
// /Type /Metadata stream, on both sides, so a page's metadata was written in
// the clear and a conforming producer's read back as ciphertext.
func TestOnlyDocumentMetadataIsExempt(t *testing.T) {
	for _, objStm := range []bool{false, true} {
		t.Run(fmt.Sprintf("objstm=%v", objStm), func(t *testing.T) {
			d := cryptTestDoc(objStm)
			l := legacy{v: 4, r: 4, keyLen: 16, length: 128, user: []byte("pw"), p: -4, encryptMetadata: false,
				cf: cf(map[string][2]any{"StdCF": {"AESV2", 16}}), stmF: "StdCF", strF: "StdCF"}
			data := encryptLegacy(t, d, l)
			if !bytes.Contains(data, []byte(sentinelDocXMP)) {
				t.Error("the document metadata was enciphered despite /EncryptMetadata false")
			}
			if bytes.Contains(data, []byte(sentinelPageXMP)) {
				t.Error("a page's metadata stream was written in the clear")
			}
			back := readPW(t, data, "pw")
			if got := streamText(t, back, objDocXMP); got != sentinelDocXMP {
				t.Errorf("document metadata = %q", got)
			}
			if got := streamText(t, back, objPageXMP); got != sentinelPageXMP {
				t.Errorf("page metadata = %q", got)
			}
		})
	}
}
