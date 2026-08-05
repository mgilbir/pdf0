package pdfa

import (
	"fmt"
	"github.com/mgilbir/pdf0/syntax"
	"testing"
)

// TestAllDelimitedKeywordsEquivalence pins allDelimitedKeywords +
// firstKeywordAtOrAfter to the same results a per-position syntax.FindDelimitedKeyword
// scan produces. checkStreamLengthBytes relies on that equivalence to replace a
// per-object forward scan (O(objects × filesize)) with a one-pass precompute and
// a binary search, so any drift here would change validation output.
func TestAllDelimitedKeywordsEquivalence(t *testing.T) {
	// Tricky bytes: "endstream" (embeds "stream" preceded by 'd'), ">>stream"
	// (preceded by '>', not whitespace), a whitespace-preceded "stream", the
	// keyword at start of buffer, and one at end of buffer.
	data := []byte("stream x >>stream\n<< /L 1 >> stream\r\ndata endstream endobj foo endobj")
	for _, kw := range []string{"stream", "endobj", "endstream"} {
		for _, ws := range []bool{true, false} {
			got := allDelimitedKeywords(data, kw, ws)
			// Reference: repeatedly call syntax.FindDelimitedKeyword advancing past each hit.
			var want []int64
			for pos := int64(0); ; {
				at := syntax.FindDelimitedKeyword(data, pos, kw, ws)
				if at < 0 {
					break
				}
				want = append(want, at)
				pos = at + 1
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("allDelimitedKeywords(%q, ws=%v) = %v, want %v", kw, ws, got, want)
			}
			// firstKeywordAtOrAfter must match syntax.FindDelimitedKeyword at every search
			// start, except the degenerate case where the start sits exactly on a
			// keyword that lacks leading whitespace: syntax.FindDelimitedKeyword accepts it
			// via its `at == start` clause. That never happens in checkStreamLengthBytes
			// (a search always starts at an "N G obj" offset, a digit — never on a
			// keyword), so the precompute deliberately omits it.
			for pos := int64(0); pos <= int64(len(data)); pos++ {
				b := syntax.FindDelimitedKeyword(data, pos, kw, ws)
				if ws && b == pos && pos > 0 && !syntax.IsWhitespace(data[pos-1]) {
					continue // the `at == start` special case, irrelevant to real usage
				}
				if a := firstKeywordAtOrAfter(got, pos); a != b {
					t.Fatalf("%q ws=%v pos=%d: firstKeywordAtOrAfter=%d syntax.FindDelimitedKeyword=%d", kw, ws, pos, a, b)
				}
			}
		}
	}
}
