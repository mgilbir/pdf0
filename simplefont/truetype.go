package simplefont

import "github.com/mgilbir/forme/font"

// TrueTypeGlyph maps a character code of a simple TrueType font to a glyph
// index using the font program's cmap subtables, following ISO 32000-2
// 9.6.6.4. symbolic is the font descriptor's Symbolic flag; name is the glyph
// name the font's encoding gives code ("" when it gives none).
//
// # The second result, and why glyph 0 is not the same as "no"
//
// It says whether the font *answered*, not whether it found a glyph. The three
// outcomes are distinct and a caller that collapses any two of them will report
// a fault that is not there or miss one that is:
//
//	(g, true)  with g != 0  the code is this glyph
//	(0, true)               the code is .notdef — the font was asked and its
//	                        answer is that this code has no glyph of its own
//	(0, false)              the font could not be asked: it carries no cmap
//	                        subtable this rule knows how to read
//
// The middle one is a real mapping. 9.6.6.4 says in as many words that a
// non-symbolic code with no name renders .notdef, and a named code absent from
// the (3,1) cmap maps to no glyph — both are the font's own answer, and both
// come back as glyph 0 because glyph 0 *is* .notdef. Only the third is
// ignorance, and a rule may not assert against a font it could not ask.
//
// Testing `g == 0` therefore merges "this font does not have that character"
// with "this reader does not understand this font", which are a finding and the
// absence of one.
//
// A cmap the parser read only in part (fp.CmapPartial) answers for the codes it
// holds, and a code it does not hold comes back (0, true) like any absent one:
// a caller must check CmapPartial before reading that answer as the font's.
func TrueTypeGlyph(fp *font.Program, symbolic bool, code byte, name string) (gid int, answered bool) {
	if symbolic {
		if fp.SymbolCmap != nil {
			if gid, ok := fp.SymbolCmap[0xF000|uint16(code)]; ok {
				return gid, true
			}
			if gid, ok := fp.SymbolCmap[uint16(code)]; ok {
				return gid, true
			}
		}
		if fp.MacCmap != nil {
			if gid, ok := fp.MacCmap[code]; ok {
				return gid, true
			}
		}
		return 0, false
	}
	// A non-symbolic code with no glyph name (undefined in the Encoding)
	// renders the .notdef glyph (ISO 32000-1 9.6.6.4).
	if name == "" {
		return 0, true
	}
	if fp.Cmap != nil {
		if r, ok := font.GlyphNameToRune(name, code); ok {
			if gid, ok := fp.Cmap[r]; ok {
				return gid, true
			}
		}
		// A named code absent from the (3,1) cmap maps to no glyph.
		return 0, true
	}
	if fp.MacCmap != nil {
		if gid, ok := fp.MacCmap[code]; ok {
			return gid, true
		}
	}
	if fp.SymbolCmap != nil {
		if gid, ok := fp.SymbolCmap[0xF000|uint16(code)]; ok {
			return gid, true
		}
	}
	return 0, false
}
