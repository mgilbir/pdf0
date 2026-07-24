package pdf0

// Huffman-coded JBIG2 symbol dictionaries and text regions (SDHUFF / SBHUFF).
// These mirror the arithmetic paths in jbig2_symbol.go but read integer
// parameters through Huffman tables (jbig2_huffman.go) instead of the MQ integer
// procedures, and code the symbol bitmaps as MMR or uncompressed collective
// bitmaps rather than per-symbol generic regions. Refinement and aggregate
// symbols (SDREFAGG) and text-region refinement (SBREFINE) still code the
// refinement bitmap arithmetically, byte-aligned over an explicit BMSIZE run.

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
	dhSel := int((flags >> 2) & 3)
	dwSel := int((flags >> 4) & 3)
	bmSel := int((flags >> 6) & 1)
	aggSel := int((flags >> 7) & 1)
	sdrTemplate := int((flags >> 12) & 1)

	pk := d.newHuffPicker(seg)
	dhTable := pk.pick(dhSel, []int{4, 5}, 3)
	dwTable := pk.pick(dwSel, []int{2, 3}, 3)
	bmTable := pk.pick(bmSel, []int{1}, 1)
	if dhTable == nil || dwTable == nil || bmTable == nil {
		return errJBIG2Unsupported
	}

	// Refinement AT pixels (template 0) follow the flags when the dictionary uses
	// refinement/aggregate symbol coding (SDREFAGG).
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

	h := &huffReader{data: r.data[r.pos:]}
	if sdrefagg != 0 {
		// Refinement/aggregate symbol coding (6.5.8.2): each symbol is an
		// individually refined or aggregated bitmap, not a slice of a height-class
		// collective bitmap.
		aggTable := pk.pick(aggSel, []int{1}, 1)
		if aggTable == nil {
			return errJBIG2Unsupported
		}
		symCodeLen := ceilLog2(int(numNew) + len(input))
		if symCodeLen < 1 {
			symCodeLen = 1
		}
		// One GR context is shared by every refinement in the dictionary; its
		// adaptive state carries across symbols (T.88 6.5.8.2).
		grCx := make([]mqState, 1<<13)
		hcHeight := 0
		for len(newSyms) < int(numNew) {
			dh, _ := dhTable.decode(h)
			hcHeight += dh
			if hcHeight <= 0 || hcHeight > 1<<16 {
				return errJBIG2Unsupported
			}
			symWidth := 0
			for {
				dw, ok := dwTable.decode(h)
				if !ok {
					break // OOB: end of height class
				}
				symWidth += dw
				if symWidth <= 0 || symWidth > 1<<16 || len(newSyms) >= int(numNew) {
					return errJBIG2Unsupported
				}
				bmp, err := decodeRefAggSymbolHuff(h, grCx, symWidth, hcHeight, aggTable, symCodeLen, input, newSyms, sdrTemplate, rAt)
				if err != nil {
					return err
				}
				newSyms = append(newSyms, bmp)
			}
		}
	} else {
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

// huffRefine decodes an arithmetic refinement bitmap embedded in a Huffman
// stream. In Huffman symbol refinement the parameters (RDW/RDH/RDX/RDY) come
// from Huffman tables, but the refinement bitmap itself is always MQ-coded, on a
// byte boundary, over the next bmSize bytes (T.88 6.4.11 / 6.5.8.2.2). It decodes
// that bitmap and advances h past the arithmetic data.
func huffRefine(h *huffReader, cx []mqState, w, height, template int, ref *jbBitmap, dx, dy int, at []atPixel, bmSize int) (*jbBitmap, error) {
	start := h.bytePos()
	end := start + bmSize
	if bmSize <= 0 || end > len(h.data) {
		return nil, errJBIG2Unsupported
	}
	dec := newMQDecoder(h.data, start, end)
	out := decodeRefinement(dec, cx, w, height, template, ref, dx, dy, false, at)
	h.pos = end * 8
	return out, nil
}

// decodeRefAggSymbolHuff decodes one refinement/aggregate symbol in a Huffman
// symbol dictionary (T.88 6.5.8.2). REFAGGNINST == 1 is a single refinement of a
// referenced symbol (6.5.8.2.2); larger counts aggregate several instances via a
// text-region decoding procedure (6.5.8.2.1). cx is the dictionary-wide GR
// context, reused across every refinement in the dictionary.
func decodeRefAggSymbolHuff(h *huffReader, cx []mqState, symWidth, hcHeight int, aggTable *huffTable, symCodeLen int, input, newSyms []*jbBitmap, sdrTemplate int, rAt []atPixel) (*jbBitmap, error) {
	nInst, _ := aggTable.decode(h)
	all := append(append([]*jbBitmap{}, input...), newSyms...)
	if nInst == 1 {
		// Single-instance refinement (6.5.8.2.2): ID (fixed length), RDX/RDY via
		// B.15, BMSIZE via SBHUFFRSIZE (B.1) — not the collective SDHUFFBMSIZE.
		id := h.readBits(symCodeLen)
		rdx, _ := stdHuffTable(15).decode(h)
		rdy, _ := stdHuffTable(15).decode(h)
		bmSize, _ := stdHuffTable(1).decode(h)
		h.align()
		if id < 0 || id >= len(all) {
			return nil, errJBIG2Unsupported
		}
		return huffRefine(h, cx, symWidth, hcHeight, sdrTemplate, all[id], rdx, rdy, rAt, bmSize)
	}
	if nInst <= 0 {
		return nil, errJBIG2Unsupported
	}
	return decodeAggregateHuff(h, cx, symWidth, hcHeight, nInst, symCodeLen, all, sdrTemplate, rAt)
}

// decodeAggregateHuff decodes an aggregate symbol as a small Huffman-coded text
// region of REFAGGNINST symbol instances (T.88 6.5.8.2.1). The strip parameters
// use fixed standard tables (FS=B.6, DS=B.8, DT=B.11) and single-pixel strips;
// refinement parameters use B.15/B.1, matching the SBHUFF text-region path. cx is
// the caller's GR context, shared across refinements.
func decodeAggregateHuff(h *huffReader, cx []mqState, w, height, numInst, symCodeLen int, syms []*jbBitmap, sbrTemplate int, rAt []atPixel) (*jbBitmap, error) {
	fsTable, dsTable, dtTable := stdHuffTable(6), stdHuffTable(8), stdHuffTable(11)
	rdwT, rdhT := stdHuffTable(15), stdHuffTable(15)
	rdxT, rdyT := stdHuffTable(15), stdHuffTable(15)
	rsizeT := stdHuffTable(1)

	region := newJBBitmap(w, height, 0)
	dt0, _ := dtTable.decode(h)
	stripT := -dt0 // SBSTRIPS == 1
	firstS := 0
	inst := 0
	for inst < numInst {
		dt, _ := dtTable.decode(h)
		stripT += dt
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
				curS += ids // SBDSOFFSET == 0
			}
			first = false
			if inst >= numInst {
				break
			}
			id := h.readBits(symCodeLen) // fixed-length symbol code (6.5.8.2.3)
			if id < 0 || id >= len(syms) {
				return nil, errJBIG2Unsupported
			}
			sym := syms[id]
			if h.bit() != 0 { // RI
				rdw, _ := rdwT.decode(h)
				rdh, _ := rdhT.decode(h)
				rdx, _ := rdxT.decode(h)
				rdy, _ := rdyT.decode(h)
				bmSize, _ := rsizeT.decode(h)
				h.align()
				rw, rh := sym.w+rdw, sym.h+rdh
				if rw <= 0 || rh <= 0 || rw > 1<<16 || rh > 1<<16 {
					return nil, errJBIG2Unsupported
				}
				refined, err := huffRefine(h, cx, rw, rh, sbrTemplate, sym, (rdw>>1)+rdx, (rdh>>1)+rdy, rAt, bmSize)
				if err != nil {
					return nil, err
				}
				sym = refined
			}
			placeSymbol(region, sym, &curS, stripT, 1 /*TOPLEFT*/, false, 0 /*OR*/)
			inst++
		}
	}
	return region, nil
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
func (d *jbig2Decoder) readTextRegionHuff(seg jbSegment, r *jbReader, ri regionInfo, logStrips, refCorner int, transposed bool, sbCombOp int, sbDefPixel byte, dsOffset int, sbrefine, sbrTemplate int) error {
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
	// Refinement parameter tables (SBHUFFRDW/RDH/RDX/RDY/RSIZE) and AT pixels
	// follow when the region uses Huffman-coded symbol refinement (T.88 Table 34,
	// 7.4.3.1.2). RDW/RDH/RDX/RDY select standard tables B.14/B.15; RSIZE selects
	// B.1.
	var rdwTable, rdhTable, rdxTable, rdyTable, rsizeTable *huffTable
	var rAt []atPixel
	if sbrefine != 0 {
		rdwTable = pk.pick(int((hflags>>6)&3), []int{14, 15}, 3)
		rdhTable = pk.pick(int((hflags>>8)&3), []int{14, 15}, 3)
		rdxTable = pk.pick(int((hflags>>10)&3), []int{14, 15}, 3)
		rdyTable = pk.pick(int((hflags>>12)&3), []int{14, 15}, 3)
		rsizeTable = pk.pick(int((hflags>>14)&1), []int{1}, 1)
		if rdwTable == nil || rdhTable == nil || rdxTable == nil || rdyTable == nil || rsizeTable == nil {
			return errJBIG2Unsupported
		}
		if sbrTemplate == 0 {
			for i := 0; i < 2; i++ {
				ax, ok1 := r.s8()
				ay, ok2 := r.s8()
				if !ok1 || !ok2 {
					return errJBIG2Unsupported
				}
				rAt = append(rAt, atPixel{ax, ay})
			}
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
	// One GR context shared by every instance refinement in the region.
	grCx := make([]mqState, 1<<13)

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
			sym := syms[id]
			// Symbol refinement (6.4.11): an RI bit gates a per-instance refined
			// copy of the referenced symbol.
			ri := 0
			if sbrefine != 0 {
				ri = h.bit()
			}
			if ri != 0 {
				rdw, _ := rdwTable.decode(h)
				rdh, _ := rdhTable.decode(h)
				rdx, _ := rdxTable.decode(h)
				rdy, _ := rdyTable.decode(h)
				bmSize, _ := rsizeTable.decode(h)
				h.align()
				rw, rh := sym.w+rdw, sym.h+rdh
				if rw <= 0 || rh <= 0 || rw > 1<<16 || rh > 1<<16 {
					return errJBIG2Unsupported
				}
				refined, err := huffRefine(h, grCx, rw, rh, sbrTemplate, sym, (rdw>>1)+rdx, (rdh>>1)+rdy, rAt, bmSize)
				if err != nil {
					return err
				}
				sym = refined
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
