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

// TestDecodeContentStreamBudget verifies the aggregate decoded-content budget:
// content streams decode normally until the per-run budget is reached, after
// which further streams are treated as undecodable (nil) so a flate-bomb file
// cannot force unbounded decode+tokenize work. Under the budget behaviour is
// unchanged.
func TestDecodeContentStreamBudget(t *testing.T) {
	doc := &Document{valCache: &validationCache{content: make(map[*Stream][]byte)}}

	// A ~1 MB content stream decodes fine while under budget.
	s1 := makeFlateContentStream(1 << 20)
	if got := decodeContentStream(doc, s1); len(got) != 1<<20 {
		t.Fatalf("under budget: decoded %d bytes, want %d", len(got), 1<<20)
	}
	if doc.valCache.contentBytes != 1<<20 {
		t.Fatalf("contentBytes = %d, want %d", doc.valCache.contentBytes, 1<<20)
	}

	// Simulate the run having reached the budget.
	doc.valCache.contentBytes = maxDecodedContentTotal
	s2 := makeFlateContentStream(1 << 20)
	if got := decodeContentStream(doc, s2); got != nil {
		t.Fatalf("over budget: decoded %d bytes, want nil (budget must skip decoding)", len(got))
	}
	// The decision is negatively cached and stable on re-request.
	if got := decodeContentStream(doc, s2); got != nil {
		t.Fatalf("over budget (cached): got %d bytes, want nil", len(got))
	}
}

// TestContentBombBoundedValidation is an end-to-end guard: a small PDF whose
// many page-content streams each inflate well past the budget (a flate bomb)
// must validate in bounded time rather than decoding and tokenizing everything.
// The budget is lowered so the test stays fast; the ratio (total content far
// exceeds the budget) is what matters. Without the budget this walks all
// nPages*perPage of content; with it, work stops once the budget is exhausted.
func TestContentBombBoundedValidation(t *testing.T) {
	const nPages = 200
	const perPage = 8 << 20 // 8 MB decoded per page → ~1.6 GB total content
	// Lower the budget to 16 MB so only ~2 streams are processed; total content
	// is ~100x the budget, so a regression (no budget) does far more work.
	defer func(orig int64) { maxDecodedContentTotal = orig }(maxDecodedContentTotal)
	maxDecodedContentTotal = 16 << 20

	pdf := buildContentBombPDF(t, nPages, perPage)
	doc, err := Read(bytes.NewReader(pdf), int64(len(pdf)))
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
