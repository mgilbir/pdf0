package htmlpdf

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/style"
)

// Rounded corners, drawn as paths: FillPath fills a shape by the even-odd
// rule, ClipPath clips what it holds to one (ISO 32000-2 8.5.3.3.3, 8.5.4).

// pathCases are documents forme draws a path for: a rounded background, a
// rounded border — a ring, two closed shapes going the same way round, which
// only the even-odd rule leaves a hole in — and a box whose rounded overflow
// clip cuts its child.
var pathCases = []struct {
	name, html string
	// ink is the colour inside the shape.
	ink color.RGBA
}{
	{"background", `<div style="width:100px;height:60px;background:rgb(0,0,255);border-radius:20px"></div>`,
		color.RGBA{0, 0, 255, 255}},
	{"ring", `<div style="width:100px;height:100px;border:10px solid rgb(0,128,0);border-radius:30px"></div>`,
		color.RGBA{0, 128, 0, 255}},
	{"clip", `<div style="width:100px;height:100px;border-radius:30px;overflow:hidden">` +
		`<div style="width:200px;height:200px;background:rgb(255,0,0)"></div></div>`,
		color.RGBA{255, 0, 0, 255}},
}

// theShape is the one path operation of a composed page: its path, and what
// the page should show inside it.
func theShape(t *testing.T, c layout.Composed) (layout.Path, string) {
	t.Helper()
	var paths []layout.Path
	var kinds []string
	for _, op := range c.Ops {
		switch v := op.(type) {
		case layout.FillPath:
			paths, kinds = append(paths, v.Path), append(kinds, "f*")
		case layout.ClipPath:
			paths, kinds = append(paths, v.Path), append(kinds, "W* n")
		}
	}
	if len(paths) != 1 {
		t.Fatalf("layout drew %d paths; the case needs one", len(paths))
	}
	return paths[0], kinds[0]
}

// clearOfEdge reports whether a point is well inside or well outside a path by
// forme's own reckoning: Contains says the same margin away in every
// direction, so an approximation of the edge within that cannot decide it.
func clearOfEdge(p layout.Path, x, y, margin float64) (inside, clear bool) {
	at := func(dx, dy float64) bool {
		px, _ := style.FromPx(x + dx)
		py, _ := style.FromPx(y + dy)
		return p.Contains(layout.Point{X: px, Y: py})
	}
	inside = at(0, 0)
	for _, d := range [][2]float64{{margin, 0}, {-margin, 0}, {0, margin}, {0, -margin},
		{margin, margin}, {-margin, -margin}, {margin, -margin}, {-margin, margin}} {
		if at(d[0], d[1]) != inside {
			return inside, false
		}
	}
	return inside, true
}

// TestAPathIsItsShape reads each case's path back out of the written content
// stream and holds it to forme's: the operator is even-odd, and every point of
// a grid over the box is inside the written path, flattened and filled by the
// even-odd rule, exactly where forme's Path.Contains says it is inside, away
// from the edge.
func TestAPathIsItsShape(t *testing.T) {
	for _, tc := range pathCases {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{HTML: tc.html}
			c := layout.Compose(in, Options{})
			path, op := theShape(t, c)
			doc, _ := roundTrip(t, in, Options{})
			var got *writtenPath
			for _, w := range writtenPaths(t, contentOf(t, doc)) {
				if w.op == op {
					w := w
					got = &w
				}
			}
			if got == nil {
				t.Fatalf("the stream paints no path with %q:\n%s", op, contentOf(t, doc))
			}
			b := path.Bounds()
			checked := 0
			for y := b.Y.Px() - 4; y <= b.Bottom().Px()+4; y += 1.25 {
				for x := b.X.Px() - 4; x <= b.Right().Px()+4; x += 1.25 {
					want, clear := clearOfEdge(path, x, y, 0.5)
					if !clear {
						continue
					}
					checked++
					if evenOdd(got.polys, x, y) != want {
						t.Fatalf("at (%v, %v) the written path has the point inside=%v; forme's has it %v",
							x, y, !want, want)
					}
				}
			}
			if checked < 1000 {
				t.Fatalf("only %d points were clear of the edge; the grid tests nothing", checked)
			}
		})
	}
}

// TestAPathIsDrawnAsItsShape asks the renderers: a point clear inside the
// shape is its colour, and a point clear outside it — in the rounded corner,
// and in the ring's hole — is the white page.
func TestAPathIsDrawnAsItsShape(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	for _, tc := range pathCases {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{HTML: tc.html}
			c := layout.Compose(in, Options{})
			path, _ := theShape(t, c)
			doc, _ := roundTrip(t, in, Options{})
			b := path.Bounds()
			var in1, out1 int
			for _, r := range renderings(t, doc, c, 144) {
				for y := b.Y.Px() + 0.5; y < b.Bottom().Px(); y += 2.5 {
					for x := b.X.Px() + 0.5; x < b.Right().Px(); x += 2.5 {
						// A renderer colours a device pixel the shape
						// touches (Ghostscript's rule without anti-aliasing),
						// and one at 144 dpi is two thirds of a layout
						// pixel: a point is asked about only where the
						// shape's edge is two such pixels away.
						want, clear := clearOfEdge(path, x, y, 1.5)
						if !clear {
							continue
						}
						expect := white
						if want {
							expect, in1 = tc.ink, in1+1
						} else {
							out1++
						}
						if got := r.at(x, y); !near(got, expect, 2) {
							t.Fatalf("%s draws (%v, %v) %v; forme's shape has it inside=%v, %v",
								r.name, x, y, got, want, expect)
						}
					}
				}
			}
			if in1 == 0 || out1 == 0 {
				t.Fatalf("sampled %d points inside and %d outside; the case tests nothing", in1, out1)
			}
		})
	}
}

// TestArcsAreWithinTheirBound measures the one approximation: an ellipse of
// radii 900 and 500 px, written as four quarter arcs, read back as the
// Béziers the stream holds and sampled along each; no sample strays from the
// ellipse by more than a hundredth of a pixel.
func TestArcsAreWithinTheirBound(t *testing.T) {
	u := func(px float64) style.Unit { v, _ := style.FromPx(px); return v }
	cx, cy, rx, ry := 1000.0, 600.0, 900.0, 500.0
	var path layout.Path
	path = append(path, layout.PathSegment{Op: layout.MoveTo, Point: layout.Point{X: u(cx + rx), Y: u(cy)}})
	for i := 0; i < 4; i++ {
		path = append(path, layout.PathSegment{Op: layout.ArcTo, Center: layout.Point{X: u(cx), Y: u(cy)},
			RadiusX: u(rx), RadiusY: u(ry), StartAngle: float64(90 * i), SweepAngle: 90})
	}
	path = append(path, layout.PathSegment{Op: layout.ClosePath})
	doc, err := writePage([]layout.Op{layout.FillPath{Path: path, Color: style.RGBA{A: 1}}},
		layout.PageSizePt(1600, 1000), 1)
	if err != nil {
		t.Fatal(err)
	}
	ws := writtenPaths(t, contentOf(t, rewritten(t, doc)))
	if len(ws) != 1 || len(ws[0].curves) == 0 {
		t.Fatalf("the stream holds %d paths; want one of curves", len(ws))
	}
	worst := 0.0
	for _, c := range ws[0].curves {
		for i := 0; i <= 64; i++ {
			p := bezier(c[0], c[1], c[2], c[3], float64(i)/64)
			// The distance to the ellipse, to first order: its implicit
			// function's excess over its gradient's length.
			dx, dy := (p[0]-cx)/rx, (p[1]-cy)/ry
			f := dx*dx + dy*dy - 1
			g := 2 * math.Hypot(dx/rx, dy/ry)
			if d := math.Abs(f / g); d > worst {
				worst = d
			}
		}
	}
	if worst > 0.01 {
		t.Errorf("the written arcs stray %.4f px from the ellipse; the bound is 0.01", worst)
	}
	if len(ws[0].curves) != 4*90/maxArcPiece {
		t.Errorf("%d curves for four quarter arcs; want %d", len(ws[0].curves), 4*90/maxArcPiece)
	}
}

// TestAPathThatCannotBeWrittenIsRefused: an arc whose angles are no numbers,
// or that sweeps past a whole turn, and a link inside a curved clip, whose
// annotation rectangle cannot follow the curve.
func TestAPathThatCannotBeWrittenIsRefused(t *testing.T) {
	arc := func(start, sweep float64) layout.Path {
		return layout.Path{{Op: layout.MoveTo}, {Op: layout.ArcTo, StartAngle: start, SweepAngle: sweep}}
	}
	for _, tc := range []struct {
		op   layout.Op
		want string
	}{
		{layout.FillPath{Path: arc(0, 360)}, ""},
		{layout.FillPath{Path: arc(0, -360)}, ""},
		{layout.FillPath{Path: arc(0, 361)}, "sweeping"},
		{layout.ClipPath{Path: arc(math.NaN(), 90)}, "no arc"},
		{layout.FillPath{Path: arc(0, math.Inf(-1))}, "no arc"},
	} {
		got := undrawable(tc.op)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%+v: refused %q, want %q", tc.op, got, tc.want)
		}
	}
	u := func(px float64) style.Unit { v, _ := style.FromPx(px); return v }
	composed := layout.Composed{Ops: []layout.Op{layout.ClipPath{
		Path: layout.Path{{Op: layout.MoveTo}, {Op: layout.LineTo, Point: layout.Point{X: u(10), Y: u(10)}}},
		Ops:  []layout.Op{layout.Link{Rects: []layout.Rect{{W: u(5), H: u(5)}}, Href: "https://example.com/"}},
	}}}
	findings, refused := checkDrawable(composed, nil)
	if !refused || len(findings) != 1 || findings[0].Rule != RuleUndrawable ||
		!strings.Contains(findings[0].Message, "link inside a clip") {
		t.Errorf("a link inside a curved clip: refused %v, findings %+v", refused, findings)
	}
}
