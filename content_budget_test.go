package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"testing"
	"time"
)

// makeFlateContentStream builds a FlateDecode content stream whose decoded size
// is decodedLen bytes (a run of a valid content operator, so it also tokenizes).
func TestContentBombBoundedValidation(t *testing.T) {
	const nPages = 200
	const perPage = 8 << 20 // 8 MB decoded per page → ~1.6 GB total content
	// Lower the budget to 16 MB so only ~2 streams are processed; total content
	// is ~100x the budget, so a regression (no budget) does far more work. This
	// also exercises the public option path end to end.
	pdf := buildContentBombPDF(t, nPages, perPage)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)), WithMaxDecodedContentBytes(16<<20))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	start := time.Now()
	done := make(chan int, 1)
	go func() { done <- len(ValidatePDFUA(doc)) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("ValidatePDFUA did not finish within 30s on a content-bomb file; budget not bounding work")
	}
	// With the budget, only ~budget bytes of content are decoded regardless of
	// how much the file claims, so validation is quick.
	if el := time.Since(start); el > 20*time.Second {
		t.Errorf("validation took %v on a bounded content-bomb; expected the budget to keep it short", el)
	}
}

// makeFlateContentStream is repeated here from the core package's own copy:
// a test helper cannot cross a package boundary, and the document-level bomb
// test below belongs in this package.
func makeFlateContentStream(decodedLen int) *Stream {
	raw := bytes.Repeat([]byte("0 0 0 rg\n"), decodedLen/9+1)[:decodedLen]
	var zb bytes.Buffer
	zw := zlib.NewWriter(&zb)
	zw.Write(raw)
	zw.Close()
	d := &Dictionary{}
	d.Set("Length", Integer(zb.Len()))
	d.Set("Filter", Name("FlateDecode"))
	return &Stream{Dict: *d, Data: zb.Bytes()}
}

// buildContentBombPDF assembles a PDF with npages pages, each /Contents a
// FlateDecode stream that decodes to ~decodedLen bytes of content operators.
// The compressed payload is shared, so the file stays small on disk.
func buildContentBombPDF(t *testing.T, npages, decodedLen int) []byte {
	t.Helper()
	comp := makeFlateContentStream(decodedLen)

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\x80\x80\x80\x80\n")
	off := map[int]int{}
	obj := func(n int, body string) { off[n] = buf.Len(); fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body) }

	kids := ""
	for i := 0; i < npages; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+i)
	}
	obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	obj(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, npages))
	for i := 0; i < npages; i++ {
		obj(3+i, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << >> >>", 3+npages+i))
	}
	for i := 0; i < npages; i++ {
		cn := 3 + npages + i
		off[cn] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Length %d /Filter /FlateDecode >>\nstream\n", cn, len(comp.Data))
		buf.Write(comp.Data)
		buf.WriteString("\nendstream\nendobj\n")
	}
	size := 3 + 2*npages
	xs := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", size)
	for n := 1; n < size; n++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off[n])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Root 1 0 R /Size %d >>\nstartxref\n%d\n%%%%EOF\n", size, xs)
	return buf.Bytes()
}
