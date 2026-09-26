package object

import (
	"math"
	"testing"
)

// TestNumericEqualityOnePolicy is the C122 guard (audit 2026-09-22): every
// comparison involving a Real uses one tolerance, whatever the other operand's
// type, and NaN is handled deliberately.
func TestNumericEqualityOnePolicy(t *testing.T) {
	nan := Real(math.NaN())
	inf, ninf := Real(math.Inf(1)), Real(math.Inf(-1))
	cases := []struct {
		a, b Object
		want bool
	}{
		// The audit's pairs: the answer no longer depends on the type of zero.
		{Real(1e-11), Real(0), false},
		{Integer(0), Real(1e-11), false},
		{Real(1e-13), Real(0), true}, // float noise near zero
		{Integer(0), Real(1e-13), true},
		{Real(0.1 + 0.2), Real(0.3), true},
		// Relative at large magnitudes, for Real-Real as for Integer-Real.
		{Real(1e20), Real(1e20 + 16384), true},
		{Integer(1e17), Real(1e17 + 16), true},
		{Real(1e17), Real(1e17 + 16), true},
		{Real(1e6), Real(1e6 + 1), false},
		{Integer(1e6), Real(1e6 + 1), false},
		// Two Integers compare exactly.
		{Integer(1e17), Integer(1e17 + 1), false},
		// NaN equals NaN and nothing else; infinities equal only themselves.
		{nan, nan, true},
		{nan, Real(0), false},
		{nan, Integer(0), false},
		{inf, inf, true},
		{inf, ninf, false},
		{inf, Real(math.MaxFloat64), false},
		{Real(math.MaxFloat64), Real(-math.MaxFloat64), false},
		{Real(math.Copysign(0, -1)), Integer(0), true},
		// Different types that are not both numbers.
		{Integer(1), Boolean(true), false},
		{Real(1), Name("1"), false},
	}
	for _, c := range cases {
		if got := Equal(c.a, c.b); got != c.want {
			t.Errorf("Equal(%v %T, %v %T) = %v, want %v", c.a, c.a, c.b, c.b, got, c.want)
		}
		if got := Equal(c.b, c.a); got != c.want {
			t.Errorf("Equal(%v %T, %v %T) = %v, want %v (symmetry)", c.b, c.b, c.a, c.a, got, c.want)
		}
	}

	// The policy is the same for an Integer and a Real holding the same value:
	// comparing x against y gives one answer whether x arrives as Integer(x)
	// or Real(x). (Exact for |x| < 2^53, where both hold x exactly.)
	values := []float64{0, 1, -1, 7, 1e3, 1e6, 1e9, 1e12, 1 << 52}
	offsets := []float64{0, 1e-14, 1e-12, 1e-11, 1e-6, 0.5, 1}
	for _, x := range values {
		for _, d := range offsets {
			y := x + d
			if Equal(Integer(int64(x)), Real(y)) != Equal(Real(x), Real(y)) {
				t.Errorf("x=%v y=%v: Integer-Real and Real-Real disagree", x, y)
			}
		}
	}

	// Reflexive, including through containers that hold a NaN.
	withNaN := NewDictionary(Entry{Key: "A", Value: Array{nan, Real(1)}})
	if !Equal(withNaN, withNaN) {
		t.Error("a dictionary holding a NaN does not equal itself")
	}
}
