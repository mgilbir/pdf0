package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestBuilderWriteValidatesClean guards the write path at the byte level, which
// the model-only round-trip test (DocumentEqual) cannot see. NewPDFADocument
// produces a conformant document; after Write and re-Read, ValidatePDFABytes —
// including the byte-level structure rules (xref format, stream lengths, no data
// after %%EOF) — must report zero violations. A writer regression that emits a
// stale /Length, a malformed xref, or a duplicate/dangling object would surface
// here even though DocumentEqual still passed.
func TestBuilderWriteValidatesClean(t *testing.T) {
	for _, lvl := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		doc := NewPDFADocumentWithInfo(lvl, "Title", "Author")
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Fatalf("%s: write: %v", lvl, err)
		}
		rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("%s: reparse written output: %v", lvl, err)
		}
		if errs := ValidatePDFABytes(rd, lvl, buf.Bytes()); len(errs) != 0 {
			t.Errorf("%s: written output has %d validation errors (writer regressed):", lvl, len(errs))
			for _, e := range errs {
				t.Errorf("   %s", e.Error())
			}
		}
	}
}

// TestWriteIsIdempotent guards write stability: Read -> Write -> Read -> Write
// must produce byte-identical output, and the two reparses must be
// DocumentEqual. An unstable or lossy writer (nondeterministic ordering, a
// dropped object, a length that shifts on rewrite) breaks this even when a
// single round-trip's DocumentEqual holds.
func TestWriteIsIdempotent(t *testing.T) {
	docs := map[string]*Document{
		"builder-2b": NewPDFADocumentWithInfo(PDFA2b, "T", "A"),
		"builder-4":  NewPDFADocument(PDFA4),
	}
	files, _ := filepath.Glob("testdata/pdf20examples/*.pdf")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := Read(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Errorf("%s: read: %v", filepath.Base(f), err)
			continue
		}
		docs[filepath.Base(f)] = doc
	}
	if len(docs) == 2 {
		t.Log("testdata/pdf20examples not present; testing builder docs only")
	}

	for name, doc := range docs {
		var w1 bytes.Buffer
		if err := doc.Write(&w1); err != nil {
			t.Errorf("%s: first write: %v", name, err)
			continue
		}
		r1, err := Read(bytes.NewReader(w1.Bytes()), int64(w1.Len()))
		if err != nil {
			t.Errorf("%s: reparse first write: %v", name, err)
			continue
		}
		var w2 bytes.Buffer
		if err := r1.Write(&w2); err != nil {
			t.Errorf("%s: second write: %v", name, err)
			continue
		}
		if !bytes.Equal(w1.Bytes(), w2.Bytes()) {
			t.Errorf("%s: write is not idempotent (%d vs %d bytes)", name, w1.Len(), w2.Len())
		}
		r2, err := Read(bytes.NewReader(w2.Bytes()), int64(w2.Len()))
		if err != nil {
			t.Errorf("%s: reparse second write: %v", name, err)
			continue
		}
		if !DocumentEqual(r1, r2) {
			t.Errorf("%s: reparsed writes are not DocumentEqual", name)
		}
	}
}
