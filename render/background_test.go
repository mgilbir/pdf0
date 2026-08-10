package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Background images.
//
// Every expected number here is the specification's own arithmetic rather than a
// value read off a run, and every assertion names the *specific* rectangle.
// "The background painted" is true for many wrong reasons: a tiling with the
// position, the size and the clip all wrong still puts ink on the page, and a
// test that only checked a mark existed would pass through every one of them.
// So each case below asserts where the first tile is, how large it is, how far
// apart the tiles are and what area they may be drawn into — four numbers, of
// which three would survive most faults.
//
// The pictures are 40 × 20 and 20 × 40, so the intrinsic ratio is exactly 2 or a
// half and every derived length is exact in layout units.

// bgPaintOf lays out a document whose backgrounds come from a directory holding
// "wide.png" (40 × 20) and "tall.png" (20 × 40), and returns the display list.
func bgPaintOf(t *testing.T, htmlSrc string, cssSrc ...string) []Op {
	t.Helper()
	return Paint(bgLayoutOf(t, htmlSrc, cssSrc...))
}

func bgLayoutOf(t *testing.T, htmlSrc string, cssSrc ...string) *Fragment {
	t.Helper()
	frag, findings := bgLayoutWithFindings(t, htmlSrc, cssSrc...)
	for _, f := range findings {
		if f.Rule == RuleResourceBlocked || f.Rule == RuleImageUndecodable {
			t.Fatalf("a background image did not load: %s", f.Error())
		}
	}
	return frag
}

func bgLayoutWithFindings(t *testing.T, htmlSrc string, cssSrc ...string) (*Fragment, []Finding) {
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
	rec := NewRecorder(nil)
	w, _ := style.FromPx(500)
	h, _ := style.FromPx(400)
	frag := Layout(built.Root, Size{W: w, H: h}, nil, rec)
	if frag == nil {
		t.Fatal("layout produced no fragment")
	}
	return frag, append(append([]Finding{}, built.Findings...), rec.Findings()...)
}

// firstTiling returns the one tiling in a display list, failing when there is
// none or more than one.
func firstTiling(t *testing.T, ops []Op) TileImage {
	t.Helper()
	var found []TileImage
	for _, op := range ops {
		if v, ok := op.(TileImage); ok {
			found = append(found, v)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the display list has %d tilings, want exactly one: %v", len(found), ops)
	}
	return found[0]
}

func tilings(ops []Op) []TileImage {
	var out []TileImage
	for _, op := range ops {
		if v, ok := op.(TileImage); ok {
			out = append(out, v)
		}
	}
	return out
}

// wantTiling asserts every number of a tiling at once, which is the point: three
// of the four survive most faults on their own.
func wantTiling(t *testing.T, got TileImage, tile, clip Rect, stepX, stepY float64) {
	t.Helper()
	if got.Tile != tile {
		t.Errorf("the first tile is %s, want %s", got.Tile, tile)
	}
	if got.Clip != clip {
		t.Errorf("the painting area is %s, want %s", got.Clip, clip)
	}
	if got.StepX.Px() != stepX || got.StepY.Px() != stepY {
		t.Errorf("the step is %v,%v, want %v,%v",
			got.StepX.Px(), got.StepY.Px(), stepX, stepY)
	}
}

func bgPx(t *testing.T, v float64) style.Unit {
	t.Helper()
	u, ok := style.FromPx(v)
	if !ok {
		t.Fatalf("%v px does not fit a layout unit", v)
	}
	return u
}

func rect(t *testing.T, x, y, w, h float64) Rect {
	t.Helper()
	return Rect{bgPx(t, x), bgPx(t, y), bgPx(t, w), bgPx(t, h)}
}

// bgBoxCSS is a 200 × 100 box with a 10px border and 20px padding, at the origin
// of the page. Its three rectangles are therefore
//
//	border box  (0, 0)   260 × 160
//	padding box (10, 10) 240 × 140
//	content box (30, 30) 200 × 100
//
// which is enough for origin and clip to be told apart in every combination —
// something a box with no border and no padding cannot do, and which is why one
// is used here rather than a plain div.
const bgBoxCSS = noDefaults + `
#a { width: 200px; height: 100px; padding: 20px;
     border-style: solid; border-width: 10px; border-color: transparent }
body { margin: 0 }
`

// TestBackgroundDefaultOriginAndClip pins the distinction most implementations
// get wrong: the initial origin is the *padding* box and the initial clip is the
// *border* box. They are not the same rectangle, so an engine that used one for
// both is wrong by the border width in one direction or the other — and with a
// solid opaque border nothing on the page shows it.
func TestBackgroundDefaultOriginAndClip(t *testing.T) {
	ops := bgPaintOf(t, `<div id="a"></div>`,
		bgBoxCSS+`#a { background-image: url(wide.png); background-repeat: no-repeat }`)
	got := firstTiling(t, ops)

	// The tile starts at the padding box's corner, and the painting area is the
	// border box.
	wantTiling(t, got,
		rect(t, 10, 10, 40, 20), // origin: padding box
		rect(t, 10, 10, 40, 20), // clipped to the single tile, inside the border box
		40, 20)
}

// TestBackgroundOriginAndClipAreIndependent moves each in turn, so that a fault
// swapping them cannot pass.
func TestBackgroundOriginAndClipAreIndependent(t *testing.T) {
	cases := []struct {
		css        string
		tile, clip Rect
	}{
		// Origin at the border box, clip left at the border box.
		{"background-origin: border-box",
			rect(t, 0, 0, 260, 160), rect(t, 0, 0, 260, 160)},
		// Origin at the content box, clip still the border box: the tiling starts
		// inside the padding and is painted out over the border.
		{"background-origin: content-box",
			rect(t, 30, 30, 200, 100), rect(t, 0, 0, 260, 160)},
		// Clip at the content box, origin still the padding box: the tiling
		// starts at the padding edge and is cut at the content edge.
		{"background-clip: content-box",
			rect(t, 10, 10, 240, 140), rect(t, 30, 30, 200, 100)},
		// Both, in opposite directions.
		{"background-origin: border-box; background-clip: padding-box",
			rect(t, 0, 0, 260, 160), rect(t, 10, 10, 240, 140)},
	}
	for _, tc := range cases {
		t.Run(tc.css, func(t *testing.T) {
			// A size of 100% makes the tile exactly the positioning area, so the
			// tile rectangle reports which box the *origin* chose; the repeat
			// leaves the clip the whole painting area, so the clip rectangle
			// reports which box the *clip* chose. Written with no-repeat the two
			// would collapse onto each other and a fault swapping them would pass.
			ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
				`#a { background-image: url(wide.png); background-repeat: repeat;
				      background-size: 100% 100%; `+tc.css+` }`)
			got := firstTiling(t, ops)
			wantTiling(t, got, tc.tile, tc.clip,
				tc.tile.W.Px(), tc.tile.H.Px())
		})
	}
}

// TestBackgroundPositionPercentageIsOfTheFreeSpace pins the subtle rule.
//
// A percentage positions the image's own corresponding point against the box's,
// so it is a percentage of (area − image) rather than of the area. The two
// differ by the image's own size, which at 50% is half an image and at 100% is a
// whole one — the difference between the picture sitting against the far edge
// and it sitting entirely outside the box.
func TestBackgroundPositionPercentageIsOfTheFreeSpace(t *testing.T) {
	// The positioning area is the padding box, 240 × 140, and the image is
	// 40 × 20. The free space is therefore 200 × 120.
	cases := []struct {
		position string
		x, y     float64
	}{
		{"0% 0%", 10, 10},
		{"50% 50%", 10 + 100, 10 + 60},
		{"100% 100%", 10 + 200, 10 + 120},
		{"25% 75%", 10 + 50, 10 + 90},
		// The keywords are the same three percentages, so they must land on the
		// same numbers.
		{"left top", 10, 10},
		{"center center", 110, 70},
		{"right bottom", 210, 130},
		// A single keyword centres the other axis.
		{"center", 110, 70},
		{"right", 210, 70},
		{"top", 110, 10},
	}
	for _, tc := range cases {
		t.Run(tc.position, func(t *testing.T) {
			ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
				`#a { background-image: url(wide.png); background-repeat: no-repeat;
				      background-position: `+tc.position+` }`)
			got := firstTiling(t, ops)
			if got.Tile.X.Px() != tc.x || got.Tile.Y.Px() != tc.y {
				t.Errorf("the tile is at (%v, %v), want (%v, %v)",
					got.Tile.X.Px(), got.Tile.Y.Px(), tc.x, tc.y)
			}
		})
	}
}

// TestBackgroundPositionLengthsAndEdges pins the length forms and the four-value
// syntax, where a keyword names the edge an offset is measured from.
func TestBackgroundPositionLengthsAndEdges(t *testing.T) {
	cases := []struct {
		position string
		x, y     float64
	}{
		// A bare length is an offset from the left and top edges of the
		// positioning area, which starts at 10.
		{"30px 40px", 40, 50},
		{"30px", 40, 70}, // the other axis centres
		// The four-value form measures from the edge it names. The padding box
		// is 240 × 140 and the image 40 × 20, so "right 30px" puts the image's
		// right edge 30 from the right: 10 + 240 − 40 − 30 = 180.
		{"right 30px bottom 40px", 180, 90},
		{"left 30px bottom 40px", 40, 90},
		// A percentage after an edge keyword is of the free space, measured from
		// that edge: 25% of 200 is 50, so from the right it is 10 + 200 − 50.
		{"right 25% top 25%", 160, 40},
		// An em is the element's own font size, which the default sheet leaves
		// at 16px.
		{"2em 1em", 42, 26},
		// A keyword and a length, in the *two*-value form. The grammar's second
		// alternative is "[left|center|right|<length-percentage>]
		// [top|center|bottom|<length-percentage>]", so this is the left edge
		// horizontally and 40 down from the top — not, as a greedy reading has
		// it, "40 in from the left" with nothing said about the vertical.
		{"left 40px", 10, 50},
		{"right 5px", 210, 15},
		{"30px bottom", 40, 130},
		// A negative vertical offset, which is the form the suite writes —
		// "background: url(x) left -1em" — and the one a greedy grouping loses
		// altogether, leaving the image at the origin.
		{"left -5px", 10, 5},
		// The same words with a third value are the four-value form again, and
		// there the keyword does take the offset: 30 in from the left, and the
		// vertical centres on the keyword it names.
		{"left 30px top", 40, 10},
		{"left 30px bottom", 40, 130},
	}
	for _, tc := range cases {
		t.Run(tc.position, func(t *testing.T) {
			ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
				`#a { background-image: url(wide.png); background-repeat: no-repeat;
				      background-position: `+tc.position+` }`)
			got := firstTiling(t, ops)
			if got.Tile.X.Px() != tc.x || got.Tile.Y.Px() != tc.y {
				t.Errorf("the tile is at (%v, %v), want (%v, %v)",
					got.Tile.X.Px(), got.Tile.Y.Px(), tc.x, tc.y)
			}
		})
	}
}

// TestBackgroundSize pins every form of the property against the specification's
// arithmetic. The positioning area is the padding box, 240 × 140.
func TestBackgroundSize(t *testing.T) {
	cases := []struct {
		image, size string
		w, h        float64
	}{
		// auto is the intrinsic size, one image pixel to one CSS pixel.
		{"wide.png", "auto", 40, 20},
		{"wide.png", "auto auto", 40, 20},
		// A length, and a length with auto: the ratio of 2 fills the other axis.
		{"wide.png", "80px", 80, 40},
		{"wide.png", "80px auto", 80, 40},
		{"wide.png", "auto 80px", 160, 80},
		{"wide.png", "80px 30px", 80, 30},
		// Percentages are of the positioning area, per axis.
		{"wide.png", "50%", 120, 60},
		{"wide.png", "50% 50%", 120, 70},
		// contain: the largest that fits, ratio kept. 240/40 = 6 and 140/20 = 7,
		// so the width binds and the scale is 6.
		{"wide.png", "contain", 240, 120},
		// cover: the smallest that covers, so the scale is 7.
		{"wide.png", "cover", 280, 140},
		// The tall picture is 20 × 40, ratio a half. 240/20 = 12, 140/40 = 3.5.
		{"tall.png", "contain", 70, 140},
		{"tall.png", "cover", 240, 480},
	}
	for _, tc := range cases {
		t.Run(tc.image+" "+tc.size, func(t *testing.T) {
			ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
				`#a { background-image: url(`+tc.image+`); background-repeat: no-repeat;
				      background-size: `+tc.size+` }`)
			got := firstTiling(t, ops)
			if got.Tile.W.Px() != tc.w || got.Tile.H.Px() != tc.h {
				t.Errorf("the tile is %v × %v, want %v × %v",
					got.Tile.W.Px(), got.Tile.H.Px(), tc.w, tc.h)
			}
		})
	}
}

// TestBackgroundSizeZeroPaintsNothing pins §3.9: an image with no area is not
// drawn. It is also the degenerate end of the amplification — a tile of zero
// size repeated is an infinite number of marks.
func TestBackgroundSizeZeroPaintsNothing(t *testing.T) {
	for _, size := range []string{"0", "0 50px", "50px 0", "0%"} {
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-size: `+size+` }`)
		if got := tilings(ops); len(got) != 0 {
			t.Errorf("background-size: %s painted %d tilings, want none", size, len(got))
		}
	}
}

// TestBackgroundRepeatModes pins the four repeat styles and the two axis
// shorthands, in the numbers each produces.
func TestBackgroundRepeatModes(t *testing.T) {
	// The positioning area is the padding box (10, 10) 240 × 140 and the
	// painting area is the border box (0, 0) 260 × 160.
	border := rect(t, 0, 0, 260, 160)

	t.Run("repeat", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: repeat }`)
		// Tiles butted together from the origin, over the whole painting area.
		wantTiling(t, firstTiling(t, ops), rect(t, 10, 10, 40, 20), border, 40, 20)
	})

	t.Run("repeat-x", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: repeat-x }`)
		// Horizontally over the whole area; vertically confined to the one band
		// the tile occupies, which is what stops it repeating down the page.
		wantTiling(t, firstTiling(t, ops),
			rect(t, 10, 10, 40, 20), rect(t, 0, 10, 260, 20), 40, 20)
	})

	t.Run("repeat-y", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: repeat-y }`)
		wantTiling(t, firstTiling(t, ops),
			rect(t, 10, 10, 40, 20), rect(t, 10, 0, 40, 160), 40, 20)
	})

	t.Run("no-repeat", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: no-repeat }`)
		wantTiling(t, firstTiling(t, ops),
			rect(t, 10, 10, 40, 20), rect(t, 10, 10, 40, 20), 40, 20)
	})

	t.Run("space", func(t *testing.T) {
		// 240 / 40 is exactly 6 across, so there is no remainder and the step is
		// the tile. Down: 140 / 20 is 7, also exact.
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: space }`)
		wantTiling(t, firstTiling(t, ops), rect(t, 10, 10, 40, 20), border, 40, 20)
	})

	t.Run("space with a remainder", func(t *testing.T) {
		// A 50px-wide tile in a 240px area: 4 fit, leaving 40px to spread over
		// the 3 gaps, so the step is 50 + 40/3. Down, a 30px tile in 140 leaves
		// 4 tiles and 20px over 3 gaps.
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: space;
			      background-size: 50px 30px }`)
		got := firstTiling(t, ops)
		if got.Tile != rect(t, 10, 10, 50, 30) {
			t.Errorf("the first tile is %s, want it against the positioning area's corner", got.Tile)
		}
		// 40/3 is 13.333…, which in 64ths of a pixel is 853 units: the assertion
		// is on the exact fixed-point value rather than on a float comparison.
		wantStepX := bgPx(t, 50).Add(bgPx(t, 40).Div(3))
		wantStepY := bgPx(t, 30).Add(bgPx(t, 20).Div(3))
		if got.StepX != wantStepX || got.StepY != wantStepY {
			t.Errorf("the step is %v,%v, want %v,%v",
				got.StepX.Px(), got.StepY.Px(), wantStepX.Px(), wantStepY.Px())
		}
	})

	t.Run("space with room for one", func(t *testing.T) {
		// A tile wider than half the area leaves room for one, and §3.6 says the
		// position is then used and the image is not repeated — which is
		// no-repeat, and is why the clip below is one tile rather than the box.
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: space;
			      background-size: 200px 100px; background-position: right bottom }`)
		got := firstTiling(t, ops)
		// The free space is 240 − 200 = 40 across and 140 − 100 = 40 down.
		wantTiling(t, got, rect(t, 50, 50, 200, 100), rect(t, 50, 50, 200, 100), 200, 100)
	})

	t.Run("round", func(t *testing.T) {
		// 240 / 50 is 4.8, which rounds to 5, so the tile becomes 48 wide.
		// 140 / 30 is 4.67, which rounds to 5, so the tile becomes 28 tall.
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: round;
			      background-size: 50px 30px }`)
		wantTiling(t, firstTiling(t, ops), rect(t, 10, 10, 48, 28), border, 48, 28)
	})

	t.Run("round keeps the ratio on an auto axis", func(t *testing.T) {
		// Rounding one axis and leaving the other auto rescales both, or the
		// picture is squashed by however much the rounding moved it. The tile is
		// 50 wide, rounds to 48, and the height follows: 25 × 48/50 = 24.
		ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
			`#a { background-image: url(wide.png); background-repeat: round no-repeat;
			      background-size: 50px auto }`)
		got := firstTiling(t, ops)
		if got.Tile.W.Px() != 48 || got.Tile.H.Px() != 24 {
			t.Errorf("the rounded tile is %v × %v, want 48 × 24 — the ratio of 2 kept",
				got.Tile.W.Px(), got.Tile.H.Px())
		}
	})
}

// TestBackgroundAttachmentFixedPositionsAgainstThePage pins the one real
// difference the property has on a page that does not scroll.
func TestBackgroundAttachmentFixedPositionsAgainstThePage(t *testing.T) {
	// The box is pushed 100px down the page, so a scrolling background starts at
	// its own padding edge and a fixed one starts at the page's corner.
	const css = bgBoxCSS + `#a { margin-top: 100px;
		background-image: url(wide.png); background-repeat: no-repeat;
		background-position: center }`

	scroll := firstTiling(t, bgPaintOf(t, `<div id="a"></div>`, css))
	fixed := firstTiling(t, bgPaintOf(t, `<div id="a"></div>`,
		css+`#a { background-attachment: fixed }`))

	// Scrolling: centred in the padding box, which is 240 × 140 at (10, 110).
	if scroll.Tile.X.Px() != 110 || scroll.Tile.Y.Px() != 170 {
		t.Errorf("a scrolling background is at (%v, %v), want (110, 170) — centred in its own box",
			scroll.Tile.X.Px(), scroll.Tile.Y.Px())
	}
	// Fixed: centred in the page's 500 × 400 content area, so at
	// (500−40)/2 = 230 and (400−20)/2 = 190.
	if fixed.Tile.X.Px() != 230 || fixed.Tile.Y.Px() != 190 {
		t.Errorf("a fixed background is at (%v, %v), want (230, 190) — centred on the page",
			fixed.Tile.X.Px(), fixed.Tile.Y.Px())
	}
	// And it is still clipped to the box, which is what makes fixed different
	// from painting on the canvas. The tile is 40 wide at x=230 and the box's
	// border box ends at 260, so 30 of it is visible and the rest is cut.
	if fixed.Clip != rect(t, 230, 190, 30, 20) {
		t.Errorf("a fixed background is clipped to %s, want (230,190 30x20) — the tile "+
			"cut by the box's border box, not the whole page", fixed.Clip)
	}
}

// TestBackgroundPaintsBetweenTheColourAndTheBorder pins CSS 2.1 Appendix E: the
// image goes over the colour and under the border, and both go under the
// content.
func TestBackgroundPaintsBetweenTheColourAndTheBorder(t *testing.T) {
	ops := bgPaintOf(t, `<div id="a">text</div>`, bgBoxCSS+
		`#a { background-color: #ff0000; background-image: url(wide.png);
		      border-color: #0000ff }`)

	colour, tile, border, text := -1, -1, -1, -1
	for i, op := range ops {
		switch v := op.(type) {
		case FillRect:
			switch {
			case v.Color.R == 255 && colour < 0:
				colour = i
			case v.Color.B == 255 && border < 0:
				border = i
			}
		case TileImage:
			if tile < 0 {
				tile = i
			}
		case DrawText:
			if text < 0 {
				text = i
			}
		}
	}
	for _, step := range []struct {
		name string
		at   int
	}{{"the colour", colour}, {"the image", tile}, {"the border", border}, {"the text", text}} {
		if step.at < 0 {
			t.Fatalf("%s did not paint: %v", step.name, ops)
		}
	}
	if !(colour < tile && tile < border && border < text) {
		t.Errorf("the painting order is colour=%d image=%d border=%d text=%d; "+
			"Appendix E puts the image over the colour and under the border",
			colour, tile, border, text)
	}
}

// TestBackgroundLayers pins the comma-separated form: the number of layers is
// background-image's, every other property is used cyclically, and the *first*
// layer written is painted last so that it ends up on top.
func TestBackgroundLayers(t *testing.T) {
	ops := bgPaintOf(t, `<div id="a"></div>`, bgBoxCSS+
		`#a { background-image: url(wide.png), url(tall.png);
		      background-repeat: no-repeat;
		      background-position: left top, right bottom }`)
	got := tilings(ops)
	if len(got) != 2 {
		t.Fatalf("two layers produced %d tilings", len(got))
	}
	// Painted back to front: the last layer written is the first drawn.
	if got[0].Tile != rect(t, 10+240-20, 10+140-40, 20, 40) {
		t.Errorf("the first mark is %s; it should be the *last* layer, at the bottom right", got[0].Tile)
	}
	if got[1].Tile != rect(t, 10, 10, 40, 20) {
		t.Errorf("the second mark is %s; it should be the first layer, at the top left", got[1].Tile)
	}
	// One repeat value for two images means both, which is the cyclic rule.
	for i, v := range got {
		if v.Clip != v.Tile {
			t.Errorf("layer %d repeats: its clip %s is not its tile %s, so the single "+
				"no-repeat did not reach it", i, v.Clip, v.Tile)
		}
	}
}

// TestBackgroundPropagatesToTheCanvas pins CSS 2.1 §14.2 and css-backgrounds-3
// §2.11: the root's background covers the whole page, and when the root declares
// none it is taken from <body> — which then paints nothing of its own.
func TestBackgroundPropagatesToTheCanvas(t *testing.T) {
	page := rect(t, 0, 0, 500, 400)

	t.Run("from the root", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, noDefaults+
			`html { background-color: #ff0000; height: 10px }`)
		var fills []FillRect
		for _, op := range ops {
			if v, ok := op.(FillRect); ok && v.Color.R == 255 {
				fills = append(fills, v)
			}
		}
		if len(fills) != 1 {
			t.Fatalf("the root's background painted %d times, want once", len(fills))
		}
		if fills[0].Rect != page {
			t.Errorf("the canvas background is %s, want the whole page %s — and not the "+
				"root's own 10px-tall box", fills[0].Rect, page)
		}
	})

	t.Run("from the body", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, noDefaults+
			`body { background-color: #00ff00; height: 10px }`)
		var fills []FillRect
		for _, op := range ops {
			if v, ok := op.(FillRect); ok && v.Color.G == 255 {
				fills = append(fills, v)
			}
		}
		if len(fills) != 1 {
			t.Fatalf("the body's background painted %d times, want once — it is "+
				"propagated to the canvas and must not also be painted on its own box", len(fills))
		}
		if fills[0].Rect != page {
			t.Errorf("the propagated background is %s, want the whole page %s", fills[0].Rect, page)
		}
	})

	t.Run("the root wins over the body", func(t *testing.T) {
		ops := bgPaintOf(t, `<div id="a"></div>`, noDefaults+
			`html { background-color: #ff0000 } body { background-color: #00ff00; height: 10px }`)
		var canvas *FillRect
		var bodyOwn *FillRect
		for i := range ops {
			v, ok := ops[i].(FillRect)
			if !ok {
				continue
			}
			c := v
			switch {
			case c.Color.R == 255:
				canvas = &c
			case c.Color.G == 255:
				bodyOwn = &c
			}
		}
		if canvas == nil || canvas.Rect != page {
			t.Errorf("the root's background did not cover the canvas: %v", canvas)
		}
		// The body declared one of its own, so it is *not* propagated and is
		// painted on the body's box like any other element's.
		if bodyOwn == nil {
			t.Fatal("the body's own background did not paint")
		}
		if bodyOwn.Rect == page {
			t.Error("the body's background covered the page; only a propagated one does")
		}
	})

	t.Run("an image propagates too", func(t *testing.T) {
		// The picture is positioned against the root's box and painted over the
		// whole canvas, which is the half of the rule that is easy to miss.
		ops := bgPaintOf(t, `<div id="a"></div>`, noDefaults+
			`body { background-image: url(wide.png); background-repeat: repeat }`)
		got := firstTiling(t, ops)
		if got.Clip != page {
			t.Errorf("the propagated image is clipped to %s, want the whole page", got.Clip)
		}
	})
}

// TestBackgroundTileCapFires is the planted-attack test: a tiling past the cap
// is refused, reported, and paints nothing.
//
// A cap that has never been seen to fire proves nothing, so this drives it from
// both directions — a document that trips it at the real value, and the same
// document passing once the cap is raised.
func TestBackgroundTileCapFires(t *testing.T) {
	// A hundredth of a pixel over a 260 × 160 border box is 26000 × 16000 tiles,
	// which is four hundred million: past the cap by two orders of magnitude and
	// reached with a declaration eleven characters long.
	const css = bgBoxCSS + `#a { background-image: url(wide.png);
		background-size: 0.01px 0.01px; background-repeat: repeat }`

	frag, findings := bgLayoutWithFindings(t, `<div id="a"></div>`, css)
	if got := tilings(Paint(frag)); len(got) != 0 {
		t.Errorf("a tiling of four hundred million cells was emitted: %v", got[0])
	}
	var reported bool
	for _, f := range findings {
		if f.Rule == RuleLimit && strings.Contains(f.Message, "tiled") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the tile cap fired silently, which is worse than not firing: %v", findings)
	}

	// And the same document with the cap raised does paint, which is what shows
	// the refusal was the cap rather than some other fault in the arithmetic.
	old := maxBackgroundTiles
	maxBackgroundTiles = 1 << 40
	defer func() { maxBackgroundTiles = old }()
	if got := tilings(bgPaintOf(t, `<div id="a"></div>`, css)); len(got) != 1 {
		t.Errorf("with the cap raised the tiling was still refused; the cap is not "+
			"what stopped it: %d tilings", len(got))
	}
}

// TestBackgroundLayerCapFires is the same for the layer list, which is the other
// axis of the same amplification: a hundred thousand layers of one tile each
// costs as much as one layer of a hundred thousand tiles.
func TestBackgroundLayerCapFires(t *testing.T) {
	old := maxBackgroundLayers
	maxBackgroundLayers = 3
	defer func() { maxBackgroundLayers = old }()

	images := strings.TrimSuffix(strings.Repeat("url(wide.png),", 6), ",")
	frag, findings := bgLayoutWithFindings(t, `<div id="a"></div>`, bgBoxCSS+
		`#a { background-image: `+images+`; background-repeat: no-repeat }`)
	if got := tilings(Paint(frag)); len(got) != 3 {
		t.Errorf("six layers with a cap of three produced %d tilings, want 3", len(got))
	}
	var reported bool
	for _, f := range findings {
		if f.Rule == RuleLimit && strings.Contains(f.Message, "layers") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the layer cap fired silently: %v", findings)
	}
}

// TestBackgroundImageFailuresAreReported pins that a background that did not
// arrive is a finding rather than a quietly empty box — the same guarantee an
// <img> has, and for the same reason.
func TestBackgroundImageFailuresAreReported(t *testing.T) {
	cases := []struct {
		css  string
		rule Rule
		says string
	}{
		{"background-image: url(missing.png)", RuleResourceBlocked, "background image"},
		{"background-image: url(http://example.com/x.png)", RuleResourceBlocked, "resolves no URLs"},
		{"background-image: url(../escape.png)", RuleResourceBlocked, "leaves the directory"},
		{"background-image: linear-gradient(red, blue)", RuleUnsupportedValue, "not one this engine can paint"},
	}
	for _, tc := range cases {
		t.Run(tc.css, func(t *testing.T) {
			frag, findings := bgLayoutWithFindings(t, `<div id="a"></div>`,
				bgBoxCSS+`#a { `+tc.css+` }`)
			if got := tilings(Paint(frag)); len(got) != 0 {
				t.Errorf("something was painted for a background that did not load")
			}
			var found bool
			for _, f := range findings {
				if f.Rule == tc.rule && strings.Contains(f.Message, tc.says) {
					found = true
				}
			}
			if !found {
				t.Errorf("no %s finding saying %q: %v", tc.rule, tc.says, findings)
			}
		})
	}
}

// TestBackgroundImageURLForms pins that both spellings of url() are read.
//
// "url(a.png)" is a single URL token and "url('a.png')" is a function with a
// string in it, because CSS tokenizes the two by different rules. A reader that
// handled only the first would silently drop every quoted reference — which is
// the more common form in real stylesheets, and which fails as a missing picture
// rather than as a parse error.
func TestBackgroundImageURLForms(t *testing.T) {
	for _, form := range []string{
		"url(wide.png)",
		`url("wide.png")`,
		"url('wide.png')",
		"url( 'wide.png' )",
	} {
		t.Run(form, func(t *testing.T) {
			frag, findings := bgLayoutWithFindings(t, `<div id="a"></div>`,
				bgBoxCSS+`#a { background-image: `+form+`; background-repeat: no-repeat }`)
			for _, f := range findings {
				if f.Rule == RuleResourceBlocked || f.Rule == RuleImageUndecodable {
					t.Fatalf("%s did not load: %s", form, f.Error())
				}
			}
			got := firstTiling(t, Paint(frag))
			if got.Tile != rect(t, 10, 10, 40, 20) {
				t.Errorf("%s drew %s, want the 40x20 picture at the padding edge", form, got.Tile)
			}
		})
	}
}

// TestBackgroundImageLoadsOnceForManyBoxes pins that the loading pass is shared
// with <img>'s: one file named by a hundred boxes is read, decoded and charged
// to the document's pixel budget once.
func TestBackgroundImageLoadsOnceForManyBoxes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString(`<div class="c"></div>`)
	}
	frag := bgLayoutOf(t, b.String(), noDefaults+
		`.c { width: 40px; height: 20px; background-image: url(wide.png) }`)

	var keys = map[string]bool{}
	for _, v := range tilings(Paint(frag)) {
		keys[v.Key] = true
	}
	if len(keys) != 1 {
		t.Errorf("fifty boxes naming one file produced %d distinct image keys, want 1", len(keys))
	}
}

// TestBackgroundTilesReportsTheCount pins the arithmetic a backend uses to
// expand a tiling, since getting it wrong by one produces a seam at the edge of
// the page that nothing else here would see.
func TestBackgroundTilesReportsTheCount(t *testing.T) {
	cases := []struct {
		name       string
		clip, tile Rect
		stepX      float64
		cols       int
	}{
		// Tiles at 0, 10, 20…, clip [0, 100): ten tiles.
		{"aligned", rect(t, 0, 0, 100, 10), rect(t, 0, 0, 10, 10), 10, 10},
		// The same tiling with the clip shifted half a tile: eleven, because the
		// one starting at −10 reaches in and the one at 100 does too.
		{"offset", rect(t, 5, 0, 100, 10), rect(t, 0, 0, 10, 10), 10, 11},
		// A tile larger than the clip is one tile.
		{"oversized", rect(t, 0, 0, 10, 10), rect(t, -20, 0, 100, 10), 100, 1},
		// Spaced tiles: step 20, tile 10, clip 100 wide. Tiles start at 0, 20…
		// 100 is outside, so five.
		{"spaced", rect(t, 0, 0, 100, 10), rect(t, 0, 0, 10, 10), 20, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := TileImage{
				Clip: tc.clip, Tile: tc.tile,
				StepX: bgPx(t, tc.stepX), StepY: tc.tile.H,
			}
			cols, rows := v.Tiles()
			if cols != tc.cols {
				t.Errorf("Tiles reports %d columns, want %d", cols, tc.cols)
			}
			if rows != 1 {
				t.Errorf("Tiles reports %d rows, want 1", rows)
			}
		})
	}
}
