package pdf0

// JPEG 2000 tier-2 decoding (ISO/IEC 15444-1 §B): the geometry of a tile —
// resolution levels, subbands and code-blocks — and the packet headers that say,
// for each quality layer, which code-blocks contribute, how many bit-plane passes
// they carry, and how many bytes. Packet headers are a bit-stuffed stream
// carrying two tag trees per precinct-subband (code-block inclusion and the count
// of leading all-zero bit-planes).
//
// This milestone establishes the structure and parses packet headers, gathering
// each code-block's coded byte segments for tier-1. It covers the baseline the
// conformance files use: the LRCP and RLCP progressions, and maximal
// (whole-resolution) precincts.

// jpxCodeblock is one code-block: its position in the subband, the coded data
// gathered from packets, and the tier-2 state carried between layers.
type jpxCodeblock struct {
	x0, y0, x1, y1 int // coordinates within the subband
	included       bool
	zeroBitPlanes  int
	lblock         int
	numPasses      int
	data           []byte
}

// jpxSubband is one subband (LL/HL/LH/HH) of a resolution level.
type jpxSubband struct {
	kind           int // 0 LL, 1 HL, 2 LH, 3 HH
	x0, y0, x1, y1 int
	numXcb, numYcb int
	blocks         []jpxCodeblock
	inclTree       *jpxTagTree
	zbpTree        *jpxTagTree
	gain           int // log2 gain for dequant band ordering (0/1/1/2)
}

// jpxResolution is one resolution level of a tile-component.
type jpxResolution struct {
	level          int
	x0, y0, x1, y1 int
	subbands       []*jpxSubband
}

// jpxTileComp holds the decoded structure for one component of one tile.
type jpxTileComp struct {
	comp                   int
	tcx0, tcy0, tcx1, tcy1 int
	resolutions            []*jpxResolution
}

func ceilDivShift(x, n int) int { return (x + (1 << n) - 1) >> n }

// buildTileComp computes the resolution/subband/code-block geometry for one
// component of the tile occupying [tx0,tx1)×[ty0,ty1) (T.800 B.6, B.7, B.15).
func buildTileComp(im *jpxImage, c, tx0, ty0, tx1, ty1 int) *jpxTileComp {
	comp := im.comps[c]
	tc := &jpxTileComp{
		comp: c,
		tcx0: ceilDiv(tx0, comp.dx), tcy0: ceilDiv(ty0, comp.dy),
		tcx1: ceilDiv(tx1, comp.dx), tcy1: ceilDiv(ty1, comp.dy),
	}
	nl := im.cod.levels
	for r := 0; r <= nl; r++ {
		nb := nl - r // decomposition sublevel for this resolution's box
		res := &jpxResolution{
			level: r,
			x0:    ceilDivShift(tc.tcx0, nb), y0: ceilDivShift(tc.tcy0, nb),
			x1: ceilDivShift(tc.tcx1, nb), y1: ceilDivShift(tc.tcy1, nb),
		}
		if r == 0 {
			res.subbands = []*jpxSubband{tc.newSubband(im, 0, nl)}
		} else {
			bandLevel := nl - r + 1
			res.subbands = []*jpxSubband{
				tc.newSubband(im, 1, bandLevel), // HL
				tc.newSubband(im, 2, bandLevel), // LH
				tc.newSubband(im, 3, bandLevel), // HH
			}
		}
		tc.resolutions = append(tc.resolutions, res)
	}
	return tc
}

// newSubband computes a subband's coordinates (T.800 B-15) and code-block grid.
// kind: 0 LL, 1 HL, 2 LH, 3 HH; nb is the decomposition level of the band.
func (tc *jpxTileComp) newSubband(im *jpxImage, kind, nb int) *jpxSubband {
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
	sb.setupBlocks(im.cod.cbW, im.cod.cbH)
	return sb
}

// setupBlocks partitions the subband into code-blocks aligned to the code-block
// partition origin (0,0), and builds the two tag trees (T.800 B.7).
func (sb *jpxSubband) setupBlocks(cbw, cbh int) {
	if sb.x1 <= sb.x0 || sb.y1 <= sb.y0 {
		return
	}
	cbx0 := sb.x0 / cbw
	cby0 := sb.y0 / cbh
	cbx1 := ceilDiv(sb.x1, cbw)
	cby1 := ceilDiv(sb.y1, cbh)
	sb.numXcb = cbx1 - cbx0
	sb.numYcb = cby1 - cby0
	for j := cby0; j < cby1; j++ {
		for i := cbx0; i < cbx1; i++ {
			b := jpxCodeblock{
				x0: maxInt(sb.x0, i*cbw), y0: maxInt(sb.y0, j*cbh),
				x1: minInt(sb.x1, (i+1)*cbw), y1: minInt(sb.y1, (j+1)*cbh),
			}
			sb.blocks = append(sb.blocks, b)
		}
	}
	sb.inclTree = newTagTree(sb.numXcb, sb.numYcb)
	sb.zbpTree = newTagTree(sb.numXcb, sb.numYcb)
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
