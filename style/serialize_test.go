package style

import (
	"testing"

	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/html"
)

// Serialising a winning value back to text.
//
// The cascade keeps a winning declaration as text rather than as component
// values, so that a computed value is comparable and can be a map key. That
// makes the serialisation a *round trip*: every reader of a computed value
// tokenizes it again, and whatever the serialisation cannot express is lost
// between the two — quietly, because what comes back is still something an
// author could have written.
//
// These pin the round trip for the one token kind where it can be lost: a
// string. The others are either their own text (an identifier) or carry the
// author's own representation (a number keeps Repr).

// TestStringValuesSurviveTheRoundTrip pins that a string in a declaration comes
// back out of the cascade as the same string.
//
// The two characters here are the two that were wrong. A newline could not be
// written at all — a CSS string may not span lines, so the value came back as a
// bad-string and "content: 'a\Ab'" was reported as a value this engine cannot
// produce. A backslash was written unescaped, so "a\\b" — one backslash between
// two letters — came back as "a" + U+000B, which nothing reported at all.
func TestStringValuesSurviveTheRoundTrip(t *testing.T) {
	cases := map[string]string{
		// name: the CSS source of the value, then what the string must hold.
		`plain`:            `"abc"`,
		`quote`:            `"a\"b"`,
		`backslash`:        `"a\\b"`,
		`newline`:          `"a\A b"`,
		`tab`:              `"a\9 b"`,
		`escape-then-hex`:  `"a\\Ab"`,
		`trailing-newline`: `"a\A "`,
		`non-ascii`:        `"héllo — ✓"`,
	}
	for name, src := range cases {
		vals, errs := css.ParseComponentValues(src)
		if len(errs) != 0 || len(vals) != 1 || vals[0].Token.Kind != css.String {
			t.Fatalf("%s: %q did not parse as one string: %v", name, src, errs)
		}
		want := vals[0].Token.Value

		text := serialize(vals)

		back, errs := css.ParseComponentValues(text)
		if len(errs) != 0 {
			t.Errorf("%s: %q serialised to %q, which does not parse: %v", name, src, text, errs)
			continue
		}
		if len(back) != 1 || back[0].Token.Kind != css.String {
			t.Errorf("%s: %q serialised to %q, which parses as %d values of kind %v, not one string",
				name, src, text, len(back), back[0].Token.Kind)
			continue
		}
		if got := back[0].Token.Value; got != want {
			t.Errorf("%s: %q holds %q; through the cascade's text %q it came back as %q",
				name, src, want, text, got)
		}
	}
}

// TestStringValueThroughTheCascade is the same claim end to end, because the
// round trip above is only worth pinning if it is the one the cascade performs.
func TestStringValueThroughTheCascade(t *testing.T) {
	doc, _, _ := html.Parse(`<p>x</p>`)
	rules, _ := css.ParseStylesheet(`p { content: "one\Atwo\\three" }`)
	got := styleOf(t, doc, []Sheet{{Origin: OriginAuthor, Rules: rules}}, "p", "content")

	vals, errs := css.ParseComponentValues(got)
	if len(errs) != 0 || len(vals) != 1 || vals[0].Token.Kind != css.String {
		t.Fatalf("the computed value %q does not parse back as one string: %v", got, errs)
	}
	if want := "one\ntwo\\three"; vals[0].Token.Value != want {
		t.Errorf("the computed value %q holds %q, want %q", got, vals[0].Token.Value, want)
	}
}
