package pdf0

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"math"
	"strings"
	"testing"
)

// noPanic runs fn and fails the test if it panics instead of returning an error.
func noPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panicked instead of returning an error: %v", name, r)
		}
	}()
	fn()
}

// TestReadNegativeXrefOffset ensures a negative traditional-xref entry offset
// never seeks the lexer to an invalid position (audit C1). The strict load
// rejects the entry; the scan rebuild then recovers the file's real objects,
// so the read succeeds without ever using the crafted offset.
func TestReadNegativeXrefOffset(t *testing.T) {
	body := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"
	xrefAt := len(body)
	xref := "xref\n0 2\n0000000000 65535 f \n-000000010 00000 n \ntrailer\n<< /Root 1 0 R /Size 2 >>\n"
	pdf := body + xref + fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefAt)
	if !strings.HasPrefix(pdf[xrefAt:], "xref") {
		t.Fatalf("test constructed a bad offset")
	}
	noPanic(t, "negative xref offset", func() {
		doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
		if err != nil {
			t.Fatalf("rebuild should recover the crafted file: %v", err)
		}
		if doc.view().Catalog() == nil {
			t.Fatalf("catalog not recovered")
		}
	})
}

// TestReadNegativePrevOffset ensures a negative /Prev offset does not panic
// (audit C1).
func TestReadNegativePrevOffset(t *testing.T) {
	body := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"
	xrefAt := len(body)
	xref := "xref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Root 1 0 R /Size 2 /Prev -5 >>\n"
	pdf := body + xref + fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefAt)
	noPanic(t, "negative /Prev", func() {
		// A broken /Prev tail is tolerated: the primary section still parses.
		if _, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf))); err != nil {
			t.Logf("read returned err=%v (acceptable; must not panic)", err)
		}
	})
}

// TestObjStmHugeNPanic ensures a huge /N does not overflow the sanity guard and
// panic in make (audit C2).
func TestObjStmHugeNPanic(t *testing.T) {
	s := &Stream{Dict: Dictionary{}, Data: []byte("12345678")}
	s.Dict.Set("Type", Name("ObjStm"))
	s.Dict.Set("N", Integer(math.MaxInt64))
	s.Dict.Set("First", Integer(8))
	noPanic(t, "objstm huge N", func() {
		if _, _, _, err := parseObjStmIndex(core.Canceler{}, s, core.DefaultLimits()); err == nil {
			t.Fatalf("expected an error for an absurd /N, got nil")
		}
	})
}

// TestStreamWrongTypedLengthRecovers ensures a Real /Length falls back to the
// endstream search instead of aborting the parse (audit C17).
func TestStreamWrongTypedLengthRecovers(t *testing.T) {
	src := "5 0 obj\n<< /Length 11.0 >>\nstream\nHello World\nendstream\nendobj\n"
	p := NewParser([]byte(src))
	iobj, err := p.ParseIndirectObject()
	if err != nil {
		t.Fatalf("expected recovery via endstream search, got err=%v", err)
	}
	st, ok := iobj.Value.(*Stream)
	if !ok {
		t.Fatalf("expected a stream, got %T", iobj.Value)
	}
	if string(st.Data) != "Hello World" {
		t.Fatalf("stream data = %q, want %q", st.Data, "Hello World")
	}
}

// TestXrefNegativeStartObj ensures a negative subsection start is rejected
// rather than producing negative object numbers.
func TestXrefNegativeStartObj(t *testing.T) {
	data := []byte("xref\n-3 2\n0000000000 00000 n \n0000000001 00000 n \ntrailer")
	if _, err := ParseXRefTable(data, 4); err == nil {
		t.Fatalf("expected an error for a negative subsection start, got nil")
	}
}

// TestReadObjectZeroInUse: real-world files mark cross-reference entry 0 as
// in-use with a "0 0 obj" body (Common Crawl sweep #13). Object number 0 is
// the reserved free-list head (ISO 32000-1 7.5.4), so the definition is
// ignored on Read — and the document then writes and round-trips instead of
// Write refusing the reserved number.
func TestReadObjectZeroInUse(t *testing.T) {
	body := "%PDF-1.7\n"
	zeroAt := len(body)
	body += "0 0 obj\n<< /Bogus true >>\nendobj\n"
	oneAt := len(body)
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	twoAt := len(body)
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	xrefAt := len(body)
	xref := fmt.Sprintf("xref\n0 3\n%010d 00000 n \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Root 1 0 R /Size 3 >>\n", zeroAt, oneAt, twoAt)
	pdf := body + xref + fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefAt)

	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, ok := doc.Objects[0]; ok {
		t.Fatal("the reserved object number 0 was loaded into the model")
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write refused a document read from a file with an in-use entry 0: %v", err)
	}
	rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("re-Read: %v", err)
	}
	if !DocumentEqual(doc, rt) {
		t.Error("round trip lost content")
	}
}

// TestStartxrefIntoTableRecovers: ISO 32000-2, 7.5.5 requires startxref to give
// the offset of "the beginning of the xref keyword in the last cross-reference
// section". Real-world files (Common Crawl sweep #13) point it a few dozen
// bytes INTO the table's entries instead; the offset then reads as an integer
// and mis-dispatched to the xref-stream parser ("unknown keyword \"f\"").
// Read recovers by relocating to the nearest preceding standalone keyword.
func TestStartxrefIntoTableRecovers(t *testing.T) {
	body := "%PDF-1.7\n"
	oneAt := len(body)
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	twoAt := len(body)
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	xrefAt := len(body)
	xref := fmt.Sprintf("xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Root 1 0 R /Size 3 >>\n", oneAt, twoAt)
	// Point startxref 56 bytes past the keyword — into the second entry, the
	// drift the sweep files carry (55-57 bytes).
	pdf := body + xref + fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefAt+56)

	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read did not recover from a startxref pointing into the table: %v", err)
	}
	if got := len(doc.Objects); got != 2 {
		t.Errorf("loaded %d objects, want 2", got)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("round-trip re-Read: %v", err)
	}
}

// TestStartxrefFarFromTable: with no xref keyword inside the bounded
// relocation window, the keyword probe gives up (unit-checked below) and the
// scan rebuild takes over, recovering the file's objects.
func TestStartxrefFarFromTable(t *testing.T) {
	body := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"
	pad := strings.Repeat("% padding\n", 300) // > the 1024-byte window
	body += pad
	pdf := body + "startxref\n" + fmt.Sprintf("%d", len(body)-20) + "\n%%EOF\n"
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("rebuild should recover the file: %v", err)
	}
	if doc.view().Catalog() == nil {
		t.Fatal("catalog not recovered")
	}
}

// TestPrecedingXrefKeywordWindow pins the relocation probe's bounds: the
// search never leaves its 1KB window, and the tail of "startxref" is not a
// keyword.
func TestPrecedingXrefKeywordWindow(t *testing.T) {
	far := []byte("xref\n" + strings.Repeat(" ", 2000))
	if got := precedingXrefKeyword(far, 2000); got != -1 {
		t.Errorf("keyword beyond the window found at %d, want -1", got)
	}
	near := []byte(strings.Repeat(" ", 100) + "xref\n0 1\n" + strings.Repeat(" ", 50))
	if got := precedingXrefKeyword(near, 140); got != 100 {
		t.Errorf("keyword at 100 not found: got %d", got)
	}
	sx := []byte("startxref\n123\n")
	if got := precedingXrefKeyword(sx, int64(len(sx))); got != -1 {
		t.Errorf("the tail of startxref matched as a keyword at %d", got)
	}
}
