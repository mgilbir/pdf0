package render

import "testing"

// §10.3.7's right-to-left half, and §10.4 where a limit changes what a box's
// descendants resolve against.
//
// CSS 2.1 §10.3.7 is written as one algorithm with the direction appearing
// twice, and an engine that reads only the left-to-right side of each sentence
// passes every test written in English prose and fails every one written in
// Hebrew or Arabic — or, as the suite has it, every one that simply declares
// "direction: rtl" on a box of Latin text. The two places are:
//
//   - "if 'direction' of the element establishing the static-position containing
//     block is 'ltr' set 'left' to the static position; else set 'right' to the
//     static position", which decides where a box with no offsets at all goes;
//   - "unless this would make them negative, in which case when direction of the
//     containing block is 'ltr' ('rtl'), set 'margin-left' ('margin-right') to
//     zero and solve for 'margin-right' ('margin-left')", which decides which of
//     a pair of over-constrained automatic margins gives way.
//
// The two key on *different* elements, which is why they are two flags and not
// one: the first on the element the box would have flowed in, the second on the
// element whose padding box the offsets are measured against. They are the same
// element in every test below, and the code keeps them apart because a
// "position: relative" wrapper inside a right-to-left paragraph is the ordinary
// case where they are not.
//
// Every number here is derived from the constraint of §10.3.7 — that left,
// margin-left, border, padding, width, margin-right and right add up to the
// containing block's width — and written out as the arithmetic that produced it.

// rtlPage is the width laid out in. It is far wider than the containing block
// below, so nothing here is decided by the page.
const rtlPage = 400

// TestRTLStaticPositionAnchorsTheRightEdge is §10.3.7's third rule where the
// static-position containing block runs right to left.
func TestRTLStaticPositionAnchorsTheRightEdge(t *testing.T) {
	// #cb is 200 wide and positioned, so it is both the containing block and the
	// element establishing the static position. #a has no offsets and no width,
	// so §10.3.7 gives it the shrink-to-fit width of its content — one 60px block
	// — and then anchors the edge the direction names.
	//
	//   left + 0 + 0 + 60 + 0 + right = 200
	//
	// with right = the static position = 0, because the hypothetical box is
	// block-level and its right margin edge would have been at #cb's content
	// right edge. So left = 140.
	src := `<div id="cb"><div id="a"><div id="k"></div></div></div>`
	sheet := noDefaults + `
	  #cb { position: relative; width: 200px; height: 200px }
	  #a { position: absolute }
	  #k { width: 60px; height: 20px }`

	root := layoutOf(t, rtlPage, src, sheet+" #cb { direction: rtl }")
	a := find(t, root, "a")
	px(t, "the shrink-to-fit width", a.BorderRect.W, 60)
	px(t, "a right-to-left static position", a.BorderRect.X, 140)

	// The same document left to right, which is the number that would also come
	// out if the direction were ignored — so the test above can only pass by
	// reading it.
	root = layoutOf(t, rtlPage, src, sheet+" #cb { direction: ltr }")
	px(t, "a left-to-right static position", find(t, root, "a").BorderRect.X, 0)
}

// TestRTLStaticPositionWithADeclaredWidth is the same rule where the width is
// given and only the two offsets are auto, which is §10.3.7's "solve for 'left'"
// case rather than its shrink-to-fit one.
func TestRTLStaticPositionWithADeclaredWidth(t *testing.T) {
	// left + 50 + right = 200 with right = 0, so left = 150.
	src := `<div id="cb"><div id="a"></div></div>`
	sheet := noDefaults + `
	  #cb { position: relative; width: 200px; height: 200px }
	  #a { position: absolute; width: 50px; height: 20px }`

	root := layoutOf(t, rtlPage, src, sheet+" #cb { direction: rtl }")
	px(t, "a right-to-left static position", find(t, root, "a").BorderRect.X, 150)

	root = layoutOf(t, rtlPage, src, sheet+" #cb { direction: ltr }")
	px(t, "a left-to-right static position", find(t, root, "a").BorderRect.X, 0)
}

// TestRTLStaticPositionOfABoxWrittenAmongTheWords is §10.3.7's hypothetical box
// where it sits on a line rather than filling the width.
//
// This is the case where the two static positions are genuinely different
// numbers rather than both being nought, and it is what makes them two fields.
// The pen is recorded in *logical* order — the order the content is written —
// so on a right-to-left line it has already counted from the right, and the
// mirror is a subtraction from the block's content right edge rather than a
// negation of the left-hand number.
func TestRTLStaticPositionOfABoxWrittenAmongTheWords(t *testing.T) {
	// A 30px inline-block, then an absolutely positioned span 50px wide. The
	// containing block is 200 wide, so:
	//
	//   left to right: the pen is 30 from the left, and the box goes there.
	//   right to left: the pen is 30 from the right, so right = 30 and
	//                  left = 200 − 30 − 50 = 120.
	//
	// Every one of 0, 30, 120 and 150 is a *different* number, so no reading of
	// this can pass by accident: 0 is "ignore the pen", 150 is "ignore the pen
	// and mirror", 30 is "mirror nothing".
	src := `<div id="cb"><span id="pre"></span><span id="a"></span></div>`
	sheet := noDefaults + `
	  #cb { position: relative; width: 200px; height: 200px }
	  #pre { display: inline-block; width: 30px; height: 10px }
	  #a { display: inline; position: absolute; width: 50px; height: 20px }`

	root := layoutOf(t, rtlPage, src, sheet+" #cb { direction: rtl }")
	// The inline-block is at the line's start edge, which right to left is the
	// right — without which the pen below would not be 30 from the right and the
	// expected number would be something else.
	px(t, "the preceding inline-block, right to left",
		find(t, root, "pre").BorderRect.X, 170)
	px(t, "a pen position mirrored", find(t, root, "a").BorderRect.X, 120)

	root = layoutOf(t, rtlPage, src, sheet+" #cb { direction: ltr }")
	px(t, "the preceding inline-block, left to right",
		find(t, root, "pre").BorderRect.X, 0)
	px(t, "a pen position", find(t, root, "a").BorderRect.X, 30)
}

// TestRTLStaticPositionOfASpecifiedInlineBox is the degenerate case of the
// above, and is here because it is the one where the two readings agree.
//
// §9.7 blockifies an absolutely positioned box, so by the time layout sees it
// every one is block-level. §10.3.7's hypothetical box is the box it would have
// been with "position: static", where there is no blockification — so a <span>
// would have been an inline box on a line. With nothing before it the pen is at
// the line's start edge, which is where a block box's leading edge would be
// too, so both readings give the same answer and neither can be wrong.
func TestRTLStaticPositionOfASpecifiedInlineBox(t *testing.T) {
	root := layoutOf(t, rtlPage,
		`<div id="cb"><span id="a"></span></div>`,
		noDefaults+`
		  #cb { position: relative; direction: rtl; width: 200px; height: 200px }
		  #a { display: inline; position: absolute; width: 50px; height: 20px }`)
	px(t, "an empty inline hypothetical box, right to left",
		find(t, root, "a").BorderRect.X, 150)
}

// TestRTLOverConstrainedAutoMarginsGiveWayOnTheRight is §10.3.7's other
// direction-dependent sentence.
func TestRTLOverConstrainedAutoMarginsGiveWayOnTheRight(t *testing.T) {
	// left + margin-left + width + margin-right + right = 200 with
	// 100 + m + 100 + m + 100 = 200, so each margin would be -50. The
	// specification refuses that: the margin at the *end* of the containing
	// block's inline direction goes to zero and the other absorbs the whole
	// difference, so right to left gives margin-right 0 and margin-left -100.
	//
	// The border box then lands at left + margin-left = 100 - 100 = 0.
	src := `<div id="cb"><div id="a"></div></div>`
	sheet := noDefaults + `
	  #cb { position: relative; width: 200px; height: 200px }
	  #a { position: absolute; left: 100px; right: 100px; width: 100px;
	       height: 20px; margin-left: auto; margin-right: auto }`

	root := layoutOf(t, rtlPage, src, sheet+" #cb { direction: rtl }")
	px(t, "an over-constrained rtl box", find(t, root, "a").BorderRect.X, 0)

	// Left to right the other margin gives way, so the box stays where its left
	// offset put it and overflows to the right.
	root = layoutOf(t, rtlPage, src, sheet+" #cb { direction: ltr }")
	px(t, "an over-constrained ltr box", find(t, root, "a").BorderRect.X, 100)

	// And with room to spare the margins really are equal, in both directions —
	// so the rule above is the exception it is written as and not a rewrite of
	// the ordinary case. 100 + m + 40 + m + 20 = 200 gives m = 20 either way.
	centred := noDefaults + `
	  #cb { position: relative; width: 200px; height: 200px }
	  #a { position: absolute; left: 100px; right: 20px; width: 40px;
	       height: 20px; margin-left: auto; margin-right: auto }`
	for _, dir := range []string{"rtl", "ltr"} {
		root = layoutOf(t, rtlPage, src, centred+" #cb { direction: "+dir+" }")
		px(t, "equal auto margins, "+dir, find(t, root, "a").BorderRect.X, 120)
	}
}

// TestAbsoluteHeightIsClampedBeforeItsContentIsLaidOut is §10.4 reaching
// through a box into its descendants.
//
// §10.7's clamp is usually described as something that happens to a box, and for
// the box itself that is enough. It is not enough for what is inside it: the
// used height is what a percentage height resolves against, so a maximum that
// cuts the box down has to cut it down *before* the content is laid out. Doing
// it afterwards gives a child asking for half of its parent half of a height its
// parent never had, and the child then hangs out of the bottom of a box it was
// written to fit inside.
func TestAbsoluteHeightIsClampedBeforeItsContentIsLaidOut(t *testing.T) {
	root := layoutOf(t, rtlPage,
		`<div id="a"><div id="k"></div></div>`,
		noDefaults+`
		  #a { position: absolute; width: 64px; height: 64px; max-height: 32px }
		  #k { height: 50%; width: 10px }`)
	px(t, "the box's own clamped height", find(t, root, "a").BorderRect.H, 32)
	px(t, "half of the clamped height", find(t, root, "k").BorderRect.H, 16)

	// The minimum reaches the same way, and in the other direction: a declared
	// height of 16 held open to 32 makes half of it 16 rather than 8.
	root = layoutOf(t, rtlPage,
		`<div id="a"><div id="k"></div></div>`,
		noDefaults+`
		  #a { position: absolute; width: 64px; height: 16px; min-height: 32px }
		  #k { height: 50%; width: 10px }`)
	px(t, "the box's own held-open height", find(t, root, "a").BorderRect.H, 32)
	px(t, "half of the held-open height", find(t, root, "k").BorderRect.H, 16)
}
