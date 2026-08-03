package fonts

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/fonttest"
	"github.com/mgilbir/pdf0/object"
)

// The font API against input it did not choose.
//
// A font file is the least trustworthy thing this module is handed. It is
// offset-driven and self-referential throughout — every table points at another
// by byte offset, and every one of those offsets is a number in the file — so a
// reader that trusts one indexes wherever it is told. This package is on the
// *writing* side, which makes it worse rather than better: a caller embedding a
// user-supplied font in a generated document is running this on bytes an
// attacker chose, inside a process that is doing something else.
//
// So the contract is the same as for the document writer: every entry point
// reports, and none of them panics.

// allocator is the smallest thing Embed needs.
type allocator struct{ n int }

func (a *allocator) Add(object.Object) object.IndirectRef {
	a.n++
	return object.IndirectRef{Number: a.n}
}

func noPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// hostileFontBytes are the shapes a font file can take that are not a font.
func hostileFontBytes() map[string][]byte {
	good := fonttest.SFNT(fonttest.SFNTOptions{
		Glyphs: []fonttest.Glyph{{Rune: 'a', Advance: 500, HasShape: true}},
	})
	truncated := append([]byte(nil), good[:len(good)/2]...)

	// A valid header whose table directory points past the end.
	badOffsets := append([]byte(nil), good...)
	for i := 12; i+16 <= 12+16*4 && i+16 <= len(badOffsets); i += 16 {
		badOffsets[i+8] = 0x7F // an offset near the top of the range
		badOffsets[i+9] = 0xFF
	}

	zeroed := make([]byte, len(good))
	copy(zeroed, good[:12])

	return map[string][]byte{
		"nil":                       nil,
		"empty":                     {},
		"one byte":                  {0x00},
		"the sfnt magic alone":      {0x00, 0x01, 0x00, 0x00},
		"OTTO magic alone":          []byte("OTTO"),
		"a truncated font":          truncated,
		"offsets past the end":      badOffsets,
		"a header and nothing else": zeroed,
		"text":                      []byte(strings.Repeat("not a font ", 100)),
		"all zeroes":                make([]byte, 4096),
		"all ones":                  bytesRepeat(0xFF, 4096),
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// TestLoadingHostileBytesReportsRatherThanPanics is the front door. Whatever
// comes back — a face or an error — must come back.
func TestLoadingHostileBytesReportsRatherThanPanics(t *testing.T) {
	for name, data := range hostileFontBytes() {
		t.Run(name, func(t *testing.T) {
			noPanic(t, "Load", func() {
				if f, err := Load(data); err == nil && f != nil {
					exerciseFace(t, f)
				}
			})
			noPanic(t, "LoadSimple", func() {
				if f, err := LoadSimple(data); err == nil && f != nil {
					exerciseFace(t, f)
				}
			})
		})
	}
}

// exerciseFace runs everything a caller can do with a face that loaded. A font
// that parses is not a font that is sane: the tables may still disagree with
// each other, and it is the second stage that meets that.
func exerciseFace(t *testing.T, f *Face) {
	t.Helper()
	const sample = "Hello — Ωμέγα 日本 ́́ ﬁ \u0930\u094D\u0915\u094D\u0924\u093F\u0902"

	noPanic(t, "Name", func() { _ = f.Name() })
	noPanic(t, "NumGlyphs", func() { _ = f.NumGlyphs() })
	noPanic(t, "IsSimple/IsStandard", func() { _, _ = f.IsSimple(), f.IsStandard() })
	noPanic(t, "Features", func() { _ = f.Features() })
	noPanic(t, "GlyphID", func() {
		for _, r := range []rune{0, 'a', 0x10FFFF, -1, 0xFFFD} {
			_, _ = f.GlyphID(r)
		}
	})
	noPanic(t, "Advance", func() {
		for _, r := range []rune{0, 'a', 0x10FFFF} {
			_, _ = f.Advance(r)
		}
	})
	noPanic(t, "Measure", func() { _ = f.Measure(sample, 12) })
	noPanic(t, "MeasureShaped", func() { _ = f.MeasureShaped(sample, 12) })
	noPanic(t, "Encode", func() { _, _ = f.Encode(sample) })
	noPanic(t, "Shape", func() { _, _ = f.Shape(sample) })
	noPanic(t, "ShapeWith", func() { _, _ = f.ShapeWith(sample, "smcp", "onum", "", "zzzz") })
	noPanic(t, "ShapeGlyphs", func() { _, _ = f.ShapeGlyphs(sample) })
	noPanic(t, "DrawShaped", func() {
		var b content.Builder
		b.BeginText().SetFont("F0", 12)
		f.DrawShaped(&b, sample, 12)
		b.EndText()
		_, _ = b.Bytes()
	})
	noPanic(t, "Draw with hand-made glyphs", func() {
		var b content.Builder
		b.BeginText().SetFont("F0", 12)
		// Glyph indices a caller could hold from another font entirely.
		f.Draw(&b, []Glyph{
			{GID: -1, XAdvance: 1},
			{GID: 1 << 20, XAdvance: -1},
			{GID: 0, XOffset: 1e300, YOffset: -1e300},
		}, 12)
		b.EndText()
		_, _ = b.Bytes()
	})
	noPanic(t, "Subset", func() { _, _ = f.Subset() })
	noPanic(t, "Embed", func() { _, _ = f.Embed(&allocator{}) })
	noPanic(t, "Used", func() { _ = f.Used() })
}

// TestTheStackSurvivesDegenerateFaces pins the fallback path, which reaches
// into every face it holds.
func TestTheStackSurvivesDegenerateFaces(t *testing.T) {
	good := loadTestFace(t, alphabet()...)
	broken, _ := Load(bytesRepeat(0xFF, 512)) // very likely nil

	stacks := map[string]*Stack{
		"empty":                NewStack(),
		"only nils":            NewStack(nil, nil),
		"a face and nils":      NewStack(nil, good, nil),
		"a face and a failure": NewStack(good, broken),
	}
	inputs := []string{
		"", "a", "abc", "́", "á́́", "日本語", "\u0930\u094D\u0915\u093F", "\u094D\u093F\u0902",
		strings.Repeat("á", 500), "\x00", "�",
	}
	for name, s := range stacks {
		for _, in := range inputs {
			noPanic(t, name+" ShapeRuns", func() { _, _ = s.ShapeRuns(in) })
			noPanic(t, name+" Measure", func() { _ = s.Measure(in, 12) })
		}
		noPanic(t, name+" Covers", func() { _ = s.Covers('a') })
		noPanic(t, name+" Faces", func() { _ = s.Faces() })
	}
}

// fuzzText carries one character of every kind the shaping paths branch on: a
// plain letter, a ligature, an unmapped ideograph, and a Devanagari syllable
// with a reph, a conjunct and a pre-base vowel sign — which is the only way the
// reordering is reached at all.
const fuzzText = "aﬁ日 \u0930\u094D\u0915\u094D\u0924\u093F\u0902"

// FuzzLoadAndUse drives the whole writing pipeline on arbitrary bytes: parse,
// shape, subset, embed. Fuzzing fails on a panic by itself, so the body only
// has to reach every stage.
//
// The seeds are the synthetic fonts this package's own tests use, so the corpus
// starts from things that are *nearly* valid — which is where the interesting
// failures are. A file that is obviously not a font is rejected in the first
// four bytes and exercises nothing.
func FuzzLoadAndUse(f *testing.F) {
	f.Add(fonttest.SFNT(fonttest.SFNTOptions{
		Glyphs: []fonttest.Glyph{{Rune: 'a', Advance: 500, HasShape: true}},
	}))
	f.Add(fonttest.SFNT(fonttest.SFNTOptions{
		Glyphs: []fonttest.Glyph{{Rune: 'f', Advance: 300, HasShape: true}},
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUB([]fonttest.Ligature{{Components: []int{1, 1}, Glyph: 1}}),
			"GPOS": fonttest.GPOS([]fonttest.KernPair{{Left: 1, Right: 1, Adjust: -50}}),
			"GDEF": fonttest.GDEF(map[int]int{1: 1}),
		},
	}))
	// A Devanagari seed: the reordering reads the font too — it asks whether
	// there is a reph for this Ra and what the below-base forms cover — so a
	// crafted font reaches it, and only a seed declaring those features gets the
	// fuzzer near enough to try.
	f.Add(fonttest.SFNT(fonttest.SFNTOptions{
		Glyphs: []fonttest.Glyph{
			{Rune: 0x0915, Advance: 500, HasShape: true}, // ka
			{Rune: 0x0930, Advance: 500, HasShape: true}, // ra
			{Rune: 0x094D, Advance: 0, HasShape: true},   // virama
			{Rune: 0x093F, Advance: 0, HasShape: true},   // the i-sign
			{Rune: 0xE000, Advance: 200, HasShape: true}, // a reph
		},
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBTable(
				[]fonttest.Lookup{{Type: 4, Subtables: [][]byte{
					fonttest.LigatureSubst([]fonttest.Ligature{
						{Components: []int{2, 3}, Glyph: 5},
					}),
				}}},
				[]fonttest.Feature{
					{Tag: "blwf", Lookups: []int{0}},
					{Tag: "half", Lookups: []int{0}},
					{Tag: "rphf", Lookups: []int{0}},
				},
				map[string]fonttest.Script{"dev2": fonttest.AllFeatures(3)},
			),
		},
	}))
	f.Add([]byte("OTTO\x00\x00\x00\x00"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, load := range []func([]byte) (*Face, error){Load, LoadSimple} {
			face, err := load(data)
			if err != nil || face == nil {
				continue
			}
			_ = face.Measure(fuzzText, 10)
			_ = face.MeasureShaped(fuzzText, 10)
			_, _ = face.Encode(fuzzText)
			_, _ = face.Shape(fuzzText)
			_, _ = face.ShapeGlyphs(fuzzText)

			var b content.Builder
			b.BeginText().SetFont("F0", 10)
			face.DrawShaped(&b, fuzzText, 10)
			b.EndText()
			_, _ = b.Bytes()

			_, _ = face.Subset()
			_, _ = face.Embed(&allocator{})
		}
	})
}

// TestAFontDeclaringNoGlyphsIsRefused pins the crash the fuzzer found.
//
// maxp holds the glyph count, and a font can declare zero. Every sfnt has
// .notdef at index zero, so that is malformed rather than empty — but the
// subsetter believed it, and writing .notdef into a slice sized from the count
// indexed past the end of nothing. The fuzz corpus is not committed, so the
// case is stated here instead.
func TestAFontDeclaringNoGlyphsIsRefused(t *testing.T) {
	good := fonttest.SFNT(fonttest.SFNTOptions{
		Glyphs: []fonttest.Glyph{{Rune: 'a', Advance: 500, HasShape: true}},
	})
	broken := corruptMaxpGlyphCount(t, good, 0)

	face, err := Load(broken)
	if err != nil {
		t.Skipf("the reader rejected it first, which is also fine: %v", err)
	}
	noPanic(t, "Subset", func() {
		if _, err := face.Subset(); err == nil {
			t.Error("a font declaring no glyphs was subsetted rather than refused")
		}
	})
	noPanic(t, "Embed", func() { _, _ = face.Embed(&allocator{}) })
}

// corruptMaxpGlyphCount rewrites the glyph count in a font's maxp table, which
// is the one number the subsetter sizes everything from.
func corruptMaxpGlyphCount(t *testing.T, data []byte, count uint16) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	numTables := int(be16(out, 4))
	for i := 0; i < numTables; i++ {
		rec := 12 + 16*i
		if rec+16 > len(out) {
			break
		}
		if string(out[rec:rec+4]) != "maxp" {
			continue
		}
		off := int(be32(out, rec+8))
		if off+6 > len(out) {
			break
		}
		// version (4 bytes), then numGlyphs.
		out[off+4] = byte(count >> 8)
		out[off+5] = byte(count)
		return out
	}
	t.Fatal("the fixture has no maxp table to corrupt")
	return nil
}

func be16(b []byte, i int) uint16 { return uint16(b[i])<<8 | uint16(b[i+1]) }
