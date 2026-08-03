package core

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/internal/font"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// Font usage: which fonts a document actually shows text in, and what text.
//
// It is a document service rather than a validator one. PDF/A asks it whether
// every shown glyph is embedded; PDF/UA asks it whether every shown glyph maps
// to Unicode. Both walk the same content streams over the same page tree, and
// the memos below make that one walk instead of two.

// FontTextUsage aggregates the text shown with one font dictionary.
type FontTextUsage struct {
	FontDict *object.Dictionary
	ObjNum   int      // font object number (0 if direct)
	Strings  [][]byte // raw shown string bytes
	Modes    map[int]bool
}

// CollectFontTextUsage walks every page's executed content (including form
// XObjects and tiling patterns) and records which fonts show which text.
func CollectFontTextUsage(doc View) map[*object.Dictionary]*FontTextUsage {
	if c := doc.Run; c != nil && c.fontUsageValid {
		return c.fontUsage
	}
	usage := make(map[*object.Dictionary]*FontTextUsage)
	if catalog := doc.Catalog(); catalog != nil {
		seen := make(map[*object.Dictionary]bool)
		applied := make(map[sfKey]bool)
		for _, page := range doc.Pages(catalog.Get("Pages")) {
			data, key := doc.ContentBytesAndKey(page.Dict.Get("Contents"))
			collectTextFromContainer(doc, page.Dict, data, key, usage, seen, applied)
		}
	}
	if c := doc.Run; c != nil {
		c.fontUsage = usage
		c.fontUsageValid = true
	}
	return usage
}

// fontEventKind classifies the replayable events extracted from a content
// stream by buildFontEvents.
type fontEventKind uint8

// FontEvent is one entry in a content stream's font-usage skeleton: the
// container-independent result of tokenizing the stream once. Replaying the
// skeleton against a container's resources reproduces exactly what a direct
// walk would attribute to each font, without re-tokenizing the bytes.
type FontEvent struct {
	kind    fontEventKind
	name    string   // evTf: the operand name of the Tf operator
	mode    int      // evTr: the text rendering mode
	strings [][]byte // evShow: the strings pending at the show operator
}

// sfKey identifies a (content stream, /Font resource dictionary) pair. A
// container's font attribution is fully determined by this pair, so two
// containers sharing both produce byte-identical contributions to the usage
// map — the second and later are skipped (see collectTextFromContainer). This
// is what stops a document that references one content stream from thousands of
// pages from re-attributing, and re-accumulating, the same shown text per page.
type sfKey struct {
	stream  *object.Stream
	fontRes *object.Dictionary
}

// collectTextFromContainer attributes the text shown in a container's content
// (key identifies the single backing stream, if any) to the fonts it selects,
// then recurses into the form XObjects and tiling patterns it actually invokes.
// Tokenization is memoized per stream via key, and the font attribution is
// skipped when an identical (stream, /Font) pair was already processed, so
// content shared across many containers is handled once rather than per
// container.
func collectTextFromContainer(doc View, container *object.Dictionary, data []byte, key *object.Stream, usage map[*object.Dictionary]*FontTextUsage, seen map[*object.Dictionary]bool, applied map[sfKey]bool) {
	if container == nil || seen[container] {
		return
	}
	seen[container] = true
	res := doc.Resources(container)

	fontRes := (*object.Dictionary)(nil)
	if res != nil {
		fontRes = doc.ResolveDict(res.Get("Font"))
	}
	fontFor := func(name string) (*object.Dictionary, int) {
		if fontRes == nil {
			return nil, 0
		}
		ref := fontRes.Get(object.Name(name))
		objNum := 0
		if ir, ok := ref.(object.IndirectRef); ok {
			objNum = ir.Number
		}
		return doc.ResolveDict(ref), objNum
	}

	// Replay the stream's font-usage skeleton against this container's fonts,
	// unless an identical (stream, /Font) pair already contributed the same
	// attribution — its shown text is already recorded.
	if res != nil {
		sk := sfKey{key, fontRes}
		if key == nil || !applied[sk] {
			if key != nil {
				applied[sk] = true
			}
			var curFont *FontTextUsage
			mode := 0
			for _, ev := range doc.ContentFontEvents(data, key) {
				switch ev.kind {
				case evTf:
					if dict, num := fontFor(ev.name); dict != nil {
						u := usage[dict]
						if u == nil {
							u = &FontTextUsage{FontDict: dict, ObjNum: num, Modes: make(map[int]bool)}
							usage[dict] = u
						}
						curFont = u
					} else {
						curFont = nil
					}
				case evTr:
					mode = ev.mode
				case evShow:
					if curFont != nil {
						curFont.Strings = append(curFont.Strings, ev.strings...)
						curFont.Modes[mode] = true
					}
				}
			}
		}
	}

	// Recurse into executed forms and patterns. Resolve the candidates first:
	// learning *which* of them the content executes costs a full pass over the
	// stream, and a container with no form XObject and no tiling pattern in
	// scope — a page whose /XObject holds nothing but images, say — has nothing
	// to recurse into, so that pass would answer a question nobody asks.
	if res == nil {
		return
	}
	type candidate struct {
		name   string
		stream *object.Stream
	}
	var forms, patterns []candidate
	if xobjDict := doc.ResolveDict(res.Get("XObject")); xobjDict != nil {
		for i, name := range xobjDict.Keys {
			if s, ok := doc.Resolve(xobjDict.Values[i]).(*object.Stream); ok {
				if st, _ := s.Dict.Get("Subtype").(object.Name); st == "Form" {
					forms = append(forms, candidate{string(name), s})
				}
			}
		}
	}
	if patDict := doc.ResolveDict(res.Get("Pattern")); patDict != nil {
		for i, name := range patDict.Keys {
			if s, ok := doc.Resolve(patDict.Values[i]).(*object.Stream); ok {
				patterns = append(patterns, candidate{string(name), s})
			}
		}
	}
	if len(forms) == 0 && len(patterns) == 0 {
		return
	}
	used := doc.ContentUsedNamesCached(data, key)
	for _, c := range forms {
		if used.XObjects[c.name] {
			collectTextFromContainer(doc, &c.stream.Dict, doc.Content(c.stream), c.stream, usage, seen, applied)
		}
	}
	for _, c := range patterns {
		if used.Patterns[c.name] {
			collectTextFromContainer(doc, &c.stream.Dict, doc.Content(c.stream), c.stream, usage, seen, applied)
		}
	}
}

// PredefinedCMapInfo carries the CIDSystemInfo a predefined CMap implies.
type PredefinedCMapInfo struct {
	Registry string
	Ordering string
}

var PredefinedCMaps = map[string]PredefinedCMapInfo{
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

func Type0Descendant(doc View, fontDict *object.Dictionary) *object.Dictionary {
	arr, ok := doc.Resolve(fontDict.Get("DescendantFonts")).(object.Array)
	if !ok || len(arr) == 0 {
		return nil
	}
	return doc.ResolveDict(arr[0])
}

// HasForbiddenUnicodeTargets scans a ToUnicode CMap for mappings to U+0000,
// U+FEFF, or U+FFFE in bfchar/bfrange destinations.
func HasForbiddenUnicodeTargets(doc View, stream *object.Stream) bool {
	data := doc.Content(stream)
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

// LoadFontProgram parses the embedded font program of a descriptor, or nil
// when none is embedded or it cannot be parsed.
func LoadFontProgram(doc View, fd *object.Dictionary) *font.Program {
	if fd == nil {
		return nil
	}
	if s, ok := doc.Resolve(fd.Get("FontFile")).(*object.Stream); ok {
		if data := doc.Content(s); data != nil {
			return noteFontProgramLimits(doc, font.ParseType1(data))
		}
	}
	if s, ok := doc.Resolve(fd.Get("FontFile2")).(*object.Stream); ok {
		if data := doc.Content(s); data != nil {
			return noteFontProgramLimits(doc, font.ParseSFNT(data, doc.Limits.CmapWork))
		}
	}
	if s, ok := doc.Resolve(fd.Get("FontFile3")).(*object.Stream); ok {
		if data := doc.Content(s); data != nil {
			subtype, _ := s.Dict.Get("Subtype").(object.Name)
			if subtype == "OpenType" {
				if fp := ParseSFNTCFF(data); fp != nil {
					return noteFontProgramLimits(doc, fp)
				}
				return noteFontProgramLimits(doc, font.ParseSFNT(data, doc.Limits.CmapWork))
			}
			return noteFontProgramLimits(doc, font.ParseCFF(data))
		}
	}
	return nil
}

// noteFontProgramLimits reports the guard trips the font-program parsers
// recorded on the program itself. The parsers take raw bytes and have no
// Document in scope, so this is where a trip re-enters the run's recorder.
func noteFontProgramLimits(doc View, fp *font.Program) *font.Program {
	if fp != nil && fp.CmapPartial {
		doc.Note(GuardCmapWork, fmt.Sprintf("an embedded font's cmap subtable needed more than %s units of expansion work to read completely; the glyph-coverage and .notdef checks for that font were skipped rather than run against a partial character map", LimitBound(int64(doc.Limits.CmapWork), DefaultMaxCmapWork)), 0)
	}
	return fp
}

// ParseSFNTCFF returns the CFF-table font program of an OpenType/CFF font,
// falling back to the sfnt view when there is no CFF table.
func ParseSFNTCFF(data []byte) *font.Program {
	if len(data) < 12 || font.Be32(data, 0) != 0x4F54544F { // 'OTTO'
		return nil
	}
	numTables := font.Be16(data, 4)
	for i := 0; i < numTables; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			return nil
		}
		if string(data[rec:rec+4]) == "CFF " {
			off := font.Be32(data, rec+8)
			length := font.Be32(data, rec+12)
			if uint64(off)+uint64(length) <= uint64(len(data)) {
				return font.ParseCFF(data[off : off+length])
			}
		}
	}
	return nil
}

// IsIdentityEncoding reports whether a Type0 font uses Identity-H/V.
func IsIdentityEncoding(doc View, fontDict *object.Dictionary) bool {
	if n, ok := doc.Resolve(fontDict.Get("Encoding")).(object.Name); ok {
		return n == "Identity-H" || n == "Identity-V"
	}
	return false
}

// ParseCharSet parses a Type 1 /CharSet string ("/name1/name2/...") into a
// set of glyph names.
func ParseCharSet(s string) map[string]bool {
	out := make(map[string]bool)
	for {
		i := strings.IndexByte(s, '/')
		if i < 0 {
			break
		}
		s = s[i+1:]
		end := 0
		for end < len(s) && s[end] != '/' && !syntax.IsWhitespace(s[end]) {
			end++
		}
		if end > 0 {
			out[s[:end]] = true
		}
		s = s[end:]
	}
	return out
}

// CIDSet is a decoded CIDSet stream: bit i (MSB-first within each byte) set
// means CID i is present. Membership is tested directly against the bytes, so a
// large — or maliciously inflated — CIDSet costs nothing beyond the bounded
// decode. Materialising a set of every present CID could be hundreds of millions
// of map entries (a 64 MB CIDSet holds 512 M bits), which a crafted file used to
// turn into ~70s of validation.
type CIDSet []byte

// DecodeCIDSet decodes a CIDSet stream into a cidSet for membership testing.
func DecodeCIDSet(doc View, s *object.Stream) CIDSet {
	return CIDSet(doc.Content(s))
}

// UsedResourceNames records which named resources a content stream actually
// executes. Device colour (and other content-level properties) only matter
// on executed content: a form XObject that is referenced in /XObject but
// never invoked with Do does not contribute (the corpus passes a DeviceCMYK
// form that no content stream draws).
type UsedResourceNames struct {
	XObjects map[string]bool
	Patterns map[string]bool
	Shadings map[string]bool
}

const (
	evTf   fontEventKind = iota // select the font named by `name`
	evTr                        // set the text rendering mode to `mode`
	evShow                      // show `strings` with the current font/mode
)

// ContentFontEvents returns the font-usage skeleton for data, memoized per
// content stream (key) when a validation cache is present so a stream shared by
// many containers is tokenized only once.
func (d View) ContentFontEvents(data []byte, key *object.Stream) []FontEvent {
	if key != nil {
		if c := d.Run; c != nil {
			if ev, ok := c.fontEvents[key]; ok {
				return ev
			}
			ev := buildFontEvents(d.Cancel, data)
			if c.fontEvents == nil {
				c.fontEvents = make(map[*object.Stream][]FontEvent)
			}
			c.fontEvents[key] = ev
			return ev
		}
	}
	return buildFontEvents(d.Cancel, data)
}

// ContentUsedNamesCached returns contentUsedNames(data), memoized per content
// stream (key) when a validation cache is present.
func (d View) ContentUsedNamesCached(data []byte, key *object.Stream) UsedResourceNames {
	if key != nil {
		if c := d.Run; c != nil {
			if u, ok := c.usedNames[key]; ok {
				return u
			}
			u := ContentUsedNames(d.Cancel, data)
			if c.usedNames == nil {
				c.usedNames = make(map[*object.Stream]UsedResourceNames)
			}
			c.usedNames[key] = u
			return u
		}
	}
	return ContentUsedNames(d.Cancel, data)
}

// ContentBytesAndKey resolves a container's content reference to its decoded
// bytes and, when the reference is a single stream, that stream (usable as a
// per-stream memoization key). object.Array contents are container-specific
// concatenations and get no key.
func (d View) ContentBytesAndKey(ref object.Object) ([]byte, *object.Stream) {
	data := ContentStreamData(d, ref)
	if s, ok := d.Resolve(ref).(*object.Stream); ok {
		return data, s
	}
	return data, nil
}

// buildFontEvents tokenizes a decoded content stream once into a replayable
// list of font events. Font-name resolution is deliberately deferred to replay
// (it depends on the container's resources); everything captured here — the
// operand names, render modes, and shown string bytes — is a pure function of
// the stream contents.
func buildFontEvents(cancel Canceler, data []byte) []FontEvent {
	if data == nil {
		return nil
	}
	var events []FontEvent
	// lastName/lastNumber hold the raw operand bytes, which forEachContentItem
	// reports as sub-slices of data. Converting them to strings on arrival cost
	// one allocation per token — 87% of the whole PDF/UA run's allocations, since
	// numbers are by far the most common token and only the rare Tr ever reads
	// one. The conversion now happens where the value is consumed.
	var lastName, lastNumber []byte
	var pending [][]byte
	ForEachContentItem(cancel, data, func(kind ContentItemKind, payload []byte) {
		switch kind {
		case ItemName:
			lastName = payload
		case ItemNumber:
			lastNumber = payload
		case ItemString:
			pending = append(pending, append([]byte(nil), payload...))
		case ItemOperator:
			switch string(payload) {
			case "Tf":
				events = append(events, FontEvent{kind: evTf, name: string(lastName)})
				pending = nil
			case "Tr":
				m := 0
				if len(lastNumber) > 0 {
					fmt.Sscanf(string(lastNumber), "%d", &m)
				}
				events = append(events, FontEvent{kind: evTr, mode: m})
				pending = nil
			case "Tj", "TJ", "'", "\"":
				events = append(events, FontEvent{kind: evShow, strings: pending})
				pending = nil
			default:
				pending = nil
			}
		}
	})
	return events
}

// ContentStreamData extracts and concatenates content stream data.
// Handles both single stream references and arrays of stream references.
func ContentStreamData(doc View, contentsRef object.Object) []byte {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *object.Stream:
		return doc.Content(v)
	case object.Array:
		var result []byte
		for _, elem := range v {
			streamObj := doc.Resolve(elem)
			if stream, ok := streamObj.(*object.Stream); ok {
				data := doc.Content(stream)
				if data != nil {
					result = append(result, ' ')
					result = append(result, data...)
				}
			}
		}
		return result
	}
	return nil
}

func ContentUsedNames(cancel Canceler, data []byte) UsedResourceNames {
	u := UsedResourceNames{
		XObjects: make(map[string]bool),
		Patterns: make(map[string]bool),
		Shadings: make(map[string]bool),
	}
	var lastName string
	ForEachContentToken(cancel, data, func(tok []byte, isName bool) {
		if isName {
			lastName = string(tok)
			return
		}
		switch string(tok) {
		case "Do":
			u.XObjects[lastName] = true
		case "sh":
			u.Shadings[lastName] = true
		case "scn", "SCN":
			// A pattern is set by name; non-pattern scn uses numeric
			// operands, in which case lastName is stale — over-recording is
			// harmless (it only widens the scan).
			u.Patterns[lastName] = true
		}
	})
	return u
}

type ContentItemKind int

// ForEachContentItem tokenizes a decoded content stream like
// forEachContentToken, additionally reporting decoded string operands and
// distinguishing numbers from operators.
//
// The scan stops when cancel fires. Together with forEachContentToken this is
// about two thirds of a large document's validation time, which is why the
// check is gated on the scan position — one comparison per token, the poll
// itself once per cancelScanBytes. See cancel.go.
// ScanContentDict returns the index just past the >> that closes the dictionary
// starting at i (which must be a <<). Nested dictionaries, literal strings and
// hex strings are stepped over, so a ">>" inside "(a>>b)" does not end the scan.
//
// An unterminated dictionary returns len(data): a truncated content stream must
// leave the caller at the end of the input rather than at the delimiter it was
// looking at, which would not advance the scan. Nesting is capped for the same
// reason every other content walk is — the input is untrusted, and a file of
// nothing but "<<" must cost time proportional to its length, not to its depth.
func ScanContentDict(data []byte, i int) int {
	const maxDepth = 64
	n := len(data)
	depth := 0
	for i < n {
		switch {
		case data[i] == '<' && i+1 < n && data[i+1] == '<':
			depth++
			i += 2
			if depth > maxDepth {
				return n
			}
		case data[i] == '>' && i+1 < n && data[i+1] == '>':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		case data[i] == '(':
			_, i = DecodeContentLiteralString(data, i)
		case data[i] == '<':
			i++
			for i < n && data[i] != '>' {
				i++
			}
			if i < n {
				i++
			}
		default:
			i++
		}
	}
	return n
}

func ForEachContentItem(cancel Canceler, data []byte, fn func(kind ContentItemKind, payload []byte)) {
	n := len(data)
	i := 0
	nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
	for i < n {
		if i >= nextCancelCheck {
			if cancel.Stopped() {
				return
			}
			nextCancelCheck = i + CancelScanBytes
		}
		for i < n && IsContentWS(data[i]) {
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
			str, next := DecodeContentLiteralString(data, i)
			fn(ItemString, str)
			i = next
		case b == '<' && i+1 < n && data[i+1] == '<':
			end := ScanContentDict(data, i)
			fn(ItemDict, data[i:end])
			i = end
		case b == '<':
			i++
			start := i
			for i < n && data[i] != '>' {
				i++
			}
			fn(ItemString, decodeHexBytes(data[start:i]))
			if i < n {
				i++
			}
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '[' || b == ']' || b == '{' || b == '}' || b == ')':
			// A stray ')' (unbalanced by any '(') is not the start of a token;
			// consume it so the scan always advances. Without this, a content
			// stream with an unmatched ')' — e.g. leaked inline-image sample
			// data — spins forever, since ')' is a delimiter the default token
			// scan below cannot consume (a parser DoS on untrusted input).
			i++
		case b == '/':
			i++
			start := i
			for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
				i++
			}
			fn(ItemName, data[start:i])
		default:
			start := i
			// Numeric tokens may be arbitrarily long (Annex C allows huge
			// precision); read them whole. Non-numeric keyword tokens are
			// capped to bound scanning over stray binary data.
			numeric := data[i] >= '0' && data[i] <= '9' || data[i] == '+' || data[i] == '-' || data[i] == '.'
			for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
				i++
			}
			if i == start {
				// Defensive: an unhandled delimiter would yield no token and no
				// progress. Skip it so the scan can never stall.
				i++
				continue
			}
			if !numeric && i-start > MaxContentTokenLen {
				continue // binary run, not a keyword; see scanStreamForDeviceOps
			}
			tok := data[start:i]
			if len(tok) == 2 && tok[0] == 'B' && tok[1] == 'I' {
				SkipInlineImage(data, &i)
				continue
			}
			if numeric {
				fn(ItemNumber, tok)
				continue
			}
			fn(ItemOperator, tok)
		}
	}
}

// ForEachContentToken is forEachContentOperator's core walker; it also
// reports name tokens (without the leading slash) so callers can associate
// operand names with the operators that consume them.
//
// The scan stops when cancel fires, checked every cancelScanBytes of input;
// see cancel.go for why the check is gated on the scan position rather than
// run per token.
func ForEachContentToken(cancel Canceler, data []byte, fn func(tok []byte, isName bool)) {
	n := len(data)
	i := 0
	nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
	for i < n {
		if i >= nextCancelCheck {
			if cancel.Stopped() {
				return
			}
			nextCancelCheck = i + CancelScanBytes
		}
		for i < n && IsContentWS(data[i]) {
			i++
		}
		if i >= n {
			return
		}
		switch b := data[i]; {
		case b == '%': // comment to end of line
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case b == '(': // string literal with escapes and balanced parens
			depth := 1
			i++
			for i < n && depth > 0 {
				switch data[i] {
				case '\\':
					i++ // skip escaped char
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
		case b == '<':
			i++
			if i < n && data[i] == '<' {
				i++ // <<
			} else { // hex string
				for i < n && data[i] != '>' {
					i++
				}
				if i < n {
					i++
				}
			}
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '[' || b == ']' || b == '{' || b == '}' || b == ')':
			// A stray ')' is a delimiter, not a token start; consume it so the
			// scan always advances (an unmatched ')' would otherwise spin
			// forever — a DoS on untrusted content).
			i++
		case b == '/':
			i++
			start := i
			for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
				i++
			}
			fn(data[start:i], true)
		default:
			start := i
			for i < n && !IsContentWS(data[i]) && !IsContentDelim(data[i]) {
				i++
			}
			if i == start {
				// Defensive: an unhandled delimiter yields no progress; skip it.
				i++
				continue
			}
			if i-start > MaxContentTokenLen {
				continue // binary run, not a token; see scanStreamForDeviceOps
			}
			tok := data[start:i]
			if len(tok) == 2 && tok[0] == 'B' && tok[1] == 'I' {
				SkipInlineImage(data, &i)
				continue
			}
			fn(tok, false)
		}
	}
}

const (
	ItemOperator ContentItemKind = iota
	ItemName
	ItemString
	ItemNumber
	// ItemDict reports a dictionary operand — a BDC/DP property list — as the
	// raw bytes from << to the matching >>. It is delivered whole rather than as
	// the loose tokens between the delimiters because a property list is a PDF
	// object: only a parser can tell /Lang's value from a name that happens to
	// follow it. Callers that do not care simply omit the case.
	ItemDict
)

// MaxContentTokenLen is the longest run of non-delimiter bytes the content
// tokenizers will hand to a caller as a token. Every PDF operator is at most
// three characters and no keyword operand comes close to this, so a longer run
// is binary data that a delimiter never terminated — most often the sample
// bytes of an inline image whose EI was not found.
//
// The scanners drop such a run whole. They used to stop reading at the cap and
// let the scan re-enter mid-run, which manufactured tokens out of binary: a
// 300-byte run whose 257th byte was 'k' produced a one-byte "k" operator and
// with it "DeviceCMYK used without matching OutputIntent or DefaultCMYK", and
// an alphabetic fragment produced "content stream contains an operator not
// defined in ISO 32000" — findings the complete token never supports. Reading
// the run to its end costs the same single linear pass the chunked version did.
//
// This one is not configurable, and deliberately: it is not a resource ceiling
// a caller might want to spend more on but a statement about what a PDF token
// can be. Moving it would change which byte runs count as operators, i.e. what
// the tokenizer means, not how much of it runs.
const MaxContentTokenLen = 256

// decodeHexBytes decodes hex-string content, tolerating white space and
// padding an odd digit count. The root package keeps its own copy for the
// file-structure rules; the function is frozen by the syntax of a PDF hex
// string, so there is nothing for the two to drift about.
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

func DecodeContentLiteralString(data []byte, i int) ([]byte, int) {
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

// Has reports whether CID i is marked present.
func (c CIDSet) Has(i int) bool {
	b := i / 8
	return b >= 0 && b < len(c) && c[b]&(0x80>>(uint(i)%8)) != 0
}

// Empty reports whether no CID is marked present (an absent or all-zero set).
func (c CIDSet) Empty() bool {
	for _, b := range c {
		if b != 0 {
			return false
		}
	}
	return true
}
