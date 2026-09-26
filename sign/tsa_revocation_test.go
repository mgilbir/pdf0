package sign

import (
	"crypto"
	"encoding/asn1"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/signtest"
)

// TestTimestampRequiresTimeStampingEKU is the C10 guard: a time-stamp token
// signed by a certificate without the id-kp-timeStamping extended key usage must
// be rejected, so an attacker cannot pass off an arbitrary self-signed
// certificate as a TSA.
func TestTimestampRequiresTimeStampingEKU(t *testing.T) {
	imprint := []byte("the bytes being time-stamped")

	// A general (non-TSA) certificate must be rejected as a TSA.
	badCert, badKey := signtest.CertKey(t)
	token, err := BuildTimestampToken(imprint, badCert, badKey, time.Now())
	if err != nil {
		t.Fatalf("buildTimestampToken: %v", err)
	}
	if _, err := verifyTimestampToken(token, digestOf(imprint)); err == nil {
		t.Fatal("a token signed by a non-TSA certificate must not verify")
	}

	// A certificate carrying the timeStamping EKU is accepted.
	tsaCert, tsaKey := signtest.TSACertKey(t)
	good, err := BuildTimestampToken(imprint, tsaCert, tsaKey, time.Now())
	if err != nil {
		t.Fatalf("buildTimestampToken (TSA): %v", err)
	}
	if _, err := verifyTimestampToken(good, digestOf(imprint)); err != nil {
		t.Fatalf("a token from a proper TSA certificate should verify: %v", err)
	}
}

// TestTimestampRequiresTSTInfoContent pins the eContentType check (audit
// 2026-09-22 C154): a SignedData by a time-stamping certificate whose content
// is not id-ct-TSTInfo is not a time-stamp token, however well it is signed.
// The content here is a real TSTInfo — only its declared type differs — so
// nothing else in the token can be what rejects it.
func TestTimestampRequiresTSTInfoContent(t *testing.T) {
	imprint := []byte("the bytes being time-stamped")
	tsaCert, tsaKey := signtest.TSACertKey(t)
	h := digestOf(imprint)
	sum, _ := h(crypto.SHA256)
	info := tstInfo{
		Version:        1,
		Policy:         oidTSAPolicy,
		MessageImprint: messageImprint{HashAlgorithm: pkixAlgorithmIdentifier{Algorithm: oidSHA256}, HashedMessage: sum},
		SerialNumber:   big.NewInt(1),
		GenTime:        time.Now().UTC().Truncate(time.Second),
	}
	tstDER, err := asn1.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := buildSignedDataEmbedded(tsaCert, tsaKey, tstDER, oidData)
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifyTimestampToken(forged, h)
	if err == nil || !strings.Contains(err.Error(), "id-ct-TSTInfo") {
		t.Fatalf("a SignedData of id-data content verified as a time-stamp: err = %v", err)
	}
	genuine, err := buildSignedDataEmbedded(tsaCert, tsaKey, tstDER, oidTSTInfo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyTimestampToken(genuine, h); err != nil {
		t.Fatalf("the same TSTInfo as id-ct-TSTInfo must verify: %v", err)
	}
}

// TestRevocationCurrentAt pins the freshness window (audit 2026-07-26 C13,
// 2026-09-22 C56): material counts as current at a time only between its
// thisUpdate and nextUpdate, and material with no nextUpdate is not current
// for ever — a CRL without one is non-conforming (RFC 5280 5.1.2.5) and never
// current, an OCSP response without one only at the moment it was produced
// (RFC 6960 4.2.2.1).
func TestRevocationCurrentAt(t *testing.T) {
	now := time.Now()
	for _, ocsp := range []bool{false, true} {
		if !currentAt(now.Add(-time.Hour), now.Add(time.Hour), now, ocsp) {
			t.Errorf("ocsp=%v: current material (thisUpdate past, nextUpdate future) must be current", ocsp)
		}
		if currentAt(now.Add(-2*time.Hour), now.Add(-time.Hour), now, ocsp) {
			t.Errorf("ocsp=%v: expired material (nextUpdate in the past) must be rejected", ocsp)
		}
		if currentAt(now.Add(time.Hour), now.Add(2*time.Hour), now, ocsp) {
			t.Errorf("ocsp=%v: not-yet-valid material (thisUpdate in the future) must be rejected", ocsp)
		}
		if currentAt(now.Add(-time.Hour), time.Time{}, now, ocsp) {
			t.Errorf("ocsp=%v: material without a nextUpdate issued an hour ago must not be current now", ocsp)
		}
		// The window is judged at the validation time, not the wall clock: a
		// week-old response is current at the time of a signature it
		// predates.
		old := now.Add(-7 * 24 * time.Hour)
		if !currentAt(old.Add(-time.Hour), old.Add(24*time.Hour), old, ocsp) {
			t.Errorf("ocsp=%v: material current at the validation time must count, however old", ocsp)
		}
	}
	if !currentAt(now.Add(-time.Minute), time.Time{}, now, true) {
		t.Error("an OCSP response without nextUpdate is current at the moment it was produced")
	}
	if currentAt(now.Add(-time.Minute), time.Time{}, now, false) {
		t.Error("a CRL without nextUpdate is never current")
	}
}
