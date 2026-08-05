package style

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// The cascade.
//
// This is the part of styling that fails *silently*. A selector that matches
// nothing shows up as a rule with no effect, which an author notices; two
// declarations ordered wrongly produce a page where the wrong one won, which
// looks like a design decision and is traced back only by reading the
// stylesheet against the specification.
//
// So every test here asserts *which declaration won*, using values that name
// their own rule — "wins", "loses" — rather than plausible ones. A test whose
// expected value is "red" passes just as well when the cascade picked the wrong
// red.

// styleOf applies stylesheets to a document and returns one element's computed
// value for a property.
func styleOf(t *testing.T, doc *html.Node, sheets []Sheet, selector, property string) string {
	t.Helper()
	got := Apply(doc, sheets)
	n := elementFor(t, doc, selector)
	cs, ok := got.Styles[n]
	if !ok {
		t.Fatalf("no computed style for the element selected by %q", selector)
	}
	return cs[property]
}

// elementFor finds the single element a selector picks, failing if it is not
// exactly one — so a test can never assert about a different element than it
// meant.
func elementFor(t *testing.T, doc *html.Node, selector string) *html.Node {
	t.Helper()
	vals, _ := css.ParseComponentValues(selector)
	sels, _, ok := css.ParseSelectorList(vals)
	if !ok {
		t.Fatalf("the selector %q was refused", selector)
	}
	m := NewMatcher(doc)
	var found []*html.Node
	doc.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		for _, s := range sels {
			if m.Match(s, n) {
				found = append(found, n)
				break
			}
		}
		return true
	})
	if len(found) != 1 {
		t.Fatalf("%q selects %d elements, want exactly 1", selector, len(found))
	}
	return found[0]
}

// author builds an author-origin sheet from source.
func author(t *testing.T, src string) Sheet { return sheet(t, OriginAuthor, src) }

func sheet(t *testing.T, origin Origin, src string) Sheet {
	t.Helper()
	rules, errs := css.ParseStylesheet(src)
	if len(errs) != 0 {
		t.Fatalf("the stylesheet %q reported %v", src, errs)
	}
	return Sheet{Origin: origin, Rules: rules}
}

const cascadeDoc = `<div id="outer" class="c"><p id="target" class="c">x</p></div>`

// TestCascadeOrderOfAppearance pins the last term: when everything else ties,
// the later declaration wins.
func TestCascadeOrderOfAppearance(t *testing.T) {
	doc := parseDoc(t, cascadeDoc)
	cases := map[string]string{
		"p { color: loses } p { color: wins }":             "wins",
		"p { color: loses; color: wins }":                  "wins",
		".c { color: loses } .c { color: wins }":           "wins",
		"#target { color: loses } #target { color: wins }": "wins",
	}
	for src, want := range cases {
		if got := styleOf(t, doc, []Sheet{author(t, src)}, "#target", "color"); got != want {
			t.Errorf("%q\n  gave %q, want %q", src, got, want)
		}
	}

	// Across sheets, later still wins — the order is one sequence over the whole
	// input, not an index within a sheet.
	got := styleOf(t, doc, []Sheet{
		author(t, "p { color: loses }"),
		author(t, "p { color: wins }"),
	}, "#target", "color")
	if got != "wins" {
		t.Errorf("across two sheets got %q, want wins", got)
	}
}

// TestCascadeSpecificity pins that specificity beats order. A more specific rule
// written earlier still wins, which is the whole reason specificity exists.
func TestCascadeSpecificity(t *testing.T) {
	doc := parseDoc(t, cascadeDoc)
	cases := map[string]string{
		"#target { color: wins } p { color: loses }":        "wins",
		"p { color: loses } #target { color: wins }":        "wins",
		".c { color: wins } p { color: loses }":             "wins",
		"p.c { color: wins } .c { color: loses }":           "wins",
		"div p { color: wins } p { color: loses }":          "wins",
		"#target { color: wins } .c.c.c.c { color: loses }": "wins",
		// The universal selector adds nothing, so a type selector beats it.
		"p { color: wins } * { color: loses }": "wins",
		// :where() contributes nothing, so its argument does not raise the
		// weight — this is the whole point of :where().
		"p { color: wins } :where(#target) { color: loses }": "wins",
		// :is() does contribute its argument's specificity.
		":is(#target) { color: wins } p { color: loses }": "wins",
	}
	for src, want := range cases {
		if got := styleOf(t, doc, []Sheet{author(t, src)}, "#target", "color"); got != want {
			t.Errorf("%q\n  gave %q, want %q", src, got, want)
		}
	}
}

// TestCascadeSpecificityOfTheMatchingSelector pins a case that is easy to get
// wrong and impossible to see afterwards. A rule with a list applies with the
// specificity of the selector that *matched*, not the most specific in the list.
func TestCascadeSpecificityOfTheMatchingSelector(t *testing.T) {
	doc := parseDoc(t, cascadeDoc)
	// The first rule matches through "p", which is (0,0,1), so the second rule's
	// ".c" at (0,1,0) beats it. Taking "#nomatch" from the list would give the
	// first rule (1,0,0) and the wrong answer.
	src := "p, #nomatch { color: loses } .c { color: wins }"
	if got := styleOf(t, doc, []Sheet{author(t, src)}, "#target", "color"); got != "wins" {
		t.Errorf("%q gave %q, want wins — the rule applied with a specificity "+
			"from a selector that did not match", src, got)
	}
}

// TestCascadeOriginAndImportance pins the strongest term, and the inversion that
// makes it subtle: an important declaration does not merely beat an unimportant
// one, it *reverses* the origin order. That is how a user stylesheet can force
// something a page cannot override.
func TestCascadeOriginAndImportance(t *testing.T) {
	doc := parseDoc(t, cascadeDoc)

	cases := []struct {
		name   string
		sheets []Sheet
		want   string
	}{
		{
			"author beats user agent",
			[]Sheet{
				sheet(t, OriginUserAgent, "p { color: loses }"),
				sheet(t, OriginAuthor, "p { color: wins }"),
			}, "wins",
		},
		{
			"author beats user agent even when less specific",
			[]Sheet{
				sheet(t, OriginUserAgent, "#target { color: loses }"),
				sheet(t, OriginAuthor, "p { color: wins }"),
			}, "wins",
		},
		{
			"user beats user agent",
			[]Sheet{
				sheet(t, OriginUserAgent, "p { color: loses }"),
				sheet(t, OriginUser, "p { color: wins }"),
			}, "wins",
		},
		{
			"author beats user",
			[]Sheet{
				sheet(t, OriginUser, "p { color: loses }"),
				sheet(t, OriginAuthor, "p { color: wins }"),
			}, "wins",
		},
		{
			"important author beats ordinary author, however specific",
			[]Sheet{
				sheet(t, OriginAuthor, "p { color: wins !important }"),
				sheet(t, OriginAuthor, "#target { color: loses }"),
			}, "wins",
		},
		{
			// The inversion: an important user rule beats an important author
			// one, which is the reverse of the ordinary order.
			"important user beats important author",
			[]Sheet{
				sheet(t, OriginAuthor, "p { color: loses !important }"),
				sheet(t, OriginUser, "p { color: wins !important }"),
			}, "wins",
		},
		{
			"important user agent beats important user",
			[]Sheet{
				sheet(t, OriginUser, "p { color: loses !important }"),
				sheet(t, OriginUserAgent, "p { color: wins !important }"),
			}, "wins",
		},
		{
			"important user agent beats important author",
			[]Sheet{
				sheet(t, OriginAuthor, "p { color: loses !important }"),
				sheet(t, OriginUserAgent, "p { color: wins !important }"),
			}, "wins",
		},
		{
			"any important beats any ordinary",
			[]Sheet{
				sheet(t, OriginUserAgent, "p { color: wins !important }"),
				sheet(t, OriginAuthor, "#target { color: loses }"),
			}, "wins",
		},
	}
	for _, tc := range cases {
		if got := styleOf(t, doc, tc.sheets, "#target", "color"); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestInlineStyle pins where a style attribute sits: above every ordinary author
// rule whatever its specificity, and below an important one.
func TestInlineStyle(t *testing.T) {
	doc := parseDoc(t,
		`<div id="outer"><p id="target" style="color: inline">x</p></div>`)

	if got := styleOf(t, doc, []Sheet{author(t, "#target { color: loses }")},
		"#target", "color"); got != "inline" {
		t.Errorf("an identifier selector beat a style attribute: got %q", got)
	}
	if got := styleOf(t, doc, []Sheet{author(t, "p { color: wins !important }")},
		"#target", "color"); got != "wins" {
		t.Errorf("a style attribute beat an important rule: got %q", got)
	}
}

// TestInheritance pins that an inheriting property reaches descendants and a
// non-inheriting one does not — which is a property of the registry, not of the
// declaration, and so is invisible in the stylesheet.
func TestInheritance(t *testing.T) {
	doc := parseDoc(t, `<div id="outer"><p id="target">x</p></div>`)
	sheets := []Sheet{author(t, "#outer { color: passed; margin-top: 5px }")}

	if got := styleOf(t, doc, sheets, "#target", "color"); got != "passed" {
		t.Errorf("color did not inherit: got %q", got)
	}
	// margin-top does not inherit, so the child has the initial value.
	if got := styleOf(t, doc, sheets, "#target", "margin-top"); got != "0" {
		t.Errorf("margin-top inherited: got %q, want the initial 0", got)
	}
	// And the element that set it still has it.
	if got := styleOf(t, doc, sheets, "#outer", "margin-top"); got != "5px" {
		t.Errorf("the element that set margin-top has %q", got)
	}
}

// TestInitialValues pins that every property has a value even when nothing sets
// one, so a caller never has to distinguish "unset" from "absent".
func TestInitialValues(t *testing.T) {
	doc := parseDoc(t, "<p id=\"target\">x</p>")
	got := Apply(doc, nil)
	cs := got.Styles[elementFor(t, doc, "#target")]

	if len(cs) != len(properties) {
		t.Errorf("the computed style holds %d properties, want all %d",
			len(cs), len(properties))
	}
	for name, prop := range properties {
		if cs[name] != prop.initial {
			t.Errorf("%s is %q with no stylesheet, want the initial %q",
				name, cs[name], prop.initial)
		}
	}
}

// TestWideKeywords pins the four keywords every property accepts. "unset" is the
// one worth the most care: it means inherit-or-initial depending on the
// property, so the same word gives two different answers.
func TestWideKeywords(t *testing.T) {
	doc := parseDoc(t, `<div id="outer"><p id="target">x</p></div>`)

	base := "#outer { color: fromparent; margin-top: 9px } " +
		"#target { color: own; margin-top: 1px }"

	cases := []struct{ decl, property, want string }{
		{"color: inherit", "color", "fromparent"},
		{"color: initial", "color", "black"},
		{"color: unset", "color", "fromparent"}, // color inherits
		{"margin-top: inherit", "margin-top", "9px"},
		{"margin-top: initial", "margin-top", "0"},
		{"margin-top: unset", "margin-top", "0"}, // margin does not inherit
	}
	for _, tc := range cases {
		src := base + " #target { " + tc.decl + " }"
		if got := styleOf(t, doc, []Sheet{author(t, src)}, "#target", tc.property); got != tc.want {
			t.Errorf("%q gave %s=%q, want %q", tc.decl, tc.property, got, tc.want)
		}
	}
}

// TestInheritAtTheRoot pins that inheriting where there is no parent gives the
// initial value rather than an empty one.
func TestInheritAtTheRoot(t *testing.T) {
	doc := parseDoc(t, "<p>x</p>")
	src := "html { color: inherit }"
	if got := styleOf(t, doc, []Sheet{author(t, src)}, "html", "color"); got != "black" {
		t.Errorf("inheriting at the root gave %q, want the initial black", got)
	}
}

// TestShorthandsExpandBeforeTheCascade pins the ordering that makes shorthands
// work. "margin: 0" then "margin-top: 1em" must leave the top at 1em, which
// only happens if the shorthand became four declarations competing individually.
func TestShorthandsExpandBeforeTheCascade(t *testing.T) {
	doc := parseDoc(t, "<p id=\"target\">x</p>")

	sides := []string{"margin-top", "margin-right", "margin-bottom", "margin-left"}
	cases := []struct {
		src  string
		want map[string]string
	}{
		{"p { margin: 1px }", map[string]string{
			"margin-top": "1px", "margin-right": "1px",
			"margin-bottom": "1px", "margin-left": "1px"}},
		{"p { margin: 1px 2px }", map[string]string{
			"margin-top": "1px", "margin-right": "2px",
			"margin-bottom": "1px", "margin-left": "2px"}},
		{"p { margin: 1px 2px 3px }", map[string]string{
			"margin-top": "1px", "margin-right": "2px",
			"margin-bottom": "3px", "margin-left": "2px"}},
		{"p { margin: 1px 2px 3px 4px }", map[string]string{
			"margin-top": "1px", "margin-right": "2px",
			"margin-bottom": "3px", "margin-left": "4px"}},
		// The ordering that matters: a longhand written after a shorthand wins
		// for its side and leaves the others alone.
		{"p { margin: 1px; margin-top: 9px }", map[string]string{
			"margin-top": "9px", "margin-right": "1px",
			"margin-bottom": "1px", "margin-left": "1px"}},
		// And one written before it loses, because the shorthand is later.
		{"p { margin-top: 9px; margin: 1px }", map[string]string{
			"margin-top": "1px", "margin-right": "1px",
			"margin-bottom": "1px", "margin-left": "1px"}},
	}
	for _, tc := range cases {
		sheets := []Sheet{author(t, tc.src)}
		for _, side := range sides {
			if got := styleOf(t, doc, sheets, "#target", side); got != tc.want[side] {
				t.Errorf("%q gave %s=%q, want %q", tc.src, side, got, tc.want[side])
			}
		}
	}
}

// TestShorthandWithAWideKeyword pins that "margin: inherit" reaches all four
// sides. The expander splits on whitespace, so without a case of its own the
// keyword would land on the top only.
func TestShorthandWithAWideKeyword(t *testing.T) {
	doc := parseDoc(t, `<div id="outer"><p id="target">x</p></div>`)
	src := "#outer { margin: 7px } #target { margin: inherit }"
	sheets := []Sheet{author(t, src)}
	for _, side := range []string{"margin-top", "margin-right", "margin-bottom", "margin-left"} {
		if got := styleOf(t, doc, sheets, "#target", side); got != "7px" {
			t.Errorf("%s is %q, want 7px — the keyword did not reach every side", side, got)
		}
	}
}

// TestUnsupportedPropertyIsReported is the guardrail of §6.3, and the cheapest
// one in the design. An engine implementing a subset *will* meet declarations it
// does not act on, and a page where one was dropped is plausible and wrong.
func TestUnsupportedPropertyIsReported(t *testing.T) {
	doc := parseDoc(t, "<p id=\"target\">x</p>")
	got := Apply(doc, []Sheet{author(t, "p { flex-wrap: wrap; color: kept }")})

	var found *Finding
	for i := range got.Findings {
		if got.Findings[i].Property == "flex-wrap" {
			found = &got.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("dropping flex-wrap was not reported; findings were %v", got.Findings)
	}
	if !found.Unsupported {
		t.Error("dropping flex-wrap was reported as malformed input, and it is correct CSS")
	}
	// The declaration beside it still applied, so one unknown property does not
	// cost the rule.
	if v := got.Styles[elementFor(t, doc, "#target")]["color"]; v != "kept" {
		t.Errorf("color is %q; an unknown property took the rest of the rule with it", v)
	}
}

// TestUnsupportedPropertyIsReportedOnce pins that a stylesheet using an
// unimplemented property forty times tells the author once. A report a person
// will not read is a report that does not exist.
func TestUnsupportedPropertyIsReportedOnce(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("p { flex-wrap: wrap }\n")
	}
	doc := parseDoc(t, "<p>x</p>")
	got := Apply(doc, []Sheet{author(t, b.String())})

	n := 0
	for _, f := range got.Findings {
		if f.Property == "flex-wrap" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("flex-wrap was reported %d times, want once", n)
	}
}

// TestFindingsAreBounded pins that a stylesheet full of unimplemented properties
// cannot produce an unbounded report.
func TestFindingsAreBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxFindings*3; i++ {
		b.WriteString("p { nosuchproperty-")
		b.WriteString(strings.Repeat("x", i%7+1))
		b.WriteString(itoa(i))
		b.WriteString(": 1 }\n")
	}
	doc := parseDoc(t, "<p>x</p>")
	got := Apply(doc, []Sheet{author(t, b.String())})
	if len(got.Findings) > maxFindings+1 {
		t.Errorf("got %d findings, want at most %d and a note", len(got.Findings), maxFindings)
	}
	if len(got.Findings) == 0 {
		t.Fatal("a stylesheet of nothing but unknown properties reported nothing")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestAtRulesAreReported pins that @media and @page are visibly absent rather
// than silently ignored — they are the next stage's work, and until it arrives
// an author has to be told their rules did nothing.
func TestAtRulesAreReported(t *testing.T) {
	doc := parseDoc(t, "<p>x</p>")
	got := Apply(doc, []Sheet{author(t, "@media print { p { color: c } } @page { margin: 1cm }")})

	names := map[string]bool{}
	for _, f := range got.Findings {
		names[f.Property] = true
	}
	for _, want := range []string{"@media", "@page"} {
		if !names[want] {
			t.Errorf("%s was not reported; findings were %v", want, got.Findings)
		}
	}
}

// TestApplyIsDeterministic pins that two runs agree. The cascade reads several
// maps, and Go randomises map iteration on every run, so an ordering that
// happens to be stable in one process is not evidence.
func TestApplyIsDeterministic(t *testing.T) {
	doc := parseDoc(t, cascadeDoc)
	src := `
p { margin: 1px 2px; color: a }
.c { color: b; padding: 3px }
#target { color: c }
div p { margin-left: 9px }
p { border-width: 1px 2px 3px 4px }`

	first := Apply(doc, []Sheet{author(t, src)})
	target := elementFor(t, doc, "#target")

	for i := 0; i < 20; i++ {
		again := Apply(doc, []Sheet{author(t, src)})
		for name := range properties {
			a, b := first.Styles[target][name], again.Styles[target][name]
			if a != b {
				t.Fatalf("run %d disagrees on %s: %q then %q", i, name, a, b)
			}
		}
	}
}

// TestStyleAppliesToEveryElement pins that nothing is missed, including the
// frame the html package always builds.
func TestStyleAppliesToEveryElement(t *testing.T) {
	doc := parseDoc(t, "<div><p>a</p><span>b</span></div>")
	got := Apply(doc, nil)

	n := 0
	doc.Walk(func(node *html.Node) bool {
		if node.Type != html.ElementNode {
			return true
		}
		n++
		if _, ok := got.Styles[node]; !ok {
			t.Errorf("<%s> has no computed style", node.Name)
		}
		return true
	})
	if len(got.Styles) != n {
		t.Errorf("there are %d elements and %d styles", n, len(got.Styles))
	}
}

// TestApplyIsTotal pins that no stylesheet and no document combination panics.
func TestApplyIsTotal(t *testing.T) {
	docs := []string{"", "<p>x</p>", cascadeDoc, structuralDoc, simpleDoc}
	sheets := []string{
		"", "p{}", "p{color:red}", "{}", "@media{}", "@page{}",
		"p{margin:}", "p{margin: 1 2 3 4 5}", "p{color:inherit}",
		"p{color:revert}", "p{:}", "p{;;;}", "*{color:a!important}",
		"p{margin:inherit}", "p{overflow:hidden}", "p{unknown:1}",
		strings.Repeat("p{color:a}", 500),
	}
	for _, d := range docs {
		doc, _, _ := html.Parse(d)
		for _, src := range sheets {
			rules, _ := css.ParseStylesheet(src)
			Apply(doc, []Sheet{{Origin: OriginAuthor, Rules: rules}})
		}
	}
}
