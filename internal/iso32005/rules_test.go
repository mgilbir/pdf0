package iso32005

import (
	"os"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/xlsxgrid"
	"github.com/mgilbir/pdf0/object"
)

// spreadsheetDiffers are the pairs, "Parent-Child", where the table and the
// PDF Association's spreadsheet with errata differ, each for a reason: the
// standard's text, or an erratum resolved after the spreadsheet (August
// 2023) was published.
var spreadsheetDiffers = map[string]string{
	// The published table lists a Title among a BlockQuote's children; the
	// spreadsheet does not. The standard's text governs.
	"BlockQuote-Title": "the Technical Specification's text",
	// pdf-association/pdf-issues#440: the spreadsheet's undefined "c".
	"WP-Figure": "#440",
}

// TestISO32005TableAgreesWithTheInclusionRules holds the table to the PDF
// Association's "Hierarchical inclusion rules for PDF tags expressed in ISO TS
// 32005", the spreadsheet of Table 5 with the errata the PDF/UA TWG approved,
// read from a copy named by PDF0_ISO32005_RULES. The spreadsheet is not in
// this repository, and is not to be: without one the test skips. Reference's
// row is held to Link's (pdf-association/pdf-issues#488, resolved after the
// spreadsheet), with Link and Reference permitted.
func TestISO32005TableAgreesWithTheInclusionRules(t *testing.T) {
	path := os.Getenv("PDF0_ISO32005_RULES")
	if path == "" {
		t.Skip("PDF0_ISO32005_RULES names no copy of the PDF Association's hierarchical inclusion rules spreadsheet")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cells, err := xlsxgrid.Sheet(data, "Errata")
	if err != nil {
		t.Fatal(err)
	}
	// Row 5 names the parents from column 2; column 1 the children from
	// row 6.
	parents := map[int]object.Name{}
	for c := 2; ; c++ {
		v, ok := cells[xlsxgrid.Cell{Row: 5, Col: c}]
		if !ok {
			break
		}
		parents[c] = object.Name(strings.TrimSpace(v))
	}
	want := map[string]Inclusion{
		"0..n": Any, "0..1": AtMostOne, "1": ExactlyOne, "∅*": Grouping,
		"‡": Described, "[a]": Described, "[b]": Described, "c": Described,
		"∅": 0, "": 0,
	}
	pairs := 0
	for r := 6; ; r++ {
		v, ok := cells[xlsxgrid.Cell{Row: r, Col: 1}]
		if !ok {
			break
		}
		child := object.Name(strings.TrimSpace(v))
		if child == "Content Item" {
			child = ContentItem
		}
		for c, parent := range parents {
			cell := strings.TrimSpace(cells[xlsxgrid.Cell{Row: r, Col: c}])
			w, known := want[cell]
			if !known {
				t.Errorf("the spreadsheet, %s in %s: a value this test cannot read, %q", child, parent, cell)
				continue
			}
			row, ok := Children[parent]
			if !ok {
				t.Errorf("the spreadsheet names a parent %s the table has no row for", parent)
				continue
			}
			if parent == "Reference" {
				// #488: Link's content model, Link and Reference permitted.
				w = Children["Link"][child]
				if child == "Link" || child == "Reference" {
					w = Any
				}
			}
			pairs++
			if got := row[child]; got != w {
				if _, ok := spreadsheetDiffers[string(parent)+"-"+string(child)]; ok {
					continue
				}
				t.Errorf("%s in %s: the table says %d, the spreadsheet %q", child, parent, got, cell)
			}
		}
	}
	if pairs < 2500 {
		t.Fatalf("compared %d pairs; the spreadsheet states over 2800", pairs)
	}
	t.Logf("%d pairs agree with the inclusion rules spreadsheet", pairs)
}
