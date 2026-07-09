package pdf0

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildMinimalPDF constructs a minimal valid PDF file as bytes.
func buildMinimalPDF() []byte {
	var buf bytes.Buffer

	// Header
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	// Object 1: Catalog
	obj1Offset := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Object 2: Pages
	obj2Offset := buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	// Object 3: Page
	obj3Offset := buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")

	// Xref table
	xrefOffset := buf.Len()
	buf.WriteString("xref\n")
	buf.WriteString("0 4\n")
	buf.WriteString("0000000000 65535 f \r\n")
	buf.WriteString(fmt.Sprintf("%010d 00000 n \r\n", obj1Offset))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \r\n", obj2Offset))
	buf.WriteString(fmt.Sprintf("%010d 00000 n \r\n", obj3Offset))

	// Trailer
	buf.WriteString("trailer\n")
	buf.WriteString("<< /Size 4 /Root 1 0 R >>\n")
	buf.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset))

	return buf.Bytes()
}

func TestReadMinimalPDF(t *testing.T) {
	data := buildMinimalPDF()
	r := bytes.NewReader(data)

	doc, err := Read(r, int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if doc.Version != "2.0" {
		t.Errorf("version: expected '2.0', got %q", doc.Version)
	}

	if len(doc.Objects) != 3 {
		t.Fatalf("expected 3 objects, got %d", len(doc.Objects))
	}

	// Check catalog
	catalog := doc.Objects[1]
	if catalog == nil {
		t.Fatal("missing object 1 (catalog)")
	}
	dict, ok := catalog.Value.(*Dictionary)
	if !ok {
		t.Fatalf("object 1: expected *Dictionary, got %T", catalog.Value)
	}
	if n := dict.Get("Type"); n == nil || n.(Name) != "Catalog" {
		t.Errorf("object 1 /Type: expected /Catalog, got %v", n)
	}

	// Check pages
	pages := doc.Objects[2]
	if pages == nil {
		t.Fatal("missing object 2 (pages)")
	}

	// Check page
	page := doc.Objects[3]
	if page == nil {
		t.Fatal("missing object 3 (page)")
	}

	// Check trailer
	root := doc.Trailer.Get("Root")
	if root == nil {
		t.Fatal("trailer missing /Root")
	}
	ref, ok := root.(IndirectRef)
	if !ok {
		t.Fatalf("trailer /Root: expected IndirectRef, got %T", root)
	}
	if ref.Number != 1 {
		t.Errorf("trailer /Root: expected ref to object 1, got %d", ref.Number)
	}
}

func TestWriteMinimalPDF(t *testing.T) {
	// Build a document manually
	doc := &Document{
		Version: "2.0",
		Objects: map[int]*IndirectObject{
			1: {
				Number: 1, Generation: 0,
				Value: &Dictionary{
					Keys:   []Name{"Type", "Pages"},
					Values: []Object{Name("Catalog"), IndirectRef{Number: 2}},
				},
			},
			2: {
				Number: 2, Generation: 0,
				Value: &Dictionary{
					Keys:   []Name{"Type", "Kids", "Count"},
					Values: []Object{Name("Pages"), Array{IndirectRef{Number: 3}}, Integer(1)},
				},
			},
			3: {
				Number: 3, Generation: 0,
				Value: &Dictionary{
					Keys:   []Name{"Type", "Parent", "MediaBox"},
					Values: []Object{Name("Page"), IndirectRef{Number: 2}, Array{Integer(0), Integer(0), Integer(612), Integer(792)}},
				},
			},
		},
		Trailer: Dictionary{
			Keys:   []Name{"Root"},
			Values: []Object{IndirectRef{Number: 1}},
		},
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify we can read it back
	data := buf.Bytes()
	r := bytes.NewReader(data)
	doc2, err := Read(r, int64(len(data)))
	if err != nil {
		t.Fatalf("Read back: %v\nWritten data:\n%s", err, data)
	}

	if doc2.Version != "2.0" {
		t.Errorf("version: expected '2.0', got %q", doc2.Version)
	}

	if len(doc2.Objects) != 3 {
		t.Errorf("expected 3 objects, got %d", len(doc2.Objects))
	}
}

func TestReadWriteRoundTrip(t *testing.T) {
	data := buildMinimalPDF()
	r := bytes.NewReader(data)

	// Read
	doc1, err := Read(r, int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Write
	var buf bytes.Buffer
	if err := doc1.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read again
	data2 := buf.Bytes()
	r2 := bytes.NewReader(data2)
	doc2, err := Read(r2, int64(len(data2)))
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}

	// Compare semantically
	if !DocumentEqual(doc1, doc2) {
		t.Error("documents are not semantically equal after round-trip")
	}
}

func TestFindStartXref(t *testing.T) {
	data := []byte("... some content ...\nstartxref\n12345\n%%EOF\n")
	offset, err := findStartXref(data)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 12345 {
		t.Errorf("expected offset 12345, got %d", offset)
	}
}

func TestParseHeader(t *testing.T) {
	tests := []struct {
		input        string
		version      string
		headerOffset int64
	}{
		{"%PDF-1.7\n", "1.7", 0},
		{"%PDF-2.0\n", "2.0", 0},
		{"%PDF-2.0\r\n", "2.0", 0},
		{"some prefix\n%PDF-2.0\n", "2.0", 12},
	}
	for _, tt := range tests {
		version, offset, err := parseHeader([]byte(tt.input))
		if err != nil {
			t.Errorf("input %q: %v", tt.input, err)
			continue
		}
		if version != tt.version {
			t.Errorf("input %q: expected version %q, got %q", tt.input, tt.version, version)
		}
		if offset != tt.headerOffset {
			t.Errorf("input %q: expected header offset %d, got %d", tt.input, tt.headerOffset, offset)
		}
	}
}

// TestRoundTripObjectStreamPDF verifies a document read from an
// xref-stream/object-stream file writes back clean: no stale /XRef or
// /ObjStm objects, no xref-stream keys polluting the trailer, and a second
// read yields a semantically equal document.
func TestRoundTripObjectStreamPDF(t *testing.T) {
	pdf := buildObjStmPDF(t)
	doc1, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		t.Fatal(err)
	}

	// The trailer must not carry xref-stream plumbing.
	for _, key := range []Name{"Type", "W", "Index", "Filter", "Length"} {
		if doc1.Trailer.Get(key) != nil {
			t.Errorf("trailer still contains xref-stream key /%s", key)
		}
	}
	// The structural objects must be gone; the content objects present.
	for num, iobj := range doc1.Objects {
		if stream, ok := iobj.Value.(*Stream); ok {
			if typ, ok := stream.Dict.Get("Type").(Name); ok && (typ == "XRef" || typ == "ObjStm") {
				t.Errorf("object %d is a stale /%s stream", num, typ)
			}
		}
	}

	var buf bytes.Buffer
	if err := doc1.Write(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	// Object streams are not repacked, so /ObjStm must not reappear. The file
	// used a cross-reference stream, so Write regenerates one (a fresh /XRef,
	// not the stale original).
	if bytes.Contains(out, []byte("/ObjStm")) {
		t.Error("written output repacked object streams (not supported)")
	}
	if !bytes.Contains(out, []byte("/XRef")) {
		t.Error("expected a regenerated cross-reference stream")
	}

	doc2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v\n%s", err, out)
	}
	if !DocumentEqual(doc1, doc2) {
		t.Error("documents differ after round-trip")
	}
	catalog := doc2.ResolveDict(doc2.Trailer.Get("Root"))
	if catalog == nil {
		t.Fatal("catalog not resolvable after round-trip")
	}
}

// TestWriteXRefSubsections verifies sparse object numbers produce compact
// subsections instead of a padded single section full of fabricated free
// entries.
func TestWriteXRefSubsections(t *testing.T) {
	doc := &Document{
		Version: "2.0",
		Objects: map[int]*IndirectObject{
			1:   {Number: 1, Value: &Dictionary{Keys: []Name{"Type"}, Values: []Object{Name("Catalog")}}},
			2:   {Number: 2, Value: Integer(1)},
			100: {Number: 100, Value: Integer(2)},
			101: {Number: 101, Value: Integer(3)},
		},
		Trailer: Dictionary{Keys: []Name{"Root"}, Values: []Object{IndirectRef{Number: 1}}},
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Two subsections: 0-2 (head + two objects) and 100-101.
	if !strings.Contains(out, "xref\n0 3\n") {
		t.Errorf("missing first subsection header '0 3' in:\n%s", out)
	}
	if !strings.Contains(out, "100 2\n") {
		t.Errorf("missing second subsection header '100 2' in:\n%s", out)
	}
	if strings.Contains(out, "0000000000 00000 f") {
		t.Errorf("output contains fabricated free entries:\n%s", out)
	}

	// And it must read back.
	data := buf.Bytes()
	doc2, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("re-read: %v\n%s", err, data)
	}
	if len(doc2.Objects) != 4 {
		t.Errorf("expected 4 objects after re-read, got %d", len(doc2.Objects))
	}
}
