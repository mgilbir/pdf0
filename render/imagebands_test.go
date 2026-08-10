package render

import (
	"image"
	"image/color"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// The check on the decomposition in picture_test.go.
//
// It is a check on the *oracle*, not on the engine, and it earns its place for
// the reason TestWPTOracleHasTeeth does: this is the one place in the comparison
// that says two unlike operations put the same ink on the page, and a claim of
// that kind is worth exactly as much as the strictness behind it. What is
// planted here is a picture that is not made of uniform rectangles, a picture
// whose rectangles are too small for the comparison to see, and a pair of
// pictures that differ — each of which the decomposition has to refuse or to
// tell apart.

func imageOf(w, h int, f func(x, y int) color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, f(x, y))
		}
	}
	return img
}

var (
	opaqueRed   = color.NRGBA{R: 255, A: 255}
	opaqueGreen = color.NRGBA{G: 128, A: 255}
	clear       = color.NRGBA{}
)

// bandedThirds is the shape the suite's patterns actually have: three stripes,
// the middle one fully transparent, which is how a test says "nothing of mine
// should show here".
func bandedThirds(w, h int, edge color.NRGBA) image.Image {
	return imageOf(w, h, func(x, y int) color.NRGBA {
		if y < h/3 || y >= 2*h/3 {
			return edge
		}
		return clear
	})
}

func TestImageBandsAreExact(t *testing.T) {
	// Three stripes decompose into three rectangles, in image pixels.
	got := bandsOf(bandedThirds(9, 9, opaqueRed))
	if len(got) != 3 {
		t.Fatalf("a three-striped picture decomposed into %d bands, want 3: %v", len(got), got)
	}
	want := []band{
		{0, 0, 9, 3, style.RGBA{R: 255, A: 1}},
		{0, 3, 9, 6, style.RGBA{}},
		{0, 6, 9, 9, style.RGBA{R: 255, A: 1}},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("band %d is %v, want %v", i, got[i], want[i])
		}
	}

	// A picture that changes along both axes at once cannot be a few uniform
	// rectangles, and is refused — by the bound rather than by any inspection of
	// a cell, which is the point scanBands works through.
	circle := imageOf(32, 32, func(x, y int) color.NRGBA {
		dx, dy := float64(x)-15.5, float64(y)-15.5
		if dx*dx+dy*dy < 100 {
			return opaqueRed
		}
		return opaqueGreen
	})
	if bandsOf(circle) != nil {
		t.Error("a circle was decomposed into uniform rectangles")
	}

	// And the invariant itself, over pictures built to break it. scanBands
	// derives its grid from the rows and columns that change and then trusts
	// that the cells are uniform; nothing in the code checks it, because the
	// argument in the comment there says nothing needs to. This is what holds
	// that argument to account, and it is the test that fails if the derivation
	// is ever weakened — reading one row of a column instead of all of them, for
	// instance, which is the obvious "optimisation".
	adversarial := map[string]image.Image{
		"quadrants with a diagonal in one": imageOf(8, 8, func(x, y int) color.NRGBA {
			if x < 4 && y < 4 && x == y {
				return opaqueGreen
			}
			if (x < 4) == (y < 4) {
				return opaqueRed
			}
			return opaqueGreen
		}),
		"one stray pixel": imageOf(6, 6, func(x, y int) color.NRGBA {
			if x == 4 && y == 1 {
				return opaqueGreen
			}
			return opaqueRed
		}),
		"differs only below the first row": imageOf(6, 6, func(x, y int) color.NRGBA {
			if y == 0 {
				return opaqueRed
			}
			if x < 3 {
				return opaqueRed
			}
			return opaqueGreen
		}),
		"differs only right of the first column": imageOf(6, 6, func(x, y int) color.NRGBA {
			if x == 0 {
				return opaqueRed
			}
			if y < 3 {
				return opaqueRed
			}
			return opaqueGreen
		}),
		"stripes with a transparent middle": bandedThirds(9, 9, opaqueRed),
		"pseudorandom noise": imageOf(5, 5, func(x, y int) color.NRGBA {
			if (x*7+y*13)%3 == 0 {
				return opaqueRed
			}
			return opaqueGreen
		}),
	}
	for name, img := range adversarial {
		bands := bandsOf(img)
		if bands == nil {
			continue // refused, which is always a sound answer
		}
		for _, b := range bands {
			checkUniform(t, name, img, b)
		}
	}
}

// checkUniform is the property the decomposition claims, checked independently
// of the code that produces it.
func checkUniform(t *testing.T, name string, img image.Image, b band) {
	t.Helper()
	at := func(x, y int) style.RGBA {
		r, g, bl, a := img.At(x, y).RGBA()
		return style.RGBA{R: float64(r >> 8), G: float64(g >> 8), B: float64(bl >> 8),
			A: float64(a) / 0xFFFF}
	}
	for y := b.y0; y < b.y1; y++ {
		for x := b.x0; x < b.x1; x++ {
			if at(x, y) != b.c {
				t.Fatalf("%s: band %v claims %v but pixel %d,%d is %v",
					name, b, b.c, x, y, at(x, y))
			}
		}
	}
}

// TestBandedPictureLetsWhatIsBehindShowThrough is the equivalence the whole
// thing exists for, and its limit.
func TestBandedPictureLetsWhatIsBehindShowThrough(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }
	clip := Rect{W: u(300), H: u(300)}
	blue := style.RGBA{B: 255, A: 1}
	red := style.RGBA{R: 255, A: 1}

	// A blue box with a striped picture over it, against the same blue box with
	// two red bars drawn on it. The middle third of the picture is transparent,
	// so the two put the same ink on the page.
	pic := bandedThirds(9, 9, opaqueRed)
	over := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(90), u(90)}, Color: blue},
		DrawImage{Rect: Rect{u(0), u(0), u(90), u(90)}, Image: pic, Key: "stripes"},
	}
	bars := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(90), u(90)}, Color: blue},
		FillRect{Rect: Rect{u(0), u(0), u(90), u(30)}, Color: red},
		FillRect{Rect: Rect{u(0), u(60), u(90), u(30)}, Color: red},
	}
	if !pictureEqual(over, bars, clip) {
		t.Error("a striped picture over blue did not equal the bars it paints")
	}

	// The limit: the blue behind it has to be what shows through. The same
	// picture over *green* is a different page, and an oracle that could not
	// say so would have bought its passes by going blind.
	green := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(90), u(90)}, Color: style.RGBA{G: 255, A: 1}},
		DrawImage{Rect: Rect{u(0), u(0), u(90), u(90)}, Image: pic, Key: "stripes"},
	}
	if pictureEqual(green, bars, clip) {
		t.Error("a striped picture over green equalled the same picture over blue")
	}

	// And the bands have to be in the right place. The same three stripes drawn
	// half as tall are not the same page.
	half := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(90), u(90)}, Color: blue},
		DrawImage{Rect: Rect{u(0), u(0), u(90), u(45)}, Image: pic, Key: "stripes"},
	}
	if pictureEqual(half, bars, clip) {
		t.Error("a striped picture drawn half as tall equalled the full-height one")
	}
}

// TestABandTooSmallToSeeIsRefused is the guard against the false pass this
// could otherwise have introduced.
//
// The comparison discards a cell narrower than a quarter of a pixel as rounding.
// A picture whose bands are that thin would therefore decompose into marks that
// are then ignored, and a document that drew it would compare equal to one that
// drew nothing at all. The decomposition refuses such a picture and it goes back
// to being one opaque mark, which differs from nothing as it should.
func TestABandTooSmallToSeeIsRefused(t *testing.T) {
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }

	// Thirty-two stripes down a 32-pixel-tall picture: within the band bound,
	// and one band per pixel row.
	pic := imageOf(4, 32, func(_, y int) color.NRGBA {
		if y%2 == 0 {
			return opaqueRed
		}
		return clear
	})
	if n := len(bandsOf(pic)); n != 32 {
		t.Fatalf("the striped picture has %d bands, want 32", n)
	}

	// Drawn 6.4 pixels tall, each band is a fifth of a pixel — under the
	// quarter-pixel the comparison can see — so the whole picture is refused
	// even though the picture itself is 90 by 6.4 and plainly visible.
	if got := bandedFills(pic, Rect{W: u(90), H: u(6.4)}); got != nil {
		t.Errorf("a picture with fifth-of-a-pixel bands was decomposed into %d marks", len(got))
	}
	// At 64 pixels tall each band is two pixels and it is decomposed, so the
	// refusal above is about the size and not about the picture.
	if got := bandedFills(pic, Rect{W: u(90), H: u(64)}); len(got) != 16 {
		t.Errorf("a visible striped picture produced %d marks, want 16 opaque bands", len(got))
	}

	// The consequence, which is the point: squeezed down it is still one opaque
	// mark, and still differs from a blank page. Had it been decomposed, every
	// one of its bands would have been discarded as rounding and the two would
	// have matched.
	clip := Rect{W: u(300), H: u(300)}
	tiny := []Op{DrawImage{Rect: Rect{u(0), u(0), u(90), u(6.4)}, Image: pic, Key: "stripes"}}
	if pictureEqual(tiny, nil, clip) {
		t.Error("a picture too finely striped to decompose compared equal to a blank page")
	}
}
