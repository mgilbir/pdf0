package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// letter-spacing, word-spacing and text-indent.
//
// Courier advances 600/1000, so at 20px every character is 12px and a run of n
// of them is 12n px. Every expected number below is that arithmetic plus the
// spacing the declaration asked for, so none of them could be recorded from a
// wrong implementation and still be right.

const spaceCSS = `#p { font-family: Courier; font-size: 20px; width: 400px }`

// runWidths is the width of every run of the first line, which is what the
// spacing properties change.
func runWidths(t *testing.T, root *Fragment, id string) []float64 {
	t.Helper()
	f := find(t, root, id)
	if len(f.Lines) == 0 {
		t.Fatalf("#%s has no lines", id)
	}
	var out []float64
	for _, r := range f.Lines[0].Runs {
		out = append(out, r.Width.Px())
	}
	return out
}

func TestLetterSpacingWidensEveryCharacter(t *testing.T) {
	// "abc" is three characters: 36px of Courier plus 3 x 5px of spacing. CSS
	// Text adds the spacing after the last character too, which is why it is
	// three and not two.
	root := layoutOf(t, 600, `<div id="p">abc def</div>`,
		noDefaults+spaceCSS+` #p { letter-spacing: 5px }`)
	got := runWidths(t, root, "p")
	if len(got) != 3 {
		t.Fatalf("the line has %d runs, want 3 — a word, a space and a word", len(got))
	}
	if got[0] != 51 {
		t.Errorf("\"abc\" with 5px letter-spacing is %gpx, want 51 (36 + 3 x 5)", got[0])
	}
	if got[1] != 17 {
		t.Errorf("the space is %gpx, want 17 (12 + 5)", got[1])
	}
}

func TestWordSpacingWidensOnlyTheSpaces(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">abc def</div>`,
		noDefaults+spaceCSS+` #p { word-spacing: 10px }`)
	got := runWidths(t, root, "p")
	if len(got) != 3 {
		t.Fatalf("the line has %d runs, want 3", len(got))
	}
	if got[0] != 36 {
		t.Errorf("\"abc\" is %gpx with word-spacing set, want 36 — word-spacing "+
			"does not touch the letters", got[0])
	}
	if got[1] != 22 {
		t.Errorf("the space is %gpx, want 22 (12 + 10)", got[1])
	}
}

func TestLetterSpacingChangesWhereTheLineBreaks(t *testing.T) {
	// The measurement is what breaking reads, so spacing that only reached
	// painting would leave both words on one line and draw them overflowing it.
	//
	// "abcde abcde" is 60 + 12 + 60 = 132px in a 150px box: one line. With 4px of
	// letter-spacing it is 80 + 16 + 80 = 176px: two.
	narrow := noDefaults + `#p { font-family: Courier; font-size: 20px; width: 150px }`
	plain := layoutOf(t, 600, `<div id="p">abcde abcde</div>`, narrow)
	if n := len(find(t, plain, "p").Lines); n != 1 {
		t.Fatalf("without spacing the text took %d lines, want 1; the document "+
			"does not test what it means to", n)
	}
	spaced := layoutOf(t, 600, `<div id="p">abcde abcde</div>`,
		narrow+` #p { letter-spacing: 4px }`)
	if n := len(find(t, spaced, "p").Lines); n != 2 {
		t.Errorf("with 4px of letter-spacing the text took %d lines, want 2 — the "+
			"spacing was not measured", n)
	}
}

func TestSpacingWidensAnIntrinsicWidth(t *testing.T) {
	// A float is sized shrink-to-fit, which is measured from the content. Spacing
	// that reached line breaking but not the intrinsic widths would give a float
	// exactly the width its text overflows by.
	//
	// "abcde" is 60px; with 6px of letter-spacing it is 90px.
	root := layoutOf(t, 600, `<div><div id="f">abcde</div></div>`,
		noDefaults+`#f { float: left; font-family: Courier; font-size: 20px;
			letter-spacing: 6px }`)
	px(t, "a float around letter-spaced text", find(t, root, "f").BorderRect.W, 90)
}

func TestSpacingDoesNotShareAMeasurementWithAnUnspacedBox(t *testing.T) {
	// The memoization bug this cache has had once before, for the "ch" unit: two
	// boxes with the same face, size and text and different spacing must not
	// share an answer. Whichever is measured first would otherwise decide for
	// both, and the document that shows it is one with two values in it.
	root := layoutOf(t, 600,
		`<div id="a">abcde</div><div id="b">abcde</div>`,
		noDefaults+`div { font-family: Courier; font-size: 20px }
		 #b { letter-spacing: 8px }`)
	plain := runWidths(t, root, "a")
	spaced := runWidths(t, root, "b")
	if plain[0] != 60 {
		t.Errorf("the unspaced run is %gpx, want 60", plain[0])
	}
	if spaced[0] != 100 {
		t.Errorf("the spaced run is %gpx, want 100 (60 + 5 x 8); it shared the "+
			"unspaced box's cached measurement", spaced[0])
	}
}

func TestLetterSpacingReachesTheDrawing(t *testing.T) {
	// The width was spent by layout, so the glyphs have to be drawn spread to
	// match. A run drawn without the spacing would sit bunched at the left of a
	// gap the right size.
	ops := paintOf(t, `<div id="p">abc</div>`,
		noDefaults+spaceCSS+` #p { letter-spacing: 5px }`)
	for _, op := range ops {
		if d, ok := op.(DrawText); ok && d.Text == "abc" {
			if got := d.CharSpacing.Px(); got != 5 {
				t.Errorf("the run was drawn with %gpx of character spacing, want 5", got)
			}
			return
		}
	}
	t.Fatal("the run was not drawn at all")
}

func TestNegativeLetterSpacingNarrowsTheText(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">abcde</div>`,
		noDefaults+spaceCSS+` #p { letter-spacing: -2px }`)
	if got := runWidths(t, root, "p")[0]; got != 50 {
		t.Errorf("\"abcde\" at -2px letter-spacing is %gpx, want 50 (60 - 5 x 2)", got)
	}
}

func TestHugeLetterSpacingSaturates(t *testing.T) {
	// A hostile stylesheet must not be able to wrap the arithmetic into a
	// negative width, which does not look like an error — it looks like a box
	// laid out inside-out.
	root := layoutOf(t, 600, `<div id="p">abcde</div>`,
		noDefaults+spaceCSS+` #p { letter-spacing: 100000000px }`)
	if got := runWidths(t, root, "p")[0]; got <= 0 {
		t.Errorf("an enormous letter-spacing produced a run %gpx wide", got)
	}
}

func TestTextIndentMovesTheFirstLineOnly(t *testing.T) {
	// §16.1. The second line starts at the block's own edge, which is what makes
	// this an indent rather than a margin.
	root := layoutOf(t, 600, `<div id="p">abcde abcde</div>`,
		noDefaults+`#p { font-family: Courier; font-size: 20px; width: 150px;
			text-indent: 30px }`)
	f := find(t, root, "p")
	if len(f.Lines) != 2 {
		t.Fatalf("the text took %d lines, want 2", len(f.Lines))
	}
	if got := f.Lines[0].Runs[0].X.Px(); got != 30 {
		t.Errorf("the first line starts at %gpx, want 30", got)
	}
	if got := f.Lines[1].Runs[0].X.Px(); got != 0 {
		t.Errorf("the second line starts at %gpx, want 0 — only the first is indented", got)
	}
}

func TestTextIndentShortensTheFirstLine(t *testing.T) {
	// The indent takes room from the line rather than being added beside it, so
	// text that fitted before the indent may not fit after it. "abcde abcde" is
	// 132px and the box is 150px wide: one line. With a 40px indent the first
	// line has 110px, so the second word moves down.
	plain := layoutOf(t, 600, `<div id="p">abcde abcde</div>`,
		noDefaults+`#p { font-family: Courier; font-size: 20px; width: 150px }`)
	if n := len(find(t, plain, "p").Lines); n != 1 {
		t.Fatalf("without an indent the text took %d lines, want 1", n)
	}
	indented := layoutOf(t, 600, `<div id="p">abcde abcde</div>`,
		noDefaults+`#p { font-family: Courier; font-size: 20px; width: 150px;
			text-indent: 40px }`)
	if n := len(find(t, indented, "p").Lines); n != 2 {
		t.Errorf("with a 40px indent the text took %d lines, want 2 — the indent "+
			"was drawn but not measured", n)
	}
}

func TestTextIndentIsInheritedAndTakesAPercentage(t *testing.T) {
	// It inherits, and a percentage is of the containing block's width: 10% of
	// the 200px block is 20px.
	root := layoutOf(t, 600, `<div id="o"><div id="p">abc</div></div>`,
		noDefaults+`#o { text-indent: 10% } #p { font-family: Courier; font-size: 20px;
			width: 200px }`)
	if got := find(t, root, "p").Lines[0].Runs[0].X.Px(); got != 20 {
		t.Errorf("an inherited 10%% indent put the first line at %gpx, want 20", got)
	}
}

func TestNegativeTextIndentHangs(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">abc</div>`,
		noDefaults+`#p { font-family: Courier; font-size: 20px; width: 200px;
			text-indent: -12px }`)
	if got := find(t, root, "p").Lines[0].Runs[0].X.Px(); got != -12 {
		t.Errorf("a hanging indent put the first line at %gpx, want -12", got)
	}
}

func TestTextIndentComposesWithTextAlign(t *testing.T) {
	// The content of an indented first line is aligned within what the indent
	// left, not within the whole line. "abc" is 36px; a 200px line with a 40px
	// indent leaves 160, and centring 36 in 160 puts it 62 past the indent.
	root := layoutOf(t, 600, `<div id="p">abc</div>`,
		noDefaults+`#p { font-family: Courier; font-size: 20px; width: 200px;
			text-indent: 40px; text-align: center }`)
	if got := find(t, root, "p").Lines[0].Runs[0].X.Px(); got != 102 {
		t.Errorf("a centred indented line starts at %gpx, want 102 (40 + (160-36)/2)", got)
	}
}

func TestTextIndentWidensAnIntrinsicWidth(t *testing.T) {
	// A shrink-to-fit box has to be wide enough for its indented first line.
	// "abc" is 36px and the indent is 30px.
	root := layoutOf(t, 600, `<div><div id="f">abc</div></div>`,
		noDefaults+`#f { float: left; font-family: Courier; font-size: 20px;
			text-indent: 30px }`)
	px(t, "a float around indented text", find(t, root, "f").BorderRect.W, 66)
}

func TestUnresolvableTextIndentIsReported(t *testing.T) {
	// "hanging" changes *which* lines are indented rather than by how much, so
	// reading it as a length would indent the wrong ones. It is refused and said
	// so rather than guessed at.
	// The pair of documents makes "once" mean the suppression rather than "there
	// was one element". Two elements naming the same unusable value produce one
	// finding; two naming different ones produce two, which is what shows both
	// were visited.
	if got := indentFindings(t,
		`<div class="p">abc</div><p class="p">def</p>`,
		`.p { text-indent: 2em hanging }`); got != 1 {
		t.Errorf("one unusable indent on two elements was reported %d times, want once", got)
	}
	if got := indentFindings(t,
		`<div id="a">abc</div><p id="b">def</p>`,
		`#a { text-indent: 2em hanging } #b { text-indent: 3em hanging }`); got != 2 {
		t.Errorf("two different unusable indents were reported %d times, want twice — "+
			"without two the document above proves nothing about the suppression", got)
	}
}

func indentFindings(t *testing.T, htmlSrc, cssSrc string) int {
	t.Helper()
	rec := NewRecorder(nil)
	built := Build(Input{HTML: htmlSrc, CSS: []Stylesheet{{Source: cssSrc}}})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, nil, rec)

	n := 0
	for _, f := range rec.Findings() {
		if f.Property == "text-indent" {
			n++
		}
	}
	return n
}

func TestWordSpacingCountsTheNoBreakSpace(t *testing.T) {
	// CSS Text's word-separator characters are more than U+0020, and the
	// no-break space is the one an author actually writes. It is not white space
	// for the purpose of collapsing, so it stays inside the word's run — which is
	// exactly why counting it needs its own walk rather than falling out of the
	// run being a space.
	root := layoutOf(t, 600, `<div id="p">a&#160;b</div>`,
		noDefaults+spaceCSS+` #p { word-spacing: 10px }`)
	if got := runWidths(t, root, "p")[0]; got != 46 {
		t.Errorf("\"a\\u00a0b\" with 10px word-spacing is %gpx, want 46 (3 x 12 + 10)", got)
	}
}

func TestSpacingNormalIsNoSpacingAtAll(t *testing.T) {
	// The initial value of both, spelled out, must produce the face's own
	// advances — a version that read "normal" as an unresolvable length and fell
	// through to zero would agree, so this is checked against the arithmetic
	// rather than against the default.
	root := layoutOf(t, 600, `<div id="p">abcde</div>`,
		noDefaults+spaceCSS+` #p { letter-spacing: normal; word-spacing: normal }`)
	if got := runWidths(t, root, "p")[0]; got != 60 {
		t.Errorf("\"abcde\" at normal spacing is %gpx, want 60", got)
	}
}

// TestSpacingKeyIsPartOfTheMeasurementCache is the same guard as the box test
// above, made directly against the cache so that a future refactor that stops
// going through layout still trips it.
func TestSpacingKeyIsPartOfTheMeasurementCache(t *testing.T) {
	l := &layouter{
		measured: map[measureKey]style.Unit{},
		fonts:    map[fontKey]resolvedFont{},
		fontSet:  StandardFonts(),
		rec:      NewRecorder(nil),
	}
	face, ok := l.fontSet.Face("Courier", false, false)
	if !ok {
		t.Fatal("no Courier")
	}
	size := mustPx(20)
	plain := l.measureSpaced(face, "abcde", size, textSpacing{})
	spaced := l.measureSpaced(face, "abcde", size, textSpacing{letter: mustPx(3)})
	if plain.Px() != 60 {
		t.Errorf("the unspaced measurement is %gpx, want 60", plain.Px())
	}
	if spaced.Px() != 75 {
		t.Errorf("the spaced measurement is %gpx, want 75; the spacing is not part "+
			"of the cache key", spaced.Px())
	}
}

func TestDecorationSpansTheSpacedRun(t *testing.T) {
	// An underline is as wide as the run, and the run is as wide as the spacing
	// made it — so the two have to have been computed from the same number.
	ops := paintOf(t, `<div id="p">abc</div>`,
		noDefaults+spaceCSS+` #p { letter-spacing: 5px; text-decoration: underline;
			color: #000000 }`)
	got := bands(ops, black)
	if len(got) != 1 {
		t.Fatalf("%d underline bands were painted, want 1", len(got))
	}
	if w := got[0].W.Px(); w != 51 {
		t.Errorf("the underline is %gpx wide, want 51 — the width of the spaced run", w)
	}
}

// TestTabTakesLetterSpacing pins the one character whose advance is not
// measured: a tab's width comes from the tab stops, and the spacing after it
// still has to be counted or the run after a tab is drawn to the right of where
// layout put it.
func TestTabTakesLetterSpacing(t *testing.T) {
	root := layoutOf(t, 600, "<div id=\"p\">a\tb</div>",
		noDefaults+spaceCSS+` #p { white-space: pre; letter-spacing: 5px; tab-size: 4 }`)
	f := find(t, root, "p")
	var tab *TextRun
	for i := range f.Lines[0].Runs {
		if strings.Contains(f.Lines[0].Runs[i].Text, "\t") {
			tab = &f.Lines[0].Runs[i]
		}
	}
	if tab == nil {
		t.Fatal("the preserved tab produced no run")
	}
	// The tab stop is four space advances: 48px. The "a" before it took 12 + 5 =
	// 17px, so the tab advances to 48 — 31px — and takes 5px of spacing after it.
	if got := tab.Width.Px(); got != 36 {
		t.Errorf("the tab is %gpx wide, want 36 (48 - 17 + 5)", got)
	}
}
