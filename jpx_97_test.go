package pdf0

import (
	"math"
	"testing"
)

// fwd97 is the forward (analysis) 9/7 lifting, used only to validate that inv97
// is its exact inverse. Even coordinates are low-pass, odd high-pass.
func fwd97(a []float32, i0, i1 int) {
	n := i1 - i0
	if n == 1 {
		return
	}
	ext := func(i int) float32 {
		for i < i0 || i >= i1 {
			if i < i0 {
				i = 2*i0 - i
			}
			if i >= i1 {
				i = 2*(i1-1) - i
			}
		}
		return a[i-i0]
	}
	for i := i0; i < i1; i++ {
		if i&1 == 1 {
			a[i-i0] += jpxA * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] += jpxB * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 1 {
			a[i-i0] += jpxG * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] += jpxD * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] /= jpxK
		} else {
			a[i-i0] *= jpxK
		}
	}
}

// TestInv97RoundTrip checks inv97 exactly inverts fwd97 over even and odd
// lengths and non-zero coordinate origins.
func TestInv97RoundTrip(t *testing.T) {
	cases := []struct{ i0, i1 int }{{0, 8}, {0, 7}, {0, 16}, {0, 15}, {0, 32}, {3, 19}, {2, 17}}
	for _, c := range cases {
		n := c.i1 - c.i0
		orig := make([]float32, n)
		for i := range orig {
			orig[i] = float32((i*37+13)%100) - 50
		}
		a := append([]float32{}, orig...)
		fwd97(a, c.i0, c.i1)
		inv97(a, c.i0, c.i1)
		for i := range a {
			if math.Abs(float64(a[i]-orig[i])) > 1e-3 {
				t.Errorf("[%d,%d) idx %d: got %.6f want %.6f", c.i0, c.i1, i, a[i], orig[i])
				break
			}
		}
	}
}
