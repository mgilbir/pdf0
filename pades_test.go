package pdf0

import (
	"bytes"
	"crypto/x509"
	"strings"
	"testing"
)

// TestPAdESRoundTrip signs a document (pdf0 now produces PAdES-B-B: the
// ETSI.CAdES.detached sub-filter and a CAdES signing-certificate attribute) and
// checks that ValidatePAdES reports a conformant B-B signature.
func TestPAdESRoundTrip(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	out := buf.Bytes()

	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res := signed.ValidatePAdES(out)
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
	if !r.CoversDocument {
		t.Error("signature should cover the document")
	}
	if !r.Conformant {
		t.Errorf("expected a conformant B-B signature, got issues: %v", r.Issues)
	}
	if r.Level != PAdESBB {
		t.Errorf("level = %q, want B-B (no timestamp/DSS present)", r.Level)
	}
}

// TestPAdESTamperDetected confirms that modifying the signed content makes the
// signature non-conformant (it no longer verifies).
func TestPAdESTamperDetected(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, _ := Read(bytes.NewReader(base), int64(len(base)))
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	// Verify against tampered bytes (reusing the clean parse, as the CMS digest
	// is recomputed over the supplied bytes): flipping a signed byte breaks it.
	tampered := append([]byte(nil), out...)
	tampered[0] ^= 0xFF
	res := signed.ValidatePAdES(tampered)
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
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, _ := Read(bytes.NewReader(base), int64(len(base)))
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	// Rewrite the sub-filter in the output to the legacy value (same length).
	out = bytes.Replace(out, []byte("/ETSI.CAdES.detached"), []byte("/adbe.pkcs7.detached"), 1)
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	res := signed.ValidatePAdES(out)
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].IsPAdES {
		t.Error("adbe.pkcs7.detached must not be reported as PAdES")
	}
	if res[0].Level != PAdESNone {
		t.Errorf("legacy signature level = %q, want none", res[0].Level)
	}
}

// TestPAdESLevelBLT constructs the document-level long-term material (a /DSS in
// the catalog) and a signature timestamp, and checks the level rises to B-LT.
func TestPAdESLevelDetection(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, _ := Read(bytes.NewReader(base), int64(len(base)))
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	// Baseline: no DSS, no timestamp -> B-B.
	if got := signed.ValidatePAdES(out)[0].Level; got != PAdESBB {
		t.Fatalf("baseline level = %q, want B-B", got)
	}
	// Add a catalog /DSS. Without a signature timestamp the level stays B-B
	// (each PAdES level requires the previous), which guards the ordering.
	cat := getCatalog(signed)
	cat.Set("DSS", &Dictionary{})
	if got := signed.ValidatePAdES(out)[0].Level; got != PAdESBB {
		t.Errorf("DSS without a timestamp must not reach B-LT; level = %q", got)
	}
}

// TestPAdESBTTimestamp signs with an RFC 3161 signature time-stamp (a local TSA)
// and checks that ValidatePAdES reaches level B-T with a cryptographically
// verified time-stamp.
func TestPAdESBTTimestamp(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSignedTimestamped(&buf, cert, key, cert, key); err != nil {
		t.Fatalf("WriteSignedTimestamped: %v", err)
	}
	out := buf.Bytes()
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	res := signed.ValidatePAdES(out)
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
	if r.TimestampTime.IsZero() {
		t.Error("expected a time-stamp time")
	}
	if r.Level != PAdESBT {
		t.Errorf("level = %q, want B-T", r.Level)
	}
	if !r.Conformant {
		t.Errorf("expected a conformant signature, issues: %v", r.Issues)
	}
}

// TestPAdESBLTA signs B-T, then adds a DSS and a document time-stamp as an
// incremental update, and checks the signature is assessed at level B-LTA with
// its original signature still valid.
func TestPAdESBLTA(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var b1 bytes.Buffer
	if err := doc.WriteSignedTimestamped(&b1, cert, key, cert, key); err != nil {
		t.Fatalf("WriteSignedTimestamped: %v", err)
	}
	o1 := b1.Bytes()

	d1, err := Read(bytes.NewReader(o1), int64(len(o1)))
	if err != nil {
		t.Fatal(err)
	}
	var b2 bytes.Buffer
	if err := d1.WriteArchivalTimestamp(&b2, o1, []*x509.Certificate{cert}, cert, key); err != nil {
		t.Fatalf("WriteArchivalTimestamp: %v", err)
	}
	o2 := b2.Bytes()

	d2, err := Read(bytes.NewReader(o2), int64(len(o2)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res := d2.ValidatePAdES(o2)
	var lta *PAdESResult
	for i := range res {
		if res[i].Level == PAdESBLTA {
			lta = &res[i]
		}
	}
	if lta == nil {
		t.Fatalf("expected a B-LTA signature; got %+v", res)
	}
	if !lta.Valid {
		t.Error("the approval signature should still verify after the archival timestamp")
	}
	// The approval signature covers only its own revision — the appended DSS and
	// document time-stamp are not under its /ByteRange — yet it stays conformant
	// because the covering document time-stamp seals the rest.
	if lta.CoversDocument {
		t.Error("the approval signature should not cover the appended archival revision")
	}
	for _, iss := range lta.Issues {
		if strings.Contains(iss, "does not cover the whole document") {
			t.Errorf("byte-range issue should be relaxed under a covering document time-stamp: %q", iss)
		}
	}
	if !lta.Conformant {
		t.Errorf("a sealed B-LTA approval signature should be conformant; issues: %v", lta.Issues)
	}
}

// TestPAdESUncoveredNotSealed checks the relaxation is specific: an approval
// signature that does not cover the whole document is still flagged when there is
// no covering, verifying document time-stamp to seal the trailing bytes.
func TestPAdESUncoveredNotSealed(t *testing.T) {
	cert, key := testCertKey(t)
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var b1 bytes.Buffer
	if err := doc.WriteSigned(&b1, cert, key); err != nil {
		t.Fatalf("WriteSigned: %v", err)
	}
	o1 := b1.Bytes()
	// Append arbitrary bytes so the signature no longer reaches the end of file,
	// with no document time-stamp covering them.
	o1 = append(o1, []byte("\n% trailing bytes not covered by any signature\n")...)

	d1, err := Read(bytes.NewReader(o1), int64(len(o1)))
	if err != nil {
		t.Fatal(err)
	}
	res := d1.ValidatePAdES(o1)
	if len(res) != 1 {
		t.Fatalf("expected one signature; got %d", len(res))
	}
	if res[0].CoversDocument {
		t.Fatal("the signature should not cover the appended bytes")
	}
	found := false
	for _, iss := range res[0].Issues {
		if strings.Contains(iss, "does not cover the whole document") {
			found = true
		}
	}
	if !found {
		t.Errorf("an uncovered signature without a sealing time-stamp should be flagged; issues: %v", res[0].Issues)
	}
}
