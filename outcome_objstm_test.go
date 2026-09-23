package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/pdfa"
)

// objStmBody is an /ObjStm container holding obj at number num.
func objStmBody(num int, obj string, extraDict string) string {
	hdr := fmt.Sprintf("%d 0 ", num)
	enc := zlibBytes([]byte(hdr + obj))
	return fmt.Sprintf("<< /Type /ObjStm /N 1 /First %d /Filter /FlateDecode%s /Length %d >>\nstream\n%s\nendstream", len(hdr), extraDict, len(enc), enc)
}

// TestPNGPredictorObjectStreamsDoNotAllocate is audit 2026-09-22 C8: eight
// object streams whose /DecodeParms name a 2 GiB PNG row and whose data is
// empty, behind a broken startxref that makes Read unpack every container.
// 1.6 KB of file was an out-of-memory kill in Read.
func TestPNGPredictorObjectStreamsDoNotAllocate(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		enc := zlibBytes(nil)
		bodies := []string{"<< /Type /Catalog >>"}
		for i := 0; i < 8; i++ {
			bodies = append(bodies, fmt.Sprintf("<< /Type /ObjStm /N 0 /First 0 /Filter /FlateDecode /DecodeParms << /Predictor 12 /Colors 64 /BitsPerComponent 16 /Columns 16777216 >> /Length %d >>\nstream\n%s\nendstream", len(enc), enc))
		}
		head, _ := numberedPDF(bodies)
		file := append(head, "startxref\n5\n%%EOF\n"...)
		if len(file) > 2048 {
			t.Fatalf("fixture is %d bytes; the audit's is 1.6 KB", len(file))
		}
		doc, err := Read(bytes.NewReader(file), int64(len(file)))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(doc.brokenObjStms) != 0 || len(doc.skippedObjStms) != 0 {
			t.Errorf("empty containers were recorded as broken %v or skipped %v", doc.brokenObjStms, doc.skippedObjStms)
		}
	})
}

// amplifiedObjStmPDF is the audit's 403 KB file (C9): k containers, each a
// 90 MB array of "1 0 R" compressed to about 130 KB, behind an xref stream
// placing object 100+i in container 3+i.
func amplifiedObjStmPDF(k int) []byte {
	bodies := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [] /Count 0 >>"}
	var arr bytes.Buffer
	arr.WriteString("[")
	for arr.Len() < 90<<20 {
		arr.WriteString("1 0 R ")
	}
	arr.WriteString("]")
	for i := 0; i < k; i++ {
		hdr := fmt.Sprintf("%d 0 ", 100+i)
		hdr += strings.Repeat(" ", 8-len(hdr))
		enc := zlibBytes(append([]byte(hdr), arr.Bytes()...))
		bodies = append(bodies, fmt.Sprintf("<< /Type /ObjStm /N 1 /First 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", len(enc), enc))
	}
	head, offs := numberedPDF(bodies)
	xoff := len(head)
	var ent []byte
	ent = append(ent, 0, 0, 0, 0, 255)
	for n := 1; n <= 2+k; n++ {
		ent = append(ent, 1)
		ent = append(ent, be(offs[n], 3)...)
		ent = append(ent, 0)
	}
	ent = append(ent, 1)
	ent = append(ent, be(xoff, 3)...)
	ent = append(ent, 0)
	for i := 0; i < k; i++ {
		ent = append(ent, 2)
		ent = append(ent, be(3+i, 3)...)
		ent = append(ent, 0)
	}
	var b bytes.Buffer
	b.Write(head)
	fmt.Fprintf(&b, "%d 0 obj\n<< /Type /XRef /Size %d /Index [0 %d 100 %d] /W [1 3 1] /Root 1 0 R /Length %d >>\nstream\n", 3+k, 100+k, 4+k, k, len(ent))
	b.Write(ent)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xoff)
	return b.Bytes()
}

// TestAmplifiedObjectStreamsStayInsideTheBudget is audit 2026-09-22 C9: three
// containers of 90 MB of "1 0 R" each (270 MB decoded, under the old 512 MB
// budget of decoded bytes) are 15 million references apiece, several times
// their text in memory; the 403 KB file was an out-of-memory kill at 2 GiB.
// The budget now meters what is materialised, including the container being
// unpacked, so the read stops inside it and says so.
func TestAmplifiedObjectStreamsStayInsideTheBudget(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 1536 << 20, Timeout: 3 * time.Minute}, func(t *testing.T) {
		file := amplifiedObjStmPDF(3)
		if len(file) > 450<<10 {
			t.Fatalf("fixture is %d bytes; the audit's is 403 KB", len(file))
		}
		doc, err := Read(bytes.NewReader(file), int64(len(file)))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		for i := 0; i < 3; i++ {
			if doc.Objects[100+i] != nil {
				t.Errorf("object %d was materialised past the budget", 100+i)
			}
		}
		if len(doc.brokenObjStms) != 0 {
			t.Errorf("containers skipped for the budget were recorded as broken: %v", doc.brokenObjStms)
		}
		if len(doc.skippedObjStms) != 3 {
			t.Fatalf("skipped = %v, want all three containers", doc.skippedObjStms)
		}
		var limit bool
		for _, v := range ValidatePDFA(doc, pdfa.PDFA2b) {
			if v.Rule == "6.1.7" {
				t.Errorf("a container skipped for the budget was reported as malformed: %s", v.Message)
			}
			if v.Rule == "limit" && strings.Contains(v.Message, core.GuardObjStmTotal) {
				limit = true
			}
		}
		if !limit {
			t.Error("no objstm budget finding")
		}
	})
}

// TestObjStmBudgetIncludesTheContainerBeingUnpacked: the budget check used to
// come before each container, against what earlier containers had cost, so one
// container could overshoot by everything it held (audit 2026-09-22 C9). A
// container's decoded bytes are held while its objects are parsed, and they
// are charged: this one decodes to 100 KB of white space around one null,
// which materialises almost nothing, and a 50 KB budget refuses it for the
// bytes alone.
func TestObjStmBudgetIncludesTheContainerBeingUnpacked(t *testing.T) {
	file := oneObjStmPDF(strings.Repeat(" ", 100_000) + "null")
	if doc := readRaw(t, file, WithMaxObjectStreamBytes(200_000)); doc.Objects[5] == nil {
		t.Fatal("control: object 5 is missing under a budget its container fits")
	}
	doc := readRaw(t, file, WithMaxObjectStreamBytes(50_000))
	if doc.Objects[5] != nil {
		t.Error("a 100 KB container was unpacked under a 50 KB budget: its own bytes were not charged")
	}
	if len(doc.skippedObjStms) != 1 || len(doc.brokenObjStms) != 0 {
		t.Errorf("skipped %v, broken %v; want the container skipped", doc.skippedObjStms, doc.brokenObjStms)
	}

	// And while its objects are parsed: 100 KB of container holding a 60 KB
	// string fits a 150 KB budget on either count alone and not on both, which
	// is what the memory held at once is.
	file = oneObjStmPDF(strings.Repeat(" ", 40_000) + "(" + strings.Repeat("s", 60_000) + ")")
	if doc := readRaw(t, file, WithMaxObjectStreamBytes(200_000)); doc.Objects[5] == nil {
		t.Fatal("control: object 5 is missing under a budget its container and string fit together")
	}
	if doc := readRaw(t, file, WithMaxObjectStreamBytes(150_000)); doc.Objects[5] != nil {
		t.Error("a container and its string were held at once past the budget")
	}
}

// TestObjStmDecodeStopsAtTheBudget: the decode of a container is itself capped
// at what the budget has left, so a container that cannot fit is refused as it
// inflates rather than after 90 MB of it has been held. The file is built by
// streaming, so the child's memory is the read's.
func TestObjStmDecodeStopsAtTheBudget(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 96 << 20, Timeout: time.Minute}, func(t *testing.T) {
		var enc bytes.Buffer
		zw := zlib.NewWriter(&enc)
		zw.Write([]byte("5 0 [ "))
		chunk := bytes.Repeat([]byte("1 0 R "), 1<<16)
		for n := 0; n < 90<<20; n += len(chunk) {
			zw.Write(chunk)
		}
		zw.Write([]byte("]"))
		zw.Close()
		body := fmt.Sprintf("<< /Type /ObjStm /N 1 /First 4 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", enc.Len(), enc.Bytes())
		file := oneObjStmFromBody(body)
		doc := readRaw(t, file, WithMaxObjectStreamBytes(8<<20))
		if doc.Objects[5] != nil {
			t.Fatal("object 5 was materialised past the budget")
		}
		if len(doc.skippedObjStms) != 1 {
			t.Fatalf("skipped = %v, want the container", doc.skippedObjStms)
		}
	})
}

// oneObjStmPDF is a file whose object 5 lives, as body, in object stream 3.
func oneObjStmPDF(body string) []byte {
	return oneObjStmFromBody(objStmBody(5, body, ""))
}

// oneObjStmFromBody is oneObjStmPDF given the container's own body.
func oneObjStmFromBody(container string) []byte {
	head, offs := numberedPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R /Extra 5 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		container,
	})
	xoff := len(head)
	var ent []byte
	ent = append(ent, 0, 0, 0, 0, 255)
	for _, n := range []int{1, 2, 3} {
		ent = append(ent, 1)
		ent = append(ent, be(offs[n], 3)...)
		ent = append(ent, 0)
	}
	ent = append(ent, 1)
	ent = append(ent, be(xoff, 3)...)
	ent = append(ent, 0, 2)
	ent = append(ent, be(3, 3)...)
	ent = append(ent, 0)
	var b bytes.Buffer
	b.Write(head)
	fmt.Fprintf(&b, "4 0 obj\n<< /Type /XRef /Size 6 /W [1 3 1] /Root 1 0 R /Length %d >>\nstream\n", len(ent))
	b.Write(ent)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xoff)
	return b.Bytes()
}

// TestCappedObjectStreamIsNotMalformed is audit 2026-09-22 C47: a valid file
// read under a decode limit its 3 KB object stream exceeds lost object 5, and
// PDF/A reported "an object stream could not be decoded (malformed stream
// data)" with no limit finding. A container over a limit is pdf0 declining; it
// is recorded as not unpacked, and reported under "limit".
func TestCappedObjectStreamIsNotMalformed(t *testing.T) {
	head, offs := numberedPDF([]string{
		"<< /Type /Catalog /Pages 2 0 R /Extra 5 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		objStmBody(5, "<< /Title ("+strings.Repeat("x", 3000)+") >>", ""),
	})
	xoff := len(head)
	var ent []byte
	ent = append(ent, 0, 0, 0, 255)
	for _, n := range []int{1, 2, 3} {
		ent = append(ent, 1)
		ent = append(ent, be(offs[n], 2)...)
		ent = append(ent, 0)
	}
	ent = append(ent, 1)
	ent = append(ent, be(xoff, 2)...)
	ent = append(ent, 0, 2)
	ent = append(ent, be(3, 2)...)
	ent = append(ent, 0)
	var b bytes.Buffer
	b.Write(head)
	fmt.Fprintf(&b, "4 0 obj\n<< /Type /XRef /Size 6 /W [1 2 1] /Root 1 0 R /Length %d >>\nstream\n", len(ent))
	b.Write(ent)
	fmt.Fprintf(&b, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", xoff)
	file := b.Bytes()

	if doc := readRaw(t, file); doc.Objects[5] == nil {
		t.Fatal("control: object 5 is missing under the default limits")
	}
	for name, c := range map[string]struct {
		opt   Option
		guard string
	}{
		"decode limit":           {WithMaxDecodedStreamBytes(1000), core.GuardDecodedStream},
		"object-stream budget":   {WithMaxObjectStreamBytes(1), core.GuardObjStmTotal},
		"budget below container": {WithMaxObjectStreamBytes(2000), core.GuardObjStmTotal},
	} {
		doc := readRaw(t, file, c.opt)
		if doc.Objects[5] != nil {
			t.Errorf("%s: object 5 was unpacked past the bound", name)
		}
		var limit bool
		for _, v := range ValidatePDFA(doc, pdfa.PDFA2b) {
			if v.Rule == "6.1.7" {
				t.Errorf("%s: reported as malformed: %s", name, v.Message)
			}
			if v.Rule == "limit" && strings.Contains(v.Message, c.guard) && strings.Contains(v.Message, "object stream 3") {
				limit = true
			}
		}
		if !limit {
			t.Errorf("%s: no %s finding naming object stream 3", name, c.guard)
		}
	}
}
