package render

import (
	"strings"
	"testing"
)

// box-sizing.
//
// Every expected number is the box-model arithmetic written out, and each
// document is built so the number could only come out right one way: a content
// width, a border width and a padding that are all different, so an off-by-one
// box cannot land on the right answer by accident.

const sizeCSS = `#b { box-sizing: border-box; padding-left: 10px; padding-right: 10px;
	border-left-style: solid; border-left-width: 5px;
	border-right-style: solid; border-right-width: 5px }`

func TestBorderBoxTakesThePaddingOutOfTheDeclaredWidth(t *testing.T) {
	// 200 declared, 30 of padding and border: a 200px border box holding 170px of
	// content. Under content-box the same declaration gives a 230px border box.
	root := layoutOf(t, 600, `<div id="b">x</div>`, noDefaults+sizeCSS+`#b { width: 200px }`)
	f := find(t, root, "b")
	px(t, "the border box of a border-box element", f.BorderRect.W, 200)
	px(t, "its content box", f.ContentRect().W, 170)
}

func TestContentBoxIsStillTheInitialValue(t *testing.T) {
	// The other half of the pair: the same document without the declaration. A
	// change that made border-box the default everywhere would pass the test
	// above and fail this one.
	root := layoutOf(t, 600, `<div id="b">x</div>`,
		noDefaults+`#b { width: 200px; padding-left: 10px; padding-right: 10px }`)
	f := find(t, root, "b")
	px(t, "the border box of a content-box element", f.BorderRect.W, 220)
	px(t, "its content box", f.ContentRect().W, 200)
}

func TestBorderBoxHeightTakesThePaddingOut(t *testing.T) {
	root := layoutOf(t, 600, `<div id="b"></div>`,
		noDefaults+`#b { box-sizing: border-box; height: 100px;
			padding-top: 20px; padding-bottom: 20px;
			border-top-style: solid; border-top-width: 5px }`)
	f := find(t, root, "b")
	px(t, "the border box height", f.BorderRect.H, 100)
	px(t, "the content box height", f.ContentRect().H, 55)
}

func TestBorderBoxMinWidthIsNotDoubleCounted(t *testing.T) {
	// The interaction the naive implementation gets wrong. min-width is a
	// border-box value too, so the used border box is 150 and the content box is
	// 150 - 30 = 120. An implementation that converted the width to a content
	// value first and then clamped it against min-width would compare 170 - or
	// 70 - against 150 and produce a content box of 150, a border box of 180.
	root := layoutOf(t, 600, `<div id="b">x</div>`,
		noDefaults+sizeCSS+`#b { width: 100px; min-width: 150px }`)
	f := find(t, root, "b")
	px(t, "the border box under a border-box minimum", f.BorderRect.W, 150)
	px(t, "its content box", f.ContentRect().W, 120)
}

func TestBorderBoxMaxWidthIsNotDoubleCounted(t *testing.T) {
	root := layoutOf(t, 600, `<div id="b">x</div>`,
		noDefaults+sizeCSS+`#b { width: 300px; max-width: 120px }`)
	f := find(t, root, "b")
	px(t, "the border box under a border-box maximum", f.BorderRect.W, 120)
	px(t, "its content box", f.ContentRect().W, 90)
}

func TestBorderBoxMaxWidthAppliesToAnAutoWidth(t *testing.T) {
	// A box with no declared width fills its containing block, and the maximum
	// still names the border box. 600 available, capped at 120: a 120px border
	// box with 90px of content.
	root := layoutOf(t, 600, `<div id="b">x</div>`,
		noDefaults+sizeCSS+`#b { max-width: 120px }`)
	f := find(t, root, "b")
	px(t, "an auto width under a border-box maximum", f.BorderRect.W, 120)
	px(t, "its content box", f.ContentRect().W, 90)
}

func TestBorderBoxWidthSmallerThanItsOwnPadding(t *testing.T) {
	// The declaration is legal and impossible: the content box cannot be
	// negative, so it is zero and the box is as wide as its own padding and
	// border. A negative content width would make every comparison downstream
	// give a plausible wrong answer.
	root := layoutOf(t, 600, `<div id="b">x</div>`, noDefaults+sizeCSS+`#b { width: 10px }`)
	f := find(t, root, "b")
	px(t, "the content box of an over-padded border box", f.ContentRect().W, 0)
	px(t, "its border box", f.BorderRect.W, 30)
}

func TestBorderBoxAppliesToAFloat(t *testing.T) {
	// A float's declared width goes through the same resolution, and a float is
	// where a wrong width is most visible: everything beside it moves.
	root := layoutOf(t, 600, `<div><div id="b">x</div></div>`,
		noDefaults+sizeCSS+`#b { float: left; width: 200px }`)
	px(t, "a floated border box", find(t, root, "b").BorderRect.W, 200)
}

func TestBorderBoxAppliesToAnInlineBlock(t *testing.T) {
	root := layoutOf(t, 600, `<div><span id="b">x</span></div>`,
		noDefaults+sizeCSS+`#b { display: inline-block; width: 200px }`)
	px(t, "an inline-block border box", find(t, root, "b").BorderRect.W, 200)
}

func TestBorderBoxAppliesToAnAbsolutelyPositionedBox(t *testing.T) {
	// §10.3.7 solves for a content width with the padding and border in the
	// constraint separately, so a declared width that already includes them has
	// to have them taken out before it enters the equation.
	root := layoutOf(t, 600, `<div id="o"><div id="b">x</div></div>`,
		noDefaults+sizeCSS+`#o { position: relative; width: 400px; height: 100px }
		 #b { position: absolute; left: 0; width: 200px }`)
	f := find(t, root, "b")
	px(t, "an absolutely positioned border box", f.BorderRect.W, 200)
	px(t, "its content box", f.ContentRect().W, 170)
}

func TestBorderBoxAppliesToAReplacedElement(t *testing.T) {
	// A replaced element is sized by §10.4's constraint table, which is stated
	// about the content box — so the declaration is converted before the table
	// rather than after it, or the ratio it works to would be the wrong one.
	root := replacedLayout(t, 600, `<div><img id="b" src="wide.png"></div>`,
		noDefaults+`#b { box-sizing: border-box; width: 100px; height: 60px;
			padding-left: 10px; padding-right: 10px;
			padding-top: 5px; padding-bottom: 5px }`)
	f := find(t, root, "b")
	px(t, "a replaced border box", f.BorderRect.W, 100)
	px(t, "its content box", f.ContentRect().W, 80)
	px(t, "its border box height", f.BorderRect.H, 60)
	px(t, "its content box height", f.ContentRect().H, 50)
}

func TestBorderBoxOnATableIsReported(t *testing.T) {
	// §17.5.2 reads the declared widths of a table's cells and columns directly,
	// so box-sizing does not reach them. A table laid out to the wrong model is a
	// plausible table with every column too wide, which is exactly the silent
	// failure the finding vocabulary exists for.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<table><tr><td id="c">a</td></tr></table>`,
		CSS:  []Stylesheet{{Source: `#c { box-sizing: border-box; width: 100px }`}},
	})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, nil, rec)

	found := 0
	for _, f := range rec.Findings() {
		if f.Property == "box-sizing" {
			found++
		}
	}
	if found != 1 {
		t.Errorf("box-sizing on a table cell was reported %d times, want once", found)
	}
}

func TestBorderBoxWidensAnIntrinsicWidth(t *testing.T) {
	// outerWidths adds a box's own padding and border to its content width. Under
	// border-box the declared width already holds them, so a version that did not
	// convert would count both and make the float 230px.
	root := layoutOf(t, 600, `<div><div id="b">x</div></div>`,
		noDefaults+sizeCSS+`#b { float: left; width: 200px }`)
	px(t, "a float declared border-box", find(t, root, "b").MarginRect().W, 200)
}

// TestAnIntrinsicSizeIsReportedRatherThanDropped pins the guardrail on the
// keywords this engine does not accept as a declared size.
//
// "width: min-content" is correct CSS Sizing, and parseLength reads it as not a
// length at all — so the declaration vanishes and the box takes its automatic
// width, which for a block is the whole containing block and about the widest
// wrong answer there is. It was found in the white-space intrinsic-size tests,
// where a box that should have been 50px came out 626px with no finding at all.
//
// The two width keywords that are now applied moved to the second list, which is
// the assertion that matters most here: the guardrail has to stop reporting
// exactly what the engine started doing, and a finding left behind on an applied
// declaration is a caller told their page is wrong when it is right.
func TestAnIntrinsicSizeIsReportedRatherThanDropped(t *testing.T) {
	report := func(t *testing.T, decl string) []Finding {
		t.Helper()
		rec := NewRecorder(nil)
		built := Build(Input{
			HTML: `<div id="b">x</div>`,
			CSS:  []Stylesheet{{Source: `#b { ` + decl + ` }`}},
		})
		Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, nil, rec)
		var out []Finding
		for _, f := range rec.Findings() {
			if f.Rule == RuleUnsupportedValue {
				out = append(out, f)
			}
		}
		return out
	}

	for _, decl := range []string{
		"width: fit-content", "width: stretch", "width: fit-content(20px)",
		"min-width: min-content", "max-width: max-content",
		"height: min-content", "min-height: min-content",
		"max-height: max-content",
	} {
		got := report(t, decl)
		if len(got) != 1 {
			t.Errorf("%q produced %d findings, want one — the declaration is dropped "+
				"and the box is laid out at its automatic size, which nothing else "+
				"about the page reveals", decl, len(got))
			continue
		}
		prop := decl[:strings.IndexByte(decl, ':')]
		if !strings.Contains(got[0].Message, prop) || got[0].Property != prop {
			t.Errorf("%q was reported as %q on %q, which does not name the property",
				decl, got[0].Message, got[0].Property)
		}
	}

	// And not for a value that is applied, nor for one where the keyword is not
	// correct CSS. An engine that reported "auto" would report nearly every box
	// in every document; one that reported a margin would send an author looking
	// for a feature instead of for the typo they made.
	for _, decl := range []string{
		"width: auto", "width: 100px", "width: 50%",
		"margin-left: min-content", "padding-top: max-content",
		"font-size: min-content",
		// Applied, so not reported. The case difference is here because the
		// keyword arrives from the cascade as the author wrote it.
		"width: min-content", "width: max-content", "width: MIN-CONTENT",
	} {
		if got := report(t, decl); len(got) != 0 {
			t.Errorf("%q was reported as an unsupported value (%q); it is either "+
				"applied or not correct CSS on that property", decl, got[0].Message)
		}
	}

	// Once per element, however many of its sizes name one.
	if got := report(t, "width: min-content; height: max-content"); len(got) != 1 {
		t.Errorf("a box with two intrinsic sizes was reported %d times, want once", len(got))
	}
}
