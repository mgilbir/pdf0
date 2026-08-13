package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// The metrics this engine used to guess at, and now asks the font for.
//
// Both of these have a fallback that is correct to use and wrong to use always.
// CSS lets a renderer assume half an em for an x-height it cannot determine, and
// recommends between 1.0 and 1.2 for a line height it cannot derive — so for as
// long as the face reported neither, the fallback *was* the specified answer and
// there was nothing to compare it against. The tests below are the two halves
// that only became distinguishable once forme reported what a font states: the
// font's answer when there is one, the fallback when there is not.

// ahemSet is a FontSet answering for Ahem, whose metrics are exact by design —
// every glyph an em square, ascent 800, descent -200, x-height 800, all out of
// 1000 units. A face invented for tests is the right one to assert against.
type ahemSet struct {
	ahem     *fonts.Face
	standard FontSet
}

func (a ahemSet) Face(family string, bold, italic bool) (*fonts.Face, bool) {
	if family == "Ahem" {
		return a.ahem, true
	}
	return a.standard.Face(family, bold, italic)
}

func loadAhem(t *testing.T) FontSet {
	t.Helper()
	dir := os.Getenv(wptEnv)
	if dir == "" {
		t.Skipf("set %s to check the metrics against a face that states them", wptEnv)
	}
	data, err := os.ReadFile(filepath.Join(dir, "fonts", "Ahem.ttf"))
	if err != nil {
		t.Skipf("no Ahem: %v", err)
	}
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading Ahem: %v", err)
	}
	return ahemSet{ahem: face, standard: StandardFonts()}
}

// boxHeight lays a document out against a font set and returns an element's
// border-box height.
func boxHeight(t *testing.T, set FontSet, htmlSrc, cssSrc, id string) float64 {
	t.Helper()
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: cssSrc}}})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(10000)
	frag := Layout(built.Root, Size{W: w, H: h}, set, rec)
	return find(t, frag, id).BorderRect.H.Px()
}

func TestNormalLineHeightComesFromTheFont(t *testing.T) {
	set := loadAhem(t)
	// Ahem states ascent 800, descent -200 and a line gap of 0, out of 1000
	// units — exactly one em, which is right for a face whose glyphs are em
	// squares and is a figure no constant would have produced. At 20px the line
	// box is 20px, not the 24px a factor of 1.2 gives.
	got := boxHeight(t, set, `<p id="p">x</p>`,
		noDefaults+`p { font-family: Ahem; font-size: 20px; line-height: normal }`, "p")
	if got != 20 {
		t.Errorf("a line of 20px Ahem is %gpx tall, want 20 — "+
			"(800 + 200 + 0) / 1000 of an em", got)
	}
}

// TestNormalLineHeightIncludesTheLineGap is the term that makes this the
// font's answer rather than a two-term approximation of it.
//
// It needs a face that states a non-zero gap, and almost none does: of every
// font in this checkout — the whole Noto fallback set and all of the suite's —
// exactly four do, all of them CanvasTest. Ahem states zero, Noto Sans states
// zero, so a test built on either agrees whether the term is there or not, and
// a planted defect that dropped it from the sum went unnoticed until this test
// existed.
func TestNormalLineHeightIncludesTheLineGap(t *testing.T) {
	dir := os.Getenv(wptEnv)
	if dir == "" {
		t.Skipf("set %s for a face that states a line gap", wptEnv)
	}
	data, err := os.ReadFile(filepath.Join(dir, "fonts", "CanvasTest.ttf"))
	if err != nil {
		t.Skipf("no CanvasTest.ttf: %v", err)
	}
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading CanvasTest: %v", err)
	}
	set := ahemSet{ahem: face, standard: StandardFonts()}

	// ascent 1745, descent -805, line gap 92, out of 1024 units: 2642/1024 of an
	// em. At 20px that is 51.6015625 in real arithmetic and it is not what a
	// layout unit can hold — 20px is 1280 units, 1280 x 2642/1024 is 3302.5, and
	// half a unit rounds up to 3303, which is 51.609375. The expectation is the
	// quantised figure and the workings are here because a test that quietly
	// widened its tolerance to absorb the difference would also absorb a bug of
	// the same size.
	//
	// Without the gap the sum is 2550/1024: 3187.5 units, rounding to 3188,
	// which is 49.8125. Far enough apart that the rounding cannot hide it.
	got := boxHeight(t, set, `<p id="p">x</p>`,
		noDefaults+`p { font-family: Ahem; font-size: 20px; line-height: normal }`, "p")
	const want = 51.609375
	if got != want {
		t.Errorf("a line of 20px CanvasTest is %gpx tall, want %g — "+
			"(1745 + 805 + 92) / 1024 of an em, quantised to a 64th; %g is the "+
			"sum without the gap", got, want, 49.8125)
	}
}

// TestNormalLineHeightUsesTheGlyphBoxWhenTheFontStatesNoGap replaces a test
// that asserted the 1.2 fallback here.
//
// A standard PDF face has no hhea and no OS/2: its metrics come from AFM data,
// which carries no line gap at all, so there is no line spacing to read. What
// the AFM does carry is the box enclosing every glyph, and that is the question
// being asked — how much room does a line of this face need — so it is used
// rather than a constant.
//
// Helvetica's box is -225 to 931 out of 1000: 1.156em, or 23.125px at 20. The
// AFM's own Ascender and Descender come to 0.925em, which is what a line spaced
// by them would be and is tighter than any browser sets the same text; the 1.2
// factor that used to stand here was in §10.8.1's recommended range but bore no
// relation to the *content area*, which is measured from the same two numbers
// and so came out at 0.925em beside a 1.2em line.
func TestNormalLineHeightUsesTheGlyphBoxWhenTheFontStatesNoGap(t *testing.T) {
	got := boxHeight(t, StandardFonts(), `<p id="p">x</p>`,
		noDefaults+`p { font-family: Helvetica; font-size: 20px; line-height: normal }`, "p")
	if got != 23.125 {
		t.Errorf("a line of 20px Helvetica is %gpx tall, want 23.125 — the glyph "+
			"box, (931 + 225) / 1000 of an em; 24 is the old constant and 18.5 "+
			"the AFM ascender and descender alone", got)
	}
}

// TestNormalLineHeightPrefersTheFontsOwnMetricsOverItsGlyphBox is the other
// side of lineMetrics, and it exists because a planted defect that used the box
// for *every* face went unnoticed by every other test here.
//
// The faces the rest of this file uses cannot tell the two apart: Ahem's glyphs
// are em squares, so its box is exactly its ascent and descent, and CanvasTest's
// box matches its hhea too. Noto Sans does not — it states 1069 and -293 out of
// 1000 and has a box running from -389 to 1067, because a handful of its glyphs
// reach far past the height the font asks for its lines to be set at. That is
// the whole reason the box is a fallback and not the answer: it is the extreme
// of what the face can draw, and hhea is what the face asks for.
func TestNormalLineHeightPrefersTheFontsOwnMetricsOverItsGlyphBox(t *testing.T) {
	dir := os.Getenv(notoEnv)
	if dir == "" {
		t.Skipf("set %s for a face whose box and metrics differ", notoEnv)
	}
	data, err := os.ReadFile(filepath.Join(dir, "NotoSans-Regular.ttf"))
	if err != nil {
		t.Skipf("no Noto Sans: %v", err)
	}
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading Noto Sans: %v", err)
	}
	d := face.Descriptor()
	if !d.Has(fonts.MetricLineGap) {
		t.Skip("this face states no line gap, so it is the fallback case")
	}
	upem := float64(face.UnitsPerEm())
	size, _ := style.FromPx(20)
	// Both expectations are computed from the Descriptor rather than from
	// lineMetrics, so that the test does not agree with the code by construction.
	stated := size.Mul((float64(d.Ascent) - float64(d.Descent) + float64(d.LineGap)) / upem)
	box := size.Mul(float64(d.BBox[3]-d.BBox[1]) / upem)
	if stated == box {
		t.Skip("this face's box is its metrics, so nothing here discriminates")
	}

	set := ahemSet{ahem: face, standard: StandardFonts()}
	got := boxHeight(t, set, `<p id="p">x</p>`,
		noDefaults+`p { font-family: Ahem; font-size: 20px; line-height: normal }`, "p")
	if got != stated.Px() {
		t.Errorf("a line of 20px Noto Sans is %gpx tall, want %g — what the face "+
			"states; %g is its glyph box, which is only used by a face that "+
			"states nothing", got, stated.Px(), box.Px())
	}
}

// TestTheLineHeightAndTheContentAreaAgree is the relationship the change above
// was for, and the one the suite checks directly.
//
// An inline box's background paints its content area, §10.6.1; a block's height
// with "line-height: normal" is its line box. For a face that states no line gap
// there is no leading to add, so the two are the same number — and
// inline-formatting-context-002 puts a black inline box beside a float holding
// the same text and asks for exactly that. They did not agree: the background
// was measured from the AFM's ascender and descender, 0.925em, and the line from
// a 1.2 constant.
//
// It is asserted through the paint rather than through the two functions,
// because comparing the two functions when both now call the same one would be
// a tautology — the defect being guarded against is precisely that they stop
// sharing a source.
func TestTheLineHeightAndTheContentAreaAgree(t *testing.T) {
	for _, family := range []string{"Helvetica", "Times", "Courier"} {
		css := noDefaults + `
			p { font-family: ` + family + `; font-size: 100px; line-height: normal;
			    margin: 0 }
			span { background: green }`
		root := layoutOf(t, 4000, `<p id="p"><span>ab</span></p>`, css)
		background := oneFill(t, Paint(root), green)
		line := find(t, root, "p").BorderRect.H
		if background.H != line {
			t.Errorf("%s: the inline background is %gpx tall and the line box %gpx; "+
				"a face that states no line gap has no leading to tell them apart",
				family, background.H.Px(), line.Px())
		}
	}
}

func TestExComesFromTheFont(t *testing.T) {
	set := loadAhem(t)
	// Ahem states an x-height of 800/1000, so 1ex at 20px is 16px and a box
	// 3ex tall is 48. Half an em — the fallback — would make it 30.
	got := boxHeight(t, set, `<div id="d"></div>`,
		noDefaults+`#d { font-family: Ahem; font-size: 20px; height: 3ex }`, "d")
	if got != 48 {
		t.Errorf("3ex of 20px Ahem is %gpx, want 48 (3 x 0.8 x 20)", got)
	}
}

func TestExFallsBackToHalfAnEm(t *testing.T) {
	// CSS Values §5.1.2: half an em must be assumed where the x-height cannot be
	// determined. A standard face states none, so this is the specified answer
	// rather than a guess — and it is only reachable *because* the other case
	// now exists.
	got := boxHeight(t, StandardFonts(), `<div id="d"></div>`,
		noDefaults+`#d { font-family: Helvetica; font-size: 20px; height: 3ex }`, "d")
	if got != 30 {
		t.Errorf("3ex of 20px Helvetica is %gpx, want 30 (3 x 0.5 x 20)", got)
	}
}

func TestExFollowsTheFace(t *testing.T) {
	// The parsed length is memoized, and the key has to separate two boxes at
	// the same size in different faces or the first to be laid out decides "ex"
	// for the other. The same bug was fixed for "ch" and would have been
	// reintroduced here by reusing its key.
	set := loadAhem(t)
	const css = noDefaults + `
	  div { font-size: 20px; height: 3ex }
	  #a { font-family: Ahem }
	  #b { font-family: Helvetica }`
	a := boxHeight(t, set, `<div id="a"></div><div id="b"></div>`, css, "a")
	b := boxHeight(t, set, `<div id="a"></div><div id="b"></div>`, css, "b")
	if a != 48 || b != 30 {
		t.Errorf("3ex resolved to %gpx in Ahem and %gpx in Helvetica, want 48 and 30 — "+
			"equal values mean the cached length is shared across faces", a, b)
	}
}
