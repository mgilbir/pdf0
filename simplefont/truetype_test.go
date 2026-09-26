package simplefont

import (
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonttest"
)

// Glyph 0 is .notdef, and .notdef is an answer.
//
// TrueTypeGlyph has three outcomes and they are not two: a code the font maps
// to .notdef and a font this reader cannot ask both come back with glyph 0,
// and only the second is ignorance. A consumer testing `gid == 0` merges a
// finding with the absence of one.
//
// Nothing here asserts what a consumer does with the answer. What it asserts
// is that the answer is still there to be read, so that an API change that
// dropped the distinction — returning a bare int, or folding .notdef into "not
// found" — fails here rather than in whatever reads it next.
//
// Ported from forme's font/glyphzero_test.go (forme 067d8c8), with the
// symbolic and Macintosh lookups added.

type sub = fonttest.CmapSub

// format4 is a format-4 subtable mapping each code to its glyph.
func format4(pairs ...[2]int) []byte {
	var segs [][3]int
	for _, p := range pairs {
		segs = append(segs, [3]int{p[0], p[0], p[1] - p[0]})
	}
	return fonttest.CmapFormat4(append(segs, [3]int{0xFFFF, 0xFFFF, 1}))
}

func parse(t testing.TB, subs []sub) *font.Program {
	t.Helper()
	fp := font.ParseSFNT(fonttest.SFNTWithCmapSubtables(subs), 1<<18)
	if fp == nil {
		t.Fatal("the fixture does not parse")
	}
	return fp
}

func TestNotdefIsAnAnswerAndNotAnAbsence(t *testing.T) {
	// A font with a (3,1) cmap holding U+0041 only. It can be asked about every
	// code; most of them it answers .notdef.
	withCmap := parse(t, []sub{{Plat: 3, Enc: 1, Data: format4([2]int{0x41, 100})}})
	// A font with no cmap subtable this rule reads at all.
	noCmap := parse(t, nil)
	// A symbolic font: (3,0) in the F000 page, and a (1,0) Macintosh table.
	symbol := parse(t, []sub{{Plat: 3, Enc: 0, Data: format4([2]int{0xF041, 7}, [2]int{0x42, 8})}})
	mac := parse(t, []sub{{Plat: 1, Enc: 0, Data: format4([2]int{0x43, 9})}})

	for _, tc := range []struct {
		what     string
		fp       *font.Program
		symbolic bool
		code     byte
		name     string
		wantGID  int
		wantOK   bool
	}{
		{"a code the font maps", withCmap, false, 'A', "A", 100, true},
		// The font has a cmap and this name is not in it. That is the font's
		// answer, not a gap in the reader.
		{"a named code the cmap does not hold", withCmap, false, 'B', "B", 0, true},
		// ISO 32000-1 9.6.6.4: a non-symbolic code with no glyph name renders
		// .notdef. Also an answer.
		{"a code with no name at all", withCmap, false, 'B', "", 0, true},
		// Nothing to ask. A rule may not assert against this.
		{"a font with no readable cmap", noCmap, false, 'A', "A", 0, false},
		{"a symbolic lookup with no symbol cmap", withCmap, true, 'A', "A", 0, false},
		// 9.6.6.4: a symbolic font's (3,0) table is asked at 0xF000+code,
		// then at the code itself; the (1,0) table at the code.
		{"a symbolic code in the F000 page", symbol, true, 'A', "", 7, true},
		{"a symbolic code at its own value", symbol, true, 'B', "", 8, true},
		{"a symbolic code the (3,0) table lacks", symbol, true, 'C', "", 0, false},
		{"a symbolic code in the Macintosh table", mac, true, 'C', "", 9, true},
		// A non-symbolic font with no (3,1) table falls back to (1,0), then
		// to (3,0) in the F000 page.
		{"a non-symbolic code through the Macintosh table", mac, false, 'C', "C", 9, true},
		{"a non-symbolic code through the symbol table", symbol, false, 'A', "A", 7, true},
	} {
		gid, ok := TrueTypeGlyph(tc.fp, tc.symbolic, tc.code, tc.name)
		if gid != tc.wantGID || ok != tc.wantOK {
			t.Errorf("%s: TrueTypeGlyph = (%d, %v), want (%d, %v)", tc.what, gid, ok, tc.wantGID, tc.wantOK)
		}
	}
}

// TestGlyphZeroAloneCannotTellThemApart is the point stated as a property:
// among the cases above, glyph 0 comes back for both an answer and an absence,
// so the integer on its own carries strictly less than the pair does.
//
// If this ever fails because every glyph-0 case agrees on the boolean, the
// second result has become redundant and the API should say so — which is a
// finding either way, and the reason this is a test rather than a comment.
func TestGlyphZeroAloneCannotTellThemApart(t *testing.T) {
	withCmap := parse(t, []sub{{Plat: 3, Enc: 1, Data: format4([2]int{0x41, 100})}})
	noCmap := parse(t, nil)

	answered, ignorant := 0, 0
	for _, tc := range []struct {
		fp   *font.Program
		name string
	}{
		{withCmap, "B"}, {withCmap, ""}, {noCmap, "A"},
	} {
		gid, ok := TrueTypeGlyph(tc.fp, false, 'B', tc.name)
		if gid != 0 {
			t.Fatalf("the fixture stopped producing glyph 0: got %d", gid)
		}
		if ok {
			answered++
		} else {
			ignorant++
		}
	}
	if answered == 0 || ignorant == 0 {
		t.Errorf("of three glyph-0 results, %d were the font's answer and %d were "+
			"the reader's ignorance; the two must both occur or the second result "+
			"is carrying nothing", answered, ignorant)
	}
}
