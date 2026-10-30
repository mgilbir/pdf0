// Package fontcheck holds the font-program checks the PDF/A and PDF/UA
// validators both make, so that each rule is written once and the two engines
// answer it the same way.
//
// ISO 19005 and ISO 14289 state several font requirements in the same words,
// and veraPDF's profiles test them with the same expressions: the width a font
// dictionary declares for a shown glyph must agree with the embedded program's
// (ISO 19005-2 6.2.11.5, ISO 14289-1 7.21.5, ISO 14289-2 8.4.5.6), and a
// non-symbolic TrueType font's /Differences may name only glyphs from the
// Adobe Glyph List (ISO 19005-2 6.2.11.6, ISO 14289-1 7.21.6, ISO 14289-2
// 8.4.5.7). The machinery under them — reading a simple font's encoding and
// /Widths, a CIDFont's /W, a Type 3 glyph's d0/d1, and the glyph a shown code
// selects in the program — lives here. What a finding says, which clause it
// cites, and which rendering modes a rule exempts are each engine's own.
//
// The package is internal: it is the validators' shared implementation, not
// API.
package fontcheck

import (
	"iter"
	"math"
	"slices"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/simplefont"
)

// WidthTolerance is how far, in thousandths of text space, a declared glyph
// width may differ from the program's and still agree. veraPDF's profiles, for
// PDF/A and PDF/UA alike, allow a difference of at most 1.
const WidthTolerance = 1.0

// Symbolic reports a font descriptor's Symbolic flag (bit 3 of /Flags). A nil
// descriptor is not symbolic.
func Symbolic(doc core.View, fd *object.Dictionary) bool {
	if fd == nil {
		return false
	}
	if flags, ok := doc.Resolve(fd.Get("Flags")).(object.Integer); ok {
		return flags&4 != 0
	}
	return false
}

// SimpleFontCodeToName builds the character-code to glyph-name table for a
// simple font per ISO 32000-1, 9.6.6: a base encoding (named, or the font's
// implicit one) updated by any Differences array.
func SimpleFontCodeToName(doc core.View, fontDict *object.Dictionary, symbolic bool) map[byte]string {
	table := make(map[byte]string)
	applyBase := func(name object.Name) {
		enc, ok := simplefont.EncodingNamed(string(name))
		if !ok {
			return
		}
		for c, n := range enc.Codes() {
			table[c] = n
		}
	}

	switch enc := doc.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		applyBase(enc)
	case *object.Dictionary:
		if base, ok := doc.Resolve(enc.Get("BaseEncoding")).(object.Name); ok {
			applyBase(base)
		} else if !symbolic {
			applyBase("StandardEncoding")
		}
		if diffs, ok := doc.Resolve(enc.Get("Differences")).(object.Array); ok {
			code := 0
			for _, el := range diffs {
				switch v := doc.Resolve(el).(type) {
				case object.Integer:
					code = int(v)
				case object.Name:
					if code >= 0 && code < 256 {
						table[byte(code)] = string(v)
					}
					code++
				}
			}
		}
		// ISO 32000-1 9.6.6.4, for a non-symbolic TrueType font whose
		// Encoding is a dictionary: "the table shall be initialized with the
		// entries from the dictionary's BaseEncoding entry ... Any entries in
		// the Differences array shall be used to update the table. Finally,
		// any undefined entries in the table shall be filled using
		// StandardEncoding." A BaseEncoding that names no predefined encoding
		// (/Identity, say) initializes nothing, so every code not in
		// Differences takes its StandardEncoding name. veraPDF's
		// TrueTypeFontProgram builds the same table.
		if st, _ := doc.ResolveName(fontDict.Get("Subtype")); st == "TrueType" && !symbolic {
			if std, ok := simplefont.EncodingNamed("StandardEncoding"); ok {
				for c, n := range std.Codes() {
					if _, set := table[c]; !set {
						table[c] = n
					}
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

// BaseEncodingModelled reports whether the base encoding under the table
// SimpleFontCodeToName builds is one this package has a table for. When it is
// not — a symbolic font whose base is the font program's own built-in
// encoding, or a predefined encoding outside ISO 32000-1 Annex D.2 such as
// MacExpertEncoding — a code absent from the table is "unknown", not
// "unencoded": only the Differences entries are trustworthy. When it is,
// absence is real and the code selects .notdef (ISO 32000-1, 9.6.6).
func BaseEncodingModelled(doc core.View, fontDict *object.Dictionary, symbolic bool) bool {
	modelled := func(n object.Name) bool {
		switch n {
		case "WinAnsiEncoding", "MacRomanEncoding", "StandardEncoding":
			return true
		}
		return false
	}
	switch enc := doc.Resolve(fontDict.Get("Encoding")).(type) {
	case object.Name:
		return modelled(enc)
	case *object.Dictionary:
		if base, ok := doc.Resolve(enc.Get("BaseEncoding")).(object.Name); ok {
			return modelled(base)
		}
		// No BaseEncoding: a non-symbolic font falls back to
		// StandardEncoding, a symbolic one to its built-in encoding.
		return !symbolic
	case nil:
		return !symbolic
	}
	return false
}

// Simple is a simple font — TrueType, Type 1 or CFF — read for checking the
// glyphs it shows: its embedded program, its encoding, and the widths its
// dictionary declares. A Type 3 font has no program; Type3WidthDiffers is its
// width rule.
type Simple struct {
	Program  *font.Program
	Subtype  object.Name
	Symbolic bool
	// Encoding is SimpleFontCodeToName's table for the font.
	Encoding map[byte]string
	// EncodingModelled is BaseEncodingModelled's answer: whether a code
	// Encoding has no name for selects .notdef, or is unknown.
	EncodingModelled bool
	// ByCode is true for a TrueType font whose codes select glyphs through
	// the program's (3,0) or (1,0) cmap by the code itself, not by glyph
	// name: "When the font has no Encoding entry, or the font descriptor's
	// Symbolic flag is set" (ISO 32000-1 9.6.6.4). veraPDF reads a
	// non-symbolic font with no /Encoding so too.
	ByCode bool
	// ByName is true for a non-symbolic TrueType program whose cmap has
	// neither of the subtables ISO 32000-1 9.6.6.4 looks a named code up in,
	// Microsoft Unicode (3,1) and Mac Roman (1,0). A named code's glyph is
	// then the one the program's post table gives that name (NamedGlyph), as
	// veraPDF looks it up, and none when the table names no such glyph —
	// what is drawn is then glyph 0, whose width is compared. pdf0's program
	// reader folds a Unicode-platform (0,x) subtable into the program's
	// Cmap, which is not consulted here.
	ByName bool

	codes        codeRange
	widths       object.Array
	haveWidths   bool
	missingWidth float64
}

// LoadSimple reads a simple font. It returns the font's descriptor whatever
// happens, and a nil Simple when the embedded program could not be read, with
// LoadFontProgram's Reason: absent, declined (a limit, a filter, ciphertext),
// or malformed.
func LoadSimple(doc core.View, fontDict *object.Dictionary) (*Simple, *object.Dictionary, core.Reason) {
	fd := doc.ResolveDict(fontDict.Get("FontDescriptor"))
	fp, r := core.LoadFontProgram(doc, fd)
	if fp == nil {
		return nil, fd, r
	}
	return newSimple(doc, fontDict, fd, fp), fd, core.ReasonOK
}

// newSimple reads the font dictionary and descriptor around a program
// already read.
func newSimple(doc core.View, fontDict, fd *object.Dictionary, fp *font.Program) *Simple {
	s := &Simple{Program: fp, Symbolic: Symbolic(doc, fd)}
	s.Subtype, _ = doc.ResolveName(fontDict.Get("Subtype"))
	s.Encoding = SimpleFontCodeToName(doc, fontDict, s.Symbolic)
	s.EncodingModelled = BaseEncodingModelled(doc, fontDict, s.Symbolic)
	s.ByCode = s.Subtype == "TrueType" && (s.Symbolic || fontDict.Get("Encoding") == nil)
	if s.Subtype == "TrueType" && !s.ByCode {
		if encs, known := CmapEncodings(doc, fd); known &&
			!slices.Contains(encs, CmapEncoding{3, 1}) && !slices.Contains(encs, CmapEncoding{1, 0}) {
			s.ByName = true
		}
	}
	s.codes = readCodeRange(doc, fontDict)
	s.widths, s.haveWidths = doc.Resolve(fontDict.Get("Widths")).(object.Array)
	if fd != nil {
		s.missingWidth = numVal(doc, fd.Get("MissingWidth"))
	}
	return s
}

// ProgramWidth is the advance width the embedded program draws a code with,
// in thousandths of text space: by glyph index through the cmap for TrueType,
// by glyph name for Type 1 and CFF.
//
// A Type 1 or CFF code whose name the program does not define, or that the
// encoding names no glyph for, draws .notdef: "If an encoding maps to a
// character name that does not exist in the Type 1 font program, the .notdef
// glyph shall be substituted" (ISO 32000-1 9.6.6.2). Its width is .notdef's.
// A code without a name when the base encoding is the program's own, which
// pdf0 does not read (EncodingModelled), names a glyph pdf0 cannot know, and
// has no width. (TrueType gets .notdef from simplefont.TrueTypeGlyph, as glyph
// 0.)
func (s *Simple) ProgramWidth(code byte) (float64, bool) {
	name := s.Encoding[code]
	if s.Subtype == "TrueType" {
		gid, ok := simplefont.TrueTypeGlyph(s.Program, s.ByCode, code, name)
		if s.ByName && name != "" {
			gid, _ = s.NamedGlyph(name) // glyph 0 when the program has none
			ok = true
		}
		if !ok || gid >= len(s.Program.WidthByGID) {
			return 0, false
		}
		return s.Program.WidthByGID[gid], true
	}
	switch {
	case name != "" && s.Program.GlyphNames[name]:
	case name != "" || s.EncodingModelled:
		name = ".notdef"
	default:
		return 0, false
	}
	w, ok := s.Program.WidthByName[name]
	return w, ok
}

// NamedGlyph is the glyph a ByName program's post table gives name, and
// whether it names one.
func (s *Simple) NamedGlyph(name string) (int, bool) {
	gid, ok := s.Program.GlyphByName[name]
	return gid, ok
}

// DeclaredWidth is the width the font dictionary declares for a code:
// Widths[code-FirstChar] when in range, else the descriptor's /MissingWidth —
// 0 when it has none (ISO 32000-1 Tables 111 and 122). A dictionary with no
// /Widths array declares a width only through a non-zero /MissingWidth.
func (s *Simple) DeclaredWidth(doc core.View, code byte) (float64, bool) {
	return declaredWidth(doc, s.widths, s.haveWidths, s.codes, code, s.missingWidth)
}

// WidthDiffers reports whether the program and the dictionary both give a
// width for code and the two differ by more than WidthTolerance.
func (s *Simple) WidthDiffers(doc core.View, code byte) bool {
	progW, haveProg := s.ProgramWidth(code)
	pdfW, havePDF := s.DeclaredWidth(doc, code)
	return haveProg && havePDF && math.Abs(pdfW-progW) > WidthTolerance
}

// CID is a Type 0 font's descendant CIDFont read for checking the glyphs it
// shows: its program, the CIDToGIDMap that names a CID's glyph, the CMap that
// cuts shown strings into codes, and the widths /W and /DW declare.
type CID struct {
	Program *font.Program
	// Subtype is the descendant's: CIDFontType0 or CIDFontType2.
	Subtype   object.Name
	GlyphMap  core.CIDGlyphMap
	CMap      *core.CMap
	HaveCMap  bool // false: a predefined CMap whose data is not carried; no code can be cut
	dw        float64
	w         *CIDWidths
	wComplete bool
}

// LoadCID reads a Type 0 font's descendant. It returns a nil CID and no
// descriptor when there is no descendant, and a nil CID with the descriptor and
// LoadFontProgram's Reason when the program could not be read.
func LoadCID(doc core.View, fontDict *object.Dictionary) (*CID, *object.Dictionary, core.Reason) {
	desc := core.Type0Descendant(doc, fontDict)
	if desc == nil {
		return nil, nil, core.ReasonAbsent
	}
	fd := doc.ResolveDict(desc.Get("FontDescriptor"))
	fp, r := core.LoadFontProgram(doc, fd)
	if fp == nil {
		return nil, fd, r
	}
	c := &CID{Program: fp}
	c.Subtype, _ = doc.ResolveName(desc.Get("Subtype"))
	c.GlyphMap = core.LoadCIDGlyphMap(doc, desc, c.Subtype)
	// The CMap: how this font's character codes become CIDs. Identity-H is one
	// answer and an embedded CMap stream is another; a predefined name is data
	// this module does not carry, and cmap is nil for it — LoadCMap records
	// that skip itself.
	var cmapReason core.Reason
	c.CMap, cmapReason = core.LoadCMap(doc, fontDict)
	c.HaveCMap = cmapReason == core.ReasonOK
	c.dw = 1000.0
	if v := doc.Resolve(desc.Get("DW")); v != nil {
		c.dw = numVal(doc, v)
	}
	c.w, c.wComplete = ParseCIDWidths(doc, desc.Get("W"))
	return c, fd, core.ReasonOK
}

// CIDGlyph is one code a CIDFont shows, as its CMap cuts the string.
type CIDGlyph struct {
	Code core.Code
	// Key is the glyph the CID selects in the program, when KeyKnown: a map
	// pdf0 could not read names none, and nothing is asserted about the glyph.
	Key      int
	KeyKnown bool
}

// Shown yields the codes in one shown string. A code the CMap cannot place
// (core.Code.Unknown — left to a CMap whose data is not carried) is skipped; a
// code the CMap does not define is yielded with Code.Mapped false and nothing
// else set, because it names no glyph. Nothing is yielded without a CMap.
func (c *CID) Shown(s []byte) iter.Seq[CIDGlyph] {
	return func(yield func(CIDGlyph) bool) {
		if !c.HaveCMap {
			return
		}
		for _, code := range c.CMap.Decode(s) {
			if code.Unknown {
				continue
			}
			g := CIDGlyph{Code: code}
			if code.Mapped {
				g.Key, g.KeyKnown = c.GlyphMap.Glyph(code.CID)
			}
			if !yield(g) {
				return
			}
		}
	}
}

// DeclaredWidth is the width /W declares for cid, or /DW. When /W is present
// and cannot be read, a CID it might have covered has no declared width.
func (c *CID) DeclaredWidth(cid int) (float64, bool) {
	if w, ok := c.w.Width(cid); ok {
		return w, true
	}
	return c.dw, c.wComplete
}

// ProgramWidth is the advance width the embedded program draws a shown code
// with, in thousandths of text space.
//
// A code the CMap maps to no CID, and a CID whose glyph the program lacks, are
// drawn with a substitute: the CID a notdef mapping in the CMap names, and
// failing that "the glyph for CID 0" (ISO 32000-1 9.7.6.3). Identity-H and -V
// have no notdef mappings, so under them the substitute is CID 0's glyph, and
// its width is the program's. A CMap of the document's own may have notdef
// mappings, which are not consulted for a missing glyph here, so under one the
// substitute is not known and there is no width. A map pdf0 could not read
// names no glyph, and has no width either.
func (c *CID) ProgramWidth(g CIDGlyph) (float64, bool) {
	if g.Code.Mapped {
		if !g.KeyKnown {
			return 0, false
		}
		if w, ok := c.GlyphMap.Width(c.Program, g.Key); ok {
			return w, true
		}
	}
	if !c.CMap.Identity() {
		return 0, false
	}
	key, ok := c.GlyphMap.Glyph(0)
	if !ok {
		return 0, false
	}
	return c.GlyphMap.Width(c.Program, key)
}

// WidthDiffers reports whether a shown glyph's declared and program widths
// (ProgramWidth) are both known and differ by more than WidthTolerance. The
// declared width is the code's CID's — CID 0's for a code the CMap maps to
// none. The answer depends on the CID alone, and is the same for every code
// the CMap leaves unmapped, so a caller may ask once per CID, and once for the
// unmapped codes.
func (c *CID) WidthDiffers(g CIDGlyph) bool {
	progW, haveProg := c.ProgramWidth(g)
	if !haveProg {
		return false
	}
	pdfW, havePDF := c.DeclaredWidth(g.Code.CID)
	return havePDF && math.Abs(pdfW-progW) > WidthTolerance
}

// Type3WidthDiffers compares each shown Type 3 glyph's advance — the wx
// operand of the d0/d1 operator that starts its CharProc — with the font's
// /Widths entry for its code (ISO 32000-1 9.6.5, 9.10).
//
// Both numbers are in glyph space: Table 112 says so of a Type 3 font's
// /Widths, unlike every other simple font's, and d0/d1 take glyph-space
// operands. They are therefore compared as written, with no FontMatrix in
// between. The check used to scale the advance into thousandths of text space
// and compare that with the glyph-space /Widths, which agreed only when the
// FontMatrix scale happened to be 0.001: a [1/2048 0 0 1/2048 0 0] font with
// matching widths was reported, and so was every font with a rotated matrix,
// whose a is 0 (audit 2026-09-22 C67).
//
// The FontMatrix still sets the tolerance. The other font kinds allow
// WidthTolerance thousandths of text space, and a glyph-space unit is
// |FontMatrix·(1,0)| text-space units long, so the same allowance is
// WidthTolerance / (1000·|(a, b)|) glyph units: one unit for the usual 0.001
// matrix, and a thousandth of one for an identity matrix, whose glyph space is
// text space. A matrix that maps the x axis to nothing gives no length to
// compare on, and the check declines.
//
// Every shown glyph's CharProc is read even after a difference is found, so
// the limits a damaged CharProc trips are recorded whatever the answer.
func Type3WidthDiffers(doc core.View, fontDict *object.Dictionary, u *core.FontTextUsage) bool {
	charProcs := doc.ResolveDict(fontDict.Get("CharProcs"))
	if charProcs == nil {
		return false
	}
	enc := SimpleFontCodeToName(doc, fontDict, false)
	codes := readCodeRange(doc, fontDict)
	widths, haveWidths := doc.Resolve(fontDict.Get("Widths")).(object.Array)
	var missingWidth float64 // a Type 3 font's descriptor is optional
	if fd := doc.ResolveDict(fontDict.Get("FontDescriptor")); fd != nil {
		missingWidth = numVal(doc, fd.Get("MissingWidth"))
	}
	fm := parseFontMatrix(doc, fontDict.Get("FontMatrix"))
	unit := math.Hypot(fm[0], fm[1]) // text-space length of one glyph-space x unit
	if unit == 0 || math.IsNaN(unit) || math.IsInf(unit, 0) {
		return false
	}
	tolerance := WidthTolerance / (1000 * unit)

	differs := false
	for _, s := range u.Strings {
		for _, code := range s {
			cp, ok := doc.Resolve(charProcs.Get(object.Name(enc[code]))).(*object.Stream)
			if !ok {
				continue
			}
			glyphW, ok := type3GlyphWidth(doc, cp)
			if !ok {
				continue
			}
			pdfW, havePDF := declaredWidth(doc, widths, haveWidths, codes, code, missingWidth)
			if havePDF && math.Abs(pdfW-glyphW) > tolerance {
				differs = true
			}
		}
	}
	return differs
}

// codeRange is a simple font's /FirstChar and /LastChar; ok only when the
// dictionary has both.
type codeRange struct {
	first, last int
	ok          bool
}

func readCodeRange(doc core.View, fontDict *object.Dictionary) codeRange {
	first, okF := doc.ResolveInt(fontDict.Get("FirstChar"))
	last, okL := doc.ResolveInt(fontDict.Get("LastChar"))
	return codeRange{first: int(first), last: int(last), ok: okF && okL}
}

// declaredWidth returns the width a simple font's dictionary declares for a
// code: Widths[code-FirstChar] when the code is in FirstChar to LastChar; else
// missingWidth, which is 0
// when the descriptor gives none, when the dictionary has a /Widths array
// (haveWidths) — "For character codes outside the range FirstChar to
// LastChar, the value of MissingWidth ... shall be used" (ISO 32000-1 Table
// 111) — or, without one, when missingWidth is not zero. A dictionary missing
// either bound has no range, and every code takes missingWidth: a font
// without /LastChar declares none of its /Widths (veraPDF 6.2.11.5 fails
// such a font where the program's widths are not 0).
func declaredWidth(doc core.View, widths object.Array, haveWidths bool, codes codeRange, code byte, missingWidth float64) (float64, bool) {
	idx := int(code) - codes.first
	if codes.ok && int(code) <= codes.last && idx >= 0 && idx < len(widths) {
		return numVal(doc, widths[idx]), true
	}
	if haveWidths || missingWidth != 0 {
		return missingWidth, true
	}
	return 0, false
}

// parseFontMatrix reads a Type 3 /FontMatrix (default [0.001 0 0 0.001 0 0]).
func parseFontMatrix(doc core.View, o object.Object) [6]float64 {
	fm := [6]float64{0.001, 0, 0, 0.001, 0, 0}
	if arr, ok := doc.Resolve(o).(object.Array); ok && len(arr) == 6 {
		for i := 0; i < 6; i++ {
			fm[i] = numVal(doc, arr[i])
		}
	}
	return fm
}

// type3GlyphWidth reads the w operand of the leading d0/d1 operator of a
// Type 3 CharProc content stream (glyph-space units).
func type3GlyphWidth(doc core.View, cp *object.Stream) (float64, bool) {
	data, _ := doc.Content(cp) // reason: no data is no width, and the width check declines; the producer recorded any declined trip
	if data == nil {
		return 0, false
	}
	// Each operand since the last operator, and whether it is a PDF number. A
	// token the lexer calls a number but that is not one ("1e3", "1.2.3") is
	// kept in its place, so that it cannot shift the operand after it into
	// the width's position, and a width written that way is no width at all.
	type operand struct {
		v  float64
		ok bool
	}
	var nums []operand
	lx := core.NewContentLexer(doc.Cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentNumber:
			v, _, ok := t.ParseNumber()
			nums = append(nums, operand{v, ok})
		case core.ContentOperator:
			switch string(t.Raw) {
			case "d0", "d1":
				if len(nums) >= 1 {
					return nums[0].v, nums[0].ok
				}
				nums = nums[:0]
			default:
				nums = nums[:0]
			}
		case core.ContentDictStart:
			lx.SkipDict(&t)
			nums = nums[:0]
		default:
			nums = nums[:0]
		}
	}
	return 0, false
}

// intVal and numVal read an integer or a number, resolved first: a /Widths
// element or a /FirstChar may be an indirect reference like any other value.
// Absent or the wrong type reads as 0.
func intVal(doc core.View, o object.Object) int {
	i, _ := doc.ResolveInt(o)
	return int(i)
}

func numVal(doc core.View, o object.Object) float64 {
	f, _ := doc.ResolveNumber(o)
	return f
}
