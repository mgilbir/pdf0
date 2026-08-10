package render

import (
	"strings"
	"testing"
)

// Quotation marks, CSS 2.1 §12.3.
//
// The assertions are on the text produced, for the reason counter_test.go gives
// for counters: the depth is the whole difficulty and an implementation that
// simply drew the first pair every time would satisfy any test that only asked
// whether a mark appeared. Every case below is chosen so that one rule decides
// it and the pairs are distinct enough to say which.

func TestQuotesDrawThePairForTheLevel(t *testing.T) {
	// One element, one quotation: the outermost pair, opened and closed.
	got := generatedText(t, `<div>x</div>`, `
		div { quotes: "A" "Z" "b" "y" }
		div::before { content: open-quote }
		div::after { content: close-quote }`)
	if !strings.Contains(got, "A") || !strings.Contains(got, "Z") {
		t.Errorf("produced %q, want the outermost pair A and Z", got)
	}
	if strings.Contains(got, "b") || strings.Contains(got, "y") {
		t.Errorf("produced %q, which used a pair from a level nothing reached", got)
	}
}

func TestQuotesNestToTheSecondPair(t *testing.T) {
	// The rule this isolates: the depth is the number of quotations *open* at
	// that point in the document, so the inner element's marks come from the
	// second pair. An implementation with no depth at all draws "AAZZ".
	got := generatedText(t, `<div id="o"><div id="i">x</div></div>`, `
		div { quotes: "A" "Z" "b" "y" }
		div::before { content: open-quote }
		div::after { content: close-quote }`)
	if want := "A|b|x|y|Z"; got != want {
		t.Errorf("produced %q, want %q — the inner quotation takes the second pair", got, want)
	}
}

func TestQuotesRepeatTheLastPairWhenNestedDeeper(t *testing.T) {
	// §12.3.2: a quotation deeper than the list provided for uses the last pair,
	// rather than dropping the mark or running off the end of the slice.
	got := generatedText(t, `<div><div><div>x</div></div></div>`, `
		div { quotes: "A" "Z" "b" "y" }
		div::before { content: open-quote }`)
	if want := "A|b|b|x"; got != want {
		t.Errorf("produced %q, want %q — the third level repeats the last pair", got, want)
	}
}

func TestNoQuoteKeywordsMoveTheDepthWithoutDrawing(t *testing.T) {
	// This is the whole reason no-open-quote exists. Suppressing the outer
	// opening mark must not thereby give the inner quotation the outer pair: the
	// depth still moved, so the inner element is at level two.
	got := generatedText(t, `<div id="o"><div id="i">x</div></div>`, `
		div { quotes: "A" "Z" "b" "y" }
		#o::before { content: no-open-quote }
		#i::before { content: open-quote }
		#i::after { content: close-quote }
		#o::after { content: no-close-quote }`)
	if want := "b|x|y"; got != want {
		t.Errorf("produced %q, want %q — no-open-quote draws nothing and still counts", got, want)
	}
}

func TestQuotesNoneDrawsNothingAndStillCounts(t *testing.T) {
	// "quotes: none" is not "no quotation": §12.3.1's keywords go on counting, so
	// an element inside one that sets a list of its own is at the deeper level.
	got := generatedText(t, `<div id="o"><div id="i">x</div></div>`, `
		#o { quotes: none }
		#i { quotes: "A" "Z" "b" "y" }
		div::before { content: open-quote }`)
	if want := "b|x"; got != want {
		t.Errorf("produced %q, want %q — the outer quotation drew nothing but was still open",
			got, want)
	}
}

func TestCloseQuoteWithNothingOpenDrawsNothing(t *testing.T) {
	// §12.3.1 makes this an error and lets the user agent render nothing. What it
	// must *not* do is take the depth negative, which would index the pair list
	// from the wrong end and draw an opening mark for the next close-quote.
	got := generatedText(t, `<div id="a">x</div><div id="b">y</div>`, `
		div { quotes: "A" "Z" }
		#a::before { content: close-quote }
		#b::before { content: open-quote }`)
	if strings.Contains(got, "Z") {
		t.Errorf("produced %q; a close-quote with nothing open draws nothing", got)
	}
	if !strings.Contains(got, "A") {
		t.Errorf("produced %q; the depth should be back at zero, so the next "+
			"open-quote takes the outermost pair", got)
	}
}

func TestQuotesRunInOrderWithinOneValue(t *testing.T) {
	// A content value may hold several keywords, and they run left to right
	// against the same depth. "open-quote open-quote" is two levels and not the
	// same mark twice.
	got := generatedText(t, `<div>x</div>`, `
		div { quotes: "A" "Z" "b" "y" "C" "X" }
		div::before { content: open-quote open-quote open-quote }`)
	if !strings.Contains(got, "Ab") || !strings.Contains(got, "AbC") {
		t.Errorf("produced %q, want the three levels A, b and C in order", got)
	}
}

func TestQuotesInherit(t *testing.T) {
	// The property is inherited, and it has to be: the ::before that draws the
	// mark hangs from a descendant of the element that set the pairs.
	got := generatedText(t, `<div><p>x</p></div>`, `
		div { quotes: "A" "Z" }
		p::before { content: open-quote }`)
	if !strings.Contains(got, "A") {
		t.Errorf("produced %q; the pairs did not reach the descendant", got)
	}
}

func TestIllegalQuotesLeavesTheInheritedPairs(t *testing.T) {
	// §4.2: a declaration with an illegal value is dropped, so what stands is
	// what the cascade would have produced without it — the *inherited* pairs and
	// not the initial ones. An odd number of strings names a level with an
	// opening mark and no closing one, which the grammar does not admit.
	got := generatedText(t, `<div><p>x</p></div>`, `
		div { quotes: "A" "Z" }
		p { quotes: "Q" }
		p::before { content: open-quote }`)
	if !strings.Contains(got, "A") {
		t.Errorf("produced %q; the illegal declaration should have been dropped, "+
			"leaving the inherited A", got)
	}
	if strings.Contains(got, "Q") {
		t.Errorf("produced %q; a list with an odd number of strings is not a value", got)
	}
}

func TestIllegalQuotesIsReported(t *testing.T) {
	got := build(t, `<div>x</div>`, `div { quotes: "Q" }`)
	var found bool
	for _, f := range got.Findings {
		if f.Property == "quotes" && strings.Contains(f.Message, "pairs of strings") {
			found = true
			if f.Unsupported() {
				t.Error("an illegal value was reported as unsupported; nothing is " +
					"missing from the engine, the stylesheet said something CSS forbids")
			}
		}
	}
	if !found {
		t.Errorf("an illegal quotes value was dropped silently: %v", got.Findings)
	}
}

func TestQuoteDepthIsBounded(t *testing.T) {
	// The depth grows with the document and the document is untrusted. This does
	// not assert a number, only that a stylesheet built to run it away is laid out
	// at all — and that the marks are still the last pair rather than an index
	// off the end.
	var b strings.Builder
	const depth = 120
	for i := 0; i < depth; i++ {
		b.WriteString(`<div>`)
	}
	b.WriteString(`x`)
	for i := 0; i < depth; i++ {
		b.WriteString(`</div>`)
	}
	got := generatedText(t, b.String(), `
		div { quotes: "A" "Z" "b" "y" }
		div::before { content: open-quote }`)
	if !strings.Contains(got, "x") {
		t.Error("a deeply nested document produced no text at all")
	}
	if strings.Count(got, "A") != 1 {
		t.Errorf("the outermost pair was used %d times, want once", strings.Count(got, "A"))
	}
}

// TestOverlongGeneratedContentIsRefused pins the one amplification the quote
// keywords add. Every other source of generated text is at most as long as the
// document that names it; "quotes" holds strings of the author's choosing and
// one keyword each fetches a whole one, so a short declaration can ask for an
// arbitrarily long run.
//
// The refusal is reported rather than truncated, for the reason the rest of this
// engine reports: a marker cut off in the middle is a page that looks finished.
func TestOverlongGeneratedContentIsRefused(t *testing.T) {
	long := strings.Repeat("x", 1<<16)
	var keywords strings.Builder
	for i := 0; i < 64; i++ {
		keywords.WriteString(" open-quote")
	}
	got := build(t, `<div>x</div>`,
		`div { quotes: "`+long+`" "`+long+`" }
		 div::before { content:`+keywords.String()+` }`)

	var found bool
	for _, f := range got.Findings {
		if f.Property == "content" && strings.Contains(f.Message, "longer than") {
			found = true
		}
	}
	if !found {
		t.Errorf("four megabytes of generated content was produced without a word "+
			"about it: %v", got.Findings)
	}
}

// TestQuoteArithmeticIsOneImplementation pins that the depth the walk carries
// forward and the text the content produces come from the same rule.
//
// They are two callers of applyQuote and they have to be, because the walk runs
// before any counter has a value and the text does not. Two implementations of
// §12.3.1's asymmetric increment would drift, and the drift would show only on a
// document where one element opens and a later one closes — which is every
// document that uses the feature at all.
func TestQuoteArithmeticIsOneImplementation(t *testing.T) {
	pairs := quoteList{{"A", "Z"}, {"b", "y"}, {"C", "X"}}
	for _, tc := range []struct {
		content string
		from    int
		want    int
	}{
		{`open-quote`, 0, 1},
		{`open-quote open-quote`, 0, 2},
		{`close-quote`, 2, 1},
		{`close-quote close-quote close-quote`, 1, 0},
		// The case a net count of opens minus closes gets wrong: the second close
		// had nothing to close, so it did not take the depth below zero and the
		// open that follows starts from zero rather than from minus one.
		{`close-quote close-quote open-quote`, 1, 1},
		{`no-open-quote no-close-quote`, 0, 0},
		{`"just a string"`, 3, 3},
	} {
		if got := quoteDepthAfter(tc.content, tc.from, pairs); got != tc.want {
			t.Errorf("%q from depth %d ended at %d, want %d", tc.content, tc.from, got, tc.want)
		}
		// And the text production must agree about where it ended, which is what
		// makes them one rule rather than two that happen to match.
		depth := tc.from
		for _, word := range strings.Fields(tc.content) {
			if op, isQuote := quoteKeyword(word); isQuote {
				_, depth = applyQuote(op, depth, pairs)
			}
		}
		if depth != tc.want {
			t.Errorf("%q: the text production ended at depth %d, want %d", tc.content, depth, tc.want)
		}
	}
}
