package pdf0

import (
	"bytes"
	"fmt"
	"testing"
)

// buildIndirectLengthStreamsPDF assembles a PDF whose stream objects each
// declare their /Length as an indirect reference to a separate integer object,
// and whose raw stream data ends in a non-whitespace byte (0x01) immediately
// before the endstream keyword — with no EOL in between. This is a legal,
// common shape: a compressed stream may end in any byte, and /Length is often a
// forward-referenced object.
//
// It is also the shape that provoked a pathological over-read. When /Length is
// indirect the parser cannot use it inline, so it fell back to searching for
// endstream; that search skips any endstream keyword not preceded by
// whitespace, so it stepped over each stream's real endstream and slurped
// forward to a distant one. With every stream doing so, a small file expanded
// to O(n^2) stream bytes on read (a 10 MB file was seen to reach 8 GB).
//
// nStreams streams are laid out, each carrying dataLen bytes.
func buildIndirectLengthStreamsPDF(t *testing.T, nStreams, dataLen int) []byte {
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

	// Stream i uses object number streamNum(i); its length object is
	// streamNum(i)+1. Both are emitted together, the length object *after* the
	// stream, so the /Length reference is a forward reference.
	streamNum := func(i int) int { return 4 + 2*i }
	payload := make([]byte, dataLen)
	for j := range payload {
		payload[j] = 'A'
	}
	payload[dataLen-1] = 0x01 // non-whitespace final byte, no EOL before endstream

	for i := 0; i < nStreams; i++ {
		sn := streamNum(i)
		ln := sn + 1
		off[sn] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d 0 R >>\nstream\n", sn, ln)
		buf.Write(payload)
		buf.WriteString("endstream\nendobj\n")
		off[ln] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%d\nendobj\n", ln, dataLen)
	}

	size := 4 + 2*nStreams // objects 0..(4+2n-1)
	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", size)
	buf.WriteString("0000000000 65535 f \n")
	for n := 1; n < size; n++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off[n])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", size, xrefStart)
	return buf.Bytes()
}

// TestParseIndirectLengthNoOverread verifies that a stream with an indirect
// /Length whose data ends in a non-whitespace byte is read by its true length,
// not by an endstream search that over-reads. It is both a correctness check
// (each stream's Data is exactly its declared length) and a DoS guard (total
// stream data stays proportional to the file, not quadratic).
func TestParseIndirectLengthNoOverread(t *testing.T) {
	const nStreams = 8
	const dataLen = 64

	pdf := buildIndirectLengthStreamsPDF(t, nStreams, dataLen)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	total := 0
	found := 0
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
	// Total must be exactly nStreams*dataLen — any over-read inflates it.
	if want := nStreams * dataLen; total != want {
		t.Fatalf("total stream data = %d bytes, want %d (quadratic over-read)", total, want)
	}
}

// TestIntegerObjectValue checks the lightweight integer-object reader that backs
// indirect /Length resolution: it accepts exactly "N G obj <int>" and rejects
// anything else, without parsing large composite values.
func TestIntegerObjectValue(t *testing.T) {
	ok := func(src string, want int64) {
		t.Helper()
		v, got := NewParser([]byte(src)).integerObjectValue()
		if !got || v != want {
			t.Errorf("integerObjectValue(%q) = (%d, %v), want (%d, true)", src, v, got, want)
		}
	}
	bad := func(src string) {
		t.Helper()
		if v, got := NewParser([]byte(src)).integerObjectValue(); got {
			t.Errorf("integerObjectValue(%q) = (%d, true), want false", src, v)
		}
	}
	ok("9 0 obj 12 endobj", 12)
	ok("100 5 obj 0", 0)
	bad("9 0 obj << /A 1 >>")     // composite value, not an integer
	bad("9 0 obj [1 2 3]")        // array value
	bad("9 0 obj -3 endobj")      // negative length
	bad("9 0 R")                  // reference, not an object definition
	bad("<< /Length 1 >>")        // not an indirect object at all
}
