package pdf0

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildShiftedOffsetPDF returns a PDF whose cross-reference table parses but
// whose per-object offsets are all shifted by delta bytes — the sweep-13
// holdout shape (its entries pointed into the previous object's "endobj").
func buildShiftedOffsetPDF(delta int) string {
	body := "%PDF-1.7\n"
	oneAt := len(body)
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	twoAt := len(body)
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	xrefAt := len(body)
	xref := fmt.Sprintf("xref\n0 3\n0000000000 65535 f \n%010d 00000 n \n%010d 00000 n \ntrailer\n<< /Root 1 0 R /Size 3 >>\n",
		oneAt+delta, twoAt+delta)
	return body + xref + fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefAt)
}

// TestRebuildShiftedObjectOffsets: a table whose offsets are wholesale shifted
// loads nothing at its stated positions; Read rebuilds the table by scanning
// for object headers (ISO 32000-2, 7.3.10 defines the "N G obj" form) and
// recovers both objects.
func TestRebuildShiftedObjectOffsets(t *testing.T) {
	pdf := buildShiftedOffsetPDF(17)
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read did not rebuild a shifted table: %v", err)
	}
	if len(doc.Objects) != 2 {
		t.Errorf("loaded %d objects, want 2", len(doc.Objects))
	}
	if getCatalog(doc) == nil {
		t.Error("catalog not reachable after rebuild")
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rt, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("round-trip re-Read: %v", err)
	}
	if !DocumentEqual(doc, rt) {
		t.Error("round trip lost content")
	}
}

// TestRebuildDeadStartxref: startxref points at bytes that are neither a table
// nor an xref stream, with no keyword within the relocation window. The file
// still holds well-formed objects and a trailer, so the scan rebuild recovers
// it.
func TestRebuildDeadStartxref(t *testing.T) {
	body := "%PDF-1.7\n"
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	body += "trailer\n<< /Root 1 0 R /Size 3 >>\n"
	pdf := body + "startxref\n3\n%%EOF\n" // offset 3: inside the header
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read did not rebuild from a dead startxref: %v", err)
	}
	if len(doc.Objects) != 2 || getCatalog(doc) == nil {
		t.Errorf("rebuild incomplete: %d objects", len(doc.Objects))
	}
}

// TestRebuildSynthesizesRoot: no trailer keyword exists anywhere (the shape of
// an xref-stream file whose stream object is the broken part); /Root is
// synthesized from the catalog object (ISO 32000-2, 7.7.2: the catalog is the
// root of the object hierarchy).
func TestRebuildSynthesizesRoot(t *testing.T) {
	body := "%PDF-1.7\n"
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	pdf := body + "startxref\n3\n%%EOF\n"
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read did not synthesize a trailer: %v", err)
	}
	root, ok := doc.Trailer.Get("Root").(IndirectRef)
	if !ok || root.Number != 1 {
		t.Fatalf("synthesized /Root = %v, want 1 0 R", doc.Trailer.Get("Root"))
	}
}

// TestRebuildLastDefinitionWins: the same object number defined twice takes
// the later definition, matching the update precedence of ISO 32000-2, 7.5.6.
func TestRebuildLastDefinitionWins(t *testing.T) {
	body := "%PDF-1.7\n"
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /Version /A >>\nendobj\n"
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R /Version /B >>\nendobj\n"
	body += "trailer\n<< /Root 1 0 R /Size 3 >>\n"
	pdf := body + "startxref\n3\n%%EOF\n"
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	cat := getCatalog(doc)
	if v, _ := cat.Get("Version").(Name); v != "B" {
		t.Errorf("catalog /Version = %q, want the later definition B", v)
	}
}

// TestRebuildDropsUnparseableEntries: a header-shaped byte run that is not
// followed by a parseable object (here inside what would be stream-like
// garbage) is dropped by the lenient load instead of failing the rebuild.
func TestRebuildDropsUnparseableEntries(t *testing.T) {
	body := "%PDF-1.7\n"
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	body += "% garbage: 99 0 obj ((( \n"
	body += "trailer\n<< /Root 1 0 R /Size 3 >>\n"
	pdf := body + "startxref\n3\n%%EOF\n"
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, ok := doc.Objects[99]; ok {
		t.Error("an unparseable scanned entry was kept")
	}
	if len(doc.Objects) != 2 {
		t.Errorf("loaded %d objects, want 2", len(doc.Objects))
	}
}

// TestRebuildMaterializesObjStm: a rebuilt table has no type-2 entries, so
// the contents of /Type /ObjStm containers are materialized directly from the
// containers the scan found.
func TestRebuildMaterializesObjStm(t *testing.T) {
	// An uncompressed object stream holding objects 3 and 4.
	payload := "<< /A 1 >> << /B 2 >>"
	header := "3 0 4 11 "
	stm := header + payload
	body := "%PDF-1.7\n"
	body += fmt.Sprintf("5 0 obj\n<< /Type /ObjStm /N 2 /First %d /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(header), len(stm), stm)
	body += "1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	body += "2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n"
	body += "trailer\n<< /Root 1 0 R /Size 6 >>\n"
	pdf := body + "startxref\n3\n%%EOF\n"
	doc, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, num := range []int{3, 4} {
		iobj, ok := doc.Objects[num]
		if !ok {
			t.Fatalf("object %d from the scanned object stream was not materialized", num)
		}
		if _, ok := iobj.Value.(*Dictionary); !ok {
			t.Errorf("object %d is %T, want *Dictionary", num, iobj.Value)
		}
	}
}

// TestRebuildFindsNothingStillFails: a file with a dead startxref and no
// object headers at all is not recoverable and must keep returning an error.
func TestRebuildFindsNothingStillFails(t *testing.T) {
	pdf := "%PDF-1.7\n" + strings.Repeat("% nothing here\n", 20) + "startxref\n3\n%%EOF\n"
	if _, err := Read(bytes.NewReader([]byte(pdf)), int64(len(pdf))); err == nil {
		t.Fatal("expected an error for a file with no recoverable objects")
	}
}
