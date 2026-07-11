package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// buildTwoObjStmPDF assembles a PDF with a direct catalog/pages/page plus two
// object streams: container 1 holds object 6 and container 7 holds object 8,
// each a filler dictionary padded to roughly fillerBytes so the test can drive
// the aggregate decompression budget. Object numbering and the xref stream are
// laid out by hand.
func buildTwoObjStmPDF(t *testing.T, fillerBytes int) []byte {
	t.Helper()
	pad := strings.Repeat("A", fillerBytes)
	filler6 := fmt.Sprintf("<< /Filler 6 /Data (%s) >>", pad)
	filler8 := fmt.Sprintf("<< /Filler 8 /Data (%s) >>", pad)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	off := map[int]int{}

	writeObjStm := func(objNum, heldNum int, body string) {
		stm := makeObjStm(t, map[int]string{heldNum: body}, []int{heldNum}, true)
		off[objNum] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /ObjStm /N 1 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
			objNum, mustInt(t, stm.Dict.Get("First")), len(stm.Data))
		buf.Write(stm.Data)
		buf.WriteString("\nendstream\nendobj\n")
	}
	writeDirect := func(objNum int, body string) {
		off[objNum] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", objNum, body)
	}

	writeObjStm(1, 6, filler6)
	writeDirect(3, "<< /Type /Catalog /Pages 4 0 R >>")
	writeDirect(4, "<< /Type /Pages /Kids [5 0 R] /Count 1 >>")
	writeDirect(5, "<< /Type /Page /Parent 4 0 R /MediaBox [0 0 612 792] >>")
	writeObjStm(7, 8, filler8)

	// Object 2: the xref stream, W [1 3 1], objects 0..8.
	xrefStart := buf.Len()
	off[2] = xrefStart
	type3 := func(a int, b int, c int) []byte { return []byte{byte(a), byte(b >> 16), byte(b >> 8), byte(b), byte(c)} }
	entries := [][]byte{
		type3(0, 0, 255),      // 0 free
		type3(1, off[1], 0),   // 1 ObjStm A
		type3(1, off[2], 0),   // 2 xref
		type3(1, off[3], 0),   // 3 catalog
		type3(1, off[4], 0),   // 4 pages
		type3(1, off[5], 0),   // 5 page
		type3(2, 1, 0),        // 6 in stream 1, index 0
		type3(1, off[7], 0),   // 7 ObjStm B
		type3(2, 7, 0),        // 8 in stream 7, index 0
	}
	var raw bytes.Buffer
	for _, e := range entries {
		raw.Write(e)
	}
	var xz bytes.Buffer
	zw := zlib.NewWriter(&xz)
	zw.Write(raw.Bytes())
	zw.Close()
	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /XRef /Size 9 /W [1 3 1] /Root 3 0 R /Filter /FlateDecode /Length %d >>\nstream\n", xz.Len())
	buf.Write(xz.Bytes())
	buf.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefStart)
	return buf.Bytes()
}

// TestObjStmDecompressionBudget verifies that once the aggregate object-stream
// decompression budget is exhausted, further object streams are left
// unmaterialized (recorded as broken) rather than parsed — bounding the work a
// small, heavily-amplified file can force — while a normal budget loads both.
func TestObjStmDecompressionBudget(t *testing.T) {
	const filler = 4000
	pdf := buildTwoObjStmPDF(t, filler)

	// Default (large) budget: both compressed objects load.
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, n := range []int{6, 8} {
		if _, ok := doc.Objects[n]; !ok {
			t.Fatalf("under default budget, compressed object %d not loaded", n)
		}
	}
	if len(doc.brokenObjStms) != 0 {
		t.Fatalf("under default budget, unexpected broken object streams: %v", doc.brokenObjStms)
	}

	// Lower the budget below one stream's decompressed size: the first stream
	// (container 1) still loads because the budget is only consulted before a
	// stream is decoded (it starts at zero), but it exhausts the budget, so the
	// second stream (container 7) is skipped.
	saved := maxObjStmDecompressedTotal
	maxObjStmDecompressedTotal = filler / 2
	defer func() { maxObjStmDecompressedTotal = saved }()

	doc2, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatalf("read with lowered budget: %v", err)
	}
	if _, ok := doc2.Objects[6]; !ok {
		t.Error("object 6 (first object stream) should still load within budget")
	}
	if _, ok := doc2.Objects[8]; ok {
		t.Error("object 8 (second object stream) should be skipped once the budget is exhausted")
	}
	found := false
	for _, n := range doc2.brokenObjStms {
		if n == 7 {
			found = true
		}
	}
	if !found {
		t.Errorf("object stream 7 should be recorded as broken; got %v", doc2.brokenObjStms)
	}
}
