package pdfa

import (
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file holds the tests of which text rendering modes exempt a font from
// the PDF/A font rules: mode 3 alone.
//
// ISO 19005-2 6.2.11.4.1 (and -4 6.2.10.4.1): "A font referenced for use
// solely in rendering mode 3 is therefore not rendered"; -4 6.2.10.5 exempts
// "Glyphs that are referenced only with rendering mode 3". Mode 7 adds the
// glyph outlines to the clipping path, which decides what is painted after it,
// and veraPDF's embedding, glyph-presence and width tests all exempt
// rendering mode 3 and nothing else.

// TestRendersVisiblyExemptsMode3Only is the predicate every gate reads.
func TestRendersVisiblyExemptsMode3Only(t *testing.T) {
	for _, c := range []struct {
		modes []int
		want  bool
	}{
		{nil, true}, // no recorded mode: treated as visible
		{[]int{0}, true},
		{[]int{3}, false},
		{[]int{7}, true},
		{[]int{3, 7}, true},
		{[]int{3, 0}, true},
	} {
		u := &core.FontTextUsage{Modes: map[int]bool{}}
		for _, m := range c.modes {
			u.Modes[m] = true
		}
		if got := rendersVisibly(u); got != c.want {
			t.Errorf("modes %v: rendersVisibly = %v, want %v", c.modes, got, c.want)
		}
	}
}

// TestClipOnlyTextNeedsAnEmbeddedFont: the embedding rule.
func TestClipOnlyTextNeedsAnEmbeddedFont(t *testing.T) {
	for _, c := range []struct {
		mode string
		want int
	}{{"0", 1}, {"3", 0}, {"7", 1}} {
		if got := fontEmbeddingFindings(t, "BT "+c.mode+" Tr /F1 12 Tf (y) Tj ET"); len(got) != c.want {
			t.Errorf("mode %s: %d embedding findings, want %d: %v", c.mode, len(got), c.want, got)
		}
	}
}

// TestClipOnlyTextNeedsItsGlyphs: the glyph-coverage rule. The program defines
// A; the page shows B.
func TestClipOnlyTextNeedsItsGlyphs(t *testing.T) {
	for _, c := range []struct {
		mode int
		want bool
	}{{0, true}, {3, false}, {7, true}} {
		fd := &object.Dictionary{}
		fd.Set("Flags", object.Integer(32))
		fd.Set("FontFile", object.IndirectRef{Number: 9})
		font := &object.Dictionary{}
		font.Set("Subtype", object.Name("Type1"))
		font.Set("Encoding", object.Name("WinAnsiEncoding"))
		font.Set("FontDescriptor", fd)
		doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: font},
			9: {Number: 9, Value: object.NewStream(&object.Dictionary{}, fonttest.Type1Program([]string{".notdef", "A"}))},
		}})
		u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{[]byte("B")}, Modes: map[int]bool{c.mode: true}}
		got := false
		for _, m := range errMessages(checkSimpleFontConsistency(doc, PDFA2b, "", font, u)) {
			if strings.Contains(m, "does not define a glyph") {
				got = true
			}
		}
		if got != c.want {
			t.Errorf("mode %d: missing-glyph finding = %v, want %v", c.mode, got, c.want)
		}
	}
}

// TestClipOnlyType3TextIsWidthChecked: the width rule, for a Type 3 font whose
// /Widths says 600 where its CharProc advances 500.
func TestClipOnlyType3TextIsWidthChecked(t *testing.T) {
	for _, c := range []struct {
		mode string
		want int
	}{{"0", 1}, {"1", 1}, {"3", 0}, {"7", 1}} {
		doc := type3WidthDoc(t, object.Array{object.Real(0.001), object.Integer(0), object.Integer(0), object.Real(0.001), object.Integer(0), object.Integer(0)}, 600, 500)
		content := []byte("BT " + c.mode + " Tr /T3 12 Tf 10 10 Td (A) Tj ET")
		doc.Objects[32] = &object.IndirectObject{Number: 32, Value: object.NewStream(&object.Dictionary{}, content)}
		if got := type3WidthFindings(validateView(doc, PDFA2b)); len(got) != c.want {
			t.Errorf("mode %s: %d width findings, want %d: %v", c.mode, len(got), c.want, got)
		}
	}
}

// TestClipOnlyTextIsHeldToTheCIDSet: PDF/A-1's CIDSet completeness rule. The
// program has glyphs for CIDs 1 and 3, and the CIDSet lists CID 1 alone.
func TestClipOnlyTextIsHeldToTheCIDSet(t *testing.T) {
	const msg = "CIDSet does not list all glyphs present in the embedded font program"
	for _, c := range []struct {
		mode string
		want bool
	}{{"0", true}, {"3", false}, {"7", true}} {
		doc := cidSetDoc(t, object.Name("Identity"), 1)
		doc.Objects[44] = &object.IndirectObject{Number: 44, Value: object.NewStream(&object.Dictionary{}, []byte("BT "+c.mode+" Tr /F1 12 Tf <0001> Tj ET"))}
		if got := hasMessage(checkCIDSetProgramComplete(doc, PDFA1b), msg); got != c.want {
			t.Errorf("mode %s: CIDSet finding = %v, want %v", c.mode, got, c.want)
		}
	}
}

// TestClipOnlyTextNeedsAReadableProgram: the damaged-program rule.
func TestClipOnlyTextNeedsAReadableProgram(t *testing.T) {
	fd := &object.Dictionary{}
	fd.Set("Flags", object.Integer(32))
	fd.Set("FontFile2", object.IndirectRef{Number: 9})
	font := &object.Dictionary{}
	font.Set("Subtype", object.Name("TrueType"))
	font.Set("FontDescriptor", fd)
	doc := mkV(core.View{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: font},
		9: {Number: 9, Value: object.NewStream(&object.Dictionary{}, []byte("not a font program"))},
	}})
	for _, c := range []struct {
		mode int
		want int
	}{{0, 1}, {3, 0}, {7, 1}} {
		u := &core.FontTextUsage{ObjNum: 1, Strings: [][]byte{[]byte("Hi")}, Modes: map[int]bool{c.mode: true}}
		if got := damagedFontProgramError(doc, PDFA2b, "", font, fd, u, core.ReasonMalformed); len(got) != c.want {
			t.Errorf("mode %d: %d damaged-program findings, want %d: %v", c.mode, len(got), c.want, got)
		}
	}
}
