package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/style"
)

// An inline box's own margin, border and padding on the horizontal axis.
//
// The numbers here are Courier's, because Courier is the one standard face whose
// advance is the same for every character: 600 units of 1000, so at 20px every
// character is 12px wide and a four-letter word is 48. That makes every expected
// position below a sum of the declaration and a multiple of twelve, derived
// rather than recorded.

// courier is the stylesheet these tests set their text in.
const courier = noDefaults + `
body, div, p, span { font-family: Courier; font-size: 20px; line-height: 20px }
`

// allLines is every line box under a fragment, in document order.
//
// A block whose children include a block has its inline content in anonymous
// boxes rather than on itself, which is exactly the shape a block inside an
// inline produces — so a test about a split inline cannot look at one fragment's
// own lines.
func allLines(f *Fragment) []LineFragment {
	var out []LineFragment
	var walk func(*Fragment)
	walk = func(cur *Fragment) {
		if cur == nil {
			return
		}
		out = append(out, cur.Lines...)
		for _, c := range cur.Children {
			walk(c)
		}
	}
	walk(f)
	return out
}

// runX returns the X of the first run whose text is exactly want, anywhere under
// the given element.
func runX(t *testing.T, root *Fragment, id, want string) float64 {
	t.Helper()
	lines := allLines(find(t, root, id))
	if len(lines) == 0 {
		t.Fatalf("#%s produced no lines", id)
	}
	for _, line := range lines {
		for _, r := range line.Runs {
			if r.Text == want {
				return r.X.Px()
			}
		}
	}
	t.Fatalf("#%s has no run %q; it has %v", id, want, lineTexts(lines))
	return 0
}

// TestInlineMarginPushesTheText is §8.3: margin-left applies to a non-replaced
// inline box, and what it does is move the content along.
//
// It is the fault the whole of insetItems was written for, and it was found in
// the WPT suite rather than here: 34 tests in css/CSS2/text check letter-spacing
// and word-spacing by drawing the same picture twice, once with the property and
// once with an equivalent margin on an inline box. The engine had the properties
// right and the margin missing, so the tests read as spacing failures.
func TestInlineMarginPushesTheText(t *testing.T) {
	root := layoutOf(t, 1000,
		`<p id="p"><span>ab</span><span style="margin-left: 30px">cd</span></p>`, courier)
	// "ab" is two Courier characters at 20px: 2 x 12 = 24. The second span
	// starts 30 further on.
	if got := runX(t, root, "p", "cd"); got != 24+30 {
		t.Errorf("the text after a 30px inline margin is at %g, want 54", got)
	}
}

// TestInlineBorderAndPaddingPushTheText is §8.4 and §8.5, which say the same
// thing about the same axis. All three add, which is the arithmetic a test in
// the suite most often checks.
func TestInlineBorderAndPaddingPushTheText(t *testing.T) {
	root := layoutOf(t, 1000, `<p id="p"><span id="s">ab</span>cd</p>`, courier+`
		#s { margin-left: 1px; border-left: 2px solid black; padding-left: 4px;
		     margin-right: 8px; border-right: 16px solid black; padding-right: 32px }
	`)
	// The leading edge is 1 + 2 + 4 = 7, so "ab" starts there.
	if got := runX(t, root, "p", "ab"); got != 7 {
		t.Errorf("the text inside the box is at %g, want 7", got)
	}
	// And the trailing edge is 8 + 16 + 32 = 56 past the end of "ab".
	if got := runX(t, root, "p", "cd"); got != 7+24+56 {
		t.Errorf("the text after the box is at %g, want 87", got)
	}
}

// TestInlineBorderNeedsAStyle pins that a border width with no style occupies
// nothing, which is the rule every other border in this engine follows and the
// one that would make this arithmetic wrong everywhere if it were forgotten
// here.
func TestInlineBorderNeedsAStyle(t *testing.T) {
	root := layoutOf(t, 1000, `<p id="p"><span style="border-left-width: 50px">ab</span></p>`,
		courier)
	if got := runX(t, root, "p", "ab"); got != 0 {
		t.Errorf("a border width with no style moved the text to %g, want 0", got)
	}
}

// TestInlineInsetDropsTheSpaceAfterTheOpeningTag is §4.1.1's fourth rule
// reaching past the inset: the white space an author leaves after an opening tag
// collapses into the space before the box, and an inline box's own margin
// standing between the two does not make them two spaces.
//
// It is where css/CSS2/margin-padding-clear/margin-left-applies-to-008 went
// wrong in the first version of this work — four pixels out, which is exactly
// one Times space.
func TestInlineInsetDropsTheSpaceAfterTheOpeningTag(t *testing.T) {
	root := layoutOf(t, 1000,
		"<p id=\"p\">ab <span style=\"margin-left: 30px\">\n  cd</span></p>", courier)
	// "ab" is 24 and the space after it is 12, so the box begins at 36 and its
	// margin puts "cd" at 66. A second space would put it at 78.
	if got := runX(t, root, "p", "cd"); got != 66 {
		t.Errorf("the text is at %g, want 66 — the space after the opening tag "+
			"was set as a second space", got)
	}
}

// TestInlineInsetIsNotContentForOverflow is the other consumer of the same
// distinction, and the one that is reachable.
//
// A word too wide for the line is reported, because the part past the edge is
// not drawn and nothing else about the page says so. A line that holds only an
// inline box's margin holds no content, so the word after it is still the first
// thing on the line and still has to be reported.
func TestInlineInsetIsNotContentForOverflow(t *testing.T) {
	// The second word is nine Courier characters at 20px: 9 x 12 = 108, which is
	// wider than the line on its own. It sits behind a 60px margin, so the line
	// breaks before the box and the second line is the margin and the word — a
	// line whose only other occupant is not content.
	rec := NewRecorder(nil)
	in := Input{HTML: `<p id="p">abcd <span style="margin-left: 60px">abcdefghi</span></p>`,
		CSS: []Stylesheet{{Source: courier}}}
	got := Build(in)
	w, _ := style.FromPx(100)
	h, _ := style.FromPx(10000)
	Layout(got.Root, Size{W: w, H: h}, nil, rec)

	for _, f := range rec.Findings() {
		if f.Rule == RuleUnbreakableOverflow && strings.Contains(f.Message, "abcdefghi") {
			return
		}
	}
	t.Errorf("no overflow was reported for a word that cannot fit beside its own "+
		"margin; the findings were %v", rec.Findings())
}

// TestInlineInsetDoesNotKeepATrailingSpace is §4.1.2's third rule reaching past
// the inset at the other end: a collapsible space at the end of a line goes,
// and a box closing after it does not save it.
//
// The assertion is on the runs rather than on the geometry, and that is the
// point of it. Written against a centred line's position it passed with the rule
// deleted, because alignedWidth discounts a trailing space from the alignment
// whether trimLineEdge removed it or not — two rules giving the same answer,
// which is the shape of test this repository has been caught by before. A
// removed space is a space that is not in the document's text, and that is what
// this checks.
func TestInlineInsetDoesNotKeepATrailingSpace(t *testing.T) {
	root := layoutOf(t, 1000,
		`<p id="p" style="text-align: center"><span style="margin-right: 40px">abcd </span></p>`,
		courier)
	lines := linesOf(t, root, "p")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lineTexts(lines))
	}
	if got := lineTexts(lines)[0]; got != "abcd" {
		t.Errorf("the line reads %q, want %q — the space before the closing "+
			"margin survived the edge", got, "abcd")
	}
	// And the geometry, since a space that was removed from the runs but still
	// counted would be a different fault: "abcd" is 4 x 12 = 48 and the margin is
	// 40, so the content is 88 wide and centring it in 1000 puts it at 456.
	if got := runX(t, root, "p", "abcd"); got != 456 {
		t.Errorf("the centred line starts at %g, want 456", got)
	}
}

// TestInlineInsetTakesTheBreakOpportunity pins where a line may end. A line may
// break before an inline box, taking its margin with it to the next line, and it
// may not break between that margin and the box's first word.
func TestInlineInsetTakesTheBreakOpportunity(t *testing.T) {
	// "abcd" is 48 wide. In a box 100 wide, "abcd abcd" fits (48+12+48 = 108
	// does not, so the second word wraps) — the point of interest is the second
	// line, which must begin with the margin and not with the word.
	root := layoutOf(t, 100,
		`<p id="p">abcd <span style="margin-left: 30px">abcd</span></p>`, courier)
	lines := linesOf(t, root, "p")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(lines), lineTexts(lines))
	}
	// The second line is the margin then the word: 30, not 0.
	if got := lines[1].Runs[0].X.Px(); got != 30 {
		t.Errorf("the word on the second line is at %g, want 30 — the margin did "+
			"not travel with the box", got)
	}
}

// TestInlineInsetWidensTheIntrinsicWidth pins that a box sized to its content
// counts the inset. A shrink-to-fit box that ignored it would be too narrow by
// exactly the margin, and its text would overflow the box that was sized to hold
// it.
func TestInlineInsetWidensTheIntrinsicWidth(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="d" style="float: left"><span style="margin-left: 30px">abcd</span></div>`,
		courier)
	// 4 x 12 = 48 of text plus 30 of margin.
	if got := find(t, root, "d").BorderRect.W.Px(); got != 78 {
		t.Errorf("the float is %g wide, want 78", got)
	}
}

// TestInlineInsetSlicesOverABlock is §8.6's slice model: an inline box a block
// splits carries its left inset on the piece it begins with and its right on the
// piece it ends with, and nothing on the join.
//
// Without it every piece would carry both, which is the fault that cost this
// change 23 tests in the suite before it was found.
func TestInlineInsetSlicesOverABlock(t *testing.T) {
	root := layoutOf(t, 1000, `
		<div id="d" style="text-align: center"><span style="margin-left: 30px;
		margin-right: 40px">ab<div id="mid">x</div>cd</span></div>`, courier)
	// Centring is what makes both ends of each piece observable: a margin at the
	// end of a line moves nothing in left-aligned text, so a test written that
	// way would pass with the trailing rule deleted.
	//
	// The first piece begins the box and does not end it, so its line is 30 + 24
	// wide. Centring 54 in 1000 starts it at 473, and "ab" sits 30 further on.
	if got := runX(t, root, "d", "ab"); got != 473+30 {
		t.Errorf("the text before the block is at %g, want 503 — the piece carries "+
			"an inset it should not", got)
	}
	// The last piece ends the box and does not begin it, so its line is 24 + 40
	// wide. Centring 64 in 1000 starts it at 468, and "cd" is at the start of it.
	if got := runX(t, root, "d", "cd"); got != 468 {
		t.Errorf("the text after the block is at %g, want 468 — the piece carries "+
			"an inset it should not", got)
	}
}

// TestInlineInsetSurvivesAnEmptyPiece is the other half of §8.6 and of §9.4.2:
// an inline box whose only child is a block still has a fragment on each side of
// it, and the box's own inset belongs to those fragments.
func TestInlineInsetSurvivesAnEmptyPiece(t *testing.T) {
	// #x is the block, and what is asserted is where it sits: the empty leading
	// piece is a line box above it, so the block starts a line's height down
	// rather than at the top of the container. §9.4.2 makes a line box holding
	// an inline box with a non-zero margin a line box that exists.
	root := layoutOf(t, 1000,
		`<div id="d"><span style="margin-left: 30px"><div id="x">ab</div></span></div>`,
		courier)
	if got := find(t, root, "x").BorderRect.Y.Px(); got != 20 {
		t.Errorf("the block is at y=%g, want 20 — the empty leading piece produced "+
			"no line box for its own margin", got)
	}
}

// TestEmptyInlineWithNoInsetDoesNotExist is §9.4.2's other direction, and it is
// the rule that keeps the fragment above from costing a margin collapse: a line
// box holding nothing but an inline box with zero margins, borders and padding
// "must be treated as not existing".
//
// It is checked through margin collapsing because that is where it is
// observable, and because three tests in css/CSS2/box-display went red when this
// was got wrong.
func TestEmptyInlineWithNoInsetDoesNotExist(t *testing.T) {
	root := layoutOf(t, 1000, `
		<div id="d"><span><div id="a" style="margin-bottom: 40px">x</div></span>
		<div id="b" style="margin-top: 30px">y</div></div>`, courier)
	a, b := find(t, root, "a"), find(t, root, "b")
	// The two margins collapse to the larger, so #b's top is 40 below #a's
	// bottom rather than 70 — and rather than being pushed further by a line box
	// for the empty piece between them.
	gap := b.BorderRect.Y.Sub(a.BorderRect.Y.Add(a.BorderRect.H)).Px()
	if gap != 40 {
		t.Errorf("the gap between the two blocks is %g, want 40 — the empty inline "+
			"piece stood between them", gap)
	}
}

// TestInlineInsetIsNotDrawn pins that an inset produces no run.
//
// A run with no text would reach the content stream as an empty text-showing
// operator, and reach a reader extracting the page as a piece of text that is
// not there.
func TestInlineInsetIsNotDrawn(t *testing.T) {
	root := layoutOf(t, 1000, `<p id="p"><span style="margin-left: 30px">ab</span></p>`,
		courier)
	for _, line := range linesOf(t, root, "p") {
		for _, r := range line.Runs {
			if r.Text == "" {
				t.Errorf("a run with no text reached the line at x=%g", r.X.Px())
			}
		}
	}
}

// TestInlineInsetRewindTerminates is the safety property on the rewind, and it
// is a property rather than a number because the failure it guards against is
// not a wrong answer but a document that never finishes.
//
// breakOneLine is the only place in this engine that returns a position it has
// already passed. It terminates because the position it returns is always
// *after* the line's start — the rewind is only armed once the line holds
// content, and content can only have been placed after the line began — so the
// next line begins strictly further on. That argument is short and the cost of
// it being wrong is a hang on an untrusted document, so it is checked against
// the shape most likely to break it: margins wider than the line, nested, with
// nothing between them that fits.
func TestInlineInsetRewindTerminates(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<p id="p">ab`)
	for i := 0; i < 200; i++ {
		b.WriteString(` <span style="margin-left: 500px; margin-right: 500px">cd`)
	}
	for i := 0; i < 200; i++ {
		b.WriteString(`</span>`)
	}
	b.WriteString(`</p>`)

	done := make(chan int, 1)
	go func() {
		root := layoutOf(t, 40, b.String(), courier)
		done <- len(linesOf(t, root, "p"))
	}()
	select {
	case n := <-done:
		if n == 0 {
			t.Error("the paragraph produced no lines")
		}
	case <-time.After(30 * time.Second):
		// A hang, not a slow machine: the whole reftest suite of five thousand
		// documents lays out in under ten seconds.
		t.Fatal("laying out 200 nested inline margins did not finish in 30 seconds")
	}
}

// TestASpentInsetRewindIsNotTakenAgain is the clearing of the inset rewind
// target once something that is not a margin has been placed.
//
// Two rewinds sit in front of the last resort, and they are tried in order: back
// to the inline box's opening margin, then back to the last break opportunity.
// The first is the earlier of the two once any opportunity has appeared after
// the box, so leaving it set would send the line further back than it has to go
// — and it is tried first, so it would win.
//
// The shape needed to see it is narrow. The item that does not fit has to be one
// that cannot begin a line of its own, or the plain break in front of a space
// ends the line before either rewind is reached; that is text written straight
// after a closing tag. And an opportunity has to fall between the box's margin
// and that text, or the two rewinds point at the same place and agree.
//
// Measured on the suite, dropping the clear moves the rewind from one firing to
// three, and both extra firings are lines like this one.
func TestASpentInsetRewindIsNotTakenAgain(t *testing.T) {
	// "xy " is 36, the margin 12, "ab" 24: exactly the 72 the box holds. Then a
	// space, "cd", and "efgh" written against the closing tag, which is what
	// cannot begin a line.
	root := layoutOf(t, 600,
		`<div id="p">xy <span style="margin-left: 12px">ab cd</span>efgh</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 72px }`)
	got := lineTextsOf(t, root, "p")
	want := []string{"xy ab", "cdefgh"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %q — the line was sent back past the "+
			"nearest opportunity to a margin that had already been spent",
			len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q (all %q)", i, got[i], want[i], got)
		}
	}
}
