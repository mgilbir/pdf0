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

// alignByte discards any partial header bits and consumes the stuffed 0 bit that
// follows a trailing 0xFF (B.10.1).
func (r *jpxPacketReader) alignByte() {
	if r.prevFF && r.bitsN == 8 {
		// A fresh 0xFF at a byte boundary still owes its stuff bit.
		r.pos++
	}
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
	r := &jpxPacketReader{data: data}
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
	one := func(c, res, layer int) error {
		if res < len(tcs[c].resolutions) {
			return readPacket(im, tcs[c].resolutions[res], layer, r)
		}
		return nil
	}
	nc := len(tcs)
	// The five progression orders differ only in loop nesting. With one precinct
	// per resolution the position/precinct dimension is trivial, so RPCL, PCRL and
	// CPRL reduce to the orderings below.
	var order [][3]int // sequence of (component, resolution, layer)
	push := func(c, res, layer int) { order = append(order, [3]int{c, res, layer}) }
	switch im.cod.progOrder {
	case 0: // LRCP
		for layer := 0; layer < layers; layer++ {
			for res := 0; res < numRes; res++ {
				for c := 0; c < nc; c++ {
					push(c, res, layer)
				}
			}
		}
	case 1: // RLCP
		for res := 0; res < numRes; res++ {
			for layer := 0; layer < layers; layer++ {
				for c := 0; c < nc; c++ {
					push(c, res, layer)
				}
			}
		}
	default:
		// RPCL/PCRL/CPRL iterate by precinct position across resolutions, which
		// the maximal-precinct simplification here does not reproduce correctly;
		// decline rather than mis-assign packets.
		return errJPX
	}
	for _, o := range order {
		if err := one(o[0], o[1], o[2]); err != nil {
			return err
		}
	}
	return nil
}

func readPacket(im *jpxImage, res *jpxResolution, layer int, r *jpxPacketReader) error {
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
	type contrib struct {
		cb     *jpxCodeblock
		length int
	}
	var contribs []contrib
	for _, sb := range res.subbands {
		for bi := range sb.blocks {
			cb := &sb.blocks[bi]
			col := bi % sb.numXcb
			row := bi / sb.numXcb
			var included bool
			if cb.included {
				included = r.bit() == 1
			} else {
				v := sb.inclTree.decode(r, col, row, layer+1)
				included = v <= layer
				if included {
					cb.included = true
					cb.zeroBitPlanes = sb.zbpTree.decode(r, col, row, 1<<30)
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
			nbits := cb.lblock + intLog2(passes)
			length := r.bits(nbits)
			cb.numPasses += passes
			contribs = append(contribs, contrib{cb, length})
		}
	}
	r.alignByte()
	skipEPH(im, r)
	// Packet body: the coded bytes for each contributing code-block, in order.
	for _, c := range contribs {
		if r.pos+c.length > len(r.data) {
			return errJPX
		}
		c.cb.data = append(c.cb.data, r.data[r.pos:r.pos+c.length]...)
		r.pos += c.length
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
