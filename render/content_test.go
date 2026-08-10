package render

import (
	"strings"
	"testing"
)

// Generated content.
//
// These are the only boxes in the tree that no element generated and no
// anonymous-box rule required: they come from a declaration, which makes this
// the one place the stylesheet adds to the document rather than describing it.

// TestGeneratedContentBracketsTheChildren pins where the two boxes go. ::before
// and ::after surround the element's own content rather than replacing it, and
// an engine that appended both would put the marker after the text.
func TestGeneratedContentBracketsTheChildren(t *testing.T) {
	got := bodyBoxes(t, `<p>middle</p>`,
		`p::before { content: "[" } p::after { content: "]" }`)
	want := `p block
  p inline
    text "["
  text "middle"
  p inline
    text "]"
`
	if got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}
}

// TestNoContentGeneratesNothing pins that a pseudo-element with nothing to say
// produces no box at all, which is not the same as producing an empty one. An
// empty inline still contributes a line box, so a list of items each preceded by
// an invisible one would be spaced as though something were there.
func TestNoContentGeneratesNothing(t *testing.T) {
	for _, sheet := range []string{
		`p::before { content: none }`,
		`p::before { content: normal }`,
		`p::before { color: red }`, // styled, but says no content
	} {
		got := bodyBoxes(t, `<p>x</p>`, sheet)
		want := `p block
  text "x"
`
		if got != want {
			t.Errorf("%s\ngot:\n%swant:\n%s", sheet, got, want)
		}
	}

	// And a pseudo-element nothing mentions costs nothing either.
	got := bodyBoxes(t, `<p>x</p>`)
	if strings.Count(got, "p ") != 1 {
		t.Errorf("an unstyled element generated pseudo-element boxes:\n%s", got)
	}
}

// TestContentIsWhiteSpaceProcessed pins that a content string goes through CSS
// Text §4 exactly as document text does.
//
// This test used to assert the opposite — that the string was taken exactly as
// written, on the reasoning that the author had typed the characters they wanted
// and "content: '  '" was a way to indent a marker. That reasoning is wrong and
// the test was pinning the bug: generated content is put in an anonymous inline
// box, and nothing exempts that box from the white-space processing every other
// inline gets. The suite says so directly in
// css/CSS2/generated-content/content-white-space-004, which puts tabs and
// newlines in a content string and requires it to set identically to the same
// words written in the markup.
func TestContentIsWhiteSpaceProcessed(t *testing.T) {
	// Under the initial value a run of spaces is one space, a segment break is a
	// space, and the leading and trailing spaces survive Phase I — where they go
	// is the line breaker's question and not this one.
	got := bodyBoxes(t, `<p>x</p>`, `p::before { content: "  a  b  " }`)
	if !strings.Contains(got, `text " a b "`) {
		t.Errorf("the content string was not collapsed:\n%s", got)
	}
	got = bodyBoxes(t, `<p>x</p>`, `p::before { content: "a\A b" }`)
	if !strings.Contains(got, `text "a b"`) {
		t.Errorf("the segment break did not become a space:\n%s", got)
	}

	// And "white-space: pre" keeps every one of them, which is the half that
	// makes the rule a rule rather than an unconditional collapse.
	got = bodyBoxes(t, `<p>x</p>`, `p::before { content: "  a  b  "; white-space: pre }`)
	if !strings.Contains(got, `text "  a  b  "`) {
		t.Errorf("white-space: pre did not preserve the string:\n%s", got)
	}
	got = bodyBoxes(t, `<p>x</p>`, `p::before { content: "a\A b"; white-space: pre }`)
	if !strings.Contains(got, "text \"a\\nb\"") {
		t.Errorf("white-space: pre did not preserve the segment break:\n%s", got)
	}
}

// TestContentConcatenates pins that a value of several parts becomes one string,
// which is how attr() is used with punctuation around it.
func TestContentConcatenates(t *testing.T) {
	got := bodyBoxes(t, `<p>x</p>`, `p::before { content: "(" "a" ")" }`)
	if !strings.Contains(got, `text "(a)"`) {
		t.Errorf("the parts were not joined:\n%s", got)
	}
}

// TestContentAttr pins attr(), which is the one function this engine produces.
// A missing attribute contributes the empty string, which is what makes attr()
// safe to use for data that is sometimes there.
func TestContentAttr(t *testing.T) {
	got := bodyBoxes(t, `<p data-label="Note">x</p>`,
		`p::before { content: attr(data-label) ": " }`)
	if !strings.Contains(got, `text "Note: "`) {
		t.Errorf("attr() did not produce the attribute:\n%s", got)
	}

	// A missing attribute is the empty string, so only the literal remains.
	got = bodyBoxes(t, `<p>x</p>`, `p::before { content: attr(data-missing) ">" }`)
	if !strings.Contains(got, `text ">"`) {
		t.Errorf("a missing attribute did not contribute the empty string:\n%s", got)
	}
}

// TestUnproducibleContentIsReported pins that content this engine cannot make is
// named rather than dropped. A marker that silently failed to appear leaves a
// page that still looks finished.
func TestUnproducibleContentIsReported(t *testing.T) {
	cases := map[string]string{
		// counter(), counters() and the quote keywords are produced now and so
		// are not here; a counter function with no name is still unproducible,
		// and is the case that keeps this covering the counter path at all.
		`p::before { content: counter() }`:     "counter",
		`p::before { content: url(mark.png) }`: "image",
		// An identifier that is not one of the keywords the property defines.
		`p::before { content: elephant }`: "elephant",
	}
	for sheet, want := range cases {
		got := build(t, `<p>x</p>`, sheet)

		var found *Finding
		for i := range got.Findings {
			if got.Findings[i].Property == "content" {
				f := got.Findings[i]
				found = &f
			}
		}
		if found == nil {
			t.Errorf("%s was dropped silently: %v", sheet, got.Findings)
			continue
		}
		if found.Rule != RuleUnsupportedValue {
			t.Errorf("%s was reported as %s, want an unsupported value", sheet, found.Rule)
		}
		if !strings.Contains(strings.ToLower(found.Message), want) {
			t.Errorf("%s reported %q, which does not say it was a %s", sheet, found.Message, want)
		}
	}
}

// TestPseudoElementInheritsFromItsOwner pins the inheritance that makes
// "p { color: red } p::before { content: '>' }" draw a red marker without the
// author saying so twice. It inherits from the element it belongs to, not from
// that element's parent.
func TestPseudoElementInheritsFromItsOwner(t *testing.T) {
	built := build(t, `<div><p>x</p></div>`,
		`div { color: rgb(1, 1, 1) } p { color: rgb(2, 2, 2) } p::before { content: ">" }`)

	var before *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if before != nil {
			return
		}
		if b.Element != nil && b.Element.Name == "p" && len(b.Children) > 0 &&
			b.Children[0].IsText() && b.Children[0].Text == ">" {
			before = b
			return
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(built.Root)
	if before == nil {
		t.Fatal("the generated box was not found")
	}
	if got := before.Style["color"]; got != "rgb(2, 2, 2)" {
		t.Errorf("the marker's colour is %q; it inherits from the <p>, not the <div>", got)
	}
}

// TestPseudoElementRulesDoNotStyleTheElement pins the separation the other way.
// A rule with a pseudo-element styles that and nothing else, and one without
// never styles a pseudo-element — getting it backwards gives every ::before its
// element's whole style twice over, and gives the element the marker's.
func TestPseudoElementRulesDoNotStyleTheElement(t *testing.T) {
	built := build(t, `<p>x</p>`,
		`p::before { content: ">"; color: rgb(9, 9, 9) } p { color: rgb(1, 1, 1) }`)

	var p, before *Box
	var walk func(*Box)
	walk = func(b *Box) {
		if b.Element != nil && b.Element.Name == "p" {
			if len(b.Children) > 0 && b.Children[0].IsText() && b.Children[0].Text == ">" {
				before = b
			} else if p == nil {
				p = b
			}
		}
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(built.Root)
	if p == nil || before == nil {
		t.Fatalf("boxes not found: p=%v before=%v", p != nil, before != nil)
	}
	if p.Style["color"] != "rgb(1, 1, 1)" {
		t.Errorf("the element took the marker's colour: %q", p.Style["color"])
	}
	if before.Style["color"] != "rgb(9, 9, 9)" {
		t.Errorf("the marker did not take its own colour: %q", before.Style["color"])
	}
	// And the element has no content of its own from the pseudo-element's rule.
	if p.Style["content"] != "normal" {
		t.Errorf("the element's content is %q; the ::before rule set it", p.Style["content"])
	}
}

// TestGeneratedContentCanBeABlock pins that display applies to a pseudo-element
// like anything else, so a generated box can take a line of its own.
func TestGeneratedContentCanBeABlock(t *testing.T) {
	got := bodyBoxes(t, `<p>x</p>`,
		`p::before { content: "above"; display: block }`)
	if !strings.Contains(got, "p block\n  p block\n") {
		t.Errorf("a block ::before did not take a line of its own:\n%s", got)
	}

	// display:none on a pseudo-element produces nothing, as it does anywhere.
	got = bodyBoxes(t, `<p>x</p>`, `p::before { content: "gone"; display: none }`)
	if strings.Contains(got, "gone") {
		t.Errorf("display:none on a ::before still generated a box:\n%s", got)
	}
}
