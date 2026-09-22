package pdf0

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/object"
)

// Fixtures for the encryption tests: a document with every kind of content the
// crypt filters distinguish, and a producer for the legacy (R2–R4) standard
// security handler written from the producer side of ISO 32000-2 Algorithms 2–5,
// independently of the reader in internal/crypt.

// The sentinels are stored uncompressed, so a test can tell from the written
// bytes alone whether each one was enciphered.
const (
	sentinelContent  = "CONTENT-SENTINEL-STREAM"
	sentinelTitle    = "TITLE-SENTINEL-STRING"
	sentinelEmbedded = "EMBEDDED-PAYLOAD-ONE-TYPED"
	sentinelUntyped  = "EMBEDDED-PAYLOAD-TWO-UNTYPED"
	sentinelDocXMP   = "DOCUMENT-LEVEL-XMP-SENTINEL"
	sentinelPageXMP  = "PAGE-LEVEL-XMP-SENTINEL"
)

// Object numbers in cryptTestDoc.
const (
	objCatalog  = 1
	objContent  = 4
	objInfo     = 5
	objDocXMP   = 6
	objEmbedded = 8  // /Type /EmbeddedFile, named by filespec 7
	objUntyped  = 10 // no /Type, named only by filespec 9's /EF
	objPageXMP  = 11
)

func rawStream(dict map[object.Name]object.Object, data string) *object.Stream {
	s := &object.Stream{Data: []byte(data)}
	for k, v := range dict {
		s.Dict.Set(k, v)
	}
	s.Dict.Set("Length", object.Integer(len(data)))
	return s
}

// cryptTestDoc builds a document with a content stream, an Info string, a
// document-level and a page-level metadata stream, and two embedded files —
// one typed /EmbeddedFile, one recognisable only through its file
// specification's /EF. With usedXRefStream, Write packs the dictionaries
// (file specifications included) into an object stream, so a reader can only
// learn which streams are embedded files after materializing it.
func cryptTestDoc(usedXRefStream bool) *Document {
	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "2.0", usedXRefStream: usedXRefStream}
	put := func(num int, v object.Object) { d.Objects[num] = &object.IndirectObject{Number: num, Value: v} }
	dict := func(kv ...any) *object.Dictionary {
		out := &object.Dictionary{}
		for i := 0; i < len(kv); i += 2 {
			out.Set(object.Name(kv[i].(string)), kv[i+1].(object.Object))
		}
		return out
	}
	ref := func(n int) object.IndirectRef { return object.IndirectRef{Number: n} }
	str := func(s string) object.String { return object.String{Value: []byte(s)} }

	names := dict("Names", object.Array{str("a.txt"), ref(7), str("b.txt"), ref(9)})
	put(objCatalog, dict("Type", object.Name("Catalog"), "Pages", ref(2), "Metadata", ref(objDocXMP),
		"Names", dict("EmbeddedFiles", names)))
	put(2, dict("Type", object.Name("Pages"), "Kids", object.Array{ref(3)}, "Count", object.Integer(1)))
	put(3, dict("Type", object.Name("Page"), "Parent", ref(2), "Contents", ref(objContent), "Metadata", ref(objPageXMP),
		"MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)}))
	put(objContent, rawStream(nil, "BT ("+sentinelContent+") Tj ET"))
	put(objInfo, dict("Title", str(sentinelTitle)))
	put(objDocXMP, rawStream(map[object.Name]object.Object{"Type": object.Name("Metadata"), "Subtype": object.Name("XML")}, sentinelDocXMP))
	put(7, dict("Type", object.Name("Filespec"), "F", str("a.txt"), "UF", str("a.txt"), "EF", dict("F", ref(objEmbedded))))
	put(objEmbedded, rawStream(map[object.Name]object.Object{"Type": object.Name("EmbeddedFile")}, sentinelEmbedded))
	put(9, dict("Type", object.Name("Filespec"), "F", str("b.txt"), "UF", str("b.txt"), "EF", dict("F", ref(objUntyped))))
	put(objUntyped, rawStream(nil, sentinelUntyped))
	put(objPageXMP, rawStream(map[object.Name]object.Object{"Type": object.Name("Metadata"), "Subtype": object.Name("XML")}, sentinelPageXMP))
	for i := 20; i < 40; i++ {
		put(i, dict("Type", object.Name("Filler"), "N", object.Integer(i)))
	}
	d.Trailer.Set("Root", ref(objCatalog))
	d.Trailer.Set("Info", ref(objInfo))
	id := []byte("0123456789abcdef")
	d.Trailer.Set("ID", object.Array{object.String{Value: id}, object.String{Value: id}})
	return d
}

// streamText returns a stream object's data as read back.
func streamText(t *testing.T, d *Document, num int) string {
	t.Helper()
	iobj, ok := d.Objects[num]
	if !ok {
		t.Fatalf("object %d missing", num)
	}
	s, ok := iobj.Value.(*object.Stream)
	if !ok {
		t.Fatalf("object %d is a %T, not a stream", num, iobj.Value)
	}
	return string(s.Data)
}

// legacy describes a revision 2–4 standard security handler to produce.
type legacy struct {
	v, r, keyLen    int
	length          int    // top-level /Length in bits; 0 omits it
	user, owner     []byte // encoded, unpadded passwords
	p               int32
	encryptMetadata bool // meaningful at V4
	cf              *object.Dictionary
	stmF, strF, eff object.Name // "" omits the entry
	// truncatedOwnerRounds computes /O the way poppler, MuPDF and qpdf read it (hashing only the
	// first keyLen bytes in each of Algorithm 3's 50 rounds) instead of the
	// spec's full 16 bytes.
	truncatedOwnerRounds bool
}

func padded(pw []byte) []byte { return crypt.PadBytes(pw) }

// dict computes /O (Algorithm 3), the file key (Algorithm 2) and /U
// (Algorithm 4 or 5), and returns the /Encrypt dictionary and the file key.
func (l legacy) dict(id []byte) (*object.Dictionary, []byte) {
	owner := l.owner
	if len(owner) == 0 {
		owner = l.user
	}
	// Algorithm 3.
	h := md5.Sum(padded(owner))
	okey := h[:]
	if l.r >= 3 {
		for i := 0; i < 50; i++ {
			in := okey
			if l.truncatedOwnerRounds {
				in = okey[:l.keyLen]
			}
			s := md5.Sum(in)
			okey = s[:]
		}
	}
	okey = okey[:l.keyLen]
	o := padded(l.user)
	if l.r == 2 {
		c, _ := rc4.NewCipher(okey)
		c.XORKeyStream(o, o)
	} else {
		o = rc4Cascade(okey, o, seq(0, 19))
	}

	// Algorithm 2.
	m := md5.New()
	m.Write(padded(l.user))
	m.Write(o)
	m.Write([]byte{byte(l.p), byte(l.p >> 8), byte(l.p >> 16), byte(l.p >> 24)})
	m.Write(id)
	if l.r >= 4 && !l.encryptMetadata {
		m.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	key := m.Sum(nil)
	if l.r >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(key[:l.keyLen])
			key = s[:]
		}
	}
	key = key[:l.keyLen]

	// Algorithm 4 (R2) or 5 (R3–4).
	var u []byte
	if l.r == 2 {
		u = make([]byte, 32)
		c, _ := rc4.NewCipher(key)
		c.XORKeyStream(u, crypt.PasswordPad)
	} else {
		m := md5.New()
		m.Write(crypt.PasswordPad)
		m.Write(id)
		u = append(rc4Cascade(key, m.Sum(nil), seq(0, 19)), bytes.Repeat([]byte{0x5A}, 16)...)
	}

	d := &object.Dictionary{}
	d.Set("Filter", object.Name("Standard"))
	d.Set("V", object.Integer(l.v))
	d.Set("R", object.Integer(l.r))
	if l.length != 0 {
		d.Set("Length", object.Integer(l.length))
	}
	d.Set("O", object.String{Value: o})
	d.Set("U", object.String{Value: u})
	d.Set("P", object.Integer(l.p))
	if l.v >= 4 {
		if l.cf != nil {
			d.Set("CF", l.cf)
		}
		for k, v := range map[object.Name]object.Name{"StmF": l.stmF, "StrF": l.strF, "EFF": l.eff} {
			if v != "" {
				d.Set(k, v)
			}
		}
		if !l.encryptMetadata {
			d.Set("EncryptMetadata", object.Boolean(false))
		}
	}
	return d, key
}

// cf builds a /CF dictionary from name → (CFM, /Length; 0 omits it).
func cf(filters map[string][2]any) *object.Dictionary {
	out := &object.Dictionary{}
	for name, spec := range filters {
		f := &object.Dictionary{}
		f.Set("CFM", object.Name(spec[0].(string)))
		if n := spec[1].(int); n != 0 {
			f.Set("Length", object.Integer(n))
		}
		f.Set("AuthEvent", object.Name("DocOpen"))
		out.Set(object.Name(name), f)
	}
	return out
}

// encryptLegacy installs l's /Encrypt dictionary on d and writes it with pdf0's
// encryptor, returning the bytes. The key the handler uses comes from Open
// validating the dictionary this test computed, so Open's derivation is
// checked against the producer side above before anything is enciphered. Open
// is given the user password's exact bytes, which every revision accepts, so
// the fixture does not depend on the password preparation under test.
func encryptLegacy(t *testing.T, d *Document, l legacy) []byte {
	t.Helper()
	ids, _ := d.Trailer.Get("ID").(object.Array)
	id, _ := ids[0].(object.String)
	dict, key := l.dict(id.Value)
	encNum := nextObjNum(d)
	d.Objects[encNum] = &object.IndirectObject{Number: encNum, Value: dict}
	d.Trailer.Set("Encrypt", object.IndirectRef{Number: encNum})
	h, err := crypt.Open(d.view(), string(l.user))
	if err != nil || h == nil {
		t.Fatalf("the reader rejects the dictionary this test produced: %v", err)
	}
	if !bytes.Equal(h.FileKey, key) {
		t.Fatalf("reader derived file key %x, producer %x", h.FileKey, key)
	}
	d.security, d.Encrypted = h, true
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

// setOpenEncryption encrypts d with AES-256 and an empty user password: a file
// anyone can open, which SetEncryption refuses to produce. Tests and fuzz seeds
// that must exercise decryption through Read's password-less path use it.
func setOpenEncryption(d *Document) error {
	h, dict, err := crypt.NewAES256("", "test-owner-password")
	if err != nil {
		return err
	}
	return d.installEncryption(h, dict)
}

func readPW(t *testing.T, data []byte, pw string) *Document {
	t.Helper()
	d, err := ReadWithPassword(bytes.NewReader(data), int64(len(data)), pw)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return d
}

// mupdfAuth asks MuPDF (through PyMuPDF) which role each password opens data
// in — "user", "owner" or "no" — skipping when python3 or PyMuPDF is absent.
func mupdfAuth(t *testing.T, data []byte, passwords ...string) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed: the MuPDF interop check cannot run")
	}
	if exec.Command(py, "-c", "import fitz").Run() != nil {
		t.Skip("PyMuPDF not installed: the MuPDF interop check cannot run")
	}
	f := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var in strings.Builder
	for _, pw := range passwords {
		fmt.Fprintf(&in, "%x\n", pw)
	}
	// authenticate returns 0 on failure, and a bit set including 2 (user)
	// or 4 (owner) on success.
	const script = `
import fitz, sys
for line in sys.stdin:
    r = fitz.open(sys.argv[1]).authenticate(bytes.fromhex(line.strip()).decode("utf-8"))
    print("owner" if r & 4 else "user" if r & 2 else "no")
`
	cmd := exec.Command(py, "-c", script, f)
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("PyMuPDF: %v", err)
	}
	return strings.Join(strings.Fields(string(out)), " ")
}

// poppler runs a poppler utility on data, skipping when it is not installed.
func poppler(t *testing.T, tool string, data []byte, args ...string) (string, error) {
	t.Helper()
	path, err := exec.LookPath(tool)
	if err != nil {
		t.Skipf("%s not installed: the poppler interop check cannot run", tool)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, append(args, f)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
