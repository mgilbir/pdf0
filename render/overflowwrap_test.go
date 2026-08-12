package render

import (
	"strings"
	"testing"
)

// overflow-wrap, CSS Text §5.5.
//
// Courier is 600/1000, so a character at 20px is 12px wide. Every expectation
// below is that arithmetic rather than a number taken from a run.

const owCSS = `#p { font-family: Courier; font-size: 20px; width: 48px }`

func TestOverflowWrapBreaksAWordWithNowhereToBreak(t *testing.T) {
	// "abcdefgh" is 96px in a 48px line and has no opportunity in it, so
	// normally it overflows as one line. break-word cuts it into four and four.
	if got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcdefgh</div>`, owCSS), "p"); len(got) != 1 {
		t.Fatalf("without overflow-wrap the word made %d lines %q, want 1 — this "+
			"test is not exercising what it claims to", len(got), got)
	}
	for _, value := range []string{"break-word", "anywhere"} {
		got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcdefgh</div>`,
			owCSS+` #p { overflow-wrap: `+value+` }`), "p")
		if len(got) != 2 || got[0] != "abcd" || got[1] != "efgh" {
			t.Errorf("overflow-wrap:%s split the word into %q, want [abcd efgh]", value, got)
		}
	}
}

// TestOverflowWrapIsOnlyALastResort is the whole of what separates it from
// break-all, and the half an implementation gets wrong by being too eager.
//
// §5.5 makes its opportunities exist only "if there are no otherwise-acceptable
// break points in the line". A line with a perfectly good space in it must break
// at the space, leaving the short word whole — even though cutting the word
// would fill the line more completely.
func TestOverflowWrapIsOnlyALastResort(t *testing.T) {
	// "ab cd" in four characters of room: "ab" then "cd", both whole. An
	// implementation that treated break-word as break-all would fit "ab c" on
	// the first line and leave "d" on the second.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">ab cd</div>`,
		owCSS+` #p { overflow-wrap: break-word }`), "p")
	if len(got) != 2 || got[0] != "ab" || got[1] != "cd" {
		t.Errorf("overflow-wrap broke %q, want [ab cd] — it took a break inside a "+
			"word when a space was available", got)
	}
}

// TestOverflowWrapBreaksTheWordItCannotFitAfterASpace is the other side: the
// long word does get broken, but only once it is what a line begins with.
func TestOverflowWrapBreaksTheWordItCannotFitAfterASpace(t *testing.T) {
	// "ab cdefghij" in four characters: line 1 is "ab", then the long word
	// begins line 2 and is cut every four characters — "cdef", "ghij".
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">ab cdefghij</div>`,
		owCSS+` #p { overflow-wrap: break-word }`), "p")
	want := []string{"ab", "cdef", "ghij"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %q", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q (all %q)", i, got[i], want[i], got)
		}
	}
}

func TestOverflowWrapKeepsAGraphemeClusterWhole(t *testing.T) {
	// The same rule as break-all's, and the same table: a cut may not separate a
	// letter from a mark that belongs to it.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">éééé</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 24px;
		      overflow-wrap: break-word }`), "p")
	if len(got) < 2 {
		t.Fatalf("the text did not wrap at all (%q); the test proves nothing", got)
	}
	for i, line := range got {
		for _, r := range line {
			if r == 0x0301 {
				t.Errorf("line %d is %q, which begins with a combining mark", i, line)
			}
			break
		}
	}
}

func TestWordWrapIsTheSameProperty(t *testing.T) {
	// word-wrap is the name the property shipped under and is a legal alias,
	// so a document using it must not be told it is unsupported *and* must get
	// the behaviour.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<div id="p">abcdefgh</div>`,
		CSS:  []Stylesheet{{Source: owCSS + ` #p { word-wrap: break-word }`}},
	})
	frag := Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, StandardFonts(), rec)
	for _, f := range append(built.Findings, rec.Findings()...) {
		if f.Property == "word-wrap" || f.Property == "overflow-wrap" {
			t.Errorf("word-wrap was reported: %s", f.Message)
		}
	}
	if got := len(find(t, frag, "p").Lines); got != 2 {
		t.Errorf("word-wrap:break-word made %d lines, want 2", got)
	}
}

func TestOverflowWrapDoesNotBreakASingleCluster(t *testing.T) {
	// A line too narrow for even one character cannot be helped: breaking a
	// cluster off would leave the rest overflowing anyway and lose nothing but
	// the reader's text. The word overflows and is reported, as it was before.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<div id="p">abcd</div>`,
		CSS: []Stylesheet{{Source: `#p { font-family: Courier; font-size: 20px;
		     width: 6px; overflow-wrap: break-word }`}},
	})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, StandardFonts(), rec)
	var reported bool
	for _, f := range rec.Findings() {
		if f.Rule == RuleUnbreakableOverflow {
			reported = true
		}
	}
	if !reported {
		t.Error("a word too wide for even one character was not reported as overflowing")
	}
}

// TestOverflowWrapReMeasuresBothHalves is the reason the split does not simply
// apportion the original width.
//
// A face may kern or ligate across the cut, so the two pieces need not add up to
// the whole. What has to be right is the width of the text that is drawn, and
// the only way to have it is to measure the pieces.
func TestOverflowWrapReMeasuresBothHalves(t *testing.T) {
	root := layoutOf(t, 600, `<div id="p">abcdefgh</div>`,
		owCSS+` #p { overflow-wrap: break-word }`)
	lines := find(t, root, "p").Lines
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for i, line := range lines {
		var w float64
		var text string
		for _, r := range line.Runs {
			w += r.Width.Px()
			text += r.Text
		}
		// Four characters of Courier at 20px is 4 x 12 = 48.
		if text != "" && w != 48 {
			t.Errorf("line %d %q measures %gpx, want 48 — the half was not "+
				"re-measured after the cut", i, text, w)
		}
	}
}

func TestOverflowWrapAcrossThreeLines(t *testing.T) {
	// A word long enough to need cutting twice, which is the case that shows
	// the cursor is carried rather than reset: the second line begins part-way
	// through the item and must itself be able to end part-way through it.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcdefghijkl</div>`,
		owCSS+` #p { overflow-wrap: break-word }`), "p")
	want := []string{"abcd", "efgh", "ijkl"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("a word cut twice gave %q, want %q", got, want)
	}
}

// TestOverflowWrapBreaksBetweenTwoItemsThatEachFit is the shape a first draft
// of this missed entirely, and it is the shape the suite tests.
//
// §5.5's condition is about the *line* having no acceptable break point, not
// about one item being wider than the line. "XXXX XX" under break-spaces cuts
// into three pieces — a word, a space, a word — each narrower than four
// characters; what does not fit is the run of them, because break-spaces puts
// no opportunity before a space. A version that fired only on an over-wide item
// left the line overflowing and moved nothing at all on the suite.
func TestOverflowWrapBreaksBetweenTwoItemsThatEachFit(t *testing.T) {
	// Four characters of Courier at 20px is 48px. "abcd ef" is 7 characters:
	// "abcd" fills the line exactly, the space would take it to five, and there
	// is no opportunity in front of the space. So the line ends after "abcd"
	// and the space goes to the next line, where it is data rather than
	// something to tidy away.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcd ef</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 48px;
		      white-space: break-spaces; overflow-wrap: break-word }`), "p")
	if len(got) != 2 || got[0] != "abcd" || got[1] != " ef" {
		t.Errorf("got %q, want [\"abcd\" \" ef\"] — the line was not ended in front "+
			"of a space it had no room for", got)
	}
}

// TestOverflowWrapDoesNotFireWhenTheLineHasASpace is that break-word does not
// become break-all: a line with an ordinary break point in it takes that one.
//
// It does not reach the rewind-target conjunct in the last-resort branch, which
// is what this comment used to claim. Both were measured by planting: disabling
// the whole rewind-to-the-last-opportunity branch, and dropping the conjunct,
// each leave this test green. What ends the line here is the plain break before
// a word that does not fit, several branches earlier — so this pins the rule and
// not the guard on it, which is a different and smaller statement.
func TestOverflowWrapDoesNotFireWhenTheLineHasASpace(t *testing.T) {
	// "ab cdef" in four characters. There *is* an acceptable break point — the
	// space — so the line ends there and "cdef" goes whole to the next line.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">ab cdef</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 48px;
		      overflow-wrap: break-word }`), "p")
	if len(got) != 2 || got[0] != "ab" || got[1] != "cdef" {
		t.Errorf("got %q, want [ab cdef] — a break was taken inside a word while "+
			"the line still had an acceptable break point", got)
	}
}

// TestAnywhereNarrowsAShrinkToFitBoxAndBreakWordDoesNot is the only difference
// between the two values, and it is invisible in a box whose width is declared.
//
// §5.5: break-word's opportunities "are not considered when calculating
// min-content intrinsic sizes", and anywhere's are. So a float squeezed into
// less room than its text needs stops at its widest *word* under break-word and
// narrows to the room it has under anywhere. Nothing in the suite distinguishes
// them, which is why this is asserted here rather than left to the reftests.
//
// The room has to be scarce for the difference to show at all, which is the
// first thing this test got wrong: shrink-to-fit is min(max(minimum, available),
// preferred), so a float with room to spare is its preferred width — the whole
// word — whatever its minimum is. Both values gave 96px and the test read that
// as the feature not working.
func TestAnywhereNarrowsAShrinkToFitBoxAndBreakWordDoesNot(t *testing.T) {
	// "abcdefgh" is eight characters of Courier at 20px: 96px as a word, 12px
	// as a character. The container is 40px, which is between the two.
	const doc = `<div id="outer"><div id="f">abcdefgh</div></div>`
	css := func(value string) string {
		return `#outer { width: 40px }
		        #f { float: left; font-family: Courier; font-size: 20px;
		             overflow-wrap: ` + value + ` }`
	}
	word := find(t, layoutOf(t, 600, doc, css("break-word")), "f").BorderRect.W.Px()
	room := find(t, layoutOf(t, 600, doc, css("anywhere")), "f").BorderRect.W.Px()

	if word != 96 {
		t.Errorf("break-word shrank the float to %gpx, want 96 — its minimum is "+
			"the whole word, so the float is that wide and overflows", word)
	}
	if room != 40 {
		t.Errorf("anywhere shrank the float to %gpx, want 40 — its minimum is one "+
			"character, so the float takes the room it has", room)
	}
}

// TestALineEndsAtItsLastOpportunityWhenTheNextThingCannotBeginOne is a bug
// found by asking why overflow-wrap's last-resort guard could not be observed.
//
// The guard says the last resort applies only where the line has no rewind
// target, which is §5.5's own wording. Removing it moved nothing, and the reason
// was not that the guard is unnecessary: it was that the rewind it names did not
// happen for anything but a space, so the case never arose. Text that begins no
// opportunity — the text after a </span>, which has none in front of it — simply
// overflowed.
func TestALineEndsAtItsLastOpportunityWhenTheNextThingCannotBeginOne(t *testing.T) {
	// "xy ab" is 60px of a 72px line and "cdefgh" is 72px more. There is no
	// opportunity between the span and the text after it, so the only place the
	// line can end is the space it already passed.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">xy <span>ab</span>cdefgh</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 72px }`), "p")
	if len(got) != 2 || got[0] != "xy" || got[1] != "abcdefgh" {
		t.Errorf("got %q, want [xy abcdefgh] — the line did not go back to the "+
			"space when the text after the span would not fit", got)
	}

	// And with overflow-wrap the word that is left is then broken on the line it
	// starts, which is the two rules composing: the rewind first, the last
	// resort only once there is nothing left to rewind to.
	got = lineTextsOf(t, layoutOf(t, 600, `<div id="p">xy <span>ab</span>cdefgh</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 72px;
		      overflow-wrap: break-word }`), "p")
	want := []string{"xy", "abcdef", "gh"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestOverflowWrapTakesTheLastCutAvailable is the top of the bisection's range.
//
// grapheme.Boundaries reports the boundaries *inside* a string and not the one
// at its end, so the largest cut it offers already leaves a cluster behind for
// the next line. Bisecting over one fewer than that — an easy off-by-one, since
// the slice is indexed at lo-1 — costs a character on every line whose fill ends
// at the last boundary, and only on those: a word cut anywhere earlier is
// unaffected, which is why the rest of this file does not notice.
func TestOverflowWrapTakesTheLastCutAvailable(t *testing.T) {
	// Three of the four characters fit, so the cut has to be the last boundary
	// there is. "abcd" in 36px of Courier at 20px is 48 against 36.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcd</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 36px;
		      overflow-wrap: break-word }`), "p")
	want := []string{"abc", "d"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %q, want %q — the fill stopped a cluster short of the "+
			"longest prefix that fits", got, want)
	}
}
