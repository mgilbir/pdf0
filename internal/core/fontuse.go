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
// to Unicode. Both read the same execution of the content (interp.go), which
// the run memoises, so that is one walk instead of two.

// FontTextUsage aggregates the text shown with one font dictionary.
type FontTextUsage struct {
	FontDict *object.Dictionary
	ObjNum   int      // font object number (0 if direct)
	Strings  [][]byte // raw shown string bytes
	Modes    map[int]bool

	// pending is shown strings not yet decoded into Strings (interp.go).
	pending []rawString
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
	var lastName []byte
	lx := NewContentLexer(cancel, data)
	var t ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case ContentName:
			lastName = t.Raw
		case ContentDictStart:
			lx.SkipDict(&t)
		case ContentOperator:
			switch string(t.Raw) {
			case "Do":
				u.XObjects[contentName(lastName)] = true
			case "sh":
				u.Shadings[contentName(lastName)] = true
			case "scn", "SCN":
				// A pattern is set by name; non-pattern scn uses numeric
				// operands, in which case lastName is stale — over-recording is
				// harmless (it only widens the scan).
				u.Patterns[contentName(lastName)] = true
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
