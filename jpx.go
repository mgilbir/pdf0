package pdf0

import (
	"encoding/binary"
	"errors"
)

// This file decodes the JPEG 2000 image codec (ISO/IEC 15444-1 / ITU-T T.800)
// as embedded in PDF via the JPXDecode filter. JPEG 2000 codes an image with a
// wavelet transform and the EBCOT entropy coder: the image is level-shifted and
// (optionally) colour-transformed, wavelet-decomposed into subbands, each subband
// split into code-blocks whose bit-planes are MQ-arithmetic coded (tier-1), and
// the code-block contributions packed into quality/resolution packets (tier-2).
//
// A PDF JPXDecode stream is either a raw codestream (starting with the SOC
// marker) or a JP2 box wrapper whose jp2c box holds the codestream. This file is
// built up in milestones; the current milestone parses the codestream structure
// (image/tile geometry, coding and quantization parameters, tile-part data). The
// pixel pipeline (tier-2, tier-1, inverse DWT) is layered on top in later files.

var errJPX = errors.New("jpx: unsupported or malformed codestream")

// JPEG 2000 marker codes (T.800 Table A.2).
const (
	jpxSOC = 0xFF4F
	jpxSIZ = 0xFF51
	jpxCOD = 0xFF52
	jpxCOC = 0xFF53
	jpxRGN = 0xFF5E
	jpxQCD = 0xFF5C
	jpxQCC = 0xFF5D
	jpxPOC = 0xFF5F
	jpxTLM = 0xFF55
	jpxPLM = 0xFF57
	jpxPLT = 0xFF58
	jpxPPM = 0xFF60
	jpxPPT = 0xFF61
	jpxSOT = 0xFF90
	jpxSOP = 0xFF91
	jpxEPH = 0xFF92
	jpxSOD = 0xFF93
	jpxEOC = 0xFFD9
	jpxCOM = 0xFF64
	jpxCRG = 0xFF63
)

type jpxComponent struct {
	depth  int
	signed bool
	dx, dy int // sub-sampling factors
}

type jpxCoding struct {
	sop, eph      bool
	precinctsUsed bool
	progOrder     int
	layers        int
	mct           int // multiple-component transform (0 none, 1 RCT/ICT)
	levels        int // number of decomposition levels
	cbW, cbH      int // code-block dimensions (pixels)
	cbStyle       int
	transform     int   // 0 = irreversible 9/7, 1 = reversible 5/3
	precinctW     []int // per resolution level (exp), when precinctsUsed
	precinctH     []int
}

type jpxStep struct{ exp, mant int }

type jpxQuant struct {
	style     int // 0 none (reversible), 1 scalar derived, 2 scalar expounded
	guardBits int
	steps     []jpxStep
}

type jpxTilePart struct {
	tile int
	data []byte
}

// jpxProg is one progression stage from a POC marker (T.800 A.6.6): packets are
// emitted for resolutions [resStart,resEnd) × components [compStart,compEnd) ×
// layers [0,layerEnd) in the order given by prog.
type jpxProg struct {
	resStart, compStart, layerEnd, resEnd, compEnd, prog int
}

type jpxImage struct {
	xsiz, ysiz     int
	xosiz, yosiz   int
	xtsiz, ytsiz   int
	xtosiz, ytosiz int
	comps          []jpxComponent
	cod            jpxCoding
	qcd            jpxQuant
	tileParts      []jpxTilePart
	poc            []jpxProg // progression-order changes, when a POC marker is present
	roishift       []int     // per-component region-of-interest MaxShift value (RGN)
	tileRoishift   map[int][]int
	tilePoc        map[int][]jpxProg
}

// numXTiles / numYTiles give the tile grid dimensions (T.800 B.3).
func (im *jpxImage) numXTiles() int {
	return ceilDiv(im.xsiz-im.xtosiz, im.xtsiz)
}
func (im *jpxImage) numYTiles() int {
	return ceilDiv(im.ysiz-im.ytosiz, im.ytsiz)
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// precinctExp returns the precinct-size exponents (PPx, PPy) for resolution r.
// Without explicit precinct sizes the maximal exponent 15 gives one precinct per
// resolution.
func (im *jpxImage) precinctExp(r int) (int, int) {
	if im.cod.precinctsUsed && r < len(im.cod.precinctW) {
		return im.cod.precinctW[r], im.cod.precinctH[r]
	}
	return 15, 15
}

// roiShift returns the region-of-interest MaxShift value for a tile-component,
// preferring a tile-part RGN over the main-header RGN (0 when none applies).
func (im *jpxImage) roiShift(tile, comp int) int {
	if v, ok := im.tileRoishift[tile]; ok && comp >= 0 && comp < len(v) {
		if v[comp] != 0 {
			return v[comp]
		}
	}
	if comp >= 0 && comp < len(im.roishift) {
		return im.roishift[comp]
	}
	return 0
}

// tileProgressions returns the POC stages that apply to a tile (a tile-part POC
// overrides the main-header POC).
func (im *jpxImage) tileProgressions(tile int) []jpxProg {
	if v, ok := im.tilePoc[tile]; ok && len(v) > 0 {
		return v
	}
	return im.poc
}

// tileCoords returns the pixel bounds [x0,x1)×[y0,y1) of tile t on the reference
// grid (T.800 B.3).
func (im *jpxImage) tileCoords(t int) (x0, y0, x1, y1 int) {
	ntx := im.numXTiles()
	if ntx <= 0 {
		return
	}
	p, q := t%ntx, t/ntx
	x0 = maxInt(im.xtosiz+p*im.xtsiz, im.xosiz)
	y0 = maxInt(im.ytosiz+q*im.ytsiz, im.yosiz)
	x1 = minInt(im.xtosiz+(p+1)*im.xtsiz, im.xsiz)
	y1 = minInt(im.ytosiz+(q+1)*im.ytsiz, im.ysiz)
	return
}

// tileData concatenates the data bodies of all tile-parts belonging to tile t.
func (im *jpxImage) tileData(t int) []byte {
	var out []byte
	for _, tp := range im.tileParts {
		if tp.tile == t {
			out = append(out, tp.data...)
		}
	}
	return out
}

// parseJPX parses a raw codestream or a JP2-wrapped one into a jpxImage.
func parseJPX(data []byte) (*jpxImage, error) {
	cs, err := jpxCodestream(data)
	if err != nil {
		return nil, err
	}
	return parseJPXCodestream(cs)
}

// jpxCodestream returns the raw codestream, unwrapping a JP2 box structure when
// present (locating the jp2c box).
func jpxCodestream(data []byte) ([]byte, error) {
	if len(data) < 2 {
		return nil, errJPX
	}
	if binary.BigEndian.Uint16(data) == jpxSOC {
		return data, nil // raw codestream
	}
	// JP2 box structure: walk top-level boxes looking for 'jp2c'.
	pos := 0
	for pos+8 <= len(data) {
		boxLen := int(binary.BigEndian.Uint32(data[pos:]))
		boxType := string(data[pos+4 : pos+8])
		hdr := 8
		if boxLen == 1 { // 64-bit extended length
			if pos+16 > len(data) {
				return nil, errJPX
			}
			boxLen = int(binary.BigEndian.Uint64(data[pos+8:]))
			hdr = 16
		} else if boxLen == 0 {
			boxLen = len(data) - pos // to end of file
		}
		if boxLen < hdr || pos+boxLen > len(data) {
			return nil, errJPX
		}
		if boxType == "jp2c" {
			return data[pos+hdr : pos+boxLen], nil
		}
		pos += boxLen
	}
	return nil, errJPX
}

// jpxMarkerReader walks marker segments in a codestream.
type jpxMarkerReader struct {
	data []byte
	pos  int
}

func parseJPXCodestream(cs []byte) (*jpxImage, error) {
	r := &jpxMarkerReader{data: cs}
	if r.u16() != jpxSOC {
		return nil, errJPX
	}
	im := &jpxImage{}
	haveSIZ := false
	for r.pos+2 <= len(cs) {
		marker := r.u16()
		if marker >= 0xFF30 && marker <= 0xFF3F {
			continue // reserved delimiting markers carry no segment
		}
		switch marker {
		case jpxEOC:
			return im, nil
		case jpxSOD:
			return nil, errJPX // SOD only appears inside a tile-part
		case jpxSOT:
			if err := r.readTilePart(im); err != nil {
				return nil, err
			}
		default:
			seg, ok := r.segment()
			if !ok {
				return nil, errJPX
			}
			switch marker {
			case jpxSIZ:
				if err := parseSIZ(seg, im); err != nil {
					return nil, err
				}
				haveSIZ = true
			case jpxCOD:
				if err := parseCOD(seg, im); err != nil {
					return nil, err
				}
			case jpxQCD:
				if err := parseQCD(seg, im); err != nil {
					return nil, err
				}
			case jpxPOC:
				parsePOC(seg, im)
			case jpxRGN:
				parseRGN(seg, im)
			case jpxCOC, jpxQCC, jpxTLM, jpxPLM, jpxPLT,
				jpxPPM, jpxPPT, jpxCOM, jpxCRG:
				// Recognised but not needed for the baseline path.
			default:
				return nil, errJPX
			}
		}
		if !haveSIZ {
			return nil, errJPX // SIZ must immediately follow SOC
		}
	}
	return im, nil
}

func (r *jpxMarkerReader) u8() int {
	if r.pos >= len(r.data) {
		r.pos++
		return 0
	}
	v := int(r.data[r.pos])
	r.pos++
	return v
}

func (r *jpxMarkerReader) u16() int {
	v := (r.u8() << 8)
	v |= r.u8()
	return v
}

func (r *jpxMarkerReader) u32() int {
	v := r.u16() << 16
	v |= r.u16()
	return v
}

// segment returns the body of a length-prefixed marker segment (the 2-byte
// length includes itself).
func (r *jpxMarkerReader) segment() ([]byte, bool) {
	if r.pos+2 > len(r.data) {
		return nil, false
	}
	length := r.u16()
	if length < 2 || r.pos+length-2 > len(r.data) {
		return nil, false
	}
	seg := r.data[r.pos : r.pos+length-2]
	r.pos += length - 2
	return seg, true
}

// readTilePart consumes an SOT..SOD tile-part header and captures its data body.
func (r *jpxMarkerReader) readTilePart(im *jpxImage) error {
	seg, ok := r.segment()
	if !ok || len(seg) < 8 {
		return errJPX
	}
	tile := int(binary.BigEndian.Uint16(seg[0:]))
	psot := int(binary.BigEndian.Uint32(seg[2:])) // total tile-part length from SOT marker
	// Scan to the SOD marker; tile-part data runs from after SOD for the
	// remaining length.
	sotStart := r.pos - len(seg) - 4 // position of the SOT marker itself
	// Tile-part header markers override the main-header defaults for this tile.
	for r.pos+2 <= len(r.data) {
		m := r.u16()
		if m == jpxSOD {
			break
		}
		mseg, ok := r.segment()
		if !ok {
			return errJPX
		}
		switch m {
		case jpxRGN:
			parseTileRGN(mseg, im, tile)
		case jpxPOC:
			parseTilePOC(mseg, im, tile)
		}
	}
	var end int
	if psot == 0 {
		end = len(r.data) // last tile-part: runs to EOC
	} else {
		end = sotStart + psot
	}
	if end > len(r.data) {
		end = len(r.data)
	}
	if r.pos > end {
		return errJPX
	}
	im.tileParts = append(im.tileParts, jpxTilePart{tile: tile, data: r.data[r.pos:end]})
	r.pos = end
	return nil
}

func parseSIZ(seg []byte, im *jpxImage) error {
	if len(seg) < 36 {
		return errJPX
	}
	g := func(o int) int { return int(binary.BigEndian.Uint32(seg[o:])) }
	im.xsiz, im.ysiz = g(2), g(6)
	im.xosiz, im.yosiz = g(10), g(14)
	im.xtsiz, im.ytsiz = g(18), g(22)
	im.xtosiz, im.ytosiz = g(26), g(30)
	csiz := int(binary.BigEndian.Uint16(seg[34:]))
	if csiz <= 0 || csiz > 16384 || len(seg) < 36+3*csiz {
		return errJPX
	}
	if im.xtsiz <= 0 || im.ytsiz <= 0 || im.xsiz <= im.xosiz || im.ysiz <= im.yosiz {
		return errJPX
	}
	im.comps = make([]jpxComponent, csiz)
	for c := 0; c < csiz; c++ {
		ssiz := seg[36+c*3]
		im.comps[c] = jpxComponent{
			depth:  int(ssiz&0x7F) + 1,
			signed: ssiz&0x80 != 0,
			dx:     int(seg[37+c*3]),
			dy:     int(seg[38+c*3]),
		}
	}
	return nil
}

func parseCOD(seg []byte, im *jpxImage) error {
	if len(seg) < 10 {
		return errJPX
	}
	scod := int(seg[0])
	cod := jpxCoding{
		precinctsUsed: scod&1 != 0,
		sop:           scod&2 != 0,
		eph:           scod&4 != 0,
		progOrder:     int(seg[1]),
		layers:        int(binary.BigEndian.Uint16(seg[2:])),
		mct:           int(seg[4]),
		levels:        int(seg[5]),
		cbW:           1 << (int(seg[6]&0x0F) + 2),
		cbH:           1 << (int(seg[7]&0x0F) + 2),
		cbStyle:       int(seg[8]),
		transform:     int(seg[9]),
	}
	if cod.levels > 32 {
		return errJPX
	}
	if cod.precinctsUsed {
		off := 10
		for i := 0; i <= cod.levels; i++ {
			if off >= len(seg) {
				return errJPX
			}
			b := int(seg[off])
			cod.precinctW = append(cod.precinctW, b&0x0F)
			cod.precinctH = append(cod.precinctH, b>>4)
			off++
		}
	}
	im.cod = cod
	return nil
}

// parsePOC parses a POC marker (T.800 A.6.6) into progression stages. Component
// range fields are two bytes when there are more than 256 components, otherwise
// one; the layer-end field is always two. A malformed segment is ignored (the
// COD progression then stands).
func parsePOC(seg []byte, im *jpxImage) {
	wide := len(im.comps) > 256
	entry := 7
	if wide {
		entry = 9
	}
	for off := 0; off+entry <= len(seg); off += entry {
		p := off
		rs := int(seg[p])
		p++
		var cs int
		if wide {
			cs = int(binary.BigEndian.Uint16(seg[p:]))
			p += 2
		} else {
			cs = int(seg[p])
			p++
		}
		lye := int(binary.BigEndian.Uint16(seg[p:]))
		p += 2
		re := int(seg[p])
		p++
		var ce int
		if wide {
			ce = int(binary.BigEndian.Uint16(seg[p:]))
			p += 2
		} else {
			ce = int(seg[p])
			p++
		}
		prog := int(seg[p])
		im.poc = append(im.poc, jpxProg{
			resStart: rs, compStart: cs, layerEnd: lye,
			resEnd: re, compEnd: ce, prog: prog,
		})
	}
}

// parseRGN parses an RGN marker (T.800 A.6.3), recording the region-of-interest
// MaxShift value for a component. Coefficients scaled up by this many bit-planes
// belong to the ROI and are shifted back down after tier-1 (Annex H). The
// component index field is two bytes when there are more than 256 components.
// rgnValues decodes an RGN segment into (component, shift), returning ok=false on
// a malformed segment. The component index field is two bytes above 256 comps.
func rgnValues(seg []byte, nc int) (comp, shift int, ok bool) {
	p := 0
	if nc > 256 {
		if len(seg) < 4 {
			return 0, 0, false
		}
		comp = int(binary.BigEndian.Uint16(seg))
		p = 2
	} else {
		if len(seg) < 3 {
			return 0, 0, false
		}
		comp = int(seg[0])
		p = 1
	}
	// seg[p] is Srgn (ROI style; 0 = implicit MaxShift), seg[p+1] is SPrgn (shift).
	if p+1 >= len(seg) || comp < 0 || comp >= nc {
		return 0, 0, false
	}
	return comp, int(seg[p+1]), true
}

func parseRGN(seg []byte, im *jpxImage) {
	comp, shift, ok := rgnValues(seg, len(im.comps))
	if !ok {
		return
	}
	if len(im.roishift) < len(im.comps) {
		im.roishift = make([]int, len(im.comps))
	}
	im.roishift[comp] = shift
}

func parseTileRGN(seg []byte, im *jpxImage, tile int) {
	comp, shift, ok := rgnValues(seg, len(im.comps))
	if !ok {
		return
	}
	if im.tileRoishift == nil {
		im.tileRoishift = map[int][]int{}
	}
	v := im.tileRoishift[tile]
	if len(v) < len(im.comps) {
		v = make([]int, len(im.comps))
		im.tileRoishift[tile] = v
	}
	v[comp] = shift
}

func parseTilePOC(seg []byte, im *jpxImage, tile int) {
	if im.tilePoc == nil {
		im.tilePoc = map[int][]jpxProg{}
	}
	tmp := &jpxImage{comps: im.comps}
	parsePOC(seg, tmp)
	if len(tmp.poc) > 0 {
		im.tilePoc[tile] = append(im.tilePoc[tile], tmp.poc...)
	}
}

func parseQCD(seg []byte, im *jpxImage) error {
	if len(seg) < 1 {
		return errJPX
	}
	sqcd := int(seg[0])
	qcd := jpxQuant{style: sqcd & 0x1F, guardBits: sqcd >> 5}
	body := seg[1:]
	switch qcd.style {
	case 0: // no quantization (reversible): one byte per subband, exponent in top 5 bits
		for _, b := range body {
			qcd.steps = append(qcd.steps, jpxStep{exp: int(b) >> 3})
		}
	case 1: // scalar derived: a single 2-byte value
		if len(body) < 2 {
			return errJPX
		}
		v := int(binary.BigEndian.Uint16(body))
		qcd.steps = append(qcd.steps, jpxStep{exp: v >> 11, mant: v & 0x7FF})
	case 2: // scalar expounded: 2 bytes per subband
		for i := 0; i+2 <= len(body); i += 2 {
			v := int(binary.BigEndian.Uint16(body[i:]))
			qcd.steps = append(qcd.steps, jpxStep{exp: v >> 11, mant: v & 0x7FF})
		}
	default:
		return errJPX
	}
	im.qcd = qcd
	return nil
}
