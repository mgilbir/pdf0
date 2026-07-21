package pdf0

// JPEG 2000 packet decoding (ISO/IEC 15444-1 §B.10): tag trees and the
// bit-stuffed packet-header stream that assigns coded bytes to code-blocks. A
// packet belongs to one (resolution, layer, component, precinct); its header
// signals, per code-block, first-inclusion (via an inclusion tag tree), the
// number of leading all-zero bit-planes (via a second tag tree), the number of
// coding passes, and the byte length, after which the body carries the bytes.

// jpxTagTree is a hierarchical tag tree (B.10.2). levels[0] is the leaf level;
// each higher level halves the dimensions until a 1×1 root.
type jpxTagTree struct {
	levels []jpxTagLevel
}

type jpxTagLevel struct {
	w, h  int
	value []int
	final []bool
}

func newTagTree(w, h int) *jpxTagTree {
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}
	t := &jpxTagTree{}
	for {
		t.levels = append(t.levels, jpxTagLevel{
			w: w, h: h,
			value: make([]int, w*h),
			final: make([]bool, w*h),
		})
		if w == 1 && h == 1 {
			break
		}
		w = (w + 1) / 2
		h = (h + 1) / 2
	}
	return t
}

// decode returns the tag-tree value for leaf (i,j), reading bits until the value
// is finalized or reaches threshold (B.10.2). A finalized value ≤ threshold-1 is
// exact; otherwise the value equals threshold (not yet determined this pass).
func (t *jpxTagTree) decode(r *jpxPacketReader, i, j, threshold int) int {
	n := len(t.levels)
	// Index of (i,j) at each level.
	idx := make([]int, n)
	ci, cj := i, j
	for lvl := 0; lvl < n; lvl++ {
		idx[lvl] = cj*t.levels[lvl].w + ci
		ci >>= 1
		cj >>= 1
	}
	parentVal := 0
	for lvl := n - 1; lvl >= 0; lvl-- {
		lv := &t.levels[lvl]
		k := idx[lvl]
		if lv.value[k] < parentVal {
			lv.value[k] = parentVal
		}
		for !lv.final[k] && lv.value[k] < threshold {
			if r.bit() == 1 {
				lv.final[k] = true
			} else {
				lv.value[k]++
			}
		}
		parentVal = lv.value[k]
	}
	return parentVal
}

// jpxPacketReader reads a tile's coded stream: a bit-stuffed bit reader for
// packet headers (a 0-bit is stuffed after every 0xFF) and a byte reader for
// packet bodies, sharing one byte cursor.
type jpxPacketReader struct {
	data   []byte
	pos    int
	cur    int
	bitsN  int
	prevFF bool
}

func (r *jpxPacketReader) bit() int {
	if r.bitsN == 0 {
		var b int
		if r.pos < len(r.data) {
			b = int(r.data[r.pos])
		} else {
			b = 0
		}
		r.pos++
		if r.prevFF {
			r.bitsN = 7 // the top bit is a stuffed 0
		} else {
			r.bitsN = 8
		}
		r.cur = b
		r.prevFF = b == 0xFF
	}
	r.bitsN--
	return (r.cur >> uint(r.bitsN)) & 1
}

func (r *jpxPacketReader) bits(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		v = (v << 1) | r.bit()
	}
	return v
}

// alignByte moves to the next byte boundary at the end of a packet header. The
// stuffed 0 bit that follows a 0xFF is already consumed while reading (bit sets
// bitsN=7 after a 0xFF), so alignment only needs to discard the partial byte.
func (r *jpxPacketReader) alignByte() {
	r.bitsN = 0
	r.prevFF = false
}

// readPasses decodes the number of coding passes for a code-block (B.10.6,
// Table B.4).
func (r *jpxPacketReader) readPasses() int {
	if r.bit() == 0 {
		return 1
	}
	if r.bit() == 0 {
		return 2
	}
	if v := r.bits(2); v != 3 {
		return 3 + v
	}
	if v := r.bits(5); v != 31 {
		return 6 + v
	}
	return 37 + r.bits(7)
}

// decodeTilePackets walks the packets of one tile across all its components in
// the coding progression, filling in each code-block's passes, zero-bit-planes
// and coded data. The tile data interleaves the components' packets, so they must
// be decoded together from one reader. Only the baseline (LRCP/RLCP progressions,
// single precinct per resolution) is handled.
func decodeTilePackets(im *jpxImage, tcs []*jpxTileComp, data []byte) error {
	order, err := tilePacketOrder(im, tcs)
	if err != nil {
		return err
	}
	r := &jpxPacketReader{data: data}
	for _, o := range order {
		if o[1] < len(tcs[o[0]].resolutions) {
			if err := readPacket(im, tcs[o[0]].resolutions[o[1]], o[3], o[2], r); err != nil {
				return err
			}
		}
	}
	return nil
}

// tilePacketOrder builds the sequence of (component, resolution, layer, precinct)
// packets for a tile in coding order (T.800 B.12), honouring POC stages when
// present.
func tilePacketOrder(im *jpxImage, tcs []*jpxTileComp) ([][4]int, error) {
	layers := im.cod.layers
	if layers < 1 {
		layers = 1
	}
	numRes := 0
	for _, tc := range tcs {
		if len(tc.resolutions) > numRes {
			numRes = len(tc.resolutions)
		}
	}
	nc := len(tcs)
	nprec := func(c, res int) int {
		if res >= len(tcs[c].resolutions) {
			return 0
		}
		rr := tcs[c].resolutions[res]
		return rr.pw * rr.ph
	}
	// Build the packet sequence as (component, resolution, layer, precinct) tuples.
	// When a POC marker is present its stages define the progression (each over a
	// resolution/component/layer sub-range); otherwise the single COD progression
	// covers everything. A packet is emitted at most once even if stages overlap.
	var order [][4]int
	seen := make(map[[4]int]bool)
	push := func(c, res, layer, prec int) {
		k := [4]int{c, res, layer, prec}
		if !seen[k] {
			seen[k] = true
			order = append(order, k)
		}
	}
	emit := func(prog, rs, re, cs, ce, lye int) error {
		if re > numRes {
			re = numRes
		}
		if ce > nc {
			ce = nc
		}
		if lye > layers {
			lye = layers
		}
		switch prog {
		case 0: // LRCP: layer, resolution, component, precinct
			for layer := 0; layer < lye; layer++ {
				for res := rs; res < re; res++ {
					for c := cs; c < ce; c++ {
						for p := 0; p < nprec(c, res); p++ {
							push(c, res, layer, p)
						}
					}
				}
			}
		case 1: // RLCP: resolution, layer, component, precinct
			for res := rs; res < re; res++ {
				for layer := 0; layer < lye; layer++ {
					for c := cs; c < ce; c++ {
						for p := 0; p < nprec(c, res); p++ {
							push(c, res, layer, p)
						}
					}
				}
			}
		default:
			// RPCL / PCRL / CPRL iterate by spatial precinct position.
			return emitPositional(im, tcs, prog, rs, re, cs, ce, lye, push)
		}
		return nil
	}
	if len(im.poc) > 0 {
		for _, s := range im.poc {
			if err := emit(s.prog, s.resStart, s.resEnd, s.compStart, s.compEnd, s.layerEnd); err != nil {
				return nil, err
			}
		}
	} else if err := emit(im.cod.progOrder, 0, numRes, 0, nc, layers); err != nil {
		return nil, err
	}
	return order, nil
}

// emitPositional generates the packet order for the position progressions
// RPCL (2), PCRL (3) and CPRL (4), which iterate over spatial precinct positions
// on the reference grid (T.800 B.12.1.3–5; mirrors OpenJPEG opj_pi_next_rpcl/
// pcrl/cprl). The three differ only in loop nesting around the shared (position →
// component/resolution → precinct) mapping.
func emitPositional(im *jpxImage, tcs []*jpxTileComp, prog, rs, re, cs, ce, lye int, push func(c, res, layer, prec int)) error {
	if len(tcs) == 0 {
		return errJPX
	}
	tx0, ty0 := tcs[0].refX0, tcs[0].refY0
	tx1, ty1 := tcs[0].refX1, tcs[0].refY1
	numRes := 0
	for _, tc := range tcs {
		if len(tc.resolutions) > numRes {
			numRes = len(tc.resolutions)
		}
	}
	if re > numRes {
		re = numRes
	}
	if ce > len(tcs) {
		ce = len(tcs)
	}
	if lye > im.cod.layers {
		lye = im.cod.layers
	}
	// stepXY returns the reference-grid precinct step for one (component,
	// resolution): the precinct size scaled up by the resolution reduction and the
	// component sub-sampling.
	stepXY := func(c, res int) (int, int) {
		tc := tcs[c]
		if res >= len(tc.resolutions) {
			return 0, 0
		}
		comp := im.comps[c]
		levels := len(tc.resolutions) - 1
		levelno := levels - res
		pdx, pdy := im.precinctExp(res)
		return comp.dx << (pdx + levelno), comp.dy << (pdy + levelno)
	}
	// precAt maps a reference position (x,y) to a precinct index for (c,res), or
	// returns (-1) when the position is not the top-left of a precinct there.
	precAt := func(c, res, x, y int) int {
		tc := tcs[c]
		if res >= len(tc.resolutions) {
			return -1
		}
		rr := tc.resolutions[res]
		if rr.pw == 0 || rr.ph == 0 {
			return -1
		}
		comp := im.comps[c]
		levels := len(tc.resolutions) - 1
		levelno := levels - res
		pdx, pdy := im.precinctExp(res)
		trx0 := ceilDiv(tx0, comp.dx<<levelno)
		try0 := ceilDiv(ty0, comp.dy<<levelno)
		trx1 := ceilDiv(tx1, comp.dx<<levelno)
		try1 := ceilDiv(ty1, comp.dy<<levelno)
		if trx0 == trx1 || try0 == try1 {
			return -1
		}
		rpx := pdx + levelno
		rpy := pdy + levelno
		// The position contributes to this resolution only at precinct boundaries
		// (or at the tile origin when the origin is not itself precinct-aligned).
		if !(x%(comp.dx<<rpx) == 0 || (x == tx0 && (trx0<<levelno)%(1<<rpx) != 0)) {
			return -1
		}
		if !(y%(comp.dy<<rpy) == 0 || (y == ty0 && (try0<<levelno)%(1<<rpy) != 0)) {
			return -1
		}
		prci := floorPow2(ceilDiv(x, comp.dx<<levelno), pdx) - floorPow2(trx0, pdx)
		prcj := floorPow2(ceilDiv(y, comp.dy<<levelno), pdy) - floorPow2(try0, pdy)
		p := prcj*rr.pw + prci
		if p < 0 || p >= rr.pw*rr.ph {
			return -1
		}
		return p
	}
	// forEachPosition walks the reference grid with the smallest precinct step over
	// the given resolution/component ranges, invoking fn(x,y).
	forEachPosition := func(resLo, resHi, cLo, cHi int, fn func(x, y int)) {
		dx, dy := 0, 0
		for res := resLo; res < resHi; res++ {
			for c := cLo; c < cHi; c++ {
				sx, sy := stepXY(c, res)
				if sx > 0 && (dx == 0 || sx < dx) {
					dx = sx
				}
				if sy > 0 && (dy == 0 || sy < dy) {
					dy = sy
				}
			}
		}
		if dx == 0 || dy == 0 {
			return
		}
		for y := ty0; y < ty1; y += dy - (y % dy) {
			for x := tx0; x < tx1; x += dx - (x % dx) {
				fn(x, y)
			}
		}
	}
	emitCRL := func(c, res, x, y int) { // component,resolution fixed → precinct,layer
		if p := precAt(c, res, x, y); p >= 0 {
			for l := 0; l < lye; l++ {
				push(c, res, l, p)
			}
		}
	}
	switch prog {
	case 2: // RPCL: resolution, position, component, layer
		for res := rs; res < re; res++ {
			forEachPosition(res, res+1, cs, ce, func(x, y int) {
				for c := cs; c < ce; c++ {
					emitCRL(c, res, x, y)
				}
			})
		}
	case 3: // PCRL: position, component, resolution, layer
		forEachPosition(rs, re, cs, ce, func(x, y int) {
			for c := cs; c < ce; c++ {
				for res := rs; res < re; res++ {
					emitCRL(c, res, x, y)
				}
			}
		})
	case 4: // CPRL: component, position, resolution, layer
		for c := cs; c < ce; c++ {
			forEachPosition(rs, re, c, c+1, func(x, y int) {
				for res := rs; res < re; res++ {
					emitCRL(c, res, x, y)
				}
			})
		}
	default:
		return errJPX
	}
	return nil
}

func readPacket(im *jpxImage, res *jpxResolution, precNo, layer int, r *jpxPacketReader) error {
	if precNo >= len(res.precincts) {
		return nil
	}
	if im.cod.sop {
		// Optional SOP marker (0xFF91, 6 bytes) precedes the packet.
		if r.pos+2 <= len(r.data) && r.data[r.pos] == 0xFF && r.data[r.pos+1] == 0x91 {
			r.pos += 6
		}
	}
	present := r.bit()
	if present == 0 {
		r.alignByte()
		skipEPH(im, r)
		return nil
	}
	// Each contributing code-block adds one or more arithmetic segments: normally
	// one per packet (layer) contribution, or one per pass under termination.
	type contrib struct {
		cb        *jpxCodeblock
		segLens   []int
		segPasses []int
	}
	var contribs []contrib
	for _, bp := range res.precincts[precNo].bands {
		for _, cb := range bp.blocks {
			var included bool
			if cb.included {
				included = r.bit() == 1
			} else {
				v := bp.inclTree.decode(r, cb.lx, cb.ly, layer+1)
				included = v <= layer
				if included {
					cb.included = true
					cb.zeroBitPlanes = bp.zbpTree.decode(r, cb.lx, cb.ly, 1<<30)
					cb.lblock = 3
				}
			}
			if !included {
				continue
			}
			passes := r.readPasses()
			for r.bit() == 1 {
				cb.lblock++
			}
			cb.numPasses += passes
			if im.cod.cbStyle&0x04 != 0 {
				// Termination on each pass: one length per pass (each pass is its
				// own segment, so floor(log2(1)) = 0 extra length bits).
				c := contrib{cb: cb}
				for p := 0; p < passes; p++ {
					c.segLens = append(c.segLens, r.bits(cb.lblock))
					c.segPasses = append(c.segPasses, 1)
				}
				contribs = append(contribs, c)
			} else {
				// One segment carrying all the contribution's passes.
				length := r.bits(cb.lblock + intLog2(passes))
				contribs = append(contribs, contrib{cb, []int{length}, []int{passes}})
			}
		}
	}
	r.alignByte()
	skipEPH(im, r)
	// Packet body: the coded bytes for each code-block's segments, in order.
	for _, c := range contribs {
		for i, L := range c.segLens {
			if r.pos+L > len(r.data) {
				return errJPX
			}
			c.cb.segs = append(c.cb.segs, jpxSeg{data: r.data[r.pos : r.pos+L], passes: c.segPasses[i]})
			r.pos += L
		}
	}
	return nil
}

func skipEPH(im *jpxImage, r *jpxPacketReader) {
	if im.cod.eph && r.pos+2 <= len(r.data) && r.data[r.pos] == 0xFF && r.data[r.pos+1] == 0x92 {
		r.pos += 2
	}
}

func intLog2(n int) int {
	l := 0
	for (1 << (l + 1)) <= n {
		l++
	}
	return l
}