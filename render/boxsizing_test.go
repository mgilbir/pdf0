package render

import "testing"

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
