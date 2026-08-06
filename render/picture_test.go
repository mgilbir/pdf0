package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgilbir/pdf0/style"
)

// Comparing two renderings as pictures rather than as lists of marks.
//
// A reftest asserts that two documents *look* the same. The obvious way to check
// that from a display list — canonicalise the marks and compare the text — is
// wrong in a way that quietly costs a large part of the suite, because it cannot
// see that one mark covers another.
//
// The idiom almost every CSS 2.1 test is written in makes this fatal. The test
// paints a red box and then covers it with a green one; the reference paints only
// green; you pass if no red is visible. As lists of marks the two never agree —
// the test has a red rectangle in it and the reference does not. Measured over
// the suite, 645 failures are of exactly this shape, and another 357 paint an
// identical rectangle twice in two colours.
//
// So the comparison below resolves occlusion. It does not rasterise: sampling a
// grid fine enough to catch a hairline would cost more than the whole suite is
// worth, and a coarse grid would drop the hairline and turn a real difference
// into a pass — the expensive direction to be wrong in.
//
// Instead it compresses coordinates. Every rectangle edge from *both* documents
// becomes a grid line, which divides the page into cells that are uniform in
// colour by construction: no rectangle edge crosses a cell, so within one cell
// every rectangle either covers all of it or none of it. Resolving each cell is
// then a walk down the paint order to the first opaque cover. That is exact — no
// resolution to choose, and no hairline to lose.

// coloured is one painted rectangle, in paint order.
type coloured struct {
	r Rect
	c style.RGBA
}

// sliver is the width below which a cell is not evidence of anything.
//
// Layout units are exact, but the two documents of a reftest reach their
// geometry by different arithmetic, and a difference of a unit or two is
// rounding rather than a rendering difference. Such a difference shows up as a
// cell a fraction of a pixel wide, whereas the thinnest thing a document can
// deliberately draw is a one-pixel line. A quarter of a pixel separates the two
// cleanly.
const sliver = style.Unit(16) // 1/4 px, given 64 units to the pixel

// picFills extracts the painted rectangles in paint order.
//
// Order is the whole point and must not be sorted away: it is what decides which
// of two overlapping marks is visible.
func picFills(ops []Op) []coloured {
	out := make([]coloured, 0, len(ops))
	for _, op := range ops {
		v, ok := op.(FillRect)
		if !ok || v.Rect.Empty() || v.Color.A == 0 {
			continue
		}
		out = append(out, coloured{v.Rect, v.Color})
	}
	return out
}

// texts extracts the glyph runs, canonicalised and sorted.
//
// Text is compared as marks rather than as picture: this engine has no glyph
// rasteriser, and treating a run as the rectangle it occupies would make two
// different words at the same place compare equal — a false pass, and the
// direction of error worth avoiding. Sorting is safe here in a way it is not for
// rectangles, because overlapping text is not how any of these tests are built.
func texts(ops []Op) []string {
	var out []string
	for _, op := range ops {
		v, ok := op.(DrawText)
		if !ok || strings.TrimSpace(v.Text) == "" {
			// A space marks no paper. It is drawn so that text extraction
			// works, and two documents may legitimately put a different number
			// of them between the same visible glyphs.
			continue
		}
		out = append(out, fmt.Sprintf("text %q at %s,%s size %s",
			v.Text, num(v.At.X), num(v.At.Y), num(v.Size)))
	}
	sort.Strings(out)
	return out
}

// edges collects the distinct grid lines of both documents along one axis.
func edges(lo, hi style.Unit, sets ...[]coloured) []style.Unit {
	seen := map[style.Unit]bool{lo: true, hi: true}
	for _, set := range sets {
		for _, f := range set {
			for _, e := range [2]style.Unit{f.r.X, f.r.X.Add(f.r.W)} {
				if e > lo && e < hi {
					seen[e] = true
				}
			}
		}
	}
	out := make([]style.Unit, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// edgesY is edges along the other axis. The two differ only in which field of
// the rectangle they read, and keeping them apart is cheaper than making Rect
// indexable for the sake of one caller.
func edgesY(lo, hi style.Unit, sets ...[]coloured) []style.Unit {
	seen := map[style.Unit]bool{lo: true, hi: true}
	for _, set := range sets {
		for _, f := range set {
			for _, e := range [2]style.Unit{f.r.Y, f.r.Y.Add(f.r.H)} {
				if e > lo && e < hi {
					seen[e] = true
				}
			}
		}
	}
	out := make([]style.Unit, 0, len(seen))
	for e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// colourAt resolves what is visible at a point.
//
// The walk is from the front so that the common case — an opaque mark on top —
// stops at the first rectangle. A translucent mark blends with what is under it,
// which is why the accumulation is a composite rather than a first-hit.
func colourAt(fs []coloured, x, y style.Unit) style.RGBA {
	var acc style.RGBA
	remaining := 1.0
	for i := len(fs) - 1; i >= 0; i-- {
		f := fs[i]
		if x < f.r.X || x >= f.r.X.Add(f.r.W) || y < f.r.Y || y >= f.r.Y.Add(f.r.H) {
			continue
		}
		a := f.c.A * remaining
		acc.R += f.c.R * a
		acc.G += f.c.G * a
		acc.B += f.c.B * a
		acc.A += a
		remaining -= a
		if remaining <= 0.001 {
			break
		}
	}
	return acc
}

// sameColour reports whether two resolved colours are indistinguishable.
//
// The tolerance is half a step of the 8-bit channel a PDF viewer will quantise
// to; anything finer cannot be seen and is arithmetic noise from compositing.
func sameColour(a, b style.RGBA) bool {
	d := func(x, y float64) bool {
		if x > y {
			x, y = y, x
		}
		return y-x < 0.5
	}
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A*255, b.A*255)
}

// pictureEqual reports whether two display lists paint the same page.
//
// clip is the area compared, which stands in for the viewport a browser would
// have shown: a mark outside it is not part of the picture, exactly as content
// scrolled off the page is not.
func pictureEqual(got, want []Op, clip Rect) bool {
	gt, wt := texts(got), texts(want)
	if len(gt) != len(wt) {
		return false
	}
	for i := range gt {
		if gt[i] != wt[i] {
			return false
		}
	}

	gf, wf := picFills(got), picFills(want)
	xs := edges(clip.X, clip.X.Add(clip.W), gf, wf)
	ys := edgesY(clip.Y, clip.Y.Add(clip.H), gf, wf)

	for i := 0; i+1 < len(xs); i++ {
		x0, x1 := xs[i], xs[i+1]
		if x1.Sub(x0) < sliver {
			continue
		}
		// The midpoint stands for the cell: no edge crosses a cell, so every
		// rectangle covers all of it or none of it.
		x := x0.Add(x1.Sub(x0).Div(2))
		for j := 0; j+1 < len(ys); j++ {
			y0, y1 := ys[j], ys[j+1]
			if y1.Sub(y0) < sliver {
				continue
			}
			y := y0.Add(y1.Sub(y0).Div(2))
			if !sameColour(colourAt(gf, x, y), colourAt(wf, x, y)) {
				return false
			}
		}
	}
	return true
}
