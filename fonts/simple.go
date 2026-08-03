package fonts

import (
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/internal/font"
	"github.com/mgilbir/pdf0/object"
)

// Embedding a font program as a simple font: one byte per character, through a
// standard encoding, rather than as a composite font keyed by glyph index.
//
// # Why both forms exist
//
// A composite font is the general answer — any character, any script, as many
// glyphs as the face has. A simple font is the narrow one: 256 codes, drawn
// from an encoding of Latin characters. Where the narrow one fits it is
// smaller in the file and simpler in the stream, because the codes *are* the
// text: a byte of a content stream set in a simple font is a character, which
// is why such a document is searchable by readers that do not consult a
// ToUnicode CMap at all.
//
// Where it does not fit, it does not fit at all. A document with a Greek word,
// a Chinese name or an em dash outside WinAnsiEncoding cannot be set this way,
// and Encode reports how many characters fell outside rather than quietly
// substituting something.
//
// The choice is the caller's and is made once, at load: LoadSimple for this
// form, Load for the composite one. It cannot be changed afterwards because
// Encode's output means different things in the two, and a face that had
// produced codes of one kind cannot honestly produce the other.

// LoadSimple parses an sfnt font program and prepares it to be embedded as a
// simple font with WinAnsiEncoding.
//
// It refuses a program that cannot serve as one: a face is only usable here if
// its character map covers the encoding, and one that covers almost none of it
// would produce a document of blanks.
func LoadSimple(data []byte) (*Face, error) {
	f, err := Load(data)
	if err != nil {
		return nil, err
	}
	if f.cff {
		// A CFF program embeds as FontFile3, and a simple font with CFF
		// outlines is a Type1C font whose encoding lives inside the program.
		// Refusing is better than writing a TrueType font dictionary around it.
		return nil, errors.New("fonts: CFF programs are not embedded as simple fonts; use Load")
	}
	covered := 0
	for r := range winAnsi {
		if _, ok := f.prog.Cmap[r]; ok {
			covered++
		}
	}
	if covered < 32 {
		return nil, fmt.Errorf("fonts: the font maps only %d of the %d characters WinAnsiEncoding names; it cannot be embedded as a simple font",
			covered, len(winAnsi))
	}
	f.simple = true
	return f, nil
}

// IsSimple reports whether the face will be embedded as a simple font — one
// byte per character — rather than as a composite one.
func (f *Face) IsSimple() bool { return f.simple }

// encodeSimple maps a string to single-byte WinAnsi codes, recording the glyphs
// those codes will draw so the subsetter keeps them.
func (f *Face) encodeSimple(s string) (codes []byte, missing int) {
	codes = make([]byte, 0, len(s))
	for _, r := range s {
		code, _, ok := stdCode(r)
		if !ok {
			missing++
			continue // outside the encoding: there is no byte that means it
		}
		gid, mapped := f.prog.Cmap[r]
		if !mapped || gid == 0 {
			missing++
			continue // the encoding has a code but this face has no glyph
		}
		f.used[gid] = true
		codes = append(codes, code)
	}
	return codes, missing
}

// embedSimple writes the font dictionary, descriptor and subsetted program for
// a simple font.
//
// The /Widths array is indexed by character code rather than by glyph, which is
// the difference that matters: the same numbers as a composite font's /W, keyed
// by the other of the two numberings. Both are written from the program's own
// metrics, because the validator checks them against it.
func (f *Face) embedSimple(doc Allocator) (object.IndirectRef, error) {
	program, kept, err := f.subset()
	if err != nil {
		return object.IndirectRef{}, err
	}
	baseFont := object.Name(subsetTag(kept) + "+" + f.name)

	programStream := &object.Stream{Dict: object.Dictionary{}, Data: program}
	programStream.Dict.Set("Length", object.Integer(len(program)))
	programStream.Dict.Set("Length1", object.Integer(len(program)))
	programRef := doc.Add(programStream)

	descriptor := &object.Dictionary{}
	descriptor.Set("Type", object.Name("FontDescriptor"))
	descriptor.Set("FontName", baseFont)
	// Nonsymbolic: the codes are characters in a standard encoding, which is
	// the whole premise of a simple font. Declaring it symbolic would tell a
	// reader to use the font's built-in encoding and ignore /Encoding, which is
	// how a document comes out as the wrong glyphs entirely.
	flags := 1 << 5
	if isFixedPitch(f.prog) {
		flags |= 1
	}
	if f.italic != 0 {
		flags |= 1 << 6
	}
	descriptor.Set("Flags", object.Integer(flags))
	descriptor.Set("FontBBox", object.Array{
		object.Integer(int(f.scale(f.bbox[0]))), object.Integer(int(f.scale(f.bbox[1]))),
		object.Integer(int(f.scale(f.bbox[2]))), object.Integer(int(f.scale(f.bbox[3]))),
	})
	descriptor.Set("ItalicAngle", object.Real(f.italic))
	descriptor.Set("Ascent", object.Integer(int(f.scale(f.ascent))))
	descriptor.Set("Descent", object.Integer(int(f.scale(f.descent))))
	descriptor.Set("CapHeight", object.Integer(int(f.scale(f.capHeight))))
	descriptor.Set("StemV", object.Integer(f.stemV))
	descriptor.Set("FontFile2", programRef)
	descriptorRef := doc.Add(descriptor)

	first, last, widths := f.simpleWidths()

	toUnicode := &object.Stream{Dict: object.Dictionary{}, Data: f.simpleToUnicode(first, last)}
	toUnicode.Dict.Set("Length", object.Integer(len(toUnicode.Data)))

	d := &object.Dictionary{}
	d.Set("Type", object.Name("Font"))
	d.Set("Subtype", object.Name("TrueType"))
	d.Set("BaseFont", baseFont)
	d.Set("FirstChar", object.Integer(first))
	d.Set("LastChar", object.Integer(last))
	d.Set("Widths", widths)
	d.Set("FontDescriptor", descriptorRef)
	d.Set("Encoding", object.Name("WinAnsiEncoding"))
	d.Set("ToUnicode", doc.Add(toUnicode))
	return doc.Add(d), nil
}

// simpleWidths builds the /Widths array and the code range it covers.
//
// The range is the whole encoding rather than only the codes used, because a
// /Widths array shorter than the codes a later edit might show is a document
// that is correct only by accident. It is 224 numbers.
func (f *Face) simpleWidths() (first, last int, widths object.Array) {
	first, last = 32, 255
	widths = make(object.Array, 0, last-first+1)
	for code := first; code <= last; code++ {
		name := font.WinAnsiEncodingNames[byte(code)]
		w := 0.0
		if r, ok := font.GlyphNameToRune(name, byte(code)); ok {
			if gid, mapped := f.prog.Cmap[r]; mapped && gid < len(f.prog.WidthByGID) {
				w = f.prog.WidthByGID[gid]
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
	pairs := make([][2]int, 0, last-first+1)
	for code := first; code <= last; code++ {
		name := font.WinAnsiEncodingNames[byte(code)]
		r, ok := font.GlyphNameToRune(name, byte(code))
		if !ok || forbiddenInToUnicode(r) {
			continue
		}
		if gid, mapped := f.prog.Cmap[r]; !mapped || gid == 0 {
			continue // no glyph: nothing will ever show this code
		}
		pairs = append(pairs, [2]int{code, int(r)})
	}
	return buildToUnicodeCMap(pairs, "<00> <FF>")
}
