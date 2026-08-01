package font

import (
	"encoding/binary"
	"strings"
)

// Minimal font-program parsers backing the PDF/A font rules: enough of
// TrueType/OpenType (sfnt tables), CFF, and Type 1 to answer three
// questions — which glyphs exist, what are their advance widths (in 1/1000
// text-space units), and which glyph does a character map to.

// Program is the common view the rules consume.
type Program struct {
	// GlyphNames lists the glyph names defined by the program (Type1/CFF
	// non-CID); nil when the format identifies glyphs by index only.
	GlyphNames map[string]bool
	// WidthByName gives advance widths (1/1000 units) for named glyphs.
	WidthByName map[string]float64
	// NumGlyphs is the glyph count (sfnt/CFF).
	NumGlyphs int
	// GlyphPresent[gid] reports whether a TrueType (glyf-based) glyph's
	// outline data lies within the glyf table (present, possibly empty) as
	// opposed to pointing beyond a truncated table (missing). nil when the
	// font has no glyf table (e.g. CFF-flavoured OpenType).
	GlyphPresent []bool
	// GlyphNonEmpty[gid] reports whether the glyph has an outline (a non-zero
	// length glyf entry). An empty entry is a blank glyph such as space.
	GlyphNonEmpty []bool
	// ComponentGID[gid] reports whether the glyph is referenced as a component
	// of a composite glyph. Such a glyph may carry an outline solely to serve
	// as a building block (e.g. an accent) without being a directly mapped CID.
	ComponentGID []bool
	// WidthByGID gives advance widths by glyph index, scaled to 1/1000.
	WidthByGID []float64
	// Cmap maps Unicode code points to glyph indices ((3,1) subtable), and
	// mac maps single-byte codes via the (1,0) subtable; symbolCmap maps
	// 0xF000-prefixed codes via a (3,0) subtable.
	Cmap       map[rune]int
	MacCmap    map[byte]int
	SymbolCmap map[uint16]int
	// CmapSubtableCount is the number of subtables declared in the sfnt cmap
	// table (ISO 19005-1 6.3.7 requires a symbolic TrueType font to have
	// exactly one). Zero when there is no cmap table.
	CmapSubtableCount int
	// CIDGIDs reports which CIDs have charstrings (CFF CID-keyed fonts);
	// nil when not CID-keyed.
	CIDGIDs map[int]bool
	// WidthByCID gives advance widths by CID for CID-keyed CFF.
	WidthByCID map[int]float64
	// CmapPartial reports that a cmap subtable stopped short of its own end
	// because the cmap work budget (limits.cmapWork, WithMaxCmapWork) ran out,
	// so the maps above are missing mappings the font really declares. A
	// consumer must not read "this code is absent from the cmap" as "this code
	// has no glyph" when it is set — that is audit C46's false positive with a
	// different cause: a truncated cmap makes trueTypeGID answer "glyph 0"
	// authoritatively, and a conformant font is then reported as
	// undefined-glyph / .notdef.
	//
	// The sfnt parser has no Document in scope, so it cannot report the trip
	// itself; loadFontProgram, which does, forwards it (see noteLimit).
	CmapPartial bool
}

// --- sfnt (TrueType / OpenType) ---

func Be16(b []byte, off int) int {
	if off+2 > len(b) {
		return 0
	}
	return int(binary.BigEndian.Uint16(b[off:]))
}

func Be32(b []byte, off int) uint32 {
	if off+4 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint32(b[off:])
}

// MarkComposite, given one glyph's glyf bytes, marks every glyph index it
// references as a component (when the glyph is composite, numberOfContours == -1).
func MarkComposite(g []byte, numGlyphs int, out []bool) {
	if len(g) < 2 || int16(Be16(g, 0)) != -1 {
		return
	}
	o := 10
	for o+4 <= len(g) {
		flags := Be16(g, o)
		if cgid := Be16(g, o+2); cgid >= 0 && cgid < numGlyphs {
			out[cgid] = true
		}
		o += 4
		if flags&0x0001 != 0 { // ARG_1_AND_2_ARE_WORDS
			o += 4
		} else {
			o += 2
		}
		switch {
		case flags&0x0008 != 0: // WE_HAVE_A_SCALE
			o += 2
		case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
			o += 4
		case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
			o += 8
		}
		if flags&0x0020 == 0 { // no MORE_COMPONENTS
			break
		}
	}
}

// be16 as signed for the numberOfContours check.

// ParseSFNT parses a TrueType/OpenType font program.
func ParseSFNT(data []byte, maxCmapWork int) *Program {
	if len(data) < 12 {
		return nil
	}
	tag := Be32(data, 0)
	if tag != 0x00010000 && tag != 0x74727565 && tag != 0x4F54544F { // 1.0, 'true', 'OTTO'
		return nil
	}
	numTables := Be16(data, 4)
	tables := make(map[string][]byte)
	for i := 0; i < numTables; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			return nil
		}
		name := string(data[rec : rec+4])
		off := Be32(data, rec+8)
		length := Be32(data, rec+12)
		if uint64(off)+uint64(length) > uint64(len(data)) {
			continue
		}
		tables[name] = data[off : off+length]
	}

	fp := &Program{}
	head := tables["head"]
	unitsPerEm := 1000
	if len(head) >= 20 {
		if u := Be16(head, 18); u > 0 {
			unitsPerEm = u
		}
	}
	if maxp := tables["maxp"]; len(maxp) >= 6 {
		fp.NumGlyphs = Be16(maxp, 4)
	}

	// hmtx: advance widths, scaled to 1/1000 units.
	if hhea, hmtx := tables["hhea"], tables["hmtx"]; len(hhea) >= 36 && hmtx != nil {
		numH := Be16(hhea, 34)
		fp.WidthByGID = make([]float64, fp.NumGlyphs)
		last := 0.0
		for gid := 0; gid < fp.NumGlyphs; gid++ {
			if gid < numH && 4*gid+2 <= len(hmtx) {
				last = float64(Be16(hmtx, 4*gid)) * 1000 / float64(unitsPerEm)
			}
			fp.WidthByGID[gid] = last
		}
	}

	// loca/glyf: which glyph indices have outline data within the table.
	if head, loca, glyf := tables["head"], tables["loca"], tables["glyf"]; len(head) >= 52 && loca != nil && glyf != nil {
		longLoca := Be16(head, 50) == 1
		glyfLen := len(glyf)
		fp.GlyphPresent = make([]bool, fp.NumGlyphs)
		offAt := func(i int) int {
			if longLoca {
				return int(Be32(loca, 4*i))
			}
			return Be16(loca, 2*i) * 2
		}
		fp.GlyphNonEmpty = make([]bool, fp.NumGlyphs)
		fp.ComponentGID = make([]bool, fp.NumGlyphs)
		for gid := 0; gid < fp.NumGlyphs; gid++ {
			start, end := offAt(gid), offAt(gid+1)
			// Present when the entry is well-formed and lies within the glyf
			// table (an empty glyph, start==end, is still present).
			fp.GlyphPresent[gid] = start <= end && end <= glyfLen
			fp.GlyphNonEmpty[gid] = start < end && end <= glyfLen
			if fp.GlyphNonEmpty[gid] {
				MarkComposite(glyf[start:end], fp.NumGlyphs, fp.ComponentGID)
			}
		}
	}

	// cmap subtables.
	if cmap := tables["cmap"]; len(cmap) >= 4 {
		n := Be16(cmap, 2)
		fp.CmapSubtableCount = n
		bestRank := 0
		for i := 0; i < n; i++ {
			rec := 4 + 8*i
			if rec+8 > len(cmap) {
				break
			}
			plat := Be16(cmap, rec)
			enc := Be16(cmap, rec+2)
			off := Be32(cmap, rec+4)
			if uint64(off) >= uint64(len(cmap)) {
				continue
			}
			sub := cmap[off:]
			rank := unicodeCmapRank(plat, enc)
			switch {
			case rank > 0:
				// Several Unicode subtables may be present; take the best one
				// (see unicodeCmapRank). Ties are resolved in favour of the
				// later subtable, which is how a font carrying two equally
				// ranked subtables has always been read.
				if rank < bestRank {
					continue
				}
				m, partial := ParseCmapSubtable(sub, maxCmapWork)
				if m != nil {
					fp.Cmap = m
					bestRank = rank
					// The chosen cmap's partialness is what matters; a
					// discarded lower-ranked subtable's is not.
					fp.CmapPartial = partial
				}
			case plat == 3 && enc == 0:
				m, partial := ParseCmapSubtable(sub, maxCmapWork)
				if m == nil {
					continue // unreadable: leave the cmap unset, not empty
				}
				fp.CmapPartial = fp.CmapPartial || partial
				fp.SymbolCmap = make(map[uint16]int, len(m))
				for r, gid := range m {
					fp.SymbolCmap[uint16(r)] = gid
				}
			case plat == 1 && enc == 0:
				m, partial := ParseCmapSubtable(sub, maxCmapWork)
				if m == nil {
					continue
				}
				fp.CmapPartial = fp.CmapPartial || partial
				fp.MacCmap = make(map[byte]int, len(m))
				for r, gid := range m {
					if r <= 0xFF {
						fp.MacCmap[byte(r)] = gid
					}
				}
			}
		}
	}
	return fp
}

// unicodeCmapRank ranks a cmap subtable's (platform, encoding) as a source of
// code-point→GID mappings, higher being better; 0 means "not a Unicode
// subtable" and leaves the pair to the symbol/Mac cases. A font may carry
// several, so the choice has to be deliberate:
//
//	(3,10) Windows full repertoire — a superset of (3,1), reaches beyond the BMP
//	(3,1)  Windows BMP             — what ISO 32000-1 9.6.6.4 names
//	(0,4)/(0,6) Unicode full repertoire
//	(0,0..3)/(0,5) Unicode BMP / variation-sequence-era subtables
//
// The Windows platform outranks the Unicode platform at equal coverage because
// ISO 32000-1 9.6.6.4 describes code→GID lookup in terms of the Windows
// subtables, and because that ordering leaves the pre-existing choice untouched
// for every font whose only mappings are the (3,1)/(3,0)/(1,0) trio.
func unicodeCmapRank(plat, enc int) int {
	switch {
	case plat == 3 && enc == 10:
		return 4
	case plat == 3 && enc == 1:
		return 3
	case plat == 0 && (enc == 4 || enc == 6):
		return 2
	case plat == 0:
		return 1
	}
	return 0
}

// The two expanding subtable formats, 4 and 12, share one work budget: the
// caller's maxWork, resolved from limits.cmapWork (WithMaxCmapWork, default
// defaultMaxCmapWork). One knob rather than two because the two formats are
// alternative encodings of the same thing — a font's code→GID coverage — and no
// caller has a reason to trust one more than the other. Each format charges its
// own counter, so maxWork bounds one subtable, not the whole table.
//
// cmapResult returns out, or nil when it holds no mapping. A subtable that maps
// nothing is, to every caller, indistinguishable from one that could not be
// read, and the distinction that matters is nil vs non-nil: trueTypeGID treats a
// non-nil cmap as authoritative, so an empty one answers "every code is .notdef"
// where the honest answer is "unknown". Reachable from well-formed bytes — a
// 16-byte format-12 subtable declaring nGroups 0, or a budget-exhausted table
// whose every candidate mapping was skipped — so it has to be handled on the way
// out rather than assumed away.
func cmapResult(out map[rune]int) map[rune]int {
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseCmapSubtable handles cmap formats 0, 4, 6, and 12. It returns nil — not an
// empty map — when the subtable cannot be read (an unsupported format, one
// truncated past use, or one that maps nothing at all): callers treat a non-nil
// cmap as authoritative, so an empty map would claim the font maps no character
// at all, which reads as "every code is .notdef" rather than "unknown".
//
// The second result reports that maxWork stopped the parse before the
// subtable's own end, so the returned map is a prefix of the font's real
// coverage. It is separate from the nil result because the mappings that were
// read are still correct — a code the map resolves resolves rightly — but a code
// it does not resolve is unknown rather than absent, and no rule may assert
// against it. Without this the budget reproduces audit C46 exactly.
//
// maxWork bounds the expansion of formats 4 and 12 (see WithMaxCmapWork);
// formats 0 and 6 are bounded by the subtable's own fixed size. Because the
// budget is configurable, a caller who lowers it moves where the prefix ends —
// which is safe precisely because the prefix is self-describing.
func ParseCmapSubtable(b []byte, maxWork int) (map[rune]int, bool) {
	out := make(map[rune]int)
	switch Be16(b, 0) {
	case 0:
		if len(b) < 262 {
			return nil, false
		}
		for c := 0; c < 256; c++ {
			if gid := int(b[6+c]); gid != 0 {
				out[rune(c)] = gid
			}
		}
	case 4:
		segX2 := Be16(b, 6)
		if segX2 == 0 || len(b) < 16+4*segX2 {
			return nil, false
		}
		endBase := 14
		startBase := endBase + segX2 + 2
		deltaBase := startBase + segX2
		rangeBase := deltaBase + segX2
		// A valid format-4 subtable partitions the BMP, so it never needs more
		// than ~65536 inner iterations. A hostile table with many segments each
		// spanning the whole range is O(segments x 65535) — seconds to minutes
		// of CPU on an untrusted font. Bound the total work (audit C10).
		work := 0
		for s := 0; s < segX2; s += 2 {
			end := Be16(b, endBase+s)
			start := Be16(b, startBase+s)
			delta := Be16(b, deltaBase+s)
			rangeOff := Be16(b, rangeBase+s)
			// The final segment of a conformant table is the sentinel
			// 0xFFFF..0xFFFF, which maps nothing. An inverted segment
			// (start > end) can only come from a malformed table; reading it
			// as if it ran from start upwards would walk into the following
			// segments' codes.
			if start == 0xFFFF || start > end {
				continue
			}
			// c is an int, so a segment ending at 0xFFFF stops on the ordinary
			// comparison — there is no 16-bit counter here to wrap. The wrap
			// guard this loop used to carry ("c != 0") therefore protected
			// nothing and, being false on entry, dropped every mapping of a
			// segment beginning at code 0 (audit C46).
			for c := start; c <= end; c++ {
				if work++; work > maxWork {
					return cmapResult(out), len(out) > 0
				}
				var gid int
				if rangeOff == 0 {
					gid = (c + delta) & 0xFFFF
				} else {
					idx := rangeBase + s + rangeOff + 2*(c-start)
					g := Be16(b, idx)
					if g == 0 {
						continue
					}
					gid = (g + delta) & 0xFFFF
				}
				if gid != 0 {
					out[rune(c)] = gid
				}
			}
		}
	case 6:
		first := Be16(b, 6)
		count := Be16(b, 8)
		if len(b) < 10+2*count {
			return nil, false
		}
		// Character codes are 16-bit, so a first+count that runs past 0xFFFF
		// is malformed. Recording those entries would be worse than dropping
		// them: the caller narrows this map to uint16 for the (3,0) symbol
		// cmap, where code 0x10000 would alias onto code 0.
		for i := 0; i < count && first+i <= 0xFFFF; i++ {
			if gid := Be16(b, 10+2*i); gid != 0 {
				out[rune(first+i)] = gid
			}
		}
	case 12:
		// Segmented coverage: format(2) reserved(2) length(4) language(4)
		// nGroups(4), then nGroups groups of startCharCode(4) endCharCode(4)
		// startGlyphID(4). This is the only format that reaches past the BMP,
		// so its keys really can exceed 0xFFFF.
		const unicodeMaxRune = 0x10FFFF
		if len(b) < 16 {
			return nil, false
		}
		length := Be32(b, 4)
		if length < 16 || uint64(length) > uint64(len(b)) {
			return nil, false
		}
		b = b[:length]
		nGroups := Be32(b, 12)
		// nGroups is a uint32: a table may claim four billion groups it does
		// not carry. Trust the bytes, not the count.
		if uint64(nGroups)*12 > uint64(len(b)-16) {
			return nil, false
		}
		// Every group is charged at least one unit of work, so this budget
		// bounds the group loop as well as the expansion. A single group may
		// span the whole of Unicode (0x110000 codes) and there may be many of
		// them, so an unbudgeted expansion is an unbounded allocation driven by
		// the font. The cap is the format-4 one: an sfnt has at most 65535
		// glyphs, so no honest font needs to map anywhere near 2^18 code
		// points, and the resulting map stays a few megabytes at worst.
		work := 0
		for g := 0; g < int(nGroups); g++ {
			if work++; work > maxWork {
				return cmapResult(out), len(out) > 0
			}
			p := 16 + 12*g
			start := Be32(b, p)
			end := Be32(b, p+4)
			startGID := Be32(b, p+8)
			// Groups are required to be sorted and non-overlapping. Neither is
			// enforced here: a table that merely lists them out of order is
			// still unambiguous except where groups overlap, and rejecting it
			// outright would return nil for a font whose mappings are perfectly
			// readable — the caller reads nil as "unknown" and stops checking.
			// Overlaps resolve to the last group written, as elsewhere in this
			// parser. An inverted group (start > end) is another matter: read
			// as running upwards from start it would walk over unrelated codes,
			// so it is skipped, as in format 4.
			if start > end || start > unicodeMaxRune {
				continue
			}
			if end > unicodeMaxRune {
				end = unicodeMaxRune
			}
			for c := start; ; c++ {
				if work++; work > maxWork {
					return cmapResult(out), len(out) > 0
				}
				gid := uint64(startGID) + uint64(c-start)
				// Glyph indices are 16-bit; anything wider is malformed and
				// must not be recorded as if it named a glyph.
				if gid != 0 && gid <= 0xFFFF {
					out[rune(c)] = int(gid)
				}
				if c == end { // c is a uint32; end may be the largest value it holds
					break
				}
			}
		}
	default:
		// Formats 2, 8, 10, 13 and 14 are not parsed.
		return nil, false
	}
	return cmapResult(out), false
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
	count := Be16(b, off)
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
			ParseFloat(sb.String(), &f)
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

// ParseCFF parses a bare CFF font (FontFile3 /Type1C or /CIDFontType0C, or
// the CFF table of an OpenType font).
func ParseCFF(data []byte) *Program {
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

	fp := &Program{}
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
	fp.NumGlyphs = len(charStrings.items)

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
	gidToSID := make([]int, fp.NumGlyphs)
	if fp.NumGlyphs > 0 {
		gidToSID[0] = 0 // .notdef
	}
	switch charsetOff {
	case 0: // ISOAdobe: identity SIDs
		for g := 1; g < fp.NumGlyphs; g++ {
			gidToSID[g] = g
		}
	case 1, 2:
		// Expert charsets — rare; leave identity.
		for g := 1; g < fp.NumGlyphs; g++ {
			gidToSID[g] = g
		}
	default:
		if charsetOff > 0 && charsetOff < len(data) {
			b := data[charsetOff:]
			switch b[0] {
			case 0:
				for g := 1; g < fp.NumGlyphs; g++ {
					if 1+2*g > len(b) {
						break
					}
					gidToSID[g] = Be16(b, 1+2*(g-1))
				}
			case 1, 2:
				g := 1
				p := 1
				step := 3
				if b[0] == 2 {
					step = 4
				}
				for g < fp.NumGlyphs && p+step <= len(b) {
					first := Be16(b, p)
					var count int
					if b[0] == 1 {
						count = int(b[p+2])
					} else {
						count = Be16(b, p+2)
					}
					for k := 0; k <= count && g < fp.NumGlyphs; k++ {
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
		fp.CIDGIDs = make(map[int]bool, fp.NumGlyphs)
		fp.WidthByCID = make(map[int]float64, fp.NumGlyphs)
		for g := 0; g < fp.NumGlyphs; g++ {
			cid := gidToSID[g]
			fp.CIDGIDs[cid] = true
			fp.WidthByCID[cid] = widthOf(charStrings.items[g])
		}
	} else {
		fp.GlyphNames = make(map[string]bool, fp.NumGlyphs)
		fp.WidthByName = make(map[string]float64, fp.NumGlyphs)
		for g := 0; g < fp.NumGlyphs; g++ {
			name := cffSIDName(gidToSID[g], stringsIdx)
			fp.GlyphNames[name] = true
			fp.WidthByName[name] = widthOf(charStrings.items[g])
		}
	}
	fp.WidthByGID = make([]float64, fp.NumGlyphs)
	for g := 0; g < fp.NumGlyphs; g++ {
		fp.WidthByGID[g] = widthOf(charStrings.items[g])
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

// ParseFloat is a tiny indirection so parseCFFDict avoids importing fmt just
// for BCD reals.
func ParseFloat(s string, f *float64) {
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

// ParseType1 parses a Type 1 font program (FontFile): the eexec-encrypted
// private portion holds the CharStrings dictionary with glyph names and
// hsbw/sbw widths.
func ParseType1(data []byte) *Program {
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
		// sscanInt skips the leading space after "/lenIV"; use its value
		// directly (parseLeadingInt would start on the space and return 0).
		if ok, val := sscanInt(text[li+6:]); ok {
			lenIV = val
		}
	}

	fp := &Program{
		GlyphNames:  make(map[string]bool),
		WidthByName: make(map[string]float64),
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
			fp.WidthByName[name] = w * scale
		}
		fp.GlyphNames[name] = true
		rest = rest[j+csLen:]
		if Type1CharStringsEnd(rest) {
			break
		}
	}
	delete(fp.GlyphNames, "")
	return fp
}

// Type1CharStringsEnd reports whether the bytes following a CharStrings entry's
// charstring data close the dictionary. A Type 1 CharStrings dictionary
// (Adobe's Type 1 Font Format, 10.3) ends with a standalone "end" token after
// the last entry's ND (or |-) token:
//
//	/A 45 RD ~~~~~ ND
//	end
//
// so the terminator is a PostScript token in the byte stream, never a glyph
// name. Testing the glyph name for "end" instead truncated the glyph list at the
// first font defining endash (or enfilledcircbullet, or any other name
// containing "end"), which then read as a font that does not define the glyphs
// it was asked to render.
//
// It reads the ND token and the one after it; a dictionary that omits ND
// terminates on the first token, which is why both positions are compared.
func Type1CharStringsEnd(b []byte) bool {
	i := 0
	for k := 0; k < 2; k++ {
		for i < len(b) && isWhitespace(b[i]) {
			i++
		}
		start := i
		for i < len(b) && !isWhitespace(b[i]) && b[i] != '/' {
			i++
		}
		if string(b[start:i]) == "end" {
			return true
		}
		if start == i {
			return false // a '/' (the next entry) or the data ran out
		}
	}
	return false
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
	ParseFloat(fields[0], &f)
	return f
}
