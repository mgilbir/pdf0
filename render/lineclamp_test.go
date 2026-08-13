package render

import "testing"

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
