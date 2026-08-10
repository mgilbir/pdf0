package render

import (
	"strings"
	"testing"
)

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

// TestAnIntrinsicKeywordSizesTheBox is CSS Sizing §3.2's two content keywords
// used as a declared width.
//
// The numbers are the text's, not a declaration's, which is the whole point of
// the keywords: "aaa bbbb" in Courier at 100px is "bbbb" at its widest
// unbreakable run and all eight characters — the space among them — when nothing
// breaks, so min-content is 240 and max-content is 480. A block's automatic
// width is the whole containing block, so an engine that dropped the declaration
// would give 1000 for both — the difference is not subtle and was invisible
// before, which is why it needed a guardrail before it needed an implementation.
func TestAnIntrinsicKeywordSizesTheBox(t *testing.T) {
	cases := map[string]float64{
		"min-content": 4 * ch,
		"max-content": 8 * ch,
		// Not implemented, so the box keeps its automatic width. It is here so
		// that the two lists cannot drift apart silently: a fit-content that
		// started being applied without its own evidence would fail here.
		"fit-content": 1000,
		"auto":        1000,
	}
	for value, want := range cases {
		root := layoutOf(t, 1000, `<div id="b">aaa bbbb</div>`,
			noDefaults+`#b { font-size: 100px; font-family: Courier; width: `+value+` }`)
		px(t, "width: "+value, find(t, root, "b").BorderRect.W, want)
	}
}

// TestAnIntrinsicKeywordIgnoresBoxSizing pins CSS Sizing §3.3's exception:
// "non-quantitative values such as auto and min-content are not influenced by
// the box-sizing property".
//
// It matters because every other declared width in this engine goes through the
// subtraction box-sizing asks for, and routing the keyword through it as well is
// the natural way to write it. That produces a box narrower than its own content
// by the padding and the border — the content then overflows a box that asked to
// be exactly as wide as its content, which reads as a line-breaking fault.
func TestAnIntrinsicKeywordIgnoresBoxSizing(t *testing.T) {
	for _, sizing := range []string{"content-box", "border-box"} {
		root := layoutOf(t, 1000, `<div id="b">aaa bbbb</div>`,
			noDefaults+`#b { font-size: 100px; font-family: Courier;
			  width: min-content; box-sizing: `+sizing+`;
			  padding-left: 30px; border-left: 20px solid }`)
		// The content box is min-content either way, so the border box is that
		// plus the fifty pixels of edge.
		px(t, "box-sizing: "+sizing, find(t, root, "b").BorderRect.W, 4*ch+50)
	}
}

// TestAnIntrinsicKeywordChildContributesOneNumber pins what a box sized to a
// keyword hands its parent.
//
// A child at "width: min-content" is that wide whether or not the parent wraps,
// exactly as a child at "width: 240px" is — so it contributes the same number to
// both of its parent's. A parent that measured the child's own *pair* would take
// the child's max-content as its own maximum and come out at 540, with the page
// showing through the three hundred pixels the child does not fill.
func TestAnIntrinsicKeywordChildContributesOneNumber(t *testing.T) {
	root := layoutOf(t, 1000,
		`<div id="f"><div id="k">aaa bbbb</div></div>`,
		noDefaults+`
		  #f { float: left }
		  #k { font-size: 100px; font-family: Courier; width: min-content }`)
	px(t, "a float over a min-content child", find(t, root, "f").BorderRect.W, 4*ch)
	px(t, "the child itself", find(t, root, "k").BorderRect.W, 4*ch)
}

// TestAnIntrinsicKeywordIsRefusedWhereItIsNotRead pins the boxes keywordWidth
// turns down, and pins that turning them down is *reported*.
//
// A table's width comes from §17.5.2 over its columns and a replaced element's
// from its own intrinsic size, and neither reads the sizing path the keyword
// arrives on. Answering for them would put a number into a calculation that
// throws it away, and — worse — would take away the finding that says the
// declaration was dropped. The pairing is the assertion: applied and silent, or
// dropped and reported, and never dropped and silent.
func TestAnIntrinsicKeywordIsRefusedWhereItIsNotRead(t *testing.T) {
	findings := func(t *testing.T, html, css string) []Finding {
		t.Helper()
		rec := NewRecorder(nil)
		built := Build(Input{HTML: html, CSS: []Stylesheet{{Source: css}}})
		Layout(built.Root, Size{W: picPx(1000), H: picPx(10000)}, nil, rec)
		var out []Finding
		for _, f := range rec.Findings() {
			if f.Rule == RuleUnsupportedValue && f.Property == "width" {
				out = append(out, f)
			}
		}
		return out
	}

	got := findings(t, `<table id="b"><tr><td>aaa bbbb</td></tr></table>`,
		`#b { width: min-content }`)
	if len(got) != 1 || !strings.Contains(got[0].Message, "min-content") {
		t.Errorf("a table at width: min-content produced %d findings, want one "+
			"saying the declaration was dropped", len(got))
	}

	// And a box that *is* read reports nothing, so the test above cannot pass
	// merely because everything is reported.
	if got := findings(t, `<div id="b">aaa bbbb</div>`, `#b { width: min-content }`); len(got) != 0 {
		t.Errorf("a block at width: min-content was reported as unsupported (%q)",
			got[0].Message)
	}
}

// TestATrimmedSpaceLeavesTheUnbreakableRun pins the half of §4.1.2's line-edge
// removal that the maximum width already had and the minimum did not.
//
// The two numbers usually agree about it for free: a space a line may end at
// ends the unbreakable run as well, so there is nothing left in the run for a
// line edge to remove. Where the text may *not* break the space joins the run,
// and a minimum measured without subtracting it is wider than the text by the
// trailing space of every line.
//
// It is pinned here rather than by a reftest because no reftest can see it
// alone: measured over the whole suite this change moves nothing on its own, and
// moves seven tests together with "width: min-content" — which is the shape of
// pair that has to be written down, since either one alone reads as dead code.
//
// The assertion has to go through the keyword, and the reason is worth stating
// because the obvious fixture is a float and a float cannot show it. A float's
// width is min(max(minimum, available), preferred), so the preferred width caps
// the minimum — and the preferred width already had the trailing space taken
// off. The mistake makes the minimum *larger* than the maximum, which is exactly
// the shape shrink-to-fit throws away. A declared "width: min-content" reads the
// minimum on its own and is the only route in this engine that does.
func TestATrimmedSpaceLeavesTheUnbreakableRun(t *testing.T) {
	// "aa " is three characters and the trailing space is collapsible, so
	// §4.1.2 removes it at the line edge and the box is two characters wide.
	// Nowrap is what puts the space inside the unbreakable run.
	root := layoutOf(t, 1000, `<div id="b">aa </div>`,
		noDefaults+`#b { font-size: 100px; font-family: Courier;
		  white-space: nowrap; width: min-content }`)
	px(t, "a nowrap block at width: min-content",
		find(t, root, "b").BorderRect.W, 2*ch)

	// A space that is *not* removed at a line edge still takes room, which is
	// what stops the rule above from being "drop every trailing space". A
	// preserved one under "pre" is content: "aa " is three characters wide.
	root = layoutOf(t, 1000, `<div id="b">aa </div>`,
		noDefaults+`#b { font-size: 100px; font-family: Courier;
		  white-space: pre; width: min-content }`)
	px(t, "a pre block at width: min-content",
		find(t, root, "b").BorderRect.W, 3*ch)
}

// TestASpaceBeforeAnAtomicInlineIsNotTrailing pins that an atomic inline ends
// the run of trailing white space, exactly as a word does.
//
// A space is only trailing if nothing follows it, and the measurement tracks
// that by clearing its running total whenever it meets content. It met a word
// and it met a forced break; it did not meet a picture or an inline-block, so
// "aa <img>" measured as though the space were at the end of the line and came
// out one space short — a float sized by it clips the right edge of the picture
// it was measured around.
//
// It has no reftest of its own and it needed this fixture to be written for it:
// removing the clause moved nothing at all across the whole suite, which is the
// answer that has to be told apart from "the clause is dead". It is not dead —
// the box below is 60px wider with it than without.
func TestASpaceBeforeAnAtomicInlineIsNotTrailing(t *testing.T) {
	root := layoutOf(t, 10000,
		`<div id="f">aa <span id="k"></span></div>`,
		noDefaults+`
		  #f { float: left; font-size: 100px; font-family: Courier }
		  #k { display: inline-block; width: 100px; height: 10px }`)
	// "aa" is two characters, the space is one, and the inline-block is 100.
	px(t, "a float over a space before an inline-block",
		find(t, root, "f").BorderRect.W, 3*ch+100)
}

// TestAnIntrinsicKeywordSizesAnAbsoluteBox pins the second place a declared
// width is read.
//
// §10.3.7 solves for a width only when there is not one, so a keyword has to
// reach it as a width and not as "auto" — and the difference is visible: an
// absolutely positioned box with an auto width shrinks to fit, which against a
// wide containing block is its *max-content* size. So the two answers here are
// 240 and 480, and the wrong one is the one that looks reasonable.
//
// It needed a fixture written for it. Removing the wiring moved nothing across
// the whole suite, and it also removes the finding that would have said the
// declaration was dropped — checkIntrinsicSizing asks the same function — so the
// box would have come out at the wrong width with nothing at all to say so.
func TestAnIntrinsicKeywordSizesAnAbsoluteBox(t *testing.T) {
	css := noDefaults + `
	  #a { position: absolute; left: 0; top: 0;
	       font-size: 100px; font-family: Courier }`
	root := layoutOf(t, 1000, `<div id="a" style="width: min-content">aaa bbbb</div>`, css)
	px(t, "an absolute box at width: min-content",
		find(t, root, "a").BorderRect.W, 4*ch)

	// The auto width beside it, which is the number the mistake produces.
	root = layoutOf(t, 1000, `<div id="a">aaa bbbb</div>`, css)
	px(t, "an absolute box at width: auto", find(t, root, "a").BorderRect.W, 8*ch)
}
