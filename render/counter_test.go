package render

import (
	"strings"
	"testing"
)

// CSS counters, CSS 2.1 §12.4.
//
// The assertions are on the *text produced*, because that is the only thing a
// counter exists to do and it is the one value a wrong scope rule cannot fake.
// Each case below is built so that one rule decides it: a test that merely
// counted upwards would pass under almost any implementation, since the
// difficult part of counters is not incrementing but knowing which counter is
// being incremented.

// generatedText collects the text of every generated box, in document order.
func generatedText(t *testing.T, htmlSrc, cssSrc string) string {
	t.Helper()
	root := layoutOf(t, 600, htmlSrc, cssSrc)
	var out []string
	var walk func(*Fragment)
	walk = func(f *Fragment) {
		if f == nil {
			return
		}
		for _, line := range f.Lines {
			for _, r := range line.Runs {
				out = append(out, r.Text)
			}
		}
		for _, c := range f.Children {
			walk(c)
		}
	}
	walk(root)
	return strings.Join(out, "|")
}

func TestCounterNumbersInDocumentOrder(t *testing.T) {
	got := generatedText(t, `<ol><li>a</li><li>b</li><li>c</li></ol>`,
		`ol { counter-reset: n } li { counter-increment: n }
		 li::before { content: counter(n) } li { list-style-type: none }`)
	for _, want := range []string{"1", "2", "3"} {
		if !strings.Contains(got, want) {
			t.Errorf("the counters produced %q, which is missing %q", got, want)
		}
	}
}

func TestCounterScopeIsPerResetNotPerName(t *testing.T) {
	// The rule this isolates: counter-reset *creates* a counter that hides any
	// outer one of the same name, rather than assigning to it. So the inner
	// list restarts and — this is the part a naive implementation gets wrong —
	// the outer list carries on from where it was when it left.
	//
	// An implementation with one value per name numbers the second outer item
	// 3, because the inner list advanced the same counter.
	got := generatedText(t,
		`<ol><li>a<ol><li>x</li><li>y</li></ol></li><li>b</li></ol>`,
		`ol { counter-reset: n } li { counter-increment: n }
		 li::before { content: counter(n) } li { list-style-type: none }`)
	// Outer: 1 then 2. Inner: 1 then 2. So "2" twice and no "3" anywhere.
	if strings.Contains(got, "3") {
		t.Errorf("the nested counters produced %q; a 3 means the inner list "+
			"advanced the outer counter instead of creating its own", got)
	}
}

func TestCounterSurvivesIntoFollowingSiblings(t *testing.T) {
	// A counter created on an element is in scope for its *following siblings*,
	// not just its descendants. An implementation that pushed on entering an
	// element and popped on leaving it would restart at every sibling and
	// produce 1, 1, 1.
	got := generatedText(t, `<div><p>a</p><p>b</p><p>c</p></div>`,
		`div { counter-reset: n } p { counter-increment: n }
		 p::before { content: counter(n) }`)
	if !strings.Contains(got, "3") {
		t.Errorf("three siblings produced %q; without a 3 the counter is being "+
			"restarted at each sibling rather than shared across the scope", got)
	}
}

func TestCountersJoinsEveryLevel(t *testing.T) {
	// counters() is the reason the state is a stack rather than a value: it
	// renders every counter of the name that is alive, outermost first. This is
	// the only test here that a single-value implementation cannot pass by
	// accident at any nesting depth.
	got := generatedText(t,
		`<ol><li>a<ol><li>x</li></ol></li></ol>`,
		`ol { counter-reset: n } li { counter-increment: n }
		 li::before { content: counters(n, ".") } li { list-style-type: none }`)
	if !strings.Contains(got, "1.1") {
		t.Errorf("a nested item produced %q, want it to contain \"1.1\"", got)
	}
}

func TestCounterIncrementWithoutResetStartsAtOne(t *testing.T) {
	// §12.4.3: incrementing a counter that is not in scope creates it at zero
	// first. A document that forgets its counter-reset still numbers.
	got := generatedText(t, `<p>a</p><p>b</p>`,
		`p { counter-increment: n } p::before { content: counter(n) }`)
	if !strings.Contains(got, "1") || !strings.Contains(got, "2") {
		t.Errorf("counting with no reset produced %q, want 1 and 2", got)
	}
}

func TestCounterResetTakesItsValue(t *testing.T) {
	// "counter-reset: n 5" then one increment is 6, and the reset happens
	// before the increment however the declarations are ordered.
	got := generatedText(t, `<p>a</p>`,
		`p { counter-increment: n; counter-reset: n 5 }
		 p::before { content: counter(n) }`)
	if !strings.Contains(got, "6") {
		t.Errorf("reset to 5 then incremented produced %q, want 6", got)
	}
}

func TestCounterStyleIsShared(t *testing.T) {
	// counter(n, upper-roman) is the same numbering as list-style-type, and the
	// two must not drift.
	got := generatedText(t, `<p>a</p><p>b</p><p>c</p><p>d</p>`,
		`p { counter-increment: n } p::before { content: counter(n, upper-roman) }`)
	if !strings.Contains(got, "IV") {
		t.Errorf("the fourth item produced %q, want it to contain \"IV\"", got)
	}
}

func TestCounterIncrementSaturates(t *testing.T) {
	// The increment is a number out of an untrusted document, and the value is
	// clamped rather than allowed to run.
	//
	// The first version of this test used two increments of two billion and
	// asserted only that no minus sign appeared. It passed with the clamp
	// deleted, because Go's int is 64 bits here and two billion twice is
	// nowhere near it — the platform's word size explained the result, not the
	// code. So the assertion is now on the clamp itself, which nothing else can
	// produce.
	got := generatedText(t, `<p>a</p>`,
		`p { counter-increment: n 3000000000 } p::before { content: counter(n) }`)
	if !strings.Contains(got, "2147483647") {
		t.Errorf("an increment past the clamp produced %q, want it clamped to "+
			"2147483647", got)
	}

	// And the case the clamp exists for: a sum that would overflow the
	// accumulator rather than merely exceed the clamp.
	got = generatedText(t, `<p>a</p><p>b</p>`,
		`p { counter-increment: n 9000000000000000000 } p::before { content: counter(n) }`)
	if strings.Contains(got, "-") {
		t.Errorf("two increments of nine quintillion produced %q; the sum wrapped", got)
	}
}

func TestDeeplyNestedCountersStayBounded(t *testing.T) {
	// The stack grows with nesting, and nesting comes from the document. This
	// is not asserting a number, only that a deep document is laid out at all
	// rather than exhausting memory or running away.
	var b strings.Builder
	const depth = 300
	for i := 0; i < depth; i++ {
		b.WriteString(`<div>`)
	}
	b.WriteString(`x`)
	for i := 0; i < depth; i++ {
		b.WriteString(`</div>`)
	}
	got := generatedText(t, b.String(),
		`div { counter-reset: n } div::before { content: counters(n, ".") }`)
	if got == "" {
		t.Error("a deeply nested document produced no text at all")
	}
}

// TestPseudoElementCounterIsInItsOwnScope pins §12.4.1's tree scope over the one
// place it is easy to lose: a pseudo-element is a *child* of its element, so a
// counter it creates is nested inside the element's own and dies with it.
//
// The case is the suite's counters-root test reduced to the rule it turns on. If
// the ::before shares its element's scope, its counter-reset overwrites the
// element's counter instead of nesting inside it, and the document numbers from
// 19998 rather than from 4 — a number a plausible implementation produces and
// nothing else does.
func TestPseudoElementCounterIsInItsOwnScope(t *testing.T) {
	got := generatedText(t, `<div id="outer"><div id="inner"></div></div>`, `
		#outer { counter-reset: c 4 }
		#outer::before { content: "["; counter-reset: c 9999; counter-increment: c 9999 }
		#inner { counter-reset: c 8 }
		#inner::before { content: counters(c, ".") }`)
	if !strings.Contains(got, "4.8") {
		t.Errorf("the counters produced %q, want the outer 4 and the inner 8 as "+
			"\"4.8\" — the pseudo-element's 9999 belongs to a scope of its own", got)
	}
	if strings.Contains(got, "19998") {
		t.Errorf("the ::before's counter-reset overwrote its element's: %q", got)
	}
}

// TestPseudoElementCounterNeedsABox pins the other half of §12.4.1: a
// pseudo-element that generates no box cannot increment anything.
//
// The initial value of "content" is "normal", so every element in every document
// carries a ::before that must do nothing at all. A rule that sets only
// counter-increment on one is a rule that changes no number — which reads as a
// mistake in the stylesheet and is exactly what the specification says.
func TestPseudoElementCounterNeedsABox(t *testing.T) {
	got := generatedText(t, `<div><span id="one"></span><span id="two"></span></div>`, `
		div { counter-reset: c }
		#one::before { counter-increment: c }
		#two::before { content: counter(c) }`)
	if !strings.Contains(got, "0") {
		t.Errorf("the counter read %q; a ::before with no content generates no box "+
			"and so increments nothing, leaving it at 0", got)
	}

	// And the same ::before with content does increment, which is what makes the
	// assertion above about the box rather than about the declaration being
	// ignored altogether.
	got = generatedText(t, `<div><span id="one"></span><span id="two"></span></div>`, `
		div { counter-reset: c }
		#one::before { content: ""; counter-increment: c }
		#two::before { content: counter(c) }`)
	if !strings.Contains(got, "1") {
		t.Errorf("the counter read %q; a ::before that does generate a box increments", got)
	}
}

// TestDisplayNoneCannotCount pins §12.4.1's other exclusion. An element that is
// not in the formatting structure has nothing to number.
func TestDisplayNoneCannotCount(t *testing.T) {
	got := generatedText(t, `<div><span id="one"></span><span id="two"></span></div>`, `
		div { counter-reset: c }
		#one { display: none; counter-increment: c }
		#two::before { content: counter(c) }`)
	if !strings.Contains(got, "0") {
		t.Errorf("the counter read %q; a display:none element cannot increment", got)
	}

	// Its subtree is out too, and this is the case that separates "skipped" from
	// "skipped along with everything inside it".
	got = generatedText(t, `<div><span id="one"><b></b></span><span id="two"></span></div>`, `
		div { counter-reset: c }
		#one { display: none }
		#one b { counter-increment: c }
		#two::before { content: counter(c) }`)
	if !strings.Contains(got, "0") {
		t.Errorf("the counter read %q; nothing inside a display:none element counts either", got)
	}

	// visibility:hidden is the value that does not exclude anything: the box is
	// laid out and takes its room, it is simply not painted. Without this the
	// rule above could be written on the wrong property and nothing would say so.
	got = generatedText(t, `<div><span id="one"></span><span id="two"></span></div>`, `
		div { counter-reset: c }
		#one { visibility: hidden; counter-increment: c }
		#two::before { content: counter(c) }`)
	if !strings.Contains(got, "1") {
		t.Errorf("the counter read %q; a hidden box is still laid out and still counts", got)
	}
}

// TestMarkerDoesNotSeeItsOwnBeforeCounter pins the ordering the element's own
// snapshot has to be taken at: ::before comes after the element in document
// order, so a counter it moves cannot reach the marker in front of it.
func TestMarkerDoesNotSeeItsOwnBeforeCounter(t *testing.T) {
	// Numbered inside, so that the marker is a run on the line and generatedText
	// can see it. Which side of the box it is drawn on decides nothing here.
	got := generatedText(t, `<ol><li></li></ol>`, `
		ol { list-style-type: decimal }
		li { list-style-position: inside }
		li::before { content: "["; counter-increment: list-item 10 }`)
	if !strings.Contains(got, "1.") {
		t.Errorf("the marker read %q, want \"1.\" — the ::before's increment is "+
			"after it in document order", got)
	}
}
