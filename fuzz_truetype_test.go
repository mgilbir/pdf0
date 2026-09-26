package pdf0

import (
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/simplefont"
)

// FuzzTrueTypeGlyph drives simplefont.TrueTypeGlyph over whatever font forme reads from
// the input, as forme's FuzzSFNTCmap did before the function moved here: the
// maps it reads come from the (3,10) > (3,1) > (0,x) selection, which runs on
// attacker-supplied platform ids, encoding ids and offsets. Whatever the font,
// an answer is a glyph the font has (when it says how many it has), and
// ignorance is glyph 0.
//
// The parse is bounded by forme's cmap work budget, set here well below its
// default so that each input costs little; run the fuzzer itself under a
// memory-capped cgroup (docs/testing.md). It lives in the root package
// because `make fuzz` fuzzes the root package's targets.
func FuzzTrueTypeGlyph(f *testing.F) {
	bmp := fonttest.CmapFormat4([][3]int{{0x41, 0x41, 3 - 0x41}, {0xF041, 0xF041, 4 - 0xF041}, {0xFFFF, 0xFFFF, 1}})
	type sub = fonttest.CmapSub
	for _, subs := range [][]sub{
		{{Plat: 3, Enc: 1, Data: bmp}},
		{{Plat: 3, Enc: 0, Data: bmp}},
		{{Plat: 1, Enc: 0, Data: bmp}},
		{{Plat: 3, Enc: 1, Data: bmp}, {Plat: 3, Enc: 0, Data: bmp}, {Plat: 1, Enc: 0, Data: bmp}},
		nil,
	} {
		f.Add(fonttest.SFNTWithCmapSubtables(subs))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fp := font.ParseSFNT(data, 1<<16)
		if fp == nil {
			return
		}
		for _, code := range []byte{0, 'A', 0x41, 0xFF} {
			for _, symbolic := range []bool{false, true} {
				for _, name := range []string{"A", "", "uni0041", ".notdef"} {
					gid, ok := simplefont.TrueTypeGlyph(fp, symbolic, code, name)
					if gid < 0 || (!ok && gid != 0) {
						t.Fatalf("TrueTypeGlyph(%v, %d, %q) = (%d, %v): ignorance must be glyph 0", symbolic, code, name, gid, ok)
					}
					if ok && gid != 0 && fp.NumGlyphs > 0 && gid >= fp.NumGlyphs {
						t.Fatalf("TrueTypeGlyph(%v, %d, %q) = %d, past the font's %d glyphs", symbolic, code, name, gid, fp.NumGlyphs)
					}
				}
			}
		}
	})
}
