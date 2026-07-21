package pdf0

// Huffman-coded JBIG2 symbol dictionaries and text regions (SDHUFF / SBHUFF).
// These mirror the arithmetic paths in jbig2_symbol.go but read integer
// parameters through Huffman tables (jbig2_huffman.go) instead of the MQ integer
// procedures, and code the symbol bitmaps as MMR or uncompressed collective
// bitmaps rather than per-symbol generic regions.

// readCustomTable decodes a table segment (type 53, T.88 B.2) and stores it.
func (d *jbig2Decoder) readCustomTable(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	flags, ok := r.u8()
	if !ok {
		return errJBIG2Unsupported
	}
	htoob := flags & 1
	htps := int((flags>>1)&7) + 1
	htrs := int((flags>>4)&7) + 1
	low, ok1 := r.u32()
	high, ok2 := r.u32()
	if !ok1 || !ok2 {
		return errJBIG2Unsupported
	}
	htLow, htHigh := int(int32(low)), int(int32(high))
	if htHigh <= htLow {
		return errJBIG2Unsupported
	}

	h := &huffReader{data: r.data[r.pos:]}
	var lines []huffLine
	cur := htLow
	for cur < htHigh {
		prefLen := h.readBits(htps)
		rangeLen := h.readBits(htrs)
		lines = append(lines, huffLine{prefLen: prefLen, rangeLen: rangeLen, rangeLow: cur, kind: huffNormal})
		cur += 1 << rangeLen
		if len(lines) > 1<<20 {
			return errJBIG2Unsupported
		}
	}
	lowPref := h.readBits(htps)
	lines = append(lines, huffLine{prefLen: lowPref, rangeLen: 32, rangeLow: htLow - 1, kind: huffLower})
	highPref := h.readBits(htps)
	lines = append(lines, huffLine{prefLen: highPref, rangeLen: 32, rangeLow: htHigh, kind: huffUpper})
	if htoob != 0 {
		oobPref := h.readBits(htps)
		lines = append(lines, huffLine{prefLen: oobPref, kind: huffOOB})
	}
	if d.huffTables == nil {
		d.huffTables = map[uint32]*huffTable{}
	}
	d.huffTables[seg.number] = newHuffTable(lines)
	return nil
}

// huffPicker selects standard or custom Huffman tables as flag fields request.
type huffPicker struct {
	custom []*huffTable
	idx    int
}

func (d *jbig2Decoder) newHuffPicker(seg jbSegment) *huffPicker {
	var custom []*huffTable
	for _, ref := range seg.refs {
		if t, ok := d.huffTables[ref]; ok {
			custom = append(custom, t)
		}
	}
	return &huffPicker{custom: custom}
}

// pick selects a standard table by option index, or the next custom table when
// sel equals customSel (3 for a 2-bit selector, 1 for a 1-bit one). Values
// between the standard options and customSel are reserved.
func (p *huffPicker) pick(sel int, std []int, customSel int) *huffTable {
	if sel == customSel {
		if p.idx >= len(p.custom) {
			return nil
		}
		t := p.custom[p.idx]
		p.idx++
		return t
	}
	if sel < 0 || sel >= len(std) {
		return nil
	}
	return stdHuffTable(std[sel])
}

// readSymbolDictHuff decodes a Huffman-coded symbol dictionary. r is positioned
// just after the 2-byte flags.
func (d *jbig2Decoder) readSymbolDictHuff(seg jbSegment, r *jbReader, flags uint16) error {
	sdrefagg := (flags >> 1) & 1
	if sdrefagg != 0 {
		return errJBIG2Unsupported // Huffman + refinement/aggregate not supported
	}
	dhSel := int((flags >> 2) & 3)
	dwSel := int((flags >> 4) & 3)
	bmSel := int((flags >> 6) & 1)

	pk := d.newHuffPicker(seg)
	dhTable := pk.pick(dhSel, []int{4, 5}, 3)
	dwTable := pk.pick(dwSel, []int{2, 3}, 3)
	bmTable := pk.pick(bmSel, []int{1}, 1)
	if dhTable == nil || dwTable == nil || bmTable == nil {
		return errJBIG2Unsupported
	}

	numEx, ok1 := r.u32()
	numNew, ok2 := r.u32()
	if !ok1 || !ok2 || numNew > 1<<20 || numEx > 1<<20 {
		return errJBIG2Unsupported
	}
	input := d.inputSymbols(seg)
	newSyms := make([]*jbBitmap, 0, numNew)

	h := &huffReader{data: r.data[r.pos:]}
	hcHeight := 0
	for len(newSyms) < int(numNew) {
		dh, _ := dhTable.decode(h)
		hcHeight += dh
		if hcHeight <= 0 || hcHeight > 1<<16 {
			return errJBIG2Unsupported
		}
		symWidth, totWidth := 0, 0
		var widths []int
		for {
			dw, ok := dwTable.decode(h)
			if !ok {
				break // OOB: end of height class
			}
			symWidth += dw
			if symWidth <= 0 || symWidth > 1<<16 || len(newSyms)+len(widths) >= int(numNew) {
				return errJBIG2Unsupported
			}
			widths = append(widths, symWidth)
			totWidth += symWidth
		}
		if totWidth == 0 {
			continue
		}
		// Decode the height-class collective bitmap (6.5.9).
		bmSize, _ := bmTable.decode(h)
		h.align()
		var collective *jbBitmap
		if bmSize == 0 {
			collective = readUncompressedBitmap(h, totWidth, hcHeight)
		} else {
			start := h.bytePos()
			if start+bmSize > len(h.data) {
				return errJBIG2Unsupported
			}
			bmp, err := decodeGenericMMR(h.data[start:start+bmSize], totWidth, hcHeight)
			if err != nil {
				return err
			}
			collective = bmp
			h.pos = (start + bmSize) * 8
		}
		x := 0
		for _, w := range widths {
			sym := newJBBitmap(w, hcHeight, 0)
			for yy := 0; yy < hcHeight; yy++ {
				for xx := 0; xx < w; xx++ {
					sym.pix[yy*w+xx] = collective.get(x+xx, yy)
				}
			}
			newSyms = append(newSyms, sym)
			x += w
		}
	}

	// Export flags (6.5.10): run lengths via Table B.1.
	exTable := stdHuffTable(1)
	all := append(append([]*jbBitmap{}, input...), newSyms...)
	exported := make([]*jbBitmap, 0, numEx)
	cur, i := 0, 0
	for i < len(all) && len(exported) < int(numEx) {
		runLen, ok := exTable.decode(h)
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

// readUncompressedBitmap reads a w x h bitmap stored one bit per pixel, MSB
// first, with each row padded to a byte boundary.
func readUncompressedBitmap(h *huffReader, w, height int) *jbBitmap {
	bmp := newJBBitmap(w, height, 0)
	for y := 0; y < height; y++ {
		for x := 0; x < w; x++ {
			bmp.pix[y*w+x] = byte(h.bit())
		}
		h.align()
	}
	return bmp
}

// readTextRegionHuff decodes a Huffman-coded text region. r is positioned just
// after the 2-byte text region flags; the caller passes the decoded flag fields.
func (d *jbig2Decoder) readTextRegionHuff(seg jbSegment, r *jbReader, ri regionInfo, logStrips, refCorner int, transposed bool, sbCombOp int, sbDefPixel byte, dsOffset int, sbrefine int) error {
	if sbrefine != 0 {
		return errJBIG2Unsupported // Huffman + refinement not supported
	}
	hflags, ok := r.u16()
	if !ok {
		return errJBIG2Unsupported
	}
	pk := d.newHuffPicker(seg)
	fsTable := pk.pick(int(hflags&3), []int{6, 7}, 3)
	dsTable := pk.pick(int((hflags>>2)&3), []int{8, 9, 10}, 3)
	dtTable := pk.pick(int((hflags>>4)&3), []int{11, 12, 13}, 3)
	if fsTable == nil || dsTable == nil || dtTable == nil {
		return errJBIG2Unsupported
	}

	numInstances, ok := r.u32()
	if !ok || numInstances > 1<<24 {
		return errJBIG2Unsupported
	}
	syms := d.inputSymbols(seg)
	if len(syms) == 0 {
		return errJBIG2Unsupported
	}

	h := &huffReader{data: r.data[r.pos:]}

	// Symbol ID Huffman table (7.4.3.1.7): run-code lengths, then per-symbol code
	// lengths (with run codes 32/33/34 for repeats), then canonical assignment.
	runLines := make([]huffLine, 35)
	for i := 0; i < 35; i++ {
		runLines[i] = huffLine{prefLen: h.readBits(4), rangeLow: i}
	}
	runTable := newHuffTable(runLines)
	symLens := make([]int, len(syms))
	prev, i := 0, 0
	for i < len(syms) {
		code, _ := runTable.decode(h)
		switch {
		case code < 32:
			symLens[i] = code
			prev = code
			i++
		case code == 32:
			n := h.readBits(2) + 3
			for ; n > 0 && i < len(syms); n-- {
				symLens[i] = prev
				i++
			}
		case code == 33:
			n := h.readBits(3) + 3
			for ; n > 0 && i < len(syms); n-- {
				symLens[i] = 0
				i++
			}
		default: // 34
			n := h.readBits(7) + 11
			for ; n > 0 && i < len(syms); n-- {
				symLens[i] = 0
				i++
			}
		}
	}
	idLines := make([]huffLine, len(syms))
	for k := range syms {
		idLines[k] = huffLine{prefLen: symLens[k], rangeLow: k}
	}
	symIDTable := newHuffTable(idLines)
	// The text-region data starts on a byte boundary after the symbol ID table
	// (T.88 7.4.3.1.7).
	h.align()

	region := newJBBitmap(ri.w, ri.h, sbDefPixel)
	strips := 1 << logStrips

	dt0, _ := dtTable.decode(h)
	stripT := -dt0 * strips
	firstS := 0
	inst := 0
	for inst < int(numInstances) {
		dt, _ := dtTable.decode(h)
		stripT += dt * strips
		dfs, _ := fsTable.decode(h)
		firstS += dfs
		curS := firstS
		first := true
		for {
			if !first {
				ids, ok := dsTable.decode(h)
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
				curT = h.readBits(logStrips)
			}
			t := stripT + curT
			id, _ := symIDTable.decode(h)
			if id < 0 || id >= len(syms) {
				return errJBIG2Unsupported
			}
			placeSymbol(region, syms[id], &curS, t, refCorner, transposed, sbCombOp)
			inst++
		}
	}
	if d.page == nil {
		d.page = newJBBitmap(d.imgW, d.imgH, 0)
	}
	d.page.blit(region, ri.x, ri.y, ri.combOp)
	return nil
}
