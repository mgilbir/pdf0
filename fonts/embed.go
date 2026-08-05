package fonts

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/mgilbir/forme/font"
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
//
// The facts come from the face in the font's own units and are put into PDF's
// form here: lengths scaled to a thousandth of an em, flags as the bits
// Table 121 defines, widths as the run-length array /W takes.

// Allocator adds an object to a document and returns the reference to it. It is
// declared here, where it is consumed, so this package does not depend on the
// one that implements it; *pdf0.Document does.
type Allocator interface {
	Add(object.Object) object.IndirectRef
}

var errNoGlyphs = errors.New("fonts: the font program declares no glyphs")

// errEmbedBeforeUse is refused rather than written. Embedding before anything
// has been encoded produces a font carrying .notdef alone, and every glyph the
// document goes on to show is then one the program does not define. That is a
// silent mistake — the file is written, and only a validator or a reader
// notices — so it is caught here, where the cause is still obvious.
var errEmbedBeforeUse = errors.New(
	"fonts: Embed before any text was encoded would embed no glyphs; " +
		"encode the document's text first, then embed")

// Embed writes the face into doc and returns the reference to put in a page's
// /Resources /Font.
//
// Only the glyphs the face has encoded are embedded, so Embed comes after the
// drawing that uses the font, not before it. A face embedded before anything is
// drawn carries .notdef alone, which is correct and useless; the ordering is
// the caller's to get right and there is no way for this package to check it.
//
// The name written to /BaseFont carries the six-letter subset tag ISO 32000-2
// 9.6.4 requires, so a reader can tell two subsets of one face apart.
func (f *Face) Embed(doc Allocator) (object.IndirectRef, error) {
	if f.IsStandard() {
		// A standard face embeds nothing: the reader has it, and naming it is
		// the whole mechanism.
		return f.embedStandard(doc)
	}
	if f.IsSimple() {
		if len(f.Used()) == 0 {
			return object.IndirectRef{}, errEmbedBeforeUse
		}
		return f.embedSimple(doc)
	}
	if f.NumGlyphs() == 0 {
		return object.IndirectRef{}, errNoGlyphs
	}
	if len(f.Used()) == 0 {
		return object.IndirectRef{}, errEmbedBeforeUse
	}

	program, kept, err := f.SubsetGlyphs()
	if err != nil {
		return object.IndirectRef{}, err
	}
	baseFont := object.Name(subsetTag(kept) + "+" + f.Name())

	// The program. /Length1 is the uncompressed length, which a reader needs to
	// know how much of a compressed stream is font data.
	programStream := &object.Stream{Dict: object.Dictionary{}, Data: program}
	programStream.Dict.Set("Length", object.Integer(len(program)))
	if f.IsCFF() {
		// FontFile3 carries a program whose format its /Subtype names; an
		// OpenType wrapper keeps the tables a reader may want beside the
		// outlines. /Length1 belongs to FontFile2 and is not written here.
		programStream.Dict.Set("Subtype", object.Name("OpenType"))
	} else {
		programStream.Dict.Set("Length1", object.Integer(len(program)))
	}
	programRef := doc.Add(programStream)

	// /CIDSet: one bit per CID, high bit of each byte first, set for the glyphs
	// the subset carries. It is mandatory now that /BaseFont is tagged as a
	// subset, and its contents are checked against the embedded program — so it
	// is written from the same kept set the subsetter used, not from the
	// original font.
	cidSet := &object.Stream{Dict: object.Dictionary{}, Data: cidSetBits(kept, f.NumGlyphs())}
	cidSet.Dict.Set("Length", object.Integer(len(cidSet.Data)))
	cidSetRef := doc.Add(cidSet)

	d := f.Descriptor()
	descriptor := &object.Dictionary{}
	descriptor.Set("Type", object.Name("FontDescriptor"))
	descriptor.Set("FontName", baseFont)
	descriptor.Set("Flags", object.Integer(d.Flags))
	descriptor.Set("FontBBox", f.bboxArray(d))
	descriptor.Set("ItalicAngle", object.Real(d.ItalicAngle))
	descriptor.Set("Ascent", object.Integer(int(f.scale(d.Ascent))))
	descriptor.Set("Descent", object.Integer(int(f.scale(d.Descent))))
	descriptor.Set("CapHeight", object.Integer(int(f.scale(d.CapHeight))))
	// StemV is estimated by the face from the weight the font declares; see its
	// documentation for why it cannot be measured there and why that is
	// acceptable. It is not scaled: the estimate is already in the thousandths
	// PDF states it in.
	descriptor.Set("StemV", object.Integer(d.StemV))
	if f.IsCFF() {
		descriptor.Set("FontFile3", programRef)
	} else {
		descriptor.Set("FontFile2", programRef)
	}
	// /CIDSet is required of a subset font whatever its outlines are, and both
	// kinds are subsets here — the /BaseFont tag says so.
	descriptor.Set("CIDSet", cidSetRef)
	descriptorRef := doc.Add(descriptor)

	advances := f.GlyphAdvances()
	defaultWidth := mostCommonWidth(advances)
	cidFont := &object.Dictionary{}
	cidFont.Set("Type", object.Name("Font"))
	if f.IsCFF() {
		cidFont.Set("Subtype", object.Name("CIDFontType0"))
	} else {
		cidFont.Set("Subtype", object.Name("CIDFontType2"))
	}
	cidFont.Set("BaseFont", baseFont)
	sysInfo := &object.Dictionary{}
	sysInfo.Set("Registry", object.String{Value: []byte("Adobe")})
	sysInfo.Set("Ordering", object.String{Value: []byte("Identity")})
	sysInfo.Set("Supplement", object.Integer(0))
	cidFont.Set("CIDSystemInfo", sysInfo)
	cidFont.Set("FontDescriptor", descriptorRef)
	cidFont.Set("DW", widthNumber(defaultWidth))
	if w := widthsArray(advances, defaultWidth); len(w) > 0 {
		cidFont.Set("W", w)
	}
	// Identity: a CID is a glyph index, which is what Identity-H encoding
	// already made the character codes. The key belongs to CIDFontType2 only —
	// for a CFF descendant the mapping is the font program's own business
	// (ISO 32000-2 9.7.4.2), and writing it there would be meaningless.
	if !f.IsCFF() {
		cidFont.Set("CIDToGIDMap", object.Name("Identity"))
	}
	cidFontRef := doc.Add(cidFont)

	toUnicode := &object.Stream{Dict: object.Dictionary{}, Data: f.toUnicodeCMap()}
	toUnicode.Dict.Set("Length", object.Integer(len(toUnicode.Data)))
	toUnicodeRef := doc.Add(toUnicode)

	fd := &object.Dictionary{}
	fd.Set("Type", object.Name("Font"))
	fd.Set("Subtype", object.Name("Type0"))
	fd.Set("BaseFont", baseFont)
	fd.Set("Encoding", object.Name("Identity-H"))
	fd.Set("DescendantFonts", object.Array{cidFontRef})
	fd.Set("ToUnicode", toUnicodeRef)
	return doc.Add(fd), nil
}

// embedSimple writes the font dictionary, descriptor and subsetted program for
// a simple font.
//
// The /Widths array is indexed by character code rather than by glyph, which is
// the difference that matters: the same numbers as a composite font's /W, keyed
// by the other of the two numberings. Both are written from the program's own
// metrics, because the validator checks them against it.
func (f *Face) embedSimple(doc Allocator) (object.IndirectRef, error) {
	program, kept, err := f.SubsetGlyphs()
	if err != nil {
		return object.IndirectRef{}, err
	}
	baseFont := object.Name(subsetTag(kept) + "+" + f.Name())

	programStream := &object.Stream{Dict: object.Dictionary{}, Data: program}
	programStream.Dict.Set("Length", object.Integer(len(program)))
	programStream.Dict.Set("Length1", object.Integer(len(program)))
	programRef := doc.Add(programStream)

	d := f.Descriptor()
	descriptor := &object.Dictionary{}
	descriptor.Set("Type", object.Name("FontDescriptor"))
	descriptor.Set("FontName", baseFont)
	// Nonsymbolic: the codes are characters in a standard encoding, which is
	// the whole premise of a simple font. Declaring it symbolic — which is what
	// the face itself reports, since a composite font's codes are glyph indices
	// and mean nothing outside it — would tell a reader to use the font's
	// built-in encoding and ignore /Encoding, which is how a document comes out
	// as the wrong glyphs entirely.
	flags := 1 << 5
	if d.Flags&flagFixedPitch != 0 {
		flags |= flagFixedPitch
	}
	if d.ItalicAngle != 0 {
		flags |= flagItalic
	}
	descriptor.Set("Flags", object.Integer(flags))
	descriptor.Set("FontBBox", f.bboxArray(d))
	descriptor.Set("ItalicAngle", object.Real(d.ItalicAngle))
	descriptor.Set("Ascent", object.Integer(int(f.scale(d.Ascent))))
	descriptor.Set("Descent", object.Integer(int(f.scale(d.Descent))))
	descriptor.Set("CapHeight", object.Integer(int(f.scale(d.CapHeight))))
	descriptor.Set("StemV", object.Integer(d.StemV))
	descriptor.Set("FontFile2", programRef)
	descriptorRef := doc.Add(descriptor)

	first, last, widths := f.simpleWidths()

	toUnicode := &object.Stream{Dict: object.Dictionary{}, Data: f.simpleToUnicode(first, last)}
	toUnicode.Dict.Set("Length", object.Integer(len(toUnicode.Data)))

	fd := &object.Dictionary{}
	fd.Set("Type", object.Name("Font"))
	fd.Set("Subtype", object.Name("TrueType"))
	fd.Set("BaseFont", baseFont)
	fd.Set("FirstChar", object.Integer(first))
	fd.Set("LastChar", object.Integer(last))
	fd.Set("Widths", widths)
	fd.Set("FontDescriptor", descriptorRef)
	fd.Set("Encoding", object.Name("WinAnsiEncoding"))
	fd.Set("ToUnicode", doc.Add(toUnicode))
	return doc.Add(fd), nil
}

// embedStandard writes the font dictionary for a standard face: a name the
// reader resolves, with the encoding the codes are in.
//
// There is no FontDescriptor and no font program. ISO 32000-2 9.6.2.2 permits
// both to be omitted for these fourteen, and writing a descriptor for a face
// whose outlines are not present would describe something this document does
// not contain.
func (f *Face) embedStandard(doc Allocator) (object.IndirectRef, error) {
	fd := &object.Dictionary{}
	fd.Set("Type", object.Name("Font"))
	fd.Set("Subtype", object.Name("Type1"))
	fd.Set("BaseFont", object.Name(f.Name()))
	// Symbol and ZapfDingbats have built-in encodings of their own; naming
	// WinAnsi for them would remap every glyph.
	if f.Name() != "Symbol" && f.Name() != "ZapfDingbats" {
		fd.Set("Encoding", object.Name("WinAnsiEncoding"))
	}
	return doc.Add(fd), nil
}

// The FontDescriptor flag bits this package sets by hand (ISO 32000-2 9.8.1,
// Table 121). The face computes the whole set for itself; these are the two a
// simple font's descriptor keeps when it discards the rest.
const (
	flagFixedPitch = 1 << 0
	flagItalic     = 1 << 6
)

// bboxArray writes the box enclosing every glyph, scaled to the thousandths of
// an em /FontBBox is stated in.
func (f *Face) bboxArray(d Descriptor) object.Array {
	return object.Array{
		object.Integer(int(f.scale(d.BBox[0]))), object.Integer(int(f.scale(d.BBox[1]))),
		object.Integer(int(f.scale(d.BBox[2]))), object.Integer(int(f.scale(d.BBox[3]))),
	}
}

// widthsArray builds /W from the program's own advances, in the
// consecutive-run form ISO 32000-2 9.7.4.3 defines.
//
// It is written from the embedded program's own metrics rather than from
// anything the caller supplies, because PDF/A checks the two against each
// other: a /W that disagrees with the program is a finding, and the only way to
// be sure they agree is to have one source.
func widthsArray(advances []float64, defaultWidth float64) object.Array {
	var out object.Array
	for gid := 0; gid < len(advances); {
		if advances[gid] == defaultWidth {
			gid++
			continue
		}
		start := gid
		var run object.Array
		for gid < len(advances) && advances[gid] != defaultWidth {
			run = append(run, widthNumber(advances[gid]))
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
func mostCommonWidth(advances []float64) float64 {
	counts := map[float64]int{}
	for _, w := range advances {
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

// simpleWidths builds the /Widths array and the code range it covers.
//
// The range is the whole encoding rather than only the codes used, because a
// /Widths array shorter than the codes a later edit might show is a document
// that is correct only by accident. It is 224 numbers.
func (f *Face) simpleWidths() (first, last int, widths object.Array) {
	first, last = 32, 255
	cmap := f.Cmap()
	advances := f.GlyphAdvances()
	widths = make(object.Array, 0, last-first+1)
	for code := first; code <= last; code++ {
		name := font.WinAnsiEncodingNames[byte(code)]
		w := 0.0
		if r, ok := font.GlyphNameToRune(name, byte(code)); ok {
			if gid, mapped := cmap[r]; mapped && gid < len(advances) {
				w = advances[gid]
			}
		}
		widths = append(widths, widthNumber(w))
	}
	return first, last, widths
}

// simpleToUnicode builds the CMap mapping each code to the character it stands
// for.
//
// A simple font's codes are already characters in a standard encoding, so a
// reader could work this out — but only one that knows the encoding. The CMap
// says it outright, which is what makes the text extractable by everything, and
// what PDF/A-2u and later require.
func (f *Face) simpleToUnicode(first, last int) []byte {
	cmap := f.Cmap()
	pairs := make([][2]int, 0, last-first+1)
	for code := first; code <= last; code++ {
		name := font.WinAnsiEncodingNames[byte(code)]
		r, ok := font.GlyphNameToRune(name, byte(code))
		if !ok || forbiddenInToUnicode(r) {
			continue
		}
		if gid, mapped := cmap[r]; !mapped || gid == 0 {
			continue // no glyph: nothing will ever show this code
		}
		pairs = append(pairs, [2]int{code, int(r)})
	}
	return buildToUnicodeCMap(pairs, "<00> <FF>")
}

// subsetTag is the six uppercase letters ISO 32000-2 9.6.4 requires in front of
// a subset font's name, as in "ABCDEF+Probe-Regular". A reader uses it to tell
// two subsets of the same face apart, so it must differ when the glyph sets do
// and match when they do not — which makes it a function of the kept glyphs
// rather than a random draw.
func subsetTag(kept []int) string {
	// FNV-1a over the kept indices: cheap, and deterministic, so the same
	// document produces the same file twice.
	var h uint64 = 14695981039346656037
	for _, gid := range kept {
		for shift := 0; shift < 32; shift += 8 {
			h ^= uint64(byte(gid >> shift))
			h *= 1099511628211
		}
	}
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = byte('A' + h%26)
		h /= 26
	}
	return string(tag)
}

// cidSetBits builds the /CIDSet bitmap: bit i, counting from the high bit of
// byte 0, is set when the subset carries glyph i.
//
// It is written from the kept set rather than by re-reading the emitted
// program, so that a disagreement between the two is a real defect this
// module's validator will report rather than something rediscovered here and
// silently papered over.
func cidSetBits(kept []int, numGlyphs int) []byte {
	bits := make([]byte, (numGlyphs+7)/8)
	for _, gid := range kept {
		if gid >= 0 && gid < numGlyphs {
			bits[gid/8] |= 0x80 >> (gid % 8)
		}
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
	cmap := f.Cmap()
	rev := make(map[int]rune, len(cmap))
	for r, gid := range cmap {
		if gid == 0 || forbiddenInToUnicode(r) {
			// U+0000, U+FEFF and U+FFFE are not text: mapping a glyph to one
			// says the character it represents is a byte-order mark or nothing
			// at all, and PDF/A reports it. A glyph whose only cmap entry is
			// one of these is left unmapped, which costs its extractability —
			// the alternative costs conformance, and an unextractable glyph is
			// the smaller loss.
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
	pairs := make([][2]int, 0, len(gids))
	for _, gid := range gids {
		pairs = append(pairs, [2]int{gid, int(rev[gid])})
	}
	return buildToUnicodeCMap(pairs, "<0000> <FFFF>")
}

// buildToUnicodeCMap writes a ToUnicode CMap over code-to-character pairs,
// which must be sorted by code.
//
// The codespace differs between the two font forms and is the caller's to
// state: a composite font's codes are two bytes and a simple font's are one,
// and a reader takes the range literally when splitting a shown string.
func buildToUnicodeCMap(pairs [][2]int, codespace string) []byte {
	var b bytes.Buffer
	b.WriteString(`/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
` + codespace + `
endcodespacerange
`)
	digits := 4
	if len(codespace) > 0 && codespace[0] == '<' && len(codespace) < 12 {
		digits = 2 // a single-byte codespace, written <00> <FF>
	}
	// bfchar sections are capped at 100 entries by the specification.
	for start := 0; start < len(pairs); start += 100 {
		end := start + 100
		if end > len(pairs) {
			end = len(pairs)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, p := range pairs[start:end] {
			fmt.Fprintf(&b, "<%0*X> <%s>\n", digits, p[0], utf16beHex(rune(p[1])))
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

// forbiddenInToUnicode reports the code points a ToUnicode CMap may not map to
// (ISO 19005-4 6.2.10.7 and its predecessors). They are not characters: two are
// byte-order marks and one is the null.
func forbiddenInToUnicode(r rune) bool {
	return r == 0 || r == 0xFEFF || r == 0xFFFE
}
