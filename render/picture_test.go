package render

import (
	"fmt"
	"image"
	"math"
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
	// img names the source when this mark is a picture rather than a fill. A
	// picture is compared by which file it is, not by its pixels: comparing
	// decoded images would make this a rasterizer, and the question a reftest
	// asks is whether the two documents drew the same thing in the same place.
	//
	// The exception is a picture that is uniformly one opaque colour, which puts
	// exactly the same ink on exactly the same paper as a fill of that colour —
	// and the suite depends on the equivalence, since its references draw the
	// expected square with a solid PNG while the test draws it with a
	// background. Those arrive here as ordinary fills, so img is empty.
	img string
}

// sample is what is visible at a point: either a colour or a picture.
//
// The two are kept apart rather than reduced to a colour because there is no
// colour that means "a picture of a cat". Folding one into the other would make
// an image compare equal to whatever fill happened to match the synthetic value.
type sample struct {
	c   style.RGBA
	img string
}

func (s sample) same(o sample) bool {
	if s.img != "" || o.img != "" {
		return s.img == o.img
	}
	return sameColour(s.c, o.c)
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
		switch v := op.(type) {
		case FillRect:
			if v.Rect.Empty() || v.Color.A == 0 {
				continue
			}
			out = append(out, coloured{r: v.Rect, c: v.Color})
		case DrawImage:
			if v.Rect.Empty() {
				continue
			}
			// A picture of one opaque colour is a fill of that colour, and is
			// treated as one so that it takes part in occlusion like any other
			// mark. The check reads every pixel and refuses any transparency —
			// half-transparent black does not put the same ink down as black.
			if c, ok := uniformColor(v.Image); ok {
				out = append(out, coloured{r: v.Rect, c: c})
				continue
			}
			// A picture made of uniform rectangles is those rectangles. See
			// imageBands: this is the same equivalence as the line above and not
			// a weaker one, because the decomposition is verified pixel by pixel
			// and refused when it does not hold exactly.
			if fills := bandedFills(v.Image, v.Rect); fills != nil {
				out = append(out, fills...)
				continue
			}
			// Anything else is opaque for the purpose of what lies under it.
			// That is an approximation for a picture with an alpha channel, and
			// it errs towards calling two documents different, which is the
			// safe direction for an oracle.
			out = append(out, coloured{r: v.Rect, c: style.RGBA{A: 1}, img: v.Key})

		case TileImage:
			out = append(out, tiledFills(v)...)
		}
	}
	return out
}

// Seeing through a picture that is not all one colour.
//
// The comparison equates a picture of one opaque colour with a fill of that
// colour, because the two put the same ink on the same paper. Everything else
// used to be one opaque mark keyed by its file, and that turns out to be the
// single largest reason a correct rendering was called wrong.
//
// The CSS 2.1 suite draws with two kinds of picture. One is a solid swatch,
// which the uniform case already handles. The other is a *pattern*: a few solid
// bands or a three-by-three grid, often with a fully transparent region in the
// middle, and the test passes by showing no red through the gap. Compared as an
// opaque unknown, such a picture hides whatever the document put behind it and
// differs from the plain green rectangle its reference draws — so the pair was
// ruled different over a gap that both documents leave in the same place.
//
// The decomposition below is exact rather than an approximation, which is what
// makes it a legitimate thing for an oracle to do. It finds the pixel rows and
// columns where the image changes, forms the grid they define, and then *checks*
// every cell of that grid pixel by pixel; if any cell is not uniform it gives up
// and the picture goes back to being an opaque mark. So it can only ever fire on
// an image that genuinely is a set of uniform rectangles, and for one of those,
// saying so is not a concession — it is the same statement as the uniform case,
// made once per band instead of once per picture.
//
// What it is, said plainly rather than flatteringly: for a small enough picture
// this *is* a rasterizer, and the header above says this comparison is not one.
// A four-by-four image is sixteen uniform rectangles however random its pixels
// are, so the rule cannot exclude it on principle and the bound is what does the
// work. That is a cost decision rather than a correctness one — the marks are
// exact either way — and it is stated here so that the bound is read as the
// line it is rather than as a round number.
//
// The other bound is about correctness and is the one that could have gone
// wrong. A band narrower on the page than the comparison's own sliver is refused
// outright, and the whole picture with it: below a quarter-pixel a cell is
// thrown away as rounding, so a heavily downscaled pattern would decompose into
// marks the comparison then ignores, and a difference inside it would disappear.
// That is the direction an oracle must never err in, so the picture goes back to
// being an opaque mark instead.

// maxImageBands bounds the grid a picture may be decomposed into.
//
// The suite's patterns are three by three and five by five, and sixty-four is
// chosen against them rather than against the cost: measured over the whole
// suite, raising it to 256 moves no test at all, and the run is a second faster
// with the decomposition than without it because a pattern that decomposes stops
// being an opaque mark that forces every cell under it to be resolved.
const maxImageBands = 64

// band is one uniform rectangle of a picture, in image pixels. x1 and y1 are
// exclusive, in the manner of image.Rectangle.
type band struct {
	x0, y0, x1, y1 int
	c              style.RGBA
}

// imageBands memoizes the decomposition, which reads every pixel twice and is
// asked for once per picture per document over five thousand documents.
var imageBands = map[image.Image][]band{}

// bandsOf decomposes a picture into the uniform rectangles it is made of, or
// reports nil if it is not made of them.
func bandsOf(img image.Image) []band {
	if img == nil {
		return nil
	}
	if got, ok := imageBands[img]; ok {
		return got
	}
	got := scanBands(img)
	imageBands[img] = got
	return got
}

func scanBands(img image.Image) []band {
	b := img.Bounds()
	if b.Empty() {
		return nil
	}
	at := func(x, y int) style.RGBA {
		r, g, bl, a := img.At(x, y).RGBA()
		return style.RGBA{
			R: float64(r >> 8), G: float64(g >> 8), B: float64(bl >> 8),
			A: float64(a) / 0xFFFF,
		}
	}

	// The grid lines: a column that differs anywhere from the one before it
	// starts a new band, and the same down the other axis.
	//
	// The lines alone are enough, and it is worth writing down why, because the
	// obvious reading is that they are not. A picture that changes along both
	// axes at once — a diagonal, a circle — plainly cannot be cut into a few
	// uniform rectangles, so the instinct is that these lines might produce a
	// coarse grid with ragged cells in it and that each cell has to be read to
	// find out. A verification pass was written on that instinct, and planting
	// its removal changed nothing at all, because the cells it checked are
	// uniform by construction:
	//
	//	Take a cell with no grid line strictly inside it and suppose two of its
	//	pixels differ. Walk from one to the other through the cell, a step at a
	//	time; the walk stays inside because a rectangle is connected. Somewhere
	//	along it two adjacent pixels differ. If they differ across a column
	//	boundary at x, then column x differs from column x-1 and x is a grid
	//	line inside the cell; if across a row boundary, likewise for y. Either
	//	way the supposition contradicts the premise.
	//
	// So the diagonal is not refused by a ragged cell — it is refused by the
	// bound, having produced a line for every row and every column. The check is
	// deleted rather than kept as a cheap assertion because it read as the thing
	// that made the decomposition safe and it was not; what makes it safe is the
	// argument above, and TestImageBandsAreExact holds the argument to account by
	// checking the invariant over pictures built to break it.
	xs := []int{b.Min.X}
	for x := b.Min.X + 1; x < b.Max.X; x++ {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if at(x, y) != at(x-1, y) {
				xs = append(xs, x)
				break
			}
		}
		if len(xs) > maxImageBands {
			return nil
		}
	}
	ys := []int{b.Min.Y}
	for y := b.Min.Y + 1; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if at(x, y) != at(x, y-1) {
				ys = append(ys, y)
				break
			}
		}
		if len(ys) > maxImageBands {
			return nil
		}
	}
	if (len(xs))*(len(ys)) > maxImageBands {
		return nil
	}
	xs = append(xs, b.Max.X)
	ys = append(ys, b.Max.Y)

	out := make([]band, 0, (len(xs)-1)*(len(ys)-1))
	for i := 0; i+1 < len(xs); i++ {
		for j := 0; j+1 < len(ys); j++ {
			c := at(xs[i], ys[j])
			out = append(out, band{xs[i], ys[j], xs[i+1], ys[j+1], c})
		}
	}
	return out
}

// bandedFills places a decomposed picture on the page, or reports nil if it
// should be compared as a picture after all.
//
// A fully transparent band is dropped rather than painted, which is the whole
// point: it is where the document behind shows through.
func bandedFills(img image.Image, r Rect) []coloured {
	bands := bandsOf(img)
	if bands == nil || r.Empty() {
		return nil
	}
	b := img.Bounds()
	sx := r.W.Px() / float64(b.Dx())
	sy := r.H.Px() / float64(b.Dy())
	out := make([]coloured, 0, len(bands))
	for _, bd := range bands {
		x0, _ := style.FromPx(r.X.Px() + float64(bd.x0-b.Min.X)*sx)
		x1, _ := style.FromPx(r.X.Px() + float64(bd.x1-b.Min.X)*sx)
		y0, _ := style.FromPx(r.Y.Px() + float64(bd.y0-b.Min.Y)*sy)
		y1, _ := style.FromPx(r.Y.Px() + float64(bd.y1-b.Min.Y)*sy)
		// A band the comparison would throw away as rounding is a band this
		// must not produce: dropping it would make the picture *less* visible
		// than it is, which is a false pass waiting to happen. The picture goes
		// back to being one opaque mark instead.
		if x1.Sub(x0) < sliver || y1.Sub(y0) < sliver {
			return nil
		}
		if bd.c.A == 0 {
			continue
		}
		out = append(out, coloured{r: Rect{X: x0, Y: y0, W: x1.Sub(x0), H: y1.Sub(y0)}, c: bd.c})
	}
	return out
}

// maxComparedTiles bounds how far a tiling is expanded for the comparison.
//
// The engine emits a tiling as one operation with a step in it, which is what
// keeps its own cost independent of the count — but this comparison resolves
// occlusion by cutting the page at every rectangle edge, so expanding a tiling
// into tiles costs the *square* of the count. A repeating 15px image over an A4
// page is nearly two thousand tiles and four thousand grid lines, which is
// twenty million cells for one document of five thousand.
//
// Past the bound the tiling is compared as a single mark keyed by its geometry.
// Two documents that tile the same picture the same way still agree; two that
// tile it differently still differ. What is lost is the ability to see *through*
// the gaps of a spaced tiling with more than this many tiles in it, which errs
// towards calling documents equal — so the bound is set well above anything the
// suite draws rather than at a round number.
const maxComparedTiles = 1024

// tiledFills turns one tiling into the marks it puts on the page.
func tiledFills(v TileImage) []coloured {
	if v.Clip.Empty() || v.Tile.Empty() || v.StepX <= 0 || v.StepY <= 0 {
		return nil
	}
	cols, rows := v.Tiles()
	if cols <= 0 || rows <= 0 {
		return nil
	}

	colour, uniform := uniformColor(v.Image)
	gapless := v.StepX <= v.Tile.W && v.StepY <= v.Tile.H
	if uniform && gapless {
		// Tiles butted against each other, all of one opaque colour: the clip is
		// covered by that colour and nothing about which tile is where is
		// visible. This is the common case — the suite's pictures are solid
		// squares — and collapsing it is what keeps the expansion below rare.
		return []coloured{{r: v.Clip, c: colour}}
	}
	// A patterned tile is decomposed like any other picture, once per tile. The
	// budget is against the *marks* rather than the tiles, because that is what
	// the grid compression pays for: a three-by-three pattern over a hundred
	// tiles is nine hundred rectangles, and the cost of the comparison is the
	// square of how many edges they contribute.
	var bands []band
	if !uniform {
		if bs := bandsOf(v.Image); bs != nil && cols*rows*len(bs) <= maxComparedTiles {
			bands = bs
		}
	}
	if bands == nil && cols > maxComparedTiles/rows {
		key := fmt.Sprintf("tiled:%s %s step %s,%s",
			v.Key, rectKey(v.Tile), num(v.StepX), num(v.StepY))
		return []coloured{{r: v.Clip, c: style.RGBA{A: 1}, img: key}}
	}

	// Few enough to place exactly. The first tile drawn is the one at or before
	// the clip's near edge, which is what the span arithmetic in Tiles counts
	// from.
	firstX := alignTile(v.Clip.X, v.Tile.X, v.Tile.W, v.StepX)
	firstY := alignTile(v.Clip.Y, v.Tile.Y, v.Tile.H, v.StepY)

	out := make([]coloured, 0, cols*rows)
	for j := 0; j < rows; j++ {
		y := firstY.Add(v.StepY.Mul(float64(j)))
		for i := 0; i < cols; i++ {
			x := firstX.Add(v.StepX.Mul(float64(i)))
			tile := Rect{X: x, Y: y, W: v.Tile.W, H: v.Tile.H}
			r := intersect(tile, v.Clip)
			if r.Empty() {
				continue
			}
			if uniform {
				out = append(out, coloured{r: r, c: colour})
				continue
			}
			if bands != nil {
				// The bands are placed against the whole tile and then clipped,
				// so a tile half off the edge shows the half of the pattern that
				// is on the page rather than the whole of it squeezed in.
				placed := bandedFills(v.Image, tile)
				if placed == nil {
					// Refused for this geometry — a band below a sliver. Fall
					// back for every tile rather than for this one, so that the
					// picture is one thing throughout.
					bands = nil
					out = append(out, coloured{r: r, c: style.RGBA{A: 1}, img: v.Key})
					continue
				}
				for _, f := range placed {
					if c := intersect(f.r, v.Clip); !c.Empty() {
						out = append(out, coloured{r: c, c: f.c})
					}
				}
				continue
			}
			out = append(out, coloured{r: r, c: style.RGBA{A: 1}, img: v.Key})
		}
	}
	return out
}

// alignTile is the position of the first tile that reaches into the clip.
func alignTile(clipLo, tileLo, size, step style.Unit) style.Unit {
	k := math.Floor(clipLo.Sub(tileLo).Sub(size).Px()/step.Px()) + 1
	return tileLo.Add(step.Mul(k))
}

func intersect(a, b Rect) Rect {
	x := style.Max(a.X, b.X)
	y := style.Max(a.Y, b.Y)
	return Rect{
		X: x, Y: y,
		W: style.Min(a.Right(), b.Right()).Sub(x),
		H: style.Min(a.Bottom(), b.Bottom()).Sub(y),
	}
}

// texts extracts the glyph runs, canonicalised and sorted.
//
// Text is compared as marks rather than as picture: this engine has no glyph
// rasteriser, and treating a run as the rectangle it occupies would make two
// different words at the same place compare equal — a false pass, and the
// direction of error worth avoiding. Sorting is safe here in a way it is not for
// rectangles, because overlapping text is not how any of these tests are built.
// textMark is one run: what it says, and where.
//
// The position is kept as a number rather than folded into the string because
// the two documents of a reftest reach it by different arithmetic, and comparing
// formatted text makes every comparison exact — which is stricter than the marks
// on the page can possibly be.
type textMark struct {
	what string
	x, y style.Unit
}

func texts(ops []Op, under []coloured) []textMark {
	var out []textMark
	for _, op := range ops {
		v, ok := op.(DrawText)
		if !ok || strings.TrimSpace(v.Text) == "" {
			// A space marks no paper. It is drawn so that text extraction
			// works, and two documents may legitimately put a different number
			// of them between the same visible glyphs.
			continue
		}
		if invisibleInk(v, under) {
			// Ink the same colour as what is under it makes no mark. This is
			// not a nicety: a whole family of tests puts "color: white" on
			// content it wants out of the way, and the reference beside it
			// simply does not draw that content at all. Counting the run as a
			// mark made every one of those pairs differ over letters neither
			// document shows.
			continue
		}
		out = append(out, textMark{
			what: fmt.Sprintf("text %q size %s", v.Text, num(v.Size)),
			x:    v.At.X, y: v.At.Y,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].what != out[j].what {
			return out[i].what < out[j].what
		}
		if out[i].x != out[j].x {
			return out[i].x < out[j].x
		}
		return out[i].y < out[j].y
	})
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

// paper is what a page is before anything is drawn on it.
//
// Treating bare page as *transparent* was wrong in a way that only showed up
// once documents started painting white deliberately. A reference that fills its
// interior white and a test that leaves it alone put exactly the same picture on
// exactly the same paper, and comparing them as white against nothing called
// them different. Nothing is not a colour; on paper it is the colour of the
// paper, and for a PDF page that is white.
var paper = style.RGBA{R: 255, G: 255, B: 255, A: 1}

// colourAt resolves what is visible at a point.
//
// The walk is from the front so that the common case — an opaque mark on top —
// stops at the first rectangle. A translucent mark blends with what is under it,
// which is why the accumulation is a composite rather than a first-hit.
func colourAt(fs []coloured, x, y style.Unit) sample {
	var acc style.RGBA
	remaining := 1.0
	for i := len(fs) - 1; i >= 0; i-- {
		f := fs[i]
		if x < f.r.X || x >= f.r.X.Add(f.r.W) || y < f.r.Y || y >= f.r.Y.Add(f.r.H) {
			continue
		}
		if f.img != "" {
			// A picture hides what is beneath it, so the walk stops here.
			// Anything translucent painted over it does not change *which*
			// picture is at this point, and blending a colour into one would
			// invent a value that neither document could be compared against.
			return sample{img: f.img}
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
	// Whatever light got through every mark falls on the page itself.
	acc.R += paper.R * remaining
	acc.G += paper.G * remaining
	acc.B += paper.B * remaining
	acc.A += remaining
	return sample{c: acc}
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
	gf, wf := picFills(got), picFills(want)
	gt, wt := texts(got, gf), texts(want, wf)
	if len(gt) != len(wt) {
		return false
	}
	for i := range gt {
		if gt[i].what != wt[i].what || !nearlyAt(gt[i], wt[i]) {
			return false
		}
	}

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
			if !colourAt(gf, x, y).same(colourAt(wf, x, y)) {
				return false
			}
		}
	}
	return true
}

// invisibleInk reports whether a run is the colour of what it is drawn on.
//
// The sample is taken at the run's origin, which is on the baseline at its left
// edge — inside the run's own line and inside whatever box is behind it. A run
// that begins over one colour and ends over another is not covered by this and
// is compared as a mark, which is the safe direction: the risk of dropping a run
// that is visible somewhere is worse than the risk of keeping one that is not.
//
// Transparent ink is included by the same rule, since a fully transparent run
// leaves whatever is underneath exactly as it was.
func invisibleInk(v DrawText, under []coloured) bool {
	if v.Color.A == 0 {
		return true
	}
	if v.Color.A < 1 {
		// Partly transparent ink changes what is under it even when it is the
		// same hue, and working out by how much is compositing the glyph
		// coverage, which this comparison deliberately does not do.
		return false
	}
	got := colourAt(under, v.At.X, v.At.Y)
	if got.img != "" {
		return false
	}
	return sameColour(got.c, v.Color)
}

// nearlyAt reports whether two runs are in the same place.
//
// "The same place" cannot mean the same number. A layout unit is a sixty-fourth
// of a pixel and the two documents of a reftest compute their geometry by
// different routes, so a run measured as three separate spaces lands a unit away
// from the same run measured once — 57.609375 against 57.59375, which is one
// unit and is not a rendering difference by any standard.
//
// The comparison used to render the position to a hundredth of a pixel and match
// the text exactly, which made it *finer* than the engine's own quantum: two
// positions a single layout unit apart could round to different hundredths and
// be ruled different. Fills have never been compared that way — the sliver rule
// discards a disagreement narrower than a quarter pixel — so text was being held
// to a standard sixteen times stricter than everything beside it, for no reason
// anyone chose.
//
// A quarter pixel it is, then, for the same reason and with the same caveat: the
// error grows with the number of runs measured separately, so this is a bound on
// what has been seen rather than a proof. What it buys is that the two halves of
// this comparison now disagree about geometry in the same way.
func nearlyAt(a, b textMark) bool {
	off := func(p, q style.Unit) bool {
		if p > q {
			p, q = q, p
		}
		return q.Sub(p) <= sliver
	}
	return off(a.x, b.x) && off(a.y, b.y)
}
