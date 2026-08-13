package style

import (
	"math"
	"testing"

	"github.com/mgilbir/pdf0/css"
)

// Layout units and lengths.
//
// There is no external suite for this, and the faults it is guarding against are
// the quietest in the engine: a unit conversion wrong in the third decimal, or
// an arithmetic that wraps instead of saturating. Neither produces an error —
// both produce a page that is subtly or spectacularly wrong with nothing to say
// why. So the assertions are about *exact* values wherever the specification
// gives one, and about the sign and ordering wherever it does not.

// TestAbsoluteUnitsAreExact pins the conversions CSS fixes by definition. Every
// one of these is a ratio the specification states, so an approximation here is
// a document printed at the wrong size.
func TestAbsoluteUnitsAreExact(t *testing.T) {
	ctx := LengthContext{}
	cases := map[string]float64{
		// unit -> CSS pixels for one of it
		"1px":  1,
		"1in":  96,
		"1pt":  96.0 / 72,
		"1pc":  16, // 12pt
		"1cm":  96 / 2.54,
		"1mm":  96 / 25.4,
		"1q":   96 / 101.6,
		"1Q":   96 / 101.6,
		"12pt": 16,
		"72pt": 96,
		"6pc":  96,
		"0":    0,
	}
	for input, wantPx := range cases {
		l := mustLength(t, input, ctx)
		if l.Kind != LengthAbsolute {
			t.Errorf("%q is not an absolute length", input)
			continue
		}
		want, _ := FromPx(wantPx)
		if l.Value != want {
			t.Errorf("%q is %v px, want %v px", input, l.Value.Px(), want.Px())
		}
	}

	// And the one conversion that leaves the engine: CSS px is 1/96 inch, a PDF
	// point is 1/72, so an inch is 72 points however it was written.
	for _, input := range []string{"1in", "96px", "72pt", "6pc", "2.54cm", "25.4mm"} {
		l := mustLength(t, input, ctx)
		if got := l.Value.Pt(); math.Abs(got-72) > 1e-9 {
			t.Errorf("%q is %v pt, want 72", input, got)
		}
	}
}

func mustLength(t *testing.T, input string, ctx LengthContext) Length {
	t.Helper()
	vals, _ := css.ParseComponentValues(input)
	l, unsupported, ok := ParseLength(vals, ctx)
	if !ok {
		t.Fatalf("%q was not read as a length (unsupported=%v)", input, unsupported)
	}
	return l
}

// TestFontRelativeUnits pins that em uses the element's own size and rem the
// root's — the difference being that em compounds as elements nest and rem does
// not, which is the entire reason both exist.
func TestFontRelativeUnits(t *testing.T) {
	px16, _ := FromPx(16)
	px20, _ := FromPx(20)
	ctx := LengthContext{FontSize: px20, RootFontSize: px16}

	cases := map[string]float64{
		"1em":   20,
		"2em":   40,
		"0.5em": 10,
		"1rem":  16,
		"2rem":  32,
	}
	for input, wantPx := range cases {
		l := mustLength(t, input, ctx)
		want, _ := FromPx(wantPx)
		if l.Value != want {
			t.Errorf("%q with font-size 20px and root 16px is %v px, want %v",
				input, l.Value.Px(), wantPx)
		}
	}
}

// TestViewportUnits pins that a viewport unit is unresolvable rather than zero
// before the page is decided. Resolving it to zero would collapse a box the
// author sized against the page, and nothing about the result would say the
// page had not been known yet.
func TestViewportUnits(t *testing.T) {
	w, _ := FromPx(800)
	h, _ := FromPx(600)
	known := LengthContext{ViewportWidth: w, ViewportHeight: h, ViewportKnown: true}

	cases := map[string]float64{
		"10vw":   80,
		"10vh":   60,
		"10vmin": 60,
		"10vmax": 80,
		"100vw":  800,
	}
	for input, wantPx := range cases {
		l := mustLength(t, input, known)
		want, _ := FromPx(wantPx)
		if l.Value != want {
			t.Errorf("%q in an 800x600 page is %v px, want %v", input, l.Value.Px(), wantPx)
		}
	}

	// Without a page, each is unresolvable and correct CSS, so unsupported
	// rather than malformed.
	for _, input := range []string{"10vw", "10vh", "10vmin", "10vmax"} {
		vals, _ := css.ParseComponentValues(input)
		l, unsupported, ok := ParseLength(vals, LengthContext{})
		if ok {
			t.Errorf("%q resolved to %v with no page known", input, l.Value.Px())
		}
		if !unsupported {
			t.Errorf("%q was reported as malformed; it is correct CSS with nothing to resolve against", input)
		}
	}
}

// TestBareNumbersAreNotLengths pins the rule every browser also enforces: only
// zero may be written without a unit. Accepting "margin: 10" would silently read
// it as ten of something.
func TestBareNumbersAreNotLengths(t *testing.T) {
	ctx := LengthContext{}
	if l := mustLength(t, "0", ctx); l.Value != 0 {
		t.Errorf("\"0\" is %v", l.Value)
	}
	for _, input := range []string{"10", "1.5", "-3", "1e3"} {
		vals, _ := css.ParseComponentValues(input)
		if l, _, ok := ParseLength(vals, ctx); ok {
			t.Errorf("%q was read as the length %v", input, l.Value.Px())
		}
	}
}

// TestUnsupportedUnitsAreDistinguished pins that a unit this engine cannot yet
// resolve is told apart from input that is not a length. The two send an author
// to different places: one is a limit of the renderer, the other is a typo.
func TestUnsupportedUnitsAreDistinguished(t *testing.T) {
	ctx := LengthContext{}
	// Correct CSS this engine does not resolve. "2ch" is here because the
	// context carries no font metrics: it is resolvable in layout, where a face
	// has been chosen, and unresolvable everywhere else — which is the same
	// shape as a viewport unit before the page is known.
	// calc() is deliberately not in this list any more: it is computed here,
	// so it is a length like any other and its own tests are in calc_test.go.
	// A calc *holding* one of these units is still unresolvable, which is the
	// case below.
	for _, input := range []string{"2ch", "1lh", "3rex"} {
		vals, _ := css.ParseComponentValues(input)
		_, unsupported, ok := ParseLength(vals, ctx)
		if ok {
			t.Errorf("%q was resolved, and it needs something not threaded here", input)
			continue
		}
		if !unsupported {
			t.Errorf("%q was reported as malformed, and it is correct CSS", input)
		}
	}
	// A calc() is only as resolvable as what is inside it, and one holding a
	// unit this context cannot answer is refused for that reason rather than
	// silently dropping the term. It is reported as malformed rather than as
	// unsupported, which is the one place calc() is coarser than a bare
	// dimension: the expression as a whole did not come out, and which of its
	// units was the reason is not carried back out.
	for _, input := range []string{"calc(1px + 2ch)", "calc(2 * 1lh)"} {
		vals, _ := css.ParseComponentValues(input)
		if _, _, ok := ParseLength(vals, ctx); ok {
			t.Errorf("%q was resolved without the metrics it needs", input)
		}
	}

	// Not a length at all.
	for _, input := range []string{"", "red", "1px 2px", "\"x\"", "1foo", "url(x)"} {
		vals, _ := css.ParseComponentValues(input)
		_, unsupported, ok := ParseLength(vals, ctx)
		if ok {
			t.Errorf("%q was read as a length", input)
			continue
		}
		if unsupported {
			t.Errorf("%q was reported as unsupported, and it is not a length at all", input)
		}
	}
}

// TestPercentagesStayUnresolved pins that a percentage is carried as one. It
// cannot be resolved without a containing block, and resolving it early against
// the wrong basis is how a width becomes a fraction of the wrong box.
func TestPercentagesStayUnresolved(t *testing.T) {
	l := mustLength(t, "50%", LengthContext{})
	if l.Kind != LengthPercent {
		t.Fatalf("\"50%%\" is kind %v, want a percentage", l.Kind)
	}
	if l.Percent != 50 {
		t.Errorf("\"50%%\" is %v percent", l.Percent)
	}

	basis, _ := FromPx(200)
	got, ok := l.Resolve(basis, true)
	if !ok {
		t.Fatal("a percentage of a definite basis did not resolve")
	}
	if want, _ := FromPx(100); got != want {
		t.Errorf("50%% of 200px is %v px, want 100", got.Px())
	}

	// Against an indefinite basis it stays unresolved rather than becoming zero,
	// which would silently collapse the box.
	if got, ok := l.Resolve(basis, false); ok {
		t.Errorf("a percentage of an indefinite basis resolved to %v px", got.Px())
	}
}

// TestAutoIsNotALength pins that "auto" is carried as an instruction rather than
// as zero — the two differ for every box that has one.
func TestAutoIsNotALength(t *testing.T) {
	l := mustLength(t, "auto", LengthContext{})
	if l.Kind != LengthAuto {
		t.Fatalf("\"auto\" is kind %v", l.Kind)
	}
	if _, ok := l.Resolve(1000, true); ok {
		t.Error("\"auto\" resolved to a number; only layout can decide it")
	}
}

// TestArithmeticSaturates is the property that keeps a hostile stylesheet from
// producing a plausible page. A width that wrapped into a negative number does
// not look like an error — it looks like a box laid out inside-out, and every
// consequence of it is plausible.
func TestArithmeticSaturates(t *testing.T) {
	if got := MaxUnit.Add(MaxUnit); got != MaxUnit {
		t.Errorf("MaxUnit + MaxUnit is %d, want MaxUnit", got)
	}
	if got := MinUnit.Add(MinUnit); got != MinUnit {
		t.Errorf("MinUnit + MinUnit is %d, want MinUnit", got)
	}
	if got := MinUnit.Sub(MaxUnit); got != MinUnit {
		t.Errorf("MinUnit - MaxUnit is %d, want MinUnit", got)
	}
	if got := MaxUnit.Sub(MinUnit); got != MaxUnit {
		t.Errorf("MaxUnit - MinUnit is %d, want MaxUnit", got)
	}
	if got := MaxUnit.Mul(1000); got != MaxUnit {
		t.Errorf("MaxUnit * 1000 is %d, want MaxUnit", got)
	}
	if got := MaxUnit.Mul(-1000); got != MinUnit {
		t.Errorf("MaxUnit * -1000 is %d, want MinUnit", got)
	}
	// Ordinary arithmetic still works, so saturation is not simply pinning
	// everything to the ends.
	a, _ := FromPx(10)
	b, _ := FromPx(2.5)
	if want, _ := FromPx(12.5); a.Add(b) != want {
		t.Errorf("10px + 2.5px is %v", a.Add(b).Px())
	}
	if want, _ := FromPx(7.5); a.Sub(b) != want {
		t.Errorf("10px - 2.5px is %v", a.Sub(b).Px())
	}
	if want, _ := FromPx(5); a.Mul(0.5) != want {
		t.Errorf("10px * 0.5 is %v", a.Mul(0.5).Px())
	}
	if want, _ := FromPx(4); a.Div(2.5) != want {
		t.Errorf("10px / 2.5 is %v", a.Div(2.5).Px())
	}
}

// TestNoInfinitiesOrNaNs pins that neither ever enters the unit system. An
// infinite length is not a very large one and a NaN is not a small one — both
// are values that make every later comparison meaningless, so a box laid out
// with one is not wrong in a bounded way, it is unordered.
func TestNoInfinitiesOrNaNs(t *testing.T) {
	if u, ok := FromPx(math.NaN()); ok || u != 0 {
		t.Errorf("a NaN length gave %d, ok=%v", u, ok)
	}
	if u, ok := FromPx(math.Inf(1)); ok || u != MaxUnit {
		t.Errorf("an infinite length gave %d, ok=%v", u, ok)
	}
	if u, ok := FromPx(math.Inf(-1)); ok || u != MinUnit {
		t.Errorf("a negative infinite length gave %d, ok=%v", u, ok)
	}

	a, _ := FromPx(10)
	if got := a.Mul(math.NaN()); got != 0 {
		t.Errorf("multiplying by NaN gave %d", got)
	}
	// Dividing by zero saturates in the direction of the sign rather than
	// producing an infinity.
	if got := a.Div(0); got != MaxUnit {
		t.Errorf("10px / 0 is %d, want MaxUnit", got)
	}
	if got := a.Mul(-1).Div(0); got != MinUnit {
		t.Errorf("-10px / 0 is %d, want MinUnit", got)
	}
	if got := Unit(0).Div(0); got != 0 {
		t.Errorf("0 / 0 is %d, want 0", got)
	}
}

// TestOversizedLengthsAreReported pins that a length past the range is
// saturated *and* reported. Saturating quietly would lay out one enormous box
// with no explanation.
func TestOversizedLengthsAreReported(t *testing.T) {
	for _, input := range []string{"1e9px", "1e12pt", "-1e9px", "99999999in"} {
		vals, _ := css.ParseComponentValues(input)
		l, unsupported, ok := ParseLength(vals, LengthContext{})
		if ok {
			t.Errorf("%q was accepted as %v px", input, l.Value.Px())
		}
		if unsupported {
			t.Errorf("%q was reported as unsupported; it is a length, just an impossible one", input)
		}
		// And what it saturated to is at an end of the range, not wrapped.
		if l.Value != MaxUnit && l.Value != MinUnit {
			t.Errorf("%q saturated to %d, which is not an end of the range", input, l.Value)
		}
	}
}

// TestClampPrefersTheMinimum pins the CSS rule that a minimum wins over a
// maximum when the two contradict each other.
func TestClampPrefersTheMinimum(t *testing.T) {
	lo, _ := FromPx(100)
	hi, _ := FromPx(50)
	v, _ := FromPx(75)
	if got := Clamp(v, lo, hi); got != lo {
		t.Errorf("clamping 75 to [100, 50] gave %v px, want 100", got.Px())
	}
	// And the ordinary case.
	lo2, _ := FromPx(10)
	hi2, _ := FromPx(20)
	for _, tc := range []struct{ in, want float64 }{{5, 10}, {15, 15}, {25, 20}} {
		in, _ := FromPx(tc.in)
		want, _ := FromPx(tc.want)
		if got := Clamp(in, lo2, hi2); got != want {
			t.Errorf("clamping %v to [10, 20] gave %v", tc.in, got.Px())
		}
	}
}

// TestFontSizeKeywordsAndRelatives pins the scale, which is not geometric — the
// specification's ratios are irregular, and inventing a series would make
// "small" and "large" the wrong sizes in a document that names them.
func TestFontSizeKeywordsAndRelatives(t *testing.T) {
	root, _ := FromPx(16)
	parent, _ := FromPx(20)

	cases := map[string]float64{
		"medium":   16,
		"small":    13,
		"large":    18,
		"xx-small": 9,
		"xx-large": 32,
		// Relative to the parent, not to the root.
		"larger":  24,
		"smaller": 20 / 1.2,
		// A percentage and an em are both relative to the parent, because the
		// element's own size is what is being computed.
		"150%": 30,
		"2em":  40,
		"1rem": 16,
		"24px": 24,
	}
	for input, wantPx := range cases {
		vals, _ := css.ParseComponentValues(input)
		got, unsupported, ok := ResolveFontSize(vals, parent, root)
		if !ok {
			t.Errorf("font-size %q was not resolved (unsupported=%v)", input, unsupported)
			continue
		}
		want, _ := FromPx(wantPx)
		if got != want {
			t.Errorf("font-size %q with parent 20px is %v px, want %v", input, got.Px(), wantPx)
		}
	}

	// A negative font size is not a small one; the specification forbids it.
	for _, input := range []string{"-1px", "-1em", "-50%"} {
		vals, _ := css.ParseComponentValues(input)
		if got, _, ok := ResolveFontSize(vals, parent, root); ok {
			t.Errorf("font-size %q resolved to %v px", input, got.Px())
		}
	}
}

// TestFontSizeEmIsRelativeToTheParent is the rule that makes nested ems compound,
// and the one most easily got wrong by resolving against the element's own size —
// which would be circular, and in practice gives every element the same size.
func TestFontSizeEmIsRelativeToTheParent(t *testing.T) {
	root, _ := FromPx(16)
	vals, _ := css.ParseComponentValues("2em")

	// Three nested elements each doubling: 16, 32, 64, 128.
	size := root
	for i, wantPx := range []float64{32, 64, 128} {
		got, _, ok := ResolveFontSize(vals, size, root)
		if !ok {
			t.Fatalf("level %d did not resolve", i)
		}
		want, _ := FromPx(wantPx)
		if got != want {
			t.Fatalf("level %d is %v px, want %v — em did not compound", i, got.Px(), wantPx)
		}
		size = got
	}

	// rem does not compound, which is the whole reason it exists.
	remVals, _ := css.ParseComponentValues("2rem")
	size = root
	for i := 0; i < 3; i++ {
		got, _, _ := ResolveFontSize(remVals, size, root)
		want, _ := FromPx(32)
		if got != want {
			t.Fatalf("level %d of 2rem is %v px, want 32 every time", i, got.Px())
		}
		size = got
	}
}

// TestUnitRoundTrip pins that a length survives conversion out and back, which
// is what paint does at the boundary.
func TestUnitRoundTrip(t *testing.T) {
	for _, px := range []float64{0, 1, 0.5, 1.0 / 64, 123.456, -7.25, 1000} {
		u, ok := FromPx(px)
		if !ok {
			t.Errorf("%v px did not fit", px)
			continue
		}
		// 1/64 px is the resolution, so anything expressible in it is exact.
		if back := u.Px(); math.Abs(back-px) > 1.0/64 {
			t.Errorf("%v px round-tripped to %v", px, back)
		}
	}
	// And the resolution really is 1/64: a smaller difference is not
	// representable, which is the trade fixed point makes.
	a, _ := FromPx(1)
	b, _ := FromPx(1 + 1.0/64)
	if a == b {
		t.Error("1px and 1+1/64px are the same value; the resolution is coarser than claimed")
	}
	c, _ := FromPx(1 + 1.0/256)
	if a != c {
		t.Error("1px and 1+1/256px differ; the resolution is finer than claimed")
	}
}

// TestFontRelativeUnitsResolve pins the two font-relative units this engine
// does resolve, and the condition each needs.
func TestFontRelativeUnitsResolve(t *testing.T) {
	px := func(v float64) Unit { u, _ := FromPx(v); return u }

	// ex needs only the font size: CSS Values §5.1.2 specifies half an em where
	// the x-height cannot be determined, and the face layer carries none.
	ctx := LengthContext{FontSize: px(20)}
	vals, _ := css.ParseComponentValues("3ex")
	got, _, ok := ParseLength(vals, ctx)
	if !ok || got.Value != px(30) {
		t.Errorf("3ex at a 20px font size gave (%v, ok=%v), want 30px", got.Value.Px(), ok)
	}

	// ch needs the advance of "0", and says so rather than resolving to zero
	// when it does not have one — a zero would silently collapse the box the
	// author was sizing.
	vals, _ = css.ParseComponentValues("10ch")
	if _, unsupported, ok := ParseLength(vals, ctx); ok || !unsupported {
		t.Errorf("10ch without metrics gave (unsupported=%v, ok=%v), want it reported", unsupported, ok)
	}
	ctx.ZeroAdvance, ctx.FontMetricsKnown = px(12), true
	got, _, ok = ParseLength(vals, ctx)
	if !ok || got.Value != px(120) {
		t.Errorf("10ch with a 12px advance gave (%v, ok=%v), want 120px", got.Value.Px(), ok)
	}
}
