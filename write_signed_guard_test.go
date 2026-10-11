package pdf0

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/crypt"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/sign"
)

// The guard on whole-document writes of a signed document (follow-up to docs
// audit 2026-10-11): Write, WriteContext, Save, SaveContext and SetEncryption
// refuse a document whose source file carries signatures, because every one
// of them rewrites the bytes those signatures cover; AllowInvalidatingSignatures
// is the consent; the incremental writers need none.

// signedFixture is one signed file and how to open it.
type signedFixture struct {
	name     string
	data     []byte
	password string // "" reads with Read
	locked   bool   // read without the password, so the document is Locked
}

// signedFixtures returns every shape of signed file the guard must catch: a
// signature written by a full rewrite, one appended incrementally with a
// signature time-stamp, a document time-stamp alone, and a signed file that
// is also encrypted — decrypted, and Locked.
func signedFixtures(t *testing.T) []signedFixture {
	t.Helper()
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	var incr, stamp bytes.Buffer
	if err := readBytes(t, buildMinimalPDF()).WriteSignedIncremental(&incr, cert, key, WithSignatureTimestamp(tsaCert, tsaKey)); err != nil {
		t.Fatalf("WriteSignedIncremental: %v", err)
	}
	if err := readBytes(t, buildMinimalPDF()).WriteArchivalTimestamp(&stamp, ValidationData{}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	enc := signedEncryptedPDF(t)
	return []signedFixture{
		{name: "signature by full rewrite", data: signMinimal(t)},
		{name: "incremental signature with time-stamp", data: incr.Bytes()},
		{name: "document time-stamp only", data: stamp.Bytes()},
		{name: "signed and encrypted, decrypted", data: enc, password: "user"},
		{name: "signed and encrypted, Locked", data: enc, locked: true},
	}
}

func (f signedFixture) open(t *testing.T) *Document {
	t.Helper()
	var doc *Document
	var err error
	if f.password != "" {
		doc, err = ReadWithPassword(bytes.NewReader(f.data), int64(len(f.data)), f.password)
	} else {
		doc, err = Read(bytes.NewReader(f.data), int64(len(f.data)))
	}
	if err != nil {
		t.Fatalf("%s: Read: %v", f.name, err)
	}
	if doc.Locked() != f.locked {
		t.Fatalf("%s: Locked = %v, want %v", f.name, doc.Locked(), f.locked)
	}
	return doc
}

// signedEncryptedPDF is a file that is both encrypted and validly signed,
// which no public writer produces (the signing writers refuse an encrypted
// document): the signature field is added, the document encrypted and
// serialised, and the placeholders filled — the signature dictionary's
// /Contents is never encrypted, and /ByteRange holds integers.
func signedEncryptedPDF(t *testing.T) []byte {
	t.Helper()
	cert, key := signtest.CertKey(t)
	withField, _, err := withSignatureField(readBytes(t, buildMinimalPDF()))
	if err != nil {
		t.Fatal(err)
	}
	h, dict, err := crypt.NewAES256("user", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := withField.installEncryption(h, dict); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := withField.write(core.Canceler{}, &buf); err != nil {
		t.Fatal(err)
	}
	out, err := patchSignature(buf.Bytes(), cert, key, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ReadWithPassword(bytes.NewReader(out), int64(len(out)), "user")
	if err != nil {
		t.Fatal(err)
	}
	res := verifySigs(t, doc, sign.VerifyOptions{})
	if !doc.Encrypted || len(res) != 1 || !res[0].DocumentUnmodified() {
		t.Fatalf("the fixture is not a valid signed encrypted file: encrypted=%v results=%+v", doc.Encrypted, res)
	}
	return out
}

// wholeWriters are the writers that regenerate the whole file.
var wholeWriters = []struct {
	name  string
	write func(d *Document, w *bytes.Buffer) error
}{
	{"Write", func(d *Document, w *bytes.Buffer) error { return d.Write(w) }},
	{"WriteContext", func(d *Document, w *bytes.Buffer) error { return d.WriteContext(context.Background(), w) }},
	{"Save", func(d *Document, w *bytes.Buffer) error { return d.Save(w) }},
	{"SaveContext", func(d *Document, w *bytes.Buffer) error { return d.SaveContext(context.Background(), w) }},
}

// editCatalog sets /Lang on the catalog: an ordinary object edit.
func editCatalog(t *testing.T, d *Document) int {
	t.Helper()
	num := object.RefNum(d.Trailer.Get("Root"))
	cat, ok := d.Objects[num].Value.(*object.Dictionary)
	if !ok {
		t.Fatal("no catalog")
	}
	c := cat.Clone()
	c.Set("Lang", object.String{Value: []byte("en")})
	d.Objects[num].Value = c
	return num
}

// mutations are what a caller does before writing. Each returns false when it
// does not apply to the document (Repair refuses a Locked one).
var mutations = []struct {
	name   string
	mutate func(t *testing.T, d *Document) bool
}{
	{"no change", func(t *testing.T, d *Document) bool { return true }},
	{"RemoveEncryption", func(t *testing.T, d *Document) bool { d.RemoveEncryption(); return true }},
	{"Repair", func(t *testing.T, d *Document) bool {
		if d.Locked() {
			return false
		}
		if _, err := d.Repair(pdfa.PDFA2b); err != nil {
			t.Fatalf("Repair: %v", err)
		}
		return true
	}},
	{"object edit", func(t *testing.T, d *Document) bool {
		if d.Locked() {
			return false
		}
		editCatalog(t, d)
		return true
	}},
}

// TestWholeWritesRefuseSignedDocument: every whole-document writer, after
// every kind of change (none included), refuses a document whose file carries
// signatures, wrapping ErrWouldInvalidateSignatures, and writes nothing.
func TestWholeWritesRefuseSignedDocument(t *testing.T) {
	for _, f := range signedFixtures(t) {
		for _, m := range mutations {
			for _, w := range wholeWriters {
				t.Run(f.name+"/"+m.name+"/"+w.name, func(t *testing.T) {
					d := f.open(t)
					if !m.mutate(t, d) {
						t.Skip("does not apply to this document")
					}
					var out bytes.Buffer
					err := w.write(d, &out)
					if !errors.Is(err, ErrWouldInvalidateSignatures) {
						t.Fatalf("%s = %v, want ErrWouldInvalidateSignatures", w.name, err)
					}
					if out.Len() != 0 {
						t.Errorf("a refused %s wrote %d bytes", w.name, out.Len())
					}
				})
			}
		}
	}
}

// TestRefusalNamesTheWayOut: the error says what to do, and does not offer an
// incremental update to a document that cannot have one (an encrypted one).
func TestRefusalNamesTheWayOut(t *testing.T) {
	for _, f := range signedFixtures(t) {
		err := f.open(t).Write(&bytes.Buffer{})
		if err == nil {
			t.Fatalf("%s: Write succeeded", f.name)
		}
		msg := err.Error()
		if !strings.Contains(msg, "AllowInvalidatingSignatures") {
			t.Errorf("%s: the error does not name the opt-in: %s", f.name, msg)
		}
		encrypted := f.password != "" || f.locked
		if got := strings.Contains(msg, "WriteIncremental"); got == encrypted {
			t.Errorf("%s: the error names WriteIncremental = %v, want %v: %s", f.name, got, !encrypted, msg)
		}
	}
}

// TestSetEncryptionRefusesSignedDocument: encryption cannot be added by an
// incremental update, so SetEncryption refuses a signed document up front and
// changes nothing; the Write after it is refused too.
func TestSetEncryptionRefusesSignedDocument(t *testing.T) {
	for _, f := range signedFixtures(t) {
		if f.locked {
			continue // SetEncryption refuses a Locked document for its own reason
		}
		t.Run(f.name, func(t *testing.T) {
			d := f.open(t)
			before := d.Trailer.Get("Encrypt")
			wasEncrypted, handler := d.Encrypted, d.security
			err := d.SetEncryption("new user", "new owner")
			if !errors.Is(err, ErrWouldInvalidateSignatures) {
				t.Fatalf("SetEncryption = %v, want ErrWouldInvalidateSignatures", err)
			}
			if !object.Equal(d.Trailer.Get("Encrypt"), before) || d.Encrypted != wasEncrypted || d.security != handler {
				t.Error("a refused SetEncryption changed the document's encryption")
			}
			var out bytes.Buffer
			if err := d.Write(&out); !errors.Is(err, ErrWouldInvalidateSignatures) || out.Len() != 0 {
				t.Errorf("Write after a refused SetEncryption = %v (%d bytes), want ErrWouldInvalidateSignatures", err, out.Len())
			}
		})
	}
}

// TestAllowInvalidatingSignatures: with the consent, every refused path
// writes, and what it writes carries the old signature dictionaries, none of
// which verifies — which is exactly what was consented to. Encrypting a signed
// document round-trips.
func TestAllowInvalidatingSignatures(t *testing.T) {
	for _, f := range signedFixtures(t) {
		for _, w := range wholeWriters {
			t.Run(f.name+"/"+w.name, func(t *testing.T) {
				d := f.open(t)
				d.AllowInvalidatingSignatures()
				var out bytes.Buffer
				if err := w.write(d, &out); err != nil {
					t.Fatalf("%s with consent: %v", w.name, err)
				}
				back, err := ReadWithPassword(bytes.NewReader(out.Bytes()), int64(out.Len()), "user")
				if err != nil {
					t.Fatalf("read back: %v", err)
				}
				if f.locked {
					return // the ciphertext passthrough: nothing to verify without the password
				}
				res := verifySigs(t, back, sign.VerifyOptions{})
				if len(res) == 0 {
					t.Fatal("the rewritten file lost its signature dictionaries")
				}
				for _, r := range res {
					if r.Valid {
						t.Errorf("a signature still verifies after a whole rewrite: %+v", r)
					}
				}
			})
		}
		if f.locked {
			continue
		}
		t.Run(f.name+"/SetEncryption+Write", func(t *testing.T) {
			d := f.open(t)
			d.AllowInvalidatingSignatures()
			if err := d.SetEncryption("new user", "new owner"); err != nil {
				t.Fatalf("SetEncryption with consent: %v", err)
			}
			var out bytes.Buffer
			if err := d.Write(&out); err != nil {
				t.Fatalf("Write: %v", err)
			}
			back, err := ReadWithPassword(bytes.NewReader(out.Bytes()), int64(out.Len()), "new user")
			if err != nil || back.Locked() || !back.Encrypted {
				t.Fatalf("the encrypted output does not open with its password: err=%v locked=%v", err, back != nil && back.Locked())
			}
			for _, r := range verifySigs(t, back, sign.VerifyOptions{}) {
				if r.Valid {
					t.Errorf("a signature verifies in the re-encrypted file: %+v", r)
				}
			}
		})
	}
}

// TestSignaturePreservingWritersNeedNoConsent: the incremental writers keep
// the file's bytes, so they are not refused, and every earlier signature
// still verifies after them.
func TestSignaturePreservingWritersNeedNoConsent(t *testing.T) {
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	writers := []struct {
		name  string
		write func(d *Document, w *bytes.Buffer) error
	}{
		{"WriteIncremental", func(d *Document, w *bytes.Buffer) error {
			ref := d.Add(object.Integer(1))
			return d.WriteIncremental(w, []int{ref.Number})
		}},
		{"WriteSignedIncremental", func(d *Document, w *bytes.Buffer) error {
			return d.WriteSignedIncremental(w, cert, key)
		}},
		{"WriteArchivalTimestamp", func(d *Document, w *bytes.Buffer) error {
			return d.WriteArchivalTimestamp(w, ValidationData{}, tsaCert, tsaKey)
		}},
	}
	for _, f := range signedFixtures(t) {
		if f.password != "" || f.locked {
			continue // the incremental writers refuse an encrypted document
		}
		for _, w := range writers {
			t.Run(f.name+"/"+w.name, func(t *testing.T) {
				d := f.open(t)
				earlier := len(verifySigs(t, d, sign.VerifyOptions{}))
				var out bytes.Buffer
				if err := w.write(d, &out); err != nil {
					t.Fatalf("%s: %v", w.name, err)
				}
				res := verifySigs(t, readBytes(t, out.Bytes()), sign.VerifyOptions{})
				if len(res) < earlier {
					t.Fatalf("%d signatures after %s, want at least %d", len(res), w.name, earlier)
				}
				for _, r := range res {
					if !r.Valid {
						t.Errorf("a signature does not verify after %s: %+v", w.name, r)
					}
				}
			})
		}
	}
}

// TestWriteOfDocumentWithNothingToInvalidate: a document read from an unsigned
// file, or built in memory — even with a signature dictionary added by hand,
// which covers no bytes of any source — writes as before.
func TestWriteOfDocumentWithNothingToInvalidate(t *testing.T) {
	sigDict := func() *object.Dictionary {
		return object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Sig")},
			object.Entry{Key: "ByteRange", Value: object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)}},
			object.Entry{Key: "Contents", Value: object.String{Value: []byte{0}, IsHex: true}},
		)
	}
	unsigned := readBytes(t, buildMinimalPDF())
	unsigned.Add(sigDict())
	inMemory := NewDocument()
	inMemory.Add(sigDict())
	for name, d := range map[string]*Document{"read from an unsigned file": unsigned, "built in memory": inMemory} {
		for _, w := range wholeWriters {
			if err := w.write(d, &bytes.Buffer{}); err != nil {
				t.Errorf("%s: %s: %v", name, w.name, err)
			}
		}
		if err := d.SetEncryption("user", ""); err != nil {
			t.Errorf("%s: SetEncryption: %v", name, err)
		}
	}
}

// TestGuardFindsSignaturesAsVerifySignaturesDoes: the guard's notion of a
// signature is the verifier's — a dictionary with /ByteRange and /Contents
// whose /Type, resolved, is absent, /Sig or /DocTimeStamp — so a signature
// VerifySignatures reports is never one Write destroys unasked, and a
// dictionary it does not report is not one Write refuses for.
func TestGuardFindsSignaturesAsVerifySignaturesDoes(t *testing.T) {
	cases := []struct {
		name string
		objs []string // objects 4 and up
	}{
		{"/Type /Sig", []string{"<< /Type /Sig /ByteRange [0 1 2 3] /Contents <00> >>"}},
		{"/Type /DocTimeStamp", []string{"<< /Type /DocTimeStamp /ByteRange [0 1 2 3] /Contents <00> >>"}},
		{"no /Type", []string{"<< /ByteRange [0 1 2 3] /Contents <00> >>"}},
		{"indirect /Type", []string{"<< /Type 5 0 R /ByteRange [0 1 2 3] /Contents <00> >>", "/Sig"}},
		{"indirect /Contents", []string{"<< /Type /Sig /ByteRange [0 1 2 3] /Contents 5 0 R >>", "<00>"}},
		{"another /Type", []string{"<< /Type /Annot /ByteRange [0 1 2 3] /Contents <00> >>"}},
		{"indirect other /Type", []string{"<< /Type 5 0 R /ByteRange [0 1 2 3] /Contents <00> >>", "/Annot"}},
		{"no /Contents", []string{"<< /Type /Sig /ByteRange [0 1 2 3] >>"}},
		{"no /ByteRange", []string{"<< /Type /Sig /Contents <00> >>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := minimalPDFWith(c.objs...)
			d := readBytes(t, data)
			verifierSees := len(verifySigs(t, d, sign.VerifyOptions{})) > 0
			err := d.Write(&bytes.Buffer{})
			refused := errors.Is(err, ErrWouldInvalidateSignatures)
			if err != nil && !refused {
				t.Fatalf("Write: %v", err)
			}
			if refused != verifierSees {
				t.Errorf("Write refused = %v, but VerifySignatures reports a signature = %v", refused, verifierSees)
			}
			var signed bytes.Buffer
			cert, key := signtest.CertKey(t)
			err = readBytes(t, data).WriteSigned(&signed, cert, key)
			if errors.Is(err, ErrAlreadySigned) != verifierSees {
				t.Errorf("WriteSigned = %v, but VerifySignatures reports a signature = %v", err, verifierSees)
			}
		})
	}
}

// TestWriteSignedAcceptsDocumentConsent: WriteSigned honours the document's
// consent as it honours InvalidatingExistingSignatures.
func TestWriteSignedAcceptsDocumentConsent(t *testing.T) {
	cert, key := signtest.CertKey(t)
	d := readBytes(t, signMinimal(t))
	d.AllowInvalidatingSignatures()
	var out bytes.Buffer
	if err := d.WriteSigned(&out, cert, key); err != nil {
		t.Fatalf("WriteSigned with the document's consent: %v", err)
	}
	valid := 0
	for _, r := range verifySigs(t, readBytes(t, out.Bytes()), sign.VerifyOptions{}) {
		if r.Valid {
			valid++
		}
	}
	if valid != 1 {
		t.Errorf("%d valid signatures, want 1 (the new one)", valid)
	}
}

// minimalPDFWith is buildMinimalPDF with the given objects appended as
// objects 4, 5, …, and the catalog unchanged.
func minimalPDFWith(objs ...string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")
	bodies := append([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
	}, objs...)
	offsets := make([]int, len(bodies))
	for i, b := range bodies {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, b)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \r\n", len(bodies)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \r\n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(bodies)+1, xref)
	return buf.Bytes()
}

// TestIncrementalRefusesEncryptedFileAfterRemoveEncryption: RemoveEncryption
// decrypts the document, not the file. An update appended to an encrypted
// file with no /Encrypt in its trailer made every reader take the file's
// ciphertext for plaintext: the signature still verified, over content that
// had become noise. The incremental writers — the way out the guard names —
// must refuse it, as they refuse an encrypted document.
func TestIncrementalRefusesEncryptedFileAfterRemoveEncryption(t *testing.T) {
	enc := signedEncryptedPDF(t)
	cert, key := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	writers := map[string]func(d *Document, w *bytes.Buffer) error{
		"WriteIncremental": func(d *Document, w *bytes.Buffer) error {
			return d.WriteIncremental(w, []int{d.Add(object.Integer(7)).Number})
		},
		"WriteSignedIncremental": func(d *Document, w *bytes.Buffer) error { return d.WriteSignedIncremental(w, cert, key) },
		"WriteArchivalTimestamp": func(d *Document, w *bytes.Buffer) error {
			return d.WriteArchivalTimestamp(w, ValidationData{}, tsaCert, tsaKey)
		},
	}
	for name, write := range writers {
		d, err := ReadWithPassword(bytes.NewReader(enc), int64(len(enc)), "user")
		if err != nil {
			t.Fatal(err)
		}
		d.RemoveEncryption()
		var out bytes.Buffer
		err = write(d, &out)
		if err == nil || !strings.Contains(err.Error(), "encrypted") {
			t.Errorf("%s after RemoveEncryption = %v, want the refusal of an encrypted file", name, err)
		}
		if out.Len() != 0 {
			t.Errorf("%s wrote %d bytes", name, out.Len())
		}
	}
	// Write's refusal must not point at that way out either.
	d, err := ReadWithPassword(bytes.NewReader(enc), int64(len(enc)), "user")
	if err != nil {
		t.Fatal(err)
	}
	d.RemoveEncryption()
	if err := d.Write(&bytes.Buffer{}); err == nil || strings.Contains(err.Error(), "WriteIncremental") {
		t.Errorf("Write after RemoveEncryption = %v; it must refuse without naming WriteIncremental", err)
	}
}
