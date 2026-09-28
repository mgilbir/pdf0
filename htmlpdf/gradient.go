package htmlpdf

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/style"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Gradients: forme's FillGradient as PDF shadings.
//
// forme states a gradient as its geometry in one tile and its colour as a
// function of an offset along the gradient line (layout.Gradient.ColorAtOffset):
// each stop's colour, and between two stops the blend the later one's Exponent
// gives, mixed in premultiplied alpha, in gamma-encoded sRGB. A gradient CSS
// interpolates in another colour space arrives already restated as sRGB stops
// within 0.4/255 of it (forme's gradientspace.go), so that function is the
// whole of what is drawn, and it is drawn exactly:
//
//   - The geometry is a shading (ISO 32000-2 8.7.4.5): a linear gradient an
//     axial one (type 2) along its gradient line; a radial one a radial one
//     (type 3) of circles about its centre, drawn under a matrix that scales
//     the vertical axis by RadiusY/RadiusX, since CSS's ending shape is an
//     ellipse and PDF's circles are circles; a conic one a function-based
//     shading (type 1) whose function is the angle about its centre.
//   - The colour is a function of the offset (7.10). The offsets the tile
//     reaches are cut at every stop, and at every period of a repeating
//     gradient, into pieces, each a blend between two stops over some
//     fraction of the way from one to the other. A stitching function (type
//     3) joins them, its /Encode giving each piece that fraction, and each
//     piece is an exponential function (type 2) whose /N is the exponent —
//     exactly the blend p^Exponent. A conic gradient's function is one
//     PostScript calculator function (type 4), which has atan, choosing its
//     piece by comparisons and blending the same way.
//   - Premultiplication matters only between two stops whose alphas differ,
//     and there the colour is (Ca·Aa·(1−w) + Cb·Ab·w) / (Aa·(1−w) + Ab·w): a
//     type 4 function computes it exactly. Where one of the two is transparent
//     the colour is the other's, constant, and where both alphas agree it is
//     the plain blend. The alpha is a separate function, the plain blend of the
//     two alphas, painted as a luminosity soft mask of the same shading in
//     DeviceGray (11.6.5.2); a gradient whose stops share one alpha has a
//     constant alpha instead, /ca, and none when it is opaque.
//
// A gradient is painted in each tile of a FillGradient: once, clipped to its
// tile, where there is one tile, and as the cell of a tiling pattern where
// there are more, as a tiled picture is.

// maxGradientPieces bounds the pieces one gradient's stitching function joins.
// forme restates a gradient in another colour space as up to 16,384 stops
// (gradientspace.go's maxInterpolatedStops) and lets a repeating one repeat up
// to 65,536 times in a tile (gradientparse.go's maxGradientRepeats); the pieces
// are the stops times the repeats, so this is what bounds the file, and a
// gradient past it is refused rather than written short.
const maxGradientPieces = 1 << 16

// maxConicPieces bounds a conic gradient's pieces, each of which is a branch
// of a PostScript calculator program a reader runs per device pixel. A
// program of a few hundred kilobytes is already past what a reader should be
// handed for a gradient; this admits every conic gradient written with the
// thousand stops forme allows in sRGB.
const maxConicPieces = 4096

// gradientPiece is one piece of a gradient line: over the offsets [lo, hi],
// the blend from colour a to colour b at the fractions p0 to p1 of the way
// between them, shaped by exponent e. A constant piece has a == b.
type gradientPiece struct {
	lo, hi float64
	a, b   style.RGBA
	e      float64
	p0, p1 float64
}

// gradientPlan is what a FillGradient is drawn from: the offsets its tile
// reaches and the pieces of the gradient line over them.
type gradientPlan struct {
	g      layout.Gradient
	lo, hi float64
	pieces []gradientPiece
	// alpha is the gradient's one alpha, where every stop has it, and
	// uniform says whether there is one.
	alpha   float64
	uniform bool
}

// planGradient works out what a FillGradient draws, or why it cannot be.
// ok is false, with an empty reason, for a gradient that paints nothing.
func planGradient(v layout.FillGradient) (plan gradientPlan, why string, ok bool) {
	g := v.Gradient
	plan.g = g
	if len(g.Stops) == 0 || v.Tile.Empty() || v.Clip.Empty() {
		return plan, "", false
	}
	for _, s := range g.Stops {
		for _, x := range []float64{s.Offset, s.Color.R, s.Color.G, s.Color.B, s.Color.A} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return plan, "a gradient with a colour stop that is not a number", false
			}
		}
	}
	w, h := v.Tile.W.Px(), v.Tile.H.Px()
	switch g.Kind {
	case layout.LinearGradient:
		sx, sy := g.Start.X.Px(), g.Start.Y.Px()
		ex, ey := g.End.X.Px()-sx, g.End.Y.Px()-sy
		d := ex*ex + ey*ey
		if !(d > 0) {
			return plan, "a linear gradient whose line has no length", false
		}
		plan.lo, plan.hi = math.Inf(1), math.Inf(-1)
		for _, c := range [4][2]float64{{0, 0}, {w, 0}, {0, h}, {w, h}} {
			t := ((c[0]-sx)*ex + (c[1]-sy)*ey) / d
			plan.lo, plan.hi = math.Min(plan.lo, t), math.Max(plan.hi, t)
		}
	case layout.RadialGradient:
		rx, ry := g.RadiusX.Px(), g.RadiusY.Px()
		if !(rx > 0) || !(ry > 0) {
			return plan, "a radial gradient whose ending shape has no size", false
		}
		cx, cy := g.Center.X.Px()/rx, g.Center.Y.Px()/ry
		W, H := w/rx, h/ry
		// The nearest and farthest the tile comes to the centre, measured
		// in the ending shape's own proportions.
		nx := math.Max(0, math.Max(-cx, cx-W))
		ny := math.Max(0, math.Max(-cy, cy-H))
		plan.lo = math.Hypot(nx, ny)
		fx := math.Max(math.Abs(cx), math.Abs(cx-W))
		fy := math.Max(math.Abs(cy), math.Abs(cy-H))
		plan.hi = math.Hypot(fx, fy)
	case layout.ConicGradient:
		if math.IsNaN(g.FromAngle) || math.IsInf(g.FromAngle, 0) {
			return plan, "a conic gradient whose starting angle is not a number", false
		}
		plan.lo, plan.hi = 0, 1
	default:
		return plan, fmt.Sprintf("a gradient of a kind (%d) this backend does not know", g.Kind), false
	}
	if math.IsNaN(plan.lo) || math.IsNaN(plan.hi) || math.IsInf(plan.lo, 0) || math.IsInf(plan.hi, 0) {
		return plan, "a gradient whose geometry is not a number", false
	}
	limit := maxGradientPieces
	if g.Kind == layout.ConicGradient {
		limit = maxConicPieces
	}
	if n := countPieces(g, plan.lo, plan.hi); n > float64(limit) {
		return plan, fmt.Sprintf("a gradient that takes %.0f pieces to state over its tile, past the %d "+
			"this backend writes one as", n, limit), false
	}
	plan.pieces = gradientPieces(g, plan.lo, plan.hi)
	plan.alpha, plan.uniform = g.Stops[0].Color.A, true
	for _, s := range g.Stops {
		if s.Color.A != plan.alpha {
			plan.uniform = false
		}
	}
	if plan.uniform && !(plan.alpha > 0) {
		return plan, "", false // transparent throughout: nothing is painted
	}
	return plan, "", true
}

// countPieces is an upper bound on the pieces gradientPieces makes, worked out
// without making them.
func countPieces(g layout.Gradient, lo, hi float64) float64 {
	n := float64(len(g.Stops) + 1)
	first, last := g.Stops[0].Offset, g.Stops[len(g.Stops)-1].Offset
	if g.Repeating && last > first {
		return n * (math.Floor((hi-first)/(last-first)) - math.Floor((lo-first)/(last-first)) + 1)
	}
	return n
}

// gradientPieces cuts the gradient line over [lo, hi] into pieces, at every
// stop and, for a repeating gradient, at every period. Each piece is the blend
// ColorAtOffset states for the offsets in it; a piece of no length — two stops
// at one offset, which is a hard change of colour — is left out, and the later
// piece begins at the offset, as ColorAtOffset's later colour does and as a
// stitching function's later function does at its bound.
func gradientPieces(g layout.Gradient, lo, hi float64) []gradientPiece {
	stops := g.Stops
	n := len(stops)
	first, last := stops[0].Offset, stops[n-1].Offset
	period, repeating := last-first, g.Repeating && last > first
	var out []gradientPiece
	// cut adds the pieces over [x, y], an interval of the line whose
	// colours are the unrepeated gradient's at [x-shift, y-shift].
	cut := func(x, y, shift float64) {
		breaks := []float64{x}
		for _, s := range stops {
			if o := s.Offset + shift; o > x && o < y {
				breaks = append(breaks, o)
			}
		}
		breaks = append(breaks, y)
		for i := 0; i+1 < len(breaks); i++ {
			a, b := breaks[i], breaks[i+1]
			if !(b > a) {
				continue
			}
			mid := (a+b)/2 - shift
			j := sort.Search(n, func(k int) bool { return stops[k].Offset > mid })
			p := gradientPiece{lo: a, hi: b, e: 1, p0: 0, p1: 1}
			switch j {
			case 0:
				p.a, p.b = stops[0].Color, stops[0].Color
			case n:
				p.a, p.b = stops[n-1].Color, stops[n-1].Color
			default:
				s0, s1 := stops[j-1], stops[j]
				span := s1.Offset - s0.Offset
				p.a, p.b = s0.Color, s1.Color
				p.p0 = fraction((a - shift - s0.Offset) / span)
				p.p1 = fraction((b - shift - s0.Offset) / span)
				if e := s1.Exponent; e > 0 && !math.IsInf(e, 0) {
					p.e = e
				}
			}
			out = append(out, p)
		}
	}
	if !repeating {
		cut(lo, hi, 0)
		return out
	}
	for k := math.Floor((lo - first) / period); ; k++ {
		start := first + k*period
		if start >= hi {
			break
		}
		cut(math.Max(lo, start), math.Min(hi, start+period), k*period)
	}
	return out
}

// fraction is a fraction of the way between two stops as a piece states it:
// in [0, 1], and exactly 0 or 1 where the arithmetic of a shifted period left
// it a rounding error away. A type 2 function's domain is [0, 1], and an
// /Encode reaching past it by 4·10⁻¹⁶ is out of range for a reader that
// checks (Ghostscript 10.02 then paints nothing at all).
func fraction(p float64) float64 {
	const snap = 1e-9
	switch {
	case p < snap:
		return 0
	case p > 1-snap:
		return 1
	}
	return p
}

// shadingWriter writes a gradient's shading and whatever it needs into a
// content stream and the resources that stream names.
type shadingWriter struct {
	doc      *pdf0.Document
	shadings map[object.Name]object.Object
	states   map[object.Name]object.Object
	alphas   *alphaStates
	// functions are the pieces' functions written so far, one object per
	// distinct piece, which a repeating gradient names once per period.
	functions map[string]object.IndirectRef
	// transparent is told when the gradient needs transparency.
	transparent func()
}

// paint draws the gradient into b, in coordinates whose origin is the tile's
// top left, clipped to the tile.
func (s *shadingWriter) paint(b *content.Builder, plan gradientPlan, w, h float64) error {
	g := plan.g
	var colours []object.Name
	for _, sh := range s.shading(plan, false, w, h) {
		name := object.Name(fmt.Sprintf("Sh%d", len(s.shadings)+1))
		s.shadings[name] = sh
		colours = append(colours, name)
	}
	b.Save()
	b.Rect(0, 0, w, h)
	b.Clip()
	b.EndPath()
	if plan.uniform {
		s.alphas.use(b, plan.alpha)
	} else {
		mask, err := s.mask(plan, w, h)
		if err != nil {
			return err
		}
		b.SetExtGState(mask)
		s.transparent()
	}
	if g.Kind == layout.RadialGradient {
		b.Concat(1, 0, 0, g.RadiusY.Px()/g.RadiusX.Px(), g.Center.X.Px(), g.Center.Y.Px())
	}
	for _, name := range colours {
		b.Shading(name)
	}
	b.Restore()
	return nil
}

// mask is the luminosity soft mask that gives a gradient whose stops' alphas
// differ its alpha: the same shading, of the alphas, in DeviceGray, over a
// black backdrop, so that the tile outside it hides what is painted.
func (s *shadingWriter) mask(plan gradientPlan, w, h float64) (object.Name, error) {
	alphas := map[object.Name]object.Object{}
	mb := &content.Builder{}
	mb.Save()
	if plan.g.Kind == layout.RadialGradient {
		g := plan.g
		mb.Concat(1, 0, 0, g.RadiusY.Px()/g.RadiusX.Px(), g.Center.X.Px(), g.Center.Y.Px())
	}
	for i, sh := range s.shading(plan, true, w, h) {
		name := object.Name(fmt.Sprintf("A%d", i+1))
		alphas[name] = sh
		mb.Shading(name)
	}
	mb.Restore()
	form, err := grayGroup(s.doc, mb, [4]float64{0, 0, w, h}, alphas)
	if err != nil {
		return "", fmt.Errorf("writing a gradient's soft mask: %w", err)
	}
	gs, err := s.doc.LuminositySoftMask(form, nil)
	if err != nil {
		return "", fmt.Errorf("writing a gradient's soft mask: %w", err)
	}
	name := object.Name(fmt.Sprintf("SM%d", len(s.states)+1))
	s.states[name] = gs
	return name, nil
}

// grayGroup writes a form XObject that is a transparency group blending in
// DeviceGray, drawn by b with the shadings named.
//
// It is what a luminosity soft mask is taken from. The mask is the group's
// luminosity, and a group in DeviceGray has its gray as its luminosity with no
// conversion (11.6.5.2); in DeviceRGB — pdf0.AddForm's group — a renderer that
// manages colour takes the gray through its gray and RGB profiles first, and
// Ghostscript then masks an alpha of 0.5 by some 0.47.
func grayGroup(doc *pdf0.Document, b *content.Builder, bbox [4]float64, shadings map[object.Name]object.Object) (object.IndirectRef, error) {
	drawn, err := b.Bytes()
	if err != nil {
		return object.IndirectRef{}, err
	}
	compressed := core.FlateEncode(drawn)
	st := object.NewStream(nil, compressed)
	st.Dict.Set("Type", object.Name("XObject"))
	st.Dict.Set("Subtype", object.Name("Form"))
	st.Dict.Set("BBox", object.Array{numberOf(bbox[0]), numberOf(bbox[1]), numberOf(bbox[2]), numberOf(bbox[3])})
	group := &object.Dictionary{}
	group.Set("Type", object.Name("Group"))
	group.Set("S", object.Name("Transparency"))
	group.Set("CS", object.Name("DeviceGray"))
	group.Set("I", object.Boolean(true))
	st.Dict.Set("Group", group)
	res := &object.Dictionary{}
	res.Set("Shading", resourceDict(shadings))
	st.Dict.Set("Resources", res)
	st.Dict.Set("Filter", object.Name("FlateDecode"))
	st.Dict.Set("Length", object.Integer(len(compressed)))
	return doc.Add(st), nil
}

// shading writes the shading of a gradient's colours, or of its alphas, over
// a tile w by h.
func (s *shadingWriter) shading(plan gradientPlan, alpha bool, w, h float64) []object.Object {
	g := plan.g
	sh := &object.Dictionary{}
	if alpha {
		sh.Set("ColorSpace", object.Name("DeviceGray"))
	} else {
		sh.Set("ColorSpace", object.Name("DeviceRGB"))
	}
	switch g.Kind {
	case layout.LinearGradient:
		sx, sy := g.Start.X.Px(), g.Start.Y.Px()
		ex, ey := g.End.X.Px()-sx, g.End.Y.Px()-sy
		sh.Set("ShadingType", object.Integer(2)) // axial
		sh.Set("Coords", object.Array{
			numberOf(sx + plan.lo*ex), numberOf(sy + plan.lo*ey),
			numberOf(sx + plan.hi*ex), numberOf(sy + plan.hi*ey),
		})
		sh.Set("Domain", object.Array{numberOf(plan.lo), numberOf(plan.hi)})
		sh.Set("Function", s.stitching(plan, alpha))
		sh.Set("Extend", object.Array{object.Boolean(true), object.Boolean(true)})
	case layout.RadialGradient:
		rx := g.RadiusX.Px()
		sh.Set("ShadingType", object.Integer(3)) // radial
		sh.Set("Coords", object.Array{
			object.Integer(0), object.Integer(0), numberOf(plan.lo * rx),
			object.Integer(0), object.Integer(0), numberOf(plan.hi * rx),
		})
		sh.Set("Domain", object.Array{numberOf(plan.lo), numberOf(plan.hi)})
		sh.Set("Function", s.stitching(plan, alpha))
		sh.Set("Extend", object.Array{object.Boolean(true), object.Boolean(true)})
	case layout.ConicGradient:
		// Function-based: the function is of the point, over the tile, in
		// the tile's own coordinates (the shading's /Matrix is the identity).
		domain := object.Array{object.Integer(0), numberOf(w), object.Integer(0), numberOf(h)}
		// One function per component (8.7.4.5.2 allows an array of n 1-out
		// functions): Ghostscript 10.02 paints nothing for a type 1 shading
		// whose one function has three outputs.
		n := 3
		if alpha {
			n = 1
		}
		fns := make(object.Array, n)
		for i := range fns {
			fns[i] = s.conicFunction(plan, alpha, i, domain)
		}
		sh.Set("ShadingType", object.Integer(1))
		sh.Set("Domain", domain)
		// Stated, though it is the default: Ghostscript 10.02 paints nothing
		// for a type 1 shading with no /Matrix.
		sh.Set("Matrix", object.Array{object.Integer(1), object.Integer(0), object.Integer(0),
			object.Integer(1), object.Integer(0), object.Integer(0)})
		sh.Set("Function", fns)
	}
	return []object.Object{s.doc.Add(sh)}
}

// stitching is the type 3 function over [lo, hi] joining the pieces'
// functions.
func (s *shadingWriter) stitching(plan gradientPlan, alpha bool) object.Object {
	fns := make(object.Array, 0, len(plan.pieces))
	bounds := make(object.Array, 0, len(plan.pieces))
	encode := make(object.Array, 0, 2*len(plan.pieces))
	for i, p := range plan.pieces {
		fns = append(fns, s.pieceFunction(p, alpha))
		if i > 0 {
			bounds = append(bounds, numberOf(p.lo))
		}
		encode = append(encode, numberOf(p.p0), numberOf(p.p1))
	}
	f := &object.Dictionary{}
	f.Set("FunctionType", object.Integer(3))
	f.Set("Domain", object.Array{numberOf(plan.lo), numberOf(plan.hi)})
	f.Set("Functions", fns)
	f.Set("Bounds", bounds)
	f.Set("Encode", encode)
	return s.doc.Add(f)
}

// pieceFunction is one piece's function of the fraction p of the way from
// one stop to the next: its colour, or its alpha.
func (s *shadingWriter) pieceFunction(p gradientPiece, alpha bool) object.IndirectRef {
	key := fmt.Sprintf("%v|%v|%v|%v", alpha, p.a, p.b, p.e)
	if ref, ok := s.functions[key]; ok {
		return ref
	}
	var f object.Object
	switch {
	case alpha:
		f = exponential([]float64{p.a.A}, []float64{p.b.A}, p.e)
	case p.a.A == p.b.A:
		f = exponential(rgbOf(p.a), rgbOf(p.b), p.e)
	case !(p.a.A > 0):
		f = exponential(rgbOf(p.b), rgbOf(p.b), 1)
	case !(p.b.A > 0):
		f = exponential(rgbOf(p.a), rgbOf(p.a), 1)
	default:
		prog := "{" + premultipliedBlend(p) + "}"
		st := object.NewStream(nil, []byte(prog))
		st.Dict.Set("FunctionType", object.Integer(4))
		st.Dict.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
		st.Dict.Set("Range", unitRange(3))
		st.Dict.Set("Length", object.Integer(len(prog)))
		f = st
	}
	ref := s.doc.Add(f)
	s.functions[key] = ref
	return ref
}

// exponential is a type 2 function from c0 to c1 with exponent n, over [0, 1].
func exponential(c0, c1 []float64, n float64) *object.Dictionary {
	f := &object.Dictionary{}
	f.Set("FunctionType", object.Integer(2))
	f.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	a, b := make(object.Array, len(c0)), make(object.Array, len(c1))
	for i := range c0 {
		a[i], b[i] = numberOf(c0[i]), numberOf(c1[i])
	}
	f.Set("C0", a)
	f.Set("C1", b)
	f.Set("N", numberOf(n))
	return f
}

func rgbOf(c style.RGBA) []float64 { return []float64{c.R / 255, c.G / 255, c.B / 255} }

func unitRange(n int) object.Array {
	out := make(object.Array, 0, 2*n)
	for i := 0; i < n; i++ {
		out = append(out, object.Integer(0), object.Integer(1))
	}
	return out
}

// ps writes a number as a PostScript calculator function reads one: no
// exponent, which the calculator's syntax does not have, and in at most
// maxPSNumber characters, since Ghostscript 10.02 refuses a longer number in
// a type 4 function and paints nothing for the shading. Fifteen characters
// keep at least twelve significant digits of every number written here —
// coordinates in pixels, fractions and exponents — which is a millionth of an
// 8-bit step.
func ps(v float64) string {
	const maxPSNumber = 15
	s := strconv.FormatFloat(v, 'f', -1, 64)
	for prec := maxPSNumber; len(s) > maxPSNumber && prec >= 0; prec-- {
		s = strconv.FormatFloat(v, 'f', prec, 64)
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "" || s == "-0" || s == "-" {
		return "0"
	}
	return s
}

// premultipliedBlend is the PostScript that turns p, on the stack, into the
// premultiplied blend of a piece's two colours at w = p^e:
//
//	(Ca·Aa + w·(Cb·Ab − Ca·Aa)) / (Aa + w·(Ab − Aa))
//
// for each of r, g and b. It is used where both alphas are above zero and
// differ, so the denominator is too.
func premultipliedBlend(p gradientPiece) string {
	var b strings.Builder
	if p.e != 1 {
		b.WriteString(ps(p.e) + " exp ")
	}
	aa, ab := p.a.A, p.b.A
	b.WriteString("dup " + ps(ab-aa) + " mul " + ps(aa) + " add ") // w A
	ca, cb := rgbOf(p.a), rgbOf(p.b)
	for i := range ca {
		pa := ca[i] * aa
		d := cb[i]*ab - pa
		// w A [r [g]]: the w and the A are i+1 below the top.
		idx := strconv.Itoa(i + 1)
		b.WriteString(idx + " index " + ps(d) + " mul " + ps(pa) + " add " + idx + " index div ")
	}
	b.WriteString("5 -2 roll pop pop")
	return b.String()
}

// conicFunction is the type 4 function of a point that a conic gradient's
// function-based shading paints: the angle of the ray from the centre through
// it, clockwise from straight up in coordinates whose y grows down the page,
// taken from FromAngle as a fraction of a turn in [0, 1), and the colour or
// alpha of the gradient there.
func (s *shadingWriter) conicFunction(plan gradientPlan, alpha bool, channel int, domain object.Array) object.IndirectRef {
	g := plan.g
	var b strings.Builder
	b.WriteString("{ ")
	// x y -> (x - cx) (cy - y): the ray's direction, with up positive.
	b.WriteString(ps(g.Center.Y.Px()) + " exch sub exch " + ps(g.Center.X.Px()) + " sub exch ")
	// atan's num is the clockwise component (dx) and den the upward one, so
	// its angle is clockwise from up, in [0, 360). At the centre itself,
	// where both are nothing, atan has no answer (PostScript's is an error),
	// and 0 is given: the centre is one point, and has no area to paint.
	b.WriteString("2 copy abs exch abs add 0 eq { pop pop 0 } { atan } ifelse ")
	b.WriteString(ps(g.FromAngle) + " sub 360 div dup floor sub ")
	conicSelect(&b, plan.pieces, 0, len(plan.pieces)-1, alpha, channel)
	b.WriteString(" }")
	prog := b.String()
	st := object.NewStream(nil, []byte(prog))
	st.Dict.Set("FunctionType", object.Integer(4))
	st.Dict.Set("Domain", domain)
	st.Dict.Set("Range", unitRange(1))
	st.Dict.Set("Length", object.Integer(len(prog)))
	return s.doc.Add(st)
}

// conicSelect writes the choice among pieces[l..r] of the one t, on the stack,
// falls in, by halving: a piece begins at its lo, so t belongs to the last
// piece whose lo is at or below it. Nesting is the logarithm of the pieces.
func conicSelect(b *strings.Builder, pieces []gradientPiece, l, r int, alpha bool, channel int) {
	if l == r {
		p := pieces[l]
		// t -> p, the fraction of the way between the piece's stops.
		scale := (p.p1 - p.p0) / (p.hi - p.lo)
		b.WriteString(ps(p.lo) + " sub " + ps(scale) + " mul " + ps(p.p0) + " add ")
		one := func(c style.RGBA) []float64 { return rgbOf(c)[channel : channel+1] }
		switch {
		case alpha:
			writeBlend(b, []float64{p.a.A}, []float64{p.b.A}, p.e)
		case p.a.A == p.b.A:
			writeBlend(b, one(p.a), one(p.b), p.e)
		case !(p.a.A > 0):
			writeBlend(b, one(p.b), one(p.b), 1)
		case !(p.b.A > 0):
			writeBlend(b, one(p.a), one(p.a), 1)
		default:
			b.WriteString(premultipliedChannel(p, channel))
		}
		return
	}
	m := (l + r + 1) / 2
	b.WriteString("dup " + ps(pieces[m].lo) + " lt { ")
	conicSelect(b, pieces, l, m-1, alpha, channel)
	b.WriteString(" } { ")
	conicSelect(b, pieces, m, r, alpha, channel)
	b.WriteString(" } ifelse")
}

// premultipliedChannel is premultipliedBlend for one component.
func premultipliedChannel(p gradientPiece, channel int) string {
	var b strings.Builder
	if p.e != 1 {
		b.WriteString(ps(p.e) + " exp ")
	}
	aa, ab := p.a.A, p.b.A
	pa := rgbOf(p.a)[channel] * aa
	d := rgbOf(p.b)[channel]*ab - pa
	// w -> (pa + w·d) / (aa + w·(ab − aa))
	b.WriteString("dup " + ps(d) + " mul " + ps(pa) + " add exch " + ps(ab-aa) + " mul " + ps(aa) + " add div")
	return b.String()
}

// writeBlend writes the PostScript that turns p, on the stack, into the plain
// blend c0 + p^e·(c1 − c0), as a type 2 function does.
func writeBlend(b *strings.Builder, c0, c1 []float64, e float64) {
	constant := true
	for i := range c0 {
		if c0[i] != c1[i] {
			constant = false
		}
	}
	if constant {
		b.WriteString("pop")
		for _, c := range c0 {
			b.WriteString(" " + ps(c))
		}
		return
	}
	if e != 1 {
		b.WriteString(ps(e) + " exp ")
	}
	// w -> c0 + w·d for each component, then the w dropped.
	for i := range c0 {
		idx := strconv.Itoa(i)
		b.WriteString(idx + " index " + ps(c1[i]-c0[i]) + " mul " + ps(c0[i]) + " add ")
	}
	b.WriteString(strconv.Itoa(len(c0)+1) + " -1 roll pop")
}

// fillGradient paints a gradient across its clip, tile by tile.
func (c *canvas) fillGradient(v layout.FillGradient) error {
	plan, _, ok := planGradient(v)
	if !ok {
		return nil // nothing to paint, or refused by checkDrawable
	}
	cols, rows := v.Tiles()
	if cols <= 0 || rows <= 0 {
		return nil
	}
	b := c.b
	b.Save()
	b.Rect(v.Clip.X.Px(), v.Clip.Y.Px(), v.Clip.W.Px(), v.Clip.H.Px())
	b.Clip()
	b.EndPath()
	w, h := v.Tile.W.Px(), v.Tile.H.Px()
	if cols == 1 && rows == 1 {
		sw := c.shadingWriter()
		b.Translate(v.Tile.X.Px(), v.Tile.Y.Px())
		if err := sw.paint(b, plan, w, h); err != nil {
			return err
		}
	} else {
		// The cell of a tiling pattern, which carries its own resources.
		cell := &content.Builder{}
		sw := &shadingWriter{
			doc:       c.w.doc,
			shadings:  map[object.Name]object.Object{},
			states:    map[object.Name]object.Object{},
			functions: c.functions(),
			transparent: func() {
				c.w.transparent = true
			},
		}
		sw.alphas = newAlphaStates()
		sw.alphas.states = sw.states
		sw.alphas.onUse = sw.transparent
		cell.Save()
		cell.Translate(v.Tile.X.Px(), v.Tile.Y.Px())
		if err := sw.paint(cell, plan, w, h); err != nil {
			return err
		}
		cell.Restore()
		res := &object.Dictionary{}
		if len(sw.shadings) > 0 {
			res.Set("Shading", resourceDict(sw.shadings))
		}
		if len(sw.states) > 0 {
			res.Set("ExtGState", resourceDict(sw.states))
		}
		pattern, err := tiling(c.w.doc, cell, res, v.Tile, v.StepX.Px(), v.StepY.Px(), c.base)
		if err != nil {
			return err
		}
		pname := object.Name(fmt.Sprintf("Pt%d", len(c.patterns)+1))
		c.patterns[pname] = pattern
		b.SetColorSpace("Pattern")
		b.SetPattern(pname)
		b.Rect(v.Clip.X.Px(), v.Clip.Y.Px(), v.Clip.W.Px(), v.Clip.H.Px())
		b.Fill()
	}
	b.Restore()
	return nil
}

// shadingWriter is a writer of shadings into this stream's own resources.
func (c *canvas) shadingWriter() *shadingWriter {
	return &shadingWriter{
		doc:         c.w.doc,
		shadings:    c.shadings,
		states:      c.states,
		alphas:      c.alphas,
		functions:   c.functions(),
		transparent: func() { c.w.transparent = true },
	}
}

// functions is the page's record of the gradient functions it has written.
func (c *canvas) functions() map[string]object.IndirectRef {
	if c.w.functions == nil {
		c.w.functions = map[string]object.IndirectRef{}
	}
	return c.w.functions
}

// resourceDict is a resource subdictionary of names.
func resourceDict(m map[object.Name]object.Object) *object.Dictionary {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, string(n))
	}
	sort.Strings(names)
	d := &object.Dictionary{}
	for _, n := range names {
		d.Set(object.Name(n), m[object.Name(n)])
	}
	return d
}
