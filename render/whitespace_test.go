package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// CSS Text §4's white space processing, and the line breaking that finishes it.
//
// Every assertion here pins an exact string or an exact advance rather than a
// property of one — "the text looks collapsed" is true of the right answer and
// of several wrong ones, and this is the file where that shortcut costs most.
// The widths come from Courier, whose every glyph is 600 units of 1000 by the
// PDF specification's own metrics, so a length in these tests is a character
// count times the size times 0.6 and can be read rather than recorded.

// mono is a stylesheet setting a monospaced face at 100px, so that one character
// is exactly 60px and a line of them is countable.
const mono = `p { font-size: 100px; font-family: Courier; line-height: 100px }`

// ch is the advance of one Courier character at 100px.
const ch = 60.0

// runTexts renders a line's runs as they were placed, which is what white space
// processing decides and what lineTexts (which joins them) would hide.
func runTexts(line LineFragment) []string {
	out := make([]string, 0, len(line.Runs))
	for _, r := range line.Runs {
		out = append(out, r.Text)
	}
	return out
}

// lineWidth is the sum of a line's advances.
func lineWidth(line LineFragment) style.Unit {
	var w style.Unit
	for _, r := range line.Runs {
		w = w.Add(r.Width)
	}
	return w
}

// TestWhiteSpaceIsThreeIndependentBits pins the matrix of CSS Text §3.
//
// It is the table and not a paraphrase of it, because the six keywords are six
// combinations of three questions and an engine that gets one cell wrong gets it
// wrong in a way that reads as a different bug entirely: "pre-line does not
// wrap" looks like a broken line breaker, not like a misread keyword.
func TestWhiteSpaceIsThreeIndependentBits(t *testing.T) {
	cases := map[string]whiteSpace{
		"normal":       {collapse: true, wrap: true},
		"nowrap":       {collapse: true},
		"pre":          {preserveBreaks: true},
		"pre-wrap":     {preserveBreaks: true, wrap: true},
		"pre-line":     {collapse: true, preserveBreaks: true, wrap: true},
		"break-spaces": {preserveBreaks: true, wrap: true, breakSpaces: true},
		// Case and surrounding space are the cascade's, not this function's.
		"  PRE-Wrap ": {preserveBreaks: true, wrap: true},
		// Anything else is the initial value, which is what the cascade would
		// have used had the declaration been thrown out.
		"":            {collapse: true, wrap: true},
		"balance":     {collapse: true, wrap: true},
		"pre wrap":    {collapse: true, wrap: true},
		"break-space": {collapse: true, wrap: true},
	}
	for value, want := range cases {
		if got := whiteSpaceOf(value); got != want {
			t.Errorf("white-space:%q read as %+v, want %+v", value, got, want)
		}
	}
}

// TestCollapsibleSpacesCollapseAcrossInlineBoxes pins the half of §4.1.1's
// fourth rule that no per-node function can express: a collapsible space
// collapses into the one before it "even one outside the boundary of the inline
// containing that space, provided both spaces are within the same inline
// formatting context".
//
// The assertion is the line's exact advance rather than its text, because the
// text is what a reader copies and the advance is what the page shows — and the
// fault this catches showed up only in the advance. "a <span> </span> b" was
// three text nodes, each collapsed correctly on its own, and set with three
// spaces between the two letters.
func TestCollapsibleSpacesCollapseAcrossInlineBoxes(t *testing.T) {
	cases := map[string]float64{
		// "a b": three characters.
		`a <span> </span> b`: 3,
		// The same, with the space split every way a document can split it.
		`a<span> </span> b`:                2 + 1,
		`a <span></span> b`:                2 + 1,
		`a <span> <em> </em> </span> b`:    2 + 1,
		`a<span> </span><span> </span>b`:   2 + 1,
		`<span>a </span><span> b</span>`:   2 + 1,
		`a <span> </span> <span> </span>b`: 2 + 1,
		// A leading and a trailing space are removed at the line's two edges,
		// so they add nothing at all.
		` <span> </span>a b<span> </span> `: 3,
	}
	for markup, chars := range cases {
		root := layoutOf(t, 10000, `<p id="p">`+markup+`</p>`, noDefaults+mono)
		lines := linesOf(t, root, "p")
		if len(lines) != 1 {
			t.Errorf("%s gave %d lines: %v", markup, len(lines), lineTexts(lines))
			continue
		}
		px(t, "the advance of "+markup, lineWidth(lines[0]), chars*ch)
	}

	// The beginning of the context counts as a collapsible space already
	// emitted, so a leading one is gone before anything measures it.
	//
	// This has to be asserted through an intrinsic width and not through a
	// line, and finding out why was worth the trouble: the line-start rule
	// removes a leading space too, so a laid-out line looks right either way.
	// What does not is the width a shrink-to-fit box is sized to, which is
	// measured over content that was never broken into lines at all.
	root := layoutOf(t, 1000, `<div id="f"> ab </div>`,
		noDefaults+`#f { float: left; font-size: 100px; font-family: Courier }`)
	px(t, "a float around \" ab \"", find(t, root, "f").BorderRect.W, 2*ch)
}

// TestPreservedLeadingSpaceSurvivesTheLineStart pins the distinction §4.1.2
// turns on: the line-start rule removes a *collapsible* space, and preserved
// white space is not one.
//
// Losing it is not a subtle fault. It is what makes a <pre> keep the
// indentation it was written with, so an engine that drops it renders every
// code listing flush left and nothing about the page says a character was
// removed.
func TestPreservedLeadingSpaceSurvivesTheLineStart(t *testing.T) {
	for _, value := range []string{"pre", "pre-wrap", "break-spaces"} {
		root := layoutOf(t, 10000, `<p id="p">   x</p>`,
			noDefaults+mono+`p { white-space: `+value+` }`)
		lines := linesOf(t, root, "p")
		if len(lines) != 1 {
			t.Fatalf("%s gave %d lines", value, len(lines))
		}
		if got := strings.Join(runTexts(lines[0]), ""); got != "   x" {
			t.Errorf("white-space:%s set %q, want %q — the indentation was written",
				value, got, "   x")
		}
		// And the letter is where those three characters put it, which the text
		// alone would not prove: a run of zero width would satisfy the string.
		last := lines[0].Runs[len(lines[0].Runs)-1]
		px(t, "white-space:"+value+"'s x position", last.X, 3*ch)
	}

	// Under a collapsing value the same leading space *is* removed, so the
	// assertion above is about preservation and not about spaces in general.
	root := layoutOf(t, 10000, `<p id="p">   x</p>`, noDefaults+mono)
	first := linesOf(t, root, "p")[0].Runs[0]
	if first.Text != "x" || first.X != 0 {
		t.Errorf("white-space:normal kept a leading space: %q at %.2f", first.Text, first.X.Px())
	}
}

// TestPreWrapBreaksAfterALeadingSpace pins the case the WPT suite's
// pre-wrap-leading-spaces family is about: a preserved space at the start of a
// line takes room, and the break opportunity after it is real.
//
// Two characters fit on the line. The space is one of them, so the word cannot
// follow it and goes to the next line — leaving a first line that is one
// hanging space and nothing else. An engine that dropped the leading space
// would fit the word on the first line and lose a line from the document.
func TestPreWrapBreaksAfterALeadingSpace(t *testing.T) {
	root := layoutOf(t, 2*ch, `<p id="p"> XX</p>`,
		noDefaults+mono+`p { white-space: pre-wrap }`)

	lines := linesOf(t, root, "p")
	got := lineTexts(lines)
	if len(got) != 2 || got[0] != " " || got[1] != "XX" {
		t.Fatalf("got %q, want [\" \" \"XX\"] — the leading space takes room and "+
			"the word breaks after it", got)
	}
	px(t, "the word's position on its own line", lines[1].Runs[0].X, 0)
}

// TestPreservedTrailingSpacesHangRatherThanPushingAWordDown pins §4.1.2's
// hanging rule, and pins it against break-spaces, which is the value that opts
// out of it.
//
// The two must differ, and the difference is the whole of what break-spaces is
// for: under pre-wrap the trailing spaces sit past the line's end and the next
// word wraps whole, and under break-spaces they take room, so the line fills up
// with them and one is carried over.
func TestPreservedTrailingSpacesHangRatherThanPushingAWordDown(t *testing.T) {
	const markup = `<p id="p">XX    XX</p>`

	root := layoutOf(t, 5*ch, markup, noDefaults+mono+`p { white-space: pre-wrap }`)
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "XX    " || got[1] != "XX" {
		t.Errorf("pre-wrap gave %q, want [\"XX    \" \"XX\"] — the four spaces hang", got)
	}

	root = layoutOf(t, 5*ch, markup, noDefaults+mono+`p { white-space: break-spaces }`)
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "XX   " || got[1] != " XX" {
		t.Errorf("break-spaces gave %q, want [\"XX   \" \" XX\"] — the spaces take "+
			"room, so the fourth wraps with the word", got)
	}

	// The case above cannot reach the rule on its own, and finding that out is
	// the most useful thing this test has done: the space run there follows a
	// word, so nothing offered it a break opportunity and the fit test never
	// looked at it. Deleting the hanging rule left the case passing.
	//
	// A run of spaces that *does* begin at an opportunity is what exercises it,
	// and the way to get one is to write the spaces across a box boundary: the
	// first span ends at a space, so the second span's run of them may begin a
	// line. It must decline — a line ending in preserved spaces ends where the
	// last word did, because the spaces take no room on the page.
	//
	// Three characters fit. "a" and the four spaces after it are five, so a
	// breaker that let the spaces take the opportunity would put them on a line
	// of their own and the document would gain a line nobody wrote.
	root = layoutOf(t, 3*ch, `<p id="p"><span>a </span><span>   b</span></p>`,
		noDefaults+mono+`p { white-space: pre-wrap }`)
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a    " || got[1] != "b" {
		t.Errorf("pre-wrap gave %q, want [\"a    \" \"b\"] — a hanging space does "+
			"not take a break opportunity offered to it", got)
	}
}

// TestCollapsibleTrailingSpaceLeavesTheLine pins the other side of §4.1.2's
// third rule, by the line's advance rather than by its last run: a space
// removed from the text but still measured into the width would leave a
// right-aligned line hanging and a centred one off-centre by half a space.
func TestCollapsibleTrailingSpaceLeavesTheLine(t *testing.T) {
	root := layoutOf(t, 10000, `<p id="p">ab   </p>`, noDefaults+mono)
	lines := linesOf(t, root, "p")
	px(t, "a line whose trailing space was removed", lineWidth(lines[0]), 2*ch)
	if got := lineTexts(lines); len(got) != 1 || got[0] != "ab" {
		t.Errorf("got %q, want [\"ab\"]", got)
	}
}

// TestTabAdvancesToTheNextTabStop pins tab-size.
//
// A tab is the one character whose advance is not a property of the text: it is
// the distance to the next stop, so two tabs in a row are not one of twice the
// width and a tab after a long word is narrower than one after a short one. An
// engine that measured U+0009 against the face — which is what this one did,
// giving whatever a face happens to return for a character it has no glyph for
// — lines a table of columns up against nothing at all.
func TestTabAdvancesToTheNextTabStop(t *testing.T) {
	// One stop is tab-size space advances: 8 x 60px by default.
	cases := []struct {
		css string
		// want is the x position of each run on the line, in px.
		want []float64
	}{
		// "a" ends at 60; the next stop at a multiple of 480 is 480.
		{"", []float64{0, 60, 480}},
		{"p { tab-size: 8 }", []float64{0, 60, 480}},
		// Four spaces to a stop: the stops are at 240, 480, ...
		{"p { tab-size: 4 }", []float64{0, 60, 240}},
		// One space to a stop, so the tab advances to 120 rather than to 60 —
		// a tab always moves, even from a position already on a stop.
		{"p { tab-size: 1 }", []float64{0, 60, 120}},
		// A length is itself rather than a count of spaces.
		{"p { tab-size: 100px }", []float64{0, 60, 100}},
		// Zero renders no tab at all, which §4.1.2 says in as many words.
		{"p { tab-size: 0 }", []float64{0, 60, 60}},
	}
	for _, tc := range cases {
		root := layoutOf(t, 10000, "<p id=\"p\">a\tb</p>",
			noDefaults+mono+`p { white-space: pre }`+tc.css)
		lines := linesOf(t, root, "p")
		if len(lines) != 1 {
			t.Fatalf("%q gave %d lines", tc.css, len(lines))
		}
		runs := lines[0].Runs
		if len(runs) != len(tc.want) {
			t.Errorf("%q gave %d runs (%q), want %d", tc.css, len(runs),
				runTexts(lines[0]), len(tc.want))
			continue
		}
		for i, want := range tc.want {
			px(t, tc.css+" run "+itoa(i)+" ("+runs[i].Text+")", runs[i].X, want)
		}
	}

	// Two tabs in a row are two stops apart, which is the assertion a single
	// tab cannot make: a width read off the face would double instead.
	root := layoutOf(t, 10000, "<p id=\"p\">a\t\tb</p>",
		noDefaults+mono+`p { white-space: pre; tab-size: 4 }`)
	runs := linesOf(t, root, "p")[0].Runs
	if len(runs) != 4 {
		t.Fatalf("got %d runs, want 4", len(runs))
	}
	px(t, "the first tab's stop", runs[2].X, 240)
	px(t, "the second tab's stop", runs[3].X, 480)
}

// TestATabTooCloseToItsStopTakesTheNextOne pins §4.1.2's threshold: "if this
// distance is less than 0.5ch, then the subsequent tab stop is used instead".
//
// It is the rule that makes a tab a tab rather than a rounding. Without it, text
// that ends a tenth of a character before a stop is followed by a tab that
// advances a tenth of a character, so the column the author wrote the tab to
// make is not a column and the two words are, to the eye, touching. The failure
// is invisible in the source and looks like a kerning fault in the page.
//
// The three cases are the two sides of the comparison and its boundary, since
// "less than" and "at most" are the same rule everywhere except at 0.5ch exactly
// — and one Courier character at 100px is 60px, so the threshold is 30px and can
// be hit on the nose rather than approached.
func TestATabTooCloseToItsStopTakesTheNextOne(t *testing.T) {
	cases := []struct {
		css string
		// want is where the text after the tab begins, in px.
		want float64
	}{
		// "a" ends at 60 and the stop is at 70, which is 10px away — inside the
		// 30px threshold, so the tab takes the stop after it.
		{"p { tab-size: 70px }", 140},
		// 30px away: exactly the threshold, and the rule is "less than", so this
		// tab keeps the stop it was going to take. A comparison written as "at
		// most" moves this one to 180 and nothing else in this test.
		{"p { tab-size: 90px }", 90},
		// A single px inside it. 89 is chosen because 29 and 30 are one px apart
		// and a layout unit is a 64th of one, so this distinguishes the two
		// comparisons without resting on the rounding of either.
		{"p { tab-size: 89px }", 178},
		// Far outside the threshold, which is every ordinary tab: the stop is
		// 420px away from the 60px "a" ends at.
		{"p { tab-size: 8 }", 480},
	}
	for _, tc := range cases {
		root := layoutOf(t, 10000, "<p id=\"p\">a\tb</p>",
			noDefaults+mono+`p { white-space: pre }`+tc.css)
		lines := linesOf(t, root, "p")
		if len(lines) != 1 {
			t.Fatalf("%q gave %d lines", tc.css, len(lines))
		}
		runs := lines[0].Runs
		if len(runs) != 3 {
			t.Fatalf("%q gave %d runs (%q), want 3", tc.css, len(runs),
				runTexts(lines[0]))
		}
		px(t, tc.css+": where the text after the tab begins", runs[2].X, tc.want)
	}

	// The same rule in the intrinsic measurement, which resolves its own tab
	// advances rather than reading them off a laid-out line. A float is sized
	// shrink-to-fit, so its width is what the measurement said: "a" at 60, the
	// tab pushed past the 70px stop to 140, and "b" for another 60.
	root := layoutOf(t, 10000,
		`<div style="width: 5000px"><p id="f">a	b</p></div>`,
		noDefaults+mono+`#f { float: left; font-size: 100px; font-family: Courier;
		  white-space: pre; tab-size: 70px }`)
	px(t, "a float measured over a tab inside the threshold",
		find(t, root, "f").BorderRect.W, 200)
}

// TestAPreservedTabIsNotDrawnAsTofu pins that the one character no face has a
// glyph for does not reach one.
//
// Every face returns .notdef for U+0009, so a run holding a tab is set as a box
// where an author wrote an indent — the purest form of the silent garbage §6.3
// is written about, and one the glyph-missing guardrail deliberately does not
// warn about because a tab is white space rather than a letter. The advance is
// already spent against the tab stops by the time anything paints, so what is
// left to draw is white space and a space draws it.
func TestAPreservedTabIsNotDrawnAsTofu(t *testing.T) {
	face, err := fonts.Standard("Courier")
	if err != nil {
		t.Fatalf("loading Courier: %v", err)
	}
	if _, ok := face.GlyphID('\t'); ok {
		t.Skip("this face has a glyph for a tab, so there is nothing to avoid")
	}

	built := Build(Input{HTML: "<pre>a\tb</pre>"})
	rec := NewRecorder(nil)
	root := Layout(built.Root, A4.Content(), nil, rec)

	var drew []string
	for _, op := range Paint(root) {
		if v, ok := op.(DrawText); ok {
			drew = append(drew, v.Text)
			for _, r := range v.Text {
				if _, ok := v.Face.GlyphID(r); !ok {
					t.Errorf("the run %q holds %q, which the face %q cannot set",
						v.Text, r, v.Face.Name())
				}
			}
		}
	}
	// The tab is still a mark of its own rather than being dropped, so the two
	// letters around it do not run together in the text a reader copies out.
	if len(drew) != 3 || drew[0] != "a" || drew[1] != " " || drew[2] != "b" {
		t.Errorf("the page drew %q, want [\"a\" \" \" \"b\"]", drew)
	}
}

// TestTabStopsAreMeasuredFromTheBlockEdge pins that the stops belong to the
// block and not to the line box, which is a distinction only a float makes
// visible — and the case where getting it wrong makes two lines of a listing
// disagree about where a column is.
func TestTabStopsAreMeasuredFromTheBlockEdge(t *testing.T) {
	root := layoutOf(t, 10*ch,
		`<div><span id="f"></span><p id="p">a	b</p></div>`,
		noDefaults+mono+`p { white-space: pre; tab-size: 4 }
		 #f { float: left; width: 120px; height: 20px }`)

	runs := linesOf(t, root, "p")[0].Runs
	if len(runs) != 3 {
		t.Fatalf("got %d runs (%q), want 3", len(runs), runTexts(linesOf(t, root, "p")[0]))
	}
	// The line starts 120px in, so "a" occupies 120..180 in the block, and the
	// next stop at a multiple of 240 is 240 — which is 120 from the line's own
	// start, not 180.
	px(t, "the tab's position within the line", runs[2].X, 240-120)
}

// TestNowrapIsAsWideAsItsWholeText pins the intrinsic width of text that may not
// break.
//
// A minimum width taken as the longest word is right for text that wraps and
// wrong for text that cannot: a float holding a nowrap sentence would be sized
// to one word of it and overflow by the rest, which looks like a float bug
// rather than like a measurement one.
func TestNowrapIsAsWideAsItsWholeText(t *testing.T) {
	// A float is sized shrink-to-fit, so its width is the measurement.
	const markup = `<div style="width: 300px"><div id="f">aaa bbb</div></div>`

	root := layoutOf(t, 1000, markup,
		noDefaults+`#f { float: left; font-size: 100px; font-family: Courier;
		  white-space: nowrap }`)
	// "aaa bbb" is seven characters and cannot be broken, so shrink-to-fit is
	// min(max(420, 300), 420) = 420.
	px(t, "a nowrap float's width", find(t, root, "f").BorderRect.W, 7*ch)

	// The same text that may wrap is capped by the space available, because its
	// minimum is one word.
	root = layoutOf(t, 1000, markup,
		noDefaults+`#f { float: left; font-size: 100px; font-family: Courier }`)
	px(t, "a wrapping float's width", find(t, root, "f").BorderRect.W, 300)
}

// TestPreservedTrailingSpaceCountsInTheIntrinsicWidth pins the *conditional*
// half of §4.1.2's hanging rule, which is the half that is easy to get backwards.
//
// A trailing preserved space hangs unconditionally only where the line was
// broken at a soft wrap. At the end of the content — which is every line an
// intrinsic measurement sees — it hangs conditionally: it takes room unless
// taking it would overflow, and a box being sized to its own preferred width
// cannot overflow. So it takes room.
func TestPreservedTrailingSpaceCountsInTheIntrinsicWidth(t *testing.T) {
	root := layoutOf(t, 1000, `<div id="f">ab  </div>`,
		noDefaults+`#f { float: left; font-size: 100px; font-family: Courier;
		  white-space: pre }`)
	px(t, "a pre float's width", find(t, root, "f").BorderRect.W, 4*ch)

	// A collapsible trailing space is *removed* rather than hung, so it never
	// counts — which is what makes the assertion above about preservation.
	root = layoutOf(t, 1000, `<div id="f">ab  </div>`,
		noDefaults+`#f { float: left; font-size: 100px; font-family: Courier }`)
	px(t, "a collapsing float's width", find(t, root, "f").BorderRect.W, 2*ch)
}

// TestSegmentBreaksArePreservedIndividually pins the line count rather than the
// string, so that the rule is asserted where it shows: a run of blank lines in a
// pre-line block is a paragraph gap, and an engine that emitted one break per
// run of white space closes every gap in the document.
func TestSegmentBreaksArePreservedIndividually(t *testing.T) {
	cases := map[string]int{
		"a\nb":       2,
		"a\n\nb":     3,
		"a\n\n\nb":   4,
		"a \n \n b":  3,
		"a\r\n\r\nb": 3,
	}
	for text, want := range cases {
		for _, value := range []string{"pre", "pre-wrap", "pre-line", "break-spaces"} {
			root := layoutOf(t, 10000, `<p id="p">`+text+`</p>`,
				noDefaults+mono+`p { white-space: `+value+` }`)
			if got := len(linesOf(t, root, "p")); got != want {
				t.Errorf("white-space:%s on %q gave %d lines, want %d",
					value, text, got, want)
			}
		}
	}

	// A CRLF is one break and not two, which is what stops a document written
	// on Windows gaining a blank line between every pair of its own.
	root := layoutOf(t, 10000, "<p id=\"p\">a\r\nb</p>",
		noDefaults+mono+`p { white-space: pre }`)
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a" || got[1] != "b" {
		t.Errorf("a CRLF gave %q, want [\"a\" \"b\"]", got)
	}
}

// TestWhitespaceOnlyInlineContentBetweenBlocks pins that the indentation of the
// markup itself produces nothing — and that preserved white space still does,
// because it is content.
func TestWhitespaceOnlyInlineContentBetweenBlocks(t *testing.T) {
	// The newlines between the two paragraphs are text nodes. They collapse
	// away, so they generate no anonymous block and no blank line.
	root := layoutOf(t, 1000, "<div id=\"d\">\n  <p id=\"a\">x</p>\n  <p id=\"b\">y</p>\n</div>",
		noDefaults+mono)
	px(t, "two stacked lines and nothing between them",
		find(t, root, "d").BorderRect.H, 200)

	// The same white space that is *preserved* is a line of its own, because a
	// blank line inside a <pre> is a line the author wrote.
	root = layoutOf(t, 1000, "<div id=\"d\">\n  <p id=\"a\">x</p>\n  <p id=\"b\">y</p>\n</div>",
		noDefaults+mono+`div { white-space: pre; font-size: 100px;
		  font-family: Courier; line-height: 100px }`)
	if h := find(t, root, "d").BorderRect.H; h <= mustUnit(200) {
		t.Errorf("preserved white space between two blocks produced no lines: the "+
			"div is %.2f px tall, want more than the two paragraphs", h.Px())
	}
}

func mustUnit(px float64) style.Unit { u, _ := style.FromPx(px); return u }

// TestBreakAfterAHyphenAndNotAfterATrailingOne pins the one break rule that is
// neither a space nor an ideograph, in both directions.
func TestBreakAfterAHyphenAndNotAfterATrailingOne(t *testing.T) {
	// "well-known" is ten characters; at six to a line it breaks at the hyphen.
	root := layoutOf(t, 6*ch, `<p id="p">well-known</p>`, noDefaults+mono)
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "well-" || got[1] != "known" {
		t.Errorf("got %q, want [\"well-\" \"known\"]", got)
	}

	// A hyphen with nothing after it is not an opportunity: there would be
	// nothing to move to the next line.
	//
	// The piece count is not the assertion, and that is the trap here. A
	// trailing hyphen joins the run before it either way, so a test that
	// counted pieces passed just as happily when the rule was deleted. What
	// changes is the opportunity the run *ends* at, which is the value that
	// travels to the next box.
	pieces, endedAtBreak := splitAtBreaks("end-", whiteSpaceOf("normal"), wordBreak{})
	if len(pieces) != 1 || pieces[0].text != "end-" {
		t.Errorf("a trailing hyphen cut the text into %d pieces", len(pieces))
	}
	if endedAtBreak {
		t.Error("a hyphen at the end of the run left a break opportunity behind it")
	}
	// One before a space does not either, for the same reason: the space is
	// already the opportunity, and the hyphen must not claim it — a piece that
	// took it would leave the word after the space unable to begin a line.
	pieces, _ = splitAtBreaks("end- x", whiteSpaceOf("normal"), wordBreak{})
	if len(pieces) != 3 || pieces[0].text != "end-" {
		t.Errorf("a hyphen before a space gave %d pieces starting %q",
			len(pieces), pieces[0].text)
	}
	if pieces[1].breakBefore {
		t.Error("the space after a trailing hyphen was marked as beginning a line")
	}
	// And a hyphen inside a word does leave one, so the assertions above are
	// about where the hyphen is and not about hyphens.
	if _, ok := splitAtBreaks("well-known", whiteSpaceOf("normal"), wordBreak{}); ok {
		t.Error("a word ending after a hyphenated compound ended at an opportunity")
	}
	if pieces, _ := splitAtBreaks("well-known", whiteSpaceOf("normal"), wordBreak{}); len(pieces) != 2 ||
		!pieces[1].breakBefore {
		t.Error("a hyphen inside a word left no break opportunity")
	}
}

// TestWhitespaceProcessingIsLinear guards the shape of the algorithm rather
// than its speed.
//
// The input is untrusted and a text node is unbounded, so a megabyte of
// alternating spaces and newlines is a document somebody will send. A quadratic
// implementation — one that rebuilt a string per space, or rescanned a run per
// character — takes hours on this input rather than milliseconds, so the budget
// is enormous on purpose: it is not measuring performance, it is separating
// linear from quadratic, and those differ here by six orders of magnitude.
func TestWhitespaceProcessingIsLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("the input is large")
	}
	text := strings.Repeat(" \n\t\r\n", 200000) + "x" // a megabyte of white space

	for _, value := range []string{"normal", "pre-line", "pre", "break-spaces"} {
		start := time.Now()
		got := collapseWhitespace(text, value)
		if elapsed := time.Since(start); elapsed > 20*time.Second {
			t.Fatalf("white-space:%s took %v over a megabyte; that is not linear",
				value, elapsed)
		}
		if !strings.HasSuffix(got, "x") {
			t.Errorf("white-space:%s lost the text at the end", value)
		}
		// The collapsing values produce a bounded result; the preserving ones
		// produce no more than they were given.
		if len(got) > len(text) {
			t.Errorf("white-space:%s grew a %d byte input to %d bytes",
				value, len(text), len(got))
		}
	}

	// And the same through line breaking, which is where a rescan would hide.
	start := time.Now()
	root := layoutOf(t, 1000, `<p id="p">`+text+`</p>`,
		noDefaults+`p { font-size: 10px; font-family: Courier }`)
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("laying out a megabyte of white space took %v", elapsed)
	}
	if len(linesOf(t, root, "p")) == 0 {
		t.Error("a megabyte of white space and one letter produced no line")
	}
}

// TestHangingSpaceHangsOffTheEndOfARightToLeftLine pins which *side* §4.1.2's
// hang is on, which is a different question from how wide it is.
//
// The white space hangs past the line's end, and the end of a right-to-left line
// is its left edge — rule L1 gives a line's trailing separators the paragraph's
// own level, so the reordering draws them before the first word rather than
// after the last. A line aligned as though the hang followed its content
// therefore starts a hang too far in, and every word on it is pushed right by
// the width of a space that was supposed to take no room at all.
//
// The numbers are chosen so that the two answers cannot be confused. Five
// characters fit; "XX" and the four spaces after it break the line, and the four
// spaces hang. Under "text-align: left" — physical, and so unaffected by the
// direction — the content is flush against the left edge and "XX" is at zero. An
// engine that hangs on the wrong side puts it at 4ch, which is not a rounding
// away from anything.
func TestHangingSpaceHangsOffTheEndOfARightToLeftLine(t *testing.T) {
	const css = noDefaults + mono + `p { white-space: pre-wrap; text-align: left }`

	root := layoutOf(t, 5*ch, `<p id="p" dir="rtl">XX    XX</p>`, css)
	lines := linesOf(t, root, "p")
	if got := lineTexts(lines); len(got) != 2 || got[0] != "XX    " {
		t.Fatalf("the line broke as %q, want the first to be \"XX    \" — the four "+
			"spaces hang, so the word after them starts a line", got)
	}
	px(t, "the word on a right-to-left line whose spaces hang",
		runAt(t, lines[0].Runs, "XX").X, 0)

	// The same document set left to right, where the hang follows the content
	// and moves nothing. It is the control: an implementation that subtracted
	// the hang from every line, rather than only from the ones it is in front
	// of, would pull this one 4ch off the left edge of the page.
	root = layoutOf(t, 5*ch, `<p id="p">XX    XX</p>`, css)
	lines = linesOf(t, root, "p")
	if got := lineTexts(lines); len(got) != 2 || got[0] != "XX    " {
		t.Fatalf("the left-to-right line broke as %q, want the first \"XX    \"", got)
	}
	px(t, "the word on a left-to-right line whose spaces hang",
		runAt(t, lines[0].Runs, "XX").X, 0)

	// And the arithmetic under an alignment that is not zero, so that the rule is
	// pinned as "the hang comes off the shift" rather than as "a right-to-left
	// line starts at zero". Two characters of content in five leave three of
	// slack, half of which is 1.5ch; the four hanging spaces come off that, so
	// they run from -2.5ch to 1.5ch and the word sits centred at 1.5ch.
	root = layoutOf(t, 5*ch, `<p id="p" dir="rtl">XX    XX</p>`,
		noDefaults+mono+`p { white-space: pre-wrap; text-align: center }`)
	lines = linesOf(t, root, "p")
	px(t, "a centred right-to-left line's word",
		runAt(t, lines[0].Runs, "XX").X, 1.5*ch)
	px(t, "the spaces hanging off its left edge",
		runAt(t, lines[0].Runs, "    ").X, -2.5*ch)
}

// TestSpaceIsMeasuredAgainstTheFace pins the constant the rest of this file is
// written against, so that a change in the standard metrics fails here and
// makes every other number in the file readable rather than mysterious.
func TestSpaceIsMeasuredAgainstTheFace(t *testing.T) {
	face, err := fonts.Standard("Courier")
	if err != nil {
		t.Fatalf("loading Courier: %v", err)
	}
	for _, s := range []string{" ", "a", "X", " "} {
		if got := face.Measure(s, 100); got != ch {
			t.Errorf("Courier measures %q as %.4f at 100px, want %.1f — the "+
				"widths in this file are character counts times that", s, got, ch)
		}
	}
}

// TestHangingWhiteSpaceAndTheTwoIntrinsicWidths is §4.1.2's fourth rule, which
// is where the white-space values stop agreeing with each other.
//
// What reaches the rule is whatever the third rule left at the end of the line:
// under a collapsing value that is the other space separators and the preserved
// tabs, the spaces themselves having been removed, and under a preserving value
// it is the spaces as well. The rule then answers one value at a time, and the
// answers differ in a way no single "does it hang" bit can express:
//
//   - normal, nowrap and pre-line hang the sequence *unconditionally*. It never
//     takes room, so it is measured into neither width.
//   - pre-wrap hangs it unconditionally too, "unless the sequence is followed by
//     a forced line break, in which case it must conditionally hang the sequence
//     instead" — and every line measured for an intrinsic width ends at a forced
//     break or at the end of the content, so here it is always the conditional
//     one. A conditional hang takes room and gives it up only where the room is
//     not there: a box at its max-content width has the room and is that much
//     wider, and a box at its min-content width is precisely the box that has
//     not, so the sequence hangs and is not measured.
//   - break-spaces is named as not hanging: the spaces are data, take room, and
//     overflow if they must.
//   - pre is not in the rule's list at all, so nothing hangs under it either.
//
// The suite states the four corners of this in its own words —
// white-space-intrinsic-size-004 for pre-wrap's maximum, -013 for its minimum,
// -015 and -016 for pre — and they contradict each other unless the conditional
// and unconditional hangs are kept apart.
func TestHangingWhiteSpaceAndTheTwoIntrinsicWidths(t *testing.T) {
	// Courier at 20px is 12px a character: "xx" is 24, and U+2000 EN QUAD is one
	// more character's width. Every number below is 24 or 24 plus that one.
	const sep = " " // en quad, an "other space separator"
	widthOf := func(t *testing.T, ws, keyword, text string) float64 {
		t.Helper()
		css := noDefaults + `
		#d { position: absolute; line-height: 1; font-family: Courier;
		     font-size: 20px; white-space: ` + ws + `; width: ` + keyword + ` }`
		root := layoutOf(t, 1000, `<div id="d">xx`+text+`<br>xx</div>`, css)
		return find(t, root, "d").BorderRect.W.Px()
	}

	for _, c := range []struct {
		ws       string
		max, min float64
		why      string
	}{
		{"normal", 24, 24, "hangs unconditionally"},
		{"nowrap", 24, 24, "hangs unconditionally"},
		{"pre-line", 24, 24, "hangs unconditionally"},
		{"pre-wrap", 36, 24, "hangs conditionally before a forced break"},
		{"pre", 36, 36, "does not hang: the rule does not name it"},
		{"break-spaces", 36, 36, "does not hang: the spaces are data"},
	} {
		if got := widthOf(t, c.ws, "max-content", sep); got != c.max {
			t.Errorf("white-space:%s max-content is %g, want %g — a trailing "+
				"separator that %s", c.ws, got, c.max, c.why)
		}
		if got := widthOf(t, c.ws, "min-content", sep); got != c.min {
			t.Errorf("white-space:%s min-content is %g, want %g — a trailing "+
				"separator that %s", c.ws, got, c.min, c.why)
		}
	}

	// A trailing *space* is the same question with the third rule in front of
	// it: under a collapsing value it is removed before the fourth rule is
	// reached, so those three answer 24 for a different reason and the two
	// preserving values answer as they do above.
	for _, c := range []struct {
		ws       string
		max, min float64
	}{
		{"normal", 24, 24}, {"nowrap", 24, 24}, {"pre-line", 24, 24},
		{"pre-wrap", 36, 24}, {"pre", 36, 36}, {"break-spaces", 36, 36},
	} {
		if got := widthOf(t, c.ws, "max-content", " "); got != c.max {
			t.Errorf("white-space:%s max-content with a trailing space is %g, want %g",
				c.ws, got, c.max)
		}
		if got := widthOf(t, c.ws, "min-content", " "); got != c.min {
			t.Errorf("white-space:%s min-content with a trailing space is %g, want %g",
				c.ws, got, c.min)
		}
	}
}

// TestALineDoesNotBreakInsideTheWhiteSpaceThatEndsIt is the other half of
// §4.1.2's third and fourth rules.
//
// Both are written over white space "at the end of a line": the third removes
// the collapsible part of it, the fourth hangs what remains. Neither can happen
// to white space the line breaks *inside*, and breaking inside it is exactly
// what a greedy fill does unprompted — the run is wider than the room left, so
// the first opportunity in it ends the line and the rest goes to the next one.
// What that produces is a line, or several, holding nothing but spaces, above a
// line holding the text that followed them.
//
// The reftests cannot see this. The suite's fixtures for it turn on an inline
// box's background covering the line box, and §10.6.1 sizes that background to
// the *font's* ascent and descent — which for the standard faces is nine tenths
// of the em and for the fonts a browser has is more than the em. So the pages
// differ by a fraction of a pixel at the top and bottom of a stripe whatever
// this does, and the wrapping has to be asserted directly.
func TestALineDoesNotBreakInsideTheWhiteSpaceThatEndsIt(t *testing.T) {
	// Four characters of room, then two of text and a run of white space far
	// wider than what is left.
	//
	// The run is separators *with ordinary spaces between them*, and the spaces
	// are the whole point. A separator hangs on its own, so a line already
	// refuses to break at one; an ordinary space under a collapsing value does
	// not hang — the third rule removes it, which can only happen once the line
	// has ended — so it is an opportunity sitting in the middle of the run, and
	// it is where the line came apart before the run was recognised as one.
	const seps = "\u2000 \u2001 \u2002 \u2003 \u2004 \u2005"
	narrow := func(ws string) string {
		return noDefaults + `
		#d { font-family: Courier; font-size: 20px; width: 48px;
		     white-space: ` + ws + ` }`
	}

	for _, ws := range []string{"normal", "pre-line", "pre-wrap"} {
		got := lineTextsOf(t, layoutOf(t, 1000,
			`<div id="d">xx`+seps+`<br>xx</div>`, narrow(ws)), "d")
		if len(got) != 2 {
			t.Errorf("white-space:%s put the trailing separators on %d lines %q, "+
				"want 2 — the run at the end of the line hangs, and a line may not "+
				"be broken inside what hangs off it", ws, len(got), got)
		}
	}

	// The same run inside an inline box with padding on it, which is how the
	// suite writes these — the spaces go in a <span> so that they can be given a
	// background and seen. A span's own padding, border and margin are items on
	// the line in their own right, and if one of them ended the run then the
	// half in front of it would be breakable again.
	{
		got := lineTextsOf(t, layoutOf(t, 1000,
			`<div id="d">xx<span style="padding: 0 2px">`+seps+`</span><br>xx</div>`,
			narrow("normal")), "d")
		if len(got) != 2 {
			t.Errorf("a hanging run inside a padded span went onto %d lines %q, "+
				"want 2 — the box's own padding is not content and does not cut "+
				"the run in two", len(got), got)
		}
	}

	// break-spaces is the value that says otherwise, and it is the reason this
	// is a rule about hanging rather than a rule about white space. Its spaces
	// are data: they take room, they overflow, and §3 puts an opportunity after
	// every one of them — so a line may end inside a run of them and must.
	got := lineTextsOf(t, layoutOf(t, 1000,
		`<div id="d">xx        <br>xx</div>`, narrow("break-spaces")), "d")
	if len(got) < 3 {
		t.Errorf("white-space:break-spaces kept eight trailing spaces on %d lines "+
			"%q, want more than 2 — its spaces do not hang, so the line breaks "+
			"among them", len(got), got)
	}
}
