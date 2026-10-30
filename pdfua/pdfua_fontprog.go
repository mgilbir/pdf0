package pdfua

import (
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/fontcheck"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the PDF/UA font rules that compare a font dictionary with
// its embedded program, over the machinery the PDF/A engine shares
// (internal/fontcheck).

// checkUAFontWidths flags a font whose dictionary declares, for a glyph it
// shows, a width that differs from the embedded program's by more than one
// unit in 1000 (ISO 14289-1 7.21.5, ISO 14289-2 8.4.5.6). The per-glyph
// comparison is fontcheck's — Simple.WidthDiffers, CID.WidthDiffers,
// Type3WidthDiffers — the one PDF/A's 6.2.11.5 makes.
//
// veraPDF's test, for PDF/UA-1 and -2 alike, passes a glyph shown in rendering
// mode 3, one with no width from the program or none from the dictionary, and
// one whose two widths differ by at most 1: only invisible text (mode 3) is
// exempt. Clip-only text (mode 7) is held to it, as the PDF/A engine holds it.
//
// veraPDF judges each glyph in the mode it was shown in. pdf0 records the
// modes per font (core.FontTextUsage.Modes), not per string, so a font is
// exempt only when it shows text in mode 3 alone; a font that shows some text
// in mode 3 and some in another has all its glyphs compared.
//
// The comparison per glyph is shared; the walk over the shown glyphs is this
// rule's, because its rendering-mode gate is.
func checkUAFontWidths(d core.View) []Violation {
	var v []Violation
	for fontDict, u := range core.CollectFontTextUsage(d) {
		if onlyInvisible(u) {
			continue
		}
		if subtype, differs := uaWidthDiffers(d, fontDict, u); differs {
			v = append(v, Violation{Clause: "7.21.5", Message: "width information for glyphs used for rendering is inconsistent between the " + string(subtype) + " font dictionary and its embedded program", Object: d.ObjNumOf(fontDict)})
		}
	}
	return v
}

// uaWidthDiffers reports whether some glyph the font shows has a declared
// width differing from its program's by more than fontcheck.WidthTolerance,
// and the subtype to name the font by — the descendant's for a Type 0 font. A
// font whose program cannot be read has no program width, and is not reported.
func uaWidthDiffers(d core.View, fontDict *object.Dictionary, u *core.FontTextUsage) (object.Name, bool) {
	subtype, _ := d.ResolveName(fontDict.Get("Subtype"))
	switch subtype {
	case "Type3":
		return subtype, fontcheck.Type3WidthDiffers(d, fontDict, u)
	case "Type0":
		c, _, _ := fontcheck.LoadCID(d, fontDict) // reason: a program that cannot be read has no width to compare; LoadFontProgram recorded any declined trip
		if c == nil {
			return subtype, false
		}
		// The answer is the CID's, so each is asked once: a long document
		// shows the same few thousand CIDs over and over. Every code the
		// CMap maps to no CID has the same answer too.
		asked := map[int]bool{}
		askedUnmapped := false
		for _, s := range u.Strings {
			for g := range c.Shown(s) {
				if g.Code.Mapped {
					if asked[g.Code.CID] {
						continue
					}
					asked[g.Code.CID] = true
				} else {
					if askedUnmapped {
						continue
					}
					askedUnmapped = true
				}
				if c.WidthDiffers(g) {
					return c.Subtype, true
				}
			}
		}
		return c.Subtype, false
	}
	s, _, _ := fontcheck.LoadSimple(d, fontDict) // reason: as for LoadCID above
	if s == nil {
		return subtype, false
	}
	var asked [256]bool // the answer is the code's, so each is asked once
	for _, str := range u.Strings {
		d.Charge(len(str))
		for _, code := range str {
			if asked[code] {
				continue
			}
			asked[code] = true
			if s.WidthDiffers(d, code) {
				return subtype, true
			}
		}
	}
	return subtype, false
}

// onlyInvisible reports whether every piece of text the font showed was in
// rendering mode 3, invisible. A font with no recorded mode shows in the
// default, mode 0.
func onlyInvisible(u *core.FontTextUsage) bool {
	if len(u.Modes) == 0 {
		return false
	}
	for m := range u.Modes {
		if m != 3 {
			return false
		}
	}
	return true
}

// checkUAGlyphsPresent is ISO 14289-1 7.21.4.1 (veraPDF 7.21.4.1-2; ISO
// 14289-2 8.4.5.5.1): an embedded simple font defines every glyph shown
// with it, text in rendering mode 3 alone aside. A glyph's presence is
// fontcheck.Simple.Glyph's, the reading PDF/A's glyph rule makes; a font
// whose program cannot be read, or a code it cannot be asked about, is not
// judged. Type 3 glyphs are procedures, and a Type 0 font's are its CIDs'
// (checkUANotdefCID).
func checkUAGlyphsPresent(d core.View) []Violation {
	var v []Violation
	for fontDict, u := range core.CollectFontTextUsage(d) {
		if onlyInvisible(u) {
			continue
		}
		switch st, _ := d.ResolveName(fontDict.Get("Subtype")); st {
		case "Type1", "MMType1", "TrueType":
		default:
			continue
		}
		sf, _, _ := fontcheck.LoadSimple(d, fontDict) // reason: a program that cannot be read is not judged; LoadFontProgram recorded any declined trip
		if sf == nil {
			continue
		}
	codes:
		for _, s := range u.Strings {
			for _, code := range s {
				d.Charge(1)
				if g := sf.Glyph(code); g.Known && !g.Present {
					v = append(v, Violation{Clause: "7.21.4.1", Message: fmt.Sprintf("the embedded %s font does not define a glyph shown with it (code %d)", sf.Subtype, code), Object: d.ObjNumOf(fontDict)})
					break codes
				}
			}
		}
	}
	return v
}

// checkUATrueTypeCmaps is the cmap half of ISO 14289-1 7.21.6 (veraPDF
// 7.21.6-1, -4) and ISO 14289-2 8.4.5.7 (-1, -4): the subtables a TrueType
// program's cmap must have, which are PDF/A-2's at part 1 and PDF/A-4's at
// part 2 (fontcheck.CmapRules).
func checkUATrueTypeCmaps(d core.View, part string) []Violation {
	rules := fontcheck.CmapOneOr30
	if part == "2" {
		rules = fontcheck.Cmap30Or10
	}
	var v []Violation
	for fontDict := range core.CollectFontTextUsage(d) {
		if st, _ := d.ResolveName(fontDict.Get("Subtype")); st != "TrueType" {
			continue
		}
		fd := d.ResolveDict(fontDict.Get("FontDescriptor"))
		if fd == nil {
			continue
		}
		msg := fontcheck.NonSymbolicCmapProblem(d, fd, rules)
		if fontcheck.Symbolic(d, fd) {
			msg = fontcheck.SymbolicCmapProblem(d, fd, rules)
		}
		if msg != "" {
			v = append(v, Violation{Clause: "7.21.6", Message: msg, Object: d.ObjNumOf(fontDict)})
		}
	}
	return v
}

// checkUASimpleCharMapping is ISO 14289-1 7.21.7 (veraPDF 7.21.7-1; ISO
// 14289-2 8.4.5.8) for simple fonts: every code shown, in any rendering
// mode, maps to Unicode — through the font's ToUnicode CMap, or through the
// glyph name its encoding gives it, a name of the Adobe Glyph List or one in
// the uniXXXX or uXXXX form. A TrueType font whose codes select glyphs by
// code (fontcheck.Simple.ByCode: symbolic, or no /Encoding) names none, and
// needs the CMap. A code with no name under an encoding pdf0 does not model —
// a Type 1 program's own — is not evidence either way.
func checkUASimpleCharMapping(d core.View) []Violation {
	var v []Violation
	for fontDict, u := range core.CollectFontTextUsage(d) {
		st, _ := d.ResolveName(fontDict.Get("Subtype"))
		switch st {
		case "Type1", "MMType1", "TrueType", "Type3":
		default:
			continue
		}
		toUni, _ := core.ParseToUnicode(d, fontDict) // reason: a CMap not read is not evidence; the producer recorded any declined trip
		if fontDict.Get("ToUnicode") != nil && toUni == nil {
			continue
		}
		fd := d.ResolveDict(fontDict.Get("FontDescriptor"))
		symbolic := fd != nil && fontcheck.Symbolic(d, fd)
		byCode := st == "TrueType" && (symbolic || fontDict.Get("Encoding") == nil)
		enc := fontcheck.SimpleFontCodeToName(d, fontDict, symbolic && st != "Type3")
		modelled := st == "Type3" || fontcheck.BaseEncodingModelled(d, fontDict, symbolic)
	codes:
		for _, s := range u.Strings {
			for _, code := range s {
				d.Charge(1)
				if toUni != nil {
					if _, ok := toUni.Runes(int(code)); ok {
						continue
					}
				}
				name := enc[code]
				switch {
				case byCode:
				case name == "" && !modelled:
					continue // the program's own encoding names it; not read
				case nameHasUnicode(name):
					continue
				}
				v = append(v, Violation{Clause: "7.21.7", Message: fmt.Sprintf("a code shown with the %s font (%d) maps to no Unicode value: no ToUnicode entry, and no glyph name with one", st, code), Object: d.ObjNumOf(fontDict)})
				break codes
			}
		}
	}
	return v
}

// nameHasUnicode reports whether a glyph name gives a Unicode value: a name
// of the Adobe Glyph List, or one in the uniXXXX or uXXXX[XX] form.
func nameHasUnicode(name string) bool {
	if name == "" || name == ".notdef" {
		return false
	}
	if fontcheck.AGLListed(name) {
		return true
	}
	hex := func(s string) bool {
		for _, c := range s {
			if !('0' <= c && c <= '9' || 'A' <= c && c <= 'F') {
				return false
			}
		}
		return true
	}
	switch {
	case len(name) == 7 && name[:3] == "uni":
		return hex(name[3:])
	case len(name) >= 5 && len(name) <= 7 && name[0] == 'u':
		return hex(name[1:])
	}
	return false
}
