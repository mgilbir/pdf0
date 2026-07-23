package pdf0

// JBIG2 pattern dictionaries and halftone regions (ISO/IEC 14492 §6.6, §6.7).
// A pattern dictionary is a set of fixed-size bitmaps representing grey levels;
// a halftone region reproduces a greyscale image by decoding a grid of grey
// values and stamping the matching pattern at each grid position. This is how
// JBIG2 encodes continuous-tone content in a bilevel stream.
//
// Both the arithmetic and MMR (Group-4) coded paths are handled: the pattern
// collective bitmap and the greyscale bitplanes may be either.

// readPatternDict decodes a pattern dictionary segment (type 16), storing its
// patterns under the segment number.
func (d *jbig2Decoder) readPatternDict(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	flags, ok := r.u8()
	if !ok {
		return errJBIG2Unsupported
	}
	hdmmr := flags & 1
	template := int((flags >> 1) & 3)
	hdpw, ok1 := r.u8()
	hdph, ok2 := r.u8()
	grayMax, ok3 := r.u32()
	if !ok1 || !ok2 || !ok3 {
		return errJBIG2Unsupported
	}
	if hdpw == 0 || hdph == 0 || grayMax > 1<<16 {
		return errJBIG2Unsupported
	}
	numPats := int(grayMax) + 1
	pw, ph := int(hdpw), int(hdph)

	// The patterns are decoded as one collective bitmap and then sliced apart.
	var collective *jbBitmap
	if hdmmr != 0 {
		bmp, err := newMMRPlaneReader(r.data[r.pos:]).plane(numPats*pw, ph, false)
		if err != nil {
			return err
		}
		collective = bmp
	} else {
		// The pattern-dictionary A1 adaptive pixel is (-HDPW, 0) (T.88 6.7.5).
		at := halftoneAT(template, atPixel{-pw, 0})
		dec := newMQDecoder(r.data[r.pos:], 0, r.remaining())
		gb := make([]mqState, 1<<16)
		collective = decodeGenericInto(dec, gb, numPats*pw, ph, template, at, false, nil)
	}

	patterns := make([]*jbBitmap, numPats)
	for m := 0; m < numPats; m++ {
		p := newJBBitmap(pw, ph, 0)
		for y := 0; y < ph; y++ {
			for x := 0; x < pw; x++ {
				p.pix[y*pw+x] = collective.get(m*pw+x, y)
			}
		}
		patterns[m] = p
	}
	if d.patterns == nil {
		d.patterns = map[uint32][]*jbBitmap{}
	}
	d.patterns[seg.number] = patterns
	return nil
}

// halftoneAT returns the adaptive-template pixels for pattern and halftone
// generic decoding: the given A1 followed by the fixed A2..A4, truncated to the
// count the template needs.
func halftoneAT(template int, a1 atPixel) []atPixel {
	at := []atPixel{a1, {-3, -1}, {2, -2}, {-2, -2}}
	if template != 0 {
		return at[:1]
	}
	return at
}

// refPatterns returns the patterns of the first referred pattern-dictionary
// segment.
func (d *jbig2Decoder) refPatterns(seg jbSegment) []*jbBitmap {
	for _, ref := range seg.refs {
		if p, ok := d.patterns[ref]; ok {
			return p
		}
	}
	return nil
}

// readHalftoneRegion decodes a halftone region segment (types 20/22/23) and
// composes it onto the page.
func (d *jbig2Decoder) readHalftoneRegion(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	ri, ok := readRegionInfo(r)
	if !ok {
		return errJBIG2Unsupported
	}
	flags, ok := r.u8()
	if !ok {
		return errJBIG2Unsupported
	}
	hmmr := flags & 1
	template := int((flags >> 1) & 3)
	enableSkip := (flags>>3)&1 != 0
	combOp := int((flags >> 4) & 7)
	defPixel := byte((flags >> 7) & 1)

	hgw, ok1 := r.u32()
	hgh, ok2 := r.u32()
	hgxU, ok3 := r.u32()
	hgyU, ok4 := r.u32()
	hrx, ok5 := r.u16()
	hry, ok6 := r.u16()
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 {
		return errJBIG2Unsupported
	}
	if hgw == 0 || hgh == 0 || hgw > 1<<16 || hgh > 1<<16 {
		return errJBIG2Unsupported
	}
	hgx, hgy := int(int32(hgxU)), int(int32(hgyU))
	gw, gh := int(hgw), int(hgh)

	patterns := d.refPatterns(seg)
	if len(patterns) == 0 {
		return errJBIG2Unsupported
	}
	hpw, hph := patterns[0].w, patterns[0].h
	bpp := ceilLog2(len(patterns))
	if bpp < 1 {
		bpp = 1
	}

	region := newJBBitmap(ri.w, ri.h, defPixel)

	// Cells whose pattern lies entirely outside the region can be skipped.
	var skip *jbBitmap
	if enableSkip {
		skip = newJBBitmap(gw, gh, 0)
		for m := 0; m < gh; m++ {
			for n := 0; n < gw; n++ {
				x := (hgx + m*int(hry) + n*int(hrx)) >> 8
				y := (hgy + m*int(hrx) - n*int(hry)) >> 8
				if x+hpw <= 0 || x >= ri.w || y+hph <= 0 || y >= ri.h {
					skip.pix[m*gw+n] = 1
				}
			}
		}
	}

	var gray []int
	if hmmr != 0 {
		gray = decodeGrayScaleMMR(r.data[r.pos:], gw, gh, bpp)
	} else {
		a1x := 3
		if template > 1 {
			a1x = 2
		}
		at := halftoneAT(template, atPixel{a1x, -1})
		dec := newMQDecoder(r.data[r.pos:], 0, r.remaining())
		gb := make([]mqState, 1<<16)
		gray = decodeGrayScale(dec, gb, gw, gh, template, bpp, at, skip)
	}

	for m := 0; m < gh; m++ {
		for n := 0; n < gw; n++ {
			if skip != nil && skip.pix[m*gw+n] != 0 {
				continue
			}
			x := (hgx + m*int(hry) + n*int(hrx)) >> 8
			y := (hgy + m*int(hrx) - n*int(hry)) >> 8
			idx := gray[m*gw+n]
			if idx < 0 {
				idx = 0
			}
			if idx >= len(patterns) {
				idx = len(patterns) - 1
			}
			blitSymbol(region, patterns[idx], x, y, combOp)
		}
	}
	if d.page == nil {
		d.page = newJBBitmap(d.imgW, d.imgH, 0)
	}
	d.page.blit(region, ri.x, ri.y, ri.combOp)
	return nil
}

// decodeGrayScale decodes a greyscale image of grey values (Annex C.5): bpp
// bitplanes, each an arithmetic generic region sharing one context, combined
// out of Gray code into an integer per cell. Returns w*h values row-major.
func decodeGrayScale(dec *mqDecoder, gb []mqState, w, h, template, bpp int, at []atPixel, skip *jbBitmap) []int {
	planes := make([]*jbBitmap, bpp)
	for i := bpp - 1; i >= 0; i-- { // most significant plane first
		planes[i] = decodeGenericInto(dec, gb, w, h, template, at, false, skip)
	}
	return grayCombine(planes, w, h, bpp)
}

// decodeGrayScaleMMR decodes the greyscale image when the halftone region is
// MMR-coded (HMMR = 1): the bitplanes are consecutive Group-4 bitmaps sharing one
// bit stream, each ended by an EOFB (Annex C.5). A malformed plane yields zeros.
func decodeGrayScaleMMR(data []byte, w, h, bpp int) []int {
	mr := newMMRPlaneReader(data)
	planes := make([]*jbBitmap, bpp)
	for i := bpp - 1; i >= 0; i-- { // most significant plane first
		p, err := mr.plane(w, h, true)
		if err != nil {
			p = newJBBitmap(w, h, 0)
		}
		planes[i] = p
	}
	return grayCombine(planes, w, h, bpp)
}

// grayCombine turns bpp Gray-coded bitplanes (MSB first) into one integer per
// cell (T.88 C.5): the top plane is the value's high bit and each lower plane is
// XORed with the running bit to convert Gray code to binary.
func grayCombine(planes []*jbBitmap, w, h, bpp int) []int {
	gray := make([]int, w*h)
	for i := range gray {
		v := int(planes[bpp-1].pix[i])
		prev := v
		for j := bpp - 2; j >= 0; j-- {
			bit := int(planes[j].pix[i]) ^ prev // Gray -> binary
			v = (v << 1) | bit
			prev = bit
		}
		gray[i] = v
	}
	return gray
}
