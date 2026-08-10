package render

import "testing"

// A percentage height, and the condition CSS 2.1 attaches to it.
//
// §10.5 states the rule and its condition in one sentence:
//
//	The percentage is calculated with respect to the height of the generated
//	box's containing block. If the height of the containing block is not
//	specified explicitly (i.e., it depends on content height), and this element
//	is not absolutely positioned, the value computes to 'auto'.
//
// §10.7 says the same of 'min-height' and 'max-height', with a different answer
// for the unresolvable case: '0' for a minimum and 'none' for a maximum.
//
// Both halves have to be pinned, and the second is the one worth insisting on.
// This engine used to refuse *every* percentage height, which honours the
// condition perfectly and never honours the rule; the two tests that would have
// caught that are the ones below with a definite containing block in them.
//
// The failure mode on the other side is worse and was also present, one function
// away from a comment warning against it: a percentage minimum or maximum
// resolved against the containing block's *width*. That produces a number rather
// than an obvious wrong answer, and it is a number that happens to be right
// whenever the containing block is square. So every expected value here is
// computed from a containing block whose width and height differ, and each is
// written out as the arithmetic that produced it.
//
// layoutOf lays out in a page 10000 CSS pixels tall, which is what a percentage
// against the initial containing block resolves against.
const pageHeight = 10000

// TestPercentageHeightResolvesAgainstADefiniteContainingBlock is §10.5's rule
// where its condition is met.
func TestPercentageHeightResolvesAgainstADefiniteContainingBlock(t *testing.T) {
	// 25% of 400 is 100, and the containing block is 800 wide so that a
	// percentage taken of the width would be 200 and could not be mistaken for
	// the right answer.
	root := layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+"#outer { height: 400px } #a { height: 25% }")
	px(t, "25% of a 400px containing block", find(t, root, "a").BorderRect.H, 100)

	// The chain the CSS 2.1 reftests are built on: the initial containing block
	// is the page, whose height is settled before layout runs, so "height: 100%"
	// on the root is definite and each descendant that declares one passes a
	// definite height down in turn.
	root = layoutOf(t, 800, `<div id="a"></div>`,
		noDefaults+"html, body, #a { height: 100% }")
	px(t, "a 100% chain from the page", find(t, root, "a").BorderRect.H, pageHeight)

	// And it compounds: a percentage height is itself a specified height, so the
	// box that has one is a definite containing block for its own children.
	root = layoutOf(t, 800,
		`<div id="outer"><div id="mid"><div id="a"></div></div></div>`,
		noDefaults+"#outer { height: 400px } #mid { height: 50% } #a { height: 50% }")
	px(t, "50% of 400", find(t, root, "mid").BorderRect.H, 200)
	px(t, "50% of that 200", find(t, root, "a").BorderRect.H, 100)
}

// TestPercentageHeightAgainstAnIndefiniteContainingBlockIsAuto is §10.5's
// condition: the half of the rule that refuses.
func TestPercentageHeightAgainstAnIndefiniteContainingBlockIsAuto(t *testing.T) {
	// #outer has no height of its own, so #a's percentage computes to auto and
	// #a is as tall as its content. The content is one 60px box, and the page is
	// 800 wide — so the three candidate answers are distinct: 60 if the rule is
	// applied, 400 if the percentage were taken of the width, and 5000 if it were
	// taken of the page.
	root := layoutOf(t, 800,
		`<div id="outer"><div id="a"><div id="kid"></div></div></div>`,
		noDefaults+"#kid { height: 60px } #a { height: 50% }")
	px(t, "a percentage of an indefinite height", find(t, root, "a").BorderRect.H, 60)
}

// TestPercentageMinAndMaxHeightUseTheHeight is §10.7 where its condition is met,
// and is the test that fails if the basis is the containing block's width.
func TestPercentageMinAndMaxHeightUseTheHeight(t *testing.T) {
	// A containing block 800 wide and 100 tall. 50% of the height is 50; 50% of
	// the width would be 400. #a is empty, so its content height is 0 and the
	// minimum is the only thing holding it open.
	root := layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+"#outer { height: 100px } #a { min-height: 50% }")
	px(t, "min-height: 50% of a 100px height", find(t, root, "a").BorderRect.H, 50)

	// The maximum, against the same containing block: 25% of 100 is 25, and the
	// declared 90px is cut down to it. 25% of the width would be 200, which would
	// not cut 90px down at all — so a wrong basis shows up here as no clamp
	// rather than as the wrong clamp.
	root = layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+"#outer { height: 100px } #a { height: 90px; max-height: 25% }")
	px(t, "max-height: 25% of a 100px height", find(t, root, "a").BorderRect.H, 25)

	// A minimum beats a maximum, which is the CSS rule the two are applied in the
	// order of. 60% of 100 is 60 and 20% of 100 is 20; the answer is 60.
	root = layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+"#outer { height: 100px } #a { min-height: 60%; max-height: 20% }")
	px(t, "a minimum over a maximum", find(t, root, "a").BorderRect.H, 60)
}

// TestPercentageMinAndMaxHeightAgainstAnIndefiniteContainingBlock is §10.7's
// condition, whose two halves differ from each other and from §10.5's.
func TestPercentageMinAndMaxHeightAgainstAnIndefiniteContainingBlock(t *testing.T) {
	// #outer's height depends on its content, so the minimum is treated as zero
	// and #a is as tall as its own content: 40. Against the width it would have
	// been 400.
	root := layoutOf(t, 800,
		`<div id="outer"><div id="a"><div id="kid"></div></div></div>`,
		noDefaults+"#kid { height: 40px } #a { min-height: 50% }")
	px(t, "min-height: 50% with no height to take it of",
		find(t, root, "a").BorderRect.H, 40)

	// The maximum is treated as 'none' rather than as zero, so the declared
	// height stands whole. Against the width, 10% of 800 is 80, which would have
	// cut 300 down — the two answers are far apart on purpose.
	root = layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+"#a { height: 300px; max-height: 10% }")
	px(t, "max-height: 10% with no height to take it of",
		find(t, root, "a").BorderRect.H, 300)
}

// TestPercentageHeightIsADeclaredHeightForBoxSizing pins that the percentage
// enters the box model at the same place an absolute length does. Under
// "border-box" the resolved percentage names the border box, so the padding
// comes out of it rather than being added to it.
func TestPercentageHeightIsADeclaredHeightForBoxSizing(t *testing.T) {
	// 50% of 200 is 100, which is the border box; 10px of padding at each end
	// leaves 80 of content.
	root := layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+`#outer { height: 200px }
		#a { height: 50%; box-sizing: border-box;
		     padding-top: 10px; padding-bottom: 10px }`)
	a := find(t, root, "a")
	px(t, "the border box under border-box", a.BorderRect.H, 100)
	px(t, "the content box under border-box", a.ContentRect().H, 80)

	// The default is content-box, where the same 100 is the content and the
	// padding is added: a 120px border box.
	root = layoutOf(t, 800, `<div id="outer"><div id="a"></div></div>`,
		noDefaults+`#outer { height: 200px }
		#a { height: 50%; padding-top: 10px; padding-bottom: 10px }`)
	a = find(t, root, "a")
	px(t, "the content box under content-box", a.ContentRect().H, 100)
	px(t, "the border box under content-box", a.BorderRect.H, 120)
}

// TestPercentageHeightForAnAbsolutelyPositionedBox pins §10.5's and §10.7's
// other clause: "and this element is not absolutely positioned".
//
// The containing block of an absolutely positioned box is a rectangle that has
// already been laid out by the time the box is placed, so its height is definite
// whatever its own height property said — and the percentage resolves where the
// same percentage in the normal flow would not.
func TestPercentageHeightForAnAbsolutelyPositionedBox(t *testing.T) {
	const src = `<div id="rel"><div id="a"></div></div>`

	// The containing block is the *padding* box of the relatively positioned
	// ancestor, which is 300 tall and 800 wide. 50% of the height is 150.
	root := layoutOf(t, 800, src, noDefaults+`
		#rel { position: relative; height: 300px }
		#a { position: absolute; height: 50% }`)
	px(t, "50% of an abspos containing block", find(t, root, "a").BorderRect.H, 150)

	// The minimum resolves against the same height, not the same width: 50% of
	// 300 is 150 and 50% of 800 would be 400.
	root = layoutOf(t, 800, src, noDefaults+`
		#rel { position: relative; height: 300px }
		#a { position: absolute; min-height: 50% }`)
	px(t, "an abspos min-height: 50%", find(t, root, "a").BorderRect.H, 150)

	// And the maximum: 20% of 300 is 60, cutting the declared 250 down. Against
	// the width it would be 160, which is also a clamp — so this one is written
	// with a declared height between the two answers so that either basis clamps
	// and only one clamps to the right number.
	root = layoutOf(t, 800, src, noDefaults+`
		#rel { position: relative; height: 300px }
		#a { position: absolute; height: 250px; max-height: 20% }`)
	px(t, "an abspos max-height: 20%", find(t, root, "a").BorderRect.H, 60)
}

// TestResolvedPercentageHeightSealsTheBottomMargin pins the consequence of
// §10.5 that is not about the height at all.
//
// §8.3.1 lets a box's bottom margin collapse with its last child's only when the
// box's own height is 'auto'. A percentage that resolves is not auto, so the two
// margins stay apart — and a percentage that does not resolve computes to auto,
// so they collapse. The same declaration, both ways round.
func TestResolvedPercentageHeightSealsTheBottomMargin(t *testing.T) {
	const src = `<div id="outer"><div id="a"><div id="kid"></div></div>` +
		`<div id="after"></div></div>`

	// Definite: #a's height is 100 and its own bottom margin of 10 is separate
	// from the child's 30, so #after's top edge is 100 + 10 below #a's top. #a
	// starts at 0 because #outer has neither border nor padding and the top
	// margins collapse out.
	root := layoutOf(t, 800, src, noDefaults+`
		#outer { height: 200px } #a { height: 50%; margin-bottom: 10px }
		#kid { height: 20px; margin-bottom: 30px }
		#after { height: 5px }`)
	px(t, "a sealed box's height", find(t, root, "a").BorderRect.H, 100)
	px(t, "the box after a sealed one", find(t, root, "after").BorderRect.Y, 110)

	// Indefinite: the same percentage computes to auto, so #a is as tall as its
	// content — 20, the child's own margin having collapsed through the open
	// bottom edge — and the two bottom margins collapse to max(30, 10) = 30.
	// #after therefore starts at 20 + 30 = 50.
	root = layoutOf(t, 800, src, noDefaults+`
		#a { height: 50%; margin-bottom: 10px }
		#kid { height: 20px; margin-bottom: 30px }
		#after { height: 5px }`)
	px(t, "an unsealed box's height", find(t, root, "a").BorderRect.H, 20)
	px(t, "the box after an unsealed one", find(t, root, "after").BorderRect.Y, 50)
}
