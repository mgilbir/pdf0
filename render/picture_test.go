package render

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mgilbir/pdf0/fonts"
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
	if cols > maxComparedTiles/rows {
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
			r := intersect(Rect{X: x, Y: y, W: v.Tile.W, H: v.Tile.H}, v.Clip)
			if r.Empty() {
				continue
			}
			if uniform {
				out = append(out, coloured{r: r, c: colour})
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
	var marking []DrawText
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
		marking = append(marking, v)
	}

	var out []textMark
	for _, v := range joinRuns(marking) {
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

// joinRuns splices marking runs that abut into the single run they are
// indistinguishable from.
//
// # Why the comparison needs this
//
// A run is a unit of *drawing*, not a unit of ink. Where one document sets "a b"
// and "c d" as two table cells that happen to touch, and the other sets "a bc d"
// as one line of a div, the two put the same glyphs at the same positions in the
// same face — and the earlier comparison ruled them different, because it matched
// each run's string against a run of the other document and found "b" beside
// "bc".
//
// That is a statement about how this engine batches its drawing and not about
// the page. §17.2.1's anonymous table objects are where it bites hardest, because
// splitting content into cells is exactly what those tests do and drawing the
// same content as flowing text is exactly what their references do: 16 of the
// css/CSS2/tables failures were this and nothing else, every glyph already at the
// right place to the layout unit. Another 11 elsewhere in the suite were the same
// thing arrived at by a different route.
//
// # What makes it safe
//
// Two runs are joined only when nothing about them could produce different ink:
// the same face, the same size, the same colour, the same letter-spacing, the
// same direction, the same baseline, and the second starting exactly where the
// first ends. The join preserves the glyph sequence and the position of every
// glyph in it, so a joined pair is equal to another joined pair only if the two
// documents put the same glyphs in the same places. It cannot make two different
// words compare equal, and it cannot move one.
//
// The end of a run is accumulated from each part's own advance rather than
// re-measured from the joined string, which matters twice: it is linear rather
// than quadratic in the length of a line, and it does not let a kerning pair that
// appears only in the joined string shift the boundary this is testing.
//
// # What it is deliberately not shown
//
// Only the runs that mark the page reach here — texts drops white space and
// invisible ink first — and that ordering is not an optimisation, it is what
// makes the advance arithmetic answerable at all. A tab does not advance by its
// glyph's width but to the next tab stop, and a space on a justified line does
// not advance by its own width either; asking a face how wide either of them is
// gives the wrong answer, the wrong answer lands in the middle of a chain of
// joins, and the two documents then join differently. That was measured: joining
// over white space too cost eight tests in css-text/white-space and bidi-text,
// every one of them a tab or a justified line, against the 27 it gained.
//
// A gap between two visible runs therefore stops the chain, whatever is in it.
// That is the safe direction — two runs a space apart are left as two marks,
// exactly as before.
//
// Right-to-left runs are left alone. Their glyphs march the other way from the
// origin, so "starts where the last one ended" is a different sum, and there is
// no case in the suite that needs it — an unnecessary rule here would be a way
// for the oracle to lose a difference it should see.
func joinRuns(runs []DrawText) []DrawText {
	type key struct {
		y, size, spacing style.Unit
		face             *fonts.Face
		colour           style.RGBA
	}
	// Runs are gathered per group and spliced within it. The order of the output
	// is not the paint order any more, which is why this is used only by texts:
	// text is compared as a sorted set of marks, and picFills — where order
	// decides what covers what — never sees it.
	groups := map[key][]DrawText{}
	var order []key
	out := make([]DrawText, 0, len(runs))
	for _, v := range runs {
		if v.RTL || v.Face == nil {
			// A run with no face has no advance to add, so "where it ends" is
			// unanswerable and the chain has to stop at it rather than treat it
			// as zero wide. Nothing the engine draws is faceless; the hand-built
			// runs in picture_check_test.go are, and a rule that joined every one
			// of those into whatever sat at the same point would make that file
			// measure something other than what it says.
			out = append(out, v)
			continue
		}
		k := key{v.At.Y, v.Size, v.CharSpacing, v.Face, v.Color}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], v)
	}
	for _, k := range order {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool { return g[i].At.X < g[j].At.X })
		cur, end := g[0], g[0].At.X.Add(runAdvance(g[0]))
		for _, next := range g[1:] {
			if abs(end.Sub(next.At.X)) <= joinSlack {
				cur.Text += next.Text
				end = end.Add(runAdvance(next))
				continue
			}
			out = append(out, cur)
			cur, end = next, next.At.X.Add(runAdvance(next))
		}
		out = append(out, cur)
	}
	return out
}

// joinSlack is how far apart two runs may be and still count as touching.
//
// One layout unit, which is the smallest difference the engine can represent.
// Both runs were placed by the same engine from the same advances, so an abutting
// pair agrees exactly; the unit of slack is for the rounding of a sum, not for a
// gap.
const joinSlack = style.Unit(1)

// runAdvance is how far a run moves the pen: the face's own advance plus the
// letter-spacing that was already spent on it.
func runAdvance(v DrawText) style.Unit {
	if v.Face == nil {
		return 0
	}
	w, _ := style.FromPx(v.Face.Measure(v.Text, v.Size.Px()))
	return w.Add(v.CharSpacing.Mul(float64(len([]rune(v.Text)))))
}

func abs(u style.Unit) style.Unit {
	if u < 0 {
		return -u
	}
	return u
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
