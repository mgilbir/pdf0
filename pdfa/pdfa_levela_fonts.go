package pdfa

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/simplefont"
)

// This file implements the Unicode-mapping requirement of Level A and Level U
// (ISO 19005-1 6.3.8, -2/-3 6.2.11.7.2): a font used to show text must let a
// consumer recover the Unicode value of every character it shows. A /ToUnicode
// CMap always does that; the standard also accepts three cases where the font
// dictionary already carries enough to derive one, and lists them as
// exemptions. Level U is exactly this requirement added to Level B, which is
// why it lives apart from the structure families of Level A.

// checkUnicodeMapping flags a font used for rendering that neither includes a
// ToUnicode CMap nor meets one of the three exemptions, and, at parts 2 and 3,
// a ToUnicode CMap that maps a code to U+0000, U+FEFF or U+FFFE — values that
// are not characters, which ISO 19005-2/-3 6.2.11.7.2 forbids alongside the
// mapping requirement (veraPDF 6.2.11.7.2 t02; ISO 19005-1 has no such
// clause). It runs at every level that carries the requirement: 1a, 2a, 2u,
// 3a and 3u.
func checkUnicodeMapping(doc core.View, level Level) []Violation {
	if !level.requiresUnicode() {
		return nil
	}
	rule := levelAClause("toUnicode", level)
	var errs []Violation
	for fontDict, u := range core.CollectFontTextUsage(doc) {
		if tu, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*object.Stream); ok {
			if level.Part() != 1 && core.HasForbiddenUnicodeTargets(doc, tu) {
				errs = append(errs, Violation{
					Rule:    rule,
					Level:   level,
					Message: "ToUnicode CMap maps a character to U+0000, U+FEFF or U+FFFE, which are not Unicode characters",
					Object:  u.ObjNum,
				})
			}
			continue
		}
		if fontDict.Get("ToUnicode") != nil || toUnicodeExempt(doc, fontDict, u) {
			continue
		}
		name, _ := doc.ResolveName(fontDict.Get("BaseFont"))
		errs = append(errs, Violation{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf("font %s is used for rendering but has no ToUnicode CMap and meets no exemption", name),
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
	subtype, _ := doc.ResolveName(fontDict.Get("Subtype"))
	if subtype == "Type0" {
		desc := core.Type0Descendant(doc, fontDict)
		if desc == nil {
			return true // no descendant to judge; not evidence of a violation
		}
		csi := doc.ResolveDict(desc.Get("CIDSystemInfo"))
		if csi == nil {
			return true
		}
		ordering, known := pdfTextString(doc, csi.Get("Ordering"))
		if !known {
			return true // ciphertext: not evidence of a violation
		}
		switch ordering {
		case "GB1", "CNS1", "Japan1", "Korea1":
			return true
		}
		return false
	}
	if predefinedLatinEncoding(doc, fontDict) {
		return true
	}
	named := func(names map[string]bool) bool {
		for n := range names {
			if !simplefont.IsStandardLatin(n) && !simplefont.IsSymbolSet(n) {
				return false
			}
		}
		return true
	}
	// The glyphs the document references, by name, when the encoding names
	// every code it shows.
	if names, known := referencedGlyphNames(doc, fontDict, u); known && named(names) {
		return true
	}
	// Otherwise the FontDescriptor's /CharSet, which lists every glyph in the
	// (subset) font: when all of those are named characters, so are the ones
	// referenced. The converse does not hold — a subset may carry glyphs the
	// document never shows, and the corpus has such a font (PDF_A-2u
	// 6-2-11-7-2-t01-pass-e), which is why the referenced names come first.
	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	if fd != nil {
		if cs, r := doc.StringValue(fd.Get("CharSet")); r == core.ReasonOK && len(cs.Value) > 0 {
			return named(core.ParseCharSet(string(cs.Value)))
		}
	}
	// Neither says: a symbolic font whose built-in encoding pdf0 does not read
	// (a CFF program's) is the shape the rule exists to catch, so it is not
	// treated as an exemption.
	return false
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

// referencedGlyphNames returns the glyph names a simple font references — the
// names its encoding gives the codes the document actually shows — and
// whether they could be established at all. If any shown code has no name,
// the answer is "unknown" rather than a shorter list, because the missing name
// is the one that would decide the rule.
//
// The encoding is the font dictionary's, over the base ISO 32000-1 9.6.6
// gives it; for a font with no /Encoding whose base is the font program's own
// built-in encoding, that is read out of an embedded Type 1 program
// (type1BuiltinEncoding).
func referencedGlyphNames(doc core.View, fontDict *object.Dictionary, u *core.FontTextUsage) (map[string]bool, bool) {
	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	symbolic := fd != nil && descriptorSymbolic(doc, fd)
	enc := simpleFontCodeToName(doc, fontDict, symbolic)
	if fontDict.Get("Encoding") == nil && symbolic && fd != nil {
		if s, ok := doc.Resolve(fd.Get("FontFile")).(*object.Stream); ok {
			// A program that did not decode leaves the encoding unknown,
			// which is how it is treated (not an exemption).
			if data, r := doc.Content(s); r == core.ReasonOK {
				if builtin, ok := type1BuiltinEncoding(data); ok {
					enc = builtin
				}
			}
		}
	}
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

// type1BuiltinEncoding reads the built-in encoding of a Type 1 font program:
// the /Encoding entry of its clear-text portion (Adobe Type 1 Font Format,
// 2.3), which is either StandardEncoding or an array filled by
// "dup <code> /<name> put" statements. The second result is false when the
// program has no /Encoding pdf0 can read, and nothing is guessed then.
//
// Only the clear text before eexec is looked at, so the scan is bounded by it;
// codes outside 0-255 are ignored.
func type1BuiltinEncoding(prog []byte) (map[byte]string, bool) {
	if i := bytes.Index(prog, []byte("eexec")); i >= 0 {
		prog = prog[:i]
	}
	i := bytes.Index(prog, []byte("/Encoding"))
	if i < 0 {
		return nil, false
	}
	fields := bytes.Fields(prog[i+len("/Encoding"):])
	if len(fields) == 0 {
		return nil, false
	}
	if string(fields[0]) == "StandardEncoding" {
		out := make(map[byte]string, simplefont.StandardEncoding.Len())
		for c, n := range simplefont.StandardEncoding.Codes() {
			out[c] = n
		}
		return out, true
	}
	out := map[byte]string{}
	for k := 0; k+3 < len(fields); k++ {
		switch string(fields[k]) {
		case "def", "readonly":
			return out, true
		case "dup":
			code, err := strconv.Atoi(string(fields[k+1]))
			name := fields[k+2]
			if err != nil || code < 0 || code > 255 || len(name) < 2 || name[0] != '/' || string(fields[k+3]) != "put" {
				continue
			}
			out[byte(code)] = string(name[1:])
			k += 3
		}
	}
	return out, len(out) > 0
}
