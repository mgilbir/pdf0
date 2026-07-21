package pdf0

// JPEG 2000 irreversible (9/7) reconstruction (ISO/IEC 15444-1 §F.3.6, §E):
// scalar dequantization of the tier-1 coefficients followed by the inverse 9/7
// wavelet transform, which unlike the reversible 5/3 uses floating-point lifting.

// jpxFBand is a float sample array with its origin on the tile-component grid.
type jpxFBand struct {
	x0, y0, w, h int
	data         []float64
}

func pow2(e int) float64 {
	v := 1.0
	if e >= 0 {
		for i := 0; i < e; i++ {
			v *= 2
		}
	} else {
		for i := 0; i < -e; i++ {
			v /= 2
		}
	}
	return v
}

// dequantSubband returns the dequantized float coefficients of a subband: step
// delta_b = 2^(Rb+gain_b-exp_b)·(1+mant_b/2^11) (T.800 E.1).
func dequantSubband(sb *jpxSubband, depth int) []float64 {
	w := sb.x1 - sb.x0
	h := sb.y1 - sb.y0
	out := make([]float64, w*h)
	if sb.coeffs == nil {
		return out
	}
	// The tier-1 magnitudes are doubled (midpoint reconstruction); the 0.5 factor
	// folds the halving into the dequantization step (OpenJPEG: 0.5·stepsize).
	delta := 0.5 * pow2(depth+sb.gain-sb.exp) * (1 + float64(sb.mant)/2048)
	for i, q := range sb.coeffs {
		out[i] = float64(q) * delta
	}
	return out
}

// reconstructComponentF decodes one tile-component through the irreversible 9/7
// path: tier-1, dequantization, then the inverse 9/7 wavelet transform.
func reconstructComponentF(im *jpxImage, tc *jpxTileComp) *jpxFBand {
	depth := im.comps[tc.comp].depth
	roishift := im.roiShift(tc.tile, tc.comp)
	for _, res := range tc.resolutions {
		for _, sb := range res.subbands {
			if !decodeSubbandCoeffs(sb, im.qcd.guardBits, depth, im.cod.cbStyle, roishift, false) {
				return nil
			}
		}
	}
	res0 := tc.resolutions[0]
	sb0 := res0.subbands[0]
	ll := &jpxFBand{x0: sb0.x0, y0: sb0.y0, w: sb0.x1 - sb0.x0, h: sb0.y1 - sb0.y0, data: dequantSubband(sb0, depth)}
	for r := 1; r < len(tc.resolutions); r++ {
		ll = idwt97Level(ll, tc.resolutions[r], depth)
	}
	return ll
}

// idwt97Level combines the low-low band with the resolution's HL/LH/HH subbands
// via the inverse 9/7 transform.
func idwt97Level(ll *jpxFBand, res *jpxResolution, depth int) *jpxFBand {
	W := res.x1 - res.x0
	H := res.y1 - res.y0
	out := &jpxFBand{x0: res.x0, y0: res.y0, w: W, h: H, data: make([]float64, W*H)}
	if W <= 0 || H <= 0 {
		return out
	}
	hl, lh, hh := res.subbands[0], res.subbands[1], res.subbands[2]
	place := func(x0, y0, w, h int, data []float64, highH, highV bool) {
		for ly := 0; ly < h; ly++ {
			for lx := 0; lx < w; lx++ {
				absX := 2 * (x0 + lx)
				if highH {
					absX++
				}
				absY := 2 * (y0 + ly)
				if highV {
					absY++
				}
				col, row := absX-res.x0, absY-res.y0
				if col >= 0 && col < W && row >= 0 && row < H {
					out.data[row*W+col] = data[ly*w+lx]
				}
			}
		}
	}
	place(ll.x0, ll.y0, ll.w, ll.h, ll.data, false, false)
	place(hl.x0, hl.y0, hl.x1-hl.x0, hl.y1-hl.y0, dequantSubband(hl, depth), true, false)
	place(lh.x0, lh.y0, lh.x1-lh.x0, lh.y1-lh.y0, dequantSubband(lh, depth), false, true)
	place(hh.x0, hh.y0, hh.x1-hh.x0, hh.y1-hh.y0, dequantSubband(hh, depth), true, true)

	col := make([]float64, H)
	for c := 0; c < W; c++ {
		for row := 0; row < H; row++ {
			col[row] = out.data[row*W+c]
		}
		inv97(col, res.y0, res.y1)
		for row := 0; row < H; row++ {
			out.data[row*W+c] = col[row]
		}
	}
	for row := 0; row < H; row++ {
		inv97(out.data[row*W:row*W+W], res.x0, res.x1)
	}
	return out
}

// 9/7 lifting coefficients (T.800 Table F.4).
const (
	jpxA = -1.586134342059924
	jpxB = -0.052980118572961
	jpxG = 0.882911075530934
	jpxD = 0.443506852043971
	jpxK = 1.230174104914001
)

// inv97 applies the 1-D inverse 9/7 lifting in place over samples whose absolute
// coordinates run [i0,i1). Even coordinates are low-pass, odd high-pass; the
// boundary uses whole-sample symmetric extension. It is the exact inverse of
// fwd97 (validated by round-trip test).
func inv97(a []float64, i0, i1 int) {
	n := i1 - i0
	if n == 1 {
		return
	}
	ext := func(i int) float64 {
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
	// Undo scaling: even (low) by K, odd (high) by 1/K.
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] *= jpxK
		} else {
			a[i-i0] /= jpxK
		}
	}
	// Undo update2 (delta) on even, predict2 (gamma) on odd,
	// update1 (beta) on even, predict1 (alpha) on odd.
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] -= jpxD * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 1 {
			a[i-i0] -= jpxG * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] -= jpxB * (ext(i-1) + ext(i+1))
		}
	}
	for i := i0; i < i1; i++ {
		if i&1 == 1 {
			a[i-i0] -= jpxA * (ext(i-1) + ext(i+1))
		}
	}
}
