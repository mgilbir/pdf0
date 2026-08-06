package render

import (
	"testing"

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
