package pdf0

import (
	"bytes"
	"fmt"
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

// TestReadNegativeXrefOffset ensures a negative traditional-xref entry offset is
// rejected with an error rather than panicking the lexer (audit C1).
func TestReadNegativeXrefOffset(t *testing.T) {
	body := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"
	xrefAt := len(body)
	xref := "xref\n0 2\n0000000000 65535 f \n-000000010 00000 n \ntrailer\n<< /Root 1 0 R /Size 2 >>\n"
	pdf := body + xref + fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefAt)
	if !strings.HasPrefix(pdf[xrefAt:], "xref") {
		t.Fatalf("test constructed a bad offset")
	}
	noPanic(t, "negative xref offset", func() {
		if _, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf))); err == nil {
			t.Fatalf("expected an error for a negative xref offset, got nil")
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
		if _, _, _, err := parseObjStmIndex(s); err == nil {
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
