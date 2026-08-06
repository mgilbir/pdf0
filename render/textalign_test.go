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

func TestTextAlignIgnoresHangingSpace(t *testing.T) {
	// §4.1.2: a trailing space is excluded from the width a line is aligned at.
	// With "pre-wrap" the space is preserved and stays in the runs — it must
	// still not be counted, or the line centres around a character that marks
	// no paper and sits half a space off.
	//
	// Both documents hold the same six visible characters, so a correct
	// implementation centres them identically.
	plain := layoutOf(t, 600, `<div id="p">abcdef</div>`,
		alignCSS+` #p { text-align: center }`)
	trailing := layoutOf(t, 600, `<div id="p">abcdef  </div>`,
		alignCSS+` #p { text-align: center; white-space: pre-wrap }`)
	want, got := lineX(t, plain, "p"), lineX(t, trailing, "p")
	if got != want {
		t.Errorf("a line with two preserved trailing spaces centred at %gpx and the "+
			"same text without them at %gpx; the hanging space is being counted",
			got, want)
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
