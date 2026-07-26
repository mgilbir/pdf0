package pdf0

import (
	"math"
)

// This file evaluates PDF functions (ISO 32000-1 clause 7.10): type 0 sampled,
// type 2 exponential interpolation, type 3 stitching and type 4 PostScript
// calculator. It is used to turn Separation/DeviceN tint values into their
// alternate-space colour components.

// maxFunctionDepth bounds recursion through type-3 stitching subfunctions so a
// maliciously nested function cannot exhaust the stack.
const maxFunctionDepth = 32

// evalFunction evaluates the PDF function fn (a dictionary or stream) for the
// input vector in, returning the output vector. ok is false for a function it
// cannot evaluate or malformed input. Inputs are clamped to /Domain and outputs
// to /Range.
func (d *Document) evalFunction(fn Object, in []float64) (out []float64, ok bool) {
	return d.evalFunctionDepth(fn, in, 0)
}

func (d *Document) evalFunctionDepth(fn Object, in []float64, depth int) ([]float64, bool) {
	if depth > maxFunctionDepth {
		return nil, false
	}
	resolved := d.Resolve(fn)
	var dict *Dictionary
	var stream *Stream
	switch v := resolved.(type) {
	case *Dictionary:
		dict = v
	case *Stream:
		stream = v
		dict = &v.Dict
	default:
		return nil, false
	}

	domain := d.floatArray(dict.Get("Domain"))
	// Clamp inputs to Domain when present.
	x := make([]float64, len(in))
	copy(x, in)
	if len(domain) >= 2*len(x) {
		for i := range x {
			x[i] = clampRange(x[i], domain[2*i], domain[2*i+1])
		}
	}

	var out []float64
	var ok bool
	switch intValue(d.Resolve(dict.Get("FunctionType"))) {
	case 2:
		out, ok = d.evalType2(dict, x)
	case 3:
		out, ok = d.evalType3(dict, domain, x, depth)
	case 0:
		if stream == nil {
			return nil, false
		}
		out, ok = d.evalType0(stream, dict, domain, x)
	case 4:
		if stream == nil {
			return nil, false
		}
		out, ok = d.evalType4(stream, dict, x)
	default:
		return nil, false
	}
	if !ok {
		return nil, false
	}
	// Clamp outputs to /Range when present.
	if rng := d.floatArray(dict.Get("Range")); len(rng) >= 2*len(out) {
		for i := range out {
			out[i] = clampRange(out[i], rng[2*i], rng[2*i+1])
		}
	}
	return out, true
}

// evalType2 evaluates an exponential interpolation function: out[i] = C0[i] +
// x^N * (C1[i]-C0[i]) over a single input.
func (d *Document) evalType2(dict *Dictionary, x []float64) ([]float64, bool) {
	if len(x) != 1 {
		return nil, false
	}
	n := floatValue(d.Resolve(dict.Get("N")))
	c0 := d.floatArray(dict.Get("C0"))
	c1 := d.floatArray(dict.Get("C1"))
	if c0 == nil {
		c0 = []float64{0}
	}
	if c1 == nil {
		c1 = []float64{1}
	}
	if len(c0) != len(c1) || len(c0) == 0 {
		return nil, false
	}
	xn := math.Pow(x[0], n)
	if math.IsNaN(xn) || math.IsInf(xn, 0) {
		return nil, false
	}
	out := make([]float64, len(c0))
	for i := range out {
		out[i] = c0[i] + xn*(c1[i]-c0[i])
	}
	return out, true
}

// evalType3 evaluates a stitching function: it selects a subfunction for the
// single input by /Bounds, remaps the input through /Encode and recurses.
func (d *Document) evalType3(dict *Dictionary, domain []float64, x []float64, depth int) ([]float64, bool) {
	if len(x) != 1 || len(domain) < 2 {
		return nil, false
	}
	funcs, ok := d.Resolve(dict.Get("Functions")).(Array)
	if !ok || len(funcs) == 0 {
		return nil, false
	}
	bounds := d.floatArray(dict.Get("Bounds"))
	encode := d.floatArray(dict.Get("Encode"))
	k := len(funcs)
	if len(bounds) != k-1 || len(encode) != 2*k {
		return nil, false
	}
	xv := x[0]
	// Find the segment: the first i where xv < bounds[i]; else last.
	i := k - 1
	for j := 0; j < k-1; j++ {
		if xv < bounds[j] {
			i = j
			break
		}
	}
	lo := domain[0]
	if i > 0 {
		lo = bounds[i-1]
	}
	hi := domain[1]
	if i < k-1 {
		hi = bounds[i]
	}
	e := interpolate(xv, lo, hi, encode[2*i], encode[2*i+1])
	return d.evalFunctionDepth(funcs[i], []float64{e}, depth+1)
}

// evalType0 evaluates a sampled function by multilinear interpolation over the
// sample grid.
func (d *Document) evalType0(stream *Stream, dict *Dictionary, domain []float64, x []float64) ([]float64, bool) {
	m := len(domain) / 2
	if m == 0 || len(x) != m {
		return nil, false
	}
	rng := d.floatArray(dict.Get("Range"))
	n := len(rng) / 2
	if n == 0 {
		return nil, false
	}
	sizeArr, ok := d.Resolve(dict.Get("Size")).(Array)
	if !ok || len(sizeArr) != m {
		return nil, false
	}
	size := make([]int, m)
	total := 1
	for i := range size {
		size[i] = intValue(d.Resolve(sizeArr[i]))
		if size[i] < 1 {
			return nil, false
		}
		total *= size[i]
	}
	bps := intValue(d.Resolve(dict.Get("BitsPerSample")))
	switch bps {
	case 1, 2, 4, 8, 12, 16, 24, 32:
	default:
		return nil, false
	}
	encode := d.floatArray(dict.Get("Encode"))
	if encode == nil {
		encode = make([]float64, 2*m)
		for i := 0; i < m; i++ {
			encode[2*i] = 0
			encode[2*i+1] = float64(size[i] - 1)
		}
	}
	if len(encode) != 2*m {
		return nil, false
	}
	decode := d.floatArray(dict.Get("Decode"))
	if decode == nil {
		decode = rng
	}
	if len(decode) != 2*n {
		return nil, false
	}
	data := decodeContentStream(d, stream)
	// Guard against a sample table that does not hold every grid sample.
	needBits := int64(total) * int64(n) * int64(bps)
	if int64(len(data))*8 < needBits {
		return nil, false
	}
	maxSample := float64(uint64(1)<<uint(bps) - 1)

	// Encode each input to a grid coordinate, clamped to [0, size-1].
	e := make([]float64, m)
	for i := 0; i < m; i++ {
		ev := interpolate(x[i], domain[2*i], domain[2*i+1], encode[2*i], encode[2*i+1])
		e[i] = clampRange(ev, 0, float64(size[i]-1))
	}

	out := make([]float64, n)
	// Iterate over the 2^m corners of the cell containing e.
	corners := 1 << uint(m)
	for c := 0; c < corners; c++ {
		weight := 1.0
		flat := 0
		stridE := 1
		for i := 0; i < m; i++ {
			lo := int(math.Floor(e[i]))
			frac := e[i] - float64(lo)
			var idx int
			if c&(1<<uint(i)) != 0 {
				idx = lo + 1
				weight *= frac
			} else {
				idx = lo
				weight *= 1 - frac
			}
			if idx > size[i]-1 {
				idx = size[i] - 1
			}
			if idx < 0 {
				idx = 0
			}
			flat += idx * stridE
			stridE *= size[i]
		}
		if weight == 0 {
			continue
		}
		base := flat * n
		for j := 0; j < n; j++ {
			raw := readSampleBits(data, (base+j)*bps, bps)
			dv := decode[2*j] + float64(raw)*(decode[2*j+1]-decode[2*j])/maxSample
			out[j] += weight * dv
		}
	}
	return out, true
}

// readSampleBits reads a bps-bit unsigned sample starting at bit offset off,
// MSB-first.
func readSampleBits(data []byte, off, bps int) uint64 {
	var v uint64
	for i := 0; i < bps; i++ {
		bitPos := off + i
		byteIdx := bitPos / 8
		bit := 0
		if byteIdx < len(data) {
			bit = int(data[byteIdx]>>(7-uint(bitPos%8))) & 1
		}
		v = (v << 1) | uint64(bit)
	}
	return v
}

// interpolate maps x from [xmin,xmax] linearly onto [ymin,ymax].
func interpolate(x, xmin, xmax, ymin, ymax float64) float64 {
	if xmax == xmin {
		return ymin
	}
	return ymin + (x-xmin)*(ymax-ymin)/(xmax-xmin)
}

func clampRange(v, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// floatArray resolves obj to an Array of numbers, or nil.
func (d *Document) floatArray(obj Object) []float64 {
	arr, ok := d.Resolve(obj).(Array)
	if !ok {
		return nil
	}
	out := make([]float64, len(arr))
	for i := range arr {
		out[i] = floatValue(d.Resolve(arr[i]))
	}
	return out
}
