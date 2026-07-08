package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// loadRefPDF returns the bytes of a reference PDF, skipping the test if the
// (gitignored) reference corpus is absent.
func loadRefPDF(t *testing.T) []byte {
	t.Helper()
	files, _ := filepath.Glob("testdata/pdf20examples/*.pdf")
	if len(files) == 0 {
		t.Skip("testdata/pdf20examples not present")
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Skipf("reading reference PDF: %v", err)
	}
	return b
}

// TestWrittenXrefIs20Bytes ensures every written xref entry is exactly 20 bytes
// and that the builder's output passes the byte-level 6.1.4 rule at every level
// (audit C6).
func TestWrittenXrefIs20Bytes(t *testing.T) {
	for _, lvl := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		doc := NewPDFADocumentWithInfo(lvl, "T", "A")
		var buf bytes.Buffer
		if err := doc.Write(&buf); err != nil {
			t.Fatalf("level %v: write: %v", lvl, err)
		}
		// Every "n"/"f" entry line must be 20 bytes including its EOL.
		re := regexp.MustCompile(`(?m)^\d{10} \d{5} [nf]\r?\n`)
		for _, m := range re.FindAll(buf.Bytes(), -1) {
			if len(m) != 20 {
				t.Errorf("level %v: xref entry line is %d bytes, want 20: %q", lvl, len(m), m)
			}
		}
		rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("level %v: reparse: %v", lvl, err)
		}
		for _, e := range ValidatePDFABytes(rd, lvl, buf.Bytes()) {
			if e.Rule == "6.1.4" {
				t.Errorf("level %v: written file still fails 6.1.4: %s", lvl, e.Error())
			}
		}
	}
}

// TestWriteRejectsObject0 ensures an in-use object 0 is refused rather than
// silently dropped (audit C16).
func TestWriteRejectsObject0(t *testing.T) {
	d := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	dict := &Dictionary{}
	dict.Set("Type", Name("Catalog"))
	d.Objects[0] = &IndirectObject{Number: 0, Value: dict}
	var buf bytes.Buffer
	if err := d.Write(&buf); err == nil {
		t.Fatalf("expected Write to refuse an in-use object 0, got nil")
	}
}

// TestWriteUpdatesIndirectLength ensures a stale indirect /Length target is
// rewritten to the actual data length (audit C8).
func TestWriteUpdatesIndirectLength(t *testing.T) {
	d := &Document{Version: "2.0", Objects: map[int]*IndirectObject{}, Trailer: Dictionary{}}
	st := &Stream{Dict: Dictionary{}, Data: []byte("Hello World")} // 11 bytes
	st.Dict.Set("Length", IndirectRef{Number: 2})
	d.Objects[1] = &IndirectObject{Number: 1, Value: st}
	d.Objects[2] = &IndirectObject{Number: 2, Value: Integer(3)} // stale: says 3
	d.Trailer.Set("Root", IndirectRef{Number: 1})

	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got, ok := rd.Objects[2].Value.(Integer); !ok || int(got) != len(st.Data) {
		t.Errorf("length object = %v, want %d", rd.Objects[2].Value, len(st.Data))
	}
	// The caller's document must not be mutated.
	if got := d.Objects[2].Value.(Integer); int(got) != 3 {
		t.Errorf("caller's length object was mutated to %d", got)
	}
}

// TestWriteObjectNumberMatchesKey ensures Read makes the xref key authoritative
// so Write never emits a body numbered differently from its slot (audit C7).
func TestWriteObjectNumberMatchesKey(t *testing.T) {
	// Round-trip a reference file and assert key == Number for every object.
	b := loadRefPDF(t)
	doc, err := Read(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for num, iobj := range doc.Objects {
		if iobj.Number != num {
			t.Errorf("object key %d holds a body numbered %d", num, iobj.Number)
		}
	}
}
