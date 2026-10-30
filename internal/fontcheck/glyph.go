package fontcheck

import (
	"fmt"
	"slices"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/simplefont"
)

// GlyphFacts is what a simple font's program says of the glyph a shown code
// draws, as the glyph rules of PDF/A and PDF/UA read it.
type GlyphFacts struct {
	// Known is false when nothing may be asserted of the code: a Type 1 or
	// CFF code with no glyph name under an encoding pdf0 does not model
	// (the name is the program's built-in encoding's, which is not read),
	// or a TrueType code when the cmap was read only in part.
	Known bool
	// Present is whether the program defines the glyph.
	Present bool
	// Notdef is whether the code draws .notdef: by name, or as TrueType
	// glyph 0. It is false where the cmap was read only in part, and for a
	// name a ByName program's post table does not give, which is a glyph
	// the program lacks.
	Notdef bool
}

// Glyph is the facts for code.
//
// A code a modelled encoding leaves unnamed selects .notdef (ISO 32000-1
// 9.6.6.1), the glyph the program carries for it: a .notdef reference, not
// a glyph the program lacks; veraPDF reads it so (PDF/A-2b
// 6-2-11-8-t01-fail-a, code 0 under /WinAnsiEncoding). A TrueType code's
// glyph is the cmap's (simplefont.TrueTypeGlyph), by code where ByCode, and
// the post table's by name where ByName.
func (s *Simple) Glyph(code byte) GlyphFacts {
	fp := s.Program
	name := s.Encoding[code]
	if name == "" && s.Subtype != "TrueType" && s.EncodingModelled {
		name = ".notdef"
	}
	present := HasGlyph(fp, s.Subtype, s.ByCode, code, name)
	byName := s.ByName && name != ""
	if byName {
		gid, named := s.NamedGlyph(name)
		present = named && gid < fp.NumGlyphs
	}
	// A named code the Unicode cmap has no entry for draws glyph 0, which
	// simplefont.TrueTypeGlyph gives as the font's answer; but it is a
	// glyph the program lacks, not a .notdef reference, as veraPDF reads it
	// at every PDF/A level and in PDF/UA (single-defect files
	// truetype-code-outside-the-cmap, truetype-glyph-missing). An unnamed
	// code is .notdef's (PDF_A-4 6-2-10-4-1-t02-fail-c).
	absent := false
	if s.Subtype == "TrueType" && !s.ByCode && !byName && name != "" && name != ".notdef" && fp.Cmap != nil {
		if r, ok := font.GlyphNameToRune(name, code); ok {
			if _, in := fp.Cmap[r]; !in {
				absent, present = true, false
			}
		}
	}
	partial := fp.CmapPartial && s.Subtype == "TrueType"
	return GlyphFacts{
		// A Type 1/CFF code with no name under an encoding pdf0 does not
		// model has its name from the program's built-in encoding; saying
		// it is missing asserted a fact not in evidence (PDF_A-1a
		// 6-3-8-t01-pass-b, -pass-e). A cmap the work budget truncated
		// resolves an unread code to glyph 0 (audit C46).
		Known:   (s.Subtype == "TrueType" || name != "" || s.EncodingModelled) && !partial,
		Present: present,
		Notdef:  !partial && !byName && !absent && IsNotdef(fp, s.Subtype, s.ByCode, code, name),
	}
}

// HasGlyph and IsNotdef read a simple font's code against its program: a
// TrueType code through the cmap, by code when byCode (Simple.ByCode), by
// glyph name otherwise; any other by the glyph name. Glyph 0 is the
// program's .notdef: present, and a .notdef reference, not a glyph the
// program lacks (veraPDF: PDF_A-4 6-2-10-4-1-t02-fail-c).
func HasGlyph(fp *font.Program, subtype object.Name, byCode bool, code byte, name string) bool {
	if subtype == "TrueType" {
		gid, ok := simplefont.TrueTypeGlyph(fp, byCode, code, name)
		return ok && gid < fp.NumGlyphs
	}
	if name == "" {
		return false
	}
	return fp.GlyphNames[name]
}

// IsNotdef: see HasGlyph.
func IsNotdef(fp *font.Program, subtype object.Name, byCode bool, code byte, name string) bool {
	if name == ".notdef" {
		return true
	}
	if subtype == "TrueType" {
		gid, ok := simplefont.TrueTypeGlyph(fp, byCode, code, name)
		return ok && gid == 0
	}
	return false
}

// CmapRules is an edition's rules on a TrueType program's cmap subtables.
type CmapRules int

const (
	// CmapExactlyOne is ISO 19005-1 6.3.7: a symbolic font's cmap shall
	// "contain exactly one encoding"; nothing of a non-symbolic one's.
	CmapExactlyOne CmapRules = iota + 1
	// CmapOneOr30 is ISO 19005-2/-3 6.2.11.6 and ISO 14289-1 7.21.6: a
	// symbolic font's cmap has exactly one encoding or the Microsoft Symbol
	// (3,0) one; a non-symbolic font's has one other than (3,0) (veraPDF
	// 6.2.11.6-1, 7.21.6-1).
	CmapOneOr30
	// Cmap30Or10 is ISO 19005-4 6.2.10.6 and ISO 14289-2 8.4.5.7: a symbolic
	// font's cmap has the Microsoft Symbol (3,0) or Mac Roman (1,0)
	// encoding; a non-symbolic font's the Microsoft Unicode (3,1) or Mac
	// Roman (1,0) one.
	Cmap30Or10
)

// SymbolicCmapProblem says what is wrong with a symbolic TrueType font's
// cmap under rules, or "" when nothing is. The records are CmapEncodings';
// a program pdf0 could not read, or one with no cmap records at all, is not
// judged.
func SymbolicCmapProblem(doc core.View, fd *object.Dictionary, rules CmapRules) string {
	encs, known := CmapEncodings(doc, fd)
	if !known || len(encs) == 0 {
		return ""
	}
	has := func(platform, encoding int) bool { return slices.Contains(encs, CmapEncoding{platform, encoding}) }
	switch rules {
	case CmapExactlyOne:
		if len(encs) != 1 {
			return fmt.Sprintf("symbolic TrueType font must have exactly one cmap subtable, found %d", len(encs))
		}
	case Cmap30Or10:
		if !has(3, 0) && !has(1, 0) {
			return "symbolic TrueType font's cmap must have a Microsoft Symbol (3,0) or Mac Roman (1,0) subtable"
		}
	case CmapOneOr30:
		if len(encs) != 1 && !has(3, 0) {
			return fmt.Sprintf("symbolic TrueType font's cmap must have exactly one subtable or a Microsoft Symbol (3,0) subtable; it has %d and no (3,0)", len(encs))
		}
	}
	return ""
}

// NonSymbolicCmapProblem says what is wrong with a non-symbolic TrueType
// font's cmap under rules, or "" when nothing is; a program pdf0 could not
// read is not judged.
func NonSymbolicCmapProblem(doc core.View, fd *object.Dictionary, rules CmapRules) string {
	if rules == CmapExactlyOne {
		return ""
	}
	encs, known := CmapEncodings(doc, fd)
	if !known {
		return ""
	}
	has := func(platform, encoding int) bool { return slices.Contains(encs, CmapEncoding{platform, encoding}) }
	if rules == Cmap30Or10 {
		if !has(3, 1) && !has(1, 0) {
			return "non-symbolic TrueType font's cmap must have a Microsoft Unicode (3,1) or Mac Roman (1,0) subtable"
		}
		return ""
	}
	if n := len(encs); n == 0 || n == 1 && has(3, 0) {
		return "non-symbolic TrueType font's cmap has no subtable but Microsoft Symbol (3,0)"
	}
	return ""
}
