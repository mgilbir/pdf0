package style

import (
	"testing"

	"github.com/mgilbir/pdf0/html"
)

// Presentational hints, and above all where they sit in the cascade.
//
// The value of a hint is easy and its priority is not: a hint that beat an
// author's stylesheet would make a document impossible to restyle, and one that
// lost to the user-agent sheet would never apply at all. Both are silent
// failures — the page simply comes out at the wrong size — so each is asserted
// by having something else compete with it.

func computed(t *testing.T, markup string, sheets ...Sheet) map[string]ComputedStyle {
	t.Helper()
	doc, _, _ := html.Parse(markup)
	styled := Apply(doc, sheets)
	out := map[string]ComputedStyle{}
	doc.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		if id, ok := n.Attr("id"); ok {
			out[id] = styled.Styles[n]
		}
		return true
	})
	return out
}

func TestHintAppliesWithNothingElseSaying(t *testing.T) {
	got := computed(t, `<img id="i" width="5" height="96">`)
	if w := got["i"]["width"]; w != "5px" {
		t.Errorf("width is %q, want 5px", w)
	}
	if h := got["i"]["height"]; h != "96px" {
		t.Errorf("height is %q, want 96px", h)
	}
}

// TestHintBeatsTheUserAgentSheet: a hint that lost to the defaults would never
// apply, since the defaults set a value for every property.
func TestHintBeatsTheUserAgentSheet(t *testing.T) {
	got := computed(t, `<img id="i" width="5">`,
		sheet(t, OriginUserAgent, `img { width: 999px }`))
	if w := got["i"]["width"]; w != "5px" {
		t.Errorf("width is %q; a user-agent rule beat a presentational hint", w)
	}
}

// TestAuthorSheetBeatsHint is the rule that makes a stylesheet able to take
// control of markup it did not write.
func TestAuthorSheetBeatsHint(t *testing.T) {
	got := computed(t, `<img id="i" width="5">`,
		sheet(t, OriginAuthor, `img { width: 60px }`))
	if w := got["i"]["width"]; w != "60px" {
		t.Errorf("width is %q; a presentational hint beat an author rule", w)
	}
}

// TestTheWeakestAuthorRuleStillBeatsAHint pins the specificity: a hint has
// none, so even a universal selector — the weakest thing an author can write —
// wins.
func TestTheWeakestAuthorRuleStillBeatsAHint(t *testing.T) {
	got := computed(t, `<img id="i" width="5">`,
		sheet(t, OriginAuthor, `* { width: 7px }`))
	if w := got["i"]["width"]; w != "7px" {
		t.Errorf("width is %q; a hint beat a universal author rule", w)
	}
}

// TestInlineStyleBeatsHint, since a style attribute is above every author rule.
func TestInlineStyleBeatsHint(t *testing.T) {
	got := computed(t, `<img id="i" width="5" style="width: 11px">`)
	if w := got["i"]["width"]; w != "11px" {
		t.Errorf("width is %q; a hint beat a style attribute", w)
	}
}

// TestHintValueSyntax pins HTML's dimension-value grammar. Everything outside
// it is ignored rather than guessed at: a value this cannot read must not
// become a length it invented.
func TestHintValueSyntax(t *testing.T) {
	cases := map[string]string{
		"5":     "5px",
		"050":   "050px",
		"5%":    "5%",
		" 5 ":   "5px",
		"5px":   "auto",
		"-5":    "auto",
		"5.5":   "auto",
		"abc":   "auto",
		"":      "auto",
		"5 6":   "auto",
		"99999": "99999px",
		// Longer than the digit bound, which exists so that an untrusted
		// attribute cannot state a number nobody meant.
		"12345678901": "auto",
	}
	for value, want := range cases {
		got := computed(t, `<img id="i" width="`+value+`">`)
		if w := got["i"]["width"]; w != want {
			t.Errorf("width=%q gave %q, want %q", value, w, want)
		}
	}
}

// TestHintsApplyOnlyToTheElementsThatHaveThem keeps the table from leaking: a
// width attribute on a <div> is not a presentational hint in HTML, and treating
// it as one would silently size boxes from stray markup.
func TestHintsApplyOnlyToTheElementsThatHaveThem(t *testing.T) {
	got := computed(t, `<div id="d" width="5"><span id="s" height="9">x</span></div>`)
	if w := got["d"]["width"]; w != "auto" {
		t.Errorf("a <div>'s width attribute set width to %q", w)
	}
	if h := got["s"]["height"]; h != "auto" {
		t.Errorf("a <span>'s height attribute set height to %q", h)
	}
}

// TestTableWidthAttributeIsAHint pins the entry the table algorithm made
// meaningful.
//
// The HTML Standard's table rendering section maps <table width> to the width
// property as a dimension value, so a bare number is pixels and a trailing
// per-cent sign is a percentage. Without it the suite's dbaron float tests size
// their table from its content and lay the document out at half the width the
// reference does.
func TestTableWidthAttributeIsAHint(t *testing.T) {
	cases := map[string]string{
		"300":  "300px",
		"100%": "100%",
		// Not a dimension value, so not a hint. A length with a unit is HTML's
		// own refusal, and it must not become a length this guessed at.
		"300px": "auto",
		"-1":    "auto",
	}
	for value, want := range cases {
		got := computed(t, `<table id="t" width="`+value+`"><tr><td>x</td></tr></table>`)
		if w := got["t"]["width"]; w != want {
			t.Errorf("<table width=%q> gave width %q, want %q", value, w, want)
		}
	}
}

// TestTableHeightAttributeIsNotAHint records a deliberate absence.
//
// HTML maps no height attribute on <table>. Browsers honour one as a legacy and
// this engine does not, because a hint the specification does not describe is a
// number with nothing to check it against.
func TestTableHeightAttributeIsNotAHint(t *testing.T) {
	got := computed(t, `<table id="t" height="300"><tr><td>x</td></tr></table>`)
	if h := got["t"]["height"]; h != "auto" {
		t.Errorf("<table height=300> set height to %q, want auto", h)
	}
}
