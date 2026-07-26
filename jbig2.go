package pdf0

import (
	"encoding/binary"
	"errors"
	"sort"
)

// This file decodes the JBIG2 bilevel image codec (ISO/IEC 14492 / ITU-T T.88)
// as embedded in PDF via the JBIG2Decode filter. JBIG2 compresses scanned
// black-and-white pages far better than CCITT by modelling repeated marks
// (symbols), halftones and generic regions with an adaptive MQ arithmetic coder.
//
// A PDF JBIG2 stream uses the "embedded" organisation (Annex D.3): no file
// header, just a sequence of segments. An optional /JBIG2Globals stream in
// /DecodeParms carries shared segments (symbol dictionaries, tables) referenced
// by the page stream.
//
// This decodes generic regions (arithmetic and MMR, including unknown-length
// segments), symbol-dictionary + text regions (both arithmetic and Huffman, with
// coding-context reuse), and generic refinement — standalone regions, text-region
// symbol refinement (SBREFINE), symbol-dictionary refinement/aggregation
// (SDREFAGG, single- and multi-instance), and intermediate regions used as a
// refinement reference — plus pattern dictionaries and halftone regions
// (arithmetic and MMR), composing them onto the page. A document that uses a
// segment type or feature not handled here falls back to the raw encoded bytes.
//
// Internally a bitmap stores one byte per pixel with 1 = black (the JBIG2
// convention). The final packed output inverts this to the PDF image convention
// (0 = black), matching the DeviceGray sample stream a 1-bpc image expects.

var errJBIG2Unsupported = errors.New("jbig2: unsupported segment")

// jbBitmap is a decoded bilevel image, one byte per pixel (0 or 1, 1 = black).
type jbBitmap struct {
	w, h int
	pix  []byte
}

func newJBBitmap(w, h int, fill byte) *jbBitmap {
	b := &jbBitmap{w: w, h: h, pix: make([]byte, w*h)}
	if fill != 0 {
		for i := range b.pix {
			b.pix[i] = fill
		}
	}
	return b
}

func (b *jbBitmap) get(x, y int) byte {
	if x < 0 || x >= b.w || y < 0 || y >= b.h {
		return 0
	}
	return b.pix[y*b.w+x]
}

func (b *jbBitmap) set(x, y int, v byte) {
	if x >= 0 && x < b.w && y >= 0 && y < b.h {
		b.pix[y*b.w+x] = v
	}
}

// jbSegment is one parsed JBIG2 segment: its header fields and raw data body.
type jbSegment struct {
	number uint32
	typ    int
	refs   []uint32
	data   []byte
}

// jbReader is a big-endian byte cursor over a segment or its data.
type jbReader struct {
	data []byte
	pos  int
}

func (r *jbReader) remaining() int { return len(r.data) - r.pos }

func (r *jbReader) u8() (byte, bool) {
	if r.pos >= len(r.data) {
		return 0, false
	}
	v := r.data[r.pos]
	r.pos++
	return v, true
}

func (r *jbReader) u16() (uint16, bool) {
	if r.pos+2 > len(r.data) {
		return 0, false
	}
	v := binary.BigEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v, true
}

func (r *jbReader) u32() (uint32, bool) {
	if r.pos+4 > len(r.data) {
		return 0, false
	}
	v := binary.BigEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return v, true
}

func (r *jbReader) s8() (int, bool) {
	v, ok := r.u8()
	return int(int8(v)), ok
}

// decodeJBIG2 decodes a JBIG2 image (globals + page stream) into packed 1-bpp
// rows in the PDF convention (0 = black), sized to width x height.
func decodeJBIG2(globals, data []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 || width > 1<<20 || height > 1<<20 {
		return nil, errJBIG2Unsupported
	}
	d := &jbig2Decoder{imgW: width, imgH: height}
	if len(globals) > 0 {
		segs, err := parseJBIG2Segments(globals)
		if err != nil {
			return nil, err
		}
		if err := d.run(segs); err != nil {
			return nil, err
		}
	}
	segs, err := parseJBIG2Segments(data)
	if err != nil {
		return nil, err
	}
	if err := d.run(segs); err != nil {
		return nil, err
	}
	if d.page == nil {
		return nil, errJBIG2Unsupported
	}
	return d.page.packPDF(), nil
}

// packPDF renders the page bitmap to packed 1-bpp rows, inverting to the PDF
// image convention (black pixels become 0 bits).
func (b *jbBitmap) packPDF() []byte {
	stride := (b.w + 7) / 8
	out := make([]byte, stride*b.h)
	for i := range out {
		out[i] = 0xFF // default white (1 bits)
	}
	for y := 0; y < b.h; y++ {
		row := out[y*stride:]
		for x := 0; x < b.w; x++ {
			if b.pix[y*b.w+x] != 0 { // black -> clear the bit to 0
				row[x/8] &^= 1 << (7 - uint(x%8))
			}
		}
	}
	return out
}

type jbig2Decoder struct {
	imgW, imgH int
	page       *jbBitmap
	symbols    map[uint32][]*jbBitmap // exported symbols per symbol-dict segment
	patterns   map[uint32][]*jbBitmap // patterns per pattern-dict segment
	huffTables map[uint32]*huffTable  // custom Huffman tables per table segment
	symCtx     map[uint32]*symbolCtx  // retained coding contexts per symbol-dict segment
	intermrgn  map[uint32]*jbBitmap   // intermediate region results (types 36/40) keyed by segment
}

// symbolCtx holds the generic (GB) and refinement (GR) arithmetic coding
// contexts a symbol dictionary retains for a later dictionary to resume from
// (SDRETAINCONTEXT / SDUSEDCONTEXT, T.88 7.4.3.1.7).
type symbolCtx struct {
	gb, gr []mqState
}

// parseJBIG2Segments parses the embedded (PDF) segment organisation: a sequence
// of segment headers each immediately followed by its data.
func parseJBIG2Segments(data []byte) ([]jbSegment, error) {
	var segs []jbSegment
	r := &jbReader{data: data}
	for r.remaining() > 0 {
		seg, ok, err := parseJBIG2Header(r)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		segs = append(segs, seg)
	}
	return segs, nil
}

func parseJBIG2Header(r *jbReader) (jbSegment, bool, error) {
	var seg jbSegment
	number, ok := r.u32()
	if !ok {
		return seg, false, nil
	}
	seg.number = number
	flags, ok := r.u8()
	if !ok {
		return seg, false, errJBIG2Unsupported
	}
	seg.typ = int(flags & 0x3F)
	pageAssoc4 := flags&0x40 != 0

	// Referred-to segment count and retention flags (7.2.4).
	rtByte, ok := r.u8()
	if !ok {
		return seg, false, errJBIG2Unsupported
	}
	var refCount uint32
	if rtByte>>5 == 7 {
		r.pos-- // the count occupies the full 4 bytes
		longCount, ok := r.u32()
		if !ok {
			return seg, false, errJBIG2Unsupported
		}
		refCount = longCount & 0x1FFFFFFF
		retentionBytes := int((refCount + 8) / 8) // ceil((refCount+1)/8)
		r.pos += retentionBytes
	} else {
		refCount = uint32(rtByte >> 5)
	}
	if refCount > 1<<20 {
		return seg, false, errJBIG2Unsupported
	}

	// Referred-to segment numbers: width depends on this segment's number.
	refSize := 1
	if seg.number > 65536 {
		refSize = 4
	} else if seg.number > 256 {
		refSize = 2
	}
	seg.refs = make([]uint32, 0, refCount)
	for i := uint32(0); i < refCount; i++ {
		var v uint32
		switch refSize {
		case 1:
			b, ok := r.u8()
			if !ok {
				return seg, false, errJBIG2Unsupported
			}
			v = uint32(b)
		case 2:
			b, ok := r.u16()
			if !ok {
				return seg, false, errJBIG2Unsupported
			}
			v = uint32(b)
		default:
			b, ok := r.u32()
			if !ok {
				return seg, false, errJBIG2Unsupported
			}
			v = b
		}
		seg.refs = append(seg.refs, v)
	}

	// Page association.
	if pageAssoc4 {
		if _, ok := r.u32(); !ok {
			return seg, false, errJBIG2Unsupported
		}
	} else {
		if _, ok := r.u8(); !ok {
			return seg, false, errJBIG2Unsupported
		}
	}

	dataLen, ok := r.u32()
	if !ok {
		return seg, false, errJBIG2Unsupported
	}
	if dataLen == 0xFFFFFFFF {
		// Unknown segment length (7.2.7): permitted only for immediate generic
		// regions. The row data is followed by a 6-byte terminator — {0xFF,0xAC}
		// (arithmetic) or {0x00,0x00} (MMR) then the region height — that we scan
		// for to recover the length.
		n, ok := unknownGenericLength(r.data[r.pos:], seg.typ)
		if !ok {
			return seg, false, errJBIG2Unsupported
		}
		dataLen = uint32(n)
	}
	if int(dataLen) > r.remaining() {
		return seg, false, errJBIG2Unsupported
	}
	seg.data = r.data[r.pos : r.pos+int(dataLen)]
	r.pos += int(dataLen)
	return seg, true, nil
}

// unknownGenericLength recovers the data length of an immediate generic region
// segment written with the unknown-length marker 0xFFFFFFFF (7.2.7). The row data
// is followed by the coder's end marker then a four-byte row count. For the
// arithmetic coder the marker {0xFF,0xAC} cannot occur in valid data (MQ byte
// stuffing forbids 0xFF followed by >0x8F), so it locates the end on its own; for
// MMR the {0x00,0x00} marker is ambiguous, so the declared region height (which
// the terminator repeats) disambiguates it.
func unknownGenericLength(data []byte, typ int) (int, bool) {
	if typ != 36 && typ != 38 && typ != 39 { // immediate/intermediate generic region
		return 0, false
	}
	if len(data) < 18 { // region info (17) + generic flags (1)
		return 0, false
	}
	if data[17]&1 == 0 { // arithmetic: the FF AC marker is self-delimiting
		for i := 18; i+6 <= len(data); i++ {
			if data[i] == 0xFF && data[i+1] == 0xAC {
				return i + 6, true // 2 marker bytes + 4-byte row count
			}
		}
		return 0, false
	}
	var pat [6]byte // MMR: {0x00,0x00} + region height
	copy(pat[2:], data[4:8])
	for i := 18; i+6 <= len(data); i++ {
		if [6]byte(data[i:i+6]) == pat {
			return i + 6, true
		}
	}
	return 0, false
}

// run dispatches each segment. Unknown or not-yet-supported region types return
// an error so the caller can fall back to the raw encoded bytes.
func (d *jbig2Decoder) run(segs []jbSegment) error {
	for _, seg := range segs {
		if err := d.dispatch(seg); err != nil {
			return err
		}
	}
	return nil
}

// dispatch decodes one segment by type.
func (d *jbig2Decoder) dispatch(seg jbSegment) error {
	switch seg.typ {
	case 48: // page info
		return d.readPageInfo(seg)
	case 36, 38, 39: // immediate generic region
		return d.readGenericRegion(seg)
	case 0: // symbol dictionary
		return d.readSymbolDict(seg)
	case 4, 6, 7: // text region (intermediate / immediate / immediate lossless)
		return d.readTextRegion(seg)
	case 40, 42, 43: // generic refinement region
		return d.readRefinementRegion(seg)
	case 16: // pattern dictionary
		return d.readPatternDict(seg)
	case 20, 22, 23: // halftone region
		return d.readHalftoneRegion(seg)
	case 53: // custom Huffman table
		return d.readCustomTable(seg)
	case 49, 50, 51, 62: // end of page/stripe/file, extension
		return nil
	default:
		return errJBIG2Unsupported
	}
}

func (d *jbig2Decoder) readPageInfo(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	w, ok1 := r.u32()
	h, ok2 := r.u32()
	r.u32() // X resolution
	r.u32() // Y resolution
	flags, ok3 := r.u8()
	if !ok1 || !ok2 || !ok3 {
		return errJBIG2Unsupported
	}
	if h == 0xFFFFFFFF {
		h = uint32(d.imgH)
	}
	if int(w) != d.imgW || int(h) <= 0 || int(w) <= 0 || int(w) > 1<<20 || int(h) > 1<<20 {
		// Trust the image dictionary's geometry for the canvas size.
		w, h = uint32(d.imgW), uint32(d.imgH)
	}
	defPixel := (flags >> 2) & 1
	d.page = newJBBitmap(int(w), int(h), defPixel)
	return nil
}

// regionInfo is the region segment information field (7.4.1).
type regionInfo struct {
	w, h, x, y int
	combOp     int
}

func readRegionInfo(r *jbReader) (regionInfo, bool) {
	var ri regionInfo
	w, ok1 := r.u32()
	h, ok2 := r.u32()
	x, ok3 := r.u32()
	y, ok4 := r.u32()
	flags, ok5 := r.u8()
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return ri, false
	}
	ri = regionInfo{w: int(w), h: int(h), x: int(x), y: int(y), combOp: int(flags & 7)}
	if ri.w <= 0 || ri.h <= 0 || ri.w > 1<<20 || ri.h > 1<<20 {
		return ri, false
	}
	return ri, true
}

func (d *jbig2Decoder) readGenericRegion(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	ri, ok := readRegionInfo(r)
	if !ok {
		return errJBIG2Unsupported
	}
	flags, ok := r.u8()
	if !ok {
		return errJBIG2Unsupported
	}
	mmr := flags&1 != 0
	template := int((flags >> 1) & 3)
	tpgdon := flags&8 != 0

	var at []atPixel
	if !mmr {
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
	}

	var bmp *jbBitmap
	var err error
	if mmr {
		bmp, err = decodeGenericMMR(r.data[r.pos:], ri.w, ri.h)
	} else {
		bmp, err = decodeGenericArith(r.data[r.pos:], ri.w, ri.h, template, tpgdon, at)
	}
	if err != nil {
		return err
	}
	if seg.typ == 36 { // intermediate: stored for a later segment, not composed
		d.storeIntermediate(seg.number, bmp)
		return nil
	}
	if d.page == nil {
		// No explicit page info; make the region the page.
		d.page = newJBBitmap(d.imgW, d.imgH, 0)
	}
	d.page.blit(bmp, ri.x, ri.y, ri.combOp)
	return nil
}

// storeIntermediate records an intermediate region result under its segment
// number so a later refinement region can use it as a reference (T.88 7.4.6/7.4.7).
func (d *jbig2Decoder) storeIntermediate(number uint32, bmp *jbBitmap) {
	if d.intermrgn == nil {
		d.intermrgn = map[uint32]*jbBitmap{}
	}
	d.intermrgn[number] = bmp
}

// blit composes src onto b at (dx,dy) using the JBIG2 combination operator
// (0 OR, 1 AND, 2 XOR, 3 XNOR, 4 REPLACE).
func (b *jbBitmap) blit(src *jbBitmap, dx, dy, op int) {
	for y := 0; y < src.h; y++ {
		py := dy + y
		if py < 0 || py >= b.h {
			continue
		}
		for x := 0; x < src.w; x++ {
			px := dx + x
			if px < 0 || px >= b.w {
				continue
			}
			s := src.pix[y*src.w+x]
			i := py*b.w + px
			switch op {
			case 0:
				b.pix[i] |= s
			case 1:
				b.pix[i] &= s
			case 2:
				b.pix[i] ^= s
			case 3:
				b.pix[i] = 1 ^ (b.pix[i] ^ s)
			default:
				b.pix[i] = s
			}
		}
	}
}

// subregion copies a w x h window of b starting at (x,y) into a new bitmap,
// reading 0 outside b's bounds.
func (b *jbBitmap) subregion(x, y, w, h int) *jbBitmap {
	s := newJBBitmap(w, h, 0)
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			s.pix[j*w+i] = b.get(x+i, y+j)
		}
	}
	return s
}

// readRefinementRegion decodes a standalone generic refinement region (types
// 40/42/43), refining the page content at the region's location in place.
func (d *jbig2Decoder) readRefinementRegion(seg jbSegment) error {
	r := &jbReader{data: seg.data}
	ri, ok := readRegionInfo(r)
	if !ok {
		return errJBIG2Unsupported
	}
	flags, ok := r.u8()
	if !ok {
		return errJBIG2Unsupported
	}
	template := int(flags & 1)
	tpgron := flags&2 != 0
	var at []atPixel
	if template == 0 {
		for i := 0; i < 2; i++ {
			ax, ok1 := r.s8()
			ay, ok2 := r.s8()
			if !ok1 || !ok2 {
				return errJBIG2Unsupported
			}
			at = append(at, atPixel{ax, ay})
		}
	}
	// The reference is an intermediate region this segment refers to, if any;
	// otherwise it is the page content at the region's location (7.4.7.2).
	var ref *jbBitmap
	for _, rr := range seg.refs {
		if im, ok := d.intermrgn[rr]; ok {
			ref = im
			break
		}
	}
	onPage := ref == nil
	if onPage {
		if d.page == nil {
			d.page = newJBBitmap(d.imgW, d.imgH, 0)
		}
		ref = d.page.subregion(ri.x, ri.y, ri.w, ri.h)
	}
	dec := newMQDecoder(r.data[r.pos:], 0, r.remaining())
	cx := make([]mqState, 1<<13)
	out := decodeRefinement(dec, cx, ri.w, ri.h, template, ref, 0, 0, tpgron, at)
	if seg.typ == 40 { // intermediate: stored, not composed
		d.storeIntermediate(seg.number, out)
		return nil
	}
	if d.page == nil {
		d.page = newJBBitmap(d.imgW, d.imgH, 0)
	}
	// Refining the page in place replaces that region; refining a referenced
	// intermediate region composes the result with the region's operator.
	op := 4 // REPLACE
	if !onPage {
		op = ri.combOp
	}
	d.page.blit(out, ri.x, ri.y, op)
	return nil
}

type atPixel struct{ x, y int }

// decodeGenericMMR decodes an MMR (T.6 Group 4) coded generic region, reusing
// the CCITT decoder. Its packed output (0 = black) is expanded to the internal
// one-byte-per-pixel form (1 = black).
func decodeGenericMMR(data []byte, w, h int) (*jbBitmap, error) {
	packed, err := decodeCCITT(data, ccittParams{k: -1, columns: w, rows: h})
	if err != nil {
		return nil, err
	}
	stride := (w + 7) / 8
	bmp := newJBBitmap(w, h, 0)
	for y := 0; y < h; y++ {
		row := packed[y*stride:]
		for x := 0; x < w; x++ {
			if row[x/8]>>(7-uint(x%8))&1 == 0 { // 0 bit = black
				bmp.pix[y*w+x] = 1
			}
		}
	}
	return bmp, nil
}

// mmrPlaneReader decodes successive Group-4 (MMR) bitmaps from one shared bit
// stream. JBIG2 codes an MMR pattern-dictionary collective bitmap as a single
// plane, and an MMR grayscale image (T.88 Annex C.5) as a run of planes each
// terminated by an EOFB and byte-aligned, so the same reader must span them all.
type mmrPlaneReader struct {
	br *ccittBitReader
}

func newMMRPlaneReader(data []byte) *mmrPlaneReader {
	return &mmrPlaneReader{br: &ccittBitReader{data: data}}
}

// plane decodes one w x h Group-4 bitmap (1 = black), then, when eofb is set,
// consumes the trailing EOFB and byte-aligns so the reader is positioned at the
// next plane.
func (m *mmrPlaneReader) plane(w, h int, eofb bool) (*jbBitmap, error) {
	bmp := newJBBitmap(w, h, 0)
	ref := []int{}
	for row := 0; row < h; row++ {
		cur, err := decode2DLine(m.br, ref, w)
		if err != nil {
			return nil, err
		}
		color, prev := 0, 0
		for _, c := range cur {
			if c > w {
				c = w
			}
			if color == 1 {
				for x := prev; x < c; x++ {
					bmp.pix[row*w+x] = 1
				}
			}
			prev, color = c, color^1
		}
		if color == 1 {
			for x := prev; x < w; x++ {
				bmp.pix[row*w+x] = 1
			}
		}
		ref = cur
	}
	if eofb {
		m.br.consumeEOFB()
		m.br.align()
	}
	return bmp, nil
}

// Generic-region coding templates (T.88 6.2.5.3): the fixed context pixels for
// GBTEMPLATE 0..3, excluding the adaptive (AT) pixels which are merged in below.
var jbCodingTemplates = [4][]atPixel{
	{{-1, -2}, {0, -2}, {1, -2}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1}, {-4, 0}, {-3, 0}, {-2, 0}, {-1, 0}},
	{{-1, -2}, {0, -2}, {1, -2}, {2, -2}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {2, -1}, {-3, 0}, {-2, 0}, {-1, 0}},
	{{-1, -2}, {0, -2}, {1, -2}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-2, 0}, {-1, 0}},
	{{-3, -1}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-4, 0}, {-3, 0}, {-2, 0}, {-1, 0}},
}

// jbReusedContexts are the SLTP contexts for typical prediction (TPGDON), one
// per template, in the sorted-template bit ordering used here.
var jbReusedContexts = [4]int{0x9B25, 0x0795, 0x00E5, 0x0195}

// decodeGenericArith decodes an arithmetic-coded generic region (T.88 6.2.5.7)
// with its own decoder and context.
func decodeGenericArith(data []byte, w, h, template int, tpgdon bool, at []atPixel) (*jbBitmap, error) {
	if template < 0 || template > 3 {
		return nil, errJBIG2Unsupported
	}
	dec := newMQDecoder(data, 0, len(data))
	cx := make([]mqState, 1<<16)
	return decodeGenericInto(dec, cx, w, h, template, at, tpgdon, nil), nil
}

// decodeGenericInto decodes a generic region into a bitmap using a caller-owned
// decoder and context array. Symbol-dictionary decoding reuses this with a
// shared context across all symbols in the dictionary. When skip is non-nil, a
// set pixel in skip forces the corresponding output pixel to 0 without decoding
// (used by halftone grayscale-image decoding).
func decodeGenericInto(dec *mqDecoder, cx []mqState, w, h, template int, at []atPixel, tpgdon bool, skip *jbBitmap) *jbBitmap {
	// Build the full template (fixed pixels + AT pixels) and sort into raster
	// order so the context label matches the reused TPGDON contexts.
	tmpl := append([]atPixel{}, jbCodingTemplates[template]...)
	tmpl = append(tmpl, at...)
	sort.SliceStable(tmpl, func(i, j int) bool {
		if tmpl[i].y != tmpl[j].y {
			return tmpl[i].y < tmpl[j].y
		}
		return tmpl[i].x < tmpl[j].x
	})

	bmp := newJBBitmap(w, h, 0)
	ltp := 0
	for y := 0; y < h; y++ {
		if tpgdon {
			ltp ^= dec.decode(cx, jbReusedContexts[template])
			if ltp == 1 {
				if y > 0 {
					copy(bmp.pix[y*w:(y+1)*w], bmp.pix[(y-1)*w:y*w])
				}
				continue
			}
		}
		for x := 0; x < w; x++ {
			if skip != nil && skip.pix[y*w+x] != 0 {
				continue // forced-0 pixel
			}
			ctx := 0
			for _, p := range tmpl {
				ctx = (ctx << 1) | int(bmp.get(x+p.x, y+p.y))
			}
			bmp.pix[y*w+x] = byte(dec.decode(cx, ctx))
		}
	}
	return bmp
}
