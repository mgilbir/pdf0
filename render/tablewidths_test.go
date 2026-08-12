package render

import (
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// The arithmetic of §17.5.2's column widths, tested against the functions
// themselves.
//
// It is here rather than left to the reftests because the reftests do not reach
// it. Breaking the grid's column cursor so that every colspan puts two cells in
// one slot leaves the suite byte for byte identical — 4274 clean passes either
// way — so whatever table layout the corpus exercises, it is not this. Of
// thirty-six mutations planted over tablelayout.go only ten were caught before
// this file, and none of the ones below.
//
// These functions are pure, or near enough, so they are called directly. That
// is deliberate: an arithmetic rule stated through a document is stated at two
// removes, and the fixture then has to be reverse-engineered every time the
// rule is edited.

// u is a whole number of pixels as the layout engine holds it.
func u(px float64) style.Unit {
	v, _ := style.FromPx(px)
	return v
}

// units renders a slice for a failure message.
func units(s []style.Unit) []float64 {
	out := make([]float64, len(s))
	for i, v := range s {
		out[i] = v.Px()
	}
	return out
}

// TestDistributeGivesOutExactlyTheWhole is the property the running total is
// there for: the parts add up to the total, whatever the weights and however
// the fractions fall.
//
// Rounding each share on its own is the natural way to write this and it is
// wrong by a unit or two, which shows as a hairline down the right edge of a
// table. The cases below are chosen so that the exact shares are not whole
// numbers — thirds, sevenths — because a total that divides evenly cannot tell
// the two apart.
func TestDistributeGivesOutExactlyTheWhole(t *testing.T) {
	cases := []struct {
		name    string
		total   float64
		weights []float64
	}{
		{"three equal columns of a hundred", 100, []float64{1, 1, 1}},
		{"seven columns", 100, []float64{1, 1, 1, 1, 1, 1, 1}},
		{"lopsided", 100, []float64{1, 2, 97}},
		{"one column takes it all", 100, []float64{5}},
		{"a zero weight among the rest", 100, []float64{0, 1, 1}},
		{"every weight zero, shared equally", 100, []float64{0, 0, 0}},
		{"negative weights are not room", 100, []float64{-5, 1, 1}},
		{"a total that divides badly", 1000, []float64{3, 3, 3}},
		{"nothing to give", 0, []float64{1, 2, 3}},
	}
	for _, c := range cases {
		out := make([]style.Unit, len(c.weights))
		distribute(u(c.total), c.weights, out)
		var sum style.Unit
		for _, v := range out {
			sum = sum.Add(v)
		}
		if sum != u(c.total) {
			t.Errorf("%s: the parts %v add up to %g, want %g — the shares were "+
				"rounded on their own instead of off a running total",
				c.name, units(out), sum.Px(), c.total)
		}
		for i, v := range out {
			if v < 0 {
				t.Errorf("%s: part %d is %g, and no column may be given a negative "+
					"share", c.name, i, v.Px())
			}
		}
	}
}

// TestDistributeIsProportionalToTheWeights is the other half: adding up is not
// enough, the shares have to be the ones asked for.
func TestDistributeIsProportionalToTheWeights(t *testing.T) {
	out := make([]style.Unit, 3)
	distribute(u(90), []float64{1, 2, 3}, out)
	for i, want := range []float64{15, 30, 45} {
		if out[i].Px() != want {
			t.Errorf("part %d is %g, want %g (all %v)", i, out[i].Px(), want, units(out))
		}
	}

	// A weightless set is shared equally rather than landing on the first
	// column, which is what a sum of zero would otherwise do.
	equal := make([]style.Unit, 4)
	distribute(u(100), []float64{0, 0, 0, 0}, equal)
	for i, v := range equal {
		if v.Px() != 25 {
			t.Errorf("with no weights part %d is %g, want 25 (all %v)",
				i, v.Px(), units(equal))
		}
	}

	// A negative weight is not a claim on anything. It must neither take a share
	// nor shrink the pool the others divide.
	neg := make([]style.Unit, 3)
	distribute(u(100), []float64{-100, 1, 1}, neg)
	if neg[0] != 0 {
		t.Errorf("a negative weight took %g, want 0", neg[0].Px())
	}
	if neg[1].Px() != 50 || neg[2].Px() != 50 {
		t.Errorf("the real weights got %g and %g, want 50 each — the negative one "+
			"was counted in the sum they divide", neg[1].Px(), neg[2].Px())
	}
}

// TestDistributeAddsToWhatIsAlreadyThere pins that out is accumulated into and
// not overwritten. autoColumnWidths fills it with the column minima first and
// then hands it here to grow, so a version that assigned would drop every
// minimum on the floor.
func TestDistributeAddsToWhatIsAlreadyThere(t *testing.T) {
	out := []style.Unit{u(10), u(20)}
	distribute(u(30), []float64{1, 1}, out)
	if out[0].Px() != 25 || out[1].Px() != 35 {
		t.Errorf("got %v, want [25 35] — the shares replaced what was there "+
			"instead of adding to it", units(out))
	}
}

// TestSpreadDemandOnlyWidensWhatIsTooNarrow is a spanning cell handing its
// demand to the columns it covers.
func TestSpreadDemandOnlyWidensWhatIsTooNarrow(t *testing.T) {
	// Two columns holding 50 between them and a cell that needs 100. The share
	// is weighted by what each column *can* hold, not by what it must: the two
	// orderings disagree here on purpose, since a column with the larger minimum
	// and the smaller maximum is the only fixture that can tell them apart.
	cols := []tableColumnDemand{
		{min: u(40), max: u(50)},
		{min: u(10), max: u(450)},
	}
	spreadDemand(cols, u(100), u(500))
	var min style.Unit
	for _, c := range cols {
		min = min.Add(c.min)
	}
	if min != u(100) {
		t.Errorf("the minima add up to %g, want 100", min.Px())
	}
	// Fifty to share over maxima of 50 and 450: five and forty-five.
	if cols[0].min != u(45) || cols[1].min != u(55) {
		t.Errorf("the columns took %g and %g of the minimum, want 45 and 55 — "+
			"the spread was weighted by the minima instead of the maxima",
			cols[0].min.Px(), cols[1].min.Px())
	}

	// A demand the columns already meet changes nothing at all.
	same := []tableColumnDemand{{min: u(50), max: u(60)}, {min: u(50), max: u(60)}}
	before := append([]tableColumnDemand(nil), same...)
	spreadDemand(same, u(100), u(120))
	for i := range same {
		if same[i] != before[i] {
			t.Errorf("column %d moved from %v to %v for a demand it already met",
				i, before[i], same[i])
		}
	}
}

// TestPercentageColumnsTakeOnlyTheSlack is §17.5.2's percentage claim.
//
// A percentage is a claim on the table's final width, settled after the rest of
// the algorithm has run. What it may take is what the other columns hold above
// their own minima, and no more: a column pushed below its minimum overflows
// its content, which is the one thing the auto algorithm promises not to do.
func TestPercentageColumnsTakeOnlyTheSlack(t *testing.T) {
	var l layouter
	// Two columns in 200px. The first asks for half; the second has content
	// needing 150 and currently holds all of its 150.
	demands := []tableColumnDemand{
		{min: u(10), max: u(10), percent: 50},
		{min: u(150), max: u(150)},
	}
	out := []style.Unit{u(50), u(150)}
	l.applyPercentages(demands, out, u(200))

	if out[1] < u(150) {
		t.Errorf("the content column was cut to %g, below the 150 its content "+
			"needs — a percentage took more than the slack", out[1].Px())
	}
	if total := out[0].Add(out[1]); total != u(200) {
		t.Errorf("the columns add up to %g, want the 200 they started with "+
			"(%v)", total.Px(), units(out))
	}

	// With slack to spare the claim is met exactly.
	demands2 := []tableColumnDemand{
		{min: u(10), max: u(10), percent: 50},
		{min: u(10), max: u(190)},
	}
	out2 := []style.Unit{u(10), u(190)}
	l.applyPercentages(demands2, out2, u(200))
	if out2[0] != u(100) {
		t.Errorf("the 50%% column got %g of 200, want 100 (all %v)",
			out2[0].Px(), units(out2))
	}
	if total := out2[0].Add(out2[1]); total != u(200) {
		t.Errorf("the columns add up to %g, want 200 (%v)", total.Px(), units(out2))
	}

	// A column already wider than its percentage keeps what it has: the claim is
	// a floor, not a target to be cut back to.
	demands3 := []tableColumnDemand{
		{min: u(10), max: u(150), percent: 10},
		{min: u(10), max: u(50)},
	}
	out3 := []style.Unit{u(150), u(50)}
	l.applyPercentages(demands3, out3, u(200))
	if out3[0] != u(150) {
		t.Errorf("a column holding 150 with a 10%% claim became %g; the claim is "+
			"a minimum and should have done nothing", out3[0].Px())
	}
}

// TestPercentagesShareAShortfall is what happens when the claims add up to more
// than there is. Each claimant gets a part of what is available rather than the
// first ones getting all of theirs and the last getting nothing.
func TestPercentagesShareAShortfall(t *testing.T) {
	var l layouter
	// Both columns ask for 60% of 200 — 120 each — and there is 100 of slack.
	demands := []tableColumnDemand{
		{min: u(10), max: u(10), percent: 60},
		{min: u(10), max: u(10), percent: 60},
		{min: u(0), max: u(180)},
	}
	out := []style.Unit{u(10), u(10), u(180)}
	l.applyPercentages(demands, out, u(200))
	if out[0] != out[1] {
		t.Errorf("two equal claims were met with %g and %g; a shortfall is shared",
			out[0].Px(), out[1].Px())
	}
	if out[0] <= u(10) {
		t.Errorf("neither claim was met at all (%v)", units(out))
	}
	if total := out[0].Add(out[1]).Add(out[2]); total != u(200) {
		t.Errorf("the columns add up to %g, want 200 (%v)", total.Px(), units(out))
	}

	// Unequal claims share it unequally. Two columns wanting 160 and 80 of a
	// 200px table, with 180 of slack to divide: the larger claim takes the
	// larger part of the shortfall, rather than the two splitting it down the
	// middle.
	demands2 := []tableColumnDemand{
		{min: u(10), max: u(10), percent: 80},
		{min: u(10), max: u(10), percent: 40},
		{min: u(0), max: u(180)},
	}
	out2 := []style.Unit{u(10), u(10), u(180)}
	l.applyPercentages(demands2, out2, u(200))
	if out2[0] <= out2[1] {
		t.Errorf("claims of 80%% and 40%% were met with %g and %g; the shortfall "+
			"is shared in proportion to what each asked for, not equally",
			out2[0].Px(), out2[1].Px())
	}
	if total := out2[0].Add(out2[1]).Add(out2[2]); total != u(200) {
		t.Errorf("the columns add up to %g, want 200 (%v)", total.Px(), units(out2))
	}
}

// TestAPercentageColumnIsNotRaidedForAnother is which columns pay for a claim.
//
// The slack a percentage takes comes from the columns that made no claim. A
// column that already holds more than its own percentage is not a donor: taking
// from it to satisfy a second claim would leave the first one short of the width
// the author asked for, and the two would trade the space back and forth.
func TestAPercentageColumnIsNotRaidedForAnother(t *testing.T) {
	var l layouter
	demands := []tableColumnDemand{
		{min: u(10), max: u(150), percent: 50},
		{min: u(10), max: u(10), percent: 20},
		{min: u(10), max: u(40)},
	}
	out := []style.Unit{u(150), u(10), u(40)}
	l.applyPercentages(demands, out, u(200))
	if out[0] != u(150) {
		t.Errorf("the column holding 150 with a claim of its own was cut to %g "+
			"to pay for another claim (all %v)", out[0].Px(), units(out))
	}
	if out[1] != u(40) {
		t.Errorf("the 20%% column got %g of 200, want 40 (all %v)",
			out[1].Px(), units(out))
	}
	if total := out[0].Add(out[1]).Add(out[2]); total != u(200) {
		t.Errorf("the columns add up to %g, want 200 (%v)", total.Px(), units(out))
	}
}

// TestACellSitsOnItsRowsBaseline is §17.5.3's baseline alignment, which is what
// makes a row of cells with different font sizes read as one line of text.
func TestACellSitsOnItsRowsBaseline(t *testing.T) {
	cell := func(align string, natural, baseline float64) placedCell {
		return placedCell{align: align, natural: u(natural), baseline: u(baseline)}
	}
	// A cell whose own baseline is above the row's is pushed down to meet it,
	// and is that much taller for it.
	if got := cell("baseline", 20, 10).stretchedHeight(u(30)); got != u(40) {
		t.Errorf("a cell of 20 with its baseline at 10 in a row whose baseline is "+
			"30 is %g tall, want 40", got.Px())
	}
	// One already at or below the row's baseline is not pulled up.
	if got := cell("baseline", 20, 30).stretchedHeight(u(10)); got != u(20) {
		t.Errorf("a cell below the row's baseline became %g tall, want its "+
			"natural 20", got.Px())
	}
	// The three alignments that are not the baseline ignore it entirely.
	for _, align := range []string{"top", "bottom", "middle"} {
		if got := cell(align, 20, 10).stretchedHeight(u(30)); got != u(20) {
			t.Errorf("a %s-aligned cell is %g tall, want its natural 20 — it was "+
				"stretched to a baseline it does not sit on", align, got.Px())
		}
	}
	// §17.5.3 names four alignments and everything else is the baseline, so
	// "super" on a cell is not a small lift, it is nothing at all.
	for _, align := range []string{"", "baseline", "super", "sub", "text-top"} {
		if !isBaselineAligned(align) {
			t.Errorf("vertical-align:%q on a cell is not baseline aligned, and "+
				"§17.5.3 makes everything outside its four values the baseline", align)
		}
	}
	for _, align := range []string{"top", "bottom", "middle"} {
		if isBaselineAligned(align) {
			t.Errorf("vertical-align:%q on a cell was treated as the baseline", align)
		}
	}
}

// The grid §17.5 builds before any width is decided: which slot each cell
// lands in once colspan and rowspan have reserved the ones around it.
//
// None of this was covered. Breaking the column cursor so that a colspan
// advances it by one instead of by the span leaves the reftests byte for byte
// identical, and so does releasing a rowspan's slot a row early.

// TestAColspanAdvancesTheGridCursorByItsSpan is the cursor itself. A cell after
// a spanning one begins past everything it covers, not one column along.
func TestAColspanAdvancesTheGridCursorByItsSpan(t *testing.T) {
	// Three columns of 100 in a fixed 300px table, so a column's index is its
	// left edge divided by a hundred and there is nothing to work out.
	root := layoutOf(t, 1000,
		`<table style="width: 300px; table-layout: fixed">`+
			`<tr><td colspan=2>a</td><td>b</td></tr>`+
			`<tr><td>c</td><td>d</td><td>e</td></tr></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 5 {
		t.Fatalf("got %d cells, want 5:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the spanning cell's left", cells[0].X, 0)
	px(t, "the spanning cell's width", cells[0].W, 200)
	px(t, "the cell after it begins past both columns", cells[1].X, 200)
	for i, want := range []float64{0, 100, 200} {
		px(t, "the second row's cell", cells[2+i].X, want)
	}
}

// TestARowspanKeepsTheSlotBelowItOccupied is the other half of the grid: the
// row underneath has to step over the column a spanning cell is still in.
func TestARowspanKeepsTheSlotBelowItOccupied(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table style="width: 200px; table-layout: fixed">`+
			`<tr><td rowspan=2>a</td><td>b</td></tr>`+
			`<tr><td id=c>c</td></tr></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the spanning cell's left", cells[0].X, 0)
	px(t, "the cell beside it", cells[1].X, 100)
	// The only cell of the second row cannot use column zero: the cell above is
	// still in it.
	px(t, "the second row's only cell", cells[2].X, 100)
}

// TestARowspanOfZeroReachesTheEndOfItsRowGroup is HTML's one meaningful zero.
//
// Everywhere else a zero span is invalid and becomes one; on rowspan it means
// "to the bottom of this row group", and reading it as one leaves the two rows
// below free to use a column that is occupied.
func TestARowspanOfZeroReachesTheEndOfItsRowGroup(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table style="width: 200px; table-layout: fixed"><tbody>`+
			`<tr><td rowspan=0>a</td><td>b</td></tr>`+
			`<tr><td>c</td></tr>`+
			`<tr><td>d</td></tr></tbody></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4:\n%s", len(cells), sketchFragments(root))
	}
	for i, name := range []string{"the second row's cell", "the third row's cell"} {
		px(t, name, cells[2+i].X, 100)
	}
}

// TestAnInvalidSpanIsOne pins the clamping HTML asks for at the small end. The
// large end is TestTableSpanClamping's, and is a different worry: this one is
// about a span that would put two cells in one slot or walk the cursor
// backwards.
func TestAnInvalidSpanIsOne(t *testing.T) {
	for _, span := range []string{"0", "-3", "notanumber", ""} {
		root := layoutOf(t, 1000,
			`<table style="width: 200px; table-layout: fixed">`+
				`<tr><td colspan="`+span+`">a</td><td>b</td></tr></table>`, bareTable)
		cells := cellRects(root)
		if len(cells) != 2 {
			t.Fatalf("colspan=%q: got %d cells, want 2", span, len(cells))
		}
		if cells[0].X != 0 || cells[1].X.Px() != 100 {
			t.Errorf("colspan=%q put the cells at %g and %g, want 0 and 100 — an "+
				"invalid span is one column", span, cells[0].X.Px(), cells[1].X.Px())
		}
	}
}

// TestASpanningCellTallerThanItsRowsGrowsTheLastOne is §17.5.3's other
// direction, and the case TestTableRowHeightsAndSpans does not reach: there the
// spanning cell is shorter than the rows it covers, so nothing has to give.
//
// The height it still needs goes on the *last* row it covers, because the rows
// above it have already been settled against their own contents and moving them
// would drag everything beside them down too.
func TestASpanningCellTallerThanItsRowsGrowsTheLastOne(t *testing.T) {
	root := layoutOf(t, 1000, `<table style="border-spacing: 10px">`+
		`<tr><td rowspan=2 style="height: 100px">s</td>`+
		`<td style="height: 20px">a</td></tr>`+
		`<tr><td style="height: 20px">b</td></tr></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	// The two rows hold 20 and 20 with 10 of spacing between them: 50 against
	// the 100 the spanning cell needs, so the second row takes the other 50.
	px(t, "the first row's height", cells[1].H, 20)
	px(t, "the second row's height", cells[2].H, 70)
	px(t, "the spanning cell's height", cells[0].H, 100)
	px(t, "the gap between the rows", cells[2].Y.Sub(cells[1].Bottom()), 10)
}

// TestSurplusWidthIsSharedInProportion is the third branch of §17.5.2.2: a table
// wider than anything its content wants.
//
// The specification does not say where the surplus goes. In proportion to what
// each column already holds is what browsers do, and it is what keeps the
// relative widths the content asked for — sharing it equally instead makes a
// wide table's narrow column creep towards the width of its wide one.
func TestSurplusWidthIsSharedInProportion(t *testing.T) {
	// Courier at 20px is 12px a character: the columns want 24 and 96, so the
	// content is 120 wide in a table declared at 240. The 120 of surplus is
	// shared 1:4, giving 48 and 192.
	root := layoutOf(t, 1000,
		`<table style="width: 240px"><tr><td>ab</td><td>abcdefgh</td></tr></table>`,
		bareTable+`table, td { font-family: Courier; font-size: 20px }`)
	cells := cellRects(root)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the narrow column", cells[0].W, 48)
	px(t, "the wide column", cells[1].W, 192)
}

// TestADefiniteTableHeightCountsTheSpacingAroundEveryRow is the outer half of
// §17.6.1's spacing.
//
// border-spacing is drawn above the first row and below the last as well as
// between them, so n rows carry n+1 gaps. Counting only the gaps between rows
// hands the rows two spacings' worth of height that is not theirs, and the table
// then overflows the height it was given by exactly that.
func TestADefiniteTableHeightCountsTheSpacingAroundEveryRow(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table style="height: 200px; border-spacing: 10px">`+
			`<tr><td>a</td></tr><tr><td>b</td></tr></table>`,
		bareTable+`table, td { font-family: Courier; font-size: 20px }`)
	cells := cellRects(root)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2:\n%s", len(cells), sketchFragments(root))
	}
	// Two rows and three gaps of ten in two hundred: eighty-five each.
	px(t, "the first row's height", cells[0].H, 85)
	px(t, "the second row's height", cells[1].H, 85)
	px(t, "the first row's top", cells[0].Y, 10)
	// And the whole of it adds up to the height that was asked for.
	if bottom := cells[1].Bottom().Add(u(10)); bottom != u(200) {
		t.Errorf("the rows and their spacing come to %g, want the 200 the table "+
			"was given", bottom.Px())
	}
}
