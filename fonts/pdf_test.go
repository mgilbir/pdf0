package fonts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/fonttest"
)

func TestShapeInvertsTheKernSign(t *testing.T) {
	f := shapingFace(t)
	spans, missing := f.Shape("AV")
	if missing != 0 {
		t.Fatalf("%d runes missing", missing)
	}
	var adjust float64
	var seen int
	for _, s := range spans {
		if s.Adjust != 0 {
			adjust = s.Adjust
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("got %d adjustments in %+v, want 1", seen, spans)
	}
	if adjust != 80 {
		t.Errorf("adjustment = %v, want +80: a kern of -80 font units tightens, and TJ subtracts", adjust)
	}
}

// TestShapeAppliesLigatures pins the substitution and the glyph it produces.

func TestShapeAppliesLigatures(t *testing.T) {
	f := shapingFace(t)
	if !f.HasLigatures() {
		t.Fatal("the font's GSUB liga feature was not read")
	}
	spans, _ := f.Shape("fi")
	var codes []byte
	for _, s := range spans {
		codes = append(codes, s.Codes...)
	}
	if len(codes) != 2 {
		t.Fatalf("shaping %q produced %d bytes, want 2: f and i become one glyph", "fi", len(codes))
	}
	if gid := int(codes[0])<<8 | int(codes[1]); gid != 5 {
		t.Errorf("the ligature produced glyph %d, want 5", gid)
	}
}

// TestShapeMeasuresWhatItDraws pins that measurement and shaping agree. If they
// could disagree, a layout engine would reserve one width and the renderer
// would paint another — the defect that shows up as a line overflowing its box
// in a viewer but not in the engine that produced it.

func TestShapeRecordsTheGlyphsItUses(t *testing.T) {
	f := shapingFace(t)
	f.Shape("fi")
	used := f.Used()
	var found bool
	for _, gid := range used {
		if gid == 5 {
			found = true
		}
	}
	if !found {
		t.Errorf("used glyphs are %v; the ligature glyph 5 must be among them", used)
	}
	for _, gid := range used {
		if gid == 3 || gid == 4 {
			t.Errorf("glyph %d was recorded although the ligature replaced it", gid)
		}
	}
}

// TestUnkernedTextIsASingleSpan pins that shaping costs nothing when the font
// has nothing to say. Splitting a run at every glyph would bloat every content
// stream for no visual difference.

func TestUnkernedTextIsASingleSpan(t *testing.T) {
	f := shapingFace(t)
	spans, _ := f.Shape("AAA") // no pair is declared for (A,A)
	if len(spans) != 1 {
		t.Errorf("got %d spans for unkerned text, want 1: %+v", len(spans), spans)
	}
}

// TestShapeIsSafeOnAFontWithNoLayoutTables pins the ordinary case: a font
// carrying neither GPOS nor GSUB shapes to exactly what Encode produces.

func TestShapeIsSafeOnAFontWithNoLayoutTables(t *testing.T) {
	f := loadTestFace(t, alphabet()...)
	if f.HasKerning() || f.HasLigatures() {
		t.Fatal("the plain fixture reported layout tables it does not have")
	}
	spans, _ := f.Shape("abc")
	var shaped []byte
	for _, s := range spans {
		shaped = append(shaped, s.Codes...)
	}
	encoded, _ := f.Encode("abc")
	if string(shaped) != string(encoded) {
		t.Errorf("shaped %v, encoded %v; with no layout tables they must agree", shaped, encoded)
	}
}

// TestShapeSpansAreWhatTheBuilderTakes pins that the output feeds straight into
// a content stream, which is the point of returning this type.

func TestShapeSpansAreWhatTheBuilderTakes(t *testing.T) {
	f := shapingFace(t)
	spans, _ := f.Shape("AV")
	var b content.Builder
	b.BeginText().SetFont("F1", 12).ShowTextAdjusted(spans...).EndText()
	if _, err := b.Bytes(); err != nil {
		t.Errorf("the builder refused the shaped spans: %v", err)
	}
}

// TestLayoutReadingSurvivesAMalformedTable pins that a font is treated as the
// untrusted input it is. A truncated GPOS must yield no kerning rather than a
// panic or a misread pair.

func TestLayoutReadingSurvivesAMalformedTable(t *testing.T) {
	full := fonttest.GPOS([]fonttest.KernPair{{Left: 1, Right: 2, Adjust: -80}})
	for cut := 0; cut < len(full); cut++ {
		data := fonttest.SFNT(fonttest.SFNTOptions{
			Name: "Trunc",
			Glyphs: []fonttest.Glyph{
				{Rune: 'A', Advance: 700, HasShape: true},
				{Rune: 'V', Advance: 700, HasShape: true},
			},
			Extra: map[string][]byte{"GPOS": full[:cut]},
		})
		f, err := Load(data)
		if err != nil {
			continue // a font this broken may legitimately be refused
		}
		f.Shape("AV") // must not panic
	}
}

// markedFace has A V and a combining mark, a kern pair for (A,V), and a GDEF
// classifying the mark. Glyph indices: A=1, V=2, mark=3.

func TestKerningSkipsMarksWhenTheLookupSaysSo(t *testing.T) {
	const ignoreMarks = 0x0008
	f := markedFace(t, ignoreMarks)

	spans, _ := f.Shape("ÁV") // A, combining acute, V
	var adjust float64
	for _, s := range spans {
		if s.Adjust != 0 {
			adjust = s.Adjust
		}
	}
	if adjust != 80 {
		t.Errorf("adjustment across a mark = %v, want 80: the lookup ignores marks", adjust)
	}

	// And the width agrees with what was drawn.
	// A=700 + mark=0 + V=700, kern -80 → 1320/1000 em.
	if got, want := f.MeasureShaped("ÁV", 10), 13.2; got != want {
		t.Errorf("MeasureShaped = %v, want %v", got, want)
	}
}

// TestKerningRespectsALookupThatDoesNotIgnoreMarks is the other direction: a
// lookup with no flag set means what it says, and the pair really is broken by
// anything between. Treating every lookup as mark-ignoring would be as wrong as
// treating none as such.

func TestKerningRespectsALookupThatDoesNotIgnoreMarks(t *testing.T) {
	f := markedFace(t, 0)
	spans, _ := f.Shape("ÁV")
	for _, s := range spans {
		if s.Adjust != 0 {
			t.Errorf("a lookup with no flags kerned across a mark: %v", s.Adjust)
		}
	}
	// Adjacent, the same pair still kerns.
	spans, _ = f.Shape("AV")
	var seen bool
	for _, s := range spans {
		if s.Adjust != 0 {
			seen = true
		}
	}
	if !seen {
		t.Error("the pair does not kern even when adjacent")
	}
}

// TestShapeWithAppliesRequestedFeatures pins the opt-in features. A font's
// small capitals are right only where small capitals were wanted, so they are
// applied on request and not by default.

func TestShapeWithAppliesRequestedFeatures(t *testing.T) {
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Smallcaps",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true}, // 1
			{Rune: 'b', Advance: 500, HasShape: true}, // 2
			{Rune: 'A', Advance: 600, HasShape: true}, // 3 — stands in for a.sc
			{Rune: 'B', Advance: 600, HasShape: true}, // 4 — stands in for b.sc
		},
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBSingle("smcp", []int{1, 2}, []int{3, 4}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if got := f.Features(); len(got) != 1 || got[0] != "smcp" {
		t.Fatalf("Features() = %v, want [smcp]", got)
	}

	plain, _ := f.Shape("ab")
	small, _ := f.ShapeWith("ab", "smcp")
	if codesOf(plain) == codesOf(small) {
		t.Error("asking for small capitals changed nothing")
	}
	if want := "\x00\x03\x00\x04"; codesOf(small) != want {
		t.Errorf("small capitals produced %q, want %q", codesOf(small), want)
	}
	// An unknown feature is a no-op, not a failure: a caller asking a face for
	// something it does not have should get the text set plainly.
	none, _ := f.ShapeWith("ab", "nope")
	if codesOf(none) != codesOf(plain) {
		t.Error("an unrecognised feature changed the text")
	}
}

// TestShapeWithRecordsSubstitutedGlyphs pins that a requested feature feeds the
// subsetter, exactly as a ligature does. A small-capital glyph is one no Encode
// call ever named.

func TestShapeWithRecordsSubstitutedGlyphs(t *testing.T) {
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Smallcaps",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 'A', Advance: 600, HasShape: true},
		},
		Extra: map[string][]byte{
			"GSUB": fonttest.GSUBSingle("smcp", []int{1}, []int{2}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	f.ShapeWith("a", "smcp")
	var found bool
	for _, gid := range f.Used() {
		if gid == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("used glyphs are %v; the substituted glyph 2 must be among them", f.Used())
	}
}

func codesOf(spans []content.TextSpan) string {
	var out []byte
	for _, s := range spans {
		out = append(out, s.Codes...)
	}
	return string(out)
}
func TestEveryWayOfMeasuringAgreesWithEveryWayOfDrawing(t *testing.T) {
	corpus := shapingCorpus(t)
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	const size = 1000
	var differing int
	for _, s := range corpus {
		glyphs, _ := f.ShapeGlyphs(s)
		drawn := MeasureGlyphs(glyphs, size)

		if measured := f.MeasureShaped(s, size); measured != drawn {
			differing++
			if differing <= 10 {
				t.Errorf("%s\n  MeasureShaped says %v and the glyphs occupy %v",
					describeRunes(s), measured, drawn)
			}
			continue
		}

		// And the spans Shape emits have to come to the same width. A span
		// carries codes, which advance the pen by what the font's /W array
		// states, and displacements, which move it the other way.
		spans, _ := f.Shape(s)
		if width := f.widthOfSpans(spans, size); width != drawn {
			differing++
			if differing <= 10 {
				t.Errorf("%s\n  the spans Shape emits occupy %v and the glyphs occupy %v",
					describeRunes(s), width, drawn)
			}
		}
	}
	if differing > 10 {
		t.Errorf("... and %d more", differing-10)
	}
	t.Logf("%d strings: every way of measuring agrees with every way of drawing", len(corpus))
}

// widthOfSpans is what a text operator will advance the pen by, given these
// spans: the font's own advance for each code, less each displacement.
//
// It reads the spans the way a PDF reader does rather than the way this package
// wrote them, which is the point — a span sequence that measured correctly by
// this package's own arithmetic and painted wrongly would pass a test that asked
// this package what it meant.

func (f *Face) widthOfSpans(spans []content.TextSpan, size float64) float64 {
	var units float64
	for _, s := range spans {
		for i := 0; i+1 < len(s.Codes); i += 2 {
			units += f.GlyphAdvance(int(s.Codes[i])<<8 | int(s.Codes[i+1]))
		}
		// A positive TJ number moves what follows closer.
		units -= s.Adjust
	}
	return units * size / 1000
}

// TestShapeAndShapeGlyphsProduceTheSameGlyphs pins that the two are one shaper
// rather than two that happen to agree on width.
//
// A span sequence and a glyph sequence can come to the same width while naming
// different glyphs — a ligature missed here, an alternate taken there — so the
// widths agreeing is not enough. The codes have to be the glyphs.

func TestShapeAndShapeGlyphsProduceTheSameGlyphs(t *testing.T) {
	corpus := shapingCorpus(t)
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	var differing int
	for _, s := range corpus {
		glyphs, missingGlyphs := f.ShapeGlyphs(s)
		spans, missingSpans := f.Shape(s)
		if missingGlyphs != missingSpans {
			t.Errorf("%s: ShapeGlyphs reports %d missing and Shape reports %d",
				describeRunes(s), missingGlyphs, missingSpans)
		}
		var codes []int
		for _, sp := range spans {
			for i := 0; i+1 < len(sp.Codes); i += 2 {
				codes = append(codes, int(sp.Codes[i])<<8|int(sp.Codes[i+1]))
			}
		}
		if len(codes) != len(glyphs) {
			differing++
			if differing <= 10 {
				t.Errorf("%s: Shape emits %d glyphs and ShapeGlyphs %d",
					describeRunes(s), len(codes), len(glyphs))
			}
			continue
		}
		for i := range codes {
			if codes[i] != glyphs[i].GID {
				differing++
				if differing <= 10 {
					t.Errorf("%s: glyph %d is %d in Shape and %d in ShapeGlyphs",
						describeRunes(s), i, codes[i], glyphs[i].GID)
				}
				break
			}
		}
	}
	if differing > 10 {
		t.Errorf("... and %d more", differing-10)
	}
}

// TestShapePlacesAMarkHorizontallyWhereShapeGlyphsDoes pins the part of
// positioning a text operator *can* express.
//
// TJ moves the pen and shows codes, and that is all it does. Everything shaping
// decides horizontally therefore comes through exactly — the kern, the advance
// a contextual rule chose, the zero width of a mark, and the horizontal half of
// where the mark sits. The vertical half cannot: see Shape's own documentation.

func TestShapePlacesAMarkHorizontallyWhereShapeGlyphsDoes(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	// A dot below the first f of "ffi": the ligature keeps the mark and the mark
	// is displaced 516 units back from the pen.
	const text = "f̣fi"
	glyphs, _ := f.ShapeGlyphs(text)
	if len(glyphs) != 2 || glyphs[1].XOffset == 0 {
		t.Fatalf("the fixture assumption is gone: %q shaped to %d glyphs with offset %v",
			text, len(glyphs), glyphs[len(glyphs)-1].XOffset)
	}

	spans, _ := f.Shape(text)
	// Walk the spans as a reader does, tracking where the pen is when each code
	// is painted, and check the mark lands where ShapeGlyphs put it.
	var pen float64
	var at int
	for _, sp := range spans {
		for i := 0; i+1 < len(sp.Codes); i += 2 {
			gid := int(sp.Codes[i])<<8 | int(sp.Codes[i+1])
			if at == 1 {
				// Where the glyphs say the mark goes: the pen after the
				// ligature, plus the mark's own displacement.
				want := glyphs[0].XAdvance + glyphs[1].XOffset
				if pen != want {
					t.Errorf("Shape paints the mark at %v; ShapeGlyphs puts it at %v", pen, want)
				}
			}
			pen += f.GlyphAdvance(gid)
			at++
		}
		pen -= sp.Adjust
	}
	if at != 2 {
		t.Errorf("Shape emitted %d glyphs, want 2", at)
	}
}
func TestSpanShapingIsAlsoInVisualOrder(t *testing.T) {
	f := hebrewFace(t)
	spans, missing := f.Shape(string([]rune{alefHeb, betHeb, gimel}))
	if missing != 0 {
		t.Fatalf("%d characters have no glyph", missing)
	}
	var codes []byte
	for _, s := range spans {
		codes = append(codes, s.Codes...)
	}
	if len(codes) != 6 {
		t.Fatalf("got %d bytes of codes, want 6", len(codes))
	}
	gimelGID, _ := f.GlyphID(gimel)
	if first := int(codes[0])<<8 | int(codes[1]); first != gimelGID {
		t.Errorf("the first glyph shown is %d, want %d (gimel, the last letter)", first, gimelGID)
	}
	// Shape and MeasureShaped have to agree, or a caller lays out to one width
	// and draws another.
	if got, want := f.MeasureShaped(string([]rune{alefHeb, betHeb, gimel}), 1000), 3*500.0; got != want {
		t.Errorf("MeasureShaped gives %v, want %v", got, want)
	}
}

// TestKerningUsesThePairAsTheFontStatesIt is the one thing reversal changes
// about kerning. The pair a font declares is the pair as the text is written; a
// reversed run meets those two glyphs the other way round, and looking the kern
// up in the order the pen sees finds either nothing or the wrong pair.
func TestTheSimpleFormIsSmallerAndNarrower(t *testing.T) {
	simple, err := NotoSansSimple()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !simple.IsSimple() {
		t.Fatal("NotoSansSimple did not return a simple face")
	}
	// Latin works.
	if _, missing := simple.ShapeGlyphs("café"); missing != 0 {
		t.Errorf("%d Latin characters could not be set in the simple form", missing)
	}
	// Greek does not: it is outside WinAnsi.
	if _, missing := simple.ShapeGlyphs("Ωμέγα"); missing == 0 {
		t.Error("the simple form set Greek; WinAnsi does not cover it")
	}
	// One byte per character.
	var b content.Builder
	b.BeginText().SetFont("F0", 12)
	simple.DrawShaped(&b, "AB", 12)
	b.EndText()
	stream, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	if !strings.Contains(string(stream), "(AB)") {
		t.Errorf("the simple form did not write one byte per character: %q", stream)
	}
}

// TestTheBundledFontSubsetsToWhatWasUsed pins the size claim. Bundling 600 kB
// is only defensible because what reaches a document is a fraction of it.
func TestDrawEmitsTheOffsetsItWasGiven(t *testing.T) {
	f := accentFace(t, 4)
	glyphs, missing := f.ShapeGlyphs("a\u0301")
	if missing != 0 {
		t.Fatalf("%d runes missing", missing)
	}

	var b content.Builder
	b.BeginText().SetFont("F1", 10)
	f.Draw(&b, glyphs, 10)
	b.EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatalf("drawing: %v", err)
	}
	stream := string(out)
	// The mark's vertical offset of 700/1000 em at 10pt is a rise of 7.
	if !contains(stream, "7 Ts") {
		t.Errorf("no rise of 7 was emitted:\n%s", stream)
	}
	// And it is put back afterwards, or everything shown next would be raised.
	if !contains(stream, "0 Ts") {
		t.Errorf("the rise was not reset:\n%s", stream)
	}
	// The horizontal offset must be emitted too. The mark sits 350 back from
	// where the pen stands after the base, and TJ subtracts — so the number in
	// the stream is +350. Without it the accent is drawn after the letter
	// rather than over it.
	if !contains(stream, "350") {
		t.Errorf("the horizontal offset was not emitted:\n%s", stream)
	}
}
func spanCodes(t *testing.T, f *Face, s string) []byte {
	t.Helper()
	spans, missing := f.Shape(s)
	if missing != 0 {
		t.Fatalf("Shape(%q): %d runes have no glyph", s, missing)
	}
	var out []byte
	for _, sp := range spans {
		out = append(out, sp.Codes...)
	}
	return out
}

func withCodes(t *testing.T, f *Face, s string) []byte {
	t.Helper()
	spans, missing := f.ShapeWith(s, "salt")
	if missing != 0 {
		t.Fatalf("ShapeWith(%q): %d runes have no glyph", s, missing)
	}
	var out []byte
	for _, sp := range spans {
		out = append(out, sp.Codes...)
	}
	return out
}

// The cross-script cursive fixture: two cursive attachment lookups, one per
// script, with opposite RightToLeft flags. A Latin word and an Arabic word join
// in opposite directions in the same font, which is the case that decides
// whether attachment can be read script-blind.
const (
	arabAlef, arabBeh = 4, 5

	alefExitY = 100
	behEntryY = 40
)

func TestSubsetTagIsAFunctionOfTheGlyphSet(t *testing.T) {
	abc := subsetTag([]int{0, 1, 2, 3})
	if abc != subsetTag([]int{0, 1, 2, 3}) {
		t.Error("the tag is not deterministic")
	}
	if abc == subsetTag([]int{0, 1, 2, 4}) {
		t.Error("two different glyph sets produced the same tag")
	}
	if len(abc) != 6 {
		t.Fatalf("tag %q is %d letters, want 6", abc, len(abc))
	}
	for _, c := range abc {
		if c < 'A' || c > 'Z' {
			t.Errorf("tag %q contains %q, which is not an uppercase letter", abc, c)
		}
	}
}

// TestSubsetRefusesATruncatedFont pins that a font that cannot be taken apart
// produces an error rather than a program claiming glyphs it does not carry. A
// font file is untrusted input like any other.

// from fonts/shape_test.go
func shapingFace(t *testing.T) *Face {
	t.Helper()
	glyphs := []fonttest.Glyph{
		{Rune: 'A', Advance: 700, HasShape: true},
		{Rune: 'V', Advance: 700, HasShape: true},
		{Rune: 'f', Advance: 300, HasShape: true},
		{Rune: 'i', Advance: 250, HasShape: true},
		{Rune: 'ﬁ', Advance: 520, HasShape: true}, // U+FB01
	}
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name:   "Shape",
		Glyphs: glyphs,
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOS([]fonttest.KernPair{{Left: 1, Right: 2, Adjust: -80}}),
			"GSUB": fonttest.GSUB([]fonttest.Ligature{{Components: []int{3, 4}, Glyph: 5}}),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestKerningIsRead pins that a GPOS pair adjustment reaches the reader at all.
// Everything else here rests on it, and a font whose kerning went unread would
// otherwise just produce unkerned text, which no other assertion notices.

// from fonts/subset_test.go
func loadTestFace(t *testing.T, glyphs ...fonttest.Glyph) *Face {
	t.Helper()
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{Name: "Probe", Glyphs: glyphs}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// from fonts/subset_test.go
func alphabet() []fonttest.Glyph {
	var gs []fonttest.Glyph
	for r := 'a'; r <= 'z'; r++ {
		gs = append(gs, fonttest.Glyph{Rune: r, Advance: 500 + int(r-'a'), HasShape: true})
	}
	return gs
}

// TestSubsetKeepsWhatWasUsedAndDropsTheRest is the property the whole exercise
// is for. A glyph that was encoded must still have an outline; one that was not
// must not, because its bytes are the saving.

// from fonts/shape_test.go
func markedFace(t *testing.T, flag int) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Marked",
		Glyphs: []fonttest.Glyph{
			{Rune: 'A', Advance: 700, HasShape: true},
			{Rune: 'V', Advance: 700, HasShape: true},
			{Rune: '́', Advance: 0, HasShape: true}, // combining acute
		},
		Extra: map[string][]byte{
			"GPOS": fonttest.GPOSWithFlag([]fonttest.KernPair{{Left: 1, Right: 2, Adjust: -80}}, flag),
			"GDEF": fonttest.GDEF(map[int]int{1: 1, 2: 1, 3: 3}), // base, base, mark
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestKerningSkipsMarksWhenTheLookupSaysSo is the correctness fix lookup flags
// exist for. A kerning lookup almost always declares that it ignores marks,
// because the pair it means to adjust is two base letters — and an accent
// written between them must not break it. Reading the pairs and not the flag
// kerns "AV" and not "A◌́V", which is a difference a reader sees.

// sameAsHarfBuzz compares a shaped run against the expectation, converting the
// expectation into this package's units rather than the other way about — a
// comparison that rounded this package's answer could hide a real difference of
// less than a unit.

// describeRunes names a string and the code points in it, so a failure over the
// corpus says which characters differed rather than showing mojibake.
func describeRunes(s string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q [", s)
	for i, r := range []rune(s) {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "U+%04X", r)
	}
	b.WriteByte(']')
	return b.String()
}

// shapingCorpus is the strings the agreement tests below are run over.
//
// It is forme's shaping corpus, which is weighted towards the places shaping
// decides something — the letter pairs that kern, the marks that attach, the
// Devanagari conjunct grid, the characters nothing is drawn for — rather than
// towards realistic prose. What it is *for* here is different from what it is
// for there: forme compares its shaping against HarfBuzz's, and this package
// takes the shaping as given and checks that its own several ways of writing it
// into a page agree with each other. A grid that exercises many paths once is
// the right input for both.
func shapingCorpus(t *testing.T) []string {
	t.Helper()
	return readNonEmptyLines(t, filepath.Join("..", "testdata", "shaping", "corpus.txt"))
}

// from fonts/bidi_test.go
func hebrewFace(t *testing.T) *Face {
	t.Helper()
	glyphs := []fonttest.Glyph{
		{Rune: alefHeb, Advance: 500, HasShape: true},
		{Rune: betHeb, Advance: 500, HasShape: true},
		{Rune: gimel, Advance: 500, HasShape: true},
		{Rune: '(', Advance: 300, HasShape: true},
		{Rune: ')', Advance: 300, HasShape: true},
		{Rune: ' ', Advance: 250, HasShape: true},
	}
	for r := 'a'; r <= 'z'; r++ {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 400, HasShape: true})
	}
	for r := '0'; r <= '9'; r++ {
		glyphs = append(glyphs, fonttest.Glyph{Rune: r, Advance: 350, HasShape: true})
	}
	f, err := Load(fonttest.SFNT(fonttest.SFNTOptions{Name: "Hebrew", Glyphs: glyphs}))
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// clustersOf is the byte offset each drawn glyph came from, which is the whole
// visible consequence of reordering.

// from fonts/bidi_test.go
const (
	alefHeb = 0x05D0 // HEBREW LETTER ALEF
	betHeb  = 0x05D1
	gimel   = 0x05D2
)

// TestBidiClassTableNamesEveryClass guards the generated table against a Unicode
// release that renames or withdraws a property value.
//
// cmd/genbidi fails if the data does not use a value the algorithm names, but
// the generator is only run by hand. This is the same check on the committed
// file, so that a table regenerated from the wrong version of the UCD — or by
// hand, which the header forbids and nothing enforces — cannot leave a branch of
// the algorithm silently unreachable.

// from fonts/position_test.go
func accentFace(t *testing.T, kind int) *Face {
	t.Helper()
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Accents",
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 500, HasShape: true},
			{Rune: 0x0301, Advance: 300, HasShape: true}, // combining acute; a real advance, so that zeroing it is observable
			{Rune: 'b', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{
			"GDEF": fonttest.GDEF(map[int]int{1: 1, 2: 3, 3: 1}), // base, mark, base
			"GPOS": fonttest.GPOSMarkToBase(kind,
				[]fonttest.MarkAttachment{{Glyph: 2, Class: 0, Anchor: fonttest.Anchor{X: 100, Y: 0}}},
				[]fonttest.BaseAttachment{{Glyph: 1, Anchors: map[int]fonttest.Anchor{0: {X: 250, Y: 700}}}},
			),
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// TestMarkAttachesToItsBase is the whole point of positioning into glyphs. The
// font says the accent's anchor meets the base's; without applying that the
// accent is drawn at its nominal advance, which for a zero-width mark is the
// origin — so every accent in a run piles up in the same place.

// from fonts/position_test.go
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestUnmarkedTextIsUnchangedByPositioning pins that the new machinery costs
// nothing where there is nothing to do: a run with no marks and no adjustments
// shapes to exactly the glyphs and advances the font gives.

// from fonts/harfbuzz_test.go
func readNonEmptyLines(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	defer file.Close()
	var out []string
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return out
}

// describeRunes names a string by its code points as well as its text, because
// most of the corpus is invisible in a terminal and several cases differ only by
// a character that has no shape.

// allocator is the smallest thing that satisfies Allocator: it keeps what it
// was given so a test can follow the references Embed writes.
type allocator struct{ objects []object.Object }

func (a *allocator) Add(o object.Object) object.IndirectRef {
	a.objects = append(a.objects, o)
	return object.IndirectRef{Number: len(a.objects)}
}

func (a *allocator) at(ref object.Object) object.Object {
	r, ok := ref.(object.IndirectRef)
	if !ok || r.Number < 1 || r.Number > len(a.objects) {
		return nil
	}
	return a.objects[r.Number-1]
}

func (a *allocator) dict(t *testing.T, ref object.Object) *object.Dictionary {
	t.Helper()
	d, ok := a.at(ref).(*object.Dictionary)
	if !ok {
		t.Fatalf("%v does not point at a dictionary", ref)
	}
	return d
}

// TestTheDescriptorIsTheFontsOwnMetricsInPDFsUnits pins the conversion, which is
// the whole of what this package does with a face's metrics: forme reports them
// in the font's own units and PDF states them in thousandths of an em, so every
// one of them is multiplied by 1000/unitsPerEm on the way out.
//
// It is checked against the face rather than against constants because a golden
// number here would pass for a face whose metrics were never read at all. The
// bundled font's unitsPerEm is not 1000, so the scaling is visible: an
// unconverted ascent would land within a few percent of the right answer, which
// is exactly the kind of wrong that survives a smell test.
func TestTheDescriptorIsTheFontsOwnMetricsInPDFsUnits(t *testing.T) {
	// 2048 units to the em, which is what a real TrueType font uses and the
	// bundled face does not: Noto Sans is drawn on a 1000-unit grid, where the
	// conversion is the identity and a missing one would be invisible.
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name:       "Scaled",
		UnitsPerEm: 2048,
		Ascent:     1638,
		Descent:    -410,
		Glyphs: []fonttest.Glyph{
			{Rune: 'a', Advance: 1024, HasShape: true},
			{Rune: 'g', Advance: 1100, HasShape: true},
		},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if f.UnitsPerEm() == 1000 {
		t.Fatal("the fixture's em is 1000 units, so this test cannot see a missing conversion")
	}
	f.ShapeGlyphs("ag") // something has to be used before a face can be embedded

	doc := &allocator{}
	ref, err := f.Embed(doc)
	if err != nil {
		t.Fatal(err)
	}
	type0 := doc.dict(t, ref)
	desc := type0.Get("DescendantFonts")
	cid := doc.dict(t, desc.(object.Array)[0])
	fdRef := cid.Get("FontDescriptor")
	fd := doc.dict(t, fdRef)

	want := f.Descriptor()
	scaled := func(v int) int { return int(float64(v) * 1000 / float64(f.UnitsPerEm())) }
	for _, c := range []struct {
		key  object.Name
		want int
	}{
		{"Ascent", scaled(want.Ascent)},
		{"Descent", scaled(want.Descent)},
		{"CapHeight", scaled(want.CapHeight)},
		{"Flags", want.Flags},
		{"StemV", want.StemV}, // already an estimate in thousandths; not scaled
	} {
		got := fd.Get(c.key)
		if got == nil {
			t.Errorf("/%s is missing from the descriptor", c.key)
			continue
		}
		if int(got.(object.Integer)) != c.want {
			t.Errorf("/%s = %v, want %d", c.key, got, c.want)
		}
	}
	bbox := fd.Get("FontBBox")
	for i, v := range bbox.(object.Array) {
		if got, w := int(v.(object.Integer)), scaled(want.BBox[i]); got != w {
			t.Errorf("/FontBBox[%d] = %d, want %d", i, got, w)
		}
	}
}

// TestTheWidthsAreTheProgramsOwn pins /DW and /W against the advances the
// embedded program carries, because that is what PDF/A compares them to: a /W
// that disagrees with the font is a finding, and the only way to be sure they
// agree is for both to come from one source.
//
// Every glyph is checked, not a sample. /W states the exceptions to /DW as
// runs, so an off-by-one in either the default or a run start shifts every
// width after it — and a spot check of three glyphs would very likely miss it.
func TestTheWidthsAreTheProgramsOwn(t *testing.T) {
	f, err := NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	f.ShapeGlyphs("The quick brown fox")

	doc := &allocator{}
	ref, err := f.Embed(doc)
	if err != nil {
		t.Fatal(err)
	}
	desc := doc.dict(t, ref).Get("DescendantFonts")
	cid := doc.dict(t, desc.(object.Array)[0])

	dwObj := cid.Get("DW")
	if dwObj == nil {
		t.Fatal("/DW is missing, so every glyph not in /W has the wrong width")
	}
	dw := numberOf(t, dwObj)

	// Rebuild the width of every glyph as a reader would: /DW everywhere, then
	// the runs in /W over the top.
	advances := f.GlyphAdvances()
	stated := make([]float64, len(advances))
	for i := range stated {
		stated[i] = dw
	}
	if wObj := cid.Get("W"); wObj != nil {
		w := wObj.(object.Array)
		for i := 0; i+1 < len(w); i += 2 {
			start := int(w[i].(object.Integer))
			run, isRun := w[i+1].(object.Array)
			if !isRun {
				t.Fatalf("/W entry at %d is not the [start [w...]] form this writes", i)
			}
			for j, v := range run {
				if start+j >= len(stated) {
					t.Fatalf("/W states a width for glyph %d, past the %d the program has",
						start+j, len(stated))
				}
				stated[start+j] = numberOf(t, v)
			}
		}
	}
	var wrong int
	for gid, w := range stated {
		if w != advances[gid] {
			wrong++
			if wrong <= 5 {
				t.Errorf("glyph %d: the font advances %v and /W says %v", gid, advances[gid], w)
			}
		}
	}
	if wrong > 5 {
		t.Errorf("%d glyphs in all have the wrong width", wrong)
	}
}

func numberOf(t *testing.T, o object.Object) float64 {
	t.Helper()
	switch v := o.(type) {
	case object.Integer:
		return float64(v)
	case object.Real:
		return float64(v)
	}
	t.Fatalf("%v is not a number", o)
	return 0
}

// TestTheEmbeddedNameCarriesTheSubsetTag pins that /BaseFont's tag is the one
// derived from the glyphs kept, not a constant.
//
// A reader tells two subsets of one face apart by that tag alone. Two documents
// embedding different glyph sets under the same name is a cache collision in
// the reader — the second document draws with the first one's glyphs — and
// nothing in either file shows it.
func TestTheEmbeddedNameCarriesTheSubsetTag(t *testing.T) {
	baseFontFor := func(text string) string {
		f, err := NotoSans()
		if err != nil {
			t.Fatal(err)
		}
		f.ShapeGlyphs(text)
		doc := &allocator{}
		ref, err := f.Embed(doc)
		if err != nil {
			t.Fatal(err)
		}
		name := doc.dict(t, ref).Get("BaseFont")
		if name == nil {
			t.Fatal("/BaseFont is missing")
		}
		_, kept, err := f.SubsetGlyphs()
		if err != nil {
			t.Fatal(err)
		}
		if want := object.Name(subsetTag(kept) + "+" + f.Name()); name != want {
			t.Errorf("/BaseFont = %v, want %v", name, want)
		}
		return string(name.(object.Name))
	}
	a, b := baseFontFor("abc"), baseFontFor("xyz")
	if a == b {
		t.Errorf("two different glyph sets embedded under the same name %q", a)
	}
	if again := baseFontFor("abc"); again != a {
		t.Errorf("the same glyph set embedded as %q and then %q", a, again)
	}
}
