package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripReferencePDFs(t *testing.T) {
	for _, path := range testfiles.PDF20Examples.Glob(t, "*.pdf") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			testRoundTripFile(t, path)
		})
	}
}

func testRoundTripFile(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}

	r := bytes.NewReader(data)
	doc, err := Read(r, int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	t.Logf("parsed %s: version=%s, %d objects", filepath.Base(path), doc.Version, len(doc.Objects))

	// Write
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read back
	data2 := buf.Bytes()
	r2 := bytes.NewReader(data2)
	doc2, err := Read(r2, int64(len(data2)))
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}

	// Semantic comparison
	if !DocumentEqual(doc, doc2) {
		t.Error("documents not semantically equal after round-trip")

		// Report differences
		if doc.Version != doc2.Version {
			t.Errorf("  version: %q vs %q", doc.Version, doc2.Version)
		}
		if len(doc.Objects) != len(doc2.Objects) {
			t.Errorf("  object count: %d vs %d", len(doc.Objects), len(doc2.Objects))
		}
		for num, obj1 := range doc.Objects {
			obj2, ok := doc2.Objects[num]
			if !ok {
				t.Errorf("  object %d: missing in round-tripped document", num)
				continue
			}
			if !Equal(obj1, obj2) {
				t.Errorf("  object %d: differs", num)
			}
		}
	}
}

func TestReadSimplePDF(t *testing.T) {
	path := testfiles.PDF20Examples.File(t, "Simple PDF 2.0 file.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	r := bytes.NewReader(data)
	doc, err := Read(r, int64(len(data)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if doc.Version != "2.0" {
		t.Errorf("version: expected '2.0', got %q", doc.Version)
	}

	// Check trailer has /Root
	root := doc.Trailer.Get("Root")
	if root == nil {
		t.Fatal("trailer missing /Root")
	}

	t.Logf("Document has %d objects", len(doc.Objects))
	for num, obj := range doc.Objects {
		dict, ok := obj.Value.(*object.Dictionary)
		if ok {
			typeVal := dict.Get("Type")
			if typeVal != nil {
				t.Logf("  object %d: /Type %v", num, typeVal)
			} else {
				t.Logf("  object %d: dictionary (%d keys)", num, dict.Len())
			}
		} else {
			t.Logf("  object %d: %T", num, obj.Value)
		}
	}
}
