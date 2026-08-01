package font

import "strings"

// Character-code to glyph-index resolution (ISO 32000-1 9.6.6.4). This is font
// logic rather than validation logic — it reads only the font program's cmap
// subtables and the glyph name — so it lives with the parser that produces them.

// trueTypeGID maps a character code to a glyph index using the font's cmap
// subtables, following ISO 32000-1, 9.6.6.4.
func TrueTypeGID(fp *Program, symbolic bool, code byte, name string) (int, bool) {
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
		if r, ok := GlyphNameToRune(name, code); ok {
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

// GlyphNameToRune maps a glyph name to a Unicode code point for the common
// cases needed by TrueType (3,1) cmap lookup: the uniXXXX/uXXXX conventions
// and the ASCII range, where the standard Latin encodings are identity.
func GlyphNameToRune(name string, code byte) (rune, bool) {
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		if v, ok := ParseHexN(name[3:]); ok {
			return rune(v), true
		}
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, ok := ParseHexN(name[1:]); ok {
			return rune(v), true
		}
	}
	// ASCII and Latin-1 high range: the standard Latin encodings are
	// identity there (0x80-0x9F differ, but those are rare in practice and
	// handled by the uni/u name forms above).
	if (code >= 0x20 && code <= 0x7E) || code >= 0xA0 {
		return rune(code), true
	}
	return 0, false
}
func ParseHexN(s string) (int, bool) {
	v := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | int(c-'0')
		case c >= 'A' && c <= 'F':
			v = v<<4 | int(c-'A'+10)
		case c >= 'a' && c <= 'f':
			v = v<<4 | int(c-'a'+10)
		default:
			return 0, false
		}
	}
	return v, true
}
