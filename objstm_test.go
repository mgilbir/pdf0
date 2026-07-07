package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"testing"
)

func makeObjStm(t *testing.T, objects map[int]string, order []int, compress bool) *Stream {
	t.Helper()
	var index, body bytes.Buffer
	for _, num := range order {
		fmt.Fprintf(&index, "%d %d ", num, body.Len())
		body.WriteString(objects[num])
		body.WriteByte(' ')
	}
	payload := append(index.Bytes(), body.Bytes()...)

	data := payload
	dict := Dictionary{}
	dict.Set("Type", Name("ObjStm"))
	dict.Set("N", Integer(len(order)))
	dict.Set("First", Integer(index.Len()))
	if compress {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		zw.Write(payload)
		zw.Close()
		data = buf.Bytes()
		dict.Set("Filter", Name("FlateDecode"))
	}
	dict.Set("Length", Integer(len(data)))
	return &Stream{Dict: dict, Data: data}
}

func TestParseObjStmIndex(t *testing.T) {
	stream := makeObjStm(t, map[int]string{
		4: "<< /Type /Catalog /Pages 5 0 R >>",
		5: "(hello)",
		6: "42",
	}, []int{4, 5, 6}, true)

	data, entries, first, err := parseObjStmIndex(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 index entries, got %d", len(entries))
	}
	if entries[0].Number != 4 || entries[1].Number != 5 || entries[2].Number != 6 {
		t.Errorf("wrong object numbers: %+v", entries)
	}
	// Parse the middle object from its recorded offset
	parser := NewParser(data)
	parser.Lexer().SetPosition(int64(first + entries[1].Offset))
	obj, err := parser.ParseObject()
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := obj.(String); !ok || string(s.Value) != "hello" {
		t.Errorf("expected string 'hello', got %#v", obj)
	}
}

func TestParseObjStmIndexRejectsBadDict(t *testing.T) {
	cases := []struct {
		name  string
		mutig func(*Stream)
	}{
		{"wrong type", func(s *Stream) { s.Dict.Set("Type", Name("XRef")) }},
		{"missing N", func(s *Stream) { s.Dict.Delete("N") }},
		{"negative N", func(s *Stream) { s.Dict.Set("N", Integer(-1)) }},
		{"missing First", func(s *Stream) { s.Dict.Delete("First") }},
		{"First beyond data", func(s *Stream) { s.Dict.Set("First", Integer(1<<30)) }},
		{"absurd N", func(s *Stream) { s.Dict.Set("N", Integer(1<<30)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := makeObjStm(t, map[int]string{4: "42"}, []int{4}, false)
			tc.mutig(stream)
			if _, _, _, err := parseObjStmIndex(stream); err == nil {
				t.Error("expected error")
			}
		})
	}
}

// buildObjStmPDF assembles a minimal PDF whose catalog, page tree, and page
// live in an object stream, referenced by an xref stream.
func buildObjStmPDF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")

	// Object 1: the ObjStm containing objects 4 (catalog), 5 (pages), 6 (page)
	objStm := makeObjStm(t, map[int]string{
		4: "<< /Type /Catalog /Pages 5 0 R >>",
		5: "<< /Type /Pages /Kids [6 0 R] /Count 1 >>",
		6: "<< /Type /Page /Parent 5 0 R /MediaBox [0 0 612 792] >>",
	}, []int{4, 5, 6}, true)

	offsets := map[int]int{}
	offsets[1] = buf.Len()
	fmt.Fprintf(&buf, "1 0 obj\n<< /Type /ObjStm /N 3 /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		mustInt(t, objStm.Dict.Get("First")), len(objStm.Data))
	buf.Write(objStm.Data)
	buf.WriteString("\nendstream\nendobj\n")

	// Object 2: the xref stream. W [1 2 1]; objects 0-6.
	xrefStart := buf.Len()
	entries := [][]byte{
		{0, 0, 0, 255}, // 0: free
		{1, byte(offsets[1] >> 8), byte(offsets[1]), 0}, // 1: ObjStm container
		{1, byte(xrefStart >> 8), byte(xrefStart), 0},   // 2: this xref stream
		{0, 0, 0, 255}, // 3: free
		{2, 0, 1, 0},   // 4: in stream 1, index 0
		{2, 0, 1, 1},   // 5: in stream 1, index 1
		{2, 0, 1, 2},   // 6: in stream 1, index 2
	}
	var xrefData bytes.Buffer
	for _, e := range entries {
		xrefData.Write(e)
	}
	var xz bytes.Buffer
	zw := zlib.NewWriter(&xz)
	zw.Write(xrefData.Bytes())
	zw.Close()

	fmt.Fprintf(&buf, "2 0 obj\n<< /Type /XRef /Size 7 /W [1 2 1] /Root 4 0 R /Filter /FlateDecode /Length %d >>\nstream\n", xz.Len())
	buf.Write(xz.Bytes())
	buf.WriteString("\nendstream\nendobj\n")

	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefStart)
	return buf.Bytes()
}

func mustInt(t *testing.T, obj Object) int {
	t.Helper()
	i, ok := obj.(Integer)
	if !ok {
		t.Fatalf("expected Integer, got %T", obj)
	}
	return int(i)
}

func TestReadObjectStreamPDF(t *testing.T) {
	pdf := buildObjStmPDF(t)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}

	for _, num := range []int{4, 5, 6} {
		if _, ok := doc.Objects[num]; !ok {
			t.Fatalf("compressed object %d not loaded", num)
		}
	}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	if catalog == nil {
		t.Fatal("catalog not resolvable")
	}
	if typ, ok := catalog.Get("Type").(Name); !ok || typ != "Catalog" {
		t.Errorf("catalog /Type wrong: %v", catalog.Get("Type"))
	}
	pages := doc.ResolveDict(catalog.Get("Pages"))
	if pages == nil {
		t.Fatal("pages not resolvable")
	}
	if cnt, ok := pages.Get("Count").(Integer); !ok || cnt != 1 {
		t.Errorf("pages /Count wrong: %v", pages.Get("Count"))
	}
}

func TestReadObjStmXrefIndexMismatch(t *testing.T) {
	// An xref entry whose IndexInStream points at a different object number
	// must error, not silently load the wrong object.
	stream := makeObjStm(t, map[int]string{4: "42", 5: "43"}, []int{4, 5}, false)
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: stream},
	}}
	table := &XRefTable{Entries: map[int]XRefEntry{
		5: {Compressed: true, StreamObjNum: 1, IndexInStream: 0}, // index 0 holds obj 4
	}}
	if err := doc.loadCompressedObjects(table); err == nil {
		t.Error("expected error on index/object-number mismatch")
	}
}

func TestReadObjStmMissingContainer(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{}}
	table := &XRefTable{Entries: map[int]XRefEntry{
		5: {Compressed: true, StreamObjNum: 9, IndexInStream: 0},
	}}
	if err := doc.loadCompressedObjects(table); err == nil {
		t.Error("expected error on missing container")
	}
}
