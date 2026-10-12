package pdfua

import (
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
// exempt. Clip-only text (mode 7) is held to it, where the PDF/A engine's gate
// exempts both.
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
		// shows the same few thousand CIDs over and over.
		asked := map[int]bool{}
		for _, s := range u.Strings {
			for g := range c.Shown(s) {
				if !g.Code.Mapped || asked[g.Code.CID] {
					continue
				}
				asked[g.Code.CID] = true
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
