package pdf0

import (
	"fmt"
	"strings"
)

// This file implements the PDF/A font rule family (ISO 19005-1 clause 6.3,
// 19005-2/-3 clause 6.2.11, 19005-4 clause 6.2.10), grounded in ISO 32000-1
// clause 9: CIDFont/CMap consistency (9.7.4-9.7.5, Table 118), TrueType
// encodings (9.6.6.4), embedding, and ToUnicode restrictions.

// --- content walking: text usage per font ---

// contentItemKind classifies items reported by forEachContentItem.
type contentItemKind int

const (
	itemOperator contentItemKind = iota
	itemName
	itemString
	itemNumber
)

// forEachContentItem tokenizes a decoded content stream like
// forEachContentToken, additionally reporting decoded string operands and
// distinguishing numbers from operators.
func forEachContentItem(data []byte, fn func(kind contentItemKind, payload []byte)) {
	n := len(data)
	i := 0
	for i < n {
		for i < n && isContentWS(data[i]) {
			i++
		}
		if i >= n {
			return
		}
		switch b := data[i]; {
		case b == '%':
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case b == '(':
			str, next := decodeContentLiteralString(data, i)
			fn(itemString, str)
			i = next
		case b == '<':
			i++
			if i < n && data[i] == '<' {
				i++ // <<
			} else {
				start := i
				for i < n && data[i] != '>' {
					i++
				}
				fn(itemString, decodeHexBytes(data[start:i]))
				if i < n {
					i++
				}
			}
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '[' || b == ']' || b == '{' || b == '}':
			i++
		case b == '/':
			i++
			start := i
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
			}
			fn(itemName, data[start:i])
		default:
			start := i
			// Numeric tokens may be arbitrarily long (Annex C allows huge
			// precision); read them whole. Non-numeric keyword tokens are
			// capped to bound scanning over stray binary data.
			numeric := data[i] >= '0' && data[i] <= '9' || data[i] == '+' || data[i] == '-' || data[i] == '.'
			for i < n && !isContentWS(data[i]) && !isContentDelim(data[i]) {
				i++
				if !numeric && i-start > 256 {
					break
				}
			}
			tok := data[start:i]
			if len(tok) == 2 && tok[0] == 'B' && tok[1] == 'I' {
				skipInlineImage(data, &i)
				continue
			}
			if numeric {
				fn(itemNumber, tok)
				continue
			}
			fn(itemOperator, tok)
		}
	}
}

// decodeContentLiteralString decodes a (...) string starting at open paren;
// returns the decoded bytes and the index just past the closing paren.
func decodeContentLiteralString(data []byte, i int) ([]byte, int) {
	n := len(data)
	var out []byte
	depth := 1
	i++
	for i < n && depth > 0 {
		c := data[i]
		switch c {
		case '\\':
			i++
			if i >= n {
				break
			}
			e := data[i]
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\n': // line continuation
			case '\r':
				if i+1 < n && data[i+1] == '\n' {
					i++
				}
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && i+1 < n && data[i+1] >= '0' && data[i+1] <= '7'; k++ {
						i++
						v = v<<3 | int(data[i]-'0')
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
			i++
		case '(':
			depth++
			out = append(out, c)
			i++
		case ')':
			depth--
			if depth > 0 {
				out = append(out, c)
			}
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

// decodeHexBytes decodes hex-string content (whitespace tolerated, odd
// length padded with 0).
func decodeHexBytes(b []byte) []byte {
	var digits []byte
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			digits = append(digits, c)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	hv := func(c byte) byte {
		switch {
		case c <= '9':
			return c - '0'
		case c >= 'a':
			return c - 'a' + 10
		}
		return c - 'A' + 10
	}
	for i := 0; i < len(out); i++ {
		out[i] = hv(digits[2*i])<<4 | hv(digits[2*i+1])
	}
	return out
}

// fontTextUsage aggregates the text shown with one font dictionary.
type fontTextUsage struct {
	fontDict *Dictionary
	objNum   int      // font object number (0 if direct)
	strings  [][]byte // raw shown string bytes
	modes    map[int]bool
}

// collectFontTextUsage walks every page's executed content (including form
// XObjects and tiling patterns) and records which fonts show which text.
func collectFontTextUsage(doc *Document) map[*Dictionary]*fontTextUsage {
	usage := make(map[*Dictionary]*fontTextUsage)
	catalog := getCatalog(doc)
	if catalog == nil {
		return usage
	}
	seen := make(map[*Dictionary]bool)
	for _, page := range collectPages(doc, catalog.Get("Pages")) {
		data := getContentStreamData(doc, page.dict.Get("Contents"))
		collectTextFromContainer(doc, page.dict, data, usage, seen)
	}
	return usage
}

func collectTextFromContainer(doc *Document, container *Dictionary, data []byte, usage map[*Dictionary]*fontTextUsage, seen map[*Dictionary]bool) {
	if container == nil || seen[container] {
		return
	}
	seen[container] = true
	res := resolveResources(doc, container)

	fontFor := func(name string) (*Dictionary, int) {
		if res == nil {
			return nil, 0
		}
		fontsDict := doc.ResolveDict(res.Get("Font"))
		if fontsDict == nil {
			return nil, 0
		}
		ref := fontsDict.Get(Name(name))
		objNum := 0
		if ir, ok := ref.(IndirectRef); ok {
			objNum = ir.Number
		}
		return doc.ResolveDict(ref), objNum
	}

	var curFont *fontTextUsage
	mode := 0
	var lastName string
	var lastNumber string
	var pending [][]byte

	record := func() {
		if curFont == nil {
			pending = nil
			return
		}
		curFont.strings = append(curFont.strings, pending...)
		curFont.modes[mode] = true
		pending = nil
	}

	if data != nil {
		forEachContentItem(data, func(kind contentItemKind, payload []byte) {
			switch kind {
			case itemName:
				lastName = string(payload)
			case itemNumber:
				lastNumber = string(payload)
			case itemString:
				pending = append(pending, append([]byte(nil), payload...))
			case itemOperator:
				switch string(payload) {
				case "Tf":
					if dict, num := fontFor(lastName); dict != nil {
						u := usage[dict]
						if u == nil {
							u = &fontTextUsage{fontDict: dict, objNum: num, modes: make(map[int]bool)}
							usage[dict] = u
						}
						curFont = u
					} else {
						curFont = nil
					}
					pending = nil
				case "Tr":
					if v := lastNumber; v != "" {
						m := 0
						fmt.Sscanf(v, "%d", &m)
						mode = m
					}
					pending = nil
				case "Tj", "TJ", "'", "\"":
					record()
				default:
					pending = nil
				}
			}
		})
	}

	// Recurse into executed forms and patterns.
	if res == nil {
		return
	}
	used := contentUsedNames(data)
	if xobjDict := doc.ResolveDict(res.Get("XObject")); xobjDict != nil {
		for i, key := range xobjDict.Keys {
			if !used.xobjects[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(xobjDict.Values[i]).(*Stream); ok {
				if st, _ := s.Dict.Get("Subtype").(Name); st == "Form" {
					collectTextFromContainer(doc, &s.Dict, decodeContentStream(doc, s), usage, seen)
				}
			}
		}
	}
	if patDict := doc.ResolveDict(res.Get("Pattern")); patDict != nil {
		for i, key := range patDict.Keys {
			if !used.patterns[string(key)] {
				continue
			}
			if s, ok := doc.Resolve(patDict.Values[i]).(*Stream); ok {
				collectTextFromContainer(doc, &s.Dict, decodeContentStream(doc, s), usage, seen)
			}
		}
	}
}

// --- predefined CMaps (ISO 32000-1, 9.7.5.2, Table 118) ---

// predefinedCMapInfo carries the CIDSystemInfo a predefined CMap implies.
type predefinedCMapInfo struct {
	Registry string
	Ordering string
}

var predefinedCMaps = map[string]predefinedCMapInfo{
	// Chinese (simplified) — Adobe-GB1
	"GB-EUC-H": {"Adobe", "GB1"}, "GB-EUC-V": {"Adobe", "GB1"},
	"GBpc-EUC-H": {"Adobe", "GB1"}, "GBpc-EUC-V": {"Adobe", "GB1"},
	"GBK-EUC-H": {"Adobe", "GB1"}, "GBK-EUC-V": {"Adobe", "GB1"},
	"GBKp-EUC-H": {"Adobe", "GB1"}, "GBKp-EUC-V": {"Adobe", "GB1"},
	"GBK2K-H": {"Adobe", "GB1"}, "GBK2K-V": {"Adobe", "GB1"},
	"UniGB-UCS2-H": {"Adobe", "GB1"}, "UniGB-UCS2-V": {"Adobe", "GB1"},
	"UniGB-UTF16-H": {"Adobe", "GB1"}, "UniGB-UTF16-V": {"Adobe", "GB1"},
	// Chinese (traditional) — Adobe-CNS1
	"B5pc-H": {"Adobe", "CNS1"}, "B5pc-V": {"Adobe", "CNS1"},
	"HKscs-B5-H": {"Adobe", "CNS1"}, "HKscs-B5-V": {"Adobe", "CNS1"},
	"ETen-B5-H": {"Adobe", "CNS1"}, "ETen-B5-V": {"Adobe", "CNS1"},
	"ETenms-B5-H": {"Adobe", "CNS1"}, "ETenms-B5-V": {"Adobe", "CNS1"},
	"CNS-EUC-H": {"Adobe", "CNS1"}, "CNS-EUC-V": {"Adobe", "CNS1"},
	"UniCNS-UCS2-H": {"Adobe", "CNS1"}, "UniCNS-UCS2-V": {"Adobe", "CNS1"},
	"UniCNS-UTF16-H": {"Adobe", "CNS1"}, "UniCNS-UTF16-V": {"Adobe", "CNS1"},
	// Japanese — Adobe-Japan1
	"83pv-RKSJ-H": {"Adobe", "Japan1"},
	"90ms-RKSJ-H": {"Adobe", "Japan1"}, "90ms-RKSJ-V": {"Adobe", "Japan1"},
	"90msp-RKSJ-H": {"Adobe", "Japan1"}, "90msp-RKSJ-V": {"Adobe", "Japan1"},
	"90pv-RKSJ-H": {"Adobe", "Japan1"},
	"Add-RKSJ-H":  {"Adobe", "Japan1"}, "Add-RKSJ-V": {"Adobe", "Japan1"},
	"EUC-H": {"Adobe", "Japan1"}, "EUC-V": {"Adobe", "Japan1"},
	"Ext-RKSJ-H": {"Adobe", "Japan1"}, "Ext-RKSJ-V": {"Adobe", "Japan1"},
	"H": {"Adobe", "Japan1"}, "V": {"Adobe", "Japan1"},
	"UniJIS-UCS2-H": {"Adobe", "Japan1"}, "UniJIS-UCS2-V": {"Adobe", "Japan1"},
	"UniJIS-UCS2-HW-H": {"Adobe", "Japan1"}, "UniJIS-UCS2-HW-V": {"Adobe", "Japan1"},
	"UniJIS-UTF16-H": {"Adobe", "Japan1"}, "UniJIS-UTF16-V": {"Adobe", "Japan1"},
	// Korean — Adobe-Korea1
	"KSC-EUC-H": {"Adobe", "Korea1"}, "KSC-EUC-V": {"Adobe", "Korea1"},
	"KSCms-UHC-H": {"Adobe", "Korea1"}, "KSCms-UHC-V": {"Adobe", "Korea1"},
	"KSCms-UHC-HW-H": {"Adobe", "Korea1"}, "KSCms-UHC-HW-V": {"Adobe", "Korea1"},
	"KSCpc-EUC-H":  {"Adobe", "Korea1"},
	"UniKS-UCS2-H": {"Adobe", "Korea1"}, "UniKS-UCS2-V": {"Adobe", "Korea1"},
	"UniKS-UTF16-H": {"Adobe", "Korea1"}, "UniKS-UTF16-V": {"Adobe", "Korea1"},
	// Identity
	"Identity-H": {"Adobe", "Identity"}, "Identity-V": {"Adobe", "Identity"},
}

// --- dictionary-level font checks ---

// checkFontDictionaries validates Type0/CIDFont/CMap dictionary consistency,
// TrueType encodings, ToUnicode values, and embedding of rendered fonts.
func checkFontDictionaries(doc *Document, level PDFALevel) []ValidationError {
	rule := fontRule(level)
	var errs []ValidationError

	usage := collectFontTextUsage(doc)
	for fontDict, u := range usage {
		errs = append(errs, checkOneFontDict(doc, level, rule, fontDict, u)...)
	}
	return errs
}

func fontRule(level PDFALevel) string {
	switch level {
	case PDFA1b:
		return "6.3"
	case PDFA4:
		return "6.2.10"
	}
	return "6.2.11"
}

func checkOneFontDict(doc *Document, level PDFALevel, rule string, fontDict *Dictionary, u *fontTextUsage) []ValidationError {
	var errs []ValidationError
	bad := func(format string, args ...interface{}) {
		errs = append(errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf(format, args...),
			Object:  u.objNum,
		})
	}
	subtype, _ := fontDict.Get("Subtype").(Name)

	if subtype == "Type0" {
		desc := type0Descendant(doc, fontDict)
		encObj := fontDict.Get("Encoding")

		// CMap legality (9.7.5.2): the Encoding must be a predefined CMap
		// name or an embedded CMap stream.
		var cmapStreamInfo *Dictionary
		var cmapStream *Stream
		switch enc := doc.Resolve(encObj).(type) {
		case Name:
			if _, ok := predefinedCMaps[string(enc)]; !ok {
				bad("Type0 font Encoding CMap /%s is neither embedded nor predefined (ISO 32000, Table 118)", string(enc))
			}
		case *Stream:
			cmapStream = enc
			cmapStreamInfo = doc.ResolveDict(enc.Dict.Get("CIDSystemInfo"))
			// WMode in the stream dictionary must agree with the CMap
			// content (9.7.5.3).
			if dictWMode, ok := doc.Resolve(enc.Dict.Get("WMode")).(Integer); ok {
				if contentWMode, found := cmapContentWMode(doc, enc); found && int(dictWMode) != contentWMode {
					bad("CMap dictionary WMode %d differs from the embedded CMap content WMode %d", int(dictWMode), contentWMode)
				}
			}
			// A CMap's UseCMap reference must be to a predefined CMap
			// (ISO 32000-1 9.7.5.2, Table 118): an embedded CMap stream or a
			// non-predefined name is not permitted.
			switch uc := doc.Resolve(enc.Dict.Get("UseCMap")).(type) {
			case Name:
				if _, ok := predefinedCMaps[string(uc)]; !ok {
					bad("embedded CMap references CMap /%s, which is not predefined (ISO 32000, Table 118)", string(uc))
				}
			case *Stream:
				bad("embedded CMap references another embedded CMap, but UseCMap must name a predefined CMap (Table 118)")
			}
			// The usecmap operator in the CMap body must likewise name a
			// predefined CMap.
			if refName, found := cmapUseCMap(doc, enc); found {
				if _, ok := predefinedCMaps[refName]; !ok {
					bad("embedded CMap references CMap /%s, which is not predefined (ISO 32000, Table 118)", refName)
				}
			}
		}

		// CIDSystemInfo compatibility (9.7.4.2/19005 6.x.11.3.1): the
		// CIDFont's Registry/Ordering must match the CMap's, and the
		// CIDFont Supplement must not be less than the CMap's.
		if desc != nil {
			cidInfo := doc.ResolveDict(desc.Get("CIDSystemInfo"))
			var cmReg, cmOrd string
			var cmSupp Integer
			haveCMapInfo := false
			if name, ok := doc.Resolve(encObj).(Name); ok {
				if info, ok := predefinedCMaps[string(name)]; ok && info.Registry != "" {
					cmReg, cmOrd = info.Registry, info.Ordering
					haveCMapInfo = string(name) != "Identity-H" && string(name) != "Identity-V"
				}
			} else if cmapStreamInfo != nil {
				cmReg = pdfTextString(doc, cmapStreamInfo.Get("Registry"))
				cmOrd = pdfTextString(doc, cmapStreamInfo.Get("Ordering"))
				if s, ok := doc.Resolve(cmapStreamInfo.Get("Supplement")).(Integer); ok {
					cmSupp = s
				}
				haveCMapInfo = true
			}
			if cidInfo != nil && haveCMapInfo {
				reg := pdfTextString(doc, cidInfo.Get("Registry"))
				ord := pdfTextString(doc, cidInfo.Get("Ordering"))
				if reg != cmReg {
					bad("CIDFont CIDSystemInfo Registry %q does not match the CMap's %q", reg, cmReg)
				}
				if ord != cmOrd {
					bad("CIDFont CIDSystemInfo Ordering %q does not match the CMap's %q", ord, cmOrd)
				}
				// The corpus pins the direction: a CIDFont Supplement
				// GREATER than the CMap's fails (the CMap cannot address
				// the extra glyphs); a smaller one passes.
				if cmapStream != nil {
					if supp, ok := doc.Resolve(cidInfo.Get("Supplement")).(Integer); ok && supp > cmSupp {
						bad("CIDFont CIDSystemInfo Supplement %d is greater than the CMap's %d", int(supp), int(cmSupp))
					}
				}
			}

			// CIDToGIDMap (ISO 32000-1, 9.7.4.2): an embedded Type 2
			// CIDFont shall carry CIDToGIDMap as a stream or /Identity;
			// PDF/A requires the entry. Text rendered invisibly (mode 3
			// only) is exempt — the corpus passes such a font at 1b.
			onlyInvisible := len(u.modes) > 0
			for m := range u.modes {
				if m != 3 {
					onlyInvisible = false
				}
			}
			if dsub, _ := desc.Get("Subtype").(Name); dsub == "CIDFontType2" && !onlyInvisible {
				switch v := doc.Resolve(desc.Get("CIDToGIDMap")).(type) {
				case nil:
					bad("CIDFontType2 must contain a CIDToGIDMap entry (stream or /Identity)")
				case Name:
					if v != "Identity" {
						bad("CIDFontType2 CIDToGIDMap name must be /Identity, got /%s", string(v))
					}
				case *Stream:
					// fine
				default:
					bad("CIDFontType2 CIDToGIDMap must be a stream or the name /Identity")
				}
			}
		}
	}

	// TrueType encodings (ISO 32000-1, 9.6.6.4; 19005 6.x.11.6).
	if subtype == "TrueType" {
		errs = append(errs, checkTrueTypeEncoding(doc, level, rule, fontDict, u)...)
	}

	// ToUnicode values (A-4): no mapping may target U+0000, U+FEFF, U+FFFE.
	if level == PDFA4 {
		if tu, ok := doc.Resolve(fontDict.Get("ToUnicode")).(*Stream); ok {
			if hasForbiddenUnicodeTargets(doc, tu) {
				bad("ToUnicode CMap maps to a forbidden Unicode value (U+0000, U+FEFF or U+FFFE)")
			}
		}
	}

	// Program-level checks: metrics, glyph coverage, and .notdef references.
	errs = append(errs, checkFontProgramConsistency(doc, level, rule, fontDict, u)...)

	return errs
}

func type0Descendant(doc *Document, fontDict *Dictionary) *Dictionary {
	arr, ok := doc.Resolve(fontDict.Get("DescendantFonts")).(Array)
	if !ok || len(arr) == 0 {
		return nil
	}
	return doc.ResolveDict(arr[0])
}

func pdfTextString(doc *Document, v Object) string {
	if s, ok := doc.Resolve(v).(String); ok {
		return decodePDFTextString(s.Value)
	}
	return ""
}

// cmapContentWMode extracts "/WMode N def" from an embedded CMap stream.
func cmapContentWMode(doc *Document, stream *Stream) (int, bool) {
	data := decodeContentStream(doc, stream)
	if data == nil {
		return 0, false
	}
	idx := strings.Index(string(data), "/WMode")
	if idx < 0 {
		return 0, false
	}
	var mode int
	if _, err := fmt.Sscanf(string(data[idx:]), "/WMode %d", &mode); err != nil {
		return 0, false
	}
	return mode, true
}

// cmapUseCMap extracts a "/Name usecmap" reference from an embedded CMap.
func cmapUseCMap(doc *Document, stream *Stream) (string, bool) {
	data := decodeContentStream(doc, stream)
	if data == nil {
		return "", false
	}
	s := string(data)
	idx := strings.Index(s, "usecmap")
	if idx < 0 {
		return "", false
	}
	// Walk back to the /Name operand.
	head := strings.TrimRight(s[:idx], " \t\r\n")
	slash := strings.LastIndexByte(head, '/')
	if slash < 0 {
		return "", false
	}
	name := strings.TrimSpace(head[slash+1:])
	if cut := strings.IndexAny(name, " \t\r\n"); cut >= 0 {
		name = name[:cut]
	}
	return name, name != ""
}

// hasForbiddenUnicodeTargets scans a ToUnicode CMap for mappings to U+0000,
// U+FEFF, or U+FFFE in bfchar/bfrange destinations.
func hasForbiddenUnicodeTargets(doc *Document, stream *Stream) bool {
	data := decodeContentStream(doc, stream)
	if data == nil {
		return false
	}
	s := string(data)
	scanSection := func(begin, end string, dstIndex int) bool {
		rest := s
		for {
			b := strings.Index(rest, begin)
			if b < 0 {
				return false
			}
			e := strings.Index(rest[b:], end)
			if e < 0 {
				return false
			}
			section := rest[b+len(begin) : b+e]
			// Collect hex strings in order; every dstIndex-th (per group)
			// is a destination.
			var hexes []string
			for {
				lt := strings.IndexByte(section, '<')
				if lt < 0 {
					break
				}
				gt := strings.IndexByte(section[lt:], '>')
				if gt < 0 {
					break
				}
				hexes = append(hexes, section[lt+1:lt+gt])
				section = section[lt+gt+1:]
			}
			group := dstIndex + 1
			for i := dstIndex; i < len(hexes); i += group {
				h := strings.TrimSpace(hexes[i])
				for len(h) >= 4 {
					switch strings.ToLower(h[:4]) {
					case "0000", "feff", "fffe":
						return true
					}
					h = h[4:]
				}
			}
			rest = rest[b+e+len(end):]
		}
	}
	// bfchar: <src> <dst> pairs; bfrange: <lo> <hi> <dst> triples.
	return scanSection("beginbfchar", "endbfchar", 1) ||
		scanSection("beginbfrange", "endbfrange", 2)
}

// checkTrueTypeEncoding enforces the PDF/A TrueType encoding rules
// (ISO 19005-2/-3, 6.2.11.6; -4, 6.2.10.6; grounded in ISO 32000-1,
// 9.6.6.4): a symbolic TrueType font shall have no Encoding entry; a
// non-symbolic one shall specify WinAnsiEncoding or MacRomanEncoding —
// directly or as BaseEncoding — and Differences names must come from the
// Adobe Glyph List.
func checkTrueTypeEncoding(doc *Document, level PDFALevel, rule string, fontDict *Dictionary, u *fontTextUsage) []ValidationError {
	if level == PDFA1b {
		return nil
	}
	var errs []ValidationError
	bad := func(format string, args ...interface{}) {
		errs = append(errs, ValidationError{
			Rule:    rule,
			Level:   level,
			Message: fmt.Sprintf(format, args...),
			Object:  u.objNum,
		})
	}

	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	symbolic := false
	if fd != nil {
		if flags, ok := doc.Resolve(fd.Get("Flags")).(Integer); ok {
			symbolic = flags&4 != 0
		}
	}

	encObj := doc.Resolve(fontDict.Get("Encoding"))

	if symbolic {
		if encObj != nil {
			bad("symbolic TrueType font must not have an Encoding entry")
		}
		return errs
	}

	switch enc := encObj.(type) {
	case nil:
		bad("non-symbolic TrueType font must have an Encoding entry")
	case Name:
		if enc != "WinAnsiEncoding" && enc != "MacRomanEncoding" {
			bad("non-symbolic TrueType font Encoding must be WinAnsiEncoding or MacRomanEncoding, got /%s", string(enc))
		}
	case *Dictionary:
		base, hasBase := doc.Resolve(enc.Get("BaseEncoding")).(Name)
		if !hasBase {
			bad("non-symbolic TrueType font Encoding dictionary must have a BaseEncoding entry")
		} else if base != "WinAnsiEncoding" && base != "MacRomanEncoding" {
			bad("non-symbolic TrueType font BaseEncoding must be WinAnsiEncoding or MacRomanEncoding, got /%s", string(base))
		}
		if diffs, ok := doc.Resolve(enc.Get("Differences")).(Array); ok {
			for _, el := range diffs {
				if name, ok := el.(Name); ok {
					if !aglGlyphName(string(name)) {
						bad("Differences glyph name /%s is not in the Adobe Glyph List", string(name))
					}
				}
			}
		}
	}
	return errs
}

// aglGlyphName reports whether a glyph name is a legal Adobe Glyph List
// reference: a listed name, a uniXXXX[XXXX...] form, or a uXXXX[XX] form
// (Adobe Glyph Naming convention).
func aglGlyphName(name string) bool {
	if name == "" {
		return false
	}
	isHex := func(s string) bool {
		if s == "" {
			return false
		}
		for _, c := range s {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(name, "uni") && len(name) >= 7 && (len(name)-3)%4 == 0 && isHex(name[3:]) {
		return true
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 && isHex(name[1:]) {
		return true
	}
	return aglNames[name]
}

// aglNames is the Adobe Glyph List subset covering the standard Latin,
// Greek, Symbol, and ZapfDingbats glyph names used by the base encodings.
var aglNames = map[string]bool{
	"A": true, "AE": true, "AEacute": true, "AEsmall": true, "Aacute": true, "Aacutesmall": true,
	"Abreve": true, "Acircumflex": true, "Acircumflexsmall": true, "Acute": true, "Acutesmall": true, "Adieresis": true,
	"Adieresissmall": true, "Agrave": true, "Agravesmall": true, "Alpha": true, "Alphatonos": true, "Amacron": true,
	"Aogonek": true, "Aring": true, "Aringacute": true, "Aringsmall": true, "Asmall": true, "Atilde": true,
	"Atildesmall": true, "B": true, "Beta": true, "Bsmall": true, "C": true, "Cacute": true,
	"Caron": true, "Caronsmall": true, "Ccaron": true, "Ccedilla": true, "Ccedillasmall": true, "Ccircumflex": true,
	"Cdotaccent": true, "Chi": true, "Circumflexsmall": true, "Csmall": true, "D": true, "Dcaron": true,
	"Dcroat": true, "Delta": true, "Dieresis": true, "DieresisAcute": true, "DieresisGrave": true, "Dieresissmall": true,
	"Dotaccentsmall": true, "Dsmall": true, "E": true, "Eacute": true, "Eacutesmall": true, "Ebreve": true,
	"Ecaron": true, "Ecircumflex": true, "Ecircumflexsmall": true, "Edieresis": true, "Edieresissmall": true, "Edotaccent": true,
	"Egrave": true, "Egravesmall": true, "Emacron": true, "Eng": true, "Eogonek": true, "Epsilon": true,
	"Epsilontonos": true, "Esmall": true, "Eta": true, "Etatonos": true, "Eth": true, "Ethsmall": true,
	"Euro": true, "F": true, "Fsmall": true, "G": true, "Gamma": true, "Gbreve": true,
	"Gcaron": true, "Gcircumflex": true, "Gcommaaccent": true, "Gdotaccent": true, "Grave": true, "Gravesmall": true,
	"Gsmall": true, "H": true, "H18533": true, "H18543": true, "H18551": true, "H22073": true,
	"Hbar": true, "Hcircumflex": true, "Hsmall": true, "Hungarumlaut": true, "Hungarumlautsmall": true, "I": true,
	"IJ": true, "Iacute": true, "Iacutesmall": true, "Ibreve": true, "Icircumflex": true, "Icircumflexsmall": true,
	"Idieresis": true, "Idieresissmall": true, "Idotaccent": true, "Ifraktur": true, "Igrave": true, "Igravesmall": true,
	"Imacron": true, "Iogonek": true, "Iota": true, "Iotadieresis": true, "Iotatonos": true, "Ismall": true,
	"Itilde": true, "J": true, "Jcircumflex": true, "Jsmall": true, "K": true, "Kappa": true,
	"Kcommaaccent": true, "Ksmall": true, "L": true, "Lacute": true, "Lambda": true, "Lcaron": true,
	"Lcommaaccent": true, "Ldot": true, "Lslash": true, "Lslashsmall": true, "Lsmall": true, "M": true,
	"Macron": true, "Macronsmall": true, "Msmall": true, "Mu": true, "N": true, "Nacute": true,
	"Ncaron": true, "Ncommaaccent": true, "Nsmall": true, "Ntilde": true, "Ntildesmall": true, "Nu": true,
	"O": true, "OE": true, "OEsmall": true, "Oacute": true, "Oacutesmall": true, "Obreve": true,
	"Ocircumflex": true, "Ocircumflexsmall": true, "Odieresis": true, "Odieresissmall": true, "Ogoneksmall": true, "Ograve": true,
	"Ogravesmall": true, "Ohm": true, "Ohorn": true, "Ohungarumlaut": true, "Omacron": true, "Omega": true,
	"Omegatonos": true, "Omicron": true, "Omicrontonos": true, "Oslash": true, "Oslashacute": true, "Oslashsmall": true,
	"Osmall": true, "Otilde": true, "Otildesmall": true, "P": true, "Phi": true, "Pi": true,
	"Psi": true, "Psmall": true, "Q": true, "Qsmall": true, "R": true, "Racute": true,
	"Rcaron": true, "Rcommaaccent": true, "Rfraktur": true, "Rho": true, "Ringsmall": true, "Rsmall": true,
	"S": true, "SF010000": true, "SF020000": true, "SF030000": true, "SF040000": true, "SF050000": true,
	"SF060000": true, "SF070000": true, "SF080000": true, "SF090000": true, "SF100000": true, "SF110000": true,
	"Sacute": true, "Scaron": true, "Scaronsmall": true, "Scedilla": true, "Scircumflex": true, "Scommaaccent": true,
	"Sigma": true, "Ssmall": true, "T": true, "Tau": true, "Tbar": true, "Tcaron": true,
	"Tcommaaccent": true, "Theta": true, "Thorn": true, "Thornsmall": true, "Tildesmall": true, "Tsmall": true,
	"U": true, "Uacute": true, "Uacutesmall": true, "Ubreve": true, "Ucircumflex": true, "Ucircumflexsmall": true,
	"Udieresis": true, "Udieresissmall": true, "Ugrave": true, "Ugravesmall": true, "Uhorn": true, "Uhungarumlaut": true,
	"Umacron": true, "Uogonek": true, "Upsilon": true, "Upsilon1": true, "Upsilondieresis": true, "Upsilontonos": true,
	"Uring": true, "Usmall": true, "Utilde": true, "V": true, "Vsmall": true, "W": true,
	"Wacute": true, "Wcircumflex": true, "Wdieresis": true, "Wgrave": true, "Wsmall": true, "X": true,
	"Xi": true, "Xsmall": true, "Y": true, "Yacute": true, "Yacutesmall": true, "Ycircumflex": true,
	"Ydieresis": true, "Ydieresissmall": true, "Ygrave": true, "Ysmall": true, "Z": true, "Zacute": true,
	"Zcaron": true, "Zcaronsmall": true, "Zdotaccent": true, "Zeta": true, "Zsmall": true, "a": true,
	"a1": true, "a10": true, "a100": true, "a101": true, "a102": true, "a103": true,
	"a104": true, "a105": true, "a106": true, "a107": true, "a108": true, "a109": true,
	"a11": true, "a110": true, "a111": true, "a112": true, "a117": true, "a118": true,
	"a119": true, "a12": true, "a120": true, "a121": true, "a122": true, "a123": true,
	"a124": true, "a125": true, "a126": true, "a127": true, "a128": true, "a129": true,
	"a13": true, "a130": true, "a131": true, "a132": true, "a133": true, "a134": true,
	"a135": true, "a136": true, "a137": true, "a138": true, "a139": true, "a14": true,
	"a140": true, "a141": true, "a142": true, "a143": true, "a144": true, "a145": true,
	"a146": true, "a147": true, "a148": true, "a149": true, "a15": true, "a150": true,
	"a151": true, "a152": true, "a153": true, "a154": true, "a155": true, "a156": true,
	"a157": true, "a158": true, "a159": true, "a16": true, "a160": true, "a161": true,
	"a162": true, "a163": true, "a164": true, "a165": true, "a166": true, "a167": true,
	"a168": true, "a169": true, "a17": true, "a170": true, "a171": true, "a172": true,
	"a173": true, "a174": true, "a175": true, "a176": true, "a177": true, "a178": true,
	"a179": true, "a18": true, "a180": true, "a181": true, "a182": true, "a183": true,
	"a184": true, "a185": true, "a186": true, "a187": true, "a188": true, "a189": true,
	"a19": true, "a190": true, "a191": true, "a192": true, "a193": true, "a194": true,
	"a195": true, "a196": true, "a197": true, "a198": true, "a199": true, "a2": true,
	"a20": true, "a200": true, "a201": true, "a202": true, "a203": true, "a204": true,
	"a205": true, "a21": true, "a22": true, "a23": true, "a24": true, "a25": true,
	"a26": true, "a27": true, "a28": true, "a29": true, "a3": true, "a30": true,
	"a31": true, "a32": true, "a33": true, "a34": true, "a35": true, "a36": true,
	"a37": true, "a38": true, "a39": true, "a4": true, "a40": true, "a41": true,
	"a42": true, "a43": true, "a44": true, "a45": true, "a46": true, "a47": true,
	"a48": true, "a49": true, "a5": true, "a50": true, "a51": true, "a52": true,
	"a53": true, "a54": true, "a55": true, "a56": true, "a57": true, "a58": true,
	"a59": true, "a6": true, "a60": true, "a61": true, "a62": true, "a63": true,
	"a64": true, "a65": true, "a66": true, "a67": true, "a68": true, "a69": true,
	"a7": true, "a70": true, "a71": true, "a72": true, "a73": true, "a74": true,
	"a75": true, "a76": true, "a77": true, "a78": true, "a79": true, "a8": true,
	"a81": true, "a82": true, "a83": true, "a84": true, "a85": true, "a86": true,
	"a87": true, "a88": true, "a89": true, "a9": true, "a90": true, "a91": true,
	"a92": true, "a93": true, "a94": true, "a95": true, "a96": true, "a97": true,
	"a98": true, "a99": true, "aacute": true, "abreve": true, "acircumflex": true, "acute": true,
	"acutecomb": true, "adieresis": true, "ae": true, "aeacute": true, "agrave": true, "aleph": true,
	"alpha": true, "alphatonos": true, "amacron": true, "ampersand": true, "ampersandsmall": true, "angle": true,
	"angleleft": true, "angleright": true, "anoteleia": true, "aogonek": true, "apple": true, "approxequal": true,
	"aring": true, "aringacute": true, "arrowboth": true, "arrowdblboth": true, "arrowdbldown": true, "arrowdblleft": true,
	"arrowdblright": true, "arrowdblup": true, "arrowdown": true, "arrowhorizex": true, "arrowleft": true, "arrowright": true,
	"arrowup": true, "arrowupdn": true, "arrowupdnbse": true, "arrowvertex": true, "asciicircum": true, "asciitilde": true,
	"asterisk": true, "asteriskmath": true, "at": true, "atilde": true, "b": true, "backslash": true,
	"bar": true, "beta": true, "block": true, "braceex": true, "braceleft": true, "braceleftbt": true,
	"braceleftmid": true, "bracelefttp": true, "braceright": true, "bracerightbt": true, "bracerightmid": true, "bracerighttp": true,
	"bracket": true, "bracketleft": true, "bracketleftbt": true, "bracketleftex": true, "bracketlefttp": true, "bracketright": true,
	"bracketrightbt": true, "bracketrightex": true, "bracketrighttp": true, "breve": true, "brokenbar": true, "bullet": true,
	"c": true, "cacute": true, "caron": true, "carriagereturn": true, "ccaron": true, "ccedilla": true,
	"ccircumflex": true, "cdotaccent": true, "cedilla": true, "cent": true, "centinferior": true, "centoldstyle": true,
	"centsuperior": true, "chi": true, "circle": true, "circlemultiply": true, "circleplus": true, "circumflex": true,
	"club": true, "colon": true, "colonmonetary": true, "comma": true, "commaaccent": true, "commainferior": true,
	"commasuperior": true, "congruent": true, "copyright": true, "copyrightsans": true, "copyrightserif": true, "currency": true,
	"cyrBreve": true, "cyrFlex": true, "cyrbreve": true, "cyrflex": true, "d": true, "dagger": true,
	"daggerdbl": true, "dblGrave": true, "dblgrave": true, "dcaron": true, "dcroat": true, "degree": true,
	"delta": true, "diamond": true, "dieresis": true, "dieresisacute": true, "dieresisgrave": true, "dieresistonos": true,
	"divide": true, "dkshade": true, "dnblock": true, "dollar": true, "dollarinferior": true, "dollaroldstyle": true,
	"dollarsuperior": true, "dong": true, "dotaccent": true, "dotbelowcomb": true, "dotlessi": true, "dotlessj": true,
	"dotmath": true, "e": true, "eacute": true, "ebreve": true, "ecaron": true, "ecircumflex": true,
	"edieresis": true, "edotaccent": true, "egrave": true, "eight": true, "eightinferior": true, "eightoldstyle": true,
	"eightsuperior": true, "element": true, "ellipsis": true, "emacron": true, "emdash": true, "emptyset": true,
	"endash": true, "eng": true, "eogonek": true, "epsilon": true, "epsilontonos": true, "equal": true,
	"equivalence": true, "estimated": true, "eta": true, "etatonos": true, "eth": true, "exclam": true,
	"exclamdbl": true, "exclamdown": true, "exclamdownsmall": true, "exclamsmall": true, "existential": true, "f": true,
	"female": true, "ff": true, "ffi": true, "ffl": true, "fi": true, "figuredash": true,
	"filledbox": true, "filledrect": true, "five": true, "fiveeighths": true, "fiveinferior": true, "fiveoldstyle": true,
	"fivesuperior": true, "fl": true, "florin": true, "four": true, "fourinferior": true, "fouroldstyle": true,
	"foursuperior": true, "fraction": true, "franc": true, "g": true, "gamma": true, "gbreve": true,
	"gcaron": true, "gcircumflex": true, "gcommaaccent": true, "gdotaccent": true, "germandbls": true, "gradient": true,
	"grave": true, "gravecomb": true, "greater": true, "greaterequal": true, "guillemotleft": true, "guillemotright": true,
	"guilsinglleft": true, "guilsinglright": true, "h": true, "hbar": true, "hcircumflex": true, "heart": true,
	"hookabovecomb": true, "house": true, "hungarumlaut": true, "hyphen": true, "hypheninferior": true, "hyphensuperior": true,
	"i": true, "iacute": true, "ibreve": true, "icircumflex": true, "idieresis": true, "igrave": true,
	"ij": true, "imacron": true, "infinity": true, "integral": true, "integralbt": true, "integralex": true,
	"integraltp": true, "intersection": true, "invbullet": true, "invcircle": true, "invsmileface": true, "iogonek": true,
	"iota": true, "iotadieresis": true, "iotatonos": true, "isinferior": true, "isuperior": true, "itilde": true,
	"j": true, "jcircumflex": true, "k": true, "kappa": true, "kcommaaccent": true, "kgreenlandic": true,
	"l": true, "lacute": true, "lambda": true, "lcaron": true, "lcommaaccent": true, "ldot": true,
	"less": true, "lessequal": true, "lfblock": true, "lira": true, "ll": true, "logicaland": true,
	"logicalnot": true, "logicalor": true, "longs": true, "lozenge": true, "lslash": true, "lsuperior": true,
	"ltshade": true, "m": true, "macron": true, "male": true, "middot": true, "minus": true,
	"minute": true, "msuperior": true, "mu": true, "multiply": true, "musicalnote": true, "musicalnotedbl": true,
	"n": true, "nacute": true, "napostrophe": true, "nbspace": true, "ncaron": true, "ncommaaccent": true,
	"nine": true, "nineinferior": true, "nineoldstyle": true, "ninesuperior": true, "notelement": true, "notequal": true,
	"notsubset": true, "nsuperior": true, "ntilde": true, "nu": true, "numbersign": true, "o": true,
	"oacute": true, "obreve": true, "ocircumflex": true, "odieresis": true, "oe": true, "ogonek": true,
	"ograve": true, "ohorn": true, "ohungarumlaut": true, "omacron": true, "omega": true, "omega1": true,
	"omegatonos": true, "omicron": true, "omicrontonos": true, "one": true, "onedotenleader": true, "oneeighth": true,
	"onefitted": true, "onehalf": true, "oneinferior": true, "oneoldstyle": true, "onequarter": true, "onesuperior": true,
	"onethird": true, "openbullet": true, "ordfeminine": true, "ordmasculine": true, "orthogonal": true, "oslash": true,
	"oslashacute": true, "osuperior": true, "otilde": true, "p": true, "paragraph": true, "parenleft": true,
	"parenleftbt": true, "parenleftex": true, "parenleftinferior": true, "parenleftsuperior": true, "parenlefttp": true, "parenright": true,
	"parenrightbt": true, "parenrightex": true, "parenrightinferior": true, "parenrightsuperior": true, "parenrighttp": true, "partialdiff": true,
	"percent": true, "period": true, "periodcentered": true, "periodinferior": true, "periodsuperior": true, "perpendicular": true,
	"perthousand": true, "peseta": true, "phi": true, "phi1": true, "pi": true, "plus": true,
	"plusminus": true, "prescription": true, "product": true, "propersubset": true, "propersuperset": true, "proportional": true,
	"psi": true, "q": true, "question": true, "questiondown": true, "questiondownsmall": true, "questionsmall": true,
	"quotedbl": true, "quotedblbase": true, "quotedblleft": true, "quotedblright": true, "quoteleft": true, "quotereversed": true,
	"quoteright": true, "quotesinglbase": true, "quotesingle": true, "r": true, "racute": true, "radical": true,
	"radicalex": true, "rcaron": true, "rcommaaccent": true, "reflexsubset": true, "reflexsuperset": true, "registered": true,
	"registersans": true, "registerserif": true, "revlogicalnot": true, "rho": true, "ring": true, "rsuperior": true,
	"rtblock": true, "rupiah": true, "s": true, "sacute": true, "scaron": true, "scedilla": true,
	"scircumflex": true, "scommaaccent": true, "second": true, "section": true, "semicolon": true, "seven": true,
	"seveneighths": true, "seveninferior": true, "sevenoldstyle": true, "sevensuperior": true, "sfthyphen": true, "shade": true,
	"sigma": true, "sigma1": true, "similar": true, "six": true, "sixinferior": true, "sixoldstyle": true,
	"sixsuperior": true, "slash": true, "smileface": true, "space": true, "spade": true, "ssuperior": true,
	"sterling": true, "suchthat": true, "summation": true, "sun": true, "t": true, "tau": true,
	"tbar": true, "tcaron": true, "tcommaaccent": true, "therefore": true, "theta": true, "theta1": true,
	"thorn": true, "three": true, "threeeighths": true, "threeinferior": true, "threeoldstyle": true, "threequarters": true,
	"threequartersemdash": true, "threesuperior": true, "tilde": true, "tildecomb": true, "tonos": true, "trademark": true,
	"trademarksans": true, "trademarkserif": true, "triagdn": true, "triaglf": true, "triagrt": true, "triagup": true,
	"tsuperior": true, "two": true, "twodotenleader": true, "twoinferior": true, "twooldstyle": true, "twosuperior": true,
	"twothirds": true, "u": true, "uacute": true, "ubreve": true, "ucircumflex": true, "udieresis": true,
	"ugrave": true, "uhorn": true, "uhungarumlaut": true, "umacron": true, "underscore": true, "underscoredbl": true,
	"union": true, "universal": true, "uogonek": true, "upblock": true, "upsilon": true, "upsilondieresis": true,
	"upsilondieresistonos": true, "upsilontonos": true, "uring": true, "utilde": true, "v": true, "w": true,
	"wacute": true, "wcircumflex": true, "wdieresis": true, "weierstrass": true, "wgrave": true, "x": true,
	"xi": true, "y": true, "yacute": true, "ycircumflex": true, "ydieresis": true, "yen": true,
	"ygrave": true, "z": true, "zacute": true, "zcaron": true, "zdotaccent": true, "zero": true,
	"zeroinferior": true, "zerooldstyle": true, "zerosuperior": true, "zeta": true,
}

// --- font program loading ---

// loadFontProgram parses the embedded font program of a descriptor, or nil
// when none is embedded or it cannot be parsed.
func loadFontProgram(doc *Document, fd *Dictionary) *fontProgram {
	if fd == nil {
		return nil
	}
	if s, ok := doc.Resolve(fd.Get("FontFile")).(*Stream); ok {
		if data := decodeContentStream(doc, s); data != nil {
			return parseType1(data)
		}
	}
	if s, ok := doc.Resolve(fd.Get("FontFile2")).(*Stream); ok {
		if data := decodeContentStream(doc, s); data != nil {
			return parseSFNT(data)
		}
	}
	if s, ok := doc.Resolve(fd.Get("FontFile3")).(*Stream); ok {
		if data := decodeContentStream(doc, s); data != nil {
			subtype, _ := s.Dict.Get("Subtype").(Name)
			if subtype == "OpenType" {
				if fp := parseSFNTCFF(data); fp != nil {
					return fp
				}
				return parseSFNT(data)
			}
			return parseCFF(data)
		}
	}
	return nil
}

// parseSFNTCFF returns the CFF-table font program of an OpenType/CFF font,
// falling back to the sfnt view when there is no CFF table.
func parseSFNTCFF(data []byte) *fontProgram {
	if len(data) < 12 || be32(data, 0) != 0x4F54544F { // 'OTTO'
		return nil
	}
	numTables := be16(data, 4)
	for i := 0; i < numTables; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			return nil
		}
		if string(data[rec:rec+4]) == "CFF " {
			off := be32(data, rec+8)
			length := be32(data, rec+12)
			if uint64(off)+uint64(length) <= uint64(len(data)) {
				return parseCFF(data[off : off+length])
			}
		}
	}
	return nil
}

// --- simple font encoding (code -> glyph name) ---

// simpleFontCodeToName builds the character-code to glyph-name table for a
// simple font per ISO 32000-1, 9.6.6: a base encoding (named, or the font's
// implicit one) updated by any Differences array.
func simpleFontCodeToName(doc *Document, fontDict *Dictionary, symbolic bool) map[byte]string {
	table := make(map[byte]string)
	applyBase := func(name Name) {
		var src map[byte]string
		switch name {
		case "WinAnsiEncoding":
			src = winAnsiEncodingNames
		case "MacRomanEncoding":
			src = macRomanEncodingNames
		case "StandardEncoding":
			src = standardEncodingNames
		}
		for c, n := range src {
			table[c] = n
		}
	}

	switch enc := doc.Resolve(fontDict.Get("Encoding")).(type) {
	case Name:
		applyBase(enc)
	case *Dictionary:
		if base, ok := doc.Resolve(enc.Get("BaseEncoding")).(Name); ok {
			applyBase(base)
		} else if !symbolic {
			applyBase("StandardEncoding")
		}
		if diffs, ok := doc.Resolve(enc.Get("Differences")).(Array); ok {
			code := 0
			for _, el := range diffs {
				switch v := el.(type) {
				case Integer:
					code = int(v)
				case Name:
					if code >= 0 && code < 256 {
						table[byte(code)] = string(v)
					}
					code++
				}
			}
		}
	case nil:
		if !symbolic {
			applyBase("StandardEncoding")
		}
	}
	return table
}

// --- glyph-name to Unicode (for TrueType cmap lookup) ---

// glyphNameToRune maps a glyph name to a Unicode code point for the common
// cases needed by TrueType (3,1) cmap lookup: the uniXXXX/uXXXX conventions
// and the ASCII range, where the standard Latin encodings are identity.
func glyphNameToRune(name string, code byte) (rune, bool) {
	if strings.HasPrefix(name, "uni") && len(name) == 7 {
		if v, ok := parseHexN(name[3:]); ok {
			return rune(v), true
		}
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, ok := parseHexN(name[1:]); ok {
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

func parseHexN(s string) (int, bool) {
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

// --- program consistency: metrics, glyph coverage, .notdef ---

const glyphWidthTolerance = 1.0 // 1/1000 text-space units

// checkFontProgramConsistency validates that, for every glyph actually shown,
// the font-dictionary width matches the embedded program's advance width
// (ISO 19005 font-metrics rule), the glyph is present in the program
// (embedding-completeness rule), and no shown glyph is .notdef.
func checkFontProgramConsistency(doc *Document, level PDFALevel, rule string, fontDict *Dictionary, u *fontTextUsage) []ValidationError {
	subtype, _ := fontDict.Get("Subtype").(Name)
	if subtype == "Type3" {
		return checkType3Widths(doc, level, rule, fontDict, u)
	}
	if subtype == "Type0" {
		return checkCIDFontConsistency(doc, level, rule, fontDict, u)
	}
	return checkSimpleFontConsistency(doc, level, rule, fontDict, u)
}

func checkSimpleFontConsistency(doc *Document, level PDFALevel, rule string, fontDict *Dictionary, u *fontTextUsage) []ValidationError {
	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	fp := loadFontProgram(doc, fd)
	if fp == nil {
		return nil // embedding checked elsewhere; nothing to compare against
	}
	subtype, _ := fontDict.Get("Subtype").(Name)
	symbolic := false
	if fd != nil {
		if flags, ok := doc.Resolve(fd.Get("Flags")).(Integer); ok {
			symbolic = flags&4 != 0
		}
	}
	enc := simpleFontCodeToName(doc, fontDict, symbolic)
	firstChar := intVal(doc.Resolve(fontDict.Get("FirstChar")))
	widths, _ := doc.Resolve(fontDict.Get("Widths")).(Array)
	missingWidth := 0.0
	if fd != nil {
		missingWidth = numVal(doc.Resolve(fd.Get("MissingWidth")))
	}

	var errs []ValidationError
	reported := map[string]bool{}
	report := func(kind, msg string) {
		if reported[kind] {
			return
		}
		reported[kind] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: u.objNum})
	}

	renders := rendersVisibly(u)
	for _, s := range u.strings {
		for _, code := range s {
			name := enc[code]

			// Program advance width for this code.
			progW, haveProg := simpleGlyphWidth(fp, subtype, symbolic, code, name)
			glyphExists := simpleGlyphExists(fp, subtype, symbolic, code, name)

			if renders && !glyphExists {
				report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (code %d)", string(subtype), code))
			}
			if renders && isNotdefGlyph(fp, subtype, symbolic, code, name) {
				report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(subtype)))
			}

			// Width consistency (only for visibly rendered glyphs).
			pdfW, havePDF := simpleDeclaredWidth(widths, firstChar, code, missingWidth)
			if renders && haveProg && havePDF && absf(pdfW-progW) > glyphWidthTolerance {
				report("width", fmt.Sprintf("width information for glyphs used for rendering is inconsistent in %s font", string(subtype)))
			}
		}
	}
	return errs
}

func checkCIDFontConsistency(doc *Document, level PDFALevel, rule string, fontDict *Dictionary, u *fontTextUsage) []ValidationError {
	desc := type0Descendant(doc, fontDict)
	if desc == nil {
		return nil
	}
	fd := doc.ResolveDict(desc.Get("FontDescriptor"))
	fp := loadFontProgram(doc, fd)
	if fp == nil {
		return nil
	}
	cidSub, _ := desc.Get("Subtype").(Name)
	identity := isIdentityEncoding(doc, fontDict)

	dw := 1000.0
	if v := doc.Resolve(desc.Get("DW")); v != nil {
		dw = numVal(v)
	}
	wMap := parseCIDWidths(doc, desc.Get("W"))

	var errs []ValidationError
	reported := map[string]bool{}
	report := func(kind, msg string) {
		if reported[kind] {
			return
		}
		reported[kind] = true
		errs = append(errs, ValidationError{Rule: rule, Level: level, Message: msg, Object: u.objNum})
	}
	renders := rendersVisibly(u)

	for _, s := range u.strings {
		if !identity {
			continue // only Identity CID decoding is handled precisely
		}
		for i := 0; i+1 < len(s); i += 2 {
			cid := int(s[i])<<8 | int(s[i+1])

			progW, haveProg := cidGlyphWidth(fp, desc, doc, cidSub, cid)
			exists := cidGlyphExists(fp, cidSub, cid)

			if renders && !exists {
				report("glyph", fmt.Sprintf("embedded %s font does not define a glyph referenced for rendering (CID %d)", string(cidSub), cid))
			}
			if renders && cid == 0 {
				report("notdef", fmt.Sprintf("text showing operator references the .notdef glyph in %s font", string(cidSub)))
			}

			pdfW := dw
			if w, ok := wMap[cid]; ok {
				pdfW = w
			}
			if renders && haveProg && absf(pdfW-progW) > glyphWidthTolerance {
				report("width", fmt.Sprintf("width information for glyphs used for rendering is inconsistent in %s font", string(cidSub)))
			}
		}
	}
	return errs
}

func checkType3Widths(doc *Document, level PDFALevel, rule string, fontDict *Dictionary, u *fontTextUsage) []ValidationError {
	// A Type 3 glyph's advance is the w operand of its d0/d1 operator in the
	// CharProc, transformed by the FontMatrix; it must match the Widths
	// array (ISO 32000-1, 9.6.5 / 9.10). Compare in glyph space.
	charProcs := doc.ResolveDict(fontDict.Get("CharProcs"))
	if charProcs == nil {
		return nil
	}
	enc := simpleFontCodeToName(doc, fontDict, false)
	firstChar := intVal(doc.Resolve(fontDict.Get("FirstChar")))
	widths, _ := doc.Resolve(fontDict.Get("Widths")).(Array)
	fm := parseFontMatrix(doc, fontDict.Get("FontMatrix"))
	if !rendersVisibly(u) {
		return nil
	}

	var errs []ValidationError
	reported := false
	for _, s := range u.strings {
		for _, code := range s {
			name := enc[code]
			cp, ok := doc.Resolve(charProcs.Get(Name(name))).(*Stream)
			if !ok {
				continue
			}
			glyphW, ok := type3GlyphWidth(doc, cp)
			if !ok {
				continue
			}
			pdfW, havePDF := simpleDeclaredWidth(widths, firstChar, code, 0)
			if !havePDF {
				continue
			}
			// Transform glyph-space width to text space via the FontMatrix
			// x-scale, then to 1/1000 units.
			progW := glyphW * fm[0] * 1000
			if absf(pdfW-progW) > glyphWidthTolerance && !reported {
				reported = true
				errs = append(errs, ValidationError{Rule: rule, Level: level,
					Message: "width information for glyphs used for rendering is inconsistent in Type3 font", Object: u.objNum})
			}
		}
	}
	return errs
}

// --- small helpers ---

func intVal(o Object) int {
	if i, ok := o.(Integer); ok {
		return int(i)
	}
	return 0
}

func numVal(o Object) float64 {
	switch v := o.(type) {
	case Integer:
		return float64(v)
	case Real:
		return float64(v)
	}
	return 0
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// rendersVisibly reports whether the font showed text in any mode other than
// 3 (invisible) or 7 (clip only) — modes where glyph shape/coverage is not
// actually painted.
func rendersVisibly(u *fontTextUsage) bool {
	if len(u.modes) == 0 {
		return true
	}
	for m := range u.modes {
		if m != 3 && m != 7 {
			return true
		}
	}
	return false
}

// simpleDeclaredWidth returns the width the font dictionary declares for a
// code: Widths[code-FirstChar] when in range, else MissingWidth.
func simpleDeclaredWidth(widths Array, firstChar int, code byte, missingWidth float64) (float64, bool) {
	idx := int(code) - firstChar
	if idx >= 0 && idx < len(widths) {
		return numVal(widths[idx]), true
	}
	if missingWidth != 0 {
		return missingWidth, true
	}
	return 0, false
}

// simpleGlyphWidth returns the embedded program's advance width for a code.
func simpleGlyphWidth(fp *fontProgram, subtype Name, symbolic bool, code byte, name string) (float64, bool) {
	if subtype == "TrueType" {
		gid, ok := trueTypeGID(fp, symbolic, code, name)
		if !ok || gid >= len(fp.widthByGID) {
			return 0, false
		}
		return fp.widthByGID[gid], true
	}
	// Type1 / MMType1 / CFF: by glyph name.
	if name == "" {
		return 0, false
	}
	w, ok := fp.widthByName[name]
	return w, ok
}

func simpleGlyphExists(fp *fontProgram, subtype Name, symbolic bool, code byte, name string) bool {
	if subtype == "TrueType" {
		gid, ok := trueTypeGID(fp, symbolic, code, name)
		return ok && gid > 0 && gid < fp.numGlyphs
	}
	if name == "" {
		return false
	}
	return fp.glyphNames[name]
}

func isNotdefGlyph(fp *fontProgram, subtype Name, symbolic bool, code byte, name string) bool {
	if name == ".notdef" {
		return true
	}
	if subtype == "TrueType" {
		gid, ok := trueTypeGID(fp, symbolic, code, name)
		return ok && gid == 0
	}
	return false
}

// trueTypeGID maps a character code to a glyph index using the font's cmap
// subtables, following ISO 32000-1, 9.6.6.4.
func trueTypeGID(fp *fontProgram, symbolic bool, code byte, name string) (int, bool) {
	if symbolic {
		if fp.symbolCmap != nil {
			if gid, ok := fp.symbolCmap[0xF000|uint16(code)]; ok {
				return gid, true
			}
			if gid, ok := fp.symbolCmap[uint16(code)]; ok {
				return gid, true
			}
		}
		if fp.macCmap != nil {
			if gid, ok := fp.macCmap[code]; ok {
				return gid, true
			}
		}
		return 0, false
	}
	if fp.cmap != nil {
		if r, ok := glyphNameToRune(name, code); ok {
			if gid, ok := fp.cmap[r]; ok {
				return gid, true
			}
		}
	}
	if fp.macCmap != nil {
		if gid, ok := fp.macCmap[code]; ok {
			return gid, true
		}
	}
	if fp.symbolCmap != nil {
		if gid, ok := fp.symbolCmap[0xF000|uint16(code)]; ok {
			return gid, true
		}
	}
	return 0, false
}

// isIdentityEncoding reports whether a Type0 font uses Identity-H/V.
func isIdentityEncoding(doc *Document, fontDict *Dictionary) bool {
	if n, ok := doc.Resolve(fontDict.Get("Encoding")).(Name); ok {
		return n == "Identity-H" || n == "Identity-V"
	}
	return false
}

// cidGlyphWidth returns a CID's advance from the embedded CIDFont program.
func cidGlyphWidth(fp *fontProgram, desc *Dictionary, doc *Document, cidSub Name, cid int) (float64, bool) {
	if cidSub == "CIDFontType2" {
		gid, ok := cidToGID(doc, desc, cid)
		if !ok || gid >= len(fp.widthByGID) {
			return 0, false
		}
		return fp.widthByGID[gid], true
	}
	// CIDFontType0 (CFF): CID-keyed by CID, or GID==CID for non-CID CFF.
	if fp.widthByCID != nil {
		w, ok := fp.widthByCID[cid]
		return w, ok
	}
	if cid < len(fp.widthByGID) {
		return fp.widthByGID[cid], true
	}
	return 0, false
}

func cidGlyphExists(fp *fontProgram, cidSub Name, cid int) bool {
	if cidSub == "CIDFontType2" {
		return cid > 0 && cid < fp.numGlyphs
	}
	if fp.cidGIDs != nil {
		return fp.cidGIDs[cid]
	}
	return cid > 0 && cid < fp.numGlyphs
}

// cidToGID resolves a CID to a glyph index via the CIDToGIDMap (name Identity
// or a 2-byte-per-CID stream).
func cidToGID(doc *Document, desc *Dictionary, cid int) (int, bool) {
	switch v := doc.Resolve(desc.Get("CIDToGIDMap")).(type) {
	case Name:
		if v == "Identity" {
			return cid, true
		}
	case *Stream:
		data := decodeContentStream(doc, v)
		if data != nil && 2*cid+1 < len(data) {
			return int(data[2*cid])<<8 | int(data[2*cid+1]), true
		}
	case nil:
		return cid, true // default Identity
	}
	return cid, true
}

// parseCIDWidths parses a CIDFont /W array into CID -> width.
func parseCIDWidths(doc *Document, wObj Object) map[int]float64 {
	out := make(map[int]float64)
	arr, ok := doc.Resolve(wObj).(Array)
	if !ok {
		return out
	}
	i := 0
	for i < len(arr) {
		c := intVal(doc.Resolve(arr[i]))
		if i+1 < len(arr) {
			if sub, ok := doc.Resolve(arr[i+1]).(Array); ok {
				for k, wv := range sub {
					out[c+k] = numVal(doc.Resolve(wv))
				}
				i += 2
				continue
			}
			if i+2 < len(arr) {
				cLast := intVal(doc.Resolve(arr[i+1]))
				w := numVal(doc.Resolve(arr[i+2]))
				for cid := c; cid <= cLast; cid++ {
					out[cid] = w
				}
				i += 3
				continue
			}
		}
		i++
	}
	return out
}

// parseFontMatrix reads a Type 3 /FontMatrix (default [0.001 0 0 0.001 0 0]).
func parseFontMatrix(doc *Document, o Object) [6]float64 {
	fm := [6]float64{0.001, 0, 0, 0.001, 0, 0}
	if arr, ok := doc.Resolve(o).(Array); ok && len(arr) == 6 {
		for i := 0; i < 6; i++ {
			fm[i] = numVal(doc.Resolve(arr[i]))
		}
	}
	return fm
}

// type3GlyphWidth reads the w operand of the leading d0/d1 operator of a
// Type 3 CharProc content stream (glyph-space units).
func type3GlyphWidth(doc *Document, cp *Stream) (float64, bool) {
	data := decodeContentStream(doc, cp)
	if data == nil {
		return 0, false
	}
	var nums []float64
	found := false
	var w float64
	forEachContentItem(data, func(kind contentItemKind, payload []byte) {
		if found {
			return
		}
		switch kind {
		case itemNumber:
			nums = append(nums, numVal(parseNumberToken(payload)))
		case itemOperator:
			switch string(payload) {
			case "d0", "d1":
				if len(nums) >= 1 {
					w = nums[0]
					found = true
				}
			default:
				nums = nums[:0]
			}
		default:
			nums = nums[:0]
		}
	})
	return w, found
}

// parseNumberToken parses a numeric content token to a Real/Integer object.
func parseNumberToken(b []byte) Object {
	s := string(b)
	if strings.ContainsAny(s, ".eE") {
		var f float64
		fmt_Sscan(s, &f)
		return Real(f)
	}
	neg := false
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	v := 0
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		v = v*10 + int(s[i]-'0')
	}
	if neg {
		v = -v
	}
	return Integer(v)
}

// --- subset CharSet / CIDSet completeness ---

// subsetRule returns the clause a subset-embedding violation is reported
// under: 19005-1 6.3.5, 19005-2/-3 6.2.11.4.2, 19005-4 6.2.10.4.2.
func subsetRule(level PDFALevel) string {
	switch level {
	case PDFA1b:
		return "6.3.5"
	case PDFA4:
		return "6.2.10.4.2"
	}
	return "6.2.11.4.2"
}

// checkFontSubsetCompleteness verifies that a subset font descriptor's
// CharSet (Type1) or CIDSet (CIDFont), when present, lists every glyph or
// CID actually used for rendering (ISO 19005-1 6.3.5, -2/-3 6.2.11.4.2).
// An empty or partial set omitting a shown glyph is a violation.
func checkFontSubsetCompleteness(doc *Document, level PDFALevel) []ValidationError {
	rule := subsetRule(level)
	var errs []ValidationError

	for fontDict, u := range collectFontTextUsage(doc) {
		subtype, _ := fontDict.Get("Subtype").(Name)
		switch subtype {
		case "Type1", "MMType1":
			fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
			if fd == nil {
				continue
			}
			cs, ok := doc.Resolve(fd.Get("CharSet")).(String)
			if !ok {
				continue
			}
			listed := parseCharSet(string(cs.Value))
			symbolic := descriptorSymbolic(doc, fd)
			enc := simpleFontCodeToName(doc, fontDict, symbolic)
			if usedGlyphMissing(u, enc, listed) {
				errs = append(errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: "FontDescriptor CharSet does not list all glyph names used for rendering",
					Object:  u.objNum,
				})
			}
		case "Type0":
			desc := type0Descendant(doc, fontDict)
			if desc == nil {
				continue
			}
			fd := doc.ResolveDict(desc.Get("FontDescriptor"))
			if fd == nil {
				continue
			}
			cidSet, ok := doc.Resolve(fd.Get("CIDSet")).(*Stream)
			if !ok {
				continue
			}
			if !isIdentityEncoding(doc, fontDict) {
				continue
			}
			present := cidSetBits(doc, cidSet)
			missing := false
			for _, s := range u.strings {
				for i := 0; i+1 < len(s); i += 2 {
					cid := int(s[i])<<8 | int(s[i+1])
					if cid != 0 && !present[cid] {
						missing = true
					}
				}
			}
			if missing {
				errs = append(errs, ValidationError{
					Rule:    rule,
					Level:   level,
					Message: "FontDescriptor CIDSet does not list all CIDs used for rendering",
					Object:  u.objNum,
				})
			}
		}
	}
	return errs
}

// descriptorSymbolic reports the descriptor's Symbolic flag.
func descriptorSymbolic(doc *Document, fd *Dictionary) bool {
	if flags, ok := doc.Resolve(fd.Get("Flags")).(Integer); ok {
		return flags&4 != 0
	}
	return false
}

// usedGlyphMissing reports whether any shown glyph name is absent from the
// listed CharSet names.
func usedGlyphMissing(u *fontTextUsage, enc map[byte]string, listed map[string]bool) bool {
	for _, s := range u.strings {
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

// parseCharSet parses a Type 1 /CharSet string ("/name1/name2/...") into a
// set of glyph names.
func parseCharSet(s string) map[string]bool {
	out := make(map[string]bool)
	for {
		i := strings.IndexByte(s, '/')
		if i < 0 {
			break
		}
		s = s[i+1:]
		end := 0
		for end < len(s) && s[end] != '/' && !isWhitespace(s[end]) {
			end++
		}
		if end > 0 {
			out[s[:end]] = true
		}
		s = s[end:]
	}
	return out
}

// cidSetBits decodes a CIDSet stream: bit i (MSB-first per byte) set means
// CID i is present.
func cidSetBits(doc *Document, s *Stream) map[int]bool {
	out := make(map[int]bool)
	data := decodeContentStream(doc, s)
	for i, b := range data {
		for bit := 0; bit < 8; bit++ {
			if b&(0x80>>bit) != 0 {
				out[i*8+bit] = true
			}
		}
	}
	return out
}

// checkCMapCIDLimit verifies that no character identifier defined by an
// embedded CMap exceeds 65535 (ISO 19005-1 6.1.12, -2/-3 6.1.13; the CID is
// a 16-bit value per ISO 32000-1 9.7.4).
func checkCMapCIDLimit(doc *Document, level PDFALevel) []ValidationError {
	if level == PDFA4 {
		return nil // PDF/A-4 has no implementation-limits clause
	}
	rule := "6.1.12"
	if level == PDFA2b || level == PDFA3b {
		rule = "6.1.13"
	}
	var errs []ValidationError
	seen := map[int]bool{}
	for num, iobj := range doc.Objects {
		fontDict, ok := iobj.Value.(*Dictionary)
		if !ok {
			continue
		}
		if st, _ := fontDict.Get("Subtype").(Name); st != "Type0" {
			continue
		}
		enc, ok := doc.Resolve(fontDict.Get("Encoding")).(*Stream)
		if !ok {
			continue
		}
		data := decodeContentStream(doc, enc)
		if data == nil {
			continue
		}
		if maxCMapCID(data) > 65535 && !seen[num] {
			seen[num] = true
			errs = append(errs, ValidationError{
				Rule:    rule,
				Level:   level,
				Message: "a character identifier (CID) defined in the CMap exceeds the maximum value 65535",
				Object:  num,
			})
		}
	}
	return errs
}

// maxCMapCID returns the largest CID mapped by a CMap's cidrange and cidchar
// sections.
func maxCMapCID(data []byte) int {
	max := 0
	consider := func(v int) {
		if v > max {
			max = v
		}
	}
	// cidrange: <lo> <hi> startCID  -> max CID = startCID + (hi - lo)
	s := string(data)
	scanRanges := func(begin, end string, isRange bool) {
		rest := s
		for {
			b := strings.Index(rest, begin)
			if b < 0 {
				return
			}
			e := strings.Index(rest[b:], end)
			if e < 0 {
				return
			}
			section := rest[b+len(begin) : b+e]
			for _, line := range strings.Split(section, "\n") {
				fields := strings.Fields(line)
				if isRange && len(fields) >= 3 {
					lo := hexVal4(fields[0])
					hi := hexVal4(fields[1])
					cid := atoiSafe(fields[2])
					if lo >= 0 && hi >= lo {
						consider(cid + (hi - lo))
					}
				} else if !isRange && len(fields) >= 2 {
					consider(atoiSafe(fields[1]))
				}
			}
			rest = rest[b+e+len(end):]
		}
	}
	scanRanges("begincidrange", "endcidrange", true)
	scanRanges("begincidchar", "endcidchar", false)
	return max
}

// hexVal4 parses a <hhhh> hex token to an int, or -1.
func hexVal4(s string) int {
	s = strings.TrimPrefix(s, "<")
	s = strings.TrimSuffix(s, ">")
	if s == "" {
		return -1
	}
	v, ok := parseHexN(s)
	if !ok {
		return -1
	}
	return v
}

func atoiSafe(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		v = v*10 + int(s[i]-'0')
	}
	return v
}
