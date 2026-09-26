package core

import (
	"math"
	"math/rand"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// referenceType0 is the multilinear interpolation over all 2^m corners that
// evalType0 used before it enumerated only the live ones (audit 2026-09-22
// C50). It is the oracle for TestType0LiveCornersMatchAllCorners.
func referenceType0(data []byte, size []int, bps, n int, decode, e []float64) []float64 {
	m := len(size)
	maxSample := float64(uint64(1)<<uint(bps) - 1)
	out := make([]float64, n)
	for c := 0; c < 1<<uint(m); c++ {
		weight := 1.0
		flat, stride := 0, 1
		for i := 0; i < m; i++ {
			lo := int(math.Floor(e[i]))
			frac := e[i] - float64(lo)
			idx := lo
			if c&(1<<uint(i)) != 0 {
				idx = lo + 1
				weight *= frac
			} else {
				weight *= 1 - frac
			}
			idx = min(max(idx, 0), size[i]-1)
			flat += idx * stride
			stride *= size[i]
		}
		if weight == 0 {
			continue
		}
		for j := 0; j < n; j++ {
			raw := readSampleBits(data, (flat*n+j)*bps, bps)
			out[j] += weight * (decode[2*j] + float64(raw)*(decode[2*j+1]-decode[2*j])/maxSample)
		}
	}
	return out
}

// TestType0LiveCornersMatchAllCorners checks the live-corner enumeration
// against the all-corner one over random sampled functions and inputs,
// including inputs that sit on grid points and at the grid's edges, where
// dimensions drop out.
func TestType0LiveCornersMatchAllCorners(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		m := 1 + rng.Intn(5)
		n := 1 + rng.Intn(3)
		size := make([]int, m)
		total := 1
		var domain, sizeArr object.Array
		for i := range size {
			size[i] = 1 + rng.Intn(4)
			total *= size[i]
			domain = append(domain, object.Integer(0), object.Integer(1))
			sizeArr = append(sizeArr, object.Integer(size[i]))
		}
		var rangeArr object.Array
		decode := make([]float64, 2*n)
		for j := 0; j < n; j++ {
			rangeArr = append(rangeArr, object.Integer(0), object.Integer(1))
			decode[2*j], decode[2*j+1] = 0, 1
		}
		data := make([]byte, total*n)
		rng.Read(data)
		dict := object.NewDictionary(
			object.Entry{Key: "FunctionType", Value: object.Integer(0)},
			object.Entry{Key: "Domain", Value: domain},
			object.Entry{Key: "Range", Value: rangeArr},
			object.Entry{Key: "Size", Value: sizeArr},
			object.Entry{Key: "BitsPerSample", Value: object.Integer(8)},
		)
		st := object.NewStream(dict, data)
		v := View{Objects: map[int]*object.IndirectObject{}, Limits: DefaultLimits(), Run: NewRun(&Recorder{})}
		for k := 0; k < 10; k++ {
			x := make([]float64, m)
			e := make([]float64, m)
			for i := range x {
				switch rng.Intn(3) {
				case 0:
					x[i] = rng.Float64()
				case 1:
					x[i] = float64(rng.Intn(size[i])) / math.Max(1, float64(size[i]-1))
				default:
					x[i] = float64(rng.Intn(2))
				}
				e[i] = clampRange(interpolate(x[i], 0, 1, 0, float64(size[i]-1)), 0, float64(size[i]-1))
			}
			got, ok := v.EvalFunction(st, x)
			if !ok {
				t.Fatalf("size %v x %v: not evaluated", size, x)
			}
			want := referenceType0(data, size, 8, n, decode, e)
			for j := range want {
				if math.Abs(got[j]-min(max(want[j], 0), 1)) > 1e-9 {
					t.Fatalf("size %v x %v: out[%d] = %v, want %v", size, x, j, got[j], want[j])
				}
			}
		}
	}
}
