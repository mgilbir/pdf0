package pdf0

import (
	"bytes"
	"fmt"
	"testing"
)

// buildXRefStreamPDF constructs a minimal PDF whose cross-reference section is a
// /Type /XRef stream (uncompressed, /W [1 2 2]).
func buildXRefStreamPDF() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")
	o1 := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	o2 := buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	o3 := buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n")
	o4 := buf.Len()

	var xd bytes.Buffer
	put := func(v, w int) {
		for i := w - 1; i >= 0; i-- {
			xd.WriteByte(byte(v >> (8 * uint(i))))
		}
	}
	put(0, 1)
	put(0, 2)
	put(65535, 2) // obj 0: free head
	for _, off := range []int{o1, o2, o3, o4} {
		put(1, 1)
		put(off, 2)
		put(0, 2)
	}
	data := xd.Bytes()

	fmt.Fprintf(&buf, "4 0 obj\n<< /Type /XRef /Size 5 /Root 1 0 R /W [1 2 2] /Length %d >>\nstream\n", len(data))
	buf.Write(data)
	buf.WriteString("\nendstream\nendobj\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", o4)
	return buf.Bytes()
}

// TestWriteXRefStream confirms that a document read from a cross-reference
// stream is written back as one (not a traditional table) and round-trips.
func TestWriteXRefStream(t *testing.T) {
	data := buildXRefStreamPDF()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !doc.usedXRefStream {
		t.Fatal("did not detect a cross-reference stream on read")
	}

	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.Bytes()
	if bytes.Contains(out, []byte("\ntrailer\n")) {
		t.Error("output uses a traditional trailer, expected a cross-reference stream")
	}
	if !bytes.Contains(out, []byte("/XRef")) {
		t.Error("output has no /Type /XRef stream")
	}
	// The three dictionary objects are compressible, so they are repacked into
	// an object stream and reachable only through type-2 xref entries.
	if !bytes.Contains(out, []byte("/ObjStm")) {
		t.Error("output did not repack objects into an object stream")
	}

	doc2, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !doc2.usedXRefStream {
		t.Error("re-written file is not a cross-reference stream")
	}
	if !DocumentEqual(doc, doc2) {
		t.Error("document did not round-trip through the cross-reference stream")
	}
	// A packed object (the catalog) must resolve through the object stream.
	if cat := doc2.ResolveDict(doc2.Trailer.Get("Root")); cat == nil {
		t.Error("catalog did not resolve through the object stream")
	}
}

// TestWriteTraditionalStaysTraditional confirms a file read from a traditional
// table is still written with one.
func TestWriteTraditionalStaysTraditional(t *testing.T) {
	data := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if doc.usedXRefStream {
		t.Fatal("traditional table misdetected as a stream")
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("\ntrailer\n")) {
		t.Error("traditional file was not written with a trailer")
	}
}
