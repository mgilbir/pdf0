package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// Tests of the picture comparison itself.
//
// This is measuring apparatus rather than engine, and a fault in it does not
// produce a wrong page — it produces a wrong *number*, silently, in every
// measurement taken afterwards. So it is tested harder than the thing it
// measures, and in both directions: that it sees differences that matter, and
// that it does not see differences that do not.

var (
	picRed   = style.RGBA{R: 255, A: 1}
	picGreen = style.RGBA{G: 128, A: 1}
	picBlue  = style.RGBA{B: 255, A: 1}
)

func picPx(n float64) style.Unit {
	u, ok := style.FromPx(n)
	if !ok {
		panic("test used a length that does not fit a layout unit")
	}
	return u
}

func picRect(x, y, w, h float64) Rect {
	return Rect{picPx(x), picPx(y), picPx(w), picPx(h)}
}

func picFill(x, y, w, h float64, c style.RGBA) Op {
	return FillRect{Rect: picRect(x, y, w, h), Color: c}
}

var picPage = picRect(0, 0, 600, 800)

func TestPictureSeesThroughOverpaint(t *testing.T) {
	// The idiom nearly the whole CSS 2.1 suite is written in: red underneath,
	// green exactly over it. It must compare equal to plain green, and this is
	// the case a list-of-marks comparison gets wrong.
	covered := []Op{picFill(10, 10, 100, 20, picRed), picFill(10, 10, 100, 20, picGreen)}
	plain := []Op{picFill(10, 10, 100, 20, picGreen)}
	if !pictureEqual(covered, plain, picPage) {
		t.Error("a fully covered red box did not compare equal to green alone")
	}
}

func TestPictureCatchesUncoveredRed(t *testing.T) {
	// The same thing with the cover a little too short. Leaving a sliver of red
	// showing is exactly the failure these tests are written to catch, so
	// missing it would make the comparison worthless rather than merely coarse.
	for _, short := range []float64{99, 99.5, 99.75} {
		leaking := []Op{picFill(10, 10, 100, 20, picRed), picFill(10, 10, short, 20, picGreen)}
		plain := []Op{picFill(10, 10, 100, 20, picGreen)}
		if pictureEqual(leaking, plain, picPage) {
			t.Errorf("a %gpx strip left showing compared equal to nothing showing", 100-short)
		}
	}
}

func TestPictureIgnoresDecomposition(t *testing.T) {
	// Two stacked bars and one tall bar are the same picture. A reference draws
	// the expected result whichever way is convenient, so a comparison that
	// depended on how the area was cut up would report differences that are not
	// there.
	stacked := []Op{picFill(8, 50, 600, 10, picGreen), picFill(8, 60, 600, 10, picGreen)}
	single := []Op{picFill(8, 50, 600, 20, picGreen)}
	if !pictureEqual(stacked, single, picPage) {
		t.Error("two stacked bars did not compare equal to one bar covering the same area")
	}
}

func TestPictureRespectsPaintOrder(t *testing.T) {
	// Order decides which mark is visible, so a comparison that sorted the marks
	// — as the previous one did — could not tell correct stacking from inverted
	// stacking. That blindness also made every z-index test meaningless.
	greenOnRed := []Op{picFill(0, 0, 50, 50, picRed), picFill(0, 0, 50, 50, picGreen)}
	redOnGreen := []Op{picFill(0, 0, 50, 50, picGreen), picFill(0, 0, 50, 50, picRed)}
	if pictureEqual(greenOnRed, redOnGreen, picPage) {
		t.Error("inverted stacking compared equal; paint order is not being read")
	}
}

func TestPictureSeesAHairline(t *testing.T) {
	// The sliver threshold exists to swallow rounding, and the risk it carries is
	// swallowing the thinnest thing a document can deliberately draw. A
	// one-pixel rule is that thing, and it must survive.
	if pictureEqual([]Op{picFill(10, 10, 1, 100, picBlue)}, nil, picPage) {
		t.Error("a 1px line compared equal to a blank page")
	}
	// Half a pixel is still a deliberate mark: borders resolve to fractions
	// whenever a percentage or a scale is involved.
	if pictureEqual([]Op{picFill(10, 10, 0.5, 100, picBlue)}, nil, picPage) {
		t.Error("a 0.5px line compared equal to a blank page")
	}
}

func TestPictureToleratesRounding(t *testing.T) {
	// The two documents of a reftest compute the same geometry by different
	// arithmetic. A unit or two of disagreement is not a rendering difference,
	// and reporting it as one would bury the real failures.
	//
	// The offset has to be several units wide to test anything. At one unit the
	// disputed cell is a single unit across, its midpoint rounds onto the edge
	// itself, and both documents report the same colour there — so the
	// comparison agrees whether or not the sliver rule exists, and a test built
	// on that would be measuring nothing. A tenth of a pixel is comfortably
	// below what can be seen and comfortably above that degenerate case.
	for _, off := range []float64{0.015, 0.05, 0.1, 0.2} {
		a := []Op{picFill(10, 10, 100, 20, picGreen)}
		b := []Op{picFill(10+off, 10, 100, 20, picGreen)}
		if !pictureEqual(a, b, picPage) {
			t.Errorf("a %gpx difference in position was reported as a rendering difference", off)
		}
	}
	// A quarter pixel is where the rule stops: beyond it a difference is
	// treated as real, because that is the scale a document draws at
	// deliberately.
	a := []Op{picFill(10, 10, 100, 20, picGreen)}
	c := []Op{picFill(11, 10, 100, 20, picGreen)}
	if pictureEqual(a, c, picPage) {
		t.Error("a whole pixel of displacement was dismissed as rounding")
	}
}

// picText is a run in ordinary black ink.
//
// The colour is not decoration here. A run is dropped from the comparison when
// its ink is the colour of what it is drawn on, and a zero-valued RGBA is fully
// transparent — so a DrawText built without one vanishes, and a test built that
// way compares two empty pages and passes whatever the comparison does. That is
// how this test first failed, which is the cheapest possible way to learn it.
func picText(s string, x, y float64) Op {
	return DrawText{
		Text: s, At: Point{picPx(x), picPx(y)}, Size: picPx(16),
		Color: style.RGBA{A: 1},
	}
}

func TestPictureComparesText(t *testing.T) {
	a := []Op{picText("Test", 8, 29)}
	b := []Op{picText("Text", 8, 29)}
	if pictureEqual(a, b, picPage) {
		t.Error("two different words at the same place compared equal")
	}
	moved := []Op{picText("Test", 80, 29)}
	if pictureEqual(a, moved, picPage) {
		t.Error("the same word in two different places compared equal")
	}
	if !pictureEqual(a, a, picPage) {
		t.Error("a display list did not compare equal to itself")
	}
}

func TestPictureToleratesTextRounding(t *testing.T) {
	// A run measured as three pieces lands a layout unit away from the same run
	// measured once. That is a sixty-fourth of a pixel and is not a rendering
	// difference — but the comparison used to render positions to a hundredth
	// and match exactly, which made it finer than the engine's own quantum and
	// stricter than the rule applied to every fill beside it.
	base := []Op{picText("Test", 8, 29)}
	for _, off := range []float64{0.015625, 0.05, 0.1, 0.2} {
		if !pictureEqual([]Op{picText("Test", 8+off, 29)}, base, picPage) {
			t.Errorf("a run %gpx away was called a rendering difference", off)
		}
		if !pictureEqual([]Op{picText("Test", 8, 29+off)}, base, picPage) {
			t.Errorf("a run %gpx down was called a rendering difference", off)
		}
	}
	// And it stops where the fills stop. Half a pixel is a real displacement,
	// and a comparison that let it through would stop noticing text moving at
	// all.
	for _, off := range []float64{0.5, 1, 5} {
		if pictureEqual([]Op{picText("Test", 8+off, 29)}, base, picPage) {
			t.Errorf("a run displaced by %gpx compared equal", off)
		}
	}
}

func TestPictureIgnoresInkTheColourOfThePaper(t *testing.T) {
	// White text on bare page marks nothing, and a reference that simply does
	// not draw it puts the same picture on the page. A whole family of tests is
	// written that way — "color: white" to put content out of the way — and
	// counting those runs made every one of the pairs differ over letters that
	// neither document shows.
	white := DrawText{
		Text: "hidden", At: Point{picPx(20), picPx(40)}, Size: picPx(16),
		Color: style.RGBA{R: 255, G: 255, B: 255, A: 1},
	}
	if !pictureEqual([]Op{white}, nil, picPage) {
		t.Error("white text on bare page did not compare equal to no text at all")
	}

	// The rule has to stop exactly there. Black text on the same page is the
	// most ordinary thing a document does, and dropping it would empty the
	// comparison of everything it is for.
	if pictureEqual([]Op{picText("visible", 20, 40)}, nil, picPage) {
		t.Error("black text on bare page compared equal to no text at all")
	}
	// And white text over something dark is visible again.
	overDark := []Op{picFill(0, 0, 200, 100, style.RGBA{A: 1}), white}
	if pictureEqual(overDark, []Op{picFill(0, 0, 200, 100, style.RGBA{A: 1})}, picPage) {
		t.Error("white text over a black box compared equal to the box alone")
	}
}

func TestPictureClipsToThePage(t *testing.T) {
	// A mark off the page is not part of the picture. Absolute positioning puts
	// boxes at negative coordinates routinely, and a reference that simply omits
	// what would not be seen is not a difference.
	off := []Op{picFill(-500, -500, 100, 100, picRed)}
	if !pictureEqual(off, nil, picPage) {
		t.Error("a mark entirely off the page was treated as part of the picture")
	}
	// But a mark that straddles the edge is partly visible and must count.
	straddling := []Op{picFill(-50, 10, 100, 100, picRed)}
	if pictureEqual(straddling, nil, picPage) {
		t.Error("a mark straddling the page edge was ignored entirely")
	}
}

func TestPictureBlendsTranslucency(t *testing.T) {
	// Half-transparent red over white is not red, and a comparison that took the
	// topmost colour would call it red. Backgrounds use alpha routinely.
	translucent := []Op{
		picFill(0, 0, 50, 50, style.RGBA{R: 255, G: 255, B: 255, A: 1}),
		picFill(0, 0, 50, 50, style.RGBA{R: 255, A: 0.5}),
	}
	if pictureEqual(translucent, []Op{picFill(0, 0, 50, 50, picRed)}, picPage) {
		t.Error("50% red over white compared equal to solid red")
	}
	blended := []Op{picFill(0, 0, 50, 50, style.RGBA{R: 255, G: 127.5, B: 127.5, A: 1})}
	if !pictureEqual(translucent, blended, picPage) {
		t.Error("50% red over white did not compare equal to the colour it blends to")
	}
}

// picFacedText is a run in a real face, which is what joinRuns needs: a run's
// advance is the face's, and a faceless run is deliberately never joined.
func picFacedText(face *fonts.Face, s string, x, y float64, c style.RGBA) DrawText {
	return DrawText{
		Text: s, At: Point{picPx(x), picPx(y)}, Size: picPx(16), Face: face, Color: c,
	}
}

// picFace is the standard serif face, and the advance of a string in it at 16px.
func picFace(t *testing.T) (*fonts.Face, func(string) float64) {
	t.Helper()
	face, ok := StandardFonts().Face("serif", false, false)
	if !ok || face == nil {
		t.Fatal("the standard font set has no serif face")
	}
	return face, func(s string) float64 { return face.Measure(s, 16) }
}

// TestPictureJoinsAbuttingRuns is the rule joinRuns exists for, and the six ways
// it must refuse.
//
// Each case is built so that exactly one thing decides it. The pair that must
// compare *equal* differs only in how the same glyphs were batched; every pair
// that must compare unequal differs in one property and is otherwise identical
// to the equal case, so a rule that stopped checking that property fails here
// and nothing else does.
func TestPictureJoinsAbuttingRuns(t *testing.T) {
	face, adv := picFace(t)
	black := style.RGBA{A: 1}
	// Where "b" ends when it starts at 20. The join must happen at exactly this
	// point and nowhere else, so the number is measured rather than assumed.
	end := 20 + adv("b")

	one := []Op{picFacedText(face, "bc", 20, 40, black)}
	two := []Op{
		picFacedText(face, "b", 20, 40, black),
		picFacedText(face, "c", end, 40, black),
	}
	if !pictureEqual(two, one, picPage) {
		t.Errorf("two runs abutting at %.4fpx did not compare equal to the one run "+
			"they draw", end)
	}
	// The same two runs in the other order in the display list. Joining walks
	// them in x order, so the batching order of the two documents cannot matter.
	if !pictureEqual([]Op{two[1], two[0]}, one, picPage) {
		t.Error("the same two abutting runs, emitted in the other order, did not join")
	}

	// A gap. Four pixels is far more than the layout unit of slack and far less
	// than a space, and it is a real difference in the picture: the glyphs are
	// not where the joined run puts them.
	gapped := []Op{
		picFacedText(face, "b", 20, 40, black),
		picFacedText(face, "c", end+4, 40, black),
	}
	if pictureEqual(gapped, one, picPage) {
		t.Error("two runs 4px apart were joined into the run that has them touching")
	}
	// An overlap is the same statement from the other side.
	overlapped := []Op{
		picFacedText(face, "b", 20, 40, black),
		picFacedText(face, "c", end-4, 40, black),
	}
	if pictureEqual(overlapped, one, picPage) {
		t.Error("two runs overlapping by 4px were joined as though they abutted")
	}

	// Two runs that touch but are in different colours are two marks. Joining
	// them would produce a run in one of the two colours and lose the other,
	// which is a difference anyone can see.
	twoColours := []Op{
		picFacedText(face, "b", 20, 40, black),
		picFacedText(face, "c", end, 40, picRed),
	}
	if pictureEqual(twoColours, one, picPage) {
		t.Error("a black run and a red run were joined into one black run")
	}
	// And at two sizes, where the glyphs are a different size on the page.
	bigger := picFacedText(face, "c", end, 40, black)
	bigger.Size = picPx(32)
	if pictureEqual([]Op{two[0], bigger}, one, picPage) {
		t.Error("a 16px run and a 32px run were joined into one 16px run")
	}
	// And with different letter-spacing, which changes where every glyph after
	// the first one lands.
	spaced := picFacedText(face, "c", end, 40, black)
	spaced.CharSpacing = picPx(4)
	if pictureEqual([]Op{two[0], spaced}, one, picPage) {
		t.Error("two runs with different letter-spacing were joined")
	}
	// Letter-spacing that both runs share is part of the advance and not a
	// reason to refuse: "b" with 4px after it ends 4px further on, and the run
	// that abuts it starts there. This is the case that makes the spacing term
	// of runAdvance decidable — without it, an advance that ignored
	// letter-spacing altogether passes every assertion above.
	withSpacing := func(s string, x float64) DrawText {
		v := picFacedText(face, s, x, 40, black)
		v.CharSpacing = picPx(4)
		return v
	}
	spacedPair := []Op{withSpacing("b", 20), withSpacing("c", end+4)}
	spacedWhole := []Op{withSpacing("bc", 20)}
	if !pictureEqual(spacedPair, spacedWhole, picPage) {
		t.Errorf("two runs abutting at %.4fpx with 4px of letter-spacing did not join",
			end+4)
	}
	// And the same pair placed as though the spacing were not there does not
	// join, which is what stops the case above passing for the wrong reason.
	if pictureEqual([]Op{withSpacing("b", 20), withSpacing("c", end)},
		spacedWhole, picPage) {
		t.Error("a run placed as though letter-spacing cost nothing was still joined")
	}
	// And on two baselines: "abutting" is along one line and says nothing about
	// two runs a line apart that happen to meet in x.
	twoLines := []Op{
		picFacedText(face, "b", 20, 40, black),
		picFacedText(face, "c", end, 60, black),
	}
	if pictureEqual(twoLines, one, picPage) {
		t.Error("two runs on different baselines were joined")
	}

	// A run with no face has no advance, so where it ends is unanswerable and
	// nothing may be joined onto it. Treating the unknown as zero would put two
	// faceless runs at one point and splice them, which is how the hand-built
	// runs everywhere else in this file would quietly stop measuring what they
	// say. The two here are at the same point for exactly that reason.
	faceless := []Op{picText("b", 20, 40), picText("c", 20, 40)}
	if pictureEqual(faceless, []Op{picText("bc", 20, 40)}, picPage) {
		t.Error("two runs with no face were joined as though each were zero wide")
	}

	// Right-to-left runs are left alone: their glyphs march away from the origin
	// the other way, so joining them in x order would concatenate the logical
	// text backwards. Pinning the refusal is what stops the rule being widened
	// without the arithmetic being redone.
	rtl := func(s string, x float64) DrawText {
		v := picFacedText(face, s, x, 40, black)
		v.RTL = true
		return v
	}
	if pictureEqual([]Op{rtl("b", 20), rtl("c", end)},
		[]Op{rtl("bc", 20)}, picPage) {
		t.Error("two right-to-left runs were joined in visual order")
	}

	// The join must not invent equality between different text. "bc" and "cb"
	// are the same glyphs and not the same picture.
	if pictureEqual(two, []Op{picFacedText(face, "cb", 20, 40, black)}, picPage) {
		t.Error("\"b\"+\"c\" compared equal to \"cb\"")
	}
	// Nor between a joined pair and the same pair somewhere else.
	if pictureEqual(two, []Op{picFacedText(face, "bc", 120, 40, black)}, picPage) {
		t.Error("a joined run compared equal to the same run 100px away")
	}
}

// TestPictureJoinsAChainOfRuns pins that the joining is over a run of runs and
// that its arithmetic does not drift.
//
// Six one-character runs laid end to end must come out as the six-character word,
// and the sixth is placed from the accumulated advances — so an implementation
// that lost a layout unit per join disagrees by the end even though it agrees at
// the start.
func TestPictureJoinsAChainOfRuns(t *testing.T) {
	face, adv := picFace(t)
	black := style.RGBA{A: 1}
	const word = "joined"

	var chain []Op
	x := 20.0
	for _, r := range word {
		chain = append(chain, picFacedText(face, string(r), x, 40, black))
		x += adv(string(r))
	}
	whole := []Op{picFacedText(face, word, 20, 40, black)}
	if !pictureEqual(chain, whole, picPage) {
		t.Errorf("%d runs laid end to end did not compare equal to the word they spell",
			len([]rune(word)))
	}
	// And one character out of place breaks it, so the chain is being checked
	// rather than merely concatenated.
	broken := append([]Op(nil), chain...)
	v := broken[3].(DrawText)
	v.At.X = v.At.X.Add(picPx(3))
	broken[3] = v
	if pictureEqual(broken, whole, picPage) {
		t.Error("a chain with one character moved 3px compared equal to the whole word")
	}
}

// Text that something later covers.
//
// This is the one occlusion question the comparison did not ask, and the
// direction it errs in is the dangerous one — a run dropped wrongly is a
// difference that disappears. So it is bounded from both sides here: exactly
// the buried run goes, and every neighbouring case stays.

// picRun is a run in a real face, so that it has a measurable extent. The
// runs elsewhere in this file are faceless on purpose; a faceless run has no
// ink to bury and is never dropped, which would make every case below vacuous.
func picRun(t *testing.T, s string, x, y float64) DrawText {
	t.Helper()
	face, ok := StandardFonts().Face("Helvetica", false, false)
	if !ok {
		t.Skip("the standard faces are not available")
	}
	return DrawText{
		Text: s, At: Point{picPx(x), picPx(y)}, Size: picPx(16),
		Face: face, Color: picRed,
	}
}

func TestPictureSeesThroughBuriedText(t *testing.T) {
	run := picRun(t, "FAIL", 0, 14)
	ink := textInk(run)

	// The shape ten of the abspos-overflow tests are: a red "FAIL" and then an
	// opaque box over the whole of it, against a reference that never wrote
	// the word.
	cover := FillRect{
		Rect:  Rect{ink.X, ink.Y.Sub(picPx(2)), ink.W.Add(picPx(4)), ink.H.Add(picPx(4))},
		Color: picGreen,
	}
	buried := []Op{run, cover}
	plain := []Op{cover}
	if !pictureEqual(buried, plain, picPage) {
		t.Error("a run completely covered by a later opaque box did not compare equal " +
			"to the box alone")
	}
}

func TestPictureKeepsTextThatIsNotBuried(t *testing.T) {
	run := picRun(t, "FAIL", 0, 14)
	ink := textInk(run)
	whole := Rect{ink.X, ink.Y.Sub(picPx(2)), ink.W.Add(picPx(4)), ink.H.Add(picPx(4))}

	cases := []struct {
		name  string
		cover Op
		// order says whether the cover is painted after the run. A box painted
		// *before* it does not hide anything, and getting that backwards is the
		// single way this rule could silently drop half the text in the suite.
		after bool
	}{
		{"an opaque box painted before the run", FillRect{Rect: whole, Color: picGreen}, false},
		{"a box covering only the left half", FillRect{
			Rect: Rect{ink.X, ink.Y, ink.W.Div(2), ink.H}, Color: picGreen}, true},
		// Exactly the ink less a pixel on each axis, which is the boundary: the
		// same box at the ink's own size does bury the run, and this one must
		// not. A rule that used a rectangle bigger than the letters would let
		// this through, which is why the margin is one pixel and not ten.
		{"a box a pixel short of the ink on each axis", FillRect{
			Rect: Rect{ink.X.Add(picPx(1)), ink.Y.Add(picPx(1)),
				ink.W.Sub(picPx(2)), ink.H.Sub(picPx(2))}, Color: picGreen}, true},
		{"a translucent box", FillRect{
			Rect: whole, Color: style.RGBA{G: 128, A: 0.5}}, true},
	}
	for _, tc := range cases {
		var ops []Op
		if tc.after {
			ops = []Op{run, tc.cover}
		} else {
			ops = []Op{tc.cover, run}
		}
		if pictureEqual(ops, []Op{tc.cover}, picPage) {
			t.Errorf("%s: the run was treated as buried", tc.name)
		}
	}

	// Two different words, both buried, are the same page — that is the claim
	// this rule makes and it has to hold in both directions. The box is wide
	// enough for either of them, so neither is peeping out of the side.
	other := picRun(t, "PASS", 0, 14)
	wide := Rect{whole.X, whole.Y,
		style.Max(whole.W, textInk(other).W.Add(picPx(8))), whole.H}
	cover := FillRect{Rect: wide, Color: picGreen}
	if !pictureEqual([]Op{run, cover}, []Op{other, cover}, picPage) {
		t.Error("two different buried words did not compare equal; both are invisible")
	}
	if pictureEqual([]Op{run, cover}, []Op{other}, picPage) {
		t.Error("a buried word compared equal to a visible one")
	}
}

// TestPictureDoesNotBuryTextUnderAPatternedPicture.
//
// A picture with a transparent region in it is where a document shows what is
// behind — the comparison decomposes such a picture into its bands precisely so
// that it can see through the gap, and treating the whole rectangle as a cover
// here would undo that in the one place it matters most.
func TestPictureDoesNotBuryTextUnderAPatternedPicture(t *testing.T) {
	run := picRun(t, "FAIL", 0, 14)
	ink := textInk(run)
	rect := Rect{ink.X, ink.Y.Sub(picPx(2)), ink.W.Add(picPx(4)), ink.H.Add(picPx(4))}

	// Two bands, the lower of which is fully transparent: the word shows
	// through it.
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{G: 128, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 128, A: 255})
	if bandsOf(img) == nil {
		t.Fatal("the two-band picture was not decomposed, so this proves nothing")
	}
	pic := DrawImage{Rect: rect, Image: img, Key: "banded"}

	if pictureEqual([]Op{run, pic}, []Op{pic}, picPage) {
		t.Error("a run under a picture with a transparent half was treated as buried")
	}
}
