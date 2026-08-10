package render

import "testing"

// Where a line may break around an atomic inline.
//
// CSS Text §5.1 settles it in one sentence — "for the purpose of line breaking,
// an atomic inline is treated as U+FFFC OBJECT REPLACEMENT CHARACTER" — and UAX
// #14 does the rest: U+FFFC is class CB, and LB20 is "break before and after
// unresolved CB". So a picture is a unit of its own, never welded to the word
// beside it, and two pictures side by side wrap apart when they do not fit.
//
// The engine used to weld them. An atomic inline took whatever opportunity the
// content before it had created and offered none of its own, so "<img/><img/>"
// was one unbreakable object as wide as both and overflowed rather than wrapped.
// That is not a corner: it is how the CSS 2.1 reftests draw a two-row expected
// picture, with two full-width images and no space between them, and eleven of
// them in css/CSS2/positioning alone were failing because the *reference* laid
// out on one line while the test document was correct.
//
// LB20 has an exception that has to come with it, and it is the earlier rule
// rather than a special case: LB7 says not to break before a space. So the
// opportunity after a picture is not one a following space may take — the space
// stays with the picture and the break falls after the space, where LB18 puts
// it. Without that clause a float written after "<img/> " lands one line too far
// down, which is what css/CSS2/normal-flow/inlines-013 measures.
//
// The widths below are chosen so that no font metric enters any expected value:
// the boxes are inline-blocks with declared widths, and the assertions are about
// which line a box is on and whether it starts at the line's left edge.

// TestTwoAtomicInlinesWrapApart is LB20's plain case.
func TestTwoAtomicInlinesWrapApart(t *testing.T) {
	const css = noDefaults + `
	span { display: inline-block; width: 60px; height: 10px }`

	// Two 60px boxes in a 100px line. The first fits, the second does not, so
	// there are two lines and the second box begins one.
	root := layoutOf(t, 100, `<div id="d"><span id="a"></span><span id="b"></span></div>`, css)
	d, a, b := find(t, root, "d"), find(t, root, "a"), find(t, root, "b")
	if len(d.Lines) != 2 {
		t.Fatalf("the block has %d lines, want 2:\n%s", len(d.Lines), sketchFragments(root))
	}
	px(t, "the first box's left edge", a.BorderRect.X, 0)
	px(t, "the second box's left edge", b.BorderRect.X, 0)
	if !(b.BorderRect.Y > a.BorderRect.Y) {
		t.Errorf("the second box is at y=%.2f and the first at y=%.2f; it should be below",
			b.BorderRect.Y.Px(), a.BorderRect.Y.Px())
	}

	// The same two boxes in a line with room for both stay on one line, which is
	// what stops the rule above from being "always break".
	root = layoutOf(t, 200, `<div id="d"><span id="a"></span><span id="b"></span></div>`, css)
	d, a, b = find(t, root, "d"), find(t, root, "a"), find(t, root, "b")
	if len(d.Lines) != 1 {
		t.Fatalf("the block has %d lines, want 1:\n%s", len(d.Lines), sketchFragments(root))
	}
	px(t, "the second box beside the first", b.BorderRect.X, 60)
	if b.BorderRect.Y != a.BorderRect.Y {
		t.Errorf("the two boxes are on different lines, y=%.2f and y=%.2f",
			a.BorderRect.Y.Px(), b.BorderRect.Y.Px())
	}
}

// TestAtomicInlineWrapsAwayFromAdjacentText is LB20 across the other boundary:
// a picture written straight after a word, with no space between them, is still
// a unit of its own.
func TestAtomicInlineWrapsAwayFromAdjacentText(t *testing.T) {
	// A 99px box in a 100px line, preceded by a letter. Whatever the letter's
	// advance is — the assertion is deliberately independent of it — it is more
	// than the one pixel left over, so the box goes to the second line.
	root := layoutOf(t, 100, `<div id="d">x<span id="s"></span></div>`,
		noDefaults+"#s { display: inline-block; width: 99px; height: 10px }")
	d, s := find(t, root, "d"), find(t, root, "s")
	if len(d.Lines) != 2 {
		t.Fatalf("the block has %d lines, want 2:\n%s", len(d.Lines), sketchFragments(root))
	}
	px(t, "the box's left edge on its own line", s.BorderRect.X, 0)
	if s.BorderRect.Y <= d.Lines[0].Rect.Y {
		t.Errorf("the box is at y=%.2f, on the first line", s.BorderRect.Y.Px())
	}

	// And a word written straight after a picture leaves it, which is the same
	// rule read the other way: the box takes the whole first line and the text
	// begins the second at its left edge.
	root = layoutOf(t, 100, `<div id="d"><span id="s"></span>xx</div>`,
		noDefaults+"#s { display: inline-block; width: 99px; height: 10px }")
	d = find(t, root, "d")
	if len(d.Lines) != 2 {
		t.Fatalf("the block has %d lines, want 2:\n%s", len(d.Lines), sketchFragments(root))
	}
	if n := len(d.Lines[1].Runs); n != 1 {
		t.Fatalf("the second line has %d runs, want 1", n)
	}
	px(t, "the text's offset on the second line", d.Lines[1].Runs[0].X, 0)
}

// TestNoBreakBeforeASpaceAfterAnAtomicInline is LB7, which is the earlier rule
// and so the one that decides.
//
// Finding a document that can tell the two answers apart took two attempts, and
// the first is worth recording because it is the shape this repository keeps
// meeting. A collapsible space is what the rule is usually about, and a
// collapsible space that begins a line is removed by §4.1.2 — so with ordinary
// white space both answers put the next word at the left edge and a test written
// on that cannot fail. Making the space preserved with "pre-wrap" does not help
// either: a preserved space carries a break opportunity of its own, so the
// carried-in one changes nothing and the clause is still not what decides. That
// version was written, planted against, and found to pass with the clause
// deleted.
//
// What makes the difference observable is a float. A float takes no width on the
// line and does not break it, but it is placed against the line it was written
// on — so which line the space lands on decides where the float goes, and
// nothing about the space itself has to be visible. That is exactly what
// css/CSS2/normal-flow/inlines-013 measures, and it is the test that moved when
// the clause was added.
//
// The document below is that test's shape. The containing block is zero wide, so
// the inline-block overflows the only line there is and the 10px float cannot
// fit beside it — §9.5 puts the float below the line box. With LB7 the space
// stays on the picture's line and the float is immediately under it; without it
// the space begins a line of its own and the float is one line further down.
func TestNoBreakBeforeASpaceAfterAnAtomicInline(t *testing.T) {
	root := layoutOf(t, 100,
		`<div id="d"><span id="s"></span> <div id="f"></div></div>`,
		noDefaults+`#d { width: 0 }
		#s { display: inline-block; width: 50px; height: 40px }
		#f { float: left; width: 10px; height: 10px }`)
	d, f := find(t, root, "d"), find(t, root, "f")
	if len(d.Lines) != 1 {
		t.Fatalf("the block has %d lines, want 1:\n%s", len(d.Lines), sketchFragments(root))
	}
	// The float's top is the bottom of the one line box. Written as the line's
	// own bottom rather than as a number because the line's height comes from
	// the default face's metrics, and this rule is not about those.
	if got, want := f.BorderRect.Y, d.Lines[0].Rect.Bottom(); got != want {
		t.Errorf("the float's top is %.2fpx and the line box ends at %.2fpx; a "+
			"gap of a whole line is the space having started one",
			got.Px(), want.Px())
	}
}
