package pdf0

import (
	"bytes"
	"fmt"
	"testing"
)

// buildNoLengthStreamsPDF assembles a PDF whose stream objects declare NO
// /Length at all, forcing parseStream to locate endstream by search, and whose
// raw data ends in a non-whitespace byte (0x01) immediately before endstream.
//
// This is the shape that made the endstream search over-read: the search used
// to require the keyword be preceded by whitespace, so a legitimate endstream
// sitting right after binary data was skipped and the search slurped forward to
// a distant one (or ran off the end). With many such streams that is an O(n^2)
// over-read; with all of them ending this way the strict search finds nothing
// and the read fails outright.
func buildNoLengthStreamsPDF(t *testing.T, nStreams, dataLen int) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	off := map[int]int{}

	writeObj := func(num int, body string) {
		off[num] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>")

	payload := make([]byte, dataLen)
	for j := range payload {
		payload[j] = 'A'
	}
	payload[dataLen-1] = 0x01 // non-whitespace final byte, no EOL before endstream

	for i := 0; i < nStreams; i++ {
		num := 4 + i
		off[num] = buf.Len()
		// No /Length key at all → parseStream must search for endstream.
		fmt.Fprintf(&buf, "%d 0 obj\n<< >>\nstream\n", num)
		buf.Write(payload)
		buf.WriteString("endstream\nendobj\n")
	}

	size := 4 + nStreams
	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", size)
	buf.WriteString("0000000000 65535 f \n")
	for n := 1; n < size; n++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off[n])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", size, xrefStart)
	return buf.Bytes()
}

// TestParseNoLengthEndstreamNoOverread verifies that a stream with no /Length
// whose data ends in a non-whitespace byte is read up to the immediately
// following endstream — not over-read past it. It is both a correctness check
// (each stream's Data is exactly its written length) and a DoS guard (total
// stream data stays proportional to the file, not quadratic).
func TestParseNoLengthEndstreamNoOverread(t *testing.T) {
	const nStreams = 8
	const dataLen = 64

	pdf := buildNoLengthStreamsPDF(t, nStreams, dataLen)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	total, found := 0, 0
	for _, io := range doc.Objects {
		st, ok := io.Value.(*Stream)
		if !ok {
			continue
		}
		found++
		total += len(st.Data)
		if len(st.Data) != dataLen {
			t.Errorf("object %d: stream data len = %d, want %d (over-read)", io.Number, len(st.Data), dataLen)
		}
		if n := len(st.Data); n > 0 && st.Data[n-1] != 0x01 {
			t.Errorf("object %d: stream data last byte = %#x, want 0x01", io.Number, st.Data[n-1])
		}
	}
	if found != nStreams {
		t.Fatalf("found %d streams, want %d", found, nStreams)
	}
	if want := nStreams * dataLen; total != want {
		t.Fatalf("total stream data = %d bytes, want %d (quadratic over-read)", total, want)
	}
}

// TestFindDelimitedKeyword covers both modes of the keyword search: the
// endstream search (requireLeadingWS=false) accepts a keyword preceded by a
// non-whitespace byte, while the structural search (true) still requires
// whitespace so it does not match the "stream" inside "endstream".
func TestFindDelimitedKeyword(t *testing.T) {
	// endstream directly after a binary byte: found only in relaxed mode.
	d := []byte("\x01endstream\n")
	if got := findDelimitedKeyword(d, 0, "endstream", false); got != 1 {
		t.Errorf("relaxed endstream search = %d, want 1", got)
	}
	if got := findDelimitedKeyword(d, 0, "endstream", true); got != -1 {
		t.Errorf("strict search should reject non-whitespace-preceded keyword, got %d", got)
	}
	// The strict "stream" search must not match the trailing "stream" in
	// "endstream" (preceded by 'd'), but must find a real whitespace-delimited one.
	s := []byte("endstream\n stream\n")
	if got := findDelimitedKeyword(s, 0, "stream", true); got != 11 {
		t.Errorf("strict stream search = %d, want 11 (the standalone keyword)", got)
	}
}
