package sign

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

func selfSignedCert(t *testing.T, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Signer"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// TestSignerChainsAtTheValidationTime is the C4 guard in its current form: a
// chain is validated at the time it is asked about — the current time, or a
// trusted time-stamp's — and verification never asks with the signer's own
// signing-time attribute, so an expired certificate cannot be rescued by
// backdating that self-asserted attribute.
func TestSignerChainsAtTheValidationTime(t *testing.T) {
	expired := selfSignedCert(t, time.Now().Add(-3*time.Hour), time.Now().Add(-1*time.Hour))
	roots := x509.NewCertPool()
	roots.AddCert(expired)
	if _, err := signerChains(expired, nil, roots, time.Now()); err == nil {
		t.Fatal("an expired certificate was trusted at the current time")
	}
	if _, err := signerChains(expired, nil, roots, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("at a time inside its validity the same certificate must chain: %v", err)
	}
	valid := selfSignedCert(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	vroots := x509.NewCertPool()
	vroots.AddCert(valid)
	if _, err := signerChains(valid, nil, vroots, time.Now()); err != nil {
		t.Fatalf("currently-valid certificate should be trusted: %v", err)
	}
}

// TestGapIsContents pins the C12 coverage rule: the one gap a signed range
// leaves is exactly the /Contents hex string.
func TestGapIsContents(t *testing.T) {
	contents := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	raw := []byte("%PDF-1.7\n/Contents <DEADBEEF>\n%%EOF\n")
	start := int64(bytes.IndexByte(raw, '<'))
	end := int64(bytes.IndexByte(raw, '>')) + 1
	r := bytes.NewReader(raw)
	if err := gapIsContents(r, start, end, contents); err != nil {
		t.Fatalf("the /Contents window should be accepted: %v", err)
	}
	if err := gapIsContents(r, start, end, []byte{0x00}); err == nil {
		t.Fatal("a gap whose hex does not equal /Contents must be rejected")
	}
	if err := gapIsContents(r, start, end-1, contents); err == nil {
		t.Fatal("a gap that stops inside the hex string must be rejected")
	}
	if err := gapIsContents(r, start-1, end, contents); err == nil {
		t.Fatal("a gap that starts before the hex string must be rejected")
	}
	// Whitespace between the digits is a hex string's right, up to a bound.
	spaced := []byte("<DE AD\nBE EF>")
	if err := gapIsContents(bytes.NewReader(spaced), 0, int64(len(spaced)), contents); err != nil {
		t.Fatalf("a hex string with whitespace should be accepted: %v", err)
	}
	padded := []byte("<DE" + strings.Repeat(" ", 64) + "ADBEEF>")
	if err := gapIsContents(bytes.NewReader(padded), 0, int64(len(padded)), contents); err == nil {
		t.Fatal("a gap padded far past its value must be rejected: its length is not bounded by the signature")
	}
}

// TestReadSignedRangeAcceptsOneLayout pins audit 2026-09-22 C5 and C6: a
// /ByteRange is accepted only as [0 len1 start2 len2] with both spans inside
// the file and the gap exactly /Contents, and every bound is checked without
// overflow — the values are attacker-chosen int64s.
func TestReadSignedRangeAcceptsOneLayout(t *testing.T) {
	contents := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	raw := []byte("%PDF-1.7\n1 0 obj << /Contents <DEADBEEF> >> endobj\n%%EOF\n")
	gs := int64(bytes.Index(raw, []byte("<DEAD")))
	ge := int64(bytes.Index(raw, []byte("F>"))) + 2
	size := int64(len(raw))
	file := bytesFile(raw)

	check := func(name string, br object.Array, wantOK bool, wantErr string) {
		t.Helper()
		sig := object.NewDictionary(
			object.Entry{Key: "ByteRange", Value: br},
			object.Entry{Key: "Contents", Value: object.String{Value: contents, IsHex: true}},
		)
		rg, err := readSignedRange(mkV(core.View{}), sig, file)
		if wantOK {
			if err != nil {
				t.Errorf("%s: rejected: %v", name, err)
			} else if rg.gapStart != gs || rg.gapEnd != ge || rg.end != size {
				t.Errorf("%s: range = %+v", name, rg)
			}
			return
		}
		if err == nil {
			t.Errorf("%s: accepted %v", name, br)
		} else if wantErr != "" && !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, wantErr)
		}
	}
	i := func(v int64) object.Integer { return object.Integer(v) }
	check("canonical", object.Array{i(0), i(gs), i(ge), i(size - ge)}, true, "")
	check("overflowing first span", object.Array{i(1), i(math.MaxInt64), i(0), i(1)}, false, "")
	check("overflowing second span", object.Array{i(0), i(gs), i(ge), i(math.MaxInt64)}, false, "beyond the end")
	check("second span past the end", object.Array{i(0), i(gs), i(ge), i(size - ge + 1)}, false, "beyond the end")
	check("start past the end", object.Array{i(0), i(gs), i(math.MaxInt64), i(0)}, false, "beyond the end")
	check("negative length", object.Array{i(0), i(-1), i(ge), i(size - ge)}, false, "")
	check("not starting at 0", object.Array{i(1), i(gs - 1), i(ge), i(size - ge)}, false, "")
	check("overlapping spans", object.Array{i(0), i(ge), i(gs), i(size - gs)}, false, "")
	check("six integers", object.Array{i(0), i(gs), i(ge), i(1), i(ge + 1), i(size - ge - 1)}, false, "four integers")
	check("gap one byte short", object.Array{i(0), i(gs + 1), i(ge), i(size - ge)}, false, "")
	check("a real in the array", object.Array{i(0), object.Real(float64(gs)), i(ge), i(size - ge)}, false, "four integers")
}
