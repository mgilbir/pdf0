package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUATableHeadersDirectCells: data cells written directly in their row are
// each reported, at no object — not merged into one report under the "object
// -1" every direct cell used to share (audit 2026-09-22 C138). The header rule
// is checkUATableGrid's (tableHeaderDefects); TestUATableHeaderAssociation
// holds its boundaries.
func TestUATableHeadersDirectCells(t *testing.T) {
	doc := mkView(map[int]*object.IndirectObject{}, nil)
	cell := func(s string) *object.Dictionary {
		d := &object.Dictionary{}
		d.Set("S", object.Name(s))
		return d
	}
	head := &object.Dictionary{}
	head.Set("S", object.Name("TR"))
	head.Set("K", object.Array{cell("TH"), cell("TH"), cell("TH")})
	body := &object.Dictionary{}
	body.Set("S", object.Name("TR"))
	body.Set("K", object.Array{cell("TD"), cell("TD"), cell("TD")})
	table := &object.Dictionary{}
	table.Set("S", object.Name("Table"))
	table.Set("K", object.Array{head, body})
	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	root.Set("K", table)
	doc.Objects[2] = &object.IndirectObject{Number: 2, Value: root}
	cat := &object.Dictionary{}
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 2})
	doc.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}

	var v []Violation
	for _, e := range checkUATableGrid(doc, cat, "1") {
		if e.Clause == "7.5" {
			v = append(v, e)
		}
	}
	if len(v) != 3 {
		t.Fatalf("three direct data cells naming no headers under unscoped THs: want three findings, got %v", v)
	}
	for _, e := range v {
		if e.Object != 0 {
			t.Errorf("a direct cell anchored to object %d, want 0: %v", e.Object, e)
		}
	}
}

// TestPositionalHeadersFindsTheHeader: in PDF/UA-2 every data cell under an
// unscoped header row finds that header by position, however far down the
// column it is. The search carries its state down each column (and along
// each row) rather than walking back from every cell.
func TestPositionalHeadersFindsTheHeader(t *testing.T) {
	const n = 1000
	rows := []tableRow{{{rowSpan: 1, colSpan: 1, th: true, elem: &object.Dictionary{}}}}
	for i := 0; i < n; i++ {
		rows = append(rows, tableRow{{rowSpan: 1, colSpan: 1, elem: &object.Dictionary{}}})
	}
	l := layoutTable(rows, core.DefaultMaxTableGridFills)
	if !l.complete || len(l.defects) != 0 {
		t.Fatalf("layout: complete=%v defects=%v", l.complete, l.defects)
	}
	got := positionalHeaders(mkView(nil, nil), l)
	for i, c := range l.cells {
		if !c.th && !got[i] {
			t.Fatalf("data cell in row %d did not find the header above it", c.row)
		}
	}
}
