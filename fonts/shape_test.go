package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/fonttest"
)

// Shaping checked against a font whose kerning and ligatures the test states,
// so an assertion can name the exact adjustment rather than whatever a real
// face happens to contain.

// shapingFace builds a face with glyphs A V f i, a kern pair and an fi
// ligature. Glyph indices follow the order given: A=1, V=2, f=3, i=4, fi=5.
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
func TestKerningIsRead(t *testing.T) {
	f := shapingFace(t)
	if !f.HasKerning() {
		t.Fatal("the font's GPOS kern feature was not read")
	}
	if got := f.layout.kern[[2]int{1, 2}]; got.firstAdvance != -80 {
		t.Errorf("kern(A,V) = %d, want -80", got)
	}
	if _, ok := f.layout.kern[[2]int{2, 1}]; ok {
		t.Error("kerning was applied in the wrong order: (V,A) is not a declared pair")
	}
}

// TestShapeInvertsTheKernSign is the assertion the whole feature turns on. A
// kern closes a gap; TJ *subtracts* its number from the position. So a negative
// kern must appear as a positive displacement, and getting it backwards spreads
// text out by exactly the amount it should have tightened — which looks
// plausible enough to ship.
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
func TestShapeMeasuresWhatItDraws(t *testing.T) {
	f := shapingFace(t)
	// A=700, V=700, kern -80 → 1320/1000 em.
	if got, want := f.MeasureShaped("AV", 10), 13.2; got != want {
		t.Errorf("MeasureShaped(\"AV\") = %v, want %v", got, want)
	}
	// Unshaped, the same string is wider: no kerning is applied.
	if got, want := f.Measure("AV", 10), 14.0; got != want {
		t.Errorf("Measure(\"AV\") = %v, want %v", got, want)
	}
	// The ligature is narrower than its parts: f=300 + i=250 = 550, fi = 520.
	if got, want := f.MeasureShaped("fi", 10), 5.2; got != want {
		t.Errorf("MeasureShaped(\"fi\") = %v, want %v", got, want)
	}
}

// TestShapeRecordsTheGlyphsItUses pins that shaping feeds the subsetter. A
// ligature glyph reached only through substitution is one no Encode call ever
// named, and a subset that dropped it would leave the page blank exactly where
// the ligature was.
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
