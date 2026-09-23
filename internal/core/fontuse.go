package core

import (
	"fmt"
	"strings"

	"github.com/mgilbir/forme/font"
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
			data, key, _ := doc.ContentBytesAndKey(page.Dict.Get("Contents")) // reason: presence-only walk; the producer recorded any declined trip
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
		for name, xref := range xobjDict.All() {
			if s, ok := doc.Resolve(xref).(*object.Stream); ok {
				if st, _ := doc.ResolveName(s.Dict.Get("Subtype")); st == "Form" {
					forms = append(forms, candidate{string(name), s})
				}
			}
		}
	}
	if patDict := doc.ResolveDict(res.Get("Pattern")); patDict != nil {
		for name, pref := range patDict.All() {
			if s, ok := doc.Resolve(pref).(*object.Stream); ok {
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
			data, _ := doc.Content(c.stream) // reason: presence-only walk; the producer recorded any declined trip
			collectTextFromContainer(doc, &c.stream.Dict, data, c.stream, usage, seen, applied)
		}
	}
	for _, c := range patterns {
		if used.Patterns[c.name] {
			data, _ := doc.Content(c.stream) // reason: presence-only walk; the producer recorded any declined trip
			collectTextFromContainer(doc, &c.stream.Dict, data, c.stream, usage, seen, applied)
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
	data, _ := doc.Content(stream) // reason: presence-only; a stream not read finds nothing and the producer recorded any declined trip
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

// LoadFontProgram parses the embedded font program of a descriptor.
//
// The Reason says what a nil program means, and the difference is the one a
// check needs: ReasonAbsent (no program embedded — the embedding rule's
// business), ReasonMalformed (the stream does not decode or the program does
// not parse: the program is damaged), or a declined reason (pdf0 did not read
// it — a size limit, an unimplemented filter, ciphertext — and nothing may be
// concluded about the font). A declined program used to come back as the same
// nil as a damaged one, and a 760 KB font read under a 100 KB scanning limit
// was reported as "damaged" (audit 2026-09-22 C47).
func LoadFontProgram(doc View, fd *object.Dictionary) (*font.Program, Reason) {
	if fd == nil {
		return nil, ReasonAbsent
	}
	program := func(fp *font.Program) (*font.Program, Reason) {
		if fp == nil {
			return nil, ReasonMalformed
		}
		return noteFontProgramLimits(doc, fp), ReasonOK
	}
	if s, ok := doc.Resolve(fd.Get("FontFile")).(*object.Stream); ok {
		data, r := doc.Content(s)
		if r != ReasonOK {
			return nil, r
		}
		return program(font.ParseType1(data))
	}
	if s, ok := doc.Resolve(fd.Get("FontFile2")).(*object.Stream); ok {
		data, r := doc.Content(s)
		if r != ReasonOK {
			return nil, r
		}
		return program(font.ParseSFNT(data, doc.Limits.CmapWork))
	}
	if s, ok := doc.Resolve(fd.Get("FontFile3")).(*object.Stream); ok {
		data, r := doc.Content(s)
		if r != ReasonOK {
			return nil, r
		}
		subtype, _ := doc.ResolveName(s.Dict.Get("Subtype"))
		if subtype == "OpenType" {
			if fp := ParseSFNTCFF(data); fp != nil {
				return program(fp)
			}
			return program(font.ParseSFNT(data, doc.Limits.CmapWork))
		}
		return program(font.ParseCFF(data))
	}
	return nil, ReasonAbsent
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
// A CIDSet that was not decoded is nil with a Reason other than ReasonOK; only
// ReasonOK makes an empty set mean "lists no CID".
func DecodeCIDSet(doc View, s *object.Stream) (CIDSet, Reason) {
	data, r := doc.Content(s)
	return CIDSet(data), r
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
// concatenations and get no key. The Reason is ContentStreamData's.
func (d View) ContentBytesAndKey(ref object.Object) ([]byte, *object.Stream, Reason) {
	data, r := ContentStreamData(d, ref)
	if s, ok := d.Resolve(ref).(*object.Stream); ok {
		return data, s, r
	}
	return data, nil, r
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
	// lastName and lastNumber are kept as tokens, whose bytes are sub-slices of
	// data: converting every name and number on arrival cost one allocation
	// per token — 87% of a PDF/UA run's allocations, since numbers are by far
	// the most common token and only the rare Tf and Tr read one.
	var lastName, lastNumber ContentTok
	var pending [][]byte
	lx := NewContentLexer(cancel, data)
	var t ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case ContentName:
			lastName = t
		case ContentNumber:
			lastNumber = t
		case ContentString, ContentHexString:
			pending = append(pending, t.Bytes())
		case ContentDictStart:
			lx.SkipDict(&t)
		case ContentOperator:
			switch string(t.Raw) {
			case "Tf":
				events = append(events, FontEvent{kind: evTf, name: lastName.Name()})
			case "Tr":
				events = append(events, FontEvent{kind: evTr, mode: int(lastNumber.Number())})
			case "Tj", "TJ", "'", "\"":
				events = append(events, FontEvent{kind: evShow, strings: pending})
			}
			pending = nil
		}
	}
	return events
}

// ContentStreamData extracts and concatenates content stream data.
// Handles both single stream references and arrays of stream references.
//
// For an array the result is every part that decoded, and the Reason is the
// worst of the parts (Reason.Worse): a check may assert on what the decoded
// parts contain, but not on what they lack unless the Reason is ReasonOK.
// No /Contents at all is ReasonAbsent — an empty page, which is legal.
func ContentStreamData(doc View, contentsRef object.Object) ([]byte, Reason) {
	resolved := doc.Resolve(contentsRef)
	switch v := resolved.(type) {
	case *object.Stream:
		return doc.Content(v)
	case object.Array:
		var result []byte
		reason := ReasonOK
		for _, elem := range v {
			streamObj := doc.Resolve(elem)
			if stream, ok := streamObj.(*object.Stream); ok {
				data, r := doc.Content(stream)
				reason = reason.Worse(r)
				if data != nil {
					result = append(result, ' ')
					result = append(result, data...)
				}
			}
		}
		return result, reason
	}
	return nil, ReasonAbsent
}

func ContentUsedNames(cancel Canceler, data []byte) UsedResourceNames {
	u := UsedResourceNames{
		XObjects: make(map[string]bool),
		Patterns: make(map[string]bool),
		Shadings: make(map[string]bool),
	}
	var lastName ContentTok
	lx := NewContentLexer(cancel, data)
	var t ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case ContentName:
			lastName = t
		case ContentDictStart:
			lx.SkipDict(&t)
		case ContentOperator:
			switch string(t.Raw) {
			case "Do":
				u.XObjects[lastName.Name()] = true
			case "sh":
				u.Shadings[lastName.Name()] = true
			case "scn", "SCN":
				// A pattern is set by name; non-pattern scn uses numeric
				// operands, in which case lastName is stale — over-recording is
				// harmless (it only widens the scan).
				u.Patterns[lastName.Name()] = true
			}
		}
	}
	return u
}

// MaxContentTokenLen is the longest run of non-delimiter bytes the content
// lexer will hand to a caller as a keyword. Every PDF operator is at most
// three characters and no keyword operand comes close to this, so a longer run
// is binary data that a delimiter never terminated — most often the sample
// bytes of an inline image whose EI was not found.
//
// The lexer drops such a run whole. Scanners used to stop reading at the cap
// and let the scan re-enter mid-run, which manufactured tokens out of binary: a
// 300-byte run whose 257th byte was 'k' produced a one-byte "k" operator and
// with it "DeviceCMYK used without matching OutputIntent or DefaultCMYK", and
// an alphabetic fragment produced "content stream contains an operator not
// defined in ISO 32000" — findings the complete token never supports.
//
// This one is not configurable, and deliberately: it is not a resource ceiling
// a caller might want to spend more on but a statement about what a PDF token
// can be. Moving it would change which byte runs count as operators, i.e. what
// the tokenizer means, not how much of it runs.
const MaxContentTokenLen = 256

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
