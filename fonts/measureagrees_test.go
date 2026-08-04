package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/content"
)

// Measuring must agree with drawing.
//
// A layout engine measures a word to decide whether it fits the line, then
// draws it. Those are different calls, and if they disagree the line is filled
// to one width and painted at another: the text overruns the column or stops
// short of it, and nothing in either call's own output shows it. It is the kind
// of defect that looks like a font problem for a week.
//
// There are three ways to put shaped text on a page and two ways to ask how wide
// it will be, and all five have to answer the same question the same way:
//
//	ShapeGlyphs + Draw     MeasureGlyphs
//	Shape       + ShowTextAdjusted
//	DrawShaped             MeasureShaped
//
// They did not. MeasureShaped and Shape were a second, weaker shaper — a
// flattened ligature table and a kerning map, which is most of shaping and not
// all of it: no contextual substitution, no syllabic reordering, no positioning
// beyond pair kerning. Over the corpus in testdata/harfbuzz that was wrong for
// 1920 of 5911 strings, by up to 17% on a Devanagari conjunct. Both now shape
// the text and then measure or serialise the result, and the second shaper is
// gone rather than merely unused.

// TestEveryWayOfMeasuringAgreesWithEveryWayOfDrawing runs the whole HarfBuzz
// corpus through both, which is what makes this more than a spot check: it is
// five thousand strings chosen to make shaping decide something.
func TestEveryWayOfMeasuringAgreesWithEveryWayOfDrawing(t *testing.T) {
	corpus, _, _ := readHarfBuzzGolden(t)
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
			units += f.advanceGID(int(s.Codes[i])<<8 | int(s.Codes[i+1]))
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
	corpus, _, _ := readHarfBuzzGolden(t)
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
			pen += f.advanceGID(gid)
			at++
		}
		pen -= sp.Adjust
	}
	if at != 2 {
		t.Errorf("Shape emitted %d glyphs, want 2", at)
	}
}
