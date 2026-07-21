package pdf0

// JPEG 2000 tier-1 EBCOT decoding (ISO/IEC 15444-1 §D). Each code-block is coded
// bit-plane by bit-plane, most-significant first, with the MQ arithmetic coder
// (the same coder as JBIG2, mq.go). A bit-plane is decoded in up to three passes
// — significance propagation, magnitude refinement, and cleanup — using context
// labels formed from the significance and signs of the eight neighbours. This
// file turns a code-block's coded bytes into signed wavelet coefficients.

// Tier-1 MQ context indices (T.800): 0–8 zero-coding (significance), 9–13 sign
// coding, 14–16 magnitude refinement, 17 run-length, 18 uniform.
const (
	jpxCtxRun = 17
	jpxCtxUni = 18
)

// Coding-pass kinds.
const (
	passCleanup = 0
	passSigProp = 1
	passMagRef  = 2
)

// decodeCodeblock decodes one code-block into w*h signed coefficients. bpStart is
// the most-significant coded bit-plane index (Mb-1-zeroBitPlanes); numPasses is
// the total coding-pass count; kind is the subband orientation (0 LL, 1 HL, 2 LH,
// 3 HH). segs are the arithmetic segments (a fresh MQ decoder per segment); the
// coefficient state persists across them.
func decodeCodeblock(segs []jpxSeg, w, h, kind, bpStart, numPasses, cbStyle int) []int32 {
	if w <= 0 || h <= 0 || bpStart < 0 || numPasses <= 0 || len(segs) == 0 {
		return make([]int32, maxInt(0, w*h))
	}
	t := &jpxT1{w: w, h: h, kind: kind, causal: cbStyle&0x08 != 0}
	n := w * h
	t.mag = make([]int32, n)
	t.sign = make([]uint8, n)
	t.sig = make([]uint8, n)
	t.vis = make([]uint8, n)
	t.refined = make([]uint8, n)
	t.cx = make([]mqState, 19)
	t.initContexts()

	segSym := cbStyle&0x20 != 0
	reset := cbStyle&0x02 != 0

	// Segments delimit arithmetically-terminated runs. Without termination on each
	// coding pass (0x04), the whole code-block is one continuous MQ run even when
	// its bytes arrive split across quality layers — so concatenate those per-layer
	// contributions and decode them under a single MQ decoder. Restarting the coder
	// at every layer boundary corrupts every pass after the first layer (the source
	// of the multi-layer noise).
	if cbStyle&0x04 == 0 && len(segs) > 1 {
		var merged []byte
		total := 0
		for _, s := range segs {
			merged = append(merged, s.data...)
			total += s.passes
		}
		segs = []jpxSeg{{data: merged, passes: total}}
	}

	// Build the pass schedule: the top bit-plane has only a cleanup pass; each
	// lower bit-plane has significance-propagation, magnitude-refinement, cleanup.
	type passSpec struct{ kind, bp int }
	var passes []passSpec
	bp := bpStart
	passes = append(passes, passSpec{passCleanup, bp})
	bp--
	for len(passes) < numPasses && bp >= 0 {
		passes = append(passes, passSpec{passSigProp, bp})
		if len(passes) >= numPasses {
			break
		}
		passes = append(passes, passSpec{passMagRef, bp})
		if len(passes) >= numPasses {
			break
		}
		passes = append(passes, passSpec{passCleanup, bp})
		bp--
	}
	if len(passes) > numPasses {
		passes = passes[:numPasses]
	}

	schedIdx := 0
	for _, seg := range segs {
		t.dec = newMQDecoder(seg.data, 0, len(seg.data))
		for p := 0; p < seg.passes && schedIdx < len(passes); p++ {
			ps := passes[schedIdx]
			schedIdx++
			if reset {
				t.initContexts()
			}
			switch ps.kind {
			case passSigProp:
				t.clearVisited()
				t.sigProp(ps.bp)
			case passMagRef:
				t.magRef(ps.bp)
			case passCleanup:
				t.cleanup(ps.bp)
				if segSym {
					// Segmentation symbol (0xA) via the uniform context, for error
					// resilience; decoded and discarded.
					for k := 0; k < 4; k++ {
						t.dec.decode(t.cx, jpxCtxUni)
					}
				}
			}
		}
	}

	out := make([]int32, n)
	for i := 0; i < n; i++ {
		if t.sign[i] == 1 {
			out[i] = -t.mag[i]
		} else {
			out[i] = t.mag[i]
		}
	}
	return out
}

type jpxT1 struct {
	w, h, kind int
	causal     bool
	mag        []int32
	sign       []uint8
	sig        []uint8
	vis        []uint8
	refined    []uint8
	cx         []mqState
	dec        *mqDecoder
}

// initContexts sets the MQ contexts to their tier-1 initial states (T.800 D.3).
func (t *jpxT1) initContexts() {
	for i := range t.cx {
		t.cx[i] = 0
	}
	t.cx[0] = 4 << 1 // zero-coding context 0 starts at (4,0)
	t.cx[jpxCtxRun] = 3 << 1
	t.cx[jpxCtxUni] = 46 << 1
}

func (t *jpxT1) clearVisited() {
	for i := range t.vis {
		t.vis[i] = 0
	}
}

func (t *jpxT1) sigAt(x, y int) int {
	if x < 0 || x >= t.w || y < 0 || y >= t.h {
		return 0
	}
	return int(t.sig[y*t.w+x])
}

// neighbourSums returns the significant-neighbour counts: horizontal (left+right),
// vertical (top+bottom) and diagonal (four corners). Under vertically-causal
// context formation the row below is ignored when it belongs to the next stripe
// (T.800 D.7).
func (t *jpxT1) neighbourSums(x, y int) (hs, vs, ds int) {
	hs = t.sigAt(x-1, y) + t.sigAt(x+1, y)
	vs = t.sigAt(x, y-1)
	ds = t.sigAt(x-1, y-1) + t.sigAt(x+1, y-1)
	if !(t.causal && y%4 == 3) {
		vs += t.sigAt(x, y+1)
		ds += t.sigAt(x-1, y+1) + t.sigAt(x+1, y+1)
	}
	return
}

// zcContext computes the zero-coding (significance) context label (T.800 D.1/D.3).
func (t *jpxT1) zcContext(x, y int) int {
	hs, vs, ds := t.neighbourSums(x, y)
	switch t.kind {
	case 1: // HL: exchange horizontal and vertical
		hs, vs = vs, hs
		fallthrough
	case 0, 2: // LL, LH (and HL after the swap)
		if hs == 2 {
			return 8
		}
		if hs == 1 {
			if vs >= 1 {
				return 7
			}
			if ds >= 1 {
				return 6
			}
			return 5
		}
		if vs == 2 {
			return 4
		}
		if vs == 1 {
			return 3
		}
		if ds >= 2 {
			return 2
		}
		if ds == 1 {
			return 1
		}
		return 0
	default: // HH
		hv := hs + vs
		if ds >= 3 {
			return 8
		}
		if ds == 2 {
			if hv >= 1 {
				return 7
			}
			return 6
		}
		if ds == 1 {
			if hv >= 2 {
				return 5
			}
			if hv == 1 {
				return 4
			}
			return 3
		}
		if hv >= 2 {
			return 2
		}
		if hv == 1 {
			return 1
		}
		return 0
	}
}

// signContribution returns the signed significance of a neighbour: +1 positive,
// -1 negative, 0 insignificant.
func (t *jpxT1) signContribution(x, y int) int {
	if t.sigAt(x, y) == 0 {
		return 0
	}
	if t.sign[y*t.w+x] == 1 {
		return -1
	}
	return 1
}

// decodeSign decodes the sign of a newly significant coefficient (T.800 D.2),
// returning 1 for negative.
func (t *jpxT1) decodeSign(x, y int) uint8 {
	hc := clampInt(t.signContribution(x-1, y)+t.signContribution(x+1, y), -1, 1)
	vc := clampInt(t.signContribution(x, y-1)+t.signContribution(x, y+1), -1, 1)
	ctx, xorBit := scContext(hc, vc)
	bit := t.dec.decode(t.cx, ctx)
	return uint8(bit ^ xorBit)
}

// scContext maps the horizontal/vertical sign contributions to a context and a
// flip bit (T.800 Table D.2). Contexts 9–13.
func scContext(hc, vc int) (ctx, xorBit int) {
	switch {
	case hc == 1 && vc == 1:
		return 13, 0
	case hc == 1 && vc == 0:
		return 12, 0
	case hc == 1 && vc == -1:
		return 11, 0
	case hc == 0 && vc == 1:
		return 10, 0
	case hc == 0 && vc == 0:
		return 9, 0
	case hc == 0 && vc == -1:
		return 10, 1
	case hc == -1 && vc == 1:
		return 11, 1
	case hc == -1 && vc == 0:
		return 12, 1
	default: // (-1,-1)
		return 13, 1
	}
}

// setSignificant marks (x,y) significant at bit-plane bp and decodes its sign.
// The magnitude is reconstructed to the bit-plane midpoint (one-and-a-half of the
// bit-plane weight), doubling the true coefficient; the caller halves it after all
// passes (T.800; OpenJPEG's oneplushalf convention).
func (t *jpxT1) setSignificant(x, y, bp int) {
	i := y*t.w + x
	t.sig[i] = 1
	t.mag[i] = (int32(1) << uint(bp+1)) | (int32(1) << uint(bp))
	t.sign[i] = t.decodeSign(x, y)
}

// sigProp is the significance propagation pass (T.800 D.3.1).
func (t *jpxT1) sigProp(bp int) {
	for y0 := 0; y0 < t.h; y0 += 4 {
		for x := 0; x < t.w; x++ {
			for dy := 0; dy < 4 && y0+dy < t.h; dy++ {
				y := y0 + dy
				i := y*t.w + x
				if t.sig[i] != 0 {
					continue
				}
				hs, vs, ds := t.neighbourSums(x, y)
				if hs+vs+ds == 0 {
					continue // no significant neighbour: left to the cleanup pass
				}
				t.vis[i] = 1
				if t.dec.decode(t.cx, t.zcContext(x, y)) == 1 {
					t.setSignificant(x, y, bp)
				}
			}
		}
	}
}

// magRef is the magnitude refinement pass (T.800 D.3.2).
func (t *jpxT1) magRef(bp int) {
	for y0 := 0; y0 < t.h; y0 += 4 {
		for x := 0; x < t.w; x++ {
			for dy := 0; dy < 4 && y0+dy < t.h; dy++ {
				y := y0 + dy
				i := y*t.w + x
				if t.sig[i] == 0 || t.vis[i] != 0 {
					continue
				}
				ctx := 16
				if t.refined[i] == 0 {
					hs, vs, ds := t.neighbourSums(x, y)
					if hs+vs+ds > 0 {
						ctx = 15
					} else {
						ctx = 14
					}
				}
				// Refinement moves the reconstructed value up or down by half the
				// bit-plane weight (in the doubled magnitude space).
				half := int32(1) << uint(bp)
				if t.dec.decode(t.cx, ctx) == 1 {
					t.mag[i] += half
				} else {
					t.mag[i] -= half
				}
				t.refined[i] = 1
			}
		}
	}
}

// cleanup is the cleanup pass with run-length coding (T.800 D.3.3).
func (t *jpxT1) cleanup(bp int) {
	for y0 := 0; y0 < t.h; y0 += 4 {
		for x := 0; x < t.w; x++ {
			dy := 0
			// Run-length: a full stripe of 4 coefficients, none significant,
			// none visited, and none with a significant neighbour.
			if y0+4 <= t.h && t.runEligible(x, y0) {
				if t.dec.decode(t.cx, jpxCtxRun) == 0 {
					continue // all four stay insignificant
				}
				pos := (t.dec.decode(t.cx, jpxCtxUni) << 1) | t.dec.decode(t.cx, jpxCtxUni)
				t.setSignificant(x, y0+pos, bp)
				dy = pos + 1
			}
			for ; dy < 4 && y0+dy < t.h; dy++ {
				y := y0 + dy
				i := y*t.w + x
				if t.sig[i] != 0 || t.vis[i] != 0 {
					continue
				}
				if t.dec.decode(t.cx, t.zcContext(x, y)) == 1 {
					t.setSignificant(x, y, bp)
				}
			}
		}
	}
}

// runEligible reports whether the four coefficients of a stripe column qualify
// for run-length cleanup coding.
func (t *jpxT1) runEligible(x, y0 int) bool {
	for dy := 0; dy < 4; dy++ {
		y := y0 + dy
		i := y*t.w + x
		if t.sig[i] != 0 || t.vis[i] != 0 {
			return false
		}
		hs, vs, ds := t.neighbourSums(x, y)
		if hs+vs+ds != 0 {
			return false
		}
	}
	return true
}
