package htmlpdf

import (
	"fmt"
	"math"

	"github.com/mgilbir/forme/layout"
	"github.com/mgilbir/forme/style"
)

// A matrix is PDF's and CSS's [a b c d e f]: (x, y) goes to (a·x + c·y + e,
// b·x + d·y + f).

var identityMatrix = [6]float64{1, 0, 0, 1, 0, 0}

// groupMatrix is a TransformGroup's matrix in the coordinates this backend
// writes, which are pixels: layout states its move, e and f, in layout units,
// as it states every coordinate, and the rest are numbers.
func groupMatrix(g layout.TransformGroup) [6]float64 {
	m := g.Matrix
	px := style.Unit(1).Px()
	return [6]float64{m[0], m[1], m[2], m[3], m[4] * px, m[5] * px}
}

// concatMatrix is the matrix that applies first and then then: what "cm"
// makes of the matrix in force when it concatenates first to it.
func concatMatrix(first, then [6]float64) [6]float64 {
	return [6]float64{
		first[0]*then[0] + first[1]*then[2],
		first[0]*then[1] + first[1]*then[3],
		first[2]*then[0] + first[3]*then[2],
		first[2]*then[1] + first[3]*then[3],
		first[4]*then[0] + first[5]*then[2] + then[4],
		first[4]*then[1] + first[5]*then[3] + then[5],
	}
}

func det(m [6]float64) float64 { return m[0]*m[3] - m[1]*m[2] }

// invertMatrix is m's inverse; m is invertible.
func invertMatrix(m [6]float64) [6]float64 {
	d := det(m)
	a, b, c, dd := m[3]/d, -m[1]/d, -m[2]/d, m[0]/d
	return [6]float64{a, b, c, dd, -(m[4]*a + m[5]*c), -(m[4]*b + m[5]*dd)}
}

// boundsOf is the rectangle around box, [xMin yMin xMax yMax], drawn through
// m.
func boundsOf(m [6]float64, box [4]float64) [4]float64 {
	out := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range [4][2]float64{{box[0], box[1]}, {box[2], box[1]}, {box[0], box[3]}, {box[2], box[3]}} {
		x := m[0]*p[0] + m[2]*p[1] + m[4]
		y := m[1]*p[0] + m[3]*p[1] + m[5]
		out[0], out[1] = math.Min(out[0], x), math.Min(out[1], y)
		out[2], out[3] = math.Max(out[2], x), math.Max(out[3], y)
	}
	return out
}

// The bounds of what a TransformGroup may do to the drawing inside it and be
// drawn: each axis scaled by at most maxGroupScale and at least its
// reciprocal, and moved by at most maxGroupMove pixels.
//
// A PDF number is a reader's float (ISO 32000-2 Annex C), and a form drawn
// inside a group has the sheet as its bounding box in the group's
// coordinates, which is the sheet through the inverse matrix: a scale of a
// millionth makes it a million sheets wide. Past these bounds the numbers a
// reader is asked to multiply stop meaning what they say, and no page a
// stylesheet means to make needs them.
const (
	maxGroupScale = 1e6
	maxGroupMove  = 1e9
)

// transformUndrawable is why a TransformGroup, whose matrix and those of the
// groups around it multiply to m, cannot be drawn, or "". A matrix that is not
// invertible is drawable: CSS Transforms 1 §6 does not display what it
// transforms, which is a page this backend draws exactly by drawing nothing.
func transformUndrawable(m [6]float64) string {
	for _, v := range m {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Sprintf("a transform whose matrix %v is not numbers", m)
		}
	}
	if math.Abs(m[4]) > maxGroupMove || math.Abs(m[5]) > maxGroupMove {
		return fmt.Sprintf("a transform that moves what it draws by (%g, %g) pixels, past the "+
			"%g a PDF reader's numbers carry exactly", m[4], m[5], maxGroupMove)
	}
	d := det(m)
	if d == 0 {
		return ""
	}
	// The singular values of the linear part: how much the matrix stretches
	// the direction it stretches most, and the one it stretches least.
	s := m[0]*m[0] + m[1]*m[1] + m[2]*m[2] + m[3]*m[3]
	most := math.Sqrt((s + math.Sqrt(math.Max(0, s*s-4*d*d))) / 2)
	least := math.Abs(d) / most
	if most > maxGroupScale || least < 1/maxGroupScale {
		return fmt.Sprintf("a transform that scales what it draws by between %g and %g, past the "+
			"millionfold either way a PDF reader's numbers carry exactly", least, most)
	}
	return ""
}
