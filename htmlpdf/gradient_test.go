package htmlpdf

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/style"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Gradients, held to forme's definition of one: layout.Gradient.ColorAt, the
// colour it paints at a point of its tile. The written shading is read back out
// of the file — its geometry from the shading dictionary and the matrices of
// the stream, its colour and alpha by evaluating the functions it names with
// pdf0's own evaluator — and asked the same question. Renderers are asked too.

// gradientCases are backgrounds that between them draw every kind of
// gradient, repeating and not, with transition hints, stops outside the
// line, a hard stop, colours interpolated in another space, a constant alpha,
// alphas that differ, a fade to transparent, and a tiled background.
var gradientCases = []struct{ name, background string }{
	{"linear", `linear-gradient(90deg, rgb(255,0,0), rgb(0,0,255))`},
	{"linear-hint", `linear-gradient(30deg, rgb(255,0,0) 10%, 30%, rgb(0,255,0) 60%, rgb(0,0,255))`},
	{"linear-outside", `linear-gradient(0deg, rgb(255,255,0) -20%, rgb(0,128,255) 150%)`},
	{"linear-hard", `linear-gradient(90deg, rgb(255,0,0) 40%, rgb(0,0,255) 40%)`},
	{"linear-oklch", `linear-gradient(in oklch, rgb(255,0,0), rgb(0,0,255))`},
	{"linear-alpha", `linear-gradient(rgba(255,0,0,0.5), rgba(0,0,255,0.5))`},
	{"linear-alphas", `linear-gradient(rgba(255,0,0,0.2), rgba(0,0,255,0.9))`},
	{"linear-fade", `linear-gradient(90deg, rgb(0,128,0), transparent)`},
	{"radial", `radial-gradient(circle at 30% 40%, rgb(255,255,0), rgb(128,0,128))`},
	{"radial-ellipse", `radial-gradient(ellipse 80px 30px at 40% 60%, rgb(255,255,0), rgba(0,0,255,0.5) 70%, transparent)`},
	{"conic", `conic-gradient(from 45deg at 30% 40%, red, yellow, lime, aqua, blue, magenta, red)`},
	{"conic-alphas", `conic-gradient(rgba(255,0,0,0.3), rgba(0,0,255,0.8), rgba(255,0,0,0.3))`},
	{"repeating-linear", `repeating-linear-gradient(45deg, rgb(255,0,0) 0 10px, rgb(0,0,255) 10px 20px)`},
	{"repeating-linear-soft", `repeating-linear-gradient(10deg, rgb(255,0,0), rgb(0,0,255) 17px, rgb(255,0,0) 34px)`},
	{"repeating-radial", `repeating-radial-gradient(circle at 20px 20px, rgb(255,0,0) 0 5px, rgba(0,0,255,0.3) 5px 12px)`},
	{"repeating-conic", `repeating-conic-gradient(rgb(255,0,0) 0 15deg, rgb(255,255,255) 15deg 30deg)`},
	{"tiled", `linear-gradient(45deg, rgb(255,0,0), rgb(0,0,255)) 0 0 / 30px 25px`},
}

func gradientDoc(background string) Input {
	return Input{HTML: `<div style="width:120px;height:80px;background:` + background + `"></div>`}
}

// theGradient is the one FillGradient of a composed page.
func theGradient(t *testing.T, c layout.Composed) layout.FillGradient {
	t.Helper()
	var out []layout.FillGradient
	for _, op := range c.Ops {
		if v, ok := op.(layout.FillGradient); ok {
			out = append(out, v)
		}
	}
	if len(out) != 1 {
		t.Fatalf("layout drew %d gradients; the case needs one (findings %v)", len(out), c.Findings)
	}
	return out[0]
}

// affine is a PDF matrix [a b c d e f].
type affine [6]float64

func (m affine) mul(n affine) affine { // m then n
	return affine{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m affine) apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

func (m affine) inverse() affine {
	det := m[0]*m[3] - m[1]*m[2]
	a, b, c, d := m[3]/det, -m[1]/det, -m[2]/det, m[0]/det
	return affine{a, b, c, d, -(m[4]*a + m[5]*c), -(m[4]*b + m[5]*d)}
}

var identity = affine{1, 0, 0, 1, 0, 0}

// paintedShading is one sh a stream paints: the shading, the transformation
// from its space to the page's default space, and the alpha it is painted
// with — a constant, or a soft mask with its own transformation.
type paintedShading struct {
	sh        *object.Dictionary
	ctm       affine
	alpha     float64
	mask      *object.Dictionary
	maskSpace affine
}

// shadingsPainted follows a content stream, and the cells of the tiling
// patterns it fills with, for every sh.
func shadingsPainted(t *testing.T, doc *pdf0.Document, stream []byte, res *object.Dictionary, ctm affine) []paintedShading {
	t.Helper()
	type state struct {
		ctm       affine
		alpha     float64
		mask      *object.Dictionary
		maskSpace affine
		pattern   *object.Stream
	}
	st := state{ctm: ctm, alpha: 1}
	var stack []state
	var out []paintedShading
	var operands []core.ContentToken
	lookup := func(kind string, name string) object.Object {
		d := doc.ResolveDict(res.Get(object.Name(kind)))
		if d == nil {
			t.Fatalf("the stream names %s %s and its resources have no %s", kind, name, kind)
		}
		return doc.Resolve(d.Get(object.Name(name)))
	}
	num := func(o object.Object) float64 {
		switch v := doc.Resolve(o).(type) {
		case object.Integer:
			return float64(v)
		case object.Real:
			return float64(v)
		}
		t.Fatalf("%v is not a number", o)
		return 0
	}
	for tk := range core.TokenizeContent(core.Canceler{}, stream) {
		if tk.Kind != core.KindOp {
			operands = append(operands, tk)
			continue
		}
		n := len(operands)
		switch tk.Op {
		case "q":
			stack = append(stack, st)
		case "Q":
			st, stack = stack[len(stack)-1], stack[:len(stack)-1]
		case "cm":
			var m affine
			for i := range m {
				m[i] = operands[n-6+i].Number()
			}
			st.ctm = m.mul(st.ctm)
		case "gs":
			gs, _ := lookup("ExtGState", operands[n-1].Name).(*object.Dictionary)
			if ca := gs.Get("ca"); ca != nil {
				st.alpha = num(ca)
			}
			if sm, ok := doc.Resolve(gs.Get("SMask")).(*object.Dictionary); ok {
				st.mask, st.maskSpace = sm, st.ctm
			}
		case "scn":
			if n >= 1 && operands[n-1].Kind == core.KindName {
				st.pattern, _ = lookup("Pattern", operands[n-1].Name).(*object.Stream)
			}
		case "f", "f*":
			if p := st.pattern; p != nil {
				var m affine
				mat, _ := doc.Resolve(p.Dict.Get("Matrix")).(object.Array)
				for i := range m {
					m[i] = num(mat[i])
				}
				cell, err := doc.StreamData(p)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, shadingsPainted(t, doc, cell, doc.ResolveDict(p.Dict.Get("Resources")), m)...)
			}
		case "sh":
			sh, _ := lookup("Shading", operands[n-1].Name).(*object.Dictionary)
			out = append(out, paintedShading{sh, st.ctm, st.alpha, st.mask, st.maskSpace})
		}
		operands = operands[:0]
	}
	return out
}

// evalRun memoises the PostScript programs the evaluations parse, across the
// tests: each is parsed once rather than once a point. It is not metered.
var evalRun = core.NewRun(nil)

// shadingValue is what a shading paints at a point of the page's default
// space, by its geometry and its function, or false where it paints nothing.
func shadingValue(t *testing.T, doc *pdf0.Document, s paintedShading, x, y float64) ([]float64, bool) {
	t.Helper()
	v := core.View{Objects: doc.Objects, Trailer: &doc.Trailer, Limits: core.DefaultLimits(), Run: evalRun}
	u, w := s.ctm.inverse().apply(x, y)
	nums := func(key string) []float64 {
		arr, _ := doc.Resolve(s.sh.Get(object.Name(key))).(object.Array)
		out := make([]float64, len(arr))
		for i, o := range arr {
			switch n := doc.Resolve(o).(type) {
			case object.Integer:
				out[i] = float64(n)
			case object.Real:
				out[i] = float64(n)
			}
		}
		return out
	}
	fn := s.sh.Get("Function")
	var in []float64
	switch typ, _ := doc.Resolve(s.sh.Get("ShadingType")).(object.Integer); typ {
	case 1:
		d := nums("Domain")
		if u < d[0] || u > d[1] || w < d[2] || w > d[3] {
			return nil, false
		}
		in = []float64{u, w}
	case 2:
		c, d := nums("Coords"), nums("Domain")
		dx, dy := c[2]-c[0], c[3]-c[1]
		tt := ((u-c[0])*dx + (w-c[1])*dy) / (dx*dx + dy*dy)
		in = []float64{d[0] + math.Max(0, math.Min(1, tt))*(d[1]-d[0])}
	case 3:
		c, d := nums("Coords"), nums("Domain")
		if c[0] != 0 || c[1] != 0 || c[3] != 0 || c[4] != 0 {
			t.Fatalf("a radial shading whose circles are not concentric at the origin: %v", c)
		}
		tt := (math.Hypot(u, w) - c[2]) / (c[5] - c[2])
		in = []float64{d[0] + math.Max(0, math.Min(1, tt))*(d[1]-d[0])}
	default:
		t.Fatalf("a shading of type %v", typ)
	}
	fns, isArray := doc.Resolve(fn).(object.Array)
	if !isArray {
		fns = object.Array{fn}
	}
	var out []float64
	for _, f := range fns {
		o, ok := v.EvalFunction(f, in)
		if !ok {
			t.Fatalf("pdf0 cannot evaluate the shading's function at %v", in)
		}
		out = append(out, o...)
	}
	return out, true
}

// writtenColour is the colour and alpha the written page gives a point of
// layout's coordinates, from the one gradient shading painted there.
func writtenColour(t *testing.T, doc *pdf0.Document, painted []paintedShading, ps pageSpace, x, y float64) (rgb [3]float64, alpha float64) {
	t.Helper()
	p := ps.point(x, y)
	for _, s := range painted {
		if s.sh.Get("ColorSpace") != object.Name("DeviceRGB") {
			continue
		}
		c, ok := shadingValue(t, doc, s, p[0], p[1])
		if !ok {
			continue
		}
		copy(rgb[:], c)
		alpha = s.alpha
		if s.mask != nil {
			form, _ := doc.Resolve(s.mask.Get("G")).(*object.Stream)
			data, err := doc.StreamData(form)
			if err != nil {
				t.Fatal(err)
			}
			in := shadingsPainted(t, doc, data, doc.ResolveDict(form.Dict.Get("Resources")), s.maskSpace)
			if len(in) != 1 {
				t.Fatalf("the soft mask paints %d shadings; want one", len(in))
			}
			a, _ := shadingValue(t, doc, in[0], p[0], p[1])
			alpha *= a[0]
		}
		return rgb, alpha
	}
	t.Fatalf("no shading paints (%v, %v)", x, y)
	return
}

// smooth reports whether forme's gradient is continuous within d of a point
// of its tile, so that the question of its colour there does not turn on an
// edge.
func smooth(g layout.Gradient, x, y, d float64) bool {
	at := func(x, y float64) style.RGBA {
		px, _ := style.FromPx(x)
		py, _ := style.FromPx(y)
		return g.ColorAt(layout.Point{X: px, Y: py})
	}
	c := at(x, y)
	for _, o := range [][2]float64{{d, 0}, {-d, 0}, {0, d}, {0, -d}} {
		e := at(x+o[0], y+o[1])
		if math.Abs(e.R-c.R) > 40 || math.Abs(e.G-c.G) > 40 || math.Abs(e.B-c.B) > 40 || math.Abs(e.A-c.A) > 0.15 {
			return false
		}
	}
	return true
}

// TestAGradientIsItsColours reads every case's shading out of the written file
// and asks it for the colour and alpha at a grid of points of the first tile:
// each is forme's, to a millionth.
func TestAGradientIsItsColours(t *testing.T) {
	for _, tc := range gradientCases {
		t.Run(tc.name, func(t *testing.T) {
			in := gradientDoc(tc.background)
			c := layout.Compose(in, Options{})
			v := theGradient(t, c)
			doc, _ := roundTrip(t, in, Options{})
			page := doc.PageList()[0]
			painted := shadingsPainted(t, doc, contentOf(t, doc), doc.ResolveDict(page.Get("Resources")), identity)
			if len(painted) == 0 {
				t.Fatalf("the page paints no shading:\n%s", contentOf(t, doc))
			}
			ps := pageSpaceOf(c)
			tx, ty := v.Tile.X.Px(), v.Tile.Y.Px()
			checked := 0
			for y := 0.5; y < v.Tile.H.Px(); y += 1.25 {
				for x := 0.5; x < v.Tile.W.Px(); x += 1.25 {
					if !smooth(v.Gradient, x, y, 0.05) {
						continue
					}
					px, _ := style.FromPx(x)
					py, _ := style.FromPx(y)
					want := v.Gradient.ColorAt(layout.Point{X: px, Y: py})
					rgb, alpha := writtenColour(t, doc, painted, ps, tx+x, ty+y)
					checked++
					if math.Abs(alpha-want.A) > 1e-6 {
						t.Fatalf("at (%v, %v) the page's alpha is %v; forme's is %v", x, y, alpha, want.A)
					}
					if want.A < 1e-6 {
						continue
					}
					for i, w := range []float64{want.R / 255, want.G / 255, want.B / 255} {
						if math.Abs(rgb[i]-w) > 1e-6 {
							t.Fatalf("at (%v, %v) the page's colour is %v; forme's is %v", x, y, rgb, want)
						}
					}
				}
			}
			if checked < 300 {
				t.Fatalf("only %d points were checked", checked)
			}
		})
	}
}

// TestAGradientIsDrawnAsItsColours asks Ghostscript and Poppler for the colour
// at a grid of points away from any edge: forme's colour composited over the
// white page, to within what a renderer's own subdivision of a shading allows.
func TestAGradientIsDrawnAsItsColours(t *testing.T) {
	for _, tc := range gradientCases {
		t.Run(tc.name, func(t *testing.T) {
			in := gradientDoc(tc.background)
			c := layout.Compose(in, Options{})
			v := theGradient(t, c)
			doc, _ := roundTrip(t, in, Options{})
			tx, ty := v.Tile.X.Px(), v.Tile.Y.Px()
			// Every tile of the clip is the first one again.
			step := func(x, step float64) float64 { return math.Mod(x, step) }
			for _, r := range renderings(t, doc, c, 96) {
				// A conic gradient is a function-based shading, which a
				// renderer paints by subdividing the tile into patches it
				// takes to be smooth. Poppler subdivides finely enough to
				// be within a dozen levels of the function everywhere;
				// Ghostscript 10.02 paints the patches across a hard stop,
				// or across the rows about the centre where the angle turns
				// through every value, as bands of wrong colour (see
				// docs/htmlpdf.md), so it is not asked about one. The file
				// is held to the function exactly by
				// TestAGradientIsItsColours.
				tolerance := 8
				if v.Gradient.Kind == layout.ConicGradient {
					if r.name == "gs" {
						continue
					}
					tolerance = 12
				}
				bad, checked := 0, 0
				var first string
				for y := 1.0; y < v.Clip.H.Px()-1; y += 3 {
					for x := 1.0; x < v.Clip.W.Px()-1; x += 3 {
						// The renderer's colour is the pixel centre's.
						cx, cy := r.centre(v.Clip.X.Px()+x, v.Clip.Y.Px()+y)
						lx, ly := step(cx-tx, v.StepX.Px()), step(cy-ty, v.StepY.Px())
						if lx < 1.5 || ly < 1.5 || lx > v.Tile.W.Px()-1.5 || ly > v.Tile.H.Px()-1.5 ||
							!smooth(v.Gradient, lx, ly, 1.5) {
							continue
						}
						px, _ := style.FromPx(lx)
						py, _ := style.FromPx(ly)
						w := v.Gradient.ColorAt(layout.Point{X: px, Y: py})
						over := func(c float64) uint8 { return uint8(math.Round(c*w.A + 255*(1-w.A))) }
						want := color.RGBA{over(w.R), over(w.G), over(w.B), 255}
						got := r.at(v.Clip.X.Px()+x, v.Clip.Y.Px()+y)
						checked++
						if !near(got, want, tolerance) {
							bad++
							if first == "" {
								first = fmt.Sprintf("(%v, %v): %v, want %v", x, y, got, want)
							}
						}
					}
				}
				if checked < 100 {
					t.Fatalf("%s: only %d points were checked", r.name, checked)
				}
				// A renderer subdivides a shading to a smoothness of its own
				// choosing; one point in fifty may fall past the tolerance.
				if bad*50 > checked {
					t.Errorf("%s: %d of %d points are off; the first %s", r.name, bad, checked, first)
				}
			}
		})
	}
}

// TestAGradientTooFineToWriteIsRefused: a gradient's pieces are its stops
// times its periods over the tile, and past maxGradientPieces (or a conic
// one's maxConicPieces) it is refused, with the count in the reason.
func TestAGradientTooFineToWriteIsRefused(t *testing.T) {
	u := func(px float64) style.Unit { v, _ := style.FromPx(px); return v }
	stops := []layout.GradientStop{{Offset: 0, Color: style.RGBA{R: 255, A: 1}}, {Offset: 1e-5, Color: style.RGBA{B: 255, A: 1}, Exponent: 1}}
	tile := layout.Rect{W: u(100), H: u(100)}
	linear := layout.FillGradient{Clip: tile, Tile: tile, StepX: u(100), StepY: u(100), Gradient: layout.Gradient{
		Kind: layout.LinearGradient, Repeating: true, Start: layout.Point{}, End: layout.Point{X: u(100)}, Stops: stops}}
	if why := undrawable(linear); !strings.Contains(why, "pieces") {
		t.Errorf("a repeating gradient of 100,000 periods in its tile: refused %q", why)
	}
	linear.Gradient.Stops[1].Offset = 0.1
	if why := undrawable(linear); why != "" {
		t.Errorf("a repeating gradient of ten periods: refused %q", why)
	}
	conic := linear
	conic.Gradient.Kind = layout.ConicGradient
	conic.Gradient.Stops[1].Offset = 1.0 / 3000
	if why := undrawable(conic); !strings.Contains(why, "pieces") {
		t.Errorf("a repeating conic gradient of 3,000 periods: refused %q", why)
	}
	nan := linear
	nan.Gradient.Stops = []layout.GradientStop{{Offset: math.NaN(), Color: style.RGBA{A: 1}}}
	if why := undrawable(nan); why == "" {
		t.Error("a gradient with a stop at NaN is not refused")
	}
}
