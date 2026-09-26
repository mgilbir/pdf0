package core

import (
	"strings"
	"unicode/utf16"

	"github.com/mgilbir/pdf0/object"
)

// FontCodes is how a Type 0 font's shown strings are cut into character codes,
// for the readers that need the codes and not the CIDs: text extraction, which
// looks each code up in the font's ToUnicode map, and the PDF/A Level A scan
// for Private Use Area characters, which does the same.
//
// Both used to cut every Type 0 string into two-byte codes. That is right for
// Identity-H and nothing else: a Shift-JIS or EUC CMap mixes one- and two-byte
// codes, and a two-byte cut reads every string after its first one-byte
// character out of step (audit 2026-09-22 C88). The CMap says how to cut, and
// this reads the part of it that does.
type FontCodes struct {
	cmap *CMap
	// unicode is set for the predefined Uni*-UCS2 and Uni*-UTF16 CMaps, whose
	// codes are UTF-16 code units: a code is its own Unicode value.
	unicode bool
}

// LoadFontCodes returns the code cutter for a Type 0 font, and false when the
// font's /Encoding gives no way to cut its strings.
//
// It prefers LoadCMap's full CMap — Identity, or an embedded one it can read,
// including one that builds on a CMap whose mapping is not carried (whose
// codespace comes from the table in cmap_predefined.go). A predefined CMap
// named directly still has a codespace from that table, and an embedded CMap
// that LoadCMap refuses still declares its own codespace ranges, to which the
// named parent's are added. Either is enough to cut codes, though not to map
// them to CIDs, and the returned value is not a CMap a caller could ask for
// CIDs.
//
// Cutting a code is not a skipped check, so the returned cutter never records
// the "codes left to a CMap not carried" trip LoadCMap's CMap records when a
// check meets one.
func LoadFontCodes(doc View, fontDict *object.Dictionary) (FontCodes, bool) {
	switch e := doc.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		// A name is answered here rather than through LoadCMap: a predefined
		// CMap's codespace is carried, so cutting its codes is not a skip, and
		// LoadCMap would report the missing code-to-CID data as one.
		if e == "Identity-H" || e == "Identity-V" {
			return FontCodes{cmap: IdentityCMap()}, true
		}
		ranges, ok := predefinedCodespaces[string(e)]
		if !ok {
			return FontCodes{}, false
		}
		name := string(e)
		uni := strings.HasPrefix(name, "Uni") && (strings.Contains(name, "-UCS2-") || strings.Contains(name, "-UTF16-"))
		return FontCodes{cmap: &CMap{codespace: ranges}, unicode: uni}, true
	case *object.Stream:
		if c, r := LoadCMap(doc, fontDict); r == ReasonOK {
			c.onUnknown = nil
			return FontCodes{cmap: c}, true
		}
		// Through the same producer: a declined decode is recorded there.
		data, r := doc.Content(e)
		if r != ReasonOK {
			return FontCodes{}, false
		}
		// The codespace ranges alone, read by the same token-level CMap
		// reader, plus those of the CMap it names as its base.
		c := &CMap{}
		scanCMap(doc.Cancel, data, cmapVisitor{
			codespace: func(lo, hi cmapCode) bool {
				c.codespace = append(c.codespace, codespaceRange{bytes: lo.n, lo: lo.v, hi: hi.v})
				return len(c.codespace) <= maxCMapEntries
			},
			useCMap: func(name string) {
				c.codespace = append(c.codespace, predefinedCodespaces[name]...)
			},
		})
		if parent, ok := doc.ResolveName(e.Dict.Get("UseCMap")); ok {
			c.codespace = append(c.codespace, predefinedCodespaces[string(parent)]...)
		}
		if len(c.codespace) == 0 {
			return FontCodes{}, false
		}
		return FontCodes{cmap: c}, true
	}
	return FontCodes{}, false
}

// TwoByteFontCodes cuts every string into two-byte codes, as Identity-H does.
// It is the guess for a Type 0 font whose /Encoding gives no codespace at all
// — a name that is not a CMap, a stream that declares none — and it is only a
// guess: right for the Identity encodings that are most of what is written,
// wrong for anything else, which is why LoadFontCodes is asked first.
func TwoByteFontCodes() FontCodes { return FontCodes{cmap: IdentityCMap()} }

// Codes cuts a shown string into codes. A zero FontCodes cuts nothing.
func (f FontCodes) Codes(s []byte) []Code {
	return f.cmap.Decode(s)
}

// Unicode is the text a code stands for when the CMap itself says — the
// UTF-16 codes of a predefined Uni* CMap — and false otherwise. A two-byte
// code is one UTF-16 unit; a four-byte one is a surrogate pair.
func (f FontCodes) Unicode(c Code) ([]rune, bool) {
	if !f.unicode {
		return nil, false
	}
	switch c.Bytes {
	case 2:
		if utf16.IsSurrogate(rune(c.Value)) {
			return nil, false
		}
		return []rune{rune(c.Value)}, true
	case 4:
		r := utf16.DecodeRune(rune(c.Value>>16), rune(c.Value&0xFFFF))
		if r == 0xFFFD {
			return nil, false
		}
		return []rune{r}, true
	}
	return nil, false
}
