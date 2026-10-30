package pdf0

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/iso32005"
	"github.com/mgilbir/pdf0/internal/xlsxgrid"
	"github.com/mgilbir/pdf0/object"
)

// TestISO32005TableAgreesWithAnnexL holds pdf0's encoding of ISO/TS 32005
// Table 5 (internal/iso32005) to ISO 32000-2 Annex L, the parent-child rules
// of the PDF 2.0 elements, which the Technical Specification extends and
// defers to "and associated errata". ISO 32000-2 carries Annex L as a
// machine-readable matrix, an xlsx attached to the standard's PDF: this test
// reads it from a copy of ISO 32000-2 named by PDF0_ISO32000_2, with pdf0's
// own reader, and compares every pair of PDF 2.0 elements it states. The
// standard is not in this repository, and is not to be: without one the test
// skips.
func TestISO32005TableAgreesWithAnnexL(t *testing.T) {
	path := os.Getenv("PDF0_ISO32000_2")
	if path == "" {
		t.Skip("PDF0_ISO32000_2 names no copy of ISO 32000-2 (with its Annex L matrix attached)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	xlsx := annexLMatrix(t, d)
	cells, err := xlsxgrid.Sheet(xlsx, "")
	if err != nil {
		t.Fatal(err)
	}
	matrix := map[[2]int]string{}
	for c, v := range cells {
		matrix[[2]int{c.Row, c.Col}] = v
	}

	// Row 2 names the parents from column 3; column 2 the children from row
	// 3, until the legend.
	parents := map[int]object.Name{}
	for c := 3; ; c++ {
		v, ok := matrix[[2]int{2, c}]
		if !ok {
			break
		}
		parents[c] = object.Name(strings.Fields(v)[0]) // "StructTreeRoot 1": a footnote mark
	}
	want := map[string]iso32005.Inclusion{
		"0..n": iso32005.Any, "0..1": iso32005.AtMostOne, "1": iso32005.ExactlyOne,
		"∅*": iso32005.Grouping, "‡": iso32005.Described, "[a]": iso32005.Described, "[b]": iso32005.Described,
		"∅": 0, "": 0,
	}
	pairs := 0
	for r := 3; ; r++ {
		child, ok := matrix[[2]int{r, 2}]
		if !ok || strings.HasPrefix(child, "Legend") {
			break
		}
		childName := object.Name(strings.TrimSpace(child))
		if childName == "content item" {
			childName = iso32005.ContentItem
		}
		for c, parent := range parents {
			row, ok := iso32005.Children[parent]
			if !ok {
				t.Errorf("Annex L names a parent %s the table has no row for", parent)
				continue
			}
			cell := strings.TrimSpace(matrix[[2]int{r, c}])
			w, known := want[cell]
			if !known {
				t.Errorf("Annex L, %s in %s: a value this test cannot read, %q", childName, parent, cell)
				continue
			}
			pairs++
			if got := row[childName]; got != w {
				t.Errorf("%s in %s: the table says %d, Annex L %q", childName, parent, got, cell)
			}
		}
	}
	if pairs < 1000 {
		t.Fatalf("compared %d pairs; the matrix states over 1700", pairs)
	}
	t.Logf("%d pairs of PDF 2.0 elements agree with Annex L", pairs)
}

// annexLMatrix is the xlsx ISO 32000-2 attaches as Annex L's matrix. The
// standard attaches it with a file attachment annotation, not in its
// EmbeddedFiles tree, so every file specification is looked at.
func annexLMatrix(t *testing.T, d *Document) []byte {
	t.Helper()
	for _, iobj := range d.Objects {
		fs, ok := iobj.Value.(*object.Dictionary)
		if !ok || fs.Get("EF") == nil {
			continue
		}
		var name string
		for _, k := range []object.Name{"UF", "F"} {
			if s, ok := d.Resolve(fs.Get(k)).(object.String); ok {
				name = string(s.Value)
				break
			}
		}
		if !strings.Contains(name, "AnnexL_matrix") || !strings.HasSuffix(name, ".xlsx") {
			continue
		}
		ef := d.ResolveDict(fs.Get("EF"))
		s, ok := d.Resolve(ef.Get("F")).(*object.Stream)
		if !ok {
			t.Fatalf("%s: no embedded file stream", name)
		}
		b, err := d.StreamData(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Fatal("the PDF has no Annex L matrix (…AnnexL_matrix….xlsx) attached")
	return nil
}
