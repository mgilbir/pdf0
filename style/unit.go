package style

import "math"

// Layout units: the number type every length in this engine is measured in.
//
// # Why fixed point, and why here
//
// §5.1 of the rendering proposal argues this and it is worth restating, because
// the obvious choice is float64 and it is wrong.
//
// PDF user space is 1/72 inch in floating point and CSS px is 1/96 inch, so
// there is no pixel grid that represents a page exactly — an A4 page is
// 793.7 × 1122.5 CSS px. Rounding layout to integers therefore produces
// resolution-dependent output for no benefit. But float64 is worse in a
// different way: comparisons stop being exact, so "does this line fit" gets a
// different answer depending on how the width was accumulated, and error
// accumulates across a long inline run in a way that is invisible until two
// paragraphs that should align do not.
//
// Fixed point at 1/64 px is what browsers converged on, for both reasons: it
// compares exactly, it does not drift, and it makes layout bit-reproducible —
// which the determinism this repository already tests for and §7's reftest
// comparison both want. Conversion to floating-point points happens once, at
// paint time.
//
// # Why in the style package
//
// A layout unit belongs to layout, and it is declared here because *computed
// values are where a length becomes a number*: "1em" is text until the cascade
// resolves it against a font size. Layout consumes style, so the type has to be
// declared at or below the point that first produces one, and that point is
// here.

// Unit is a length in 64ths of a CSS pixel.
//
// Signed 32-bit gives a range of about ±33.5 million px, which is four hundred
// times the longest side of any page anyone will set. The range is not the
// reason for the size, though — the reason is that every arithmetic below
// *saturates* at it rather than wrapping, and a bound that a hostile stylesheet
// can reach in one step is easier to reason about than one it reaches after a
// thousand additions.
type Unit int32

const (
	// unitsPerPx is the fixed-point scale.
	unitsPerPx = 64

	// MaxUnit and MinUnit are the ends of the range. They are what every
	// operation saturates to.
	MaxUnit Unit = math.MaxInt32
	MinUnit Unit = math.MinInt32
)

// The conversions between CSS's absolute units and px, all exact by definition:
// CSS fixes 1in as 96px, and every other absolute unit as a fraction of an inch.
const (
	pxPerIn = 96.0
	pxPerPt = pxPerIn / 72 // 1pt is 1/72 inch
	pxPerPc = pxPerIn / 6  // 1pc is 12pt
	pxPerCm = pxPerIn / 2.54
	pxPerMm = pxPerIn / 25.4
	pxPerQ  = pxPerIn / 101.6 // 1Q is a quarter of a millimetre
)

// FromPx converts a length in CSS pixels, saturating rather than wrapping.
//
// ok reports whether the value fitted. A caller that ignores it gets a
// well-defined number at the end of the range, which is the safe direction to be
// wrong in — but "width: 1e9px" is a stylesheet saying something impossible, and
// the layer above turns a false here into a finding rather than laying out a
// box the width of a country.
func FromPx(px float64) (u Unit, ok bool) {
	if math.IsNaN(px) {
		// A NaN length poisons every comparison it reaches — it is neither
		// greater nor less than anything — so it becomes zero here rather than
		// travelling.
		return 0, false
	}
	v := px * unitsPerPx
	switch {
	case v > float64(MaxUnit):
		return MaxUnit, false
	case v < float64(MinUnit):
		return MinUnit, false
	}
	return Unit(math.Round(v)), true
}

// Px returns the length in CSS pixels.
func (u Unit) Px() float64 { return float64(u) / unitsPerPx }

// Pt returns the length in PDF user-space units, which are 1/72 inch.
//
// This is the conversion that leaves layout: CSS px is 1/96 inch and a PDF point
// is 1/72, so the factor is exactly 0.75 and it is applied once, at the boundary.
func (u Unit) Pt() float64 { return u.Px() * 72 / 96 }

// Add, Sub and Mul saturate at the ends of the range.
//
// Wrapping is the failure that must not happen: a width that overflowed into a
// negative number does not look like an error, it looks like a box laid out
// inside-out, and every consequence of it is a plausible-looking page. Saturating
// is wrong by a bounded amount and stays ordered.
func (u Unit) Add(v Unit) Unit {
	sum := int64(u) + int64(v)
	return clampUnit(sum)
}

func (u Unit) Sub(v Unit) Unit {
	return clampUnit(int64(u) - int64(v))
}

// Mul scales a length by a plain number, which is what a scale factor, a
// line-height multiplier and a percentage all are.
func (u Unit) Mul(f float64) Unit {
	if math.IsNaN(f) {
		return 0
	}
	v := float64(u) * f
	switch {
	case v > float64(MaxUnit):
		return MaxUnit
	case v < float64(MinUnit):
		return MinUnit
	}
	return Unit(math.Round(v))
}

// Div divides by a plain number. Dividing by zero saturates in the direction of
// the sign rather than producing an infinity, for the reason FromPx refuses a
// NaN: an infinite length is not a very large one, it is a value that makes
// every later comparison meaningless.
func (u Unit) Div(f float64) Unit {
	if f == 0 || math.IsNaN(f) {
		switch {
		case u > 0:
			return MaxUnit
		case u < 0:
			return MinUnit
		}
		return 0
	}
	return u.Mul(1 / f)
}

func clampUnit(v int64) Unit {
	switch {
	case v > int64(MaxUnit):
		return MaxUnit
	case v < int64(MinUnit):
		return MinUnit
	}
	return Unit(v)
}

// Min and Max are here rather than left to the caller because a layout engine
// writes them constantly and Go's generic builtins do not order a named integer
// type any more clearly than this does.
func Min(a, b Unit) Unit {
	if a < b {
		return a
	}
	return b
}

func Max(a, b Unit) Unit {
	if a > b {
		return a
	}
	return b
}

// Clamp constrains a length to a range, with the CSS rule that a minimum wins
// over a maximum when the two contradict each other: "min-width: 100px;
// max-width: 50px" gives 100px, not 50.
//
// The order of the operations *is* that rule, and there is deliberately no
// separate test for hi < lo. Applying the maximum first and the minimum second
// means an impossible range collapses to the minimum on its own — an explicit
// guard was written here first, and removing it changed no answer, so it was
// dead code stating a rule the arithmetic already enforced.
func Clamp(v, lo, hi Unit) Unit {
	return Max(lo, Min(hi, v))
}
