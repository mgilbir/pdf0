package pdf0

import (
	"strings"
	"testing"
)

// cell is a test helper for a (rowSpan, colSpan) cell.
func cell(rs, cs int) tableCell { return tableCell{rowSpan: rs, colSpan: cs} }

func TestGridDefects(t *testing.T) {
	has := func(vs []UAViolation, sub string) bool {
		for _, e := range vs {
			if strings.Contains(e.Message, sub) {
				return true
			}
		}
		return false
	}

	// A clean 2x2 grid.
	clean := []tableRow{{cell(1, 1), cell(1, 1)}, {cell(1, 1), cell(1, 1)}}
	if v := gridDefects(clean); len(v) != 0 {
		t.Errorf("clean 2x2 grid flagged: %v", v)
	}

	// A clean grid with a valid rowspan (col 0 spans both rows).
	spanOK := []tableRow{{cell(2, 1), cell(1, 1)}, {cell(1, 1)}}
	if v := gridDefects(spanOK); len(v) != 0 {
		t.Errorf("valid rowspan grid flagged: %v", v)
	}

	// A clean grid with a valid colspan header row.
	colspanOK := []tableRow{{cell(1, 2)}, {cell(1, 1), cell(1, 1)}}
	if v := gridDefects(colspanOK); len(v) != 0 {
		t.Errorf("valid colspan grid flagged: %v", v)
	}

	// RowSpan extends beyond the table (3 rows, cell claims 5).
	oob := []tableRow{{cell(5, 1), cell(1, 1)}, {cell(1, 1)}, {cell(1, 1)}}
	if !has(gridDefects(oob), "beyond the last row") {
		t.Error("out-of-bounds rowspan not flagged")
	}

	// A hole: second row is short, leaving an empty grid cell.
	hole := []tableRow{{cell(1, 1), cell(1, 1)}, {cell(1, 1)}}
	if !has(gridDefects(hole), "empty") {
		t.Error("grid hole not flagged")
	}

	// A colspan that widens one row past the other creates a hole.
	wide := []tableRow{{cell(1, 3)}, {cell(1, 1), cell(1, 1)}}
	if !has(gridDefects(wide), "empty") {
		t.Error("inconsistent width not flagged")
	}
}
