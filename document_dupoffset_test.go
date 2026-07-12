package pdf0

import (
	"bytes"
	"fmt"
	"testing"
)

// buildDupOffsetPDF assembles a PDF whose cross-reference table points several
// distinct object numbers at the SAME byte offset — the offset of one stream
// object carrying dataLen bytes. This is malformed but a reader must tolerate
// it without re-materializing (re-allocating) the stream once per number: a
// real file was found with 819 xref entries all aimed at one 7.7 MB stream,
// which expanded to 6.3 GB of stream data on read.
//
// Objects 1..3 are a minimal catalog/pages/page. Object 4 is the stream. The
// xref then maps object numbers 4..(4+dups-1) all to object 4's offset.
func buildDupOffsetPDF(t *testing.T, dataLen, dups int) []byte {
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

	// Object 4: the one real stream. Its data ends in a non-whitespace byte to
	// make sure length handling, not an endstream search, governs the read.
	payload := make([]byte, dataLen)
	for i := range payload {
		payload[i] = 'A'
	}
	payload[dataLen-1] = 0x01
	off[4] = buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n", dataLen)
	buf.Write(payload)
	buf.WriteString("endstream\nendobj\n")

	size := 4 + dups // objects 0..(4+dups-1); 4..(4+dups-1) all share offset off[4]
	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", size)
	buf.WriteString("0000000000 65535 f \n")
	for n := 1; n < size; n++ {
		o := off[n]
		if n >= 4 {
			o = off[4] // duplicate: every number >= 4 points at object 4's bytes
		}
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", size, xrefStart)
	return buf.Bytes()
}

// TestReadDuplicateXrefOffsetShares verifies that when many object numbers point
// at the same offset, the object is parsed once and its value shared, rather
// than re-materialized per number. It asserts pointer identity of the shared
// stream (so its data is allocated once) while each object keeps its own
// authoritative number.
func TestReadDuplicateXrefOffsetShares(t *testing.T) {
	const dataLen = 4096
	const dups = 50 // object numbers 4..53 all point at object 4's offset

	pdf := buildDupOffsetPDF(t, dataLen, dups)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var first *Stream
	distinct := map[*Stream]bool{}
	for n := 4; n < 4+dups; n++ {
		io, ok := doc.Objects[n]
		if !ok {
			t.Fatalf("object %d missing", n)
		}
		if io.Number != n {
			t.Errorf("object %d has Number %d; each duplicate must keep its own number", n, io.Number)
		}
		st, ok := io.Value.(*Stream)
		if !ok {
			t.Fatalf("object %d value is %T, want *Stream", n, io.Value)
		}
		distinct[st] = true
		if first == nil {
			first = st
		}
		if len(st.Data) != dataLen {
			t.Errorf("object %d stream data len = %d, want %d", n, len(st.Data), dataLen)
		}
	}
	// All duplicate-offset objects must share ONE *Stream — proof the data was
	// allocated once, not `dups` times.
	if len(distinct) != 1 {
		t.Fatalf("duplicate-offset objects hold %d distinct streams, want 1 (re-materialized)", len(distinct))
	}
	// And the shared data slice is backed by the same array.
	for n := 4; n < 4+dups; n++ {
		st := doc.Objects[n].Value.(*Stream)
		if len(st.Data) > 0 && len(first.Data) > 0 && &st.Data[0] != &first.Data[0] {
			t.Fatalf("object %d data is a separate allocation; expected the shared backing array", n)
		}
	}
}
