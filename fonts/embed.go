package fonts

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/mgilbir/pdf0/object"
)

// Embedding a face as the PDF object graph a reader needs: a Type0 font, its
// CIDFontType2 descendant, a font descriptor, the program itself, a CIDSet and
// a ToUnicode CMap (ISO 32000-2 9.7).
//
// What each of these must contain is not a matter of taste here. This module's
// PDF/A validator already checks the lot — that /W agrees with the embedded
// program's own metrics, that /CIDSet lists exactly the glyphs the program has,
// that a Type0 font carries a ToUnicode CMap — so the specification of this
// writer is executable and the tests aim it back at the output.

// Allocator adds an object to a document and returns the reference to it. It is
// declared here, where it is consumed, so this package does not depend on the
// one that implements it; *pdf0.Document does.
type Allocator interface {
	Add(object.Object) object.IndirectRef
}

// Embed writes the face into doc and returns the reference to put in a page's
// /Resources /Font.
//
// The whole program is embedded, not a subset: every glyph the font has ends up
// in the file whether the document uses it or not. For a large face that is
// tens of megabytes, and it is the next thing to fix.
func (f *Face) Embed(doc Allocator) (object.IndirectRef, error) {
	if f.prog.NumGlyphs == 0 {
		return object.IndirectRef{}, errNoGlyphs
	}

	// The program. /Length1 is the uncompressed length, which a reader needs to
	// know how much of a compressed stream is font data.
	program := &object.Stream{Dict: object.Dictionary{}, Data: f.data}
	program.Dict.Set("Length", object.Integer(len(f.data)))
	program.Dict.Set("Length1", object.Integer(len(f.data)))
	programRef := doc.Add(program)

	// /CIDSet: one bit per CID, high bit of each byte first, set when the
	// program has that glyph. Embedding whole means every glyph is present, and
	// the validator checks this against the program rather than taking it on
	// trust — so it is written from the program.
	//
	// It is not required for a font embedded whole, only for a subset, and
	// writing it anyway is what makes that transition safe: when subsetting
	// lands the /BaseFont gains a six-letter tag, /CIDSet becomes mandatory,
	// and its contents must then list the subset's glyphs rather than the
	// original's. This is the line that has to change with it.
	cidSet := &object.Stream{Dict: object.Dictionary{}, Data: f.cidSetBits()}
	cidSet.Dict.Set("Length", object.Integer(len(cidSet.Data)))
	cidSetRef := doc.Add(cidSet)

	descriptor := &object.Dictionary{}
	descriptor.Set("Type", object.Name("FontDescriptor"))
	descriptor.Set("FontName", object.Name(f.name))
	descriptor.Set("Flags", object.Integer(f.flags))
	descriptor.Set("FontBBox", object.Array{
		object.Integer(int(f.scale(f.bbox[0]))), object.Integer(int(f.scale(f.bbox[1]))),
		object.Integer(int(f.scale(f.bbox[2]))), object.Integer(int(f.scale(f.bbox[3]))),
	})
	descriptor.Set("ItalicAngle", object.Real(f.italic))
	descriptor.Set("Ascent", object.Integer(int(f.scale(f.ascent))))
	descriptor.Set("Descent", object.Integer(int(f.scale(f.descent))))
	descriptor.Set("CapHeight", object.Integer(int(f.scale(f.capHeight))))
	// StemV has no counterpart in an sfnt: it is a Type 1 notion that the
	// format simply does not carry. A descriptor must have one, so this is an
	// estimate and is documented as such rather than dressed up as measured.
	descriptor.Set("StemV", object.Integer(80))
	descriptor.Set("FontFile2", programRef)
	descriptor.Set("CIDSet", cidSetRef)
	descriptorRef := doc.Add(descriptor)

	defaultWidth := f.mostCommonWidth()
	cidFont := &object.Dictionary{}
	cidFont.Set("Type", object.Name("Font"))
	cidFont.Set("Subtype", object.Name("CIDFontType2"))
	cidFont.Set("BaseFont", object.Name(f.name))
	sysInfo := &object.Dictionary{}
	sysInfo.Set("Registry", object.String{Value: []byte("Adobe")})
	sysInfo.Set("Ordering", object.String{Value: []byte("Identity")})
	sysInfo.Set("Supplement", object.Integer(0))
	cidFont.Set("CIDSystemInfo", sysInfo)
	cidFont.Set("FontDescriptor", descriptorRef)
	cidFont.Set("DW", widthNumber(defaultWidth))
	if w := f.widthsArray(defaultWidth); len(w) > 0 {
		cidFont.Set("W", w)
	}
	// Identity: a CID is a glyph index, which is what Identity-H encoding
	// already made the character codes.
	cidFont.Set("CIDToGIDMap", object.Name("Identity"))
	cidFontRef := doc.Add(cidFont)

	toUnicode := &object.Stream{Dict: object.Dictionary{}, Data: f.toUnicodeCMap()}
	toUnicode.Dict.Set("Length", object.Integer(len(toUnicode.Data)))
	toUnicodeRef := doc.Add(toUnicode)

	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type0"))
	font.Set("BaseFont", object.Name(f.name))
	font.Set("Encoding", object.Name("Identity-H"))
	font.Set("DescendantFonts", object.Array{cidFontRef})
	font.Set("ToUnicode", toUnicodeRef)
	return doc.Add(font), nil
}

// cidSetBits builds the /CIDSet bitmap: bit i, counting from the high bit of
// byte 0, is set when the program has glyph i.
func (f *Face) cidSetBits() []byte {
	n := f.prog.NumGlyphs
	bits := make([]byte, (n+7)/8)
	for gid := 0; gid < n; gid++ {
		if f.prog.GlyphPresent != nil && gid < len(f.prog.GlyphPresent) && !f.prog.GlyphPresent[gid] {
			continue
		}
		bits[gid/8] |= 0x80 >> (gid % 8)
	}
	return bits
}

// toUnicodeCMap builds the CMap that maps character codes back to Unicode
// (ISO 32000-2 9.10.3). Without it the text on the page cannot be extracted,
// searched or read aloud — the glyph indices mean nothing outside the font —
// and PDF/A requires one.
//
// It is written from the font's own cmap, inverted: every code the encoder can
// produce for a rune maps back to that rune. Where several runes share a glyph
// the lowest wins, which is arbitrary but deterministic, and the alternative —
// omitting the entry — would make that glyph unextractable.
func (f *Face) toUnicodeCMap() []byte {
	rev := make(map[int]rune, len(f.prog.Cmap))
	for r, gid := range f.prog.Cmap {
		if gid == 0 {
			continue
		}
		if prev, ok := rev[gid]; !ok || r < prev {
			rev[gid] = r
		}
	}
	gids := make([]int, 0, len(rev))
	for gid := range rev {
		gids = append(gids, gid)
	}
	sort.Ints(gids)

	var b bytes.Buffer
	b.WriteString(`/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
`)
	// bfchar sections are capped at 100 entries by the specification.
	for start := 0; start < len(gids); start += 100 {
		end := start + 100
		if end > len(gids) {
			end = len(gids)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, gid := range gids[start:end] {
			fmt.Fprintf(&b, "<%04X> <%s>\n", gid, utf16beHex(rev[gid]))
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString(`endcmap
CMapName currentdict /CMap defineresource pop
end
end
`)
	return b.Bytes()
}

// utf16beHex renders a rune as the hexadecimal UTF-16BE a bfchar destination
// takes, including the surrogate pair an astral character needs.
func utf16beHex(r rune) string {
	if r > 0xFFFF {
		r -= 0x10000
		hi := 0xD800 + (r >> 10)
		lo := 0xDC00 + (r & 0x3FF)
		return fmt.Sprintf("%04X%04X", hi, lo)
	}
	return fmt.Sprintf("%04X", r)
}
