package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// The collapsing border model, CSS 2.1 §17.6.2.
//
// # Why these tests are built the way they are
//
// §17.6.2.1 is an *ordered* cascade — hidden, then none, then the widest, then
// the style, then the element — and an ordered cascade is unusually good at
// passing a test for the wrong reason. A case where a 5px border beats a 1px one
// comes out right whatever the style-priority rule does, because the widest-wins
// rule decided it before the style was ever consulted. A suite of such cases
// looks thorough and proves only the one rule.
//
// So each case below is built so that exactly *one* rule can decide it, and the
// comment on each says which: equal widths to test the style order, equal widths
// and equal styles to test the element order, and a wider hidden border against
// a narrower solid one to check that hidden really does come first rather than
// merely usually winning.
//
// Every expected number is arithmetic from §17.6.2's own equation
//
//	row-width = 0.5*bw0 + pl1 + w1 + pr1 + bw1 + ... + prn + 0.5*bwn
//
// rather than a number read off a run.

// collapsing is the stylesheet these tests are written against: nothing from the
// user-agent sheet, so an expected number is the rule under test rather than the
// rule plus 1px of cell padding.
const collapsing = `
html, body, p, div { margin: 0; padding: 0 }
table { border-collapse: collapse }
td, th { padding: 0 }
`

// ---------------------------------------------------------------------------
// The conflict resolution, one rule at a time
// ---------------------------------------------------------------------------

// TestBorderConflictRules is §17.6.2.1, each case isolating one rule.
//
// The document is always two cells side by side, so the only edge in question is
// the vertical grid line between them and the only interesting number is the
// width the whole table comes to. With the outer lines held at zero, that width
// is exactly the winning border on the middle line — which is what makes the
// assertion a statement about the resolution rather than about the geometry.
func TestBorderConflictRules(t *testing.T) {
	cases := []struct {
		name  string
		rule  string
		css   string
		width float64
	}{{
		// The widest-wins rule, in the only shape where it can be the deciding
		// one: two solid borders differing only in width.
		name:  "the wider border wins",
		rule:  "narrow borders are discarded in favor of wider ones",
		css:   `#a { border-right: 4px solid red } #b { border-left: 10px solid blue }`,
		width: 10,
	}, {
		// Equal widths, so the widest-wins rule cannot decide it and the style
		// order must. double beats solid; both are 6px, so a broken style rule
		// would give 6 as well — which is why the assertion below is on the
		// *colour* as well as the width.
		name:  "on equal widths double beats solid",
		rule:  "double, solid, dashed, dotted, ridge, outset, groove, inset",
		css:   `#a { border-right: 6px solid red } #b { border-left: 6px double blue }`,
		width: 6,
	}, {
		// Equal widths again, and the pair is chosen from the far end of the
		// order: groove beats inset and nothing else separates them.
		name:  "on equal widths groove beats inset",
		rule:  "double, solid, dashed, dotted, ridge, outset, groove, inset",
		css:   `#a { border-right: 6px inset red } #b { border-left: 6px groove blue }`,
		width: 6,
	}, {
		// A hidden border is *narrower* than the one it beats — it has no width
		// at all — so this can only pass if hidden is consulted before the width.
		// The table comes to nothing across, because the only line with a border
		// has been suppressed.
		name:  "hidden beats a wider border",
		rule:  "borders with the border-style of hidden take precedence",
		css:   `#a { border-right: 20px hidden red } #b { border-left: 20px solid blue }`,
		width: 0,
	}, {
		// none loses to everything, and the way it loses is that it has no width:
		// a border-width declared beside "border-style: none" is not a width at
		// all, which is the same rule that makes "border-width: 5px" on an
		// ordinary box occupy nothing. So a 40px none loses to a 1px solid.
		//
		// This is deliberately not written as "none loses on a tie", which is how
		// the specification states it and which nothing can observe: a none
		// border is zero wide, so the widest-wins rule has already settled it,
		// and two zero-width borders draw the same nothing either way.
		name:  "a none border has no width, however wide it was declared",
		rule:  "borders with a style of none have the lowest priority",
		css:   `#a { border-right: 40px none red } #b { border-left: 1px solid blue }`,
		width: 1,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := layoutOf(t, 1000,
				`<table id=t><tr><td id=a></td><td id=b></td></tr></table>`,
				collapsing+tc.css)
			table := find(t, root, "t")
			px(t, "the table's width ("+tc.rule+")", table.BorderRect.W, tc.width)
		})
	}
}

// TestBorderConflictKeepsTheWinnersStyle pins the half of the resolution that a
// width cannot see: *which* border was chosen, not merely how wide it is.
//
// Both candidates are 6px, so every case here comes out 6px wide whichever wins.
// The only evidence is what is drawn, and each case is a pair whose two borders
// are told apart by a colour that only the winner can produce.
func TestBorderConflictKeepsTheWinnersStyle(t *testing.T) {
	red := style.RGBA{R: 255, A: 1}
	blue := style.RGBA{B: 255, A: 1}

	cases := []struct {
		name string
		rule string
		css  string
		want style.RGBA
	}{{
		name: "double beats solid",
		rule: "style order",
		css:  `#a { border-right: 6px solid red } #b { border-left: 6px double blue }`,
		want: blue,
	}, {
		name: "solid beats dashed",
		rule: "style order",
		css:  `#a { border-right: 6px dashed red } #b { border-left: 6px solid blue }`,
		want: blue,
	}, {
		// Equal width *and* equal style, so nothing but the element order can
		// decide it: §17.6.2.1's "the one further to the left wins".
		name: "the left cell wins a tie",
		rule: "element order",
		css:  `#a { border-right: 6px solid blue } #b { border-left: 6px solid red }`,
		want: blue,
	}, {
		// Equal width and style again, and now the pair is a cell against the
		// row it is in. A cell wins over a row whatever their positions, which
		// is the first half of the element rule rather than the tie-break.
		name: "a cell beats the row it is in",
		rule: "element order",
		css:  `#r { border-left: 6px solid red } #a { border-left: 6px solid blue }`,
		want: blue,
	}, {
		// A row against the table: same width, same style, and the row is
		// earlier in §17.6.2.1's list.
		name: "a row beats the table",
		rule: "element order",
		css: `#t { border-left: 6px solid red } ` +
			`#r { border-left: 6px solid blue }`,
		want: blue,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The cells hold text so that the rows have a height: a border with
			// no length to run along paints nothing, and this test would then
			// pass by drawing neither candidate.
			ops := paintOf(t,
				`<table id=t><tr id=r><td id=a>x</td><td id=b>y</td></tr></table>`,
				collapsing+tc.css)
			var other style.RGBA
			if tc.want == blue {
				other = red
			} else {
				other = blue
			}
			var wanted, unwanted int
			for _, op := range ops {
				f, ok := op.(FillRect)
				if !ok || f.Rect.Empty() {
					continue
				}
				switch f.Color {
				case tc.want:
					wanted++
				case other:
					unwanted++
				}
			}
			if wanted == 0 {
				t.Errorf("the winning border (%s) was not drawn at all", tc.rule)
			}
			if unwanted != 0 {
				t.Errorf("the losing border was drawn %d times; %s decides this edge",
					unwanted, tc.rule)
			}
		})
	}
}

// TestBorderConflictOrderIsACascade is the check that the four rules are applied
// in sequence rather than as a set.
//
// Each pair is arranged so that a *later* rule, applied on its own, would give
// the other answer. If the implementation consulted them in the wrong order —
// or consulted only the ones that happened to differ — one of these comes out
// backwards.
func TestBorderConflictOrderIsACascade(t *testing.T) {
	cases := []struct {
		name string
		css  string
		// width is the width of the table, which is the width of the middle
		// grid line and so of the winning border.
		width float64
	}{{
		// Width before style: solid outranks dotted in the style order, and it
		// must not be consulted at all because the dotted border is wider.
		name:  "width is asked before style",
		css:   `#a { border-right: 4px solid red } #b { border-left: 9px dotted blue }`,
		width: 9,
	}, {
		// Style before element: the right cell is later in document order and
		// wins anyway, because double outranks solid and the element rule is
		// only reached on a tie.
		name:  "style is asked before the element",
		css:   `#a { border-right: 5px solid red } #b { border-left: 5px double blue }`,
		width: 5,
	}, {
		// Hidden before width, stated as a width: a hidden 1px border on the
		// left cell suppresses a 30px border on the right one, so the table is
		// as wide as nothing.
		name:  "hidden is asked before width",
		css:   `#a { border-right: 1px hidden red } #b { border-left: 30px solid blue }`,
		width: 0,
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := layoutOf(t, 1000,
				`<table id=t><tr><td id=a></td><td id=b></td></tr></table>`,
				collapsing+tc.css)
			px(t, "the table's width", find(t, root, "t").BorderRect.W, tc.width)
		})
	}
}

// TestBorderConflictReadsEverySource pins that all six kinds of box are
// candidates, which is the part of §17.6.2.1 the separated model never needed:
// a row, a row group, a column and a column group have no borders of their own
// there and are conflict candidates here.
//
// Each case gives exactly one box a border and nothing else has one at all, so
// the table's size is that border and a box that was never read gives zero.
//
// Both axes are checked for every source, and that is not padding. The vertical
// grid lines and the horizontal ones are resolved by two different walks, and a
// box contributes to them by different routes: a column's *left* border is
// decided along a vertical line and its *top* border only at the very top of the
// table. A test on one axis alone leaves half of each walk unexercised — which
// was found by planting a defect that dropped the columns from the horizontal
// walk and watching a left-border-only version of this test pass.
func TestBorderConflictReadsEverySource(t *testing.T) {
	const doc = `<table id=t>` +
		`<colgroup id=cg><col id=c1><col id=c2></colgroup>` +
		`<tbody id=g><tr id=r><td id=a></td><td id=b></td></tr></tbody></table>`

	for _, tc := range []struct{ name, sel string }{
		{"a cell", "#a"},
		{"a row", "#r"},
		{"a row group", "#g"},
		{"a column", "#c1"},
		{"a column group", "#cg"},
		{"the table", "#t"},
	} {
		for _, axis := range []struct {
			name       string
			horizontal bool
		}{
			{"left", true}, {"right", true},
			{"top", false}, {"bottom", false},
		} {
			t.Run(tc.name+" "+axis.name, func(t *testing.T) {
				css := collapsing + tc.sel + " { border-" + axis.name +
					": 7px solid black }"
				root := layoutOf(t, 1000, doc, css)
				table := find(t, root, "t")
				// The line is 7px wide and every column and row of this document
				// is empty, so the whole table measures that one line across.
				got := table.BorderRect.H
				if axis.horizontal {
					got = table.BorderRect.W
				}
				px(t, "the table's extent with a border-"+axis.name+" on "+tc.name,
					got, 7)
			})
		}
	}
}

// ---------------------------------------------------------------------------
// The geometry
// ---------------------------------------------------------------------------

// TestCollapsedTableGeometry is §17.6.2's equation, on the arrangement most of
// the suite's border-conflict references measure.
//
// Four empty cells with no padding and a 20px border each. The five vertical
// grid lines are 20px wide, the four columns are 0 wide, and the table's border
// box reaches from the outside of the first line to the outside of the last:
//
//	20 + 0 + 20 + 0 + 20 + 0 + 20 + 0 + 20 = 100
//
// The table's own used border is *half* the outer line — 10px — and its content
// box is the other 80, which is §17.6.2's "the width of the table includes half
// the width of the table's border" and is the sentence this test exists for.
func TestCollapsedTableGeometry(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table id=t><tr>`+
			`<td id=a></td><td id=b></td><td id=c></td><td id=d></td>`+
			`</tr></table>`,
		collapsing+`td { border: 20px solid green }`)

	table := find(t, root, "t")
	px(t, "the table's border-box width", table.BorderRect.W, 100)
	px(t, "the table's own used border-left", table.Border.Left, 10)
	px(t, "the table's own used border-right", table.Border.Right, 10)
	px(t, "the table's padding-left", table.Padding.Left, 0)

	// A single row of empty cells is 40 tall for the same reason: two horizontal
	// lines of 20 with a row of nothing between them.
	px(t, "the table's border-box height", table.BorderRect.H, 40)

	// Every cell's border box runs from the centre of one grid line to the
	// centre of the next, so they are 20 apart and 20 wide. The first begins 10
	// in from the table's edge, because the outer half of the first line is the
	// table's own border and only the inner half is the cell's.
	cells := cellRects(root)
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4:\n%s", len(cells), sketchFragments(root))
	}
	for i, cell := range cells {
		px(t, "cell "+string(rune('a'+i))+"'s left edge",
			cell.X.Sub(table.BorderRect.X), float64(10+20*i))
		px(t, "cell "+string(rune('a'+i))+"'s border-box width", cell.W, 20)
	}
	// And the cells fill the table's content box exactly, leaving only the two
	// outer half-lines that are the table's own border.
	px(t, "the right edge of the last cell",
		cells[3].Right().Sub(table.BorderRect.X), 90)
	px(t, "the table's content width", table.ContentRect().W, 80)
}

// TestCollapsedBorderIsCentredOnTheGridLine pins the half that decides where
// content sits: a collapsed border is centred on the line, so half of it eats
// into each of the two cells beside it.
//
// One cell 100px wide with a 20px border all round, in the collapsing model. Its
// content box is 100 wide because "width" names the content box, its border box
// is 120 because it owns half of each 20px line, and the table is 140 because
// the lines are 20px and the table owns the other halves. An implementation that
// gave the cell its whole border would make the table 160 — which is exactly
// what the separated model gives, and is the wrong answer this checks against.
func TestCollapsedBorderIsCentredOnTheGridLine(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table id=t><tr><td id=a style="width: 100px"></td></tr></table>`,
		collapsing+`td { border: 20px solid green }`)

	table, cell := find(t, root, "t"), find(t, root, "a")
	px(t, "the table's border-box width", table.BorderRect.W, 140)
	px(t, "the cell's border-box width", cell.BorderRect.W, 120)
	px(t, "the cell's used border-left", cell.Border.Left, 10)
	px(t, "the cell's used border-right", cell.Border.Right, 10)
	px(t, "the cell's content width", cell.ContentRect().W, 100)

	// The same document in the separated model, which is the number this must
	// not produce: the cell keeps its whole 20px border on each side.
	sep := layoutOf(t, 1000,
		`<table id=t><tr><td id=a style="width: 100px"></td></tr></table>`,
		`html, body, p, div { margin: 0; padding: 0 }
		 table { border-collapse: separate; border-spacing: 0 }
		 td { padding: 0; border: 20px solid green }`)
	px(t, "the separated model's table width", find(t, sep, "t").BorderRect.W, 140-0)
}

// TestCollapsedGridLineTakesTheWidestBorderInIt pins that a grid line is as wide
// as the widest border winning anywhere along it, because the columns either
// side of it have to be in the same place in every row.
//
// Two rows, and only the top row's cells have a wide border on the middle line.
// The bottom row's cells have a narrow one. The line is the wide one throughout,
// so the table is as wide as the wide row demands and the narrow border is drawn
// centred in the space.
func TestCollapsedGridLineTakesTheWidestBorderInIt(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table id=t>`+
			`<tr><td id=a></td><td id=b></td></tr>`+
			`<tr><td id=c></td><td id=d></td></tr></table>`,
		collapsing+
			`#a { border-right: 12px solid green } `+
			`#c { border-right: 2px solid green }`)

	// The middle line is 12 wide and the outer ones are nothing, so the table is
	// 12 across whichever row is looked at.
	px(t, "the table's width", find(t, root, "t").BorderRect.W, 12)
	// And the two cells in the lower row are still in the columns the upper row
	// settled: the narrow border does not pull them together.
	cells := cellRects(root)
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4", len(cells))
	}
	if cells[1].X != cells[3].X {
		t.Errorf("the second column starts at %.2f in one row and %.2f in the other; "+
			"a grid line is in the same place in every row",
			cells[1].X.Px(), cells[3].X.Px())
	}
}

// TestCollapsedTableHasNoPadding pins the other sentence of §17.6.2: "in this
// model, a table does not have padding".
//
// It cannot: the grid lines start at the table's border, so a padding would open
// a gap between the table's own half of a line and the cell's half — two halves
// of one border with space between them.
func TestCollapsedTableHasNoPadding(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table id=t style="padding: 30px"><tr><td id=a></td></tr></table>`,
		collapsing+`td { border: 10px solid green }`)
	table := find(t, root, "t")
	px(t, "the table's padding-left", table.Padding.Left, 0)
	// Two 10px lines with a column of nothing between them, and no padding
	// anywhere: 20. With the declared padding honoured it would be 80.
	px(t, "the table's width with a padding declared", table.BorderRect.W, 20)
}

// TestCollapsedSpanningCellSuppressesTheLineItCrosses pins that a rowspan is one
// cell rather than two with a line between them.
//
// The row has a border, so without this rule a rule would be drawn straight
// across the middle of the spanning cell. The assertion is on the ink: the
// horizontal line between the two rows exists over the second column and not
// over the first.
func TestCollapsedSpanningCellSuppressesTheLineItCrosses(t *testing.T) {
	ops := paintOf(t,
		`<table id=t>`+
			`<tr><td id=a rowspan=2>x</td><td>y</td></tr>`+
			`<tr><td>z</td></tr></table>`,
		collapsing+`tr { border-top: 4px solid green; border-bottom: 4px solid green }`)

	// The middle horizontal line: 4px tall, and it must not reach over the first
	// column. Its y is not asserted — where it is is the geometry's business —
	// only that no 4px-tall band starts at the table's left edge and is as wide
	// as the whole table.
	var full, partial int
	root := layoutOf(t, 1000,
		`<table id=t>`+
			`<tr><td id=a rowspan=2>x</td><td>y</td></tr>`+
			`<tr><td>z</td></tr></table>`,
		collapsing+`tr { border-top: 4px solid green; border-bottom: 4px solid green }`)
	table := find(t, root, "t")
	for _, op := range ops {
		f, ok := op.(FillRect)
		if !ok || f.Rect.Empty() || f.Rect.H != mustPx(4) {
			continue
		}
		if f.Rect.Y <= table.BorderRect.Y || f.Rect.Bottom() >= table.BorderRect.Bottom() {
			// The lines at the very top and bottom of the table, which do span
			// the whole width.
			continue
		}
		if f.Rect.W >= table.BorderRect.W {
			full++
		} else {
			partial++
		}
	}
	if full != 0 {
		t.Errorf("the line between the rows runs the whole width of the table %d times; "+
			"the cell spanning both rows has no line through it", full)
	}
	if partial == 0 {
		t.Error("no line was drawn between the rows at all, over either column")
	}
}

// TestEmptyCellsDoesNotApplyWhenBordersCollapse pins §17.6.1.1's scope.
//
// "empty-cells" hides an empty cell's background and border, and it is a rule of
// the separated model. In the collapsing model an empty cell owns half of four
// grid lines whose other halves belong to its neighbours, so hiding it would
// take away half of somebody else's border.
func TestEmptyCellsDoesNotApplyWhenBordersCollapse(t *testing.T) {
	count := func(src string) int {
		ops := paintOf(t,
			`<table id=t style="empty-cells: hide"><tr>`+
				`<td id=a></td><td id=b>x</td></tr></table>`,
			// A width, so that the empty cell has an area to paint: a cell with
			// nothing in it and nothing to make it wide paints nothing whatever
			// empty-cells says, and the test would pass on that instead.
			src+`td { background-color: #ff0000; width: 20px; height: 20px }`)
		n := 0
		for _, op := range ops {
			if f, ok := op.(FillRect); ok && !f.Rect.Empty() &&
				f.Color == (style.RGBA{R: 255, A: 1}) {
				n++
			}
		}
		return n
	}
	separated := `html, body, p, div { margin: 0; padding: 0 }
		table { border-collapse: separate; border-spacing: 0 }
		td { padding: 0 }
		`
	if got := count(separated); got != 1 {
		t.Errorf("the separated model painted %d cell backgrounds, want 1: "+
			"empty-cells: hide leaves the empty one out", got)
	}
	if got := count(collapsing); got != 2 {
		t.Errorf("the collapsing model painted %d cell backgrounds, want 2: "+
			"empty-cells does not apply when borders collapse", got)
	}
}

// TestCollapsedBordersArePaintedByTheTable pins §17.6.2's painting rule.
//
// A collapsed border belongs half to each of the two cells beside it, so neither
// of them can draw it — and it has to be drawn after every background in the
// table, because it runs under the edge of a row and of two cells. The document
// gives the row a background that covers the middle grid line; the border must
// still be visible.
func TestCollapsedBordersArePaintedByTheTable(t *testing.T) {
	ops := paintOf(t,
		`<table id=t><tr id=r><td id=a>x</td><td id=b>y</td></tr></table>`,
		collapsing+
			`#r { background-color: #ffff00 }`+
			`td { border: 10px solid #008000 }`)

	green := style.RGBA{R: 0, G: 128, B: 0, A: 1}
	yellow := style.RGBA{R: 255, G: 255, A: 1}
	lastYellow, lastGreen := -1, -1
	for i, op := range ops {
		f, ok := op.(FillRect)
		if !ok || f.Rect.Empty() {
			continue
		}
		switch f.Color {
		case yellow:
			lastYellow = i
		case green:
			lastGreen = i
		}
	}
	if lastGreen < 0 {
		t.Fatal("no collapsed border was drawn at all")
	}
	if lastYellow < 0 {
		t.Fatal("the row's background was not drawn; this test needs it")
	}
	if lastGreen < lastYellow {
		t.Errorf("the last collapsed border is op %d and the row's background is op %d; "+
			"a background painted after a grid line rubs it out", lastGreen, lastYellow)
	}
}

// TestCollapsedTableDoesNotPaintItsOwnBorderTwice pins that the table's declared
// border goes through the conflict resolution like every other candidate.
//
// The table declares a wide red border and the cells a narrower green one on the
// same edges. The cells lose — the table's is wider — so the drawn border is the
// table's, once. What must not happen is the table drawing its own border
// through the ordinary path *as well*, which would put the loser on the page
// wherever a cell won.
func TestCollapsedTableDoesNotPaintItsOwnBorderTwice(t *testing.T) {
	ops := paintOf(t,
		`<table id=t><tr><td id=a>x</td></tr></table>`,
		collapsing+
			`#t { border: 4px solid #ff0000 }`+
			`td { border: 12px solid #008000 }`)

	var red, green int
	for _, op := range ops {
		f, ok := op.(FillRect)
		if !ok || f.Rect.Empty() {
			continue
		}
		switch f.Color {
		case style.RGBA{R: 255, A: 1}:
			red++
		case style.RGBA{G: 128, A: 1}:
			green++
		}
	}
	if green == 0 {
		t.Error("the cell's 12px border lost to the table's 4px one")
	}
	if red != 0 {
		t.Errorf("the table's own border was drawn %d times; it lost every edge "+
			"to the cell's wider one", red)
	}
}

// ---------------------------------------------------------------------------
// The bounds
// ---------------------------------------------------------------------------

// TestCollapsedRunCapFires watches the bound on how much grid line this engine
// will resolve into drawable stretches.
//
// The cap is lowered rather than the document made enormous, for the reason
// maxTableColumns gives: a bound that has only ever been observed not to trip is
// one nobody knows works. What is checked is that it is reported — a table
// missing half its rules and saying nothing is precisely the silent truncation
// the limit rule exists to prevent.
func TestCollapsedRunCapFires(t *testing.T) {
	fired[RuleLimit] = true

	saved := maxCollapsedRuns
	maxCollapsedRuns = 4
	defer func() { maxCollapsedRuns = saved }()

	var rows strings.Builder
	for i := 0; i < 8; i++ {
		rows.WriteString(`<tr><td>a</td><td>b</td><td>c</td></tr>`)
	}
	rec := NewRecorder(nil)
	built := Build(Input{HTML: `<table>` + rows.String() + `</table>`,
		CSS: []Stylesheet{{Source: collapsing + `td { border: 1px solid black }`}}})
	w, _ := style.FromPx(1000)
	Layout(built.Root, Size{W: w, H: w}, nil, rec)

	if rec.Count(RuleLimit) == 0 {
		t.Error("the run cap dropped grid lines and said nothing")
	}
}

// TestCollapsedGridIsLinearInTheCells is the security test.
//
// A grid is rows × columns *slots*, and a document is untrusted: a table with
// many rows and a cell spanning to the column cap has a grid of hundreds of
// millions of slots and is a few kilobytes of markup. Nothing in the collapsing
// model may be proportional to that. The assertion is on the number of drawable
// stretches, which is what would grow per slot if the resolution were done one
// square at a time — a document like this has a handful of them.
func TestCollapsedGridIsLinearInTheCells(t *testing.T) {
	var rows strings.Builder
	// One cell spanning the column cap, then two thousand rows of nothing. The
	// grid is 1000 × 2001 slots; the borders in it are the four sides of one
	// cell and the four sides of the table.
	rows.WriteString(`<tr><td colspan=1000></td></tr>`)
	for i := 0; i < 2000; i++ {
		rows.WriteString(`<tr></tr>`)
	}
	const doc, css = `<table id=t>`, `td { border: 3px solid black }`
	root := layoutOf(t, 1000, doc+rows.String()+`</table>`, collapsing+css)

	table := find(t, root, "t")
	if n := len(table.collapsed); n > 64 {
		t.Errorf("a grid of two million slots produced %d drawable stretches; "+
			"the resolution is meant to be per cell edge and not per slot", n)
	}
	// And the *work* is bounded too, which the count of drawable stretches
	// cannot see: adjacent stretches with the same winner are merged, so an
	// implementation that asked the conflict resolution about every one of the
	// two million squares would draw exactly the same page. This is the only
	// witness there is that it did not.
	cg := collapsedGridOf(t, doc+rows.String()+`</table>`, collapsing+css)
	if cg.resolved > 4*(2001+1000) {
		t.Errorf("the conflict resolution was asked about %d stretches of a grid "+
			"with 2001 rows and 1000 columns; it is meant to be asked once per "+
			"cell edge and once per line, not once per slot", cg.resolved)
	}
	// And it is still the right answer: a 3px line down each side of the one
	// cell, with the thousand columns it spans holding nothing between them.
	px(t, "the table's width", table.BorderRect.W, 6)
}

// collapsedGridOf resolves a document's first table's grid lines, so that a test
// can look at what the resolution cost as well as at what it drew.
func collapsedGridOf(t *testing.T, htmlSrc, cssSrc string) *collapsedGrid {
	t.Helper()
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: cssSrc}}})
	table := findBoxWhere(t, built.Root, func(b *Box) bool { return b.Inner == InnerTable })
	w, _ := style.FromPx(1000)
	l := &layouter{
		rec: NewRecorder(nil), avail: Size{W: w, H: w},
		lengths: map[lengthKey]style.Length{},
		grids:   map[*Box]*tableGrid{},
	}
	return l.collapsedGridFor(table)
}
