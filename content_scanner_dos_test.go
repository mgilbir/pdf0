package pdf0

import (
	"testing"
	"time"
)

// TestContentScannersTerminateOnStrayParen guards against a parser DoS: a
// content stream with an unmatched ')' (e.g. leaked inline-image sample data)
// once spun the token scanners forever because ')' is a delimiter that the
// default token read cannot consume, leaving the cursor unadvanced. Each
// scanner must terminate.
func TestContentScannersTerminateOnStrayParen(t *testing.T) {
	inputs := [][]byte{
		[]byte(")"),
		[]byte("BT /F1 12 Tf ) Tj ET"),
		[]byte(") ) ) ) )"),
		[]byte("q 1 0 0 1 0 0 cm ))) /X Do Q"),
		append([]byte("/P <</MCID 0>>BDC "), []byte(")))abc)))def")...),
	}
	run := func(name string, fn func()) {
		done := make(chan struct{})
		go func() { defer close(done); fn() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not terminate on stray ')'", name)
		}
	}
	for _, in := range inputs {
		in := in
		run("forEachContentItem", func() { forEachContentItem(in, func(contentItemKind, []byte) {}) })
		run("forEachContentToken", func() { forEachContentToken(in, func([]byte, bool) {}) })
		run("contentUsedNames", func() { contentUsedNames(in) })
	}
}
