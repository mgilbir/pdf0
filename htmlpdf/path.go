package htmlpdf

import (
	"fmt"
	"math"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/pdf0/content"
)

// Shapes that are not rectangles: forme's rounded corners.
//
// A layout.Path is straight lines and arcs of axis-aligned ellipses, a
// sequence of closed shapes filled by the even-odd rule (CSS Backgrounds 3
// §4, forme's path.go). A PDF path is straight lines and cubic Bézier curves,
// filled by the even-odd rule with "f*" and clipped by it with "W* n" (ISO
// 32000-2 8.5.3.3.3, 8.5.4), so everything but the arcs is written as it is.
//
// # The one approximation
//
// PDF has no arc operator, and no cubic Bézier is an arc of an ellipse. An arc
// is written as cubic pieces of at most maxArcPiece degrees each, the standard
// construction that meets the ellipse at both ends of the piece with the
// ellipse's own tangents there (control points at 4/3·tan(θ/4) of the radii
// along them). The curve strays from a circle of radius r by a distance that
// grows as the sixth power of the angle: 2.7·10⁻⁴ r for a quarter circle, and
// 4.2·10⁻⁶ r for the 45° pieces written here — under a hundredth of a pixel
// for any radius under 2,300 px. An ellipse is a circle scaled on one axis, so
// its error is bounded by the larger radius's. forme states this as the backend's
// approximation and leaves the backend to choose it; TestArcsAreWithinTheirBound
// holds it.

// maxArcPiece is the largest arc, in degrees, one cubic Bézier is written for.
const maxArcPiece = 45

// maxArcSweep bounds one arc's sweep. forme's arcs are quarter ellipses and
// pieces of one; a sweep past a whole turn traces the ellipse again, which the
// even-odd rule counts, so it would have to be written as many times — and a
// number of turns is a number a hostile document could make large.
const maxArcSweep = 360

// pathUndrawable is why a path cannot be written, or "".
func pathUndrawable(p layout.Path) string {
	for _, s := range p {
		if s.Op != layout.ArcTo {
			continue
		}
		if math.IsNaN(s.StartAngle) || math.IsInf(s.StartAngle, 0) ||
			math.IsNaN(s.SweepAngle) || math.IsInf(s.SweepAngle, 0) {
			return fmt.Sprintf("an arc from %g through %g degrees, which is no arc", s.StartAngle, s.SweepAngle)
		}
		if math.Abs(s.SweepAngle) > maxArcSweep {
			return fmt.Sprintf("an arc sweeping %g degrees, past the whole turn a path of rounded corners "+
				"has any use for", s.SweepAngle)
		}
	}
	return ""
}

// pathTo appends a display-list path to the builder's current path and
// reports whether it drew anything: a path of no segments is no path.
//
// Every shape is closed, as forme's are: by its ClosePath, by the MoveTo that
// begins the next one, or at the end.
func pathTo(b *content.Builder, p layout.Path) bool {
	var (
		open           bool
		sx, sy, cx, cy float64 // where the shape began, and the current point
		drawn          bool
	)
	begin := func(x, y float64) {
		b.MoveTo(x, y)
		open, drawn = true, true
		sx, sy, cx, cy = x, y, x, y
	}
	closeShape := func() {
		if open {
			b.ClosePath()
			open = false
			cx, cy = sx, sy
		}
	}
	for _, s := range p {
		switch s.Op {
		case layout.MoveTo:
			closeShape()
			begin(s.Point.X.Px(), s.Point.Y.Px())
		case layout.LineTo:
			if !open {
				begin(cx, cy)
			}
			cx, cy = s.Point.X.Px(), s.Point.Y.Px()
			b.LineTo(cx, cy)
		case layout.ArcTo:
			x, y := arcPoint(s, s.StartAngle)
			switch {
			case !open:
				begin(x, y)
			case x != cx || y != cy:
				// The current point is not where the arc starts: a straight
				// line joins them first, as forme's ArcTo says.
				b.LineTo(x, y)
			}
			cx, cy = arcTo(b, s)
		case layout.ClosePath:
			closeShape()
		}
	}
	closeShape()
	return drawn
}

// arcPoint is the point of an arc's ellipse at an angle in degrees, in layout
// pixels: y grows down the page, so a growing angle goes round clockwise as
// the page is seen.
func arcPoint(s layout.PathSegment, deg float64) (x, y float64) {
	a := deg * math.Pi / 180
	return s.Center.X.Px() + s.RadiusX.Px()*math.Cos(a), s.Center.Y.Px() + s.RadiusY.Px()*math.Sin(a)
}

// arcTo writes an arc, whose start is the current point, as cubic Béziers of
// at most maxArcPiece degrees, and returns its end.
func arcTo(b *content.Builder, s layout.PathSegment) (x, y float64) {
	n := int(math.Ceil(math.Abs(s.SweepAngle) / maxArcPiece))
	if n == 0 {
		return arcPoint(s, s.StartAngle)
	}
	rx, ry := s.RadiusX.Px(), s.RadiusY.Px()
	cx, cy := s.Center.X.Px(), s.Center.Y.Px()
	step := s.SweepAngle / float64(n) * math.Pi / 180
	// The tangent lengths, as a fraction of the radii: 4/3·tan(θ/4). A
	// negative step goes the other way round, and so do its tangents.
	k := 4.0 / 3.0 * math.Tan(step/4)
	a0 := s.StartAngle * math.Pi / 180
	for i := 0; i < n; i++ {
		a1 := a0 + step
		if i == n-1 {
			a1 = (s.StartAngle + s.SweepAngle) * math.Pi / 180 // no drift at the end
		}
		c0, s0 := math.Cos(a0), math.Sin(a0)
		c1, s1 := math.Cos(a1), math.Sin(a1)
		x0, y0 := cx+rx*c0, cy+ry*s0
		x, y = cx+rx*c1, cy+ry*s1
		b.CurveTo(
			x0-k*rx*s0, y0+k*ry*c0,
			x+k*rx*s1, y-k*ry*c1,
			x, y)
		a0 = a1
	}
	return x, y
}

// fillPath paints a shape in a solid colour by the even-odd rule.
func (c *canvas) fillPath(v layout.FillPath) {
	if !(v.Color.A > 0) || len(v.Path) == 0 {
		return // no ink, or no shape
	}
	b := c.b
	b.Save()
	clipTo(b, v.Clip)
	c.alphas.use(b, v.Color.A)
	b.SetRGB(v.Color.R/255, v.Color.G/255, v.Color.B/255)
	if pathTo(b, v.Path) {
		b.FillEvenOdd()
	}
	b.Restore()
}

// clipPath draws operations clipped to a shape by the even-odd rule.
//
// The clip is set immediately after the Save and taken away by the Restore
// with the rest of the graphics state, as clipTo's is, so it cannot outlive
// what it holds. A shape of no segments clips everything away, and what it
// holds is not drawn.
func (c *canvas) clipPath(v layout.ClipPath) error {
	b := c.b
	b.Save()
	if !pathTo(b, v.Path) {
		b.Restore()
		return nil
	}
	b.ClipEvenOdd()
	b.EndPath()
	c.curved++
	err := c.drawOps(v.Ops)
	c.curved--
	b.Restore()
	return err
}
