package pdf0

import (
	"bytes"
	"fmt"
	"github.com/mgilbir/pdf0/internal/signtest"
	"testing"
)

// buildPDFWithPageContents builds a one-page document whose page carries a
// content stream, so the page dictionary holds a literal "/Contents 4 0 R"
// BEFORE the signature dictionary appears in the output.
//
// buildMinimalPDF deliberately has no page content, which is why every signing
// test passed while signing was broken for real documents: patchSignature used
// to anchor on the first "/Contents" in the file and therefore never met a page
// content-stream reference in the test corpus.
func buildPDFWithPageContents() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-2.0\n%\x80\x80\x80\x80\n")

	body := "BT /F1 12 Tf 72 700 Td (hello) Tj ET\n"

	off1 := buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	off2 := buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	off3 := buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>\nendobj\n")
	off4 := buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(body), body)

	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 5\n")
	buf.WriteString("0000000000 65535 f \r\n")
	for _, o := range []int{off1, off2, off3, off4} {
		fmt.Fprintf(&buf, "%010d 00000 n \r\n", o)
	}
	buf.WriteString("trailer\n<< /Size 5 /Root 1 0 R >>\n")
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", xrefOffset)
	return buf.Bytes()
}

// TestSignDocumentWithPageContents is the regression test for the /ByteRange
// placeholder anchor. patchSignature located the /Contents placeholder with
// bytes.Index(data, "/Contents"), which matches the PAGE's content-stream
// reference first, so signing any realistic document failed with
// "signing: /ByteRange placeholder not found".
func TestSignDocumentWithPageContents(t *testing.T) {
	cert, key := signtest.CertKey(t)
	base := buildPDFWithPageContents()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSigned(&buf, cert, key); err != nil {
		t.Fatalf("WriteSigned on a document with page content: %v", err)
	}
	out := buf.Bytes()

	// The page's own /Contents must be untouched: an anchor that matched the
	// page would have overwritten the content stream's reference with hex.
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read signed: %v", err)
	}
	pages := signed.PageList()
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	if got := signed.ExtractText(); !bytes.Contains([]byte(got), []byte("hello")) {
		t.Errorf("page content lost or corrupted by signing: %q", got)
	}

	res := signed.VerifySignatures(out)
	if len(res) != 1 {
		t.Fatalf("got %d signatures, want 1", len(res))
	}
	if !res[0].Valid || res[0].Err != nil {
		t.Fatalf("signature invalid: valid=%v err=%v", res[0].Valid, res[0].Err)
	}
	if !res[0].CoversWholeDocument {
		t.Error("signature should cover the whole document")
	}
	if !res[0].DocumentUnmodified() {
		t.Error("DocumentUnmodified should hold for a freshly signed document")
	}
}

// TestSignIncrementalWithPageContents covers the same anchor bug on the
// incremental path, which is the one that must work for real files.
func TestSignIncrementalWithPageContents(t *testing.T) {
	cert, key := signtest.CertKey(t)
	original := buildPDFWithPageContents()
	doc, err := Read(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.WriteSignedIncremental(&buf, original, cert, key); err != nil {
		t.Fatalf("WriteSignedIncremental on a document with page content: %v", err)
	}
	out := buf.Bytes()
	if !bytes.HasPrefix(out, original) {
		t.Fatal("incremental signature altered the original bytes")
	}
	signed, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res := signed.VerifySignatures(out)
	if len(res) != 1 || !res[0].Valid || !res[0].CoversWholeDocument {
		t.Fatalf("incremental signature did not verify: %+v", res)
	}
}

// TestSignIncrementalSecondSignature covers the other half of the anchor bug:
// adding a signature to an already-signed file. The first signature's /Contents
// is a filled hex blob that precedes the new signature dictionary, so an anchor
// on the first /Contents would have patched the existing signature. Anchoring on
// the /ByteRange placeholder — which the filled signature no longer carries —
// targets only the new one.
//
// WriteSignedIncremental's documented purpose is exactly this: add a signature
// without invalidating one already present.
func TestSignIncrementalSecondSignature(t *testing.T) {
	cert, key := signtest.CertKey(t)

	base := buildPDFWithPageContents()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	if err := doc.WriteSigned(&first, cert, key); err != nil {
		t.Fatalf("first WriteSigned: %v", err)
	}
	onceSigned := first.Bytes()

	reread, err := Read(bytes.NewReader(onceSigned), int64(len(onceSigned)))
	if err != nil {
		t.Fatalf("re-read once-signed: %v", err)
	}
	var second bytes.Buffer
	if err := reread.WriteSignedIncremental(&second, onceSigned, cert, key); err != nil {
		t.Fatalf("second (incremental) WriteSignedIncremental: %v", err)
	}
	out := second.Bytes()

	// The first signature's bytes must survive verbatim.
	if !bytes.HasPrefix(out, onceSigned) {
		t.Fatal("second signature altered the first signature's bytes")
	}

	twice, err := Read(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("re-read twice-signed: %v", err)
	}
	res := twice.VerifySignatures(out)
	if len(res) != 2 {
		t.Fatalf("got %d signatures, want 2", len(res))
	}

	// Both signatures must still verify cryptographically. Exactly one — the
	// newest — covers the whole file; the earlier one is legitimately no longer
	// whole-file after the incremental append, which is precisely why Valid
	// alone is not a safe verdict.
	covering := 0
	for i, r := range res {
		if !r.Valid || r.Err != nil {
			t.Errorf("signature %d invalid after the second signing: valid=%v err=%v", i, r.Valid, r.Err)
		}
		if r.CoversWholeDocument {
			covering++
		}
	}
	if covering != 1 {
		t.Errorf("got %d signatures covering the whole document, want exactly 1", covering)
	}
}

// TestFindSigSlotsTargetsTheUnfilledPlaceholder pins the anchor directly: given
// bytes holding a page /Contents, a filled signature, and then an unfilled one,
// findSigSlots must return the unfilled dictionary's offsets.
func TestFindSigSlotsTargetsTheUnfilledPlaceholder(t *testing.T) {
	data := []byte(
		"3 0 obj\n<< /Type /Page /Contents 4 0 R >>\nendobj\n" +
			"5 0 obj\n<< /Type /Sig /ByteRange [0 0000000100 0000000200 0000000300] /Contents <abcdef> >>\nendobj\n" +
			"6 0 obj\n<< /Type /Sig /ByteRange [" + byteRangePlaceholder + "] /Contents <0000> >>\nendobj\n")

	slots, err := findSigSlots(data, "signing")
	if err != nil {
		t.Fatalf("findSigSlots: %v", err)
	}
	if got := string(data[slots.byteRange : slots.byteRange+len(byteRangePlaceholder)]); got != byteRangePlaceholder {
		t.Errorf("byteRange offset does not point at the placeholder: %q", got)
	}
	if got := string(data[slots.contentsStart:slots.contentsEnd]); got != "<0000>" {
		t.Errorf("contents window = %q, want the unfilled <0000> of object 6", got)
	}
}

// TestFindSigSlotsMissingPlaceholder confirms the error path is reported against
// the placeholder rather than silently patching arbitrary bytes.
func TestFindSigSlotsMissingPlaceholder(t *testing.T) {
	if _, err := findSigSlots([]byte("<< /Type /Page /Contents 4 0 R >>"), "signing"); err == nil {
		t.Fatal("expected an error when no /ByteRange placeholder is present")
	}
}
