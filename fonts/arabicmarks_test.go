package fonts

import (
	"strings"
	"testing"
)

// The order Arabic writes its marks in.
//
// Canonical ordering sorts a letter's marks by combining class, and for Arabic
// that produces an order nobody writes. A hamza is not a vowel — it is part of
// how the letter is spelled — so it belongs against the letter, under or over
// any vowel that is also there. Its class is 220 or 230, far above a vowel's 27
// to 34, so sorting by class alone puts it outside the vowel and the two are
// drawn stacked the wrong way round.
//
// Unicode states the correction in UTR #53. This is the test that it happens for
// Arabic and, just as importantly, that it does not happen for anything else.

// marksOf normalises a string and returns what came out, so a test can assert
// on the order rather than on the glyphs a particular font would draw.
func marksOf(t *testing.T, s string, arabic bool) []rune {
	t.Helper()
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	runes := []rune(s)
	offsets := make([]int, len(runes))
	for i := range offsets {
		offsets[i] = i
	}
	out, _ := f.normalize(runes, offsets, false, false, arabic)
	return out
}

func describeCodepoints(rs []rune) string {
	var b strings.Builder
	for i, r := range rs {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("U+")
		const hex = "0123456789ABCDEF"
		for shift := 12; shift >= 0; shift -= 4 {
			if r>>uint(shift) != 0 || shift <= 8 {
				b.WriteByte(hex[(r>>uint(shift))&0xF])
			}
		}
	}
	return b.String()
}

// TestAHamzaComesBeforeTheVowelInArabic is the rule, stated as a test.
//
// The expected orders are HarfBuzz's, measured over Noto Sans Arabic: for waw +
// hamza-below + fatha it produces the hamza glyph before the fatha, which is the
// reverse of what canonical ordering leaves.
func TestAHamzaComesBeforeTheVowelInArabic(t *testing.T) {
	const (
		waw        = 0x0648
		hamzaBelow = 0x0655 // combining class 220
		hamzaAbove = 0x0654 // combining class 230
		fatha      = 0x064E // combining class 30
		damma      = 0x064F // combining class 30
		shadda     = 0x0651 // combining class 33
	)
	for _, tc := range []struct {
		in, want []rune
		why      string
	}{
		{
			[]rune{waw, hamzaBelow, fatha}, []rune{waw, hamzaBelow, fatha},
			"a hamza below and a vowel, written hamza first",
		},
		{
			[]rune{waw, fatha, hamzaBelow}, []rune{waw, hamzaBelow, fatha},
			"the same two written the other way round: the hamza still comes first",
		},
		{
			[]rune{waw, fatha, hamzaAbove}, []rune{waw, hamzaAbove, fatha},
			"a hamza above and a vowel",
		},
		{
			[]rune{waw, shadda, fatha, hamzaBelow}, []rune{waw, hamzaBelow, shadda, fatha},
			"a hamza, a shadda and a vowel: only the hamza moves",
		},
		{
			[]rune{waw, fatha, hamzaAbove, hamzaBelow},
			[]rune{waw, hamzaBelow, hamzaAbove, fatha},
			"both hamzas: below is innermost, then above, then the vowel",
		},
		{
			[]rune{waw, fatha, damma}, []rune{waw, fatha, damma},
			"two vowels and no hamza: canonical order stands",
		},
	} {
		got := marksOf(t, string(tc.in), true)
		if !sameRunes(got, tc.want) {
			t.Errorf("%s:\n  %s\n  became %s\n  want   %s", tc.why,
				describeCodepoints(tc.in), describeCodepoints(got), describeCodepoints(tc.want))
		}
	}
}

// TestTheArabicMarkOrderIsArabicsAlone is the other half, and the reason the
// rule is gated on the script rather than applied everywhere.
//
// In Latin the same two combining classes mean the opposite thing: a dot below
// at 220 and an acute at 230 are ordinary marks whose canonical order is the
// order they are drawn in, innermost first. Hebrew is where it would do real
// damage — its points sit at classes 10 to 26, below both, so the rule would
// pull a mark in front of a point exactly as it pulls a hamza in front of a
// vowel, and the word would be pointed wrongly.
//
// The Latin corpus in testdata/harfbuzz cannot catch this: a canonical sort
// already leaves 220 before 230, so for Latin the rule is a no-op and applying
// it everywhere would look harmless. That is why the gate is asserted here, on
// the script each run is actually given.
func TestTheArabicMarkOrderIsArabicsAlone(t *testing.T) {
	const (
		dotBelow = 0x0323 // combining class 220
		grave    = 0x0300 // combining class 230
		hiriq    = 0x05B4 // a Hebrew point, combining class 14
		hebLamed = 0x05DC
	)
	loadBearing := 0
	for _, tc := range []struct {
		in  []rune
		why string
	}{
		{[]rune{'o', dotBelow, grave}, "a dot below and a grave on a Latin letter"},
		{[]rune{'o', grave, dotBelow}, "the same two the other way round"},
		{[]rune{hebLamed, hiriq, dotBelow}, "a Hebrew point and a mark below"},
	} {
		// The gate as the pipeline computes it: the run's own script.
		if scriptSelects(runScript(string(tc.in)), "arab") {
			t.Errorf("%s: this run is treated as Arabic, so the rule reaches it", tc.why)
			continue
		}
		// Which is what makes it come out in plain canonical order.
		canonical := marksOf(t, string(tc.in), false)
		forced := marksOf(t, string(tc.in), true)
		if !sameRunes(canonical, forced) {
			// This case proves the gate is load-bearing rather than decorative:
			// the rule *would* have changed it.
			loadBearing++
			t.Logf("%s: the rule would have reordered this to %s; the gate is what stops it",
				tc.why, describeCodepoints(forced))
		}
	}
	if loadBearing == 0 {
		t.Error("none of these runs would have been changed by the Arabic rule, so this " +
			"proves nothing about the gate — find a case where it would.")
	}
}

// TestTheArabicMarkOrderIsAppliedAtAll guards the gate itself.
//
// The rule is switched on by the run's script, and a gate that never opens looks
// exactly like a rule that never fires. This asserts the two answers differ for
// a run where they should — so a change that stopped selecting Arabic would fail
// here rather than pass quietly.
func TestTheArabicMarkOrderIsAppliedAtAll(t *testing.T) {
	in := []rune{0x0648, 0x064E, 0x0655} // waw, fatha, hamza below
	plain := marksOf(t, string(in), false)
	arabic := marksOf(t, string(in), true)
	if sameRunes(plain, arabic) {
		t.Fatalf("the Arabic ordering changed nothing: %s stayed %s either way",
			describeCodepoints(in), describeCodepoints(plain))
	}
	// And the run really does reach it through shaping, not only when the flag
	// is passed by hand.
	f, err := NotoSans()
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if !scriptSelects(runScript(string(in)), "arab") {
		t.Errorf("a run of Arabic characters is not recognised as Arabic, so the rule "+
			"would never be reached: runScript gave %v", scriptTags(runScript(string(in))))
	}
	_ = f
}
