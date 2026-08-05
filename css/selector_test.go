package css

import (
	"strings"
	"testing"
)

// Selectors. There is no external suite for this layer — the CSS parsing tests
// stop at syntax — so unlike the tokenizer and the parser these tests carry the
// weight alone, and are written to be worth that.
//
// Two things follow. Every assertion is about a *consequence*: not "this parses"
// but "this selects a different set of elements than that", because a selector
// that parses into the wrong shape is indistinguishable from a correct one until
// something is matched against it. And the refusals are tested as carefully as
// the acceptances, since the subset boundary is the whole design.

func parseSel(t *testing.T, input string) ([]Selector, []Error, bool) {
	t.Helper()
	// Tokenizer diagnostics are deliberately not fatal here. An input such as
	// "[" is malformed at both layers, and a helper that refused to reach the
	// selector parser could not test what it does with one.
	vals, _ := ParseComponentValues(input)
	return ParseSelectorList(vals)
}

// mustParse parses a selector list that is expected to be entirely usable.
func mustParse(t *testing.T, input string) []Selector {
	t.Helper()
	sels, errs, ok := parseSel(t, input)
	if !ok {
		t.Fatalf("%q was refused: %v", input, errs)
	}
	if len(errs) != 0 {
		t.Fatalf("%q parsed but reported %v", input, errs)
	}
	return sels
}

// sketch renders a selector compactly, so a table reads as the thing asserted.
func sketch(s Selector) string {
	var b strings.Builder
	for i, c := range s.Compounds {
		if i > 0 {
			b.WriteString(" " + strings.TrimSpace(c.Combinator.String()) + " ")
			if c.Combinator == Descendant {
				// A descendant combinator renders as a single space, and
				// TrimSpace ate it.
				b.WriteString("")
			}
		}
		if c.Universal {
			b.WriteString("*")
		}
		b.WriteString(c.Type)
		for _, id := range c.IDs {
			b.WriteString("#" + id)
		}
		for _, cl := range c.Classes {
			b.WriteString("." + cl)
		}
		for _, a := range c.Attrs {
			b.WriteString("[" + a.Name + a.Op.String())
			if a.Op != AttrExists {
				b.WriteString(a.Value)
				if a.Insensitive {
					b.WriteString(" i")
				}
			}
			b.WriteString("]")
		}
		for _, ps := range c.Pseudos {
			b.WriteString(":" + ps.Name)
		}
	}
	if s.PseudoElement != "" {
		b.WriteString("::" + s.PseudoElement)
	}
	return b.String()
}

// TestSelectorShapes pins that the parts of a selector land where they belong.
// Each of these would still "parse" if compounds were split wrongly, and each
// would then select something different.
func TestSelectorShapes(t *testing.T) {
	cases := map[string]string{
		"a":                 "a",
		"*":                 "*",
		".c":                ".c",
		"#i":                "#i",
		"a.c":               "a.c",
		"a.c.d":             "a.c.d",
		"a#i.c":             "a#i.c",
		"a b":               "a  b",
		"a>b":               "a > b",
		"a > b":             "a > b",
		"a  >  b":           "a > b",
		"a+b":               "a + b",
		"a ~ b":             "a ~ b",
		"a b c":             "a  b  c",
		"a > b c":           "a > b  c",
		"[href]":            "[href]",
		"[href=x]":          "[href=x]",
		`[href="x"]`:        "[href=x]",
		"[href~=x]":         "[href~=x]",
		"[href|=x]":         "[href|=x]",
		"[href^=x]":         "[href^=x]",
		"[href$=x]":         "[href$=x]",
		"[href*=x]":         "[href*=x]",
		"[href=x i]":        "[href=x i]",
		"[href=x s]":        "[href=x]",
		"a[href][b=c]":      "a[href][b=c]",
		"a:first-child":     "a:first-child",
		"a:nth-child(2n+1)": "a:nth-child",
		"a::before":         "a::before",
		"a:before":          "a::before",
		"*::after":          "*::after",
		":root":             ":root",
		"a:not(.c)":         "a:not",
		"a:is(.c,.d)":       "a:is",
	}
	for input, want := range cases {
		sels := mustParse(t, input)
		if len(sels) != 1 {
			t.Errorf("%q gave %d selectors, want 1", input, len(sels))
			continue
		}
		if got := sketch(sels[0]); got != want {
			t.Errorf("%q\n  got  %s\n  want %s", input, got, want)
		}
	}
}

// TestPseudoNamesAreCaseInsensitive pins that the name is folded before it is
// looked up. Getting this wrong is invisible in the common case, because
// everyone writes them lowercase, and then a stylesheet using ":Hover" is
// silently accepted as an unknown-but-tolerated name — or ":BEFORE" quietly
// stops generating content.
//
// The bare forms are here too: "::before" on its own is a whole selector,
// meaning "*::before", and its compound constrains nothing. That is the one
// shape where an empty compound is correct rather than a selector that matches
// every element in the document.
func TestPseudoNamesAreCaseInsensitive(t *testing.T) {
	same := [][]string{
		{"a::before", "a::BEFORE", "a::Before", "a:before", "a:Before"},
		{":root", ":ROOT", ":Root"},
		{"a:first-child", "a:FIRST-CHILD"},
		{"::before", ":before", "::BEFORE", ":Before"},
	}
	for _, group := range same {
		want := sketch(mustParse(t, group[0])[0])
		for _, input := range group[1:] {
			if got := sketch(mustParse(t, input)[0]); got != want {
				t.Errorf("%q read as %s, want the same as %q, %s", input, got, group[0], want)
			}
		}
	}

	// The refusals fold too, or ":HOVER" slips through as an unknown name
	// rather than being named as the interactive selector it is.
	for _, input := range []string{"a:HOVER", "a:Hover", "a::SELECTION"} {
		_, errs, ok := parseSel(t, input)
		if ok {
			t.Errorf("%q was accepted", input)
			continue
		}
		if len(errs) == 0 || !errs[0].Unsupported {
			t.Errorf("%q was not reported as unsupported: %v", input, errs)
		}
	}

	// A bare pseudo-element selects every element's box, so its compound is
	// empty on purpose.
	sels := mustParse(t, "::before")
	if len(sels[0].Compounds) != 1 || sels[0].PseudoElement != "before" {
		t.Errorf("\"::before\" gave %d compounds and pseudo-element %q",
			len(sels[0].Compounds), sels[0].PseudoElement)
	}
}

// TestSelectorListSplits pins that a list is split on its own commas and not on
// the ones inside a functional pseudo-class.
func TestSelectorListSplits(t *testing.T) {
	cases := map[string]int{
		"a":                       1,
		"a, b":                    2,
		"a,b,c":                   3,
		"a:is(b, c)":              1,
		"a:is(b, c), d":           2,
		"a:not(b), c:is(d, e), f": 3,
		"a[x=\",\"]":              1,
	}
	for input, want := range cases {
		sels := mustParse(t, input)
		if len(sels) != want {
			t.Errorf("%q gave %d selectors, want %d", input, len(sels), want)
		}
	}
}

// TestCombinatorsAreNotDoubled pins the one place whitespace handling goes
// wrong. "a > b" has a space on each side of the ">", and a reader that treats
// each run of whitespace as a descendant combinator finds three combinators and
// two phantom compounds.
func TestCombinatorsAreNotDoubled(t *testing.T) {
	for _, input := range []string{"a>b", "a >b", "a> b", "a > b", "a\t>\n b"} {
		sels := mustParse(t, input)
		if len(sels[0].Compounds) != 2 {
			t.Errorf("%q gave %d compounds, want 2", input, len(sels[0].Compounds))
			continue
		}
		if got := sels[0].Compounds[1].Combinator; got != Child {
			t.Errorf("%q joined with %q, want \">\"", input, got)
		}
	}
	// And a plain space really is a combinator.
	sels := mustParse(t, "a b")
	if got := sels[0].Compounds[1].Combinator; got != Descendant {
		t.Errorf("\"a b\" joined with %q, want a descendant combinator", got)
	}
}

// TestSpecificity pins Selectors Level 4 §17. The cascade is decided by these
// numbers, so a wrong one silently applies the wrong declaration — the hardest
// class of rendering fault to trace back to its cause.
func TestSpecificity(t *testing.T) {
	cases := map[string]Specificity{
		"*":         {0, 0, 0},
		"a":         {0, 0, 1},
		"a b":       {0, 0, 2},
		"a > b + c": {0, 0, 3},
		".c":        {0, 1, 0},
		".c.d":      {0, 2, 0},
		"#i":        {1, 0, 0},
		// Two identifiers is legal and matches nothing; "#a#a" is the
		// specificity hack, and both count twice.
		"#i#j":          {2, 0, 0},
		"#i#i":          {2, 0, 0},
		"a.c":           {0, 1, 1},
		"a#i.c[x]":      {1, 2, 1},
		"[href]":        {0, 1, 0},
		"[href=x]":      {0, 1, 0},
		"a:first-child": {0, 1, 1},
		"a::before":     {0, 0, 2},
		"::before":      {0, 0, 1},
		":root":         {0, 1, 0},
		// The universal selector adds nothing, so it loses to a type selector.
		"*.c": {0, 1, 0},
		// :is() and :not() take the specificity of their most specific
		// argument, and contribute nothing themselves.
		":is(a)":      {0, 0, 1},
		":is(a, .c)":  {0, 1, 0},
		":is(a, #i)":  {1, 0, 0},
		":not(.c)":    {0, 1, 0},
		":not(a, #i)": {1, 0, 0},
		"a:is(.c)":    {0, 1, 1},
		// :where() contributes nothing at all, whatever is inside it. That is
		// its entire reason to exist.
		":where(#i)":  {0, 0, 0},
		"a:where(#i)": {0, 0, 1},
		// :nth-child() is a pseudo-class, and its "of" list adds its own.
		":nth-child(2n)":       {0, 1, 0},
		":nth-child(2n of .c)": {0, 2, 0},
		":nth-child(2n of #i)": {1, 1, 0},
	}
	for input, want := range cases {
		sels, _, ok := parseSel(t, input)
		if !ok {
			if want != (Specificity{}) {
				t.Errorf("%q was refused, and should have specificity %v", input, want)
			}
			continue
		}
		if got := sels[0].Specificity; got != want {
			t.Errorf("%q has specificity %v, want %v", input, got, want)
		}
	}
}

// TestSpecificityOrders pins that the three components do not carry. A thousand
// classes lose to one identifier, which is why specificity is not a number.
func TestSpecificityOrders(t *testing.T) {
	ascending := []string{
		"*",
		"a",
		"a b",
		".c",
		".c a",
		".c.d",
		"#i",
		"#i .c",
		"#i#j2",
	}
	var prev Specificity
	for i, input := range ascending {
		sels := mustParse(t, input)
		got := sels[0].Specificity
		if i > 0 && !prev.Less(got) {
			t.Errorf("%q has specificity %v, which does not beat the previous %v",
				input, got, prev)
		}
		prev = got
	}

	// A hundred classes still lose to one identifier.
	many := mustParse(t, strings.Repeat(".c", 100))[0].Specificity
	one := mustParse(t, "#i")[0].Specificity
	if !many.Less(one) {
		t.Errorf("a hundred classes %v beat one identifier %v", many, one)
	}
}

// TestDynamicSelectorsAreRefusedAndSaidSo is the subset boundary, which is the
// design decision this file exists to protect. A printed page has no pointer, no
// focus and no history, so these have no answer — and answering "false" quietly
// is the failure §6.3 is written about, because the page then looks plausible
// and is wrong.
func TestDynamicSelectorsAreRefusedAndSaidSo(t *testing.T) {
	dynamic := []string{
		"a:hover", "a:focus", "a:active", "a:visited", "input:checked",
		"input:disabled", "input:enabled", ":target", "input:valid",
		"a:focus-within", "input:placeholder-shown", ":fullscreen",
		"a:hover .c", ".c a:hover", "a:not(:hover)",
	}
	for _, input := range dynamic {
		sels, errs, ok := parseSel(t, input)
		if ok || len(sels) != 0 {
			t.Errorf("%q was accepted, and a page laid out once cannot answer it", input)
			continue
		}
		if len(errs) == 0 {
			t.Errorf("%q was refused with no explanation", input)
			continue
		}
		// It has to be reported as unsupported rather than as malformed: the
		// author wrote correct CSS, and telling them it is a syntax error sends
		// them looking for a typo that is not there.
		if !errs[0].Unsupported {
			t.Errorf("%q was reported as malformed (%q), and it is correct CSS "+
				"this engine chooses not to apply", input, errs[0].Message)
		}
	}

	// And the message says which it is, because "no such pseudo-class" and
	// "this cannot apply to a printed page" send an author to different places.
	_, errs, _ := parseSel(t, "a:hover")
	if !strings.Contains(errs[0].Message, "interact") {
		t.Errorf("the message for :hover is %q, and does not say why it cannot apply",
			errs[0].Message)
	}
	_, errs, _ = parseSel(t, "a:nonesuch")
	if strings.Contains(errs[0].Message, "interact") {
		t.Errorf("the message for an invented pseudo-class is %q, "+
			"which blames interactivity for a typo", errs[0].Message)
	}
}

// TestStaticSelectorsAreKept is the other side of the boundary. The structural
// family is the whole reason to have selectors in a document generator, and
// :link is on the static side however much it looks like :hover — whether an
// element has an href is a fact about the document.
func TestStaticSelectorsAreKept(t *testing.T) {
	static := []string{
		":root", ":empty",
		"a:first-child", "a:last-child", "a:only-child",
		"a:first-of-type", "a:last-of-type", "a:only-of-type",
		"a:nth-child(2n+1)", "a:nth-last-child(odd)",
		"a:nth-of-type(3)", "a:nth-last-of-type(even)",
		"a:nth-child(2n of .c)",
		"a:not(.c)", "a:is(.c, .d)", "a:where(.c)",
		"p:lang(en)", `p:lang("en-GB")`, "p:lang(en, fr)",
		"a:link", "a:any-link",
		"a::before", "a::after", "p::first-line", "p::first-letter",
		"li::marker",
	}
	for _, input := range static {
		if _, errs, ok := parseSel(t, input); !ok {
			t.Errorf("%q was refused, and a document decides it by itself: %v", input, errs)
		}
	}
}

// TestForgivingSelectorLists pins the difference between :is() and :not(), which
// is not a detail: dropping an argument from :is() narrows what a rule matches,
// and dropping one from :not() *widens* it. Being forgiving in :not() would
// apply a style to the very elements the author excluded.
func TestForgivingSelectorLists(t *testing.T) {
	// :is() and :where() survive an argument this engine cannot use.
	for _, input := range []string{
		"a:is(.c, :hover)",
		"a:where(:hover, .c)",
		"a:is(:hover, .c, :focus)",
	} {
		sels, errs, ok := parseSel(t, input)
		if !ok || len(sels) != 1 {
			t.Errorf("%q was refused, and a forgiving list should have dropped "+
				"the argument and stood: %v", input, errs)
			continue
		}
		// Dropped, but still reported: the author is told the rule is narrower
		// than they wrote.
		if len(errs) == 0 {
			t.Errorf("%q dropped an argument silently", input)
		}
	}

	// :not() does not.
	for _, input := range []string{
		"a:not(.c, :hover)",
		"a:not(:hover)",
	} {
		if sels, _, ok := parseSel(t, input); ok || len(sels) != 0 {
			t.Errorf("%q was accepted; forgiving :not() widens what the rule matches", input)
		}
	}

	// And an :is() with nothing usable left is refused rather than kept as a
	// selector that matches nothing.
	if _, _, ok := parseSel(t, "a:is(:hover)"); ok {
		t.Error("\"a:is(:hover)\" was accepted, and there is nothing left in it")
	}
}

// TestMalformedSelectorsAreRefused pins that broken input is refused and
// reported as broken, not as unsupported.
func TestMalformedSelectorsAreRefused(t *testing.T) {
	malformed := []string{
		"", " ", ",", "a,", ",a", "a,,b",
		">", "a >", "> a", "a > > b", "a > + b",
		".", ".1", "a.", "#", // "#" alone is a delimiter, not a hash
		"a b#", "[", "[]", "[=x]", "[a=]", "[a=x y]", "[a~x]",
		"a::", "a:", "::", ":",
		"a::before::after", "a::before b",
		"a:not()", // nothing to negate
		"a:is()",
		"a:nth-child()", "a:nth-child(x)", "a:nth-child(2n of)",
		"a:lang()", "a:root(x)",
	}
	for _, input := range malformed {
		sels, errs, ok := parseSel(t, input)
		if ok {
			t.Errorf("%q was accepted", input)
			continue
		}
		if len(sels) != 0 {
			t.Errorf("%q was refused and still returned %d selectors, which a "+
				"caller that skipped ok would apply", input, len(sels))
		}
		if len(errs) == 0 {
			t.Errorf("%q was refused with no explanation", input)
		}
	}
}

// TestNamespacesAreRefused pins that a qualified name is refused rather than
// misread. "svg|circle" is not the element "svg" followed by something — a
// reader that ignores the namespace selects elements the author did not ask for.
func TestNamespacesAreRefused(t *testing.T) {
	for _, input := range []string{
		"svg|circle", "*|a", "|a", "[svg|href]", "a[*|x=y]",
	} {
		sels, errs, ok := parseSel(t, input)
		if ok || len(sels) != 0 {
			t.Errorf("%q was accepted, and its namespace was ignored", input)
			continue
		}
		if len(errs) == 0 || !errs[0].Unsupported {
			t.Errorf("%q was not reported as unsupported: %v", input, errs)
		}
	}

	// But "|=" inside an attribute selector is the dash-match operator and has
	// nothing to do with namespaces.
	sels := mustParse(t, "[lang|=en]")
	if got := sels[0].Compounds[0].Attrs[0].Op; got != AttrDashMatch {
		t.Errorf("[lang|=en] read its operator as %q, want |=", got)
	}
}

// TestSelectorNestingIsBounded is the security property. A stylesheet is
// untrusted, and :is(:is(:is(...))) must not exhaust the stack.
func TestSelectorNestingIsBounded(t *testing.T) {
	deep := maxSelectorDepth * 4
	input := strings.Repeat(":is(", deep) + "a" + strings.Repeat(")", deep)

	vals, _ := ParseComponentValues(input)
	sels, errs, ok := ParseSelectorList(vals)
	if ok {
		t.Errorf("a selector nested %d deep was accepted", deep)
	}
	if len(errs) == 0 {
		t.Error("a selector nested too deeply was refused with no explanation")
	}
	_ = sels
}

// TestSelectorParseIsTotal is the property that matters for untrusted input:
// nothing panics, whatever arrives.
func TestSelectorParseIsTotal(t *testing.T) {
	inputs := []string{
		"", " ", ",", ":", "::", "[", "]", "(", ")", "*", ">", "+", "~", "|",
		"a:", "a::", "a:not", "a:not(", "a:is(,)", "[a", "[a=", "[a=b",
		"#", ".", "..", "#.", "a||b", "a|", "|a", "&", "%", "@",
		strings.Repeat(",", 1000),
		strings.Repeat("a ", 1000),
		strings.Repeat(":is(", 1000),
		strings.Repeat("[a=b]", 1000),
		strings.Repeat(">", 1000),
	}
	for _, in := range inputs {
		vals, _ := ParseComponentValues(in)
		ParseSelectorList(vals)
	}
}

// TestSelectorErrorsAreBounded pins that a hostile stylesheet cannot produce an
// unbounded diagnostic list here either.
func TestSelectorErrorsAreBounded(t *testing.T) {
	vals, _ := ParseComponentValues(strings.Repeat(":hover,", maxErrors*4))
	_, errs, _ := ParseSelectorList(vals)
	if len(errs) > maxErrors+1 {
		t.Errorf("got %d errors, want at most %d and a note", len(errs), maxErrors)
	}
	if len(errs) == 0 {
		t.Fatal("a list of nothing but refused selectors reported nothing")
	}
}
