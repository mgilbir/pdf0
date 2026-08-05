package fonttest

import (
	"testing"

	"github.com/mgilbir/forme/font"
)

func TestSyntheticFontParses(t *testing.T) {
	data := SFNT(SFNTOptions{Name: "Probe", Glyphs: []Glyph{
		{Rune: 'A', Advance: 600, HasShape: true},
		{Rune: 'B', Advance: 700, HasShape: true},
		{Rune: ' ', Advance: 250, HasShape: false},
	}})
	fp := font.ParseSFNT(data, 1<<20)
	if fp == nil {
		t.Fatal("this module's own reader rejected the synthetic font")
	}
	if fp.NumGlyphs != 4 {
		t.Errorf("NumGlyphs = %d, want 4 (.notdef + 3)", fp.NumGlyphs)
	}
	for r, wantGID := range map[rune]int{'A': 1, 'B': 2, ' ': 3} {
		if got := fp.Cmap[r]; got != wantGID {
			t.Errorf("cmap[%q] = %d, want %d", r, got, wantGID)
		}
	}
	for gid, want := range map[int]float64{1: 600, 2: 700, 3: 250} {
		if got := fp.WidthByGID[gid]; got != want {
			t.Errorf("width[gid %d] = %v, want %v", gid, got, want)
		}
	}
	if !fp.GlyphNonEmpty[1] || !fp.GlyphNonEmpty[2] {
		t.Error("glyphs given a shape parsed as empty")
	}
	if fp.GlyphNonEmpty[3] {
		t.Error("the space glyph parsed as having an outline")
	}
	if !fp.GlyphPresent[1] || !fp.GlyphPresent[3] {
		t.Error("a glyph parsed as missing from glyf")
	}
}
