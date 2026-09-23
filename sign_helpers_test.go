package pdf0

import (
	"testing"

	"github.com/mgilbir/pdf0/sign"
)

// verifySigs is Document.VerifySignatures for a test: an error — which means
// verification could not run, never "no signatures" — fails the test.
func verifySigs(t testing.TB, d *Document, opts sign.VerifyOptions) []sign.Result {
	t.Helper()
	res, err := d.VerifySignatures(opts)
	if err != nil {
		t.Fatalf("VerifySignatures: %v", err)
	}
	return res
}

// coveringDocTimestamp reports whether a document time-stamp in d verifies
// over the whole file d was read from — the outermost archival seal — and
// every signature and time-stamp in d is intact: each verifies, and every
// change after it is permitted.
func coveringDocTimestamp(t testing.TB, d *Document) bool {
	t.Helper()
	covers := false
	for _, r := range verifySigs(t, d, sign.VerifyOptions{}) {
		if !r.Intact() {
			t.Errorf("%s (time-stamp=%v) is not intact: valid=%v err=%v disallowed=%v", r.Field, r.DocTimestamp, r.Valid, r.Err, r.DisallowedChanges)
		}
		if r.DocTimestamp && r.DocumentUnmodified() {
			covers = true
		}
	}
	return covers
}

// padesOf is Document.ValidatePAdES for a test, with the same error handling.
func padesOf(t testing.TB, d *Document, opts sign.VerifyOptions) []sign.PAdESResult {
	t.Helper()
	res, err := d.ValidatePAdES(opts)
	if err != nil {
		t.Fatalf("ValidatePAdES: %v", err)
	}
	return res
}
