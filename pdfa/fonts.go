package pdfa

import (
	"encoding/binary"
	"fmt"
	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/fontcheck"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/simplefont"
	"slices"
	"unicode"
)

// This file implements the PDF/A font rule family (ISO 19005-1 clause 6.3,
// 19005-2/-3 clause 6.2.11, 19005-4 clause 6.2.10), grounded in ISO 32000-1
// clause 9: CIDFont/CMap consistency (9.7.4-9.7.5, Table 118), TrueType
// encodings (9.6.6.4), embedding, and ToUnicode restrictions. The machinery
// PDF/UA's font rules share — a font's widths, encoding and program glyphs —
// is internal/fontcheck's.

// --- content walking: text usage per font ---

// --- predefined CMaps (ISO 32000-1, 9.7.5.2, Table 118) ---

// --- dictionary-level font checks ---

// checkFontDictionaries validates Type0/CIDFont/CMap dictionary consistency,
// TrueType encodings, ToUnicode values, and embedding of rendered fonts.
func checkFontDictionaries(doc core.View, level Level) []Violation {
	rule := fontRule(level)
	var errs []Violation

	usage := core.CollectFontTextUsage(doc)
	for fontDict, u := range usage {
		if !isFontDict(doc, fontDict) {
			continue
		}
		errs = append(errs, checkOneFontDict(doc, level, rule, fontDict, u)...)
	}
	return errs
}

// fontSubtypes are the kinds of font ISO 32000-1 9.5 names (Table 110), and
// the CIDFonts a Type 0 font descends to (9.7.4).
var fontSubtypes = map[object.Name]bool{
	"Type1": true, "MMType1": true, "TrueType": true, "Type3": true,
	"Type0": true, "CIDFontType0": true, "CIDFontType2": true,
}

// isFontDict reports whether d is a font dictionary of a kind ISO 32000-1
// names. One with any other /Subtype, or none, is no font a reader can draw
// with: veraPDF does not judge it by the font rules, and a Tf naming it
// selects an undefined resource.
func isFontDict(doc core.View, d *object.Dictionary) bool {
	if d == nil {
		return false
	}
	st, _ := doc.ResolveName(d.Get("Subtype"))
	return fontSubtypes[st]
}

func fontRule(level Level) string {
	switch level.Part() {
	case 1:
		return "6.3"
	case 4:
		return "6.2.10"
	}
	return "6.2.11"
}

// fontClause returns the specific ISO clause for a font-rule concept at the
// given level. The font requirements live under different clause trees per part
// (ISO 19005-1 6.3.x; -2/-3 6.2.11.x; -4 6.2.10.x), so a single fontRule parent
// clause mis-cites the rule in the reported error. Clauses follow the veraPDF
// validation profiles.
func fontClause(concept string, level Level) string {
	// [1b, 2b/3b, 4]
	m := map[string][3]string{
		"general":       {"6.3.2", "6.2.11.2", "6.2.10.2"},
		"cidSystemInfo": {"6.3.3.1", "6.2.11.3.1", "6.2.10.3.1"},
		"cidToGID":      {"6.3.3.2", "6.2.11.3.2", "6.2.10.3.2"},
		"cmap":          {"6.3.3.3", "6.2.11.3.3", "6.2.10.3.3"},
		"embed":         {"6.3.4", "6.2.11.4.1", "6.2.10.4.1"},
		"glyphs":        {"6.3.5", "6.2.11.4.1", "6.2.10.4.1"},
		"charSet":       {"6.3.5", "6.2.11.4.2", "6.2.10.4.2"},
		"width":         {"6.3.6", "6.2.11.5", "6.2.10.5"},
		"encoding":      {"6.3.7", "6.2.11.6", "6.2.10.6"},
		"notdef":        {"", "6.2.11.8", "6.2.10.9"}, // none at PDF/A-1: see fontKindApplies
		"toUnicode":     {"6.3.2", "6.2.11.2", "6.2.10.7"},
	}
	c, ok := m[concept]
	if !ok {
		return fontRule(level)
	}
	switch level.Part() {
	case 1:
		return c[0]
	case 4:
		return c[2]
	default:
		return c[1]
	}
}

func checkOneFontDict(doc core.View, level Level, rule string, fontDict *object.Dictionary, u *core.FontTextUsage) []Violation {
	var errs []Violation
	bad := func(concept, format string, args ...interface{}) {
		errs = append(errs, Violation{
			Rule:    fontClause(concept, level),
			Level:   level,
			Message: fmt.Sprintf(format, args...),
			Object:  u.ObjNum,
		})
	}
	subtype, _ := doc.ResolveName(fontDict.Get("Subtype"))

	errs = append(errs, checkFontDictEntries(doc, level, fontDict, subtype, u)...)

	if subtype == "Type0" {
		desc := core.Type0Descendant(doc, fontDict)
		encObj := fontDict.Get("Encoding")

		// CMap legality (9.7.5.2): the Encoding must be a predefined CMap
		// name or an embedded CMap stream.
		var cmapStreamInfo *object.Dictionary
		switch enc := doc.Resolve(encObj).(type) {
		case object.Name:
			if _, ok := core.PredefinedCMaps[string(enc)]; !ok {
				bad("cmap", "Type0 font Encoding CMap /%s is neither embedded nor predefined (ISO 32000, Table 118)", string(enc))
			}
		case *object.Stream:
			cmapStreamInfo = doc.ResolveDict(enc.Dict.Get("CIDSystemInfo"))
			// WMode in the stream dictionary must agree with the CMap
			// content (9.7.5.3).
			if dictWMode, ok := doc.Resolve(enc.Dict.Get("WMode")).(object.Integer); ok {
				if contentWMode, found := cmapContentWMode(doc, enc); found && int(dictWMode) != contentWMode {
					bad("cmap", "CMap dictionary WMode %d differs from the embedded CMap content WMode %d", int(dictWMode), contentWMode)
				}
			}
			// A CMap's UseCMap reference must be to a predefined CMap
			// (ISO 32000-1 9.7.5.2, Table 118): an embedded CMap stream or a
			// non-predefined name is not permitted.
			switch uc := doc.Resolve(enc.Dict.Get("UseCMap")).(type) {
			case object.Name:
				if _, ok := core.PredefinedCMaps[string(uc)]; !ok {
					bad("cmap", "embedded CMap references CMap /%s, which is not predefined (ISO 32000, Table 118)", string(uc))
				}
			case *object.Stream:
				bad("cmap", "embedded CMap references another embedded CMap, but UseCMap must name a predefined CMap (Table 118)")
			}
			// The usecmap operator in the CMap body must likewise name a
			// predefined CMap.
			if refName, found := cmapUseCMap(doc, enc); found {
				if _, ok := core.PredefinedCMaps[refName]; !ok {
					bad("cmap", "embedded CMap references CMap /%s, which is not predefined (ISO 32000, Table 118)", refName)
				}
			}
		}

		// CIDSystemInfo compatibility (9.7.4.2/19005 6.x.11.3.1): the
		// CIDFont's Registry/Ordering must match the CMap's, and — from
		// ISO 19005-2 on — the CIDFont Supplement must not exceed the
		// CMap's.
		if desc != nil {
			cidInfo := doc.ResolveDict(desc.Get("CIDSystemInfo"))
			var cmReg, cmOrd string
			var cmSupp object.Integer
			haveCMapInfo := false
			if name, ok := doc.Resolve(encObj).(object.Name); ok {
				if info, ok := core.PredefinedCMaps[string(name)]; ok && info.Registry != "" {
					cmReg, cmOrd = info.Registry, info.Ordering
					haveCMapInfo = string(name) != "Identity-H" && string(name) != "Identity-V"
				}
			} else if cmapStreamInfo != nil {
				var regKnown, ordKnown bool
				cmReg, regKnown = pdfTextString(doc, cmapStreamInfo.Get("Registry"))
				cmOrd, ordKnown = pdfTextString(doc, cmapStreamInfo.Get("Ordering"))
				if s, ok := doc.Resolve(cmapStreamInfo.Get("Supplement")).(object.Integer); ok {
					cmSupp = s
				}
				haveCMapInfo = regKnown && ordKnown
			}
			reg, regKnown := "", false
			ord, ordKnown := "", false
			if cidInfo != nil {
				reg, regKnown = pdfTextString(doc, cidInfo.Get("Registry"))
				ord, ordKnown = pdfTextString(doc, cidInfo.Get("Ordering"))
			}
			// A CMap that says no Registry and Ordering — a name that is
			// neither predefined nor embedded, or an embedded CMap with no
			// CIDSystemInfo — cannot be matched to the CIDFont's, and the
			// relationship the rule requires does not hold (veraPDF:
			// PDF_A-2b 6-2-11-3-3-t01-fail-a, -4 6-2-10-3-3-t01-fail-a).
			// Identity needs none.
			if !haveCMapInfo {
				switch enc := doc.Resolve(encObj).(type) {
				case object.Name:
					if _, ok := core.PredefinedCMaps[string(enc)]; !ok {
						bad("cidSystemInfo", "the CIDFont's CIDSystemInfo cannot be matched to the CMap /%s, which is neither predefined nor embedded and so states no Registry or Ordering", string(enc))
					}
				case *object.Stream:
					if cmapStreamInfo == nil {
						bad("cidSystemInfo", "the CIDFont's CIDSystemInfo cannot be matched to the embedded CMap, which has no CIDSystemInfo")
					}
				}
			}
			if cidInfo != nil && haveCMapInfo && regKnown && ordKnown {
				if reg != cmReg {
					bad("cidSystemInfo", "CIDFont CIDSystemInfo Registry %q does not match the CMap's %q", reg, cmReg)
				}
				if ord != cmOrd {
					bad("cidSystemInfo", "CIDFont CIDSystemInfo Ordering %q does not match the CMap's %q", ord, cmOrd)
				}
				// The Supplement relationship is a part-2-and-later rule:
				// ISO 19005-2 6.2.11.3.1 adds "the value of the Supplement
				// key ... of the CIDFont shall be less than or equal to
				// the Supplement key ... of the CMap", which ISO 19005-1
				// 6.3.3.1 does not require (it constrains Registry and
				// Ordering only). Applying it at PDF/A-1 false-positives on
				// PDF_A-1a 6-3-8-t01-pass-f, which pairs a Supplement-3
				// CIDFont with the Supplement-2 Adobe-Japan1-2 CMap and is a
				// pass file; the veraPDF profiles agree — "Supplement"
				// appears in PDFA-2*/3*/4* but in neither 1A nor 1B.
				if level.Part() != 1 && cmapStreamInfo != nil {
					if supp, ok := doc.Resolve(cidInfo.Get("Supplement")).(object.Integer); ok && supp > cmSupp {
						bad("cidSystemInfo", "CIDFont CIDSystemInfo Supplement %d is greater than the CMap's %d", int(supp), int(cmSupp))
					}
				}
			}

			// CIDToGIDMap (ISO 32000-1, 9.7.4.2): an embedded Type 2
			// CIDFont shall carry CIDToGIDMap as a stream or /Identity;
			// PDF/A requires the entry. Text rendered invisibly (mode 3
			// only) is exempt — the corpus passes such a font at 1b.
			if dsub, _ := doc.ResolveName(desc.Get("Subtype")); dsub == "CIDFontType2" && rendersVisibly(u) {
				switch v := doc.Resolve(desc.Get("CIDToGIDMap")).(type) {
				case nil:
					bad("cidToGID", "CIDFontType2 must contain a CIDToGIDMap entry (stream or /Identity)")
				case object.Name:
					if v != "Identity" {
						bad("cidToGID", "CIDFontType2 CIDToGIDMap name must be /Identity, got /%s", string(v))
					}
				case *object.Stream:
					// fine
				default:
					bad("cidToGID", "CIDFontType2 CIDToGIDMap must be a stream or the name /Identity")
				}
			}
		}
	}

	// TrueType encodings (ISO 32000-1, 9.6.6.4; 19005 6.x.11.6).
	if subtype == "TrueType" {
		errs = append(errs, checkTrueTypeEncoding(doc, level, rule, fontDict, u)...)
	}

	// ToUnicode values (A-4): no mapping may target U+0000, U+FEFF, U+FFFE.
	if level.Part() == 4 {
		if tu, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*object.Stream); ok {
			if core.HasForbiddenUnicodeTargets(doc, tu) {
				bad("toUnicode", "ToUnicode CMap maps to a forbidden Unicode value (U+0000, U+FEFF or U+FFFE)")
			}
		}
	}

	// The font file's kind at PDF/A-1 (ISO 19005-1 6.3.2; veraPDF 6.3.2-7):
	// PDF 1.4 names two, Type1C and CIDFontType0C, and a font file with any
	// other /Subtype — OpenType, which PDF 1.6 added — is one PDF/A-1 does
	// not allow. FontFile and FontFile2 carry no /Subtype.
	if level.Part() == 1 {
		fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
		if subtype == "Type0" {
			fd = nil
			if desc := core.Type0Descendant(doc, fontDict); desc != nil {
				fd = doc.ResolveDict(desc.Get("FontDescriptor"))
			}
		}
		if fd != nil {
			for _, key := range []object.Name{"FontFile", "FontFile2", "FontFile3"} {
				s, ok := doc.Resolve(fd.Get(key)).(*object.Stream)
				if !ok {
					continue
				}
				if st, ok := doc.ResolveName(s.Dict.Get("Subtype")); ok && st != "Type1C" && st != "CIDFontType0C" {
					bad("general", "the embedded font file's /Subtype is /%s; PDF 1.4 defines only "+
						"/Type1C and /CIDFontType0C", string(st))
				}
			}
		}
	}

	// Program-level checks: metrics, glyph coverage, and .notdef references.
	errs = append(errs, checkFontProgramConsistency(doc, level, rule, fontDict, u)...)

	return errs
}

// checkFontDictEntries holds a font dictionary to the entries ISO 32000-1
// requires of it (Tables 111, 112 and 121), as ISO 19005 6.3.2 / 6.2.11.2 /
// 6.2.10.2 do and the veraPDF profiles test (6.x.2-1, -3 to -6): /Type /Font;
// a /BaseFont for every kind but Type 3; and for a simple font /FirstChar,
// /LastChar and a /Widths of LastChar-FirstChar+1 entries — which a standard
// 14 Type 1 font left unembedded may do without (Table 111).
func checkFontDictEntries(doc core.View, level Level, fontDict *object.Dictionary, subtype object.Name, u *core.FontTextUsage) []Violation {
	var errs []Violation
	bad := func(format string, args ...interface{}) {
		errs = append(errs, Violation{Rule: fontClause("general", level), Level: level,
			Message: fmt.Sprintf(format, args...), Object: u.ObjNum})
	}
	if t, _ := doc.ResolveName(fontDict.Get("Type")); t != "Font" {
		bad("the font dictionary's /Type is not /Font")
	}
	baseFont, haveBaseFont := doc.ResolveName(fontDict.Get("BaseFont"))
	if subtype != "Type3" && !haveBaseFont {
		bad("the /%s font dictionary has no /BaseFont", string(subtype))
	}
	switch subtype {
	case "Type1", "MMType1", "TrueType", "Type3":
	default:
		return errs
	}
	if subtype == "Type1" && standard14[string(baseFont)] && !hasEmbeddedFontProgram(doc, doc.ResolveDict(fontDict.Get("FontDescriptor"))) {
		return errs
	}
	first, haveFirst := doc.ResolveInt(fontDict.Get("FirstChar"))
	last, haveLast := doc.ResolveInt(fontDict.Get("LastChar"))
	if !haveFirst {
		bad("the simple font dictionary has no /FirstChar")
	}
	if !haveLast {
		bad("the simple font dictionary has no /LastChar")
	}
	widths, haveWidths := doc.Resolve(fontDict.Get("Widths")).(object.Array)
	switch {
	case !haveWidths:
		bad("the simple font dictionary has no /Widths array")
	case haveFirst && haveLast && int64(len(widths)) != int64(last)-int64(first)+1:
		bad("the font's /Widths has %d entries, but /FirstChar %d to /LastChar %d is %d codes",
			len(widths), first, last, int64(last)-int64(first)+1)
	}
	return errs
}

// standard14 are the standard Type 1 fonts' names (ISO 32000-1 9.6.2.2).
var standard14 = map[string]bool{
	"Times-Roman": true, "Times-Bold": true, "Times-Italic": true, "Times-BoldItalic": true,
	"Helvetica": true, "Helvetica-Bold": true, "Helvetica-Oblique": true, "Helvetica-BoldOblique": true,
	"Courier": true, "Courier-Bold": true, "Courier-Oblique": true, "Courier-BoldOblique": true,
	"Symbol": true, "ZapfDingbats": true,
}

// pdfTextString is v as a decoded text string ("" when it is not a string),
// and whether its value is known: false in a Locked document, whose strings
// are ciphertext and must not be compared (core.View.StringValue).
func pdfTextString(doc core.View, v object.Object) (string, bool) {
	s, r := doc.TextString(v)
	return s, r != core.ReasonLocked
}

// cmapContentWMode is the writing mode an embedded CMap program sets with
// "/WMode n def", read as tokens: the words in a comment are not a definition.
func cmapContentWMode(doc core.View, stream *object.Stream) (int, bool) {
	data, _ := doc.Content(stream) // reason: presence-only; the producer recorded any declined trip
	if data == nil {
		return 0, false
	}
	return core.CMapWMode(doc.Cancel, data)
}

// cmapUseCMap is the "/Name usecmap" reference of an embedded CMap: the
// operator, not the word in a comment (audit 2026-09-22 C75).
func cmapUseCMap(doc core.View, stream *object.Stream) (string, bool) {
	data, _ := doc.Content(stream) // reason: presence-only; the producer recorded any declined trip
	if data == nil {
		return "", false
	}
	return core.CMapUseCMap(doc.Cancel, data)
}

// checkCMapEmbedded enforces ISO 19005-1 6.3.3.3: in PDF/A-1 every CMap used by
// a Type 0 font other than Identity-H/Identity-V shall be embedded (given as a
// stream), not referenced by a predefined name. Parts 2 and later permit
// predefined CMaps by name, so this is a PDF/A-1-only rule.
func checkCMapEmbedded(doc core.View, level Level) []Violation {
	if level.Part() != 1 {
		return nil
	}
	var errs []Violation
	for _, r := range doc.ReachableDicts() {
		dict, num := r.Dict, r.ObjNum
		if r.Stream != nil {
			continue
		}
		if st, _ := doc.ResolveName(dict.Get("Subtype")); st != "Type0" {
			continue
		}
		if enc, ok := doc.Resolve(dict.Get("Encoding")).(object.Name); ok && enc != "Identity-H" && enc != "Identity-V" {
			errs = append(errs, Violation{
				Rule:    "6.3.3.3",
				Level:   level,
				Message: fmt.Sprintf("CMap /%s must be embedded (PDF/A-1 permits only Identity-H/V by name)", string(enc)),
				Object:  num,
			})
		}
	}
	return errs
}

// checkTrueTypeEncoding enforces the PDF/A TrueType encoding rules
// (ISO 19005-1, 6.3.7; -2/-3, 6.2.11.6; -4, 6.2.10.6; grounded in ISO
// 32000-1, 9.6.6.4): a symbolic TrueType font shall have no Encoding entry,
// and its program's cmap the subtables the part asks for (symbolicCmapProblem);
// a non-symbolic one shall specify WinAnsiEncoding or MacRomanEncoding —
// directly or as BaseEncoding. An Encoding dictionary shall have no
// Differences entry at all at PDF/A-1; from PDF/A-2 on, its names must be
// listed in the Adobe Glyph List, and an embedded program must have a (3,1)
// cmap subtable. ISO 32000-1 cites "Adobe Glyph List, Version 2.0 (September
// 2002)", which is glyphlist.txt, and fontcheck.AGLListed reads it as veraPDF
// does — its 4,281 names and .notdef; not the ITC Zapf Dingbats list, and not
// a uniXXXX name the list does not carry. PDF/UA's rule reads the same list.
//
// Two readings are pdf0's choice. .notdef is accepted in a Differences array,
// as veraPDF accepts it (its copy of the list carries the name). And the names
// are tested whether or not the font program is embedded: the clause's "if the
// font program is embedded" governs only its cmap half, so an unembedded font
// is held to the list too. veraPDF tests the names of an embedded font only,
// and passes an unembedded one whatever its Differences name.
func checkTrueTypeEncoding(doc core.View, level Level, rule string, fontDict *object.Dictionary, u *core.FontTextUsage) []Violation {
	rule = fontClause("encoding", level) // 6.3.7 / 6.2.11.6 / 6.2.10.6
	var errs []Violation
	bad := func(format string, args ...interface{}) {
		errs = append(errs, Violation{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf(format, args...),
			Object:  u.ObjNum,
		})
	}

	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	symbolic := false
	if fd != nil {
		if flags, ok := doc.Resolve(fd.Get("Flags")).(object.Integer); ok {
			symbolic = flags&4 != 0
		}
	}

	encObj := doc.Resolve(fontDict.Get("Encoding"))

	if symbolic {
		if encObj != nil {
			bad("symbolic TrueType font must not have an Encoding entry")
		}
		if fd != nil {
			if msg := symbolicCmapProblem(doc, level, fd); msg != "" {
				bad("%s", msg)
			}
		}
		return errs
	}

	if fd != nil {
		if msg := nonSymbolicCmapProblem(doc, level, fd); msg != "" {
			bad("%s", msg)
		}
	}

	switch enc := encObj.(type) {
	case nil:
		bad("non-symbolic TrueType font must have an Encoding entry")
	case object.Name:
		if enc != "WinAnsiEncoding" && enc != "MacRomanEncoding" {
			bad("non-symbolic TrueType font Encoding must be WinAnsiEncoding or MacRomanEncoding, got /%s", string(enc))
		}
	case *object.Dictionary:
		base, hasBase := doc.Resolve(enc.Get("BaseEncoding")).(object.Name)
		if !hasBase {
			bad("non-symbolic TrueType font Encoding dictionary must have a BaseEncoding entry")
		} else if base != "WinAnsiEncoding" && base != "MacRomanEncoding" {
			bad("non-symbolic TrueType font BaseEncoding must be WinAnsiEncoding or MacRomanEncoding, got /%s", string(base))
		}
		switch {
		case !enc.Has("Differences"):
		case level.Part() == 1:
			// ISO 19005-1 6.3.7: "If the value of the Encoding entry is a
			// dictionary, it shall not contain a Differences entry." PDF/A-1
			// has no Adobe Glyph List exception; that arrived with ISO
			// 19005-2. The key's presence is the test, as veraPDF reads it —
			// an empty array included.
			bad("non-symbolic TrueType font Encoding dictionary must not contain a Differences entry")
		default:
			// ISO 19005-2/-3 6.2.11.6, -4 6.2.10.6: no Differences array
			// "unless all of the glyph names in the Differences array are
			// listed in the Adobe Glyph List and, if the font program is
			// embedded, it shall contain at least the Microsoft Unicode (3,1
			// – Platform ID = 3, Encoding ID = 1) encoding in the 'cmap'
			// table". The cmap half is read as veraPDF reads it
			// (fontcheck.DifferencesNotInAGL): an empty array triggers it
			// too, and a program pdf0 could not read is not evidence.
			if diffs, ok := doc.Resolve(enc.Get("Differences")).(object.Array); ok {
				// One finding per name, however often the array repeats it.
				reported := map[object.Name]bool{}
				for _, el := range diffs {
					if name, ok := doc.ResolveName(el); ok && !reported[name] && !fontcheck.AGLListed(string(name)) {
						reported[name] = true
						bad("Differences glyph name /%s is not in the Adobe Glyph List", string(name))
					}
				}
			}
			if _, noCmap31 := fontcheck.DifferencesNotInAGL(doc, fontDict); noCmap31 {
				bad("non-symbolic TrueType font has a Differences array, but its embedded program has no Microsoft Unicode (3,1) cmap subtable")
			}
		}
	}
	return errs
}

// symbolicCmapProblem says what is wrong with a symbolic TrueType font's cmap
// table for the level, or "" when nothing is. Each part has its own rule:
//
//   - ISO 19005-1 6.3.7: the cmap shall "contain exactly one encoding".
//   - ISO 19005-2/-3 6.2.11.6: "either exactly one encoding or ... at least,
//     the Microsoft Symbol (3,0 – Platform ID=3, Encoding ID=0) encoding".
//   - ISO 19005-4 6.2.10.6: "either the Microsoft Symbol (3,0 ...) or the Mac
//     Roman (1,0 – Platform ID=1, Encoding ID=0) encoding". Exactly one
//     encoding is no longer enough by itself.
//
// The records are fontcheck.CmapEncodings'. A program pdf0 could not read, or
// one with no cmap records at all, is not judged.
func symbolicCmapProblem(doc core.View, level Level, fd *object.Dictionary) string {
	encs, known := fontcheck.CmapEncodings(doc, fd)
	if !known || len(encs) == 0 {
		return ""
	}
	has := func(platform, encoding int) bool {
		return slices.Contains(encs, fontcheck.CmapEncoding{Platform: platform, Encoding: encoding})
	}
	switch level.Part() {
	case 1:
		if len(encs) != 1 {
			return fmt.Sprintf("symbolic TrueType font must have exactly one cmap subtable, found %d", len(encs))
		}
	case 4:
		if !has(3, 0) && !has(1, 0) {
			return "symbolic TrueType font's cmap must have a Microsoft Symbol (3,0) or Mac Roman (1,0) subtable"
		}
	default:
		if len(encs) != 1 && !has(3, 0) {
			return fmt.Sprintf("symbolic TrueType font's cmap must have exactly one subtable or a Microsoft Symbol (3,0) subtable; it has %d and no (3,0)", len(encs))
		}
	}
	return ""
}

// nonSymbolicCmapProblem says what is wrong with a non-symbolic TrueType
// font's cmap table for the level, or "" when nothing is. ISO 19005-1 has no
// such rule. The veraPDF profiles read the others so:
//
//   - PDF/A-2 and -3 6.2.11.6-1: a subtable other than Microsoft Symbol (3,0).
//   - ISO 19005-4 6.2.10.6: "the Microsoft Unicode (3,1 ...) or the Mac Roman
//     (1,0 ...) encoding" (veraPDF 6.2.10.6-1).
//
// The records are fontcheck.CmapEncodings'; a program pdf0 could not read is
// not judged.
func nonSymbolicCmapProblem(doc core.View, level Level, fd *object.Dictionary) string {
	if level.Part() == 1 {
		return ""
	}
	encs, known := fontcheck.CmapEncodings(doc, fd)
	if !known {
		return ""
	}
	has := func(platform, encoding int) bool {
		return slices.Contains(encs, fontcheck.CmapEncoding{Platform: platform, Encoding: encoding})
	}
	if level.Part() == 4 {
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

// --- font program loading ---

// --- simple font encoding (code -> glyph name) ---

// simpleFontCodeToName is fontcheck.SimpleFontCodeToName: the code-to-name
// table every simple-font rule here reads, the width rule included.
func simpleFontCodeToName(doc core.View, fontDict *object.Dictionary, symbolic bool) map[byte]string {
	return fontcheck.SimpleFontCodeToName(doc, fontDict, symbolic)
}

// simpleFontBaseEncodingModelled is fontcheck.BaseEncodingModelled: whether a
// code simpleFontCodeToName leaves unnamed selects .notdef (the base encoding
// is one pdf0 has a table for) or is unknown (the program's own, or one
// outside ISO 32000-1 Annex D.2).
func simpleFontBaseEncodingModelled(doc core.View, fontDict *object.Dictionary, symbolic bool) bool {
	return fontcheck.BaseEncodingModelled(doc, fontDict, symbolic)
}

// --- glyph-name to Unicode (for TrueType cmap lookup) ---

// --- program consistency: metrics, glyph coverage, .notdef ---

// checkFontProgramConsistency validates that, for every glyph actually shown,
// the font-dictionary width matches the embedded program's advance width
// (ISO 19005 font-metrics rule), the glyph is present in the program
// (embedding-completeness rule), and no shown glyph is .notdef.
func checkFontProgramConsistency(doc core.View, level Level, rule string, fontDict *object.Dictionary, u *core.FontTextUsage) []Violation {
	subtype, _ := doc.ResolveName(fontDict.Get("Subtype"))
	if subtype == "Type3" {
		return checkType3Widths(doc, level, rule, fontDict, u)
	}
	if subtype == "Type0" {
		return checkCIDFontConsistency(doc, level, rule, fontDict, u)
	}
	return checkSimpleFontConsistency(doc, level, rule, fontDict, u)
}

// damagedFontProgramError is returned in place of the silent fp==nil exemption:
// if a font whose glyphs are actually shown carries an embedded font program
// that could not be parsed, the program is damaged and cannot provide those
// glyphs. Only visibly-rendered fonts count (invisible text needs no embedded
// program at all), and only when a FontFile is present (a missing program is
// the separate embedding rule). Across the corpus, every valid embedded program
// parses, so this raises no false positive.
//
// Only ReasonMalformed is damage. A program pdf0 declined to read — over a
// size limit, under a filter it does not implement, ciphertext — says nothing
// about the font, and LoadFontProgram has recorded the trip; a 760 KB font
// read under a 100 KB scanning limit used to be reported as damaged here
// (audit 2026-09-22 C47).
func damagedFontProgramError(doc core.View, level Level, rule string, fontDict, fd *object.Dictionary, u *core.FontTextUsage, r core.Reason) []Violation {
	if r != core.ReasonMalformed || fd == nil || !rendersVisibly(u) || !hasEmbeddedFontProgram(doc, fd) {
		return nil
	}
	subtype, _ := doc.ResolveName(fontDict.Get("Subtype"))
	return []Violation{{
		Rule:    fontClause("embed", level),
		Level:   level,
		Message: fmt.Sprintf("embedded %s font program is damaged and could not be parsed", string(subtype)),
		Object:  u.ObjNum,
	}}
}

// hasEmbeddedFontProgram reports whether the descriptor carries an embedded font
// program stream (FontFile/FontFile2/FontFile3).
func hasEmbeddedFontProgram(doc core.View, fd *object.Dictionary) bool {
	for _, k := range []object.Name{"FontFile", "FontFile2", "FontFile3"} {
		if _, ok := doc.Resolve(fd.Get(k)).(*object.Stream); ok {
			return true
		}
	}
	return false
}

// fontKindApplies reports whether the level has a rule for a report kind.
// ISO 19005-1 has no .notdef rule: PDF/A-2 introduced it (6.2.11.8, -4
// 6.2.10.9), and the PDF/A-1b profile has none, so at PDF/A-1 a .notdef
// reference is a glyph the program defines and nothing more.
func fontKindApplies(kind string, level Level) bool {
	return kind != "notdef" || level.Part() != 1
}

// fontKindClause maps a report "kind" to its ISO clause for the level.
func fontKindClause(kind string, level Level) string {
	switch kind {
	case "glyph":
		return fontClause("glyphs", level)
	case "notdef":
		return fontClause("notdef", level)
	case "width":
		return fontClause("width", level)
	case "toUnicode":
		return fontClause("toUnicode", level)
	}
	return fontClause("general", level)
}

func checkSimpleFontConsistency(doc core.View, level Level, rule string, fontDict *object.Dictionary, u *core.FontTextUsage) []Violation {
	sf, fd, r := fontcheck.LoadSimple(doc, fontDict)
	if sf == nil {
		return damagedFontProgramError(doc, level, rule, fontDict, fd, u, r)
	}
	fp, subtype, symbolic, enc := sf.Program, sf.Subtype, sf.Symbolic, sf.Encoding
	baseEncodingModelled := simpleFontBaseEncodingModelled(doc, fontDict, symbolic)

	var errs []Violation
	reported := map[string]bool{}
	report := func(kind, msg string) {
		if reported[kind] || !fontKindApplies(kind, level) {
			return
		}
		reported[kind] = true
		errs = append(errs, Violation{Rule: fontKindClause(kind, level), Level: level, Message: msg, Object: u.ObjNum})
	}

	renders := rendersVisibly(u)
	// At PDF/A-4 a shown code's Unicode value must not be U+0000 (6.2.10.7).
	// A ToUnicode CMap that leaves the code out, under an encoding that
	// gives it no name either, says nothing else of it, and veraPDF reads
	// U+0000 (PDF_A-4 6-2-10-4-1-t02-fail-c: code 0 under WinAnsiEncoding,
	// absent from the CMap). A font with no ToUnicode is not held to it.
	var toUni *core.ToUnicode
	if level.Part() == 4 {
		toUni, _ = core.ParseToUnicode(doc, fontDict) // reason: only a CMap that was read is judged; the producer recorded any declined trip
	}
	for _, s := range u.Strings {
		for _, code := range s {
			name := enc[code]
			if toUni != nil && name == "" {
				if _, mapped := toUni.Runes(int(code)); !mapped {
					report("toUnicode", fmt.Sprintf("a code shown in %s font has no Unicode value: neither its ToUnicode CMap nor its encoding maps code %d", string(subtype), code))
				}
			}
			// A code the modelled encoding leaves unnamed selects .notdef
			// (ISO 32000-1 9.6.6.1), the glyph the program carries for it:
			// that is a .notdef reference, not a glyph the program lacks.
			// veraPDF reads it so (6-2-11-8-t01-fail-a at PDF/A-2b, code 0
			// under /WinAnsiEncoding; 6-2-10-9-t01-fail-a at -4, code 31).
			if name == "" && subtype != "TrueType" && baseEncodingModelled {
				name = ".notdef"
			}

			glyphExists := simpleGlyphExists(fp, subtype, symbolic, code, name)
			// A named code a non-symbolic TrueType program has no standard
			// cmap for is a glyph it lacks, not a .notdef reference
			// (fontcheck.Simple.NoStandardCmap).
			lacksStandardCmap := sf.NoStandardCmap && name != ""
			if lacksStandardCmap {
				glyphExists = false
			}

			// A Type 1/CFF code with no glyph name is only evidence of a
			// missing glyph when the base encoding is one this package
			// models; otherwise the name comes from the font program's
			// built-in encoding, which is not parsed here, and reporting
			// the glyph absent asserts a fact not in evidence. That
			// false-positives on the conforming corpus files PDF_A-1a
			// 6-3-8-t01-pass-b (/MacExpertEncoding) and -pass-e (symbolic,
			// no /Encoding), whose /CharSet lists the very glyph reported
			// missing. TrueType is unaffected: its codes go through the
			// program's cmap, not a glyph name.
			codeMapped := subtype == "TrueType" || name != "" || baseEncodingModelled

			// A code that the cmap does not resolve is only evidence of a
			// missing glyph when the cmap is complete. When the work budget
			// truncated it (fp.cmapPartial) the code is *unknown*, and
			// simplefont.TrueTypeGlyph's "non-nil cmap is authoritative" contract would
			// otherwise turn every unread mapping into glyph 0 — audit C46's
			// false positive, reached through the budget instead of the
			// dropped-segment bug.
			if fp.CmapPartial && subtype == "TrueType" {
				codeMapped = false
			}

			if renders && codeMapped && !glyphExists {
				report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (code %d)", string(subtype), code))
			}
			// A .notdef reference is prohibited even in invisible text
			// (rendering mode 3), so this is not gated on visible rendering.
			// It is gated on the cmap being complete for the same reason as
			// the glyph rule above: a truncated cmap resolves an unread code
			// to gid 0, which reads as .notdef.
			if !(fp.CmapPartial && subtype == "TrueType") && !lacksStandardCmap && isNotdefGlyph(fp, subtype, symbolic, code, name) {
				report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(subtype)))
			}

			// Width consistency (only for visibly rendered glyphs).
			if renders && sf.WidthDiffers(doc, code) {
				report("width", fmt.Sprintf("width information for glyphs used for rendering is inconsistent in %s font", string(subtype)))
			}
		}
	}
	return errs
}

func checkCIDFontConsistency(doc core.View, level Level, rule string, fontDict *object.Dictionary, u *core.FontTextUsage) []Violation {
	cf, fd, r := fontcheck.LoadCID(doc, fontDict)
	if cf == nil {
		// No descendant (and no descriptor) is nothing to report; a program
		// that could not be read may be damage.
		return damagedFontProgramError(doc, level, rule, fontDict, fd, u, r)
	}
	fp, cidSub, gmap := cf.Program, cf.Subtype, cf.GlyphMap

	var errs []Violation
	reported := map[string]bool{}
	report := func(kind, msg string) {
		if reported[kind] || !fontKindApplies(kind, level) {
			return
		}
		reported[kind] = true
		errs = append(errs, Violation{Rule: fontKindClause(kind, level), Level: level, Message: msg, Object: u.ObjNum})
	}
	renders := rendersVisibly(u)
	toUni, _ := core.ParseToUnicode(doc, fontDict) // reason: only a mapping that is present is acted on below; the producer recorded any declined trip

	for _, s := range u.Strings {
		if !cf.HaveCMap {
			// A predefined CMap this module does not carry. Nothing about the
			// string can be said: not where one code ends, not which glyph any
			// of them names. Skipping is the honest answer, and the note above
			// says the checks did not run.
			continue
		}
		// Identity-H/V codes are exactly two bytes; a string of odd length
		// ends in an incomplete code that cannot reference a defined glyph
		// (ISO 32000-1 9.7.5, 9.10). An embedded CMap says its own code widths,
		// so the same question is asked of it below, per code.
		if cf.CMap.Identity() && len(s)%2 == 1 {
			if renders {
				report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (incomplete character code)", string(cidSub)))
			}
			// What a reader draws for it is .notdef (veraPDF names the
			// glyph so: PDF_A-2b 6-2-11-4-1-t02-fail-e).
			report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(cidSub)))
		}
		// cf.Shown skips a code left to a CMap whose data is not carried; the
		// CMap has reported that those codes were not checked.
		for g := range cf.Shown(s) {
			code := g.Code
			if !code.Mapped {
				// A code the document wrote and its own CMap does not define.
				// It names no glyph, so every check below would be about CID 0
				// by accident rather than because the page asked for .notdef.
				if renders {
					report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (character code %d is outside the font's CMap)", string(cidSub), code.Value))
				}
				// Drawn with .notdef: a reference to it.
				report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(cidSub)))
				// It is still drawn, with CID 0's glyph (ISO 32000-1
				// 9.7.6.3), and advances by that glyph's width; CID.WidthDiffers
				// says when that substitute is known.
				if renders && cf.WidthDiffers(g) {
					report("width", fmt.Sprintf("width information for glyphs used for rendering is inconsistent in %s font", string(cidSub)))
				}
				continue
			}
			cid := code.CID

			// The glyph the CID selects, once, for every check below. A map
			// pdf0 could not read names none, and nothing is asserted about
			// the glyph.
			key, keyKnown := g.Key, g.KeyKnown
			exists := true
			if keyKnown {
				exists = gmap.Exists(fp, key)
			}

			if !exists {
				// A CID the program lacks is drawn with .notdef, so it is a
				// .notdef reference as well as a missing glyph (veraPDF:
				// PDF_A-2b 6-2-11-4-1-t02-fail-d, 6-1-13-t10-fail-a).
				report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(cidSub)))
			}
			if renders && !exists {
				report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (CID %d)", string(cidSub), cid))
			} else if renders && keyKnown && gmap.Empty(fp, key) {
				// A subset must embed an outline for every rendered glyph; an
				// empty glyf entry is acceptable only for a whitespace
				// character (ISO 19005 6.2.11.4.1/6.2.10.4.1).
				// Keyed by the character *code*, not the CID. §9.10.3 maps a
				// ToUnicode CMap over the codes a content stream writes, and
				// the two are the same number only under Identity — for a font
				// with its own CMap, asking by CID reads whatever entry happens
				// to sit at that number, or none.
				if r, ok := toUni.First(int(code.Value)); ok && !isGlyphWhitespace(r) {
					report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (CID %d)", string(cidSub), cid))
				}
			}
			if cid == 0 {
				report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(cidSub)))
			}

			if renders && cf.WidthDiffers(g) {
				report("width", fmt.Sprintf("width information for glyphs used for rendering is inconsistent in %s font", string(cidSub)))
			}
		}
	}
	return errs
}

// checkType3Widths reports a Type 3 font whose /Widths disagree with the
// advances its CharProcs' d0/d1 operators give (fontcheck.Type3WidthDiffers,
// which explains why the two are compared in glyph space and how the
// FontMatrix sets the tolerance).
func checkType3Widths(doc core.View, level Level, rule string, fontDict *object.Dictionary, u *core.FontTextUsage) []Violation {
	if !rendersVisibly(u) || !fontcheck.Type3WidthDiffers(doc, fontDict, u) {
		return nil
	}
	return []Violation{{Rule: fontClause("width", level), Level: level,
		Message: "width information for glyphs used for rendering is inconsistent in Type3 font", Object: u.ObjNum}}
}

// --- small helpers ---

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// rendersVisibly reports whether the font is "used for rendering": whether it
// showed text in any mode other than 3. ISO 19005-2 6.2.11.4.1 (and -4
// 6.2.10.4.1): "A font referenced for use solely in rendering mode 3 is
// therefore not rendered"; -4 6.2.10.5 exempts "Glyphs that are referenced
// only with rendering mode 3". Mode 7 adds the glyph outlines to the clipping
// path, so the glyphs' shapes and advances decide what is painted; veraPDF's
// embedding, glyph-presence and width tests exempt rendering mode 3 alone.
func rendersVisibly(u *core.FontTextUsage) bool {
	if len(u.Modes) == 0 {
		return true
	}
	for m := range u.Modes {
		if m != 3 {
			return true
		}
	}
	return false
}

func simpleGlyphExists(fp *font.Program, subtype object.Name, symbolic bool, code byte, name string) bool {
	if subtype == "TrueType" {
		// Glyph 0 is the program's .notdef: present, and a .notdef
		// reference (isNotdefGlyph), not a glyph the program lacks
		// (veraPDF: PDF_A-4 6-2-10-4-1-t02-fail-c).
		gid, ok := simplefont.TrueTypeGlyph(fp, symbolic, code, name)
		return ok && gid < fp.NumGlyphs
	}
	if name == "" {
		return false
	}
	return fp.GlyphNames[name]
}

func isNotdefGlyph(fp *font.Program, subtype object.Name, symbolic bool, code byte, name string) bool {
	if name == ".notdef" {
		return true
	}
	if subtype == "TrueType" {
		gid, ok := simplefont.TrueTypeGlyph(fp, symbolic, code, name)
		return ok && gid == 0
	}
	return false
}

// --- subset CharSet / CIDSet completeness ---

// subsetRule returns the clause a subset-embedding violation is reported
// under: 19005-1 6.3.5, 19005-2/-3 6.2.11.4.2. PDF/A-4 has no such clause —
// PDF 2.0 deprecates CharSet and CIDSet, and veraPDF's PDF/A-4 profile checks
// 6.2.10.4.1 and nothing under 6.2.10.4.2 — so no subset rule runs there.
func subsetRule(level Level) string {
	if level.Part() == 1 {
		return "6.3.5"
	}
	return "6.2.11.4.2"
}

// checkFontSubsetCompleteness verifies that a subset font descriptor's
// CharSet (Type1) or CIDSet (CIDFont), when present, lists every glyph or
// CID actually used for rendering (ISO 19005-1 6.3.5, -2/-3 6.2.11.4.2).
// An empty or partial set omitting a shown glyph is a violation. PDF/A-4 does
// not regulate either set (see subsetRule).
func checkFontSubsetCompleteness(doc core.View, level Level) []Violation {
	if level.Part() == 4 {
		return nil
	}
	rule := subsetRule(level)
	var errs []Violation

	for fontDict, u := range core.CollectFontTextUsage(doc) {
		subtype, _ := doc.ResolveName(fontDict.Get("Subtype"))
		switch subtype {
		case "Type1", "MMType1":
			fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
			if fd == nil {
				continue
			}
			cs, r := doc.StringValue(fd.Get("CharSet"))
			if r != core.ReasonOK {
				continue // absent, or ciphertext that lists nothing readable
			}
			listed := core.ParseCharSet(string(cs.Value))
			symbolic := descriptorSymbolic(doc, fd)
			enc := simpleFontCodeToName(doc, fontDict, symbolic)
			if usedGlyphMissing(u, enc, listed) {
				errs = append(errs, Violation{
					Rule:    rule,
					Level:   level,
					Message: "FontDescriptor CharSet does not list all glyph names used for rendering",
					Object:  u.ObjNum,
				})
			}
		case "Type0":
			desc := core.Type0Descendant(doc, fontDict)
			if desc == nil {
				continue
			}
			fd := doc.ResolveDict(desc.Get("FontDescriptor"))
			if fd == nil {
				continue
			}
			cidSetStream, ok := doc.Resolve(fd.Get("CIDSet")).(*object.Stream)
			if !ok {
				continue
			}
			// The same CMap question as the width and coverage checks: without
			// one, the string cannot be cut into codes and no CID can be named.
			// LoadCMap records a skip it cannot help (a predefined CMap).
			cmap, r := core.LoadCMap(doc, fontDict)
			if r != core.ReasonOK {
				continue
			}
			present, r := core.DecodeCIDSet(doc, cidSetStream)
			if r.Declined() {
				continue // an unread CIDSet lists nothing; the producer recorded the trip (C47)
			}
			missing := false
			for _, s := range u.Strings {
				for _, code := range cmap.Decode([]byte(s)) {
					if code.Mapped && code.CID != 0 && !present.Has(code.CID) {
						missing = true
					}
				}
			}
			if missing {
				errs = append(errs, Violation{
					Rule:    rule,
					Level:   level,
					Message: "FontDescriptor CIDSet does not list all CIDs used for rendering",
					Object:  u.ObjNum,
				})
			}
		}
	}
	return errs
}

// descriptorSymbolic reports the descriptor's Symbolic flag
// (fontcheck.Symbolic).
func descriptorSymbolic(doc core.View, fd *object.Dictionary) bool {
	return fontcheck.Symbolic(doc, fd)
}

// usedGlyphMissing reports whether any shown glyph name is absent from the
// listed CharSet names.
func usedGlyphMissing(u *core.FontTextUsage, enc map[byte]string, listed map[string]bool) bool {
	for _, s := range u.Strings {
		for _, code := range s {
			name := enc[code]
			if name == "" || name == ".notdef" {
				continue
			}
			if !listed[name] {
				return true
			}
		}
	}
	return false
}

// checkCMapCIDLimit verifies that no character identifier defined by an
// embedded CMap exceeds 65535 (ISO 19005-1 6.1.12, -2/-3 6.1.13; the CID is
// a 16-bit value per ISO 32000-1 9.7.4).
func checkCMapCIDLimit(doc core.View, level Level) []Violation {
	if level.Part() == 4 {
		return nil // PDF/A-4 has no implementation-limits clause
	}
	rule := "6.1.12"
	if level.Part() == 2 || level.Part() == 3 {
		rule = "6.1.13"
	}
	var errs []Violation
	seen := map[int]bool{}
	for _, r := range doc.ReachableDicts() {
		fontDict, num := r.Dict, r.ObjNum
		if r.Stream != nil {
			continue
		}
		if st, _ := doc.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
			continue
		}
		enc, ok := doc.Resolve(fontDict.Get("Encoding")).(*object.Stream)
		if !ok {
			continue
		}
		data, _ := doc.Content(enc) // reason: presence-only; the producer recorded any declined trip
		if data == nil {
			continue
		}
		if core.CMapMaxCID(doc.Cancel, data) > 65535 && !seen[num] {
			seen[num] = true
			errs = append(errs, Violation{
				Rule:    rule,
				Level:   level,
				Message: "a character identifier (CID) defined in the CMap exceeds the maximum value 65535",
				Object:  num,
			})
		}
	}
	return errs
}

// subsetTagged reports whether a font name begins with a subset tag: six
// capital letters and a plus sign (ISO 32000-1 9.6.4).
func subsetTagged(name string) bool {
	if len(name) < 7 || name[6] != '+' {
		return false
	}
	for i := 0; i < 6; i++ {
		if name[i] < 'A' || name[i] > 'Z' {
			return false
		}
	}
	return true
}

// checkCIDSetMatchesProgram enforces ISO 19005-2/-3 6.2.11.4.2 for a subset
// CIDFont that carries a /CIDSet: the set "shall identify all CIDs which are
// present in the font program, regardless of whether a CID in the font is
// referenced or used by the PDF or not" — every CID the program has is in the
// set, and every CID in the set is one the program has.
//
// What a program has is read as veraPDF reads it (its GFPDCIDFont rule, over
// its font parser's list of a program's CIDs), since what the CIDs of a
// program are depends on how it is keyed:
//
//   - a TrueType program (CIDFontType2) through an Identity CIDToGIDMap has
//     the CIDs 0 to one less than the long metrics its hmtx states, empty
//     glyphs included; through a mapping stream, the CIDs the stream maps
//     to a glyph the program declares. A CID in the set has to map to a
//     glyph the program declares (maxp);
//   - a CID-keyed CFF has the CIDs its charset names;
//   - a CFF that is not CID-keyed has none, so a set that names any CID but
//     0 names one the program lacks.
//
// CID 0, .notdef, is neither required nor checked. PDF/A-1 checks the CIDs
// shown instead (checkCIDSetProgramComplete), and PDF/A-4 drops the rule.
func checkCIDSetMatchesProgram(doc core.View, level Level) []Violation {
	if p := level.Part(); p != 2 && p != 3 {
		return nil
	}
	var errs []Violation
	for fontDict := range core.CollectFontTextUsage(doc) {
		if st, _ := doc.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
			continue
		}
		desc := core.Type0Descendant(doc, fontDict)
		if desc == nil {
			continue
		}
		if name, _ := doc.ResolveName(desc.Get("BaseFont")); !subsetTagged(string(name)) {
			continue
		}
		fd := doc.ResolveDict(desc.Get("FontDescriptor"))
		if fd == nil {
			continue
		}
		cidSetStream, ok := doc.Resolve(fd.Get("CIDSet")).(*object.Stream)
		if !ok {
			continue
		}
		set, r := core.DecodeCIDSet(doc, cidSetStream)
		if r.Declined() {
			continue // an unread CIDSet lists nothing; the producer recorded the trip (C47)
		}
		cids, has, ok := programCIDs(doc, desc, fd)
		if !ok {
			continue // a program this cannot read is not one it can hold the set to
		}
		num := 0
		if ir, ok := doc.Resolve(fontDict.Get("DescendantFonts")).(object.Array); ok && len(ir) > 0 {
			num = resolveObjNum(doc, ir[0])
		}
		missing, extra := -1, -1
		for _, cid := range cids {
			if cid != 0 && !set.Has(cid) {
				missing = cid
				break
			}
		}
		for cid := 1; cid < 8*len(set); cid++ {
			if set.Has(cid) && !has(cid) {
				extra = cid
				break
			}
		}
		switch {
		case missing >= 0:
			errs = append(errs, Violation{Rule: subsetRule(level), Level: level, Object: num,
				Message: fmt.Sprintf("the CIDSet does not list CID %d, which the embedded font program has", missing)})
		case extra >= 0:
			errs = append(errs, Violation{Rule: subsetRule(level), Level: level, Object: num,
				Message: fmt.Sprintf("the CIDSet lists CID %d, which the embedded font program does not have", extra)})
		}
	}
	return errs
}

// programCIDs is the CIDs a CIDFont's embedded program has, as
// checkCIDSetMatchesProgram reads them, and a test for one; ok is false for a
// program it cannot read or a kind it does not judge.
func programCIDs(doc core.View, desc, fd *object.Dictionary) (cids []int, has func(int) bool, ok bool) {
	none := func(int) bool { return false }
	switch sub, _ := doc.ResolveName(desc.Get("Subtype")); sub {
	case "CIDFontType2":
		s, isStream := doc.Resolve(fd.Get("FontFile2")).(*object.Stream)
		if !isStream {
			return nil, nil, false
		}
		data, r := doc.Content(s)
		if r != core.ReasonOK {
			return nil, nil, false
		}
		tables := font.SFNTTables(data)
		maxp, hhea := tables["maxp"], tables["hhea"]
		if len(maxp) < 6 || len(hhea) < 36 {
			return nil, nil, false
		}
		numGlyphs := int(binary.BigEndian.Uint16(maxp[4:]))
		longMetrics := int(binary.BigEndian.Uint16(hhea[34:]))
		switch m := doc.Resolve(desc.Get("CIDToGIDMap")).(type) {
		case *object.Stream:
			gids, r := doc.Content(m)
			if r != core.ReasonOK {
				return nil, nil, false
			}
			has = func(cid int) bool {
				return cid != 0 && 2*cid+1 < len(gids) && int(binary.BigEndian.Uint16(gids[2*cid:])) < numGlyphs
			}
			for cid := 0; cid < len(gids)/2; cid++ {
				if has(cid) {
					cids = append(cids, cid)
				}
			}
			return cids, has, true
		case object.Name, nil:
			if m != nil && m != object.Name("Identity") {
				return nil, nil, false
			}
			for cid := 0; cid < longMetrics; cid++ {
				cids = append(cids, cid)
			}
			return cids, func(cid int) bool { return cid != 0 && cid < numGlyphs }, true
		}
		return nil, nil, false
	case "CIDFontType0":
		s, isStream := doc.Resolve(fd.Get("FontFile3")).(*object.Stream)
		if !isStream {
			return nil, nil, false
		}
		data, r := doc.Content(s)
		if r != core.ReasonOK {
			return nil, nil, false
		}
		if st, _ := doc.ResolveName(s.Dict.Get("Subtype")); st == "OpenType" {
			data = font.SFNTTables(data)["CFF "]
		}
		cff := font.ParseCFF(data)
		if cff == nil {
			return nil, nil, false
		}
		if cff.GIDToCID == nil {
			return nil, none, true // not CID-keyed: no CIDs of its own
		}
		charset := map[int]bool{}
		for _, cid := range cff.GIDToCID {
			charset[cid] = true
			cids = append(cids, cid)
		}
		return cids, func(cid int) bool { return cid != 0 && charset[cid] }, true
	}
	return nil, nil, false
}

// checkCIDSetProgramComplete enforces the stricter PDF/A-1 subset rule
// (ISO 19005-1 6.3.5): a CIDFont subset's CIDSet must be present, non-empty,
// and enumerate every CID whose glyph is present in the embedded font
// program. (PDF/A-2/-3 only require the CIDSet to cover the CIDs actually
// used for rendering, handled by checkFontSubsetCompleteness.)
func checkCIDSetProgramComplete(doc core.View, level Level) []Violation {
	if level.Part() != 1 {
		return nil
	}
	catalog := doc.Catalog()
	if catalog == nil {
		return nil
	}
	var errs []Violation
	for fontDict, u := range core.CollectFontTextUsage(doc) {
		if st, _ := doc.ResolveName(fontDict.Get("Subtype")); st != "Type0" {
			continue
		}
		if !rendersVisibly(u) {
			continue // invisible text is exempt
		}
		desc := core.Type0Descendant(doc, fontDict)
		if desc == nil {
			continue
		}
		fd := doc.ResolveDict(desc.Get("FontDescriptor"))
		if fd == nil {
			continue
		}
		cidSetStream, ok := doc.Resolve(fd.Get("CIDSet")).(*object.Stream)
		if !ok {
			continue // presence is checked by checkFontSubsets
		}
		fp, _ := core.LoadFontProgram(doc, fd) // reason: a nil program skips the check; the producer recorded any declined trip
		if fp == nil {
			continue
		}
		present, r := core.DecodeCIDSet(doc, cidSetStream)
		if r.Declined() {
			// Not read: an unread CIDSet is not an empty one (audit 2026-09-22
			// C47). The producer recorded the trip.
			continue
		}
		num := 0
		if ir, ok := doc.Resolve(fontDict.Get("DescendantFonts")).(object.Array); ok && len(ir) > 0 {
			num = resolveObjNum(doc, ir[0])
		}
		// An empty CIDSet on a visibly-rendered CIDFont subset that has
		// glyphs is a clear violation. (Enumeration completeness beyond
		// emptiness is not reliably decidable from the program alone —
		// CIDToGIDMap Identity fonts legitimately omit unused CIDs — so only
		// emptiness is flagged here.)
		if present.Empty() && fp.NumGlyphs > 1 {
			errs = append(errs, Violation{Rule: "6.3.5", Level: level,
				Message: "CIDFont subset FontDescriptor contains an empty CIDSet stream", Object: num})
			continue
		}
		// The CIDSet must enumerate every glyph present in the embedded font
		// program (ISO 19005-1 6.3.5). With an Identity CIDToGIDMap the CID
		// equals the glyph index, so a present (non-empty) glyph whose bit is
		// clear is a violation. Empty glyphs are not counted as present.
		missing := false
		if fp.CIDGIDs != nil {
			// CFF CIDFont: the charset enumerates the CIDs actually present.
			for cid := range fp.CIDGIDs {
				if cid != 0 && !present.Has(cid) {
					missing = true
					break
				}
			}
		} else if cidSub, _ := doc.ResolveName(desc.Get("Subtype")); cidSub == "CIDFontType2" && fp.GlyphNonEmpty != nil {
			// CIDFontType2: a CID's glyph is present when the glyph its
			// CIDToGIDMap selects has an outline, and every such CID must be
			// listed. With an Identity map that is every non-empty glyph
			// index; with a stream map it is every CID the map sends to one
			// (the same glyph selection the width and existence checks use).
			// A map pdf0 could not read names no glyph, and nothing is said.
			switch gmap := core.LoadCIDGlyphMap(doc, desc, cidSub); {
			case !gmap.Stream():
				for gid, ne := range fp.GlyphNonEmpty {
					if ne && !present.Has(gid) {
						missing = true
						break
					}
				}
			case gmap.Readable():
				for cid := 1; cid < gmap.StreamCIDs(); cid++ {
					if gid, _ := gmap.Glyph(cid); gid > 0 && gid < len(fp.GlyphNonEmpty) && fp.GlyphNonEmpty[gid] && !present.Has(cid) {
						missing = true
						break
					}
				}
			}
		}
		if missing {
			errs = append(errs, Violation{Rule: "6.3.5", Level: level,
				Message: "CIDSet does not list all glyphs present in the embedded font program", Object: num})
		}
	}
	return errs
}

// isGlyphWhitespace reports whether a Unicode scalar value is whitespace, for
// which an empty (outline-less) glyph is legitimate.
func isGlyphWhitespace(r rune) bool {
	return unicode.IsSpace(r) || r == 0x200B || r == 0xFEFF
}
