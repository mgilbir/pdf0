package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Sizing replaced elements.
//
// Every expected number here is worked out from the specification's own
// arithmetic rather than read off a run, because the whole failure mode of
// replaced-element sizing is a picture that is *plausibly* the wrong size: an
// image at its intrinsic size where it should have been scaled looks like a
// photograph nobody resized, and one whose ratio was not kept looks like a bad
// photograph. Neither says anything about the engine.
//
// The images are 40 × 20 unless stated, so the intrinsic ratio is exactly 2 and
// every derived number is exact in the layout units.

// replacedLayout lays out a document whose images come from a directory holding
// one 40 × 20 picture called "wide.png" and one 20 × 40 called "tall.png".
func replacedLayout(t *testing.T, width float64, htmlSrc string, cssSrc ...string) *Fragment {
	t.Helper()
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "wide.png"), 40, 20)
	writePNG(t, filepath.Join(dir, "tall.png"), 20, 40)

	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })

	in := Input{HTML: htmlSrc, Resources: res}
	for _, c := range cssSrc {
		in.CSS = append(in.CSS, Stylesheet{Source: c})
	}
	built := Build(in)
	if built.Root == nil {
		t.Fatal("the document produced no boxes")
	}
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked || f.Rule == RuleImageUndecodable {
			t.Fatalf("an image did not load: %s", f.Error())
		}
	}
	rec := NewRecorder(nil)
	w, _ := style.FromPx(width)
	h, _ := style.FromPx(10000)
	frag := Layout(built.Root, Size{W: w, H: h}, nil, rec)
	if frag == nil {
		t.Fatal("layout produced no fragment")
	}
	return frag
}

// contentSize is a fragment's content box, which is what a replaced element's
// used width and height are.
func contentSize(f *Fragment) (style.Unit, style.Unit) {
	r := f.ContentRect()
	return r.W, r.H
}

// TestReplacedIntrinsicSize is §10.3.2 and §10.6.2 with nothing declared: the
// picture is its own size.
func TestReplacedIntrinsicSize(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 40)
	px(t, "the used height", h, 20)
}

// TestReplacedRatioFromWidth is §10.6.2: an auto height with a used width and an
// intrinsic ratio is the width divided by the ratio.
//
// This is the rule that keeps a picture in proportion, and the one an engine
// gets wrong by falling back to the intrinsic height — which would give 20 here
// and produce a squashed image at every size but the original.
func TestReplacedRatioFromWidth(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img id="i" src="wide.png"></div>`, noDefaults, `#i { width: 200px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 200)
	px(t, "the used height", h, 100)
}

// TestReplacedRatioFromHeight is the same rule on the other axis, §10.3.2.
func TestReplacedRatioFromHeight(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img id="i" src="wide.png"></div>`, noDefaults, `#i { height: 100px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 200)
	px(t, "the used height", h, 100)
}

// TestReplacedBothDeclared pins that two declarations win over the ratio: an
// author who states both has asked for a distorted picture and gets one.
func TestReplacedBothDeclared(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { width: 300px; height: 30px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 300)
	px(t, "the used height", h, 30)
}

// TestReplacedMaxWidthKeepsTheRatio is one row of §10.4's constraint table: a
// width past the maximum takes the maximum, and the height follows it down.
//
// The height is the assertion that matters. Clamping the width alone would give
// 50 × 20, which is a picture of the right size and the wrong shape — and is
// what an implementation that treated §10.4 as two independent clamps produces.
func TestReplacedMaxWidthKeepsTheRatio(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { width: 200px; max-width: 50px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 50)
	px(t, "the used height", h, 25)
}

// TestReplacedMaxHeightKeepsTheRatio is the mirror row, and is the case a real
// test in the suite is built on: an image with a height past its max-height has
// its *width* recomputed from the constrained height.
func TestReplacedMaxHeightKeepsTheRatio(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { height: 200px; max-height: 50px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 100)
	px(t, "the used height", h, 50)
}

// TestReplacedMinWidthKeepsTheRatio is the minimum's row.
func TestReplacedMinWidthKeepsTheRatio(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { width: 40px; min-width: 100px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 100)
	px(t, "the used height", h, 50)
}

// TestReplacedContradictoryConstraintsAbandonTheRatio is the pair of rows where
// §10.4 gives up on the shape: a box that is too narrow and too tall cannot be
// fixed by scaling, because the two violations pull in opposite directions.
//
// It is worth pinning precisely because it is the one place the table does
// *not* preserve the ratio, and an implementation that tried to would produce a
// box violating one of the two constraints it was given.
func TestReplacedContradictoryConstraintsAbandonTheRatio(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { width: 40px; height: 20px; min-width: 100px; max-height: 10px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 100)
	px(t, "the used height", h, 10)
}

// TestReplacedPresentationalAttributes pins that "<img width=5 height=96>" is a
// declaration and not decoration.
//
// The suite's own reference documents draw most of their expected pictures this
// way, and so does two decades of email markup.
func TestReplacedPresentationalAttributes(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img id="i" src="wide.png" width="5" height="96"></div>`, noDefaults)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 5)
	px(t, "the used height", h, 96)
}

// TestPresentationalAttributeGivesOnlyOneAxis pins that a width attribute alone
// leaves the height to the ratio, exactly as a CSS width would.
func TestPresentationalAttributeGivesOnlyOneAxis(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img id="i" src="wide.png" width="200"></div>`, noDefaults)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 200)
	px(t, "the used height", h, 100)
}

// TestStylesheetBeatsPresentationalAttribute is where the attribute sits in the
// cascade. A stylesheet has to be able to take control of markup it did not
// write, and this is the rule that lets it.
func TestStylesheetBeatsPresentationalAttribute(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img id="i" src="wide.png" width="5" height="96"></div>`,
		noDefaults, `img { width: 60px; height: 30px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 60)
	px(t, "the used height", h, 30)
}

// TestPresentationalAttributeBeatsNothingElse pins the other side of it: the
// attribute is still a declaration, so it wins over the initial value.
// Percentages are accepted too, since HTML's dimension values allow them.
func TestPresentationalAttributePercentage(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png" width="50%"></div>`,
		noDefaults, `#d { width: 400px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the used width", w, 200)
	px(t, "the used height", h, 100)
}

// TestPresentationalAttributeRejectsNonDimensions pins that a value HTML does
// not define as a dimension is ignored rather than guessed at.
func TestPresentationalAttributeRejectsNonDimensions(t *testing.T) {
	for _, value := range []string{"abc", "-40", "40px", "4.5", "", "  ", "40 "} {
		root := replacedLayout(t, 500,
			`<div><img id="i" src="wide.png" width="`+value+`"></div>`, noDefaults)
		w, _ := contentSize(find(t, root, "i"))
		px(t, "the used width for width="+value, w, 40)
	}
}

// TestBlockReplacedCentres is §10.3.4: the width comes from §10.3.2 and then
// the ordinary block margin arithmetic runs against it, which is what makes
// "margin: 0 auto" centre an image.
//
// An engine that let a block-level replaced element's auto width fill the
// containing block — the rule for a non-replaced block — would produce a
// 500-pixel box and no visible centring at all.
func TestBlockReplacedCentres(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { display: block; margin-left: auto; margin-right: auto }`)
	f := find(t, root, "i")
	w, _ := contentSize(f)
	px(t, "the used width", w, 40)
	px(t, "the left margin", f.Margin.Left, 230)
	px(t, "the border box's left edge", f.BorderRect.X, 230)
}

// TestFloatedReplacedTakesItsIntrinsicWidth pins §10.3.5 for a replaced float.
//
// A float's auto width is shrink-to-fit, which is defined over *content* widths
// — and a replaced element has no content to measure. Its size is the answer,
// and an engine that measured the box tree instead would find no children and
// give the float a width of zero, leaving the picture hanging out of a sliver.
func TestFloatedReplacedTakesItsIntrinsicWidth(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { float: left }`)
	f := find(t, root, "i")
	w, h := contentSize(f)
	px(t, "the float's width", w, 40)
	px(t, "the float's height", h, 20)
	px(t, "the float's left edge", f.BorderRect.X, 0)
}

// TestFloatShrinksToFitAnImageInside is the intrinsic-width half of the same
// rule, and the one that needs the box tree to know an image has a size.
//
// A float with no width shrinks to fit its content, and shrink-to-fit is
// measured over the box tree rather than by a trial layout. A replaced element
// has no children to measure, so an engine that walked its subtree would find
// nothing, give the float a width of zero, and leave the picture hanging out of
// a sliver — which looks like a broken float rather than like an unmeasured
// image.
func TestFloatShrinksToFitAnImageInside(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img src="wide.png"></div>`, noDefaults,
		`#d { float: left }`)
	px(t, "the float's width", find(t, root, "d").BorderRect.W, 40)
}

// TestFloatShrinksToFitAScaledImage pins that the *used* width is what the
// float takes, not the intrinsic one — a declared width on the image has to
// reach the measurement.
func TestFloatShrinksToFitAScaledImage(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#d { float: left } #i { width: 120px }`)
	px(t, "the float's width", find(t, root, "d").BorderRect.W, 120)
}

// TestAbsolutelyPositionedReplacedDoesNotStretch is §10.3.6.
//
// "left: 0; right: 0" makes a non-replaced box as wide as its containing block.
// A replaced one already knows how wide it is, so the constraint is
// over-determined and the *end* offset is what gives way. Getting this wrong
// stretches every absolutely positioned image to the width of the page.
func TestAbsolutelyPositionedReplacedDoesNotStretch(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#d { position: relative; width: 300px; height: 200px }
		 #i { position: absolute; left: 0; right: 0; top: 0; bottom: 0 }`)
	f := find(t, root, "i")
	w, h := contentSize(f)
	px(t, "the used width", w, 40)
	px(t, "the used height", h, 20)
	px(t, "the left edge", f.BorderRect.X, 0)
}

// TestAbsolutelyPositionedReplacedCentres pins the auto-margin case of §10.3.7
// against a replaced element, where the size is known and the margins absorb
// the slack.
func TestAbsolutelyPositionedReplacedCentres(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#d { position: relative; width: 300px; height: 200px }
		 #i { position: absolute; left: 0; right: 0;
		      margin-left: auto; margin-right: auto }`)
	f := find(t, root, "i")
	w, _ := contentSize(f)
	px(t, "the used width", w, 40)
	// (300 - 40) / 2 from each side.
	px(t, "the left edge", f.BorderRect.X, 130)
}

// TestInlineReplacedSitsOnTheBaseline is §10.8.1: the baseline of an inline
// replaced element is its bottom margin edge.
//
// The consequence is the one everybody meets and nobody expects — a line
// holding nothing but an image is *taller* than the image, because the strut
// still wants its descender space, which is the familiar gap under a picture in
// a table cell. The image's own top is at the top of the line, and the box below
// it is the strut's descent.
func TestInlineReplacedSitsOnTheBaseline(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 20px }`)
	d := find(t, root, "d")
	if len(d.Lines) != 1 {
		t.Fatalf("the block has %d lines, want 1", len(d.Lines))
	}
	line := d.Lines[0]

	// The image is 20 tall and is all ascent, so the baseline is at 20 — past
	// the strut's own baseline, which is under 20 for a 16px font on a 20px
	// line.
	px(t, "the line's baseline", line.Baseline, 20)
	if line.Rect.H <= line.Baseline {
		t.Errorf("the line is %.2fpx tall with a baseline at %.2f; the strut's "+
			"descent is missing, which is the gap under an image",
			line.Rect.H.Px(), line.Baseline.Px())
	}

	img := find(t, root, "i")
	px(t, "the image's top", img.BorderRect.Y, 0)
}

// TestInlineReplacedWithAMarginHangsByItsMarginEdge pins that the baseline is
// the bottom *margin* edge and not the bottom border edge, which is the
// difference a bottom margin makes visible.
func TestInlineReplacedWithAMarginHangsByItsMarginEdge(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 20px }
		 #i { margin-bottom: 10px }`)
	line := find(t, root, "d").Lines[0]
	// 20 of picture plus 10 of margin, all of it above the baseline.
	px(t, "the line's baseline", line.Baseline, 30)
	px(t, "the image's top", find(t, root, "i").BorderRect.Y, 0)
}

// TestInlineReplacedAdvancesTheLine pins that an image takes its margin box's
// width from the line, so the words after it start past it.
func TestInlineReplacedAdvancesTheLine(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png">after</div>`, noDefaults,
		`#i { margin-left: 5px; margin-right: 7px }`)
	d := find(t, root, "d")
	line := d.Lines[0]
	if len(line.Runs) != 1 {
		t.Fatalf("the line has %d text runs, want 1", len(line.Runs))
	}
	// The image contributes 5 + 40 + 7.
	px(t, "the text's offset along the line", line.Runs[0].X, 52)
	px(t, "the image's border box", find(t, root, "i").BorderRect.X, 5)
}

// TestVerticalAlignTopOnAnImage pins the one vertical-align keyword the suite's
// references lean on hardest: the image's top goes to the line's top rather
// than its bottom to the baseline.
func TestVerticalAlignTopOnAnImage(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d">x<img id="i" src="wide.png"></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 20px } #i { vertical-align: top }`)
	d := find(t, root, "d")
	line := d.Lines[0]
	// The image is 20 tall and the strut's line is 20, so the line does not
	// grow — and the image sits at its very top.
	px(t, "the line's height", line.Rect.H, 20)
	px(t, "the image's top", find(t, root, "i").BorderRect.Y, 0)
}

// TestVerticalAlignBottomGrowsTheLine pins the second pass of §10.8.1: a
// box aligned to the line's bottom edge that does not fit makes the line taller
// on the other side.
func TestVerticalAlignBottomGrowsTheLine(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d">x<img id="i" src="wide.png"></div>`, noDefaults,
		`#d { font-size: 16px; line-height: 10px } #i { vertical-align: bottom }`)
	d := find(t, root, "d")
	line := d.Lines[0]
	px(t, "the line's height", line.Rect.H, 20)
	// Its bottom is the line's bottom, so its top is 20 - 20.
	px(t, "the image's top", find(t, root, "i").BorderRect.Y, 0)
}

// TestReplacedPaintsItsImage pins that the display list carries the picture,
// over the content box rather than the border box — a replaced element's
// content goes inside its padding, like everything else's.
func TestReplacedPaintsItsImage(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div id="d"><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { padding-left: 3px; padding-top: 4px;
		      border-left-style: solid; border-left-width: 2px }`)
	var drawn []DrawImage
	for _, op := range Paint(root) {
		if v, ok := op.(DrawImage); ok {
			drawn = append(drawn, v)
		}
	}
	if len(drawn) != 1 {
		t.Fatalf("the display list holds %d images, want 1", len(drawn))
	}
	content := find(t, root, "i").ContentRect()
	if drawn[0].Rect != content {
		t.Errorf("the image is drawn over %s, want the content box %s",
			drawn[0].Rect, content)
	}
	px(t, "the image's left edge", drawn[0].Rect.X, 5)
	px(t, "the image's width", drawn[0].Rect.W, 40)
}

// TestOneSourceIsOneImage pins that a document naming a file twice carries one
// key, which is what lets the backend embed it once.
func TestOneSourceIsOneImage(t *testing.T) {
	root := replacedLayout(t, 500,
		`<div><img src="wide.png"><img src="wide.png"><img src="tall.png"></div>`,
		noDefaults)
	keys := map[string]int{}
	for _, op := range Paint(root) {
		if v, ok := op.(DrawImage); ok {
			keys[v.Key]++
		}
	}
	if len(keys) != 2 {
		t.Fatalf("three images produced %d distinct keys, want 2: %v", len(keys), keys)
	}
}

// TestOtherReplacedElementsAreReported pins the scope boundary.
//
// <img> is the replaced element this engine draws. Everything else that is one
// — a plugin, a nested document, a video, a canvas, a drawing — is out of scope,
// and being out of scope has to mean *reported* rather than laid out as an
// ordinary box. An <svg> quietly rendered as an empty inline is a page with a
// chart missing and nothing to say so, which is precisely the silent failure §6
// exists to name.
//
// The form controls used to be in this list and are not any more. The reason
// they were here confused interactivity with layout: a control has a size and
// content on paper whether or not anything can be clicked, and refusing the
// element lost the content. control.go says what each is drawn as; the boundary
// that stays is that nothing is interactive and no PDF form field is produced,
// and TestControlsAreLaidOutAsStaticBoxes is what holds it.
//
// <object> left the list for a different reason: HTML says an object whose data
// cannot be used is represented by its children, and dropping the element threw
// those away. Its data is still never fetched — see
// TestObjectReportsItsBlockedData.
func TestOtherReplacedElementsAreReported(t *testing.T) {
	cases := map[string]string{
		"svg":    `<svg width="100" height="100"><rect width="50" height="50"/></svg>`,
		"iframe": `<iframe src="x.html" width="100"></iframe>`,
		"video":  `<video src="x.mp4"></video>`,
		"canvas": `<canvas width="100" height="100"></canvas>`,
		"embed":  `<embed src="x.swf">`,
	}
	for name, markup := range cases {
		built := Build(Input{HTML: `<div>` + markup + `</div>`})
		var reported bool
		for _, f := range built.Findings {
			if f.Rule == RuleUnsupportedElement && strings.Contains(f.Message, name) {
				reported = true
			}
		}
		if !reported {
			t.Errorf("<%s> produced no unsupported-element finding: %v", name, built.Findings)
		}
		// And nothing of it reaches the page. A box for it would be an empty
		// one at the wrong size, which is worse than none.
		var walk func(*Box) bool
		walk = func(b *Box) bool {
			if b == nil {
				return false
			}
			if b.Element != nil && strings.EqualFold(b.Element.Name, name) {
				return true
			}
			for _, c := range b.Children {
				if walk(c) {
					return true
				}
			}
			return false
		}
		if walk(built.Root) {
			t.Errorf("<%s> generated a box", name)
		}
	}
}

// TestSrcsetIsReported pins that an image with several candidate files says so.
//
// Choosing the wrong one of them is the quietest failure an image can have: the
// page carries a picture, it is simply not the picture the author's rules would
// have chosen, and nothing about the document says which was used.
func TestSrcsetIsReported(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "wide.png"), 40, 20)
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()

	built := Build(Input{
		HTML:      `<img id="i" src="wide.png" srcset="wide.png 1x, other.png 2x">`,
		Resources: res,
	})
	if findBox(t, built.Root, "i").Replaced == nil {
		t.Fatal("the src was not used")
	}
	requireFinding(t, built.Findings, RuleUnsupportedValue, "srcset")
}

// TestReplacedInATightFloatIsReported pins that an image too wide for the space
// it is in raises the unbreakable-overflow guardrail rather than being clipped
// in silence.
func TestReplacedInATightFloatIsReported(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "wide.png"), 40, 20)
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()

	built := Build(Input{
		HTML:      `<div id="d"><img src="wide.png"></div>`,
		CSS:       []Stylesheet{{Source: noDefaults}, {Source: `#d { width: 10px }`}},
		Resources: res,
	})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(500)
	h, _ := style.FromPx(1000)
	Layout(built.Root, Size{W: w, H: h}, nil, rec)
	requireFinding(t, rec.Findings(), RuleUnbreakableOverflow, "the image is 40px wide")
}

// TestReplacedZeroTentativeSizeUsesTheIntrinsicRatio is §10.4's table where the
// tentative pair it divides by is 0 by 0.
//
// Every row of the table scales one axis by the ratio of the *tentative* used
// width and height, and "height: 0" on an image with an auto width makes both of
// them nought: the height was declared as zero and the width was computed from
// it. The ratio is then 0/0, which is no ratio at all, and an implementation
// that noticed the division and bailed out to two independent clamps produces a
// picture 0 wide and 100 tall — a rectangle where the author asked for a square,
// from declarations that say nothing about the shape at all.
//
// The intrinsic ratio is what stands in for it, and it is the only shape there
// is: the picture's own. wide.png is 40 × 20, so a minimum height of 100 makes
// the width 200.
func TestReplacedZeroTentativeSizeUsesTheIntrinsicRatio(t *testing.T) {
	root := replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { height: 0; width: auto; min-height: 100px }`)
	w, h := contentSize(find(t, root, "i"))
	px(t, "the width recomputed from the held-open height", w, 200)
	px(t, "the used height", h, 100)

	// The same the other way round, which is a different row of the table and so
	// cannot pass by the same accident: a minimum *width* of 100 makes the
	// height 50.
	root = replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { width: 0; height: auto; min-width: 100px }`)
	w, h = contentSize(find(t, root, "i"))
	px(t, "the used width", w, 100)
	px(t, "the height recomputed from the held-open width", h, 50)

	// And where only *one* axis is nought the ratio really is gone — the box has
	// no shape left to keep — so the two limits are independent and the height
	// stays where it was declared. This is the case the guard still refuses, and
	// it is here so that widening the guard to cover it is a decision somebody
	// takes rather than one that slips through.
	root = replacedLayout(t, 500, `<div><img id="i" src="wide.png"></div>`, noDefaults,
		`#i { width: 0; height: 50px; min-width: 100px }`)
	w, h = contentSize(find(t, root, "i"))
	px(t, "the used width", w, 100)
	px(t, "a declared height a degenerate ratio cannot move", h, 50)
}
