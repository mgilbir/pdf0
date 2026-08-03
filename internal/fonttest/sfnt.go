package fonttest

import (
	"encoding/binary"
	"sort"
)

// A complete, valid sfnt font built from nothing.
//
// The alternative was vendoring a real face or fetching one the way the corpora
// are fetched. A synthetic font is better for testing than either: its metrics
// are whatever the test says they are, so an assertion can name an exact width
// rather than whatever DejaVu happens to use, and it keeps the font tests
// hermetic. It is a real font — the table directory, checksums, glyf outlines
// and cmap are what the format specifies, and this module's own sfnt reader
// parses it — just a very small one.

// Glyph is one glyph of a synthetic font: the character it maps from, its
// advance width in font units, and whether it has an outline. A glyph with no
// outline is a blank one, which is what a space is; it is not a missing glyph.
type Glyph struct {
	Rune     rune
	Advance  int
	HasShape bool
}

// SFNTOptions configures a synthetic font. The zero value is a usable
// 1000-unit-per-em font named "Test".
type SFNTOptions struct {
	Name       string // PostScript name, for /BaseFont; "Test" if empty
	UnitsPerEm int    // 1000 if zero
	Ascent     int    // 800 if zero, in font units
	Descent    int    // -200 if zero, in font units (negative)
	Glyphs     []Glyph
}

// SFNT builds a TrueType font. Glyph 0 is always .notdef, as the format
// requires, and the supplied glyphs follow in order, so the glyph index of
// opts.Glyphs[i] is i+1.
func SFNT(opts SFNTOptions) []byte {
	if opts.Name == "" {
		opts.Name = "Test"
	}
	if opts.UnitsPerEm == 0 {
		opts.UnitsPerEm = 1000
	}
	if opts.Ascent == 0 {
		opts.Ascent = 800
	}
	if opts.Descent == 0 {
		opts.Descent = -200
	}
	numGlyphs := len(opts.Glyphs) + 1

	// glyf and loca. .notdef is empty; a glyph with a shape gets a simple
	// square contour, which is enough for a reader to see a real outline.
	// loca holds numGlyphs+1 offsets: entry i is where glyph i starts, and the
	// last is the end of the final glyph. A glyph whose start equals its end is
	// blank, which is how .notdef and the space glyph are written here.
	var glyf []byte
	loca := make([]uint32, 0, numGlyphs+1)
	loca = append(loca, 0) // .notdef starts at 0
	loca = append(loca, 0) // and is empty, so glyph 1 starts at 0 too
	for _, g := range opts.Glyphs {
		if g.HasShape {
			glyf = append(glyf, simpleSquare(opts.UnitsPerEm)...)
		}
		loca = append(loca, uint32(len(glyf)))
	}
	locaTable := make([]byte, 4*len(loca)) // long format, per head.indexToLocFormat
	for i, off := range loca {
		binary.BigEndian.PutUint32(locaTable[4*i:], off)
	}

	head := make([]byte, 54)
	binary.BigEndian.PutUint32(head[0:], 0x00010000)  // version
	binary.BigEndian.PutUint32(head[4:], 0x00010000)  // fontRevision
	binary.BigEndian.PutUint32(head[12:], 0x5F0F3CF5) // magicNumber
	binary.BigEndian.PutUint16(head[16:], 0)          // flags
	binary.BigEndian.PutUint16(head[18:], uint16(opts.UnitsPerEm))
	putI16(head[36:], 0)                      // xMin
	putI16(head[38:], int16(opts.Descent))    // yMin
	putI16(head[40:], int16(opts.UnitsPerEm)) // xMax
	putI16(head[42:], int16(opts.Ascent))     // yMax
	binary.BigEndian.PutUint16(head[44:], 0)  // macStyle
	binary.BigEndian.PutUint16(head[46:], 8)  // lowestRecPPEM
	binary.BigEndian.PutUint16(head[50:], 1)  // indexToLocFormat: 1 = long
	binary.BigEndian.PutUint16(head[52:], 0)  // glyphDataFormat

	hhea := make([]byte, 36)
	binary.BigEndian.PutUint32(hhea[0:], 0x00010000)
	putI16(hhea[4:], int16(opts.Ascent))
	putI16(hhea[6:], int16(opts.Descent))
	putI16(hhea[8:], 0) // lineGap
	binary.BigEndian.PutUint16(hhea[34:], uint16(numGlyphs))

	hmtx := make([]byte, 4*numGlyphs)
	binary.BigEndian.PutUint16(hmtx[0:], 0) // .notdef advance
	for i, g := range opts.Glyphs {
		binary.BigEndian.PutUint16(hmtx[4*(i+1):], uint16(g.Advance))
	}

	maxp := make([]byte, 32)
	binary.BigEndian.PutUint32(maxp[0:], 0x00010000)
	binary.BigEndian.PutUint16(maxp[4:], uint16(numGlyphs))
	binary.BigEndian.PutUint16(maxp[6:], 4) // maxPoints

	// cmap: one (3,1) format-4 subtable, one segment per glyph plus the
	// mandatory 0xFFFF sentinel.
	segs := make([][3]int, 0, len(opts.Glyphs)+1)
	sorted := append([]Glyph(nil), opts.Glyphs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Rune < sorted[j].Rune })
	for _, g := range sorted {
		gid := 0
		for i, o := range opts.Glyphs {
			if o.Rune == g.Rune {
				gid = i + 1
				break
			}
		}
		c := int(g.Rune)
		segs = append(segs, [3]int{c, c, (gid - c) & 0xFFFF})
	}
	segs = append(segs, [3]int{0xFFFF, 0xFFFF, 1})
	cmap := SFNTCmapTable(CmapFormat4(segs))

	// post version 3.0: no glyph names, which is legal and is what a subset
	// font normally carries.
	post := make([]byte, 32)
	binary.BigEndian.PutUint32(post[0:], 0x00030000)

	name := nameTable(opts.Name)

	return assemble(map[string][]byte{
		"cmap": cmap, "glyf": glyf, "head": head, "hhea": hhea, "hmtx": hmtx,
		"loca": locaTable, "maxp": maxp, "name": name, "post": post,
	})
}

// SFNTCmapTable wraps one format-4 subtable in a cmap table with a single
// (3,1) encoding record.
func SFNTCmapTable(subtable []byte) []byte {
	out := make([]byte, 12+len(subtable))
	binary.BigEndian.PutUint16(out[0:], 0) // version
	binary.BigEndian.PutUint16(out[2:], 1) // numTables
	binary.BigEndian.PutUint16(out[4:], 3) // platformID: Windows
	binary.BigEndian.PutUint16(out[6:], 1) // encodingID: BMP
	binary.BigEndian.PutUint32(out[8:], 12)
	copy(out[12:], subtable)
	return out
}

// simpleSquare is one closed contour: a square inset from the em box. Its only
// job is to be a real outline, so that a reader sees a glyph with a shape
// rather than a blank.
func simpleSquare(em int) []byte {
	lo, hi := int16(em/10), int16(em*9/10)
	g := make([]byte, 0, 64)
	hdr := make([]byte, 10)
	putI16(hdr[0:], 1) // numberOfContours
	putI16(hdr[2:], lo)
	putI16(hdr[4:], 0)
	putI16(hdr[6:], hi)
	putI16(hdr[8:], hi)
	g = append(g, hdr...)
	end := make([]byte, 2)
	binary.BigEndian.PutUint16(end, 3) // endPtsOfContours[0] = 3 (four points)
	g = append(g, end...)
	g = append(g, 0, 0) // instructionLength
	// Four on-curve points, x and y as signed 16-bit deltas.
	g = append(g, 0x01, 0x01, 0x01, 0x01) // flags: on-curve, long vectors
	xs := []int16{lo, hi - lo, 0, lo - hi}
	ys := []int16{0, 0, hi - lo, 0}
	for _, v := range xs {
		b := make([]byte, 2)
		putI16(b, v)
		g = append(g, b...)
	}
	for _, v := range ys {
		b := make([]byte, 2)
		putI16(b, v)
		g = append(g, b...)
	}
	for len(g)%4 != 0 { // glyf entries are long-aligned
		g = append(g, 0)
	}
	return g
}

// nameTable builds a name table carrying the PostScript name (name ID 6) in
// the Windows/Unicode encoding, which is where a reader looks first.
func nameTable(psName string) []byte {
	utf16be := make([]byte, 0, 2*len(psName))
	for _, r := range psName {
		utf16be = append(utf16be, byte(r>>8), byte(r))
	}
	const numRecords = 1
	hdr := make([]byte, 6+12*numRecords)
	binary.BigEndian.PutUint16(hdr[0:], 0)          // format
	binary.BigEndian.PutUint16(hdr[2:], numRecords) // count
	binary.BigEndian.PutUint16(hdr[4:], uint16(len(hdr)))
	rec := hdr[6:]
	binary.BigEndian.PutUint16(rec[0:], 3) // platformID: Windows
	binary.BigEndian.PutUint16(rec[2:], 1) // encodingID: Unicode BMP
	binary.BigEndian.PutUint16(rec[4:], 0x0409)
	binary.BigEndian.PutUint16(rec[6:], 6) // nameID: PostScript name
	binary.BigEndian.PutUint16(rec[8:], uint16(len(utf16be)))
	binary.BigEndian.PutUint16(rec[10:], 0) // offset
	return append(hdr, utf16be...)
}

// assemble writes the table directory and the tables, with the checksums and
// the four-byte alignment the format requires.
func assemble(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags) // the directory must be sorted by tag

	n := len(tags)
	searchRange, entrySelector := 16, 0
	for searchRange*2 <= 16*n {
		searchRange *= 2
		entrySelector++
	}

	dir := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(dir[0:], 0x00010000)
	binary.BigEndian.PutUint16(dir[4:], uint16(n))
	binary.BigEndian.PutUint16(dir[6:], uint16(searchRange))
	binary.BigEndian.PutUint16(dir[8:], uint16(entrySelector))
	binary.BigEndian.PutUint16(dir[10:], uint16(16*n-searchRange))

	out := dir
	for i, tag := range tags {
		body := tables[tag]
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
		rec := 12 + 16*i
		copy(dir[rec:], tag)
		binary.BigEndian.PutUint32(dir[rec+4:], checksum(body))
		binary.BigEndian.PutUint32(dir[rec+8:], uint32(len(out)))
		binary.BigEndian.PutUint32(dir[rec+12:], uint32(len(body)))
		out = append(out, body...)
	}
	copy(out, dir) // the directory was mutated after being appended
	return out
}

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(b); i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	if r := len(b) % 4; r != 0 {
		var last [4]byte
		copy(last[:], b[len(b)-r:])
		sum += binary.BigEndian.Uint32(last[:])
	}
	return sum
}

func putI16(b []byte, v int16) { binary.BigEndian.PutUint16(b, uint16(v)) }
