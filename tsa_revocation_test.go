package pdf0

import (
	"testing"
	"time"
)

// TestTimestampRequiresTimeStampingEKU is the C10 guard: a time-stamp token
// signed by a certificate without the id-kp-timeStamping extended key usage must
// be rejected, so an attacker cannot pass off an arbitrary self-signed
// certificate as a TSA.
func TestTimestampRequiresTimeStampingEKU(t *testing.T) {
	imprint := []byte("the bytes being time-stamped")

	// A general (non-TSA) certificate must be rejected as a TSA.
	badCert, badKey := testCertKey(t)
	token, err := buildTimestampToken(imprint, badCert, badKey, time.Now())
	if err != nil {
		t.Fatalf("buildTimestampToken: %v", err)
	}
	if _, _, err := verifyTimestampToken(token, imprint); err == nil {
		t.Fatal("a token signed by a non-TSA certificate must not verify")
	}

	// A certificate carrying the timeStamping EKU is accepted.
	tsaCert, tsaKey := testTSACertKey(t)
	good, err := buildTimestampToken(imprint, tsaCert, tsaKey, time.Now())
	if err != nil {
		t.Fatalf("buildTimestampToken (TSA): %v", err)
	}
	if _, _, err := verifyTimestampToken(good, imprint); err != nil {
		t.Fatalf("a token from a proper TSA certificate should verify: %v", err)
	}
}

// TestRevocationFreshness is the C13 guard: revocation material outside its
// validity window (expired, superseded, or not yet in force) is not
// authoritative, so a stale response cannot be replayed to mask a later change.
func TestRevocationFreshness(t *testing.T) {
	now := time.Now()
	if !revocationFresh(now.Add(-time.Hour), now.Add(time.Hour)) {
		t.Error("current material (thisUpdate past, nextUpdate future) must be fresh")
	}
	if revocationFresh(now.Add(-2*time.Hour), now.Add(-time.Hour)) {
		t.Error("expired material (nextUpdate in the past) must be rejected")
	}
	if revocationFresh(now.Add(time.Hour), now.Add(2*time.Hour)) {
		t.Error("not-yet-valid material (thisUpdate in the future) must be rejected")
	}
	if !revocationFresh(now.Add(-time.Hour), time.Time{}) {
		t.Error("material without a nextUpdate (no expiry) must be fresh when in force")
	}
}
