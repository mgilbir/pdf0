// Package fonts embeds font programs into a PDF and answers the measurement
// questions laying text out asks.
//
// It is the other half of drawing text. The content package writes the
// operators; this decides what bytes those operators show and puts the font
// program in the file so a reader can render them.
//
// # Composite fonts only, deliberately
//
// A face is embedded as a Type0 font with Identity-H encoding and a
// CIDFontType2 descendant (ISO 32000-2 9.7). The alternative — a simple font
// with a single-byte encoding — is limited to 256 codes and to the glyphs a
// standard encoding names, which rules out most of Unicode. Anything laying out
// real text needs the composite form, so that is the only form here rather than
// a choice to get wrong.
//
// The practical consequence is in the encoding: a character code is two bytes,
// big-endian, and equals the glyph index. Encode does that mapping; the bytes it
// returns are what content.Builder.ShowText takes.
//
// # Subsetting, and the ordering it imposes
//
// Only the glyphs a face has been asked to encode are embedded, so Embed must
// come after the drawing that uses the font. Embedding first produces a font
// carrying .notdef alone, and every glyph the document goes on to show is one
// the program does not define; Embed refuses that rather than writing it.
//
// # Shaping
//
// Shape applies the font's own kerning and ligatures and returns spans ready
// for a text operator; Encode is the plain path that maps runes to glyphs one
// at a time. Neither resolves scripts or language systems, attaches marks or
// reorders glyphs, so text in a script that needs those — Arabic, Devanagari —
// is not correctly set by this package. See layout.go for exactly what is read.
//
// # What it does not do
//
// Both glyf and CFF outlines are subsetted, by the same rule: glyph indices are
// retained and a dropped glyph becomes an empty one.
//
// A CID-keyed CFF is refused outright. Its CIDs are not glyph indices, and
// everything here assumes they are.
package fonts

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mgilbir/pdf0/internal/font"
	"github.com/mgilbir/pdf0/object"
)

// maxCmapWork bounds the cmap parse. A font reaching this is malformed or
// hostile; the reader reports a partial cmap rather than spinning, and Load
// refuses it rather than embedding a font whose mapping it only half knows.
const maxCmapWork = 1 << 22

// Face is a loaded font program: its metrics, its character-to-glyph mapping,
// and the bytes to embed.
//
// It is not safe for concurrent use. Encode records which glyphs a document
// used, so two goroutines encoding through one Face race on that record.
type Face struct {
	data []byte
	prog *font.Program

	name       string
	unitsPerEm int
	ascent     int
	descent    int
	capHeight  int
	bbox       [4]int
	italic     float64
	stemV      int
	flags      int

	// cff reports that the outlines are CFF rather than glyf, which changes
	// both how the program is embedded and whether it can be subsetted.
	cff bool

	layout *layout // kerning and ligatures, empty when the font declares none

	used map[int]bool // glyph indices this face has encoded
}

// Load parses an sfnt font program — TrueType or OpenType — and prepares it for
// embedding. The bytes are retained and written into the PDF as they are.
func Load(data []byte) (*Face, error) {
	tables := font.SFNTTables(data)
	if tables == nil {
		return nil, errors.New("fonts: not an sfnt font program (TrueType or OpenType)")
	}
	_, hasGlyf := tables["glyf"]
	_, hasCFF := tables["CFF "]
	if !hasGlyf && !hasCFF {
		return nil, errors.New("fonts: the font carries neither glyf nor CFF outlines")
	}
	prog := font.ParseSFNT(data, maxCmapWork)
	if prog == nil {
		return nil, errors.New("fonts: the font program could not be parsed")
	}
	if prog.CmapPartial {
		return nil, errors.New("fonts: the font's character map is truncated, so its glyph coverage is unknown")
	}
	if len(prog.Cmap) == 0 {
		return nil, errors.New("fonts: the font has no Unicode character map")
	}
	if !hasGlyf {
		// The CFF table has to be parsed on its own: the sfnt reader answers
		// questions from cmap, hmtx and maxp and never opens it, so nothing
		// about the outlines is known until it is asked directly. (Reading
		// prog.WidthByCID here instead would be a check that can never fire.)
		cff := font.ParseCFF(tables["CFF "])
		if cff == nil {
			return nil, errors.New("fonts: the CFF table could not be parsed")
		}
		if cff.WidthByCID != nil {
			// A CID-keyed CFF numbers its glyphs by CID and maps CID to glyph
			// index through its charset — two numberings, not one. Everything
			// here assumes they are the same: Encode emits glyph indices as
			// character codes, and /W is written by glyph index. Embedding one
			// anyway produces widths keyed by one numbering and codes by the
			// other, which this module's own validator reports.
			//
			// Handling it means reading the charset and encoding through it.
			// Refusing until then is the honest answer; mis-embedding is not.
			return nil, errors.New("fonts: CID-keyed CFF fonts are not supported; their CIDs are not glyph indices")
		}
	}

	f := &Face{
		data:       data,
		prog:       prog,
		cff:        !hasGlyf,
		unitsPerEm: 1000,
		used:       map[int]bool{},
	}
	head := tables["head"]
	if len(head) >= 54 {
		if u := font.Be16(head, 18); u > 0 {
			f.unitsPerEm = u
		}
		f.bbox = [4]int{
			signed16(font.Be16(head, 36)), signed16(font.Be16(head, 38)),
			signed16(font.Be16(head, 40)), signed16(font.Be16(head, 42)),
		}
		if font.Be16(head, 44)&0x02 != 0 { // macStyle italic
			f.italic = -12
		}
	}
	if hhea := tables["hhea"]; len(hhea) >= 36 {
		f.ascent = signed16(font.Be16(hhea, 4))
		f.descent = signed16(font.Be16(hhea, 6))
	}
	if os2 := tables["OS/2"]; len(os2) >= 90 {
		f.capHeight = signed16(font.Be16(os2, 88))
	}
	f.stemV = stemV(tables["OS/2"])
	if f.capHeight == 0 {
		f.capHeight = f.ascent
	}
	f.layout = readLayout(tables)
	f.name = postScriptName(tables["name"])
	if f.name == "" {
		f.name = "Embedded"
	}
	// Flags (ISO 32000-2 9.8.2, Table 121). Symbolic is the honest answer for a
	// font embedded with Identity-H: the codes are glyph indices, not
	// characters in any standard encoding, so bit 3 (Symbolic) is set and bit 6
	// (Nonsymbolic) is not.
	f.flags = 1 << 2 // Symbolic
	if isFixedPitch(prog) {
		f.flags |= 1 // FixedPitch
	}
	if f.italic != 0 {
		f.flags |= 1 << 6 // Italic
	}
	return f, nil
}

// Name is the font's PostScript name, which becomes /BaseFont.
func (f *Face) Name() string { return f.name }

// GlyphID maps a rune to its glyph index, reporting whether the font has one.
func (f *Face) GlyphID(r rune) (int, bool) {
	gid, ok := f.prog.Cmap[r]
	return gid, ok && gid != 0
}

// Advance is the horizontal advance of a rune in thousandths of an em, the
// unit PDF text space uses. It reports whether the font maps the rune at all;
// for one it does not, the advance is .notdef's.
func (f *Face) Advance(r rune) (float64, bool) {
	gid, ok := f.GlyphID(r)
	if !ok {
		return f.advanceGID(0), false
	}
	return f.advanceGID(gid), true
}

func (f *Face) advanceGID(gid int) float64 {
	if gid < 0 || gid >= len(f.prog.WidthByGID) {
		return 0
	}
	return f.prog.WidthByGID[gid]
}

// Measure is the width of a string set at the given size, in user-space units.
// Runes the font does not map contribute .notdef's advance, which is what a
// renderer will draw.
func (f *Face) Measure(s string, size float64) float64 {
	var total float64
	for _, r := range s {
		w, _ := f.Advance(r)
		total += w
	}
	return total * size / 1000
}

// Encode maps a string to the character codes a Type0/Identity-H font expects:
// two bytes per glyph, big-endian, each equal to the glyph index. The result is
// what content.Builder.ShowText takes.
//
// A rune the font does not map encodes as glyph 0, which renders as .notdef —
// the visible "this font has no glyph for that" box. That is deliberate: an
// error here would mean a caller could not lay out text containing one stray
// character, and silently dropping it would lose content. The second result
// reports how many runes were missing so a caller that cares can react.
func (f *Face) Encode(s string) (codes []byte, missing int) {
	codes = make([]byte, 0, 2*len(s))
	for _, r := range s {
		gid, ok := f.GlyphID(r)
		if !ok {
			missing++
			gid = 0
		}
		f.used[gid] = true
		codes = append(codes, byte(gid>>8), byte(gid))
	}
	return codes, missing
}

// Used returns the glyph indices this face has encoded, in order. It is what a
// subsetter will keep, and what /CIDSet is written from.
func (f *Face) Used() []int {
	out := make([]int, 0, len(f.used))
	for gid := range f.used {
		out = append(out, gid)
	}
	sort.Ints(out)
	return out
}

// scale converts a value in font units to the 1/1000 em units PDF wants.
func (f *Face) scale(v int) float64 {
	return float64(v) * 1000 / float64(f.unitsPerEm)
}

func signed16(v int) int {
	if v >= 0x8000 {
		return v - 0x10000
	}
	return v
}

// isFixedPitch reports whether every mapped glyph has the same advance.
func isFixedPitch(p *font.Program) bool {
	first, seen := 0.0, false
	for _, gid := range p.Cmap {
		if gid <= 0 || gid >= len(p.WidthByGID) {
			continue
		}
		w := p.WidthByGID[gid]
		if !seen {
			first, seen = w, true
			continue
		}
		if w != first {
			return false
		}
	}
	return seen
}

// postScriptName reads name ID 6 from an sfnt name table, preferring the
// Windows/Unicode record a modern font carries and falling back to the
// Macintosh/Roman one.
func postScriptName(name []byte) string {
	if len(name) < 6 {
		return ""
	}
	count := font.Be16(name, 2)
	storage := font.Be16(name, 4)
	var mac string
	for i := 0; i < count; i++ {
		rec := 6 + 12*i
		if rec+12 > len(name) {
			break
		}
		if font.Be16(name, rec+6) != 6 { // nameID
			continue
		}
		platform := font.Be16(name, rec)
		length := font.Be16(name, rec+8)
		off := storage + font.Be16(name, rec+10)
		if off+length > len(name) {
			continue
		}
		raw := name[off : off+length]
		switch platform {
		case 3, 0: // Windows or Unicode: UTF-16BE
			var s []byte
			for j := 0; j+1 < len(raw); j += 2 {
				if r := int(raw[j])<<8 | int(raw[j+1]); r > 0 && r < 0x80 {
					s = append(s, byte(r))
				}
			}
			if len(s) > 0 {
				return sanitizeName(string(s))
			}
		case 1: // Macintosh: single byte
			if mac == "" && len(raw) > 0 {
				mac = sanitizeName(string(raw))
			}
		}
	}
	return mac
}

// sanitizeName keeps a PostScript name to the characters ISO 32000-2 9.8.2
// allows in one. A name out of a font file is untrusted input.
func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < 63; i++ {
		c := s[i]
		if c > ' ' && c < 0x7F && c != '(' && c != ')' && c != '<' && c != '>' &&
			c != '[' && c != ']' && c != '{' && c != '}' && c != '/' && c != '%' && c != '#' {
			out = append(out, c)
		}
	}
	return string(out)
}

// widthsArray builds the /W entry: the advance of every glyph that differs from
// /DW, in the consecutive-run form ISO 32000-2 9.7.4.3 defines.
//
// It is written from the embedded program's own hmtx rather than from anything
// the caller supplies, because PDF/A checks the two against each other: a /W
// that disagrees with the program is a finding, and the only way to be sure
// they agree is to have one source.
func (f *Face) widthsArray(defaultWidth float64) object.Array {
	n := len(f.prog.WidthByGID)
	var out object.Array
	for gid := 0; gid < n; {
		if f.prog.WidthByGID[gid] == defaultWidth {
			gid++
			continue
		}
		start := gid
		var run object.Array
		for gid < n && f.prog.WidthByGID[gid] != defaultWidth {
			run = append(run, widthNumber(f.prog.WidthByGID[gid]))
			gid++
		}
		out = append(out, object.Integer(start), run)
	}
	return out
}

// widthNumber writes a width as an integer when it is one, which keeps /W
// compact and matches what the validator compares against.
func widthNumber(w float64) object.Object {
	if w == float64(int(w)) {
		return object.Integer(int(w))
	}
	return object.Real(w)
}

// mostCommonWidth picks /DW: the advance shared by the most glyphs, so /W
// carries the exceptions rather than the rule.
func (f *Face) mostCommonWidth() float64 {
	counts := map[float64]int{}
	for _, w := range f.prog.WidthByGID {
		counts[w]++
	}
	best, bestN := 1000.0, -1
	for w, n := range counts {
		if n > bestN || (n == bestN && w < best) {
			best, bestN = w, n
		}
	}
	return best
}

var errNoGlyphs = fmt.Errorf("fonts: the font program declares no glyphs")

// stemV estimates the dominant vertical stem width, which /FontDescriptor
// requires (ISO 32000-2 9.8.1, Table 120).
//
// It is an estimate and cannot honestly be anything else here. StemV is a Type 1
// notion: an sfnt does not carry it, and the only way to measure it is to
// analyse glyph outlines, deciding which contour segments are the stem of a
// letter — real work, and work whose answer no consumer in this module checks.
//
// What the font does carry is the weight it claims, in OS/2 usWeightClass, and
// stem width tracks weight closely. The relation below is the one PDF tooling
// has converged on: roughly 50 units at Thin rising past 200 at Black, growing
// with the square of weight rather than linearly, which is how stems actually
// thicken. A font with no OS/2 table falls back to the value for Regular.
//
// Being wrong here costs little — a viewer uses StemV only to synthesise a
// substitute face when the embedded one is unavailable, which for an embedded
// subset is never — but being wrong in a *documented* way is the point.
func stemV(os2 []byte) int {
	const regular = 400
	weight := regular
	if len(os2) >= 6 {
		if w := font.Be16(os2, 4); w >= 1 && w <= 1000 {
			weight = w
		}
	}
	// 50 at weight 100, ~88 at 400 (Regular), ~165 at 700 (Bold).
	v := 50 + (weight*weight)/6000
	if v > 250 {
		v = 250
	}
	return v
}

// NumGlyphs is the number of glyphs the font program declares, including
// .notdef. It is unchanged by subsetting, which retains glyph indices.
func (f *Face) NumGlyphs() int { return f.prog.NumGlyphs }
