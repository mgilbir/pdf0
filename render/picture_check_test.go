package render

import (
	"testing"

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

func TestPictureComparesText(t *testing.T) {
	a := []Op{DrawText{Text: "Test", At: Point{picPx(8), picPx(29)}, Size: picPx(16)}}
	b := []Op{DrawText{Text: "Text", At: Point{picPx(8), picPx(29)}, Size: picPx(16)}}
	if pictureEqual(a, b, picPage) {
		t.Error("two different words at the same place compared equal")
	}
	moved := []Op{DrawText{Text: "Test", At: Point{picPx(80), picPx(29)}, Size: picPx(16)}}
	if pictureEqual(a, moved, picPage) {
		t.Error("the same word in two different places compared equal")
	}
	if !pictureEqual(a, a, picPage) {
		t.Error("a display list did not compare equal to itself")
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
