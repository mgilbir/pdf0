package render

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/grapheme"
)

// word-break: break-all, CSS Text §5.2.
//
// Every expectation below is arithmetic that can be read: Courier is 600/1000,
// so a character at 20px is 12px and a line of n characters is 12n.

// lineTextsOf returns the text of each line of an element, joined per line.
func lineTextsOf(t *testing.T, root *Fragment, id string) []string {
	t.Helper()
	var out []string
	for _, line := range find(t, root, id).Lines {
		var b strings.Builder
		for _, r := range line.Runs {
			b.WriteString(r.Text)
		}
		out = append(out, b.String())
	}
	return out
}

func TestBreakAllCutsInsideAWord(t *testing.T) {
	// "abcdefgh" is one word of 96px in a 48px line. Without break-all there is
	// nowhere to cut it, so it overflows as one line; with break-all it becomes
	// four characters and four.
	const css = `#p { font-family: Courier; font-size: 20px; width: 48px }`

	if got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcdefgh</div>`, css), "p"); len(got) != 1 {
		t.Fatalf("without break-all the word made %d lines %q, want 1 — this test "+
			"is not exercising what it claims to", len(got), got)
	}
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">abcdefgh</div>`,
		css+` #p { word-break: break-all }`), "p")
	if len(got) != 2 || got[0] != "abcd" || got[1] != "efgh" {
		t.Errorf("break-all split the word into %q, want [abcd efgh]", got)
	}
}

// TestBreakAllDoesNotBreakBeforeASpace is UAX #14's LB7, which break-all does
// not lift — and it is the rule that decides the suite's fixtures rather than a
// detail.
//
// A line may not end between a word and the space after it: the space belongs to
// the line the word is on. An implementation that offered an opportunity at
// every character boundary without qualification would put the break there,
// because that position fits more of the text.
func TestBreakAllDoesNotBreakBeforeASpace(t *testing.T) {
	// The suite's own fixture, in Courier. "X XX X" in four characters of room:
	// the positions a line may end at are after the first space (2 characters),
	// between the two X's of the middle word (3), and after the second space
	// (5). Four characters of room takes the longest that fits, which is three
	// — *not* "X XX", because ending there would mean breaking between the
	// fourth character and the space after it.
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">X XX X</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 48px;
		      white-space: break-spaces; word-break: break-all }`), "p")
	if len(got) != 2 || got[0] != "X X" || got[1] != "X X" {
		t.Errorf("break-all broke %q, want [\"X X\" \"X X\"] — an opportunity was "+
			"taken before a space, which LB7 forbids", got)
	}
}

// TestBreakAllKeepsAGraphemeClusterWhole is the reason this needed a Unicode
// table rather than a loop over runes.
//
// A cluster is the typographic character unit CSS Text §2 defines a soft wrap
// opportunity to fall *between*, so cutting inside one is not an approximation
// of the rule — it corrupts the text it was asked to fit, by separating a letter
// from the mark that belongs to it.
func TestBreakAllKeepsAGraphemeClusterWhole(t *testing.T) {
	// "e" with a combining acute, four times, in a line two characters wide.
	// Courier has no combining acute of its own width, but the split is decided
	// before any face is consulted: what is asserted is that no line begins with
	// a combining mark.
	const combining = "éééé"
	got := lineTextsOf(t, layoutOf(t, 600, `<div id="p">`+combining+`</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 24px;
		      word-break: break-all }`), "p")
	if len(got) < 2 {
		t.Fatalf("the text did not wrap at all (%q); the test proves nothing", got)
	}
	for i, line := range got {
		for _, r := range line {
			if r == 0x0301 {
				t.Errorf("line %d is %q, which begins with a combining mark — "+
					"break-all cut inside a grapheme cluster", i, line)
			}
			break
		}
	}
}

func TestBreakAllIsInherited(t *testing.T) {
	// The property inherits, which is what makes a rule on a container reach
	// the text inside it.
	got := lineTextsOf(t, layoutOf(t, 600,
		`<div id="outer"><div id="p">abcdefgh</div></div>`,
		`#p { font-family: Courier; font-size: 20px; width: 48px }
		 #outer { word-break: break-all }`), "p")
	if len(got) != 2 {
		t.Errorf("an inherited break-all made %d lines %q, want 2", len(got), got)
	}
}

func TestKeepAllIsReported(t *testing.T) {
	// keep-all is not implemented and is read as normal, which allows a break
	// the value forbids. Applying it silently is the kind of wrong page that
	// looks deliberate.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<div id="p">日本語のテキスト</div>`,
		CSS:  []Stylesheet{{Source: `#p { width: 40px; word-break: keep-all }`}},
	})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, StandardFonts(), rec)

	var found int
	for _, f := range rec.Findings() {
		if f.Property == "word-break" && strings.Contains(f.Message, "keep-all") {
			found++
		}
	}
	if found == 0 {
		t.Error("word-break:keep-all was read as normal silently")
	}
	if found > 1 {
		t.Errorf("it was reported %d times; once is enough", found)
	}
}

func TestBreakAllIsNotReportedAsUnsupported(t *testing.T) {
	// The value this engine does implement must not be reported, or every
	// document using it is permanently tainted.
	rec := NewRecorder(nil)
	built := Build(Input{
		HTML: `<div id="p">abcdefgh</div>`,
		CSS:  []Stylesheet{{Source: `#p { width: 40px; word-break: break-all }`}},
	})
	Layout(built.Root, Size{W: picPx(600), H: picPx(10000)}, StandardFonts(), rec)
	for _, f := range rec.Findings() {
		if f.Property == "word-break" {
			t.Errorf("break-all was reported as unsupported: %s", f.Message)
		}
	}
}

// TestSplitAtBreaksCutsAtClusterBoundariesOnly checks the split directly, over
// text whose clustering is not in doubt, because the layout tests above can only
// see the boundaries a particular width happens to land on.
func TestSplitAtBreaksCutsAtClusterBoundariesOnly(t *testing.T) {
	cases := []string{
		"abcd",
		"éé",                  // combining marks
		"\U0001f1e6\U0001f1e7",  // a flag: two regional indicators, one cluster
		"\U0001f468‍\U0001f469", // an emoji ZWJ sequence
		"क्क",                   // a Devanagari conjunct across its virama
		"각",                    // conjoining Hangul
		"a b́c",                 // a space among them
	}
	for _, text := range cases {
		pieces, _ := splitAtBreaks(text, whiteSpaceOf("collapse"), wordBreak{breakAll: true}, lineBreak{})

		// Every cut must fall on a boundary the segmenter agrees with, and the
		// pieces must still spell the text.
		allowed := map[int]bool{0: true, len(text): true}
		for _, b := range grapheme.Boundaries(nil, text) {
			allowed[b] = true
		}
		at := 0
		var rebuilt strings.Builder
		for _, p := range pieces {
			if !allowed[at] {
				t.Errorf("%q was cut at byte %d, which is inside a grapheme cluster", text, at)
			}
			at += len(p.text)
			rebuilt.WriteString(p.text)
		}
		if rebuilt.String() != text {
			t.Errorf("the pieces of %q spell %q", text, rebuilt.String())
		}
	}
}

// TestAHangulSyllableIsNotCutFromItsJamo is a bug this machinery found rather
// than a feature it added, and it needs no word-break at all.
//
// The ideograph rule offered a break on both sides of everything in the Hangul
// syllables block. A syllable written as a precomposed LV followed by its own
// trailing consonant is two characters and one syllable — U+AC00 and U+11A8 are
// "가" and the "ᆨ" that makes it "각" — so the rule put a line break in the
// middle of one letter. UAX #29 joins them by rule GB7, which is why routing
// every cut through the segmenter fixes it.
func TestAHangulSyllableIsNotCutFromItsJamo(t *testing.T) {
	const syllable = "각" // 각, spelled LV + T
	pieces, _ := splitAtBreaks(syllable, whiteSpaceOf("collapse"), wordBreak{}, lineBreak{})
	for i, p := range pieces {
		if i > 0 && p.breakBefore {
			t.Fatalf("a line may end inside one Hangul syllable: %q then %q",
				pieces[i-1].text, p.text)
		}
	}

	// And two whole syllables still break between them, which is the rule this
	// must not have disabled to pass.
	pieces, _ = splitAtBreaks("가가", whiteSpaceOf("collapse"), wordBreak{}, lineBreak{})
	if len(pieces) != 2 || !pieces[1].breakBefore {
		t.Errorf("two Hangul syllables gave %d pieces with no opportunity between "+
			"them; the ideograph rule has been turned off rather than corrected",
			len(pieces))
	}
}

// TestAnIdeographOffersABreakToTheNextBox is the deferred opportunity crossing
// a box boundary, which is the one path the tests above cannot reach.
//
// The break after an ideograph is held back until the following character says
// the cluster ended — but when the ideograph is the last thing in its box, that
// character is in another box and this one has to hand the opportunity on. A
// version that dropped it would run "日本" together on one line however narrow
// the block, and every test that keeps both characters in one box would still
// pass.
func TestAnIdeographOffersABreakToTheNextBox(t *testing.T) {
	// Two ideographs, one per box, in a block one ideograph wide. Ahem is not
	// needed: what is asserted is the number of lines, and a break between them
	// is the only way to get two.
	root := layoutOf(t, 600, `<div id="p"><span>日</span>本</div>`,
		`#p { font-family: Courier; font-size: 20px; width: 20px }`)
	if got := len(find(t, root, "p").Lines); got != 2 {
		t.Errorf("two ideographs in separate boxes made %d line(s), want 2 — the "+
			"opportunity after the first was not passed to the box after it", got)
	}
}
