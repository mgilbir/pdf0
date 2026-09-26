package pdfa

import (
	"encoding/binary"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
	"time"
)

func errMessages(errs []Violation) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Message
	}
	return out
}

// sfntTable is one table of a synthetic sfnt font program.
type sfntTable struct {
	tag  string
	data []byte
}

// buildTestSFNT assembles a minimal TrueType font program from the given
// tables. Only the table directory has to be right for parseSFNT.
func buildTestSFNT(tables []sfntTable) []byte {
	n := len(tables)
	dir := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(dir[0:], 0x00010000)
	binary.BigEndian.PutUint16(dir[4:], uint16(n))
	body := []byte{}
	off := len(dir)
	for i, t := range tables {
		rec := 12 + 16*i
		copy(dir[rec:], t.tag)
		binary.BigEndian.PutUint32(dir[rec+8:], uint32(off+len(body)))
		binary.BigEndian.PutUint32(dir[rec+12:], uint32(len(t.data)))
		body = append(body, t.data...)
	}
	return append(dir, body...)
}

// cmapTable wraps one subtable as a (3,1) Windows Unicode cmap table.
func cmapTable(sub []byte) []byte {
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[2:], 1) // one subtable
	binary.BigEndian.PutUint16(hdr[4:], 3) // platform 3
	binary.BigEndian.PutUint16(hdr[6:], 1) // encoding 1
	binary.BigEndian.PutUint32(hdr[8:], 12)
	return append(hdr, sub...)
}

// --- the cmap work budget (limits.cmapWork, WithMaxCmapWork) ---

// budgetBustingCmap builds a format-4 subtable whose segments cost more work
// than defaultMaxCmapWork allows. The first five segments each span the whole
// BMP with a delta chosen so code 0x41 ('A') maps to glyph 0 and is therefore
// not recorded; the sixth segment, the only one that maps 'A' to a real glyph,
// is never reached because the budget runs out inside the fifth. The result is
// a cmap that is missing a mapping the font really declares — the exact
// condition under which "this code has no glyph" is not a fact.
func budgetBustingCmap() []byte {
	segs := make([][3]int, 0, 7)
	for i := 0; i < 5; i++ {
		segs = append(segs, [3]int{0x0000, 0xFFFE, 0x10000 - 0x41})
	}
	segs = append(segs, [3]int{0x0041, 0x0041, 100})
	segs = append(segs, [3]int{0xFFFF, 0xFFFF, 1}) // sentinel
	return fonttest.CmapFormat4(segs)
}

// TestCmapWorkBudgetDoesNotCondemnGlyphs is the false positive itself: with the
// budget tripped, font.TrueTypeGID's "a non-nil cmap is authoritative" contract turns
// every unread mapping into glyph 0, so a conformant font is reported as not
// defining a glyph it does define, and as referencing .notdef.
//
// Before the fix this failed with:
//
//	glyph-coverage rules fired on a font whose cmap was truncated by the work
//	budget: [embedded TrueType font does not define a glyph referenced for
//	rendering (code 65) text showing operator references the .notdef glyph in
//	TrueType font]
func TestCmapWorkBudgetDoesNotCondemnGlyphs(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		head := make([]byte, 54)
		binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
		maxp := make([]byte, 6)
		binary.BigEndian.PutUint16(maxp[4:], 300) // numGlyphs
		prog := buildTestSFNT([]sfntTable{
			{"head", head},
			{"maxp", maxp},
			{"cmap", cmapTable(budgetBustingCmap())},
		})

		fd := &object.Dictionary{}
		fd.Set("Flags", object.Integer(32)) // non-symbolic: codes go through the (3,1) cmap
		fd.Set("FontFile2", object.IndirectRef{Number: 9})
		font := &object.Dictionary{}
		font.Set("Subtype", object.Name("TrueType"))
		font.Set("Encoding", object.Name("WinAnsiEncoding"))
		font.Set("FontDescriptor", fd)
		doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: font},
			9: {Number: 9, Value: &object.Stream{Dict: object.Dictionary{}, Data: prog}},
		}})

		if fp, _ := core.LoadFontProgram(doc, fd); fp == nil || !fp.CmapPartial { // reason: the fixture check is on the program itself
			t.Fatalf("fixture wrong: font program parsed=%v, cmapPartial=%v", fp != nil, fp != nil && fp.CmapPartial)
		}

		u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{[]byte("A")}, Modes: map[int]bool{0: true}}
		msgs := errMessages(checkSimpleFontConsistency(doc, PDFA1b, "6.3", font, u))
		var bad []string
		for _, m := range msgs {
			if strings.Contains(m, "does not define a glyph") || strings.Contains(m, ".notdef") {
				bad = append(bad, m)
			}
		}
		if len(bad) > 0 {
			t.Errorf("glyph-coverage rules fired on a font whose cmap was truncated by the work budget: %v", bad)
		}

		// The trip is not silently swallowed either.
		trips := doc.Run.Trips.Snapshot()
		if len(trips) == 0 || trips[0].Guard() != core.GuardCmapWork {
			t.Errorf("cmap work-budget trip was not reported: %v", trips)
		}
	})
}

// --- /W read without expansion (audit 2026-09-22 C10) ---

// TestCIDWidthRangeIsCompleteWithoutExpansion pins the parse-level contract: a
// /W range of any width is read — not expanded, and not dropped — so the widths
// it declares are complete. (A per-range span limit used to drop it and mark
// the widths incomplete.)
func TestCIDWidthRangeIsCompleteWithoutExpansion(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		doc := mkV(core.View{Objects: map[int]*object.IndirectObject{}})
		w, complete := parseCIDWidths(doc, object.Array{object.Integer(0), object.Integer(2_000_000_000), object.Real(500)})
		if !complete {
			t.Error("a wide /W range is read in full, and the widths are complete")
		}
		if got, ok := w.width(1_999_999_999); !ok || got != 500 {
			t.Errorf("width(1999999999) = %v, %v; want 500, true", got, ok)
		}
		// A malformed (inverted) range declares nothing, so nothing is missing.
		if _, complete := parseCIDWidths(doc, object.Array{object.Integer(100), object.Integer(10), object.Real(500)}); !complete {
			t.Error("an inverted /W range is malformed input, and declares nothing")
		}
		// A /W that is not an array cannot be read.
		if _, complete := parseCIDWidths(doc, object.Integer(3)); complete {
			t.Error("a /W that is not an array claims to be complete")
		}
	})
}

// TestCIDWidthWideRangeIsCompared is the false positive the span limit was
// guarding against and the check it gave up: a font whose /W states every
// width correctly, as one range wider than any expansion budget, is not
// reported (its CIDs do not fall back to /DW), and one that states them
// wrongly, the same way, is.
//
// When a span limit dropped the range, the width check was skipped for the
// font altogether, so the wrong widths were never reported.
func TestCIDWidthWideRangeIsCompared(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: time.Minute}, func(t *testing.T) {
		// A CIDFontType2 program whose glyph 1 advances 500 units.
		head := make([]byte, 54)
		binary.BigEndian.PutUint16(head[18:], 1000)
		maxp := make([]byte, 6)
		binary.BigEndian.PutUint16(maxp[4:], 4)
		hhea := make([]byte, 36)
		binary.BigEndian.PutUint16(hhea[34:], 4) // numberOfHMetrics
		hmtx := make([]byte, 16)
		for gid := 0; gid < 4; gid++ {
			binary.BigEndian.PutUint16(hmtx[4*gid:], 500)
		}
		loca := make([]byte, 2*(4+1))
		for i := range loca {
			loca[i] = 0
		}
		glyf := make([]byte, 0)
		prog := buildTestSFNT([]sfntTable{
			{"head", head}, {"maxp", maxp}, {"hhea", hhea}, {"hmtx", hmtx},
			{"loca", loca}, {"glyf", glyf},
		})

		fd := &object.Dictionary{}
		fd.Set("Flags", object.Integer(4))
		fd.Set("FontFile2", object.IndirectRef{Number: 9})
		desc := &object.Dictionary{}
		desc.Set("Subtype", object.Name("CIDFontType2"))
		desc.Set("FontDescriptor", fd)
		desc.Set("CIDToGIDMap", object.Name("Identity"))
		widthFindings := func(declared float64) []string {
			// The file declares one width for every CID as one range, wider
			// than any expansion could afford.
			desc.Set("W", object.Array{object.Integer(0), object.Integer(2_000_000_000), object.Real(declared)})
			font := &object.Dictionary{}
			font.Set("Subtype", object.Name("Type0"))
			font.Set("Encoding", object.Name("Identity-H"))
			font.Set("DescendantFonts", object.Array{desc})
			doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
				1: {Number: 1, Value: font},
				9: {Number: 9, Value: &object.Stream{Dict: object.Dictionary{}, Data: prog}},
			}})
			u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{{0x00, 0x01}}, Modes: map[int]bool{0: true}}
			var bad []string
			for _, m := range errMessages(checkCIDFontConsistency(doc, PDFA1b, "6.3", font, u)) {
				if strings.Contains(m, "width information") {
					bad = append(bad, m)
				}
			}
			if trips := doc.Run.Trips.Snapshot(); len(trips) != 0 {
				t.Errorf("declared %v: a /W read in full reported a trip: %v", declared, trips)
			}
			return bad
		}
		if bad := widthFindings(500); len(bad) > 0 {
			t.Errorf("width rule fired on a font whose wide /W range states the right width: %v", bad)
		}
		if bad := widthFindings(700); len(bad) == 0 {
			t.Error("width rule did not fire on a font whose wide /W range states the wrong width")
		}
	})
}

// --- the aggregate content budget vs. the document's own identification ---
