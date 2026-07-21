package pdf0

// JPEG 2000 reconstruction: tier-1 code-block decoding into subband
// coefficients, the inverse 5/3 (reversible) wavelet transform, and the DC level
// shift that yields image samples (ISO/IEC 15444-1 §D, §F, §G). This milestone
// handles the reversible path used by the baseline conformance files; the
// irreversible 9/7 transform and scalar dequantization are layered on later.

// jpxBand is a rectangular array of samples with its origin on the tile-component
// grid.
type jpxBand struct {
	x0, y0, w, h int
	data         []int32
}

func (b *jpxBand) at(x, y int) int32 { return b.data[y*b.w+x] }

// decodeSubbandCoeffs runs tier-1 on every code-block of a subband and assembles
// the subband's coefficient array. For the reversible transform the number of
// magnitude bit-planes is G + Rb + gain_b - 1 (the QCD exponent, when explicit,
// equals Rb+gain, but the derived QCD may signal 0); the irreversible path uses
// the QCD exponent directly.
func decodeSubbandCoeffs(sb *jpxSubband, guardBits, compDepth int, reversible bool) {
	sbw := sb.x1 - sb.x0
	sbh := sb.y1 - sb.y0
	if sbw <= 0 || sbh <= 0 {
		return
	}
	sb.coeffs = make([]int32, sbw*sbh)
	mb := guardBits + sb.exp - 1
	if reversible {
		mb = guardBits + compDepth + sb.gain - 1
	}
	for bi := range sb.blocks {
		cb := &sb.blocks[bi]
		cbw := cb.x1 - cb.x0
		cbh := cb.y1 - cb.y0
		if cbw <= 0 || cbh <= 0 || len(cb.data) == 0 || cb.numPasses == 0 {
			continue
		}
		bpStart := mb - 1 - cb.zeroBitPlanes
		if bpStart < 0 {
			continue
		}
		coeffs := decodeCodeblock(cb.data, cbw, cbh, sb.kind, bpStart, cb.numPasses)
		for y := 0; y < cbh; y++ {
			for x := 0; x < cbw; x++ {
				sb.coeffs[(cb.y0-sb.y0+y)*sbw+(cb.x0-sb.x0+x)] = coeffs[y*cbw+x]
			}
		}
	}
}

// reconstructComponent decodes one tile-component to samples: tier-1 on every
// subband, then the inverse DWT up the resolution pyramid.
func reconstructComponent(im *jpxImage, tc *jpxTileComp) *jpxBand {
	reversible := im.cod.transform == 1
	depth := im.comps[tc.comp].depth
	for _, res := range tc.resolutions {
		for _, sb := range res.subbands {
			decodeSubbandCoeffs(sb, im.qcd.guardBits, depth, reversible)
		}
	}
	res0 := tc.resolutions[0]
	sb0 := res0.subbands[0]
	ll := &jpxBand{x0: sb0.x0, y0: sb0.y0, w: sb0.x1 - sb0.x0, h: sb0.y1 - sb0.y0, data: sb0.coeffs}
	if ll.data == nil {
		ll.data = make([]int32, ll.w*ll.h)
	}
	for r := 1; r < len(tc.resolutions); r++ {
		res := tc.resolutions[r]
		ll = idwt53Level(ll, res)
	}
	return ll
}

// idwt53Level combines the low-low band (ll) with the HL/LH/HH subbands of the
// resolution to reconstruct the resolution's samples (inverse 5/3, T.800 F.3).
func idwt53Level(ll *jpxBand, res *jpxResolution) *jpxBand {
	W := res.x1 - res.x0
	H := res.y1 - res.y0
	out := &jpxBand{x0: res.x0, y0: res.y0, w: W, h: H, data: make([]int32, W*H)}
	if W <= 0 || H <= 0 {
		return out
	}
	hl, lh, hh := res.subbands[0], res.subbands[1], res.subbands[2]
	place := func(x0, y0, w, h int, data []int32, highH, highV bool) {
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
	place(hl.x0, hl.y0, hl.x1-hl.x0, hl.y1-hl.y0, hl.coeffs, true, false)
	place(lh.x0, lh.y0, lh.x1-lh.x0, lh.y1-lh.y0, lh.coeffs, false, true)
	place(hh.x0, hh.y0, hh.x1-hh.x0, hh.y1-hh.y0, hh.coeffs, true, true)

	// Vertical inverse on each column, then horizontal inverse on each row.
	col := make([]int32, H)
	for c := 0; c < W; c++ {
		for row := 0; row < H; row++ {
			col[row] = out.data[row*W+c]
		}
		inv53(col, res.y0, res.y1)
		for row := 0; row < H; row++ {
			out.data[row*W+c] = col[row]
		}
	}
	for row := 0; row < H; row++ {
		inv53(out.data[row*W:row*W+W], res.x0, res.x1)
	}
	return out
}

// inv53 applies the 1-D inverse 5/3 lifting in place over samples whose absolute
// coordinates run [i0,i1). Even coordinates are low-pass, odd are high-pass;
// boundaries use whole-sample symmetric extension (T.800 F.3.4, F.4.8.2).
func inv53(a []int32, i0, i1 int) {
	n := i1 - i0
	if n == 1 {
		if i0&1 == 1 {
			a[0] >>= 1
		}
		return
	}
	ext := func(i int) int32 {
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
	// Undo the update step on the even (low-pass) samples.
	for i := i0; i < i1; i++ {
		if i&1 == 0 {
			a[i-i0] -= (ext(i-1) + ext(i+1) + 2) >> 2
		}
	}
	// Undo the predict step on the odd (high-pass) samples.
	for i := i0; i < i1; i++ {
		if i&1 == 1 {
			a[i-i0] += (ext(i-1) + ext(i+1)) >> 1
		}
	}
}
