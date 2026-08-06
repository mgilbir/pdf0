package render

import (
	"github.com/mgilbir/pdf0/style"
)

// Tables: CSS 2.1 §17.
//
// A table is the one construct in CSS where the box tree the author wrote is
// almost never the box tree that gets laid out. §17.2.1 is a repair algorithm:
// it inserts the rows, cells and tables the markup left out, and it does so
// because the *layout* algorithm has a precondition the markup cannot be trusted
// to meet — a table is rows of cells, and everything else has to be made into
// that shape before a column width can mean anything.
//
// This file is that repair, §17.2.1 and §17.4; tablelayout.go is the algorithm
// it feeds. The two are one feature and are split only by size: every rule here
// is justified by something the layout would otherwise have to handle as a
// special case, and reading either alone leaves half the argument out.
//
// # Why the repair is worth doing properly
//
// It is tempting to treat §17.2.1 as pedantry — who writes a <td> outside a
// <tr>? — and the answer is that nobody writes one and browsers produce them
// constantly. The HTML parser is not the only source of a box tree: any
// "display: table-cell" on a <div>, any <table> whose <tbody> was implied, any
// stylesheet that makes a list into a table produces exactly the malformed
// shapes these rules are written for. Skipping them does not save the special
// cases, it moves them into the layout algorithm where each one is a guess.

// The classification predicates of §17.2.1. They read as a thicket of near
// synonyms and each one draws a line the algorithm needs:
//
//   - a *row group* is one of the three grouping displays, which behave
//     identically here and differ only in where §17.5.1 puts them on the page;
//   - a *proper table child* is what may sit directly inside a table. A cell is
//     deliberately not one: a cell belongs to a row, and a cell found in a table
//     is the everyday malformation the repair exists for;
//   - an *internal table box* is everything the table owns, cells included. It
//     is the set that gets swept into an anonymous table when it is found
//     somewhere it does not belong.

func isRowGroup(b *Box) bool { return b.Inner == InnerTableRowGroup }

func isTableRoot(b *Box) bool { return b.Inner == InnerTable }

// isProperTableChild reports whether a box may be a direct child of a table.
func isProperTableChild(b *Box) bool {
	switch b.Inner {
	case InnerTableRowGroup, InnerTableRow, InnerTableColumn,
		InnerTableColumnGroup, InnerTableCaption:
		return true
	}
	return false
}

// isInternalTableBox reports whether a box is one of the pieces a table is made
// of. A caption is not: it is a proper table child that sits beside the grid
// rather than in it, which is why §17.4 gives it to the wrapper box.
func isInternalTableBox(b *Box) bool {
	switch b.Inner {
	case InnerTableCell, InnerTableRow, InnerTableRowGroup,
		InnerTableColumn, InnerTableColumnGroup:
		return true
	}
	return false
}

// isTableInternalContainer reports whether a box holds table structure directly:
// a table, a row group or a row.
//
// It is what decides where a run of white space is thrown away. White space
// between a </td> and the next <td> is in the markup of every hand-written table
// in existence, and if it survived it would become an anonymous cell — a column
// per newline. Nothing else in this engine has a failure mode that loud.
func isTableInternalContainer(b *Box) bool {
	switch b.Inner {
	case InnerTable, InnerTableRowGroup, InnerTableRow:
		return true
	}
	return false
}

// tableFixupNeeded is the fast path. A document with no table display value
// anywhere pays one predicate per box for the whole of §17.2.1.
func tableFixupNeeded(parent *Box, kids []*Box) bool {
	if parent.Inner != InnerFlow && parent.Inner != InnerFlowRoot {
		return true
	}
	for _, c := range kids {
		if isProperTableChild(c) || c.Inner == InnerTableCell || isTableRoot(c) {
			return true
		}
	}
	return false
}

// fixupTables is §17.2.1 applied to one box's children.
//
// The three groups run in the order the specification lists them, and the order
// is load-bearing rather than editorial. Consider "<table><div>a</div><td>b</td>
// </table>": the missing-child-wrapper rule sweeps the div and the cell into one
// anonymous row, because they are consecutive children that are not proper table
// children, and the div then becomes an anonymous cell beside b. Run the
// missing-parent rule first and the cell gets a row of its own, the div gets
// another, and the two end up on separate lines of the table.
func (b *boxBuilder) fixupTables(parent *Box) []*Box {
	kids := parent.Children
	if len(kids) == 0 || !tableFixupNeeded(parent, kids) {
		return kids
	}
	kids = dropIrrelevantTableBoxes(parent, kids)
	kids = b.generateMissingChildren(parent, kids)
	kids = b.generateMissingParents(parent, kids)
	return kids
}

// dropIrrelevantTableBoxes is §17.2.1's first group: the boxes that are treated
// as though they had "display: none".
func dropIrrelevantTableBoxes(parent *Box, kids []*Box) []*Box {
	switch parent.Inner {
	case InnerTableColumn:
		// A column describes a column; it has no content of its own and anything
		// written inside it is dropped. <col> is empty in HTML, so this is
		// reached through "display: table-column" on an element that has
		// children — which is exactly the case where an engine that laid them
		// out would draw the content of every column on top of the table.
		return nil
	case InnerTableColumnGroup:
		var out []*Box
		for _, c := range kids {
			if c.Inner == InnerTableColumn {
				out = append(out, c)
			}
		}
		return out
	}

	// White space between table structure. The specification states this as two
	// rules, one about a tabular container's children and one about a box
	// between two internal table siblings; what is here is the union, which is
	// the behaviour every browser has and is simpler to be sure of.
	//
	// The knowing divergence: the specification qualifies the first rule with
	// "and its immediately preceding and following siblings, if any, are proper
	// table descendants", so white space alone inside an otherwise empty <tr>
	// would generate a cell. It does not here, and it does not in any browser —
	// "<tr>\n</tr>" is a row with no cells, not a row with one empty one.
	drop := make([]bool, len(kids))
	inContainer := isTableInternalContainer(parent)
	for i, c := range kids {
		if !c.IsText() || !isCollapsibleText(c) {
			continue
		}
		if inContainer {
			drop[i] = true
			continue
		}
		// Between two internal table boxes, wherever that happens: it is what
		// makes two rows written on separate lines of a <div> consecutive, which
		// is what lets the missing-parent rule gather them into one table
		// instead of two.
		if before, ok := previousBox(kids, i, drop); ok && (isInternalTableBox(before) || before.Inner == InnerTableCaption) {
			if after, ok := nextBox(kids, i); ok && (isInternalTableBox(after) || after.Inner == InnerTableCaption) {
				drop[i] = true
			}
		}
	}

	out := kids[:0:0]
	for i, c := range kids {
		if !drop[i] {
			out = append(out, c)
		}
	}
	return out
}

// isCollapsibleText reports whether a text box is white space that the table
// rules may throw away.
//
// Collapsing has already run, so "\n    " between two cells is a single space
// here — but "white-space: pre" preserves it verbatim, and text the author
// asked to be preserved is content whatever it is made of.
func isCollapsibleText(b *Box) bool {
	switch b.Style["white-space"] {
	case "pre", "pre-wrap", "break-spaces":
		return false
	}
	for _, r := range b.Text {
		switch r {
		case ' ', '\t', '\n', '\r', '\f':
		default:
			return false
		}
	}
	return true
}

// previousBox and nextBox find the neighbours a rule is stated against, skipping
// the boxes already condemned so that two runs of white space either side of a
// row do not each keep the other alive.
func previousBox(kids []*Box, i int, drop []bool) (*Box, bool) {
	for j := i - 1; j >= 0; j-- {
		if drop[j] {
			continue
		}
		return kids[j], true
	}
	return nil, false
}

func nextBox(kids []*Box, i int) (*Box, bool) {
	if i+1 < len(kids) {
		return kids[i+1], true
	}
	return nil, false
}

// generateMissingChildren is §17.2.1's second group: a container that holds the
// wrong kind of child grows the box that should have been between them.
func (b *boxBuilder) generateMissingChildren(parent *Box, kids []*Box) []*Box {
	var wrap Inner
	var belongs func(*Box) bool
	switch {
	case isTableRoot(parent):
		wrap, belongs = InnerTableRow, isProperTableChild
	case isRowGroup(parent):
		wrap = InnerTableRow
		belongs = func(c *Box) bool { return c.Inner == InnerTableRow }
	case parent.Inner == InnerTableRow:
		wrap = InnerTableCell
		belongs = func(c *Box) bool { return c.Inner == InnerTableCell }
	default:
		return kids
	}

	var out []*Box
	for i := 0; i < len(kids); {
		if belongs(kids[i]) {
			out = append(out, kids[i])
			i++
			continue
		}
		j := i
		for j < len(kids) && !belongs(kids[j]) {
			j++
		}
		out = append(out, b.anonymousTableBox(parent, wrap, kids[i:j]))
		i = j
	}
	return out
}

// generateMissingParents is §17.2.1's third group: structure found outside the
// table it belongs to grows the table around it.
//
// It runs in two steps because a stray cell needs two boxes built over it, and
// building them in one pass would mean deciding how far a run reaches while the
// run is still changing shape. The first step turns runs of cells into rows; the
// second sweeps every misparented piece of table into an anonymous table, by
// which time the cells have become rows and there is one kind of thing to sweep.
func (b *boxBuilder) generateMissingParents(parent *Box, kids []*Box) []*Box {
	if parent.Inner != InnerTableRow {
		kids = b.wrapRuns(parent, kids, InnerTableRow,
			func(c *Box) bool { return c.Inner == InnerTableCell },
			func(c *Box) bool { return c.Inner == InnerTableCell })
	}

	// A proper table child outside a table. Which ones count as misparented
	// depends on what they are, because the legal parents differ: a row may sit
	// in a row group or in a table, a column in a column group or a table, and
	// everything else only in a table.
	misparented := func(c *Box) bool {
		switch c.Inner {
		case InnerTableRow:
			return !isRowGroup(parent) && !isTableRoot(parent)
		case InnerTableColumn:
			return parent.Inner != InnerTableColumnGroup && !isTableRoot(parent)
		case InnerTableRowGroup, InnerTableColumnGroup, InnerTableCaption:
			return !isTableRoot(parent)
		}
		return false
	}
	// The run that gets swept in with the first misparented box is the internal
	// table boxes after it — not every proper table child. A caption may *start*
	// a run and may not extend one, which is what stops two tables written side
	// by side, each with a caption, from being merged into one.
	return b.wrapRuns(parent, kids, InnerTable, misparented, isInternalTableBox)
}

// wrapRuns gathers each maximal run that starts at a box satisfying start and
// continues while extend holds, and puts an anonymous box of the given kind
// around it.
func (b *boxBuilder) wrapRuns(parent *Box, kids []*Box, kind Inner,
	start, extend func(*Box) bool) []*Box {

	var out []*Box
	for i := 0; i < len(kids); {
		if !start(kids[i]) {
			out = append(out, kids[i])
			i++
			continue
		}
		j := i + 1
		for j < len(kids) && extend(kids[j]) {
			j++
		}
		out = append(out, b.anonymousTableBox(parent, kind, kids[i:j]))
		i = j
	}
	return out
}

// anonymousTableBox builds one of the boxes §17.2.1 inserts.
//
// The children handed to it have already been through the rest of the fixup at
// their old level, but not through the part of it that belongs to their *new*
// parent — an anonymous cell is a block container that has just acquired a mixed
// run of block and inline children, and nothing has yet wrapped the inline ones.
// So a generated cell finishes its own children, which is the same two steps
// every other block container went through on the way here.
func (b *boxBuilder) anonymousTableBox(parent *Box, kind Inner, children []*Box) *Box {
	outer := OuterBlock
	if kind == InnerTable && parent.Outer == OuterInline && !isBlockContainer(parent) {
		// §17.2.1: an anonymous table generated inside an inline box is an
		// inline-table.
		outer = OuterInline
	}
	box := &Box{
		Outer: outer, Inner: kind,
		Style:    style.Inherited(parent.Style),
		FontSize: parent.FontSize,
		Parent:   parent,
		Children: append([]*Box(nil), children...),
	}
	if !b.roomAt(offsetOf(parent)) {
		// The cap has been reached. The children keep their old parent rather
		// than being dropped: a box tree missing a wrapper lays out wrongly, and
		// one missing the content lays out empty.
		return box
	}
	for _, c := range box.Children {
		c.Parent = box
	}
	// A generated box has the same problem its parent just had, one level down,
	// and it is the same rule that fixes it. A row generated around a run of
	// stray content still owes each of them a cell; a cell generated around a
	// stray row group still owes it a table. Both were found by tests that
	// expected nothing to be drawn and got a red box: "<div display:table-row>
	// <div display:table-row-group>" is a row group in an anonymous cell, and a
	// row group with no table around it is a box with a background and no area.
	box.Children = b.fixupTables(box)
	for _, c := range box.Children {
		c.Parent = box
	}
	if kind == InnerTableCell {
		// And a cell is a block container, which the two flow rules have not
		// reached: it has just acquired a mixed run of block and inline children
		// and nothing has wrapped the inline ones.
		box.Children = b.splitBlockInInline(box)
		box.Children = b.wrapInlines(box)
	}
	return box
}

// wrapTables generates §17.4's table wrapper box, in one pass over the finished
// tree.
//
// # Why a table needs two boxes
//
// The properties an author writes on a <table> divide in two. Some describe the
// grid — its width, its borders, its background, the spacing between its cells —
// and some describe where the whole thing sits among its siblings: its margins,
// its float, its position. A caption is not in the grid and is not a sibling of
// the table either; it is above or below it and as wide as it is.
//
// One box cannot be both. The caption has to be inside something for "as wide as
// the table" to mean anything, and it has to be outside the table's border box
// or the table's own border would be drawn around it. §17.4's answer is an
// anonymous box holding the captions and the table, with the positioning half of
// the properties moved onto it — and that is what this builds.
//
// It runs as a separate pass rather than during fixup because a table generated
// by §17.2.1 needs a wrapper as much as one the author wrote, and generating
// wrappers while still generating tables is a rule that has to reason about
// boxes that do not exist yet.
func (b *boxBuilder) wrapTables(box *Box) *Box {
	for i, c := range box.Children {
		c = b.wrapTables(c)
		c.Parent = box
		box.Children[i] = c
	}
	if !isTableRoot(box) {
		return box
	}
	return b.tableWrapper(box)
}

// tableWrapper builds the anonymous box of §17.4 around a table.
func (b *boxBuilder) tableWrapper(table *Box) *Box {
	if !b.roomAt(offsetOf(table)) {
		return table
	}
	// The wrapper inherits, like every anonymous box, and then takes the
	// properties §17.4 assigns to it. Copying them rather than sharing the
	// table's computed style is what keeps the table's border, padding and
	// background from being painted twice, once by each box.
	cs := style.Inherited(table.Style)
	for _, name := range wrapperProperties {
		if v, ok := table.Style[name]; ok {
			cs[name] = v
		}
	}

	wrapper := &Box{
		Outer: table.Outer, Inner: InnerFlowRoot,
		Style: cs, FontSize: table.FontSize,
		Float: table.Float, Clear: table.Clear,
		Position: table.Position, ZIndex: table.ZIndex, ZAuto: table.ZAuto,
		Order:  table.Order,
		Parent: table.Parent,
	}
	wrapper.TableWrapper = true

	// The table itself is no longer the box that floats, is positioned or has
	// margins. Leaving any of those on it would apply them twice — the wrapper
	// would move and the table would move again inside it.
	table.Float, table.Clear = FloatNone, ClearNone
	table.Position, table.ZIndex, table.ZAuto = PositionStatic, 0, true
	table.Outer = OuterBlock
	table.Parent = wrapper

	// The captions come out of the table and sit either side of it, which is the
	// whole of what caption-side does once the wrapper exists: block layout puts
	// the boxes in the order they are in.
	var above, below, grid []*Box
	for _, c := range table.Children {
		if c.Inner != InnerTableCaption {
			grid = append(grid, c)
			continue
		}
		c.Parent = wrapper
		if captionAtBottom(c) {
			below = append(below, c)
		} else {
			above = append(above, c)
		}
	}
	table.Children = grid

	wrapper.Children = append(wrapper.Children, above...)
	wrapper.Children = append(wrapper.Children, table)
	wrapper.Children = append(wrapper.Children, below...)
	return wrapper
}

// wrapperProperties are the ones §17.4 says are used on the wrapper and not on
// the table.
//
// "clear" is not in the specification's list and is here anyway: it is the
// property that decides where a floated neighbour lets the box start, which is a
// question about where the whole table goes and is meaningless applied to a grid
// inside a wrapper that has already been placed. Every browser does the same.
var wrapperProperties = []string{
	"position", "float", "clear", "z-index",
	"margin-top", "margin-right", "margin-bottom", "margin-left",
	"top", "right", "bottom", "left",
}

// captionAtBottom reads caption-side.
//
// Only "bottom" moves it; "top" is the initial value and anything else is a
// value the cascade should already have rejected.
func captionAtBottom(b *Box) bool {
	return b.Style["caption-side"] == "bottom"
}
