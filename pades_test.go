package pdf0

import (
	"bytes"
	"crypto/x509"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
)

// signMinimal signs buildMinimalPDF with WriteSigned and the given options,
// and returns the signed file.
func signMinimal(t *testing.T, opts ...SignOption) []byte {
	t.Helper()
	cert, key := signtest.CertKey(t)
	base := buildMinimalPDF()
	var buf bytes.Buffer
	if err := readBytes(t, base).WriteSigned(&buf, cert, key, opts...); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	return buf.Bytes()
}

// TestPAdESRoundTrip signs a document (pdf0 now produces PAdES-B-B: the
// ETSI.CAdES.detached sub-filter and a CAdES signing-certificate attribute) and
// checks that ValidatePAdES reports a conformant B-B signature.
func TestPAdESRoundTrip(t *testing.T) {
	out := signMinimal(t)
	res := padesOf(t, readBytes(t, out), sign.VerifyOptions{})
	if len(res) != 1 {
		t.Fatalf("got %d PAdES results, want 1", len(res))
	}
	r := res[0]
	if !r.IsPAdES {
		t.Errorf("expected a PAdES signature (sub-filter %q)", r.SubFilter)
	}
	if !r.Valid {
		t.Error("signature should verify")
	}
	if !r.CoversDocument || !r.ChangesAllowed {
		t.Errorf("signature should cover the document (covers=%v allowed=%v)", r.CoversDocument, r.ChangesAllowed)
	}
	if !r.Conformant {
		t.Errorf("expected a conformant B-B signature, got issues: %v", r.Issues)
	}
	if r.Level != sign.PAdESBB {
		t.Errorf("level = %q, want B-B (no timestamp/DSS present)", r.Level)
	}
}

// TestPAdESTamperDetected confirms that modifying the signed content makes the
// signature non-conformant (it no longer verifies). The file is changed in
// place, inside the signed range, and read back: verification reads the file
// a document was read from, so the tampered file is what it judges.
func TestPAdESTamperDetected(t *testing.T) {
	out := signMinimal(t)
	i := bytes.Index(out, []byte("612 792"))
	if i < 0 {
		t.Fatal("no MediaBox to tamper with")
	}
	tampered := append([]byte(nil), out...)
	tampered[i] = '9' // 612 -> 912: the same length, still a valid file
	res := padesOf(t, readBytes(t, tampered), sign.VerifyOptions{})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Valid {
		t.Error("tampered signature should not verify")
	}
	if res[0].Conformant {
		t.Error("tampered signature should not be conformant")
	}
}

// TestPAdESLegacyNotPAdES confirms a legacy adbe.pkcs7.detached signature is
// reported as not PAdES (but still cryptographically assessed).
func TestPAdESLegacyNotPAdES(t *testing.T) {
	out := signMinimal(t)
	// Rewrite the sub-filter in the output to the legacy value (same length).
	out = bytes.Replace(out, []byte("/ETSI.CAdES.detached"), []byte("/adbe.pkcs7.detached"), 1)
	res := padesOf(t, readBytes(t, out), sign.VerifyOptions{})
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].IsPAdES {
		t.Error("adbe.pkcs7.detached must not be reported as PAdES")
	}
	if res[0].Level != sign.PAdESNone {
		t.Errorf("legacy signature level = %q, want none", res[0].Level)
	}
}

// TestPAdESLevelDetection checks the level ordering: a catalog /DSS without a
// signature time-stamp does not reach B-LT, since each level requires the
// previous.
func TestPAdESLevelDetection(t *testing.T) {
	out := signMinimal(t)
	signed := readBytes(t, out)
	// Baseline: no DSS, no timestamp -> B-B.
	if got := padesOf(t, signed, sign.VerifyOptions{})[0].Level; got != sign.PAdESBB {
		t.Fatalf("baseline level = %q, want B-B", got)
	}
	cat := signed.view().Catalog()
	cat.Set("DSS", &object.Dictionary{})
	if got := padesOf(t, signed, sign.VerifyOptions{})[0].Level; got != sign.PAdESBB {
		t.Errorf("DSS without a timestamp must not reach B-LT; level = %q", got)
	}
}

// TestPAdESBTTimestamp signs with an RFC 3161 signature time-stamp (a local TSA)
// and checks that ValidatePAdES reaches level B-T with a cryptographically
// verified time-stamp — trusted only when the TSA is among the roots.
func TestPAdESBTTimestamp(t *testing.T) {
	tsaCert, tsaKey := signtest.TSACertKey(t)
	out := signMinimal(t, WithSignatureTimestamp(tsaCert, tsaKey))
	signed := readBytes(t, out)
	res := padesOf(t, signed, sign.VerifyOptions{})
	if len(res) != 1 {
		t.Fatalf("got %d PAdES results, want 1", len(res))
	}
	r := res[0]
	if !r.Valid {
		t.Error("signature should verify")
	}
	if !r.TimestampValid {
		t.Errorf("signature time-stamp should verify; issues: %v", r.Issues)
	}
	if r.TimestampTrusted {
		t.Error("a time-stamp is trusted only against roots, and none were given")
	}
	if r.TimestampTime.IsZero() {
		t.Error("expected a time-stamp time")
	}
	if r.Level != sign.PAdESBT {
		t.Errorf("level = %q, want B-T", r.Level)
	}
	if !r.Conformant {
		t.Errorf("expected a conformant signature, issues: %v", r.Issues)
	}
	tsaRoots := x509.NewCertPool()
	tsaRoots.AddCert(tsaCert)
	if r := padesOf(t, signed, sign.VerifyOptions{TSARoots: tsaRoots})[0]; !r.TimestampTrusted {
		t.Error("with the TSA among the time-stamp roots its time-stamp should be trusted")
	}
}

// TestPAdESBLTA signs B-T, then adds a DSS and a document time-stamp as an
// incremental update, and checks the signature is assessed at level B-LTA with
// its original signature still valid.
//
// The signature no longer covers the file — the archival revision follows it
// — and it is conformant because every change in that revision is a permitted
// archival addition (ChangesAllowed), not because the document time-stamp
// "seals" it: an earlier version of this test asserted that sealing, which is
// the relaxation that let a time-stamp excuse any tampering (audit
// 2026-09-22 C1; see TestPAdESTimestampDoesNotExcuseTampering).
func TestPAdESBLTA(t *testing.T) {
	cert, _ := signtest.CertKey(t)
	tsaCert, tsaKey := signtest.TSACertKey(t)
	o1 := signMinimal(t, WithSignatureTimestamp(tsaCert, tsaKey))

	var b2 bytes.Buffer
	if err := readBytes(t, o1).WriteArchivalTimestamp(&b2, ValidationData{Certs: []*x509.Certificate{cert}}, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	res := padesOf(t, readBytes(t, b2.Bytes()), sign.VerifyOptions{})
	var lta *sign.PAdESResult
	for i := range res {
		if res[i].Level == sign.PAdESBLTA {
			lta = &res[i]
		}
	}
	if lta == nil {
		t.Fatalf("expected a B-LTA signature; got %+v", res)
	}
	if !lta.Valid {
		t.Error("the approval signature should still verify after the archival timestamp")
	}
	if lta.CoversDocument {
		t.Error("the approval signature should not cover the appended archival revision")
	}
	if !lta.ChangesAllowed || len(lta.DisallowedChanges) != 0 {
		t.Errorf("the archival revision holds only permitted changes; disallowed: %v", lta.DisallowedChanges)
	}
	if !lta.Conformant {
		t.Errorf("a B-LTA approval signature followed only by archival changes should be conformant; issues: %v", lta.Issues)
	}
}

// TestPAdESUncoveredBytesAreFlagged checks that bytes appended after the
// signed revision, which no revision accounts for, are reported: they are not
// a permitted change.
func TestPAdESUncoveredBytesAreFlagged(t *testing.T) {
	o1 := signMinimal(t)
	o1 = append(o1, []byte("\n% trailing bytes not covered by any signature\n")...)
	res := padesOf(t, readBytes(t, o1), sign.VerifyOptions{})
	if len(res) != 1 {
		t.Fatalf("expected one signature; got %d", len(res))
	}
	if res[0].CoversDocument || res[0].ChangesAllowed || res[0].Conformant {
		t.Fatalf("the signature should neither cover nor permit the appended bytes: %+v", res[0])
	}
	found := false
	for _, iss := range res[0].Issues {
		if strings.Contains(iss, "does not cover the whole document") && strings.Contains(iss, "not part of any revision") {
			found = true
		}
	}
	if !found {
		t.Errorf("the appended bytes should be named as a disallowed change; issues: %v", res[0].Issues)
	}
}

// TestPAdESReportsRevocation is docs audit 2026-10-11 D3: a PAdES result
// carries the signer's revocation status, as VerifySignatures's does. The
// signer's CA has revoked it, and the DSS says so. The chain still builds —
// TrustedChain does not include the signer's own revocation — and the PAdES
// assessment is of the signature's form, which revocation does not change;
// what a ValidatePAdES-only caller needs is the status beside them.
func TestPAdESReportsRevocation(t *testing.T) {
	ca, caKey := signtest.CA(t, "pdf0 PAdES revocation CA")
	tsaCert, tsaKey := signtest.TSACertKey(t)
	signed, leaf := signedByLeaf(t, ca, caKey, leafTemplate("pdf0 PAdES revoked signer"))
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	for _, tc := range []struct {
		name    string
		revoked []*x509.Certificate
		want    sign.RevocationStatus
	}{
		{"revoked", []*x509.Certificate{leaf}, sign.RevocationRevoked},
		{"not revoked", nil, sign.RevocationGood},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vd := ValidationData{Certs: []*x509.Certificate{ca}, CRLs: [][]byte{signtest.MakeCRL(t, ca, caKey, tc.revoked)}}
			d := readBytes(t, archive(t, signed, vd, tsaCert, tsaKey))
			approval, _ := approvalAndTimestamps(verifySigs(t, d, sign.VerifyOptions{Roots: roots}))
			res := padesOf(t, d, sign.VerifyOptions{Roots: roots})
			if len(res) != 1 || len(approval) != 1 {
				t.Fatalf("got %d PAdES results and %d signatures, want one of each", len(res), len(approval))
			}
			p := res[0]
			if p.Revocation != approval[0].Revocation {
				t.Errorf("PAdES revocation %+v differs from VerifySignatures's %+v", p.Revocation, approval[0].Revocation)
			}
			if p.Revocation.Status != tc.want || p.Revocation.Source != "CRL" {
				t.Errorf("revocation = %v/%q, want %v/CRL", p.Revocation.Status, p.Revocation.Source, tc.want)
			}
			if !p.TrustedChain || !p.Conformant {
				t.Errorf("trusted=%v conformant=%v issues=%v: neither includes the signer's revocation", p.TrustedChain, p.Conformant, p.Issues)
			}
		})
	}
}
