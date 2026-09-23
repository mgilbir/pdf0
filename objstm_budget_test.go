package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/hostile"
	"strings"
	"testing"
	"time"
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
	type3 := func(a int, b int, c int) []byte {
		return []byte{byte(a), byte(b >> 16), byte(b >> 8), byte(b), byte(c)}
	}
	entries := [][]byte{
		type3(0, 0, 255),    // 0 free
		type3(1, off[1], 0), // 1 ObjStm A
		type3(1, off[2], 0), // 2 xref
		type3(1, off[3], 0), // 3 catalog
		type3(1, off[4], 0), // 4 pages
		type3(1, off[5], 0), // 5 page
		type3(2, 1, 0),      // 6 in stream 1, index 0
		type3(1, off[7], 0), // 7 ObjStm B
		type3(2, 7, 0),      // 8 in stream 7, index 0
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
// materialisation budget is spent, further object streams are left
// unmaterialized (recorded as skipped, not broken) rather than parsed —
// bounding the work a small, heavily-amplified file can force — while a normal
// budget loads both.
func TestObjStmDecompressionBudget(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
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
		if len(doc.brokenObjStms) != 0 || len(doc.skippedObjStms) != 0 {
			t.Fatalf("under default budget, unexpected broken or skipped object streams: %v %v", doc.brokenObjStms, doc.skippedObjStms)
		}

		// Each container decodes to about 4 KB and materialises a dictionary
		// the meter charges about 4.3 KB for (syntax.MaterialCost per value
		// and key, plus the string's bytes). While one is unpacked its decoded
		// bytes are charged too, so the first needs about 8.4 KB and, once its
		// decoded bytes are returned, leaves 4.3 KB charged.
		//
		// A budget of 10 KB fits the first and not the second: object 6 loads,
		// object 8 does not, and container 7 is recorded as not unpacked by
		// request — never as broken (audit 2026-09-22 C47).
		doc2, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxObjectStreamBytes(10_000))
		if err != nil {
			t.Fatalf("read with lowered budget: %v", err)
		}
		if _, ok := doc2.Objects[6]; !ok {
			t.Error("object 6 (first object stream) should load within the budget")
		}
		if _, ok := doc2.Objects[8]; ok {
			t.Error("object 8 (second object stream) should be skipped once the budget is spent")
		}
		if len(doc2.brokenObjStms) != 0 {
			t.Errorf("a container skipped for the budget was recorded as broken: %v", doc2.brokenObjStms)
		}
		if len(doc2.skippedObjStms) != 1 || doc2.skippedObjStms[0] != (core.SkippedObjStm{Num: 7, Reason: core.ReasonLimit}) {
			t.Errorf("skipped = %v, want container 7 for the limit", doc2.skippedObjStms)
		}

		// A budget below one container's own size admits neither: the check
		// includes the container being unpacked (audit 2026-09-22 C9). It used
		// to be consulted only before a container, so the first one always
		// loaded whatever the budget.
		doc3, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxObjectStreamBytes(int64(filler/2)))
		if err != nil {
			t.Fatalf("read with a budget below one container: %v", err)
		}
		for _, n := range []int{6, 8} {
			if _, ok := doc3.Objects[n]; ok {
				t.Errorf("object %d loaded under a budget smaller than its container", n)
			}
		}
		if len(doc3.skippedObjStms) != 2 || len(doc3.brokenObjStms) != 0 {
			t.Errorf("skipped %v, broken %v; want both containers skipped and none broken", doc3.skippedObjStms, doc3.brokenObjStms)
		}
	})
}
