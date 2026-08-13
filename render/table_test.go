package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Tables, CSS 2.1 §17.
//
// The box-tree half of these run whole documents through parse, style and box
// construction, for the reason box_test.go gives: the faults §17.2.1 has are
// about what the *cascade* produced, and a hand-built tree assumes away the step
// that goes wrong.

// TestTableWrapperBox pins §17.4: a table is two boxes, and which properties
// each of them gets.
func TestTableWrapperBox(t *testing.T) {
	got := bodyBoxes(t, `<table><tr><td>a</td></tr></table>`)
	want := `anonymous block/flow-root
  table block/table
    tr block/table-row
      td block/table-cell
        text "a"
`
	if got != want {
		t.Errorf("a table\n%s\nwant\n%s", got, want)
	}
}

// TestTableWrapperTakesThePositioningProperties pins the division §17.4 draws.
//
// It is the half of the wrapper that is easy to get wrong and invisible when it
// is: the margins have to move, because a wrapper that did not take them would
// leave the table indented inside a wrapper that was also indented, and the
// border has to stay, because a wrapper that took it would draw the table's
// border around the caption as well.
func TestTableWrapperTakesThePositioningProperties(t *testing.T) {
	got := build(t, `<table id=t style="margin-left: 30px; border: 2px solid black; float: left">`+
		`<tr><td>a</td></tr></table>`)
	table := findBoxWhere(t, got.Root, func(b *Box) bool { return b.Inner == InnerTable })
	wrapper := table.Parent
	if wrapper == nil || !wrapper.TableWrapper {
		t.Fatalf("the table's parent is not a wrapper:\n%s", sketchBox(got.Root))
	}

	if wrapper.Style["margin-left"] != "30px" {
		t.Errorf("the wrapper's margin-left is %q, want 30px", wrapper.Style["margin-left"])
	}
	if wrapper.Style["border-left-style"] != "none" {
		t.Errorf("the wrapper took the border (%q); §17.4 leaves it on the table",
			wrapper.Style["border-left-style"])
	}
	if table.Style["border-left-style"] != "solid" {
		t.Errorf("the table box lost its border (%q)", table.Style["border-left-style"])
	}
	if wrapper.Float != FloatLeft {
		t.Errorf("the wrapper does not float (%v); the float belongs to it", wrapper.Float)
	}
	if table.Float != FloatNone {
		t.Errorf("the table box still floats (%v), so the declaration would apply twice",
			table.Float)
	}
}

// TestTableAnonymousObjects is §17.2.1, one document per rule.
func TestTableAnonymousObjects(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{{
		// The rule that matters most in practice. Every hand-written table has
		// white space between its tags, and a cell per newline is the loudest
		// failure this stage can have.
		name: "white space between table structure is dropped",
		html: "<table>\n <tr>\n  <td>a</td>\n  <td>b</td>\n </tr>\n</table>",
		want: `anonymous block/flow-root
  table block/table
    tr block/table-row
      td block/table-cell
        text "a"
      td block/table-cell
        text "b"
`,
	}, {
		// A cell on its own grows both the boxes it is missing.
		name: "a stray cell grows a row and a table",
		html: `<div style="display: table-cell">a</div>`,
		want: `anonymous block/flow-root
  anonymous block/table
    anonymous block/table-row
      div block/table-cell
        text "a"
`,
	}, {
		// One row for the whole run, not one per box: the ordering of §17.2.1's
		// three groups is what decides this, and getting it wrong puts the two
		// on separate lines of the table.
		name: "a run of strays shares one anonymous row",
		html: `<table><div>d</div><td>c</td></table>`,
		want: `anonymous block/flow-root
  table block/table
    anonymous block/table-row
      anonymous block/table-cell
        div block
          text "d"
      td block/table-cell
        text "c"
`,
	}, {
		// Consecutive rows in a <div> are one table, not two. The white space
		// between them is what would otherwise break the run.
		name: "consecutive rows outside a table share one anonymous table",
		html: "<div><div style='display:table-row'>a</div>\n" +
			"<div style='display:table-row'>b</div></div>",
		want: `div block
  anonymous block/flow-root
    anonymous block/table
      div block/table-row
        anonymous block/table-cell
          text "a"
      div block/table-row
        anonymous block/table-cell
          text "b"
`,
	}, {
		// A column describes a column and holds nothing.
		name: "the content of a column is dropped",
		html: `<div style="display: table-column">gone</div>`,
		want: `anonymous block/flow-root
  anonymous block/table
    div block/table-column
`,
	}, {
		// And a column group holds only columns.
		name: "a non-column child of a column group is dropped",
		html: `<div style="display: table-column-group"><span>gone</span>` +
			`<i style="display: table-column"></i></div>`,
		want: `anonymous block/flow-root
  anonymous block/table
    div block/table-column-group
      i block/table-column
`,
	}, {
		// The anonymous cell is a block container, so the anonymous *block* rule
		// has to reach inside it — which it only does if the cell finishes its
		// own children after being generated.
		name: "an anonymous cell wraps its own mixed content",
		html: `<div style="display: table-row">loose<p>block</p></div>`,
		want: `anonymous block/flow-root
  anonymous block/table
    div block/table-row
      anonymous block/table-cell
        anonymous block
          text "loose"
        p block
          text "block"
`,
	}, {
		// A generated box needs the repair applied to it as well. A row group
		// inside a row is wrapped in an anonymous cell, and inside that cell it
		// is still a row group with no table — which is a box with a background
		// and no area. Found by a test that expected a blank page and got a red
		// square.
		name: "a generated cell repairs its own children",
		html: `<div style="display: table-row"><div style="display: table-row-group"></div></div>`,
		want: `anonymous block/flow-root
  anonymous block/table
    div block/table-row
      anonymous block/table-cell
        anonymous block/flow-root
          anonymous block/table
            div block/table-row-group
`,
	}, {
		// A stray cell inside a sentence stays in the sentence. The table that
		// grows around it is an inline-table, and the span is not split.
		name: "a stray cell inside an inline grows an inline table",
		html: `<span>x<i style="display: table-cell">c</i>y</span>`,
		want: `span inline
  text "x"
  anonymous inline/flow-root
    anonymous block/table
      anonymous block/table-row
        i block/table-cell
          text "c"
  text "y"
`,
	}, {
		// §17.2.1 throws white space away for what is *around* it, and an
		// engine that looked only at the parent got this one backwards. The
		// space before <span>bc</span> has table structure on one side and an
		// inline box on the other, so the rule does not reach it — and the
		// missing-child rule then sweeps space, span and space into a single
		// anonymous cell, which is what makes the row read "a bc d" rather than
		// "abcd" with the words run together.
		name: "white space beside an inline is kept and joins its cell",
		html: `<span style="display:table-row">` +
			`<span style="display:table-cell">a</span> <span>bc</span> ` +
			`<span style="display:table-cell">d</span></span>`,
		want: `anonymous block/flow-root
  anonymous block/table
    span block/table-row
      span block/table-cell
        text "a"
      anonymous block/table-cell
        text " "
        span inline
          text "bc"
        text " "
      span block/table-cell
        text "d"
`,
	}, {
		// The other side of the same rule, and the reason the neighbours are
		// checked rather than assumed: white space with structure on both sides
		// still goes, which is every hand-written table in existence.
		name: "white space between two cells is still dropped",
		html: `<span style="display:table-row">` +
			`<span style="display:table-cell">a</span> ` +
			`<span style="display:table-cell">d</span></span>`,
		want: `anonymous block/flow-root
  anonymous block/table
    span block/table-row
      span block/table-cell
        text "a"
      span block/table-cell
        text "d"
`,
	}, {
		// The one place a missing neighbour is no objection. §17.2.1 qualifies
		// the container rule with "if any", so a row holding nothing but the
		// newline between its own tags is a row with no cells — not a row with
		// one empty one, which is what every browser produces too.
		name: "white space alone in a row generates no cell",
		html: "<table><tr>\n</tr></table>",
		want: `anonymous block/flow-root
  table block/table
    tr block/table-row
`,
	}, {
		// A replaced element cannot be an internal table box: its content is not
		// a formatting context a table's structure can be put through, so the
		// declaration is dropped and it is laid out as the inline replaced box it
		// is — which the missing-child rule then wraps in an anonymous cell,
		// along with the text beside it.
		//
		// Honouring it instead made the picture a cell, and the cell took the
		// column's width and the row's height and stretched the picture to fill
		// them.
		name: "a replaced element cannot be a table cell",
		html: `<span style="display:table-row">x<img style="display:table-cell"/>y</span>`,
		want: `anonymous block/flow-root
  anonymous block/table
    span block/table-row
      anonymous block/table-cell
        text "x"
        img inline
        text "y"
`,
	}, {
		// The same for a row, and the same for a non-replaced element beside it:
		// the div does become the cell it asked to be.
		name: "a replaced element cannot be a table row either",
		html: `<div style="display:table"><img style="display:table-row"/>` +
			`<div style="display:table-cell">c</div></div>`,
		want: `anonymous block/flow-root
  div block/table
    anonymous block/table-row
      anonymous block/table-cell
        img inline
      div block/table-cell
        text "c"
`,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bodyBoxes(t, tc.html); got != tc.want {
				t.Errorf("%s\ngot\n%s\nwant\n%s", tc.html, got, tc.want)
			}
		})
	}
}

// TestCaptionSide pins that a caption leaves the table and lands on the side
// caption-side names.
//
// The assertion is on the *order* of the wrapper's children rather than on the
// caption existing, because a caption that never moved would still exist — it
// would be a child of the table, inside its border box, which is precisely the
// wrong answer that looks right in a sketch.
func TestCaptionSide(t *testing.T) {
	for _, tc := range []struct {
		side  string
		first Inner
	}{
		{"top", InnerTableCaption},
		{"bottom", InnerTable},
	} {
		got := build(t, `<table style="caption-side: `+tc.side+`">`+
			`<caption>cap</caption><tr><td>a</td></tr></table>`)
		wrapper := findBoxWhere(t, got.Root, func(b *Box) bool { return b.TableWrapper })
		if len(wrapper.Children) != 2 {
			t.Fatalf("caption-side:%s gave the wrapper %d children, want 2:\n%s",
				tc.side, len(wrapper.Children), sketchBox(got.Root))
		}
		if wrapper.Children[0].Inner != tc.first {
			t.Errorf("caption-side:%s puts %v first, want %v",
				tc.side, wrapper.Children[0].Inner, tc.first)
		}
	}
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

// The layout tests assert absolute numbers against §17's own arithmetic, which
// is the point of them: the reftest oracle next door compares two documents
// rendered by the same engine and cannot see a fault that moves both. Every
// expected value here is worked out from the specification and from the widths
// the standard faces give, and the comment on each says which.

// bare turns off everything the user-agent sheet does to a table, so that an
// expected number is the rule under test and not the sum of the rule and 2px of
// spacing.
const bareTable = `
html, body, p, div { margin: 0; padding: 0 }
table { border-spacing: 0 }
td, th { padding: 0 }
`

// cellRect returns the border boxes of a table's cells, in document order.
func cellRects(root *Fragment) []Rect {
	var out []Rect
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f.Box != nil && f.Box.Inner == InnerTableCell {
			out = append(out, f.BorderRect)
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// TestTableColumnsDivideTheWidth pins §17.5.2: the columns and the spacing add
// up to the table's content width exactly, and each cell sits at its column.
//
// The arithmetic: a table 300 wide with 10px of spacing has spacing before,
// between and after two columns — 30 in all — leaving 270 to divide. Both
// columns hold the same text, so the automatic algorithm gives them the same
// maximum and they take 135 each.
func TestTableColumnsDivideTheWidth(t *testing.T) {
	root := layoutOf(t, 1000, `<table id=t style="width: 300px; border-spacing: 10px">`+
		`<tr><td>ab</td><td>ab</td></tr></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2:\n%s", len(cells), sketchFragments(root))
	}
	table := find(t, root, "t")
	px(t, "the table's width", table.BorderRect.W, 300)
	px(t, "the first column's left edge", cells[0].X.Sub(table.BorderRect.X), 10)
	px(t, "the first column's width", cells[0].W, 135)
	px(t, "the second column's left edge", cells[1].X.Sub(table.BorderRect.X), 155)
	px(t, "the second column's width", cells[1].W, 135)
	// And the table is exactly as wide as its parts, with no hairline left over.
	px(t, "the right edge of the grid",
		cells[1].Right().Add(mustPx(10)).Sub(table.BorderRect.X), 300)
}

// TestTableAutoWidthDoesNotFillItsContainingBlock pins §17.5.2.2's formula in
// the two directions that distinguish a table from an ordinary block.
//
// A table whose content wants less than the containing block gets what it wants
// — a <div> in the same place would fill the page. A table whose content wants
// more is as wide as the containing block, not as wide as its content. Both are
// asserted because an implementation that simply filled the parent passes the
// second on its own.
func TestTableAutoWidthDoesNotFillItsContainingBlock(t *testing.T) {
	narrow := layoutOf(t, 1000, `<table id=t><tr><td>ab</td></tr></table>`, bareTable)
	table := find(t, narrow, "t")
	if w := table.BorderRect.W; w >= mustPx(1000) {
		t.Errorf("a table of two characters is %.2f px wide; it should want less than the page",
			w.Px())
	}
	// And so is §17.4's wrapper: it is as wide as the table's border box and no
	// wider, which is what makes "margin: 0 auto" centre a table. Asserting the
	// table alone misses this — the two mechanisms each keep the table narrow on
	// their own, so a wrapper that filled the page would leave the table where
	// it was and only show up when something was centred against it.
	wrapper := findFragment(t, narrow, func(f *Fragment) bool {
		return f.Box != nil && f.Box.TableWrapper
	})
	if wrapper.BorderRect.W != table.BorderRect.W {
		t.Errorf("the table wrapper is %.2f px wide and the table inside it %.2f",
			wrapper.BorderRect.W.Px(), table.BorderRect.W.Px())
	}

	wide := layoutOf(t, 200, `<table id=t><tr><td>`+
		strings.Repeat("word ", 60)+`</td></tr></table>`, bareTable)
	px(t, "a table whose content overflows the page", find(t, wide, "t").BorderRect.W, 200)
}

// TestTableFixedLayoutIgnoresContent pins §17.5.2.1.
//
// The declared widths in the first row decide the columns whatever is in them,
// and the column left over shares what remains. The second row is deliberately
// far wider than the first and must change nothing — that is the whole of what
// "fixed" buys and the whole of what it costs.
func TestTableFixedLayoutIgnoresContent(t *testing.T) {
	root := layoutOf(t, 1000, `<table id=t style="table-layout: fixed; width: 300px">`+
		`<tr><td style="width: 100px">a</td><td>b</td></tr>`+
		`<tr><td>`+strings.Repeat("x", 200)+`</td><td>y</td></tr></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the declared column", cells[0].W, 100)
	px(t, "the remaining column", cells[1].W, 200)
	px(t, "the second row's first column", cells[2].W, 100)
}

// TestAColumnGroupWithNoColumnsDescribesItsOwn pins §17.2's second rule for
// generating the column grid:
//
//	A 'table-column-group' box ... if it has no children, it generates as many
//	anonymous 'table-column' boxes as its 'span' says
//
// and the consequence §17.5.2 then relies on: those columns have no box of their
// own for a width to be written on, so the group's own width is theirs. A group
// whose width is read from nowhere leaves the columns to be sized by their
// content, and a table of empty cells then has no width at all — which is what
// this engine did, and what width-applies-to-005 in the suite is 96 pixels of.
//
// The two cases are the two rules, and each is the wrong answer for the other.
// With no children the group is the column box and its 96px sizes the table.
// With a child column, the column has its own box and its own 40px, and the
// group's 96 says nothing about it.
func TestAColumnGroupWithNoColumnsDescribesItsOwn(t *testing.T) {
	for _, tc := range []struct {
		what, inner string
		want        float64
	}{
		{"a group with no columns", "", 96},
		{"a group whose column has its own width", `<col style="width: 40px">`, 40},
	} {
		root := layoutOf(t, 1000,
			`<table id=t style="table-layout: fixed">`+
				`<colgroup id=cg style="width: 96px">`+tc.inner+`</colgroup>`+
				`<tr><td id=c></td></tr></table>`, bareTable)
		px(t, tc.what+": the table's width", find(t, root, "t").BorderRect.W, tc.want)

		// And the group's background is put down once. It is already the
		// fragment the columns hang from, so painting it again as one of them
		// would double it — invisible for an opaque colour and not for this one.
		root = layoutOf(t, 1000,
			`<table id=t style="table-layout: fixed">`+
				`<colgroup id=cg style="width: 96px; background: rgba(0,0,255,0.5)">`+
				tc.inner+`</colgroup><tr><td id=c style="height: 20px"></td></tr></table>`, bareTable)
		n := 0
		for _, op := range Paint(root) {
			if v, ok := op.(FillRect); ok && v.Color.B == 255 {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: the group's background was painted %d times, want 1", tc.what, n)
		}
	}
}

// TestTableColumnUnderflowIsReported pins the guardrail: a fixed layout that
// squeezes a column below its content is a silent clip, and §6.2 says so out
// loud.
func TestTableColumnUnderflowIsReported(t *testing.T) {
	fired[RuleTableColumnUnderflow] = true

	rec := NewRecorder(nil)
	built := Build(Input{HTML: `<table style="table-layout: fixed; width: 20px">` +
		`<tr><td>` + strings.Repeat("x", 200) + `</td></tr></table>`,
		CSS: []Stylesheet{{Source: bareTable}}})
	w, _ := style.FromPx(1000)
	h, _ := style.FromPx(1000)
	Layout(built.Root, Size{W: w, H: h}, nil, rec)

	if rec.Count(RuleTableColumnUnderflow) == 0 {
		t.Errorf("a column of 20px holding 200 characters raised nothing:\n%v", rec.Findings())
	}

	// And a table wide enough for its content says nothing, so the rule is not
	// simply always on.
	rec = NewRecorder(nil)
	built = Build(Input{HTML: `<table style="table-layout: fixed; width: 900px">` +
		`<tr><td>x</td></tr></table>`,
		CSS: []Stylesheet{{Source: bareTable}}})
	Layout(built.Root, Size{W: w, H: h}, nil, rec)
	if n := rec.Count(RuleTableColumnUnderflow); n != 0 {
		t.Errorf("a table with room to spare reported %d underflows", n)
	}
}

// TestTableRowHeightsAndSpans pins §17.5.3's vertical arithmetic.
//
// Two rows of 40 and 30, with 10px of vertical spacing: the second row starts 50
// below the first, and a cell spanning both is 80 tall — the two rows plus the
// spacing it swallows between them. A rowspan that did not add the spacing would
// be 10px short and look like a rounding error.
func TestTableRowHeightsAndSpans(t *testing.T) {
	root := layoutOf(t, 1000, `<table style="border-spacing: 10px">`+
		`<tr><td rowspan=2 id=span>s</td><td style="height: 40px">a</td></tr>`+
		`<tr><td style="height: 30px">b</td></tr></table>`, bareTable)
	cells := cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the first row's height", cells[1].H, 40)
	px(t, "the second row's height", cells[2].H, 30)
	px(t, "the gap between the two rows", cells[2].Y.Sub(cells[1].Bottom()), 10)
	px(t, "the spanning cell's height", cells[0].H, 80)
	px(t, "the spanning cell's top", cells[0].Y, cells[1].Y.Px())
}

// TestTableVerticalAlign pins §17.5.3's four alignments.
//
// One tall cell sets the row's height at 60; the others are one line of 16px
// text. Top puts the line at the row's top, bottom at 60 minus the line's
// height, middle halfway between.
//
// The line is 17.859375: the default face here is Times, which states no line
// gap, so "line-height: normal" is the box enclosing its glyphs — -218 to 898
// out of 1000, or 1.116 of an em — and 16px is 1024 units, of which 1116/1000
// is 1142.784, rounding to 1143 units. See lineMetrics for why the glyph box
// and not the AFM's ascender and descender.
func TestTableVerticalAlign(t *testing.T) {
	root := layoutOf(t, 1000, `<table>`+
		`<tr><td style="height: 60px">tall</td>`+
		`<td id=top style="vertical-align: top">a</td>`+
		`<td id=mid style="vertical-align: middle">a</td>`+
		`<td id=bot style="vertical-align: bottom">a</td></tr></table>`, bareTable)

	const line = 17.859375
	for _, tc := range []struct {
		id   string
		want float64
	}{
		{"top", 0},
		{"mid", (60 - line) / 2},
		{"bot", 60 - line},
	} {
		cell := find(t, root, tc.id)
		if len(cell.Lines) != 1 {
			t.Fatalf("#%s has %d lines, want 1", tc.id, len(cell.Lines))
		}
		px(t, "the line box of #"+tc.id, cell.Lines[0].Rect.Y, tc.want)
		px(t, "the height of #"+tc.id, cell.BorderRect.H, 60)
	}
}

// TestTableBaselineAlignment pins the alignment that makes a table of text read
// as rows rather than as a grid of boxes.
//
// The two cells hold text at different sizes, so their first baselines are at
// different depths. Aligning on the baseline moves the smaller text down until
// the two sit on one line, which means the *difference* between the two cells'
// line positions is the difference between their baselines — and the cell with
// the deeper baseline does not move at all.
//
// The value is asked for rather than relied on. This used to say it was pinning
// the *default* and to leave the property unset, which was wrong twice over:
// vertical-align's initial value is baseline, but a cell never sees it, because
// HTML's rendering section puts "vertical-align: middle" on the row groups and
// "vertical-align: inherit" on the rows and cells. A cell's default is middle,
// and TestTableCellsAreMiddleAlignedByDefault is where that is stated.
func TestTableBaselineAlignment(t *testing.T) {
	root := layoutOf(t, 1000, `<table><tr>`+
		`<td id=big style="font-size: 40px; vertical-align: baseline">A</td>`+
		`<td id=small style="font-size: 10px; vertical-align: baseline">a</td>`+
		`</tr></table>`, bareTable)

	big, small := find(t, root, "big"), find(t, root, "small")
	if len(big.Lines) != 1 || len(small.Lines) != 1 {
		t.Fatalf("expected one line in each cell:\n%s", sketchFragments(root))
	}
	bigBase := big.Lines[0].Rect.Y.Add(big.Lines[0].Baseline)
	smallBase := small.Lines[0].Rect.Y.Add(small.Lines[0].Baseline)
	if bigBase != smallBase {
		t.Errorf("the two cells' baselines are %.4f and %.4f px from the row top; "+
			"vertical-align: baseline should put them on one line",
			bigBase.Px(), smallBase.Px())
	}
	// And the deeper one did not move, since it is what the row aligned on.
	px(t, "the big cell's line box", big.Lines[0].Rect.Y, 0)
	if small.Lines[0].Rect.Y <= 0 {
		t.Errorf("the small cell's line did not move down to meet the baseline "+
			"(it is at %.4f)", small.Lines[0].Rect.Y.Px())
	}
}

// TestTableEmptyCells pins §17.6.1.1 in both directions: an empty cell asked to
// hide draws nothing, and the same cell with a character in it draws.
func TestTableEmptyCells(t *testing.T) {
	// The empty cell is given a width of its own: a column with nothing in it
	// and no width is zero wide, so a test without one would be asserting that
	// an empty rectangle paints nothing, which it does either way.
	const src = `<table style="empty-cells: %s"><tr>` +
		`<td id=full style="background: red">x</td>` +
		`<td id=empty style="background: red; width: 50px; height: 20px"></td></tr></table>`

	painted := func(css string) int {
		root := layoutOf(t, 1000, strings.Replace(src, "%s", css, 1), bareTable)
		n := 0
		for _, op := range Paint(root) {
			if fill, ok := op.(FillRect); ok && fill.Color.R == 255 && !fill.Rect.Empty() {
				n++
			}
		}
		return n
	}
	if got := painted("show"); got != 2 {
		t.Errorf("empty-cells: show painted %d red cells, want 2", got)
	}
	if got := painted("hide"); got != 1 {
		t.Errorf("empty-cells: hide painted %d red cells, want 1", got)
	}
}

// TestTableRowGroupOrder pins §17.5.1: the header goes first and the footer
// last, wherever the markup put them.
func TestTableRowGroupOrder(t *testing.T) {
	root := layoutOf(t, 1000, `<table>`+
		`<tfoot><tr><td id=f>f</td></tr></tfoot>`+
		`<tbody><tr><td id=b>b</td></tr></tbody>`+
		`<thead><tr><td id=h>h</td></tr></thead></table>`, bareTable)

	h, b, f := find(t, root, "h"), find(t, root, "b"), find(t, root, "f")
	if !(h.BorderRect.Y < b.BorderRect.Y && b.BorderRect.Y < f.BorderRect.Y) {
		t.Errorf("the rows are at head=%.2f body=%.2f foot=%.2f; the header goes "+
			"first and the footer last whatever order they were written in",
			h.BorderRect.Y.Px(), b.BorderRect.Y.Px(), f.BorderRect.Y.Px())
	}
}

// TestTableSpanClamping is the security test: colspan and rowspan are
// attacker-controlled integers, and a document that names a hundred million
// columns must not allocate them.
//
// The assertion is on the grid rather than on the render, because a render that
// survived would prove only that the machine was big enough that day.
func TestTableSpanClamping(t *testing.T) {
	l := &layouter{rec: NewRecorder(nil), lengths: map[lengthKey]style.Length{},
		grids: map[*Box]*tableGrid{}}

	built := Build(Input{HTML: `<table><tr>` +
		`<td colspan="99999999" rowspan="99999999">a</td></tr></table>`})
	table := findBoxWhere(t, built.Root, func(b *Box) bool { return b.Inner == InnerTable })

	g := l.tableGridFor(table)
	if g.cols != maxColSpan {
		t.Errorf("colspan=99999999 produced %d columns, want it clamped to %d",
			g.cols, maxColSpan)
	}
	if len(g.cells) != 1 {
		t.Fatalf("got %d cells, want 1", len(g.cells))
	}
	if got := g.cells[0].colSpan; got != maxColSpan {
		t.Errorf("the cell spans %d columns, want %d", got, maxColSpan)
	}
	// A rowspan is clipped to the rows that exist, so a span of ninety-nine
	// million in a one-row table is a span of one. Nothing proportional to the
	// attribute is ever allocated.
	if got := g.cells[0].rowSpan; got != 1 {
		t.Errorf("the cell spans %d rows in a table with one, want 1", got)
	}
}

// TestTableColumnCapFires watches the grid bound trip.
//
// The cap is lowered rather than the document made enormous, for the reason
// maxBoxes gives: a bound that has only ever been observed not to trip is one
// nobody knows works, and building four thousand columns to watch this one
// would cost more than the rest of the file.
func TestTableColumnCapFires(t *testing.T) {
	fired[RuleLimit] = true

	saved := maxTableColumns
	maxTableColumns = 3
	defer func() { maxTableColumns = saved }()

	rec := NewRecorder(nil)
	l := &layouter{rec: rec, lengths: map[lengthKey]style.Length{},
		grids: map[*Box]*tableGrid{}}
	built := Build(Input{HTML: `<table><tr><td>a</td><td>b</td><td>c</td>` +
		`<td>d</td><td>e</td></tr></table>`})
	table := findBoxWhere(t, built.Root, func(b *Box) bool { return b.Inner == InnerTable })

	g := l.tableGridFor(table)
	if g.cols > 3 {
		t.Errorf("the grid has %d columns with the cap at 3", g.cols)
	}
	if len(g.cells) != 3 {
		t.Errorf("%d cells were placed, want the 3 that fit under the cap", len(g.cells))
	}
	if rec.Count(RuleLimit) == 0 {
		t.Error("the cap dropped cells and said nothing, which is the silent " +
			"truncation the limit rule exists to prevent")
	}
}

// TestBorderCollapseIsNotReported pins that §17.6.2 is implemented rather than
// admitted to.
//
// This was the opposite test: the collapsing model was refused out loud, because
// a table laid out with the separated model where the author asked for the
// collapsing one is wrong by a border width on every line and looks deliberate.
// bordercollapse.go is that admission redeemed, and what is left to guard is
// that the finding does not come back by accident — a leftover report would keep
// every document with a collapsed table out of §7.1's clean-pass count for a
// feature that is now there.
func TestBorderCollapseIsNotReported(t *testing.T) {
	for _, value := range []string{"collapse", "separate"} {
		rec := NewRecorder(nil)
		built := Build(Input{HTML: `<table style="border-collapse: ` + value + `">` +
			`<tr><td>a</td></tr></table>`})
		w, _ := style.FromPx(1000)
		Layout(built.Root, Size{W: w, H: w}, nil, rec)
		for _, f := range rec.Findings() {
			if f.Property == "border-collapse" {
				t.Errorf("border-collapse: %s reported %q", value, f.Message)
			}
		}
	}
}

// TestTableCellHeightIsAMinimum pins the rule of §17.5.3 that is the opposite of
// what a height does to an ordinary block: content is never cut off to honour
// it.
func TestTableCellHeightIsAMinimum(t *testing.T) {
	root := layoutOf(t, 1000,
		`<table><tr><td id=c style="height: 1px">a<br>b<br>c</td></tr></table>`, bareTable)
	cell := find(t, root, "c")
	// Three line boxes, each rounded to the layout unit before they are added
	// up — which is not the same number as rounding their sum.
	if want := mustPx(17.859375).Mul(3); cell.BorderRect.H != want {
		t.Errorf("a cell declared 1px tall holding three lines is %.2f px, want %.2f",
			cell.BorderRect.H.Px(), want.Px())
	}
}

// TestPresentationalTableAttributes pins the HTML mapping, in both directions:
// the attribute overrides the user-agent sheet and loses to the author's CSS.
func TestPresentationalTableAttributes(t *testing.T) {
	// Without the attribute the user-agent sheet's 2px of spacing and 1px of
	// cell padding apply, so the two cells are 4px apart across the gap.
	plain := cellRects(layoutOf(t, 1000, `<table><tr><td>a</td><td>b</td></tr></table>`))
	if len(plain) != 2 {
		t.Fatalf("got %d cells, want 2", len(plain))
	}
	px(t, "the default gap between two cells", plain[1].X.Sub(plain[0].Right()), 2)

	// cellspacing=0 beats the user-agent rule.
	zero := cellRects(layoutOf(t, 1000,
		`<table cellspacing="0"><tr><td>a</td><td>b</td></tr></table>`))
	px(t, "the gap with cellspacing=0", zero[1].X.Sub(zero[0].Right()), 0)

	// And loses to the author's, which is the half that is easy to get wrong by
	// treating the attribute as an inline style.
	beaten := cellRects(layoutOf(t, 1000,
		`<table cellspacing="0"><tr><td>a</td><td>b</td></tr></table>`,
		`table { border-spacing: 6px }`))
	px(t, "the gap when CSS overrides cellspacing", beaten[1].X.Sub(beaten[0].Right()), 6)

	// cellpadding reaches the cells, which are not the element carrying it.
	padded := cellRects(layoutOf(t, 1000,
		`<table cellspacing="0" cellpadding="7"><tr><td>a</td></tr></table>`))
	bare := cellRects(layoutOf(t, 1000,
		`<table cellspacing="0" cellpadding="0"><tr><td>a</td></tr></table>`))
	px(t, "the width cellpadding=7 adds to a cell", padded[0].W.Sub(bare[0].W), 14)
}

// TestFontSizeDoesNotCompound pins the bug the table work found.
//
// CSS inherits the *computed* font-size, which is an absolute length. An engine
// that stores the specified value and re-resolves it at every level doubles the
// size once per element inside a "font-size: 2em" wrapper — and it is invisible
// until two subtrees of different depths have to line up, which is exactly what
// a table full of nested boxes does.
func TestFontSizeDoesNotCompound(t *testing.T) {
	got := build(t, `<div style="font-size: 2em"><p id=a>x</p>`+
		`<div><div><p id=b>y</p></div></div></div>`)

	for _, id := range []string{"a", "b"} {
		box := findBoxWhere(t, got.Root, func(b *Box) bool {
			if b.Element == nil {
				return false
			}
			v, _ := b.Element.Attr("id")
			return v == id
		})
		if want := mustPx(32); box.FontSize != want {
			t.Errorf("#%s is set in %.2f px; everything inside one \"font-size: 2em\" "+
				"is 32px however deeply it is nested", id, box.FontSize.Px())
		}
	}

	// An element that declares its own relative size *does* compound, which is
	// the half a naive "inherited values are never resolved" fix breaks.
	nested := build(t, `<div style="font-size: 2em"><div id=c style="font-size: 2em">z</div></div>`)
	box := findBoxWhere(t, nested.Root, func(b *Box) bool {
		v, _ := "", ""
		if b.Element != nil {
			v, _ = b.Element.Attr("id")
		}
		return v == "c"
	})
	if want := mustPx(64); box.FontSize != want {
		t.Errorf("two nested declarations of \"font-size: 2em\" gave %.2f px, want %.2f",
			box.FontSize.Px(), want.Px())
	}

	// A pseudo-element inherits from the element it belongs to, so it has the
	// same trap and needed the same answer. This case was added after a planted
	// defect — removing the guard on the pseudo-element path — broke nothing at
	// all, which meant that half of the fix was untested.
	pseudo := build(t, `<div style="font-size: 2em"><p id=p>x</p></div>`,
		`p::before { content: "m" }`)
	before := findBoxWhere(t, pseudo.Root, func(b *Box) bool {
		return b.Element != nil && b.Element.Name == "p" && len(b.Children) > 0 &&
			b.Children[0].IsText() && b.Children[0].Text == "m"
	})
	if want := mustPx(32); before.Children[0].FontSize != want {
		t.Errorf("a ::before with no size of its own is set in %.2f px inside a "+
			"\"font-size: 2em\" wrapper, want %.2f",
			before.Children[0].FontSize.Px(), want.Px())
	}

	// And one that declares a relative size still resolves it against the
	// element it belongs to.
	sized := build(t, `<div style="font-size: 2em"><p id=p>x</p></div>`,
		`p::before { content: "m"; font-size: 2em }`)
	own := findBoxWhere(t, sized.Root, func(b *Box) bool {
		return b.Element != nil && b.Element.Name == "p" && len(b.Children) > 0 &&
			b.Children[0].IsText() && b.Children[0].Text == "m"
	})
	if want := mustPx(64); own.Children[0].FontSize != want {
		t.Errorf("a ::before declaring \"font-size: 2em\" is set in %.2f px, want %.2f",
			own.Children[0].FontSize.Px(), want.Px())
	}
}

// TestTableColumnSpanWidens pins the part of §17.5.2.2 that a table of equal
// cells cannot see: a cell spanning several columns has to make them wide enough
// between them, and the shortfall is shared rather than landing on one.
//
// The single-column cells ask for the width of "x", so a spanning cell holding
// far more than two of those is what decides both columns. The assertion is that
// the two columns *together* hold it and that neither took the whole increase.
func TestTableColumnSpanWidens(t *testing.T) {
	root := layoutOf(t, 1000, `<table><tr>`+
		`<td>x</td><td>x</td></tr>`+
		`<tr><td colspan=2 id=wide>`+strings.Repeat("w", 40)+`</td></tr></table>`,
		bareTable)

	cells := cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	wide := find(t, root, "wide")
	if got, want := cells[0].W.Add(cells[1].W), wide.BorderRect.W; got != want {
		t.Errorf("the two columns are %.2f px together and the cell spanning them "+
			"is %.2f", got.Px(), want.Px())
	}
	if cells[0].W == cells[1].W && cells[0].W < mustPx(10) {
		t.Errorf("neither column grew: they are %.2f px each, and the spanning "+
			"cell needs %.2f", cells[0].W.Px(), wide.BorderRect.W.Px())
	}
	// The growth is shared, not dumped on one column. Both started equal, so
	// both end equal to within the layout unit that the exact-sum distribution
	// deliberately leaves on the last column rather than losing.
	if d := cells[0].W.Sub(cells[1].W); d > 1 || d < -1 {
		t.Errorf("two columns that started equal ended at %.2f and %.2f; the "+
			"spanning cell's excess should be shared between them",
			cells[0].W.Px(), cells[1].W.Px())
	}
}

// TestFixedColumnTakesTheCellsPaddingAndBorder pins the half of §17.5.2.1 that a
// cell with no padding cannot see: a first-row cell's declared "width" is a
// content width, so the column it settles has to hold that plus the cell's own
// horizontal padding and border.
//
// The arithmetic, from a 400px table with three columns and no spacing:
//
//	column 2 = 80 (width) + 60 + 60 (padding) = 200
//	columns 1 and 3 = (400 − 200) / 2 = 100 each
//
// Every number is exact in layout units, so there is nothing to quantise.
//
// The two directions are asserted together on purpose. Reading the 80 as the
// whole column makes the middle column 80 and the outer two 160, so a test that
// only looked at the cell that declared the width would be satisfied by either
// answer being *somewhere* — it is the pair that decides.
func TestFixedColumnTakesTheCellsPaddingAndBorder(t *testing.T) {
	root := layoutOf(t, 1000, `<table id=t style="table-layout: fixed; width: 400px">`+
		`<tr><td>a</td>`+
		`<td id=mid style="width: 80px; padding: 0 60px">b</td>`+
		`<td>c</td></tr></table>`, bareTable)

	cells := cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the column of the cell that declared a width", cells[1].W, 200)
	px(t, "the first of the columns that declared nothing", cells[0].W, 100)
	px(t, "the second of the columns that declared nothing", cells[2].W, 100)

	// A border counts for exactly the same reason and by the same arithmetic, so
	// an implementation that added only the padding is caught here rather than
	// looking correct above.
	root = layoutOf(t, 1000, `<table id=t style="table-layout: fixed; width: 400px">`+
		`<tr><td>a</td>`+
		`<td id=mid style="width: 80px; border-left: solid 60px; border-right: solid 60px">b</td>`+
		`<td>c</td></tr></table>`, bareTable)
	cells = cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the column of a cell whose width is bordered rather than padded",
		cells[1].W, 200)
	px(t, "a column beside it", cells[0].W, 100)
}

// TestFixedColumnTakesHalfACollapsedBorder is the same rule under §17.6.2, where
// a cell's used border is half of each grid line rather than what it declared.
//
// The suite writes the arithmetic out in fixed-table-layout-003f01, and it is the
// case that tells the two models apart:
//
//	column 2 = 30 (half the left line) + 80 (width) + 30 (half the right line) = 140
//	columns 1 and 3 = (400 − 140) / 2 = 130 each
//
// The same document in the separated model gives 200 and 100 — that is the case
// above — so the two together are what pin the halving rather than either alone.
// The declared 60px border is *painted* 60 wide all the same, centred on the grid
// line and so reaching 30px into the column next door; only the 30 on this side
// of the line is width the column has to find.
func TestFixedColumnTakesHalfACollapsedBorder(t *testing.T) {
	root := layoutOf(t, 1000, `<table id=t style="table-layout: fixed; width: 400px">`+
		`<tr><td>a</td>`+
		`<td id=mid style="width: 80px; border-left: solid 60px; border-right: solid 60px">b</td>`+
		`<td>c</td></tr></table>`, collapsing)

	table := find(t, root, "t")
	px(t, "the table's width", table.BorderRect.W, 400)

	cells := cellRects(root)
	if len(cells) != 3 {
		t.Fatalf("got %d cells, want 3:\n%s", len(cells), sketchFragments(root))
	}
	px(t, "the column of the cell that declared a width", cells[1].W, 140)
	px(t, "the first of the columns that declared nothing", cells[0].W, 130)
	px(t, "the second of the columns that declared nothing", cells[2].W, 130)
	// The cell's own used border is the half-line, which is what made the column
	// 140 rather than 200. Asserting it here is what separates "the column came
	// out at 140" from "the column came out at 140 for the right reason".
	px(t, "the cell's used border-left", find(t, root, "mid").Border.Left, 30)
}

// TestHTMLTableWidthIsItsBorderBox pins §17.6.1's divergence, in both directions.
//
// The specification is explicit that the two are different boxes:
//
//	The width of the table is the distance from the left inner padding edge to
//	the right inner padding edge (including the border spacing but excluding
//	padding and border). However, in HTML and XHTML1, the width of the <table>
//	element is the distance from the left border edge to the right border edge.
//
// So a <table> with a 25px border and "width: 200px" is 200px wide altogether,
// and a div with "display: table" and exactly the same declarations is 250px. The
// pair is the test: either rule on its own passes half of it.
func TestHTMLTableWidthIsItsBorderBox(t *testing.T) {
	const decl = `width: 200px; border: solid 25px`
	root := layoutOf(t, 1000,
		`<table id=t style="`+decl+`"><tr><td>a</td></tr></table>`, bareTable)
	px(t, "an HTML table's border box", find(t, root, "t").BorderRect.W, 200)

	root = layoutOf(t, 1000,
		`<div id=t style="display: table; `+decl+`">`+
			`<div style="display: table-row"><div style="display: table-cell">a</div></div>`+
			`</div>`, bareTable)
	px(t, "a CSS table's border box", find(t, root, "t").BorderRect.W, 250)
}

// TestFixedTableIsAtLeastAsWideAsItsColumns pins the last sentence of §17.5.2.1:
// "the width of the table is then the greater of the value of the 'width'
// property for the table element and the sum of the column widths (plus cell
// spacing or borders)".
//
// The arithmetic: two columns the author sized at 20px, with 20px of
// border-spacing before, between and after them, come to 20 + 20 + 20 + 20 + 20 =
// 100. A table declared 70px wide is 100px wide, because nothing in the fixed
// algorithm can make a column narrower than it was declared.
//
// The failure it guards against is invisible in the cells, which is why the
// assertion is on the table: the columns were always laid out at 20px, and it was
// the table's own box that came out 30px short — so the cells hung out of the
// right-hand side of their own table and whatever was behind it showed through.
//
// The other direction is asserted beside it, because "the greater of" has two
// halves and a rule that simply took the columns' sum would pass the first: a
// table declared 300px wide with the same 100px of columns stays 300, and the
// fixed algorithm's promise that a table is the width it was given survives.
func TestFixedTableIsAtLeastAsWideAsItsColumns(t *testing.T) {
	const row = `<tr><td style="width: 20px">1</td><td style="width: 20px">2</td></tr>`
	narrow := layoutOf(t, 1000,
		`<div id=t style="display: table; table-layout: fixed; border-spacing: 20px; `+
			`width: 70px">`+row+`</div>`, bareTable)
	px(t, "a fixed table declared narrower than its own columns",
		find(t, narrow, "t").BorderRect.W, 100)

	wide := layoutOf(t, 1000,
		`<div id=t style="display: table; table-layout: fixed; border-spacing: 20px; `+
			`width: 300px">`+row+`</div>`, bareTable)
	px(t, "a fixed table declared wider than its own columns",
		find(t, wide, "t").BorderRect.W, 300)
	// And the surplus went to the columns rather than being left as a gap, which
	// is the rest of that sentence: 300 − 60 of spacing is 240 over two columns.
	cells := cellRects(wide)
	if len(cells) != 2 {
		t.Fatalf("got %d cells, want 2:\n%s", len(cells), sketchFragments(wide))
	}
	px(t, "the first column of the wider table", cells[0].W, 120)

	// The case above cannot tell "the greater of the two" from "the sum of the
	// columns", and that is not a hole in the assertion — it is the arithmetic:
	// whenever the declared width is the larger, the fixed algorithm spends the
	// difference on the columns and the sum comes to the declared width exactly.
	// A table with no columns at all is the one document where the two differ,
	// because there is nothing to spend the width on and the sum is zero. Planting
	// the clause found this: without a table like this one, dropping "the greater
	// of" entirely changes nothing anywhere in the suite.
	empty := layoutOf(t, 1000,
		`<div id=t style="display: table; table-layout: fixed; width: 300px; `+
			`height: 40px"></div>`, bareTable)
	px(t, "a fixed table with no columns in it", find(t, empty, "t").BorderRect.W, 300)
}

// TestTablePercentageWidthIsOfTheWrappersContainingBlock pins §17.4's rule for a
// percentage: it is a percentage of the containing block the *wrapper* is in, not
// of the wrapper, and not of what is left after the table's own border.
//
// Both of those were wrong, in the same document and in opposite directions, and
// each hid the other:
//
//   - the wrapper shrank to fit the table's content, so "width: 80%" meant eighty
//     per cent of the widest word in the table;
//   - and the percentage was resolved against the room left after the table's own
//     border, so a 20px border took 16px off the answer as well.
//
// The arithmetic here: a 500px containing block, "width: 80%" and a 20px border
// either side. The table is 400px wide altogether — a <table>'s width is its
// border box — and its content box is 360. Resolving against the wrapper would
// give 320 and against the room after the border 368, so the three answers are
// far enough apart that the test cannot pass by accident.
func TestTablePercentageWidthIsOfTheWrappersContainingBlock(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div style="width: 500px">`+
			`<table id=t style="width: 80%; border: solid 20px">`+
			`<tr><td>a</td></tr></table></div>`, bareTable)

	table := find(t, root, "t")
	px(t, "the table's border box", table.BorderRect.W, 400)
	px(t, "the table's content box", table.ContentRect().W, 360)

	// §17.4's wrapper is as wide as the table's border box and no wider, which is
	// what makes the whole thing hold together: a wrapper that had shrunk to the
	// content would be the containing block the percentage was answered against.
	wrapper := findFragment(t, root, func(f *Fragment) bool {
		return f.Box != nil && f.Box.TableWrapper
	})
	px(t, "the table wrapper", wrapper.BorderRect.W, 400)

	// The same document as a CSS table, where the declared width is the content
	// box — see htmlTableWidth. 80% of 500 is 400 of content and the border makes
	// the box 440, so the wrapper is 440 and not 400. It is the same percentage
	// against the same containing block and a different answer, which is what
	// makes this the case that decides whether the wrapper adds the table's edges
	// back on.
	root = layoutOf(t, 1000,
		`<div style="width: 500px">`+
			`<div id=t style="display: table; width: 80%; border: solid 20px">`+
			`<div style="display: table-row"><div style="display: table-cell">a</div></div>`+
			`</div></div>`, bareTable)
	px(t, "a CSS table's border box", find(t, root, "t").BorderRect.W, 440)
	wrapper = findFragment(t, root, func(f *Fragment) bool {
		return f.Box != nil && f.Box.TableWrapper
	})
	px(t, "the wrapper around a CSS table", wrapper.BorderRect.W, 440)
}

// findFragment returns the first fragment in tree order satisfying a predicate.
func findFragment(t *testing.T, root *Fragment, ok func(*Fragment) bool) *Fragment {
	t.Helper()
	var found *Fragment
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f == nil || found != nil {
			return
		}
		if ok(f) {
			found = f
			return
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no fragment matched:\n%s", sketchFragments(root))
	}
	return found
}

// findBox returns the first box in tree order satisfying a predicate.
func findBoxWhere(t *testing.T, root *Box, ok func(*Box) bool) *Box {
	t.Helper()
	var found *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b == nil || found != nil {
			return
		}
		if ok(b) {
			found = b
			return
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no box matched:\n%s", sketchBox(root))
	}
	return found
}

// TestPreservedWhiteSpaceBetweenTableTagsIsStillDropped is §17.2.1's
// remove-irrelevant-boxes rule against the white-space property.
//
// Both of the rules are stated over "an anonymous inline box that contains only
// white space" and neither is qualified by white-space, because the question is
// what the box is made of rather than what would have become of it. Reading the
// property here turned the newline and indentation between two tags into table
// content: a cell appeared before every real one, and the table's own content
// moved a column to the right.
func TestPreservedWhiteSpaceBetweenTableTagsIsStillDropped(t *testing.T) {
	// The indentation is the point, so it is written out rather than folded on
	// to one line: this document has a text node between every pair of tags.
	const doc = `<div id="t">
		<div id="r">
			<div id="c">ab</div>
		</div>
	</div>`
	const css = `#t { display: table; white-space: pre; font-family: Courier;
	                  font-size: 20px }
	             #r { display: table-row }
	             #c { display: table-cell }`

	root := layoutOf(t, 600, doc, css)
	cell := find(t, root, "c")
	table := find(t, root, "t")
	if got := cell.BorderRect.X; got != table.ContentRect().X {
		t.Errorf("the cell starts at %gpx and the table's content at %gpx — the "+
			"white space between the tags generated a cell before it",
			got.Px(), table.ContentRect().X.Px())
	}

	// And the cell's own text still has its spaces, which is the half of the
	// rule that must not be lost to fix the other: "white-space: pre" has to
	// reach the cell's text.
	root = layoutOf(t, 600, `<div id="t"><div id="r"><div id="c">a  b</div></div></div>`, css)
	var text string
	for _, ln := range find(t, root, "c").Lines {
		for _, run := range ln.Runs {
			text += run.Text
		}
	}
	if text != "a  b" {
		t.Errorf("the cell's text is %q, want %q — the property stopped applying "+
			"to the cell as well as to the tags", text, "a  b")
	}
}

// TestTableCellsAreMiddleAlignedByDefault is where a cell's vertical alignment
// comes from, which is not the property's initial value.
//
// vertical-align does not inherit and its initial value is baseline, so left to
// CSS alone every cell would align on its row's baseline. HTML's rendering
// section overrides both halves of that:
//
//	thead, tbody, tfoot, table > tr { vertical-align: middle }
//	tr, td, th                      { vertical-align: inherit }
//
// which is why a short cell beside a tall one sits in the middle of the row
// rather than on its first line — the behaviour anyone who has centred
// something in a table cell without asking has relied on.
//
// The inherit is the half that is easy to drop, and dropping it is invisible
// until an author writes the property on a row. A UA rule naming the cells
// directly would set their value outright, and since the property does not
// inherit, an author's "vertical-align: top" on the <tr> would then reach
// nothing at all.
func TestTableCellsAreMiddleAlignedByDefault(t *testing.T) {
	// One cell sixty tall, one holding a single line. Middle puts the line
	// halfway down the row; baseline would put it at the top.
	root := layoutOf(t, 1000, `<table><tr>`+
		`<td style="height: 60px">tall</td><td id=q>a</td></tr></table>`, bareTable)
	q := find(t, root, "q")
	if len(q.Lines) != 1 {
		t.Fatalf("expected one line in the short cell:\n%s", sketchFragments(root))
	}
	line := q.Lines[0].Rect
	if line.Y <= 0 {
		t.Errorf("the short cell's line is at %g, at the top of a 60px row — the "+
			"cell was aligned on the baseline, so the middle never came down the "+
			"table", line.Y.Px())
	}
	// Halfway down, stated as the two gaps being the same: the room above the
	// line and the room below it. Measured rather than computed from the font
	// size, because "normal" line-height is whatever the face recommends and is
	// not exactly 1.2 of it.
	//
	// Within a layout unit, because the exact centre of this row falls on half
	// of one — the row has 40.796875px to share — and the engine works in
	// sixty-fourths of a pixel. A tolerance of one unit is the smallest that can
	// be right, and asking for equality is asking the arithmetic to do something
	// it cannot.
	above, below := line.Y, mustPx(60).Sub(line.Y).Sub(line.H)
	if d := above.Sub(below); d > 1 || d < -1 {
		t.Errorf("the short cell's line has %gpx above it and %gpx below it in a "+
			"60px row — the content is not centred", above.Px(), below.Px())
	}

	// And the author can still say otherwise on the row, which is what the
	// inherit in the rule is for.
	root = layoutOf(t, 1000, `<table><tr style="vertical-align: top">`+
		`<td style="height: 60px">tall</td><td id=q>a</td></tr></table>`, bareTable)
	if got := find(t, root, "q").Lines[0].Rect.Y; got != 0 {
		t.Errorf("with vertical-align:top on the row the short cell's line is at "+
			"%g, want 0 — the row's value did not reach the cell", got.Px())
	}
}

// TestColumnWidthLimitsApplyToColumns is §10.4's two limits reaching a table
// column, which they do: the properties apply to everything but non-replaced
// inline elements, table rows and row groups, and a column is none of those.
//
// A column is not laid out — it produces no fragment — so its width reaches the
// page only through the cells that sit in it, and every property written on one
// has to be read where the columns are sized rather than where boxes are. The
// width was; the two limits were not, so "min-width" on a <col> did nothing at
// all and "max-width" was overridden by the first cell that wanted more.
//
// The empty-table case is the one that shows the minimum matters on its own. A
// column with a minimum and no width has no content to be sized by, so without
// the minimum it comes out at nothing and the table with it — which is
// min-width-applies-to-005, a square that does not appear.
func TestColumnWidthLimitsApplyToColumns(t *testing.T) {
	const base = noDefaults + `
	#table { display: table; table-layout: fixed }
	#row { display: table-row }
	#cell { display: table-cell; height: 96px }`
	const html = `<div id="table"><div id="test"></div>` +
		`<div id="row"><div id="cell"></div></div></div>`
	widthOf := func(t *testing.T, decl string) float64 {
		t.Helper()
		root := layoutOf(t, 1000, html, base+`#test { `+decl+` }`)
		return find(t, root, "cell").BorderRect.W.Px()
	}

	for _, display := range []string{"table-column", "table-column-group"} {
		for _, c := range []struct {
			what, decl string
			want       float64
		}{
			{"a width", `width: 96px`, 96},
			{"a minimum and no width", `min-width: 96px`, 96},
			{"a maximum under the width", `max-width: 96px; width: 288px`, 96},
			{"a maximum over the width", `max-width: 288px; width: 96px`, 96},
			// §10.4 applies the maximum first and the minimum to what that
			// leaves, so a minimum above the maximum wins.
			{"a minimum above the maximum", `min-width: 96px; max-width: 48px`, 96},
		} {
			decl := "display: " + display + "; " + c.decl
			if got := widthOf(t, decl); got != c.want {
				t.Errorf("%s with %s: the column is %g wide, want %g",
					display, c.what, got, c.want)
			}
		}
	}

	// The fixed algorithm is a second reader of the same properties, and it is
	// only reached when the table itself declares a width — so it needs a case
	// of its own or the clamp it does is never run.
	//
	// The table is declared at the width the maximum allows, so that the column
	// exactly fills it: a fixed table with room left over shares the surplus
	// among its columns, which would put back what the maximum took off and
	// leave nothing to see.
	root := layoutOf(t, 1000,
		`<div id="table" style="width: 96px"><div id="test"></div>`+
			`<div id="row"><div id="cell"></div></div></div>`,
		base+`#test { display: table-column; width: 288px; max-width: 96px }`)
	if got := find(t, root, "cell").BorderRect.W.Px(); got != 96 {
		t.Errorf("in a fixed table the column is %g wide, want 96 — its maximum "+
			"was not applied to the width declared on it", got)
	}
}

// TestTableHeightLimitsReachTheRows is §10.7's two limits meeting §17.5.3's
// height algorithm.
//
// A table's height is shared out over its rows, so a limit that moved the table
// has to reach them or the table is one height and the rows in it are another.
// Both halves showed: a minimum left the rows at what their content needed
// inside a table that was taller, and a maximum left them at what the declared
// height gave them inside a table that was shorter — the cells hanging out of
// the bottom of their own table's background.
//
// The last case is the one that says why the maximum is applied only to a height
// that was declared. A maximum is not a height for the rows to share: it caps
// what the content produced. Handing it to them as one would stretch rows
// *shorter* than the cap up to meet it, which is the one thing a maximum must
// never do — and is a mistake that looks right in every test where the content
// is taller than the cap.
func TestTableHeightLimitsReachTheRows(t *testing.T) {
	const base = noDefaults + `
	#row { display: table-row }
	#cell { display: table-cell; width: 96px; height: 20px }`
	sizes := func(t *testing.T, decl string) (table, cell float64) {
		t.Helper()
		root := layoutOf(t, 1000,
			`<div id="table"><div id="row"><div id="cell"></div></div></div>`,
			base+`#table { `+decl+` }`)
		return find(t, root, "table").BorderRect.H.Px(),
			find(t, root, "cell").BorderRect.H.Px()
	}

	for _, c := range []struct {
		what, decl  string
		table, cell float64
	}{
		// A minimum above the content raises the table, and the row with it.
		{"a minimum over a table", `display: table; min-height: 96px`, 96, 96},
		{"a minimum over an inline table", `display: inline-table; min-height: 96px`, 96, 96},
		// A maximum under a declared height lowers both.
		{"a maximum under a declared height",
			`display: table; height: 288px; max-height: 96px`, 96, 96},
		// A maximum over a declared height is not a floor: neither moves.
		{"a maximum over a declared height",
			`display: table; height: 96px; max-height: 288px`, 96, 96},
		// A maximum with no height at all leaves the content where it is. This
		// is the case that must not stretch.
		{"a maximum with no height", `display: table; max-height: 96px`, 20, 20},
		// And a minimum under the content changes nothing either.
		{"a minimum under the content", `display: table; min-height: 10px`, 20, 20},
		// Both limits at once, which is where their order shows. §10.7 applies
		// the maximum to the declared height and the minimum to what that
		// leaves, so a minimum above the maximum wins: 288 capped to 96 and then
		// raised to 200. Applying them the other way round gives 96, and every
		// case above is blind to the difference.
		{"a minimum above a maximum",
			`display: table; height: 288px; min-height: 200px; max-height: 96px`, 200, 200},
	} {
		table, cell := sizes(t, c.decl)
		if table != c.table || cell != c.cell {
			t.Errorf("%s: the table is %g tall holding a %gpx row, want %g and %g",
				c.what, table, cell, c.table, c.cell)
		}
	}
}
