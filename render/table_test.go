package render

import (
	"testing"
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
	table := findBox(t, got.Root, func(b *Box) bool { return b.Inner == InnerTable })
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
		wrapper := findBox(t, got.Root, func(b *Box) bool { return b.TableWrapper })
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

// findBox returns the first box in tree order satisfying a predicate.
func findBox(t *testing.T, root *Box, ok func(*Box) bool) *Box {
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
