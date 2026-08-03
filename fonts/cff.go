package fonts

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Reading and rewriting the structures of a Compact Font Format program, for
// subsetting it.
//
// These are the producing side. internal/font reads a CFF to answer questions
// about a document — which glyphs exist, how wide they are — and shares nothing
// with this beyond the format itself. That separation is deliberate: a
// subsetter and a validator that agreed by sharing code would agree even when
// both were wrong, and it is the validator that judges what this writes.
//
// # What a CFF is, in the shape this needs
//
// A header, then four INDEX structures in fixed order — Name, Top DICT, String,
// Global Subr — and then a region of data the Top DICT points into: the
// CharStrings INDEX (the outlines), the charset, an optional Encoding, and a
// Private DICT that may itself point at a Local Subr INDEX.
//
// Everything after the four INDEXes is reached by absolute offset from the
// start of the font. So changing the size of the CharStrings INDEX moves
// everything after it and every offset that names it, which is why subsetting a
// CFF is rewriting a font rather than deleting from one.

// CFF limits. A font is untrusted input and every structure here is
// offset-driven; these bound the walk so a crafted font cannot turn a few bytes
// of declaration into unbounded work.
const (
	maxCFFIndexItems = 1 << 17
	maxCFFSize       = 1 << 26
)

// cffIndex is a parsed INDEX: a count, an offset array, and the items.
type cffIndex struct {
	items [][]byte
	end   int // offset just past this INDEX in the source
}

// readCFFIndex parses the INDEX at off.
func readCFFIndex(b []byte, off int) (*cffIndex, error) {
	if off < 0 || off+2 > len(b) {
		return nil, fmt.Errorf("fonts: CFF INDEX at %d is outside the font", off)
	}
	count := int(binary.BigEndian.Uint16(b[off:]))
	if count == 0 {
		return &cffIndex{end: off + 2}, nil
	}
	if count > maxCFFIndexItems {
		return nil, fmt.Errorf("fonts: CFF INDEX declares %d items", count)
	}
	if off+3 > len(b) {
		return nil, errors.New("fonts: truncated CFF INDEX header")
	}
	offSize := int(b[off+2])
	if offSize < 1 || offSize > 4 {
		return nil, fmt.Errorf("fonts: CFF INDEX offset size %d is not 1..4", offSize)
	}
	offArray := off + 3
	dataStart := offArray + (count+1)*offSize - 1
	if dataStart < 0 || offArray+(count+1)*offSize > len(b) {
		return nil, errors.New("fonts: truncated CFF INDEX offset array")
	}
	read := func(i int) int {
		p := offArray + i*offSize
		v := 0
		for k := 0; k < offSize; k++ {
			v = v<<8 | int(b[p+k])
		}
		return v
	}
	idx := &cffIndex{items: make([][]byte, 0, count)}
	for i := 0; i < count; i++ {
		start, end := read(i), read(i+1)
		if start < 1 || end < start || dataStart+end > len(b) {
			return nil, fmt.Errorf("fonts: CFF INDEX item %d lies outside the font", i)
		}
		idx.items = append(idx.items, b[dataStart+start:dataStart+end])
	}
	idx.end = dataStart + read(count)
	if idx.end > len(b) {
		return nil, errors.New("fonts: CFF INDEX ends outside the font")
	}
	return idx, nil
}

// writeCFFIndex serialises items as an INDEX, choosing the smallest offset
// width that spans the data, as the format expects.
func writeCFFIndex(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte{0, 0}
	}
	total := 1
	for _, it := range items {
		total += len(it)
	}
	offSize := 1
	switch {
	case total > 1<<24:
		offSize = 4
	case total > 1<<16:
		offSize = 3
	case total > 1<<8:
		offSize = 2
	}
	out := make([]byte, 0, 3+(len(items)+1)*offSize+total)
	out = binary.BigEndian.AppendUint16(out, uint16(len(items)))
	out = append(out, byte(offSize))
	put := func(v int) {
		for k := offSize - 1; k >= 0; k-- {
			out = append(out, byte(v>>(8*k)))
		}
	}
	pos := 1
	put(pos)
	for _, it := range items {
		pos += len(it)
		put(pos)
	}
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

// cffOp is a DICT entry: its operands and its operator. The operator is the
// byte value, or 1200+b for the two-byte escaped form.
type cffOp struct {
	operands []int
	op       int
}

// The Top DICT operators this code has to understand, because each names an
// offset that moves when the font is rewritten.
const (
	opCharset     = 15
	opEncoding    = 16
	opCharStrings = 17
	opPrivate     = 18
	opSubrs       = 19 // in the Private DICT, relative to it
	opCharstringT = 1206
	opROS         = 1230 // present only in a CID-keyed font
)

// parseCFFDict reads a DICT into its entries, in order.
//
// Real operands are dropped to integers. That is lossless for every operator
// this rewrites — they are all offsets — and the entries it does not rewrite
// are re-emitted from their original bytes, so a real value never round-trips
// through an int.
func parseCFFDict(b []byte) ([]cffOp, [][]byte, error) {
	var (
		ops      []cffOp
		raw      [][]byte
		operands []int
		start    int
	)
	for i := 0; i < len(b); {
		v := int(b[i])
		switch {
		case v <= 21: // operator
			op := v
			n := 1
			if v == 12 {
				if i+1 >= len(b) {
					return nil, nil, errors.New("fonts: truncated CFF DICT operator")
				}
				op = 1200 + int(b[i+1])
				n = 2
			}
			ops = append(ops, cffOp{operands: operands, op: op})
			raw = append(raw, b[start:i+n])
			operands = nil
			i += n
			start = i
		case v == 28:
			if i+3 > len(b) {
				return nil, nil, errors.New("fonts: truncated CFF DICT operand")
			}
			operands = append(operands, int(int16(binary.BigEndian.Uint16(b[i+1:]))))
			i += 3
		case v == 29:
			if i+5 > len(b) {
				return nil, nil, errors.New("fonts: truncated CFF DICT operand")
			}
			operands = append(operands, int(int32(binary.BigEndian.Uint32(b[i+1:]))))
			i += 5
		case v == 30: // real, binary-coded decimal
			j := i + 1
			for j < len(b) {
				hi, lo := b[j]>>4, b[j]&0xF
				j++
				if hi == 0xF || lo == 0xF {
					break
				}
			}
			operands = append(operands, 0) // not an offset; never rewritten
			i = j
		case v >= 32 && v <= 246:
			operands = append(operands, v-139)
			i++
		case v >= 247 && v <= 250:
			if i+2 > len(b) {
				return nil, nil, errors.New("fonts: truncated CFF DICT operand")
			}
			operands = append(operands, (v-247)*256+int(b[i+1])+108)
			i += 2
		case v >= 251 && v <= 254:
			if i+2 > len(b) {
				return nil, nil, errors.New("fonts: truncated CFF DICT operand")
			}
			operands = append(operands, -(v-251)*256-int(b[i+1])-108)
			i += 2
		default:
			return nil, nil, fmt.Errorf("fonts: reserved byte %d in a CFF DICT", v)
		}
	}
	return ops, raw, nil
}

// cffInt encodes an integer in the fixed five-byte form.
//
// Always five bytes, even for a value a single byte could hold. That is the
// point: a DICT whose operand widths depend on the values it holds changes size
// when an offset changes, which moves the very offsets being written. Fixing
// the width makes the rewritten DICT's size independent of its contents, so one
// pass suffices.
func cffInt(v int) []byte {
	out := []byte{29, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(out[1:], uint32(int32(v)))
	return out
}

// PrivateDictForTest returns a CFF program's Private DICT bytes, as its Top
// DICT locates them.
//
// It exists so a test can compare the region across a subsetting round trip.
// The size and offset are copied through rather than recomputed, so nothing in
// the writer can drift them — but "nothing can drift it" is an argument, and a
// comparison is a check.
func PrivateDictForTest(cff []byte) ([]byte, error) {
	top, err := topDictOf(cff)
	if err != nil {
		return nil, err
	}
	for _, e := range top {
		if e.op == opPrivate && len(e.operands) == 2 {
			size, off := e.operands[0], e.operands[1]
			if size < 0 || off < 0 || off+size > len(cff) {
				return nil, errors.New("fonts: the CFF Private DICT lies outside the font")
			}
			return cff[off : off+size], nil
		}
	}
	return nil, nil // a font may legitimately have none
}

// topDictOf parses a CFF program's single Top DICT.
func topDictOf(cff []byte) ([]cffOp, error) {
	if len(cff) < 4 {
		return nil, errors.New("fonts: CFF program is too short")
	}
	nameIndex, err := readCFFIndex(cff, int(cff[2]))
	if err != nil {
		return nil, err
	}
	topIndex, err := readCFFIndex(cff, nameIndex.end)
	if err != nil {
		return nil, err
	}
	if len(topIndex.items) != 1 {
		return nil, errors.New("fonts: not a single-font CFF program")
	}
	top, _, err := parseCFFDict(topIndex.items[0])
	return top, err
}

// CharStringsForTest returns the charstrings of a CFF program.
//
// It exists so that a test in another package can look at what the subsetter
// wrote — that a dropped glyph became an endchar and a kept one did not — which
// is the property the saving depends on and which nothing else observes. There
// is no other caller, and the name says so.
func CharStringsForTest(cff []byte) ([][]byte, error) {
	top, err := topDictOf(cff)
	if err != nil {
		return nil, err
	}
	for _, e := range top {
		if e.op == opCharStrings && len(e.operands) == 1 {
			idx, err := readCFFIndex(cff, e.operands[0])
			if err != nil {
				return nil, err
			}
			return idx.items, nil
		}
	}
	return nil, errors.New("fonts: the CFF Top DICT names no CharStrings")
}
