package render

import "testing"

// What a box contributes to the width its parent shrinks to fit.
//
// CSS 2.1 §10.3.5 sizes a float by two numbers taken from its content, and CSS
// Sizing names them: the min-content and max-content *contributions* of the
// children. A contribution is what the child will actually take up once it has
// been laid out, which is not the same as what its content would like — a child
// whose own maximum cuts it down contributes the cut-down number, and a parent
// that measured the uncut one comes out wider than anything inside it, with the
// page showing through the difference.
//
// The two rules here are the two halves of that, and they are worth pinning
// separately because only one of them is visible to the reftest suite. Measured
// over the whole of the W3C CSS 2.1 suite, the maximum and minimum moved
// eighteen tests and the percentage moved none — so the percentage's evidence is
// entirely here, and this is the fixture it was written for rather than a
// restatement of something a reftest already checks.
//
// Every number is a declared length, so nothing below depends on a font.

// TestIntrinsicContributionHonoursAMaximumWidth is §10.4 seen from the parent.
func TestIntrinsicContributionHonoursAMaximumWidth(t *testing.T) {
	// The float shrinks to fit, and the only thing in it is a block that asks for
	// 200 and is cut to 50. The page is 1000 wide, so a float sized by the
	// uncut number would be 200 and would fit — the maximum is the only thing
	// that can produce 50.
	root := layoutOf(t, 1000,
		`<div id="f"><div id="k"></div></div>`,
		noDefaults+`
		  #f { float: left }
		  #k { width: 200px; max-width: 50px; height: 10px }`)
	px(t, "a float over a child with a maximum", find(t, root, "f").BorderRect.W, 50)
	px(t, "the child itself", find(t, root, "k").BorderRect.W, 50)
}

// TestIntrinsicContributionHonoursAMinimumWidth is the other limit, which moves
// the number the other way and so cannot be produced by the same mistake.
func TestIntrinsicContributionHonoursAMinimumWidth(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="f"><div id="k"></div></div>`,
		noDefaults+`
		  #f { float: left }
		  #k { width: 20px; min-width: 80px; height: 10px }`)
	px(t, "a float over a child with a minimum", find(t, root, "f").BorderRect.W, 80)
	px(t, "the child itself", find(t, root, "k").BorderRect.W, 80)
}

// TestAMinimumWidthBeatsAMaximumInAnIntrinsicContribution is §10.4's order,
// which is the same everywhere in CSS and is easy to get backwards in a clamp.
//
// What it guards is the *order the clamp applies the two in* — style.Clamp puts
// the maximum first and the minimum last — rather than any clause of its own.
// An explicit "the maximum is at least the minimum" was written beside the
// clamp and then deleted: planting its removal moved nothing, because it can
// never be the thing that decides. This is where the rule is checked instead.
func TestAMinimumWidthBeatsAMaximumInAnIntrinsicContribution(t *testing.T) {
	// 80 as the minimum, 50 as the maximum: they contradict, and the minimum
	// wins. Clamping in the other order would give 50.
	root := layoutOf(t, 1000,
		`<div id="f"><div id="k"></div></div>`,
		noDefaults+`
		  #f { float: left }
		  #k { width: 200px; min-width: 80px; max-width: 50px; height: 10px }`)
	px(t, "a minimum against a smaller maximum", find(t, root, "f").BorderRect.W, 80)
}

// TestAPercentageWidthContributesAsThoughItWereAuto is CSS Sizing's rule for a
// percentage with nothing to be a percentage of.
//
// An intrinsic width is measured before any containing block exists, so there is
// no basis. The rule is that such a percentage behaves as "auto" — as *no
// declaration at all*, so the box contributes what its own content wants.
// Resolving it against a basis of nought is the plausible wrong answer and the
// one that was here: it makes the declaration mean "width: 0", so a float
// holding nothing else comes out empty and its content hangs outside it.
func TestAPercentageWidthContributesAsThoughItWereAuto(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="f"><div id="k"><div id="g"></div></div></div>`,
		noDefaults+`
		  #f { float: left }
		  #k { width: 50% }
		  #g { width: 70px; height: 10px }`)
	// The float is as wide as the grandchild, because the percentage said
	// nothing about how wide the child wants to be.
	px(t, "a float over a percentage-width child", find(t, root, "f").BorderRect.W, 70)
	// And the percentage then resolves against the float, which is what makes
	// the answer "as though auto" rather than "as though 70px": half of 70.
	px(t, "the percentage against the settled float", find(t, root, "k").BorderRect.W, 35)
}

// TestAPercentageLimitContributesNothing is the same rule for the two limits.
//
// A percentage maximum against no basis behaves as "none" and a percentage
// minimum as zero, which for a contribution is the same thing: neither
// constrains. Resolving either against nought would make "max-width: 50%" mean
// "max-width: 0" and collapse the float to nothing.
func TestAPercentageLimitContributesNothing(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="f"><div id="k"></div></div>`,
		noDefaults+`
		  #f { float: left }
		  #k { width: 70px; max-width: 50%; height: 10px }`)
	px(t, "a float over a percentage maximum", find(t, root, "f").BorderRect.W, 70)
}
