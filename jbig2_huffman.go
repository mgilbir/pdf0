package pdf0

// JBIG2 Huffman coding (ISO/IEC 14492 Annex B). As an alternative to arithmetic
// coding, symbol dictionaries and text regions may code their integer parameters
// with Huffman tables — the fifteen standard tables of Annex B, or custom tables
// carried in table segments (type 53). This file holds the Huffman bit reader,
// the table representation and canonical-code assignment, and the standard
// tables; the symbol/text Huffman decoding paths live in jbig2_huffcode.go.

// huffReader reads bits MSB-first from a byte slice (Huffman-coded data is not
// arithmetic-coded, so it uses plain big-endian bit order).
type huffReader struct {
	data []byte
	pos  int // bit offset
}

func (h *huffReader) bit() int {
	if h.pos >= len(h.data)*8 {
		h.pos++
		return 0
	}
	b := int(h.data[h.pos/8]>>(7-uint(h.pos%8))) & 1
	h.pos++
	return b
}

func (h *huffReader) readBits(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		v = (v << 1) | h.bit()
	}
	return v
}

func (h *huffReader) align() {
	if r := h.pos % 8; r != 0 {
		h.pos += 8 - r
	}
}

func (h *huffReader) bytePos() int { return (h.pos + 7) / 8 }

// huffLine is one line of a Huffman table: a prefix length, a range length and
// low bound, and a kind (normal, lower range, upper range, or out-of-band).
type huffLine struct {
	prefLen  int
	rangeLen int
	rangeLow int
	kind     int // 0 normal, 1 lower, 2 upper, 3 OOB
	code     int // assigned canonical code (filled by assign)
}

const (
	huffNormal = 0
	huffLower  = 1
	huffUpper  = 2
	huffOOB    = 3
)

type huffTable struct {
	lines []huffLine
}

// assign gives canonical prefix codes to the lines by length then table order
// (T.88 B.3).
func (t *huffTable) assign() {
	maxLen := 0
	for _, l := range t.lines {
		if l.prefLen > maxLen {
			maxLen = l.prefLen
		}
	}
	count := make([]int, maxLen+2)
	for _, l := range t.lines {
		if l.prefLen > 0 {
			count[l.prefLen]++
		}
	}
	firstCode := make([]int, maxLen+2)
	for length := 1; length <= maxLen; length++ {
		firstCode[length] = (firstCode[length-1] + count[length-1]) << 1
	}
	next := append([]int{}, firstCode...)
	for i := range t.lines {
		if t.lines[i].prefLen > 0 {
			t.lines[i].code = next[t.lines[i].prefLen]
			next[t.lines[i].prefLen]++
		}
	}
}

// decode reads one value; ok is false for the out-of-band code.
func (t *huffTable) decode(h *huffReader) (int, bool) {
	code, length := 0, 0
	for length < 32 {
		code = (code << 1) | h.bit()
		length++
		for _, l := range t.lines {
			if l.prefLen == length && l.code == code {
				switch l.kind {
				case huffOOB:
					return 0, false
				case huffLower:
					return l.rangeLow - h.readBits(32), true
				case huffUpper:
					return l.rangeLow + h.readBits(32), true
				default:
					return l.rangeLow + h.readBits(l.rangeLen), true
				}
			}
		}
	}
	return 0, false
}

func newHuffTable(lines []huffLine) *huffTable {
	t := &huffTable{lines: lines}
	t.assign()
	return t
}

// stdHuffTable returns one of the fifteen standard Huffman tables (T.88 Annex B).
func stdHuffTable(n int) *huffTable {
	L := func(p, rl, low, k int) huffLine { return huffLine{prefLen: p, rangeLen: rl, rangeLow: low, kind: k} }
	var lines []huffLine
	switch n {
	case 1:
		lines = []huffLine{L(1, 4, 0, 0), L(2, 8, 16, 0), L(3, 16, 272, 0), L(3, 32, 65808, huffUpper)}
	case 2:
		lines = []huffLine{L(1, 0, 0, 0), L(2, 0, 1, 0), L(3, 0, 2, 0), L(4, 3, 3, 0), L(5, 6, 11, 0), L(6, 32, 75, huffUpper), L(6, 0, 0, huffOOB)}
	case 3:
		lines = []huffLine{L(8, 8, -256, 0), L(1, 0, 0, 0), L(2, 0, 1, 0), L(3, 0, 2, 0), L(4, 3, 3, 0), L(5, 6, 11, 0), L(8, 32, -257, huffLower), L(7, 32, 75, huffUpper), L(6, 0, 0, huffOOB)}
	case 4:
		lines = []huffLine{L(1, 0, 1, 0), L(2, 0, 2, 0), L(3, 0, 3, 0), L(4, 3, 4, 0), L(5, 6, 12, 0), L(5, 32, 76, huffUpper)}
	case 5:
		lines = []huffLine{L(7, 8, -255, 0), L(1, 0, 1, 0), L(2, 0, 2, 0), L(3, 0, 3, 0), L(4, 3, 4, 0), L(5, 6, 12, 0), L(7, 32, -256, huffLower), L(6, 32, 76, huffUpper)}
	case 6:
		lines = []huffLine{L(5, 10, -2048, 0), L(4, 9, -1024, 0), L(4, 8, -512, 0), L(4, 7, -256, 0), L(5, 6, -128, 0), L(5, 5, -64, 0), L(4, 5, -32, 0), L(2, 7, 0, 0), L(3, 7, 128, 0), L(3, 8, 256, 0), L(4, 9, 512, 0), L(4, 10, 1024, 0), L(6, 32, -2049, huffLower), L(6, 32, 2048, huffUpper)}
	case 7:
		lines = []huffLine{L(4, 9, -1024, 0), L(3, 8, -512, 0), L(4, 7, -256, 0), L(5, 6, -128, 0), L(5, 5, -64, 0), L(4, 5, -32, 0), L(4, 5, 0, 0), L(5, 5, 32, 0), L(5, 6, 64, 0), L(4, 7, 128, 0), L(3, 8, 256, 0), L(3, 9, 512, 0), L(3, 10, 1024, 0), L(5, 32, -1025, huffLower), L(5, 32, 2048, huffUpper)}
	case 8:
		lines = []huffLine{L(8, 3, -15, 0), L(9, 1, -7, 0), L(8, 1, -5, 0), L(9, 0, -3, 0), L(7, 0, -2, 0), L(4, 0, -1, 0), L(2, 1, 0, 0), L(5, 0, 2, 0), L(6, 0, 3, 0), L(3, 4, 4, 0), L(6, 1, 20, 0), L(4, 4, 22, 0), L(4, 5, 38, 0), L(5, 6, 70, 0), L(5, 7, 134, 0), L(6, 7, 262, 0), L(7, 8, 390, 0), L(6, 10, 646, 0), L(9, 32, -16, huffLower), L(9, 32, 1670, huffUpper), L(2, 0, 0, huffOOB)}
	case 9:
		lines = []huffLine{L(8, 4, -31, 0), L(9, 2, -15, 0), L(8, 2, -11, 0), L(9, 1, -7, 0), L(7, 1, -5, 0), L(4, 1, -3, 0), L(3, 1, -1, 0), L(3, 1, 1, 0), L(5, 1, 3, 0), L(6, 1, 5, 0), L(3, 5, 7, 0), L(6, 2, 39, 0), L(4, 5, 43, 0), L(4, 6, 75, 0), L(5, 7, 139, 0), L(5, 8, 267, 0), L(6, 8, 523, 0), L(7, 9, 779, 0), L(6, 11, 1291, 0), L(9, 32, -32, huffLower), L(9, 32, 3339, huffUpper), L(2, 0, 0, huffOOB)}
	case 10:
		lines = []huffLine{L(7, 4, -21, 0), L(8, 0, -5, 0), L(7, 0, -4, 0), L(5, 0, -3, 0), L(2, 2, -2, 0), L(5, 0, 2, 0), L(6, 0, 3, 0), L(7, 0, 4, 0), L(8, 0, 5, 0), L(2, 6, 6, 0), L(5, 5, 70, 0), L(6, 5, 102, 0), L(6, 6, 134, 0), L(6, 7, 198, 0), L(6, 8, 326, 0), L(6, 9, 582, 0), L(6, 10, 1094, 0), L(7, 11, 2118, 0), L(8, 32, -22, huffLower), L(8, 32, 4166, huffUpper), L(2, 0, 0, huffOOB)}
	case 11:
		lines = []huffLine{L(1, 0, 1, 0), L(2, 1, 2, 0), L(4, 0, 4, 0), L(4, 1, 5, 0), L(5, 1, 7, 0), L(5, 2, 9, 0), L(6, 2, 13, 0), L(7, 2, 17, 0), L(7, 3, 21, 0), L(7, 4, 29, 0), L(7, 5, 45, 0), L(7, 6, 77, 0), L(7, 32, 141, huffUpper)}
	case 12:
		lines = []huffLine{L(1, 0, 1, 0), L(2, 0, 2, 0), L(3, 1, 3, 0), L(5, 0, 5, 0), L(5, 1, 6, 0), L(6, 1, 8, 0), L(7, 0, 10, 0), L(7, 1, 11, 0), L(7, 2, 13, 0), L(7, 3, 17, 0), L(7, 4, 25, 0), L(8, 5, 41, 0), L(8, 32, 73, huffUpper)}
	case 13:
		lines = []huffLine{L(1, 0, 1, 0), L(3, 0, 2, 0), L(4, 0, 3, 0), L(5, 0, 4, 0), L(4, 1, 5, 0), L(3, 3, 7, 0), L(6, 1, 15, 0), L(6, 2, 17, 0), L(6, 3, 21, 0), L(6, 4, 29, 0), L(6, 5, 45, 0), L(7, 6, 77, 0), L(7, 32, 141, huffUpper)}
	case 14:
		lines = []huffLine{L(3, 0, -2, 0), L(3, 0, -1, 0), L(1, 0, 0, 0), L(3, 0, 1, 0), L(3, 0, 2, 0)}
	case 15:
		lines = []huffLine{L(7, 4, -24, 0), L(6, 2, -8, 0), L(5, 1, -4, 0), L(4, 0, -2, 0), L(3, 0, -1, 0), L(1, 0, 0, 0), L(3, 0, 1, 0), L(4, 0, 2, 0), L(5, 1, 3, 0), L(6, 2, 5, 0), L(7, 4, 9, 0), L(7, 32, -25, huffLower), L(7, 32, 25, huffUpper)}
	default:
		return nil
	}
	return newHuffTable(lines)
}
