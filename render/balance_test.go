package render

import (
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// text-wrap-style: balance, CSS Text §5.1.
//
// Every width below is a character count in Courier, whose every glyph is 600
// units of 1000 — see the note in breakspaces_test.go — so a line of N
// characters is exactly N*ch wide and the expected breaks are arithmetic rather
// than recordings.

// TestBalanceEvensTheLines is the value at the point it differs from greedy
// filling, stated as the difference.
//
// The same text in the same width breaks in two places, and the balanced one is
// the specification's own example of what it is for: greedy fills the first line
// to thirty-three characters and leaves twelve on the second, which is the
// ragged look balancing exists to fix.
func TestBalanceEvensTheLines(t *testing.T) {
	const src = `<p id="p">The quickest brown fox jumped over the lazy dog</p>`

	root := layoutOf(t, 10000, src, widthCSS(35, ""))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "The quickest brown fox jumped over" || got[1] != "the lazy dog" {
		t.Errorf("greedy gave %q, want the first line filled to 34", got)
	}

	root = layoutOf(t, 10000, src, widthCSS(35, "text-wrap: balance"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "The quickest brown fox" || got[1] != "jumped over the lazy dog" {
		t.Errorf("balance gave %q, want [\"The quickest brown fox\" "+
			"\"jumped over the lazy dog\"] — 22 and 24 rather than 34 and 12", got)
	}
}

// TestBalanceKeepsTheLineCount is the constraint the search is under, and the
// one a naive "make the lines even" would break.
//
// Balancing may not cost a line. The narrowest width that still fits the text in
// the number of lines it already had is the answer precisely because anything
// narrower adds one, and a paragraph that grew a line to look tidier would be
// balancing at the expense of the thing being balanced.
func TestBalanceKeepsTheLineCount(t *testing.T) {
	for _, chars := range []float64{10, 12, 20, 35, 60} {
		src := `<p id="p">The quickest brown fox jumped over the lazy dog</p>`
		plain := lineTexts(linesOf(t, layoutOf(t, 10000, src, widthCSS(chars, "")), "p"))
		balanced := lineTexts(linesOf(t, layoutOf(t, 10000, src,
			widthCSS(chars, "text-wrap: balance")), "p"))
		if len(plain) != len(balanced) {
			t.Errorf("at %gch the text takes %d lines greedily and %d balanced: %q",
				chars, len(plain), len(balanced), balanced)
		}
	}
}

// TestBalanceDoesNotNarrowTheLineBox: §5.1 chooses where the lines break and
// says nothing about how wide they are.
//
// The distinction is invisible in left-aligned text and is the whole rendering
// in aligned text: a right-aligned balanced line ends at the box's right edge,
// not at the narrower measure the breaks were chosen in. An implementation that
// laid the box out narrow and left it there would pull every line left by the
// difference.
func TestBalanceDoesNotNarrowTheLineBox(t *testing.T) {
	const src = `<p id="p">The quickest brown fox jumped over the lazy dog</p>`
	root := layoutOf(t, 10000, src,
		widthCSS(35, "text-wrap: balance; text-align: right"))
	lines := linesOf(t, root, "p")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var w style.Unit
		for _, r := range line.Runs {
			w = w.Add(r.Width)
		}
		if got := line.Rect.W.Px(); got != 35*ch {
			t.Errorf("line %d's box is %gpx wide, want the full %g", i, got, 35*ch)
		}
		// Right-aligned, so the last run ends at the box's right edge.
		last := line.Runs[len(line.Runs)-1]
		if end := last.X.Add(last.Width).Px(); end != 35*ch {
			t.Errorf("line %d ends at %g, want the right edge %g", i, end, 35*ch)
		}
	}
}

// TestBalanceIsPerForcedBreakGroup is §5.1's "each group of lines separated by a
// forced line break is balanced separately".
//
// The two halves are chosen to balance to *different* widths, which is what
// makes the test able to tell the rule from its absence: the first balances to
// 24 characters and the second to 30, so one cap for the whole box would be 30
// and the first half would break at "jumped" instead of at "fox". Two halves of
// the same text agree under either rule and prove nothing — which is what this
// test did first, and it let a planted single-cap defect through.
//
// It is the rule the suite's text-wrap-balance-004 is about, and this engine got
// it wrong first in exactly that way.
func TestBalanceIsPerForcedBreakGroup(t *testing.T) {
	const first = "The quickest brown fox jumped over the lazy dog"
	// Three words of fourteen: two lines either way it is split, so it balances
	// to twenty-nine — wider than the first half's twenty-four, and *breakable*,
	// which is what makes the difference show. Two unbreakable runs of thirty
	// would not: they overflow a twenty-four-character cap rather than wrapping,
	// so the joint search settles on twenty-four and both rules agree. That was
	// this test's first fixture and a planted single-cap defect walked through it.
	const second = "aaaaaaaaaaaaaa bbbbbbbbbbbbbb cccccccccccccc"

	alone := func(text string) []string {
		return lineTexts(linesOf(t, layoutOf(t, 10000, `<p id="p">`+text+`</p>`,
			widthCSS(35, "text-wrap: balance")), "p"))
	}
	want := append(append([]string{}, alone(first)...), alone(second)...)

	root := layoutOf(t, 10000, `<p id="p">`+first+`<br>`+second+`</p>`,
		widthCSS(35, "text-wrap: balance"))
	got := lineTexts(linesOf(t, root, "p"))
	if len(got) != len(want) {
		t.Fatalf("the two halves came to %d lines %q, want %d %q",
			len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q — each half balances as it would alone",
				i, got[i], want[i])
		}
	}
}

// TestBalanceLeavesTheIndentToTheFirstLine is where the balanced width and
// §16.1's indent meet, and they meet in an order that matters.
//
// The balanced width is a *line* width: the search counted the first line's room
// as that width less the indent, so the indent has to come off the cap and not
// off the band before it. Taking it off the band leaves the two measures
// disagreeing by five characters on the one line the indent applies to, and the
// first line then breaks where nothing chose.
//
// The numbers are the suite's text-wrap-balance-text-indent-001: ten characters
// of width, five of indent, and text that balances to seven.
func TestBalanceLeavesTheIndentToTheFirstLine(t *testing.T) {
	const src = `<p id="p">01 34 6 89 12 3 56</p>`

	root := layoutOf(t, 10000, src, widthCSS(10, "text-indent: 5ch"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 3 ||
		got[0] != "01 34" || got[1] != "6 89 12 3" || got[2] != "56" {
		t.Errorf("greedy with an indent gave %q, want "+
			"[\"01 34\" \"6 89 12 3\" \"56\"]", got)
	}

	root = layoutOf(t, 10000, src,
		widthCSS(10, "text-indent: 5ch; text-wrap: balance"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 3 ||
		got[0] != "01" || got[1] != "34 6 89" || got[2] != "12 3 56" {
		t.Errorf("balanced with an indent gave %q, want "+
			"[\"01\" \"34 6 89\" \"12 3 56\"] — seven characters a line, "+
			"two of them on the first after the indent", got)
	}

	// And the indent belongs to the first group only. §16.1 gives it to the
	// first formatted line of the element, and the line after a <br> is not one
	// — so the second group balances as though there were no indent at all,
	// which for this text is not balancing it at all: ten characters either way
	// it is split. Charging it the indent would balance it to seven and break it
	// into three.
	root = layoutOf(t, 10000, `<p id="p">01 34 6 89 12 3 56<br>01 34 6 89 12 3 56</p>`,
		widthCSS(10, "text-indent: 5ch; text-wrap: balance"))
	got := lineTexts(linesOf(t, root, "p"))
	if len(got) != 5 {
		t.Fatalf("the two groups came to %d lines %q, want 5", len(got), got)
	}
	if got[3] != "01 34 6 89" || got[4] != "12 3 56" {
		t.Errorf("the group after the <br> is %q, want [\"01 34 6 89\" "+
			"\"12 3 56\"] — the indent is the first group's alone", got[3:])
	}
}

// TestBalanceStopsAtTheLineLimit pins the bound, by lowering it far enough to
// watch it decide something.
//
// A bound that has only ever been observed not to trip is one nobody knows
// works, and this one is load-bearing: the search breaks the whole paragraph
// once per probe, so the limit is what keeps a page of prose from being laid out
// sixteen times over.
func TestBalanceStopsAtTheLineLimit(t *testing.T) {
	const src = `<p id="p">The quickest brown fox jumped over the lazy dog</p>`
	defer func(n int) { maxBalanceLines = n }(maxBalanceLines)

	maxBalanceLines = 6
	balanced := lineTexts(linesOf(t, layoutOf(t, 10000, src,
		widthCSS(35, "text-wrap: balance")), "p"))
	if balanced[0] != "The quickest brown fox" {
		t.Fatalf("the fixture does not balance at all: %q", balanced)
	}

	maxBalanceLines = 1
	got := lineTexts(linesOf(t, layoutOf(t, 10000, src,
		widthCSS(35, "text-wrap: balance")), "p"))
	if got[0] != "The quickest brown fox jumped over" {
		t.Errorf("past the limit the lines are %q, want the greedy break — "+
			"balancing should be off, not merely cheaper", got)
	}
}

// TestBalancingBesideAFloatUsesTheWidthsTheLinesHad is §5.1 where the lines are
// not all the same width.
//
// A float inside the box shortens the lines beside it and leaves the ones below
// it alone, so there is no single width to search in — and the widths cannot be
// known before the box is laid out, because a float inside it is placed as the
// lines are built and what shortens a line is decided by the lines above it. So
// the box is laid out once to find out, thrown away, and laid out again in the
// measure that answer gives.
//
// The assertion is the difference between the two: searching as though the box
// were the width it declares puts a word on the second line that does not fit
// there once the float is counted.
func TestBalancingBesideAFloatUsesTheWidthsTheLinesHad(t *testing.T) {
	// 23.5 characters of block, a float seven wide and two lines tall, so the
	// first two lines have 16.5 and the third has all of it.
	const src = `<p id="p"><span id="f"></span>abc de fg hij klm nop qrst uvw xyz!</p>`
	css := noDefaults + mono + `p { width: ` + itoa(int(23.5*ch)) + `px;
		text-wrap-style: balance }
		#f { float: left; width: ` + itoa(int(7*ch)) + `px; height: 200px }`

	got := lineTexts(linesOf(t, layoutOf(t, 10000, src, css), "p"))
	want := []string{"abc de fg hij", "klm nop qrst", "uvw xyz!"}
	if len(got) != len(want) {
		t.Fatalf("%d lines %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q — the search has to run in the "+
				"widths the float leaves, not in the width the box declares",
				i, got[i], want[i])
		}
	}
}
