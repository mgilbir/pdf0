package bidi

import (
	"strings"
	"testing"
	"time"
)

// Unit tests for the algorithm, beside the conformance suites.
//
// The suites are the real check on the rules and these do not try to duplicate
// them: 862,000 cases from the Unicode Consortium say more about W2 than any
// example written here could. What is here is what they do *not* cover — the
// bounds, the fast path, the per-line half of the algorithm that the suites
// exercise only with the whole text as one line, and a handful of cases held in
// terms a reader can check by eye rather than by index.

// alef, bet, gimel are Hebrew letters; alifArabic is an Arabic one. Spelling
// them out keeps the tests readable in an editor that will not render the
// script, and keeps a reversed expectation from looking like a correct one.
const (
	alef       = 'א'
	bet        = 'ב'
	gimel      = 'ג'
	alifArabic = 'ا'
)

// TestMixedDirectionOrder pins the cases a plain reversal gets wrong.
//
// Each of these passes under "reverse the right-to-left characters" only by
// accident, and most do not pass at all — which is the point. A test on an
// all-Hebrew string would pass under a reversal, so there is not one here.
func TestMixedDirectionOrder(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		dir   Direction
		order []int
	}{
		{
			// The number keeps its own direction inside the Hebrew: "12" is
			// drawn "12" and not "21", while the letters around it reverse.
			name: "digits inside right-to-left text",
			text: string([]rune{alef, bet, ' ', '1', '2', ' ', gimel}),
			dir:  LeftToRight,
			// Visually: gimel, space, 12, space, bet, alef.
			order: []int{6, 5, 3, 4, 2, 1, 0},
		},
		{
			// A neutral between two right-to-left runs joins them (N1), so the
			// space does not become a left-to-right island.
			name:  "neutral between two right-to-left runs",
			text:  string([]rune{alef, ' ', bet}),
			dir:   LeftToRight,
			order: []int{2, 1, 0},
		},
		{
			// A neutral between opposite directions takes the paragraph's (N2),
			// so the space stays with the Latin run at level 0.
			name:  "neutral between opposite directions",
			text:  string([]rune{'a', ' ', alef}),
			dir:   LeftToRight,
			order: []int{0, 1, 2},
		},
		{
			// A Latin word inside a right-to-left paragraph: the sentence
			// reverses around it and the word itself does not — "ab" is drawn
			// "ab", which a reversal of the whole line would not give.
			name:  "latin inside right-to-left",
			text:  string([]rune{alef, ' ', 'a', 'b', ' ', bet}),
			dir:   RightToLeft,
			order: []int{5, 4, 2, 3, 1, 0},
		},
		{
			// The whole point of the direction property: Latin text in a
			// right-to-left paragraph keeps its own order.
			name:  "latin in a right-to-left paragraph",
			text:  "ab cd",
			dir:   RightToLeft,
			order: []int{0, 1, 2, 3, 4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := []rune(tc.text)
			p := Resolve(text, tc.dir)
			got := Reorder(p.LineLevels(0, len(text)))
			if len(got) != len(tc.order) {
				t.Fatalf("order %v, want %v", got, tc.order)
			}
			for i := range got {
				if got[i] != tc.order[i] {
					t.Fatalf("order %v, want %v (levels %v)", got, tc.order, p.Levels())
				}
			}
		})
	}
}

// TestArabicDigitsDifferFromHebrewDigits is rule W2 on its own, because it is
// the rule that no reversal and no "is this character right-to-left" heuristic
// can express: the same digit takes a different level after an Arabic letter
// than after a Hebrew one, and the two therefore draw in different places.
func TestArabicDigitsDifferFromHebrewDigits(t *testing.T) {
	afterHebrew := []rune{alef, '1', '2'}
	afterArabic := []rune{alifArabic, '1', '2'}

	h := Resolve(afterHebrew, LeftToRight).Levels()
	a := Resolve(afterArabic, LeftToRight).Levels()

	// European number after Hebrew: level 2 by I1. Arabic number after an
	// Arabic letter — W2 turned it into one — also level 2, but the two reach it
	// by different rules, so the check that matters is the *order*.
	if h[1] != 2 || a[1] != 2 {
		t.Fatalf("digit levels: after Hebrew %v, after Arabic %v", h, a)
	}
	// What differs is what a separator between them does. W4 joins two European
	// numbers across a plus sign and does *not* join two Arabic ones — so
	// "1+2" written after an Arabic letter has a plus that is neither, ends up a
	// neutral, and takes the surrounding right-to-left direction; the same three
	// characters after a Latin letter are one left-to-right number.
	arabic := Resolve([]rune{alifArabic, '1', '+', '2'}, LeftToRight).Levels()
	if arabic[2] != 1 {
		t.Errorf("the plus of \"1+2\" in Arabic context is at level %d, want 1 — W4 "+
			"joins two European numbers and not two Arabic ones (levels %v)", arabic[2], arabic)
	}
	latin := Resolve([]rune{'a', '1', '+', '2'}, LeftToRight).Levels()
	if latin[2] != 0 {
		t.Errorf("the plus of \"1+2\" in Latin context is at level %d, want 0 (levels %v)",
			latin[2], latin)
	}
}

// TestBracketsFollowTheirContents is rule N0, which resolves a bracket pair as a
// unit rather than as two neutrals that happen to look alike.
//
// The case has to be chosen with care, and the obvious one does not work: for
// "a (HEBREW)" the neutral rules N1 and N2 reach the same answer as N0 on their
// own, so a test on it passes with N0 deleted. This is a case where they do not.
// "ab (c)" in a right-to-left paragraph has Latin inside the brackets and Latin
// before them, so N0 gives the pair the direction of its contents and both
// brackets go left-to-right with the letters. Without it the closing bracket has
// right-to-left text on one side — the end of the paragraph — and falls back to
// the paragraph's own direction, which puts it at the wrong end of the phrase.
func TestBracketsFollowTheirContents(t *testing.T) {
	text := []rune("ab (c)")
	levels := Resolve(text, RightToLeft).Levels()
	for i, want := range []uint8{2, 2, 2, 2, 2, 2} {
		if levels[i] != want {
			t.Fatalf("levels %v, want all 2 — rule N0 gives the bracket pair the "+
				"direction of what is inside it, and the closing bracket at index 5 "+
				"is the one that differs without it", levels)
		}
	}

	// And the rule really is about the *pair*: with right-to-left text inside,
	// in the same left-to-right context, the brackets follow that instead.
	text = []rune{'a', 'b', ' ', '(', alef, ')'}
	levels = Resolve(text, RightToLeft).Levels()
	if levels[3] != 1 || levels[5] != 1 {
		t.Errorf("with Hebrew inside, the brackets are at levels %d and %d, want 1 "+
			"and 1 (levels %v)", levels[3], levels[5], levels)
	}
}

// TestExplicitDepthCapFires is the security bound.
//
// Without it, a document made of nothing but U+202B is a directional-status
// stack one entry deep per three bytes of input, and the levels themselves
// overflow the byte they are stored in. With it the state is fixed at 127
// entries however deep the nesting goes, and every level past the cap is
// overflow.
func TestExplicitDepthCapFires(t *testing.T) {
	// Each override raises the level to the next odd one, so the 63rd reaches
	// 125 and the 64th would reach 127 — past the cap, and therefore counted as
	// overflow and dropped. A *right-to-left* override is used rather than an
	// embedding so that the letter inside it is right-to-left at an odd level and
	// rules I1 and I2 add nothing: what is left is the explicit level, which is
	// what the cap is about.
	const deep = 20000
	text := []rune(strings.Repeat("‮", deep) + "a")
	p := Resolve(text, LeftToRight)

	highest := uint8(0)
	for _, l := range p.Levels() {
		if l > highest {
			highest = l
		}
	}
	if highest > MaxDepth {
		t.Fatalf("the deepest level reached is %d, past the cap of %d — the cap did "+
			"not fire, and the stack it did not bound is one entry per three bytes "+
			"of an untrusted document", highest, MaxDepth)
	}
	if highest != MaxDepth {
		t.Errorf("the deepest level reached is %d; %d overrides should reach the cap "+
			"of %d exactly, so this test is no longer exercising it", highest, deep, MaxDepth)
	}
	// And the character past the overflow is still laid out, at the capped
	// level rather than dropped: refusing the document would be the wrong answer
	// to a document that is merely absurd.
	if got := p.Levels()[len(text)-1]; got != MaxDepth {
		t.Errorf("the letter after %d overrides is at level %d, want %d",
			deep, got, MaxDepth)
	}

	// The same for isolates, which overflow through a counter of their own. An
	// isolate applies no override, so the letter inside is left-to-right at an
	// odd level and rule I2 adds one — MaxDepth+1 is the answer, and anything
	// above MaxDepth+2 would mean the cap was not applied at all.
	iso := []rune(strings.Repeat("⁧", deep) + "a")
	highest = 0
	for _, l := range Resolve(iso, LeftToRight).Levels() {
		if l > highest {
			highest = l
		}
	}
	if highest != MaxDepth+1 {
		t.Errorf("isolates reached level %d, want %d", highest, MaxDepth+1)
	}
}

// TestBracketStackIsBounded is BD16's own cap. A document nesting parentheses
// ten thousand deep must cost a fixed 63 entries, and the text must still come
// out — the rule is abandoned for the rest of the sequence, not the text.
func TestBracketStackIsBounded(t *testing.T) {
	const deep = 50000
	text := []rune(strings.Repeat("(", deep) + string(alef) + strings.Repeat(")", deep))
	p := Resolve(text, LeftToRight)
	if p.Len() != len(text) {
		t.Fatalf("resolved %d characters of %d", p.Len(), len(text))
	}
	// The Hebrew letter in the middle is still right-to-left whatever BD16 did
	// with the brackets around it.
	if got := p.Levels()[deep]; got != 1 {
		t.Errorf("the letter inside %d bracket pairs is at level %d, want 1", deep, got)
	}
}

// TestLinearInParagraphLength is the performance bound.
//
// The algorithm is specified as several passes, and two of them have an obvious
// quadratic implementation: N0's backward scan for the strong context before
// each bracket pair, and BD13's search for the run holding a matching PDI. Both
// are written to avoid it, and this is what says so — a megabyte of
// mixed-direction text with a bracket pair every few characters, which a
// quadratic implementation would not finish in the life of the test binary.
func TestLinearInParagraphLength(t *testing.T) {
	var b strings.Builder
	for b.Len() < 1<<20 {
		b.WriteString("abc (")
		b.WriteRune(alef)
		b.WriteRune(bet)
		b.WriteString(") 123, ")
		b.WriteRune(gimel)
		b.WriteString(" xyz ")
	}
	text := []rune(b.String())

	start := time.Now()
	p := Resolve(text, LeftToRight)
	Reorder(p.LineLevels(0, len(text)))
	elapsed := time.Since(start)

	// The bound is generous on purpose: the claim is "linear, not quadratic",
	// and a quadratic implementation over a million characters is hours rather
	// than a factor of two. A bound tight enough to catch a constant-factor
	// regression would be a test that fails on a loaded machine.
	if elapsed > 30*time.Second {
		t.Errorf("resolving %d characters took %v, which is not the linear behaviour "+
			"this is written for", len(text), elapsed)
	}
	t.Logf("%d characters in %v", len(text), elapsed)
}

// TestNeededMatchesTheAlgorithm pins the fast path, in both directions.
//
// The scan skips everything below U+0590 without looking it up, which is only
// sound while no character down there is right-to-left, Arabic-numeric or an
// explicit formatting code. That is a fact about the generated table, so it is
// checked against the generated table rather than assumed.
func TestNeededMatchesTheAlgorithm(t *testing.T) {
	for r := rune(0); r < 0x0590; r++ {
		switch ClassOf(r) {
		case R, AL, AN, LRE, RLE, LRO, RLO, PDF, LRI, RLI, FSI, PDI:
			t.Fatalf("U+%04X is below the cut-off Needed relies on and has a class the "+
				"algorithm is needed for; the fast path would skip text that needs it", r)
		}
	}
	for _, s := range []string{"", "hello", "1,234.00", "a (b) c", "naïve — café"} {
		if Needed(s) {
			t.Errorf("Needed(%q) is true; nothing in it needs the algorithm", s)
		}
	}
	for _, s := range []string{string(alef), string(alifArabic), "a‫b", "٠"} {
		if !Needed(s) {
			t.Errorf("Needed(%q) is false, but the text needs the algorithm", s)
		}
	}
	// And what Needed permits skipping really is the identity: a paragraph it
	// says nothing about must resolve to all-zero levels under a left-to-right
	// base direction, or the fast path is dropping work rather than skipping it.
	for _, s := range []string{"hello", "1,234.00", "a (b) c\tand more", "naïve — café"} {
		for _, l := range Resolve([]rune(s), LeftToRight).Levels() {
			if l != 0 {
				t.Fatalf("%q resolves to a non-zero level although Needed says the "+
					"algorithm may be skipped for it", s)
			}
		}
	}
}

// TestLineLevelsResetTrailingWhitespace is rule L1, which is the half of the
// algorithm the conformance suites exercise only with the whole text as one
// line — and the half a layout engine needs per line.
func TestLineLevelsResetTrailingWhitespace(t *testing.T) {
	// A space between two Hebrew words is right-to-left: N1 gives it the
	// direction both sides agree on, and it stays there in the middle of a line.
	text := []rune{alef, ' ', bet}
	line := Resolve(text, LeftToRight).LineLevels(0, 3)
	if line[1] != 1 {
		t.Errorf("the space between two Hebrew words is at level %d, want 1 — L1 "+
			"applies at a line's edges and not inside it (line %v)", line[1], line)
	}

	// A tab is a segment separator, so L1 resets it wherever it falls, and the
	// white space before it with it. Without clause 3 the space would stay
	// right-to-left and sit on the wrong side of the tab stop.
	text = []rune{alef, ' ', '\t', bet}
	p := Resolve(text, LeftToRight)
	if p.Levels()[2] != 1 {
		t.Fatalf("before L1 the tab is at level %d, want 1 — it is a neutral between "+
			"two Hebrew letters, so this test would prove nothing", p.Levels()[2])
	}
	line = p.LineLevels(0, 4)
	if line[2] != 0 {
		t.Errorf("the tab is at level %d, want the paragraph's 0", line[2])
	}
	if line[1] != 0 {
		t.Errorf("the space before the tab is at level %d, want 0 — L1 clause 3 "+
			"resets white space preceding a separator", line[1])
	}
	if line[0] != 1 || line[3] != 1 {
		t.Errorf("L1 moved the letters too: %v", line)
	}
}

// TestLineLevelsAreIndependentPerLine is why the algorithm is split the way it
// is. The same paragraph broken in two places produces different levels for the
// same character, because what is at the end of a line depends on where the line
// ended.
func TestLineLevelsAreIndependentPerLine(t *testing.T) {
	text := []rune{alef, ' ', bet, ' ', gimel}
	p := Resolve(text, LeftToRight)

	whole := p.LineLevels(0, 5)
	if whole[1] != 1 || whole[3] != 1 {
		t.Fatalf("as one line, the interior spaces are at %v", whole)
	}
	first := p.LineLevels(0, 2)
	if first[1] != 0 {
		t.Errorf("as the end of a line, the space is at level %d, want 0", first[1])
	}
}

// TestReorderNestsCorrectly is rule L2 on levels chosen by hand, so that a
// failure points at the reversal and not at everything that produced the levels.
func TestReorderNestsCorrectly(t *testing.T) {
	cases := []struct {
		levels []uint8
		want   []int
	}{
		{[]uint8{0, 0, 0}, []int{0, 1, 2}},
		{[]uint8{1, 1, 1}, []int{2, 1, 0}},
		{[]uint8{0, 0, 1, 1, 0}, []int{0, 1, 3, 2, 4}},
		// A left-to-right island inside a right-to-left run: the island keeps
		// its order and the run around it reverses.
		{[]uint8{1, 2, 2, 1}, []int{3, 1, 2, 0}},
		// And a number inside that island, one level deeper again.
		{[]uint8{1, 2, 3, 2, 1}, []int{4, 1, 2, 3, 0}},
	}
	for _, tc := range cases {
		got := Reorder(tc.levels)
		if len(got) != len(tc.want) {
			t.Fatalf("Reorder(%v) = %v, want %v", tc.levels, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Reorder(%v) = %v, want %v", tc.levels, got, tc.want)
				break
			}
		}
	}
}

func BenchmarkResolve(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 1<<20 {
		sb.WriteString("abc (")
		sb.WriteRune(alef)
		sb.WriteRune(bet)
		sb.WriteString(") 123, ")
		sb.WriteRune(gimel)
		sb.WriteString(" xyz ")
	}
	text := []rune(sb.String())
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := Resolve(text, LeftToRight)
		Reorder(p.LineLevels(0, len(text)))
	}
}
