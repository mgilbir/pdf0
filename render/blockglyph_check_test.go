package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// The checks on blockglyph_test.go, which is a piece of the oracle and so has to
// be held to the standard the oracle is: a rule that accepted too much would
// turn a real difference into a pass, and one that accepted too little would
// leave the failures it exists to remove.

// upx is a length in CSS pixels.
func upx(t *testing.T, v float64) style.Unit {
	t.Helper()
	u, ok := style.FromPx(v)
	if !ok {
		t.Fatalf("%v px is not a length", v)
	}
	return u
}

// TestBlockGlyphsCoverAhem classifies the shipped font and pins the answer.
//
// The numbers are not recorded from a run: they are what the font is documented
// to be, and reading them out of the file is what makes the rule in
// blockglyph_test.go a fact about this checkout rather than a hope. A font that
// stopped being rectangles would be caught here rather than quietly compared as
// text again.
func TestBlockGlyphsCoverAhem(t *testing.T) {
	bf := ahemBlockFont(t)

	if bf.unitsPerEm != 1000 {
		t.Errorf("Ahem's em is %d units, want 1000", bf.unitsPerEm)
	}

	// Every character the font has is either blank or a rectangle. That is the
	// property the whole conversion rests on, and it is the one worth asserting
	// rather than a count of how many are which.
	var blanks, full, partial int
	for _, br := range bf.rects {
		switch {
		case br.blank:
			blanks++
		case br.x0 == 0 && br.y0 == -200 && br.x1 == 1000 && br.y1 == 800:
			full++
		default:
			partial++
		}
	}
	if got := len(bf.rects); got != 278 {
		t.Errorf("classified %d of Ahem's characters, want all 278", got)
	}
	if full != 258 || blanks != 14 || partial != 6 {
		t.Errorf("Ahem is %d full squares, %d blank and %d partial rectangles; "+
			"want 258, 14 and 6", full, blanks, partial)
	}

	// The individual facts a test in the suite depends on. 'X' is the whole em
	// square from 0.8em above the baseline to 0.2em below, which is what makes
	// "four characters of 20px Ahem" an 80x20 block; a space inks nothing; 'p'
	// is the descender box alone and 'É' the ascender box alone, which is how a
	// test asserts where a baseline is.
	for _, tc := range []struct {
		r    rune
		want blockRect
	}{
		{'X', blockRect{x0: 0, y0: -200, x1: 1000, y1: 800}},
		{'a', blockRect{x0: 0, y0: -200, x1: 1000, y1: 800}},
		{' ', blockRect{blank: true}},
		{'\u00a0', blockRect{blank: true}},
		{'\u200b', blockRect{blank: true}},
		{'p', blockRect{x0: 0, y0: -200, x1: 1000, y1: 0}},
		{'É', blockRect{x0: 0, y0: 0, x1: 1000, y1: 800}},
	} {
		got, ok := bf.rects[tc.r]
		if !ok {
			t.Errorf("Ahem has no rectangle for %q", tc.r)
			continue
		}
		if got != tc.want {
			t.Errorf("Ahem %q is %+v, want %+v", tc.r, got, tc.want)
		}
	}
}

// TestBlockGlyphsRefuseOrdinaryFaces is the other half of the claim: the rule
// has to be selective, or the conversion would replace real glyphs with boxes
// and make two different words at the same place compare equal.
//
// The assertion this test was first written with was wrong, and the correction
// is worth recording because it changes what the rule is understood to mean. It
// asserted that a text face yields *no* rectangles. NotoSans yields twenty-five:
// 'l', the dotless 'ı', the Cyrillic 'І', '|', '_', the hyphen, the dashes, the
// minus sign and the combining overbars and strikethroughs. Every one of those
// really is a plain filled bar in a sans face, so calling it a rectangle is not
// a fault in the rule — it is the rule being right about a font that happens to
// draw a letter as one.
//
// What must hold is narrower and is what is checked here: a glyph with a curve
// or a counter in it is refused, and nothing in a text face is mistaken for the
// em-filling square that would make it compare equal to a box.
func TestBlockGlyphsRefuseOrdinaryFaces(t *testing.T) {
	dir := os.Getenv(notoEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make noto-fonts`) to check the rule refuses a text face", notoEnv)
	}
	data, err := os.ReadFile(filepath.Join(dir, "NotoSans-Regular.ttf"))
	if err != nil {
		t.Skipf("NotoSans-Regular.ttf: %v", err)
	}
	bf, err := newBlockFont(data)
	if err != nil {
		t.Fatalf("NotoSans has no glyph table this can read: %v", err)
	}

	// Letters and digits with a curve, a diagonal or a counter. None of these is
	// a rectangle in any face, and accepting one would be the failure mode that
	// matters — a word turned into boxes.
	for _, r := range []rune("AaBbCcOoQqXxYyZzGgSsWw02345689?!.,;:%&@#") {
		if br, ok := bf.rects[r]; ok && !br.blank {
			t.Errorf("NotoSans %q was classified as the rectangle %+v", r, br)
		}
	}

	// The ones it does accept must be bars: nothing in a text face fills the em
	// the way Ahem's squares do, and a glyph that did would compare equal to a
	// background box drawn behind it.
	em := bf.unitsPerEm
	var accepted int
	for r, br := range bf.rects {
		if br.blank {
			continue
		}
		accepted++
		w, h := br.x1-br.x0, br.y1-br.y0
		if w*3 > em*2 && h*3 > em*2 {
			t.Errorf("NotoSans %q (U+%04X) was classified as %dx%d font units, "+
				"which is most of a %d-unit em", r, r, w, h, em)
		}
	}
	if accepted == 0 {
		t.Error("no NotoSans glyph was classified as a rectangle, so the bound above " +
			"was never applied and this test proves nothing")
	}
	t.Logf("NotoSans: %d characters are plain rectangles and %d ink nothing; "+
		"every other glyph in the font was refused", accepted, len(bf.rects)-accepted)
}

// TestBlockFillsPlacesTheInk derives the rectangles a run of Ahem draws from the
// font's own metrics and requires the conversion to produce exactly those.
//
// The numbers are arithmetic, not a recording. Ahem advances 1em per character,
// its ink runs from 0.8em above the baseline to 0.2em below, so three characters
// of 20px Ahem with the baseline at y=100 are three 20x20 squares side by side
// with their tops at y=100-16=84.
func TestBlockFillsPlacesTheInk(t *testing.T) {
	face := ahemFace(t)
	black := style.RGBA{A: 1}
	run := DrawText{
		At: Point{X: upx(t, 30), Y: upx(t, 100)}, Text: "XXX",
		Face: face, Size: upx(t, 20), Color: black,
	}
	want := []Op{
		FillRect{Rect: Rect{upx(t, 30), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
		FillRect{Rect: Rect{upx(t, 50), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
		FillRect{Rect: Rect{upx(t, 70), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
	}
	assertOps(t, blockFills([]Op{run}), want)

	// A space inks nothing and still advances, so the character after it sits an
	// em further on and no rectangle is drawn where it was.
	run.Text = "X X"
	assertOps(t, blockFills([]Op{run}), []Op{
		FillRect{Rect: Rect{upx(t, 30), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
		FillRect{Rect: Rect{upx(t, 70), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
	})

	// 'p' is the descender box: 0.2em tall, sitting on the baseline rather than
	// above it, so its top is the baseline itself.
	run.Text = "p"
	assertOps(t, blockFills([]Op{run}), []Op{
		FillRect{Rect: Rect{upx(t, 30), upx(t, 100), upx(t, 20), upx(t, 4)}, Color: black},
	})

	// letter-spacing is an extra advance after every character, and layout has
	// already spent it in the run's width — so the second square is an em plus
	// the spacing along, not an em.
	run.Text = "XX"
	run.CharSpacing = upx(t, 5)
	assertOps(t, blockFills([]Op{run}), []Op{
		FillRect{Rect: Rect{upx(t, 30), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
		FillRect{Rect: Rect{upx(t, 55), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
	})

	// A right-to-left run draws its first character at the right-hand end. With
	// Ahem's uniform squares that is only visible where the glyphs differ, so
	// the check uses one that inks a different box.
	run = DrawText{
		At: Point{X: upx(t, 30), Y: upx(t, 100)}, Text: "pX", RTL: true,
		Face: face, Size: upx(t, 20), Color: black,
	}
	assertOps(t, blockFills([]Op{run}), []Op{
		// 'X' comes first visually, at the left.
		FillRect{Rect: Rect{upx(t, 30), upx(t, 84), upx(t, 20), upx(t, 20)}, Color: black},
		FillRect{Rect: Rect{upx(t, 50), upx(t, 100), upx(t, 20), upx(t, 4)}, Color: black},
	})
}

// TestBlockFillsLeavesTextAlone pins what the conversion refuses, which is the
// half that keeps the oracle sharp.
func TestBlockFillsLeavesTextAlone(t *testing.T) {
	ahemFace(t) // so that the font set, and so the registry, is built

	// A face with no rectangle glyphs is untouched, whatever it is asked to set.
	std, ok := StandardFonts().Face("serif", false, false)
	if !ok {
		t.Fatal("no standard serif face")
	}
	run := DrawText{
		At: Point{X: upx(t, 10), Y: upx(t, 10)}, Text: "XXX",
		Face: std, Size: upx(t, 20), Color: style.RGBA{A: 1},
	}
	got := blockFills([]Op{run})
	if len(got) != 1 {
		t.Fatalf("a standard face produced %d ops, want the run back unchanged", len(got))
	}
	if _, ok := got[0].(DrawText); !ok {
		t.Errorf("a standard face's run became %T", got[0])
	}

	// Ops that are not text pass through, and in their original order — the
	// order is what decides which of two overlapping marks is visible.
	red := style.RGBA{R: 255, A: 1}
	fill := FillRect{Rect: Rect{upx(t, 0), upx(t, 0), upx(t, 1), upx(t, 1)}, Color: red}
	ahemRun := DrawText{
		At: Point{X: upx(t, 0), Y: upx(t, 20)}, Text: "X",
		Face: ahemFace(t), Size: upx(t, 20), Color: red,
	}
	// A run with one character that is not a rectangle is left as text in its
	// entirety. Converting the rest would put some of the run on the page as
	// fills and leave the other characters to be compared as a shorter string,
	// which is neither of the two things this comparison knows how to do.
	//
	// Ahem cannot express that on its own — every character it has is a
	// rectangle — so the run is set in a Noto face, where 'l' is a plain bar and
	// 'o' is not.
	if noto := notoFaces(); len(noto) > 0 {
		if blockFonts[noto[0]] == nil {
			t.Fatal("the Noto face has no rectangle table, so the mixed run below proves nothing")
		}
		mixed := DrawText{
			At: Point{X: upx(t, 0), Y: upx(t, 20)}, Text: "lo",
			Face: noto[0], Size: upx(t, 20), Color: red,
		}
		if got := blockFills([]Op{mixed}); len(got) != 1 {
			t.Errorf("a run of one rectangle and one letter produced %d ops, want the run back", len(got))
		} else if _, ok := got[0].(DrawText); !ok {
			t.Errorf("a run of one rectangle and one letter became %T", got[0])
		}
		// And the all-rectangle run beside it *is* converted, so the check above
		// is about the mixed run rather than about the face.
		bars := mixed
		bars.Text = "ll"
		if got := blockFills([]Op{bars}); len(got) != 2 {
			t.Errorf("a run of two rectangles produced %d ops, want two fills", len(got))
		}
	}

	got = blockFills([]Op{fill, ahemRun, fill})
	if len(got) != 3 {
		t.Fatalf("got %d ops, want 3", len(got))
	}
	if got[0] != Op(fill) || got[2] != Op(fill) {
		t.Errorf("the fills around a converted run did not survive in place")
	}
	if _, ok := got[1].(FillRect); !ok {
		t.Errorf("the Ahem run became %T, want a fill", got[1])
	}
}

// TestBlockFillsRefusesWhatShapingMoved exercises the guard on the arithmetic.
//
// Reconstructing where each glyph sits means adding up advances, and a run where
// shaping moved a glyph — a kern, a ligature, a mark that attaches to the letter
// before it — is one the reconstruction would place wrongly. The guard is that
// the advances must sum to the width the face measures for the whole run.
//
// No font in this checkout can make it fire: of the sixteen font files in the
// WPT and Noto directories, not one has two rectangle glyphs whose pair measures
// anything other than the sum of the two. So the measurement is injected, which
// is the only way to see the guard work rather than assume it.
func TestBlockFillsRefusesWhatShapingMoved(t *testing.T) {
	face := ahemFace(t)
	bf := ahemBlockFont(t)
	run := DrawText{
		At: Point{X: upx(t, 0), Y: upx(t, 20)}, Text: "XX",
		Face: face, Size: upx(t, 20), Color: style.RGBA{A: 1},
	}

	// The face's own measurement, which agrees with itself, is converted. That
	// is the control: without it a guard that refused everything would pass this
	// test.
	if ops, ok := bf.fills(run, face.Measure); !ok || len(ops) != 2 {
		t.Fatalf("the unshaped control produced %d ops (ok=%v), want two fills", len(ops), ok)
	}

	// A face that sets the pair narrower than the two characters apart — a kern,
	// or a ligature — must be refused rather than drawn an em apart.
	kerned := func(s string, size float64) float64 {
		if len([]rune(s)) > 1 {
			return face.Measure(s, size) - 3
		}
		return face.Measure(s, size)
	}
	if ops, ok := bf.fills(run, kerned); ok {
		t.Errorf("a run whose pair measures 3px narrower than its characters was "+
			"converted to %d fills", len(ops))
	}
	// And in the other direction, since a mark that adds width is as wrong as
	// one that removes it.
	spread := func(s string, size float64) float64 {
		if len([]rune(s)) > 1 {
			return face.Measure(s, size) + 3
		}
		return face.Measure(s, size)
	}
	if _, ok := bf.fills(run, spread); ok {
		t.Error("a run whose pair measures 3px wider than its characters was converted")
	}
}

// TestBlockGlyphRuleHasTeeth plants the outlines the rule must refuse.
//
// Every one of these is a shape that is not a filled axis-aligned rectangle, and
// accepting any of them would put ink on the comparison's page that the document
// does not put on its own.
func TestBlockGlyphRuleHasTeeth(t *testing.T) {
	square := [][2]int{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
	allOn := []bool{true, true, true, true}

	// The reference shape must be accepted, or the refusals below prove nothing.
	sq := simpleGlyph(1, square, allOn)
	br, ok := rectContour(sq)
	if !ok {
		t.Fatal("the reference square was refused, so this test proves nothing")
	}
	if br != (blockRect{x0: 0, y0: 0, x1: 100, y1: 100}) {
		t.Fatalf("the reference square read as %+v, want 0,0 100,100", br)
	}
	// And the same square written the other way round, so that a delta the
	// decoder sign-extends wrongly is caught rather than cancelling out.
	if br, ok := rectContour(simpleGlyph(1,
		[][2]int{{-40, -60}, {-40, 40}, {60, 40}, {60, -60}}, allOn)); !ok {
		t.Error("a square with negative coordinates was refused")
	} else if br != (blockRect{x0: -40, y0: -60, x1: 60, y1: 40}) {
		t.Errorf("a square with negative coordinates read as %+v, want -40,-60 60,40", br)
	}

	for _, tc := range []struct {
		name string
		g    []byte
	}{
		{"a composite glyph", simpleGlyph(-1, square, allOn)},
		{"two contours", simpleGlyph(2, square, allOn)},
		{"three points", simpleGlyph(1, square[:3], allOn[:3])},
		// Six points tracing the rectangle one and a half times round. Every
		// corner is right and every side is axis-aligned, so nothing but the
		// four-point bound refuses it — which is what makes it the case that
		// shows the bound doing work rather than being shadowed.
		//
		// This one is a *conservative* refusal rather than a correction: the
		// contour really does ink the rectangle, and a version of this that
		// walked any number of points would be entitled to accept it. Refusing
		// it costs a pass and cannot cost the oracle a tooth, which is the
		// direction to be wrong in, and the point of the case is to pin that the
		// bound is where the refusal comes from.
		{"six points round the rectangle", simpleGlyph(1,
			[][2]int{{0, 0}, {100, 0}, {100, 100}, {0, 100}, {0, 0}, {100, 0}},
			[]bool{true, true, true, true, true, true})},
		// Four points with one off the curve is a shape with a curved side.
		{"a curve", simpleGlyph(1, square, []bool{true, false, true, true})},
		// Four on-curve corners, but not axis-aligned.
		{"a parallelogram", simpleGlyph(1,
			[][2]int{{0, 0}, {100, 0}, {120, 100}, {20, 100}}, allOn)},
		// Four collinear points enclose no area. The corner rule is what catches
		// this, since a box with a zero-length side has only two corners to
		// offer and four distinct ones are required.
		{"a line", simpleGlyph(1,
			[][2]int{{0, 0}, {30, 0}, {60, 0}, {90, 0}}, allOn)},
		// A one-unit-tall bar written as four points, two of them coincident:
		// the same argument, at the smallest size where the area is not zero,
		// so that a rule keyed on area rather than on corners is not what is
		// being relied on.
		{"a doubled corner", simpleGlyph(1,
			[][2]int{{0, 0}, {100, 0}, {100, 1}, {100, 0}}, allOn)},
		// Four points on the rectangle's edges, but two of them the same
		// corner: three corners with a repeat is a triangle, not a rectangle.
		{"a triangle", simpleGlyph(1,
			[][2]int{{0, 0}, {100, 0}, {100, 100}, {100, 0}}, allOn)},
		// The four corners of a rectangle, visited across the diagonal. It
		// draws two triangles meeting at the centre, not the box, so the ink is
		// half what a rectangle would put down and in the wrong half.
		{"a bow tie", simpleGlyph(1,
			[][2]int{{0, 0}, {100, 100}, {100, 0}, {0, 100}}, allOn)},
		{"a truncated description", sq[:len(sq)-3]},
		{"nothing at all", nil},
	} {
		if br, ok := rectContour(tc.g); ok {
			t.Errorf("%s was accepted as the rectangle %+v", tc.name, br)
		}
	}
}

// simpleGlyph writes a TrueType glyph description with absolute points, using
// the two-byte delta form throughout so that nothing turns on the short-vector
// encoding — that is the decoder's business, and the fonts exercise it.
func simpleGlyph(nContours int16, pts [][2]int, onCurve []bool) []byte {
	end := uint16(len(pts) - 1)
	g := []byte{
		byte(uint16(nContours) >> 8), byte(uint16(nContours)),
		0, 0, 0, 0, 0, 0, 0, 0, // the bounding box, which is not read
		byte(end >> 8), byte(end),
		0, 0, // no instructions
	}
	for _, on := range onCurve {
		var f byte
		if on {
			f = 1
		}
		g = append(g, f)
	}
	put := func(axis int) {
		prev := 0
		for _, p := range pts {
			d := uint16(int16(p[axis] - prev))
			g = append(g, byte(d>>8), byte(d))
			prev = p[axis]
		}
	}
	put(0)
	put(1)
	return g
}

// ahemFace is the test font, or a skip.
func ahemFace(t *testing.T) *fonts.Face {
	t.Helper()
	wptDir(t)
	fs, ok := fontSetForWPT().(wptFonts)
	if !ok || fs.ahem == nil {
		t.Skip("the checkout has no Ahem.ttf")
	}
	return fs.ahem
}

func ahemBlockFont(t *testing.T) *blockFont {
	t.Helper()
	bf := blockFonts[ahemFace(t)]
	if bf == nil {
		t.Fatal("Ahem was loaded but has no rectangle table")
	}
	return bf
}

// assertOps compares two display lists exactly, in order.
func assertOps(t *testing.T, got, want []Op) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d ops, want %d:\ngot:\n%swant:\n%s",
			len(got), len(want), dumpForDiff(got), dumpForDiff(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("op %d:\n got %v\nwant %v", i, got[i], want[i])
		}
	}
}

func dumpForDiff(ops []Op) string {
	var s string
	for _, op := range ops {
		if f, ok := op.(FillRect); ok {
			s += "  fill " + rectKey(f.Rect) + " " + f.Color.String() + "\n"
			continue
		}
		s += "  other\n"
	}
	return s
}
