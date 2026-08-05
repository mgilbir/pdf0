package pdfa

import (
	"fmt"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file implements the Level A Unicode-mapping requirement (ISO 19005-1
// 6.3.8, -2/-3 6.2.11.7.2): a font used to show text must let a consumer
// recover the Unicode value of every character it shows. A /ToUnicode CMap
// always does that; the standard also accepts three cases where the font
// dictionary already carries enough to derive one, and lists them as
// exemptions.

// checkLevelAToUnicode flags a font used for rendering that neither includes a
// ToUnicode CMap nor meets one of the three exemptions.
func checkLevelAToUnicode(doc core.View, level Level) []Violation {
	rule := levelAClause("toUnicode", level)
	var errs []Violation
	for fontDict, u := range core.CollectFontTextUsage(doc) {
		if fontDict.Get("ToUnicode") != nil || toUnicodeExempt(doc, fontDict, u) {
			continue
		}
		name, _ := fontDict.Get("BaseFont").(object.Name)
		errs = append(errs, Violation{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf("font /%s is used for rendering but has no ToUnicode CMap and meets no exemption", name),
			Object:  u.ObjNum,
		})
	}
	return errs
}

// toUnicodeExempt reports whether a font may omit its ToUnicode CMap. The three
// exemptions are the ones the standard lists:
//
//   - the font uses one of the predefined encodings MacRomanEncoding,
//     MacExpertEncoding or WinAnsiEncoding, whose glyph names have known
//     Unicode values;
//   - the glyph names it references come from the Adobe standard Latin
//     character set or the set of named characters in the Symbol font;
//   - it is a Type 0 font whose descendant CIDFont uses the Adobe-GB1,
//     Adobe-CNS1, Adobe-Japan1 or Adobe-Korea1 character collection, for which
//     Adobe publishes a Unicode mapping.
//
// The Identity-H and Identity-V CMaps are conspicuously *not* an exemption: a
// composite font using them declares the Adobe-Identity ordering, which is
// exactly the case where a CID carries no Unicode meaning. The corpus is
// unambiguous on the point — an Identity-H font without ToUnicode is a failing
// file.
func toUnicodeExempt(doc core.View, fontDict *object.Dictionary, u *core.FontTextUsage) bool {
	subtype, _ := fontDict.Get("Subtype").(object.Name)
	if subtype == "Type0" {
		desc := core.Type0Descendant(doc, fontDict)
		if desc == nil {
			return true // no descendant to judge; not evidence of a violation
		}
		csi := doc.ResolveDict(desc.Get("CIDSystemInfo"))
		if csi == nil {
			return true
		}
		switch pdfTextString(doc, csi.Get("Ordering")) {
		case "GB1", "CNS1", "Japan1", "Korea1":
			return true
		}
		return false
	}
	if predefinedLatinEncoding(doc, fontDict) {
		return true
	}
	names, known := referencedGlyphNames(doc, fontDict, u)
	if !known {
		// The font's own built-in encoding decides which glyph a code selects,
		// and this package does not read it, so the names cannot be listed. That
		// is the shape a symbolic font with no /Encoding has — and the shape the
		// rule exists to catch — so it is not treated as an exemption.
		return false
	}
	for n := range names {
		if !font.StandardLatinName(n) && !font.SymbolSetNames[n] {
			return false
		}
	}
	return true
}

// predefinedLatinEncoding reports whether a simple font's /Encoding names one of
// the predefined encodings the exemption lists.
//
// An /Encoding dictionary whose /BaseEncoding names one counts as well. The
// exemption exists because the encoding fixes the Unicode value of every code,
// and a /Differences array over such a base leaves that true for every code it
// does not touch and supplies an explicit glyph name for the ones it does.
func predefinedLatinEncoding(doc core.View, fontDict *object.Dictionary) bool {
	predefined := func(n object.Name) bool {
		switch n {
		case "MacRomanEncoding", "MacExpertEncoding", "WinAnsiEncoding":
			return true
		}
		return false
	}
	switch enc := doc.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		return predefined(enc)
	case *object.Dictionary:
		base, _ := doc.Resolve(enc.Get("BaseEncoding")).(object.Name)
		return predefined(base)
	}
	return false
}

// referencedGlyphNames returns the glyph names a simple font references, and
// whether they could be established at all.
//
// The FontDescriptor's /CharSet is preferred: it is the subset font's own list
// of the glyphs it contains, which is precisely "the glyph names of the glyphs
// referenced". Without one the names are read off the font's encoding for the
// codes the document actually shows — and if any shown code has no name there,
// the answer is "unknown" rather than a shorter list, because the missing name
// is the one that would decide the rule.
func referencedGlyphNames(doc core.View, fontDict *object.Dictionary, u *core.FontTextUsage) (map[string]bool, bool) {
	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	if fd != nil {
		if cs, ok := doc.Resolve(fd.Get("CharSet")).(object.String); ok && len(cs.Value) > 0 {
			return core.ParseCharSet(string(cs.Value)), true
		}
	}
	symbolic := fd != nil && descriptorSymbolic(doc, fd)
	enc := simpleFontCodeToName(doc, fontDict, symbolic)
	names := map[string]bool{}
	for _, s := range u.Strings {
		for _, code := range s {
			name := enc[code]
			if name == "" {
				return nil, false
			}
			if name == ".notdef" {
				continue
			}
			names[name] = true
		}
	}
	return names, true
}
