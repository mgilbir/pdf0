package style

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// Selector matching.
//
// This is where the css package's selector structures are first put to work,
// and the tests are shaped by that. A selector parser can be checked for the
// shape it builds, but a wrong shape and a right one are indistinguishable
// until something matches against a document — so what is asserted here is
// always *which elements* a selector selects, by name, against a document
// written to make each near miss a different answer.

// selected parses a selector, matches it against every element of a document,
// and returns the id of each element it selected, in document order.
//
// Ids rather than positions, so a failure names the element an author would
// recognise instead of an index into a walk.
func selected(t *testing.T, doc *html.Node, selector string) []string {
	t.Helper()
	vals, _ := css.ParseComponentValues(selector)
	sels, errs, ok := css.ParseSelectorList(vals)
	if !ok {
		t.Fatalf("the selector %q was refused: %v", selector, errs)
	}

	m := NewMatcher(doc)
	var got []string
	doc.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		for _, s := range sels {
			if m.Match(s, n) {
				id, _ := n.Attr("id")
				if id == "" {
					id = "<" + n.Name + ">"
				}
				got = append(got, id)
				break
			}
		}
		return true
	})
	if m.Tripped() {
		t.Fatalf("matching %q ran out of budget", selector)
	}
	return got
}

func parseDoc(t *testing.T, src string) *html.Node {
	t.Helper()
	doc, errs, ok := html.Parse(src)
	if !ok {
		t.Fatalf("the document was refused: %v", errs)
	}
	return doc
}

// check runs a table of selector to expected ids against one document.
func check(t *testing.T, doc *html.Node, cases map[string]string) {
	t.Helper()
	for selector, want := range cases {
		got := strings.Join(selected(t, doc, selector), " ")
		if got != want {
			t.Errorf("%q\n  selected %s\n  want     %s", selector, quoteList(got), quoteList(want))
		}
	}
}

func quoteList(s string) string {
	if s == "" {
		return "(nothing)"
	}
	return s
}

// The document the simple cases run against. Every element has an id, and the
// shapes are chosen so that a selector confusing two constructs gives a
// different answer rather than the same one.
const simpleDoc = `
<div id="outer" class="box wide">
  <p id="p1" class="lead">one</p>
  <p id="p2" class="lead special">two</p>
  <span id="s1">three</span>
  <p id="p3">four</p>
  <div id="inner" class="box">
    <p id="p4" class="lead">five</p>
    <a id="a1" href="x.html">link</a>
    <a id="a2">no href</a>
  </div>
</div>`

func TestMatchSimple(t *testing.T) {
	doc := parseDoc(t, simpleDoc)
	check(t, doc, map[string]string{
		"p":          "p1 p2 p3 p4",
		"span":       "s1",
		"*":          "<html> <head> <body> outer p1 p2 s1 p3 inner p4 a1 a2",
		"#p2":        "p2",
		".lead":      "p1 p2 p4",
		".box":       "outer inner",
		".box.wide":  "outer",
		"p.lead":     "p1 p2 p4",
		"p.special":  "p2",
		"div p":      "p1 p2 p3 p4",
		"div > p":    "p1 p2 p3 p4",
		"#outer > p": "p1 p2 p3",
		"#inner p":   "p4",
		"#inner > p": "p4",
		"div div p":  "p4",
		// p3 follows the <span>, not a <p>, so "+" does not reach it — which is
		// the whole difference from "~" below.
		"p + p":        "p2",
		"p + span":     "s1",
		"span + p":     "p3",
		"p ~ p":        "p2 p3",
		"span ~ p":     "p3",
		"p ~ span":     "s1",
		"nosuchtag":    "",
		".nosuchclass": "",
		"#nosuchid":    "",
	})
}

// TestMatchIsAboutElementsNotNodes pins that the sibling combinators skip text.
// "p + p" means two paragraphs with only text between them; a matcher counting
// text nodes as siblings finds none of these.
func TestMatchIsAboutElementsNotNodes(t *testing.T) {
	doc := parseDoc(t, `<div><p id="a">x</p>
	text between
	<p id="b">y</p></div>`)
	check(t, doc, map[string]string{
		"p + p": "b",
		"p ~ p": "b",
	})
}

// TestMatchClassIsAMembershipTest pins that a class is one of a
// whitespace-separated set, not a substring. A substring test selects
// class="subtitle" for ".title", which is a rule applying to elements nobody
// asked for.
func TestMatchClassIsAMembershipTest(t *testing.T) {
	doc := parseDoc(t, `
<p id="a" class="title">a</p>
<p id="b" class="subtitle">b</p>
<p id="c" class="title main">c</p>
<p id="d" class="main title">d</p>
<p id="e" class="titles">e</p>`)
	check(t, doc, map[string]string{
		".title":    "a c d",
		".subtitle": "b",
		".main":     "c d",
		".titles":   "e",
	})
}

// TestMatchAttributes walks the seven operators. Each pair below differs in
// exactly the way one operator cares about and another does not.
func TestMatchAttributes(t *testing.T) {
	doc := parseDoc(t, `
<a id="a" href="http://example.com/a.html" hreflang="en-GB" rel="next tag">a</a>
<a id="b" href="a.html" hreflang="en" rel="tag">b</a>
<a id="c" href="B.HTML" hreflang="english" rel="tagged">c</a>
<a id="d">d</a>`)
	check(t, doc, map[string]string{
		"[href]":            "a b c",
		"[hreflang]":        "a b c",
		"[nosuchattr]":      "",
		`[href="a.html"]`:   "b",
		`[href="B.HTML"]`:   "c",
		`[href="b.html"]`:   "",
		`[href="b.html" i]`: "c",
		`[href="B.HTML" s]`: "c",
		// ~= is a membership test over a whitespace-separated set.
		`[rel~="tag"]`:  "a b",
		`[rel~="next"]`: "a",
		`[rel~="tagg"]`: "",
		// |= is the language one: "en" matches "en" and "en-GB", not "english".
		`[hreflang|="en"]`: "a b",
		// ^= $= *= are plain string tests.
		`[href^="http"]`:    "a",
		`[href^="a"]`:       "b",
		`[href$=".html"]`:   "a b",
		`[href*="example"]`: "a",
		`[href*=".htm"]`:    "a b",
		// The empty string is a substring of everything, so the standard makes
		// it match nothing rather than duplicating [href].
		`[href*=""]`: "",
		`[href^=""]`: "",
		`[href$=""]`: "",
	})
}

// The document the structural pseudo-classes run against.
const structuralDoc = `
<ul id="list">
  <li id="i1">one</li>
  <li id="i2">two</li>
  <li id="i3">three</li>
  <li id="i4">four</li>
  <li id="i5">five</li>
</ul>
<div id="mixed">
  <h2 id="h1">h</h2>
  <p id="m1">a</p>
  <span id="sp">s</span>
  <p id="m2">b</p>
  <p id="m3">c</p>
</div>
<div id="empties"><p id="e1"></p><p id="e2"> </p><p id="e3">x</p></div>`

func TestMatchStructural(t *testing.T) {
	doc := parseDoc(t, structuralDoc)
	check(t, doc, map[string]string{
		"li:first-child":               "i1",
		"li:last-child":                "i5",
		"li:only-child":                "",
		"#list li:nth-child(1)":        "i1",
		"#list li:nth-child(2)":        "i2",
		"#list li:nth-child(odd)":      "i1 i3 i5",
		"#list li:nth-child(even)":     "i2 i4",
		"#list li:nth-child(2n+1)":     "i1 i3 i5",
		"#list li:nth-child(3n)":       "i3",
		"#list li:nth-last-child(1)":   "i5",
		"#list li:nth-last-child(2)":   "i4",
		"#list li:nth-last-child(odd)": "i1 i3 i5",
		// n may not be negative, so 2n+3 does not select the first.
		"#list li:nth-child(2n+3)": "i3 i5",
		// A negative A counts down and stops.
		"#list li:nth-child(-n+2)": "i1 i2",

		// of-type counts only siblings with the same name, which is the whole
		// difference from the child family.
		"#mixed p:first-child":         "",
		"#mixed p:first-of-type":       "m1",
		"#mixed p:last-of-type":        "m3",
		"#mixed h2:only-of-type":       "h1",
		"#mixed span:only-of-type":     "sp",
		"#mixed p:only-of-type":        "",
		"#mixed p:nth-of-type(2)":      "m2",
		"#mixed p:nth-last-of-type(1)": "m3",
		"#mixed *:first-child":         "h1",

		// :empty counts whitespace as content, so only a truly empty element.
		"#empties p:empty": "e1",
	})
}

// TestMatchRoot pins that :root is the html element and not simply "the
// outermost thing". The document node is not an element and no selector
// matches it.
func TestMatchRoot(t *testing.T) {
	doc := parseDoc(t, "<p>x</p>")
	check(t, doc, map[string]string{
		":root":     "<html>",
		"html:root": "<html>",
		"body:root": "",
		":root > *": "<head> <body>",
	})
}

// TestMatchRootIsTheDocumentElement pins that :root means *that* element and not
// "whatever has no element parent".
//
// The two agree on every tree the html package builds, since it always produces
// exactly one <html> under the document node — which is why this needs a
// detached element to tell them apart, and why it is worth having: an element
// matched outside the document it belongs to would otherwise be styled as the
// root, taking every :root rule with it.
func TestMatchRootIsTheDocumentElement(t *testing.T) {
	doc := parseDoc(t, "<p>x</p>")
	vals, _ := css.ParseComponentValues(":root")
	sels, _, ok := css.ParseSelectorList(vals)
	if !ok {
		t.Fatal("\":root\" was refused")
	}
	m := NewMatcher(doc)

	if !m.Match(sels[0], doc.Element("html")) {
		t.Error("the html element is not :root, and it is the document element")
	}
	// An element with no parent at all, which is not this document's root.
	detached := &html.Node{Type: html.ElementNode, Name: "div"}
	if m.Match(sels[0], detached) {
		t.Error("a detached element matched :root, so every :root rule would style it")
	}
	// And an element from another document is not this one's root either.
	other := parseDoc(t, "<div>y</div>")
	if m.Match(sels[0], other.Element("html")) {
		t.Error("another document's root matched :root here")
	}
}

// TestMatchLogicalCombinations pins :is(), :where() and :not(). The first two
// are the same for matching and differ only in specificity, which is the css
// package's business; :not() is the one whose sense can be inverted without
// anything looking wrong until the page is read.
func TestMatchLogicalCombinations(t *testing.T) {
	doc := parseDoc(t, simpleDoc)
	check(t, doc, map[string]string{
		":is(span)":                  "s1",
		"p:is(.lead)":                "p1 p2 p4",
		"p:is(.lead, .special)":      "p1 p2 p4",
		"p:where(.lead)":             "p1 p2 p4",
		"p:not(.lead)":               "p3",
		"p:not(.lead):not(.special)": "p3",
		"#outer > p:not(.lead)":      "p3",
		// :not() with a list excludes everything in it.
		"p:not(.lead, .special)": "p3",
		// A combinator inside :is() still means what it says.
		":is(#inner p)": "p4",
	})
}

// TestMatchLinks pins the one pseudo-class that looks interactive and is not.
// Whether an <a> has an href is a fact about the document, so :link and
// :any-link are answerable; :visited is not, and the css package refuses it.
func TestMatchLinks(t *testing.T) {
	doc := parseDoc(t, simpleDoc)
	check(t, doc, map[string]string{
		"a:link":     "a1",
		"a:any-link": "a1",
		":link":      "a1",
	})
}

// TestMatchLang pins that :lang() reads the nearest declaration at or above the
// element, and compares as a language range — which is why it exists rather
// than authors writing [lang|=en].
func TestMatchLang(t *testing.T) {
	doc := parseDoc(t, `
<div id="outer" lang="en-GB">
  <p id="a">a</p>
  <p id="b" lang="fr">b</p>
  <div id="mid" lang="de-AT"><p id="c">c</p></div>
</div>
<p id="d">d</p>`)
	check(t, doc, map[string]string{
		"p:lang(en)":    "a",
		"p:lang(en-GB)": "a",
		"p:lang(fr)":    "b",
		"p:lang(de)":    "c",
		"p:lang(de-AT)": "c",
		// An element with no declaration anywhere above it has no language.
		"p:lang(es)": "",
		// The comparison is a range, so "en" takes "en-GB" but "en-US" does not.
		"p:lang(en-US)":  "",
		"p:lang(fr, de)": "b c",
	})
}

// TestMatchIsRightToLeft is a performance property rather than a correctness
// one, and it is asserted because the alternative is quietly quadratic.
// Matching from the subject outwards rejects most elements on their own name;
// searching from the left walks the tree for every rule.
func TestMatchIsRightToLeft(t *testing.T) {
	// A deep document where the leftmost compound matches nothing, so a
	// left-to-right matcher would search the whole tree and a right-to-left one
	// rejects on the subject immediately.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("<div>")
	}
	b.WriteString("<p id=\"deep\">x</p>")
	for i := 0; i < 200; i++ {
		b.WriteString("</div>")
	}
	doc := parseDoc(t, b.String())

	// The subject rejects at once: no <span> anywhere, so no ancestor walk runs.
	if got := selected(t, doc, "div span"); len(got) != 0 {
		t.Errorf("selected %v, want nothing", got)
	}
	// And a selector that does match still finds it, through 200 ancestors.
	if got := strings.Join(selected(t, doc, "div p"), " "); got != "deep" {
		t.Errorf("selected %q, want deep", got)
	}
}

// TestMatchBudgetTripsAndIsReported is the security property, and the reporting
// half of it matters as much as the bound. A matcher that silently answered "no
// match" when it ran out of budget would produce a page with styles missing and
// nothing at all to say so.
func TestMatchBudgetTripsAndIsReported(t *testing.T) {
	// A deep tree and a selector that must backtrack over every combination of
	// ancestors. Both halves are needed, and the second is easy to get wrong: a
	// selector whose every compound matches every ancestor is *linear*, because
	// the first ancestor tried always succeeds. The blowup needs a compound that
	// never matches, at the far left, so each of the combinations the ones to
	// its right can be satisfied by is tried and then abandoned.
	const depth = 200 // under the html package's own nesting cap
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString("<div class=\"x\">")
	}
	b.WriteString("<p id=\"deep\">x</p>")
	for i := 0; i < depth; i++ {
		b.WriteString("</div>")
	}
	doc := parseDoc(t, b.String())

	selector := ".nowhere " + strings.Repeat(".x ", 11) + "p"
	vals, _ := css.ParseComponentValues(selector)
	sels, _, ok := css.ParseSelectorList(vals)
	if !ok {
		t.Fatal("the selector was refused")
	}

	m := NewMatcher(doc)
	target := doc.Element("p")
	m.Match(sels[0], target)

	if !m.Tripped() {
		t.Error("a selector that backtracks over a 300-deep tree did not trip the budget")
	}
	// And it terminated, which is the other half.
}

// TestMatchNeverPanics is the totality property. Every document the html package
// can build, against every selector the css package can build, has to produce an
// answer.
func TestMatchNeverPanics(t *testing.T) {
	docs := []string{
		"", "<p>x</p>", "<div><p>x</p></div>", simpleDoc, structuralDoc,
		"<ul><li>a<li>b</ul>", "<table><tr><td>x</table>",
	}
	selectors := []string{
		"*", "p", "#a", ".c", "a b c d", "a>b+c~d", "[x]", "[x=y]",
		":root", ":empty", ":first-child", ":nth-child(2n+1)", ":not(p)",
		":is(a, b)", ":where(*)", ":lang(en)", ":link",
		"p:nth-child(2n of .c)", "*:not(:is(:where(p)))",
	}
	for _, src := range docs {
		doc, _, _ := html.Parse(src)
		m := NewMatcher(doc)
		for _, sel := range selectors {
			vals, _ := css.ParseComponentValues(sel)
			sels, _, ok := css.ParseSelectorList(vals)
			if !ok {
				continue
			}
			doc.Walk(func(n *html.Node) bool {
				for _, s := range sels {
					m.Match(s, n)
				}
				return true
			})
		}
	}
}

// TestMatchRejectsNonElements pins that nothing but an element is ever
// selected. A text node matching "*" would give every rule a phantom subject.
func TestMatchRejectsNonElements(t *testing.T) {
	doc := parseDoc(t, "<p>text</p>")
	vals, _ := css.ParseComponentValues("*")
	sels, _, _ := css.ParseSelectorList(vals)

	m := NewMatcher(doc)
	if m.Match(sels[0], doc) {
		t.Error("the document node was selected by \"*\"")
	}
	var text *html.Node
	doc.Walk(func(n *html.Node) bool {
		if n.Type == html.TextNode {
			text = n
		}
		return true
	})
	if text == nil {
		t.Fatal("the document has no text node to test with")
	}
	if m.Match(sels[0], text) {
		t.Error("a text node was selected by \"*\"")
	}
	if m.Match(sels[0], nil) {
		t.Error("nil was selected by \"*\"")
	}
}
