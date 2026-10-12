package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
)

// The commands that rewrite a whole file — decrypt, encrypt, repair, merge —
// refuse a signed input unless -invalidate-signatures is given, because the
// rewrite breaks every signature in it (follow-up to docs audit 2026-10-11).

// signedBytes is pdfBytes signed with a throwaway self-signed key.
func signedBytes(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pdf0 CLI test signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := openDoc(t, pdfBytes(t, nil), "").WriteSigned(&buf, cert, key); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// signedEncryptedBytes is an encrypted file carrying a signature dictionary.
// pdf0 cannot write a validly signed encrypted file, and the guard does not
// ask whether a signature verifies, so a dictionary is enough to stand for one.
func signedEncryptedBytes(t *testing.T, user string) []byte {
	t.Helper()
	doc := onePageDoc(t)
	doc.Add(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Sig")},
		object.Entry{Key: "ByteRange", Value: object.Array{object.Integer(0), object.Integer(1), object.Integer(2), object.Integer(3)}},
		object.Entry{Key: "Contents", Value: object.String{Value: []byte{0}, IsHex: true}},
	))
	if err := doc.SetEncryption(user, user); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// signedCases are the commands, each given a signed input, and the input the
// refusal must name.
func signedCases(t *testing.T, dir string) (cases []struct {
	name   string
	args   func(flag bool, out string) []string
	signed string
}, pw string) {
	t.Helper()
	signed := writeFile(t, filepath.Join(dir, "signed.pdf"), signedBytes(t))
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	encSigned := writeFile(t, filepath.Join(dir, "encsigned.pdf"), signedEncryptedBytes(t, "userpw"))
	pw = writeFile(t, filepath.Join(dir, "pw"), []byte("userpw"))
	with := func(flag bool, cmd string, rest ...string) []string {
		args := []string{cmd}
		if flag {
			args = append(args, "-invalidate-signatures")
		}
		return append(args, rest...)
	}
	add := func(name, signedIn string, args func(flag bool, out string) []string) {
		cases = append(cases, struct {
			name   string
			args   func(flag bool, out string) []string
			signed string
		}{name, args, signedIn})
	}
	add("encrypt", signed, func(f bool, out string) []string {
		return with(f, "encrypt", "-user-password-file", pw, signed, out)
	})
	add("repair", signed, func(f bool, out string) []string { return with(f, "repair", signed, out) })
	add("decrypt", encSigned, func(f bool, out string) []string {
		return with(f, "decrypt", "-password-file", pw, encSigned, out)
	})
	add("merge, signed first", signed, func(f bool, out string) []string { return with(f, "merge", out, signed, plain) })
	add("merge, signed later", signed, func(f bool, out string) []string { return with(f, "merge", out, plain, signed) })
	return cases, pw
}

// TestCLIRefusesToInvalidateSignatures: without the flag each command refuses
// a signed input — exit 3, the run failed — names the input and the flag in a
// CLI user's words, and writes nothing.
func TestCLIRefusesToInvalidateSignatures(t *testing.T) {
	dir := t.TempDir()
	cases, _ := signedCases(t, dir)
	for i, c := range cases {
		out := filepath.Join(dir, "out"+string(rune('a'+i))+".pdf")
		res := runCLI(t, cliRun{args: c.args(false, out)})
		if res.code != 3 {
			t.Errorf("%s: want exit 3, got %v", c.name, res)
		}
		if !strings.Contains(res.stderr, "-invalidate-signatures") || !strings.Contains(res.stderr, c.signed+" is signed") {
			t.Errorf("%s: the refusal does not name the input and the flag: %v", c.name, res)
		}
		if strings.Contains(res.stderr, "AllowInvalidatingSignatures") {
			t.Errorf("%s: the refusal names the Go method, not the flag: %v", c.name, res)
		}
		assertNotExist(t, out)
	}
	assertNoTempFiles(t, dir)
}

// TestCLIInvalidateSignaturesFlag: with the flag each command writes its
// output, and every signature that came through no longer verifies.
func TestCLIInvalidateSignaturesFlag(t *testing.T) {
	dir := t.TempDir()
	cases, _ := signedCases(t, dir)
	for i, c := range cases {
		out := filepath.Join(dir, "out"+string(rune('a'+i))+".pdf")
		res := runCLI(t, cliRun{args: c.args(true, out)})
		// repair also reports what it could not fix (exit 1); the rest succeed.
		if res.code != 0 && !(c.name == "repair" && res.code == 1) {
			t.Errorf("%s with -invalidate-signatures: %v", c.name, res)
			continue
		}
		password := ""
		if c.name == "encrypt" {
			password = "userpw"
		}
		doc := openDoc(t, readFile(t, out), password)
		results, err := doc.VerifySignatures(sign.VerifyOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if r.Valid {
				t.Errorf("%s: a signature still verifies in the rewritten file", c.name)
			}
		}
	}
}

// TestCLIUnsignedInputNeedsNoFlag: an unsigned input is written as before;
// the flag is not a requirement.
func TestCLIUnsignedInputNeedsNoFlag(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	plain2 := writeFile(t, filepath.Join(dir, "plain2.pdf"), pdfBytes(t, nil))
	pw := writeFile(t, filepath.Join(dir, "pw"), []byte("userpw"))
	for _, args := range [][]string{
		{"encrypt", "-user-password-file", pw, plain, filepath.Join(dir, "e.pdf")},
		{"merge", filepath.Join(dir, "m.pdf"), plain, plain2},
	} {
		if res := runCLI(t, cliRun{args: args}); res.code != 0 {
			t.Errorf("%v: %v", args, res)
		}
	}
}
