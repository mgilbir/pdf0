package pdf0

// JBIG2 symbol dictionaries and text regions (ISO/IEC 14492 §6.4, §6.5). A
// symbol dictionary decodes a set of small bitmaps (the marks on a scanned page,
// typically glyphs); a text region places instances of those symbols at decoded
// positions to reconstruct the page. Both use the MQ arithmetic coder through
// the integer decoding procedures IADH/IADW/IAEX/… (Annex A) and the symbol-ID
// decoder IAID.
//
// This covers the arithmetic-coded path without symbol refinement or aggregation
// (SDREFAGG/SBREFINE) and without the Huffman-coded variants (SDHUFF/SBHUFF);
// segments needing those are reported unsupported so the image falls back to its
// raw bytes.

// newIAx allocates an integer-arithmetic context (Annex A: 512 states).
func newIAx() []mqState { return make([]mqState, 512) }

// decodeInt decodes one integer (Annex A.2). ok is false for the out-of-band
// (OOB) value, which terminates variable-length sequences.
func decodeInt(dec *mqDecoder, cx []mqState) (value int, ok bool) {
	prev := 1
	readBits := func(n int) int {
		v := 0
		for i := 0; i < n; i++ {
			bit := dec.decode(cx, prev)
			if prev < 256 {
				prev = (prev << 1) | bit
			} else {
				prev = ((((prev << 1) | bit) & 511) | 256)
			}
			v = (v << 1) | bit
		}
		return v
	}
	sign := readBits(1)
	var v int
	switch {
	case readBits(1) == 0:
		v = readBits(2)
	case readBits(1) == 0:
		v = readBits(4) + 4
	case readBits(1) == 0:
		v = readBits(6) + 20
	case readBits(1) == 0:
		v = readBits(8) + 84
	case readBits(1) == 0:
		v = readBits(12) + 340
	default:
		v = readBits(32) + 4436
	}
	if sign == 0 {
		return v, true
	}
	if v > 0 {
		return -v, true
	}
	return 0, false // negative zero = OOB
}

// decodeIAID decodes a symbol ID of codeLen bits (Annex A.3).
func decodeIAID(dec *mqDecoder, cx []mqState, codeLen int) int {
	prev := 1
	for i := 0; i < codeLen; i++ {
		bit := dec.decode(cx, prev)
		prev = (prev << 1) | bit
	}
	return prev - (1 << codeLen)
}

func ceilLog2(n int) int {
	l := 0
	for (1 << l) < n {
		l++
	}
	return l
}

// inputSymbols gathers the exported symbols of the symbol-dictionary segments
// this segment refers to, in reference order.
func (d *jbig2Decoder) inputSymbols(seg jbSegment) []*jbBitmap {
	var syms []*jbBitmap
	for _, ref := range seg.refs {
		if s, ok := d.symbols[ref]; ok {
			syms = append(syms, s...)
		}
	}
	return syms
}

// readSymbolDict decodes a symbol dictionary segment (type 0), storing its
// exported symbols under the segment number.
func (d *jbig2Decoder) readSymbolDict(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	flags, ok := r.u16()
	if !ok {
		return errJBIG2Unsupported
	}
	sdhuff := flags & 1
	sdrefagg := (flags >> 1) & 1
	template := int((flags >> 10) & 3)
	sdrTemplate := int((flags >> 12) & 1)
	if sdhuff != 0 {
		return errJBIG2Unsupported // Huffman-coded symbol dictionaries not yet supported
	}

	var at []atPixel
	n := 1
	if template == 0 {
		n = 4
	}
	for i := 0; i < n; i++ {
		ax, ok1 := r.s8()
		ay, ok2 := r.s8()
		if !ok1 || !ok2 {
			return errJBIG2Unsupported
		}
		at = append(at, atPixel{ax, ay})
	}

	// Refinement AT pixels follow the coding AT when aggregation uses template 0.
	var rAt []atPixel
	if sdrefagg != 0 && sdrTemplate == 0 {
		for i := 0; i < 2; i++ {
			ax, ok1 := r.s8()
			ay, ok2 := r.s8()
			if !ok1 || !ok2 {
				return errJBIG2Unsupported
			}
			rAt = append(rAt, atPixel{ax, ay})
		}
	}

	numEx, ok1 := r.u32()
	numNew, ok2 := r.u32()
	if !ok1 || !ok2 || numNew > 1<<20 || numEx > 1<<20 {
		return errJBIG2Unsupported
	}

	input := d.inputSymbols(seg)
	newSyms := make([]*jbBitmap, 0, numNew)

	dec := newMQDecoder(r.data[r.pos:], 0, r.remaining())
	iadh, iadw, iaex := newIAx(), newIAx(), newIAx()
	iaai, iardx, iardy := newIAx(), newIAx(), newIAx()
	gb := make([]mqState, 1<<16)
	grCx := make([]mqState, 1<<13)
	// Symbol-ID code length for aggregate/refinement references (input ++ new).
	refCodeLen := ceilLog2(int(numNew) + len(input))
	if refCodeLen < 1 {
		refCodeLen = 1
	}
	iaid := make([]mqState, 1<<uint(refCodeLen+1))

	hcHeight := 0
	for len(newSyms) < int(numNew) {
		dh, _ := decodeInt(dec, iadh)
		hcHeight += dh
		if hcHeight <= 0 || hcHeight > 1<<16 {
			return errJBIG2Unsupported
		}
		symWidth := 0
		for {
			dw, ok := decodeInt(dec, iadw)
			if !ok {
				break // OOB: end of height class
			}
			symWidth += dw
			if symWidth <= 0 || symWidth > 1<<16 || len(newSyms) >= int(numNew) {
				return errJBIG2Unsupported
			}
			var bmp *jbBitmap
			if sdrefagg == 0 {
				bmp = decodeGenericInto(dec, gb, symWidth, hcHeight, template, at, false, nil)
			} else {
				// Aggregate/refinement coding (6.5.8.2).
				nInst, _ := decodeInt(dec, iaai)
				if nInst != 1 {
					return errJBIG2Unsupported // multi-instance aggregate not yet supported
				}
				id := decodeIAID(dec, iaid, refCodeLen)
				rdx, _ := decodeInt(dec, iardx)
				rdy, _ := decodeInt(dec, iardy)
				all := append(append([]*jbBitmap{}, input...), newSyms...)
				if id < 0 || id >= len(all) {
					return errJBIG2Unsupported
				}
				bmp = decodeRefinement(dec, grCx, symWidth, hcHeight, sdrTemplate, all[id], rdx, rdy, false, rAt)
			}
			newSyms = append(newSyms, bmp)
		}
	}

	// Export flags (6.5.10): run-length select which of (input ++ new) to export.
	all := append(append([]*jbBitmap{}, input...), newSyms...)
	exported := make([]*jbBitmap, 0, numEx)
	cur := 0
	i := 0
	for i < len(all) && len(exported) < int(numEx) {
		runLen, ok := decodeInt(dec, iaex)
		if !ok || runLen < 0 || i+runLen > len(all) {
			return errJBIG2Unsupported
		}
		if cur == 1 {
			exported = append(exported, all[i:i+runLen]...)
		}
		i += runLen
		cur ^= 1
	}
	if d.symbols == nil {
		d.symbols = map[uint32][]*jbBitmap{}
	}
	d.symbols[seg.number] = exported
	return nil
}

// readTextRegion decodes a text region segment (types 4/6/7) and composes it
// onto the page.
func (d *jbig2Decoder) readTextRegion(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	ri, ok := readRegionInfo(r)
	if !ok {
		return errJBIG2Unsupported
	}
	flags, ok := r.u16()
	if !ok {
		return errJBIG2Unsupported
	}
	sbhuff := flags & 1
	sbrefine := (flags >> 1) & 1
	logStrips := int((flags >> 2) & 3)
	refCorner := int((flags >> 4) & 3)
	transposed := (flags>>6)&1 != 0
	sbCombOp := int((flags >> 7) & 3)
	sbDefPixel := byte((flags >> 9) & 1)
	dsOffset := int((flags >> 10) & 0x1F)
	if dsOffset > 15 {
		dsOffset -= 32 // signed 5-bit
	}
	sbrTemplate := int((flags >> 15) & 1)
	if sbhuff != 0 {
		return errJBIG2Unsupported // Huffman-coded text regions not yet supported
	}

	// Refinement AT pixels precede SBNUMINSTANCES when refinement uses template 0.
	var rAt []atPixel
	if sbrefine != 0 && sbrTemplate == 0 {
		for i := 0; i < 2; i++ {
			ax, ok1 := r.s8()
			ay, ok2 := r.s8()
			if !ok1 || !ok2 {
				return errJBIG2Unsupported
			}
			rAt = append(rAt, atPixel{ax, ay})
		}
	}

	numInstances, ok := r.u32()
	if !ok || numInstances > 1<<24 {
		return errJBIG2Unsupported
	}

	syms := d.inputSymbols(seg)
	if len(syms) == 0 {
		return errJBIG2Unsupported
	}
	symCodeLen := ceilLog2(len(syms))
	if symCodeLen < 1 {
		symCodeLen = 1
	}

	dec := newMQDecoder(r.data[r.pos:], 0, r.remaining())
	iadt, iafs, iads, iait := newIAx(), newIAx(), newIAx(), newIAx()
	iari, iardw, iardh, iardx, iardy := newIAx(), newIAx(), newIAx(), newIAx(), newIAx()
	iaid := make([]mqState, 1<<uint(symCodeLen+1))
	grCx := make([]mqState, 1<<13)

	region := newJBBitmap(ri.w, ri.h, sbDefPixel)
	strips := 1 << logStrips

	dt, _ := decodeInt(dec, iadt)
	stripT := -dt * strips
	firstS := 0
	inst := 0
	for inst < int(numInstances) {
		dt, _ := decodeInt(dec, iadt)
		stripT += dt * strips
		dfs, _ := decodeInt(dec, iafs)
		firstS += dfs
		curS := firstS
		first := true
		for {
			if !first {
				ids, ok := decodeInt(dec, iads)
				if !ok {
					break // OOB: end of strip
				}
				curS += ids + dsOffset
			}
			first = false
			if inst >= int(numInstances) {
				break
			}
			curT := 0
			if strips > 1 {
				curT, _ = decodeInt(dec, iait)
			}
			t := stripT + curT
			id := decodeIAID(dec, iaid, symCodeLen)
			if id < 0 || id >= len(syms) {
				return errJBIG2Unsupported
			}
			sym := syms[id]
			if sbrefine != 0 {
				if ri, _ := decodeInt(dec, iari); ri != 0 {
					rdw, _ := decodeInt(dec, iardw)
					rdh, _ := decodeInt(dec, iardh)
					rdx, _ := decodeInt(dec, iardx)
					rdy, _ := decodeInt(dec, iardy)
					rw, rh := sym.w+rdw, sym.h+rdh
					if rw <= 0 || rh <= 0 || rw > 1<<16 || rh > 1<<16 {
						return errJBIG2Unsupported
					}
					sym = decodeRefinement(dec, grCx, rw, rh, sbrTemplate, sym,
						(rdw>>1)+rdx, (rdh>>1)+rdy, false, rAt)
				}
			}
			placeSymbol(region, sym, &curS, t, refCorner, transposed, sbCombOp)
			inst++
		}
	}
	if d.page == nil {
		d.page = newJBBitmap(d.imgW, d.imgH, 0)
	}
	d.page.blit(region, ri.x, ri.y, ri.combOp)
	return nil
}

// placeSymbol draws one symbol instance into the region at the current S
// coordinate and strip T, per the reference corner and transposition, advancing
// curS so the next instance follows. See T.88 6.4.5.
func placeSymbol(region, sym *jbBitmap, curS *int, t, refCorner int, transposed bool, combOp int) {
	w, h := sym.w, sym.h
	rightCorner := refCorner == 2 || refCorner == 3 // BOTTOMRIGHT, TOPRIGHT
	topCorner := refCorner == 1 || refCorner == 3   // TOPLEFT, TOPRIGHT
	if !transposed {
		if rightCorner {
			*curS += w - 1
		}
		x0 := *curS
		if rightCorner {
			x0 = *curS - w + 1
		}
		y0 := t
		if !topCorner {
			y0 = t - h + 1
		}
		blitSymbol(region, sym, x0, y0, combOp)
		if !rightCorner {
			*curS += w - 1
		}
	} else {
		bottomCorner := refCorner == 0 || refCorner == 2
		if bottomCorner {
			*curS += h - 1
		}
		y0 := *curS
		if bottomCorner {
			y0 = *curS - h + 1
		}
		x0 := t
		if rightCorner {
			x0 = t - w + 1
		}
		blitSymbol(region, sym, x0, y0, combOp)
		if !bottomCorner {
			*curS += h - 1
		}
	}
}

// blitSymbol composes a symbol bitmap onto a region using a combination operator.
func blitSymbol(region, sym *jbBitmap, x0, y0, op int) {
	for y := 0; y < sym.h; y++ {
		py := y0 + y
		if py < 0 || py >= region.h {
			continue
		}
		for x := 0; x < sym.w; x++ {
			px := x0 + x
			if px < 0 || px >= region.w {
				continue
			}
			s := sym.pix[y*sym.w+x]
			i := py*region.w + px
			switch op {
			case 0:
				region.pix[i] |= s
			case 1:
				region.pix[i] &= s
			case 2:
				region.pix[i] ^= s
			case 3:
				region.pix[i] = 1 ^ (region.pix[i] ^ s)
			default:
				region.pix[i] = s
			}
		}
	}
}
