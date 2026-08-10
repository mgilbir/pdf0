package render

import (
	"fmt"

	"github.com/mgilbir/pdf0/style"
)

// Geometry, in layout units.
//
// Everything here is in absolute page coordinates with the origin at the top
// left and y increasing downwards, which is CSS's convention rather than PDF's.
// The flip to PDF's bottom-left origin happens once, where the display list
// becomes a content stream — for the same reason the conversion from layout
// units to points happens there: a coordinate system that changes halfway
// through an engine is one where every sign error is plausible.

// Point is a position.
type Point struct{ X, Y style.Unit }

// Size is an extent.
type Size struct{ W, H style.Unit }

// Rect is a rectangle: a position and an extent.
//
// It is stored as position-and-size rather than as two corners because layout
// computes it that way — a box is placed and then given a width — and because
// a rectangle with a negative extent is then representable, which is worth
// being able to see rather than normalising away.
type Rect struct {
	X, Y, W, H style.Unit
}

func (r Rect) Right() style.Unit  { return r.X.Add(r.W) }
func (r Rect) Bottom() style.Unit { return r.Y.Add(r.H) }

func (r Rect) Origin() Point { return Point{r.X, r.Y} }
func (r Rect) Size() Size    { return Size{r.W, r.H} }

// Empty reports whether the rectangle encloses nothing.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Contains reports whether inner lies entirely within r.
//
// It is what the overflow guardrails ask, so its edge behaviour matters: a box
// exactly filling its container is contained, since a border that lands on the
// boundary is not overflow.
func (r Rect) Contains(inner Rect) bool {
	return inner.X >= r.X && inner.Y >= r.Y &&
		inner.Right() <= r.Right() && inner.Bottom() <= r.Bottom()
}

// Intersect is the rectangle two rectangles have in common.
//
// Two rectangles that do not overlap produce one with a negative extent, which
// Empty reports and every consumer treats as nothing. It is not normalised to
// zero, for the reason Rect itself is not: a value that says "less than
// nothing" is worth being able to see.
//
// The arithmetic cannot wrap. Every operation on a Unit saturates at the ends
// of its range, so the worst an intersection of two extreme rectangles can
// produce is a saturated extent — never a negative width that reads as a very
// large one, which is the failure that would turn a clip into an amplifier.
func (r Rect) Intersect(o Rect) Rect {
	x := style.Max(r.X, o.X)
	y := style.Max(r.Y, o.Y)
	return Rect{
		X: x, Y: y,
		W: style.Min(r.Right(), o.Right()).Sub(x),
		H: style.Min(r.Bottom(), o.Bottom()).Sub(y),
	}
}

// Inset shrinks a rectangle by an edge on each side, which is what stepping in
// from a border box to a padding box to a content box does.
//
// The result is clamped to a non-negative extent. A padding wider than the box
// it is inside produces an empty content box rather than an inside-out one, and
// an inside-out rectangle is the shape that makes every later comparison give a
// plausible wrong answer.
func (r Rect) Inset(e Edges) Rect {
	out := Rect{
		X: r.X.Add(e.Left),
		Y: r.Y.Add(e.Top),
		W: r.W.Sub(e.Left).Sub(e.Right),
		H: r.H.Sub(e.Top).Sub(e.Bottom),
	}
	if out.W < 0 {
		out.W = 0
	}
	if out.H < 0 {
		out.H = 0
	}
	return out
}

// Outset grows a rectangle by an edge on each side, which is what stepping out
// to a margin box does. It is not clamped: a negative margin legitimately
// produces a margin box smaller than the border box.
func (r Rect) Outset(e Edges) Rect {
	return Rect{
		X: r.X.Sub(e.Left),
		Y: r.Y.Sub(e.Top),
		W: r.W.Add(e.Left).Add(e.Right),
		H: r.H.Add(e.Top).Add(e.Bottom),
	}
}

func (r Rect) String() string {
	return fmt.Sprintf("(%.2f,%.2f %.2fx%.2f)", r.X.Px(), r.Y.Px(), r.W.Px(), r.H.Px())
}

// Edges is a value per side, in the order CSS writes them.
type Edges struct {
	Top, Right, Bottom, Left style.Unit
}

// Horizontal and Vertical are the sums along each axis, which the box-model
// arithmetic asks for constantly.
func (e Edges) Horizontal() style.Unit { return e.Left.Add(e.Right) }
func (e Edges) Vertical() style.Unit   { return e.Top.Add(e.Bottom) }

func (e Edges) Add(o Edges) Edges {
	return Edges{
		Top:    e.Top.Add(o.Top),
		Right:  e.Right.Add(o.Right),
		Bottom: e.Bottom.Add(o.Bottom),
		Left:   e.Left.Add(o.Left),
	}
}

func (e Edges) String() string {
	return fmt.Sprintf("[%.2f %.2f %.2f %.2f]",
		e.Top.Px(), e.Right.Px(), e.Bottom.Px(), e.Left.Px())
}
