package core

import (
	"math"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// TestType0HostileSize: a sampled function's /Size comes from the file. Sizes
// whose product overflows int — as an Integer, or as a Real that object.Int
// saturates to math.MaxInt (audit C118) — must make the function unusable,
// not wrap to a small total that passes the sample-table check and then index
// outside the table. Before the guard, [MaxInt MaxInt] multiplied to 1.
func TestType0HostileSize(t *testing.T) {
	for _, size := range []object.Array{
		{object.Integer(math.MaxInt64), object.Integer(math.MaxInt64)},
		{object.Real(1e300), object.Real(1e300)},
		{object.Real(1e300), object.Integer(2)},
		{object.Integer(1 << 40), object.Integer(1 << 40)},
		// Audit 2026-09-22 C15: one dimension, no overflow in the product, and
		// a sample offset that used to go negative.
		{object.Integer(1 << 62)},
	} {
		st := &object.Stream{Data: make([]byte, 64)}
		st.Dict.Set("FunctionType", object.Integer(0))
		domain := object.Array{}
		for range size {
			domain = append(domain, object.Integer(0), object.Integer(1))
		}
		st.Dict.Set("Domain", domain)
		st.Dict.Set("Range", object.Array{object.Integer(0), object.Integer(1)})
		st.Dict.Set("Size", size)
		st.Dict.Set("BitsPerSample", object.Integer(8))
		var out []float64
		var ok bool
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("/Size %v: panic %v", size, r)
				}
			}()
			in := make([]float64, len(size))
			for i := range in {
				in[i] = 0.7
			}
			out, ok = View{Limits: DefaultLimits()}.EvalFunction(st, in)
		}()
		if ok {
			t.Errorf("/Size %v: evaluated to %v; want the function refused", size, out)
		}
	}
}
