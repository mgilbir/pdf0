package object

import (
	"math"
	"testing"
)

// TestIntSaturates (C118): Int of a Real outside the range of int is defined —
// saturated at math.MaxInt or math.MinInt, and 0 for NaN — rather than the
// implementation-specific result of the conversion, which on amd64 is
// math.MinInt for every overflow, positive ones included.
func TestIntSaturates(t *testing.T) {
	cases := []struct {
		in   Object
		want int
	}{
		{Real(1e300), math.MaxInt},
		{Real(math.Inf(1)), math.MaxInt},
		{Real(9223372036854775808.0), math.MaxInt}, // 2^63: first value past MaxInt
		{Real(-1e300), math.MinInt},
		{Real(math.Inf(-1)), math.MinInt},
		{Real(-9223372036854775808.0), math.MinInt}, // exactly MinInt
		{Real(math.NaN()), 0},
		{Real(1.9), 1},
		{Real(-1.9), -1},
		{Real(4611686018427387904.0), 1 << 62},
		{Integer(math.MaxInt64), math.MaxInt},
		{Integer(math.MinInt64), math.MinInt},
		{Integer(7), 7},
		{Name("7"), 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := Int(c.in); got != c.want {
			t.Errorf("Int(%#v) = %d, want %d", c.in, got, c.want)
		}
	}
}
