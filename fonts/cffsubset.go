package fonts

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/internal/font"
)

// Subsetting a CFF program, by the same rule the glyf subsetter follows: glyph
// indices are retained, and a dropped glyph becomes an empty one.
//
// For CFF "empty" means a charstring of a single endchar operator — a glyph
// that draws nothing. It is not zero-length, because a zero-length charstring
// is not a charstring; a renderer asked for one is entitled to reject the font.
// One byte per dropped glyph is what this costs, against the tens or hundreds
// the outline would have.
//
// Retaining indices matters even more here than it does for glyf. A CFF's
// charset maps glyph index to name, its encoding maps code to glyph index, and
// its charstrings call subroutines by index into tables shared by every glyph.
// Renumbering would mean rewriting all of that; keeping the numbering means
// charset, encoding, and both subroutine INDEXes are copied through untouched,
// and the only structure that changes is the one whose size changed.

// subsetCFF rewrites a CFF program to carry outlines only for the kept glyphs.
//
// The kept slice is indexed by glyph, as the glyf subsetter's is, so both take
// the same decision from the same place.
func subsetCFF(data []byte, keep []bool) ([]byte, error) {
	if len(data) > maxCFFSize {
		return nil, fmt.Errorf("fonts: CFF program of %d bytes is too large to subset", len(data))
	}
	if len(data) < 4 {
		return nil, errors.New("fonts: CFF program is too short to hold a header")
	}
	hdrSize := int(data[2])
	if hdrSize < 4 || hdrSize > len(data) {
		return nil, fmt.Errorf("fonts: CFF header size %d is not usable", hdrSize)
	}

	nameIndex, err := readCFFIndex(data, hdrSize)
	if err != nil {
		return nil, err
	}
	topIndex, err := readCFFIndex(data, nameIndex.end)
	if err != nil {
		return nil, err
	}
	if len(topIndex.items) != 1 {
		return nil, fmt.Errorf("fonts: CFF holds %d Top DICTs; only a single-font program is supported", len(topIndex.items))
	}
	stringIndex, err := readCFFIndex(data, topIndex.end)
	if err != nil {
		return nil, err
	}
	gsubrIndex, err := readCFFIndex(data, stringIndex.end)
	if err != nil {
		return nil, err
	}

	top, topRaw, err := parseCFFDict(topIndex.items[0])
	if err != nil {
		return nil, err
	}

	var charStringsOff, charsetOff, encodingOff, privSize, privOff int
	for _, e := range top {
		switch e.op {
		case opROS:
			return nil, errors.New("fonts: CID-keyed CFF programs are not subsetted")
		case opCharStrings:
			if len(e.operands) == 1 {
				charStringsOff = e.operands[0]
			}
		case opCharset:
			if len(e.operands) == 1 {
				charsetOff = e.operands[0]
			}
		case opEncoding:
			if len(e.operands) == 1 {
				encodingOff = e.operands[0]
			}
		case opPrivate:
			if len(e.operands) == 2 {
				privSize, privOff = e.operands[0], e.operands[1]
			}
		}
	}
	if charStringsOff <= 0 {
		return nil, errors.New("fonts: CFF Top DICT names no CharStrings")
	}
	charStrings, err := readCFFIndex(data, charStringsOff)
	if err != nil {
		return nil, err
	}
	n := len(charStrings.items)
	if n != len(keep) {
		return nil, fmt.Errorf("fonts: the CFF holds %d glyphs but %d were decided on", n, len(keep))
	}

	// The new CharStrings: kept glyphs verbatim, dropped ones a bare endchar.
	newCharStrings := make([][]byte, n)
	for i := 0; i < n; i++ {
		if keep[i] {
			newCharStrings[i] = charStrings.items[i]
			continue
		}
		newCharStrings[i] = []byte{14} // endchar
	}
	csBlob := writeCFFIndex(newCharStrings)

	// The regions the Top DICT points at, copied through unchanged. Their
	// contents do not depend on which glyphs survive, because the numbering did
	// not change; only where they sit does.
	charsetBlob, err := sliceCharset(data, charsetOff, n)
	if err != nil {
		return nil, err
	}
	encodingBlob, err := sliceEncoding(data, encodingOff)
	if err != nil {
		return nil, err
	}
	var privBlob []byte
	if privSize > 0 {
		if privOff < 0 || privOff+privSize > len(data) {
			return nil, errors.New("fonts: the CFF Private DICT lies outside the font")
		}
		privBlob = data[privOff : privOff+privSize]
	}
	// Local subroutines live after the Private DICT and are named from inside
	// it, relative to its start — so as long as the Private DICT moves as a
	// unit with them, that offset stays correct and needs no rewriting.
	var localSubrs []byte
	if privBlob != nil {
		privOps, _, err := parseCFFDict(privBlob)
		if err != nil {
			return nil, err
		}
		for _, e := range privOps {
			if e.op == opSubrs && len(e.operands) == 1 && e.operands[0] > 0 {
				idx, err := readCFFIndex(data, privOff+e.operands[0])
				if err != nil {
					return nil, err
				}
				localSubrs = data[privOff+e.operands[0] : idx.end]
			}
		}
	}

	// Lay the font out, then write the Top DICT with the offsets that layout
	// produced. Every rewritten operand is five bytes, so the DICT's size does
	// not depend on the values going into it and one pass is enough.
	rewrite := func(charset, encoding, charstrings, private int) []byte {
		var out []byte
		for i, e := range top {
			switch e.op {
			case opCharset:
				out = append(out, cffInt(charset)...)
				out = append(out, byte(opCharset))
			case opEncoding:
				out = append(out, cffInt(encoding)...)
				out = append(out, byte(opEncoding))
			case opCharStrings:
				out = append(out, cffInt(charstrings)...)
				out = append(out, byte(opCharStrings))
			case opPrivate:
				out = append(out, cffInt(privSize)...)
				out = append(out, cffInt(private)...)
				out = append(out, byte(opPrivate))
			default:
				out = append(out, topRaw[i]...) // verbatim, operands and all
			}
		}
		return out
	}

	// The Top DICT's size is stable, so compute it once against placeholders.
	topBlob := rewrite(0, 0, 0, 0)
	prefix := hdrSize
	prefix += len(writeCFFIndex(nameIndex.items))
	prefix += len(writeCFFIndex([][]byte{topBlob}))
	prefix += len(writeCFFIndex(stringIndex.items))
	prefix += len(writeCFFIndex(gsubrIndex.items))

	charsetAt := prefix
	encodingAt := charsetAt + len(charsetBlob)
	charStringsAt := encodingAt + len(encodingBlob)
	privateAt := charStringsAt + len(csBlob)

	// A predefined charset or encoding is named by a small number rather than
	// an offset, and must keep that number.
	if charsetOff <= 2 {
		charsetAt = charsetOff
	}
	if encodingOff <= 1 {
		encodingAt = encodingOff
	}
	topBlob = rewrite(charsetAt, encodingAt, charStringsAt, privateAt)

	out := make([]byte, 0, len(data))
	out = append(out, data[:hdrSize]...)
	out = append(out, writeCFFIndex(nameIndex.items)...)
	out = append(out, writeCFFIndex([][]byte{topBlob})...)
	out = append(out, writeCFFIndex(stringIndex.items)...)
	out = append(out, writeCFFIndex(gsubrIndex.items)...)
	out = append(out, charsetBlob...)
	out = append(out, encodingBlob...)
	out = append(out, csBlob...)
	out = append(out, privBlob...)
	out = append(out, localSubrs...)
	if len(out) != privateAt+len(privBlob)+len(localSubrs) {
		return nil, errors.New("fonts: internal: the CFF layout did not match what the Top DICT was told")
	}
	return out, nil
}

// sliceCharset returns the charset's bytes. Its length is not stored anywhere:
// it follows from the format and the glyph count, which is why this has to
// walk it rather than copy a range.
func sliceCharset(data []byte, off, nGlyphs int) ([]byte, error) {
	if off <= 2 {
		return nil, nil // predefined: ISOAdobe, Expert or ExpertSubset
	}
	if off >= len(data) {
		return nil, errors.New("fonts: the CFF charset lies outside the font")
	}
	c := data[off:]
	switch c[0] {
	case 0:
		size := 1 + 2*(nGlyphs-1)
		if size > len(c) {
			return nil, errors.New("fonts: truncated CFF charset")
		}
		return c[:size], nil
	case 1, 2:
		step := 3
		if c[0] == 2 {
			step = 4
		}
		covered, size := 1, 1 // glyph 0 is .notdef and is not listed
		for covered < nGlyphs {
			if size+step > len(c) {
				return nil, errors.New("fonts: truncated CFF charset")
			}
			nLeft := int(c[size+2])
			if step == 4 {
				nLeft = int(binary.BigEndian.Uint16(c[size+2:]))
			}
			covered += nLeft + 1
			size += step
		}
		return c[:size], nil
	}
	return nil, fmt.Errorf("fonts: CFF charset format %d is not one the format defines", c[0])
}

// sliceEncoding returns the encoding's bytes, whose length likewise follows
// from its format.
func sliceEncoding(data []byte, off int) ([]byte, error) {
	if off <= 1 {
		return nil, nil // predefined: Standard or Expert
	}
	if off >= len(data) {
		return nil, errors.New("fonts: the CFF encoding lies outside the font")
	}
	e := data[off:]
	format := e[0]
	var size int
	switch format &^ 0x80 {
	case 0:
		if len(e) < 2 {
			return nil, errors.New("fonts: truncated CFF encoding")
		}
		size = 2 + int(e[1])
	case 1:
		if len(e) < 2 {
			return nil, errors.New("fonts: truncated CFF encoding")
		}
		size = 2 + 2*int(e[1])
	default:
		return nil, fmt.Errorf("fonts: CFF encoding format %d is not one the format defines", format)
	}
	if format&0x80 != 0 { // supplements follow
		if size >= len(e) {
			return nil, errors.New("fonts: truncated CFF encoding supplements")
		}
		size += 1 + 3*int(e[size])
	}
	if size > len(e) {
		return nil, errors.New("fonts: truncated CFF encoding")
	}
	return e[:size], nil
}

// subsetOpenTypeCFF rebuilds an OpenType font around a subsetted CFF table.
func (f *Face) subsetOpenTypeCFF() ([]byte, []int, error) {
	tables := font.SFNTTables(f.data)
	if tables == nil || tables["CFF "] == nil {
		return nil, nil, errors.New("fonts: the font no longer carries a CFF table")
	}
	n := f.prog.NumGlyphs
	keep := make([]bool, n)
	keep[0] = true // .notdef
	for gid := range f.used {
		if gid >= 0 && gid < n {
			keep[gid] = true
		}
	}
	// CFF has no composite glyphs in the glyf sense — a charstring that reuses
	// another shape does it through seac or a subroutine, and both are carried
	// along by keeping the subroutine INDEXes whole — so there is no closure to
	// take here.

	sub, err := subsetCFF(tables["CFF "], keep)
	if err != nil {
		return nil, nil, err
	}
	out := map[string][]byte{}
	for _, tag := range []string{"cmap", "head", "hhea", "hmtx", "maxp", "name", "post", "OS/2"} {
		if b, ok := tables[tag]; ok {
			out[tag] = b
		}
	}
	out["CFF "] = sub

	kept := make([]int, 0, len(keep))
	for gid, k := range keep {
		if k {
			kept = append(kept, gid)
		}
	}
	return assembleOTTO(out), kept, nil
}

// assembleOTTO writes an OpenType font whose outlines are CFF.
func assembleOTTO(tables map[string][]byte) []byte {
	out := assembleSFNT(tables)
	binary.BigEndian.PutUint32(out[0:], 0x4F54544F) // 'OTTO'
	return out
}
