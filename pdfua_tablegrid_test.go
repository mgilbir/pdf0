package pdf0

import (
	"strings"
	"testing"
	"time"
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

// TestGridDefectsSparseHuge guards against the rows×width blow-up: a table with
// tens of thousands of rows and a single very wide row must be analyzed in time
// proportional to the row count, not the (billions of) grid cells — while still
// detecting (or not detecting) the hole correctly.
func TestGridDefectsSparseHuge(t *testing.T) {
	const nRows = 60000
	const width = 30000

	// One wide row of `width` single cells; every other row has a single cell,
	// so those rows leave the rest of the grid empty → a hole.
	holed := make([]tableRow, nRows)
	wideRow := make(tableRow, width)
	for c := range wideRow {
		wideRow[c] = cell(1, 1)
	}
	holed[0] = wideRow
	for r := 1; r < nRows; r++ {
		holed[r] = tableRow{cell(1, 1)}
	}

	start := time.Now()
	got := gridDefects(holed)
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("gridDefects took %v on a %dx%d sparse table; expected O(rows)", el, nRows, width)
	}
	hasHole := false
	for _, e := range got {
		if strings.Contains(e.Message, "empty") {
			hasHole = true
		}
	}
	if !hasHole {
		t.Error("sparse huge table with short rows should be flagged as having a hole")
	}

	// A tall single-column table (every row exactly one cell) fills its whole
	// width, so there is no hole — the O(rows) test must agree.
	full := make([]tableRow, nRows)
	for r := range full {
		full[r] = tableRow{cell(1, 1)}
	}
	if v := gridDefects(full); len(v) != 0 {
		t.Errorf("tall single-column table flagged: %v", v)
	}
}

// TestGridDefectsSpanBomb guards against the fill-loop blow-up: a cell whose
// ColSpan or RowSpan is enormous must not make gridDefects fill billions of
// grid slots. Such a table exceeds the work budget and is analyzed in bounded
// time (reported with no grid defects rather than hanging).
func TestGridDefectsSpanBomb(t *testing.T) {
	cases := []struct {
		name string
		rows []tableRow
	}{
		// A single cell claiming a two-billion-column span.
		{"colspan", []tableRow{{cell(1, 1 << 31)}, {cell(1, 1), cell(1, 1)}}},
		// A single cell claiming a two-billion-row span.
		{"rowspan", []tableRow{{cell(1<<31, 1)}, {cell(1, 1)}}},
		// Near-int-max spans must not overflow the budget arithmetic.
		{"maxint", []tableRow{{cell(1, 1<<62)}, {cell(1, 1<<62)}}},
		// Many moderately-large cells that together blow the budget.
		{"cumulative", func() []tableRow {
			rows := make([]tableRow, 100)
			for i := range rows {
				rows[i] = tableRow{cell(1, 1 << 20)}
			}
			return rows
		}()},
	}
	// The budget bounds the work to a few million map writes (~seconds at the
	// ceiling), so any of these returns quickly; an unbounded fill would run for
	// minutes. The threshold is generous so the test is not flaky under load —
	// the point is bounded-vs-unbounded, not a precise time.
	for _, tc := range cases {
		done := make(chan int, 1)
		go func() { done <- len(gridDefects(tc.rows)) }()
		select {
		case <-done:
		case <-time.After(25 * time.Second):
			t.Fatalf("%s: gridDefects did not return within 25s; span bomb not bounded", tc.name)
		}
	}

	// A table just under the budget is still laid out normally (not treated as
	// oversize): two full rows of a modest width report no defect.
	small := []tableRow{{cell(1, 3)}, {cell(1, 1), cell(1, 1), cell(1, 1)}}
	if v := gridDefects(small); len(v) != 0 {
		t.Errorf("well-formed small table flagged: %v", v)
	}
}
