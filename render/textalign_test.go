package render

import (
	"strings"
	"testing"
)

// text-align, CSS 2.1 §16.2.
//
// Every position below is arithmetic that can be read rather than a number
// recorded from a run: Courier is 600/1000, so a character at 20px is 12px wide
// and a six-character word is 72px. A recorded number would agree just as well
// with a wrong implementation.

// lineX returns the x of the first run of the first line of an element.
func lineX(t *testing.T, root *Fragment, id string) float64 {
	t.Helper()
	f := find(t, root, id)
	if len(f.Lines) == 0 || len(f.Lines[0].Runs) == 0 {
		t.Fatalf("#%s has no line runs to align", id)
	}
	return f.Lines[0].Runs[0].X.Px()
}

const alignCSS = `#p { font-family: Courier; font-size: 20px; width: 300px }`

func TestTextAlignPositionsTheLine(t *testing.T) {
	// "abcdef" is six characters: 6 x 0.6 x 20 = 72px in a 300px line.
	// left 0, right 228, centre 114.
	cases := map[string]float64{
		"left":   0,
		"start":  0,
		"right":  228,
		"end":    228,
		"center": 114,
	}
	for value, want := range cases {
		root := layoutOf(t, 600, `<div id="p">abcdef</div>`,
			alignCSS+` #p { text-align: `+value+` }`)
		if got := lineX(t, root, "p"); got != want {
			t.Errorf("text-align:%s put the line at %gpx, want %g", value, got, want)
		}
	}
}

func TestTextAlignIsInherited(t *testing.T) {
	// The property is inherited, which is how "body { text-align: center }"
	// works at all. A version that read it only off the element declaring it
	// would leave every paragraph flush left.
	root := layoutOf(t, 600, `<div id="outer"><div id="p">abcdef</div></div>`,
		alignCSS+` #outer { text-align: center }`)
	if got := lineX(t, root, "p"); got != 114 {
		t.Errorf("an inherited text-align:center put the line at %gpx, want 114", got)
	}
}

// TestTextAlignIgnoresUnconditionallyHangingSpace is §4.1.2's hang at its full
// strength: a line that ended at a *soft wrap* leaves its preserved trailing
// space outside the width the line is aligned at.
//
// The line has to be one that wrapped, and that is the whole point of the second
// word. A trailing space at the end of the content hangs only *conditionally* —
// see the test below — so a document written without the wrap would be asking
// this rule a question the other rule answers, which is the shape of test this
// repository has been caught by twice.
func TestTextAlignIgnoresUnconditionallyHangingSpace(t *testing.T) {
	// In 100px — eight characters and a third of Courier at 20px — "abcdef  "
	// is 96 wide and the second "abcdef" does not fit after it, so the first
	// line ends at a soft wrap with two preserved spaces on it. Aligned at 72
	// rather than 96, the line centres at (100-72)/2 = 14.
	root := layoutOf(t, 600, `<div id="p">abcdef  abcdef</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 100px;
		      text-align: center; white-space: pre-wrap }`)
	lines := linesOf(t, root, "p")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lineTexts(lines))
	}
	if got := lines[0].Runs[0].X.Px(); got != 14 {
		t.Errorf("a soft-wrapped line with two hanging spaces centred at %gpx, "+
			"want 14 — the hanging space is being counted", got)
	}
}

// TestTextAlignCountsAConditionallyHangingSpace is the other half, and it is the
// half that is easy to get backwards — this engine had it backwards, and a test
// asserting the wrong answer pinned it there.
//
// §4.1.2: preserved white space at the end of a line hangs unconditionally
// "unless the sequence is followed by a forced line break, in which case it must
// conditionally hang the sequence instead", and something that conditionally
// hangs "hangs only if it does not otherwise fit in the line". The end of the
// content is such a break — the specification's own example is a paragraph whose
// only content is " 0 ", with no <br> in it anywhere.
func TestTextAlignCountsAConditionallyHangingSpace(t *testing.T) {
	// The example from §4.1.2, in Courier rather than in ch: five characters is
	// 60px, " 0 " is three of them, and centring 36 in 60 puts the line at 12.
	// Aligning it as though the trailing space hung would put it at 18 — half a
	// character off, which is exactly what the specification says must not
	// happen.
	root := layoutOf(t, 600, `<div id="p"> 0 </div>`,
		`#p { font-family: Courier; font-size: 20px; width: 60px;
		      text-align: center; white-space: pre-wrap }`)
	if got := lineX(t, root, "p"); got != 12 {
		t.Errorf("the specification's centred \" 0 \" example is at %gpx, want 12", got)
	}

	// And a space that does *not* fit hangs even here, which is what makes the
	// rule conditional rather than simply off. "abcdef  " is 96 in a line 84
	// wide, so the two spaces cannot fit; the six characters do, and a
	// right-aligned line puts them flush against the edge with the spaces
	// hanging past it.
	root = layoutOf(t, 600, `<div id="p">abcdef  </div>`,
		`#p { font-family: Courier; font-size: 20px; width: 84px;
		      text-align: right; white-space: pre-wrap }`)
	if got := lineX(t, root, "p"); got != 12 {
		t.Errorf("a right-aligned line whose trailing spaces overflow starts at "+
			"%gpx, want 12 — the spaces should hang past the edge", got)
	}
}

func TestTextAlignDoesNotMoveAnOverfullLine(t *testing.T) {
	// A line wider than the space it has overflows to the right whatever the
	// alignment says. Centring it would push it off the left edge as well, which
	// loses content rather than moving it.
	root := layoutOf(t, 600, `<div id="p">abcdefghijklmnopqrstuvwxyz</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 40px;
		      white-space: nowrap; text-align: center }`)
	if got := lineX(t, root, "p"); got != 0 {
		t.Errorf("an overfull centred line was moved to %gpx; it should stay at 0", got)
	}
}

func TestTextAlignMovesAtomicInlines(t *testing.T) {
	// An inline-block is placed as a child of the block rather than as a run, so
	// an implementation that shifted only the runs would centre the text and
	// leave the picture behind — the two would come apart, which is worse than
	// not aligning at all.
	root := layoutOf(t, 600, `<div id="p"><span id="box"></span></div>`,
		`#p { width: 300px; text-align: center; font-size: 20px }
		 #box { display: inline-block; width: 100px; height: 10px }`)
	// The fragment's rectangle is in page coordinates, so the offset is measured
	// from the block's own content edge rather than from the page — otherwise
	// the assertion is really about the body's margin.
	box := find(t, root, "box")
	within := box.BorderRect.X.Sub(find(t, root, "p").ContentRect().X)
	// 300 - 100 = 200 of slack, half of it is 100.
	if got := within.Px(); got != 100 {
		t.Errorf("a centred inline-block sits %gpx into its block, want 100", got)
	}
}

func TestTextAlignJustifyIsReported(t *testing.T) {
	// Justification is not performed. Setting justified text ragged without
	// saying so is the kind of wrong page that looks deliberate, so it is
	// reported — and reported once for the box rather than once per line.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<div id="p">one two three four five six seven eight nine ten</div>`,
		CSS:  []Stylesheet{{Source: `#p { width: 80px; text-align: justify }`}},
	})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, nil, rec)

	var found int
	for _, f := range rec.Findings() {
		if f.Property == "text-align" && strings.Contains(f.Message, "justify") {
			found++
		}
	}
	if found == 0 {
		t.Error("text-align:justify was applied silently; it is not implemented")
	}
	if found > 1 {
		t.Errorf("the justification gap was reported %d times; once per box is enough", found)
	}
}
