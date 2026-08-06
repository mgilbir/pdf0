package render

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// break-spaces, and §4.1's "other space separators".
//
// The two belong in one file because they are one rule read twice. §4.1.2's
// fourth rule is written over "white space, other space separators, and/or
// preserved tabs" and says what a run of them at the end of a line weighs;
// break-spaces is the value that opts out of it. So the ideographic space and
// the break-spaces keyword ask the same question — what does a space at the end
// of a line cost — one about a character and one about a property.
//
// Every number below is arithmetic rather than a recording: the text is Courier,
// whose every glyph is 600 units of 1000, so a character at 100px is 60px wide
// and a length is a character count. mono and ch are in whitespace_test.go.
//
// The separators are written as escapes rather than as themselves, because a
// test whose meaning depends on which invisible character survived an editor is
// a test nobody can review.

// widthCSS is a stylesheet fixing a paragraph's width in characters.
func widthCSS(chars float64, extra string) string {
	return noDefaults + mono + `p { width: ` +
		strconv.FormatFloat(chars*ch, 'f', -1, 64) + `px; ` + extra + ` }`
}

// TestBreakSpacesBreaksOnlyAfterASpace is §3's break-spaces at the point that
// distinguishes it from pre-wrap, and the point this engine had wrong.
//
// "A soft wrap opportunity exists after every preserved white space character
// and after every other space separator (including between adjacent spaces)" —
// and, by omission, nowhere pre-wrap would not have one either. So a preserved
// space belongs to the unit *before* it, and greedy filling has to measure that
// whole unit: "a bb a" in four characters is "a " and "bb a", because "bb " is
// three characters and does not fit after the first two.
//
// The comparison against pre-wrap is the assertion. The same six characters in
// the same four-character line break in two different places, and an engine that
// treated break-spaces as a synonym for pre-wrap would give the pre-wrap answer
// to both.
func TestBreakSpacesBreaksOnlyAfterASpace(t *testing.T) {
	const src = `<p id="p">a bb a</p>`

	root := layoutOf(t, 10000, src, widthCSS(4, "white-space: break-spaces"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a " || got[1] != "bb a" {
		t.Errorf("break-spaces broke %q into %q, want [\"a \" \"bb a\"]", "a bb a", got)
	}

	// pre-wrap over the same text: the space hangs past the end of the line
	// instead of taking room, so "a bb" fits and the last word wraps alone.
	root = layoutOf(t, 10000, src, widthCSS(4, "white-space: pre-wrap"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a bb " || got[1] != "a" {
		t.Errorf("pre-wrap broke %q into %q, want [\"a bb \" \"a\"]", "a bb a", got)
	}
}

// TestBreakSpacesGoesBackToTheNearestOpportunity pins *which* opportunity the
// line is given back to, which the test above cannot decide because the line it
// breaks has only one.
//
// A line that cannot hold the space at the end of its last unit ends at the
// opportunity nearest that space and not at the first one it passed. The two
// differ by a whole word, and the marker being armed once per line rather than
// once per opportunity is a one-word change that this file did not catch until
// it was planted.
func TestBreakSpacesGoesBackToTheNearestOpportunity(t *testing.T) {
	// Four units of two characters each — "a ", "b ", "c ", "dd" — in a line
	// five wide. "a b " is four and fits; adding "c " would be six. So the line
	// ends after the second space, three opportunities in.
	root := layoutOf(t, 10000, `<p id="p">a b c dd</p>`,
		widthCSS(5, "white-space: break-spaces"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a b " || got[1] != "c dd" {
		t.Errorf("break-spaces broke %q into %q, want [\"a b \" \"c dd\"]",
			"a b c dd", got)
	}
}

// TestBreakSpacesBreaksBetweenTwoSpaces is the parenthesis in the same sentence:
// the opportunity exists "including between adjacent spaces".
//
// It is a separate test from the one above because it is a separate claim, and
// because it is the one that cannot be satisfied by treating a run of spaces as
// a single item however the fit is measured. "a   b" in three characters puts
// two of its three spaces on the first line and the third on the second.
func TestBreakSpacesBreaksBetweenTwoSpaces(t *testing.T) {
	const src = `<p id="p">a   b</p>`

	root := layoutOf(t, 10000, src, widthCSS(3, "white-space: break-spaces"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a  " || got[1] != " b" {
		t.Errorf("break-spaces broke %q into %q, want [\"a  \" \" b\"]", "a   b", got)
	}

	// pre-wrap hangs the whole run rather than splitting it, so the letter after
	// it begins the next line and the line before holds all three spaces.
	root = layoutOf(t, 10000, src, widthCSS(3, "white-space: pre-wrap"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "a   " || got[1] != "b" {
		t.Errorf("pre-wrap broke %q into %q, want [\"a   \" \"b\"]", "a   b", got)
	}
}

// TestBreakSpacesOverflowsWhenNothingFits is the note attached to the value:
// "this value does not guarantee that there will never be any overflow due to
// white space: for example, if the line length is so short that even a single
// white space character does not fit, overflow is unavoidable."
//
// It is the case the rewind must *not* fire on, and it is worth its own test
// because the two are one condition apart: a rewind with no opportunity behind
// it has nowhere to go, and an implementation that invented one would either
// drop the space or loop.
func TestBreakSpacesOverflowsWhenNothingFits(t *testing.T) {
	// One character wide, and the first unit is "ab " — three characters with no
	// opportunity inside it. It overflows rather than moving.
	root := layoutOf(t, 10000, `<p id="p">ab c</p>`,
		widthCSS(1, "white-space: break-spaces"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 ||
		got[0] != "ab " || got[1] != "c" {
		t.Errorf("break-spaces broke %q into %q, want [\"ab \" \"c\"]", "ab c", got)
	}
}

// TestBreakSpacesTrailingSpaceIsAligned pins the other end of the value: a
// break-spaces space "cannot hang nor have its advance width collapsed", so it
// counts when the line is placed in the width it was given.
//
// pre-wrap beside it is the control, and the line has to be one that *wrapped*:
// at the end of the content a pre-wrap space only conditionally hangs and is
// counted too, so a document without the second word would be asking this
// question of a rule that does not decide it.
func TestBreakSpacesTrailingSpaceIsAligned(t *testing.T) {
	const src = `<p id="p">a bb</p>`

	root := layoutOf(t, 10000, src,
		widthCSS(3, "text-align: right; white-space: break-spaces"))
	lines := linesOf(t, root, "p")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lineTexts(lines))
	}
	// Line one is "a " — two characters right-aligned in three, so one character
	// of slack in front of it.
	if got := lines[0].Runs[0].X.Px(); got != ch {
		t.Errorf("a right-aligned break-spaces line starts at %gpx, want %g — its "+
			"trailing space is being hung", got, ch)
	}

	root = layoutOf(t, 10000, src,
		widthCSS(3, "text-align: right; white-space: pre-wrap"))
	lines = linesOf(t, root, "p")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lineTexts(lines))
	}
	// The same line under pre-wrap: the space hangs, so only "a" is aligned and
	// it sits flush against the right edge with the space past it.
	if got := lines[0].Runs[0].X.Px(); got != 2*ch {
		t.Errorf("a right-aligned pre-wrap line starts at %gpx, want %g", got, 2*ch)
	}
}

// TestBreakSpacesRewindTerminates is the safety property on the second rewind in
// breakOneLine, on the model of TestInlineInsetRewindTerminates.
//
// The argument is the same and so is the cost of it being wrong. The marker is
// only armed once the line holds content, so it always points past the item the
// line began at, and the line the rewind produces therefore starts strictly
// further on. The shape most likely to break that is a line too narrow to hold
// anything, so that every unit both overflows and has an opportunity behind it.
func TestBreakSpacesRewindTerminates(t *testing.T) {
	if testing.Short() {
		t.Skip("the input is large")
	}
	text := strings.Repeat("a b ", 5000)
	done := make(chan int, 1)
	go func() {
		root := layoutOf(t, 10000, `<p id="p">`+text+`</p>`,
			noDefaults+mono+`p { width: 1px; white-space: break-spaces }`)
		done <- len(linesOf(t, root, "p"))
	}()
	select {
	case n := <-done:
		if n == 0 {
			t.Error("the paragraph produced no lines")
		}
	case <-time.After(30 * time.Second):
		// A hang, not a slow machine: the whole reftest suite of five thousand
		// documents lays out in under ten seconds.
		t.Fatal("breaking twenty thousand characters at 1px did not finish in 30 seconds")
	}
}

// separators is the Unicode Zs category less the two §4.1 excludes by name.
var separators = []rune{
	0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006,
	0x2007, 0x2008, 0x2009, 0x200A, 0x202F, 0x205F, 0x3000,
}

// TestOtherSpaceSeparatorsAreWhiteSpaceForPhaseII walks that set.
//
// The two rules over it are tested apart, because they disagree about two of its
// members: §4.1.2 says all of them hang at the end of a line, and UAX #14 says
// only some of them offer a place to break. An implementation that read one rule
// as the other produces a plausible-looking page either way — a no-break space
// that breaks, or an ideographic space that stretches a box.
func TestOtherSpaceSeparatorsAreWhiteSpaceForPhaseII(t *testing.T) {
	for _, r := range separators {
		if !isOtherSpaceSeparator(r) {
			t.Errorf("U+%04X is in Zs and is neither U+0020 nor U+00A0, so it is "+
				"an other space separator", r)
		}
		pieces, _ := splitAtBreaks("ab"+string(r)+"cd", whiteSpaceOf("pre-wrap"))
		if len(pieces) != 3 {
			t.Errorf("U+%04X cut its text into %d pieces, want 3", r, len(pieces))
			continue
		}
		if !pieces[1].space || pieces[1].collapsible {
			t.Errorf("U+%04X gave a piece {space:%v collapsible:%v}, want a "+
				"preserved space: Phase I is defined over U+0020, U+0009 and the "+
				"segment breaks and never touches this character",
				r, pieces[1].space, pieces[1].collapsible)
		}
	}

	// The two §4.1 excludes by name, and the one Unicode removed from Zs in 6.3.
	// A no-break space is not white space for any of these rules, which is the
	// whole reason an author writes one.
	for _, r := range []rune{' ', 0x00A0, 0x180E} {
		if isOtherSpaceSeparator(r) {
			t.Errorf("U+%04X is not an other space separator", r)
		}
	}
}

// TestSeparatorBreakOpportunitiesFollowUAX14 is the other half of the same set.
//
// U+2007 FIGURE SPACE holds a column of digits together and U+202F NARROW
// NO-BREAK SPACE holds a number to its unit; UAX #14 gives both class GL and no
// opportunity. Everything else here is class BA, except U+3000, which is class
// ID and breaks like the ideographs it is spaced among.
func TestSeparatorBreakOpportunitiesFollowUAX14(t *testing.T) {
	for _, r := range separators {
		want := r != 0x2007 && r != 0x202F
		if got := separatorBreaksAfter(r); got != want {
			t.Errorf("a line may end after U+%04X: got %v, want %v", r, got, want)
		}
		pieces, _ := splitAtBreaks("ab"+string(r)+"cd", whiteSpaceOf("pre-wrap"))
		if len(pieces) == 3 && pieces[2].breakBefore != want {
			t.Errorf("the text after U+%04X may begin a line: got %v, want %v",
				r, pieces[2].breakBefore, want)
		}
		// break-spaces overrides both exceptions: it puts an opportunity "after
		// every other space separator", with no carve-out for the no-break ones.
		pieces, _ = splitAtBreaks("ab"+string(r)+"cd", whiteSpaceOf("break-spaces"))
		if len(pieces) == 3 && !pieces[2].breakBefore {
			t.Errorf("break-spaces left no opportunity after U+%04X", r)
		}
	}
}

// TestNoBreakSeparatorDoesNotBreakALine is that claim where it shows: on the
// page, and in the minimum width a float is sized by.
//
// It is separate from the table above because a flag being right is not the same
// as it being read.
func TestNoBreakSeparatorDoesNotBreakALine(t *testing.T) {
	// U+202F between two pairs: five characters that cannot be broken, in a line
	// three wide. It overflows as one line rather than wrapping.
	root := layoutOf(t, 10000, "<p id=\"p\">ab\u202fcd</p>",
		widthCSS(3, "white-space: pre-wrap"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 1 {
		t.Errorf("a narrow no-break space broke the text into %q", got)
	}
	// U+2000 in the same place does break, so the assertion above is about the
	// character and not about the machinery.
	root = layoutOf(t, 10000, "<p id=\"p\">ab\u2000cd</p>",
		widthCSS(3, "white-space: pre-wrap"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 {
		t.Errorf("an en quad did not break the text; got %q", got)
	}

	// And the minimum width, which is what a float in a narrow place is given.
	// Five characters that cannot be broken need five characters of room; the
	// same text with a breakable separator needs two.
	const shrink = `<div style="width: 1px"><div id="f">%</div></div>`
	css := noDefaults + mono + `#f { float: left; white-space: pre-wrap;
	    font-size: 100px; font-family: Courier }`

	root = layoutOf(t, 10000, strings.Replace(shrink, "%", "ab\u202fcd", 1), css)
	px(t, "the minimum width across a no-break separator",
		find(t, root, "f").BorderRect.W, 5*ch)

	root = layoutOf(t, 10000, strings.Replace(shrink, "%", "ab\u2000cd", 1), css)
	px(t, "the minimum width across a breakable separator",
		find(t, root, "f").BorderRect.W, 2*ch)
}

// TestIdeographicSpaceHangsAtTheEndOfALine is §4.1.2's fourth rule reaching a
// character Phase I never saw.
//
// A trailing U+3000 is not collapsible — nothing collapses it, at any value of
// white-space — and it is not removed either, because §4.1.2's third rule takes
// only the collapsible spaces and the ogham space mark. So it hangs, and the
// line it ends is aligned as though it were not there.
func TestIdeographicSpaceHangsAtTheEndOfALine(t *testing.T) {
	// It survives collapsing, which is the first half: under the initial value
	// of white-space the text still has all three characters.
	root := layoutOf(t, 10000, "<p id=\"p\">a\u3000b</p>", noDefaults+mono)
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 1 || got[0] != "a\u3000b" {
		t.Errorf("collapsing white space gave %q, want one line of \"a\\u3000b\"", got)
	}

	// And it hangs, which is the second. "ab\u3000cd" in four characters wraps
	// after the separator, so the first line is "ab" and a hanging U+3000;
	// right-aligned, the two visible characters are flush against the edge.
	root = layoutOf(t, 10000, "<p id=\"p\">ab\u3000cd</p>",
		widthCSS(4, "text-align: right"))
	lines := linesOf(t, root, "p")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lineTexts(lines))
	}
	if got := lines[0].Runs[0].X.Px(); got != 2*ch {
		t.Errorf("a right-aligned line ending in an ideographic space starts at "+
			"%gpx, want %g — the separator is being counted", got, 2*ch)
	}
}

// TestOghamSpaceMarkIsRemovedAtALineEnd is §4.1.2's third rule, which names one
// character and only one: "any trailing U+1680 OGHAM SPACE MARK whose
// white-space property is normal, nowrap, or pre-line".
//
// It is removed rather than hung, and the difference is one a reader can see: an
// ogham space mark is a stemline in an ogham face and not blank paper. The rule
// is conditional on the value, so both directions are here.
func TestOghamSpaceMarkIsRemovedAtALineEnd(t *testing.T) {
	// Wrapping at the separator: the first line ends with it, and under a
	// collapsing value it goes.
	root := layoutOf(t, 10000, "<p id=\"p\">ab\u1680cd</p>", widthCSS(3, ""))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 || got[0] != "ab" {
		t.Errorf("a trailing ogham space mark under a collapsing value gave %q, "+
			"want a first line of \"ab\"", got)
	}

	// Under pre-wrap it is kept, because the rule names three values and pre-wrap
	// is not one of them.
	root = layoutOf(t, 10000, "<p id=\"p\">ab\u1680cd</p>",
		widthCSS(3, "white-space: pre-wrap"))
	if got := lineTexts(linesOf(t, root, "p")); len(got) != 2 || got[0] != "ab\u1680" {
		t.Errorf("a trailing ogham space mark under pre-wrap gave %q, want a "+
			"first line of \"ab\\u1680\"", got)
	}

	// It is still not *collapsible*, which is the distinction the second flag
	// exists for: a run of them is a run of stemlines, and folding two into one
	// would shorten the line by a character the author drew.
	pieces, _ := splitAtBreaks("a\u1680\u1680b", whiteSpaceOf("normal"))
	if len(pieces) != 4 {
		t.Errorf("two ogham space marks gave %d pieces, want 4 — they were "+
			"collapsed into one", len(pieces))
	}

	// And the removal reaches the intrinsic width, which is the number a float
	// in a wide place is sized by. Every line an intrinsic measurement sees ends
	// at the end of the content, so a trailing ogham space mark is removed from
	// each of them and the box is two characters wide rather than three.
	const shrink = `<div style="width: 10000px"><div id="f">%</div></div>`
	css := noDefaults + `#f { float: left; font-size: 100px;
	    font-family: Courier; line-height: 100px }`

	root = layoutOf(t, 10000, strings.Replace(shrink, "%", "ab\u1680", 1), css)
	px(t, "the preferred width of text ending in an ogham space mark",
		find(t, root, "f").BorderRect.W, 2*ch)

	// A trailing separator that is *not* the one the rule names is the contrast:
	// it hangs rather than being removed, and §4.1.2 makes the hang at the end
	// of the content conditional, so it takes room in a box that cannot overflow.
	root = layoutOf(t, 10000, strings.Replace(shrink, "%", "ab\u3000", 1), css)
	px(t, "the preferred width of text ending in an ideographic space",
		find(t, root, "f").BorderRect.W, 3*ch)
}
