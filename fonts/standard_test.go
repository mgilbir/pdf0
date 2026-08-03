package fonts

import (
	"strings"
	"testing"
)

// TestStandardMetricsAreAdobes pins a handful of published widths. They are
// generated from Adobe's AFM files, so this is checking that the generation and
// the lookup path agree with the source — a width silently wrong by a few units
// mis-sets every line and nothing else here would notice, because there is no
// program to check it against.
func TestStandardMetricsAreAdobes(t *testing.T) {
	cases := []struct {
		font string
		r    rune
		want float64
	}{
		{"Helvetica", ' ', 278},
		{"Helvetica", 'A', 667},
		{"Helvetica", 'i', 222},
		{"Helvetica-Bold", 'A', 722},
		{"Times-Roman", 'A', 722},
		{"Times-Roman", 'i', 278},
		{"Courier", 'm', 600}, // monospaced: every glyph is 600
		{"Courier", 'i', 600},
		{"Courier-Bold", 'W', 600},
	}
	for _, c := range cases {
		f, err := Standard(c.font)
		if err != nil {
			t.Fatalf("%s: %v", c.font, err)
		}
		got, ok := f.Advance(c.r)
		if !ok {
			t.Errorf("%s has no width for %q", c.font, c.r)
			continue
		}
		if got != c.want {
			t.Errorf("%s advance(%q) = %v, want %v", c.font, c.r, got, c.want)
		}
	}
}

// TestAllFourteenAreThere pins the set. A reader is required to have exactly
// these, and a caller naming one this package lacks would get an error for a
// font that is in fact always available.
func TestAllFourteenAreThere(t *testing.T) {
	names := StandardNames()
	if len(names) != 14 {
		t.Errorf("StandardNames returned %d fonts, want 14: %v", len(names), names)
	}
	for _, n := range names {
		f, err := Standard(n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if !f.IsStandard() {
			t.Errorf("%s does not report itself as a standard face", n)
		}
		if _, err := f.Subset(); err == nil {
			t.Errorf("%s claimed to subset a font with no program", n)
		}
	}
	if _, err := Standard("Comic Sans"); err == nil {
		t.Error("a font outside the fourteen was accepted")
	}
}

// TestStandardEncodesOneBytePerCharacter pins the encoding difference that
// matters. An embedded face is Identity-H and writes two bytes per glyph; a
// standard face is a character encoding and writes one. A caller mixing them up
// produces text that renders as nonsense.
func TestStandardEncodesOneBytePerCharacter(t *testing.T) {
	f, err := Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	codes, missing := f.Encode("Hi!")
	if missing != 0 {
		t.Errorf("missing = %d, want 0", missing)
	}
	if string(codes) != "Hi!" {
		t.Errorf("Encode(%q) = %q, want the same bytes: WinAnsi is ASCII here", "Hi!", codes)
	}
	// A character outside WinAnsi is reported and set as a space, rather than
	// silently dropped or turned into a code that means something else.
	codes, missing = f.Encode("a中b")
	if missing != 1 {
		t.Errorf("missing = %d, want 1", missing)
	}
	if string(codes) != "a b" {
		t.Errorf("Encode = %q, want %q", codes, "a b")
	}
}

// TestStandardCoversWinAnsiAccents pins that the encoding really is WinAnsi and
// not ASCII: the accented Latin letters a European document needs must have
// codes and widths.
func TestStandardCoversWinAnsiAccents(t *testing.T) {
	f, err := Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range "éàüñçøÆ€" {
		if _, ok := f.Advance(r); !ok {
			t.Errorf("no width for %q, which WinAnsiEncoding covers", r)
		}
	}
	if _, missing := f.Encode("café"); missing != 0 {
		t.Errorf("café reported %d missing characters", missing)
	}
}

// TestMeasureMatchesTheSumOfAdvances pins that measuring a string is measuring
// its characters — the property a line-breaker depends on.
func TestMeasureMatchesTheSumOfAdvances(t *testing.T) {
	f, err := Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	const s = "The quick brown fox"
	var sum float64
	for _, r := range s {
		w, _ := f.Advance(r)
		sum += w
	}
	if got, want := f.Measure(s, 12), sum*12/1000; got != want {
		t.Errorf("Measure = %v, want %v", got, want)
	}
	// And a longer string is wider, which is the property that makes wrapping
	// terminate.
	if f.Measure(s+" jumps", 12) <= f.Measure(s, 12) {
		t.Error("adding words did not make the line wider")
	}
}

// TestStandardIsNotForPDFA records the constraint in the place a caller looks.
// A standard face embeds no program, and a conforming document may not show a
// font it does not embed.
func TestStandardIsNotForPDFA(t *testing.T) {
	f, err := Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsStandard() {
		t.Fatal("IsStandard is what a caller checks before drawing; it must be true here")
	}
	if !strings.Contains(f.Name(), "Helvetica") {
		t.Errorf("Name = %q", f.Name())
	}
}
