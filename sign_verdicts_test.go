package pdf0

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/sign"
)

// The tests in this file pin what a signature verdict means (audit 2026-09-22
// §3.A): the bytes the signer signed are the bytes the reader sees, or differ
// only in permitted ways; the signer chains to a caller-chosen root with a
// document-signing purpose; and revocation data is authenticated by the
// certificate's real issuer. Each builds its certificates in-process.

// firstPageNum returns the object number of d's first page.
func firstPageNum(t *testing.T, d *Document) int {
	t.Helper()
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	pg := firstPage(d, cat)
	if pg == nil {
		t.Fatal("no page")
	}
	return d.view().DictObjNum(pg)
}

// tamperPage appends an incremental update to data that shrinks the first
// page's /MediaBox to 10×10 — a change to what the document shows.
func tamperPage(t *testing.T, data []byte) []byte {
	t.Helper()
	d := readBytes(t, data)
	n := firstPageNum(t, d)
	pg := d.Objects[n].Value.(*object.Dictionary).Clone()
	pg.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)})
	d.Objects[n] = &object.IndirectObject{Number: n, Value: pg}
	var out bytes.Buffer
	if err := d.WriteIncremental(&out, []int{n}); err != nil {
		t.Fatalf("WriteIncremental: %v", err)
	}
	return out.Bytes()
}

// archive appends a DSS holding vd and a document time-stamp by the given TSA.
func archive(t *testing.T, data []byte, vd ValidationData, tsaCert *x509.Certificate, tsaKey crypto.Signer) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := readBytes(t, data).WriteArchivalTimestamp(&out, vd, tsaCert, tsaKey); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	return out.Bytes()
}

// approvalAndTimestamps splits results into the approval signatures and the
// document time-stamps.
func approvalAndTimestamps(res []sign.Result) (approval, timestamps []sign.Result) {
	for _, r := range res {
		if r.DocTimestamp {
			timestamps = append(timestamps, r)
		} else {
			approval = append(approval, r)
		}
	}
	return
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestTimestampDoesNotExcuseTampering is audit 2026-09-22 C1. A signed page
// is changed by an incremental update, and a document time-stamp is then laid
// over the whole file. The time-stamp used to "seal" the tampering: the
// signature came back PAdES-conformant, with no issue, over a page that really
// had been changed. A time-stamp proves when bytes existed, not that a change
// was permitted — a public TSA stamps any hash — so the verdict must not
// change whether the TSA is self-signed and untrusted or chains to the
// caller's time-stamp roots.
func TestTimestampDoesNotExcuseTampering(t *testing.T) {
	for _, bt := range []bool{false, true} {
		for _, trustedTSA := range []bool{false, true} {
			t.Run(fmt.Sprintf("B-T=%v/trustedTSA=%v", bt, trustedTSA), func(t *testing.T) {
				tsaCert, tsaKey := signtest.TSACertKey(t)
				var opts []SignOption
				if bt {
					opts = append(opts, WithSignatureTimestamp(tsaCert, tsaKey))
				}
				signed := signMinimal(t, opts...)
				final := archive(t, tamperPage(t, signed), ValidationData{}, tsaCert, tsaKey)
				d := readBytes(t, final)
				if mb := d.Objects[firstPageNum(t, d)].Value.(*object.Dictionary).Get("MediaBox"); !object.Equal(mb, object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}) {
					t.Fatalf("the tampered page should read [0 0 10 10], got %v", mb)
				}
				vo := sign.VerifyOptions{}
				if trustedTSA {
					pool := x509.NewCertPool()
					pool.AddCert(tsaCert)
					vo.TSARoots = pool
				}

				pades := padesOf(t, d, vo)
				if len(pades) != 1 {
					t.Fatalf("got %d PAdES results, want 1", len(pades))
				}
				p := pades[0]
				if !p.Valid {
					t.Fatalf("the signed bytes are untouched, so the signature itself verifies: %+v", p)
				}
				if p.Conformant || p.ChangesAllowed {
					t.Fatalf("a signature followed by a page change must not be conformant, time-stamp or not: %+v", p)
				}
				if !anyContains(p.DisallowedChanges, "MediaBox") || !anyContains(p.Issues, "MediaBox") {
					t.Errorf("the page change should be named: disallowed=%v issues=%v", p.DisallowedChanges, p.Issues)
				}
				if bt && p.Level != sign.PAdESBLTA {
					t.Errorf("the material present is B-LTA's; level = %q", p.Level)
				}

				approval, stamps := approvalAndTimestamps(verifySigs(t, d, vo))
				if len(approval) != 1 || len(stamps) != 1 {
					t.Fatalf("want one signature and one time-stamp, got %d and %d", len(approval), len(stamps))
				}
				if approval[0].Intact() || approval[0].DocumentUnmodified() {
					t.Errorf("the signature is not intact after a page change: %+v", approval[0])
				}
				ts := stamps[0]
				if !ts.Valid || !ts.Intact() || !ts.CoversWholeDocument || ts.TimestampTime.IsZero() {
					t.Errorf("the document time-stamp itself is valid over the whole file: %+v", ts)
				}
				if ts.TrustedChain != trustedTSA || ts.TimestampTrusted != trustedTSA {
					t.Errorf("the time-stamp is trusted exactly when its TSA is among the roots: trusted=%v ts=%v chainErr=%v", ts.TrustedChain, ts.TimestampTrusted, ts.ChainErr)
				}
			})
		}
	}
}

// TestDocTimestampIsReportedAsATimestamp pins audit 2026-09-22 C154: a
// document time-stamp is verified as an RFC 3161 token over the bytes it
// covers, not as a CMS signature whose digest "does not match (content was
// modified)", which every one used to report.
func TestDocTimestampIsReportedAsATimestamp(t *testing.T) {
	tsaCert, tsaKey := signtest.TSACertKey(t)
	final := archive(t, signMinimal(t, WithSignatureTimestamp(tsaCert, tsaKey)), ValidationData{}, tsaCert, tsaKey)
	pool := x509.NewCertPool()
	pool.AddCert(tsaCert)
	approval, stamps := approvalAndTimestamps(verifySigs(t, readBytes(t, final), sign.VerifyOptions{TSARoots: pool}))
	if len(approval) != 1 || len(stamps) != 1 {
		t.Fatalf("want one signature and one time-stamp, got %d and %d", len(approval), len(stamps))
	}
	ts := stamps[0]
	if !ts.Valid || ts.Err != nil || !ts.DocumentUnmodified() || ts.SignerCommonName != tsaCert.Subject.CommonName {
		t.Errorf("document time-stamp: valid=%v err=%v unmodified=%v signer=%q", ts.Valid, ts.Err, ts.DocumentUnmodified(), ts.SignerCommonName)
	}
	a := approval[0]
	if !a.Intact() || a.DocumentUnmodified() {
		t.Errorf("the signature is intact (only archival changes follow it) but no longer covers the file: %+v", a)
	}
	// The trusted signature time-stamp sets the validation time.
	if !a.TimestampTrusted || !a.ValidationTime.Equal(a.TimestampTime) {
		t.Errorf("validation time %v should be the trusted signature time-stamp's %v", a.ValidationTime, a.TimestampTime)
	}
}

// signedByLeaf signs the minimal document with a certificate ca issues from
// tmpl, and returns the signed file and the leaf.
func signedByLeaf(t *testing.T, ca *x509.Certificate, caKey crypto.Signer, tmpl *x509.Certificate) ([]byte, *x509.Certificate) {
	t.Helper()
	leaf, leafKey := signtest.Issue(t, tmpl, ca, caKey)
	var buf bytes.Buffer
	if err := readBytes(t, buildMinimalPDF()).WriteSigned(&buf, leaf, leafKey); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	return buf.Bytes(), leaf
}

func leafTemplate(cn string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    signtest.NotBefore,
		NotAfter:     signtest.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
}

// TestRevocationIssuerIsTheVerifiedIssuer is audit 2026-09-22 C2. The real CA
// has revoked the signer. An attacker appends a DSS holding a fake CA with the
// real one's name and an OCSP "good" that the fake signed. The issuer used to
// be found by name alone, and the first definite answer — the forged OCSP,
// read first — won: the revoked signer came back trusted and good. The issuer
// must be the certificate that verifiably issued the leaf (the next in the
// verified chain), and a revocation must win over any "good".
func TestRevocationIssuerIsTheVerifiedIssuer(t *testing.T) {
	ca, caKey := signtest.CA(t, "Real CA")
	fake, fakeKey := signtest.CA(t, "Real CA") // same distinguished name, different key
	if !bytes.Equal(ca.RawSubject, fake.RawSubject) {
		t.Fatal("the fake CA must carry the real one's name for this test to mean anything")
	}
	tsaCert, tsaKey := signtest.TSACertKey(t)
	signed, leaf := signedByLeaf(t, ca, caKey, leafTemplate("Mallory (revoked)"))
	crl := signtest.MakeCRL(t, ca, caKey, []*x509.Certificate{leaf})
	forgedGood := signtest.MakeOCSP(t, leaf, fake, fakeKey, "good")
	roots := x509.NewCertPool()
	roots.AddCert(ca)

	for _, tc := range []struct {
		name       string
		vd         ValidationData
		roots      *x509.CertPool
		wantStatus sign.RevocationStatus
		wantSource string
	}{
		{"honest DSS", ValidationData{Certs: []*x509.Certificate{ca}, CRLs: [][]byte{crl}}, roots, sign.RevocationRevoked, "CRL"},
		{"forged issuer beside the real CRL", ValidationData{Certs: []*x509.Certificate{fake, ca}, CRLs: [][]byte{crl}, OCSPs: [][]byte{forgedGood}}, roots, sign.RevocationRevoked, "CRL"},
		{"forged issuer beside the real CRL, no roots", ValidationData{Certs: []*x509.Certificate{fake, ca}, CRLs: [][]byte{crl}, OCSPs: [][]byte{forgedGood}}, nil, sign.RevocationRevoked, "CRL"},
		{"forged issuer alone", ValidationData{Certs: []*x509.Certificate{fake, ca}, OCSPs: [][]byte{forgedGood}}, roots, sign.RevocationUnknown, ""},
		{"forged issuer alone, no roots", ValidationData{Certs: []*x509.Certificate{fake, ca}, OCSPs: [][]byte{forgedGood}}, nil, sign.RevocationUnknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := readBytes(t, archive(t, signed, tc.vd, tsaCert, tsaKey))
			approval, _ := approvalAndTimestamps(verifySigs(t, d, sign.VerifyOptions{Roots: tc.roots}))
			if len(approval) != 1 {
				t.Fatalf("got %d signatures, want 1", len(approval))
			}
			r := approval[0]
			if !r.Valid || r.TrustedChain != (tc.roots != nil) {
				t.Fatalf("valid=%v trusted=%v chainErr=%v", r.Valid, r.TrustedChain, r.ChainErr)
			}
			if r.Revocation.Status != tc.wantStatus || r.Revocation.Source != tc.wantSource {
				t.Errorf("revocation = %v/%q, want %v/%q", r.Revocation.Status, r.Revocation.Source, tc.wantStatus, tc.wantSource)
			}
		})
	}
}

// TestRevocationFreshnessAndPrecedence pins audit 2026-09-22 C56 and C154
// through the whole verifier: an OCSP "good" counts only when it is current
// at the validation time — not when it has expired, and not "for ever" when
// it names no nextUpdate — and a revoking CRL wins over a current "good".
func TestRevocationFreshnessAndPrecedence(t *testing.T) {
	ca, caKey := signtest.CA(t, "pdf0 freshness CA")
	tsaCert, tsaKey := signtest.TSACertKey(t)
	signed, leaf := signedByLeaf(t, ca, caKey, leafTemplate("pdf0 freshness signer"))
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	now := time.Now().UTC().Truncate(time.Second)
	ocsp := func(status string, this, next time.Time) []byte {
		return signtest.MakeOCSPAt(t, leaf, ca, caKey, status, this, next)
	}
	for _, tc := range []struct {
		name  string
		vd    ValidationData
		want  sign.RevocationStatus
		wantS string
	}{
		{"current good OCSP", ValidationData{OCSPs: [][]byte{ocsp("good", now.Add(-time.Hour), now.Add(24*time.Hour))}}, sign.RevocationGood, "OCSP"},
		{"expired good OCSP", ValidationData{OCSPs: [][]byte{ocsp("good", now.Add(-10*24*time.Hour), now.Add(-3*24*time.Hour))}}, sign.RevocationUnknown, ""},
		{"not-yet-valid good OCSP", ValidationData{OCSPs: [][]byte{ocsp("good", now.Add(24*time.Hour), now.Add(48*time.Hour))}}, sign.RevocationUnknown, ""},
		{"day-old good OCSP without nextUpdate", ValidationData{OCSPs: [][]byte{ocsp("good", now.Add(-24*time.Hour), time.Time{})}}, sign.RevocationUnknown, ""},
		{"just-produced good OCSP without nextUpdate", ValidationData{OCSPs: [][]byte{ocsp("good", now.Add(-time.Minute), time.Time{})}}, sign.RevocationGood, "OCSP"},
		{"current good OCSP and a revoking CRL", ValidationData{
			CRLs:  [][]byte{signtest.MakeCRL(t, ca, caKey, []*x509.Certificate{leaf})},
			OCSPs: [][]byte{ocsp("good", now.Add(-time.Hour), now.Add(24*time.Hour))},
		}, sign.RevocationRevoked, "CRL"},
		{"stale CRL that revokes", ValidationData{
			CRLs: [][]byte{signtest.MakeCRLAt(t, ca, caKey, []*x509.Certificate{leaf}, now.Add(-10*24*time.Hour), now.Add(-3*24*time.Hour))},
		}, sign.RevocationRevoked, "CRL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := readBytes(t, archive(t, signed, tc.vd, tsaCert, tsaKey))
			approval, _ := approvalAndTimestamps(verifySigs(t, d, sign.VerifyOptions{Roots: roots}))
			if len(approval) != 1 || !approval[0].TrustedChain {
				t.Fatalf("want one trusted signature: %+v", approval)
			}
			if got := approval[0].Revocation; got.Status != tc.want || got.Source != tc.wantS {
				t.Errorf("revocation = %v/%q, want %v/%q", got.Status, got.Source, tc.want, tc.wantS)
			}
		})
	}
}

// TestSignerPurposePolicy is audit 2026-09-22 C22: a chain to a trusted root
// is not enough; the certificate must be for signing documents. A TLS server
// certificate issued by a CA the caller trusts used to be a "trusted signer".
func TestSignerPurposePolicy(t *testing.T) {
	ca, caKey := signtest.CA(t, "pdf0 purpose CA")
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	tmpl := func(ku x509.KeyUsage, eku []x509.ExtKeyUsage, unknown []asn1.ObjectIdentifier) *x509.Certificate {
		c := leafTemplate("purpose")
		c.KeyUsage, c.ExtKeyUsage, c.UnknownExtKeyUsage = ku, eku, unknown
		return c
	}
	for _, tc := range []struct {
		name    string
		cert    *x509.Certificate
		trusted bool
		errHas  string
	}{
		{"TLS server certificate", tmpl(x509.KeyUsageDigitalSignature|x509.KeyUsageKeyEncipherment, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, nil), false, "extended key usage"},
		{"TLS server certificate without key usage", tmpl(0, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, nil), false, "key usage"},
		{"no key usage extension", tmpl(0, nil, nil), false, "key usage"},
		{"key encipherment only", tmpl(x509.KeyUsageKeyEncipherment, nil, nil), false, "digitalSignature"},
		{"digitalSignature, no EKU", tmpl(x509.KeyUsageDigitalSignature, nil, nil), true, ""},
		{"contentCommitment, no EKU", tmpl(x509.KeyUsageContentCommitment, nil, nil), true, ""},
		{"emailProtection", tmpl(x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}, nil), true, ""},
		{"id-kp-documentSigning", tmpl(x509.KeyUsageDigitalSignature, nil, []asn1.ObjectIdentifier{{1, 3, 6, 1, 5, 5, 7, 3, 36}}), true, ""},
		{"Adobe Authentic Documents", tmpl(x509.KeyUsageDigitalSignature, nil, []asn1.ObjectIdentifier{{1, 2, 840, 113583, 1, 1, 5}}), true, ""},
		{"code signing only", tmpl(x509.KeyUsageDigitalSignature, []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}, nil), false, "extended key usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signed, _ := signedByLeaf(t, ca, caKey, tc.cert)
			res := verifySigs(t, readBytes(t, signed), sign.VerifyOptions{Roots: roots})
			if len(res) != 1 || !res[0].Valid || !res[0].Intact() {
				t.Fatalf("the signature itself is sound: %+v", res)
			}
			if res[0].TrustedChain != tc.trusted {
				t.Fatalf("TrustedChain = %v, want %v (chainErr %v)", res[0].TrustedChain, tc.trusted, res[0].ChainErr)
			}
			if !tc.trusted && (res[0].ChainErr == nil || !strings.Contains(res[0].ChainErr.Error(), tc.errHas)) {
				t.Errorf("ChainErr = %v, want it to mention %q", res[0].ChainErr, tc.errHas)
			}
		})
	}

	// An intermediate restricted to TLS cannot issue document signers.
	inter, interKey := signtest.Issue(t, &x509.Certificate{
		SerialNumber: big.NewInt(77), Subject: pkix.Name{CommonName: "TLS-only intermediate"},
		NotBefore: signtest.NotBefore, NotAfter: signtest.NotAfter, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, caKey)
	leaf, leafKey := signtest.Issue(t, tmpl(x509.KeyUsageDigitalSignature, nil, nil), inter, interKey)
	d := readBytes(t, buildMinimalPDF())
	var buf bytes.Buffer
	if err := d.WriteSigned(&buf, leaf, leafKey); err != nil {
		t.Fatal(err)
	}
	// The intermediate travels in the DSS, as B-LT material does.
	tsaCert, tsaKey := signtest.TSACertKey(t)
	res := verifySigs(t, readBytes(t, archive(t, buf.Bytes(), ValidationData{Certs: []*x509.Certificate{inter}}, tsaCert, tsaKey)), sign.VerifyOptions{Roots: roots})
	approval, _ := approvalAndTimestamps(res)
	if len(approval) != 1 || !approval[0].Valid {
		t.Fatalf("want one valid signature: %+v", approval)
	}
	if approval[0].TrustedChain || approval[0].ChainErr == nil || !strings.Contains(approval[0].ChainErr.Error(), "intermediate") {
		t.Errorf("a signer under an intermediate restricted to serverAuth must not be trusted: trusted=%v chainErr=%v", approval[0].TrustedChain, approval[0].ChainErr)
	}
}
