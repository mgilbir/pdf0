package render

import (
	"strings"
	"testing"
)

// line-clamp, CSS Overflow 4.
//
// Every width below is a character count in Courier, whose every glyph is 600
// units of 1000 — see the note in breakspaces_test.go — so the expected breaks
// are arithmetic rather than recordings. The ellipsis is one character wide in
// that face, which is what makes "nine characters less an ellipsis" eight.

// clampSrc is the suite's fixture for the interesting case: content that runs to
// four lines at nine characters, whose third line is a word too long to sit
// beside the mark.
const clampSrc = `<p id="p">1 2 3 4 5 6 7 8 9 unbreakable stuff</p>`

// TestLineClampStopsAfterItsLines is the property doing what it says, and saying
// that it did.
func TestLineClampStopsAfterItsLines(t *testing.T) {
	plain := lineTexts(linesOf(t, layoutOf(t, 10000, clampSrc, widthCSS(9, "")), "p"))
	if len(plain) != 4 {
		t.Fatalf("unclamped the text takes %d lines %q, want 4", len(plain), plain)
	}

	got := lineTexts(linesOf(t, layoutOf(t, 10000, clampSrc,
		widthCSS(9, "line-clamp: 3")), "p"))
	if len(got) != 3 {
		t.Fatalf("clamped to 3 the text takes %d lines %q", len(got), got)
	}
	if got[0] != plain[0] || got[1] != plain[1] {
		t.Errorf("the lines before the last are %q, want the unclamped %q",
			got[:2], plain[:2])
	}
	// The third line's word is eleven characters and the room beside the mark is
	// eight, so nothing of it is shown — the mark is what stands in its place.
	if got[2] != "…" {
		t.Errorf("the clamped line is %q, want the ellipsis alone: "+
			"\"unbreakable\" does not fit beside it", got[2])
	}
}

// TestLineClampSaysNothingWhenNothingIsDiscarded is the difference between the
// property and a truncation.
//
// A block clamped to more lines than it has is not clamped, and marking it would
// tell the reader something was cut off when nothing was.
func TestLineClampSaysNothingWhenNothingIsDiscarded(t *testing.T) {
	// Fewer lines than the clamp allows.
	for _, clamp := range []string{"", "line-clamp: 3"} {
		got := lineTexts(linesOf(t, layoutOf(t, 10000,
			`<p id="p">one two</p>`, widthCSS(9, clamp)), "p"))
		if len(got) != 1 || got[0] != "one two" {
			t.Errorf("{%s} gave %q, want one line of the whole text", clamp, got)
		}
	}
	// And exactly as many, which is the case that can go wrong on its own: the
	// clamp's last line is reached, and it is the mark rather than the count
	// that must not appear.
	const exact = `<p id="p">1 2 3 4 5 6 7 8 9 0 1 2 3</p>`
	plain := lineTexts(linesOf(t, layoutOf(t, 10000, exact, widthCSS(9, "")), "p"))
	if len(plain) != 3 {
		t.Fatalf("the fixture takes %d lines %q, want exactly 3", len(plain), plain)
	}
	got := lineTexts(linesOf(t, layoutOf(t, 10000, exact,
		widthCSS(9, "line-clamp: 3")), "p"))
	if len(got) != 3 {
		t.Fatalf("clamped to its own line count it takes %d lines %q", len(got), got)
	}
	for i := range plain {
		if got[i] != plain[i] {
			t.Errorf("line %d is %q, want %q — nothing was discarded, so nothing "+
				"is marked and no room is held back for a mark", i, got[i], plain[i])
		}
	}
}

// TestThePrefixedClampNeedsItsCompany is CSS Overflow 4's compatibility section,
// which defines the legacy behaviour as a trio rather than as a property.
//
// "-webkit-line-clamp" on an ordinary block asks for nothing, and browsers give
// it nothing — the declaration only ever worked alongside "display: -webkit-box"
// and "-webkit-box-orient: vertical". Reading it on its own would clamp
// documents that have carried the line around for years without it doing
// anything.
func TestThePrefixedClampNeedsItsCompany(t *testing.T) {
	lines := func(css string) []string {
		return lineTexts(linesOf(t, layoutOf(t, 10000, clampSrc, widthCSS(9, css)), "p"))
	}
	if got := lines("-webkit-line-clamp: 3"); len(got) != 4 {
		t.Errorf("the prefixed property alone clamped to %d lines %q, want 4 — "+
			"it is not a clamp without the two declarations that made it one",
			len(got), got)
	}
	if got := lines(`-webkit-line-clamp: 3; display: -webkit-box;
		-webkit-box-orient: vertical; overflow: hidden`); len(got) != 3 {
		t.Errorf("the prefixed trio gave %d lines %q, want 3", len(got), got)
	}
	// And the orientation is part of it: the horizontal box is the old flexbox
	// laying its children in a row, which the clamp never applied to.
	if got := lines(`-webkit-line-clamp: 3; display: -webkit-box;
		-webkit-box-orient: horizontal; overflow: hidden`); len(got) != 4 {
		t.Errorf("a horizontal -webkit-box clamped to %d lines %q, want 4",
			len(got), got)
	}
}

// TestTheEllipsisIsTheBlocksOwnSize: §"block-ellipsis" puts the mark in the
// block container, not in whichever inline box the last line happened to end
// inside.
//
// The suite's line-clamp-002 turns on it — a block four times the size of the
// span inside it, where the mark is four characters of the span's text wide —
// and the difference is invisible in any document that sets one size
// throughout.
func TestTheEllipsisIsTheBlocksOwnSize(t *testing.T) {
	// The block is at 100px and the span at 25px, so the mark is four of the
	// span's characters wide. Nine of the span's characters fit in the block's
	// 2.25 characters of width; the mark takes four of them.
	root := layoutOf(t, 10000,
		`<p id="p"><span>aaaa bbbb cccc dddd</span></p>`,
		noDefaults+mono+`p { width: `+itoa(int(9*ch/4))+`px; line-clamp: 1 }
		span { font-size: 25px }`)
	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1", len(lines))
	}
	var mark *TextRun
	for i := range lines[0].Runs {
		if lines[0].Runs[i].Text == "…" {
			mark = &lines[0].Runs[i]
		}
	}
	if mark == nil {
		t.Fatal("no ellipsis on the clamped line")
	}
	if got := mark.Size.Px(); got != 100 {
		t.Errorf("the ellipsis is set at %gpx, want the block's 100 — it belongs "+
			"to the block container and not to the span the line ended in", got)
	}
}

// TestBalancingAClampedBlockKeepsWhatIsShown is §5.1 meeting the clamp, and the
// rule is not the one balancing uses on its own.
//
// The clamp has already decided how many lines there are, so "the narrowest
// width that keeps the line count" asks nothing — every width gives three. What
// must not change is how much of the content is *shown*: balancing evens the
// lines out, it does not throw more away. So the narrowest width that reaches as
// far into the text is the answer, and it can reach further — the suite's
// line-clamp-003 shows more text balanced than unbalanced, because a narrower
// measure lets the last line hold something beside the mark.
func TestBalancingAClampedBlockKeepsWhatIsShown(t *testing.T) {
	plain := lineTexts(linesOf(t, layoutOf(t, 10000, clampSrc,
		widthCSS(9, "line-clamp: 3")), "p"))
	if len(plain) != 3 || plain[2] != "…" {
		t.Fatalf("the unbalanced clamp gave %q, want three lines ending in the "+
			"mark alone", plain)
	}
	got := lineTexts(linesOf(t, layoutOf(t, 10000, clampSrc,
		widthCSS(9, "line-clamp: 3; text-wrap-style: balance")), "p"))
	want := []string{"1 2 3", "4 5 6", "7 8 9…"}
	if len(got) != len(want) {
		t.Fatalf("balanced and clamped gave %d lines %q, want %q", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q — balancing evens the lines and "+
				"shows at least what the full width showed", i, got[i], want[i])
		}
	}
}

// The clamp across a subtree, which is what CSS Overflow 4 actually specifies:
// "its line-based clamp point is set to the first possible clamp point after its
// Nth descendant in-flow line box". Every test below has a clamp container whose
// lines are not all its own, which is the case the property is usually written
// for — a card with a heading and a paragraph, cut to a fixed number of lines.

// monoBlocks is the fixture stylesheet: Courier at 100px, so a character is 60px
// wide and a width is a character count, exactly as in the tests above.
const monoBlocks = noDefaults +
	`div { font-size: 100px; font-family: Courier; line-height: 100px }`

// subtreeLines is every line in a subtree, in document order.
//
// Each fragment in these fixtures holds either lines or block children and never
// both — the anonymous block boxes around a container's own text see to that —
// so emitting a fragment's lines before descending into it is document order.
func subtreeLines(f *Fragment) []LineFragment {
	out := append([]LineFragment(nil), f.Lines...)
	for _, kid := range f.Children {
		out = append(out, subtreeLines(kid)...)
	}
	return out
}

// TestTheClampCountsLinesInDescendants is the property's own wording, and the
// case a per-paragraph clamp cannot express.
//
// The container's three lines come from three different boxes: the first from
// the anonymous block around its own text, the second and third from the block
// nested inside it. A clamp that counted only the lines of the box that declares
// it would find one line where the property allows three, clamp nothing, and
// leave the two blocks after the clamp point on the page.
func TestTheClampCountsLinesInDescendants(t *testing.T) {
	const src = `<div id="c">1 2 3<div id="k">4 5 6 7 8 9 0</div>x y z</div>`
	css := monoBlocks + `#c { width: ` + itoa(9*int(ch)) + `px; line-clamp: 3 }`

	root := layoutOf(t, 10000, src, monoBlocks+`#c { width: `+itoa(9*int(ch))+`px }`)
	if got := lineTexts(subtreeLines(find(t, root, "c"))); len(got) != 4 {
		t.Fatalf("unclamped the fixture is %d lines %q, want 4", len(got), got)
	}

	root = layoutOf(t, 10000, src, css)
	got := lineTexts(subtreeLines(find(t, root, "c")))
	want := []string{"1 2 3", "4 5 6 7 8", "9 0…"}
	if len(got) != len(want) {
		t.Fatalf("clamped to 3 the subtree has %d lines %q, want %q", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q", i, got[i], want[i])
		}
	}
	// The mark is on a line the nested block made, so the clamp point fell
	// inside a descendant and not at a block boundary.
	if n := len(linesOf(t, root, "k")); n != 2 {
		t.Errorf("the nested block kept %d lines, want 2 — the clamp point is "+
			"inside it", n)
	}
}

// TestContentAfterTheClampPointIsDiscarded: "remaining content is fragmented
// away and neither rendered nor measured".
//
// Neither rendered nor measured, so the container is as tall as the lines it
// shows. A box that merely hid the overflow would be the full height with the
// rest painted outside it, which is a different rendering the moment anything
// below it is in the flow.
func TestContentAfterTheClampPointIsDiscarded(t *testing.T) {
	const src = `<div id="c">1 2 3<div id="k">4 5 6 7 8 9 0</div><div id="gone">x y z</div></div>` +
		`<div id="after">tail</div>`
	root := layoutOf(t, 10000, src,
		monoBlocks+`#c { width: `+itoa(9*int(ch))+`px; line-clamp: 3 }`)

	if fragmentFor(root, "gone") != nil {
		t.Errorf("the block after the clamp point was laid out, want no fragment "+
			"at all:\n%s", sketchFragments(root))
	}
	if h := find(t, root, "c").BorderRect.H.Px(); h != 300 {
		t.Errorf("the clamped container is %gpx tall, want 300 — three lines of "+
			"100px and nothing measured for what was discarded", h)
	}
	if y := find(t, root, "after").BorderRect.Y.Px(); y != 300 {
		t.Errorf("the block after the container starts at y=%g, want 300", y)
	}
}

// TestTheClampCountsNoLineInAFloat is the "in-flow" in "Nth descendant in-flow
// line box".
//
// A float met on the container's first line is laid out there and then, so its
// lines are made before most of the container's own. Counting them spends the
// whole allowance on content the property does not describe, and a container
// clamped to three lines that holds a four-line float shows none of its own text
// at all.
func TestTheClampCountsNoLineInAFloat(t *testing.T) {
	const src = `<div id="c"><div id="f">a b c d</div>` +
		`1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4</div>`
	root := layoutOf(t, 10000, src, monoBlocks+
		`#c { width: `+itoa(20*int(ch))+`px; line-clamp: 3 }
		 #f { float: left; width: `+itoa(int(ch))+`px }`)

	if n := len(linesOf(t, root, "f")); n != 4 {
		t.Fatalf("the float has %d lines, want 4 — the fixture needs it to be "+
			"able to exhaust the clamp on its own", n)
	}
	own := lineTexts(find(t, root, "c").Lines)
	if len(own) != 3 {
		t.Errorf("the container shows %d lines of its own text %q, want 3",
			len(own), own)
	}
}

// TestTheMarkIsTheContainersFontOnADescendantsLine extends the rule of
// TestTheEllipsisIsTheBlocksOwnSize to the line the clamp really lands on.
//
// §"block-ellipsis" wraps the mark "in an anonymous inline whose parent is the
// block container's root inline box" — the *container's*, which once the count
// runs across descendants is not the box whose line the mark is drawn on.
func TestTheMarkIsTheContainersFontOnADescendantsLine(t *testing.T) {
	root := layoutOf(t, 10000,
		`<div id="c"><div id="k">aaaa bbbb cccc dddd</div></div>`,
		monoBlocks+`#c { width: `+itoa(int(9*ch/4))+`px; line-clamp: 1 }
		 #k { font-size: 25px }`)

	lines := linesOf(t, root, "k")
	if len(lines) != 1 {
		t.Fatalf("the nested block has %d lines, want 1", len(lines))
	}
	var mark *TextRun
	for i := range lines[0].Runs {
		if lines[0].Runs[i].Text == "…" {
			mark = &lines[0].Runs[i]
		}
	}
	if mark == nil {
		t.Fatal("no ellipsis on the clamped line of the nested block")
	}
	if got := mark.Size.Px(); got != 100 {
		t.Errorf("the mark is set at %gpx, want the container's 100 — the line "+
			"it sits on belongs to the nested block, the mark does not", got)
	}
}

// TestBalancingIsMeasuredAgainstTheClampsOwnLineCount is the trap the two-pass
// count sets for itself.
//
// The count has to lay the container out once to discover whether anything is
// cut, and to see the line that overflows it must be allowed one line more than
// the clamp. That extra line is for *stopping* and nothing else: §5.1 balances
// content into the lines it is allowed, so a balancer told it has four lines
// when the property allows three spreads the text into four, and the box is then
// found to overflow a clamp it fits.
//
// The suite states it as line-clamp-006, whose content balances into exactly the
// two lines it is clamped to and must therefore show no mark at all.
func TestBalancingIsMeasuredAgainstTheClampsOwnLineCount(t *testing.T) {
	const src = `<p id="p">1 2 3 4 5 6 7 8 9 0 1 2</p>`
	plain := lineTexts(linesOf(t, layoutOf(t, 10000, src, widthCSS(9, "")), "p"))
	if len(plain) != 3 {
		t.Fatalf("the fixture is %d lines %q unclamped, want exactly 3 so that a "+
			"clamp of three cuts nothing", len(plain), plain)
	}

	got := lineTexts(linesOf(t, layoutOf(t, 10000, src,
		widthCSS(9, "line-clamp: 3; text-wrap-style: balance")), "p"))
	if len(got) != 3 {
		t.Fatalf("balanced under a clamp of three the text is %d lines %q, want 3",
			len(got), got)
	}
	for i, line := range got {
		if strings.Contains(line, "…") {
			t.Errorf("line %d is %q — nothing was cut, so no mark belongs on the "+
				"page", i, line)
		}
	}
}

// TestTheClampCountsNoLineInAnAtomicInline is the other half of the count, and
// it is arithmetic rather than a quotation.
//
// An inline-block sits *on* a line box, and that line box is counted. Counting
// the lines inside it as well charges the clamp twice for one band of the page:
// a container clamped to three lines whose first line holds a four-line
// inline-block would be over its allowance before the line it is on is finished.
func TestTheClampCountsNoLineInAnAtomicInline(t *testing.T) {
	const src = `<div id="c"><span id="s">a b c d</span> ` +
		`1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6</div>`
	root := layoutOf(t, 10000, src, monoBlocks+
		`#c { width: `+itoa(20*int(ch))+`px; line-clamp: 3 }
		 #s { display: inline-block; width: `+itoa(int(ch))+`px }`)

	if n := len(linesOf(t, root, "s")); n != 4 {
		t.Fatalf("the inline-block has %d lines, want 4 — the fixture needs it to "+
			"be able to exhaust the clamp on its own", n)
	}
	if own := find(t, root, "c").Lines; len(own) != 3 {
		t.Errorf("the container shows %d lines, want 3", len(own))
	}
}

// TestTheClampCountsNoLineInATable is a limit of this engine stated as a rule,
// and the honest way to state it.
//
// A table's lines are descendant in-flow line boxes and the property's wording
// counts them. What the property then asks for is a clamp point among them, and
// this engine cannot fragment a table row — so counting them would spend the
// allowance on lines it has no way to cut and discard whatever followed the
// table instead. Leaving the table out of the count keeps the clamp acting only
// where it can act.
func TestTheClampCountsNoLineInATable(t *testing.T) {
	const src = `<div id="c"><table><tr><td>a<br>b<br>c<br>d</td></tr></table>` +
		`<div id="after">1 2 3</div></div>`
	root := layoutOf(t, 10000, src,
		monoBlocks+`#c { width: `+itoa(20*int(ch))+`px; line-clamp: 3 }`)

	if fragmentFor(root, "after") == nil {
		t.Errorf("the block after a four-line table was discarded by a clamp of "+
			"three, which spent the allowance on lines it cannot cut:\n%s",
			sketchFragments(root))
	}
}

// fragmentFor is find without the failure, for asserting that a box was *not*
// laid out.
func fragmentFor(f *Fragment, id string) *Fragment {
	if f == nil {
		return nil
	}
	if f.Box != nil && f.Box.Element != nil {
		if got, _ := f.Box.Element.Attr("id"); got == id {
			return f
		}
	}
	for _, c := range f.Children {
		if out := fragmentFor(c, id); out != nil {
			return out
		}
	}
	return nil
}
