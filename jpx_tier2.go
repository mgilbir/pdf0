package pdf0

// JPEG 2000 tier-2 decoding (ISO/IEC 15444-1 §B): the geometry of a tile —
// resolution levels, subbands, precincts and code-blocks — and the packet
// headers that say, for each quality layer, which code-blocks contribute, how
// many bit-plane passes they carry, and how many bytes. Packet headers are a
// bit-stuffed stream carrying two tag trees per precinct-subband (code-block
// inclusion and the count of leading all-zero bit-planes).
//
// Precincts partition each resolution; a packet belongs to one
// (resolution, layer, component, precinct). Maximal precincts (the default,
// exponent 15) reduce to one precinct covering the whole resolution.

// jpxCodeblock is one code-block: its position in the subband, its index within
// the precinct's tag-tree grid, the coded data gathered from packets, and the
// tier-2 state carried between layers.
type jpxCodeblock struct {
	x0, y0, x1, y1 int // coordinates within the subband
	lx, ly         int // index within the owning precinct-band tag-tree grid
	included       bool
	zeroBitPlanes  int
	lblock         int
	numPasses      int
	segs           []jpxSeg
}

// jpxSeg is one arithmetic codeword segment: its coded bytes and the number of
// coding passes it carries. A fresh MQ decoder is used for each segment.
type jpxSeg struct {
	data   []byte
	passes int
}

// jpxSubband is one subband (LL/HL/LH/HH) of a resolution level. It owns every
// code-block for coefficient assembly; the precincts reference the same blocks
// for packet reading.
type jpxSubband struct {
	kind           int // 0 LL, 1 HL, 2 LH, 3 HH
	x0, y0, x1, y1 int
	blocks         []*jpxCodeblock
	gain           int
	exp            int
	mant           int
	coeffs         []int32
}

// jpxBandPrec is one subband's slice inside one precinct: its code-blocks and the
// two tag trees sized to that slice's code-block grid.
type jpxBandPrec struct {
	sb       *jpxSubband
	blocks   []*jpxCodeblock
	cw, ch   int
	inclTree *jpxTagTree
	zbpTree  *jpxTagTree
}

// jpxPrecinct groups the code-blocks of every subband of a resolution that fall
// in one spatial partition.
type jpxPrecinct struct {
	bands []*jpxBandPrec // parallel to jpxResolution.subbands
}

// jpxResolution is one resolution level of a tile-component.
type jpxResolution struct {
	level          int
	x0, y0, x1, y1 int
	subbands       []*jpxSubband
	pw, ph         int // precinct grid dimensions
	precincts      []*jpxPrecinct
}

// jpxTileComp holds the decoded structure for one component of one tile.
type jpxTileComp struct {
	comp                   int
	tcx0, tcy0, tcx1, tcy1 int // component-grid tile bounds
	refX0, refY0, refX1, refY1 int // reference-grid tile bounds (for positional progressions)
	resolutions            []*jpxResolution
}

func ceilDivShift(x, n int) int { return (x + (1 << n) - 1) >> n }

// floorPow2 / ceilPow2 divide by 2^n rounding down / up (arithmetic shift, valid
// for the non-negative tile-component coordinates used here).
func floorPow2(x, n int) int { return x >> n }
func ceilPow2(x, n int) int  { return (x + (1 << n) - 1) >> n }

// buildTileComp computes the resolution/subband/precinct/code-block geometry for
// one component of the tile occupying [tx0,tx1)×[ty0,ty1) (T.800 B.6, B.7, B.15).
func buildTileComp(im *jpxImage, c, tx0, ty0, tx1, ty1 int) *jpxTileComp {
	comp := im.comps[c]
	tc := &jpxTileComp{
		comp: c,
		tcx0: ceilDiv(tx0, comp.dx), tcy0: ceilDiv(ty0, comp.dy),
		tcx1: ceilDiv(tx1, comp.dx), tcy1: ceilDiv(ty1, comp.dy),
		refX0: tx0, refY0: ty0, refX1: tx1, refY1: ty1,
	}
	nl := im.cod.levels
	cbExpW := intLog2(im.cod.cbW)
	cbExpH := intLog2(im.cod.cbH)
	for r := 0; r <= nl; r++ {
		nb := nl - r // decomposition sublevel for this resolution's box
		res := &jpxResolution{
			level: r,
			x0:    ceilDivShift(tc.tcx0, nb), y0: ceilDivShift(tc.tcy0, nb),
			x1: ceilDivShift(tc.tcx1, nb), y1: ceilDivShift(tc.tcy1, nb),
		}
		if r == 0 {
			res.subbands = []*jpxSubband{tc.newSubband(0, nl)}
		} else {
			bandLevel := nl - r + 1
			res.subbands = []*jpxSubband{
				tc.newSubband(1, bandLevel), // HL
				tc.newSubband(2, bandLevel), // LH
				tc.newSubband(3, bandLevel), // HH
			}
		}
		buildPrecincts(im, res, r, cbExpW, cbExpH)
		tc.resolutions = append(tc.resolutions, res)
	}
	assignQuant(im, tc)
	return tc
}

// buildPrecincts partitions a resolution into precincts and each precinct's
// subbands into code-blocks with tag trees (T.800 B.6, B.7; mirrors OpenJPEG
// opj_tcd_init_tile).
func buildPrecincts(im *jpxImage, res *jpxResolution, r, cbExpW, cbExpH int) {
	pdx, pdy := im.precinctExp(r)
	tlPrcX := floorPow2(res.x0, pdx) << pdx
	tlPrcY := floorPow2(res.y0, pdy) << pdy
	brPrcX := ceilPow2(res.x1, pdx) << pdx
	brPrcY := ceilPow2(res.y1, pdy) << pdy
	if res.x1 > res.x0 {
		res.pw = (brPrcX - tlPrcX) >> pdx
	}
	if res.y1 > res.y0 {
		res.ph = (brPrcY - tlPrcY) >> pdy
	}
	// Code-block-group (precinct-in-band) exponents: full at resolution 0, halved
	// for the detail resolutions whose subbands sit at half the grid.
	cbgExpW, cbgExpH := pdx, pdy
	tlCbgX, tlCbgY := tlPrcX, tlPrcY
	if r > 0 {
		cbgExpW, cbgExpH = pdx-1, pdy-1
		tlCbgX = ceilPow2(tlPrcX, 1)
		tlCbgY = ceilPow2(tlPrcY, 1)
	}
	cw := minInt(cbExpW, cbgExpW)
	ch := minInt(cbExpH, cbgExpH)

	res.precincts = make([]*jpxPrecinct, res.pw*res.ph)
	for p := range res.precincts {
		px, py := p%res.pw, p/res.pw
		cbgX0 := tlCbgX + px*(1<<cbgExpW)
		cbgY0 := tlCbgY + py*(1<<cbgExpH)
		cbgX1 := cbgX0 + (1 << cbgExpW)
		cbgY1 := cbgY0 + (1 << cbgExpH)
		prec := &jpxPrecinct{bands: make([]*jpxBandPrec, len(res.subbands))}
		for bi, sb := range res.subbands {
			prec.bands[bi] = buildBandPrec(sb, cbgX0, cbgY0, cbgX1, cbgY1, cw, ch)
		}
		res.precincts[p] = prec
	}
}

// buildBandPrec builds the code-blocks and tag trees for one subband within one
// precinct's code-block-group region [gx0,gy0,gx1,gy1).
func buildBandPrec(sb *jpxSubband, gx0, gy0, gx1, gy1, cw, ch int) *jpxBandPrec {
	bp := &jpxBandPrec{sb: sb}
	// Precinct region clipped to the band.
	px0 := maxInt(gx0, sb.x0)
	py0 := maxInt(gy0, sb.y0)
	px1 := minInt(gx1, sb.x1)
	py1 := minInt(gy1, sb.y1)
	if px1 <= px0 || py1 <= py0 {
		return bp // empty precinct-band
	}
	cbx0 := floorPow2(px0, cw)
	cby0 := floorPow2(py0, ch)
	cbx1 := ceilPow2(px1, cw)
	cby1 := ceilPow2(py1, ch)
	bp.cw = cbx1 - cbx0
	bp.ch = cby1 - cby0
	for j := cby0; j < cby1; j++ {
		for i := cbx0; i < cbx1; i++ {
			cb := &jpxCodeblock{
				x0: maxInt(sb.x0, i<<cw), y0: maxInt(sb.y0, j<<ch),
				x1: minInt(sb.x1, (i+1)<<cw), y1: minInt(sb.y1, (j+1)<<ch),
				lx: i - cbx0, ly: j - cby0,
			}
			if cb.x1 <= cb.x0 || cb.y1 <= cb.y0 {
				continue
			}
			bp.blocks = append(bp.blocks, cb)
			sb.blocks = append(sb.blocks, cb)
		}
	}
	bp.inclTree = newTagTree(bp.cw, bp.ch)
	bp.zbpTree = newTagTree(bp.cw, bp.ch)
	return bp
}

// assignQuant fills each subband's quantization parameters, in the QCD ordering
// (the LL of the coarsest resolution, then HL/LH/HH from coarsest to finest).
func assignQuant(im *jpxImage, tc *jpxTileComp) {
	bandIdx := 0
	for _, res := range tc.resolutions {
		for _, sb := range res.subbands {
			s := jpxStep{}
			if im.qcd.style == 1 && len(im.qcd.steps) > 0 {
				s = im.qcd.steps[0] // derived: scaled per level during dequant
			} else if bandIdx < len(im.qcd.steps) {
				s = im.qcd.steps[bandIdx]
			}
			sb.exp, sb.mant = s.exp, s.mant
			bandIdx++
		}
	}
}

// newSubband computes a subband's coordinates (T.800 B-15). kind: 0 LL, 1 HL,
// 2 LH, 3 HH; nb is the decomposition level of the band.
func (tc *jpxTileComp) newSubband(kind, nb int) *jpxSubband {
	xob, yob := kind&1, kind>>1
	half := 0
	if nb > 0 {
		half = 1 << (nb - 1)
	}
	x0 := ceilDivShift(tc.tcx0-half*xob, nb)
	y0 := ceilDivShift(tc.tcy0-half*yob, nb)
	x1 := ceilDivShift(tc.tcx1-half*xob, nb)
	y1 := ceilDivShift(tc.tcy1-half*yob, nb)
	sb := &jpxSubband{kind: kind, x0: x0, y0: y0, x1: x1, y1: y1}
	switch kind {
	case 1, 2:
		sb.gain = 1
	case 3:
		sb.gain = 2
	}
	return sb
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
