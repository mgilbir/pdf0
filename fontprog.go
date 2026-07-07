package pdf0

import (
	"encoding/binary"
	"strings"
)

// Minimal font-program parsers backing the PDF/A font rules: enough of
// TrueType/OpenType (sfnt tables), CFF, and Type 1 to answer three
// questions — which glyphs exist, what are their advance widths (in 1/1000
// text-space units), and which glyph does a character map to.

// fontProgram is the common view the rules consume.
type fontProgram struct {
	// glyphNames lists the glyph names defined by the program (Type1/CFF
	// non-CID); nil when the format identifies glyphs by index only.
	glyphNames map[string]bool
	// widthByName gives advance widths (1/1000 units) for named glyphs.
	widthByName map[string]float64
	// numGlyphs is the glyph count (sfnt/CFF).
	numGlyphs int
	// widthByGID gives advance widths by glyph index, scaled to 1/1000.
	widthByGID []float64
	// cmap maps Unicode code points to glyph indices ((3,1) subtable), and
	// mac maps single-byte codes via the (1,0) subtable; symbolCmap maps
	// 0xF000-prefixed codes via a (3,0) subtable.
	cmap       map[rune]int
	macCmap    map[byte]int
	symbolCmap map[uint16]int
	// cidGIDs reports which CIDs have charstrings (CFF CID-keyed fonts);
	// nil when not CID-keyed.
	cidGIDs map[int]bool
	// widthByCID gives advance widths by CID for CID-keyed CFF.
	widthByCID map[int]float64
}

// --- sfnt (TrueType / OpenType) ---

func be16(b []byte, off int) int {
	if off+2 > len(b) {
		return 0
	}
	return int(binary.BigEndian.Uint16(b[off:]))
}

func be32(b []byte, off int) uint32 {
	if off+4 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint32(b[off:])
}

// parseSFNT parses a TrueType/OpenType font program.
func parseSFNT(data []byte) *fontProgram {
	if len(data) < 12 {
		return nil
	}
	tag := be32(data, 0)
	if tag != 0x00010000 && tag != 0x74727565 && tag != 0x4F54544F { // 1.0, 'true', 'OTTO'
		return nil
	}
	numTables := be16(data, 4)
	tables := make(map[string][]byte)
	for i := 0; i < numTables; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			return nil
		}
		name := string(data[rec : rec+4])
		off := be32(data, rec+8)
		length := be32(data, rec+12)
		if uint64(off)+uint64(length) > uint64(len(data)) {
			continue
		}
		tables[name] = data[off : off+length]
	}

	fp := &fontProgram{}
	head := tables["head"]
	unitsPerEm := 1000
	if len(head) >= 20 {
		if u := be16(head, 18); u > 0 {
			unitsPerEm = u
		}
	}
	if maxp := tables["maxp"]; len(maxp) >= 6 {
		fp.numGlyphs = be16(maxp, 4)
	}

	// hmtx: advance widths, scaled to 1/1000 units.
	if hhea, hmtx := tables["hhea"], tables["hmtx"]; len(hhea) >= 36 && hmtx != nil {
		numH := be16(hhea, 34)
		fp.widthByGID = make([]float64, fp.numGlyphs)
		last := 0.0
		for gid := 0; gid < fp.numGlyphs; gid++ {
			if gid < numH && 4*gid+2 <= len(hmtx) {
				last = float64(be16(hmtx, 4*gid)) * 1000 / float64(unitsPerEm)
			}
			fp.widthByGID[gid] = last
		}
	}

	// cmap subtables.
	if cmap := tables["cmap"]; len(cmap) >= 4 {
		n := be16(cmap, 2)
		for i := 0; i < n; i++ {
			rec := 4 + 8*i
			if rec+8 > len(cmap) {
				break
			}
			plat := be16(cmap, rec)
			enc := be16(cmap, rec+2)
			off := be32(cmap, rec+4)
			if uint64(off) >= uint64(len(cmap)) {
				continue
			}
			sub := cmap[off:]
			switch {
			case plat == 3 && enc == 1:
				fp.cmap = parseCmapSubtable(sub)
			case plat == 3 && enc == 0:
				m := parseCmapSubtable(sub)
				fp.symbolCmap = make(map[uint16]int, len(m))
				for r, gid := range m {
					fp.symbolCmap[uint16(r)] = gid
				}
			case plat == 1 && enc == 0:
				m := parseCmapSubtable(sub)
				fp.macCmap = make(map[byte]int, len(m))
				for r, gid := range m {
					if r <= 0xFF {
						fp.macCmap[byte(r)] = gid
					}
				}
			}
		}
	}
	return fp
}

// parseCmapSubtable handles cmap formats 0, 4, and 6.
func parseCmapSubtable(b []byte) map[rune]int {
	out := make(map[rune]int)
	switch be16(b, 0) {
	case 0:
		if len(b) < 262 {
			return out
		}
		for c := 0; c < 256; c++ {
			if gid := int(b[6+c]); gid != 0 {
				out[rune(c)] = gid
			}
		}
	case 4:
		segX2 := be16(b, 6)
		if segX2 == 0 || len(b) < 16+4*segX2 {
			return out
		}
		endBase := 14
		startBase := endBase + segX2 + 2
		deltaBase := startBase + segX2
		rangeBase := deltaBase + segX2
		for s := 0; s < segX2; s += 2 {
			end := be16(b, endBase+s)
			start := be16(b, startBase+s)
			delta := be16(b, deltaBase+s)
			rangeOff := be16(b, rangeBase+s)
			if start == 0xFFFF {
				continue
			}
			for c := start; c <= end && c != 0; c++ {
				var gid int
				if rangeOff == 0 {
					gid = (c + delta) & 0xFFFF
				} else {
					idx := rangeBase + s + rangeOff + 2*(c-start)
					g := be16(b, idx)
					if g == 0 {
						continue
					}
					gid = (g + delta) & 0xFFFF
				}
				if gid != 0 {
					out[rune(c)] = gid
				}
				if c == 0xFFFF {
					break
				}
			}
		}
	case 6:
		first := be16(b, 6)
		count := be16(b, 8)
		for i := 0; i < count; i++ {
			if gid := be16(b, 10+2*i); gid != 0 {
				out[rune(first+i)] = gid
			}
		}
	}
	return out
}

// --- CFF ---

type cffIndex struct {
	items [][]byte
}

func parseCFFIndex(b []byte, off int) (cffIndex, int) {
	var idx cffIndex
	if off+2 > len(b) {
		return idx, len(b)
	}
	count := be16(b, off)
	if count == 0 {
		return idx, off + 2
	}
	if off+3 > len(b) {
		return idx, len(b)
	}
	offSize := int(b[off+2])
	if offSize < 1 || offSize > 4 {
		return idx, len(b)
	}
	offArray := off + 3
	readOff := func(i int) int {
		p := offArray + i*offSize
		if p+offSize > len(b) {
			return -1
		}
		v := 0
		for k := 0; k < offSize; k++ {
			v = v<<8 | int(b[p+k])
		}
		return v
	}
	dataStart := offArray + (count+1)*offSize - 1
	for i := 0; i < count; i++ {
		s, e := readOff(i), readOff(i+1)
		if s < 1 || e < s || dataStart+e > len(b) {
			return idx, len(b)
		}
		idx.items = append(idx.items, b[dataStart+s:dataStart+e])
	}
	end := dataStart + readOff(count)
	if end > len(b) || end < 0 {
		end = len(b)
	}
	return idx, end
}

// parseCFFDict extracts operator → operands from a CFF DICT.
func parseCFFDict(b []byte) map[int][]float64 {
	out := make(map[int][]float64)
	var operands []float64
	i := 0
	for i < len(b) {
		v := int(b[i])
		switch {
		case v <= 21: // operator
			op := v
			i++
			if v == 12 && i < len(b) {
				op = 1200 + int(b[i])
				i++
			}
			out[op] = append([]float64(nil), operands...)
			operands = operands[:0]
		case v == 28:
			if i+3 > len(b) {
				return out
			}
			operands = append(operands, float64(int16(binary.BigEndian.Uint16(b[i+1:]))))
			i += 3
		case v == 29:
			if i+5 > len(b) {
				return out
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(b[i+1:]))))
			i += 5
		case v == 30: // real number (BCD)
			i++
			var sb strings.Builder
			for i < len(b) {
				hi, lo := b[i]>>4, b[i]&0xF
				i++
				done := false
				for _, nib := range []byte{hi, lo} {
					switch {
					case nib <= 9:
						sb.WriteByte('0' + nib)
					case nib == 0xA:
						sb.WriteByte('.')
					case nib == 0xB:
						sb.WriteByte('E')
					case nib == 0xC:
						sb.WriteString("E-")
					case nib == 0xE:
						sb.WriteByte('-')
					case nib == 0xF:
						done = true
					}
					if done {
						break
					}
				}
				if done {
					break
				}
			}
			var f float64
			fmt_Sscan(sb.String(), &f)
			operands = append(operands, f)
		case v >= 32 && v <= 246:
			operands = append(operands, float64(v-139))
			i++
		case v >= 247 && v <= 250:
			if i+2 > len(b) {
				return out
			}
			operands = append(operands, float64((v-247)*256+int(b[i+1])+108))
			i += 2
		case v >= 251 && v <= 254:
			if i+2 > len(b) {
				return out
			}
			operands = append(operands, float64(-(v-251)*256-int(b[i+1])-108))
			i += 2
		default:
			i++
		}
	}
	return out
}

// parseCFF parses a bare CFF font (FontFile3 /Type1C or /CIDFontType0C, or
// the CFF table of an OpenType font).
func parseCFF(data []byte) *fontProgram {
	if len(data) < 4 || data[0] != 1 {
		return nil
	}
	hdrSize := int(data[2])
	_, afterNames := parseCFFIndex(data, hdrSize)
	topDicts, afterTop := parseCFFIndex(data, afterNames)
	stringsIdx, _ := parseCFFIndex(data, afterTop)
	if len(topDicts.items) == 0 {
		return nil
	}
	top := parseCFFDict(topDicts.items[0])

	fp := &fontProgram{}
	// FontMatrix (top DICT op 12 7) x-scale, default 0.001; normalise
	// charstring widths to 1/1000 text-space units.
	scale := 1.0
	if fm, ok := top[1207]; ok && len(fm) >= 1 && fm[0] != 0 {
		scale = fm[0] * 1000
	}
	csOff := dictInt(top, 17)
	if csOff <= 0 || csOff >= len(data) {
		return nil
	}
	charStrings, _ := parseCFFIndex(data, csOff)
	fp.numGlyphs = len(charStrings.items)

	_, isCID := top[1230] // ROS
	// Private DICT: nominal/default widths.
	defaultWidthX, nominalWidthX := 0.0, 0.0
	var localSubrs cffIndex
	if priv, ok := top[18]; ok && len(priv) == 2 {
		pOff, pSize := int(priv[1]), int(priv[0])
		if pOff > 0 && pOff+pSize <= len(data) {
			pd := parseCFFDict(data[pOff : pOff+pSize])
			if v, ok := pd[20]; ok && len(v) == 1 {
				defaultWidthX = v[0]
			}
			if v, ok := pd[21]; ok && len(v) == 1 {
				nominalWidthX = v[0]
			}
			if v, ok := pd[19]; ok && len(v) == 1 { // Subrs
				if so := pOff + int(v[0]); so > 0 && so < len(data) {
					localSubrs, _ = parseCFFIndex(data, so)
				}
			}
		}
	}
	_ = localSubrs

	// charset: GID → SID (names) or CID.
	charsetOff := dictInt(top, 15)
	gidToSID := make([]int, fp.numGlyphs)
	if fp.numGlyphs > 0 {
		gidToSID[0] = 0 // .notdef
	}
	switch charsetOff {
	case 0: // ISOAdobe: identity SIDs
		for g := 1; g < fp.numGlyphs; g++ {
			gidToSID[g] = g
		}
	case 1, 2:
		// Expert charsets — rare; leave identity.
		for g := 1; g < fp.numGlyphs; g++ {
			gidToSID[g] = g
		}
	default:
		if charsetOff > 0 && charsetOff < len(data) {
			b := data[charsetOff:]
			switch b[0] {
			case 0:
				for g := 1; g < fp.numGlyphs; g++ {
					if 1+2*g > len(b) {
						break
					}
					gidToSID[g] = be16(b, 1+2*(g-1))
				}
			case 1, 2:
				g := 1
				p := 1
				step := 3
				if b[0] == 2 {
					step = 4
				}
				for g < fp.numGlyphs && p+step <= len(b) {
					first := be16(b, p)
					var count int
					if b[0] == 1 {
						count = int(b[p+2])
					} else {
						count = be16(b, p+2)
					}
					for k := 0; k <= count && g < fp.numGlyphs; k++ {
						gidToSID[g] = first + k
						g++
					}
					p += step
				}
			}
		}
	}

	// Charstring widths (Type 2: optional leading width operand).
	widthOf := func(cs []byte) float64 {
		w, has := type2CharstringWidth(cs)
		if !has {
			return defaultWidthX * scale
		}
		return (nominalWidthX + w) * scale
	}

	if isCID {
		fp.cidGIDs = make(map[int]bool, fp.numGlyphs)
		fp.widthByCID = make(map[int]float64, fp.numGlyphs)
		for g := 0; g < fp.numGlyphs; g++ {
			cid := gidToSID[g]
			fp.cidGIDs[cid] = true
			fp.widthByCID[cid] = widthOf(charStrings.items[g])
		}
	} else {
		fp.glyphNames = make(map[string]bool, fp.numGlyphs)
		fp.widthByName = make(map[string]float64, fp.numGlyphs)
		for g := 0; g < fp.numGlyphs; g++ {
			name := cffSIDName(gidToSID[g], stringsIdx)
			fp.glyphNames[name] = true
			fp.widthByName[name] = widthOf(charStrings.items[g])
		}
	}
	fp.widthByGID = make([]float64, fp.numGlyphs)
	for g := 0; g < fp.numGlyphs; g++ {
		fp.widthByGID[g] = widthOf(charStrings.items[g])
	}
	return fp
}

func dictInt(d map[int][]float64, op int) int {
	if v, ok := d[op]; ok && len(v) >= 1 {
		return int(v[len(v)-1])
	}
	return 0
}

// type2CharstringWidth reports the optional leading width delta of a Type 2
// charstring: present when the operand count before the first stack-clearing
// operator exceeds that operator's expected arguments.
func type2CharstringWidth(cs []byte) (float64, bool) {
	var operands []float64
	i := 0
	for i < len(cs) {
		v := int(cs[i])
		switch {
		case v == 28:
			if i+3 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64(int16(binary.BigEndian.Uint16(cs[i+1:]))))
			i += 3
		case v == 255:
			if i+5 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(cs[i+1:])))/65536)
			i += 5
		case v >= 32 && v <= 246:
			operands = append(operands, float64(v-139))
			i++
		case v >= 247 && v <= 250:
			if i+2 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64((v-247)*256+int(cs[i+1])+108))
			i += 2
		case v >= 251 && v <= 254:
			if i+2 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64(-(v-251)*256-int(cs[i+1])-108))
			i += 2
		default:
			// First operator reached.
			expected := -1
			switch v {
			case 1, 3, 18, 23: // hstem vstem hstemhm vstemhm
				expected = len(operands) &^ 1 // even
			case 19, 20: // hintmask cntrmask
				expected = len(operands) &^ 1
			case 21: // rmoveto
				expected = 2
			case 22, 4: // hmoveto vmoveto
				expected = 1
			case 14: // endchar
				expected = 0
			default:
				return 0, false // hstem etc. not first: no width info
			}
			if len(operands) > expected {
				return operands[0], true
			}
			return 0, false
		}
	}
	return 0, false
}

// cffStandardStrings is the tail-safe accessor for the 391 standard strings.
func cffSIDName(sid int, idx cffIndex) string {
	if sid < len(cffStandardStrings) {
		return cffStandardStrings[sid]
	}
	i := sid - len(cffStandardStrings)
	if i < len(idx.items) {
		return string(idx.items[i])
	}
	return ""
}

// fmt_Sscan is a tiny indirection so parseCFFDict avoids importing fmt just
// for BCD reals.
func fmt_Sscan(s string, f *float64) {
	var v float64
	var neg bool
	i := 0
	if i < len(s) && s[i] == '-' {
		neg = true
		i++
	}
	frac := 0.0
	scale := 0.1
	inFrac := false
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if inFrac {
				frac += float64(c-'0') * scale
				scale /= 10
			} else {
				v = v*10 + float64(c-'0')
			}
		case c == '.':
			inFrac = true
		case c == 'E':
			// exponent: parse remainder as int
			exp := 0
			eneg := false
			j := i + 1
			if j < len(s) && s[j] == '-' {
				eneg = true
				j++
			}
			for ; j < len(s); j++ {
				if s[j] >= '0' && s[j] <= '9' {
					exp = exp*10 + int(s[j]-'0')
				}
			}
			total := v + frac
			for k := 0; k < exp; k++ {
				if eneg {
					total /= 10
				} else {
					total *= 10
				}
			}
			if neg {
				total = -total
			}
			*f = total
			return
		}
	}
	total := v + frac
	if neg {
		total = -total
	}
	*f = total
}

// --- Type 1 ---

// parseType1 parses a Type 1 font program (FontFile): the eexec-encrypted
// private portion holds the CharStrings dictionary with glyph names and
// hsbw/sbw widths.
func parseType1(data []byte) *fontProgram {
	// PFB segmented format: 0x80 0x01/0x02 length(4, little-endian).
	if len(data) > 6 && data[0] == 0x80 {
		var joined []byte
		i := 0
		for i+6 <= len(data) && data[i] == 0x80 {
			t := data[i+1]
			l := int(binary.LittleEndian.Uint32(data[i+2:]))
			if t == 3 || i+6+l > len(data) {
				break
			}
			joined = append(joined, data[i+6:i+6+l]...)
			i += 6 + l
		}
		data = joined
	}

	// FontMatrix (cleartext, before eexec) scales charstring units to text
	// space; default is 0.001 (1000-unit glyph space).
	scale := 1.0
	if fm := extractType1FontMatrix(data); fm != 0 {
		scale = fm * 1000
	}

	idx := strings.Index(string(data), "eexec")
	if idx < 0 {
		return nil
	}
	enc := data[idx+len("eexec"):]
	// Skip EOL whitespace after eexec.
	for len(enc) > 0 && (enc[0] == '\r' || enc[0] == '\n' || enc[0] == ' ' || enc[0] == '\t') {
		enc = enc[1:]
	}
	// Hex form detection: first 4 bytes all hex digits.
	isHexDigit := func(c byte) bool {
		return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
	}
	if len(enc) >= 4 && isHexDigit(enc[0]) && isHexDigit(enc[1]) && isHexDigit(enc[2]) && isHexDigit(enc[3]) {
		enc = decodeHexBytes(enc)
	}
	priv := eexecDecrypt(enc, 55665, 4)
	text := string(priv)

	lenIV := 4
	if li := strings.Index(text, "/lenIV"); li >= 0 {
		var v int
		if n, _ := sscanInt(text[li+6:]); n {
			v = parseLeadingInt(text[li+6:])
			lenIV = v
		}
	}

	fp := &fontProgram{
		glyphNames:  make(map[string]bool),
		widthByName: make(map[string]float64),
	}
	// CharStrings entries: /name len RD ...bytes... ND
	pos := strings.Index(text, "/CharStrings")
	if pos < 0 {
		return nil
	}
	rest := priv[pos:]
	for {
		s := indexAfter(rest, '/')
		if s < 0 {
			break
		}
		rest = rest[s:]
		nameEnd := 0
		for nameEnd < len(rest) && !isWhitespace(rest[nameEnd]) && rest[nameEnd] != '(' && rest[nameEnd] != '{' {
			nameEnd++
		}
		name := string(rest[:nameEnd])
		rest = rest[nameEnd:]
		// Expect: <len> RD/-| <bytes> ND/|-
		var csLen int
		j := 0
		for j < len(rest) && isWhitespace(rest[j]) {
			j++
		}
		numStart := j
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j == numStart {
			if name == "CharStrings" || strings.HasPrefix(name, "Private") {
				continue
			}
			if strings.HasPrefix(name, "end") {
				break
			}
			continue
		}
		csLen = parseLeadingInt(string(rest[numStart:j]))
		for j < len(rest) && isWhitespace(rest[j]) {
			j++
		}
		// Skip the RD token (RD or -|).
		tokStart := j
		for j < len(rest) && !isWhitespace(rest[j]) {
			j++
		}
		if j >= len(rest) || j == tokStart {
			break
		}
		j++ // single space after RD
		if j+csLen > len(rest) {
			break
		}
		cs := eexecDecrypt(rest[j:j+csLen], 4330, lenIV)
		if w, ok := type1CharstringWidth(cs); ok {
			fp.widthByName[name] = w * scale
		}
		fp.glyphNames[name] = true
		rest = rest[j+csLen:]
		if strings.Contains(name, "end") {
			break
		}
	}
	delete(fp.glyphNames, "")
	return fp
}

func indexAfter(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i + 1
		}
	}
	return -1
}

func sscanInt(s string) (bool, int) {
	i := 0
	for i < len(s) && s[i] == ' ' {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start {
		return false, 0
	}
	return true, parseLeadingInt(s[start:i])
}

func parseLeadingInt(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		v = v*10 + int(c-'0')
	}
	return v
}

// eexecDecrypt implements the Type 1 decryption (r=55665 for eexec,
// r=4330 for charstrings), discarding the first n plaintext bytes.
func eexecDecrypt(data []byte, r uint16, discard int) []byte {
	const c1, c2 = 52845, 22719
	out := make([]byte, 0, len(data))
	for _, c := range data {
		p := c ^ byte(r>>8)
		r = (uint16(c)+r)*c1 + c2
		out = append(out, p)
	}
	if discard >= len(out) {
		return nil
	}
	return out[discard:]
}

// type1CharstringWidth extracts the width from a decrypted Type 1
// charstring: hsbw (13) gives [sbx wx], sbw (12 7) gives [sbx sby wx wy].
func type1CharstringWidth(cs []byte) (float64, bool) {
	var operands []float64
	i := 0
	for i < len(cs) {
		v := int(cs[i])
		switch {
		case v >= 32 && v <= 246:
			operands = append(operands, float64(v-139))
			i++
		case v >= 247 && v <= 250:
			if i+2 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64((v-247)*256+int(cs[i+1])+108))
			i += 2
		case v >= 251 && v <= 254:
			if i+2 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64(-(v-251)*256-int(cs[i+1])-108))
			i += 2
		case v == 255:
			if i+5 > len(cs) {
				return 0, false
			}
			operands = append(operands, float64(int32(binary.BigEndian.Uint32(cs[i+1:]))))
			i += 5
		case v == 13: // hsbw
			if len(operands) >= 2 {
				return operands[1], true
			}
			return 0, false
		case v == 12:
			if i+1 < len(cs) && cs[i+1] == 7 { // sbw
				if len(operands) >= 3 {
					return operands[2], true
				}
				return 0, false
			}
			i += 2
		default:
			return 0, false
		}
	}
	return 0, false
}

// extractType1FontMatrix reads the x-scale of a Type 1 font's cleartext
// /FontMatrix (default 0.001), used to normalise charstring widths to
// 1/1000 text-space units.
func extractType1FontMatrix(data []byte) float64 {
	i := strings.Index(string(data), "/FontMatrix")
	if i < 0 {
		return 0
	}
	s := string(data[i:])
	lb := strings.IndexByte(s, '[')
	if lb < 0 {
		return 0
	}
	rb := strings.IndexByte(s[lb:], ']')
	if rb < 0 {
		return 0
	}
	fields := strings.Fields(s[lb+1 : lb+rb])
	if len(fields) < 1 {
		return 0
	}
	var f float64
	fmt_Sscan(fields[0], &f)
	return f
}
