package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// Bidirectional text in layout: the direction and unicode-bidi properties, and
// the reordering of a line's runs.
//
// The algorithm itself is tested next door, against Unicode's own conformance
// data — 862,000 cases, which is a stronger statement about rules W2 and N0 than
// anything here could be. What these tests are about is the part that is this
// package's: which text ends up in the paragraph, where the formatting codes
// come from, and where the runs land.
//
// Every position below is arithmetic that can be read rather than a number
// recorded from a run. Courier is 600/1000 and monospaced, so at 20px every
// character — including the ones it has no glyph for, which are set at the width
// of a space — is 12px wide.

// hebrew is two Hebrew letters, and hebrew2 two more. They are spelled as
// escapes so that a reversed expectation cannot be mistaken for a correct one by
// an editor that renders the script, and so that this file reads the same in
// every terminal.
const (
	hebrewAB = "אב" // alef bet
	hebrewGD = "גד" // gimel dalet
)

const bidiCSS = `#p { font-family: Courier; font-size: 20px; width: 300px }`

// runsOf returns the first line's runs of an element.
func runsOf(t *testing.T, root *Fragment, id string) []TextRun {
	t.Helper()
	f := find(t, root, id)
	if len(f.Lines) == 0 {
		t.Fatalf("#%s has no lines", id)
	}
	return f.Lines[0].Runs
}

// runAt returns the run whose text is s, and fails if there is not exactly one.
func runAt(t *testing.T, runs []TextRun, s string) TextRun {
	t.Helper()
	var found *TextRun
	for i := range runs {
		if runs[i].Text == s {
			if found != nil {
				t.Fatalf("two runs read %q", s)
			}
			found = &runs[i]
		}
	}
	if found == nil {
		var all []string
		for _, r := range runs {
			all = append(all, r.Text)
		}
		t.Fatalf("no run reads %q; the line holds %q", s, all)
	}
	return *found
}

// TestRightToLeftRunsAreReordered is the reordering itself, on a line whose runs
// are all right-to-left.
//
// Two Hebrew words separated by a space: the second word is drawn first, the
// space in the middle, the first word last. A renderer that reversed the glyphs
// of each word and left the words where they were would put them in the order
// they were written, which is the failure this is about — the words would read
// backwards while every letter looked right.
func TestRightToLeftRunsAreReordered(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">`+hebrewAB+` `+hebrewGD+`</div>`, bidiCSS)
	runs := runsOf(t, root, "p")
	if len(runs) != 3 {
		t.Fatalf("the line has %d runs, want 3", len(runs))
	}

	// Two letters at 12px each, then a 12px space: the second word starts the
	// line at 0, the space is at 24, the first word at 36.
	if got := runAt(t, runs, hebrewGD).X.Px(); got != 0 {
		t.Errorf("the second word is at %gpx, want 0 — it is drawn first", got)
	}
	if got := runAt(t, runs, " ").X.Px(); got != 24 {
		t.Errorf("the space is at %gpx, want 24", got)
	}
	if got := runAt(t, runs, hebrewAB).X.Px(); got != 36 {
		t.Errorf("the first word is at %gpx, want 36 — it is drawn last", got)
	}

	// And the runs are still in the order they were written, which is what a
	// reader copying the text out of the page gets.
	if runs[0].Text != hebrewAB || runs[2].Text != hebrewGD {
		t.Errorf("the runs are in visual order (%q, %q); the slice is the document's "+
			"order and only the X is the page's", runs[0].Text, runs[2].Text)
	}
	if !runs[0].RTL {
		t.Error("a Hebrew run was not marked right-to-left, so its glyphs would be " +
			"drawn in the order the characters appear")
	}
}

// TestLatinInsideRightToLeftKeepsItsOrder is the case a reversal gets wrong.
//
// A Latin word inside a right-to-left paragraph moves with the sentence and is
// *not* itself reversed. Reversing the whole line would put the words in the
// right places and spell every one of them backwards.
func TestLatinInsideRightToLeftKeepsItsOrder(t *testing.T) {
	root := layoutOf(t, 600,
		`<div id="p" dir="rtl">`+hebrewAB+` abc `+hebrewGD+`</div>`, bidiCSS)
	runs := runsOf(t, root, "p")

	// Widths: 24, 12, 36, 12, 24 — 108 in a 300px line. The paragraph is
	// right-to-left, so with the initial "text-align: start" the line is flush
	// right and everything is offset by 192.
	const shift = 300 - 108
	for _, tc := range []struct {
		text string
		x    float64
		rtl  bool
	}{
		{hebrewGD, shift + 0, true},
		{"abc", shift + 36, false},
		{hebrewAB, shift + 84, true},
	} {
		run := runAt(t, runs, tc.text)
		if got := run.X.Px(); got != tc.x {
			t.Errorf("%q is at %gpx, want %g", tc.text, got, tc.x)
		}
		if run.RTL != tc.rtl {
			t.Errorf("%q is marked RTL=%v, want %v — its glyphs would be drawn in "+
				"the wrong order", tc.text, run.RTL, tc.rtl)
		}
	}
}

// TestNumbersInRightToLeftTextRunLeftToRight is rules W2 and I1, which are the
// part of the algorithm no heuristic reaches.
//
// A number written after Hebrew reads left to right — "12" and not "21" — while
// sitting inside a right-to-left run. It takes a level of its own, two above the
// paragraph's, which is what puts it in the right place *and* keeps its digits
// in the right order.
func TestNumbersInRightToLeftTextRunLeftToRight(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">`+hebrewAB+` 12</div>`, bidiCSS)
	runs := runsOf(t, root, "p")

	number := runAt(t, runs, "12")
	if number.RTL {
		t.Error(`the number was marked right-to-left, so "12" would be drawn "21"`)
	}
	// The whole phrase is right-to-left, so the number is at the left of it: 12,
	// then the space, then the word.
	if got := number.X.Px(); got != 0 {
		t.Errorf("the number is at %gpx, want 0", got)
	}
	if got := runAt(t, runs, hebrewAB).X.Px(); got != 36 {
		t.Errorf("the Hebrew word is at %gpx, want 36", got)
	}
}

// TestDirectionRTLAlignsFromTheRight is the effect of direction that most
// documents will actually see: the inline base direction is what "text-align:
// start" resolves against, and start is the initial value.
func TestDirectionRTLAlignsFromTheRight(t *testing.T) {
	// Six characters at 12px is 72px in a 300px line, so a right-to-left block
	// starts them at 228.
	root := layoutOf(t, 600, `<div id="p" dir="rtl">abcdef</div>`, bidiCSS)
	if got := lineX(t, root, "p"); got != 228 {
		t.Errorf("a right-to-left block put its first line at %gpx, want 228", got)
	}
	// "end" is the other one that flips, and "left" and "right" are physical and
	// must not.
	cases := map[string]float64{"end": 0, "left": 0, "right": 228, "start": 228}
	for value, want := range cases {
		root := layoutOf(t, 600, `<div id="p" dir="rtl">abcdef</div>`,
			bidiCSS+` #p { text-align: `+value+` }`)
		if got := lineX(t, root, "p"); got != want {
			t.Errorf("direction:rtl with text-align:%s put the line at %gpx, want %g",
				value, got, want)
		}
	}
}

// TestDirectionAloneOnAnInlineDoesNothing pins the rule everyone meets once: a
// direction on an inline box with the initial unicode-bidi has no effect at all.
//
// It is not an omission, it is the property. An inline box that changed the
// direction without opening an embedding would reorder text outside itself.
func TestDirectionAloneOnAnInlineDoesNothing(t *testing.T) {
	plain := layoutOf(t, 600, `<div id="p">abc def</div>`, bidiCSS)
	directed := layoutOf(t, 600,
		`<div id="p">abc <span style="direction: rtl">def</span></div>`, bidiCSS)
	for _, want := range runsOf(t, plain, "p") {
		got := runAt(t, runsOf(t, directed, "p"), want.Text)
		if got.X != want.X || got.RTL != want.RTL {
			t.Errorf("%q moved from %gpx to %gpx (RTL %v to %v) for a direction on an "+
				"inline with unicode-bidi:normal, which has no effect",
				want.Text, want.X.Px(), got.X.Px(), want.RTL, got.RTL)
		}
	}

	// The same declaration *with* an override does have an effect, which is what
	// says the test above is about the property and not about the declaration
	// being thrown away on the way in. An override is used rather than an
	// embedding because an embedding of Latin text is genuinely inert — it
	// raises the level by two and changes nothing visible — which would make a
	// weaker companion than none.
	overridden := layoutOf(t, 600,
		`<div id="p">abc <span style="direction: rtl; unicode-bidi: bidi-override">def</span></div>`,
		bidiCSS)
	if !runAt(t, runsOf(t, overridden, "p"), "def").RTL {
		t.Error("direction:rtl with unicode-bidi:bidi-override did not reach the run " +
			"either, so the test above proves nothing")
	}
}

// TestBidiOverrideForcesTheDirection is unicode-bidi: bidi-override, which is
// what <bdo> is.
func TestBidiOverrideForcesTheDirection(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">abc <bdo dir="rtl">def</bdo></div>`, bidiCSS)
	over := runAt(t, runsOf(t, root, "p"), "def")
	if !over.RTL {
		t.Error("<bdo dir=rtl> did not force its contents right-to-left, so \"def\" " +
			"would be drawn \"def\" rather than \"fed\"")
	}
	// The text around it is untouched: an override is not a paragraph
	// direction.
	if runAt(t, runsOf(t, root, "p"), "abc").RTL {
		t.Error("the text before the <bdo> was made right-to-left too")
	}
	if got := runAt(t, runsOf(t, root, "p"), "abc").X.Px(); got != 0 {
		t.Errorf("the text before the <bdo> is at %gpx, want 0", got)
	}
}

// TestIsolateKeepsTheContentsOutOfTheSurroundings is unicode-bidi: isolate, and
// the difference between it and an embedding is the whole reason it exists.
//
// Rule P2 decides a paragraph's direction from its first strong character and
// *skips over an isolate entirely* while looking. So a Hebrew phrase at the start
// of an otherwise English sentence sets the whole sentence right to left when it
// is embedded, and does not when it is isolated. That is the bug isolates were
// added to Unicode to fix, and it is the one an author hits when they interpolate
// a user's name into a sentence.
func TestIsolateKeepsTheContentsOutOfTheSurroundings(t *testing.T) {
	// The block takes its direction from its contents, so P2 is what decides,
	// and the two spans differ only in unicode-bidi.
	const doc = `<div id="p" style="unicode-bidi: plaintext">` +
		`<span style="%s">` + hebrewAB + `</span> abc</div>`

	isolated := layoutOf(t, 600, strings.Replace(doc, "%s", "unicode-bidi: isolate", 1), bidiCSS)
	if got := runAt(t, runsOf(t, isolated, "p"), "abc").X.Px(); got != 36 {
		t.Errorf("with an isolate the English is at %gpx, want 36 — the isolated "+
			"Hebrew must not decide the direction of the sentence around it", got)
	}

	embedded := layoutOf(t, 600,
		strings.Replace(doc, "%s", "direction: rtl; unicode-bidi: embed", 1), bidiCSS)
	// An embedding is visible to rule P2, so the paragraph is right-to-left: the
	// three runs are 24 + 12 + 36 = 72px wide, reordered, and flush right.
	if got := runAt(t, runsOf(t, embedded, "p"), "abc").X.Px(); got != 300-72 {
		t.Errorf("with an embedding the English is at %gpx, want %g — the embedded "+
			"Hebrew decides the paragraph's direction, which is exactly what an "+
			"isolate prevents and what makes the two values different",
			got, float64(300-72))
	}
}

// TestForcedBreakStartsANewBidiParagraph pins CSS's paragraph split.
//
// The algorithm's first rule resolves each paragraph on its own, and a forced
// line break ends one. Without the split the first strong character of the block
// would decide the direction of every line in it — so a <br> between a Hebrew
// line and a Latin one would set the Latin one right-to-left.
func TestForcedBreakStartsANewBidiParagraph(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p" style="unicode-bidi: plaintext">`+
		hebrewAB+`<br>abc</div>`, bidiCSS)
	f := find(t, root, "p")
	if len(f.Lines) != 2 {
		t.Fatalf("the block produced %d lines, want 2", len(f.Lines))
	}
	// The first paragraph is Hebrew, so it is right-to-left and flush right;
	// the second is Latin and starts at the left. Under one paragraph for the
	// whole block, the second line would be right-to-left as well.
	if got := f.Lines[0].Runs[0].X.Px(); got != 300-24 {
		t.Errorf("the Hebrew line is at %gpx, want %g", got, float64(300-24))
	}
	if got := f.Lines[1].Runs[0].X.Px(); got != 0 {
		t.Errorf("the Latin line after the break is at %gpx, want 0 — the forced "+
			"break did not start a new bidi paragraph", got)
	}
}

// TestPlaintextTakesTheDirectionFromTheText is unicode-bidi: plaintext, which
// is what <bdi> and dir=auto are for: content whose language the author does not
// know.
func TestPlaintextTakesTheDirectionFromTheText(t *testing.T) {
	// The same declaration over two different contents, and the direction
	// follows the content rather than the declaration.
	hebrew := layoutOf(t, 600, `<div id="p" dir="auto">`+hebrewAB+`</div>`, bidiCSS)
	if got := lineX(t, hebrew, "p"); got != 300-24 {
		t.Errorf("dir=auto over Hebrew put the line at %gpx, want %g", got, float64(300-24))
	}
	latin := layoutOf(t, 600, `<div id="p" dir="auto">abcdef</div>`, bidiCSS)
	if got := lineX(t, latin, "p"); got != 0 {
		t.Errorf("dir=auto over Latin put the line at %gpx, want 0", got)
	}
}

// TestEachLineIsReorderedOnItsOwn is why the reordering is per line rather than
// per paragraph. A paragraph broken across two lines has each of them reordered
// against its own extent, and rule L1 resets the space the break happened at.
func TestEachLineIsReorderedOnItsOwn(t *testing.T) {
	// Three Hebrew words in a box wide enough for two of them: 2 + 1 + 2 + 1 + 2
	// characters, so 72px holds "AB CD" and pushes "EF" to the next line.
	const three = hebrewAB + " " + hebrewGD + " הו"
	root := layoutOf(t, 600, `<div id="p">`+three+`</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 72px }`)
	f := find(t, root, "p")
	if len(f.Lines) != 2 {
		t.Fatalf("the block produced %d lines, want 2", len(f.Lines))
	}
	// First line: the second word is drawn first and the first word last, within
	// this line's own 60px of content.
	first := f.Lines[0].Runs
	if got := runAt(t, first, hebrewGD).X.Px(); got != 0 {
		t.Errorf("on the first line the second word is at %gpx, want 0", got)
	}
	if got := runAt(t, first, hebrewAB).X.Px(); got != 36 {
		t.Errorf("on the first line the first word is at %gpx, want 36", got)
	}
	// Second line: one word, at the start of the line.
	if got := f.Lines[1].Runs[0].X.Px(); got != 0 {
		t.Errorf("the second line starts at %gpx, want 0", got)
	}
}

// TestOverConstrainedMarginsFollowTheContainingBlock is CSS 2.1 §10.3.3's other
// half: which margin gives way when the arithmetic does not add up depends on
// the containing block's direction.
func TestOverConstrainedMarginsFollowTheContainingBlock(t *testing.T) {
	// The body's own margin is taken out so that the numbers below are the
	// arithmetic of §10.3.3 and nothing else.
	const css = `body { margin: 0 } #outer { width: 300px } ` +
		`#inner { width: 100px; margin-left: 10px; margin-right: 10px }`
	inner := func(dir string) float64 {
		root := layoutOf(t, 600,
			`<div id="outer" dir="`+dir+`"><div id="inner"></div></div>`, css)
		return find(t, root, "inner").BorderRect.X.Px()
	}
	// 100 + 10 + 10 is not 300, so one margin is ignored. Left to right, it is
	// the right one and the box stays 10px from the left edge; right to left, it
	// is the left one and the box sits 10px from the right: 300 - 10 - 100.
	if got := inner("ltr"); got != 10 {
		t.Errorf("in a left-to-right containing block the box is at %gpx, want 10", got)
	}
	if got := inner("rtl"); got != 190 {
		t.Errorf("in a right-to-left containing block the box is at %gpx, want 190 — "+
			"§10.3.3 ignores the left margin there, not the right", got)
	}
}

// TestBidiIsNotReportedUnsupported is the other half of the claim the
// unsupported-script guardrail used to make. Right-to-left text is laid out now,
// so reporting it would be telling an author a gap has not been closed while
// they look at the thing that closed it.
func TestBidiIsNotReportedUnsupported(t *testing.T) {
	for _, text := range []string{hebrewAB, "مرحبا"} {
		got := Build(Input{HTML: "<p>" + text + "</p>"})
		rec := NewRecorder(nil)
		w, _ := style.FromPx(1000)
		Layout(got.Root, Size{W: w, H: w}, nil, rec)
		for _, f := range rec.Findings() {
			if f.Rule == RuleUnsupportedScript {
				t.Errorf("%q was reported as an unsupported script: %v", text, f)
			}
		}
	}
	// And the two properties are no longer reported as unimplemented, which is
	// the finding that used to taint every document declaring one.
	got := Build(Input{
		HTML: `<p style="direction: rtl; unicode-bidi: bidi-override">x</p>`,
	})
	for _, f := range got.Findings {
		if f.Rule == RuleUnsupportedProperty &&
			(f.Property == "direction" || f.Property == "unicode-bidi") {
			t.Errorf("%s is still reported as unimplemented: %v", f.Property, f)
		}
	}
}

// TestBidiControlsInTheTextAreHonoured pins that the algorithm sees the codes an
// author wrote as well as the ones unicode-bidi stands for. U+202E is a
// right-to-left override, and a document using one expects it to work.
func TestBidiControlsInTheTextAreHonoured(t *testing.T) {
	root := layoutOf(t, 600, "<div id=\"p\">abc‮def‬</div>", bidiCSS)
	runs := runsOf(t, root, "p")
	var over *TextRun
	for i := range runs {
		if strings.Contains(runs[i].Text, "def") {
			over = &runs[i]
		}
	}
	if over == nil {
		t.Fatalf("no run holds the overridden text; the line holds %d runs", len(runs))
	}
	if !over.RTL {
		t.Error("a U+202E in the document text did not make the run after it " +
			"right-to-left")
	}
}

// TestPlainLatinIsUntouched is the regression guard the whole feature needs: a
// document with nothing bidirectional in it must lay out exactly as it did
// before any of this existed, and must not pay for the algorithm either.
func TestPlainLatinIsUntouched(t *testing.T) {
	root := layoutOf(t, 600,
		`<div id="p">The quick brown fox jumps over the lazy dog</div>`, bidiCSS)
	f := find(t, root, "p")
	var x style.Unit
	for _, line := range f.Lines {
		for _, run := range line.Runs {
			if run.X != x {
				t.Fatalf("a run of plain Latin text is at %gpx, want %gpx — the pen "+
					"position is no longer the sum of the runs before it",
					run.X.Px(), x.Px())
			}
			if run.RTL {
				t.Errorf("the Latin run %q was marked right-to-left", run.Text)
			}
			x = x.Add(run.Width)
		}
		x = 0
	}
}
