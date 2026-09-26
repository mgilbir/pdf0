package pdf0

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// aesFile encrypts cryptTestDoc with SetEncryption and returns the bytes.
func aesFile(t *testing.T, user, owner string) []byte {
	t.Helper()
	d := cryptTestDoc(false)
	if err := d.SetEncryption(user, owner); err != nil {
		t.Fatalf("SetEncryption: %v", err)
	}
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func opens(t *testing.T, data []byte, pw string) bool {
	t.Helper()
	d := readPW(t, data, pw)
	if d.Locked() {
		return false
	}
	if got := streamText(t, d, objContent); !strings.Contains(got, sentinelContent) {
		t.Fatalf("password %q opened the file but the content is %q", pw, got)
	}
	return true
}

// TestEmptyOwnerPasswordDoesNotOpen is audit 2026-09-22 C23: SetEncryption
// with an empty owner password wrote "" as the owner password, and since every
// reader tries the empty password, anyone could open the file. The owner
// password is now random; only the user password opens it.
func TestEmptyOwnerPasswordDoesNotOpen(t *testing.T) {
	data := aesFile(t, "user-secret", "")
	if bytes.Contains(data, []byte(sentinelTitle)) || bytes.Contains(data, []byte(sentinelContent)) {
		t.Fatal("plaintext in the encrypted output")
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Locked() {
		t.Fatal("Read with no password opened a file whose user password is set")
	}
	if !errors.Is(doc.LockReason(), ErrWrongPassword) {
		t.Errorf("LockReason = %v, want ErrWrongPassword", doc.LockReason())
	}
	if !opens(t, data, "user-secret") {
		t.Error("the user password does not open the file")
	}

	// Poppler agrees: no password fails, the user password succeeds.
	if out, err := poppler(t, "pdfinfo", data); err == nil {
		t.Errorf("pdfinfo opened the file with no password:\n%s", out)
	}
	if out, err := poppler(t, "pdfinfo", data, "-upw", "user-secret"); err != nil {
		t.Errorf("pdfinfo -upw: %v\n%s", err, out)
	}
}

// TestSetEncryptionRefusesEmptyUserPassword: an empty user password opens for
// anyone, and SetEncryption grants every permission, so the file would be
// encrypted but unprotected.
func TestSetEncryptionRefusesEmptyUserPassword(t *testing.T) {
	for _, owner := range []string{"", "owner"} {
		d := cryptTestDoc(false)
		if err := d.SetEncryption("", owner); err == nil {
			t.Errorf("SetEncryption(\"\", %q) succeeded", owner)
		}
		if d.Encrypted || d.Trailer.Get("Encrypt") != nil {
			t.Errorf("a refused SetEncryption(\"\", %q) changed the document", owner)
		}
	}
}

// TestR6PasswordPreparation is audit 2026-09-22 C58: an R6 password is
// SASLprep'd, UTF-8 encoded and truncated to 127 bytes (ISO 32000-2 Algorithm
// 2.A steps a–b) on both sides. pdf0 hashed the raw bytes, untruncated.
func TestR6PasswordPreparation(t *testing.T) {
	long := strings.Repeat("a", 200)
	cases := []struct {
		name, set string
		open      []string // each opens the file
		closed    []string // none opens it
	}{
		{"200 bytes", long, []string{long, long[:127], long[:127] + "zzz"}, []string{long[:126]}},
		{"NFKC: decomposed set, composed open", "pa\u0308sswort", []string{"p\u00E4sswort", "pa\u0308sswort"}, []string{"passwort"}},
		{"NFKC: composed set, decomposed open", "p\u00E4sswort", []string{"pa\u0308sswort"}, nil},
		{"non-ASCII space maps to SPACE", "open\u00A0sesame", []string{"open sesame", "open\u3000sesame"}, []string{"opensesame"}},
		{"soft hyphen maps to nothing", "pass\u00ADword", []string{"password"}, nil},
		{"compatibility ligature", "\uFB01le", []string{"file"}, nil},
		{"multi-byte UTF-8 cut at 127 bytes", strings.Repeat("\u00E4", 100), []string{strings.Repeat("\u00E4", 64)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := aesFile(t, c.set, "owner-pw")
			for _, pw := range c.open {
				if !opens(t, data, pw) {
					t.Errorf("%+q does not open a file whose password was set as %+q", pw, c.set)
				}
			}
			for _, pw := range c.closed {
				if opens(t, data, pw) {
					t.Errorf("%+q opens a file whose password was set as %+q", pw, c.set)
				}
			}
		})
	}
}

// TestR6ProhibitedPasswordRefused: a password SASLprep prohibits cannot be
// reproduced by a conforming reader, so it is refused rather than set.
func TestR6ProhibitedPasswordRefused(t *testing.T) {
	for _, c := range []struct{ user, owner string }{
		{"bell\u0007", "owner"},
		{"user", "private\uE000"},
		{"x\u0221", "owner"}, // unassigned in Unicode 3.2: prohibited in a stored string
		{"\u05D0a", "owner"}, // right-to-left mixed with left-to-right
	} {
		d := cryptTestDoc(false)
		if err := d.SetEncryption(c.user, c.owner); err == nil {
			t.Errorf("SetEncryption(%+q, %+q) accepted a prohibited password", c.user, c.owner)
		}
	}
}

// TestR6PasswordInterop checks pdf0's R6 files against two independent
// implementations of Algorithm 2.A. MuPDF truncates to 127 bytes as the spec
// requires, so it is the oracle for long passwords: it must open pdf0's file
// with the 200-byte password and its 127-byte prefix, and not with 126 bytes.
// Poppler's command-line tools keep only the first 32 characters of a password
// (a fixed argument buffer), so they can check short non-ASCII passwords only —
// which is also why the audit's pdfinfo repro rejected a 200-byte password.
func TestR6PasswordInterop(t *testing.T) {
	long := strings.Repeat("a", 200)
	if got := mupdfAuth(t, aesFile(t, long, "owner-pw"), long, long[:127], long[:126], "owner-pw"); got != "user user no owner" {
		t.Errorf("MuPDF on a 200-byte password (full, 127, 126 bytes, owner): %s", got)
	}
	for _, pw := range []string{"p\u00E4sswort", "\u4E2D\u6587\u5BC6\u7801"} {
		data := aesFile(t, pw, "owner-pw")
		if out, err := poppler(t, "pdfinfo", data, "-upw", pw); err != nil {
			t.Errorf("pdfinfo -upw %+q: %v\n%s", pw, err, out)
		}
		if out, err := poppler(t, "pdfinfo", data, "-opw", "owner-pw"); err != nil {
			t.Errorf("pdfinfo -opw (user %+q): %v\n%s", pw, err, out)
		}
	}
}

// TestR4PasswordPDFDocEncoding: at revisions 2–4 a password is converted to
// PDFDocEncoding (ISO 32000-2 Algorithm 2 step a). pdf0 hashed its UTF-8 bytes,
// so a non-ASCII password set by a conforming producer never matched. Files
// hashed with the raw UTF-8 bytes (what pdf0 and other non-transcoding
// producers wrote) still open.
func TestR4PasswordPDFDocEncoding(t *testing.T) {
	cases := []struct {
		name    string
		encoded []byte // the bytes the producer hashed
		typed   string // what the user types
	}{
		{"PDFDocEncoding", []byte{'c', 'a', 'f', 0xE9, ' ', 0xA0}, "caf\u00E9 \u20AC"},
		{"PDFDocEncoding specials", []byte{0x80, 0x93, 0x18}, "\u2022\uFB01\u02D8"},
		{"raw UTF-8 fallback", []byte("caf\u00E9"), "caf\u00E9"},
		{"unencodable falls back to UTF-8", []byte("\u4E2D\u6587"), "\u4E2D\u6587"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, l := v2File(t)
			l.user, l.owner = c.encoded, []byte("owner")
			data := encryptLegacy(t, d, l)
			if !opens(t, data, c.typed) {
				t.Errorf("%+q does not open a file whose password bytes are % X", c.typed, c.encoded)
			}
			if opens(t, data, "") || opens(t, data, "caf") {
				t.Error("a wrong password opened the file")
			}
			if c.name == "PDFDocEncoding" {
				// MuPDF transcodes the typed password the same way.
				if got := mupdfAuth(t, data, c.typed); got != "user" {
					t.Errorf("MuPDF: %s", got)
				}
			}
		})
	}
}

// TestR3OwnerPasswordShortKey: Algorithm 3 step c is read two ways. Its text
// re-hashes the whole 16-byte digest in each of its 50 rounds; poppler, MuPDF
// and qpdf re-hash only the first keyLen bytes, as pdf0 did. They agree for
// 128-bit keys; below that, a file written to the letter of the spec had an
// owner password pdf0 could not open. Both readings are now tried, the
// readers' first, and poppler and MuPDF confirm the fixture's reading.
func TestR3OwnerPasswordShortKey(t *testing.T) {
	for _, c := range []struct {
		name      string
		v, keyLen int
		length    int
		truncated bool
	}{
		{"V1/R3 40-bit, spec rounds", 1, 5, 0, false},
		{"V2/R3 40-bit, spec rounds", 2, 5, 40, false},
		{"V2/R3 88-bit, spec rounds", 2, 11, 88, false},
		{"V2/R3 40-bit, qpdf rounds", 2, 5, 40, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := cryptTestDoc(false)
			l := legacy{v: c.v, r: 3, keyLen: c.keyLen, length: c.length, user: []byte("user"), owner: []byte("owner"),
				p: -3904, truncatedOwnerRounds: c.truncated}
			data := encryptLegacy(t, d, l)
			if !opens(t, data, "owner") {
				t.Error("the owner password does not open the file")
			}
			if !opens(t, data, "user") {
				t.Error("the user password does not open the file")
			}
			if c.truncated {
				if out, err := poppler(t, "pdfinfo", data, "-opw", "owner"); err != nil {
					t.Errorf("pdfinfo -opw: %v\n%s", err, out)
				}
				if got := mupdfAuth(t, data, "owner", "user"); got != "owner user" {
					t.Errorf("MuPDF (owner, user): %s", got)
				}
			}
		})
	}
}

// TestSetEncryptionReplacesOldEncryption: SetEncryption on a document Read
// decrypted replaced the trailer's /Encrypt but left the old dictionary in
// Objects, where Write encrypted it as ordinary content and emitted it as an
// orphan (audit 2026-09-22 C154). RemoveEncryption had the sibling defect for
// an indirect /CF. Both now remove what only the old dictionary referenced,
// and strip per-stream /Crypt filters that named its crypt filters.
func TestSetEncryptionReplacesOldEncryption(t *testing.T) {
	first := func(t *testing.T) (*Document, int, int) {
		d := cryptTestDoc(false)
		crypted := d.Objects[objUntyped].Value.(*object.Stream)
		crypted.Dict.Set("Filter", object.Name("Crypt"))
		if err := d.SetEncryption("first", ""); err != nil {
			t.Fatal(err)
		}
		makeIndirect(d, "CF")
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		back := readPW(t, buf.Bytes(), "first")
		if back.Locked() {
			t.Fatalf("Locked: %v", back.LockReason())
		}
		encRef, _ := back.Trailer.Get("Encrypt").(object.IndirectRef)
		cfRef, _ := back.ResolveDict(encRef).Get("CF").(object.IndirectRef)
		if encRef.Number == 0 || cfRef.Number == 0 {
			t.Fatal("fixture: expected an indirect /Encrypt with an indirect /CF")
		}
		return back, encRef.Number, cfRef.Number
	}

	t.Run("SetEncryption", func(t *testing.T) {
		d, oldEnc, oldCF := first(t)
		nContent := len(d.Objects) - 2
		if err := d.SetEncryption("second", ""); err != nil {
			t.Fatal(err)
		}
		newEnc := d.Trailer.Get("Encrypt").(object.IndirectRef).Number
		for _, n := range []int{oldEnc, oldCF} {
			if n != newEnc && d.Objects[n] != nil {
				t.Errorf("object %d of the old /Encrypt is still in Objects", n)
			}
		}
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		if n := bytes.Count(data, []byte("/Filter /Standard")); n != 1 {
			t.Errorf("%d /Encrypt dictionaries written, want 1", n)
		}
		if bytes.Contains(data, []byte("/Crypt")) {
			t.Error("a /Crypt filter naming the old crypt filters survived")
		}
		back := readPW(t, data, "second")
		if back.Locked() {
			t.Fatalf("new password: %v", back.LockReason())
		}
		if len(back.Objects) != nContent+1 {
			t.Errorf("read back %d objects, want %d content objects and the new /Encrypt", len(back.Objects), nContent)
		}
		if got := streamText(t, back, objUntyped); got != sentinelUntyped {
			t.Errorf("formerly /Crypt stream = %q", got)
		}
		if !readPW(t, data, "first").Locked() {
			t.Error("the old password still opens the file")
		}
	})

	t.Run("RemoveEncryption", func(t *testing.T) {
		d, oldEnc, oldCF := first(t)
		d.RemoveEncryption()
		for _, n := range []int{oldEnc, oldCF} {
			if d.Objects[n] != nil {
				t.Errorf("object %d of the removed /Encrypt is still in Objects", n)
			}
		}
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		for _, s := range []string{"/Standard", "/StdCF", "/Crypt", "/Encrypt"} {
			if bytes.Contains(data, []byte(s)) {
				t.Errorf("%s survived RemoveEncryption", s)
			}
		}
		back := readPW(t, data, "")
		if back.Encrypted || !strings.Contains(streamText(t, back, objContent), sentinelContent) {
			t.Error("the decrypted output does not read back in the clear")
		}
	})
}

// TestR6PermsValidated is ISO 32000-2 Algorithm 13 (audit 2026-09-22 C154).
// /UE is not authenticated — a password matching /U decrypts whatever /UE
// holds — so /Perms, encrypted under the file key with the marker "adb", is the
// only proof the recovered key is right. Without it the document stays Locked;
// with it intact but disagreeing with /P, the document decrypts and the
// mismatch is a warning.
func TestR6PermsValidated(t *testing.T) {
	build := func(t *testing.T, mutate func(enc *object.Dictionary)) []byte {
		d := cryptTestDoc(false)
		if err := d.SetEncryption("pw", "owner"); err != nil {
			t.Fatal(err)
		}
		mutate(d.ResolveDict(d.Trailer.Get("Encrypt")))
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	flip := func(key object.Name) func(*object.Dictionary) {
		return func(enc *object.Dictionary) {
			s := enc.Get(key).(object.String)
			v := append([]byte(nil), s.Value...)
			v[0] ^= 0x01
			enc.Set(key, object.String{Value: v})
		}
	}

	t.Run("intact", func(t *testing.T) {
		doc := readPW(t, build(t, func(*object.Dictionary) {}), "pw")
		if doc.Locked() || len(doc.EncryptionWarnings()) != 0 {
			t.Fatalf("Locked = %v (%v), warnings %v", doc.Locked(), doc.LockReason(), doc.EncryptionWarnings())
		}
	})
	for _, key := range []object.Name{"Perms", "UE", "OE"} {
		t.Run("corrupt "+string(key), func(t *testing.T) {
			data := build(t, flip(key))
			pw := "pw"
			if key == "OE" {
				pw = "owner"
			}
			doc := readPW(t, data, pw)
			if !doc.Locked() {
				t.Fatalf("a corrupt /%s decrypted the document with an unproven key", key)
			}
			if !errors.Is(doc.LockReason(), ErrEncryptionMalformed) {
				t.Errorf("LockReason = %v, want ErrEncryptionMalformed", doc.LockReason())
			}
		})
	}
	t.Run("P disagrees", func(t *testing.T) {
		doc := readPW(t, build(t, func(enc *object.Dictionary) { enc.Set("P", object.Integer(-3904)) }), "pw")
		if doc.Locked() {
			t.Fatalf("Locked: %v", doc.LockReason())
		}
		w := doc.EncryptionWarnings()
		if len(w) != 1 || !errors.Is(w[0], ErrPermsMismatch) {
			t.Errorf("EncryptionWarnings = %v, want one ErrPermsMismatch", w)
		}
	})
	t.Run("EncryptMetadata disagrees", func(t *testing.T) {
		doc := readPW(t, build(t, func(enc *object.Dictionary) { enc.Set("EncryptMetadata", object.Boolean(false)) }), "pw")
		w := doc.EncryptionWarnings()
		if len(w) != 1 || !errors.Is(w[0], ErrPermsMismatch) {
			t.Errorf("EncryptionWarnings = %v, want one ErrPermsMismatch", w)
		}
	})
}

// TestFuzzEncryptSeedDecrypts keeps the fuzz corpus honest: its encrypted seeds
// must open through Read's password-less path, or FuzzRead never reaches the
// decryption code and silently fuzzes a plaintext file instead.
func TestFuzzEncryptSeedDecrypts(t *testing.T) {
	for _, base := range [][]byte{buildMinimalPDF(), buildXRefStreamPDF()} {
		seed := encryptSeed(base)
		doc, err := Read(bytes.NewReader(seed), int64(len(seed)))
		if err != nil {
			t.Fatal(err)
		}
		if !doc.Encrypted || doc.Locked() {
			t.Fatalf("seed Encrypted = %v, Locked = %v (%v)", doc.Encrypted, doc.Locked(), doc.LockReason())
		}
	}
}
