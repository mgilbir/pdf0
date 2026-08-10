package render

import (
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// CSS 2.1 §10.8.1: every inline-level box on a line takes part in its height.
//
// The engine stacked only the *atomic* inlines — a replaced element, an
// inline-block — so a run of text in a <span> set larger than the paragraph
// around it grew nothing at all. Its line box stayed the strut's height and its
// baseline sat where the smaller type wanted it, which lifts the text of a drop
// cap, a marker, or any generated content with a font-size on it into the line
// above.
//
// How it was found is worth recording, because the search was for something
// else. Ninety-one failures in css/CSS2/generated-content and seventy-one in
// css/CSS2/lists read as counters and quotes; ninety-seven of them raised no
// finding at all, and the first one dumped was a ::before with "font-size: 30px"
// whose baseline was 11.66px too high. A plain <span> at 30px in a 16px div drew
// the identical wrong page, so it was never about generated content — the tests
// are simply full of pseudo-elements with a size on them.
//
// Ahem is what makes the arithmetic assertable: its ascent is 0.8em and its
// descent 0.2em with no line gap, so "line-height: normal" is exactly one em and
// every figure below is a whole number of pixels rather than a tolerance.

// lineOfBlock is the first line box of the element with an id, and the height of
// the block that holds it.
func lineOfBlock(t *testing.T, set FontSet, htmlSrc, cssSrc, id string) (LineFragment, style.Unit) {
	t.Helper()
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: cssSrc}}})
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(600)
	root := Layout(built.Root, Size{W: w, H: h}, set, NewRecorder(nil))

	var found *Fragment
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if found != nil {
			return
		}
		if f.Box != nil && f.Box.Element != nil {
			if got, _ := f.Box.Element.Attr("id"); got == id {
				found = f
				return
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	if found == nil {
		t.Fatalf("no element with id %q", id)
	}
	if len(found.Lines) == 0 {
		t.Fatalf("%q has no line box", id)
	}
	return found.Lines[0], found.BorderRect.H
}

func TestATallerInlineGrowsItsLineBox(t *testing.T) {
	set := loadAhem(t)
	// The strut is 10px Ahem: ascent 8, descent 2, line-height normal 10, so its
	// baseline is at 8. The span is 20px: ascent 16, descent 4, line-height 20,
	// baseline at 16. Aligned on their baselines the line reaches 16 above and 4
	// below, so it is 20 tall with its baseline at 16 — and not 10 tall with its
	// baseline at 8, which is what stacking the strut alone gives.
	line, height := lineOfBlock(t, set,
		`<div id="d"><span id="s">x</span></div>`,
		noDefaults+`#d { font-family: Ahem; font-size: 10px } #s { font-size: 20px }`, "d")

	if got := line.Rect.H.Px(); got != 20 {
		t.Errorf("the line box is %vpx tall, want 20 — the 20px span reaches 16 "+
			"above the baseline and 4 below", got)
	}
	if got := line.Baseline.Px(); got != 16 {
		t.Errorf("the baseline is at %vpx, want 16", got)
	}
	if got := height.Px(); got != 20 {
		t.Errorf("the block is %vpx tall, want 20", got)
	}
}

func TestLineBoxIsTheUnionOfBothSides(t *testing.T) {
	set := loadAhem(t)
	// The case that separates "take the tallest box" from §10.8.1's rule, which
	// is a maximum *per side*. Neither box here is the answer:
	//
	//   strut  30px Ahem, line-height 24: ascent 24, descent 6, half-leading
	//          (24-30)/2 = -3, so it reaches 21 above the baseline and 3 below.
	//   span   10px Ahem, line-height 24: ascent 8, descent 2, half-leading
	//          (24-10)/2 = 7, so it reaches 15 above and 9 below.
	//
	// Both are 24 tall. The union takes 21 from the strut and 9 from the span, so
	// the line is 30 with its baseline at 21 — taller than either box on it. An
	// implementation that took the taller box's height would say 24.
	line, _ := lineOfBlock(t, set,
		`<div id="d"><span id="s">x</span></div>`,
		noDefaults+`
		 #d { font-family: Ahem; font-size: 30px; line-height: 24px }
		 #s { font-size: 10px; line-height: 24px }`, "d")

	if got := line.Baseline.Px(); got != 21 {
		t.Errorf("the baseline is at %vpx, want 21 — the strut's side", got)
	}
	if got := line.Rect.H.Px(); got != 30 {
		t.Errorf("the line box is %vpx tall, want 30 — 21 from the strut above and "+
			"9 from the span below, though neither box is more than 24", got)
	}
}

func TestPlainTextDoesNotChangeItsOwnLineBox(t *testing.T) {
	set := loadAhem(t)
	// The other direction, and the reason the change is safe for every page that
	// was already right: a text box inherits its block's font and line-height, so
	// its leading *is* the strut's leading and the maximum picks the same two
	// numbers. A line of ordinary text is exactly as tall as it was.
	line, _ := lineOfBlock(t, set, `<div id="d">xyz</div>`,
		noDefaults+`#d { font-family: Ahem; font-size: 20px }`, "d")
	if got := line.Rect.H.Px(); got != 20 {
		t.Errorf("a plain line of 20px Ahem is %vpx tall, want 20", got)
	}
	if got := line.Baseline.Px(); got != 16 {
		t.Errorf("its baseline is at %vpx, want 16", got)
	}
}

func TestAShorterInlineDoesNotShrinkItsLineBox(t *testing.T) {
	set := loadAhem(t)
	// The maximum has to be a maximum in both directions. A 10px span inside a
	// 20px block does not pull the line down to its own height, and an
	// implementation that assigned the last item's leading rather than taking the
	// larger would give 10.
	line, _ := lineOfBlock(t, set,
		`<div id="d">a<span id="s">x</span></div>`,
		noDefaults+`#d { font-family: Ahem; font-size: 20px } #s { font-size: 10px }`, "d")
	if got := line.Rect.H.Px(); got != 20 {
		t.Errorf("the line box is %vpx tall, want the 20px strut's 20", got)
	}
	if got := line.Baseline.Px(); got != 16 {
		t.Errorf("the baseline is at %vpx, want 16", got)
	}
}

func TestZeroLineHeightIsAValueAndNotAnAbsence(t *testing.T) {
	set := loadAhem(t)
	// "line-height: 0" asks for a box shorter than its own type, and the leading
	// it produces is genuinely zero above and zero below — which is why the flag
	// that says an item has metrics is separate from the metrics. Reading the two
	// zeroes as "this item has no leading" would let the strut win a line the
	// span was written to collapse.
	//
	// 20px Ahem with line-height 0: ascent 16, descent 4, half-leading
	// (0-20)/2 = -10, so the box reaches 6 above the baseline and -6 below. The
	// strut here is the same declaration, so the line is 0 tall with its baseline
	// at 6.
	line, _ := lineOfBlock(t, set,
		`<div id="d"><span id="s">x</span></div>`,
		noDefaults+`
		 #d { font-family: Ahem; font-size: 20px; line-height: 0 }
		 #s { font-size: 20px; line-height: 0 }`, "d")
	if got := line.Rect.H.Px(); got != 0 {
		t.Errorf("the line box is %vpx tall, want 0", got)
	}
	if got := line.Baseline.Px(); got != 6 {
		t.Errorf("the baseline is at %vpx, want 6", got)
	}
}

func TestOnlyRunsOfTextBringLeading(t *testing.T) {
	// The other half of the flag, and the half that took a fixture chosen for it.
	//
	// A line holds items that are not text and have no metrics at all: the marker
	// for a float met among the words, the one for an absolutely positioned box,
	// and an inline box's own margin, border and padding. Letting those through
	// makes each of them offer a reach of zero above and zero below — which is
	// harmless on almost every line, and is not harmless on a line whose strut
	// reaches a *negative* distance below the baseline.
	//
	// 20px Ahem with line-height 0: ascent 16, descent 4, half-leading
	// (0-20)/2 = -10. The baseline is at 6 and the strut's descent is 0-6 = -6, so
	// the line is 6 + (-6) = 0 tall, which is what "line-height: 0" asks for. An
	// inset item offering 0 would win that maximum and the line would be 6.
	//
	// This was measured rather than argued: with the flag ignored, nothing in the
	// unit tests moved and nothing in all 5212 reftests moved either. The guard
	// decided nothing until this document existed.
	set := loadAhem(t)
	line, _ := lineOfBlock(t, set,
		`<div id="d"><span id="s">x</span></div>`,
		noDefaults+`
		 #d { font-family: Ahem; font-size: 20px; line-height: 0 }
		 #s { padding-left: 10px }`, "d")
	if got := line.Rect.H.Px(); got != 0 {
		t.Errorf("the line box is %vpx tall, want 0 — the inline box's padding is "+
			"not a run of text and brings no leading of its own", got)
	}
}
