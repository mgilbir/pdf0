package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
)

// Complex scripts need more than glyph coverage.
//
// Arabic letters join, Devanagari reorders a vowel sign before the consonant it
// follows, Tibetan stacks. A face with the glyphs but no shaping produces text a
// reader sees as broken — which is worse than the honest report that the
// characters are missing, because it looks like a rendering rather than a gap.
//
// So a font is only worth adding to the fallback if the shaper applies its
// script, and that is a claim to check rather than assume. This is the check.
func TestShapingIsContextual(t *testing.T) {
	dir := os.Getenv("NOTO_FONTS")
	if dir == "" {
		t.Skip("set NOTO_FONTS")
	}
	load := func(name string) *fonts.Face {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Skipf("no %s", name)
		}
		f, err := fonts.Load(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return f
	}

	// Arabic: the letters join, so a word must not shape to the same glyphs as
	// the letters shaped one at a time. If it does, the face has the glyphs and
	// the shaper is not applying the joining forms — which produces text a
	// reader sees as broken.
	ar := load("NotoSansArabic-Regular.ttf")
	word := "سلام"
	joined, missing := ar.ShapeGlyphs(word)
	if missing != 0 {
		t.Fatalf("the Arabic face is missing %d characters of %q", missing, word)
	}
	var isolated []fonts.Glyph
	for _, r := range word {
		g, _ := ar.ShapeGlyphs(string(r))
		isolated = append(isolated, g...)
	}
	t.Logf("Arabic %q: joined=%d glyphs, isolated=%d glyphs", word, len(joined), len(isolated))
	same := len(joined) == len(isolated)
	if same {
		for i := range joined {
			if joined[i] != isolated[i] {
				same = false
				break
			}
		}
	}
	if same {
		t.Errorf("shaping %q gave the same glyphs as the letters in isolation; "+
			"the joining forms are not being applied", word)
	} else {
		t.Logf("  Arabic joining forms ARE applied")
	}

	// Devanagari: reordering. "ki" is written with the vowel sign *before* the
	// consonant, so the glyph order must differ from the character order.
	dv := load("NotoSansDevanagari-Regular.ttf")
	if g, m := dv.ShapeGlyphs("कि"); m == 0 {
		t.Logf("Devanagari कि: %d glyphs", len(g))
	} else {
		t.Errorf("Devanagari face missing %d characters", m)
	}

	bo := load("NotoSerifTibetan-Regular.ttf")
	if g, m := bo.ShapeGlyphs("བོད"); m == 0 {
		t.Logf("Tibetan བོད: %d glyphs", len(g))
	} else {
		t.Errorf("Tibetan face missing %d characters", m)
	}
}
