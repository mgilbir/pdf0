package fonts

import (
	"fmt"
	"sort"

	"github.com/mgilbir/pdf0/internal/font"
	"github.com/mgilbir/pdf0/object"
)

// The fourteen standard fonts: the faces a PDF reader is required to have, so
// that a document may name one and embed nothing.
//
// They are the reason this package has two kinds of face. An embedded face is
// described by its program — the metrics, the glyph coverage and the outlines
// all come out of the same bytes. A standard face has no program at all: it is
// a name the reader resolves, and the metrics are the ones Adobe published,
// which is why they are compiled in.
//
// # When to use one, and when not
//
// A standard face costs nothing in file size and needs no font to be available
// at build time, which makes it the right choice for a plain PDF whose text is
// Latin. It is the wrong choice for two reasons that matter:
//
//   - PDF/A forbids it. Every font a conforming document shows must be
//     embedded, precisely so that the file renders the same in fifty years as
//     it does today; this module's own validator reports such a page under
//     clause 6.2.11.4.1, as a font with no /FontDescriptor — which is the
//     mechanism, since a descriptor is where a program hangs. Embed a real face
//     for anything that must conform.
//   - The coverage is WinAnsiEncoding: 224 characters of Latin. Anything
//     outside it — a dash of Greek, a Chinese name, an emoji — has no glyph.
//
// Encode reports how many characters fell outside, so a caller can find out
// rather than discover it in the rendered page.

// StandardNames lists the fourteen faces, as a document names them.
func StandardNames() []string {
	out := make([]string, 0, len(standard14))
	for name := range standard14 {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Standard returns a face for one of the fourteen standard fonts, by the name a
// PDF uses for it: "Helvetica", "Times-Roman", "Courier-Bold" and so on.
// StandardNames lists them all.
//
// The face carries metrics and no program. It measures text and it embeds as a
// reference rather than a font, which is what makes it free and what makes it
// unusable in a conforming PDF/A.
func Standard(name string) (*Face, error) {
	m, ok := standard14[name]
	if !ok {
		return nil, fmt.Errorf("fonts: %q is not one of the fourteen standard fonts; see StandardNames", name)
	}
	f := &Face{
		name:       name,
		std:        m,
		unitsPerEm: 1000, // the standard faces are defined in 1/1000 em
		ascent:     m.ascent,
		descent:    m.descent,
		capHeight:  m.capHeight,
		bbox:       m.bbox,
		italic:     m.italic,
		used:       map[int]bool{},
		layout:     &layout{kern: map[[2]int]int{}, ligatures: map[int][]ligature{}, glyphClass: map[int]int{}, single: map[string]map[int]int{}},
	}
	f.stemV = stemV(nil)
	// Nonsymbolic, because the codes are WinAnsi characters rather than glyph
	// indices — the opposite of an embedded Identity-H face. Symbol and
	// ZapfDingbats carry their own encodings and are symbolic.
	if name == "Symbol" || name == "ZapfDingbats" {
		f.flags = 1 << 2
	} else {
		f.flags = 1 << 5
	}
	if m.fixedPitch {
		f.flags |= 1
	}
	if f.italic != 0 {
		f.flags |= 1 << 6
	}
	return f, nil
}

// IsStandard reports whether the face is one of the fourteen rather than an
// embedded program. A caller that must produce a conforming PDF/A can check it
// before drawing rather than after validating.
func (f *Face) IsStandard() bool { return f.std != nil }

// winAnsi maps a character to its WinAnsiEncoding byte and to the glyph name
// that byte stands for.
//
// The two are needed together: the code is what goes into the content stream
// and the name is what the metrics are keyed by, because a standard font's
// widths are published per glyph name and not per code.
//
// It is built once rather than searched per character. Measuring a paragraph
// asks this question for every character in it, and a scan of the encoding for
// each would make laying out a page quadratic in a way that only shows up on
// long documents.
var winAnsi = func() map[rune]struct {
	code byte
	name string
} {
	out := make(map[rune]struct {
		code byte
		name string
	}, len(font.WinAnsiEncodingNames))
	for code, name := range font.WinAnsiEncodingNames {
		r, ok := font.GlyphNameToRune(name, code)
		if !ok {
			continue
		}
		// Lower codes win where two name the same character, so the mapping is
		// deterministic rather than dependent on map iteration order.
		if prev, seen := out[r]; seen && prev.code <= code {
			continue
		}
		out[r] = struct {
			code byte
			name string
		}{code, name}
	}
	return out
}()

func stdCode(r rune) (byte, string, bool) {
	e, ok := winAnsi[r]
	return e.code, e.name, ok
}

// stdAdvance is the advance of a rune in a standard face, in 1/1000 em.
func (f *Face) stdAdvance(r rune) (float64, bool) {
	_, name, ok := stdCode(r)
	if !ok {
		return 0, false
	}
	w, ok := f.std.widths[name]
	if !ok {
		return 0, false
	}
	return float64(w), true
}

// embedStandard writes the font dictionary for a standard face: a name the
// reader resolves, with the encoding the codes are in.
//
// There is no FontDescriptor and no font program. ISO 32000-2 9.6.2.2 permits
// both to be omitted for these fourteen, and writing a descriptor for a face
// whose outlines are not present would describe something this document does
// not contain.
func (f *Face) embedStandard(doc Allocator) (object.IndirectRef, error) {
	d := &object.Dictionary{}
	d.Set("Type", object.Name("Font"))
	d.Set("Subtype", object.Name("Type1"))
	d.Set("BaseFont", object.Name(f.name))
	// Symbol and ZapfDingbats have built-in encodings of their own; naming
	// WinAnsi for them would remap every glyph.
	if f.name != "Symbol" && f.name != "ZapfDingbats" {
		d.Set("Encoding", object.Name("WinAnsiEncoding"))
	}
	return doc.Add(d), nil
}
