package pdf0

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// TestAllDelimitedKeywordsEquivalence pins allDelimitedKeywords +
// firstKeywordAtOrAfter to the same results a per-position findDelimitedKeyword
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
			// Reference: repeatedly call findDelimitedKeyword advancing past each hit.
			var want []int64
			for pos := int64(0); ; {
				at := findDelimitedKeyword(data, pos, kw, ws)
				if at < 0 {
					break
				}
				want = append(want, at)
				pos = at + 1
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("allDelimitedKeywords(%q, ws=%v) = %v, want %v", kw, ws, got, want)
			}
			// firstKeywordAtOrAfter must match findDelimitedKeyword at every search
			// start, except the degenerate case where the start sits exactly on a
			// keyword that lacks leading whitespace: findDelimitedKeyword accepts it
			// via its `at == start` clause. That never happens in checkStreamLengthBytes
			// (a search always starts at an "N G obj" offset, a digit — never on a
			// keyword), so the precompute deliberately omits it.
			for pos := int64(0); pos <= int64(len(data)); pos++ {
				b := findDelimitedKeyword(data, pos, kw, ws)
				if ws && b == pos && pos > 0 && !isWhitespace(data[pos-1]) {
					continue // the `at == start` special case, irrelevant to real usage
				}
				if a := firstKeywordAtOrAfter(got, pos); a != b {
					t.Fatalf("%q ws=%v pos=%d: firstKeywordAtOrAfter=%d findDelimitedKeyword=%d", kw, ws, pos, a, b)
				}
			}
		}
	}
}

// TestCheckStreamLengthNoQuadraticScan guards the byte-level stream-length check
// against the O(objects × filesize) blow-up it once had. When a producer writes
// the stream keyword with no whitespace before it ("...>>stream"), the per-object
// forward search for a whitespace-delimited "stream" matched none and scanned to
// end of file every time; a real 27 MB file with ~40k such streams spent >80 s in
// this one check. With the single-pass precompute the whole validation stays fast.
func TestCheckStreamLengthNoQuadraticScan(t *testing.T) {
	const n = 25000        // stream objects
	const dataLen = 500    // bytes per stream
	body := bytes.Repeat([]byte("A"), dataLen)

	var buf bytes.Buffer
	offsets := make([]int, n+4)
	buf.WriteString("%PDF-1.7\n")
	write := func(num int, s string) {
		offsets[num] = buf.Len()
		buf.WriteString(s)
	}
	write(1, "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	write(2, "2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	write(3, "3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")
	// Filler streams whose stream keyword has NO leading whitespace ("...>>stream").
	for i := 4; i < n+4; i++ {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d>>stream\r\n", i, dataLen)
		buf.Write(body)
		buf.WriteString("\r\nendstream\nendobj\n")
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", n+4)
	for i := 1; i < n+4; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", n+4, xref)

	raw := buf.Bytes()
	doc, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	start := time.Now()
	done := make(chan struct{}, 1)
	go func() { _ = ValidatePDFABytes(doc, PDFA4, raw); done <- struct{}{} }()
	select {
	case <-done:
	case <-time.After(40 * time.Second):
		t.Fatal("ValidatePDFA did not finish within 40s on a many-stream file; the byte-level length check is scanning per object again")
	}
	if el := time.Since(start); el > 20*time.Second {
		t.Errorf("validation took %v on %d streams; expected the single-pass keyword precompute to keep it near-linear", el, n)
	}
}
