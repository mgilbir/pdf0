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
	case nil:
		if !symbolic {
			applyBase("StandardEncoding")
		}
	}
	return table
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

	firstChar    int
	widths       object.Array
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
	s := &Simple{Program: fp, Symbolic: Symbolic(doc, fd)}
	s.Subtype, _ = doc.ResolveName(fontDict.Get("Subtype"))
	s.Encoding = SimpleFontCodeToName(doc, fontDict, s.Symbolic)
	s.firstChar = intVal(doc, fontDict.Get("FirstChar"))
	s.widths, _ = doc.Resolve(fontDict.Get("Widths")).(object.Array)
	if fd != nil {
		s.missingWidth = numVal(doc, fd.Get("MissingWidth"))
	}
	return s, fd, core.ReasonOK
}

// ProgramWidth is the embedded program's advance width for a code, in
// thousandths of text space: by glyph index through the cmap for TrueType, by
// glyph name for Type 1 and CFF.
func (s *Simple) ProgramWidth(code byte) (float64, bool) {
	name := s.Encoding[code]
	if s.Subtype == "TrueType" {
		gid, ok := simplefont.TrueTypeGlyph(s.Program, s.Symbolic, code, name)
		if !ok || gid >= len(s.Program.WidthByGID) {
			return 0, false
		}
		return s.Program.WidthByGID[gid], true
	}
	if name == "" {
		return 0, false
	}
	w, ok := s.Program.WidthByName[name]
	return w, ok
}

// DeclaredWidth is the width the font dictionary declares for a code:
// Widths[code-FirstChar] when in range, else a non-zero /MissingWidth.
func (s *Simple) DeclaredWidth(doc core.View, code byte) (float64, bool) {
	return declaredWidth(doc, s.widths, s.firstChar, code, s.missingWidth)
}

// SelectsNotdef reports whether a TrueType code selects glyph 0, .notdef, by
// the font's own answer (simplefont.TrueTypeGlyph): a non-symbolic code the
// encoding names no glyph for, or one whose glyph the (3,1) cmap lacks. It is
// false for Type 1 and CFF, whose codes name glyphs.
func (s *Simple) SelectsNotdef(code byte) bool {
	if s.Subtype != "TrueType" {
		return false
	}
	gid, ok := simplefont.TrueTypeGlyph(s.Program, s.Symbolic, code, s.Encoding[code])
	return ok && gid == 0
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

// WidthDiffers reports whether a mapped glyph's declared and program widths
// are both known and differ by more than WidthTolerance. The answer depends on
// the CID alone, so a caller may ask once per CID.
func (c *CID) WidthDiffers(g CIDGlyph) bool {
	if !g.Code.Mapped || !g.KeyKnown {
		return false
	}
	progW, haveProg := c.GlyphMap.Width(c.Program, g.Key)
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
	firstChar := intVal(doc, fontDict.Get("FirstChar"))
	widths, _ := doc.Resolve(fontDict.Get("Widths")).(object.Array)
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
			pdfW, havePDF := declaredWidth(doc, widths, firstChar, code, 0)
			if havePDF && math.Abs(pdfW-glyphW) > tolerance {
				differs = true
			}
		}
	}
	return differs
}

// declaredWidth returns the width a simple font's dictionary declares for a
// code: Widths[code-FirstChar] when in range, else missingWidth when it is not
// zero.
func declaredWidth(doc core.View, widths object.Array, firstChar int, code byte, missingWidth float64) (float64, bool) {
	idx := int(code) - firstChar
	if idx >= 0 && idx < len(widths) {
		return numVal(doc, widths[idx]), true
	}
	if missingWidth != 0 {
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
