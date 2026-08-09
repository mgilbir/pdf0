package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/style"
)

// text-decoration, CSS 2.1 §16.3.1.
//
// Every number below is arithmetic that can be read rather than a figure
// recorded from a run. Courier advances 600/1000, so a six-character word at
// 20px is 72px wide; a decoration is 0.05em thick, so at 20px it is 1px; and an
// underline's centre is 0.1em below the baseline, so its top edge is 20 x 0.1 -
// 0.5 = 1.5px below it.
//
// Asserting "something was painted" is what makes a decoration test worthless —
// a document paints a great deal, and a test that counts marks passes for a
// dozen wrong reasons. So each of these names the rectangle it expects: its
// colour, its width, and where its top edge is relative to the baseline of the
// text it belongs to.

const decoCSS = `#p { font-family: Courier; font-size: 20px; color: #000000 }`

// bands returns the filled rectangles of a given colour, which is how a
// decoration is told apart from a background: nothing else in these documents
// paints.
func bands(ops []Op, want style.RGBA) []Rect {
	var out []Rect
	for _, op := range ops {
		r, ok := op.(FillRect)
		if !ok || r.Color != want {
			continue
		}
		out = append(out, r.Rect)
	}
	return out
}

// baselineOfFirstRun is where the text of an element sits, in page coordinates.
func baselineOfFirstRun(t *testing.T, root *Fragment, id string) style.Unit {
	t.Helper()
	f := find(t, root, id)
	if len(f.Lines) == 0 {
		t.Fatalf("#%s has no lines", id)
	}
	return f.ContentRect().Y.Add(f.Lines[0].Rect.Y).Add(f.Lines[0].Baseline)
}

var black = style.RGBA{A: 1}

func TestUnderlineIsDrawnUnderTheText(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">abcdef</div>`,
		noDefaults+decoCSS+` #p { text-decoration: underline }`)
	got := bands(Paint(root), black)
	if len(got) != 1 {
		t.Fatalf("an underlined word painted %d bands, want 1", len(got))
	}
	// Six Courier characters at 20px: 6 x 0.6 x 20 = 72px.
	if w := got[0].W.Px(); w != 72 {
		t.Errorf("the underline is %gpx wide, want 72 — the width of the text", w)
	}
	if h := got[0].H.Px(); h != 1 {
		t.Errorf("the underline is %gpx thick, want 1 (0.05em at 20px)", h)
	}
	// The band's top edge is 0.1em below the baseline less half a thickness.
	baseline := baselineOfFirstRun(t, root, "p")
	if want := baseline.Add(mustPx(1.5)); got[0].Y != want {
		t.Errorf("the underline's top edge is at %gpx and the baseline at %gpx; "+
			"want the edge 1.5px below it", got[0].Y.Px(), baseline.Px())
	}
}

func TestOverlineAndLineThroughSitWhereTheyShould(t *testing.T) {
	// Courier's ascent is 629/1000 and its cap height 562/1000, so at 20px an
	// overline's top edge is 20 x 0.629 = 12.58px above the baseline, and a
	// line-through is centred on half an x-height of 20 x 0.562 x 0.7 = 7.868px,
	// putting its top edge 7.868/2 + 0.5 = 4.434px above it.
	//
	// The two are asserted together because the whole point is that they are
	// *different* heights: an implementation that drew all three lines in one
	// place would satisfy either assertion on its own.
	root := layoutOf(t, 600, `<div id="p">abcdef</div>`,
		noDefaults+decoCSS+` #p { text-decoration: overline }`)
	over := bands(Paint(root), black)
	if len(over) != 1 {
		t.Fatalf("an overlined word painted %d bands, want 1", len(over))
	}
	baseline := baselineOfFirstRun(t, root, "p")
	aboveOver := baseline.Sub(over[0].Y).Px()

	root = layoutOf(t, 600, `<div id="p">abcdef</div>`,
		noDefaults+decoCSS+` #p { text-decoration: line-through }`)
	through := bands(Paint(root), black)
	if len(through) != 1 {
		t.Fatalf("a struck word painted %d bands, want 1", len(through))
	}
	aboveStrike := baseline.Sub(through[0].Y).Px()

	if aboveOver < 12.5 || aboveOver > 12.7 {
		t.Errorf("the overline's top edge is %gpx above the baseline, want about "+
			"12.58 — the face's own ascent at 20px", aboveOver)
	}
	if aboveStrike < 4.3 || aboveStrike > 4.5 {
		t.Errorf("the line-through's top edge is %gpx above the baseline, want about "+
			"4.43 — half the x-height", aboveStrike)
	}
	if aboveOver <= aboveStrike {
		t.Errorf("the overline is at %gpx and the line-through at %gpx above the "+
			"baseline; the overline must be the higher of the two", aboveOver, aboveStrike)
	}
}

func TestDecorationReachesADescendantInTheDeclaringBoxesColour(t *testing.T) {
	// §16.3.1's two rules at once, and the pair is the point. The <em> is
	// underlined although it declares nothing, and the line is the *paragraph's*
	// black rather than the em's red — a version built on inheritance would
	// underline the em in red, which is a plausible page and the wrong one.
	root := layoutOf(t, 600,
		`<div id="p">ab<em id="e">cdef</em></div>`,
		noDefaults+decoCSS+` #p { text-decoration: underline } #e { color: #ff0000 }`)
	ops := Paint(root)

	red := style.RGBA{R: 255, A: 1}
	if got := bands(ops, red); len(got) != 0 {
		t.Errorf("%d bands were painted in the em's own red; the decoration takes "+
			"the colour of the box that declared it", len(got))
	}
	got := bands(ops, black)
	if len(got) != 2 {
		t.Fatalf("the two runs painted %d black bands, want one each", len(got))
	}
	// "ab" then "cdef": 24px and 48px, adjoining, so the line reads as one.
	total := got[0].W.Add(got[1].W).Px()
	if total != 72 {
		t.Errorf("the two bands are %gpx wide together, want 72 — the whole text", total)
	}
	if got[0].Right() != got[1].X {
		t.Errorf("the bands are at %v and %v and leave a gap; an underline across "+
			"two runs has to join up", got[0], got[1])
	}
}

func TestDecorationDoesNotReachIntoAnInlineBlock(t *testing.T) {
	// §16.3.1: the decoration is not drawn across an atomic inline. The
	// inline-block holds text of its own, and a line ruled through the paragraph
	// stops at its edge.
	root := layoutOf(t, 600,
		`<div id="p">ab<span id="k">cdef</span></div>`,
		noDefaults+decoCSS+` #p { text-decoration: underline }
		 #k { display: inline-block }`)
	got := bands(Paint(root), black)
	if len(got) != 1 {
		t.Fatalf("%d bands were painted, want 1 — only the text outside the "+
			"inline-block is underlined", len(got))
	}
	if w := got[0].W.Px(); w != 24 {
		t.Errorf("the underline is %gpx wide, want 24 — the two characters before "+
			"the inline-block and not the four inside it", w)
	}
}

func TestDecorationColourIsItsOwnWhenDeclared(t *testing.T) {
	// text-decoration-color is not "currentcolor" here, so the line and the
	// letters are different colours. Nothing else this engine draws can produce a
	// blue rectangle in this document.
	root := layoutOf(t, 600, `<div id="p">abcdef</div>`,
		noDefaults+decoCSS+` #p { text-decoration: underline #0000ff }`)
	blue := style.RGBA{B: 255, A: 1}
	got := bands(Paint(root), blue)
	if len(got) != 1 {
		t.Fatalf("%d blue bands were painted, want 1", len(got))
	}
	if got := bands(Paint(root), black); len(got) != 0 {
		t.Errorf("%d bands were painted in the text's colour as well", len(got))
	}
}

func TestUnderlineAndOverlineTogether(t *testing.T) {
	// One declaration asking for two lines. The shorthand used to keep the first
	// keyword and report the second as unimplemented, which was a finding about
	// its own reading rather than about anything missing.
	root := layoutOf(t, 600, `<div id="p">abcdef</div>`,
		noDefaults+decoCSS+` #p { text-decoration: underline overline }`)
	got := bands(Paint(root), black)
	if len(got) != 2 {
		t.Fatalf("\"underline overline\" painted %d bands, want 2", len(got))
	}
	if got[0].Y == got[1].Y {
		t.Errorf("both bands are at y=%gpx; the two lines go in different places",
			got[0].Y.Px())
	}
}

func TestLinksAreUnderlinedByTheDefaultStylesheet(t *testing.T) {
	// The user-agent stylesheet has said "a { text-decoration-line: underline }"
	// all along and nothing drew it, which is the gap this file closes. The
	// colour is the stylesheet's #0000ee, so the band cannot be confused with
	// anything else on the page.
	root := layoutOf(t, 600, `<p><a href="x" id="a">abcdef</a></p>`, decoCSS)
	linkBlue := style.RGBA{B: 238, A: 1}
	if got := bands(Paint(root), linkBlue); len(got) != 1 {
		t.Fatalf("a link painted %d underlines, want 1", len(got))
	}
}

func TestDecorationIsNotPaintedForAHiddenRun(t *testing.T) {
	// visibility and text-decoration meet here: a hidden box paints neither its
	// letters nor the line ruled through them.
	root := layoutOf(t, 600, `<div id="p">abcdef</div>`,
		noDefaults+decoCSS+` #p { text-decoration: underline; visibility: hidden }`)
	if got := bands(Paint(root), black); len(got) != 0 {
		t.Errorf("a hidden run painted %d decoration bands", len(got))
	}
}

func TestAnOverlineAboveThePageTopStillProducesADocument(t *testing.T) {
	// A line box shorter than its own font — which "line-height: 0.3" asks for —
	// puts the overline above the line box, and at the top of a page that is above
	// the page. The overflow-page guardrail is an Error, so a decoration counted
	// as a box there would refuse to produce a document at all: a two-pixel
	// overhang turned into no output, from a rule whose purpose is to catch a
	// wrong scale calculation.
	//
	// The letters' own ascenders reach the same place and are not counted, which
	// is the argument for the decoration not being either.
	res, err := Render(Input{
		HTML: `<div id="p">abcdef</div>`,
		CSS: []Stylesheet{{Source: `html, body { margin: 0; padding: 0 }
			#p { line-height: 0.3; font-size: 40px; text-decoration: overline }`}},
	}, Options{})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if res.Document == nil {
		t.Fatalf("no document was produced: %v", res.Findings)
	}
	for _, f := range res.Findings {
		if f.Rule == RuleOverflowPage {
			t.Errorf("an overline was counted as content leaving the page: %s", f.Error())
		}
	}
	// And the guard still has teeth for a real box in the same place: a
	// background pulled above the page top is content leaving the page.
	rec := NewRecorder(nil)
	ops := []Op{FillRect{Rect: Rect{Y: mustPx(-10), W: mustPx(10), H: mustPx(5)},
		Color: black}}
	checkPageOverflow(rec, ops, A4.Content(), 1)
	if rec.Count(RuleOverflowPage) == 0 {
		t.Error("a box above the page top was not reported; the guard is now blind")
	}
}

func TestUnknownDecorationLineIsReported(t *testing.T) {
	// "blink" is correct CSS this engine does not draw. The longhand goes through
	// the registry untouched, so without a check of its own it would be a
	// declaration that was understood, stored and silently drawn as nothing.
	// Two documents rather than one, because "reported once" on a single element
	// is a claim about there being one place to complain about rather than about
	// the suppression. The second document names two *different* undrawable
	// values and expects two findings, which is what shows both elements were
	// visited; the first then shows that two elements naming the same value
	// produce one. A stylesheet using "blink" on four hundred elements is one
	// thing an author needs to be told, and the Recorder cannot do that on its
	// own: it keys on the element as well as the message.
	same := decorationFindings(t,
		`<div class="p">abcdef</div><p class="p">ghi</p>`,
		`.p { text-decoration-line: blink }`)
	if same != 1 {
		t.Errorf("one undrawable value on two elements was reported %d times, want once",
			same)
	}
	both := decorationFindings(t,
		`<div id="a">abcdef</div><p id="b">ghi</p>`,
		`#a { text-decoration-line: blink } #b { text-decoration-line: grammar-error }`)
	if both != 2 {
		t.Errorf("two different undrawable values were reported %d times, want twice — "+
			"without two the document above proves nothing about the suppression", both)
	}
}

func decorationFindings(t *testing.T, htmlSrc, cssSrc string) int {
	t.Helper()
	rec := NewRecorder(nil)
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: cssSrc}}})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, nil, rec)

	n := 0
	for _, f := range rec.Findings() {
		if f.Property == "text-decoration-line" {
			n++
		}
	}
	return n
}

// TestUnderlineComesFromTheFaceThatStatesOne is the other half of the fallback
// the tests above pin.
//
// Those use Courier, a standard PDF face, which has no post table at all — its
// metrics come from AFM data and forme reports no underline for it, so the
// 0.05em/0.1em fractions stand and are the specified thing to do. A face that
// *does* state a position and a thickness must be believed instead, and until
// forme reported the post table there was no way to tell the two situations
// apart.
func TestUnderlineComesFromTheFaceThatStatesOne(t *testing.T) {
	set := loadAhem(t)
	// Ahem states an underline at -133 with a thickness of 20, out of 1000
	// units. At 20px that is a band 0.4px thick whose top edge is 2.66px below
	// the baseline — against the fallback's 1px at 1.5px, which is what Courier
	// gives two tests above.
	built := Build(Input{
		HTML: `<div id="p">abcdef</div>`,
		CSS: []Stylesheet{{Source: noDefaults +
			`#p { font-family: Ahem; font-size: 20px; text-decoration: underline }`}},
	})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(10000)
	root := Layout(built.Root, Size{W: w, H: h}, set, rec)

	got := bands(Paint(root), black)
	if len(got) != 1 {
		t.Fatalf("an underlined word painted %d bands, want 1", len(got))
	}
	// 20 units of 1000 at 20px is 0.4px, which is 25.6 layout units and rounds
	// to 26 — a sixty-fourth over, and the figure is written out rather than
	// rounded off because a test that tolerated the difference would tolerate a
	// wrong thickness too.
	if h := got[0].H.Px(); h != 0.40625 {
		t.Errorf("the underline is %gpx thick, want 0.40625 (20/1000 em at 20px, "+
			"quantised); 1 means the face was ignored for the fallback", h)
	}
	baseline := baselineOfFirstRun(t, root, "p")
	if want := baseline.Add(mustPx(2.65625)); got[0].Y != want {
		t.Errorf("the underline's top edge is %gpx below the baseline, want 2.65625 "+
			"(133/1000 em at 20px, quantised); 1.5 means the fallback was used",
			got[0].Y.Sub(baseline).Px())
	}
}

// faceFrom loads a face from the fetched corpora, or skips.
func faceFrom(t *testing.T, env, rel string) FontSet {
	t.Helper()
	dir := os.Getenv(env)
	if dir == "" {
		t.Skipf("set %s for a face that states these metrics", env)
	}
	data, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Skipf("no %s: %v", rel, err)
	}
	face, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading %s: %v", rel, err)
	}
	return ahemSet{ahem: face, standard: StandardFonts()}
}

// decoBand lays out one decorated word against a font set and returns its band.
func decoBand(t *testing.T, set FontSet, decoration string) (Rect, style.Unit) {
	t.Helper()
	built := Build(Input{
		HTML: `<div id="p">abcdef</div>`,
		CSS: []Stylesheet{{Source: noDefaults +
			`#p { font-family: Ahem; font-size: 20px; text-decoration: ` + decoration + ` }`}},
	})
	rec := NewRecorder(nil)
	w, _ := style.FromPx(600)
	h, _ := style.FromPx(10000)
	root := Layout(built.Root, Size{W: w, H: h}, set, rec)
	got := bands(Paint(root), black)
	if len(got) != 1 {
		t.Fatalf("%s painted %d bands, want 1", decoration, len(got))
	}
	return got[0], baselineOfFirstRun(t, root, "p")
}

// TestLineThroughComesFromTheFaceThatStatesOne pins the strikeout half.
//
// It is separate from the underline because OS/2 states a strikeout position and
// size independently of post's underline, and 86 of the 88 faces in this
// checkout state both — so a line-through drawn at the underline's thickness is
// wrong for almost every real font, at exactly the right height, which reads as
// a choice rather than a mistake.
func TestLineThroughComesFromTheFaceThatStatesOne(t *testing.T) {
	set := faceFrom(t, "NOTO_FONTS", "NotoSans-Regular.ttf")
	band, baseline := decoBand(t, set, "line-through")

	// Noto Sans states a strikeout at 322 with a size of 50, out of 1000 units.
	// At 20px the band is 1px thick with its top 6.44px *above* the baseline.
	if h := band.H.Px(); h != 1 {
		t.Errorf("the line-through is %gpx thick, want 1 (50/1000 em at 20px)", h)
	}
	if above := baseline.Sub(band.Y).Px(); above != 6.4375 {
		t.Errorf("the line-through's top edge is %gpx above the baseline, want 6.4375 "+
			"(322/1000 em at 20px, quantised); about 5.86 means the x-height "+
			"estimate was used instead of the stated strikeout", above)
	}
}

// TestLineThroughFallsBackToTheXHeight is the other side, and it needs a face
// that states an x-height and no strikeout. Two of the eighty-eight do.
func TestLineThroughFallsBackToTheXHeight(t *testing.T) {
	set := faceFrom(t, "WPT_TESTS", "fonts/baseline-diagnostic/BaselineDiagnostic.ttf")
	band, baseline := decoBand(t, set, "line-through")

	// The face states an x-height of 250/1000 and no strikeout, so the line goes
	// through the middle of it: 20px x 0.25 / 2 = 2.5px above the baseline, less
	// half the band's own thickness. Reading half an em instead — the estimate
	// used before the face could be asked — would put it at 5px.
	above := baseline.Sub(band.Y).Px()
	if above < 2.4 || above > 3.1 {
		t.Errorf("the line-through's top edge is %gpx above the baseline; want about "+
			"2.5 to 3, half of the stated 0.25em x-height plus half a band. Near 5 "+
			"means the half-em estimate was used although the face stated one", above)
	}
}

// TestLineThroughUsesItsOwnThickness is the clause that says a strikeout is not
// an underline drawn higher up.
//
// It needs a face whose two sizes differ, and Noto Sans is not one — it states
// 50 for both, so a line-through drawn at the underline's thickness is right by
// coincidence there and the clause decides nothing. Fifteen faces in this
// checkout do differ, Ahem among them at 50 against 20, which is why this test
// uses it rather than the face the tests above use.
func TestLineThroughUsesItsOwnThickness(t *testing.T) {
	set := loadAhem(t)
	band, _ := decoBand(t, set, "line-through")
	// 50/1000 of an em at 20px is 1px. The underline's 20/1000 would be 0.4,
	// which quantises to 0.40625 — the figure the underline test asserts.
	if h := band.H.Px(); h != 1 {
		t.Errorf("the line-through is %gpx thick, want 1 (the stated strikeout size "+
			"of 50/1000 em); 0.40625 is the underline's thickness and means the "+
			"strikeout's own was ignored", h)
	}
}
