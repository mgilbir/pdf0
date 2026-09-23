package checked

import (
	"math"
	"testing"
)

func TestMul(t *testing.T) {
	for _, c := range []struct {
		in   []int64
		want int64
		ok   bool
	}{
		{nil, 1, true},
		{[]int64{3, 4}, 12, true},
		{[]int64{0, math.MaxInt64, math.MaxInt64}, 0, true},
		{[]int64{1 << 60, 2}, 1 << 61, true},
		// The C11 geometry: 2^60 × 2 × 8 bits wraps an int64 multiply.
		{[]int64{1 << 60, 2, 8}, 0, false},
		{[]int64{math.MaxInt64, 2}, 0, false},
		// Each factor is small; the product is not.
		{[]int64{1 << 31, 1 << 31, 1 << 2}, 0, false},
		{[]int64{-1, 5}, 0, false},
		{[]int64{5, -1}, 0, false},
	} {
		got, ok := Mul(c.in...)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("Mul(%v) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDecimal(t *testing.T) {
	for _, c := range []struct {
		in   string
		v    int64
		n    int
		fits bool
	}{
		{"", 0, 0, true},
		{"x1", 0, 0, true},
		{"0", 0, 1, true},
		{"123 ID", 123, 3, true},
		{"9223372036854775807", math.MaxInt64, 19, true},
		{"9223372036854775808", math.MaxInt64, 19, false},
		{"99999999999999999999999 rest", math.MaxInt64, 23, false},
	} {
		v, n, fits := Decimal(c.in)
		if v != c.v || n != c.n || fits != c.fits {
			t.Errorf("Decimal(%q) = %d, %d, %v; want %d, %d, %v", c.in, v, n, fits, c.v, c.n, c.fits)
		}
		if bv, bn, bfits := Decimal([]byte(c.in)); bv != v || bn != n || bfits != fits {
			t.Errorf("Decimal differs between string and []byte for %q", c.in)
		}
	}
}

func TestWithin(t *testing.T) {
	if !Within(12, 3, 4) || Within(11, 3, 4) {
		t.Error("Within compares the product with the bound, inclusive")
	}
	if Within(math.MaxInt64, math.MaxInt64, 2) {
		t.Error("an overflowing product is within no bound")
	}
}
