package pdf0

import "errors"

// This file decodes CCITTFaxDecode image data: the ITU-T T.4 (Group 3) and T.6
// (Group 4) fax coding schemes used by PDF for bilevel (1-bit) images, typically
// scanned documents. It is a self-contained decoder over the modified-Huffman run
// codes and the two-dimensional (READ) coding modes; no external dependency.
//
// The decoder handles the three /K variants of the CCITTFaxDecode filter:
//
//   - K < 0  Group 4, pure two-dimensional coding (T.6). By far the most common
//            in PDFs.
//   - K = 0  Group 3 one-dimensional coding (T.4, modified Huffman).
//   - K > 0  Group 3 two-dimensional coding (T.4): each line carries a mode bit
//            selecting 1-D or 2-D coding relative to the line above.
//
// Output is packed 1 bit per pixel, MSB first, one byte-aligned row at a time, in
// the normal PDF image convention: a 0 bit is a black pixel and a 1 bit is white.
// /BlackIs1 concerns only the polarity of the encoded stream's consumer, not the
// pixel colours a fax coder reconstructs (a coded black run is always black), so
// it does not change this output — the decoded image is always emitted 0=black so
// it renders directly as a DeviceGray sample stream.

// ccittParams carries the CCITTFaxDecode /DecodeParms that steer the decoder.
type ccittParams struct {
	k         int  // /K: <0 Group 4, 0 Group 3 1-D, >0 Group 3 2-D
	columns   int  // /Columns: pixels per row (default 1728)
	rows      int  // /Rows: number of rows (0 = decode until end of data)
	byteAlign bool // /EncodedByteAlign: each row starts on a byte boundary
}

var errCCITTData = errors.New("ccitt: malformed fax data")

// decodeCCITT decodes a CCITT Group 3/4 stream into packed 1-bpp rows.
func decodeCCITT(data []byte, p ccittParams) ([]byte, error) {
	cols := p.columns
	if cols <= 0 {
		cols = 1728
	}
	if cols > 1<<20 {
		return nil, errCCITTData // implausible width; refuse before allocating
	}
	maxRows := p.rows
	if maxRows <= 0 {
		maxRows = 1 << 20 // decode until the data runs out, but keep it bounded
	}

	br := &ccittBitReader{data: data}
	stride := (cols + 7) / 8
	var out []byte
	ref := []int{} // reference line changing elements; empty = all white

	for row := 0; row < maxRows; row++ {
		if p.byteAlign && row > 0 {
			br.align()
		}
		if br.eof() {
			break
		}
		// Group 3 lines may be preceded by end-of-line codes; a run of EOLs
		// (the end-of-block marker) means the image is finished.
		if p.k >= 0 {
			if eob := br.skipEOLs(); eob {
				break
			}
			if br.eof() {
				break
			}
		}

		var cur []int
		var err error
		switch {
		case p.k < 0:
			cur, err = decode2DLine(br, ref, cols)
		case p.k == 0:
			cur, err = decode1DLine(br, cols)
		default: // K > 0: a mode bit selects 1-D (1) or 2-D (0)
			bit, ok := br.bit()
			if !ok {
				err = errCCITTData
			} else if bit == 1 {
				cur, err = decode1DLine(br, cols)
			} else {
				cur, err = decode2DLine(br, ref, cols)
			}
		}
		if err != nil {
			if p.rows <= 0 && len(out) > 0 {
				break // unknown length: a decode failure marks the end of data
			}
			return nil, err
		}
		out = append(out, packCCITTRow(cur, cols, stride)...)
		ref = cur
	}
	if len(out) == 0 {
		return nil, errCCITTData
	}
	return out, nil
}

// decode1DLine decodes one modified-Huffman (Group 3 1-D) line into its changing
// elements: the pixel positions at which the colour flips, starting from white.
func decode1DLine(br *ccittBitReader, cols int) ([]int, error) {
	var changes []int
	pos, color := 0, 0 // colour 0 = white, 1 = black
	for pos < cols {
		run, err := decodeRun(br, color)
		if err != nil {
			return nil, err
		}
		pos += run
		if pos > cols {
			pos = cols
		}
		changes = append(changes, pos)
		color ^= 1
	}
	return changes, nil
}

// decode2DLine decodes one two-dimensional (READ) line relative to ref, the
// changing elements of the line above. Shared by Group 4 and Group 3 2-D lines.
func decode2DLine(br *ccittBitReader, ref []int, cols int) ([]int, error) {
	var cur []int
	a0, color := -1, 0
	for a0 < cols {
		b1, b2 := findB1B2(ref, a0, color, cols)
		mode, err := readMode(br)
		if err != nil {
			return nil, err
		}
		switch {
		case mode == modePass:
			// The current colour extends across b1..b2; no changing element.
			a0 = b2
		case mode == modeHoriz:
			start := a0
			if start < 0 {
				start = 0
			}
			r1, err := decodeRun(br, color)
			if err != nil {
				return nil, err
			}
			r2, err := decodeRun(br, color^1)
			if err != nil {
				return nil, err
			}
			a1 := clampInt(start+r1, 0, cols)
			a2 := clampInt(a1+r2, 0, cols)
			cur = append(cur, a1, a2)
			a0 = a2
		case mode >= modeV0 && mode <= modeVL3:
			a1 := clampInt(b1+vertDelta[mode], 0, cols)
			cur = append(cur, a1)
			a0 = a1
			color ^= 1
		default:
			return nil, errCCITTData // extension codes are not supported
		}
	}
	return cur, nil
}

// findB1B2 locates the two reference-line changing elements used by 2-D coding:
// b1 is the first changing element on the reference line to the right of a0 whose
// colour is opposite to the current colour, and b2 is the one after it. Positions
// past the end of the reference line default to the row width.
func findB1B2(ref []int, a0, color, cols int) (b1, b2 int) {
	i := 0
	for i < len(ref) && ref[i] <= a0 {
		i++
	}
	// A changing element at even index begins a black run, at odd index a white
	// run; b1 must have the colour opposite to a0's, i.e. index parity == color.
	if i < len(ref) && i%2 != color {
		i++
	}
	b1, b2 = cols, cols
	if i < len(ref) {
		b1 = ref[i]
	}
	if i+1 < len(ref) {
		b2 = ref[i+1]
	}
	return b1, b2
}

// packCCITTRow renders a row's changing elements into stride bytes of packed
// pixels: white is emitted as 1 bits, black runs are cleared to 0.
func packCCITTRow(changes []int, cols, stride int) []byte {
	row := make([]byte, stride)
	for i := range row {
		row[i] = 0xFF // start all white
	}
	color, prev := 0, 0
	clear := func(from, to int) {
		for x := from; x < to; x++ {
			row[x/8] &^= 1 << (7 - uint(x%8))
		}
	}
	for _, c := range changes {
		if c > cols {
			c = cols
		}
		if color == 1 {
			clear(prev, c)
		}
		prev, color = c, color^1
	}
	if color == 1 {
		clear(prev, cols)
	}
	return row
}

// decodeRun reads one complete run of the given colour: zero or more make-up
// codes (multiples of 64) followed by one terminating code (0..63).
func decodeRun(br *ccittBitReader, color int) (int, error) {
	total := 0
	for {
		run, err := readRunCode(br, color)
		if err != nil {
			return 0, err
		}
		total += run
		if run < 64 {
			return total, nil
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// --- bit reader ------------------------------------------------------------

type ccittBitReader struct {
	data []byte
	pos  int // bit offset from the start of data
}

func (b *ccittBitReader) bit() (int, bool) {
	if b.pos >= len(b.data)*8 {
		return 0, false
	}
	byteIdx := b.pos / 8
	bitIdx := 7 - uint(b.pos%8)
	b.pos++
	return int(b.data[byteIdx]>>bitIdx) & 1, true
}

func (b *ccittBitReader) eof() bool {
	return b.pos >= len(b.data)*8
}

func (b *ccittBitReader) align() {
	if r := b.pos % 8; r != 0 {
		b.pos += 8 - r
	}
}

// consumeEOFB consumes the Group-4 end-of-facsimile-block marker (two EOL codes,
// 000000000001000000000001) that terminates each MMR bitmap. Nothing is consumed
// if the marker is absent.
func (b *ccittBitReader) consumeEOFB() {
	save := b.pos
	if b.consumeEOL() && b.consumeEOL() {
		return
	}
	b.pos = save
}

// skipEOLs consumes any run of consecutive end-of-line codes (000000000001) at
// the current position. It reports whether two or more were seen back to back,
// which is the Group 3/4 end-of-block marker.
func (b *ccittBitReader) skipEOLs() (endOfBlock bool) {
	count := 0
	for {
		save := b.pos
		if !b.consumeEOL() {
			b.pos = save
			break
		}
		count++
	}
	return count >= 2
}

// consumeEOL reads a single EOL code (eleven 0 bits then a 1) if present at the
// current position, returning whether one was consumed. Leading fill bits (extra
// zeros) before the terminating 1 are tolerated.
func (b *ccittBitReader) consumeEOL() bool {
	save := b.pos
	zeros := 0
	for {
		bit, ok := b.bit()
		if !ok {
			b.pos = save
			return false
		}
		if bit == 0 {
			zeros++
			continue
		}
		// hit a 1 bit
		if zeros >= 11 {
			return true
		}
		b.pos = save
		return false
	}
}

// --- code tables -----------------------------------------------------------

// Two-dimensional coding modes (T.6 Table 1 / T.4 Table 4). The vertical modes
// are kept contiguous (V0, VR1..3, VL1..3) so decode2DLine can range-test them.
const (
	modePass = iota
	modeHoriz
	modeV0
	modeVR1
	modeVR2
	modeVR3
	modeVL1
	modeVL2
	modeVL3
)

// vertDelta is the a1 = b1 + delta offset for each vertical mode.
var vertDelta = map[int]int{
	modeV0: 0, modeVR1: 1, modeVR2: 2, modeVR3: 3,
	modeVL1: -1, modeVL2: -2, modeVL3: -3,
}

type ccittCode struct {
	bits string
	val  int
}

var (
	whiteRunTree *ccittNode
	blackRunTree *ccittNode
	modeTree     *ccittNode
)

// ccittNode is a binary trie node used to match a prefix-free code bit by bit.
type ccittNode struct {
	kids [2]*ccittNode
	val  int
	leaf bool
}

func buildCCITTTree(codes []ccittCode) *ccittNode {
	root := &ccittNode{}
	for _, c := range codes {
		n := root
		for i := 0; i < len(c.bits); i++ {
			b := c.bits[i] - '0'
			if n.kids[b] == nil {
				n.kids[b] = &ccittNode{}
			}
			n = n.kids[b]
		}
		n.leaf, n.val = true, c.val
	}
	return root
}

// matchCode walks the trie consuming bits until it reaches a leaf.
func matchCode(br *ccittBitReader, root *ccittNode) (int, bool) {
	n := root
	for i := 0; i < 24; i++ { // bounded: no CCITT code exceeds ~14 bits
		bit, ok := br.bit()
		if !ok {
			return 0, false
		}
		n = n.kids[bit]
		if n == nil {
			return 0, false
		}
		if n.leaf {
			return n.val, true
		}
	}
	return 0, false
}

func readRunCode(br *ccittBitReader, color int) (int, error) {
	tree := whiteRunTree
	if color == 1 {
		tree = blackRunTree
	}
	v, ok := matchCode(br, tree)
	if !ok {
		return 0, errCCITTData
	}
	return v, nil
}

func readMode(br *ccittBitReader) (int, error) {
	v, ok := matchCode(br, modeTree)
	if !ok {
		return 0, errCCITTData
	}
	return v, nil
}

func init() {
	whiteRunTree = buildCCITTTree(append(append([]ccittCode{}, whiteCodes...), sharedMakeup...))
	blackRunTree = buildCCITTTree(append(append([]ccittCode{}, blackCodes...), sharedMakeup...))
	modeTree = buildCCITTTree(modeCodes)
}

var modeCodes = []ccittCode{
	{"0001", modePass},
	{"001", modeHoriz},
	{"1", modeV0},
	{"011", modeVR1},
	{"000011", modeVR2},
	{"0000011", modeVR3},
	{"010", modeVL1},
	{"000010", modeVL2},
	{"0000010", modeVL3},
}

// sharedMakeup are the extended make-up codes (1792..2560) common to both colours
// (T.4 Table 3).
var sharedMakeup = []ccittCode{
	{"00000001000", 1792},
	{"00000001100", 1856},
	{"00000001101", 1920},
	{"000000010010", 1984},
	{"000000010011", 2048},
	{"000000010100", 2112},
	{"000000010101", 2176},
	{"000000010110", 2240},
	{"000000010111", 2304},
	{"000000011100", 2368},
	{"000000011101", 2432},
	{"000000011110", 2496},
	{"000000011111", 2560},
}

// whiteCodes are the white terminating (0..63) and make-up (64..1728) codes
// (T.4 Tables 2 and 3).
var whiteCodes = []ccittCode{
	{"00110101", 0}, {"000111", 1}, {"0111", 2}, {"1000", 3},
	{"1011", 4}, {"1100", 5}, {"1110", 6}, {"1111", 7},
	{"10011", 8}, {"10100", 9}, {"00111", 10}, {"01000", 11},
	{"001000", 12}, {"000011", 13}, {"110100", 14}, {"110101", 15},
	{"101010", 16}, {"101011", 17}, {"0100111", 18}, {"0001100", 19},
	{"0001000", 20}, {"0010111", 21}, {"0000011", 22}, {"0000100", 23},
	{"0101000", 24}, {"0101011", 25}, {"0010011", 26}, {"0100100", 27},
	{"0011000", 28}, {"00000010", 29}, {"00000011", 30}, {"00011010", 31},
	{"00011011", 32}, {"00010010", 33}, {"00010011", 34}, {"00010100", 35},
	{"00010101", 36}, {"00010110", 37}, {"00010111", 38}, {"00101000", 39},
	{"00101001", 40}, {"00101010", 41}, {"00101011", 42}, {"00101100", 43},
	{"00101101", 44}, {"00000100", 45}, {"00000101", 46}, {"00001010", 47},
	{"00001011", 48}, {"01010010", 49}, {"01010011", 50}, {"01010100", 51},
	{"01010101", 52}, {"00100100", 53}, {"00100101", 54}, {"01011000", 55},
	{"01011001", 56}, {"01011010", 57}, {"01011011", 58}, {"01001010", 59},
	{"01001011", 60}, {"00110010", 61}, {"00110011", 62}, {"00110100", 63},
	{"11011", 64}, {"10010", 128}, {"010111", 192}, {"0110111", 256},
	{"00110110", 320}, {"00110111", 384}, {"01100100", 448}, {"01100101", 512},
	{"01101000", 576}, {"01100111", 640}, {"011001100", 704}, {"011001101", 768},
	{"011010010", 832}, {"011010011", 896}, {"011010100", 960}, {"011010101", 1024},
	{"011010110", 1088}, {"011010111", 1152}, {"011011000", 1216}, {"011011001", 1280},
	{"011011010", 1344}, {"011011011", 1408}, {"010011000", 1472}, {"010011001", 1536},
	{"010011010", 1600}, {"011000", 1664}, {"010011011", 1728},
}

// blackCodes are the black terminating (0..63) and make-up (64..1728) codes
// (T.4 Tables 2 and 3).
var blackCodes = []ccittCode{
	{"0000110111", 0}, {"010", 1}, {"11", 2}, {"10", 3},
	{"011", 4}, {"0011", 5}, {"0010", 6}, {"00011", 7},
	{"000101", 8}, {"000100", 9}, {"0000100", 10}, {"0000101", 11},
	{"0000111", 12}, {"00000100", 13}, {"00000111", 14}, {"000011000", 15},
	{"0000010111", 16}, {"0000011000", 17}, {"0000001000", 18}, {"00001100111", 19},
	{"00001101000", 20}, {"00001101100", 21}, {"00000110111", 22}, {"00000101000", 23},
	{"00000010111", 24}, {"00000011000", 25}, {"000011001010", 26}, {"000011001011", 27},
	{"000011001100", 28}, {"000011001101", 29}, {"000001101000", 30}, {"000001101001", 31},
	{"000001101010", 32}, {"000001101011", 33}, {"000011010010", 34}, {"000011010011", 35},
	{"000011010100", 36}, {"000011010101", 37}, {"000011010110", 38}, {"000011010111", 39},
	{"000001101100", 40}, {"000001101101", 41}, {"000011011010", 42}, {"000011011011", 43},
	{"000001010100", 44}, {"000001010101", 45}, {"000001010110", 46}, {"000001010111", 47},
	{"000001100100", 48}, {"000001100101", 49}, {"000001010010", 50}, {"000001010011", 51},
	{"000000100100", 52}, {"000000110111", 53}, {"000000111000", 54}, {"000000100111", 55},
	{"000000101000", 56}, {"000001011000", 57}, {"000001011001", 58}, {"000000101011", 59},
	{"000000101100", 60}, {"000001011010", 61}, {"000001100110", 62}, {"000001100111", 63},
	{"0000001111", 64}, {"000011001000", 128}, {"000011001001", 192}, {"000001011011", 256},
	{"000000110011", 320}, {"000000110100", 384}, {"000000110101", 448}, {"0000001101100", 512},
	{"0000001101101", 576}, {"0000001001010", 640}, {"0000001001011", 704}, {"0000001001100", 768},
	{"0000001001101", 832}, {"0000001110010", 896}, {"0000001110011", 960}, {"0000001110100", 1024},
	{"0000001110101", 1088}, {"0000001110110", 1152}, {"0000001110111", 1216}, {"0000001010010", 1280},
	{"0000001010011", 1344}, {"0000001010100", 1408}, {"0000001010101", 1472}, {"0000001011010", 1536},
	{"0000001011011", 1600}, {"0000001100100", 1664}, {"0000001100101", 1728},
}
