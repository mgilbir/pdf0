package pdf0

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	mrand "math/rand"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
)

// Hostile signature dictionaries. Every one of these runs the public verifiers
// in a capped child process (internal/hostile): the failure they guard
// against is a panic, an allocation or a loop the file controls.

// patchByteRange rewrites the first /ByteRange array in data in place, padding
// with spaces so no offset moves.
func patchByteRange(t *testing.T, data []byte, repl string) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	i := bytes.Index(out, []byte("/ByteRange"))
	j := bytes.IndexByte(out[i:], '[') + i
	k := bytes.IndexByte(out[j:], ']') + j
	if len(repl) > k-j-1 {
		t.Fatalf("replacement %q is wider than the array (%d bytes)", repl, k-j-1)
	}
	copy(out[j+1:k], repl+strings.Repeat(" ", k-j-1-len(repl)))
	return out
}

// verifyBoth runs both public verifiers on data and returns their results; an
// error from either — which means verification could not run — fails.
func verifyBoth(t *testing.T, data []byte) ([]sign.Result, []sign.PAdESResult) {
	t.Helper()
	d, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return verifySigs(t, d, sign.VerifyOptions{}), padesOf(t, d, sign.VerifyOptions{})
}

// TestByteRangeOverflowIsRejected is audit 2026-09-22 C5: start+length
// computed unchecked overflowed, and the negative slice bound it produced
// panicked out of the public verifier.
func TestByteRangeOverflowIsRejected(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		signed := signMinimal(t)
		for _, br := range []string{
			"1 9223372036854775807 0 1",
			"0 99 9223372036854775807 9",
			"0 99 150 9223372036854775807",
			"0 99 150 9223372036854775000",
			"0 -9223372036854775807 0 1",
		} {
			res, pades := verifyBoth(t, patchByteRange(t, signed, br))
			if len(res) != 1 || res[0].Valid || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "/ByteRange") {
				t.Errorf("[%s]: %+v, want one invalid result with a /ByteRange error", br, res)
			}
			if len(pades) != 1 || pades[0].Conformant || pades[0].Valid {
				t.Errorf("[%s]: PAdES %+v, want not valid", br, pades)
			}
		}
	})
}

// TestByteRangeSegmentFloodIsRejected is audit 2026-09-22 C6: a 1.5 MB file
// whose /ByteRange repeats [0 1500000] 150,000 times made the verifier
// concatenate 225 GB of segments, and was killed at a 2 GiB cap in 2.5 s. A
// range is now exactly four integers, hashed where it lies.
func TestByteRangeSegmentFloodIsRejected(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 512 << 20, Timeout: time.Minute}, func(t *testing.T) {
		d := readBytes(t, buildMinimalPDF())
		br := make(object.Array, 0, 300000)
		for i := 0; i < 150000; i++ {
			br = append(br, object.Integer(0), object.Integer(1500000))
		}
		d.Add(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Sig")},
			object.Entry{Key: "ByteRange", Value: br},
			object.Entry{Key: "Contents", Value: object.String{Value: []byte{0x30, 0x00}, IsHex: true}},
		))
		var buf bytes.Buffer
		if err := d.Write(&buf); err != nil {
			t.Fatal(err)
		}
		if buf.Len() < 1<<20 {
			t.Fatalf("the flood file is %d bytes; it should be over a megabyte", buf.Len())
		}
		res, pades := verifyBoth(t, buf.Bytes())
		if len(res) != 1 || res[0].Valid || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "four integers") {
			t.Fatalf("%+v, want one invalid result refusing the range", res)
		}
		if len(pades) != 1 || pades[0].Valid {
			t.Fatalf("PAdES %+v, want not valid", pades)
		}
	})
}

// TestMalformedSignatureCorpus mutates a valid signed file's signature
// dictionary in every way the verifier reads — the /ByteRange integers and
// their count, the /Contents value, the gap it sits in — and runs both
// verifiers on each. None may panic, fail to return, or report a mutated
// signature valid.
func TestMalformedSignatureCorpus(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 1 << 30, Timeout: 3 * time.Minute}, func(t *testing.T) {
		signed := signMinimal(t)
		rng := mrand.New(mrand.NewSource(20260922))
		i := bytes.Index(signed, []byte("/ByteRange"))
		j := bytes.IndexByte(signed[i:], '[') + i
		k := bytes.IndexByte(signed[j:], ']') + j
		var orig [4]int64
		if _, err := fmt.Sscan(string(signed[j+1:k]), &orig[0], &orig[1], &orig[2], &orig[3]); err != nil {
			t.Fatal(err)
		}
		interesting := []int64{0, 1, -1, 2, orig[1], orig[1] - 1, orig[1] + 1, orig[2], orig[2] + 1, int64(len(signed)), int64(len(signed)) + 1,
			1 << 31, 1<<31 - 1, 1 << 32, 1<<62 + 1, 9223372036854775807, -9223372036854775807}
		pick := func() int64 {
			if rng.Intn(3) == 0 {
				return rng.Int63n(int64(len(signed)) + 2)
			}
			return interesting[rng.Intn(len(interesting))]
		}
		var cases [][]byte
		for n := 0; n < 300; n++ {
			v := orig
			for m := 1 + rng.Intn(3); m > 0; m-- {
				v[rng.Intn(4)] = pick()
			}
			if v == orig {
				continue
			}
			repl := fmt.Sprintf("%d %d %d %d", v[0], v[1], v[2], v[3])
			if len(repl) > k-j-1 {
				continue
			}
			cases = append(cases, patchByteRange(t, signed, repl))
		}
		// Wrong element counts and types.
		for _, repl := range []string{"", "0", "0 1 2", "0 1 2 3 4 5", "/X 1 2 3", "0 1.5 2 3", "[0] 1 2 3", "0 0 0 0"} {
			cases = append(cases, patchByteRange(t, signed, repl))
		}
		// The /Contents value: a byte flipped in the DER, the DER cut short
		// with zeros, and the gap's hex made invalid.
		ci := bytes.Index(signed[k:], []byte("<")) + k
		for _, off := range []int{1, 2, 3, 10, 40, 200, 1000} {
			m := append([]byte(nil), signed...)
			if m[ci+off] == '0' {
				m[ci+off] = '1'
			} else {
				m[ci+off] = '0'
			}
			cases = append(cases, m)
		}
		{
			m := append([]byte(nil), signed...)
			copy(m[ci+1:ci+201], bytes.Repeat([]byte("0"), 200))
			cases = append(cases, m)
			m2 := append([]byte(nil), signed...)
			m2[ci+5] = 'Z'
			cases = append(cases, m2)
		}
		for n, c := range cases {
			d, err := Read(bytes.NewReader(c), int64(len(c)))
			if err != nil {
				continue // not a PDF any more: nothing to verify
			}
			res, err := d.VerifySignatures(sign.VerifyOptions{})
			if err != nil {
				t.Errorf("case %d: VerifySignatures: %v", n, err)
				continue
			}
			for _, r := range res {
				if r.Valid {
					t.Errorf("case %d: a mutated signature verified: %+v", n, r)
				}
			}
			pades, err := d.ValidatePAdES(sign.VerifyOptions{})
			if err != nil {
				t.Errorf("case %d: ValidatePAdES: %v", n, err)
				continue
			}
			for _, p := range pades {
				if p.Valid || p.Conformant {
					t.Errorf("case %d: a mutated signature assessed valid: %+v", n, p)
				}
			}
		}
		if len(cases) < 250 {
			t.Fatalf("only %d cases were generated", len(cases))
		}
	})
}

// TestManyRevisionSignaturesHashOnce: a file of thousands of revisions, each
// holding a signature dictionary whose range correctly ends at its revision,
// makes the verifier hash once per signature. Hashing each range from the
// start of the file costs the file's size per signature — quadratic, here
// about 250 GB of SHA-256 — so the prefix is hashed once and shared. Each
// signature carries the same real CMS, which parses and names SHA-256, so
// every range really is hashed before its digest fails to match.
func TestManyRevisionSignaturesHashOnce(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 1 << 30, Timeout: time.Minute}, func(t *testing.T) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "flood"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		cert, _ := x509.ParseCertificate(der)
		cms, err := sign.BuildSignedDataFull(cert, key, []byte("anything"), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		contents := "<" + hex.EncodeToString(cms) + ">"
		const revisions = 20000
		var buf bytes.Buffer
		buf.Write(buildMinimalPDF())
		prev := int64(bytes.LastIndex(buf.Bytes(), []byte("xref\n")))
		for n := 0; n < revisions; n++ {
			num := 10 + n
			objOff := buf.Len()
			fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /Sig /ByteRange [0 %010d %010d %010d] /Contents ", num, 0, 0, 0)
			gapStart := buf.Len()
			buf.WriteString(contents)
			gapEnd := buf.Len()
			buf.WriteString(" >>\nendobj\n")
			xrefOff := buf.Len()
			fmt.Fprintf(&buf, "xref\n%d 1\n%010d 00000 n \r\ntrailer\n<< /Size %d /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", num, objOff, num+1, prev, xrefOff)
			end := buf.Len()
			br := fmt.Sprintf("0 %010d %010d %010d", gapStart, gapEnd, end-gapEnd)
			b := buf.Bytes()
			at := bytes.LastIndex(b[:gapStart], []byte("[0 ")) + 1
			copy(b[at:], br)
			prev = int64(xrefOff)
		}
		data := buf.Bytes()
		d, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(d.Source().Revisions()); n != revisions+1 {
			t.Fatalf("the file has %d revisions, want %d", n, revisions+1)
		}
		start := time.Now()
		res := verifySigs(t, d, sign.VerifyOptions{})
		if len(res) != revisions {
			t.Fatalf("got %d results, want %d", len(res), revisions)
		}
		for _, r := range res {
			if r.Valid || r.Err == nil || !strings.Contains(r.Err.Error(), "digest does not match") {
				t.Fatalf("each signature is hashed and fails its digest: %+v", r)
			}
		}
		t.Logf("%d signatures over a %d-byte file verified in %v", revisions, len(data), time.Since(start))
	})
}

// TestRevisionDiffBudgetIsNeverPermission: when comparing revisions exceeds
// its work budget, the changes after a signature are unknown, and unknown is
// reported as not permitted — never as "nothing changed".
func TestRevisionDiffBudgetIsNeverPermission(t *testing.T) {
	cert, _ := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	d := readBytes(t, archive(t, signMinimal(t), ValidationData{Certs: []*x509.Certificate{cert}}, tsaCert, tsaKey))
	if r := firstSignature(t, archiveBytes(d)); !r.Intact() {
		t.Fatalf("with the normal budget the archival update is permitted: %+v", r.DisallowedChanges)
	}
	f := d.signedFile(d.canceler())
	f.work = 1
	approval, _ := approvalAndTimestamps(sign.VerifySignatures(d.view(), f, sign.VerifyOptions{}))
	if len(approval) != 1 || approval[0].ChangesAllowed || !anyContains(approval[0].DisallowedChanges, "work budget") {
		t.Fatalf("an exhausted budget must leave the changes unknown and not permitted: %+v", approval)
	}
}

// archiveBytes returns the bytes d was read from.
func archiveBytes(d *Document) []byte { return d.Source().data }
